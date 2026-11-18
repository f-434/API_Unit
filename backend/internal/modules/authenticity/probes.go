package authenticity

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"apiunit/internal/model"
	"apiunit/internal/tokens"
)

const probeClaudeModel = "claude-model-proof-probe-2026-10-28"
const probeBeta = "model-proof-probe-2026-10-28"

func (r *modelRun) runFingerprint() {
	switch r.protocolName {
	case model.ProtocolClaude:
		r.fingerprintClaudeUnknownModel()
		r.fingerprintClaudeUnknownBeta()
	case model.ProtocolGemini:
		r.fingerprintGeminiInvalidTools()
	default:
		r.fingerprintOpenAIReasoningEffort()
	}
}

func (r *modelRun) fingerprintOpenAIReasoningEffort() {
	const id = "P1-1"
	const caseID = "fp.openai.reasoning_effort"
	if !isOpenAIReasoningName(r.job.model) {
		r.add(CheckReport{
			ID: id, Group: GroupFingerprint, Name: "reasoning_effort=xxx 应返回官方校验错误",
			Status: StatusNA, Weight: WeightSoft, CaseID: caseID,
			Evidence: "当前模型不是推理模型，官方不会校验 reasoning_effort，跳过",
		})
		return
	}
	body := map[string]any{
		"model":                 r.job.model,
		"messages":              []any{map[string]any{"role": "user", "content": "ping"}},
		"reasoning_effort":      "xxx",
		"max_completion_tokens": 16,
	}
	start := time.Now()
	res := r.postWithRetry(r.ctx, r.endpoint, r.headers, body, r.timeout())
	r.observe(res)
	if r.useTemplateCheck(caseID, id, "reasoning_effort=xxx 应返回官方校验错误", caseID, res, start) {
		return
	}
	var parsed struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Param   string `json:"param"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(res.Body, &parsed)
	e := parsed.Error
	msg := e.Message
	if msg == "" {
		msg = errorMessage(res.Body)
	}
	paramOK := e.Param == "reasoning_effort" || e.Param == "reasoning.effort" ||
		strings.Contains(msg, "reasoning_effort") || strings.Contains(msg, "reasoning.effort")
	valueOK := e.Code == "invalid_value" ||
		strings.Contains(msg, "Invalid value") || strings.Contains(msg, "Supported values")
	typeOK := e.Type == "" || e.Type == "invalid_request_error"
	ok := res.Status >= 400 && res.Status < 500 && paramOK && valueOK && typeOK
	status := StatusFail
	evidence := fmt.Sprintf("HTTP %d; type=%q param=%q code=%q message=%q",
		res.Status, e.Type, e.Param, e.Code, truncateText(strings.TrimSpace(msg), 300))
	if ok {
		status = StatusPass
		evidence = fmt.Sprintf("匹配官方校验错误; HTTP %d; param=%q code=%q", res.Status, e.Param, e.Code)
	}
	c := CheckReport{
		ID: id, Group: GroupFingerprint, Name: "reasoning_effort=xxx 应返回官方校验错误",
		Status: status, Weight: WeightHard, Evidence: evidence, CaseID: caseID,
		DurationMs: time.Since(start).Milliseconds(),
	}
	r.add(c)
	r.record(res, caseID, "指纹：OpenAI reasoning_effort", GroupFingerprint, "", c)
}

func (r *modelRun) fingerprintClaudeUnknownModel() {
	const id = "P1-5"
	const caseID = "fp.claude.unknown_model"
	body := map[string]any{
		"model":      probeClaudeModel,
		"max_tokens": 16,
		"messages":   []any{map[string]any{"role": "user", "content": "ping"}},
	}
	start := time.Now()
	res := r.postWithRetry(r.ctx, r.endpoint, r.headers, body, r.timeout())
	r.observe(res)
	if r.useTemplateCheck(caseID, id, "未知模型应返回官方 not_found_error", caseID, res, start) {
		return
	}
	var parsed struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(res.Body, &parsed)
	msg := parsed.Error.Message
	ok := res.Status == http.StatusNotFound && parsed.Type == "error" &&
		parsed.Error.Type == "not_found_error" &&
		strings.HasPrefix(strings.TrimSpace(msg), "model:") && strings.Contains(msg, probeClaudeModel)
	status := StatusFail
	evidence := fmt.Sprintf("HTTP %d; type=%q error.type=%q message=%q", res.Status, parsed.Type, parsed.Error.Type, truncateText(msg, 300))
	if ok {
		status = StatusPass
		evidence = "匹配 Anthropic 官方 not_found_error 结构"
	} else if res.Status == http.StatusNotFound && parsed.Error.Type == "not_found_error" {
		status = StatusWarn
		evidence += "（404 结构部分匹配，但消息不符合官方格式）"
	}
	c := CheckReport{
		ID: id, Group: GroupFingerprint, Name: "未知模型应返回官方 not_found_error",
		Status: status, Weight: WeightHard, Evidence: evidence, CaseID: caseID,
		DurationMs: time.Since(start).Milliseconds(),
	}
	r.add(c)
	r.record(res, caseID, "指纹：Anthropic 未知模型", GroupFingerprint, "", c)
}

func (r *modelRun) fingerprintClaudeUnknownBeta() {
	const id = "P1-6"
	const caseID = "fp.claude.unknown_beta"
	body := map[string]any{
		"model":      r.job.model,
		"max_tokens": 16,
		"messages":   []any{map[string]any{"role": "user", "content": "ping"}},
	}
	headers := cloneHeaders(r.headers)
	headers["anthropic-beta"] = joinBeta(headers["anthropic-beta"], probeBeta)
	start := time.Now()
	res := r.postWithRetry(r.ctx, r.endpoint, headers, body, r.timeout())
	r.observe(res)
	if r.useTemplateCheck(caseID, id, "未知 anthropic-beta 应返回官方校验错误", caseID, res, start) {
		return
	}
	var parsed struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(res.Body, &parsed)
	msg := parsed.Error.Message
	ok := res.Status >= 400 && res.Status < 500 && parsed.Type == "error" &&
		parsed.Error.Type == "invalid_request_error" &&
		strings.Contains(msg, "Unexpected value") && strings.Contains(msg, probeBeta) &&
		strings.Contains(msg, "anthropic-beta")
	status := StatusFail
	evidence := fmt.Sprintf("HTTP %d; type=%q error.type=%q message=%q", res.Status, parsed.Type, parsed.Error.Type, truncateText(msg, 300))
	if ok {
		status = StatusPass
		evidence = "匹配 Anthropic 官方 beta 校验错误"
	} else if res.Status >= 400 && res.Status < 500 && parsed.Error.Type == "invalid_request_error" {
		status = StatusWarn
		evidence += "（4xx 结构部分匹配，但文案不符合官方格式）"
	}
	c := CheckReport{
		ID: id, Group: GroupFingerprint, Name: "未知 anthropic-beta 应返回官方校验错误",
		Status: status, Weight: WeightHard, Evidence: evidence, CaseID: caseID,
		DurationMs: time.Since(start).Milliseconds(),
	}
	r.add(c)
	r.record(res, caseID, "指纹：Anthropic 未知 beta", GroupFingerprint, "", c)
}

func (r *modelRun) fingerprintGeminiInvalidTools() {
	const id = "P1-7"
	const caseID = "fp.gemini.invalid_tools"
	body := map[string]any{
		"contents": []any{map[string]any{
			"role":  "user",
			"parts": []any{map[string]any{"text": "ping"}},
		}},
		"tools": []any{map[string]any{"xxx": map[string]any{}}},
	}
	start := time.Now()
	res := r.postWithRetry(r.ctx, r.endpoint, r.headers, body, r.timeout())
	r.observe(res)
	if r.useTemplateCheck(caseID, id, "非法 tools 应返回官方 INVALID_ARGUMENT", caseID, res, start) {
		return
	}
	var parsed struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Status  string `json:"status"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	_ = json.Unmarshal(res.Body, &parsed)
	msg := parsed.Error.Message
	// The direct Gemini API returns status=INVALID_ARGUMENT. New API and some
	// compatible gateways rewrite the envelope to type=upstream_error with no
	// status while preserving the official message. Accept both; the message
	// plus tools[0] is the stable fingerprint.
	statusOK := parsed.Error.Status == "" || parsed.Error.Status == "INVALID_ARGUMENT"
	msgOK := strings.Contains(msg, `Unknown name "xxx"`) && strings.Contains(msg, "tools[0]")
	ok := res.Status == http.StatusBadRequest && parsed.Error.Code == 400 && statusOK && msgOK
	status := StatusFail
	evidence := fmt.Sprintf("HTTP %d; code=%d status=%q type=%q message=%q",
		res.Status, parsed.Error.Code, parsed.Error.Status, parsed.Error.Type, truncateText(msg, 300))
	if ok {
		status = StatusPass
		if parsed.Error.Status == "" {
			evidence = "匹配官方错误文案（网关省略 status，按官方文案判定）"
		} else {
			evidence = "匹配 Gemini 官方 INVALID_ARGUMENT 结构"
		}
	} else if res.Status == http.StatusBadRequest && msgOK {
		status = StatusWarn
		evidence += "（400 且文案匹配，但 code/status 结构不标准）"
	}
	c := CheckReport{
		ID: id, Group: GroupFingerprint, Name: "非法 tools 应返回官方 INVALID_ARGUMENT",
		Status: status, Weight: WeightHard, Evidence: evidence, CaseID: caseID,
		DurationMs: time.Since(start).Milliseconds(),
	}
	r.add(c)
	r.record(res, caseID, "指纹：Gemini 非法 tools", GroupFingerprint, "", c)
}

