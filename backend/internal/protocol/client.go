package protocol

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"apiunit/internal/model"
)

const defaultMaxBody = 32 << 20

type ClientOptions struct {
	DialTimeout           time.Duration
	TLSHandshakeTimeout   time.Duration
	ResponseHeaderTimeout time.Duration
	DiscoverTimeout       time.Duration
	DiscoverTotalTimeout  time.Duration
	DiscoverRetries       int
}

type Client struct {
	http    *http.Client
	maxBody int64

	discoverTimeout      time.Duration
	discoverTotalTimeout time.Duration
	discoverRetries      int
}

func NewClient() *Client { return NewClientWithOptions(ClientOptions{}) }

func NewClientWithOptions(o ClientOptions) *Client {
	if o.DialTimeout <= 0 {
		o.DialTimeout = 15 * time.Second
	}
	if o.TLSHandshakeTimeout <= 0 {
		o.TLSHandshakeTimeout = 30 * time.Second
	}
	if o.ResponseHeaderTimeout <= 0 {
		o.ResponseHeaderTimeout = 60 * time.Second
	}
	if o.DiscoverTimeout <= 0 {
		o.DiscoverTimeout = 45 * time.Second
	}
	if o.DiscoverTotalTimeout <= 0 {
		o.DiscoverTotalTimeout = 120 * time.Second
	}
	if o.DiscoverRetries < 0 {
		o.DiscoverRetries = 2
	}
	if o.DiscoverRetries > 10 {
		o.DiscoverRetries = 10
	}
	tr := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   o.DialTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          500,
		MaxIdleConnsPerHost:   200,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   o.TLSHandshakeTimeout,
		ResponseHeaderTimeout: o.ResponseHeaderTimeout,
		ExpectContinueTimeout: time.Second,
	}
	return &Client{
		http:                 &http.Client{Transport: tr},
		maxBody:              defaultMaxBody,
		discoverTimeout:      o.DiscoverTimeout,
		discoverTotalTimeout: o.DiscoverTotalTimeout,
		discoverRetries:      o.DiscoverRetries,
	}
}

// Do performs one chat completion call and normalizes the response.
func (c *Client) Do(ctx context.Context, t TargetInfo, req ChatRequest) (*Outcome, error) {
	out := &Outcome{}
	if strings.TrimSpace(t.BaseURL) == "" {
		return out, fmt.Errorf("Base URL 不能为空")
	}
	root, err := NormalizeBaseURL(t.BaseURL)
	if err != nil {
		return out, err
	}
	p := ResolveProtocol(t.Protocol, req.Model)
	out.Protocol = string(p)
	endpoint := Endpoint(root, p, req.Model, req.Stream)
	out.Endpoint = endpoint
	body, err := BuildBody(p, req)
	if err != nil {
		return out, err
	}
	out.RequestJSON = string(body)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return out, err
	}
	authHeaders(httpReq, t, p)

	start := time.Now()
	resp, err := c.http.Do(httpReq)
	if err != nil {
		out.LatencyMs = ElapsedMs(start)
		out.ErrorMessage = friendlyNetError(err)
		return out, err
	}
	defer resp.Body.Close()
	out.Status = resp.StatusCode
	out.Header = resp.Header
	out.RequestID = requestID(resp.Header)
	cr := &countingReader{rc: resp.Body}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(cr, 1<<20))
		out.ResponseBytes = cr.n
		out.ResponseJSON = string(data)
		out.LatencyMs = ElapsedMs(start)
		out.ErrorMessage, out.ErrorCode = ParseAPIError(data)
		if out.ErrorMessage == "" {
			snippet := strings.TrimSpace(string(data))
			if len(snippet) > 800 {
				snippet = snippet[:800]
			}
			out.ErrorMessage = fmt.Sprintf("HTTP %d %s", resp.StatusCode, snippet)
		}
		return out, nil
	}

	br := bufio.NewReaderSize(cr, 64*1024)
	peek, _ := br.Peek(64)
	isSSE := looksLikeSSE(peek)
	if isSSE {
		hasPayload, perr := parseStream(p, br, out, start)
		out.ResponseBytes = cr.n
		out.LatencyMs = ElapsedMs(start)
		if perr != nil && ctx.Err() != nil {
			out.ErrorMessage = friendlyNetError(ctx.Err())
			return out, ctx.Err()
		}
		if out.ErrorMessage == "" && perr != nil {
			out.ErrorMessage = perr.Error()
			if out.ErrorCode == "" {
				out.ErrorCode = "stream"
			}
			out.StreamError = true
		}
		out.OK = finalizeStream(out, hasPayload)
		return out, nil
	}

	data, readErr := io.ReadAll(io.LimitReader(br, c.maxBody+1))
	out.ResponseBytes = cr.n
	out.LatencyMs = ElapsedMs(start)
	if readErr != nil {
		out.ErrorMessage = readErr.Error()
		return out, readErr
	}
	if int64(len(data)) > c.maxBody {
		out.Truncated = true
		data = data[:c.maxBody]
	}
	out.ResponseJSON = string(data)
	hasPayload, perr := parseJSONPayload(p, data, out)
	if perr != nil {
		out.ErrorMessage = perr.Error()
		out.ErrorCode = "parse"
		out.OK = false
		return out, nil
	}
	out.OK = finalizeJSON(out, hasPayload)
	if out.OK {
		// Non-stream: TTFT is indistinguishable from total latency.
		out.TTFTMs = out.LatencyMs
	}
	return out, nil
}

