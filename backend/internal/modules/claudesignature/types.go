// Package claudesignature 对 Claude Messages API 返回的 thinking 签名做
// 通用 protobuf 解包，并检查签名内嵌元数据与本次请求是否一致。
//
// 边界：signature 字段是 base64 编码的 protobuf，不是加密数据；本包只做
// 结构解析和启发式元数据提取，无法验证密码学签名（客户端拿不到 Anthropic
// 的验签公钥），因此结论只表示“结构与元数据未发现矛盾”，不代表签名真实。
package claudesignature

// 单次请求的核验结论。
const (
	VerdictPass         = "pass"         // 解析成功，未发现矛盾
	VerdictSuspicious   = "suspicious"   // 签名缺失/解析失败/元数据矛盾/重复
	VerdictInconclusive = "inconclusive" // 没有可解析的签名（未返回 thinking 或仅 redacted）
	VerdictError        = "error"        // 请求、鉴权或响应解析失败
)

// 检查项状态，与项目内其他模块保持一致。
const (
	CheckPass = "pass"
	CheckFail = "fail"
	CheckWarn = "warn"
	CheckNA   = "na"
)

// 签名布局。full 表示内嵌 model/uuid/timestamp；minimal 只有 block type；
// unknown 表示 protobuf 能解析但未识别出关注字段；opaque 表示 redacted_thinking。
const (
	LayoutFull    = "full"
	LayoutMinimal = "minimal"
	LayoutUnknown = "unknown"
	LayoutOpaque  = "opaque"
)

// Config 是一次 Claude 签名核验的运行配置。
type Config struct {
	Targets      []TargetSpec `json:"targets"`
	Count        int          `json:"count"`       // 每个模型的请求次数
	Concurrency  int          `json:"concurrency"` // 同时执行的模型数
	DelayMs      int          `json:"delayMs"`     // 同一模型相邻请求的间隔
	TimeoutMs    int          `json:"timeoutMs"`
	MaxTokens    int          `json:"maxTokens"`
	Thinking     string       `json:"thinking"` // auto | adaptive | budget | off
	BudgetTokens int          `json:"budgetTokens"`
	Effort       string       `json:"effort"` // "" | low | medium | high | xhigh | max
	Prompt       string       `json:"prompt"` // 可选的基础提示词；留空自动生成
	Retry        RetryConfig  `json:"retry"`
}

// TargetSpec 描述一个上游及其要测试的模型。该模块固定使用 Claude Messages 协议。
type TargetSpec struct {
	TargetID string   `json:"targetId"`
	Models   []string `json:"models"`
	// AuthMode: auto | api-key | bearer。auto 会按 Key 形态选择 x-api-key 或 Bearer。
	AuthMode string `json:"authMode,omitempty"`
	Label    string `json:"label,omitempty"`
}

// RetryConfig 只重试传输错误、429 与 5xx；参数不兼容会走 thinking 回退，不占重试次数。
type RetryConfig struct {
	MaxRetries     int  `json:"maxRetries"`
	BackoffMs      int  `json:"backoffMs"`
	MaxBackoffMs   int  `json:"maxBackoffMs"`
	RetryOn429     bool `json:"retryOn429"`
	RetryOn5xx     bool `json:"retryOn5xx"`
	RetryOnNetwork bool `json:"retryOnNetwork"`
}

// Report 是持久化到 RunSummary.Data 的模块报告。
type Report struct {
	Models   []ModelReport   `json:"models"`
	Requests []RequestReport `json:"requests"`
	Summary  Summary         `json:"summary"`
	Notes    []string        `json:"notes"`
}

// Summary 是整个运行的汇总。通过率只统计“通过 + 可疑 + 执行错误”，
// 证据不足不计入分母，避免把“模型没有返回 thinking”当成失败。
type Summary struct {
	Models        int     `json:"models"`
	Requests      int     `json:"requests"`
	Planned       int     `json:"planned"`
	Passed        int     `json:"passed"`
	Suspicious    int     `json:"suspicious"`
	Inconclusive  int     `json:"inconclusive"`
	Errors        int     `json:"errors"`
	Signatures    int     `json:"signatures"`
	Parsed        int     `json:"parsed"`
	Full          int     `json:"full"`
	Minimal       int     `json:"minimal"`
	Unknown       int     `json:"unknown"`
	Opaque        int     `json:"opaque"`
	Duplicates    int     `json:"duplicates"`
	WithRequestID int     `json:"withRequestId"`
	Cost          float64 `json:"cost"`
	ElapsedMs     float64 `json:"elapsedMs"`
}