// ---------- source ----------

func (r *modelRun) runSource() {
	if len(r.lastBody) == 0 {
		r.add(CheckReport{
			ID: "P2-1", Group: GroupSource, Name: "响应来源识别",
			Status: StatusNA, Weight: WeightSoft, Evidence: "没有拿到可用于判断来源的成功响应",
		})
		return
	}
	r.source = detectSource(r.protocolName, r.lastHeader, r.lastBody, r.lastEndpoint)
	evidence := r.source.Name + " / 置信度 " + r.source.Confidence
	if len(r.source.Evidence) > 0 {
		evidence += "；" + strings.Join(r.source.Evidence, "；")
	}
	r.add(CheckReport{
		ID: "P2-1", Group: GroupSource, Name: "响应来源识别",
		Status: StatusPass, Weight: WeightSoft, Evidence: evidence,
	})
	r.add(endpointConsistencyCheck(r.endpoint, r.source))
	r.add(gatewayCheck(r.lastHeader))
}

func detectSource(proto model.Protocol, h http.Header, body []byte, endpoint string) SourceReport {
	out := SourceReport{Name: "未识别（自定义端点 / 中转）", Confidence: "low"}
	if h == nil {
		h = http.Header{}
	}
	has := func(keys ...string) (string, bool) {
		for _, k := range keys {
			if v := strings.TrimSpace(h.Get(k)); v != "" {
				return k + "=" + v, true
			}
		}
		return "", false
	}
	gatewayEvidence := ""
	if v, ok := has("X-Oneapi-Request-Id", "X-New-Api-Request-Id", "X-Api-Request-Id", "X-One-Api-Request-Id"); ok {
		gatewayEvidence = v
	}
	switch proto {
	case model.ProtocolClaude:
		if v, ok := has("X-Amzn-Bedrock-Request-Id", "X-Amzn-Requestid"); ok {
			return SourceReport{Name: "Amazon Bedrock", Confidence: "high", Evidence: []string{v}}
		}
		if v, ok := has("X-Goog-Request-Id", "X-Goog-Api-Client"); ok {
			return SourceReport{Name: "Google Vertex AI", Confidence: "high", Evidence: []string{v}}
		}
		if v, ok := has("Apim-Request-Id", "X-Ms-Request-Id", "X-Ms-Region"); ok {
			return SourceReport{Name: "Microsoft Foundry", Confidence: "high", Evidence: []string{v}}
		}
		if v, ok := has("Anthropic-Organization-Id"); ok {
			return SourceReport{Name: "Anthropic 官方", Confidence: "high", Evidence: []string{v}}
		}
		for k := range h {
			if strings.HasPrefix(strings.ToLower(k), "anthropic-ratelimit-") {
				return SourceReport{Name: "Anthropic 官方", Confidence: "high", Evidence: []string{strings.ToLower(k)}}
			}
		}
		if strings.HasPrefix(strings.TrimSpace(h.Get("Request-Id")), "req_") {
			return SourceReport{Name: "Anthropic 官方", Confidence: "high", Evidence: []string{"Request-Id=" + h.Get("Request-Id")}}
		}
		if gatewayEvidence != "" {
			return SourceReport{Name: "中转网关（上游未知）", Confidence: "medium", Evidence: []string{gatewayEvidence}}
		}
		if hint := endpointSourceHint(endpoint); hint != "" {
			return SourceReport{Name: hint + "（端点推断）", Confidence: "low"}
		}
		return out

	case model.ProtocolGemini:
		m := decodeObject(body)
		if m != nil {
			if v := getString(m, "usageMetadata", "trafficType"); v != "" {
				return SourceReport{Name: "Google Vertex AI", Confidence: "medium", Evidence: []string{"usageMetadata.trafficType=" + v}}
			}
			if v := getString(m, "createTime"); v != "" {
				return SourceReport{Name: "Google Vertex AI", Confidence: "medium", Evidence: []string{"createTime=" + v}}
			}
		}
		if strings.Contains(string(body), "vertexaisearch.cloud.google.com") {
			return SourceReport{Name: "Google Vertex AI", Confidence: "medium", Evidence: []string{"grounding 经 vertexaisearch.cloud.google.com"}}
		}
		if gatewayEvidence != "" {
			return SourceReport{Name: "中转网关（上游未知）", Confidence: "medium", Evidence: []string{gatewayEvidence}}
		}
		if hint := endpointSourceHint(endpoint); strings.Contains(hint, "AI Studio") {
			return SourceReport{Name: "Google AI Studio（端点推断）", Confidence: "medium"}
		}
		return SourceReport{Name: "Google AI Studio（未检测到 Vertex 特征）", Confidence: "low"}

	default:
		if v, ok := has("X-Ms-Request-Id", "Apim-Request-Id", "X-Ms-Region"); ok {
			return SourceReport{Name: "Microsoft Azure", Confidence: "high", Evidence: []string{v}}
		}
		if v, ok := has("Openai-Organization", "Openai-Processing-Ms", "Openai-Version"); ok {
			return SourceReport{Name: "OpenAI 官方", Confidence: "high", Evidence: []string{v}}
		}
		m := decodeObject(body)
		if m != nil {
			if _, ok := m["content_filters"]; ok {
				return SourceReport{Name: "Microsoft Azure", Confidence: "medium", Evidence: []string{"响应含 content_filters"}}
			}
			if _, ok := m["quota"]; ok {
				return SourceReport{Name: "Microsoft Azure", Confidence: "medium", Evidence: []string{"响应含 quota"}}
			}
		}
		if gatewayEvidence != "" {
			return SourceReport{Name: "中转网关（上游未知）", Confidence: "medium", Evidence: []string{gatewayEvidence}}
		}
		return out
	}
}

