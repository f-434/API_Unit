// Package tokens implements token counting strategies: embedded tiktoken
// codecs for OpenAI, a local SentencePiece (Gemma) tokenizer for Gemini, an
// optional online OpenAI counting endpoint and a heuristic fallback.
package tokens

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/tiktoken-go/tokenizer"
	"github.com/tiktoken-go/tokenizer/codec"

	"apiunit/internal/protocol"
)

type Strategy string

const (
	StrategyAuto      Strategy = "auto"
	StrategyTiktoken  Strategy = "tiktoken"
	StrategyGemini    Strategy = "gemini"
	StrategyOnline    Strategy = "online"
	StrategyHeuristic Strategy = "heuristic"
	StrategyNone      Strategy = "none"
)

type Result struct {
	Tokens    int    `json:"tokens"`
	Source    string `json:"source"`
	Detail    string `json:"detail,omitempty"`
	Estimated bool   `json:"estimated"`
}

type Counter struct {
	offline  bool
	tokenDir string
	http     *httpClient

	mu     sync.Mutex
	o200k  tokenizer.Codec
	cl100k tokenizer.Codec

	geminiMu         sync.Mutex
	geminiDownloadMu sync.Mutex
	processors       map[string]*geminiProcessor
	geminiErrors     map[string]string
}

func New(tokenDir string, offline bool) *Counter {
	return &Counter{
		offline:      offline,
		tokenDir:     tokenDir,
		http:         newHTTPClient(),
		processors:   map[string]*geminiProcessor{},
		geminiErrors: map[string]string{},
	}
}

func (c *Counter) codecFor(model string) tokenizer.Codec {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.o200k == nil {
		c.o200k = codec.NewO200kBase()
		c.cl100k = codec.NewCl100kBase()
	}
	if usesCl100k(model) {
		return c.cl100k
	}
	return c.o200k
}

func usesCl100k(model string) bool {
	m := strings.ToLower(strings.TrimPrefix(model, "ft:"))
	if i := strings.LastIndexAny(m, "/:"); i >= 0 {
		m = m[i+1:]
	}
	return m == "gpt-4" || strings.HasPrefix(m, "gpt-4-") ||
		strings.HasPrefix(m, "gpt-3.5") || strings.HasPrefix(m, "gpt-35")
}

// CountText counts a plain string with the requested strategy.
func (c *Counter) CountText(modelName, text string, strategy Strategy) Result {
	if text == "" {
		return Result{Tokens: 0, Source: "empty"}
	}
	if strategy == "" {
		strategy = StrategyAuto
	}
	if strategy == StrategyNone {
		return Result{Tokens: 0, Source: "none"}
	}
	if strategy == StrategyTiktoken {
		if n, err := c.tiktokenCount(modelName, text); err == nil {
			return Result{Tokens: n, Source: "tiktoken:" + c.codecName(modelName)}
		}
		return c.heuristic(text, "tiktoken 不可用，已回退估算")
	}
	if strategy == StrategyGemini {
		if n, name, err := c.geminiCount(modelName, text); err == nil {
			return Result{Tokens: n, Source: "gemini-local:" + name}
		} else {
			return c.heuristic(text, "Gemini 本地分词器不可用："+err.Error())
		}
	}
	if strategy == StrategyHeuristic {
		return c.heuristic(text, "")
	}
	if strategy == StrategyOnline {
		// Online counting requires a full chat request; fall through to family
		// default here and let CountChat handle it.
		strategy = StrategyAuto
	}

	switch family(modelName) {
	case "openai":
		if n, err := c.tiktokenCount(modelName, text); err == nil {
			return Result{Tokens: n, Source: "tiktoken:" + c.codecName(modelName)}
		}
	case "gemini":
		if n, name, err := c.geminiCount(modelName, text); err == nil {
			return Result{Tokens: n, Source: "gemini-local:" + name}
		}
	}
	return c.heuristic(text, "")
}

// CountChat approximates the prompt token count including per-message overhead.
func (c *Counter) CountChat(modelName string, msgs []protocol.ChatMessage, strategy Strategy) Result {
	if strategy == StrategyNone {
		return Result{Tokens: 0, Source: "none"}
	}
	if strategy == StrategyHeuristic {
		return c.heuristicChat(msgs, "")
	}
	total := 0
	source := "heuristic"
	estimated := false
	detail := ""
	for i, m := range msgs {
		r := c.CountText(modelName, m.Content, strategy)
		if i == 0 {
			source = r.Source
			estimated = r.Estimated
			detail = r.Detail
		}
		if r.Estimated && strategy == StrategyAuto {
			estimated = true
		}
		total += r.Tokens + 4
	}
	total += 3
	return Result{Tokens: total, Source: source, Detail: detail, Estimated: estimated}
}

