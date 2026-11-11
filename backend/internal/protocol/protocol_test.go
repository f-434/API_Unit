package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"apiunit/internal/model"
)

func TestNormalizeBaseURL(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"api.example.com", "https://api.example.com"},
		{"https://api.example.com/", "https://api.example.com"},
		{"https://api.example.com/v1", "https://api.example.com"},
		{"https://api.example.com/v1/chat/completions", "https://api.example.com"},
		{"https://api.example.com/prefix/v1/chat/completions?x=1", "https://api.example.com/prefix"},
		{"http://127.0.0.1:3000/v1beta/models/gemini-2.5-pro:generateContent", "http://127.0.0.1:3000"},
		{"https://relay.example.com/api/v1/models", "https://relay.example.com/api"},
	}
	for _, c := range cases {
		got, err := NormalizeBaseURL(c.in)
		if err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("%s => %s, want %s", c.in, got, c.want)
		}
	}
	if _, err := NormalizeBaseURL("ftp://x"); err == nil {
		t.Error("expected scheme error")
	}
}

func TestBuildGeminiRichRequest(t *testing.T) {
	temp := 0.2
	topK := 40
	budget := 1024
	req := ChatRequest{
		Model: "gemini-2.5-flash",
		Messages: []ChatMessage{
			{Role: "system", Content: "be safe"},
			{Role: "user", Parts: []ContentPart{
				{Type: "text", Text: "describe"},
				{Type: "image", URL: "https://example.com/a.png", MimeType: "image/png"},
			}},
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Name: "lookup", Arguments: `{"q":"x"}`}}},
			{Role: "tool", Name: "lookup", ToolCallID: "c1", Content: `{"result":"ok"}`},
		},
		Tools:           []Tool{{Name: "lookup", Description: "look things up", Parameters: map[string]any{"type": "object"}}},
		ToolChoice:      "lookup",
		BuiltinTools:    []string{"google_search"},
		SafetySettings:  []SafetySetting{{Category: "HARM_CATEGORY_HARASSMENT", Threshold: "BLOCK_NONE"}},
		CachedContent:   "cachedContents/abc",
		MaxTokens:       128,
		Temperature:     &temp,
		TopK:            &topK,
		ThinkingBudget:  &budget,
		ExtraGeneration: map[string]any{"foo": "bar"},
		ExtraBody:       map[string]any{"metadata": map[string]any{"trace": "t1"}},
	}
	b, err := BuildBody(model.ProtocolGemini, req)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(b, &body); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"systemInstruction", "contents", "tools", "toolConfig", "generationConfig", "safetySettings", "cachedContent", "metadata"} {
		if _, ok := body[key]; !ok {
			t.Errorf("gemini body missing %s: %s", key, string(b))
		}
	}
	gen := body["generationConfig"].(map[string]any)
	if gen["topK"].(float64) != 40 || gen["foo"] != "bar" {
		t.Errorf("generationConfig mismatch: %v", gen)
	}
	contents := body["contents"].([]any)
	toolMsg := contents[len(contents)-1].(map[string]any)
	parts := toolMsg["parts"].([]any)
	foundResponse := false
	for _, p := range parts {
		if _, ok := p.(map[string]any)["functionResponse"]; ok {
			foundResponse = true
		}
	}
	if !foundResponse {
		t.Errorf("missing functionResponse: %v", parts)
	}
}

func TestBuildClaudeRichRequest(t *testing.T) {
	topK := 10
	req := ChatRequest{
		Model: "claude-sonnet",
		Messages: []ChatMessage{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "hello"},
			{Role: "tool", ToolCallID: "toolu_1", Content: "tool result"},
		},
		Tools:      []Tool{{Name: "get", Description: "desc", Parameters: map[string]any{"type": "object"}}},
		ToolChoice: "get",
		TopK:       &topK,
		MaxTokens:  64,
	}
	b, err := BuildBody(model.ProtocolClaude, req)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{`"system":"sys"`, `"tool_result"`, `"tool_use_id":"toolu_1"`, `"input_schema"`, `"top_k":10`} {
		if !strings.Contains(s, want) {
			t.Errorf("claude body missing %s: %s", want, s)
		}
	}
}

