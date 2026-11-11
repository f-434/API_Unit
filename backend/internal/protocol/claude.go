package protocol

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

func buildClaudeBody(req ChatRequest) ([]byte, error) {
	var system strings.Builder
	msgs := make([]map[string]any, 0, len(req.Messages))
	for _, m := range req.Messages {
		role := strings.ToLower(m.Role)
		switch role {
		case "system", "developer":
			if system.Len() > 0 {
				system.WriteString("\n\n")
			}
			system.WriteString(MessageText(m))
		case "assistant":
			msgs = append(msgs, map[string]any{"role": "assistant", "content": claudeBlocks(m, true)})
		case "tool", "function":
			blocks := []map[string]any{{
				"type": "tool_result", "tool_use_id": m.ToolCallID,
				"content": MessageText(m),
			}}
			msgs = append(msgs, map[string]any{"role": "user", "content": blocks})
		default:
			msgs = append(msgs, map[string]any{"role": "user", "content": claudeBlocks(m, false)})
		}
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 256
	}
	body := map[string]any{
		"model":      req.Model,
		"messages":   msgs,
		"max_tokens": maxTokens,
	}
	if system.Len() > 0 {
		body["system"] = system.String()
	}
	if req.Stream {
		body["stream"] = true
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		body["top_p"] = *req.TopP
	}
	if req.TopK != nil {
		body["top_k"] = *req.TopK
	}
	if len(req.Stop) > 0 {
		body["stop_sequences"] = req.Stop
	}
	if req.ThinkingBudget != nil {
		body["thinking"] = map[string]any{"type": "enabled", "budget_tokens": *req.ThinkingBudget}
	}
	if len(req.Tools) > 0 || len(req.BuiltinTools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools)+len(req.BuiltinTools))
		for _, t := range req.Tools {
			tool := map[string]any{"name": t.Name}
			if t.Description != "" {
				tool["description"] = t.Description
			}
			if t.Parameters != nil {
				tool["input_schema"] = t.Parameters
			}
			tools = append(tools, tool)
		}
		for _, b := range req.BuiltinTools {
			if b == "web_search" || b == "web_search_preview" {
				tools = append(tools, map[string]any{"type": "web_search_20250305", "name": "web_search"})
			}
		}
		body["tools"] = tools
	}
	if req.ToolChoice != "" {
		switch req.ToolChoice {
		case "auto":
			body["tool_choice"] = map[string]any{"type": "auto"}
		case "none":
			body["tool_choice"] = map[string]any{"type": "none"}
		case "required":
			body["tool_choice"] = map[string]any{"type": "any"}
		default:
			body["tool_choice"] = map[string]any{"type": "tool", "name": req.ToolChoice}
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

func claudeBlocks(m ChatMessage, assistant bool) any {
	text := m.Content
	if len(m.Parts) == 0 && len(m.ToolCalls) == 0 {
		return text
	}
	blocks := make([]map[string]any, 0, len(m.Parts)+len(m.ToolCalls)+1)
	if text != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": text})
	}
	for _, p := range m.Parts {
		switch strings.ToLower(p.Type) {
		case "text":
			blocks = append(blocks, map[string]any{"type": "text", "text": p.Text})
		case "image", "image_url":
			src := map[string]any{}
			if p.Data != "" {
				mime := p.MimeType
				if mime == "" {
					mime = "image/png"
				}
				src = map[string]any{"type": "base64", "media_type": mime, "data": p.Data}
			} else {
				src = map[string]any{"type": "url", "url": p.URL}
			}
			blocks = append(blocks, map[string]any{"type": "image", "source": src})
		default:
			// Claude supports documents; keep the raw payload so request
			// snapshots capture external data faithfully.
			mime := p.MimeType
			if mime == "" {
				mime = "application/octet-stream"
			}
			src := map[string]any{"type": "base64", "media_type": mime, "data": p.Data}
			if p.Data == "" {
				src = map[string]any{"type": "url", "url": p.URL}
			}
			blocks = append(blocks, map[string]any{"type": "document", "source": src})
		}
	}
	for _, c := range m.ToolCalls {
		input := parseJSONValue(c.Arguments)
		blocks = append(blocks, map[string]any{"type": "tool_use", "id": c.ID, "name": c.Name, "input": input})
	}
	if len(blocks) == 0 {
		return ""
	}
	return blocks
}

func MessageText(m ChatMessage) string {
	if m.Content != "" {
		return m.Content
	}
	var b strings.Builder
	for _, p := range m.Parts {
		if p.Text != "" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

func parseJSONValue(raw string) any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]any{}
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return raw
	}
	return v
}

type claudeUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

func (u *claudeUsage) normalize() Usage {
	if u == nil {
		return Usage{}
	}
	prompt := u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
	total := prompt + u.OutputTokens
	return Usage{
		Prompt: prompt, Completion: u.OutputTokens, Total: total,
		Cached: u.CacheReadInputTokens, CacheWrite: u.CacheCreationInputTokens,
		Reported: true, PromptKnown: prompt > 0, CompKnown: u.OutputTokens > 0,
	}
}

type claudeError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type claudeContent struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Thinking string `json:"thinking"`
}

