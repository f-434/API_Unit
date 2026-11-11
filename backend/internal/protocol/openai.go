package protocol

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"apiunit/internal/model"
)

func buildOpenAIBody(req ChatRequest) ([]byte, error) {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 256
	}
	reasoning := isOpenAIReasoning(req.Model)
	body := map[string]any{
		"model":    req.Model,
		"messages": openAIMessages(req.Messages),
	}
	n := 1
	if req.CandidateCount != nil && *req.CandidateCount > 0 {
		n = *req.CandidateCount
	}
	body["n"] = n
	if req.Stream {
		body["stream"] = true
		body["stream_options"] = map[string]any{"include_usage": true}
	}
	if reasoning {
		body["max_completion_tokens"] = maxTokens
		if req.ReasoningEffort != "" {
			body["reasoning_effort"] = req.ReasoningEffort
		}
	} else {
		body["max_tokens"] = maxTokens
		if req.Temperature != nil {
			body["temperature"] = *req.Temperature
		}
		if req.TopP != nil {
			body["top_p"] = *req.TopP
		}
	}
	if len(req.Stop) > 0 {
		body["stop"] = req.Stop
	}
	if req.Seed != nil {
		body["seed"] = *req.Seed
	}
	if req.PresencePenalty != nil {
		body["presence_penalty"] = *req.PresencePenalty
	}
	if req.FrequencyPenalty != nil {
		body["frequency_penalty"] = *req.FrequencyPenalty
	}
	if req.ResponseMimeType != "" || req.ResponseSchema != nil {
		rf := map[string]any{}
		if req.ResponseSchema != nil {
			name := "response"
			rf["type"] = "json_schema"
			rf["json_schema"] = map[string]any{"name": name, "schema": req.ResponseSchema}
		} else {
			rf["type"] = "json_object"
		}
		body["response_format"] = rf
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			fn := map[string]any{"name": t.Name}
			if t.Description != "" {
				fn["description"] = t.Description
			}
			if t.Parameters != nil {
				fn["parameters"] = t.Parameters
			}
			tools = append(tools, map[string]any{"type": "function", "function": fn})
		}
		body["tools"] = tools
	}
	if req.ToolChoice != "" {
		switch req.ToolChoice {
		case "auto", "none", "required":
			body["tool_choice"] = req.ToolChoice
		default:
			body["tool_choice"] = map[string]any{"type": "function", "function": map[string]any{"name": req.ToolChoice}}
		}
	}
	for _, b := range req.BuiltinTools {
		if b == "web_search" || b == "web_search_preview" {
			body["web_search_options"] = map[string]any{}
		}
	}
	for k, v := range req.ExtraGeneration {
		body[k] = v
	}
	for k, v := range req.ExtraBody {
		body[k] = v
	}
	body["model"] = req.Model
	return json.Marshal(body)
}

func openAIMessages(msgs []ChatMessage) []map[string]any {
	out := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		role := m.Role
		if role == "" {
			role = "user"
		}
		item := map[string]any{"role": role}
		if role == "tool" {
			if len(m.Parts) > 0 {
				item["content"] = openAIParts(m.Parts)
			} else {
				item["content"] = m.Content
			}
			if m.ToolCallID != "" {
				item["tool_call_id"] = m.ToolCallID
			}
			out = append(out, item)
			continue
		}
		if len(m.Parts) > 0 {
			item["content"] = openAIParts(m.Parts)
		} else {
			item["content"] = m.Content
		}
		if len(m.ToolCalls) > 0 {
			calls := make([]map[string]any, 0, len(m.ToolCalls))
			for _, c := range m.ToolCalls {
				args := c.Arguments
				if strings.TrimSpace(args) == "" {
					args = "{}"
				}
				calls = append(calls, map[string]any{
					"id": c.ID, "type": "function",
					"function": map[string]any{"name": c.Name, "arguments": args},
				})
			}
			item["tool_calls"] = calls
		}
		if m.Name != "" {
			item["name"] = m.Name
		}
		out = append(out, item)
	}
	return out
}

