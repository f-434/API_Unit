// Package imagedetection 实现「生图检测」：沿用 image-param-verifier 的
// 参数一致性测试思路，真实验证 OpenAI Images 兼容接口的文生图、改图与
// 聊天生图能力；并在保存原始图片字节的基础上，检查 C2PA 元数据与
// SynthID 水印两类来源签名。
//
// 关键约束：图片必须以「上游返回的原始字节」落盘，不能解码后重新编码，
// 否则会破坏 C2PA 元数据，也可能削弱 SynthID 水印。
package imagedetection

import (
	"apiunit/internal/engine"
)

const (
	ModelKindImages = "images"
	ModelKindChat   = "chat"
)

const (
	PhaseGenTransparent = "generation_transparent"
	PhaseGenOpaque      = "generation_opaque"
	PhaseGenAuto        = "generation_auto"
	PhaseEditImages     = "edit_images_edits"
	PhaseEditAlias      = "edit_alias_edits"
	PhaseChatGeneration = "chat_generation"
	// PhaseEditSource 只在「仅勾选改图」时出现：自动补跑一次文生图作为改图输入。
	PhaseEditSource = "edit_source"
)

const (
	StatusPass = "pass"
	StatusFail = "fail"
	StatusNA   = "na"
	StatusWarn = "warn"
)

const (
	SignatureDetected    = "detected"
	SignatureNotDetected = "not_detected"
	SignaturePossible    = "possible"
	SignatureUnknown     = "unknown"
)

const (
	OnlineChecked      = "checked"
	OnlineUnconfigured = "unconfigured"
	OnlineError        = "error"
	OnlineSkipped      = "skipped"
)

// PhaseInfo 描述一个可选择的测试项，前端据此渲染勾选框。
type PhaseInfo struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	Kind  string `json:"kind"` // images 或 chat
}

// PhaseInfos 的顺序即前端展示顺序。
var PhaseInfos = []PhaseInfo{
	{PhaseGenTransparent, "文生图 · transparent", ModelKindImages},
	{PhaseGenOpaque, "文生图 · opaque", ModelKindImages},
	{PhaseGenAuto, "文生图 · auto", ModelKindImages},
	{PhaseEditImages, "改图 · /v1/images/edits", ModelKindImages},
	{PhaseEditAlias, "改图 · /v1/edits", ModelKindImages},
	{PhaseChatGeneration, "聊天生图 · /v1/chat/completions", ModelKindChat},
}

// Config 是本次生图检测的运行配置。
type Config struct {
	Targets     []TargetSpec       `json:"targets"`
	Concurrency int                `json:"concurrency"`
	TimeoutMs   int                `json:"timeoutMs"`
	Retry       engine.RetryPolicy `json:"retry"`
	// Phases 为空表示执行当前模型类型支持的全部测试项。
	Phases           []string        `json:"phases,omitempty"`
	GenerationPrompt string          `json:"generationPrompt,omitempty"`
	EditPrompt       string          `json:"editPrompt,omitempty"`
	OutputFormat     string          `json:"outputFormat,omitempty"`
	Signature        SignatureConfig `json:"signature"`
}

type SignatureConfig struct {
	// Local 控制是否解析本地 C2PA 元数据（不依赖任何外部服务）。
	Local bool `json:"local"`
	// Online 控制是否调用 OpenAI Content Provenance API 校验 C2PA + SynthID。
	Online bool `json:"online"`
}

type TargetSpec struct {
	TargetID   string      `json:"targetId"`
	Models     []ModelSpec `json:"models"`
	Protocol   string      `json:"protocol,omitempty"`
	Label      string      `json:"label,omitempty"`
	AuthMode   string      `json:"authMode,omitempty"`
	APIVersion string      `json:"apiVersion,omitempty"`
}

// ModelSpec 描述一个待测试的图片模型。Kind 为空时按名称推断，
// Size 为空时按名称推断（4k → 4096，2k → 2048，其余 1024）。
type ModelSpec struct {
	Name string `json:"name"`
	Kind string `json:"kind,omitempty"`
	Size string `json:"size,omitempty"`
}

// Report 存入 RunSummary.Data，由前端渲染。
type Report struct {
	Models  []ModelReport `json:"models"`
	Summary Summary       `json:"summary"`
	Notes   []string      `json:"notes"`
}

type Summary struct {
	Models          int `json:"models"`
	Phases          int `json:"phases"`
	Passed          int `json:"passed"`
	Failed          int `json:"failed"`
	NA              int `json:"na"`
	C2PADetected    int `json:"c2paDetected"`
	SynthIDDetected int `json:"synthIdDetected"`
	OnlineChecked   int `json:"onlineChecked"`
}

type ModelReport struct {
	TargetID   string        `json:"targetId"`
	TargetName string        `json:"targetName"`
	Model      string        `json:"model"`
	Kind       string        `json:"kind"`
	Size       string        `json:"size"`
	Protocol   string        `json:"protocol"`
	Endpoint   string        `json:"endpoint,omitempty"`
	Passed     int           `json:"passed"`
	Failed     int           `json:"failed"`
	NA         int           `json:"na"`
	Total      int           `json:"total"`
	DurationMs int64         `json:"durationMs"`
	Phases     []PhaseReport `json:"phases"`
	Error      string        `json:"error,omitempty"`
}

