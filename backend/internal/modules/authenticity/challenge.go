package authenticity

import (
	"encoding/json"
	"fmt"
	"strings"

	"apiunit/internal/model"
	"apiunit/internal/protocol"
)

type toolCallInfo struct {
	ID       string
	Name     string
	ArgsRaw  string
	Args     map[string]any
	Round1   any
	RawBlock any
}

func (r *modelRun) runChallenge() {
	r.stage("能力挑战：第 1 轮工具调用")
	nonce := r.challenge.Nonce
	thinking := r.cfg.Checks.Identity
	round1 := round1Prompt(r.cfg.Challenge, nonce)
	system := systemPrompt(r.cfg.Challenge)

	var (
		tool     toolCallInfo
		res      httpResult
		parsedOK bool
		fallback string
	)

	switch r.protocolName {
	case model.ProtocolClaude:
		tool, res, parsedOK, fallback = r.challengeClaudeRound1(system, round1, thinking)
	case model.ProtocolGemini:
		tool, res, parsedOK, fallback = r.challengeGeminiRound1(system, round1, thinking)
	default:
		tool, res, parsedOK, fallback = r.challengeOpenAIRound1(system, round1, thinking)
	}

	connectStatus := StatusPass
	connectEvidence := fmt.Sprintf("HTTP %d", res.Status)
	if res.Err != nil {
		connectStatus = StatusFail
		connectEvidence = "网络错误: " + res.Err.Error()
		r.setRunError(connectEvidence)
	} else if !res.statusOK() {
		connectStatus = StatusFail
		connectEvidence += " " + truncateText(strings.TrimSpace(string(res.Body)), 300)
	}
	checks1 := []CheckReport{{
		ID: "P0-1", Group: GroupConsistency, Name: "鉴权与端点可达",
		Status: connectStatus, Weight: WeightHard, Evidence: connectEvidence, CaseID: "cap.turn1",
		DurationMs: int64(res.LatencyMs),
	}}
	toolEmitted := tool.Name != ""
	nameOK := strings.EqualFold(tool.Name, r.cfg.Challenge.ToolName)
	argsOK := false
	if tool.Args != nil {
		if v, ok := tool.Args["challenge"].(string); ok && v == nonce {
			argsOK = true
		}
	}
	finishOK := toolEmitted && finishIsToolUse(r.protocolName, r.lastFinish)
	checks1 = append(checks1,
		CheckReport{
			ID: "P3-1", Group: GroupCapability, Name: "第 1 轮发出工具调用",
			Status: boolStatus(toolEmitted, "未产生工具调用", fmt.Sprintf("工具调用 %d 个", 1)),
			Weight: WeightCapability, Evidence: evidenceForTool(tool, parsedOK, fallback), CaseID: "cap.turn1",
		},
		CheckReport{
			ID: "P3-2", Group: GroupCapability, Name: "函数名与定义一致",
			Status: boolStatus(nameOK, "实际工具名="+tool.Name, "工具名="+tool.Name),
			Weight: WeightCapability, Evidence: "期望=" + r.cfg.Challenge.ToolName + "，实际=" + tool.Name, CaseID: "cap.turn1",
		},
		CheckReport{
			ID: "P3-3", Group: GroupCapability, Name: "参数合法且包含 challenge",
			Status: boolStatus(argsOK, "参数未包含正确的 challenge", "challenge 精确匹配"),
			Weight: WeightCapability, Evidence: "args=" + truncateText(tool.ArgsRaw, 300), CaseID: "cap.turn1",
		},
		CheckReport{
			ID: "P3-4", Group: GroupCapability, Name: "结束原因符合工具调用",
			Status: boolStatus(finishOK, "finish="+r.lastFinish, "finish="+r.lastFinish),
			Weight: WeightMetadata, Evidence: "finish=" + r.lastFinish, CaseID: "cap.turn1",
		},
	)
	if toolsUnsupported(res) {
		for i := range checks1 {
			if checks1[i].Group == GroupCapability {
				checks1[i].Status = StatusNA
				checks1[i].Evidence = "上游明确声明不支持工具调用，能力维度无法判定: " + truncateText(strings.TrimSpace(string(res.Body)), 240)
			}
		}
		r.add(checks1...)
		r.record(res, "cap.turn1", "能力挑战：上游不支持工具", GroupCapability, r.lastText, checks1...)
		return
	}
	if res.Status == 401 || res.Status == 403 || res.Status == 404 || res.Status == 429 || res.Status >= 500 {
		r.setRunError(fmt.Sprintf("上游返回 HTTP %d，无法完成能力判定: %s", res.Status,
			truncateText(strings.TrimSpace(string(res.Body)), 240)))
	}
	r.add(checks1...)
	r.record(res, "cap.turn1", "能力挑战：第 1 轮工具调用", GroupCapability, r.lastText, checks1...)

	if !toolEmitted || !nameOK || !argsOK {
		r.stage("能力挑战：第 1 轮未通过")
		na := func(id, name, evidence string) CheckReport {
			return CheckReport{ID: id, Group: GroupCapability, Name: name, Status: StatusNA, Weight: WeightCapability, Evidence: evidence, CaseID: "cap.turn1"}
		}
		checks := []CheckReport{
			{ID: "P3-11", Group: GroupCapability, Name: "两轮工具回环完成", Status: StatusFail, Weight: WeightCapability,
				Evidence: "第 1 轮未完成有效工具调用，第 2 轮未执行", CaseID: "cap.turn1"},
			na("P3-5", "第 2 轮请求成功", "第 1 轮失败，未执行"),
			na("P3-6", "最终输出可解析为 JSON", "第 1 轮失败，未执行"),
			na("P3-7", "challenge 精确回显", "第 1 轮失败，未执行"),
			na("P3-8", "secret 精确回显", "第 1 轮失败，未执行"),
			na("P3-9", "计算结果精确", "第 1 轮失败，未执行"),
			na("P3-10", "输出严格为 JSON", "第 1 轮失败，未执行"),
		}
		r.add(checks...)
		return
	}

	r.stage("能力挑战：第 2 轮使用工具结果")
	round1Used := r.challengePrompt
	if round1Used == "" {
		round1Used = round1
	}
	round2 := round2Prompt(r.cfg.Challenge, nonce)
	toolResult := map[string]any{
		"secret":    r.challenge.Secret,
		"a":         r.challenge.A,
		"b":         r.challenge.B,
		"challenge": nonce,
	}
	var res2 httpResult
	switch r.protocolName {
	case model.ProtocolClaude:
		res2 = r.challengeClaudeRound2(system, round1Used, round2, tool, toolResult)
	case model.ProtocolGemini:
		res2 = r.challengeGeminiRound2(round1Used, round2, tool, toolResult)
	default:
		res2 = r.challengeOpenAIRound2(system, round1Used, round2, tool, toolResult)
	}

	final, strict, jsonErr := decodeLooseJSON(r.lastText)
	httpOK := res2.Err == nil && res2.statusOK()
	challengeEcho := false
	secretEcho := false
	sumOK := false
	if final != nil {
		if v, ok := final["challenge"].(string); ok && v == nonce {
			challengeEcho = true
		}
		if v, ok := final["secret"].(string); ok && v == r.challenge.Secret {
			secretEcho = true
		}
		switch v := final["sum"].(type) {
		case float64:
			sumOK = int(v) == r.challenge.Sum
		case json.Number:
			n, _ := v.Int64()
			sumOK = int(n) == r.challenge.Sum
		case string:
			sumOK = strings.TrimSpace(v) == fmt.Sprintf("%d", r.challenge.Sum)
		}
	}
	checks2 := []CheckReport{
		{
			ID: "P3-5", Group: GroupCapability, Name: "第 2 轮请求成功",
			Status: boolStatus(httpOK, fmt.Sprintf("HTTP %d", res2.Status), fmt.Sprintf("HTTP %d", res2.Status)),
			Weight: WeightCapability, Evidence: httpEvidence(res2), CaseID: "cap.turn2",
			DurationMs: int64(res2.LatencyMs),
		},
		{
			ID: "P3-6", Group: GroupCapability, Name: "最终输出可解析为 JSON",
			Status: boolStatus(final != nil, errText(jsonErr), "解析成功"),
			Weight: WeightCapability, Evidence: truncateText(r.lastText, 300), CaseID: "cap.turn2",
		},
		{
			ID: "P3-7", Group: GroupCapability, Name: "challenge 精确回显",
			Status: boolStatus(challengeEcho, "未回显本次 challenge", "challenge="+nonce),
			Weight: WeightCapability, Evidence: "期望=" + nonce + "，实际=" + finalString(final, "challenge"), CaseID: "cap.turn2",
		},
		{
			ID: "P3-8", Group: GroupCapability, Name: "secret 精确回显（防回放）",
			Status: boolStatus(secretEcho, "secret 不一致，可能未读取工具结果或回放旧答案", "secret 与工具结果一致"),
			Weight: WeightCapability, Evidence: "期望=" + r.challenge.Secret + "，实际=" + finalString(final, "secret"), CaseID: "cap.turn2",
		},
		{
			ID: "P3-9", Group: GroupCapability, Name: "计算结果精确",
			Status: boolStatus(sumOK, "sum 不一致", fmt.Sprintf("sum=%d", r.challenge.Sum)),
			Weight: WeightCapability, Evidence: fmt.Sprintf("期望=%d，实际=%v", r.challenge.Sum, finalValue(final, "sum")), CaseID: "cap.turn2",
		},
		{
			ID: "P3-10", Group: GroupCapability, Name: "输出严格为 JSON（无 Markdown/多余文本）",
			Status: boolStatus(strict, "输出含 Markdown 或额外文本", "严格 JSON"),
			Weight: WeightMetadata, Evidence: truncateText(r.lastText, 200), CaseID: "cap.turn2",
		},
	}
	loopOK := httpOK && final != nil && challengeEcho && secretEcho && sumOK
	checks2 = append(checks2, CheckReport{
		ID: "P3-11", Group: GroupCapability, Name: "两轮工具回环完成",
		Status: boolStatus(loopOK, "两轮工具回环未完整通过", "两轮工具回环通过"),
		Weight: WeightCapability, Evidence: fmt.Sprintf("http=%t json=%t challenge=%t secret=%t sum=%t", httpOK, final != nil, challengeEcho, secretEcho, sumOK),
	})
	r.add(checks2...)
	r.record(res2, "cap.turn2", "能力挑战：第 2 轮使用工具结果", GroupCapability, r.lastText, checks2...)
}