func endpointConsistencyCheck(endpoint string, source SourceReport) CheckReport {
	const id = "P2-2"
	const name = "端点与响应来源一致"
	hint := endpointSourceHint(endpoint)
	if hint == "" {
		return CheckReport{
			ID: id, Group: GroupSource, Name: name, Status: StatusNA,
			Weight: WeightSoft, Evidence: "自定义端点，无法仅凭 URL 判定来源",
		}
	}
	if source.Confidence == "low" || source.Confidence == "unknown" {
		return CheckReport{
			ID: id, Group: GroupSource, Name: name, Status: StatusNA,
			Weight: WeightSoft, Evidence: "端点指向 " + hint + "，但响应特征不足以确认",
		}
	}
	if strings.Contains(source.Name, hint) || strings.Contains(hint, source.Name) {
		return CheckReport{
			ID: id, Group: GroupSource, Name: name, Status: StatusPass,
			Weight: WeightMetadata, Evidence: "端点与响应特征一致: " + hint,
		}
	}
	return CheckReport{
		ID: id, Group: GroupSource, Name: name, Status: StatusFail,
		Weight: WeightMetadata, Evidence: "端点指向 " + hint + "，响应特征指向 " + source.Name,
	}
}

func gatewayCheck(h http.Header) CheckReport {
	const id = "P2-3"
	const name = "未检测到网关改写特征"
	if h == nil {
		return CheckReport{ID: id, Group: GroupSource, Name: name, Status: StatusNA, Weight: WeightSoft, Evidence: "无响应头"}
	}
	for _, k := range []string{"X-Oneapi-Request-Id", "X-New-Api-Request-Id", "X-Api-Request-Id", "X-One-Api-Request-Id"} {
		if v := strings.TrimSpace(h.Get(k)); v != "" {
			return CheckReport{
				ID: id, Group: GroupSource, Name: name, Status: StatusWarn,
				Weight: WeightSoft, Evidence: "检测到网关响应头 " + k + "=" + v + "，上游平台可能被网关改写",
			}
		}
	}
	return CheckReport{
		ID: id, Group: GroupSource, Name: name, Status: StatusPass,
		Weight: WeightSoft, Evidence: "未检测到常见中转网关响应头",
	}
}

