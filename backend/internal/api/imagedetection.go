package api

import (
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"

	"apiunit/internal/model"
	"apiunit/internal/modules/imagedetection"
)

// handleImageDetectionConfigGet 返回脱敏后的在线验签配置与可选测试项。
func (s *Server) handleImageDetectionConfigGet(w http.ResponseWriter, r *http.Request) {
	settings, err := imagedetection.LoadProvenanceSettings(s.store)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "读取在线验签配置失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"settings": imagedetection.ExportSettingsView(settings),
		"phases":   imagedetection.PhaseInfos,
	})
}

// handleImageDetectionConfigSave 保存 OpenAI Content Provenance API 的
// Base URL 与 API Key。Key 加密落库，接口只返回掩码。
func (s *Server) handleImageDetectionConfigSave(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BaseURL    string `json:"baseUrl"`
		APIKey     string `json:"apiKey"`
		ClearKey   bool   `json:"clearKey"`
		ProxyURL   string `json:"proxyUrl"`
		ClearProxy bool   `json:"clearProxy"`
		// ForceTLS12 为 nil 时保持原值，避免只保存 API Key 时被意外重置。
		ForceTLS12 *bool `json:"forceTls12"`
	}
	if err := decode(r, &req, 1<<20); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	existing, err := imagedetection.LoadProvenanceSettings(s.store)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "读取现有配置失败: "+err.Error())
		return
	}
	if req.ClearKey {
		existing.APIKey = ""
	} else if key := strings.TrimSpace(req.APIKey); key != "" && key != "****" {
		existing.APIKey = key
	}
	if raw := strings.TrimSpace(req.BaseURL); raw != "" {
		if err := imagedetection.ValidateBaseURL(raw); err != nil {
			apiError(w, http.StatusBadRequest, "Base URL 无效: "+err.Error())
			return
		}
		existing.BaseURL = imagedetection.NormalizeBaseURL(raw)
	}
	if req.ClearProxy {
		existing.ProxyURL = ""
	} else if raw := strings.TrimSpace(req.ProxyURL); raw != "" {
		if err := imagedetection.ValidateProxyURL(raw); err != nil {
			apiError(w, http.StatusBadRequest, "代理地址无效: "+err.Error())
			return
		}
		existing.ProxyURL = raw
	}
	if req.ForceTLS12 != nil {
		existing.ForceTLS12 = *req.ForceTLS12
	}
	if err := imagedetection.SaveProvenanceSettings(s.store, existing); err != nil {
		apiError(w, http.StatusInternalServerError, "保存配置失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"settings": imagedetection.ExportSettingsView(existing),
		"phases":   imagedetection.PhaseInfos,
	})
}

// handleImageDetectionFile 提供图片与在线校验站点读取。
//
// 该路由位于登录校验之外：在线校验站点（如 Adobe Inspect 的 source 参数）
// 无法携带本站会话 Cookie。文件名包含随机前缀，运行 ID 也不可枚举，
// 因此以「不可猜测的链接」作为访问凭证。响应显式允许跨域，便于在线站点抓取。
func (s *Server) handleImageDetectionFile(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "runId")
	file := chi.URLParam(r, "file")
	if !imagedetection.ValidRunID(runID) || !imagedetection.ValidImageFile(file) {
		http.NotFound(w, r)
		return
	}
	switch strings.ToLower(filepath.Ext(file)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
	default:
		http.NotFound(w, r)
		return
	}
	run, err := s.store.GetRun(runID)
	if err != nil || run.Kind != model.KindImageDetection {
		http.NotFound(w, r)
		return
	}
	root := imagedetection.ImageDir(s.cfg.DataDir)
	full := filepath.Join(root, runID, file)
	rel, err := filepath.Rel(root, full)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cross-Origin-Resource-Policy", "cross-origin")
	w.Header().Set("Access-Control-Expose-Headers", "Content-Length, Content-Type")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	// 图片链接虽然不可枚举，也不要被搜索引擎收录。
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	http.ServeFile(w, r, full)
}