// finalizeStream decides whether a 2xx streaming response counts as success.
// A usable stream must carry real model output and a normal terminal signal.
func finalizeStream(out *Outcome, hasPayload bool) bool {
	if out.ErrorMessage != "" {
		return false
	}
	if !hasPayload {
		out.ErrorCode = "protocol"
		out.ErrorMessage = "流式响应中没有可解析的内容"
		return false
	}
	if !out.HasContent {
		if out.Malformed > 0 {
			out.ErrorCode = "stream"
			out.StreamError = true
			out.ErrorMessage = fmt.Sprintf("流式响应包含 %d 个无法解析的数据块，且没有有效内容", out.Malformed)
			return false
		}
		out.ErrorCode, out.ErrorMessage = emptyOutcomeMessage(out.FinishReason)
		return false
	}
	if !out.Terminal {
		out.ErrorCode = "stream"
		out.StreamError = true
		out.ErrorMessage = "流式响应未正常结束（缺少终止标记）"
		return false
	}
	return true
}

// finalizeJSON decides whether a 2xx non-streaming response counts as success.
func finalizeJSON(out *Outcome, hasPayload bool) bool {
	if out.ErrorMessage != "" {
		return false
	}
	if !hasPayload {
		out.ErrorCode = "protocol"
		out.ErrorMessage = "响应中没有可解析的内容"
		return false
	}
	if !out.HasContent {
		out.ErrorCode, out.ErrorMessage = emptyOutcomeMessage(out.FinishReason)
		return false
	}
	return true
}

func emptyOutcomeMessage(finishReason string) (code, message string) {
	fr := strings.ToLower(strings.TrimSpace(finishReason))
	switch fr {
	case "safety", "recitation", "blocklist", "prohibited_content", "spii", "image_safety", "content_filter":
		return "blocked", "输出被安全策略拦截（finishReason=" + finishReason + "）"
	case "refusal":
		return "blocked", "模型拒绝输出（finishReason=" + finishReason + "）"
	}
	if finishReason == "" {
		return "empty", "上游返回空内容"
	}
	return "empty", "上游返回空内容（finishReason=" + finishReason + "）"
}

func looksLikeSSE(b []byte) bool {
	s := strings.TrimLeft(string(b), " \t\r\n")
	return strings.HasPrefix(s, "data:") || strings.HasPrefix(s, "event:") || strings.HasPrefix(s, ":")
}

type countingReader struct {
	rc io.ReadCloser
	n  int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.rc.Read(p)
	c.n += int64(n)
	return n, err
}

func friendlyNetError(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	low := strings.ToLower(s)
	switch {
	case strings.Contains(low, "context deadline exceeded"):
		return "请求超时"
	case strings.Contains(low, "context canceled"):
		return "请求已取消"
	case strings.Contains(low, "connection refused"):
		return "连接被拒绝"
	case strings.Contains(low, "no such host"):
		return "域名解析失败"
	case strings.Contains(low, "tls"):
		return "TLS 握手失败: " + s
	default:
		return s
	}
}

var requestIDHeaders = []string{
	"X-Oneapi-Request-Id", "X-Request-Id", "Request-Id", "X-Amzn-Requestid",
	"X-Amzn-Request-Id", "Cf-Ray", "X-Trace-Id",
}

func requestID(h http.Header) string {
	for _, k := range requestIDHeaders {
		if v := h.Get(k); v != "" {
			return v
		}
	}
	return ""
}

// ParseAPIError extracts a human-readable message and code from the various
// error envelopes used by OpenAI, Claude, Gemini and New API.
func ParseAPIError(body []byte) (message, code string) {
	var root map[string]any
	if json.Unmarshal(body, &root) != nil {
		// Some gateways return HTML or plain text for proxy-level errors.
		return sanitizeMessage(string(body)), ""
	}
	if v, ok := root["error"]; ok {
		switch e := v.(type) {
		case string:
			message = e
		case map[string]any:
			message = firstString(e, "message", "msg", "detail", "error_msg", "error_description")
			code = firstString(e, "code", "type", "status")
		}
	}
	if message == "" {
		message = firstString(root, "message", "msg", "detail", "error_msg", "error_description", "description")
	}
	if code == "" {
		code = firstString(root, "code", "message_key")
	}
	message = sanitizeMessage(message)
	return message, code
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch x := v.(type) {
			case string:
				if strings.TrimSpace(x) != "" {
					return x
				}
			case json.Number:
				return x.String()
			case float64:
				return fmt.Sprintf("%.0f", x)
			}
		}
	}
	return ""
}

func sanitizeMessage(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "<") {
		// Strip crude HTML error pages.
		var b strings.Builder
		inTag := false
		for _, r := range s {
			switch {
			case r == '<':
				inTag = true
			case r == '>':
				inTag = false
			case !inTag:
				b.WriteRune(r)
			}
		}
		s = strings.Join(strings.Fields(b.String()), " ")
	}
	if len([]rune(s)) > 1200 {
		r := []rune(s)
		s = string(r[:1200])
	}
	return s
}

func extractOpenAIContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var b strings.Builder
		for _, p := range parts {
			if p.Text != "" {
				b.WriteString(p.Text)
			}
		}
		return b.String()
	}
	return ""
}

var _ = model.ProtocolAuto