// ---------- identity / consistency / tokenizer ----------

func (r *modelRun) runIdentity() {
	if r.lastModel != "" {
		ok := modelMatches(r.lastModel, r.job.model)
		status := StatusPass
		if !ok {
			status = StatusFail
		}
		r.add(CheckReport{
			ID: "P4-1", Group: GroupIdentity, Name: "响应 model 与请求模型一致",
			Status: status, Weight: WeightMetadata,
			Evidence: fmt.Sprintf("请求 model=%q，实际 model=%q", r.job.model, r.lastModel),
		})
	} else {
		r.add(CheckReport{
			ID: "P4-1", Group: GroupIdentity, Name: "响应 model 与请求模型一致",
			Status: StatusNA, Weight: WeightMetadata, Evidence: "响应未返回 model 字段",
		})
	}

	r.add(responseIDCheck(r.protocolName, r.lastID))
	r.add(r.featureGateCheck())
}

func responseIDCheck(proto model.Protocol, id string) CheckReport {
	const name = "响应 ID 格式符合模型家族"
	if strings.TrimSpace(id) == "" {
		return CheckReport{ID: "P4-2", Group: GroupIdentity, Name: name, Status: StatusNA, Weight: WeightMetadata, Evidence: "响应未返回 ID"}
	}
	ok := true
	switch proto {
	case model.ProtocolClaude:
		ok = strings.HasPrefix(id, "msg_")
	case model.ProtocolGemini:
		ok = len(id) >= 8
	default:
		ok = strings.HasPrefix(id, "chatcmpl-") || strings.HasPrefix(id, "resp_") || strings.HasPrefix(id, "cmpl-")
	}
	status := StatusPass
	if !ok {
		status = StatusWarn
	}
	return CheckReport{
		ID: "P4-2", Group: GroupIdentity, Name: name,
		Status: status, Weight: WeightMetadata, Evidence: "id=" + truncateText(id, 120),
	}
}