// CountOnline asks the upstream OpenAI-compatible service to count input tokens.
func (c *Counter) CountOnline(ctx context.Context, t protocol.TargetInfo, modelName string, msgs []protocol.ChatMessage) (Result, error) {
	root, err := protocol.NormalizeBaseURL(t.BaseURL)
	if err != nil {
		return Result{}, err
	}
	input := make([]map[string]string, 0, len(msgs))
	for _, m := range msgs {
		input = append(input, map[string]string{"role": m.Role, "content": m.Content})
	}
	body, _ := json.Marshal(map[string]any{"model": modelName, "input": input})
	url := root + "/v1/responses/input_tokens"
	data, err := c.http.postJSON(ctx, url, body, map[string]string{
		"Authorization": "Bearer " + t.APIKey,
		"Content-Type":  "application/json",
	})
	if err != nil {
		return Result{}, err
	}
	var resp struct {
		InputTokens int `json:"input_tokens"`
		TotalTokens int `json:"total_tokens"`
		Usage       struct {
			InputTokens int `json:"input_tokens"`
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return Result{}, fmt.Errorf("在线计数响应解析失败: %w", err)
	}
	n := resp.InputTokens
	if n == 0 {
		n = resp.TotalTokens
	}
	if n == 0 {
		n = resp.Usage.InputTokens
	}
	if n == 0 {
		n = resp.Usage.TotalTokens
	}
	if n == 0 {
		return Result{}, fmt.Errorf("在线计数未返回 input_tokens")
	}
	return Result{Tokens: n, Source: "openai-online"}, nil
}

// Estimate returns a rough token count for already-truncated UI previews.
func (c *Counter) Estimate(modelName, text string) int {
	return c.CountText(modelName, text, StrategyAuto).Tokens
}

func (c *Counter) tiktokenCount(modelName, text string) (int, error) {
	codec := c.codecFor(modelName)
	c.mu.Lock()
	defer c.mu.Unlock()
	total, start := 0, 0
	runStart := 0
	runClass := runClassOfRune(0)
	for i, r := range text {
		class := runClassOfRune(r)
		if class != runClass {
			runClass, runStart = class, i
			continue
		}
		if class != runNumber && i-runStart >= 1024 {
			n, err := codec.Count(text[start:i])
			if err != nil {
				return 0, err
			}
			total += n
			start, runStart = i, i
		}
	}
	n, err := codec.Count(text[start:])
	if err != nil {
		return 0, err
	}
	return total + n, nil
}

func (c *Counter) codecName(modelName string) string {
	if usesCl100k(modelName) {
		return "cl100k_base"
	}
	return "o200k_base"
}

type runClass int

const (
	runNone runClass = iota
	runLetter
	runNumber
	runSpace
	runSymbol
)

func runClassOfRune(r rune) runClass {
	if r < utf8.RuneSelf {
		switch {
		case 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z':
			return runLetter
		case '0' <= r && r <= '9':
			return runNumber
		case r == ' ' || '\t' <= r && r <= '\r':
			return runSpace
		default:
			return runSymbol
		}
	}
	switch {
	case unicode.IsLetter(r) || unicode.IsMark(r):
		return runLetter
	case unicode.IsNumber(r):
		return runNumber
	case unicode.IsSpace(r):
		return runSpace
	default:
		return runSymbol
	}
}

func (c *Counter) heuristic(text, detail string) Result {
	ascii := 0.0
	wide := 0.0
	for _, r := range text {
		switch {
		case r < utf8.RuneSelf:
			ascii++
		case unicode.IsSpace(r):
			ascii++
		default:
			wide++
		}
	}
	n := int(ascii/3.8 + wide/1.5)
	if n < 1 {
		n = 1
	}
	return Result{Tokens: n, Source: "heuristic", Detail: detail, Estimated: true}
}

func (c *Counter) heuristicChat(msgs []protocol.ChatMessage, detail string) Result {
	total := 3
	for _, m := range msgs {
		total += c.heuristic(m.Content, "").Tokens + 4
	}
	return Result{Tokens: total, Source: "heuristic", Detail: detail, Estimated: true}
}

func family(modelName string) string {
	m := strings.ToLower(modelName)
	if i := strings.LastIndexAny(m, "/:"); i >= 0 {
		m = m[i+1:]
	}
	switch {
	case strings.Contains(m, "claude"):
		return "claude"
	case strings.Contains(m, "gemini") || strings.Contains(m, "learnlm"):
		return "gemini"
	case strings.HasPrefix(m, "gpt-") || strings.HasPrefix(m, "chatgpt") ||
		strings.HasPrefix(m, "o1") || strings.HasPrefix(m, "o3") || strings.HasPrefix(m, "o4") ||
		strings.HasPrefix(m, "gpt-5") || strings.HasPrefix(m, "gpt-6") ||
		strings.HasPrefix(m, "davinci") || strings.HasPrefix(m, "babbage"):
		return "openai"
	default:
		return "other"
	}
}
