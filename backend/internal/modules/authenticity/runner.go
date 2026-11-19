package authenticity

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"apiunit/internal/engine"
	"apiunit/internal/model"
	"apiunit/internal/protocol"
	"apiunit/internal/store"
)

type modelRun struct {
	ctx          context.Context
	sess         *engine.Session
	cfg          *Config
	store        *store.Store
	hosted       HostedSettings
	pub          *livePublisher
	job          job
	protocolName model.Protocol
	endpoint     string
	headers      map[string]string
	challenge    ChallengeReport
	checks       []CheckReport
	source       SourceReport

	lastHeader       http.Header
	lastBody         []byte
	lastStatus       int
	lastLatency      float64
	lastModel        string
	lastID           string
	lastFinish       string
	lastText         string
	lastReason       string
	lastUsage        usageInfo
	lastMessages     []protocol.ChatMessage
	lastSignatures   []string
	lastThinking     []string
	lastThoughtSigs  []string
	lastThoughtParts int
	lastGrounding    []any
	lastWebQueries   []string
	lastCaseID       string
	challengePrompt  string
	lastSuccess      bool
	errorMsg         string

	// sourceSamples 保存本次运行中所有成功（2xx）响应的样本。来源识别只使用
	// 成功响应，避免最后一次请求失败时用错误体误判来源；同时保留产生证据的
	// 用例 ID，便于前端回溯原始请求/响应。
	sourceSamples []sourceSample
}

func runModel(ctx context.Context, sess *engine.Session, st *store.Store, cfg *Config, hosted HostedSettings, j job, pub *livePublisher) ModelReport {
	start := time.Now()
	if pub != nil {
		pub.start(j, "初始化")
	}
	rep := ModelReport{
		TargetID:   j.target.ID,
		TargetName: j.target.Name,
		Model:      j.model,
		Repetition: j.repetition,
		Source:     SourceReport{Name: "未识别", Confidence: "unknown"},
	}
	proto := resolveProtocol(j.target, j.spec, j.model)
	rep.Protocol = string(proto)
	endpoint, err := resolveEndpoint(j.target, j.spec, proto, j.model, "chat")
	if err != nil {
		rep.Verdict = VerdictError
		rep.VerdictLabel = verdictLabel(rep.Verdict)
		rep.Error = "端点解析失败: " + err.Error()
		rep.DurationMs = time.Since(start).Milliseconds()
		if pub != nil {
			pub.setError(j, rep.Error)
			pub.finish(j, rep.Verdict, rep.Error)
		}
		return rep
	}
	rep.Endpoint = endpoint
	r := &modelRun{
		ctx:          ctx,
		sess:         sess,
		store:        st,
		cfg:          cfg,
		hosted:       hosted,
		pub:          pub,
		job:          j,
		protocolName: proto,
		endpoint:     endpoint,
		headers:      authHeaders(j.target, j.spec, proto, endpoint),
	}
	r.challenge = newChallenge(cfg.Challenge.NonceLength)
	r.challenge.ToolName = cfg.Challenge.ToolName
	rep.Challenge = r.challenge

	if cfg.Checks.Fingerprint {
		r.stage("接口指纹校验")
		r.runFingerprint()
	}
	if cfg.Checks.Capability {
		r.stage("真实能力挑战")
		r.runChallenge()
	} else {
		r.stage("连通性探测")
		r.runPing()
	}
	if cfg.Checks.Source {
		r.stage("响应来源识别")
		r.ensureResponse()
		r.runSource()
	}
	if cfg.Checks.Identity {
		r.stage("模型身份检查")
		r.ensureResponse()
		r.runIdentity()
	}
	if cfg.Checks.Consistency {
		r.stage("数据一致性检查")
		r.ensureResponse()
		r.runConsistency()
	}
	if cfg.Checks.Tokenizer {
		r.stage("本地分词器对比")
		r.runTokenizer()
	}
	if cfg.Checks.Signature && r.protocolName == model.ProtocolGemini {
		r.stage("思维签名真实性")
		r.runGeminiSignature()
	}
	if cfg.Hosted.Enabled && (r.hosted.MCP.ServerURL != "" || r.hosted.Proof.BaseURL != "") {
		r.stage("工具调用（MCP / 证明页）")
		r.runHosted()
	} else if cfg.Hosted.Enabled {
		r.add(CheckReport{
			ID: "H0", Group: GroupHosted, Name: "工具调用配置可用",
			Status: StatusNA, Weight: WeightSoft,
			Evidence: "未配置 MCP 地址或证明页地址",
		})
	}
	if len(cfg.CustomProbes) > 0 {
		r.stage("自定义探针")
		r.runCustomProbes()
	}

	rep.Source = r.source
	if r.lastModel != "" {
		rep.ResponseModel = r.lastModel
	}
	rep.Checks = r.checks
	rep.Passed, rep.Failed, rep.NA, rep.Total = countChecks(r.checks)
	rep.Dimensions = dimensions(r.checks)
	rep.Verdict, rep.Confidence = judge(r)
	rep.VerdictLabel = verdictLabel(rep.Verdict)
	rep.DurationMs = time.Since(start).Milliseconds()
	if rep.Verdict == VerdictError && rep.Error == "" {
		rep.Error = r.errorMsg
	}
	if pub != nil {
		pub.finish(j, rep.Verdict, rep.Error)
	}
	return rep
}

