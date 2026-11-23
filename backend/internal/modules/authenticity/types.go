// Package authenticity implements the model-authenticity test kind. It keeps
// the original Model Proof idea (fingerprint + source + real capability) but
// replaces the hard-coded t1y.net MCP dependency with:
//
//   - a local, per-run nonce challenge that exercises a real client-side tool
//     call loop (works on ordinary OpenAI-compatible gateways), and
//   - an optional, user-configured hosted proof profile (MCP server + proof
//     page) for OpenAI Responses / Anthropic / Gemini hosted tools.
package authenticity

import (
	"apiunit/internal/engine"
)

const (
	VerdictAuthentic    = "authentic"
	VerdictLikely       = "likely"
	VerdictSuspicious   = "suspicious"
	VerdictCounterfeit  = "counterfeit"
	VerdictInconclusive = "inconclusive"
	VerdictError        = "error"
)

const (
	StatusPass = "pass"
	StatusFail = "fail"
	StatusNA   = "na"
	StatusWarn = "warn"
)

const (
	WeightHard       = "hard"
	WeightCapability = "capability"
	WeightMetadata   = "metadata"
	WeightSoft       = "soft"
)

const (
	GroupFingerprint = "fingerprint"
	GroupSource      = "source"
	GroupCapability  = "capability"
	GroupIdentity    = "identity"
	GroupConsistency = "consistency"
	GroupSignature   = "signature"
	GroupHosted      = "hosted"
	GroupCustom      = "custom"
)

const (
	hostedProfileGeneric = "generic"
	hostedProfileLegacy  = "t1y-legacy"
	hostedProfileCustom  = "custom"
)

type Config struct {
	Targets     []TargetSpec       `json:"targets"`
	Concurrency int                `json:"concurrency"`
	Repetitions int                `json:"repetitions"` // full per-model suite repetitions
	TimeoutMs   int                `json:"timeoutMs"`
	Retry       engine.RetryPolicy `json:"retry"`
	Checks      ChecksConfig       `json:"checks"`
	Challenge   ChallengeConfig    `json:"challenge"`
	Hosted      HostedConfig       `json:"hosted"`
	Thresholds  Thresholds         `json:"thresholds"`
	// CustomProbes lets advanced users add protocol-agnostic probes without a
	// code change. Each probe is one HTTP request plus deterministic
	// expectations; its request/response is stored like every built-in probe.
	CustomProbes []CustomProbe `json:"customProbes,omitempty"`
}

type CustomProbe struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Protocol string            `json:"protocol,omitempty"` // empty/auto = all protocols
	Method   string            `json:"method,omitempty"`   // POST (default) / GET
	Path     string            `json:"path"`               // relative to the target root, or an absolute URL
	Headers  map[string]string `json:"headers,omitempty"`
	Body     any               `json:"body,omitempty"`
	Expected []Expectation     `json:"expected,omitempty"`
}

type Expectation struct {
	Type  string `json:"type"` // status | json_path | contains | regex | header
	Path  string `json:"path,omitempty"`
	Value string `json:"value,omitempty"`
}

type TargetSpec struct {
	TargetID   string   `json:"targetId"`
	Models     []string `json:"models"`
	Protocol   string   `json:"protocol,omitempty"`
	Label      string   `json:"label,omitempty"`
	AuthMode   string   `json:"authMode,omitempty"`   // api-key (default) | bearer
	APIVersion string   `json:"apiVersion,omitempty"` // Azure only
	// APIStyle 选择 OpenAI 协议的来源探针接口：chat（默认）/ responses / auto。
	// 仅影响来源探针；指纹、能力挑战、身份与一致性仍走 Chat Completions。
	APIStyle string `json:"apiStyle,omitempty"`
}

type ChecksConfig struct {
	Fingerprint bool `json:"fingerprint"`
	Source      bool `json:"source"`
	Capability  bool `json:"capability"`
	Identity    bool `json:"identity"`
	Consistency bool `json:"consistency"`
	Signature   bool `json:"signature"`
	Tokenizer   bool `json:"tokenizer"`
}

type ChallengeConfig struct {
	ToolName     string   `json:"toolName"`
	NonceLength  int      `json:"nonceLength"`
	MaxTokens    int      `json:"maxTokens"`
	Temperature  *float64 `json:"temperature"`
	ForceTool    bool     `json:"forceTool"`
	SystemPrompt string   `json:"systemPrompt,omitempty"`
	Round1Prompt string   `json:"round1Prompt,omitempty"`
	Round2Prompt string   `json:"round2Prompt,omitempty"`
}

// HostedConfig is the non-secret part of the hosted proof configuration. The
// MCP URL/headers and proof-page details live in the encrypted settings store
// and are merged in at run time, so secrets never appear in run config JSON.
type HostedConfig struct {
	Enabled   bool            `json:"enabled"`
	Profile   string          `json:"profile,omitempty"`
	Providers map[string]bool `json:"providers,omitempty"`
	TimeoutMs int             `json:"timeoutMs"`
}

// HostedSettings is persisted encrypted under the authenticity.hosted setting.
type HostedSettings struct {
	Profile        string          `json:"profile"`
	DefaultEnabled bool            `json:"defaultEnabled"`
	Providers      map[string]bool `json:"providers,omitempty"`
	MCP            MCPConfig       `json:"mcp"`
	Proof          ProofConfig     `json:"proof"`
	Legacy         LegacyConfig    `json:"legacy"`
}

