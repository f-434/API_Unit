package authenticity

import (
	"fmt"
	"strings"
)

type usageInfo struct {
	Prompt     int
	Completion int
	Total      int
	Cached     int
	CacheWrite int
	Reasoning  int
	Reported   bool
}

type openAIToolCall struct {
	ID        string
	Name      string
	Arguments string
}

type openAIChatResult struct {
	ID           string
	Model        string
	Content      string
	Reasoning    string
	ToolCalls    []openAIToolCall
	FinishReason string
	Usage        usageInfo
	Raw          map[string]any
}

type claudeToolUse struct {
	ID    string
	Name  string
	Input any
}

type claudeResult struct {
	ID         string
	Model      string
	Content    []any
	Text       string
	ToolUses   []claudeToolUse
	Thinking   []string
	Signatures []string
	StopReason string
	Usage      usageInfo
	Raw        map[string]any
}

type geminiFunctionCall struct {
	Name      string
	Args      any
	Signature string
}

type geminiResult struct {
	ModelVersion      string
	ResponseID        string
	Text              string
	FunctionCalls     []geminiFunctionCall
	ThoughtParts      int
	ThoughtSignatures []string
	FinishReason      string
	Usage             usageInfo
	Grounding         []any
	WebQueries        []string
	Raw               map[string]any
}

type responseCall struct {
	Type      string
	Name      string
	Arguments string
	Output    string
	Sources   []string
}

type responsesResult struct {
	ID         string
	Model      string
	Status     string
	OutputText string
	MCPCalls   []responseCall
	WebCalls   []responseCall
	Reasoning  int
	Usage      usageInfo
	Raw        map[string]any
}

func parseOpenAIChat(raw []byte) openAIChatResult {
	out := openAIChatResult{Raw: decodeObject(raw)}
	if out.Raw == nil {
		return out
	}
	out.ID = getString(out.Raw, "id")
	out.Model = getString(out.Raw, "model")
	choice := firstMap(getSlice(out.Raw, "choices"))
	if choice != nil {
		out.FinishReason = getString(choice, "finish_reason")
		msg := getMap(choice, "message")
		if msg != nil {
			out.Content = contentText(msg["content"])
			out.Reasoning = firstString(msg, "reasoning_content", "reasoning")
			for _, item := range getSlice(msg, "tool_calls") {
				call, ok := item.(map[string]any)
				if !ok {
					continue
				}
				fn := getMap(call, "function")
				if fn == nil {
					continue
				}
				out.ToolCalls = append(out.ToolCalls, openAIToolCall{
					ID:        getString(call, "id"),
					Name:      getString(fn, "name"),
					Arguments: getString(fn, "arguments"),
				})
			}
			// Legacy function_call (single call) used by some compatible gateways.
			if len(out.ToolCalls) == 0 {
				if fc := getMap(msg, "function_call"); fc != nil {
					out.ToolCalls = append(out.ToolCalls, openAIToolCall{
						Name:      getString(fc, "name"),
						Arguments: getString(fc, "arguments"),
					})
				}
			}
		}
	}
	u := getMap(out.Raw, "usage")
	if u != nil {
		out.Usage = usageInfo{
			Prompt:     getInt(u, "prompt_tokens"),
			Completion: getInt(u, "completion_tokens"),
			Total:      getInt(u, "total_tokens"),
			Cached:     getInt(u, "prompt_tokens_details", "cached_tokens"),
			Reasoning:  getInt(u, "completion_tokens_details", "reasoning_tokens"),
			Reported:   true,
		}
	}
	return out
}