func (r *modelRun) challengeOpenAIRound1(system, prompt string, thinking bool) (toolCallInfo, httpResult, bool, string) {
	r.challengePrompt = prompt
	messages := []any{
		map[string]any{"role": "system", "content": system},
		map[string]any{"role": "user", "content": prompt},
	}
	fallback := ""
	res := r.postWithRetry(r.ctx, r.endpoint, r.headers, openAIChatBody(r.job.model, messages, r.cfg.Challenge, true, r.cfg.Challenge.ForceTool, thinking), r.timeout())
	parsed := parseOpenAIChat(res.Body)
	if res.Status == 400 && r.cfg.Challenge.ForceTool && retryableToolChoice(res.Body) {
		fallback = "tool_choice 被上游拒绝，已回退 auto"
		res = r.postWithRetry(r.ctx, r.endpoint, r.headers, openAIChatBody(r.job.model, messages, r.cfg.Challenge, true, false, thinking), r.timeout())
		parsed = parseOpenAIChat(res.Body)
	}
	if res.Err == nil && res.statusOK() && len(parsed.ToolCalls) == 0 {
		retry := retryChallengePrompt(r.cfg.Challenge, r.challenge.Nonce)
		r.challengePrompt = retry
		fallback = appendFallback(fallback, "未产生工具调用，已用强化提示重试一次")
		messages = []any{
			map[string]any{"role": "system", "content": system},
			map[string]any{"role": "user", "content": retry},
		}
		res = r.postWithRetry(r.ctx, r.endpoint, r.headers, openAIChatBody(r.job.model, messages, r.cfg.Challenge, true, false, false), r.timeout())
		parsed = parseOpenAIChat(res.Body)
	}
	r.observe(res)
	r.captureOpenAI(parsed)
	if len(parsed.ToolCalls) == 0 {
		return toolCallInfo{}, res, parsed.Raw != nil, fallback
	}
	call := parsed.ToolCalls[0]
	if strings.TrimSpace(call.ID) == "" {
		call.ID = "call_proof"
	}
	args := parseArgs(call.Arguments)
	return toolCallInfo{ID: call.ID, Name: call.Name, ArgsRaw: call.Arguments, Args: args, Round1: call}, res, parsed.Raw != nil, fallback
}

