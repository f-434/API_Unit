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

// sourceSample 是本次运行中捕获到的一次成功（2xx）响应。来源识别只使用
// 成功响应，避免最后一次请求失败时用错误响应体误判；同时保留产生证据的
// 用例 ID，前端可据此回溯原始请求/响应。
type sourceSample struct {
	Header http.Header
	Body   []byte
	CaseID string
}

var gatewayHeaderKeys = []string{
	"X-Oneapi-Request-Id",
	"X-New-Api-Request-Id",
	"X-Api-Request-Id",
	"X-One-Api-Request-Id",
}

func (r *modelRun) runSource() {
	if len(r.sourceSamples) == 0 {
		r.add(CheckReport{
			ID: "P2-1", Group: GroupSource, Name: "响应来源识别",
			Status: StatusNA, Weight: WeightSoft, CaseID: r.lastCaseID,
			Evidence: "没有拿到可用于判断来源的成功响应",
		})
		r.add(endpointConsistencyCheck(r.endpoint, SourceReport{Name: "未识别", Confidence: "unknown"}, r.lastCaseID))
		r.add(gatewayCheck(r.lastHeader, "", r.lastCaseID))
		return
	}
	source, sourceCaseID := detectSource(r.protocolName, r.sourceSamples, r.endpoint)
	r.source = source
	evidence := source.Name + " / 置信度 " + source.Confidence
	if source.Gateway != "" {
		evidence += "；检测到中转特征 " + source.Gateway
	}
	if len(source.Evidence) > 0 {
		evidence += "；" + strings.Join(source.Evidence, "；")
	}
	r.add(CheckReport{
		ID: "P2-1", Group: GroupSource, Name: "响应来源识别",
		Status: StatusPass, Weight: WeightSoft, Evidence: evidence, CaseID: sourceCaseID,
	})
	r.add(endpointConsistencyCheck(r.endpoint, source, sourceCaseID))
	r.add(gatewayCheck(r.lastHeader, source.Gateway, sourceCaseID))
}