func newChallenge(nonceLen int) ChallengeReport {
	if nonceLen < 8 {
		nonceLen = 8
	}
	nonce := randomHex(nonceLen)
	secret := randomHex(nonceLen)
	a := randomInt(10, 99)
	b := randomInt(10, 99)
	return ChallengeReport{ToolName: "", Nonce: nonce, Secret: secret, A: a, B: b, Sum: a + b}
}

func randomHex(n int) string {
	raw := make([]byte, (n+1)/2)
	_, _ = rand.Read(raw)
	s := hex.EncodeToString(raw)
	if len(s) > n {
		s = s[:n]
	}
	return s
}

func randomInt(min, max int) int {
	if max <= min {
		return min
	}
	buf := make([]byte, 4)
	_, _ = rand.Read(buf)
	v := int(buf[0])<<24 | int(buf[1])<<16 | int(buf[2])<<8 | int(buf[3])
	if v < 0 {
		v = -v
	}
	return min + v%(max-min+1)
}

func (r *modelRun) add(checks ...CheckReport) {
	for _, c := range checks {
		if c.GroupName == "" {
			c.GroupName = groupName(c.Group)
		}
		if c.Weight == "" {
			c.Weight = WeightSoft
		}
		if c.Repetition == 0 {
			c.Repetition = r.job.repetition
		}
		r.checks = append(r.checks, c)
		if r.pub != nil {
			r.pub.addCheck(r.job, c)
		}
	}
}

func (r *modelRun) stage(name string) {
	if r.pub != nil {
		r.pub.setStage(r.job, name)
	}
}

func (r *modelRun) setRunError(msg string) {
	r.errorMsg = msg
	if r.pub != nil {
		r.pub.setError(r.job, msg)
	}
}

func (r *modelRun) hasCheck(id string) bool {
	for _, c := range r.checks {
		if c.ID == id {
			return true
		}
	}
	return false
}

func countChecks(checks []CheckReport) (pass, fail, na, total int) {
	for _, c := range checks {
		total++
		switch c.Status {
		case StatusPass, StatusWarn:
			pass++
		case StatusFail:
			fail++
		case StatusNA:
			na++
		}
	}
	return
}

func dimensions(checks []CheckReport) []DimensionReport {
	order := []string{GroupFingerprint, GroupSource, GroupCapability, GroupIdentity, GroupConsistency, GroupSignature, GroupHosted, GroupCustom}
	index := map[string]*DimensionReport{}
	out := make([]DimensionReport, 0, len(order))
	for _, g := range order {
		d := DimensionReport{Key: g, Name: groupName(g)}
		out = append(out, d)
		index[g] = &out[len(out)-1]
	}
	for _, c := range checks {
		d := index[c.Group]
		if d == nil {
			continue
		}
		switch c.Status {
		case StatusPass, StatusWarn:
			d.Passed++
		case StatusFail:
			d.Failed++
		case StatusNA:
			d.NA++
		}
	}
	return out
}