func (r *modelRun) challengeOpenAIRound2(system, round1, round2 string, tool toolCallInfo, result map[string]any) httpResult {
	rawResult, _ := json.Marshal(result)
	args := tool.ArgsRaw
	if strings.TrimSpace(args) == "" {
		args = jsonPretty(tool.Args)
	}
	messages := []any{
		map[string]any{"role": "system", "content": system},
		map[string]any{"role": "user", "content": round1},
		map[string]any{
			"role":    "assistant",
			"content": "",
			"tool_calls": []any{map[string]any{
				"id": tool.ID, "type": "function",
				"function": map[string]any{"name": tool.Name, "arguments": args},
			}},
		},
		map[string]any{"role": "tool", "tool_call_id": tool.ID, "content": string(rawResult)},
		map[string]any{"role": "user", "content": round2},
	}
	r.lastMessages = []protocol.ChatMessage{
		{Role: "system", Content: system},
		{Role: "user", Content: round1},
		{Role: "assistant", ToolCalls: []protocol.ToolCall{{ID: tool.ID, Name: tool.Name, Arguments: args}}},
		{Role: "tool", ToolCallID: tool.ID, Content: string(rawResult)},
		{Role: "user", Content: round2},
	}
	res := r.postWithRetry(r.ctx, r.endpoint, r.headers, openAIChatBody(r.job.model, messages, r.cfg.Challenge, false, false, false), r.timeout())
	r.observe(res)
	r.captureOpenAI(parseOpenAIChat(res.Body))
	return res
}

