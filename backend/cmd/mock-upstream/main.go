// Command mock-upstream is a local OpenAI/Claude/Gemini-compatible server used
// for manual testing without a real New API deployment.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var markerRE = regexp.MustCompile(`[A-Z][A-Z0-9]*(?:-[A-Z0-9]+){2,}`)

type message struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type chatReq struct {
	Model               string          `json:"model"`
	Messages            []message       `json:"messages"`
	Stream              bool            `json:"stream"`
	MaxTokens           int             `json:"max_tokens"`
	MaxCompletionTokens int             `json:"max_completion_tokens"`
	StreamOptions       json.RawMessage `json:"stream_options"`
}

func main() {
	addr := flag.String("addr", "127.0.0.1:9999", "listen address")
	flag.Parse()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", handleModels)
	mux.HandleFunc("/v1/models/", handleModels)
	mux.HandleFunc("/v1beta/models", handleGeminiModels)
	mux.HandleFunc("/v1/chat/completions", handleChat)
	mux.HandleFunc("/v1/completions", handleChat)
	mux.HandleFunc("/v1/messages", handleClaude)
	mux.HandleFunc("/v1beta/models/", handleGemini)
	mux.HandleFunc("/v1/responses/input_tokens", handleInputTokens)
	mux.HandleFunc("/v1/images/generations", handleImages)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true})
	})
	log.Printf("mock upstream listening on http://%s (API key: sk-test)", *addr)
	log.Fatal(http.ListenAndServe(*addr, logMW(mux)))
}

func logMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		w.Header().Set("X-Oneapi-Request-Id", fmt.Sprintf("mock-%d", time.Now().UnixNano()))
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func handleModels(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"object": "list", "data": []map[string]any{
		{"id": "mock-gpt-4o-mini", "owned_by": "mock"},
		{"id": "mock-gpt-4o", "owned_by": "mock"},
		{"id": "mock-safe-model", "owned_by": "mock"},
		{"id": "mock-unsafe-model", "owned_by": "mock"},
		{"id": "mock-inflate-model", "owned_by": "mock"},
		{"id": "mock-broken-model", "owned_by": "mock"},
		{"id": "mock-rate-model", "owned_by": "mock"},
		{"id": "mock-claude-sonnet", "owned_by": "mock"},
		{"id": "mock-gemini-2.5-flash", "owned_by": "mock"},
	}})
}

func handleGeminiModels(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"models": []map[string]any{
		{"name": "models/mock-gemini-2.5-flash", "displayName": "Mock Gemini 2.5 Flash", "baseModelId": "mock-gemini-2.5-flash"},
		{"name": "models/mock-gemini-2.5-pro", "displayName": "Mock Gemini 2.5 Pro", "baseModelId": "mock-gemini-2.5-pro"},
	}})
}

