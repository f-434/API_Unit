package imagedetection

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"apiunit/internal/model"
)

func encodePNG(t *testing.T, transparent bool) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 8, 6))
	for y := 0; y < 6; y++ {
		for x := 0; x < 8; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 20, G: 40, B: 60, A: 255})
		}
	}
	if transparent {
		img.SetNRGBA(0, 0, color.NRGBA{A: 0})
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func TestNormalizeModelSize(t *testing.T) {
	cases := []struct {
		raw   string
		name  string
		want  string
		isErr bool
	}{
		{"", "gpt-image-1", "1024x1024", false},
		{"auto", "seedream-4k", "4096x4096", false},
		{"2k", "whatever", "2048x2048", false},
		{"1024x1024", "whatever", "1024x1024", false},
		{"100", "whatever", "", true},
	}
	for _, c := range cases {
		got, err := normalizeModelSize(c.raw, c.name)
		if c.isErr {
			if err == nil {
				t.Fatalf("normalizeModelSize(%q) 期望报错，实际 %q", c.raw, got)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Fatalf("normalizeModelSize(%q) = %q, %v; want %q", c.raw, got, err, c.want)
		}
	}
}

func TestPhasePlan(t *testing.T) {
	all := phasePlan(ModelSpec{Kind: ModelKindImages}, nil)
	wantAll := []string{PhaseGenTransparent, PhaseGenOpaque, PhaseGenAuto, PhaseEditImages, PhaseEditAlias}
	if strings.Join(all, ",") != strings.Join(wantAll, ",") {
		t.Fatalf("images 全量计划 = %v; want %v", all, wantAll)
	}
	onlyEdits := phasePlan(ModelSpec{Kind: ModelKindImages}, map[string]bool{PhaseEditImages: true})
	wantEdits := []string{PhaseEditSource, PhaseEditImages}
	if strings.Join(onlyEdits, ",") != strings.Join(wantEdits, ",") {
		t.Fatalf("仅改图计划 = %v; want %v", onlyEdits, wantEdits)
	}
	chat := phasePlan(ModelSpec{Kind: ModelKindChat}, nil)
	if len(chat) != 1 || chat[0] != PhaseChatGeneration {
		t.Fatalf("chat 计划 = %v", chat)
	}
	if got := phasePlan(ModelSpec{Kind: ModelKindImages}, map[string]bool{PhaseChatGeneration: true}); len(got) != 0 {
		t.Fatalf("图片模型不应执行 chat 测试项，实际 %v", got)
	}
}

func TestCheckImageTransparency(t *testing.T) {
	transparent := encodePNG(t, true)
	format, w, h, checks := checkImage(transparent, "8x6", "png", "transparent")
	if format != "png" || w != 8 || h != 6 {
		t.Fatalf("checkImage 格式/尺寸 = %s %dx%d", format, w, h)
	}
	if checksStatus(checks) != StatusPass {
		t.Fatalf("透明 PNG 应通过 transparent 检查: %+v", checks)
	}
	_, _, _, checks = checkImage(transparent, "8x6", "png", "opaque")
	if checksStatus(checks) != StatusFail {
		t.Fatalf("透明 PNG 不应通过 opaque 检查: %+v", checks)
	}

	opaque := encodePNG(t, false)
	_, _, _, checks = checkImage(opaque, "8x6", "png", "transparent")
	if checksStatus(checks) != StatusFail {
		t.Fatalf("不透明 PNG 不应通过 transparent 检查: %+v", checks)
	}
	_, _, _, checks = checkImage(opaque, "8x6", "png", "opaque")
	if checksStatus(checks) != StatusPass {
		t.Fatalf("不透明 PNG 应通过 opaque 检查: %+v", checks)
	}
}

func TestJPEGSizeAndOpaque(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 13, 7))
	for y := 0; y < 7; y++ {
		for x := 0; x < 13; x++ {
			img.Set(x, y, color.RGBA{R: 200, G: 100, B: 50, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	raw := buf.Bytes()
	format, _ := sniffImage(raw)
	if format != "jpeg" {
		t.Fatalf("格式 = %s; want jpeg", format)
	}
	w, h, ok := imageSize(raw, format)
	if !ok || w != 13 || h != 7 {
		t.Fatalf("JPEG 尺寸 = %dx%d %v", w, h, ok)
	}
	_, _, _, checks := checkImage(raw, "13x7", "jpeg", "opaque")
	if checksStatus(checks) != StatusPass {
		t.Fatalf("JPEG opaque 检查应通过: %+v", checks)
	}
}

func TestLocalC2PAAndSynthID(t *testing.T) {
	plain := encodePNG(t, false)
	local := analyzeLocalC2PA(plain)
	if local.Status != SignatureNotDetected {
		t.Fatalf("普通 PNG C2PA 状态 = %s; want not_detected", local.Status)
	}
	withManifest := append(append([]byte{}, plain...), []byte("JUMBF ... c2pa ... SynthID ...")...)
	local = analyzeLocalC2PA(withManifest)
	if local.Status != SignatureDetected {
		t.Fatalf("带 JUMBF 的图片 C2PA 状态 = %s; want detected", local.Status)
	}
	synth := analyzeLocalSynthID(withManifest)
	if synth.Status != SignaturePossible {
		t.Fatalf("SynthID 标记状态 = %s; want possible", synth.Status)
	}
	if got := analyzeLocalSynthID(plain).Status; got != SignatureUnknown {
		t.Fatalf("普通图片 SynthID 状态 = %s; want unknown", got)
	}
}

func TestParseProvenanceResponse(t *testing.T) {
	body := []byte(`{
		"object": "content_provenance_check",
		"created_at": 1778000000,
		"results": [
			{"type": "c2pa", "outcome": "detected", "validation_state": "trusted",
			 "issuer": "OpenAI OpCo, LLC", "model": "gpt-image", "generated_at": "2026-07-27T18:34:12Z"},
			{"type": "synthid", "outcome": "not_detected", "model": "", "generated_at": null}
		]
	}`)
	got, err := parseProvenanceResponse(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.Status != OnlineChecked || got.CreatedAt != 1778000000 {
		t.Fatalf("在线结果状态/时间 = %s/%d", got.Status, got.CreatedAt)
	}
	if got.C2PA.Outcome != "detected" || got.C2PA.ValidationState != "trusted" || got.C2PA.Issuer == "" {
		t.Fatalf("C2PA 解析结果错误: %+v", got.C2PA)
	}
	if got.SynthID.Outcome != "not_detected" {
		t.Fatalf("SynthID 解析结果错误: %+v", got.SynthID)
	}
}

func TestSanitizeResponse(t *testing.T) {
	raw := []byte(`{"data":[{"b64_json":"` + strings.Repeat("A", 1024) + `"}]}`)
	out := sanitizeResponse(raw)
	if strings.Contains(out, strings.Repeat("A", 100)) {
		t.Fatal("sanitizeResponse 未移除 base64 内容")
	}
	if !strings.Contains(out, "base64 omitted") {
		t.Fatalf("sanitizeResponse 输出缺少标记: %s", out)
	}
}

func TestValidImageFile(t *testing.T) {
	valid := []string{"abcdef12_generation_transparent.png", "0123456789ab_edit_images_edits.jpg"}
	invalid := []string{"../x.png", "x.png", "abcdef12_../x.png", "abcdef12_x.txt"}
	for _, name := range valid {
		if !ValidImageFile(name) {
			t.Fatalf("%q 应为合法文件名", name)
		}
	}
	for _, name := range invalid {
		if ValidImageFile(name) {
			t.Fatalf("%q 应为非法文件名", name)
		}
	}
	if !ValidRunID("r_1m2n3o4o5a1b2c3d4") {
		t.Fatal("合法 runID 校验失败")
	}
	if ValidRunID("../etc") || ValidRunID("r_../x") {
		t.Fatal("非法 runID 未被拒绝")
	}
}

func TestDoGenerationAndEdit(t *testing.T) {
	pngBytes := encodePNG(t, true)
	b64 := base64.StdEncoding.EncodeToString(pngBytes)
	genSeen := false
	editSeen := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/images/generations":
			genSeen = true
			_ = json.NewEncoder(w).Encode(map[string]any{
				"created": 1,
				"data":    []map[string]string{{"b64_json": b64}},
			})
		case "/v1/images/edits":
			editSeen = true
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
			_ = json.NewEncoder(w).Encode(map[string]any{
				"created": 1,
				"data":    []map[string]string{{"b64_json": b64}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cfg := &Config{
		TimeoutMs:        10000,
		OutputFormat:     "png",
		GenerationPrompt: "cat",
		EditPrompt:       "edit",
		Signature:        SignatureConfig{Local: true},
	}
	r := &runner{
		cfg:       cfg,
		settings:  defaultSettings(),
		runID:     "r_test1234567890",
		imageRoot: t.TempDir(),
		http:      srv.Client(),
	}
	j := job{
		target: model.TargetSecret{
			Target: model.Target{ID: "t_mock", Name: "mock", BaseURL: srv.URL},
			APIKey: "sk-test",
		},
		model: ModelSpec{Name: "mock-image", Kind: ModelKindImages, Size: "8x6"},
	}

	pr, img := r.doGeneration(context.Background(), j, PhaseGenTransparent, "transparent")
	if pr.Status != StatusPass {
		t.Fatalf("文生图状态 = %s, reason=%s, checks=%+v", pr.Status, pr.Reason, pr.Checks)
	}
	if !genSeen {
		t.Fatal("未请求 /v1/images/generations")
	}
	if !bytes.Equal(img, pngBytes) {
		t.Fatal("返回的图片字节被修改")
	}
	if pr.Image == nil || pr.Image.Width != 8 || pr.Image.Height != 6 || pr.Image.Format != "png" {
		t.Fatalf("图片元信息错误: %+v", pr.Image)
	}
	if pr.Image.Signature.Online == nil || pr.Image.Signature.Online.Status != OnlineSkipped {
		t.Fatalf("未启用在线验签时应标记 skipped: %+v", pr.Image.Signature.Online)
	}
	if len(pr.Image.Signature.Links) != 3 {
		t.Fatalf("在线校验入口数量 = %d", len(pr.Image.Signature.Links))
	}
	fileName := filepath.Base(pr.Image.Path)
	if _, err := os.Stat(filepath.Join(r.imageRoot, r.runID, fileName)); err != nil {
		t.Fatalf("图片未落盘: %v", err)
	}

	state := &modelState{sourceBytes: pngBytes, sourceFormat: "png", sourceBackground: "transparent"}
	edit := r.doEdit(context.Background(), j, PhaseEditImages, state)
	if edit.Status != StatusPass {
		t.Fatalf("改图状态 = %s, reason=%s, checks=%+v", edit.Status, edit.Reason, edit.Checks)
	}
	if !editSeen {
		t.Fatal("未请求 /v1/images/edits")
	}
}

func TestSignatureReportLinks(t *testing.T) {
	links := buildVerifyLinks("https://example.com/a.png")
	if len(links) != 3 {
		t.Fatalf("在线校验入口数量 = %d; want 3", len(links))
	}
	if !strings.Contains(links[0].AutoURL, "inspect.cr?source=") {
		t.Fatalf("Adobe 自动链接缺失: %+v", links[0])
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(`{"results":[]}`), &raw); err != nil {
		t.Fatal(err)
	}
}