func (r *modelRun) challengeClaudeRound1(system, prompt string, thinking bool) (toolCallInfo, httpResult, bool, string) {
	r.challengePrompt = prompt
	messages := []any{map[string]any{"role": "user", "content": prompt}}
	fallback := ""
	res := r.postWithRetry(r.ctx, r.endpoint, r.headers, claudeBody(r.job.model, messages, system, r.cfg.Challenge, true, r.cfg.Challenge.ForceTool, thinking), r.timeout())
	parsed := parseClaude(res.Body)
	if res.Status == 400 && (r.cfg.Challenge.ForceTool || thinking) {
		fallback = "Claude 拒绝了 tool_choice/thinking 组合，已回退 auto"
		res = r.postWithRetry(r.ctx, r.endpoint, r.headers, claudeBody(r.job.model, messages, system, r.cfg.Challenge, true, false, false), r.timeout())
		parsed = parseClaude(res.Body)
	}
	if res.Err == nil && res.statusOK() && len(parsed.ToolUses) == 0 {
		retry := retryChallengePrompt(r.cfg.Challenge, r.challenge.Nonce)
		r.challengePrompt = retry
		fallback = appendFallback(fallback, "未产生工具调用，已用强化提示重试一次")
		messages = []any{map[string]any{"role": "user", "content": retry}}
		res = r.postWithRetry(r.ctx, r.endpoint, r.headers, claudeBody(r.job.model, messages, system, r.cfg.Challenge, true, false, false), r.timeout())
		parsed = parseClaude(res.Body)
	}
	r.observe(res)
	r.captureClaude(parsed)
	if len(parsed.ToolUses) == 0 {
		return toolCallInfo{}, res, parsed.Raw != nil, fallback
	}
	use := parsed.ToolUses[0]
	if strings.TrimSpace(use.ID) == "" {
		use.ID = "toolu_proof"
	}
	args := parseArgsValue(use.Input)
	raw, _ := json.Marshal(use.Input)
	return toolCallInfo{
		ID: use.ID, Name: use.Name, ArgsRaw: string(raw), Args: args,
		Round1: use, RawBlock: parsed.Content,
	}, res, parsed.Raw != nil, fallback
}