// detectSource 按 model-proof 的口径识别“上游平台”，中转网关只作为单独
// 维度（Gateway）展示，不再覆盖上游结论。
//
//   - Claude：只看响应头（中转可能原样转发响应体，不能作为来源证据）。
//   - Gemini：trafficType / createTime / vertexaisearch 等 Vertex 特征；
//     均不存在时按 model-proof 口径推断为 AI Studio（低置信度）。
//   - OpenAI：Azure / 官方响应头优先；其次适配 Chat Completions 的 Azure
//     内容过滤字段；均无特征时按 model-proof 口径推断为 OpenAI 官方（低置信度）。
func detectSource(proto model.Protocol, samples []sourceSample, endpoint string) (SourceReport, string) {
	gwKey, gwValue, gwCase := findHeader(samples, gatewayHeaderKeys...)
	gateway := ""
	if gwKey != "" {
		gateway = gwKey + "=" + gwValue
	}
	lastCase := ""
	for _, s := range samples {
		if s.CaseID != "" {
			lastCase = s.CaseID
		}
	}

	switch proto {
	case model.ProtocolClaude:
		if k, v, cid := findHeader(samples, "X-Amzn-Bedrock-Request-Id", "X-Amzn-Requestid"); k != "" {
			return newSource("Amazon Bedrock", "high", []string{k + "=" + v}, gateway), cid
		}
		if k, v, cid := findHeader(samples, "X-Goog-Request-Id", "X-Goog-Api-Client"); k != "" {
			return newSource("Google Vertex AI", "high", []string{k + "=" + v}, gateway), cid
		}
		if k, v, cid := findHeader(samples, "Apim-Request-Id", "X-Ms-Request-Id", "X-Ms-Region"); k != "" {
			return newSource("Microsoft Foundry", "high", []string{k + "=" + v}, gateway), cid
		}
		if k, v, cid := findHeader(samples, "Anthropic-Organization-Id"); k != "" {
			return newSource("Anthropic 官方", "high", []string{k + "=" + v}, gateway), cid
		}
		if k, v, cid := findHeaderPrefix(samples, "anthropic-ratelimit-"); k != "" {
			return newSource("Anthropic 官方", "high", []string{k + "=" + v}, gateway), cid
		}
		if k, v, cid := findHeader(samples, "Request-Id"); k != "" && strings.HasPrefix(strings.TrimSpace(v), "req_") {
			return newSource("Anthropic 官方", "high", []string{k + "=" + v}, gateway), cid
		}
		if hint := endpointSourceHint(endpoint); hint == "Anthropic 官方" {
			return newSource("Anthropic 官方（端点推断）", "medium", nil, gateway), lastCase
		}
		if gateway != "" {
			return newSource("自定义端点 / 中转（上游未知）", "low", []string{gateway}, gateway), gwCase
		}
		return newSource("自定义端点（上游未知）", "low", nil, gateway), lastCase

	case model.ProtocolGemini:
		for _, s := range samples {
			m := decodeObject(s.Body)
			if m == nil {
				continue
			}
			if v := getString(m, "usageMetadata", "trafficType"); v != "" {
				return newSource("Google Vertex AI", "medium", []string{"usageMetadata.trafficType=" + v}, gateway), s.CaseID
			}
			if v := getString(m, "createTime"); v != "" {
				return newSource("Google Vertex AI", "medium", []string{"createTime=" + v}, gateway), s.CaseID
			}
			if strings.Contains(string(s.Body), "vertexaisearch.cloud.google.com") {
				return newSource("Google Vertex AI", "medium", []string{"grounding 经 vertexaisearch.cloud.google.com 跳转"}, gateway), s.CaseID
			}
		}
		hint := endpointSourceHint(endpoint)
		if hint == "Google Vertex AI" {
			return newSource("Google Vertex AI（端点推断）", "medium", nil, gateway), lastCase
		}
		if hint == "Google AI Studio" {
			return newSource("Google AI Studio（端点推断）", "medium", nil, gateway), lastCase
		}
		// 与 model-proof 一致：没有任何 Vertex 特征时推断为 AI Studio。
		// 经中转站时仍可能是被剥离特征的 Vertex，因此只给低置信度。
		return newSource("Google AI Studio（未检测到 Vertex 特征）", "low", nil, gateway), lastCase

	default:
		if k, v, cid := findHeader(samples, "X-Ms-Request-Id", "Apim-Request-Id", "X-Ms-Region"); k != "" {
			return newSource("Microsoft Azure", "high", []string{k + "=" + v}, gateway), cid
		}
		if k, v, cid := findHeader(samples, "Openai-Organization", "Openai-Processing-Ms", "Openai-Version"); k != "" {
			return newSource("OpenAI 官方", "high", []string{k + "=" + v}, gateway), cid
		}
		for _, s := range samples {
			m := decodeObject(s.Body)
			if m == nil {
				continue
			}
			if sig := azureBodySignature(m); sig != "" {
				return newSource("Microsoft Azure", "medium", []string{sig}, gateway), s.CaseID
			}
		}
		switch hint := endpointSourceHint(endpoint); hint {
		case "Microsoft Azure":
			return newSource("Microsoft Azure（端点推断）", "medium", nil, gateway), lastCase
		case "OpenAI 官方":
			return newSource("OpenAI 官方（端点推断）", "medium", nil, gateway), lastCase
		}
		for _, s := range samples {
			if looksLikeOpenAIResponse(s.Body) {
				return newSource("OpenAI 官方（未检测到 Azure 特征）", "low", nil, gateway), s.CaseID
			}
		}
		if gateway != "" {
			return newSource("OpenAI 兼容端点 / 中转（上游未知）", "low", []string{gateway}, gateway), gwCase
		}
		return newSource("未识别（自定义端点）", "low", nil, gateway), lastCase
	}
}

func newSource(name, confidence string, evidence []string, gateway string) SourceReport {
	return SourceReport{Name: name, Confidence: confidence, Evidence: evidence, Gateway: gateway}
}

// findHeader 返回第一个命中的响应头（key、value）及其来源用例。
func findHeader(samples []sourceSample, keys ...string) (key, value, caseID string) {
	for _, s := range samples {
		if s.Header == nil {
			continue
		}
		for _, k := range keys {
			if v := strings.TrimSpace(s.Header.Get(k)); v != "" {
				return k, v, s.CaseID
			}
		}
	}
	return "", "", ""
}

// findHeaderPrefix 按前缀匹配响应头，用于 anthropic-ratelimit-* 这类动态头。
func findHeaderPrefix(samples []sourceSample, prefix string) (key, value, caseID string) {
	prefix = strings.ToLower(prefix)
	for _, s := range samples {
		for k, vals := range s.Header {
			if !strings.HasPrefix(strings.ToLower(k), prefix) || len(vals) == 0 {
				continue
			}
			if v := strings.TrimSpace(vals[0]); v != "" {
				return strings.ToLower(k), v, s.CaseID
			}
		}
	}
	return "", "", ""
}

// azureBodySignature 识别 Azure OpenAI Chat Completions 的内容过滤字段。
// model-proof 使用的 content_filters / quota 属于 Responses API，这里同时
// 兼容 Chat Completions 的 prompt_filter_results / content_filter_results。
func azureBodySignature(m map[string]any) string {
	if v := getSlice(m, "prompt_filter_results"); len(v) > 0 {
		return "响应含 prompt_filter_results"
	}
	for _, c := range getSlice(m, "choices") {
		cm, _ := c.(map[string]any)
		if cm == nil {
			continue
		}
		if getMap(cm, "content_filter_results") != nil {
			return "choices[].content_filter_results"
		}
	}
	if v, ok := m["content_filters"]; ok && v != nil {
		return "响应含 content_filters"
	}
	if v, ok := m["quota"]; ok && v != nil {
		return "响应含 quota"
	}
	return ""
}

