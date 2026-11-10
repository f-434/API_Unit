package protocol

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

func buildGeminiBody(req ChatRequest) ([]byte, error) {
	contents := make([]map[string]any, 0, len(req.Messages))
	var systemParts []map[string]any
	for _, m := range req.Messages {
		role := strings.ToLower(m.Role)
		if role == "system" || role == "developer" {
			systemParts = append(systemParts, map[string]any{"text": MessageText(m)})
			continue
		}
		r := "user"
		switch role {
		case "assistant":
			r = "model"
		case "tool", "function":
			r = "user"
		}
		contents = append(contents, map[string]any{
			"role":  r,
			"parts": geminiParts(m),
		})
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 256
	}
	gen := map[string]any{"maxOutputTokens": maxTokens}
	if req.Temperature != nil {
		gen["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		gen["topP"] = *req.TopP
	}
	if req.TopK != nil {
		gen["topK"] = *req.TopK
	}
	if req.CandidateCount != nil {
		gen["candidateCount"] = *req.CandidateCount
	}
	if len(req.Stop) > 0 {
		gen["stopSequences"] = req.Stop
	}
	if req.Seed != nil {
		gen["seed"] = *req.Seed
	}
	if req.ResponseMimeType != "" {
		gen["responseMimeType"] = req.ResponseMimeType
	}
	if req.ResponseSchema != nil {
		gen["responseSchema"] = req.ResponseSchema
	}
	if req.PresencePenalty != nil {
		gen["presencePenalty"] = *req.PresencePenalty
	}
	if req.FrequencyPenalty != nil {
		gen["frequencyPenalty"] = *req.FrequencyPenalty
	}
	if req.ThinkingBudget != nil {
		gen["thinkingConfig"] = map[string]any{"thinkingBudget": *req.ThinkingBudget}
	}
	for k, v := range req.ExtraGeneration {
		gen[k] = v
	}
	body := map[string]any{
		"contents":         contents,
		"generationConfig": gen,
	}
	if len(systemParts) > 0 {
		body["systemInstruction"] = map[string]any{"parts": systemParts}
	}
	if len(req.Tools) > 0 || len(req.BuiltinTools) > 0 {
		tools := make([]map[string]any, 0, 2)
		if len(req.Tools) > 0 {
			decls := make([]map[string]any, 0, len(req.Tools))
			for _, t := range req.Tools {
				d := map[string]any{"name": t.Name}
				if t.Description != "" {
					d["description"] = t.Description
				}
				if t.Parameters != nil {
					d["parameters"] = t.Parameters
				}
				decls = append(decls, d)
			}
			tools = append(tools, map[string]any{"functionDeclarations": decls})
		}
		for _, b := range req.BuiltinTools {
			if b == "google_search" || b == "web_search" || b == "web_search_preview" {
				tools = append(tools, map[string]any{"googleSearch": map[string]any{}})
			}
		}
		body["tools"] = tools
	}
	if req.ToolChoice != "" {
		cfg := map[string]any{}
		switch req.ToolChoice {
		case "auto":
			cfg["mode"] = "AUTO"
		case "none":
			cfg["mode"] = "NONE"
		case "required":
			cfg["mode"] = "ANY"
		default:
			cfg["mode"] = "ANY"
			cfg["allowedFunctionNames"] = []string{req.ToolChoice}
		}
		body["toolConfig"] = map[string]any{"functionCallingConfig": cfg}
	}
	if len(req.SafetySettings) > 0 {
		settings := make([]map[string]any, 0, len(req.SafetySettings))
		for _, s := range req.SafetySettings {
			settings = append(settings, map[string]any{"category": s.Category, "threshold": s.Threshold})
		}
		body["safetySettings"] = settings
	}
	if req.CachedContent != "" {
		body["cachedContent"] = req.CachedContent
	}
	for k, v := range req.ExtraBody {
		body[k] = v
	}
	return json.Marshal(body)
}

func geminiParts(m ChatMessage) []map[string]any {
	parts := make([]map[string]any, 0, len(m.Parts)+len(m.ToolCalls)+1)
	isToolRole := strings.EqualFold(m.Role, "tool") || strings.EqualFold(m.Role, "function")
	if m.Content != "" && !isToolRole {
		parts = append(parts, map[string]any{"text": m.Content})
	}
	for _, p := range m.Parts {
		switch strings.ToLower(p.Type) {
		case "text":
			parts = append(parts, map[string]any{"text": p.Text})
		default:
			mime := p.MimeType
			if mime == "" {
				mime = mimeForPart(p.Type)
			}
			if p.Data != "" {
				parts = append(parts, map[string]any{"inlineData": map[string]any{"mimeType": mime, "data": p.Data}})
			} else {
				parts = append(parts, map[string]any{"fileData": map[string]any{"mimeType": mime, "fileUri": p.URL}})
			}
		}
	}
	for _, c := range m.ToolCalls {
		parts = append(parts, map[string]any{
			"functionCall": map[string]any{"name": c.Name, "args": parseJSONValue(c.Arguments)},
		})
	}
	if isToolRole {
		name := m.Name
		if name == "" {
			name = m.ToolCallID
		}
		var resp any = map[string]any{"result": MessageText(m)}
		if v := parseJSONValue(m.Content); v != nil {
			if _, ok := v.(map[string]any); ok {
				resp = v
			}
		}
		parts = append(parts, map[string]any{
			"functionResponse": map[string]any{"name": name, "response": resp},
		})
	}
	if len(parts) == 0 {
		parts = append(parts, map[string]any{"text": ""})
	}
	return parts
}

func mimeForPart(t string) string {
	switch strings.ToLower(t) {
	case "image", "image_url":
		return "image/png"
	case "audio", "input_audio":
		return "audio/wav"
	case "video", "video_url":
		return "video/mp4"
	default:
		return "application/octet-stream"
	}
}

type geminiPart struct {
	Text    string `json:"text"`
	Thought bool   `json:"thought"`
}

type geminiUsage struct {
	PromptTokenCount        int `json:"promptTokenCount"`
	CandidatesTokenCount    int `json:"candidatesTokenCount"`
	TotalTokenCount         int `json:"totalTokenCount"`
	CachedContentTokenCount int `json:"cachedContentTokenCount"`
	ThoughtsTokenCount      int `json:"thoughtsTokenCount"`
}

func (u *geminiUsage) normalize() Usage {
	if u == nil {
		return Usage{}
	}
	comp := u.CandidatesTokenCount + u.ThoughtsTokenCount
	total := u.TotalTokenCount
	if total == 0 {
		total = u.PromptTokenCount + comp
	}
	return Usage{
		Prompt: u.PromptTokenCount, Completion: comp, Total: total,
		Cached: u.CachedContentTokenCount,
		Reasoning: u.ThoughtsTokenCount,
		Reported: true, PromptKnown: u.PromptTokenCount > 0, CompKnown: comp > 0,
	}
}

type geminiResponse struct {
	ResponseID   string `json:"responseId"`
	ModelVersion string `json:"modelVersion"`
	Candidates   []struct {
		Content struct {
			Parts []geminiPart `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata  *geminiUsage `json:"usageMetadata"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
}

func (r *geminiResponse) consume(out *Outcome, start time.Time, streaming bool) bool {
	hasPayload := false
	if r.Error != nil && r.Error.Message != "" {
		out.ErrorMessage = r.Error.Message
		out.ErrorCode = r.Error.Status
		return false
	}
	if r.PromptFeedback != nil && r.PromptFeedback.BlockReason != "" {
		out.ErrorMessage = "请求被安全策略拦截: " + r.PromptFeedback.BlockReason
		out.ErrorCode = "prompt_blocked"
		return false
	}
	if r.ResponseID != "" {
		out.ResponseID = r.ResponseID
	}
	if r.ModelVersion != "" {
		out.UpstreamModel = r.ModelVersion
	}
	if r.UsageMetadata != nil {
		out.Usage = out.Usage.Merge(r.UsageMetadata.normalize())
		hasPayload = true
	}
	for _, c := range r.Candidates {
		hasPayload = true
		for _, p := range c.Content.Parts {
			if p.Text == "" {
				continue
			}
			if p.Thought {
				out.Reasoning += p.Text
			} else {
				out.Text += p.Text
			}
			if streaming {
				noteToken(out, start, len(p.Text))
			}
		}
		if c.FinishReason != "" {
			out.FinishReason = c.FinishReason
		}
	}
	return hasPayload
}

func parseGeminiJSON(data []byte, out *Outcome) (bool, error) {
	var r geminiResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return false, fmt.Errorf("Gemini 响应解析失败: %w", err)
	}
	has := r.consume(out, time.Now(), false)
	if out.ErrorMessage != "" {
		return false, nil
	}
	return has, nil
}

func parseGeminiStream(r io.Reader, out *Outcome, start time.Time, cap *capture) (bool, error) {
	hasPayload := false
	err := readSSE(r, func(ev SSEEvent) error {
		data := strings.TrimSpace(ev.Data)
		if data == "" || data == "[DONE]" {
			if data == "[DONE]" {
				return io.EOF
			}
			return nil
		}
		cap.Add(data + "\n")
		var chunk geminiResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			out.ChunkCount++
			hasPayload = true
			return nil
		}
		out.ChunkCount++
		if chunk.consume(out, start, true) {
			hasPayload = true
		}
		if out.ErrorMessage != "" {
			out.StreamError = true
			return io.EOF
		}
		return nil
	})
	if err == io.EOF {
		return hasPayload, nil
	}
	return hasPayload, err
}