func TestBuildOpenAIRichRequest(t *testing.T) {
	req := ChatRequest{
		Model: "gpt-4o-mini",
		Messages: []ChatMessage{
			{Role: "user", Parts: []ContentPart{{Type: "text", Text: "see"}, {Type: "image", URL: "https://x/y.png"}}},
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Name: "fn", Arguments: `{}`}}},
			{Role: "tool", ToolCallID: "call_1", Content: "done"},
		},
		Tools:        []Tool{{Name: "fn", Description: "d"}},
		ToolChoice:   "required",
		BuiltinTools: []string{"web_search"},
		MaxTokens:    32,
	}
	b, err := BuildBody(model.ProtocolOpenAI, req)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{`"image_url"`, `"tool_calls"`, `"tool_call_id":"call_1"`, `"tool_choice":"required"`, `"web_search_options"`} {
		if !strings.Contains(s, want) {
			t.Errorf("openai body missing %s: %s", want, s)
		}
	}
}

func TestParseAPIError(t *testing.T) {
	msg, code := ParseAPIError([]byte(`{"error":{"message":"no channel","code":"model_not_found"}}`))
	if msg != "no channel" || code != "model_not_found" {
		t.Errorf("got %q %q", msg, code)
	}
	msg, code = ParseAPIError([]byte(`<html><body>bad gateway</body></html>`))
	if msg != "bad gateway" || code != "" {
		t.Errorf("html strip got %q %q", msg, code)
	}
}

func TestStreamSuccessRequiresContentAndTerminal(t *testing.T) {
	cases := []struct {
		name     string
		protocol model.Protocol
		stream   string
		wantOK   bool
		wantCode string
	}{
		{
			name:     "usage only with done is empty",
			protocol: model.ProtocolOpenAI,
			stream:   "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":0}}\n\ndata: [DONE]\n\n",
			wantOK:   false,
			wantCode: "empty",
		},
		{
			name:     "text but no terminal marker is interrupted",
			protocol: model.ProtocolOpenAI,
			stream:   "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n",
			wantOK:   false,
			wantCode: "stream",
		},
		{
			name:     "text with finish reason is ok",
			protocol: model.ProtocolOpenAI,
			stream:   "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\n",
			wantOK:   true,
		},
		{
			name:     "garbage chunk without content is not a payload",
			protocol: model.ProtocolOpenAI,
			stream:   "data: 当前分组上游负载已饱和\n\n",
			wantOK:   false,
			wantCode: "protocol",
		},
		{
			name:     "claude without message_stop is interrupted",
			protocol: model.ProtocolClaude,
			stream:   "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n",
			wantOK:   false,
			wantCode: "stream",
		},
		{
			name:     "gemini empty candidate is empty",
			protocol: model.ProtocolGemini,
			stream:   "data: {\"candidates\":[{\"content\":{\"parts\":[]},\"finishReason\":\"MAX_TOKENS\"}],\"usageMetadata\":{\"promptTokenCount\":8}}\n\n",
			wantOK:   false,
			wantCode: "empty",
		},
		{
			name:     "gemini text with finish reason is ok",
			protocol: model.ProtocolGemini,
			stream:   "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hi\"}]},\"finishReason\":\"STOP\"}]}\n\n",
			wantOK:   true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := &Outcome{}
			has, err := parseStream(c.protocol, strings.NewReader(c.stream), out, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			ok := finalizeStream(out, has)
			if ok != c.wantOK {
				t.Fatalf("ok=%v want %v (out=%+v)", ok, c.wantOK, out)
			}
			if c.wantCode != "" && out.ErrorCode != c.wantCode {
				t.Errorf("code=%q want %q (%s)", out.ErrorCode, c.wantCode, out.ErrorMessage)
			}
		})
	}
}

