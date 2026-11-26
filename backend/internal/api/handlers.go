package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"apiunit/internal/model"
	"apiunit/internal/modules/imagedetection"
	"apiunit/internal/modules/modcommon"
	"apiunit/internal/protocol"
	"apiunit/internal/seed"
	"apiunit/internal/tokenmeta"
	"apiunit/internal/tokens"
)

// ---------------- targets ----------------

func (s *Server) handleTargetsList(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListTargets()
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

type targetReq struct {
	Name     string  `json:"name"`
	BaseURL  string  `json:"baseUrl"`
	Protocol string  `json:"protocol"`
	APIKey   *string `json:"apiKey"`
	ClearKey bool    `json:"clearKey"`
	// ModelsPath 可选：自定义模型列表接口，支持 /v1/models 或完整 URL。
	ModelsPath *string `json:"modelsPath"`
	// TokenFamily 是渠道所属的 Token 口径路径（如 gemini/vt），用于匹配手工 Token 基准。
	TokenFamily *string `json:"tokenFamily"`
	// TargetID 仅用于“测试连接”：编辑已有上游且未重新输入 Key 时沿用已保存的 Key。
	TargetID string `json:"targetId"`
	// Models 是添加/编辑时“测试连接并获取模型”得到的模型列表。
	// 仅用于记录模型数与最近检测时间，避免保存后还要再点一次“取模型”。
	Models []string `json:"models"`
}

func (s *Server) handleTargetCreate(w http.ResponseWriter, r *http.Request) {
	var req targetReq
	if err := decode(r, &req, 1<<20); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	t, key, err := validateTarget(targetReq{
		Name: req.Name, BaseURL: req.BaseURL, Protocol: req.Protocol,
		APIKey: req.APIKey,
	})
	if err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	family, err := s.resolveTokenFamily(req.TokenFamily, "")
	if err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	t.TokenFamily = family
	if err := s.store.SaveTarget(t, key); err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 添加时已经测试过连接，直接落库模型数与检测时间。
	if req.Models != nil {
		_ = s.store.TouchTarget(t.ID, len(req.Models))
	}
	saved, err := s.store.GetTarget(t.ID, false)
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"item": saved})
}

func validateTarget(req targetReq) (*model.Target, string, error) {
	base, err := protocol.NormalizeBaseURL(req.BaseURL)
	if err != nil {
		return nil, "", err
	}
	proto := modcommon.NormProtocol(req.Protocol)
	name := strings.TrimSpace(req.Name)
	if name == "" {
		if u, err := url.Parse(base); err == nil {
			name = u.Host
		}
	}
	if name == "" {
		name = "未命名上游"
	}
	key := ""
	if req.APIKey != nil {
		key = strings.TrimSpace(*req.APIKey)
	}
	return &model.Target{
		Name: name, BaseURL: base, Protocol: proto,
		ModelsPath: normalizeModelsPath(req.ModelsPath),
	}, key, nil
}