func (r *modelRun) featureGateCheck() CheckReport {
	const id = "P4-3"
	const name = "声称模型的关键特征存在"
	switch r.protocolName {
	case model.ProtocolClaude:
		claimed := claudeAdaptive(r.job.model) || !strings.HasPrefix(normalizeModel(r.job.model), "claude-3")
		if !claimed {
			return CheckReport{ID: id, Group: GroupIdentity, Name: name, Status: StatusNA, Weight: WeightMetadata, Evidence: "Claude 3.x 未声称 thinking"}
		}
		if len(r.lastSignatures) > 0 {
			return CheckReport{ID: id, Group: GroupIdentity, Name: name, Status: StatusPass, Weight: WeightMetadata, Evidence: fmt.Sprintf("thinking 签名 %d 个", len(r.lastSignatures))}
		}
		return CheckReport{ID: id, Group: GroupIdentity, Name: name, Status: StatusWarn, Weight: WeightMetadata, Evidence: "未观察到 thinking 签名；可能是网关剥离或模型不支持"}
	case model.ProtocolGemini:
		if geminiMajor(r.job.model) == 0 || geminiMajor(r.job.model) < 2 {
			return CheckReport{ID: id, Group: GroupIdentity, Name: name, Status: StatusNA, Weight: WeightMetadata, Evidence: "未声称 thinking"}
		}
		if r.lastUsage.Reasoning > 0 || r.lastThoughtParts > 0 || len(r.lastThoughtSigs) > 0 {
			return CheckReport{ID: id, Group: GroupIdentity, Name: name, Status: StatusPass, Weight: WeightMetadata, Evidence: fmt.Sprintf("thoughtsTokenCount=%d thoughtParts=%d signatures=%d", r.lastUsage.Reasoning, r.lastThoughtParts, len(r.lastThoughtSigs))}
		}
		return CheckReport{ID: id, Group: GroupIdentity, Name: name, Status: StatusWarn, Weight: WeightMetadata, Evidence: "未观察到 thinking 内容；可能是网关剥离或模型降级"}
	default:
		if !isOpenAIReasoningName(r.job.model) {
			return CheckReport{ID: id, Group: GroupIdentity, Name: name, Status: StatusNA, Weight: WeightMetadata, Evidence: "当前模型未声称 reasoning"}
		}
		if r.lastUsage.Reasoning > 0 || strings.TrimSpace(r.lastReason) != "" {
			return CheckReport{ID: id, Group: GroupIdentity, Name: name, Status: StatusPass, Weight: WeightMetadata, Evidence: fmt.Sprintf("reasoning_tokens=%d", r.lastUsage.Reasoning)}
		}
		return CheckReport{ID: id, Group: GroupIdentity, Name: name, Status: StatusWarn, Weight: WeightMetadata, Evidence: "未观察到 reasoning tokens；可能是网关剥离或模型降级"}
	}
}