func TestJSONSuccessRequiresContent(t *testing.T) {
	out := &Outcome{}
	body := `{"candidates":[{"content":{"parts":[]},"finishReason":"MAX_TOKENS"}],"usageMetadata":{"promptTokenCount":8,"totalTokenCount":8}}`
	has, err := parseJSONPayload(model.ProtocolGemini, []byte(body), out)
	if err != nil {
		t.Fatal(err)
	}
	if finalizeJSON(out, has) {
		t.Fatalf("empty candidates must fail: %+v", out)
	}
	if out.ErrorCode != "empty" {
		t.Errorf("code=%q want empty", out.ErrorCode)
	}

	out = &Outcome{}
	body = `{"error":{"message":"no channel","code":"model_not_found"}}`
	has, err = parseJSONPayload(model.ProtocolGemini, []byte(body), out)
	if err != nil {
		t.Fatal(err)
	}
	if finalizeJSON(out, has) {
		t.Fatalf("error object must fail: %+v", out)
	}
}

func TestFetchModelsRetriesTransient(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) < 3 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"message":"busy","code":"server_error"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-test","owned_by":"mock"}]}`))
	}))
	defer srv.Close()

	c := NewClientWithOptions(ClientOptions{
		DialTimeout:           time.Second,
		TLSHandshakeTimeout:   time.Second,
		ResponseHeaderTimeout: time.Second,
		DiscoverTimeout:       2 * time.Second,
		DiscoverRetries:       2,
	})
	models, err := c.fetchModelsWithRetry(context.Background(), discoverAttempt{
		protocol: model.ProtocolOpenAI,
		label:    "test",
		url:      srv.URL + "/v1/models",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "gpt-test" {
		t.Fatalf("unexpected models: %+v", models)
	}
	if got := atomic.LoadInt32(&hits); got != 3 {
		t.Fatalf("hits=%d want 3", got)
	}
}

func TestFetchModelsDoesNotRetryAuthFailure(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key","code":"invalid_api_key"}}`))
	}))
	defer srv.Close()

	c := NewClientWithOptions(ClientOptions{DiscoverTimeout: 2 * time.Second, DiscoverRetries: 3})
	_, err := c.fetchModelsWithRetry(context.Background(), discoverAttempt{
		protocol: model.ProtocolOpenAI,
		label:    "test",
		url:      srv.URL + "/v1/models",
	})
	if err == nil {
		t.Fatal("expected auth error")
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("hits=%d want 1", got)
	}
	var se *httpStatusError
	if !errors.As(err, &se) || se.Status != http.StatusUnauthorized {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDiscoveryNetworkHelpers(t *testing.T) {
	tlsErr := errors.New(`Get "https://example.com/v1/models": net/http: TLS handshake timeout`)
	if !isNetworkErr(tlsErr) {
		t.Fatal("TLS handshake timeout should be treated as a network error")
	}
	if !isTransientDiscoveryErr(tlsErr) {
		t.Fatal("TLS handshake timeout should be retryable")
	}
	authErr := &httpStatusError{Status: 401, Code: "invalid_api_key", Message: "bad key"}
	if isNetworkErr(authErr) || isTransientDiscoveryErr(authErr) {
		t.Fatal("auth errors must not be retried")
	}
	if networkHint([]string{"OpenAI /v1/models：" + tlsErr.Error()}) == "" {
		t.Fatal("expected a network hint")
	}
	if networkHint([]string{"OpenAI /v1/models：HTTP 401 invalid_api_key"}) != "" {
		t.Fatal("did not expect a network hint for auth errors")
	}
}

func TestParseOpenAIStreamMultiLine(t *testing.T) {
	stream := "data: {\"id\":\"x\",\"model\":\"m\",\"choices\":[{\"delta\":{\"content\":\"he\"}}]}\n\n" +
		"data: {\"id\":\"x\",\"choices\":[{\"delta\":{\"content\":\"llo\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n\n" +
		"data: [DONE]\n\n"
	out := &Outcome{}
	has, err := parseStream(model.ProtocolOpenAI, strings.NewReader(stream), out, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !has || out.Text != "hello" || out.Usage.Prompt != 3 || out.Usage.Completion != 2 {
		t.Errorf("unexpected outcome: %+v", out)
	}
	if out.TTFTMs < 0 || out.GenerationMs < 0 {
		t.Errorf("bad timings: %+v", out)
	}
}
