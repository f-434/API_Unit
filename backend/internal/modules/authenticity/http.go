package authenticity

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"apiunit/internal/model"
	"apiunit/internal/protocol"
)

const defaultAzureAPIVersion = "2024-10-21"

var rawClient = &http.Client{
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 4 * time.Minute,
		ExpectContinueTimeout: time.Second,
	},
}

type httpResult struct {
	Status       int
	Header       http.Header
	Body         []byte
	RequestJSON  string
	ResponseJSON string
	LatencyMs    float64
	Endpoint     string
	Err          error
}

func (r httpResult) statusOK() bool { return r.Status >= 200 && r.Status < 300 }

func resolveProtocol(target model.TargetSecret, spec TargetSpec, modelName string) model.Protocol {
	if p := strings.ToLower(strings.TrimSpace(spec.Protocol)); p != "" && p != "auto" {
		return model.Protocol(p)
	}
	if target.Protocol != "" && target.Protocol != model.ProtocolAuto {
		return target.Protocol
	}
	return model.InferProtocol(modelName)
}

func isAzureEndpoint(base string) bool {
	u, err := url.Parse(base)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Host)
	return strings.Contains(host, "openai.azure.com") ||
		strings.Contains(host, "services.ai.azure.com") ||
		strings.Contains(host, "cognitiveservices.azure.com") ||
		strings.Contains(host, "azure-api.net")
}

func resolveEndpoint(target model.TargetSecret, spec TargetSpec, protocolName model.Protocol, modelName, kind string) (string, error) {
	root, err := protocol.NormalizeBaseURL(target.BaseURL)
	if err != nil {
		return "", err
	}
	azure := isAzureEndpoint(root)
	apiVersion := strings.TrimSpace(spec.APIVersion)
	if apiVersion == "" {
		apiVersion = defaultAzureAPIVersion
	}
	escaped := url.PathEscape(modelName)
	switch protocolName {
	case model.ProtocolClaude:
		return root + "/v1/messages", nil
	case model.ProtocolGemini:
		return root + "/v1beta/models/" + escaped + ":generateContent", nil
	default:
		if azure {
			prefix := strings.TrimSuffix(root, "/openai")
			if kind == "responses" {
				return prefix + "/openai/deployments/" + escaped + "/responses?api-version=" + url.QueryEscape(apiVersion), nil
			}
			return prefix + "/openai/deployments/" + escaped + "/chat/completions?api-version=" + url.QueryEscape(apiVersion), nil
		}
		if kind == "responses" {
			return root + "/v1/responses", nil
		}
		return root + "/v1/chat/completions", nil
	}
}

func authHeaders(target model.TargetSecret, spec TargetSpec, protocolName model.Protocol, endpoint string) map[string]string {
	key := strings.TrimSpace(target.APIKey)
	mode := strings.ToLower(strings.TrimSpace(spec.AuthMode))
	h := map[string]string{"Content-Type": "application/json"}
	azure := isAzureEndpoint(endpoint)
	switch protocolName {
	case model.ProtocolClaude:
		h["anthropic-version"] = "2023-06-01"
		if mode == "bearer" || looksLikeOAuth(key) {
			h["Authorization"] = bearerValue(key)
			h["anthropic-beta"] = "oauth-2025-04-20"
		} else {
			h["x-api-key"] = key
		}
	case model.ProtocolGemini:
		if mode == "bearer" {
			h["Authorization"] = bearerValue(key)
		} else {
			h["x-goog-api-key"] = key
		}
	default:
		if azure && mode != "bearer" && !looksLikeJWT(key) {
			h["api-key"] = key
		} else {
			h["Authorization"] = bearerValue(key)
		}
	}
	return h
}

func bearerValue(key string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(key)), "bearer ") {
		return key
	}
	return "Bearer " + key
}

func looksLikeJWT(key string) bool {
	return strings.HasPrefix(strings.TrimSpace(key), "eyJ")
}

func looksLikeOAuth(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	return strings.HasPrefix(k, "bearer ") || strings.HasPrefix(k, "sk-ant-oat")
}

