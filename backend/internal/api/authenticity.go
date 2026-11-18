package api

import (
	"net/http"
	"net/url"
	"strings"

	"apiunit/internal/modules/authenticity"
)

func (s *Server) handleAuthenticityConfigGet(w http.ResponseWriter, r *http.Request) {
	settings, err := authenticity.LoadHostedSettings(s.store)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "读取配置失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"settings": authenticity.MaskHostedSettings(settings),
	})
}

func (s *Server) handleAuthenticityConfigSave(w http.ResponseWriter, r *http.Request) {
	var incoming authenticity.HostedSettings
	if err := decode(r, &incoming, 1<<20); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	existing, err := authenticity.LoadHostedSettings(s.store)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "读取现有配置失败: "+err.Error())
		return
	}
	incoming.MCP.Headers = authenticity.MergeMaskedHeaders(incoming.MCP.Headers, existing.MCP.Headers)
	if strings.TrimSpace(incoming.Profile) == "" {
		incoming.Profile = "generic"
	}
	switch strings.ToLower(strings.TrimSpace(incoming.Profile)) {
	case "generic", "t1y-legacy", "custom":
	default:
		apiError(w, http.StatusBadRequest, "profile 只能是 generic、t1y-legacy 或 custom")
		return
	}
	if err := validateHTTPURL(incoming.MCP.ServerURL); err != nil {
		apiError(w, http.StatusBadRequest, "MCP serverUrl 无效: "+err.Error())
		return
	}
	if err := validateHTTPURL(incoming.Proof.BaseURL); err != nil {
		apiError(w, http.StatusBadRequest, "证明页 baseUrl 无效: "+err.Error())
		return
	}
	if strings.TrimSpace(incoming.Proof.PathTemplate) == "" {
		incoming.Proof.PathTemplate = "/proof/{runToken}"
	}
	if strings.TrimSpace(incoming.MCP.RequireApproval) == "" {
		incoming.MCP.RequireApproval = "never"
	}
	if err := authenticity.SaveHostedSettings(s.store, incoming); err != nil {
		apiError(w, http.StatusInternalServerError, "保存配置失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"settings": authenticity.MaskHostedSettings(incoming),
	})
}

func validateHTTPURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return &url.Error{Op: "parse", URL: raw, Err: errUnsupportedScheme{}}
	}
	if u.Host == "" {
		return &url.Error{Op: "parse", URL: raw, Err: errMissingHost{}}
	}
	return nil
}

type errUnsupportedScheme struct{}

func (errUnsupportedScheme) Error() string { return "仅支持 http/https" }

type errMissingHost struct{}

func (errMissingHost) Error() string { return "缺少主机名" }
