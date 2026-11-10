package protocol

import (
	"encoding/json"
	"strings"
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
		Tools:          []Tool{{Name: "lookup", Description: "look things up", Parameters: map[string]any{"type": "object"}}},
		ToolChoice:     "lookup",
		BuiltinTools:   []string{"google_search"},
		SafetySettings: []SafetySetting{{Category: "HARM_CATEGORY_HARASSMENT", Threshold: "BLOCK_NONE"}},
		CachedContent:  "cachedContents/abc",
		MaxTokens:      128,
		Temperature:    &temp,
		TopK:           &topK,
		ThinkingBudget: &budget,
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