// normalizeModelsPath 统一自定义模型列表接口的写法，空值表示自动探测。
func normalizeModelsPath(raw *string) string {
	if raw == nil {
		return ""
	}
	p := strings.TrimSpace(*raw)
	if p == "" {
		return ""
	}
	if strings.HasPrefix(p, "http://") || strings.HasPrefix(p, "https://") {
		return p
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}

// resolveTokenFamily 解析并校验渠道的口径路径；未提交时沿用旧值。
func (s *Server) resolveTokenFamily(next *string, fallback string) (string, error) {
	old := strings.Trim(strings.TrimSpace(fallback), "/")
	family := old
	if next != nil {
		family = strings.Trim(strings.TrimSpace(*next), "/")
	}
	if family == "" {
		return "", nil
	}
	// 口径树被删减后，旧渠道保留原路径仍可读取；只有新选择才要求节点存在。
	if family != old && !tokenmeta.Load(s.store).HasPath(family) {
		return "", fmt.Errorf("未知的 Token 口径：%s", family)
	}
	return family, nil
}

func (s *Server) handleTargetUpdate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	existing, err := s.store.GetTarget(id, false)
	if err != nil {
		apiError(w, http.StatusNotFound, "目标不存在")
		return
	}
	var req targetReq
	if err := decode(r, &req, 1<<20); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.BaseURL == "" {
		req.BaseURL = existing.BaseURL
	}
	if req.Name == "" {
		req.Name = existing.Name
	}
	if req.Protocol == "" {
		req.Protocol = string(existing.Protocol)
	}
	// 未提交 modelsPath 字段时保持原值，提交空字符串表示恢复自动探测。
	if req.ModelsPath == nil {
		modelsPath := existing.ModelsPath
		req.ModelsPath = &modelsPath
	}
	t, key, err := validateTarget(req)
	if err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	family, err := s.resolveTokenFamily(req.TokenFamily, existing.TokenFamily)
	if err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	t.ID = existing.ID
	t.CreatedAt = existing.CreatedAt
	t.ModelCount = existing.ModelCount
	t.LastTestedAt = existing.LastTestedAt
	t.TokenFamily = family
	if req.ClearKey {
		if err := s.store.ClearTargetKey(id); err != nil {
			apiError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err := s.store.SaveTarget(t, key); err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 编辑时若重新测试过连接，同步模型数与检测时间。
	if req.Models != nil {
		_ = s.store.TouchTarget(id, len(req.Models))
	}
	updated, _ := s.store.GetTarget(id, false)
	writeJSON(w, http.StatusOK, map[string]any{"item": updated})
}

func (s *Server) handleTargetDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteTarget(chi.URLParam(r, "id")); err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleTargetTest(w http.ResponseWriter, r *http.Request) {
	var req targetReq
	if err := decode(r, &req, 1<<20); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	base, err := protocol.NormalizeBaseURL(req.BaseURL)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	key := ""
	if req.APIKey != nil {
		key = strings.TrimSpace(*req.APIKey)
	}
	// 编辑已有上游时前端不会回填 Key，这里沿用已保存的 Key 进行测试，避免误报 401。
	if key == "" && strings.TrimSpace(req.TargetID) != "" {
		if saved, err := s.store.GetTarget(strings.TrimSpace(req.TargetID), true); err == nil {
			key = saved.APIKey
		}
	}
	start := time.Now()
	res, err := s.client.DiscoverModels(r.Context(), protocol.TargetInfo{
		BaseURL: base, APIKey: key, Protocol: modcommon.NormProtocol(req.Protocol),
		ModelsPath: normalizeModelsPath(req.ModelsPath),
	})
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": false, "error": err.Error(), "elapsedMs": elapsed,
			"warnings": warningsOf(res), "models": []any{},
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "models": res.Models, "endpoint": res.Endpoint,
		"warnings": res.Warnings, "elapsedMs": elapsed,
	})
}

func warningsOf(res *protocol.DiscoverResult) []string {
	if res == nil {
		return []string{}
	}
	if res.Warnings == nil {
		return []string{}
	}
	return res.Warnings
}

func (s *Server) handleTargetModels(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	t, err := s.store.GetTarget(id, true)
	if err != nil {
		apiError(w, http.StatusNotFound, "目标不存在")
		return
	}
	res, err := s.client.DiscoverModels(r.Context(), protocol.TargetInfo{
		ID: t.ID, Name: t.Name, BaseURL: t.BaseURL, APIKey: t.APIKey, Protocol: t.Protocol,
		ModelsPath: t.ModelsPath,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "warnings": warningsOf(res)})
		return
	}
	_ = s.store.TouchTarget(id, len(res.Models))
	writeJSON(w, http.StatusOK, map[string]any{"models": res.Models, "endpoint": res.Endpoint, "warnings": res.Warnings})
}

// ---------------- prompts ----------------