func parseClaude(raw []byte) claudeResult {
	out := claudeResult{Raw: decodeObject(raw)}
	if out.Raw == nil {
		return out
	}
	out.ID = getString(out.Raw, "id")
	out.Model = getString(out.Raw, "model")
	out.StopReason = getString(out.Raw, "stop_reason")
	for _, item := range getSlice(out.Raw, "content") {
		block, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out.Content = append(out.Content, block)
		switch getString(block, "type") {
		case "text":
			out.Text += getString(block, "text")
		case "tool_use":
			out.ToolUses = append(out.ToolUses, claudeToolUse{
				ID:    getString(block, "id"),
				Name:  getString(block, "name"),
				Input: block["input"],
			})
		case "thinking":
			if s := getString(block, "thinking"); s != "" {
				out.Thinking = append(out.Thinking, s)
			}
			if s := getString(block, "signature"); s != "" {
				out.Signatures = append(out.Signatures, s)
			}
		}
	}
	u := getMap(out.Raw, "usage")
	if u != nil {
		out.Usage = usageInfo{
			Prompt:     getInt(u, "input_tokens"),
			Completion: getInt(u, "output_tokens"),
			Total:      getInt(u, "input_tokens") + getInt(u, "output_tokens"),
			Cached:     getInt(u, "cache_read_input_tokens"),
			CacheWrite: getInt(u, "cache_creation_input_tokens"),
			Reported:   true,
		}
	}
	return out
}

func parseGemini(raw []byte) geminiResult {
	out := geminiResult{Raw: decodeObject(raw)}
	if out.Raw == nil {
		return out
	}
	out.ModelVersion = getString(out.Raw, "modelVersion")
	out.ResponseID = getString(out.Raw, "responseId")
	cand := firstMap(getSlice(out.Raw, "candidates"))
	if cand != nil {
		out.FinishReason = getString(cand, "finishReason")
		content := getMap(cand, "content")
		for _, item := range getSlice(content, "parts") {
			part, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if thought, _ := part["thought"].(bool); thought {
				out.ThoughtParts++
			}
			if s := getString(part, "text"); s != "" {
				if thought, _ := part["thought"].(bool); !thought {
					out.Text += s
				}
			}
			if s := getString(part, "thoughtSignature"); s != "" {
				out.ThoughtSignatures = append(out.ThoughtSignatures, s)
			}
			if fc := getMap(part, "functionCall"); fc != nil {
				out.FunctionCalls = append(out.FunctionCalls, geminiFunctionCall{
					Name:      getString(fc, "name"),
					Args:      fc["args"],
					Signature: getString(part, "thoughtSignature"),
				})
			}
		}
		gm := getMap(cand, "groundingMetadata")
		if gm != nil {
			out.Grounding = append(out.Grounding, gm)
			for _, q := range getSlice(gm, "webSearchQueries") {
				if s, ok := q.(string); ok {
					out.WebQueries = append(out.WebQueries, s)
				}
			}
		}
	}
	u := getMap(out.Raw, "usageMetadata")
	if u != nil {
		out.Usage = usageInfo{
			Prompt:     getInt(u, "promptTokenCount"),
			Completion: getInt(u, "candidatesTokenCount"),
			Total:      getInt(u, "totalTokenCount"),
			Cached:     getInt(u, "cachedContentTokenCount"),
			Reasoning:  getInt(u, "thoughtsTokenCount"),
			Reported:   true,
		}
	}
	return out
}

func parseResponses(raw []byte) responsesResult {
	out := responsesResult{Raw: decodeObject(raw)}
	if out.Raw == nil {
		return out
	}
	out.ID = getString(out.Raw, "id")
	out.Model = getString(out.Raw, "model")
	out.Status = getString(out.Raw, "status")
	out.OutputText = getString(out.Raw, "output_text")
	for _, item := range getSlice(out.Raw, "output") {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		typ := getString(entry, "type")
		switch typ {
		case "message":
			for _, c := range getSlice(entry, "content") {
				part, ok := c.(map[string]any)
				if !ok {
					continue
				}
				if getString(part, "type") == "output_text" {
					out.OutputText += getString(part, "text")
				}
			}
		case "mcp_call":
			call := responseCall{
				Type:      typ,
				Name:      getString(entry, "name"),
				Arguments: getString(entry, "arguments"),
				Output:    getString(entry, "output"),
			}
			if call.Output == "" {
				call.Output = jsonPretty(entry["output"])
			}
			out.MCPCalls = append(out.MCPCalls, call)
		case "web_search_call":
			call := responseCall{Type: typ}
			action := getMap(entry, "action")
			for _, s := range getSlice(action, "sources") {
				if src, ok := s.(map[string]any); ok {
					if u := getString(src, "url"); u != "" {
						call.Sources = append(call.Sources, u)
					}
				}
			}
			out.WebCalls = append(out.WebCalls, call)
		case "reasoning":
			out.Reasoning++
		}
	}
	u := getMap(out.Raw, "usage")
	if u != nil {
		out.Usage = usageInfo{
			Prompt:     getInt(u, "input_tokens"),
			Completion: getInt(u, "output_tokens"),
			Total:      getInt(u, "total_tokens"),
			Cached:     getInt(u, "input_tokens_details", "cached_tokens"),
			Reasoning:  getInt(u, "output_tokens_details", "reasoning_tokens"),
			Reported:   true,
		}
	}
	return out
}

