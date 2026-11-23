package authenticity

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"apiunit/internal/model"
	"apiunit/internal/store"
)

func srcSample(header http.Header, body, caseID string) sourceSample {
	return sourceSample{Header: header, Body: []byte(body), CaseID: caseID}
}

func TestSourceAPIStyle(t *testing.T) {
	tests := []struct {
		name     string
		proto    model.Protocol
		endpoint string
		style    string
		want     string
	}{
		{"default chat", model.ProtocolOpenAI, "https://relay.example.com", "", "chat"},
		{"explicit responses", model.ProtocolOpenAI, "https://relay.example.com", "responses", "responses"},
		{"auto official", model.ProtocolOpenAI, "https://api.openai.com/v1/chat/completions", "auto", "responses"},
		{"auto azure", model.ProtocolOpenAI, "https://res.openai.azure.com/openai/deployments/x/chat/completions", "auto", "responses"},
		{"auto custom", model.ProtocolOpenAI, "https://relay.example.com", "auto", "chat"},
		{"non-openai ignores responses", model.ProtocolClaude, "https://api.anthropic.com/v1/messages", "responses", "chat"},
	}
	for _, tc := range tests {
		if got := sourceAPIStyle(tc.proto, tc.endpoint, tc.style); got != tc.want {
			t.Fatalf("%s: sourceAPIStyle = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestSourceProbeResponses 模拟 Chat Completions 正常、Responses 返回
// Azure 内容过滤字段：来源应只采用 Responses 探针，并保存可回溯的探针条目。
func TestSourceProbeResponses(t *testing.T) {
	var sawResponses bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/chat/completions":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "chatcmpl-x", "object": "chat.completion", "model": "gpt-6-luna",
				"choices": []any{map[string]any{
					"message":       map[string]any{"role": "assistant", "content": "pong"},
					"finish_reason": "stop",
				}},
				"usage": map[string]any{"prompt_tokens": 5, "completion_tokens": 1, "total_tokens": 6},
			})
		case "/v1/responses":
			sawResponses = true
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "resp_probe", "object": "response", "model": "gpt-6-luna", "status": "completed",
				"content_filters": []any{map[string]any{"blocked": false}},
				"usage":           map[string]any{"input_tokens": 3, "output_tokens": 1, "total_tokens": 4},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	st, targetID := testStore(t)
	if err := st.SaveTarget(&model.Target{ID: targetID, Name: "relay", BaseURL: srv.URL, Protocol: model.ProtocolOpenAI}, "sk-test"); err != nil {
		t.Fatal(err)
	}
	report, runID := runModuleWithID(t, st, targetID, Config{
		Targets: []TargetSpec{{
			TargetID: targetID, Models: []string{"gpt-6-luna"},
			Protocol: "openai", APIStyle: "responses",
		}},
		Checks: ChecksConfig{Source: true},
	})
	if !sawResponses {
		t.Fatal("expected Responses source probe")
	}
	r := report.Models[0]
	if r.Source.Name != "Microsoft Azure" || r.Source.Confidence != "medium" {
		t.Fatalf("source=%+v, want Azure from Responses content_filters", r.Source)
	}
	var probeCheck *CheckReport
	for i := range r.Checks {
		if r.Checks[i].ID == "P2-0" {
			probeCheck = &r.Checks[i]
		}
	}
	if probeCheck == nil || probeCheck.Status != StatusPass || probeCheck.CaseID != "source.responses" {
		t.Fatalf("P2-0=%+v, want pass with source.responses", probeCheck)
	}
	items, _, err := st.ListItems(store.ItemFilter{RunID: runID, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var probeItem *model.Item
	for i := range items {
		if items[i].CaseID == "source.responses" {
			probeItem = &items[i]
			break
		}
	}
	if probeItem == nil {
		t.Fatal("missing source.responses item")
	}
	if probeItem.Meta["sourceProbe"] != true || probeItem.Meta["apiStyle"] != "responses" {
		t.Fatalf("probe meta=%+v, want sourceProbe/apiStyle", probeItem.Meta)
	}
}

func TestDetectSourceGeminiAIStudioFallback(t *testing.T) {
	h := http.Header{}
	h.Set("X-Oneapi-Request-Id", "gw-1")
	body := `{"candidates":[{"content":{"parts":[{"text":"pong"}]}}],"usageMetadata":{"serviceTier":"default"},"modelVersion":"gemini-4"}`
	src, caseID := detectSource(model.ProtocolGemini, []sourceSample{srcSample(h, body, "cap.turn2")}, "https://relay.example.com")
	if !strings.Contains(src.Name, "Google AI Studio") {
		t.Fatalf("name=%q, want AI Studio", src.Name)
	}
	if src.Confidence != "low" {
		t.Fatalf("confidence=%q, want low", src.Confidence)
	}
	if !strings.Contains(src.Gateway, "X-Oneapi-Request-Id") {
		t.Fatalf("gateway=%q, want oneapi marker", src.Gateway)
	}
	if caseID != "cap.turn2" {
		t.Fatalf("caseID=%q, want cap.turn2", caseID)
	}
}

func TestDetectSourceGeminiVertex(t *testing.T) {
	body := `{"candidates":[{"content":{"parts":[{"text":"pong"}]}}],"usageMetadata":{"trafficType":"ON_DEMAND"}}`
	src, caseID := detectSource(model.ProtocolGemini, []sourceSample{srcSample(nil, body, "ping")}, "https://relay.example.com")
	if src.Name != "Google Vertex AI" {
		t.Fatalf("name=%q, want Vertex", src.Name)
	}
	if src.Confidence != "medium" {
		t.Fatalf("confidence=%q, want medium", src.Confidence)
	}
	if caseID != "ping" {
		t.Fatalf("caseID=%q, want ping", caseID)
	}
}

func TestDetectSourceOpenAIFallback(t *testing.T) {
	h := http.Header{}
	h.Set("X-Oneapi-Request-Id", "gw-1")
	body := `{"id":"chatcmpl-x","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"pong"}}]}`
	src, _ := detectSource(model.ProtocolOpenAI, []sourceSample{srcSample(h, body, "ping")}, "https://relay.example.com")
	if !strings.Contains(src.Name, "OpenAI 官方") {
		t.Fatalf("name=%q, want OpenAI official fallback", src.Name)
	}
	if src.Confidence != "low" {
		t.Fatalf("confidence=%q, want low", src.Confidence)
	}
	if !strings.Contains(src.Gateway, "X-Oneapi-Request-Id") {
		t.Fatalf("gateway=%q, want oneapi marker", src.Gateway)
	}
}

func TestDetectSourceOpenAIAzureBody(t *testing.T) {
	body := `{"id":"chatcmpl-x","object":"chat.completion","prompt_filter_results":[{"prompt_index":0}],"choices":[{"message":{"role":"assistant","content":"pong"},"content_filter_results":{"hate":{"filtered":false}}}]}`
	src, caseID := detectSource(model.ProtocolOpenAI, []sourceSample{srcSample(nil, body, "ping")}, "https://relay.example.com")
	if src.Name != "Microsoft Azure" {
		t.Fatalf("name=%q, want Azure", src.Name)
	}
	if src.Confidence != "medium" {
		t.Fatalf("confidence=%q, want medium", src.Confidence)
	}
	if caseID != "ping" {
		t.Fatalf("caseID=%q, want ping", caseID)
	}
}

// 官方 Responses API 也可能带 content_filters/quota=null，不能据此误判 Azure。
func TestDetectSourceOpenAINullMarkersNotAzure(t *testing.T) {
	body := `{"id":"chatcmpl-x","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"pong"}}],"content_filters":null,"quota":null}`
	src, _ := detectSource(model.ProtocolOpenAI, []sourceSample{srcSample(nil, body, "ping")}, "https://relay.example.com")
	if src.Name == "Microsoft Azure" {
		t.Fatalf("name=%q, null markers must not mean Azure", src.Name)
	}
	if !strings.Contains(src.Name, "OpenAI 官方") || src.Confidence != "low" {
		t.Fatalf("source=%+v, want OpenAI low-confidence fallback", src)
	}
}

func TestDetectSourceOpenAIAzureHeader(t *testing.T) {
	h := http.Header{}
	h.Set("X-Ms-Request-Id", "ms-1")
	src, _ := detectSource(model.ProtocolOpenAI, []sourceSample{srcSample(h, "{}", "ping")}, "https://example.com")
	if src.Name != "Microsoft Azure" || src.Confidence != "high" {
		t.Fatalf("source=%+v, want Azure high", src)
	}
}

func TestDetectSourceClaudeHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("X-Amzn-Bedrock-Request-Id", "bedrock-1")
	src, _ := detectSource(model.ProtocolClaude, []sourceSample{srcSample(h, "{}", "ping")}, "https://example.com")
	if src.Name != "Amazon Bedrock" || src.Confidence != "high" {
		t.Fatalf("source=%+v, want Bedrock high", src)
	}
}

func TestDetectSourceClaudeRelayStaysUnknown(t *testing.T) {
	h := http.Header{}
	h.Set("X-Oneapi-Request-Id", "gw-1")
	src, caseID := detectSource(model.ProtocolClaude, []sourceSample{srcSample(h, `{"type":"message"}`, "ping")}, "https://relay.example.com")
	if !strings.Contains(src.Name, "中转") {
		t.Fatalf("name=%q, want relay/unknown", src.Name)
	}
	if src.Gateway == "" {
		t.Fatalf("gateway is empty")
	}
	if caseID != "ping" {
		t.Fatalf("caseID=%q, want ping", caseID)
	}
}

// TestSourceFallbackOnRelayEndToEnd 模拟一个只返回 OpenAI 兼容响应、带
// X-Oneapi 头但没有官方响应头的中转站：来源应回落为低置信度的 OpenAI 官方，
// 网关特征单独记录；同时每条检查都能通过 caseId 找回落库的请求/响应。
func TestSourceFallbackOnRelayEndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Oneapi-Request-Id", "gw-e2e")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-x", "object": "chat.completion", "model": "gpt-6-luna",
			"choices": []any{map[string]any{
				"message":       map[string]any{"role": "assistant", "content": "pong"},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{"prompt_tokens": 5, "completion_tokens": 1, "total_tokens": 6},
		})
	}))
	defer srv.Close()

	st, targetID := testStore(t)
	if err := st.SaveTarget(&model.Target{ID: targetID, Name: "relay", BaseURL: srv.URL, Protocol: model.ProtocolOpenAI}, "sk-test"); err != nil {
		t.Fatal(err)
	}
	report, runID := runModuleWithID(t, st, targetID, Config{
		Targets: []TargetSpec{{TargetID: targetID, Models: []string{"gpt-6-luna"}, Protocol: "openai"}},
		Checks:  ChecksConfig{Source: true, Identity: true, Consistency: true},
	})
	r := report.Models[0]
	if !strings.Contains(r.Source.Name, "OpenAI 官方") || r.Source.Confidence != "low" {
		t.Fatalf("source=%+v, want low-confidence OpenAI fallback", r.Source)
	}
	if !strings.Contains(r.Source.Gateway, "X-Oneapi-Request-Id") {
		t.Fatalf("gateway=%q, want oneapi marker", r.Source.Gateway)
	}

	items, _, err := st.ListItems(store.ItemFilter{RunID: runID, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	caseExists := map[string]bool{}
	var responseHeaders map[string]any
	for _, it := range items {
		if it.CaseID != "" {
			caseExists[it.CaseID] = true
		}
		if h, ok := it.Meta["responseHeaders"].(map[string]any); ok {
			responseHeaders = h
		}
	}
	if responseHeaders["X-Oneapi-Request-Id"] != "gw-e2e" {
		t.Fatalf("responseHeaders=%+v, want saved X-Oneapi-Request-Id", responseHeaders)
	}
	for _, c := range r.Checks {
		if c.CaseID == "" {
			t.Fatalf("check %s (%s) has no caseId", c.ID, c.Name)
		}
		if !caseExists[c.CaseID] {
			t.Fatalf("check %s references missing item caseId=%q", c.ID, c.CaseID)
		}
	}
}

func TestResponseHeaderSnapshot(t *testing.T) {
	h := http.Header{}
	h.Set("X-Request-Id", "req-1")
	h.Set("Openai-Organization", "org-test")
	h.Set("Anthropic-Ratelimit-Requests-Limit", "1000")
	h.Set("Set-Cookie", "session=secret")
	h.Set("Authorization", "Bearer sk-hidden")
	h.Set("X-Debug-Key", "sk-secret-value")

	out := responseHeaderSnapshot(h, []string{"sk-secret-value"})
	if out["X-Request-Id"] != "req-1" || out["Openai-Organization"] != "org-test" {
		t.Fatalf("missing diagnostic headers: %+v", out)
	}
	if _, ok := out["Set-Cookie"]; ok {
		t.Fatalf("Set-Cookie must not be persisted")
	}
	if _, ok := out["Authorization"]; ok {
		t.Fatalf("Authorization must not be persisted")
	}
	if out["X-Debug-Key"] != "***" {
		t.Fatalf("secret not redacted: %q", out["X-Debug-Key"])
	}
}