type MCPConfig struct {
	ServerLabel       string            `json:"serverLabel"`
	ServerDescription string            `json:"serverDescription"`
	ServerURL         string            `json:"serverUrl"`
	Headers           map[string]string `json:"headers"`
	RequireApproval   string            `json:"requireApproval"`
	ToolName          string            `json:"toolName"`
}

type ProofConfig struct {
	BaseURL            string `json:"baseUrl"`
	Domain             string `json:"domain"`
	PathTemplate       string `json:"pathTemplate"`
	ExpectedNonceField string `json:"expectedNonceField"`
}

type LegacyConfig struct {
	Domain           string         `json:"domain"`
	EnvVar           string         `json:"envVar"`
	ExpectedEnvValue string         `json:"expectedEnvValue"`
	Collection       string         `json:"collection"`
	ExpectedRecord   map[string]any `json:"expectedRecord"`
	Keyword          string         `json:"keyword"`
}

type Thresholds struct {
	TokenizerPct     float64 `json:"tokenizerPct"`
	MaxResponseBytes int64   `json:"maxResponseBytes"`
}

// Report is stored in RunSummary.Data and rendered by the frontend.
type Report struct {
	Models  []ModelReport `json:"models"`
	Summary VerdictCounts `json:"summary"`
	Notes   []string      `json:"notes"`
}

type VerdictCounts struct {
	Models       int `json:"models"`
	Authentic    int `json:"authentic"`
	Likely       int `json:"likely"`
	Suspicious   int `json:"suspicious"`
	Counterfeit  int `json:"counterfeit"`
	Inconclusive int `json:"inconclusive"`
	Error        int `json:"error"`
	Passed       int `json:"passed"`
	Failed       int `json:"failed"`
	NA           int `json:"na"`
}

type ModelReport struct {
	TargetID      string            `json:"targetId"`
	TargetName    string            `json:"targetName"`
	Model         string            `json:"model"`
	Protocol      string            `json:"protocol"`
	Endpoint      string            `json:"endpoint"`
	ResponseModel string            `json:"responseModel,omitempty"`
	Source        SourceReport      `json:"source"`
	Verdict       string            `json:"verdict"`
	VerdictLabel  string            `json:"verdictLabel"`
	Confidence    string            `json:"confidence"`
	Repetition    int               `json:"repetition,omitempty"`
	Repetitions   int               `json:"repetitions"`
	RepVerdicts   []RepVerdict      `json:"repVerdicts,omitempty"`
	Passed        int               `json:"passed"`
	Failed        int               `json:"failed"`
	NA            int               `json:"na"`
	Total         int               `json:"total"`
	DurationMs    int64             `json:"durationMs"`
	Dimensions    []DimensionReport `json:"dimensions"`
	Checks        []CheckReport     `json:"checks"`
	Challenge     ChallengeReport   `json:"challenge,omitempty"`
	Error         string            `json:"error,omitempty"`
}

type RepVerdict struct {
	Repetition int    `json:"repetition"`
	Verdict    string `json:"verdict"`
	Confidence string `json:"confidence"`
	Passed     int    `json:"passed"`
	Failed     int    `json:"failed"`
	NA         int    `json:"na"`
	DurationMs int64  `json:"durationMs"`
	Error      string `json:"error,omitempty"`
}

type SourceReport struct {
	Name       string   `json:"name"`
	Confidence string   `json:"confidence"` // high | medium | low | unknown
	Evidence   []string `json:"evidence,omitempty"`
	// Gateway 记录响应中检测到的中转网关特征（如 X-Oneapi-Request-Id）。
	// 它与上游来源是两个维度：中转站可以原样转发官方响应，因此网关特征
	// 不再覆盖来源判断，而是单独展示并用于 P2-3 检查。
	Gateway string `json:"gateway,omitempty"`
}

type DimensionReport struct {
	Key    string `json:"key"`
	Name   string `json:"name"`
	Passed int    `json:"passed"`
	Failed int    `json:"failed"`
	NA     int    `json:"na"`
}

type CheckReport struct {
	ID         string `json:"id"`
	Group      string `json:"group"`
	GroupName  string `json:"groupName"`
	Name       string `json:"name"`
	Status     string `json:"status"` // pass | fail | na | warn
	Weight     string `json:"weight"`
	Evidence   string `json:"evidence,omitempty"`
	DurationMs int64  `json:"durationMs,omitempty"`
	CaseID     string `json:"caseId,omitempty"`
	Repetition int    `json:"repetition,omitempty"`
}

type ChallengeReport struct {
	ToolName string `json:"toolName"`
	Nonce    string `json:"nonce"`
	Secret   string `json:"secret"`
	A        int    `json:"a"`
	B        int    `json:"b"`
	Sum      int    `json:"sum"`
}

func groupName(g string) string {
	switch g {
	case GroupFingerprint:
		return "接口指纹"
	case GroupSource:
		return "响应来源"
	case GroupCapability:
		return "真实能力"
	case GroupIdentity:
		return "模型身份"
	case GroupConsistency:
		return "数据一致性"
	case GroupSignature:
		return "思维签名真实性"
	case GroupHosted:
		return "工具调用情况"
	case GroupCustom:
		return "自定义探针"
	default:
		return "其他"
	}
}

func verdictLabel(v string) string {
	switch v {
	case VerdictAuthentic:
		return "通过"
	case VerdictLikely:
		return "较可信"
	case VerdictSuspicious:
		return "可疑"
	case VerdictCounterfeit:
		return "仿冒"
	case VerdictInconclusive:
		return "证据不足"
	case VerdictError:
		return "执行错误"
	default:
		return v
	}
}