func (s *Server) handlePromptsList(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListPrompts()
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

type promptReq struct {
	Name     string `json:"name"`
	Category string `json:"category"`
	Content  string `json:"content"`
	// TokenOverrides 为“口径路径 → 本地 Token 数”的手工基准。
	// 指针用于区分“未提交”和“提交空对象”。
	TokenOverrides *map[string]int `json:"tokenOverrides"`
	// Recalibrate 表示用户确认把现有基准套用到一个已修改的提示词内容上。
	Recalibrate bool `json:"recalibrate"`
}

// applyPromptOverrideState 统一整理基准并计算失效状态，供响应直接展示。
func applyPromptOverrideState(p *model.Prompt) {
	p.TokenOverrides = tokenmeta.NormalizeOverrides(p.TokenOverrides)
	p.TokenOverridesStale = len(p.TokenOverrides) > 0 &&
		(p.TokenOverridesHash == "" || p.TokenOverridesHash != model.HashText(p.Content))
}

func (s *Server) handlePromptCreate(w http.ResponseWriter, r *http.Request) {
	var req promptReq
	if err := decode(r, &req, 1<<20); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		apiError(w, http.StatusBadRequest, "提示词内容不能为空")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		req.Name = "未命名提示词"
	}
	p := &model.Prompt{Name: req.Name, Category: req.Category, Content: req.Content}
	if p.Category == "" {
		p.Category = "custom"
	}
	if req.TokenOverrides != nil {
		p.TokenOverrides = tokenmeta.NormalizeOverrides(*req.TokenOverrides)
	}
	if len(p.TokenOverrides) > 0 {
		p.TokenOverridesHash = model.HashText(p.Content)
	}
	if err := s.store.SavePrompt(p); err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	applyPromptOverrideState(p)
	writeJSON(w, http.StatusCreated, map[string]any{"item": p})
}

func (s *Server) handlePromptUpdate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	existing, err := s.findPrompt(id)
	if err != nil {
		apiError(w, http.StatusNotFound, "提示词不存在")
		return
	}
	var req promptReq
	if err := decode(r, &req, 1<<20); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		apiError(w, http.StatusBadRequest, "提示词内容不能为空")
		return
	}
	p := *existing
	if strings.TrimSpace(req.Name) != "" {
		p.Name = req.Name
	}
	if req.Category != "" {
		p.Category = req.Category
	}
	p.Content = req.Content
	if req.TokenOverrides != nil {
		next := tokenmeta.NormalizeOverrides(*req.TokenOverrides)
		// 基准数值没动且未显式确认重新校准：保留旧摘要。
		// 这样内容变更后基准会自动失效，普通保存不会误把旧数值重新启用。
		if tokenmeta.OverridesEqual(next, existing.TokenOverrides) && !req.Recalibrate {
			p.TokenOverrides = existing.TokenOverrides
			p.TokenOverridesHash = existing.TokenOverridesHash
		} else {
			p.TokenOverrides = next
			p.TokenOverridesHash = ""
			if len(next) > 0 {
				p.TokenOverridesHash = model.HashText(p.Content)
			}
		}
	}
	if err := s.store.SavePrompt(&p); err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	applyPromptOverrideState(&p)
	writeJSON(w, http.StatusOK, map[string]any{"item": p})
}

