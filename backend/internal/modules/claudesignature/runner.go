package claudesignature

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	mathrand "math/rand"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"apiunit/internal/engine"
	"apiunit/internal/model"
	"apiunit/internal/netguard"
	"apiunit/internal/protocol"
)

const (
	maxResponseBytes  = 16 << 20
	maxStoredResponse = 4 << 20
	maxStoredOutput   = 256 << 10

	// 中转站与 Cloudflare 常按 UA 拦截；与配套验收脚本保持一致，使用浏览器 UA。
	userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
)

var (
	adaptiveFamilyRe  = regexp.MustCompile(`claude[-_](?:opus|sonnet|haiku|fable|mythos)[-_](\d+)(?:[.-](\d+))?`)
	adaptiveVersionRe = regexp.MustCompile(`claude[-_](\d+)(?:[.-](\d+))?[-_](?:opus|sonnet|haiku)`)

	signatureHTTP = &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   20,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   30 * time.Second,
			ResponseHeaderTimeout: 5 * time.Minute,
			ExpectContinueTimeout: time.Second,
		},
		// API Key 会随请求头发送，禁止跨主机重定向和 HTTPS 降级。
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("重定向次数过多")
			}
			origin := via[0]
			if req.URL.Host != origin.URL.Host {
				return fmt.Errorf("拒绝跨主机重定向: %s -> %s", origin.URL.Host, req.URL.Host)
			}
			if origin.URL.Scheme == "https" && req.URL.Scheme != "https" {
				return fmt.Errorf("拒绝 HTTPS 降级重定向")
			}
			return nil
		},
	}
)

// plan 是一次 thinking 参数尝试。effort 只对支持的模型生效，失败后会被移除。
type plan struct {
	Thinking string
	Effort   string
}

type httpResult struct {
	Status    int
	Header    http.Header
	Body      []byte
	LatencyMs float64
	Endpoint  string
	Err       error
}

func (r httpResult) ok() bool { return r.Status >= 200 && r.Status < 300 }

type claudeResponse struct {
	ID         string          `json:"id"`
	Model      string          `json:"model"`
	Type       string          `json:"type"`
	Content    []claudeBlock   `json:"content"`
	StopReason string          `json:"stop_reason"`
	Usage      *claudeUsage    `json:"usage"`
	Error      *claudeAPIError `json:"error"`
}

type claudeBlock struct {
	Type      string `json:"type"`
	Text      string `json:"text"`
	Thinking  string `json:"thinking"`
	Signature string `json:"signature"`
	Data      string `json:"data"`
}

type claudeUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

type claudeAPIError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type requestOutcome struct {
	report RequestReport
	item   model.Item
	fatal  bool
}

// runModel 串行执行一个模型的所有采样请求；模型之间由 Module.Run 控制并发。
func runModel(ctx context.Context, sess *engine.Session, cfg *Config, job modelJob) (ModelReport, []RequestReport) {
	start := time.Now()
	displayName := job.label
	if displayName == "" {
		displayName = job.target.Name
	}
	rep := ModelReport{
		TargetID:   job.target.ID,
		TargetName: displayName,
		Model:      job.model,
		Protocol:   string(job.protocol),
		Endpoint:   job.endpoint,
		AuthMode:   job.authMode,
	}
	maker := newPromptMaker(cfg.Prompt)
	requests := make([]RequestReport, 0, cfg.Count)
	planIndex := 0
	sess.SetCurrentModel(job.model)
	sess.SetPhase("签名采样")

	for i := 1; i <= cfg.Count; i++ {
		if ctx.Err() != nil {
			break
		}
		prompt := maker.next(i)
		out, nextPlan, fatal := executeRequest(ctx, sess, cfg, job, i, prompt, planIndex)
		if nextPlan >= 0 && nextPlan < len(job.plans) {
			planIndex = nextPlan
		}
		sess.Record(out.item)
		sess.AddExtra("claudeSignatureDone", 1)
		requests = append(requests, out.report)
		if fatal {
			if out.report.Error != "" {
				rep.Error = out.report.Error
			}
			break
		}
		if ctx.Err() != nil {
			break
		}
		if cfg.DelayMs > 0 && i < cfg.Count {
			select {
			case <-time.After(time.Duration(cfg.DelayMs) * time.Millisecond):
			case <-ctx.Done():
			}
		}
	}
	rep.DurationMs = time.Since(start).Milliseconds()
	return rep, requests
}

