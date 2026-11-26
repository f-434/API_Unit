package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"apiunit/internal/config"
	"apiunit/internal/engine"
	"apiunit/internal/model"
	"apiunit/internal/secret"
	"apiunit/internal/store"
	"apiunit/internal/tokens"
)

// doJSON 发送 JSON 请求并返回状态码，out 非空时解码响应。
func doJSON(t *testing.T, method, url string, body any, out any) int {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("decode %s %s: %v (%s)", method, url, err, raw)
		}
	}
	return resp.StatusCode
}

// TestPromptTokenReferenceFlow 覆盖“渠道口径 → 提示词手工基准 → 审计计数”
// 的完整链路，包括内容变更后的自动失效与一键回填。
func TestPromptTokenReferenceFlow(t *testing.T) {
	dir := t.TempDir()
	box, err := secret.LoadOrCreate(filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatalf("secret: %v", err)
	}
	st, err := store.Open(filepath.Join(dir, "apiunit.db"), box)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer st.Close()

	cfg := &config.Config{DataDir: dir, WebDir: dir, MaxConcurrentRuns: 1, MaxBodyBytes: 8 << 20}
	manager := engine.NewManager(st, nil, nil, 1)
	ts := httptest.NewServer(New(cfg, st, manager, tokens.New(dir, true), nil, box).Router())
	defer ts.Close()

	var targetResp struct {
		Item model.Target `json:"item"`
	}
	if code := doJSON(t, http.MethodPost, ts.URL+"/api/targets", map[string]any{
		"name": "gemini-vt", "baseUrl": "https://example.com", "protocol": "openai",
		"apiKey": "sk-test", "tokenFamily": "gemini/vt",
	}, &targetResp); code != http.StatusCreated {
		t.Fatalf("create target HTTP %d", code)
	}
	if targetResp.Item.TokenFamily != "gemini/vt" {
		t.Fatalf("tokenFamily = %q", targetResp.Item.TokenFamily)
	}
	targetID := targetResp.Item.ID

	var promptResp struct {
		Item model.Prompt `json:"item"`
	}
	if code := doJSON(t, http.MethodPost, ts.URL+"/api/prompts", map[string]any{
		"name": "审计提示词", "category": "custom", "content": "审计提示词",
		"tokenOverrides": map[string]int{"gemini": 20, "gemini/vt": 30},
	}, &promptResp); code != http.StatusCreated {
		t.Fatalf("create prompt HTTP %d", code)
	}
	promptID := promptResp.Item.ID
	if promptResp.Item.TokenOverridesStale {
		t.Fatal("新建提示词的基准不应失效")
	}

	count := func(content string) tokens.Result {
		var res tokens.Result
		if code := doJSON(t, http.MethodPost, ts.URL+"/api/tokenizers/count", map[string]any{
			"model": "gemini-2.5-flash", "strategy": "auto", "targetId": targetID,
			"promptId": promptID, "messages": []map[string]string{{"role": "user", "content": content}},
		}, &res); code != http.StatusOK {
			t.Fatalf("count HTTP %d", code)
		}
		return res
	}

	res := count("审计提示词")
	if res.Source != "manual" || res.Tokens != 30 {
		t.Fatalf("应命中 gemini/vt 手工基准 30，实际 %+v", res)
	}

	// 内容变更但基准数值不变：基准自动失效，回退分词器。
	if code := doJSON(t, http.MethodPut, ts.URL+"/api/prompts/"+promptID, map[string]any{
		"name": "审计提示词", "category": "custom", "content": "审计提示词 v2",
		"tokenOverrides": map[string]int{"gemini": 20, "gemini/vt": 30},
	}, &promptResp); code != http.StatusOK {
		t.Fatalf("update prompt HTTP %d", code)
	}
	if !promptResp.Item.TokenOverridesStale {
		t.Fatal("内容变更后基准应标记失效")
	}
	if res := count("审计提示词 v2"); res.Source == "manual" {
		t.Fatalf("失效基准不应参与计数，实际 %+v", res)
	}

	// 一键回填会按当前内容重新校准。
	if code := doJSON(t, http.MethodPost, ts.URL+"/api/prompts/"+promptID+"/token-reference", map[string]any{
		"family": "gemini/vt", "tokens": 42,
	}, &promptResp); code != http.StatusOK {
		t.Fatalf("token-reference HTTP %d", code)
	}
	if promptResp.Item.TokenOverridesStale || promptResp.Item.TokenOverrides["gemini/vt"] != 42 {
		t.Fatalf("回填后基准 = %+v", promptResp.Item)
	}
	if res := count("审计提示词 v2"); res.Source != "manual" || res.Tokens != 42 {
		t.Fatalf("回填后应命中 42，实际 %+v", res)
	}

	// 编辑了内容但未存入提示词库时，不套用基准。
	if res := count("临时改写的内容"); res.Source == "manual" {
		t.Fatalf("内容不一致时不应套用基准，实际 %+v", res)
	}

	// 未知口径路径应在保存渠道时拒绝。
	if code := doJSON(t, http.MethodPost, ts.URL+"/api/targets", map[string]any{
		"name": "bad", "baseUrl": "https://example.com", "protocol": "openai",
		"apiKey": "sk-test", "tokenFamily": "gemini/unknown",
	}, nil); code != http.StatusBadRequest {
		t.Fatalf("未知口径路径 HTTP %d, want 400", code)
	}
}

func TestTokenFamiliesSave(t *testing.T) {
	dir := t.TempDir()
	box, err := secret.LoadOrCreate(filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatalf("secret: %v", err)
	}
	st, err := store.Open(filepath.Join(dir, "apiunit.db"), box)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer st.Close()

	cfg := &config.Config{DataDir: dir, WebDir: dir, MaxConcurrentRuns: 1, MaxBodyBytes: 8 << 20}
	ts := httptest.NewServer(New(cfg, st, engine.NewManager(st, nil, nil, 1), tokens.New(dir, true), nil, box).Router())
	defer ts.Close()

	var out struct {
		Items []map[string]any `json:"items"`
	}
	if code := doJSON(t, http.MethodPut, ts.URL+"/api/token-families", map[string]any{
		"items": []map[string]any{{
			"key": "gemini", "label": "Gemini",
			"children": []map[string]any{{"key": "as", "label": "AI Studio"}},
		}},
	}, &out); code != http.StatusOK {
		t.Fatalf("save families HTTP %d", code)
	}
	if len(out.Items) != 1 {
		t.Fatalf("families = %+v", out.Items)
	}

	// 同级重复键必须被拒绝，避免路径歧义。
	if code := doJSON(t, http.MethodPut, ts.URL+"/api/token-families", map[string]any{
		"items": []map[string]any{{
			"key": "gemini", "label": "Gemini",
			"children": []map[string]any{{"key": "as", "label": "A"}, {"key": "as", "label": "B"}},
		}},
	}, nil); code != http.StatusBadRequest {
		t.Fatalf("重复键 HTTP %d, want 400", code)
	}

	// meta 应返回当前口径树。
	var meta struct {
		TokenFamilies []map[string]any `json:"tokenFamilies"`
	}
	if code := doJSON(t, http.MethodGet, ts.URL+"/api/meta", nil, &meta); code != http.StatusOK {
		t.Fatalf("meta HTTP %d", code)
	}
	if len(meta.TokenFamilies) != 1 {
		t.Fatalf("meta.tokenFamilies = %+v", meta.TokenFamilies)
	}
}