func (r *modelRun) runConsistency() {
	u := r.lastUsage
	tpl := r.usageTemplate()
	includeReasoning := usageIncludesReasoning(r.protocolName, tpl)
	allowReasoningGT := usageAllowsReasoningGTCompletion(r.protocolName, tpl)
	if !u.Reported {
		r.add(CheckReport{
			ID: "P5-1", Group: GroupConsistency, Name: "usage 数学一致",
			Status: StatusNA, Weight: WeightMetadata, Evidence: "响应未上报 usage",
		})
	} else {
		status := StatusPass
		evidence := fmt.Sprintf("prompt=%d completion=%d total=%d", u.Prompt, u.Completion, u.Total)
		ok := true
		if u.Prompt > 0 && u.Completion > 0 && u.Total > 0 {
			base := u.Prompt + u.Completion
			ok = absInt(u.Total-base) <= 2
			// Gemini reports thoughtsTokenCount separately and includes it in
			// totalTokenCount, so total = prompt + candidates + thoughts is
			// valid. A versioned usage template can change this rule.
			if includeReasoning && u.Reasoning > 0 {
				withThoughts := base + u.Reasoning
				evidence += fmt.Sprintf(" thoughts=%d withThoughts=%d", u.Reasoning, withThoughts)
				ok = ok || absInt(u.Total-withThoughts) <= 2
			}
			if !ok {
				status = StatusFail
				evidence += "（总和不一致）"
			}
		}
		r.add(CheckReport{
			ID: "P5-1", Group: GroupConsistency, Name: "usage 数学一致",
			Status: status, Weight: WeightMetadata, Evidence: evidence, CaseID: r.lastCaseID,
		})
	}

	status := StatusNA
	evidence := "未上报缓存/推理 Token"
	if u.Cached > 0 || u.Reasoning > 0 {
		status = StatusPass
		evidence = fmt.Sprintf("cached=%d reasoning=%d", u.Cached, u.Reasoning)
		if u.Prompt > 0 && u.Cached > u.Prompt {
			status = StatusFail
			evidence += "（缓存 Token 超过输入）"
		}
		// For Gemini, thoughtsTokenCount is independent of
		// candidatesTokenCount, so reasoning may exceed completion. A usage
		// template can override this per provider.
		if !allowReasoningGT && u.Completion > 0 && u.Reasoning > u.Completion {
			status = StatusFail
			evidence += "（推理 Token 超过输出）"
		}
	}
	r.add(CheckReport{
		ID: "P5-2", Group: GroupConsistency, Name: "缓存 / 推理 Token 边界合法",
		Status: status, Weight: WeightMetadata, Evidence: evidence, CaseID: r.lastCaseID,
	})
}