type claudeResponse struct {
	ID         string          `json:"id"`
	Model      string          `json:"model"`
	Type       string          `json:"type"`
	Content    []claudeContent `json:"content"`
	StopReason string          `json:"stop_reason"`
	Usage      *claudeUsage    `json:"usage"`
	Error      *claudeError    `json:"error"`
}

func parseClaudeJSON(data []byte, out *Outcome) (bool, error) {
	var r claudeResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return false, fmt.Errorf("Claude 响应解析失败: %w", err)
	}
	if r.Error != nil && r.Error.Message != "" {
		out.ErrorMessage = r.Error.Message
		out.ErrorCode = r.Error.Type
		return false, nil
	}
	out.ResponseID = r.ID
	out.UpstreamModel = r.Model
	hasPayload := false
	if r.Usage != nil {
		out.Usage = out.Usage.Merge(r.Usage.normalize())
		hasPayload = true
	}
	for _, c := range r.Content {
		hasPayload = true
		switch c.Type {
		case "text":
			if c.Text != "" {
				out.Text += c.Text
				out.HasContent = true
			}
		case "thinking":
			if c.Thinking != "" {
				out.Reasoning += c.Thinking
				out.HasContent = true
			}
		case "tool_use", "server_tool_use":
			out.HasContent = true
		}
	}
	out.FinishReason = r.StopReason
	if r.StopReason != "" {
		out.Terminal = true
	}
	return hasPayload, nil
}

type claudeStreamEvent struct {
	Type    string `json:"type"`
	Message *struct {
		ID    string       `json:"id"`
		Model string       `json:"model"`
		Usage *claudeUsage `json:"usage"`
	} `json:"message"`
	ContentBlock *claudeContent `json:"content_block"`
	Delta        *struct {
		Type       string `json:"type"`
		Text       string `json:"text"`
		Thinking   string `json:"thinking"`
		StopReason string `json:"stop_reason"`
	} `json:"delta"`
	Usage      *claudeUsage `json:"usage"`
	StopReason string       `json:"stop_reason"`
	Error      *claudeError `json:"error"`
}

func parseClaudeStream(r io.Reader, out *Outcome, start time.Time, cap *capture) (bool, error) {
	hasPayload := false
	err := readSSE(r, func(ev SSEEvent) error {
		data := strings.TrimSpace(ev.Data)
		if data == "" {
			return nil
		}
		cap.Add(data + "\n")
		var e claudeStreamEvent
		if err := json.Unmarshal([]byte(data), &e); err != nil {
			out.ChunkCount++
			out.Malformed++
			return nil
		}
		out.ChunkCount++
		typ := e.Type
		if typ == "" {
			typ = ev.Name
		}
		if e.Error != nil && e.Error.Message != "" {
			out.ErrorMessage = e.Error.Message
			out.ErrorCode = e.Error.Type
			out.StreamError = true
			return io.EOF
		}
		switch typ {
		case "message_start":
			hasPayload = true
			if e.Message != nil {
				out.ResponseID = e.Message.ID
				out.UpstreamModel = e.Message.Model
				if e.Message.Usage != nil {
					out.Usage = out.Usage.Merge(e.Message.Usage.normalize())
				}
			}
		case "content_block_start":
			hasPayload = true
			if e.ContentBlock != nil {
				switch e.ContentBlock.Type {
				case "text":
					if e.ContentBlock.Text != "" {
						out.Text += e.ContentBlock.Text
						out.HasContent = true
						noteToken(out, start, len(e.ContentBlock.Text))
					}
				case "thinking":
					if e.ContentBlock.Thinking != "" {
						out.Reasoning += e.ContentBlock.Thinking
						out.HasContent = true
						noteToken(out, start, len(e.ContentBlock.Thinking))
					}
				case "tool_use", "server_tool_use":
					out.HasContent = true
				}
			}
		case "content_block_delta":
			hasPayload = true
			if e.Delta != nil {
				if e.Delta.Text != "" {
					out.Text += e.Delta.Text
					out.HasContent = true
					noteToken(out, start, len(e.Delta.Text))
				}
				if e.Delta.Thinking != "" {
					out.Reasoning += e.Delta.Thinking
					out.HasContent = true
					noteToken(out, start, len(e.Delta.Thinking))
				}
			}
		case "message_delta":
			hasPayload = true
			if e.Delta != nil {
				if e.Delta.StopReason != "" {
					out.FinishReason = e.Delta.StopReason
				}
				if e.Delta.Thinking != "" {
					out.Reasoning += e.Delta.Thinking
					noteToken(out, start, len(e.Delta.Thinking))
				}
			}
			if e.StopReason != "" {
				out.FinishReason = e.StopReason
			}
			if e.Usage != nil {
				out.Usage = out.Usage.Merge(e.Usage.normalize())
			}
		case "message_stop":
			hasPayload = true
			out.Terminal = true
		case "ping":
		default:
			if e.Usage != nil {
				out.Usage = out.Usage.Merge(e.Usage.normalize())
				hasPayload = true
			}
		}
		return nil
	})
	if err == io.EOF {
		return hasPayload, nil
	}
	return hasPayload, err
}