func firstMap(list []any) map[string]any {
	for _, item := range list {
		if m, ok := item.(map[string]any); ok {
			return m
		}
	}
	return nil
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func contentText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		var b strings.Builder
		for _, item := range x {
			if part, ok := item.(map[string]any); ok {
				b.WriteString(getString(part, "text"))
			}
		}
		return b.String()
	}
	return ""
}

// ---------- request builders ----------

func toolDefinition(cfg ChallengeConfig) map[string]any {
	return map[string]any{
		"name":        cfg.ToolName,
		"description": "读取本次真实性校验的证明记录。必须传入挑战码 challenge，返回 secret、a、b。",
		"parameters": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"challenge": map[string]any{
					"type":        "string",
					"description": "本次校验的挑战码（原样传入）",
				},
			},
			"required":             []string{"challenge"},
			"additionalProperties": false,
		},
	}
}

func round1Prompt(cfg ChallengeConfig, nonce string) string {
	if strings.TrimSpace(cfg.Round1Prompt) != "" {
		return strings.ReplaceAll(cfg.Round1Prompt, "{{nonce}}", nonce)
	}
	return fmt.Sprintf(
		"你正在参加一次接口能力校验。必须调用工具 %q，参数 challenge 必须严格等于 %q。"+
			"不要直接回答，不要省略工具调用；调用后等待工具返回结果。",
		cfg.ToolName, nonce)
}

func round2Prompt(cfg ChallengeConfig, nonce string) string {
	if strings.TrimSpace(cfg.Round2Prompt) != "" {
		return strings.ReplaceAll(cfg.Round2Prompt, "{{nonce}}", nonce)
	}
	return fmt.Sprintf(
		"工具已经返回结果。请只输出一个 JSON 对象，不要 Markdown，不要额外说明："+
			`{"challenge":%q,"secret":"<工具返回的 secret>","sum":<a+b 的结果>}`,
		nonce)
}

func systemPrompt(cfg ChallengeConfig) string {
	if strings.TrimSpace(cfg.SystemPrompt) != "" {
		return cfg.SystemPrompt
	}
	return "严格遵守校验指令；不得编造工具结果，不得省略工具调用。最终输出必须是合法 JSON。"
}

func openAIChatBody(modelName string, messages []any, cfg ChallengeConfig, tools bool, force bool, thinking bool) map[string]any {
	body := map[string]any{
		"model":       modelName,
		"messages":    messages,
		"max_tokens":  cfg.MaxTokens,
		"temperature": *cfg.Temperature,
	}
	if isOpenAIReasoningName(modelName) {
		delete(body, "max_tokens")
		delete(body, "temperature")
		body["max_completion_tokens"] = cfg.MaxTokens
		if thinking {
			body["reasoning_effort"] = "medium"
		}
	}
	if tools {
		td := toolDefinition(cfg)
		body["tools"] = []any{map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        td["name"],
				"description": td["description"],
				"parameters":  td["parameters"],
			},
		}}
		if force {
			body["tool_choice"] = map[string]any{
				"type":     "function",
				"function": map[string]any{"name": cfg.ToolName},
			}
		} else {
			body["tool_choice"] = "auto"
		}
	}
	return body
}