// handlePromptTokenReference 把一次审计实测值写成提示词在指定口径上的基准。
// tokens<=0 表示删除该路径的基准。
func (s *Server) handlePromptTokenReference(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, err := s.findPrompt(id)
	if err != nil {
		apiError(w, http.StatusNotFound, "提示词不存在")
		return
	}
	var req struct {
		Family string `json:"family"`
		Tokens int    `json:"tokens"`
	}
	if err := decode(r, &req, 1<<20); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	family := strings.Trim(strings.TrimSpace(req.Family), "/")
	if family == "" {
		apiError(w, http.StatusBadRequest, "请选择 Token 口径")
		return
	}
	if req.Tokens > 0 && !tokenmeta.Load(s.store).HasPath(family) {
		apiError(w, http.StatusBadRequest, "未知的 Token 口径："+family)
		return
	}
	// 内容已变更导致旧基准失效时，只保留这次回填的值，
	// 避免把过时的其他口径数值一并重新启用。
	overrides := map[string]int{}
	if tokenmeta.OverridesActive(*p) {
		for k, v := range p.TokenOverrides {
			overrides[k] = v
		}
	}
	if req.Tokens > 0 {
		overrides[family] = req.Tokens
	} else {
		delete(overrides, family)
	}
	p.TokenOverrides = tokenmeta.NormalizeOverrides(overrides)
	p.TokenOverridesHash = ""
	if len(p.TokenOverrides) > 0 {
		p.TokenOverridesHash = model.HashText(p.Content)
	}
	if err := s.store.SavePrompt(p); err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	applyPromptOverrideState(p)
	writeJSON(w, http.StatusOK, map[string]any{"item": p})
}

func (s *Server) findPrompt(id string) (*model.Prompt, error) {
	list, err := s.store.ListPrompts()
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == id {
			return &list[i], nil
		}
	}
	return nil, fmt.Errorf("not found")
}

func (s *Server) handlePromptDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, err := s.findPrompt(id)
	if err != nil {
		apiError(w, http.StatusNotFound, "提示词不存在")
		return
	}
	if p.Builtin {
		apiError(w, http.StatusBadRequest, "内置提示词不可删除")
		return
	}
	if err := s.store.DeletePrompt(id); err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handlePromptsImport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Prompts []promptReq `json:"prompts"`
	}
	if err := decodeLenient(r, &req, 8<<20); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	created := 0
	for _, p := range req.Prompts {
		if strings.TrimSpace(p.Content) == "" {
			continue
		}
		if strings.TrimSpace(p.Name) == "" {
			p.Name = "导入提示词"
		}
		if p.Category == "" {
			p.Category = "imported"
		}
		item := &model.Prompt{Name: p.Name, Category: p.Category, Content: p.Content}
		if p.TokenOverrides != nil {
			item.TokenOverrides = tokenmeta.NormalizeOverrides(*p.TokenOverrides)
			if len(item.TokenOverrides) > 0 {
				item.TokenOverridesHash = model.HashText(item.Content)
			}
		}
		if err := s.store.SavePrompt(item); err != nil {
			apiError(w, http.StatusInternalServerError, err.Error())
			return
		}
		created++
	}
	writeJSON(w, http.StatusOK, map[string]any{"created": created})
}

func (s *Server) handlePromptsExport(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListPrompts()
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"prompts": list})
}

// ---------------- attacks ----------------

func (s *Server) handleAttacksList(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListAttacks()
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list, "categories": seed.Categories()})
}

type attackReq struct {
	Name         string        `json:"name"`
	Category     string        `json:"category"`
	Severity     string        `json:"severity"`
	SystemPrompt string        `json:"systemPrompt"`
	Prompt       string        `json:"prompt"`
	Canary       string        `json:"canary"`
	Checks       []model.Check `json:"checks"`
	RequireJudge bool          `json:"requireJudge"`
	Rubric       string        `json:"rubric"`
}

func validateAttack(req attackReq) error {
	if strings.TrimSpace(req.Name) == "" {
		return fmt.Errorf("用例名称不能为空")
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return fmt.Errorf("攻击提示词不能为空")
	}
	if strings.TrimSpace(req.Category) == "" {
		return fmt.Errorf("分类不能为空")
	}
	switch req.Severity {
	case "low", "medium", "high", "critical":
	default:
		return fmt.Errorf("严重程度必须是 low/medium/high/critical")
	}
	for _, c := range req.Checks {
		switch c.Kind {
		case "canary_leak", "contains", "refusal":
		case "regex":
			if _, err := regexp.Compile(c.Value); err != nil {
				return fmt.Errorf("正则无效: %v", err)
			}
		default:
			return fmt.Errorf("未知检查类型: %s", c.Kind)
		}
	}
	return nil
}