type PhaseReport struct {
	ID            string        `json:"id"`
	Name          string        `json:"name"`
	Status        string        `json:"status"` // pass | fail | na | warn
	Reason        string        `json:"reason,omitempty"`
	StatusCode    int           `json:"statusCode,omitempty"`
	ResponseModel string        `json:"responseModel,omitempty"`
	Checks        []CheckDetail `json:"checks"`
	Image         *ImageReport  `json:"image,omitempty"`
	DurationMs    int64         `json:"durationMs"`
	RequestID     string        `json:"requestId,omitempty"`
	Error         string        `json:"error,omitempty"`
	RequestJSON   string        `json:"requestJson,omitempty"`
	ResponseJSON  string        `json:"responseJson,omitempty"`
}

type CheckDetail struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// ImageReport 记录一张图片的元信息。Path 是站内图片地址，
// 前端可直接用于预览、下载和在线校验链接。
type ImageReport struct {
	Path        string          `json:"path"`
	UpstreamURL string          `json:"upstreamUrl,omitempty"`
	Format      string          `json:"format"`
	Width       int             `json:"width"`
	Height      int             `json:"height"`
	Bytes       int             `json:"bytes"`
	SHA256      string          `json:"sha256"`
	Signature   SignatureReport `json:"signature"`
}

// SignatureReport 汇总本地与在线两种签名检查结果。
type SignatureReport struct {
	C2PA    LocalSignature   `json:"c2pa"`
	SynthID LocalSignature   `json:"synthId"`
	Online  *OnlineSignature `json:"online,omitempty"`
	Links   []VerifyLink     `json:"links"`
	Notes   []string         `json:"notes,omitempty"`
}

// LocalSignature 是本地解析结果。C2PA 只检查元数据是否存在，
// 不做证书链验签；SynthID 本地只能检查显式标记，无法验证水印本身。
type LocalSignature struct {
	Status   string   `json:"status"` // detected | not_detected | possible | unknown
	Evidence []string `json:"evidence,omitempty"`
}

// OnlineSignature 是 OpenAI Content Provenance API 的返回结果。
// 该接口会同时检查 C2PA 元数据与 SynthID 水印。
type OnlineSignature struct {
	Status    string        `json:"status"` // checked | unconfigured | error | skipped
	Error     string        `json:"error,omitempty"`
	C2PA      OnlineC2PA    `json:"c2pa"`
	SynthID   OnlineSynthID `json:"synthId"`
	CreatedAt int64         `json:"createdAt,omitempty"`
	Raw       string        `json:"raw,omitempty"`
}

type OnlineC2PA struct {
	Outcome         string `json:"outcome,omitempty"`         // detected | not_detected
	ValidationState string `json:"validationState,omitempty"` // trusted | valid | invalid | not_present
	Issuer          string `json:"issuer,omitempty"`
	Model           string `json:"model,omitempty"`
	GeneratedAt     string `json:"generatedAt,omitempty"`
}

type OnlineSynthID struct {
	Outcome     string `json:"outcome,omitempty"`
	Model       string `json:"model,omitempty"`
	GeneratedAt string `json:"generatedAt,omitempty"`
}

// VerifyLink 指向第三方在线验签页面。AutoURL 在有公网图片地址时可用于
// Adobe Inspect 的 source 参数；其余站点需要手动上传图片。
type VerifyLink struct {
	Key     string `json:"key"` // adobe | openai | azure
	Label   string `json:"label"`
	URL     string `json:"url"`
	Mode    string `json:"mode"` // auto | upload
	Hint    string `json:"hint,omitempty"`
	AutoURL string `json:"autoUrl,omitempty"`
}

// LiveModel 是运行中的进度视图，通过 Session.Extra 推送到前端。
type LiveModel struct {
	TargetID   string `json:"targetId"`
	TargetName string `json:"targetName"`
	Model      string `json:"model"`
	Kind       string `json:"kind"`
	Size       string `json:"size"`
	Stage      string `json:"stage"`
	Done       bool   `json:"done"`
	Passed     int    `json:"passed"`
	Failed     int    `json:"failed"`
	NA         int    `json:"na"`
	Error      string `json:"error,omitempty"`
	StartedAt  int64  `json:"startedAt"`
	ElapsedMs  int64  `json:"elapsedMs"`
}

func phaseLabel(name string) string {
	switch name {
	case PhaseGenTransparent:
		return "文生图 · transparent"
	case PhaseGenOpaque:
		return "文生图 · opaque"
	case PhaseGenAuto:
		return "文生图 · auto"
	case PhaseEditImages:
		return "改图 · /v1/images/edits"
	case PhaseEditAlias:
		return "改图 · /v1/edits"
	case PhaseChatGeneration:
		return "聊天生图"
	case PhaseEditSource:
		return "自动生成改图原图"
	default:
		return name
	}
}