func (r *modelRun) post(ctx context.Context, endpoint string, headers map[string]string, body any, timeout time.Duration) httpResult {
	return r.do(ctx, http.MethodPost, endpoint, headers, body, timeout)
}

func (r *modelRun) do(ctx context.Context, method, endpoint string, headers map[string]string, body any, timeout time.Duration) httpResult {
	out := httpResult{Endpoint: endpoint}
	var raw []byte
	var err error
	if body != nil {
		raw, err = json.Marshal(body)
		if err != nil {
			out.Err = err
			return out
		}
		out.RequestJSON = string(raw)
	}
	start := time.Now()
	actx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		actx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	var reader io.Reader
	if raw != nil {
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(actx, method, endpoint, reader)
	if err != nil {
		out.Err = err
		return out
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "apiunit-authenticity/1.0")
	resp, err := rawClient.Do(req)
	out.LatencyMs = float64(time.Since(start).Microseconds()) / 1000.0
	if err != nil {
		out.Err = err
		return out
	}
	defer resp.Body.Close()
	out.Status = resp.StatusCode
	out.Header = resp.Header
	limit := r.cfg.Thresholds.MaxResponseBytes
	if limit <= 0 {
		limit = 8 << 20
	}
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, limit))
	out.Body = data
	out.ResponseJSON = string(data)
	if readErr != nil && out.Err == nil {
		out.Err = readErr
	}
	return out
}

func (r *modelRun) postWithRetry(ctx context.Context, endpoint string, headers map[string]string, body any, timeout time.Duration) httpResult {
	attempts := r.cfg.Retry.MaxRetries + 1
	var last httpResult
	for i := 1; i <= attempts; i++ {
		last = r.post(ctx, endpoint, headers, body, timeout)
		if ctx.Err() != nil {
			return last
		}
		if last.Err == nil {
			if last.Status == http.StatusTooManyRequests ||
				(last.Status >= 500 && r.cfg.Retry.RetryOn5xx) ||
				(last.Status == 408 && r.cfg.Retry.RetryOn5xx) {
				// retry
			} else {
				return last
			}
		} else if !r.cfg.Retry.RetryOnNetwork {
			return last
		}
		if i == attempts {
			break
		}
		delay := time.Duration(r.cfg.Retry.BackoffMs) * time.Millisecond
		for k := 1; k < i; k++ {
			delay *= 2
			if delay > time.Duration(r.cfg.Retry.MaxBackoffMs)*time.Millisecond {
				delay = time.Duration(r.cfg.Retry.MaxBackoffMs) * time.Millisecond
				break
			}
		}
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return last
		}
	}
	return last
}

// ---------- loose JSON helpers ----------

func decodeObject(raw []byte) map[string]any {
	var m map[string]any
	if json.Unmarshal(raw, &m) == nil {
		return m
	}
	return nil
}

func getString(m map[string]any, path ...string) string {
	v := getPath(m, path...)
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	case float64:
		return fmt.Sprintf("%v", x)
	}
	return ""
}

func getFloat(m map[string]any, path ...string) float64 {
	v := getPath(m, path...)
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case json.Number:
		f, _ := x.Float64()
		return f
	}
	return 0
}

func getInt(m map[string]any, path ...string) int { return int(getFloat(m, path...)) }

func getBool(m map[string]any, path ...string) bool {
	switch v := getPath(m, path...).(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	}
	return false
}

func getMap(m map[string]any, path ...string) map[string]any {
	if v, ok := getPath(m, path...).(map[string]any); ok {
		return v
	}
	return nil
}

func getSlice(m map[string]any, path ...string) []any {
	if v, ok := getPath(m, path...).([]any); ok {
		return v
	}
	return nil
}

func getPath(m map[string]any, path ...string) any {
	var cur any = m
	for _, p := range path {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = obj[p]
	}
	return cur
}

func truncateText(s string, n int) string {
	if n <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

func jsonPretty(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return ""
	}
	return string(b)
}

func containsFold(haystack, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}