func (r *modelRun) challengeClaudeRound2(system, round1, round2 string, tool toolCallInfo, result map[string]any) httpResult {
	rawResult, _ := json.Marshal(result)
	assistant := tool.RawBlock
	if assistant == nil {
		assistant = []any{map[string]any{
			"type": "tool_use", "id": tool.ID, "name": tool.Name,
			"input": tool.Args,
		}}
	}
	messages := []any{
		map[string]any{"role": "user", "content": round1},
		map[string]any{"role": "assistant", "content": assistant},
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": tool.ID, "content": string(rawResult)},
			map[string]any{"type": "text", "text": round2},
		}},
	}
	r.lastMessages = []protocol.ChatMessage{
		{Role: "system", Content: system},
		{Role: "user", Content: round1},
		{Role: "assistant", ToolCalls: []protocol.ToolCall{{ID: tool.ID, Name: tool.Name, Arguments: jsonPretty(tool.Args)}}},
		{Role: "tool", ToolCallID: tool.ID, Content: string(rawResult)},
		{Role: "user", Content: round2},
	}
	res := r.postWithRetry(r.ctx, r.endpoint, r.headers, claudeBody(r.job.model, messages, system, r.cfg.Challenge, false, false, false), r.timeout())
	r.observe(res)
	r.captureClaude(parseClaude(res.Body))
	return res
}

func (r *modelRun) challengeGeminiRound1(system, prompt string, thinking bool) (toolCallInfo, httpResult, bool, string) {
	r.challengePrompt = prompt
	contents := []any{
		map[string]any{"role": "user", "parts": []any{map[string]any{"text": prompt}}},
	}
	fallback := ""
	res := r.postWithRetry(r.ctx, r.endpoint, r.headers, geminiBody(r.job.model, contents, r.cfg.Challenge, true, r.cfg.Challenge.ForceTool, thinking), r.timeout())
	parsed := parseGemini(res.Body)
	if res.Status == 400 && (r.cfg.Challenge.ForceTool || thinking) {
		fallback = "Gemini 拒绝了 functionCallingConfig/thinkingConfig，已回退"
		res = r.postWithRetry(r.ctx, r.endpoint, r.headers, geminiBody(r.job.model, contents, r.cfg.Challenge, true, false, false), r.timeout())
		parsed = parseGemini(res.Body)
	}
	if res.Err == nil && res.statusOK() && len(parsed.FunctionCalls) == 0 {
		retry := retryChallengePrompt(r.cfg.Challenge, r.challenge.Nonce)
		r.challengePrompt = retry
		fallback = appendFallback(fallback, "未产生工具调用，已用强化提示重试一次")
		contents = []any{
			map[string]any{"role": "user", "parts": []any{map[string]any{"text": retry}}},
		}
		res = r.postWithRetry(r.ctx, r.endpoint, r.headers, geminiBody(r.job.model, contents, r.cfg.Challenge, true, false, false), r.timeout())
		parsed = parseGemini(res.Body)
	}
	r.observe(res)
	r.captureGemini(parsed)
	if len(parsed.FunctionCalls) == 0 {
		return toolCallInfo{}, res, parsed.Raw != nil, fallback
	}
	call := parsed.FunctionCalls[0]
	args := parseArgsValue(call.Args)
	raw, _ := json.Marshal(call.Args)
	return toolCallInfo{
		ID: "gemini_call", Name: call.Name, ArgsRaw: string(raw), Args: args,
		Round1: call,
	}, res, parsed.Raw != nil, fallback
}