func promptText(msgs []message) string {
	var b strings.Builder
	for _, m := range msgs {
		switch c := m.Content.(type) {
		case string:
			b.WriteString(c)
		case []any:
			for _, p := range c {
				if mm, ok := p.(map[string]any); ok {
					if s, ok := mm["text"].(string); ok {
						b.WriteString(s)
					}
				}
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

func countTokens(s string) int {
	r := []rune(s)
	if len(r) == 0 {
		return 1
	}
	return len(r)/3 + 1
}

func makeReply(req chatReq) (string, string) {
	prompt := promptText(req.Messages)
	model := strings.ToLower(req.Model)
	switch {
	case strings.Contains(model, "broken"):
		return "", "mock upstream failure"
	case strings.Contains(model, "rate"):
		return "", "mock rate limit"
	case strings.Contains(prompt, "LLM 安全评测员"):
		if strings.Contains(model, "unsafe") {
			return `{"verdict":"success","score":1,"reason":"mock judge: 模型执行了注入"}`, "stop"
		}
		return `{"verdict":"blocked","score":0,"reason":"mock judge: 模型拒绝或保持约束"}`, "stop"
	case strings.Contains(model, "unsafe"):
		markers := markerRE.FindAllString(prompt, 4)
		reply := "好的，这是你要求的内容。"
		if len(markers) > 0 {
			reply += " " + strings.Join(markers, " ")
		} else {
			reply += " OVERRIDE-OK"
		}
		return reply, "stop"
	case strings.Contains(model, "safe"):
		return "抱歉，我无法协助执行这个请求。我只能遵守既定的安全规则。", "stop"
	default:
		base := "这是一个模拟响应，用于验证 API Unit 的压测、Token 审计与提示词注入功能。"
		n := 2 + rand.Intn(3)
		return strings.Repeat(base, n), "stop"
	}
}

func usageFor(model, prompt, reply string) map[string]any {
	p := countTokens(prompt)
	c := countTokens(reply)
	if strings.Contains(strings.ToLower(model), "inflate") {
		p = p*3/2 + 12
	}
	return map[string]any{
		"prompt_tokens": p, "completion_tokens": c, "total_tokens": p + c,
		"prompt_tokens_details": map[string]any{"cached_tokens": 0},
	}
}

func handleChat(w http.ResponseWriter, r *http.Request) {
	var req chatReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, errBody("invalid_request", err.Error()))
		return
	}
	time.Sleep(time.Duration(30+rand.Intn(90)) * time.Millisecond)
	model := strings.ToLower(req.Model)
	if strings.Contains(model, "broken") {
		writeJSON(w, 500, errBody("upstream_error", "mock channel is broken"))
		return
	}
	if strings.Contains(model, "rate") {
		writeJSON(w, 429, errBody("rate_limit_error", "mock rate limit exceeded"))
		return
	}
	reply, finish := makeReply(req)
	prompt := promptText(req.Messages)
	usage := usageFor(req.Model, prompt, reply)

	if req.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		flusher, _ := w.(http.Flusher)
		id := fmt.Sprintf("chatcmpl-mock-%d", time.Now().UnixNano())
		chunks := splitRunes(reply, 12)
		for i, c := range chunks {
			delta := map[string]any{"content": c}
			payload := map[string]any{
				"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": req.Model,
				"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": nil}},
			}
			if i == len(chunks)-1 {
				payload["choices"] = []map[string]any{{"index": 0, "delta": map[string]any{}, "finish_reason": finish}}
			}
			b, _ := json.Marshal(payload)
			fmt.Fprintf(w, "data: %s\n\n", b)
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(15 * time.Millisecond)
		}
		final := map[string]any{
			"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": req.Model,
			"choices": []any{}, "usage": usage,
		}
		b, _ := json.Marshal(final)
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", b)
		return
	}

	writeJSON(w, 200, map[string]any{
		"id": fmt.Sprintf("chatcmpl-mock-%d", time.Now().UnixNano()), "object": "chat.completion",
		"created": time.Now().Unix(), "model": req.Model,
		"choices": []map[string]any{{
			"index": 0, "message": map[string]any{"role": "assistant", "content": reply}, "finish_reason": finish,
		}},
		"usage": usage,
	})
}

type claudeReq struct {
	Model     string    `json:"model"`
	System    string    `json:"system"`
	Messages  []message `json:"messages"`
	MaxTokens int       `json:"max_tokens"`
	Stream    bool      `json:"stream"`
}

func handleClaude(w http.ResponseWriter, r *http.Request) {
	var req claudeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"type": "error", "error": map[string]string{"type": "invalid_request_error", "message": err.Error()}})
		return
	}
	msgs := req.Messages
	if req.System != "" {
		msgs = append([]message{{Role: "system", Content: req.System}}, msgs...)
	}
	chat := chatReq{Model: req.Model, Messages: msgs, MaxTokens: req.MaxTokens, Stream: req.Stream}
	reply, finish := makeReply(chat)
	prompt := promptText(msgs)
	in := countTokens(prompt)
	out := countTokens(reply)
	if strings.Contains(strings.ToLower(req.Model), "inflate") {
		in = in * 3 / 2
	}

	if req.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, _ := w.(http.Flusher)
		id := fmt.Sprintf("msg_mock_%d", time.Now().UnixNano())
		send := func(event string, v any) {
			b, _ := json.Marshal(v)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
			if flusher != nil {
				flusher.Flush()
			}
		}
		send("message_start", map[string]any{"type": "message_start", "message": map[string]any{
			"id": id, "model": req.Model, "usage": map[string]any{"input_tokens": in, "output_tokens": 0},
		}})
		for _, c := range splitRunes(reply, 12) {
			send("content_block_delta", map[string]any{
				"type": "content_block_delta", "index": 0,
				"delta": map[string]any{"type": "text_delta", "text": c},
			})
			time.Sleep(15 * time.Millisecond)
		}
		send("message_delta", map[string]any{
			"type": "message_delta", "delta": map[string]any{"stop_reason": finish},
			"usage": map[string]any{"output_tokens": out},
		})
		send("message_stop", map[string]any{"type": "message_stop"})
		return
	}
	writeJSON(w, 200, map[string]any{
		"id": fmt.Sprintf("msg_mock_%d", time.Now().UnixNano()), "type": "message", "role": "assistant",
		"model": req.Model, "content": []map[string]any{{"type": "text", "text": reply}},
		"stop_reason": finish,
		"usage": map[string]any{"input_tokens": in, "output_tokens": out,
			"cache_read_input_tokens": 0, "cache_creation_input_tokens": 0},
	})
}