// looksLikeOpenAIResponse 只用于“无 Azure 特征”时的低置信度回落判断。
func looksLikeOpenAIResponse(body []byte) bool {
	m := decodeObject(body)
	if m == nil {
		return false
	}
	switch getString(m, "object") {
	case "chat.completion", "response":
		return true
	}
	id := getString(m, "id")
	if strings.HasPrefix(id, "chatcmpl-") || strings.HasPrefix(id, "resp_") || strings.HasPrefix(id, "cmpl-") {
		return true
	}
	return getSlice(m, "choices") != nil || getSlice(m, "output") != nil
}

func endpointConsistencyCheck(endpoint string, source SourceReport, caseID string) CheckReport {
	const id = "P2-2"
	const name = "端点与响应来源一致"
	hint := endpointSourceHint(endpoint)
	if hint == "" {
		return CheckReport{
			ID: id, Group: GroupSource, Name: name, Status: StatusNA,
			Weight: WeightSoft, Evidence: "自定义端点，无法仅凭 URL 判定来源", CaseID: caseID,
		}
	}
	if source.Confidence == "low" || source.Confidence == "unknown" {
		return CheckReport{
			ID: id, Group: GroupSource, Name: name, Status: StatusNA,
			Weight: WeightSoft, Evidence: "端点指向 " + hint + "，但响应特征不足以确认", CaseID: caseID,
		}
	}
	if sourceNamesMatch(hint, source.Name) {
		return CheckReport{
			ID: id, Group: GroupSource, Name: name, Status: StatusPass,
			Weight: WeightMetadata, Evidence: "端点与响应特征一致: " + hint, CaseID: caseID,
		}
	}
	return CheckReport{
		ID: id, Group: GroupSource, Name: name, Status: StatusFail,
		Weight: WeightMetadata, Evidence: "端点指向 " + hint + "，响应特征指向 " + source.Name, CaseID: caseID,
	}
}

func sourceNamesMatch(hint, detected string) bool {
	if strings.Contains(detected, hint) || strings.Contains(hint, detected) {
		return true
	}
	// Claude 经 Microsoft Foundry 时端点提示是 Microsoft Azure，响应头指向
	// Microsoft Foundry，两者属于同一产品家族。
	azureFoundry := func(a, b string) bool {
		return (strings.Contains(a, "Azure") && strings.Contains(b, "Foundry")) ||
			(strings.Contains(a, "Foundry") && strings.Contains(b, "Azure"))
	}
	return azureFoundry(hint, detected)
}

func gatewayCheck(h http.Header, gateway, caseID string) CheckReport {
	const id = "P2-3"
	const name = "未检测到网关改写特征"
	if gateway != "" {
		return CheckReport{
			ID: id, Group: GroupSource, Name: name, Status: StatusWarn,
			Weight: WeightSoft, Evidence: "检测到网关特征 " + gateway + "，上游平台可能被网关改写", CaseID: caseID,
		}
	}
	if h == nil {
		return CheckReport{ID: id, Group: GroupSource, Name: name, Status: StatusNA, Weight: WeightSoft, Evidence: "无响应头", CaseID: caseID}
	}
	for _, k := range gatewayHeaderKeys {
		if v := strings.TrimSpace(h.Get(k)); v != "" {
			return CheckReport{
				ID: id, Group: GroupSource, Name: name, Status: StatusWarn,
				Weight: WeightSoft, Evidence: "检测到网关响应头 " + k + "=" + v + "，上游平台可能被网关改写", CaseID: caseID,
			}
		}
	}
	return CheckReport{
		ID: id, Group: GroupSource, Name: name, Status: StatusPass,
		Weight: WeightSoft, Evidence: "未检测到常见中转网关响应头", CaseID: caseID,
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
			Status: status, Weight: WeightMetadata, CaseID: r.lastCaseID,
			Evidence: fmt.Sprintf("请求 model=%q，实际 model=%q", r.job.model, r.lastModel),
		})
	} else {
		r.add(CheckReport{
			ID: "P4-1", Group: GroupIdentity, Name: "响应 model 与请求模型一致",
			Status: StatusNA, Weight: WeightMetadata, Evidence: "响应未返回 model 字段", CaseID: r.lastCaseID,
		})
	}

	r.add(responseIDCheck(r.protocolName, r.lastID, r.lastCaseID))
	r.add(r.featureGateCheck(r.lastCaseID))
}