func (s *Server) handleAttackCreate(w http.ResponseWriter, r *http.Request) {
	var req attackReq
	if err := decode(r, &req, 1<<20); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateAttack(req); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	a := &model.Attack{
		Name: req.Name, Category: req.Category, Severity: req.Severity,
		SystemPrompt: req.SystemPrompt, Prompt: req.Prompt, Canary: req.Canary,
		Checks: req.Checks, RequireJudge: req.RequireJudge, Rubric: req.Rubric,
	}
	if err := s.store.SaveAttack(a); err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"item": a})
}

func (s *Server) findAttack(id string) (*model.Attack, error) {
	list, err := s.store.ListAttacks()
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == id {
			return &list[i], nil
		}
	}
	return nil, fmt.Errorf("not found")
}

func (s *Server) handleAttackUpdate(w http.ResponseWriter, r *http.Request) {
	existing, err := s.findAttack(chi.URLParam(r, "id"))
	if err != nil {
		apiError(w, http.StatusNotFound, "用例不存在")
		return
	}
	var req attackReq
	if err := decode(r, &req, 1<<20); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateAttack(req); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	a := *existing
	a.Name = req.Name
	a.Category = req.Category
	a.Severity = req.Severity
	a.SystemPrompt = req.SystemPrompt
	a.Prompt = req.Prompt
	a.Canary = req.Canary
	a.Checks = req.Checks
	a.RequireJudge = req.RequireJudge
	a.Rubric = req.Rubric
	if err := s.store.SaveAttack(&a); err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"item": a})
}

func (s *Server) handleAttackDelete(w http.ResponseWriter, r *http.Request) {
	a, err := s.findAttack(chi.URLParam(r, "id"))
	if err != nil {
		apiError(w, http.StatusNotFound, "用例不存在")
		return
	}
	if a.Builtin {
		apiError(w, http.StatusBadRequest, "内置用例不可删除，可在“恢复内置用例”后使用")
		return
	}
	if err := s.store.DeleteAttack(a.ID); err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleAttacksImport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Attacks []attackReq `json:"attacks"`
	}
	if err := decodeLenient(r, &req, 8<<20); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	created := 0
	for _, a := range req.Attacks {
		if err := validateAttack(a); err != nil {
			apiError(w, http.StatusBadRequest, "第 "+strconv.Itoa(created+1)+" 个用例无效: "+err.Error())
			return
		}
		item := &model.Attack{
			Name: a.Name, Category: a.Category, Severity: a.Severity,
			SystemPrompt: a.SystemPrompt, Prompt: a.Prompt, Canary: a.Canary,
			Checks: a.Checks, RequireJudge: a.RequireJudge, Rubric: a.Rubric,
		}
		if err := s.store.SaveAttack(item); err != nil {
			apiError(w, http.StatusInternalServerError, err.Error())
			return
		}
		created++
	}
	writeJSON(w, http.StatusOK, map[string]any{"created": created})
}

func (s *Server) handleAttacksExport(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListAttacks()
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"attacks": list})
}

func (s *Server) handleAttacksReset(w http.ResponseWriter, r *http.Request) {
	if err := s.store.ResetBuiltinAttacks(seed.Attacks()); err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "count": len(seed.Attacks())})
}

// ---------------- prices ----------------

type priceReq struct {
	Pattern        string  `json:"pattern"`
	InputPerM      float64 `json:"inputPerM"`
	OutputPerM     float64 `json:"outputPerM"`
	CacheReadPerM  float64 `json:"cacheReadPerM"`
	CacheWritePerM float64 `json:"cacheWritePerM"`
	Currency       string  `json:"currency"`
	Note           string  `json:"note"`
}