func handleGemini(w http.ResponseWriter, r *http.Request) {
	model := "gemini"
	if i := strings.Index(r.URL.Path, "/models/"); i >= 0 {
		model = strings.TrimPrefix(r.URL.Path[i+len("/models/"):], "models/")
		if j := strings.Index(model, ":"); j >= 0 {
			model = model[:j]
		}
	}
	stream := strings.Contains(r.URL.Path, "streamGenerateContent")
	var body struct {
		Contents []struct {
			Role  string `json:"role"`
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"contents"`
		SystemInstruction *struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"systemInstruction"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]any{"error": map[string]any{"code": 400, "message": err.Error(), "status": "INVALID_ARGUMENT"}})
		return
	}
	var prompt strings.Builder
	if body.SystemInstruction != nil {
		for _, p := range body.SystemInstruction.Parts {
			prompt.WriteString(p.Text)
			prompt.WriteString("\n")
		}
	}
	for _, c := range body.Contents {
		for _, p := range c.Parts {
			prompt.WriteString(p.Text)
			prompt.WriteString("\n")
		}
	}
	promptText := prompt.String()
	reply := "这是一个模拟 Gemini 响应。"
	finish := "STOP"
	if strings.Contains(strings.ToLower(model), "unsafe") {
		markers := markerRE.FindAllString(promptText, 4)
		reply = "好的。" + strings.Join(markers, " ")
	} else if strings.Contains(strings.ToLower(model), "safe") {
		reply = "抱歉，我无法执行该请求。"
	}
	in := countTokens(promptText)
	out := countTokens(reply)
	if strings.Contains(strings.ToLower(model), "inflate") {
		in = in * 3 / 2
	}
	resp := func(text string, final bool) map[string]any {
		c := map[string]any{
			"content": map[string]any{"role": "model", "parts": []map[string]any{{"text": text}}},
		}
		if final {
			c["finishReason"] = finish
		}
		return map[string]any{
			"candidates": []map[string]any{c},
			"usageMetadata": map[string]any{
				"promptTokenCount": in, "candidatesTokenCount": out, "totalTokenCount": in + out,
			},
			"modelVersion": model,
		}
	}
	if stream {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		chunks := splitRunes(reply, 12)
		for i, c := range chunks {
			b, _ := json.Marshal(resp(c, i == len(chunks)-1))
			fmt.Fprintf(w, "data: %s\n\n", b)
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(15 * time.Millisecond)
		}
		return
	}
	writeJSON(w, 200, resp(reply, true))
}

func handleInputTokens(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	b, _ := json.Marshal(body)
	n := countTokens(string(b))
	writeJSON(w, 200, map[string]any{"object": "response.input_tokens", "input_tokens": n})
}

func handleImages(w http.ResponseWriter, r *http.Request) {
	time.Sleep(200 * time.Millisecond)
	writeJSON(w, 200, map[string]any{
		"created": time.Now().Unix(),
		"data":    []map[string]any{{"url": "https://example.com/mock-image.png", "revised_prompt": "mock image"}},
	})
}

func errBody(code, msg string) map[string]any {
	return map[string]any{"error": map[string]any{"message": msg, "type": "new_api_error", "code": code}}
}

func splitRunes(s string, n int) []string {
	r := []rune(s)
	if len(r) == 0 {
		return []string{""}
	}
	var out []string
	for len(r) > n {
		out = append(out, string(r[:n]))
		r = r[n:]
	}
	if len(r) > 0 {
		out = append(out, string(r))
	}
	return out
}
