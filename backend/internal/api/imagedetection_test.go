package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"apiunit/internal/config"
	"apiunit/internal/engine"
	"apiunit/internal/model"
	"apiunit/internal/modules/imagedetection"
	"apiunit/internal/secret"
	"apiunit/internal/store"
)

// TestImageDetectionEndToEnd 走真实的 HTTP API：创建上游 → 启动生图检测 →
// 等待完成 → 读取报告 → 通过公开图片路由取回原始 PNG 字节。
func TestImageDetectionEndToEnd(t *testing.T) {
	pngBytes := testPNG(t)
	b64 := base64.StdEncoding.EncodeToString(pngBytes)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			http.Error(w, "bad auth", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v1/images/generations":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"created": 1,
				"data":    []map[string]string{{"b64_json": b64}},
			})
		case "/v1/images/edits":
			if err := r.ParseMultipartForm(16 << 20); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			file, _, err := r.FormFile("image")
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			defer file.Close()
			if _, err := io.Copy(io.Discard, file); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"created": 1,
				"data":    []map[string]string{{"b64_json": b64}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	dir := t.TempDir()
	box, err := secret.LoadOrCreate(filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatalf("secret: %v", err)
	}
	st, err := store.Open(filepath.Join(dir, "apiunit.db"), box)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer st.Close()

	target := &model.Target{Name: "mock-image", BaseURL: upstream.URL, Protocol: model.ProtocolOpenAI}
	if err := st.SaveTarget(target, "sk-test"); err != nil {
		t.Fatalf("save target: %v", err)
	}

	cfg := &config.Config{
		DataDir: dir, WebDir: dir, MaxConcurrentRuns: 2, MaxBodyBytes: 32 << 20,
	}
	manager := engine.NewManager(st, nil, nil, 2)
	manager.Register(imagedetection.New(st, imagedetection.ImageDir(dir)))
	ts := httptest.NewServer(New(cfg, st, manager, nil, nil, box).Router())
	defer ts.Close()

	runBody := map[string]any{
		"kind": "image_detection",
		"config": map[string]any{
			"targets": []map[string]any{{
				"targetId": target.ID,
				"models":   []map[string]string{{"name": "mock-image", "kind": "images", "size": "8x6"}},
			}},
			"concurrency": 1,
			"timeoutMs":   10000,
			"phases":      []string{"generation_transparent", "edit_images_edits"},
			"signature":   map[string]bool{"local": true, "online": false},
		},
	}
	body, _ := json.Marshal(runBody)
	resp, err := http.Post(ts.URL+"/api/runs", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("create run HTTP %d: %s", resp.StatusCode, raw)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil || created.ID == "" {
		t.Fatalf("decode run id: %v", err)
	}

	var view struct {
		Run     model.Run        `json:"run"`
		Summary model.RunSummary `json:"summary"`
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		r, err := http.Get(ts.URL + "/api/runs/" + created.ID)
		if err != nil {
			t.Fatalf("get run: %v", err)
		}
		err = json.NewDecoder(r.Body).Decode(&view)
		r.Body.Close()
		if err != nil {
			t.Fatalf("decode run: %v", err)
		}
		if view.Run.Status != model.StatusQueued && view.Run.Status != model.StatusRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run did not finish, last status %s", view.Run.Status)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if view.Run.Status != model.StatusCompleted {
		t.Fatalf("run status = %s, error = %s", view.Run.Status, view.Run.Error)
	}
	if view.Summary.Total != 2 || view.Summary.Success != 2 || view.Summary.Failed != 0 {
		t.Fatalf("summary = %+v", view.Summary)
	}

	raw, _ := json.Marshal(view.Summary.Data)
	var report imagedetection.Report
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if len(report.Models) != 1 || len(report.Models[0].Phases) != 2 {
		t.Fatalf("report shape = %+v", report.Models)
	}
	imagePath := ""
	for _, p := range report.Models[0].Phases {
		if p.Image != nil {
			imagePath = p.Image.Path
			if p.Image.Signature.C2PA.Status != imagedetection.SignatureNotDetected {
				t.Fatalf("普通 PNG 不应检测到 C2PA: %+v", p.Image.Signature.C2PA)
			}
		}
	}
	if imagePath == "" {
		t.Fatal("报告中没有图片路径")
	}

	imgResp, err := http.Get(ts.URL + imagePath)
	if err != nil {
		t.Fatalf("get image: %v", err)
	}
	defer imgResp.Body.Close()
	if imgResp.StatusCode != http.StatusOK {
		t.Fatalf("image HTTP %d", imgResp.StatusCode)
	}
	if got := imgResp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("图片路由缺少 CORS 头: %q", got)
	}
	gotBytes, _ := io.ReadAll(imgResp.Body)
	if !bytes.Equal(gotBytes, pngBytes) {
		t.Fatalf("图片字节不一致: got %d, want %d", len(gotBytes), len(pngBytes))
	}

	// 删除测试记录后图片目录应被清理。
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/runs/"+created.ID, nil)
	delResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete run: %v", err)
	}
	delResp.Body.Close()
	if delResp.StatusCode != http.StatusOK {
		t.Fatalf("delete run HTTP %d", delResp.StatusCode)
	}
	imgResp2, err := http.Get(ts.URL + imagePath)
	if err != nil {
		t.Fatalf("get deleted image: %v", err)
	}
	imgResp2.Body.Close()
	if imgResp2.StatusCode != http.StatusNotFound {
		t.Fatalf("删除记录后图片仍可访问: HTTP %d", imgResp2.StatusCode)
	}
}

func testPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 8, 6))
	for y := 0; y < 6; y++ {
		for x := 0; x < 8; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 10, G: 20, B: 30, A: 255})
		}
	}
	img.SetNRGBA(0, 0, color.NRGBA{})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// TestImageDetectionConfigProxyRoundTrip 验证代理与 TLS 1.2 开关能保存、脱敏并回读。
func TestImageDetectionConfigProxyRoundTrip(t *testing.T) {
	dir := t.TempDir()
	box, err := secret.LoadOrCreate(filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatalf("secret: %v", err)
	}
	st, err := store.Open(filepath.Join(dir, "apiunit.db"), box)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer st.Close()

	cfg := &config.Config{DataDir: dir, WebDir: dir, MaxConcurrentRuns: 1, MaxBodyBytes: 1 << 20}
	ts := httptest.NewServer(New(cfg, st, engine.NewManager(st, nil, nil, 1), nil, nil, box).Router())
	defer ts.Close()

	body := `{"baseUrl":"https://api.openai.com","proxyUrl":"http://user:secret@127.0.0.1:8080","forceTls12":true}`
	resp, err := http.Post(ts.URL+"/api/image-detection/config", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("post config: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("post config HTTP %d: %s", resp.StatusCode, raw)
	}
	var saved struct {
		Settings imagedetection.SettingsView `json:"settings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&saved); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if !saved.Settings.ProxyConfigured || !saved.Settings.ForceTLS12 {
		t.Fatalf("配置未生效: %+v", saved.Settings)
	}
	if strings.Contains(saved.Settings.ProxyMasked, "secret") || !strings.Contains(saved.Settings.ProxyMasked, "xxxx") {
		t.Fatalf("代理密码未脱敏: %q", saved.Settings.ProxyMasked)
	}

	// 再读一次，确认设置已持久化。
	getResp, err := http.Get(ts.URL + "/api/image-detection/config")
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	defer getResp.Body.Close()
	var again struct {
		Settings imagedetection.SettingsView `json:"settings"`
	}
	if err := json.NewDecoder(getResp.Body).Decode(&again); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if !again.Settings.ProxyConfigured || !again.Settings.ForceTLS12 {
		t.Fatalf("配置未持久化: %+v", again.Settings)
	}
}