func (s *Server) handlePricesList(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListPrices()
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *Server) handlePriceCreate(w http.ResponseWriter, r *http.Request) {
	var req priceReq
	if err := decode(r, &req, 1<<20); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.Pattern) == "" {
		apiError(w, http.StatusBadRequest, "模型匹配模式不能为空")
		return
	}
	p := &model.Price{
		Pattern: req.Pattern, InputPerM: req.InputPerM, OutputPerM: req.OutputPerM,
		CacheReadPerM: req.CacheReadPerM, CacheWritePerM: req.CacheWritePerM,
		Currency: req.Currency, Note: req.Note,
	}
	if err := s.store.SavePrice(p); err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"item": p})
}

func (s *Server) handlePriceUpdate(w http.ResponseWriter, r *http.Request) {
	var req priceReq
	if err := decode(r, &req, 1<<20); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	p := &model.Price{
		ID: chi.URLParam(r, "id"), Pattern: req.Pattern, InputPerM: req.InputPerM,
		OutputPerM: req.OutputPerM, CacheReadPerM: req.CacheReadPerM,
		CacheWritePerM: req.CacheWritePerM, Currency: req.Currency, Note: req.Note,
	}
	if err := s.store.SavePrice(p); err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"item": p})
}

func (s *Server) handlePriceDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeletePrice(chi.URLParam(r, "id")); err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------------- tokenizers / meta / settings ----------------

func (s *Server) handleTokenizerStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.counter.Status())
}

func (s *Server) handleTokenizerEnsure(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	err := s.counter.EnsureGemini(ctx)
	status := s.counter.Status()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error(), "status": status})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": status})
}

func (s *Server) handleTokenizerReload(w http.ResponseWriter, r *http.Request) {
	if err := s.counter.ReloadGemini(); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error(), "status": s.counter.Status()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": s.counter.Status()})
}

func (s *Server) handleTokenizerReset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Download bool `json:"download"`
	}
	if r.Body != nil {
		_ = decodeLenient(r, &req, 1<<20)
	}
	if err := s.counter.ResetGemini(); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error(), "status": s.counter.Status()})
		return
	}
	if req.Download {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
		defer cancel()
		if err := s.counter.EnsureGemini(ctx); err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error(), "status": s.counter.Status()})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": s.counter.Status()})
}

func (s *Server) handleTokenizerCount(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Model    string                 `json:"model"`
		Text     string                 `json:"text"`
		Messages []protocol.ChatMessage `json:"messages"`
		Strategy string                 `json:"strategy"`
		TargetID string                 `json:"targetId"`
		// PromptID 可选：提示词库中的提示词。内容未改动且渠道标注了口径时，
		// 直接返回手工维护的本地 Token 基准。
		PromptID string `json:"promptId"`
	}
	if err := decodeLenient(r, &req, 8<<20); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	strategy := tokens.Strategy(strings.ToLower(strings.TrimSpace(req.Strategy)))
	if strategy == "" {
		strategy = tokens.StrategyAuto
	}
	if res, ok := s.manualTokenCount(req.PromptID, req.TargetID, req.Messages, strategy); ok {
		writeJSON(w, http.StatusOK, res)
		return
	}
	if strategy == tokens.StrategyOnline {
		if req.TargetID == "" {
			apiError(w, http.StatusBadRequest, "在线计数需要选择目标")
			return
		}
		target, err := modcommon.ResolveTarget(s.store, req.TargetID)
		if err != nil {
			apiError(w, http.StatusBadRequest, err.Error())
			return
		}
		res, err := s.counter.CountOnline(r.Context(), target, req.Model, req.Messages)
		if err != nil {
			apiError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, res)
		return
	}
	var res tokens.Result
	if len(req.Messages) > 0 {
		res = s.counter.CountChat(req.Model, req.Messages, strategy)
	} else {
		res = s.counter.CountText(req.Model, req.Text, strategy)
	}
	writeJSON(w, http.StatusOK, res)
}

