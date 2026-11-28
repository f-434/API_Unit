package claudesignature

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"apiunit/internal/engine"
	"apiunit/internal/model"
)

func newTestSession(t *testing.T) *engine.Session {
	t.Helper()
	// 这些测试不落库、不调用分词器，因此 store/client/counter 可以为空。
	return engine.NewSession("r_test", model.KindClaudeSignature, nil, nil, nil, nil)
}

func TestRunModelFallsBackFromAdaptive(t *testing.T) {
	var calls int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&calls, 1)
		var body struct {
			Thinking map[string]any `json:"thinking"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Thinking["type"] == "adaptive" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"thinking.adaptive is not supported"}}`))
			return
		}
		if body.Thinking["type"] != "enabled" {
			http.Error(w, "expected budget thinking", http.StatusBadRequest)
			return
		}
		w.Header().Set("X-Oneapi-Request-Id", fmt.Sprintf("req-%d", n))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          "msg_1",
			"model":       "claude-opus-5-5",
			"content":     []map[string]any{{"type": "thinking", "thinking": "x", "signature": testSignature(t, 10)}},
			"stop_reason": "end_turn",
			"usage":       map[string]any{"input_tokens": 1, "output_tokens": 1},
		})
	}))
	defer server.Close()

	cfg := &Config{
		Count: 1, Concurrency: 1, TimeoutMs: 5000, MaxTokens: 4096,
		Thinking: "auto", BudgetTokens: 2048,
		Retry: RetryConfig{MaxRetries: 0},
	}
	job := modelJob{
		target: model.TargetSecret{Target: model.Target{ID: "t1", Name: "mock", BaseURL: server.URL, Protocol: model.ProtocolClaude}, APIKey: "sk-test"},
		model:  "claude-opus-5-5", protocol: model.ProtocolClaude,
		endpoint: server.URL + "/v1/messages", authMode: "api-key",
		plans: buildPlans(cfg, "claude-opus-5-5"),
	}
	sess := newTestSession(t)
	rep, requests := runModel(context.Background(), sess, cfg, job)
	if rep.Passed != 0 { // runModel 只回填基础信息，结论在 finalizeReport 里汇总。
		t.Fatalf("unexpected base report: %+v", rep)
	}
	if len(requests) != 1 {
		t.Fatalf("requests = %d", len(requests))
	}
	r := requests[0]
	if r.Verdict != VerdictPass {
		t.Fatalf("verdict=%s checks=%+v", r.Verdict, r.Checks)
	}
	if r.Attempts != 2 {
		t.Fatalf("attempts = %d", r.Attempts)
	}
	if r.RequestID != "req-2" {
		t.Fatalf("request id = %q", r.RequestID)
	}
	if calls != 2 {
		t.Fatalf("server calls = %d", calls)
	}
}

func TestRunModelStopsOnAuthFailure(t *testing.T) {
	var calls int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls, 1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer server.Close()

	cfg := &Config{
		Count: 3, TimeoutMs: 5000, MaxTokens: 4096,
		Thinking: "off", BudgetTokens: 1024,
	}
	job := modelJob{
		target: model.TargetSecret{Target: model.Target{ID: "t2", Name: "mock", BaseURL: server.URL, Protocol: model.ProtocolClaude}, APIKey: "bad"},
		model:  "claude-opus-5-5", protocol: model.ProtocolClaude,
		endpoint: server.URL + "/v1/messages", authMode: "api-key",
		plans: buildPlans(cfg, "claude-opus-5-5"),
	}
	sess := newTestSession(t)
	rep, requests := runModel(context.Background(), sess, cfg, job)
	if len(requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(requests))
	}
	if rep.Error == "" {
		t.Fatal("expected fatal model error")
	}
	if requests[0].Verdict != VerdictError || requests[0].ErrorType != "auth" {
		t.Fatalf("request = %+v", requests[0])
	}
	if calls != 1 {
		t.Fatalf("server calls = %d, want 1", calls)
	}
}

func TestDuplicateSignaturesMarked(t *testing.T) {
	report := &Report{
		Models: []ModelReport{{TargetID: "t", TargetName: "mock", Model: "m"}},
		Requests: []RequestReport{
			{Index: 1, GlobalIndex: 1, TargetID: "t", Model: "m", Verdict: VerdictPass,
				Signatures: []SignatureInfo{{SignatureSHA256: "abc", Layout: LayoutFull}}},
			{Index: 2, GlobalIndex: 2, TargetID: "t", Model: "m", Verdict: VerdictPass,
				Signatures: []SignatureInfo{{SignatureSHA256: "abc", Layout: LayoutFull}}},
		},
		Summary: Summary{Planned: 2},
	}
	finalizeReport(report)
	if report.Requests[0].Verdict != VerdictSuspicious || report.Requests[1].Verdict != VerdictSuspicious {
		t.Fatalf("verdicts = %s / %s", report.Requests[0].Verdict, report.Requests[1].Verdict)
	}
	if report.Summary.Duplicates != 2 || report.Summary.Suspicious != 2 {
		t.Fatalf("summary = %+v", report.Summary)
	}
}

func TestTimestampPlausibility(t *testing.T) {
	now := time.Now().UnixMilli()
	fresh := &SignatureInfo{UnixTimestamp: time.Now().Unix(), TimestampSource: "field"}
	status, _ := compareTimestamps(now, []*SignatureInfo{fresh})
	if status != CheckPass {
		t.Fatalf("fresh status = %s", status)
	}
	old := &SignatureInfo{UnixTimestamp: time.Now().Add(-48 * time.Hour).Unix(), TimestampSource: "field"}
	status, _ = compareTimestamps(now, []*SignatureInfo{old})
	if status != CheckFail {
		t.Fatalf("old status = %s", status)
	}
	oldSearch := &SignatureInfo{UnixTimestamp: time.Now().Add(-48 * time.Hour).Unix(), TimestampSource: "search"}
	status, _ = compareTimestamps(now, []*SignatureInfo{oldSearch})
	if status != CheckWarn {
		t.Fatalf("search status = %s", status)
	}
}
