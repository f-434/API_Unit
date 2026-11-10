// Package protocol implements the wire protocols used by the test runners:
// OpenAI Chat Completions, Anthropic Messages and Gemini generateContent.
package protocol

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"apiunit/internal/model"
)

const Version = "1.0.0"

type TargetInfo struct {
	ID       string
	Name     string
	BaseURL  string
	APIKey   string
	Protocol model.Protocol
}

// ContentPart represents one multimodal part. Type is one of text, image,
// audio, video, file. Data is base64 (inline) and URL is a remote reference.
type ContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	URL      string `json:"url,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	FileID   string `json:"fileId,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

type ToolCall struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name"`
	Arguments string `json:"arguments,omitempty"` // raw JSON object string
}

type ChatMessage struct {
	Role       string        `json:"role"`
	Content    string        `json:"content,omitempty"`
	Parts      []ContentPart `json:"parts,omitempty"`
	ToolCalls  []ToolCall    `json:"toolCalls,omitempty"`
	ToolCallID string        `json:"toolCallId,omitempty"`
	Name       string        `json:"name,omitempty"`
}

type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type SafetySetting struct {
	Category  string `json:"category"`
	Threshold string `json:"threshold"`
}

type ChatRequest struct {
	Model       string     `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	MaxTokens   int        `json:"maxTokens,omitempty"`
	Temperature *float64   `json:"temperature,omitempty"`
	TopP        *float64   `json:"topP,omitempty"`
	TopK        *int       `json:"topK,omitempty"`
	Stream      bool       `json:"stream,omitempty"`
	Stop        []string   `json:"stop,omitempty"`
	Seed        *int       `json:"seed,omitempty"`

	CandidateCount   *int           `json:"candidateCount,omitempty"`
	ResponseMimeType string         `json:"responseMimeType,omitempty"`
	ResponseSchema   map[string]any `json:"responseSchema,omitempty"`
	ThinkingBudget   *int           `json:"thinkingBudget,omitempty"`
	ReasoningEffort  string         `json:"reasoningEffort,omitempty"`
	PresencePenalty  *float64       `json:"presencePenalty,omitempty"`
	FrequencyPenalty *float64       `json:"frequencyPenalty,omitempty"`

	Tools          []Tool          `json:"tools,omitempty"`
	ToolChoice     string          `json:"toolChoice,omitempty"`
	BuiltinTools   []string        `json:"builtinTools,omitempty"`
	SafetySettings []SafetySetting `json:"safetySettings,omitempty"`
	CachedContent  string          `json:"cachedContent,omitempty"`

	ExtraGeneration map[string]any `json:"extraGeneration,omitempty"`
	ExtraBody       map[string]any `json:"extraBody,omitempty"`
}

type Usage struct {
	Prompt      int
	Completion  int
	Total       int
	Cached      int
	CacheWrite  int
	Reasoning   int
	Reported    bool
	PromptKnown bool
	CompKnown   bool
}

func (u Usage) Merge(o Usage) Usage {
	if o.Prompt != 0 {
		u.Prompt = o.Prompt
		u.PromptKnown = true
	}
	if o.Completion != 0 {
		u.Completion = o.Completion
		u.CompKnown = true
	}
	if o.Total != 0 {
		u.Total = o.Total
	}
	if o.Cached != 0 {
		u.Cached = o.Cached
	}
	if o.CacheWrite != 0 {
		u.CacheWrite = o.CacheWrite
	}
	if o.Reasoning != 0 {
		u.Reasoning = o.Reasoning
	}
	if o.Reported {
		u.Reported = true
	}
	if o.PromptKnown {
		u.PromptKnown = true
	}
	if o.CompKnown {
		u.CompKnown = true
	}
	return u
}

// Outcome is the normalized result of one HTTP call.
type Outcome struct {
	Status        int
	OK            bool
	Text          string
	Reasoning     string
	FinishReason  string
	Usage         Usage
	TTFTMs        float64
	GenerationMs  float64
	ChunkCount    int
	TextChunks    int
	RequestID     string
	ResponseID    string
	UpstreamModel string
	Endpoint      string
	Protocol      string
	RequestJSON   string
	ResponseJSON  string
	ResponseBytes int64
	Header        http.Header
	LatencyMs     float64
	ErrorMessage  string
	ErrorCode     string
	StreamError   bool
	Truncated     bool
	TextBytes     int

	firstTokenAt time.Time
	lastTokenAt  time.Time
}

// NormalizeBaseURL accepts loose user input such as "api.example.com/v1" and
// returns a canonical scheme://host[/prefix] root.
func NormalizeBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("Base URL 不能为空")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("Base URL 无效: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("仅支持 http/https 地址")
	}
	if u.Host == "" {
		return "", fmt.Errorf("Base URL 缺少主机名")
	}
	u.RawQuery = ""
	u.Fragment = ""
	p := strings.TrimRight(u.Path, "/")
	lower := strings.ToLower(p)
	suffixes := []string{
		"/v1/chat/completions", "/v1/messages", "/v1/responses", "/v1/completions",
		"/v1/embeddings", "/v1/models", "/v1beta/openai/models", "/v1beta/models",
	}
	for {
		changed := false
		for _, sfx := range suffixes {
			if strings.HasSuffix(lower, sfx) {
				p = p[:len(p)-len(sfx)]
				lower = strings.ToLower(p)
				changed = true
				break
			}
		}
		if !changed {
			break
		}
	}
	// Gemini full model path: /v1beta/models/gemini-2.5-pro:generateContent or .../models/...
	if i := strings.Index(lower, "/v1beta/models/"); i >= 0 {
		p = p[:i]
		lower = strings.ToLower(p)
	}
	p = strings.TrimRight(p, "/")
	if strings.HasSuffix(strings.ToLower(p), "/v1beta") {
		p = p[:len(p)-len("/v1beta")]
	} else if strings.HasSuffix(strings.ToLower(p), "/v1") {
		p = p[:len(p)-len("/v1")]
	}
	p = strings.TrimRight(p, "/")
	return fmt.Sprintf("%s://%s%s", u.Scheme, u.Host, p), nil
}

func ResolveProtocol(target model.Protocol, modelName string) model.Protocol {
	if target == "" || target == model.ProtocolAuto {
		return model.InferProtocol(modelName)
	}
	return target
}

func Endpoint(root string, p model.Protocol, modelName string, stream bool) string {
	switch p {
	case model.ProtocolClaude:
		return root + "/v1/messages"
	case model.ProtocolGemini:
		m := strings.TrimPrefix(modelName, "models/")
		if stream {
			return root + "/v1beta/models/" + url.PathEscape(m) + ":streamGenerateContent?alt=sse"
		}
		return root + "/v1beta/models/" + url.PathEscape(m) + ":generateContent"
	default:
		return root + "/v1/chat/completions"
	}
}

func authHeaders(req *http.Request, t TargetInfo, p model.Protocol) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("User-Agent", "apiunit/"+Version)
	key := strings.TrimSpace(t.APIKey)
	switch p {
	case model.ProtocolClaude:
		req.Header.Set("x-api-key", key)
		req.Header.Set("anthropic-version", "2023-06-01")
	case model.ProtocolGemini:
		req.Header.Set("x-goog-api-key", key)
	default:
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
	}
}

// BuildBody renders the protocol-specific request JSON without auth data.
func BuildBody(p model.Protocol, req ChatRequest) ([]byte, error) {
	switch p {
	case model.ProtocolClaude:
		return buildClaudeBody(req)
	case model.ProtocolGemini:
		return buildGeminiBody(req)
	default:
		return buildOpenAIBody(req)
	}
}

type Capability struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Stream  bool   `json:"stream"`
	Default bool   `json:"default"`
}

func Capabilities() []Capability {
	return []Capability{
		{ID: string(model.ProtocolOpenAI), Label: "OpenAI Chat Completions", Stream: true, Default: true},
		{ID: string(model.ProtocolClaude), Label: "Anthropic Messages", Stream: true},
		{ID: string(model.ProtocolGemini), Label: "Gemini generateContent", Stream: true},
	}
}

// classifier-friendly network error helpers
func IsTimeout(err error) bool {
	if err == nil {
		return false
	}
	if ne, ok := err.(interface{ Timeout() bool }); ok && ne.Timeout() {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "timeout") ||
		strings.Contains(strings.ToLower(err.Error()), "deadline exceeded")
}

func ContextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func ElapsedMs(start time.Time) float64 {
	return float64(time.Since(start).Microseconds()) / 1000.0
}

var _ = json.Marshal