// manualTokenCount 在提示词内容未改动、且渠道标注了口径时返回手工基准。
func (s *Server) manualTokenCount(promptID, targetID string, msgs []protocol.ChatMessage, strategy tokens.Strategy) (tokens.Result, bool) {
	if promptID == "" || targetID == "" || strategy == tokens.StrategyNone {
		return tokens.Result{}, false
	}
	if len(msgs) != 1 || !strings.EqualFold(msgs[0].Role, "user") {
		return tokens.Result{}, false
	}
	p, err := s.findPrompt(promptID)
	if err != nil || p.Content != msgs[0].Content {
		return tokens.Result{}, false
	}
	target, err := s.store.GetTarget(targetID, false)
	if err != nil {
		return tokens.Result{}, false
	}
	n, key, ok := tokenmeta.ResolveOverride(*p, target.TokenFamily)
	if !ok {
		return tokens.Result{}, false
	}
	return tokens.Result{Tokens: n, Source: "manual", Detail: key}, true
}

// ---------------- token families ----------------

func (s *Server) handleTokenFamiliesGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": tokenmeta.Load(s.store)})
}

func (s *Server) handleTokenFamiliesSave(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Items tokenmeta.Taxonomy `json:"items"`
	}
	if err := decode(r, &req, 1<<20); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := tokenmeta.Save(s.store, req.Items); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": tokenmeta.Load(s.store)})
}

func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	strategies := []map[string]string{
		{"id": string(tokens.StrategyAuto), "label": "自动（按模型家族）"},
		{"id": string(tokens.StrategyTiktoken), "label": "tiktoken（OpenAI 本地）"},
		{"id": string(tokens.StrategyGemini), "label": "Gemini 本地 SentencePiece"},
		{"id": string(tokens.StrategyOnline), "label": "OpenAI 在线计数"},
		{"id": string(tokens.StrategyHeuristic), "label": "启发式估算"},
		{"id": string(tokens.StrategyNone), "label": "不计数"},
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version":             s.cfg.Version,
		"authEnabled":         s.cfg.AuthEnabled(),
		"kinds":               s.manager.Kinds(),
		"protocols":           protocol.Capabilities(),
		"categories":          seed.Categories(),
		"tokenizerStrategies": strategies,
		"tokenFamilies":       tokenmeta.Load(s.store),
		"limits": map[string]any{
			"maxConcurrentRuns": s.cfg.MaxConcurrentRuns,
			"maxBodyMb":         s.cfg.MaxBodyBytes >> 20,
			"retentionDays":     s.cfg.RetentionDays,
		},
		"tokenizers": s.counter.Status(),
	})
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"version":           s.cfg.Version,
		"dataDir":           s.cfg.DataDir,
		"webDir":            s.cfg.WebDir,
		"authEnabled":       s.cfg.AuthEnabled(),
		"offline":           s.cfg.Offline,
		"retentionDays":     s.cfg.RetentionDays,
		"maxConcurrentRuns": s.cfg.MaxConcurrentRuns,
		"activeRuns":        s.manager.ActiveCount(),
		"uptimeSec":         int(time.Since(s.started).Seconds()),
	})
}

func (s *Server) handlePurge(w http.ResponseWriter, r *http.Request) {
	days := s.cfg.RetentionDays
	var req struct {
		Days int `json:"days"`
	}
	if err := decodeLenient(r, &req, 1<<20); err == nil && req.Days > 0 {
		days = req.Days
	}
	if days <= 0 {
		apiError(w, http.StatusBadRequest, "保留天数必须大于 0")
		return
	}
	cutoff := model.NowMs() - int64(days)*24*3600*1000
	n, err := s.store.PurgeBefore(cutoff)
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_, _ = imagedetection.PurgeImages(imagedetection.ImageDir(s.cfg.DataDir), cutoff)
	writeJSON(w, http.StatusOK, map[string]any{"deleted": n})
}
