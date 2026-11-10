package protocol

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	for _, at := range order {
		models, err := c.fetchModels(ctx, at)
		if err != nil {
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
	return res, fmt.Errorf("未获取到模型列表：%s", strings.Join(res.Warnings, "；"))
}

func (c *Client) fetchModels(ctx context.Context, at discoverAttempt) ([]ModelInfo, error) {
	cctx, cancel := context.WithTimeout(ctx, 25*time.Second)
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
		return nil, fmt.Errorf("HTTP %d %s %s", resp.StatusCode, code, msg)
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