func openAIParts(parts []ContentPart) []map[string]any {
	out := make([]map[string]any, 0, len(parts))
	for _, p := range parts {
		switch strings.ToLower(p.Type) {
		case "image", "image_url":
			url := p.URL
			if url == "" && p.Data != "" {
				mime := p.MimeType
				if mime == "" {
					mime = "image/png"
				}
				url = "data:" + mime + ";base64," + p.Data
			}
			iu := map[string]any{"url": url}
			if p.Detail != "" {
				iu["detail"] = p.Detail
			}
			out = append(out, map[string]any{"type": "image_url", "image_url": iu})
		case "audio", "input_audio":
			format := "wav"
			if p.MimeType != "" {
				if i := strings.LastIndex(p.MimeType, "/"); i >= 0 && i+1 < len(p.MimeType) {
					format = p.MimeType[i+1:]
				}
			}
			data := p.Data
			if data == "" {
				data = p.URL
			}
			out = append(out, map[string]any{"type": "input_audio", "input_audio": map[string]any{"data": data, "format": format}})
		case "file":
			file := map[string]any{}
			if p.FileID != "" {
				file["file_id"] = p.FileID
			}
			if p.Data != "" {
				mime := p.MimeType
				if mime == "" {
					mime = "application/octet-stream"
				}
				file["file_data"] = "data:" + mime + ";base64," + p.Data
				file["filename"] = "upload." + extForMime(mime)
			}
			if p.URL != "" {
				file["file_data"] = p.URL
			}
			out = append(out, map[string]any{"type": "file", "file": file})
		case "video", "video_url":
			out = append(out, map[string]any{"type": "video_url", "video_url": map[string]any{"url": p.URL}})
		default:
			text := p.Text
			if text == "" && p.URL != "" {
				text = p.URL
			}
			out = append(out, map[string]any{"type": "text", "text": text})
		}
	}
	return out
}

func extForMime(mime string) string {
	if i := strings.LastIndex(mime, "/"); i >= 0 && i+1 < len(mime) {
		return mime[i+1:]
	}
	return "bin"
}

func isOpenAIReasoning(modelName string) bool {
	m := strings.ToLower(modelName)
	if i := strings.LastIndexAny(m, "/:"); i >= 0 {
		m = m[i+1:]
	}
	for _, p := range []string{"o1", "o3", "o4", "gpt-5", "gpt-6"} {
		if strings.HasPrefix(m, p) {
			return true
		}
	}
	return false
}

type openAIUsage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	TotalTokens         int `json:"total_tokens"`
	PromptCacheHit      int `json:"prompt_cache_hit_tokens"`
	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	PromptTokensDetails struct {
		CachedTokens             int `json:"cached_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		CacheWriteTokens         int `json:"cache_write_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

func (u *openAIUsage) normalize() Usage {
	if u == nil {
		return Usage{}
	}
	prompt := u.PromptTokens
	if prompt == 0 {
		prompt = u.InputTokens
	}
	comp := u.CompletionTokens
	if comp == 0 {
		comp = u.OutputTokens
	}
	cached := u.PromptTokensDetails.CachedTokens
	if cached == 0 {
		cached = u.PromptCacheHit
	}
	total := u.TotalTokens
	if total == 0 {
		total = prompt + comp
	}
	return Usage{
		Prompt: prompt, Completion: comp, Total: total,
		Cached: cached, CacheWrite: u.PromptTokensDetails.CacheWriteTokens + u.PromptTokensDetails.CacheCreationInputTokens,
		Reasoning: u.CompletionTokensDetails.ReasoningTokens,
		Reported:  true, PromptKnown: prompt > 0, CompKnown: comp > 0,
	}
}

type openAIMessage struct {
	Content          json.RawMessage `json:"content"`
	ReasoningContent string          `json:"reasoning_content"`
	Reasoning        string          `json:"reasoning"`
	ToolCalls        json.RawMessage `json:"tool_calls"`
}

type openAIChoice struct {
	Delta        *openAIMessage `json:"delta"`
	Message      *openAIMessage `json:"message"`
	Text         string         `json:"text"`
	FinishReason *string        `json:"finish_reason"`
}

type openAIResponse struct {
	ID      string          `json:"id"`
	Model   string          `json:"model"`
	Object  string          `json:"object"`
	Choices []openAIChoice  `json:"choices"`
	Usage   *openAIUsage    `json:"usage"`
	Error   json.RawMessage `json:"error"`
}

func parseOpenAIJSON(data []byte, out *Outcome) (bool, error) {
	var r openAIResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return false, fmt.Errorf("OpenAI 响应解析失败: %w", err)
	}
	if len(r.Error) > 0 && string(r.Error) != "null" {
		out.ErrorMessage, out.ErrorCode = ParseAPIError(data)
		if out.ErrorMessage == "" {
			out.ErrorMessage = "上游返回 error 对象"
		}
		return false, nil
	}
	out.ResponseID = r.ID
	out.UpstreamModel = r.Model
	hasPayload := false
	if r.Usage != nil {
		out.Usage = out.Usage.Merge(r.Usage.normalize())
	}
	for _, ch := range r.Choices {
		hasPayload = true
		if ch.Message != nil {
			text := extractOpenAIContent(ch.Message.Content)
			if text == "" {
				text = ch.Message.ReasoningContent
				if text == "" {
					text = ch.Message.Reasoning
				}
			}
			if text != "" {
				out.Text += text
				out.HasContent = true
			}
			if rawPresent(ch.Message.ToolCalls) {
				out.HasContent = true
			}
		}
		if ch.Text != "" {
			out.Text += ch.Text
			out.HasContent = true
		}
		if ch.FinishReason != nil && *ch.FinishReason != "" {
			out.FinishReason = *ch.FinishReason
			out.Terminal = true
		}
	}
	return hasPayload, nil
}

