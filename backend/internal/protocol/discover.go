package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"apiunit/internal/model"
)

type ModelInfo struct {
	ID       string `json:"id"`
	OwnedBy  string `json:"ownedBy,omitempty"`
	Protocol string `json:"protocol"`
}

type DiscoverResult struct {
	Models   []ModelInfo `json:"models"`
	Endpoint string      `json:"endpoint"`
	Warnings []string    `json:"warnings"`
}

type discoverAttempt struct {
	protocol model.Protocol
	label    string
	url      string
	headers  map[string]string
}

func (c *Client) DiscoverModels(ctx context.Context, t TargetInfo) (*DiscoverResult, error) {
	root, err := NormalizeBaseURL(t.BaseURL)
	if err != nil {
		return nil, err
	}
	total := c.discoverTotalTimeout
	if total <= 0 {
		total = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, total)
	defer cancel()
	key := strings.TrimSpace(t.APIKey)
	openAI := discoverAttempt{
		protocol: model.ProtocolOpenAI, label: "OpenAI /v1/models",
		url:     root + "/v1/models",
		headers: map[string]string{"Authorization": "Bearer " + key},
	}
	claude := discoverAttempt{
		protocol: model.ProtocolClaude, label: "Anthropic /v1/models",
		url:     root + "/v1/models",
		headers: map[string]string{"x-api-key": key, "anthropic-version": "2023-06-01"},
	}
	gemini := discoverAttempt{
		protocol: model.ProtocolGemini, label: "Gemini /v1beta/models",
		url:     root + "/v1beta/models",
		headers: map[string]string{"x-goog-api-key": key},
	}

	var order []discoverAttempt
	switch t.Protocol {
	case model.ProtocolClaude:
		order = []discoverAttempt{claude, openAI, gemini}
	case model.ProtocolGemini:
		order = []discoverAttempt{gemini, openAI}
	default:
		order = []discoverAttempt{openAI, claude, gemini}
	}

	res := &DiscoverResult{Models: []ModelInfo{}, Warnings: []string{}}
	unreachable := map[string]bool{}
	for _, at := range order {
		if unreachable[at.url] {
			res.Warnings = append(res.Warnings, at.label+"：同一地址网络不可达，已跳过")
			continue
		}
		models, err := c.fetchModelsWithRetry(ctx, at)
		if err != nil {
			if isNetworkErr(err) {
				unreachable[at.url] = true
			}
			res.Warnings = append(res.Warnings, at.label+"："+err.Error())
			continue
		}
		if len(models) == 0 {
			res.Warnings = append(res.Warnings, at.label+"：返回了空模型列表")
			continue
		}
		res.Models = models
		res.Endpoint = at.url
		return res, nil
	}
	if ctx.Err() != nil {
		res.Warnings = append(res.Warnings, fmt.Sprintf("总超时 %s 已到", total))
	}
	msg := strings.Join(res.Warnings, "；")
	if hint := networkHint(res.Warnings); hint != "" {
		msg += "。" + hint
	}
	return res, fmt.Errorf("未获取到模型列表：%s", msg)
}

func (c *Client) fetchModelsWithRetry(ctx context.Context, at discoverAttempt) ([]ModelInfo, error) {
	attempts := c.discoverRetries + 1
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			select {
			case <-time.After(time.Duration(i) * time.Second):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		models, err := c.fetchModels(ctx, at)
		if err == nil {
			return models, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !isTransientDiscoveryErr(err) {
			return nil, err
		}
	}
	return nil, lastErr
}

type httpStatusError struct {
	Status  int
	Code    string
	Message string
}

func (e *httpStatusError) Error() string {
	return strings.Join(strings.Fields(fmt.Sprintf("HTTP %d %s %s", e.Status, e.Code, e.Message)), " ")
}

func isTransientDiscoveryErr(err error) bool {
	var se *httpStatusError
	if errors.As(err, &se) {
		return se.Status == http.StatusTooManyRequests || se.Status >= 500
	}
	return isNetworkErr(err)
}

func isNetworkErr(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var se *httpStatusError
	if errors.As(err, &se) {
		return false
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	s := strings.ToLower(err.Error())
	for _, x := range []string{
		"tls handshake timeout", "connection timed out", "i/o timeout", "connection reset",
		"connection refused", "unexpected eof", "server closed idle connection",
		"network is unreachable", "no route to host", "temporary failure",
	} {
		if strings.Contains(s, x) {
			return true
		}
	}
	return false
}

func networkHint(warnings []string) string {
	s := strings.ToLower(strings.Join(warnings, " "))
	if !strings.Contains(s, "tls handshake timeout") &&
		!strings.Contains(s, "i/o timeout") &&
		!strings.Contains(s, "connection timed out") &&
		!strings.Contains(s, "deadline exceeded") &&
		!strings.Contains(s, "总超时") {
		return ""
	}
	return "到该地址的网络不稳定（TLS 握手超时）。建议更换网络或开启代理，并在启动服务前设置 HTTPS_PROXY/HTTP_PROXY；" +
		"也可通过 HTTP_TLS_TIMEOUT_SEC、DISCOVER_TIMEOUT_SEC、DISCOVER_TOTAL_TIMEOUT_SEC、DISCOVER_RETRIES 调整超时与重试"
}

func (c *Client) fetchModels(ctx context.Context, at discoverAttempt) ([]ModelInfo, error) {
	timeout := c.discoverTimeout
	if timeout <= 0 {
		timeout = 45 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, at.url, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range at.headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "apiunit/"+Version)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		msg, code := ParseAPIError(data)
		if msg == "" {
			msg = strings.TrimSpace(string(data))
			if len(msg) > 300 {
				msg = msg[:300]
			}
		}
		return nil, &httpStatusError{Status: resp.StatusCode, Code: code, Message: msg}
	}

	var oa struct {
		Data []struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
		Models []struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			DisplayName string `json:"displayName"`
			BaseModelID string `json:"baseModelId"`
			OwnedBy     string `json:"owned_by"`
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &oa); err != nil {
		return nil, fmt.Errorf("模型列表解析失败: %w", err)
	}
	seen := map[string]bool{}
	out := make([]ModelInfo, 0)
	add := func(id, owner string) {
		id = strings.TrimSpace(strings.TrimPrefix(id, "models/"))
		if id == "" || seen[id] || !looksLikeChatModel(id) {
			return
		}
		seen[id] = true
		out = append(out, ModelInfo{ID: id, OwnedBy: owner, Protocol: string(at.protocol)})
	}
	for _, m := range oa.Data {
		add(m.ID, m.OwnedBy)
	}
	for _, m := range oa.Models {
		id := m.ID
		if id == "" {
			id = m.Name
		}
		if id == "" {
			id = m.BaseModelID
		}
		add(id, m.OwnedBy)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func looksLikeChatModel(id string) bool {
	l := strings.ToLower(id)
	exclude := []string{
		"embedding", "embed-", "-embed", "whisper", "tts", "text-to-speech", "speech-to-text",
		"moderation", "rerank", "dall-e", "gpt-image", "imagen", "stable-diffusion",
		"transcribe", "sora", "video", "audio", "realtime", "flux", "midjourney",
	}
	for _, x := range exclude {
		if strings.Contains(l, x) {
			return false
		}
	}
	return true
}