func responseIDCheck(proto model.Protocol, id, caseID string) CheckReport {
	const name = "响应 ID 格式符合模型家族"
	if strings.TrimSpace(id) == "" {
		return CheckReport{ID: "P4-2", Group: GroupIdentity, Name: name, Status: StatusNA, Weight: WeightMetadata, Evidence: "响应未返回 ID", CaseID: caseID}
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
		Status: status, Weight: WeightMetadata, Evidence: "id=" + truncateText(id, 120), CaseID: caseID,
	}
}

func (r *modelRun) featureGateCheck(caseID string) CheckReport {
	const id = "P4-3"
	const name = "声称模型的关键特征存在"
	switch r.protocolName {
	case model.ProtocolClaude:
		claimed := claudeAdaptive(r.job.model) || !strings.HasPrefix(normalizeModel(r.job.model), "claude-3")
		if !claimed {
			return CheckReport{ID: id, Group: GroupIdentity, Name: name, Status: StatusNA, Weight: WeightMetadata, Evidence: "Claude 3.x 未声称 thinking", CaseID: caseID}
		}
		if len(r.lastSignatures) > 0 {
			return CheckReport{ID: id, Group: GroupIdentity, Name: name, Status: StatusPass, Weight: WeightMetadata, Evidence: fmt.Sprintf("thinking 签名 %d 个", len(r.lastSignatures)), CaseID: caseID}
		}
		return CheckReport{ID: id, Group: GroupIdentity, Name: name, Status: StatusWarn, Weight: WeightMetadata, Evidence: "未观察到 thinking 签名；可能是网关剥离或模型不支持", CaseID: caseID}
	case model.ProtocolGemini:
		if geminiMajor(r.job.model) == 0 || geminiMajor(r.job.model) < 2 {
			return CheckReport{ID: id, Group: GroupIdentity, Name: name, Status: StatusNA, Weight: WeightMetadata, Evidence: "未声称 thinking", CaseID: caseID}
		}
		if r.lastUsage.Reasoning > 0 || r.lastThoughtParts > 0 || len(r.lastThoughtSigs) > 0 {
			return CheckReport{ID: id, Group: GroupIdentity, Name: name, Status: StatusPass, Weight: WeightMetadata, Evidence: fmt.Sprintf("thoughtsTokenCount=%d thoughtParts=%d signatures=%d", r.lastUsage.Reasoning, r.lastThoughtParts, len(r.lastThoughtSigs)), CaseID: caseID}
		}
		return CheckReport{ID: id, Group: GroupIdentity, Name: name, Status: StatusWarn, Weight: WeightMetadata, Evidence: "未观察到 thinking 内容；可能是网关剥离或模型降级", CaseID: caseID}
	default:
		if !isOpenAIReasoningName(r.job.model) {
			return CheckReport{ID: id, Group: GroupIdentity, Name: name, Status: StatusNA, Weight: WeightMetadata, Evidence: "当前模型未声称 reasoning", CaseID: caseID}
		}
		if r.lastUsage.Reasoning > 0 || strings.TrimSpace(r.lastReason) != "" {
			return CheckReport{ID: id, Group: GroupIdentity, Name: name, Status: StatusPass, Weight: WeightMetadata, Evidence: fmt.Sprintf("reasoning_tokens=%d", r.lastUsage.Reasoning), CaseID: caseID}
		}
		return CheckReport{ID: id, Group: GroupIdentity, Name: name, Status: StatusWarn, Weight: WeightMetadata, Evidence: "未观察到 reasoning tokens；可能是网关剥离或模型降级", CaseID: caseID}
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
			Status: StatusNA, Weight: WeightMetadata, Evidence: "响应未上报 usage", CaseID: r.lastCaseID,
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
			Status: StatusNA, Weight: WeightMetadata, Evidence: "上游未上报输入 Token", CaseID: r.lastCaseID,
		})
		return
	}
	if len(r.lastMessages) == 0 {
		r.add(CheckReport{
			ID: "P5-3", Group: GroupConsistency, Name: "本地分词器与 usage 偏差",
			Status: StatusNA, Weight: WeightMetadata, Evidence: "没有可用于本地计数的消息", CaseID: r.lastCaseID,
		})
		return
	}
	counted := r.sess.Counter().CountChat(r.job.model, r.lastMessages, tokens.StrategyAuto)
	if counted.Tokens <= 0 {
		r.add(CheckReport{
			ID: "P5-3", Group: GroupConsistency, Name: "本地分词器与 usage 偏差",
			Status: StatusNA, Weight: WeightMetadata, Evidence: "本地分词器没有返回有效计数", CaseID: r.lastCaseID,
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
		Status: status, Weight: WeightMetadata, CaseID: r.lastCaseID,
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
