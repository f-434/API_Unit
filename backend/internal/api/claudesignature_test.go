package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"apiunit/internal/config"
	"apiunit/internal/engine"
	"apiunit/internal/model"
	"apiunit/internal/modules/claudesignature"
	"apiunit/internal/secret"
	"apiunit/internal/store"
)

func sigPBVarint(field int, v uint64) []byte {
	out := sigPBUvarint(uint64(field<<3 | 0))
	return append(out, sigPBUvarint(v)...)
}

func sigPBBytes(field int, data []byte) []byte {
	out := sigPBUvarint(uint64(field<<3 | 2))
	out = append(out, sigPBUvarint(uint64(len(data)))...)
	return append(out, data...)
}

func sigPBUvarint(v uint64) []byte {
	var out []byte
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			b |= 0x80
		}
		out = append(out, b)
		if v == 0 {
			return out
		}
	}
}

func sigTestSignature(n uint64) string {
	ts := time.Now().Add(-time.Second).Unix()
	var msg []byte
	msg = append(msg, sigPBBytes(8, []byte("thinking"))...)
	msg = append(msg, sigPBBytes(6, []byte("claude-opus-5-5"))...)
	msg = append(msg, sigPBBytes(11, []byte("123e4567-e89b-12d3-a456-426614174000"))...)
	msg = append(msg, sigPBVarint(21, uint64(ts))...)
	msg = append(msg, sigPBVarint(100, n)...)
	return base64.StdEncoding.EncodeToString(msg)
}

// TestClaudeSignatureEndToEnd 走真实 HTTP API：启动签名核验 → 等待完成 →
// 校验 oneapi-request-id、protobuf 解析结果与报告汇总。
func TestClaudeSignatureEndToEnd(t *testing.T) {
	var calls int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&calls, 1)
		if got := r.Header.Get("x-api-key"); got != "sk-test" {
			http.Error(w, "bad auth", http.StatusUnauthorized)
			return
		}
		var body struct {
			Model    string         `json:"model"`
			Thinking map[string]any `json:"thinking"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.Model != "claude-opus-5-5" {
			http.Error(w, "unexpected model", http.StatusBadRequest)
			return
		}
		if body.Thinking["type"] != "adaptive" {
			http.Error(w, "expected adaptive thinking", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Oneapi-Request-Id", fmt.Sprintf("req-%d", n))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":    fmt.Sprintf("msg_%d", n),
			"type":  "message",
			"role":  "assistant",
			"model": "claude-opus-5-5",
			"content": []map[string]any{
				{"type": "thinking", "thinking": "计算过程", "signature": sigTestSignature(uint64(n))},
				{"type": "text", "text": "答案是 42"},
			},
			"stop_reason": "end_turn",
			"usage": map[string]any{
				"input_tokens":  12,
				"output_tokens": 34,
			},
		})
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

	target := &model.Target{Name: "mock-claude", BaseURL: upstream.URL, Protocol: model.ProtocolClaude}
	if err := st.SaveTarget(target, "sk-test"); err != nil {
		t.Fatalf("save target: %v", err)
	}

	cfg := &config.Config{
		DataDir: dir, WebDir: dir, MaxConcurrentRuns: 2, MaxBodyBytes: 32 << 20,
	}
	manager := engine.NewManager(st, nil, nil, 2)
	manager.Register(claudesignature.New(st))
	ts := httptest.NewServer(New(cfg, st, manager, nil, nil, box).Router())
	defer ts.Close()

	runBody := map[string]any{
		"kind": "claude_signature",
		"config": map[string]any{
			"targets": []map[string]any{{
				"targetId": target.ID,
				"models":   []string{"claude-opus-5-5"},
				"authMode": "api-key",
			}},
			"count":        2,
			"concurrency":  1,
			"delayMs":      0,
			"timeoutMs":    10000,
			"maxTokens":    2048,
			"thinking":     "auto",
			"budgetTokens": 1024,
			"retry": map[string]any{
				"maxRetries": 0,
			},
		},
	}
	body, _ := json.Marshal(runBody)
	resp, err := http.Post(ts.URL+"/api/runs", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create run HTTP %d", resp.StatusCode)
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

	raw, err := json.Marshal(view.Summary.Data)
	if err != nil {
		t.Fatalf("marshal data: %v", err)
	}
	var report claudesignature.Report
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if len(report.Requests) != 2 {
		t.Fatalf("requests = %d", len(report.Requests))
	}
	if report.Summary.Parsed != 2 || report.Summary.WithRequestID != 2 {
		t.Fatalf("summary = %+v", report.Summary)
	}
	for i, req := range report.Requests {
		if req.Verdict != claudesignature.VerdictPass {
			t.Fatalf("request %d verdict = %s checks = %+v", i, req.Verdict, req.Checks)
		}
		if req.RequestID == "" {
			t.Fatalf("request %d missing oneapi-request-id", i)
		}
		if len(req.Signatures) != 1 {
			t.Fatalf("request %d signatures = %+v", i, req.Signatures)
		}
		sig := req.Signatures[0]
		if sig.Layout != claudesignature.LayoutFull || sig.Model != "claude-opus-5-5" {
			t.Fatalf("request %d signature = %+v", i, sig)
		}
		if sig.SignedAtUTC8 == "" || sig.SignatureSHA256 == "" {
			t.Fatalf("request %d missing parsed fields: %+v", i, sig)
		}
	}

	itemResp, err := http.Get(ts.URL + "/api/runs/" + created.ID + "/items?limit=10")
	if err != nil {
		t.Fatalf("items: %v", err)
	}
	defer itemResp.Body.Close()
	var items struct {
		Items []model.Item `json:"items"`
		Total int          `json:"total"`
	}
	if err := json.NewDecoder(itemResp.Body).Decode(&items); err != nil {
		t.Fatalf("decode items: %v", err)
	}
	if items.Total != 2 || len(items.Items) != 2 {
		t.Fatalf("items total=%d len=%d", items.Total, len(items.Items))
	}
	for _, it := range items.Items {
		if it.RequestID == "" {
			t.Fatalf("item missing request id: %+v", it)
		}
		if it.Verdict != claudesignature.VerdictPass {
			t.Fatalf("item verdict = %s", it.Verdict)
		}
	}
}