func (r *modelRun) runTokenizer() {
	if !r.lastUsage.Reported || r.lastUsage.Prompt <= 0 {
		r.add(CheckReport{
			ID: "P5-3", Group: GroupConsistency, Name: "本地分词器与 usage 偏差",
			Status: StatusNA, Weight: WeightMetadata, Evidence: "上游未上报输入 Token",
		})
		return
	}
	if len(r.lastMessages) == 0 {
		r.add(CheckReport{
			ID: "P5-3", Group: GroupConsistency, Name: "本地分词器与 usage 偏差",
			Status: StatusNA, Weight: WeightMetadata, Evidence: "没有可用于本地计数的消息",
		})
		return
	}
	counted := r.sess.Counter().CountChat(r.job.model, r.lastMessages, tokens.StrategyAuto)
	if counted.Tokens <= 0 {
		r.add(CheckReport{
			ID: "P5-3", Group: GroupConsistency, Name: "本地分词器与 usage 偏差",
			Status: StatusNA, Weight: WeightMetadata, Evidence: "本地分词器没有返回有效计数",
		})
		return
	}
	dev := math.Abs(float64(counted.Tokens-r.lastUsage.Prompt)) / float64(r.lastUsage.Prompt) * 100
	status := StatusPass
	if dev > r.cfg.Thresholds.TokenizerPct {
		status = StatusWarn
	}
	r.add(CheckReport{
		ID: "P5-3", Group: GroupConsistency, Name: "本地分词器与 usage 偏差",
		Status: status, Weight: WeightMetadata,
		Evidence: fmt.Sprintf("local=%d upstream=%d deviation=%.1f%% source=%s（工具 schema 等未计入本地，偏差仅供参考）",
			counted.Tokens, r.lastUsage.Prompt, dev, counted.Source),
	})
}

func cloneHeaders(in map[string]string) map[string]string {
	out := make(map[string]string, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}

func joinBeta(existing, extra string) string {
	existing = strings.TrimSpace(existing)
	if existing == "" {
		return extra
	}
	return existing + "," + extra
}

func errorMessage(body []byte) string {
	var m map[string]any
	if json.Unmarshal(body, &m) == nil {
		if e := getMap(m, "error"); e != nil {
			if s := getString(e, "message"); s != "" {
				return s
			}
		}
		if s := getString(m, "message"); s != "" {
			return s
		}
	}
	return string(body)
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