func parseOpenAIStream(r io.Reader, out *Outcome, start time.Time, cap *capture) (bool, error) {
	hasPayload := false
	done := false
	err := readSSE(r, func(ev SSEEvent) error {
		data := strings.TrimSpace(ev.Data)
		if data == "" {
			return nil
		}
		if data == "[DONE]" {
			done = true
			out.Terminal = true
			return io.EOF
		}
		cap.Add(data + "\n")
		var chunk openAIResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			out.ChunkCount++
			out.Malformed++
			return nil
		}
		out.ChunkCount++
		if len(chunk.Error) > 0 && string(chunk.Error) != "null" {
			out.ErrorMessage, out.ErrorCode = ParseAPIError([]byte(data))
			if out.ErrorMessage == "" {
				out.ErrorMessage = "流式响应中返回 error 对象"
			}
			out.StreamError = true
			return io.EOF
		}
		if chunk.ID != "" {
			out.ResponseID = chunk.ID
		}
		if chunk.Model != "" {
			out.UpstreamModel = chunk.Model
		}
		if chunk.Usage != nil {
			hasPayload = true
			out.Usage = out.Usage.Merge(chunk.Usage.normalize())
		}
		for _, ch := range chunk.Choices {
			hasPayload = true
			if ch.FinishReason != nil && *ch.FinishReason != "" {
				out.FinishReason = *ch.FinishReason
				out.Terminal = true
			}
			if ch.Delta == nil {
				continue
			}
			text := extractOpenAIContent(ch.Delta.Content)
			reasoning := ch.Delta.ReasoningContent
			if reasoning == "" {
				reasoning = ch.Delta.Reasoning
			}
			if text != "" {
				out.Text += text
				out.HasContent = true
				noteToken(out, start, len(text))
			}
			if reasoning != "" {
				out.Reasoning += reasoning
				out.HasContent = true
				noteToken(out, start, len(reasoning))
			}
			if rawPresent(ch.Delta.ToolCalls) {
				out.HasContent = true
			}
		}
		return nil
	})
	if err == io.EOF {
		return hasPayload, nil
	}
	if err != nil {
		return hasPayload, err
	}
	_ = done
	return hasPayload, nil
}

// noteToken records TTFT and the last token timestamp.
func noteToken(out *Outcome, start time.Time, byteLen int) {
	now := time.Now()
	if out.firstTokenAt.IsZero() {
		out.firstTokenAt = now
		out.TTFTMs = float64(now.Sub(start).Microseconds()) / 1000.0
	}
	out.lastTokenAt = now
	out.TextChunks++
	out.TextBytes += byteLen
}

func parseJSONPayload(p model.Protocol, data []byte, out *Outcome) (bool, error) {
	switch p {
	case model.ProtocolClaude:
		return parseClaudeJSON(data, out)
	case model.ProtocolGemini:
		return parseGeminiJSON(data, out)
	default:
		return parseOpenAIJSON(data, out)
	}
}

const maxStreamCapture = 1 << 20

func parseStream(p model.Protocol, r io.Reader, out *Outcome, start time.Time) (bool, error) {
	cap := newCapture(maxStreamCapture)
	var has bool
	var err error
	switch p {
	case model.ProtocolClaude:
		has, err = parseClaudeStream(r, out, start, cap)
	case model.ProtocolGemini:
		has, err = parseGeminiStream(r, out, start, cap)
	default:
		has, err = parseOpenAIStream(r, out, start, cap)
	}
	out.ResponseJSON = cap.String()
	out.Truncated = cap.trunc
	if !out.firstTokenAt.IsZero() {
		out.GenerationMs = float64(out.lastTokenAt.Sub(out.firstTokenAt).Microseconds()) / 1000.0
	}
	return has, err
}
