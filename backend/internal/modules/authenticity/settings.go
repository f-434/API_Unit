package authenticity

import (
	"encoding/json"
	"fmt"
	"strings"

	"apiunit/internal/store"
)

const hostedSettingKey = "authenticity.hosted"

func defaultHostedSettings() HostedSettings {
	return HostedSettings{
		Profile:        hostedProfileGeneric,
		DefaultEnabled: false,
		Providers:      map[string]bool{"openai": true, "claude": true, "gemini": true},
		MCP: MCPConfig{
			ServerLabel:       "proof",
			ServerDescription: "只读证明服务：返回本次校验的 nonce 与记录。",
			RequireApproval:   "never",
			ToolName:          "get_proof_record",
		},
		Proof: ProofConfig{
			PathTemplate:       "/proof/{runToken}",
			ExpectedNonceField: "secret",
		},
		Legacy: LegacyConfig{
			EnvVar:           "MODEL_AUTHENTICITY_STATUS",
			ExpectedEnvValue: "verified",
			Collection:       "users",
			ExpectedRecord:   map[string]any{"name": "王华", "sex": "男", "age": 21},
			Keyword:          "t1yOS",
		},
	}
}

func loadHostedSettings(st *store.Store) (HostedSettings, error) {
	out := defaultHostedSettings()
	if st == nil {
		return out, nil
	}
	raw, err := st.GetSecretSetting(hostedSettingKey)
	if err != nil {
		return out, err
	}
	if strings.TrimSpace(raw) == "" {
		return out, nil
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return out, fmt.Errorf("解析工具调用配置失败: %w", err)
	}
	if out.Profile == "" {
		out.Profile = hostedProfileGeneric
	}
	if out.Providers == nil {
		out.Providers = map[string]bool{"openai": true, "claude": true, "gemini": true}
	}
	if out.MCP.ServerLabel == "" {
		out.MCP.ServerLabel = "proof"
	}
	if out.MCP.ServerDescription == "" {
		out.MCP.ServerDescription = "只读证明服务：返回本次校验的 nonce 与记录。"
	}
	if out.MCP.RequireApproval == "" {
		out.MCP.RequireApproval = "never"
	}
	if out.MCP.ToolName == "" {
		out.MCP.ToolName = "get_proof_record"
	}
	if out.Proof.PathTemplate == "" {
		out.Proof.PathTemplate = "/proof/{runToken}"
	}
	if out.Proof.ExpectedNonceField == "" {
		out.Proof.ExpectedNonceField = "secret"
	}
	return out, nil
}

// LoadHostedSettings is the API-facing loader. It returns the full settings
// including MCP header values, so callers must mask before serializing.
func LoadHostedSettings(st *store.Store) (HostedSettings, error) { return loadHostedSettings(st) }

// SaveHostedSettings encrypts the whole settings document (headers included)
// before writing it to SQLite.
func SaveHostedSettings(st *store.Store, s HostedSettings) error {
	if st == nil {
		return fmt.Errorf("存储不可用")
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return st.SetSecretSetting(hostedSettingKey, string(raw))
}

// MaskHostedSettings replaces non-empty MCP header values with "****". The API
// never returns the real credential.
func MaskHostedSettings(s HostedSettings) HostedSettings {
	out := s
	out.MCP.Headers = make(map[string]string, len(s.MCP.Headers))
	for k, v := range s.MCP.Headers {
		if strings.TrimSpace(v) == "" {
			out.MCP.Headers[k] = ""
			continue
		}
		out.MCP.Headers[k] = "****"
	}
	return out
}

// MergeMaskedHeaders keeps the stored value when the client submits a masked
// header, so loading and saving the settings form does not erase credentials.
func MergeMaskedHeaders(incoming, existing map[string]string) map[string]string {
	out := make(map[string]string, len(incoming))
	for k, v := range incoming {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if v == "****" {
			if old, ok := existing[k]; ok {
				out[k] = old
				continue
			}
		}
		out[k] = strings.TrimSpace(v)
	}
	return out
}