// ModelReport 是一个上游 + 模型的汇总。
type ModelReport struct {
	TargetID     string  `json:"targetId"`
	TargetName   string  `json:"targetName"`
	Model        string  `json:"model"`
	Protocol     string  `json:"protocol"`
	Endpoint     string  `json:"endpoint"`
	AuthMode     string  `json:"authMode"`
	Requests     int     `json:"requests"`
	Passed       int     `json:"passed"`
	Suspicious   int     `json:"suspicious"`
	Inconclusive int     `json:"inconclusive"`
	Errors       int     `json:"errors"`
	Signatures   int     `json:"signatures"`
	Parsed       int     `json:"parsed"`
	Duplicates   int     `json:"duplicates"`
	Cost         float64 `json:"cost"`
	DurationMs   int64   `json:"durationMs"`
	Error        string  `json:"error,omitempty"`
}

// RequestReport 是一条请求的完整核验记录，主列表直接消费这些字段。
type RequestReport struct {
	Index         int    `json:"index"`       // 模型内序号，从 1 开始
	GlobalIndex   int    `json:"globalIndex"` // 报告内序号，从 1 开始
	TargetID      string `json:"targetId"`
	TargetName    string `json:"targetName"`
	Model         string `json:"model"`
	ResponseModel string `json:"responseModel,omitempty"`
	Protocol      string `json:"protocol"`
	Endpoint      string `json:"endpoint"`
	// RequestID 优先取响应头 oneapi-request-id / x-oneapi-request-id。
	RequestID    string          `json:"requestId,omitempty"`
	ResponseID   string          `json:"responseId,omitempty"`
	HTTPStatus   int             `json:"httpStatus"`
	LatencyMs    float64         `json:"latencyMs"`
	RequestTime  int64           `json:"requestTime"`
	Prompt       string          `json:"prompt"`
	Verdict      string          `json:"verdict"`
	VerdictLabel string          `json:"verdictLabel"`
	Error        string          `json:"error,omitempty"`
	ErrorType    string          `json:"errorType,omitempty"`
	StopReason   string          `json:"stopReason,omitempty"`
	Attempts     int             `json:"attempts"`
	Cost         float64         `json:"cost"`
	Usage        Usage           `json:"usage"`
	Blocks       []Block         `json:"blocks"`
	Signatures   []SignatureInfo `json:"signatures"`
	Checks       []CheckReport   `json:"checks"`
	Duplicate    bool            `json:"duplicate,omitempty"`
}

// Usage 是 Claude 响应中的计费字段。
type Usage struct {
	InputTokens      int `json:"inputTokens"`
	OutputTokens     int `json:"outputTokens"`
	TotalTokens      int `json:"totalTokens"`
	CacheReadTokens  int `json:"cacheReadTokens"`
	CacheWriteTokens int `json:"cacheWriteTokens"`
}

// Block 是响应 content 块的轻量摘要。
type Block struct {
	Type         string `json:"type"`
	TextChars    int    `json:"textChars,omitempty"`
	HasSignature bool   `json:"hasSignature,omitempty"`
}

// SignatureInfo 是一个 thinking / redacted_thinking 块的解析结果。
type SignatureInfo struct {
	BlockType         string   `json:"blockType"` // 响应块类型：thinking | redacted_thinking
	SignatureChars    int      `json:"signatureChars"`
	ProtoBytes        int      `json:"protoBytes"`
	Layout            string   `json:"layout"`
	Model             string   `json:"model,omitempty"`
	ModelSource       string   `json:"modelSource,omitempty"` // meta | nested | search
	EmbeddedBlockType string   `json:"embeddedBlockType,omitempty"`
	BlockTypeSource   string   `json:"blockTypeSource,omitempty"` // meta | search
	UUID              string   `json:"uuid,omitempty"`
	UUIDSource        string   `json:"uuidSource,omitempty"`
	UnixTimestamp     int64    `json:"unixTimestamp,omitempty"`
	TimestampSource   string   `json:"timestampSource,omitempty"` // field | meta | search
	SignedAtUTC       string   `json:"signedAtUtc,omitempty"`
	SignedAtUTC8      string   `json:"signedAtUtc8,omitempty"`
	MetaPath          string   `json:"metaPath,omitempty"`
	MetaFields        []int    `json:"metaFields,omitempty"`
	TotalFields       int      `json:"totalFields"`
	Warnings          []string `json:"warnings,omitempty"`
	Tree              string   `json:"tree,omitempty"`
	SignaturePrefix   string   `json:"signaturePrefix,omitempty"`
	SignatureSHA256   string   `json:"signatureSha256,omitempty"`
	ParseError        string   `json:"parseError,omitempty"`
	Opaque            bool     `json:"opaque,omitempty"`
	Recognized        bool     `json:"recognized"`
}

// CheckReport 是确定性检查结果。
type CheckReport struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	Evidence string `json:"evidence,omitempty"`
}

func verdictLabel(v string) string {
	switch v {
	case VerdictPass:
		return "通过"
	case VerdictSuspicious:
		return "可疑"
	case VerdictInconclusive:
		return "证据不足"
	case VerdictError:
		return "执行错误"
	}
	return v
}