// executeRequest 处理一次逻辑请求：参数回退、传输重试、响应解析、检查项生成。
// 返回最终使用的 plan 下标与是否遇到不可恢复错误（鉴权/模型不存在）。
func executeRequest(ctx context.Context, sess *engine.Session, cfg *Config, job modelJob, index int, prompt string, planIndex int) (requestOutcome, int, bool) {
	startedAt := time.Now()
	requestTime := startedAt.UnixMilli()
	if planIndex < 0 || planIndex >= len(job.plans) {
		planIndex = 0
	}

	var (
		res       httpResult
		usedPlan  plan
		body      []byte
		attempts  int
		fatal     bool
		planErr   error
		finalPlan = planIndex
	)
	for pi := planIndex; pi < len(job.plans); pi++ {
		usedPlan = job.plans[pi]
		body, planErr = buildClaudeBody(cfg, job.model, prompt, usedPlan)
		if planErr != nil {
			res = httpResult{Endpoint: job.endpoint, Err: planErr}
			finalPlan = pi
			break
		}
		var n int
		res, n = sendWithRetry(ctx, cfg, job, body)
		attempts += n
		finalPlan = pi
		if res.Err != nil || res.ok() {
			break
		}
		if res.Status == http.StatusUnauthorized || res.Status == http.StatusForbidden ||
			res.Status == http.StatusNotFound {
			fatal = true
			break
		}
		// 400 且错误信息与 thinking 参数相关时，尝试下一个回退方案。
		if res.Status == http.StatusBadRequest && isParamRejection(res) && pi+1 < len(job.plans) {
			continue
		}
		break
	}

	report, item := buildOutcome(ctx, sess, cfg, job, index, prompt, requestTime, usedPlan, res, body, attempts, fatal)
	return requestOutcome{report: report, item: item, fatal: fatal}, finalPlan, fatal
}

func buildClaudeBody(cfg *Config, modelName, prompt string, p plan) ([]byte, error) {
	body := map[string]any{
		"model":      modelName,
		"max_tokens": cfg.MaxTokens,
		"messages": []map[string]any{{
			"role":    "user",
			"content": prompt,
		}},
	}
	switch p.Thinking {
	case "adaptive":
		body["thinking"] = map[string]any{"type": "adaptive"}
	case "budget":
		budget := cfg.BudgetTokens
		if budget > cfg.MaxTokens-1 {
			budget = cfg.MaxTokens - 1
		}
		body["thinking"] = map[string]any{"type": "enabled", "budget_tokens": budget}
	}
	if p.Effort != "" {
		body["output_config"] = map[string]any{"effort": p.Effort}
	}
	return json.Marshal(body)
}

func sendWithRetry(ctx context.Context, cfg *Config, job modelJob, body []byte) (httpResult, int) {
	timeout := time.Duration(cfg.TimeoutMs) * time.Millisecond
	attempts := 0
	var last httpResult
	for attempt := 0; attempt <= cfg.Retry.MaxRetries; attempt++ {
		if attempt > 0 {
			delay := time.Duration(cfg.Retry.BackoffMs) * time.Millisecond
			for i := 1; i < attempt; i++ {
				delay *= 2
				if delay > time.Duration(cfg.Retry.MaxBackoffMs)*time.Millisecond {
					delay = time.Duration(cfg.Retry.MaxBackoffMs) * time.Millisecond
					break
				}
			}
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return httpResult{Endpoint: job.endpoint, Err: ctx.Err()}, attempts
			}
		}
		last = doHTTP(ctx, job, body, timeout)
		attempts++
		if last.Err != nil {
			if ctx.Err() != nil || !cfg.Retry.RetryOnNetwork {
				return last, attempts
			}
			continue
		}
		if !shouldRetryStatus(last.Status, cfg.Retry) {
			return last, attempts
		}
	}
	return last, attempts
}

func shouldRetryStatus(status int, r RetryConfig) bool {
	switch {
	case status == http.StatusTooManyRequests:
		return r.RetryOn429
	case status == http.StatusRequestTimeout, status == http.StatusConflict, status == 425:
		return r.RetryOn5xx
	case status >= 500:
		return r.RetryOn5xx
	}
	return false
}