func claudeBody(modelName string, messages []any, system string, cfg ChallengeConfig, tools bool, force bool, thinking bool) map[string]any {
	body := map[string]any{
		"model":       modelName,
		"max_tokens":  cfg.MaxTokens,
		"messages":    messages,
		"temperature": *cfg.Temperature,
	}
	if system != "" {
		body["system"] = system
	}
	if tools {
		td := toolDefinition(cfg)
		body["tools"] = []any{map[string]any{
			"name":         td["name"],
			"description":  td["description"],
			"input_schema": td["parameters"],
		}}
		if force {
			body["tool_choice"] = map[string]any{"type": "tool", "name": cfg.ToolName}
		} else {
			body["tool_choice"] = map[string]any{"type": "auto"}
		}
	}
	if thinking {
		if claudeAdaptive(modelName) {
			body["thinking"] = map[string]any{"type": "adaptive"}
		} else if !strings.HasPrefix(normalizeModel(modelName), "claude-3") {
			budget := 1024
			if budget >= cfg.MaxTokens {
				budget = cfg.MaxTokens / 2
			}
			if budget > 0 {
				body["thinking"] = map[string]any{"type": "enabled", "budget_tokens": budget}
			}
		}
	}
	return body
}

func geminiBody(modelName string, contents []any, cfg ChallengeConfig, tools bool, force bool, thinking bool) map[string]any {
	gen := map[string]any{
		"maxOutputTokens": cfg.MaxTokens,
		"temperature":     *cfg.Temperature,
	}
	if thinking {
		if geminiMajor(modelName) >= 3 || geminiMajor(modelName) == 0 {
			gen["thinkingConfig"] = map[string]any{"thinkingLevel": "HIGH"}
		} else if strings.HasPrefix(strings.TrimPrefix(normalizeModel(modelName), "gemini-"), "2.5") {
			gen["thinkingConfig"] = map[string]any{"thinkingBudget": 1024}
		}
	}
	body := map[string]any{
		"contents":         contents,
		"generationConfig": gen,
	}
	if tools {
		td := toolDefinition(cfg)
		body["tools"] = []any{map[string]any{
			"functionDeclarations": []any{map[string]any{
				"name":        td["name"],
				"description": td["description"],
				"parameters":  td["parameters"],
			}},
		}}
		cfgMap := map[string]any{"mode": "AUTO"}
		if force {
			cfgMap = map[string]any{"mode": "ANY", "allowedFunctionNames": []string{cfg.ToolName}}
		}
		body["toolConfig"] = map[string]any{"functionCallingConfig": cfgMap}
	}
	return body
}

func isOpenAIReasoningName(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	for _, prefix := range []string{"o1", "o3", "o4", "gpt-5", "gpt-6", "gpt-7", "reasoning"} {
		if strings.HasPrefix(n, prefix) || strings.Contains(n, "/"+prefix) {
			return true
		}
	}
	return false
}

func claudeAdaptive(name string) bool {
	n := normalizeModel(name)
	for _, part := range []string{"fable", "mythos", "opus-5", "sonnet-5", "haiku-5", "opus-4-6", "opus-4-7", "opus-4-8", "sonnet-4-6"} {
		if strings.Contains(n, part) {
			return true
		}
	}
	return false
}

func geminiMajor(name string) int {
	n := normalizeModel(name)
	rest := strings.TrimPrefix(n, "gemini-")
	if rest == n {
		return 0
	}
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0
	}
	nval := 0
	for i := 0; i < end; i++ {
		nval = nval*10 + int(rest[i]-'0')
	}
	return nval
}

func normalizeModel(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.TrimPrefix(s, "models/")
	for _, p := range []string{"openai/", "azure/", "google/", "anthropic/", "anthropic."} {
		s = strings.TrimPrefix(s, p)
	}
	return s
}

func modelMatches(actual, expected string) bool {
	a := normalizeModel(actual)
	e := normalizeModel(expected)
	if a == "" || e == "" {
		return false
	}
	if a == e {
		return true
	}
	if len(a) > len(e) && strings.HasPrefix(a, e) {
		switch a[len(e)] {
		case '-', '@', ':', '/', '.':
			return true
		}
	}
	if len(e) > len(a) && strings.HasPrefix(e, a) {
		switch e[len(a)] {
		case '-', '@', ':', '/', '.':
			return true
		}
	}
	return false
}