func (r *modelRun) challengeGeminiRound2(round1, round2 string, tool toolCallInfo, result map[string]any) httpResult {
	contents := []any{
		map[string]any{"role": "user", "parts": []any{map[string]any{"text": round1}}},
		map[string]any{"role": "model", "parts": []any{map[string]any{
			"functionCall": map[string]any{"name": tool.Name, "args": tool.Args},
		}}},
		map[string]any{"role": "user", "parts": []any{
			map[string]any{"functionResponse": map[string]any{"name": tool.Name, "response": result}},
			map[string]any{"text": round2},
		}},
	}
	r.lastMessages = []protocol.ChatMessage{
		{Role: "user", Content: round1},
		{Role: "assistant", ToolCalls: []protocol.ToolCall{{Name: tool.Name, Arguments: jsonPretty(tool.Args)}}},
		{Role: "tool", Name: tool.Name, Content: jsonPretty(result)},
		{Role: "user", Content: round2},
	}
	res := r.postWithRetry(r.ctx, r.endpoint, r.headers, geminiBody(r.job.model, contents, r.cfg.Challenge, false, false, false), r.timeout())
	r.observe(res)
	r.captureGemini(parseGemini(res.Body))
	return res
}

func parseArgs(raw string) map[string]any {
	var m map[string]any
	if json.Unmarshal([]byte(raw), &m) == nil {
		return m
	}
	return map[string]any{}
}

func parseArgsValue(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	raw, _ := json.Marshal(v)
	return parseArgs(string(raw))
}

func finishIsToolUse(proto model.Protocol, finish string) bool {
	f := strings.ToLower(strings.TrimSpace(finish))
	switch proto {
	case model.ProtocolClaude:
		return f == "tool_use"
	case model.ProtocolGemini:
		return f == "stop" || f == "function_call" || f == ""
	default:
		return f == "tool_calls" || f == "function_call" || f == ""
	}
}

func toolsUnsupported(res httpResult) bool {
	if res.Status != 400 && res.Status != 422 {
		return false
	}
	s := strings.ToLower(string(res.Body))
	if !strings.Contains(s, "tool") && !strings.Contains(s, "function") {
		return false
	}
	for _, phrase := range []string{
		"not support", "unsupported", "does not support", "doesn't support",
		"不支持", "unknown field", "cannot find field",
	} {
		if strings.Contains(s, phrase) {
			return true
		}
	}
	return false
}

func retryableToolChoice(body []byte) bool {
	s := strings.ToLower(string(body))
	return strings.Contains(s, "tool_choice") || strings.Contains(s, "tool choice") ||
		strings.Contains(s, "function") || strings.Contains(s, "thinking")
}

func boolStatus(ok bool, failEvidence, passEvidence string) string {
	if ok {
		return StatusPass
	}
	return StatusFail
}

func evidenceForTool(tool toolCallInfo, parsed bool, fallback string) string {
	if tool.Name == "" {
		if !parsed {
			return "响应无法解析为工具调用结构"
		}
		return "响应中没有 tool_call/tool_use/functionCall"
	}
	out := "工具=" + tool.Name
	if fallback != "" {
		out += "；" + fallback
	}
	return out
}

func httpEvidence(res httpResult) string {
	if res.Err != nil {
		return "网络错误: " + res.Err.Error()
	}
	return fmt.Sprintf("HTTP %d %s", res.Status, truncateText(strings.TrimSpace(string(res.Body)), 300))
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func finalString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return fmt.Sprintf("%v", m[key])
}

func finalValue(m map[string]any, key string) any {
	if m == nil {
		return nil
	}
	return m[key]
}

func retryChallengePrompt(cfg ChallengeConfig, nonce string) string {
	return fmt.Sprintf("立即调用工具 %q，参数 challenge 必须严格等于 %q。不要输出普通文本，必须发起工具调用。",
		cfg.ToolName, nonce)
}

func appendFallback(existing, extra string) string {
	if existing == "" {
		return extra
	}
	return existing + "；" + extra
}