func primaryStatus(checks []CheckReport) string {
	if len(checks) == 0 {
		return StatusNA
	}
	status := StatusPass
	for _, c := range checks {
		switch c.Status {
		case StatusFail:
			return StatusFail
		case StatusWarn:
			if status == StatusPass {
				status = StatusWarn
			}
		case StatusNA:
			if status == StatusPass {
				status = StatusNA
			}
		}
	}
	return status
}

func (r *modelRun) observe(res httpResult) {
	if res.Header != nil {
		r.lastHeader = res.Header
	}
	if res.Body != nil {
		r.lastBody = res.Body
	}
	r.lastStatus = res.Status
	r.lastLatency = res.LatencyMs
	if res.statusOK() {
		r.lastSuccess = true
	}
}

func (r *modelRun) record(res httpResult, caseID, caseName, category, output string, checks ...CheckReport) model.Item {
	status := primaryStatus(checks)
	secrets := r.secretValues()
	it := model.Item{
		TargetID:      r.job.target.ID,
		TargetName:    r.job.target.Name,
		Model:         r.job.model,
		Protocol:      string(r.protocolName),
		CaseID:        caseID,
		CaseName:      caseName,
		Category:      category,
		Severity:      primaryWeight(checks),
		Status:        res.Status,
		OK:            status == StatusPass || status == StatusWarn,
		LatencyMs:     res.LatencyMs,
		FinishReason:  r.lastFinish,
		OutputText:    output,
		OutputPreview: previewText(output),
		RequestJSON:   redactSecrets(res.RequestJSON, secrets),
		ResponseJSON:  redactSecrets(res.ResponseJSON, secrets),
		ResponseModel: r.lastModel,
		RequestID:     r.lastID,
		Attempts:      1,
		Repetition:    r.job.repetition,
		Verdict:       status,
		Meta: map[string]any{
			"endpoint":     redactSecrets(res.Endpoint, secrets),
			"httpStatus":   res.Status,
			"checkStatus":  status,
			"authenticity": true,
			"repetition":   r.job.repetition,
		},
	}
	if r.lastModel != "" {
		it.Meta["upstreamModel"] = r.lastModel
	}
	if len(checks) > 0 {
		it.Meta["checkId"] = checks[0].ID
		it.Meta["checkName"] = checks[0].Name
		it.Meta["evidence"] = checks[0].Evidence
	}
	if headers := responseHeaderSnapshot(res.Header, secrets); len(headers) > 0 {
		it.Meta["responseHeaders"] = headers
	}
	if len(checks) > 1 {
		ids := make([]string, 0, len(checks))
		for _, c := range checks {
			ids = append(ids, c.ID)
		}
		it.Meta["checkIds"] = ids
	}
	applyUsage(&it, r.lastUsage)
	for _, c := range checks {
		it.Checks = append(it.Checks, model.CheckResult{
			Check:    model.Check{Kind: "authenticity", Value: c.ID, Description: c.Name},
			Matched:  c.Status == StatusPass,
			Evidence: c.Evidence,
			Decisive: c.Weight == WeightHard || c.Weight == WeightCapability,
		})
	}
	r.lastCaseID = caseID
	if res.statusOK() {
		r.sourceSamples = append(r.sourceSamples, sourceSample{
			Header: res.Header,
			Body:   res.Body,
			CaseID: caseID,
		})
	}
	if res.Err != nil {
		it.ErrorType = "network"
		it.ErrorMessage = redactSecrets(res.Err.Error(), secrets)
	} else if res.Status >= 400 && status == StatusFail {
		it.ErrorType = "probe"
		it.ErrorMessage = redactSecrets(truncateText(strings.TrimSpace(string(res.Body)), 500), secrets)
	}
	return r.sess.Record(it)
}

func primaryWeight(checks []CheckReport) string {
	for _, c := range checks {
		if c.Weight == WeightHard {
			return WeightHard
		}
	}
	for _, c := range checks {
		if c.Weight == WeightCapability {
			return WeightCapability
		}
	}
	for _, c := range checks {
		if c.Weight == WeightMetadata {
			return WeightMetadata
		}
	}
	return WeightSoft
}