func doHTTP(ctx context.Context, job modelJob, body []byte, timeout time.Duration) httpResult {
	out := httpResult{Endpoint: job.endpoint}
	if err := netguard.CheckURL(job.endpoint); err != nil {
		out.Err = err
		return out
	}
	actx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		actx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(actx, http.MethodPost, job.endpoint, bytes.NewReader(body))
	if err != nil {
		out.Err = err
		return out
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("User-Agent", userAgent)
	key := strings.TrimSpace(job.target.APIKey)
	if job.authMode == "bearer" {
		if isOAuthKey(key) {
			req.Header.Set("anthropic-beta", "oauth-2025-04-20")
		}
		if !strings.HasPrefix(strings.ToLower(key), "bearer ") {
			key = "Bearer " + key
		}
		req.Header.Set("Authorization", key)
	} else {
		req.Header.Set("x-api-key", key)
	}

	start := time.Now()
	resp, err := signatureHTTP.Do(req)
	out.LatencyMs = float64(time.Since(start).Microseconds()) / 1000.0
	if err != nil {
		out.Err = err
		return out
	}
	defer resp.Body.Close()
	out.Status = resp.StatusCode
	out.Header = resp.Header
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if int64(len(data)) > maxResponseBytes {
		data = data[:maxResponseBytes]
		out.Err = fmt.Errorf("响应体超过 %d 字节上限", maxResponseBytes)
		return out
	}
	out.Body = data
	if readErr != nil {
		out.Err = readErr
	}
	return out
}

func isParamRejection(res httpResult) bool {
	text := strings.ToLower(string(res.Body))
	for _, kw := range []string{"thinking", "budget_tokens", "adaptive", "output_config", "effort", "display"} {
		if strings.Contains(text, kw) {
			return true
		}
	}
	return false
}

func isOAuthKey(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	return strings.HasPrefix(k, "bearer sk-ant-oat") || strings.HasPrefix(k, "sk-ant-oat")
}

func requestID(h http.Header) (string, string) {
	for _, key := range []string{
		"X-Oneapi-Request-Id", "X-New-Api-Request-Id", "X-Request-Id",
		"Request-Id", "X-Trace-Id", "Cf-Ray",
	} {
		if v := strings.TrimSpace(h.Get(key)); v != "" {
			return v, key
		}
	}
	return "", ""
}

func buildOutcome(ctx context.Context, sess *engine.Session, cfg *Config, job modelJob, index int, prompt string,
	requestTime int64, usedPlan plan, res httpResult, requestBody []byte, attempts int, fatal bool) (RequestReport, model.Item) {

	requestIDValue, requestIDSource := requestID(res.Header)
	targetName := job.label
	if targetName == "" {
		targetName = job.target.Name
	}
	report := RequestReport{
		Index:        index,
		TargetID:     job.target.ID,
		TargetName:   targetName,
		Model:        job.model,
		Protocol:     string(job.protocol),
		Endpoint:     job.endpoint,
		RequestID:    requestIDValue,
		HTTPStatus:   res.Status,
		LatencyMs:    res.LatencyMs,
		RequestTime:  requestTime,
		Prompt:       prompt,
		Attempts:     attempts,
		Verdict:      VerdictError,
		VerdictLabel: verdictLabel(VerdictError),
	}
	item := model.Item{
		TargetID:     job.target.ID,
		TargetName:   targetName,
		Model:        job.model,
		Protocol:     string(job.protocol),
		CaseID:       "signature",
		CaseName:     "thinking 签名解析",
		Category:     "signature",
		Severity:     "info",
		Repetition:   index,
		Status:       res.Status,
		LatencyMs:    res.LatencyMs,
		TTFTMs:       res.LatencyMs,
		RequestID:    requestIDValue,
		Attempts:     attempts,
		StartedAt:    requestTime,
		RequestJSON:  string(requestBody),
		ResponseJSON: truncateString(string(res.Body), maxStoredResponse),
		Meta: map[string]any{
			"claudeSignature":  true,
			"requestIndex":     index,
			"signatureVerdict": VerdictError,
			"planThinking":     usedPlan.Thinking,
			"planEffort":       usedPlan.Effort,
			"requestIdSource":  requestIDSource,
			"endpoint":         job.endpoint,
			"httpStatus":       res.Status,
			"attempts":         attempts,
			"fatal":            fatal,
		},
	}

	if res.Err != nil {
		errorType := "network"
		message := friendlyError(res.Err)
		switch {
		case errors.Is(res.Err, context.Canceled):
			errorType = "canceled"
			message = "请求已取消"
		case protocol.IsTimeout(res.Err):
			errorType = "timeout"
		}
		report.ErrorType = errorType
		report.Error = message
		item.ErrorType = errorType
		item.ErrorMessage = message
		report.Checks = []CheckReport{{
			ID: "http", Name: "请求成功", Status: CheckFail, Evidence: report.Error,
		}}
		item.Checks = itemChecks(report.Checks)
		return report, finalizeItem(item, report)
	}

	if !res.ok() {
		message, code := protocol.ParseAPIError(res.Body)
		if message == "" {
			message = fmt.Sprintf("HTTP %d", res.Status)
		}
		report.ErrorType = classifyStatus(res.Status, code, message)
		report.Error = message
		report.Checks = []CheckReport{{
			ID: "http", Name: "请求成功", Status: CheckFail,
			Evidence: fmt.Sprintf("HTTP %d: %s", res.Status, message),
		}}
		item.OK = false
		item.ErrorType = report.ErrorType
		item.ErrorMessage = message
		item.Checks = itemChecks(report.Checks)
		return report, finalizeItem(item, report)
	}

	var parsed claudeResponse
	trimmed := bytes.TrimSpace(res.Body)
	if bytes.HasPrefix(trimmed, []byte("data:")) || bytes.HasPrefix(trimmed, []byte("event:")) {
		report.ErrorType = "protocol"
		report.Error = "上游返回了流式响应；本模块需要非流式 JSON 响应"
		report.Checks = []CheckReport{{
			ID: "response", Name: "响应解析", Status: CheckFail, Evidence: report.Error,
		}}
		item.ErrorType = "protocol"
		item.ErrorMessage = report.Error
		item.Checks = itemChecks(report.Checks)
		return report, finalizeItem(item, report)
	}
	if err := json.Unmarshal(res.Body, &parsed); err != nil {
		report.ErrorType = "parse"
		report.Error = "Claude 响应解析失败: " + err.Error()
		report.Checks = []CheckReport{{
			ID: "response", Name: "响应解析", Status: CheckFail, Evidence: report.Error,
		}}
		item.ErrorType = "parse"
		item.ErrorMessage = report.Error
		item.Checks = itemChecks(report.Checks)
		return report, finalizeItem(item, report)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		report.ErrorType = "api"
		report.Error = parsed.Error.Message
		report.Checks = []CheckReport{{
			ID: "response", Name: "响应解析", Status: CheckFail,
			Evidence: parsed.Error.Type + ": " + parsed.Error.Message,
		}}
		item.ErrorType = "api"
		item.ErrorMessage = parsed.Error.Message
		item.Checks = itemChecks(report.Checks)
		return report, finalizeItem(item, report)
	}

	item.OK = true
	report.ResponseID = parsed.ID
	report.ResponseModel = parsed.Model
	report.StopReason = parsed.StopReason
	item.ResponseModel = parsed.Model
	item.Meta["responseId"] = parsed.ID
	item.FinishReason = parsed.StopReason
	if parsed.Usage != nil {
		promptTokens := parsed.Usage.InputTokens + parsed.Usage.CacheReadInputTokens + parsed.Usage.CacheCreationInputTokens
		report.Usage = Usage{
			InputTokens:      parsed.Usage.InputTokens,
			OutputTokens:     parsed.Usage.OutputTokens,
			TotalTokens:      promptTokens + parsed.Usage.OutputTokens,
			CacheReadTokens:  parsed.Usage.CacheReadInputTokens,
			CacheWriteTokens: parsed.Usage.CacheCreationInputTokens,
		}
		item.PromptTokens = promptTokens
		item.CompletionTokens = parsed.Usage.OutputTokens
		item.TotalTokens = promptTokens + parsed.Usage.OutputTokens
		item.CachedTokens = parsed.Usage.CacheReadInputTokens
		item.CacheWriteTokens = parsed.Usage.CacheCreationInputTokens
		item.TokenSource = "usage"
	} else {
		item.TokenSource = "none"
	}
	item.Cost = sess.EstimateCost(job.model, item.PromptTokens, item.CompletionTokens, item.CachedTokens, item.CacheWriteTokens)
	report.Cost = item.Cost
	if parsed.Model != "" {
		item.Meta["upstreamModel"] = parsed.Model
	}

	analysis := analyzeResponse(job.model, requestTime, parsed.Content)
	report.Blocks = analysis.blocks
	report.Signatures = analysis.signatures
	report.Checks = analysis.checks
	report.Verdict = analysis.verdict
	report.VerdictLabel = verdictLabel(analysis.verdict)
	item.OutputText = truncateString(analysis.outputText, maxStoredOutput)
	item.OutputPreview = previewText(analysis.outputText, 600)
	item.Verdict = analysis.verdict
	item.Checks = itemChecks(analysis.checks)
	item.Meta["signatureVerdict"] = analysis.verdict
	item.Meta["signatureCount"] = len(analysis.signatures)
	item.Meta["parsedCount"] = analysis.parsed
	item.Meta["layout"] = analysis.layouts
	item.Meta["embeddedModel"] = analysis.embeddedModel
	item.Meta["embeddedBlockType"] = analysis.embeddedBlockType
	item.Meta["signedAtUtc8"] = analysis.signedAt
	item.Meta["signatureWarnings"] = analysis.warningCount
	if analysis.parseError != "" {
		item.Meta["parseError"] = analysis.parseError
	}
	if int64(len(res.Body)) > maxStoredResponse {
		item.Meta["truncated"] = true
	}
	return report, finalizeItem(item, report)
}

// finalizeItem 统一补齐由报告派生的字段，避免各错误分支重复赋值。
func finalizeItem(item model.Item, report RequestReport) model.Item {
	item.Verdict = report.Verdict
	if item.Checks == nil {
		item.Checks = itemChecks(report.Checks)
	}
	if item.Meta == nil {
		item.Meta = map[string]any{}
	}
	item.Meta["signatureVerdict"] = report.Verdict
	return item
}

func itemChecks(checks []CheckReport) []model.CheckResult {
	out := make([]model.CheckResult, 0, len(checks))
	for _, c := range checks {
		out = append(out, model.CheckResult{
			Check:    model.Check{Kind: "claude_signature", Value: c.ID, Description: c.Name},
			Matched:  c.Status == CheckPass || c.Status == CheckWarn,
			Evidence: c.Evidence,
			Decisive: c.Status == CheckFail,
		})
	}
	return out
}

type responseAnalysis struct {
	blocks            []Block
	signatures        []SignatureInfo
	checks            []CheckReport
	verdict           string
	outputText        string
	parsed            int
	layouts           string
	embeddedModel     string
	embeddedBlockType string
	signedAt          string
	warningCount      int
	parseError        string
}

func analyzeResponse(requestedModel string, requestTime int64, content []claudeBlock) responseAnalysis {
	a := responseAnalysis{}
	thinkingText := strings.Builder{}
	textOut := strings.Builder{}
	var thinkingBlocks, redactedBlocks int
	var thinkingWithoutSignature bool
	var parseErrors []string
	var parsedInfos []*SignatureInfo
	var sourceTypes = map[int]string{}
	layoutSet := map[string]bool{}

	a.checks = append(a.checks, CheckReport{ID: "http", Name: "请求成功", Status: CheckPass, Evidence: "HTTP 2xx"})

	for _, block := range content {
		summary := Block{Type: block.Type}
		switch block.Type {
		case "text":
			summary.TextChars = len([]rune(block.Text))
			textOut.WriteString(block.Text)
		case "thinking":
			thinkingBlocks++
			summary.TextChars = len([]rune(block.Thinking))
			summary.HasSignature = strings.TrimSpace(block.Signature) != ""
			thinkingText.WriteString(block.Thinking)
			if summary.HasSignature {
				info, err := analyzeSignature(block.Signature, fmt.Sprintf("第 %d 个 thinking 签名", len(a.signatures)+1))
				if err != nil {
					parseErrors = append(parseErrors, err.Error())
					a.signatures = append(a.signatures, SignatureInfo{
						BlockType:       "thinking",
						SignatureChars:  len(strings.TrimSpace(block.Signature)),
						SignaturePrefix: prefix(strings.TrimSpace(block.Signature), 64),
						ParseError:      err.Error(),
					})
				} else {
					info.BlockType = "thinking"
					a.signatures = append(a.signatures, *info)
					parsedInfos = append(parsedInfos, info)
					sourceTypes[len(a.signatures)-1] = "thinking"
					layoutSet[info.Layout] = true
					a.parsed++
					if info.EmbeddedBlockType != "" && a.embeddedBlockType == "" {
						a.embeddedBlockType = info.EmbeddedBlockType
					}
					if info.Model != "" && a.embeddedModel == "" {
						a.embeddedModel = info.Model
					}
					if info.SignedAtUTC8 != "" && a.signedAt == "" {
						a.signedAt = info.SignedAtUTC8
					}
					a.warningCount += len(info.Warnings)
				}
			} else {
				thinkingWithoutSignature = true
			}
		case "redacted_thinking":
			redactedBlocks++
			summary.HasSignature = strings.TrimSpace(block.Data) != ""
			if summary.HasSignature {
				compact, err := compactBase64(block.Data, "redacted_thinking")
				if err != nil {
					parseErrors = append(parseErrors, err.Error())
					a.signatures = append(a.signatures, SignatureInfo{
						BlockType:       "redacted_thinking",
						SignaturePrefix: prefix(strings.TrimSpace(block.Data), 64),
						ParseError:      err.Error(),
					})
				} else {
					a.signatures = append(a.signatures, SignatureInfo{
						BlockType:       "redacted_thinking",
						SignatureChars:  len(compact),
						ProtoBytes:      base64DecodedLen(compact),
						Layout:          LayoutOpaque,
						Opaque:          true,
						SignaturePrefix: prefix(compact, 64),
					})
					layoutSet[LayoutOpaque] = true
				}
			}
		default:
			if block.Type != "" {
				summary.TextChars = len([]rune(block.Text))
			}
		}
		if block.Type != "" {
			a.blocks = append(a.blocks, summary)
		}
	}

	// thinking 块与签名存在性
	if thinkingBlocks+redactedBlocks == 0 {
		a.checks = append(a.checks, CheckReport{
			ID: "thinking_block", Name: "返回 thinking 块", Status: CheckNA,
			Evidence: "响应中没有 thinking / redacted_thinking 块",
		})
	} else {
		evidence := fmt.Sprintf("thinking=%d，redacted_thinking=%d", thinkingBlocks, redactedBlocks)
		a.checks = append(a.checks, CheckReport{
			ID: "thinking_block", Name: "返回 thinking 块", Status: CheckPass, Evidence: evidence,
		})
	}
	switch {
	case thinkingWithoutSignature:
		a.checks = append(a.checks, CheckReport{
			ID: "signature_present", Name: "签名存在", Status: CheckFail,
			Evidence: "thinking 块没有 signature 字段",
		})
	case len(parsedInfos) > 0:
		a.checks = append(a.checks, CheckReport{
			ID: "signature_present", Name: "签名存在", Status: CheckPass,
			Evidence: fmt.Sprintf("解析到 %d 个 thinking 签名", len(parsedInfos)),
		})
	case redactedBlocks > 0:
		a.checks = append(a.checks, CheckReport{
			ID: "signature_present", Name: "签名存在", Status: CheckNA,
			Evidence: "仅返回 redacted_thinking，内容按设计不透明",
		})
	default:
		a.checks = append(a.checks, CheckReport{
			ID: "signature_present", Name: "签名存在", Status: CheckNA,
			Evidence: "没有可解析的 thinking 签名",
		})
	}

	// base64 / protobuf
	if len(parseErrors) > 0 {
		base64Failed := false
		protobufFailed := false
		for _, msg := range parseErrors {
			if strings.Contains(msg, "base64") {
				base64Failed = true
			} else if strings.Contains(msg, "protobuf") {
				protobufFailed = true
			} else {
				base64Failed = true
			}
		}
		a.parseError = strings.Join(parseErrors, "；")
		if base64Failed {
			a.checks = append(a.checks, CheckReport{ID: "base64", Name: "Base64 解码", Status: CheckFail, Evidence: a.parseError})
		} else {
			a.checks = append(a.checks, CheckReport{ID: "base64", Name: "Base64 解码", Status: CheckPass})
		}
		if protobufFailed {
			a.checks = append(a.checks, CheckReport{ID: "protobuf", Name: "Protobuf 解析", Status: CheckFail, Evidence: a.parseError})
		} else {
			a.checks = append(a.checks, CheckReport{ID: "protobuf", Name: "Protobuf 解析", Status: CheckPass})
		}
	} else if len(parsedInfos) > 0 {
		a.checks = append(a.checks,
			CheckReport{ID: "base64", Name: "Base64 解码", Status: CheckPass},
			CheckReport{ID: "protobuf", Name: "Protobuf 解析", Status: CheckPass},
		)
	} else {
		a.checks = append(a.checks,
			CheckReport{ID: "base64", Name: "Base64 解码", Status: CheckNA},
			CheckReport{ID: "protobuf", Name: "Protobuf 解析", Status: CheckNA},
		)
	}

	// 元数据识别
	recognized := 0
	for _, info := range parsedInfos {
		if info.Recognized {
			recognized++
		}
	}
	switch {
	case len(parsedInfos) == 0:
		a.checks = append(a.checks, CheckReport{ID: "metadata", Name: "识别元数据", Status: CheckNA})
	case recognized == 0:
		a.checks = append(a.checks, CheckReport{
			ID: "metadata", Name: "识别元数据", Status: CheckWarn,
			Evidence: "protobuf 可解析，但未识别出 model/block/uuid/时间戳；可能是未知布局或加密载荷",
		})
	default:
		a.checks = append(a.checks, CheckReport{
			ID: "metadata", Name: "识别元数据", Status: CheckPass,
			Evidence: fmt.Sprintf("%d/%d 个签名识别出元数据", recognized, len(parsedInfos)),
		})
	}

	// block type 一致性
	blockStatus, blockEvidence := compareBlockTypes(a.signatures, sourceTypes)
	if blockStatus != "" {
		a.checks = append(a.checks, CheckReport{ID: "block_type", Name: "块类型一致", Status: blockStatus, Evidence: blockEvidence})
	}

	// 模型一致性
	modelStatus, modelEvidence := compareModels(requestedModel, parsedInfos)
	if modelStatus != "" {
		a.checks = append(a.checks, CheckReport{ID: "model", Name: "模型一致", Status: modelStatus, Evidence: modelEvidence})
	}

	// 时间戳合理性
	tsStatus, tsEvidence := compareTimestamps(requestTime, parsedInfos)
	if tsStatus != "" {
		a.checks = append(a.checks, CheckReport{ID: "timestamp", Name: "签名时间合理", Status: tsStatus, Evidence: tsEvidence})
	}

	// 汇总布局
	layouts := make([]string, 0, len(layoutSet))
	for _, name := range []string{LayoutFull, LayoutMinimal, LayoutUnknown, LayoutOpaque} {
		if layoutSet[name] {
			layouts = append(layouts, name)
		}
	}
	a.layouts = strings.Join(layouts, ",")

	// 输出文本：优先展示 thinking 内容，其次普通文本。
	if thinkingText.Len() > 0 {
		a.outputText = thinkingText.String()
	} else {
		a.outputText = textOut.String()
	}

	// 判定
	if hasFail(a.checks) {
		a.verdict = VerdictSuspicious
	} else if len(parsedInfos) == 0 || recognized == 0 {
		a.verdict = VerdictInconclusive
	} else {
		a.verdict = VerdictPass
	}
	return a
}

func compareBlockTypes(signatures []SignatureInfo, sourceTypes map[int]string) (string, string) {
	var fails, warns, passes []string
	checked := 0
	for i, info := range signatures {
		if info.Opaque || info.ParseError != "" || info.EmbeddedBlockType == "" {
			continue
		}
		checked++
		source := sourceTypes[i]
		if source == "" {
			source = info.BlockType
		}
		if info.EmbeddedBlockType == source {
			passes = append(passes, info.EmbeddedBlockType)
			continue
		}
		msg := fmt.Sprintf("响应块为 %s，签名内为 %s", source, info.EmbeddedBlockType)
		if info.BlockTypeSource == "search" {
			warns = append(warns, msg)
		} else {
			fails = append(fails, msg)
		}
	}
	switch {
	case len(fails) > 0:
		return CheckFail, strings.Join(fails, "；")
	case len(warns) > 0:
		return CheckWarn, strings.Join(warns, "；")
	case checked > 0:
		return CheckPass, strings.Join(uniqueStrings(passes), ",")
	}
	return CheckNA, "签名中没有 block type"
}

func compareModels(requested string, infos []*SignatureInfo) (string, string) {
	var fails, warns, passes []string
	checked := 0
	for _, info := range infos {
		if info.Model == "" {
			continue
		}
		checked++
		status, evidence := modelMatch(requested, info.Model, info.ModelSource)
		switch status {
		case CheckFail:
			fails = append(fails, evidence)
		case CheckWarn:
			warns = append(warns, evidence)
		default:
			passes = append(passes, evidence)
		}
	}
	switch {
	case len(fails) > 0:
		return CheckFail, strings.Join(uniqueStrings(fails), "；")
	case len(warns) > 0:
		return CheckWarn, strings.Join(uniqueStrings(warns), "；")
	case checked > 0:
		return CheckPass, strings.Join(uniqueStrings(passes), "；")
	}
	return CheckNA, "签名中没有模型名"
}

func modelMatch(requested, embedded, source string) (string, string) {
	r := normalizeModel(requested)
	e := normalizeModel(embedded)
	evidence := fmt.Sprintf("请求 %s / 签名 %s", requested, embedded)
	if r == e {
		return CheckPass, evidence
	}
	// 日期后缀别名：claude-sonnet-4-5 与 claude-sonnet-4-5-20250929 视为同一模型。
	if strings.HasPrefix(r, e+"-") && isDateSuffix(strings.TrimPrefix(r, e+"-")) {
		return CheckPass, evidence + "（日期别名）"
	}
	if strings.HasPrefix(e, r+"-") && isDateSuffix(strings.TrimPrefix(e, r+"-")) {
		return CheckPass, evidence + "（日期别名）"
	}
	if sameFamily(r, e) {
		return CheckWarn, evidence + "（同家族但版本不同）"
	}
	if source == "search" {
		return CheckWarn, evidence + "（模型名来自全树搜索，置信度较低）"
	}
	return CheckFail, evidence
}

func normalizeModel(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(s, "models/")
	// 中转站常带厂商前缀，例如 anthropic/claude-opus-5-5；比较时只看末级模型名。
	if i := strings.LastIndexAny(s, "/:"); i >= 0 {
		s = s[i+1:]
	}
	s = strings.ReplaceAll(s, "_", "-")
	return s
}

func isDateSuffix(s string) bool {
	if len(s) != 8 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func sameFamily(a, b string) bool {
	if !strings.Contains(a, "claude") || !strings.Contains(b, "claude") {
		return false
	}
	for _, family := range []string{"opus", "sonnet", "haiku", "fable", "mythos"} {
		if strings.Contains(a, family) && strings.Contains(b, family) {
			return true
		}
	}
	return false
}

func compareTimestamps(requestTime int64, infos []*SignatureInfo) (string, string) {
	var fails, warns, passes []string
	checked := 0
	for _, info := range infos {
		if info.UnixTimestamp <= 0 {
			continue
		}
		checked++
		delta := float64(info.UnixTimestamp*1000-requestTime) / 1000.0
		evidence := fmt.Sprintf("签名时间 %s，与请求时间相差 %s", info.SignedAtUTC8, humanDuration(delta*1000))
		abs := math.Abs(delta)
		switch {
		case abs <= 300:
			passes = append(passes, evidence)
		case abs <= 86400:
			warns = append(warns, evidence)
		case info.TimestampSource == "field":
			fails = append(fails, evidence)
		default:
			warns = append(warns, evidence)
		}
	}
	switch {
	case len(fails) > 0:
		return CheckFail, strings.Join(uniqueStrings(fails), "；")
	case len(warns) > 0:
		return CheckWarn, strings.Join(uniqueStrings(warns), "；")
	case checked > 0:
		return CheckPass, strings.Join(uniqueStrings(passes), "；")
	}
	return CheckNA, "签名中没有时间戳"
}

func hasFail(checks []CheckReport) bool {
	for _, c := range checks {
		if c.Status == CheckFail {
			return true
		}
	}
	return false
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func classifyStatus(status int, code, message string) string {
	lower := strings.ToLower(code + " " + message)
	switch {
	case status == 401 || status == 403:
		if strings.Contains(lower, "quota") {
			return "quota"
		}
		return "auth"
	case status == 429:
		return "rate_limit"
	case status == 404:
		return "not_found"
	case status == 400 || status == 422:
		return "bad_request"
	case status >= 500:
		return "server"
	case status == 0:
		return "network"
	}
	return "http"
}

func friendlyError(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if protocol.IsTimeout(err) {
		return "请求超时: " + s
	}
	return s
}

func base64DecodedLen(compact string) int {
	if compact == "" {
		return 0
	}
	padded := compact
	if rem := len(padded) % 4; rem != 0 {
		padded += strings.Repeat("=", 4-rem)
	}
	padded = strings.ReplaceAll(padded, "-", "+")
	padded = strings.ReplaceAll(padded, "_", "/")
	data, err := base64.StdEncoding.DecodeString(padded)
	if err != nil {
		return 0
	}
	return len(data)
}

func truncateString(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n]
}

func previewText(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// ---------- 提示词生成 ----------

var promptTemplates = []string{
	"Compute {a} * {b} + {c} and show every step of your arithmetic.",
	"A shop sells {n} items at {p} units each. After a {d}% discount, what is the total? Show the arithmetic.",
	"What is the sum of all integers from 1 to {n}? Explain the formula you used.",
	"Suppose today is {weekday}. What weekday is it {k} days from now? Explain how you counted.",
	"Which is larger, {a}/{b} or {c}/{d}? Show the cross-multiplication.",
	"A train covers {d} km in {h} hours. What is its average speed in metres per second? Show each conversion.",
	"Solve for x: {a}x + {b} = {c}. Show the isolation steps.",
	"How many distinct permutations does the word '{word}' have? Explain the counting argument.",
	"A rectangle is {a} cm by {b} cm. What is the length of its diagonal, rounded to two decimals? Show the working.",
	"A population of {n} grows by {d}% each year for {y} years. What is the final size? Show the calculation.",
}

var promptWords = []string{"banana", "success", "tomorrow", "committee", "mississippi", "parallel", "bookkeeper", "assess"}
var promptWeekdays = []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}

type promptMaker struct {
	rng    *mathrand.Rand
	custom string
	seen   map[string]bool
}

func newPromptMaker(custom string) *promptMaker {
	var seedBytes [8]byte
	_, _ = rand.Read(seedBytes[:])
	seed := time.Now().UnixNano() ^ int64(binary.LittleEndian.Uint64(seedBytes[:]))
	return &promptMaker{
		rng:    mathrand.New(mathrand.NewSource(seed)),
		custom: strings.TrimSpace(custom),
		seen:   map[string]bool{},
	}
}

func (p *promptMaker) next(index int) string {
	for i := 0; i < 64; i++ {
		nonce := randomHex(6)
		base := p.custom
		if base == "" {
			base = p.template()
		}
		prompt := fmt.Sprintf("[ref %s / request %d] %s", nonce, index, base)
		if !p.seen[prompt] {
			p.seen[prompt] = true
			return prompt
		}
	}
	return fmt.Sprintf("[ref %s / request %d] %s", randomHex(6), index, p.template())
}

func (p *promptMaker) template() string {
	values := map[string]any{
		"a":       p.rng.Intn(87) + 11,
		"b":       p.rng.Intn(17) + 3,
		"c":       p.rng.Intn(88) + 2,
		"d":       p.rng.Intn(8) + 2,
		"h":       p.rng.Intn(8) + 2,
		"k":       p.rng.Intn(391) + 10,
		"n":       p.rng.Intn(884) + 17,
		"p":       p.rng.Intn(43) + 3,
		"y":       p.rng.Intn(11) + 2,
		"word":    promptWords[p.rng.Intn(len(promptWords))],
		"weekday": promptWeekdays[p.rng.Intn(len(promptWeekdays))],
	}
	tpl := promptTemplates[p.rng.Intn(len(promptTemplates))]
	return strings.NewReplacer(
		"{a}", fmt.Sprint(values["a"]),
		"{b}", fmt.Sprint(values["b"]),
		"{c}", fmt.Sprint(values["c"]),
		"{d}", fmt.Sprint(values["d"]),
		"{h}", fmt.Sprint(values["h"]),
		"{k}", fmt.Sprint(values["k"]),
		"{n}", fmt.Sprint(values["n"]),
		"{p}", fmt.Sprint(values["p"]),
		"{y}", fmt.Sprint(values["y"]),
		"{word}", fmt.Sprint(values["word"]),
		"{weekday}", fmt.Sprint(values["weekday"]),
	).Replace(tpl)
}

func randomHex(n int) string {
	raw := make([]byte, (n+1)/2)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	s := hex.EncodeToString(raw)
	if len(s) > n {
		s = s[:n]
	}
	return s
}
