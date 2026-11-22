package imagedetection

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"apiunit/internal/model"
	"apiunit/internal/store"
)

const settingsKey = "imagedetection.provenance"

// Settings 保存在加密的设置表里，避免 API Key、代理密码出现在运行配置或报告 JSON 中。
type Settings struct {
	BaseURL string `json:"baseUrl"`
	APIKey  string `json:"apiKey"`
	// ProxyURL 是生图请求使用的代理，支持 http/https/socks5，可带账号密码。
	ProxyURL string `json:"proxyUrl,omitempty"`
	// ForceTLS12 强制生图请求使用 TLS 1.2，用于绕过部分网络对 TLS 1.3 的限速。
	ForceTLS12 bool `json:"forceTls12,omitempty"`
}

// SettingsView 是返回给前端的脱敏视图。
type SettingsView struct {
	BaseURL         string `json:"baseUrl"`
	HasKey          bool   `json:"hasKey"`
	KeyMasked       string `json:"keyMasked"`
	Endpoint        string `json:"endpoint"`
	ProxyConfigured bool   `json:"proxyConfigured"`
	ProxyMasked     string `json:"proxyMasked,omitempty"`
	ForceTLS12      bool   `json:"forceTls12"`
}

func defaultSettings() Settings {
	return Settings{BaseURL: "https://api.openai.com"}
}

func normalizedBaseURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "https://api.openai.com"
	}
	raw = strings.TrimRight(raw, "/")
	if strings.HasSuffix(strings.ToLower(raw), "/v1") {
		raw = raw[:len(raw)-len("/v1")]
	}
	return raw
}

// loadSettings 读取在线验签配置。配置不存在时返回默认值。
func loadSettings(st *store.Store) (Settings, error) {
	out := defaultSettings()
	if st == nil {
		return out, nil
	}
	raw, err := st.GetSecretSetting(settingsKey)
	if err != nil {
		return out, err
	}
	if strings.TrimSpace(raw) == "" {
		return out, nil
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return out, fmt.Errorf("解析在线验签配置失败: %w", err)
	}
	out.BaseURL = normalizedBaseURL(out.BaseURL)
	return out, nil
}

func saveSettings(st *store.Store, s Settings) error {
	if st == nil {
		return fmt.Errorf("存储不可用")
	}
	s.BaseURL = normalizedBaseURL(s.BaseURL)
	s.ProxyURL = strings.TrimSpace(s.ProxyURL)
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return st.SetSecretSetting(settingsKey, string(raw))
}

// LoadProvenanceSettings 是 API 层使用的导出入口。
func LoadProvenanceSettings(st *store.Store) (Settings, error) { return loadSettings(st) }

// SaveProvenanceSettings 加密保存在线验签配置。
func SaveProvenanceSettings(st *store.Store, s Settings) error { return saveSettings(st, s) }

// ExportSettingsView 返回脱敏后的配置视图。
func ExportSettingsView(s Settings) SettingsView { return viewSettings(s) }

// NormalizeBaseURL 统一在线验签接口的 Base URL 写法。
func NormalizeBaseURL(raw string) string { return normalizedBaseURL(raw) }

// ValidateBaseURL 校验用户填写的 Base URL。
func ValidateBaseURL(raw string) error { return validateSettingsBaseURL(raw) }

// ValidateProxyURL 校验用户填写的生图请求代理；为空表示不使用自定义代理。
func ValidateProxyURL(raw string) error { return validateProxyURL(raw) }

func viewSettings(s Settings) SettingsView {
	return SettingsView{
		BaseURL:         normalizedBaseURL(s.BaseURL),
		HasKey:          strings.TrimSpace(s.APIKey) != "",
		KeyMasked:       model.MaskKey(s.APIKey),
		Endpoint:        provenanceEndpoint(s),
		ProxyConfigured: strings.TrimSpace(s.ProxyURL) != "",
		ProxyMasked:     maskProxyURL(s.ProxyURL),
		ForceTLS12:      s.ForceTLS12,
	}
}

// maskProxyURL 隐藏代理地址中的密码，避免在接口返回与页面中泄露。
func maskProxyURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "****"
	}
	if u.User != nil {
		if _, hasPassword := u.User.Password(); hasPassword {
			// url.UserPassword 会转义 *，这里用 xxxx 保持可读性。
			u.User = url.UserPassword(u.User.Username(), "xxxx")
		}
	}
	return u.String()
}

func validateProxyURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "socks5", "socks5h":
	default:
		return fmt.Errorf("仅支持 http/https/socks5 代理")
	}
	if u.Host == "" {
		return fmt.Errorf("缺少主机名")
	}
	return nil
}

func provenanceEndpoint(s Settings) string {
	return normalizedBaseURL(s.BaseURL) + "/v1/content_provenance_checks"
}

func validateSettingsBaseURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("仅支持 http/https 地址")
	}
	if u.Host == "" {
		return fmt.Errorf("缺少主机名")
	}
	return nil
}