func applyUsage(it *model.Item, u usageInfo) {
	if !u.Reported {
		return
	}
	it.PromptTokens = u.Prompt
	it.CompletionTokens = u.Completion
	it.TotalTokens = u.Total
	it.CachedTokens = u.Cached
	it.CacheWriteTokens = u.CacheWrite
	it.ReasoningTokens = u.Reasoning
	it.TokenSource = "usage"
	if it.TotalTokens < it.PromptTokens+it.CompletionTokens {
		it.TotalTokens = it.PromptTokens + it.CompletionTokens
	}
}

func (r *modelRun) secretValues() []string {
	out := make([]string, 0, len(r.hosted.MCP.Headers)+1)
	if v := strings.TrimSpace(r.job.target.APIKey); len(v) >= 4 {
		out = append(out, v)
	}
	for _, v := range r.hosted.MCP.Headers {
		if v = strings.TrimSpace(v); len(v) >= 4 {
			out = append(out, v)
		}
	}
	return out
}

func redactSecrets(s string, secrets []string) string {
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		s = strings.ReplaceAll(s, secret, "***")
	}
	return s
}

// responseHeaderSnapshot keeps only diagnostic response headers. They are
// persisted on the item so a human can verify source detection (x-ms-*,
// x-amzn-*, x-goog-*, x-oneapi-*, OpenAI-Organization, etc.).
func responseHeaderSnapshot(h http.Header, secrets []string) map[string]string {
	if h == nil {
		return nil
	}
	out := make(map[string]string)
	for k, vals := range h {
		lk := strings.ToLower(k)
		if !diagnosticHeader(lk) {
			continue
		}
		v := strings.TrimSpace(strings.Join(vals, ", "))
		if v == "" {
			continue
		}
		if len(v) > 512 {
			v = v[:512] + "..."
		}
		out[k] = redactSecrets(v, secrets)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func diagnosticHeader(lk string) bool {
	switch {
	case strings.HasPrefix(lk, "x-"),
		strings.HasPrefix(lk, "openai-"),
		strings.HasPrefix(lk, "anthropic-"):
		return true
	case lk == "apim-request-id",
		lk == "request-id",
		lk == "server",
		lk == "cf-ray",
		lk == "via",
		lk == "traceparent":
		return true
	}
	return false
}

func previewText(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) > 600 {
		return string([]rune(s)[:600])
	}
	return s
}

func (r *modelRun) ensureResponse() {
	if r.lastBody != nil {
		return
	}
	r.runPing()
}

func (r *modelRun) runPing() {
	r.stage("连通性探测")
	caseID := "ping"
	var res httpResult
	switch r.protocolName {
	case model.ProtocolClaude:
		req := claudeBody(r.job.model, []any{
			map[string]any{"role": "user", "content": "ping"},
		}, "", ChallengeConfig{MaxTokens: 8, Temperature: floatPtr(0)}, false, false, false)
		r.challenge.ToolName = r.cfg.Challenge.ToolName
		res = r.postWithRetry(r.ctx, r.endpoint, r.headers, req, r.timeout())
		r.observe(res)
		parsed := parseClaude(res.Body)
		r.captureClaude(parsed)
	case model.ProtocolGemini:
		req := geminiBody(r.job.model, []any{
			map[string]any{"role": "user", "parts": []any{map[string]any{"text": "ping"}}},
		}, ChallengeConfig{MaxTokens: 8, Temperature: floatPtr(0)}, false, false, false)
		res = r.postWithRetry(r.ctx, r.endpoint, r.headers, req, r.timeout())
		r.observe(res)
		parsed := parseGemini(res.Body)
		r.captureGemini(parsed)
	default:
		req := openAIChatBody(r.job.model, []any{
			map[string]any{"role": "user", "content": "ping"},
		}, ChallengeConfig{MaxTokens: 8, Temperature: floatPtr(0)}, false, false, false)
		res = r.postWithRetry(r.ctx, r.endpoint, r.headers, req, r.timeout())
		r.observe(res)
		parsed := parseOpenAIChat(res.Body)
		r.captureOpenAI(parsed)
	}
	status := StatusPass
	evidence := fmt.Sprintf("HTTP %d", res.Status)
	if res.Err != nil {
		status = StatusFail
		evidence = "网络错误: " + res.Err.Error()
		r.setRunError(evidence)
	} else if !res.statusOK() {
		status = StatusFail
		evidence += " " + truncateText(strings.TrimSpace(string(res.Body)), 300)
		if res.Status == 401 || res.Status == 403 || res.Status == 404 || res.Status == 429 || res.Status >= 500 {
			r.setRunError(fmt.Sprintf("上游返回 HTTP %d，无法完成检测", res.Status))
		}
	}
	p0 := CheckReport{
		ID: "P0-1", Group: GroupConsistency, Name: "鉴权与端点可达",
		Status: status, Weight: WeightHard, Evidence: evidence, CaseID: caseID,
		DurationMs: int64(res.LatencyMs),
	}
	if !r.hasCheck("P0-1") {
		r.add(p0)
	}
	output := r.lastText
	r.record(res, caseID, "连通性探测", GroupConsistency, output, p0)
}

func (r *modelRun) timeout() time.Duration {
	d := time.Duration(r.cfg.TimeoutMs) * time.Millisecond
	if d <= 0 {
		d = 3 * time.Minute
	}
	return d
}

func floatPtr(v float64) *float64 { return &v }

func (r *modelRun) captureOpenAI(res openAIChatResult) {
	if res.ID != "" {
		r.lastID = res.ID
	}
	if res.Model != "" {
		r.lastModel = res.Model
	}
	if res.FinishReason != "" {
		r.lastFinish = res.FinishReason
	}
	if res.Content != "" {
		r.lastText = res.Content
	}
	if res.Reasoning != "" {
		r.lastReason = res.Reasoning
	}
	if res.Usage.Reported {
		r.lastUsage = res.Usage
	}
}

func (r *modelRun) captureClaude(res claudeResult) {
	if res.ID != "" {
		r.lastID = res.ID
	}
	if res.Model != "" {
		r.lastModel = res.Model
	}
	if res.StopReason != "" {
		r.lastFinish = res.StopReason
	}
	if res.Text != "" {
		r.lastText = res.Text
	}
	if res.Usage.Reported {
		r.lastUsage = res.Usage
	}
	r.lastSignatures = res.Signatures
	r.lastThinking = res.Thinking
}

func (r *modelRun) captureGemini(res geminiResult) {
	if res.ResponseID != "" {
		r.lastID = res.ResponseID
	}
	if res.ModelVersion != "" {
		r.lastModel = res.ModelVersion
	}
	if res.FinishReason != "" {
		r.lastFinish = res.FinishReason
	}
	if res.Text != "" {
		r.lastText = res.Text
	}
	if res.Usage.Reported {
		r.lastUsage = res.Usage
	}
	r.lastThoughtSigs = res.ThoughtSignatures
	r.lastThoughtParts = res.ThoughtParts
	r.lastGrounding = res.Grounding
	r.lastWebQueries = res.WebQueries
}

func decodeLooseJSON(text string) (map[string]any, bool, error) {
	s := strings.TrimSpace(text)
	strict := strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}")
	if strings.HasPrefix(s, "```") {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
		s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "```"))
		strict = false
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(s), &out); err == nil {
		return out, strict, nil
	}
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start >= 0 && end > start {
		if err := json.Unmarshal([]byte(s[start:end+1]), &out); err == nil {
			return out, false, nil
		}
	}
	return nil, false, fmt.Errorf("输出不是合法 JSON: %s", truncateText(s, 240))
}

func endpointSourceHint(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}
	host := strings.ToLower(u.Host)
	path := strings.ToLower(u.Path)
	switch {
	case host == "api.openai.com" || strings.HasSuffix(host, ".openai.com"):
		return "OpenAI 官方"
	case strings.Contains(host, "openai.azure.com") || strings.Contains(host, "services.ai.azure.com"):
		return "Microsoft Azure"
	case host == "api.anthropic.com" || strings.HasSuffix(host, ".anthropic.com"):
		return "Anthropic 官方"
	case strings.Contains(host, "generativelanguage.googleapis.com"):
		return "Google AI Studio"
	case strings.Contains(host, "aiplatform.googleapis.com"):
		return "Google Vertex AI"
	case strings.Contains(host, "bedrock") && strings.Contains(host, "amazonaws.com"):
		return "Amazon Bedrock"
	case strings.Contains(path, "/openai/deployments/"):
		return "Microsoft Azure"
	}
	return ""
}
