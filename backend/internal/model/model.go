// Package model contains the domain types shared by the storage layer, the
// test engine and the HTTP API. JSON tags are camelCase because the same types
// are serialized to the React frontend.
package model

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

type Protocol string

const (
	ProtocolAuto   Protocol = "auto"
	ProtocolOpenAI Protocol = "openai"
	ProtocolClaude Protocol = "claude"
	ProtocolGemini Protocol = "gemini"
)

type RunKind string

const (
	KindStress      RunKind = "stress"
	KindTokenAudit  RunKind = "token_audit"
	KindRequestDiff RunKind = "request_diff"
	KindInjection   RunKind = "injection"
)

type RunStatus string

const (
	StatusQueued      RunStatus = "queued"
	StatusRunning     RunStatus = "running"
	StatusCompleted   RunStatus = "completed"
	StatusStopped     RunStatus = "stopped"
	StatusFailed      RunStatus = "failed"
	StatusInterrupted RunStatus = "interrupted"
)

func NowMs() int64 { return time.Now().UnixMilli() }

// NewID returns a sortable, opaque identifier such as r_1m2n3o4o5a1b2c3d4.
func NewID(prefix string) string {
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s%x%s", prefix, time.Now().UnixMilli(), hex.EncodeToString(b))
}

// Target is a New API / OpenAI-compatible endpoint plus a stored credential.
type Target struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	BaseURL      string   `json:"baseUrl"`
	Protocol     Protocol `json:"protocol"`
	HasKey       bool     `json:"hasKey"`
	KeyMasked    string   `json:"keyMasked"`
	ModelCount   int      `json:"modelCount,omitempty"`
	CreatedAt    int64    `json:"createdAt"`
	UpdatedAt    int64    `json:"updatedAt"`
	LastTestedAt int64    `json:"lastTestedAt,omitempty"`
}

// TargetSecret is the internal representation including the decrypted key.
type TargetSecret struct {
	Target
	APIKey string `json:"-"`
}

func MaskKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	r := []rune(key)
	if len(r) <= 8 {
		return "****"
	}
	return string(r[:4]) + "****" + string(r[len(r)-4:])
}

// Prompt is a reusable input used by token audits and (optionally) stress runs.
type Prompt struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Category  string `json:"category"`
	Content   string `json:"content"`
	Builtin   bool   `json:"builtin"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

// Check is a deterministic success criterion for an injection test case.
type Check struct {
	Kind        string `json:"kind"` // canary_leak | contains | regex | refusal
	Value       string `json:"value,omitempty"`
	Description string `json:"description,omitempty"`
}

// Attack is a prompt-injection test case.
type Attack struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Category     string  `json:"category"`
	Severity     string  `json:"severity"` // low | medium | high | critical
	SystemPrompt string  `json:"systemPrompt"`
	Prompt       string  `json:"prompt"`
	Canary       string  `json:"canary"`
	Checks       []Check `json:"checks"`
	RequireJudge bool    `json:"requireJudge"`
	Rubric       string  `json:"rubric"`
	Builtin      bool    `json:"builtin"`
	CreatedAt    int64   `json:"createdAt"`
	UpdatedAt    int64   `json:"updatedAt"`
}

type Price struct {
	ID             string  `json:"id"`
	Pattern        string  `json:"pattern"`
	InputPerM      float64 `json:"inputPerM"`
	OutputPerM     float64 `json:"outputPerM"`
	CacheReadPerM  float64 `json:"cacheReadPerM"`
	CacheWritePerM float64 `json:"cacheWritePerM"`
	Currency       string  `json:"currency"`
	Note           string  `json:"note,omitempty"`
	UpdatedAt      int64   `json:"updatedAt"`
}

// Run is the persisted run header. Config and Summary are opaque JSON blobs.
type Run struct {
	ID         string    `json:"id"`
	Kind       RunKind  `json:"kind"`
	Status     RunStatus `json:"status"`
	Config     string    `json:"-"`
	Summary    string    `json:"-"`
	Error      string    `json:"error,omitempty"`
	CreatedAt  int64     `json:"createdAt"`
	StartedAt  int64     `json:"startedAt"`
	FinishedAt int64     `json:"finishedAt"`
	DurationMs int64     `json:"durationMs"`
}

// RunSummary is the headline persisted for every run. Data holds the
// module-specific report payload (stress aggregate, audit matrix, ASR tables).
type RunSummary struct {
	Planned     int64    `json:"planned"`
	Total       int64    `json:"total"`
	Success     int64    `json:"success"`
	Failed      int64    `json:"failed"`
	Canceled    int64    `json:"canceled"`
	SuccessRate float64  `json:"successRate"`
	QPS         float64  `json:"qps"`
	ASR         float64  `json:"asr,omitempty"`
	ElapsedMs   float64  `json:"elapsedMs"`
	Cost        float64  `json:"cost,omitempty"`
	Models      []string `json:"models,omitempty"`
	Data        any      `json:"data,omitempty"`
}

type JudgeVerdict struct {
	Method   string  `json:"method"` // rule | llm | hybrid
	Verdict  string  `json:"verdict"` // success | blocked | unclear | error
	Score    float64 `json:"score"`
	Reason   string  `json:"reason"`
	Category string  `json:"category,omitempty"`
	Model    string  `json:"model,omitempty"`
	Tokens   int     `json:"tokens,omitempty"`
	Cost     float64 `json:"cost,omitempty"`
	Raw      string  `json:"raw,omitempty"`
}

type CheckResult struct {
	Check     Check  `json:"check"`
	Matched   bool   `json:"matched"`
	Evidence  string `json:"evidence,omitempty"`
	Decisive  bool   `json:"decisive"`
}

// Item is one logical request. Streaming retries are folded into Attempts.
type Item struct {
	ID       int64  `json:"id"`
	RunID    string `json:"runId"`
	Index    int    `json:"index"`
	Attempts int    `json:"attempts"`

	TargetID   string `json:"targetId,omitempty"`
	TargetName string `json:"targetName,omitempty"`
	Model      string `json:"model"`
	Protocol   string `json:"protocol"`
	CaseID     string `json:"caseId,omitempty"`
	CaseName   string `json:"caseName,omitempty"`
	Category   string `json:"category,omitempty"`
	Severity   string `json:"severity,omitempty"`
	Repetition int    `json:"repetition,omitempty"`

	StartedAt     int64   `json:"startedAt"`
	StartOffsetMs float64 `json:"startOffsetMs"`
	LatencyMs     float64 `json:"latencyMs"`
	TTFTMs        float64 `json:"ttftMs"`
	GenerationMs  float64 `json:"generationMs"`
	TPOTMs        float64 `json:"tpotMs"`

	Status int  `json:"status"`
	OK     bool `json:"ok"`

	PromptTokens     int    `json:"promptTokens"`
	CompletionTokens int    `json:"completionTokens"`
	TotalTokens      int    `json:"totalTokens"`
	CachedTokens     int    `json:"cachedTokens"`
	CacheWriteTokens int    `json:"cacheWriteTokens"`
	ReasoningTokens  int    `json:"reasoningTokens"`
	LocalPromptTokens int   `json:"localPromptTokens,omitempty"`
	TokenSource      string `json:"tokenSource"` // usage | mixed | estimated | none
	Cost             float64 `json:"cost"`

	ErrorType    string `json:"errorType,omitempty"`
	ErrorMessage string `json:"errorMessage,omitempty"`
	RequestID    string `json:"requestId,omitempty"`
	FinishReason string `json:"finishReason,omitempty"`

	OutputPreview string `json:"outputPreview,omitempty"`
	OutputText    string `json:"-"` // full text, stored but not listed
	RequestJSON   string `json:"-"` // raw request body
	ResponseJSON  string `json:"-"` // raw response body / assembled stream

	Verdict string        `json:"verdict,omitempty"`
	Judge   *JudgeVerdict `json:"judge,omitempty"`
	Checks  []CheckResult `json:"checks,omitempty"`

	Meta      map[string]any `json:"meta,omitempty"`
	CreatedAt int64          `json:"createdAt,omitempty"`
}

// ItemDetail adds the heavy fields for the detail drawer.
type ItemDetail struct {
	Item
	OutputText   string `json:"outputText"`
	RequestJSON  string `json:"requestJson"`
	ResponseJSON string `json:"responseJson"`
}

func (it Item) Detail() ItemDetail {
	return ItemDetail{Item: it, OutputText: it.OutputText, RequestJSON: it.RequestJSON, ResponseJSON: it.ResponseJSON}
}

type Stats struct {
	Count int     `json:"count"`
	Min   float64 `json:"min"`
	Max   float64 `json:"max"`
	Avg   float64 `json:"avg"`
	P50   float64 `json:"p50"`
	P90   float64 `json:"p90"`
	P95   float64 `json:"p95"`
	P99   float64 `json:"p99"`
	Sum   float64 `json:"sum"`
}

type TokenStats struct {
	Prompt        int64   `json:"prompt"`
	Completion    int64   `json:"completion"`
	Total         int64   `json:"total"`
	Cached        int64   `json:"cached"`
	CacheWrite    int64   `json:"cacheWrite"`
	Reasoning     int64   `json:"reasoning"`
	AvgCompletion float64 `json:"avgCompletion"`
	AvgTPS        float64 `json:"avgTokensPerSec"`
	Estimated     int64   `json:"estimated"`
	Cost          float64 `json:"cost"`
}

type StatusCount struct {
	Status int   `json:"status"`
	Count  int64 `json:"count"`
}

type ErrorGroup struct {
	Type     string   `json:"type"`
	Message  string   `json:"message"`
	Count    int64    `json:"count"`
	Statuses []int    `json:"statuses,omitempty"`
	Models   []string `json:"models,omitempty"`
	FirstAtMs float64 `json:"firstAtMs,omitempty"`
}

type SeriesPoint struct {
	Sec               int     `json:"sec"`
	Started           int64   `json:"started"`
	Completed         int64   `json:"completed"`
	Success           int64   `json:"success"`
	Failed            int64   `json:"failed"`
	ActiveConcurrency float64 `json:"activeConcurrency"`
	AvgLatencyMs      float64 `json:"avgLatencyMs"`
	RPS               float64 `json:"rps"`
}

type HistBucket struct {
	Label string `json:"label"`
	From  float64 `json:"from"`
	To    float64 `json:"to"`
	Count int64  `json:"count"`
}

type Aggregate struct {
	Total       int64         `json:"total"`
	Success     int64         `json:"success"`
	Failed      int64         `json:"failed"`
	Canceled    int64         `json:"canceled"`
	SuccessRate float64       `json:"successRate"`
	ErrorRate   float64       `json:"errorRate"`
	QPS         float64       `json:"qps"`
	ElapsedMs   float64       `json:"elapsedMs"`
	Latency     Stats         `json:"latency"`
	TTFT        Stats         `json:"ttft"`
	TPOT        Stats         `json:"tpot"`
	Tokens      TokenStats    `json:"tokens"`
	StatusCodes []StatusCount `json:"statusCodes"`
	Errors      []ErrorGroup  `json:"errors"`
	Series      []SeriesPoint `json:"series"`
	Histogram   []HistBucket  `json:"histogram"`
}

type ModelAggregate struct {
	Model      string `json:"model"`
	TargetID   string `json:"targetId,omitempty"`
	TargetName string `json:"targetName,omitempty"`
	Protocol   string `json:"protocol"`
	Endpoint   string `json:"endpoint,omitempty"`
	Aggregate
}

type ModelLive struct {
	Model       string `json:"model"`
	TargetName  string `json:"targetName,omitempty"`
	Protocol    string `json:"protocol"`
	Completed   int64  `json:"completed"`
	Success     int64  `json:"success"`
	Failed      int64  `json:"failed"`
	Active      int    `json:"active"`
	AvgLatency  float64 `json:"avgLatencyMs"`
	TTFT        float64 `json:"ttftMs"`
	QPS         float64 `json:"qps"`
	SuccessRate float64 `json:"successRate"`
}

// Live is the bounded snapshot pushed over SSE every 500ms.
type Live struct {
	RunID             string         `json:"runId"`
	Kind              string         `json:"kind"`
	Status            string         `json:"status"`
	Phase             string         `json:"phase,omitempty"`
	ElapsedMs         int64          `json:"elapsedMs"`
	Planned           int64          `json:"planned"`
	Completed         int64          `json:"completed"`
	Success           int64          `json:"success"`
	Failed            int64          `json:"failed"`
	Canceled          int64          `json:"canceled"`
	SuccessRate       float64        `json:"successRate"`
	QPS               float64        `json:"qps"`
	ActiveConcurrency int            `json:"activeConcurrency"`
	CurrentModel      string         `json:"currentModel,omitempty"`
	Latency           Stats          `json:"latency"`
	TTFT              Stats          `json:"ttft"`
	TPOT              Stats          `json:"tpot"`
	Tokens            TokenStats     `json:"tokens"`
	StatusCodes       []StatusCount  `json:"statusCodes"`
	Errors            []ErrorGroup   `json:"errors"`
	Series            []SeriesPoint  `json:"series"`
	Histogram         []HistBucket   `json:"histogram"`
	Models            []ModelLive    `json:"models"`
	Recent            []Item         `json:"recent"`
	Extra             map[string]any `json:"extra,omitempty"`
	Error             string         `json:"error,omitempty"`
}

// Fingerprints are SHA-256 hashes of the canonical request sections. They let
// the UI detect additions, removals and tampering without diffing every byte.
type Fingerprints struct {
	System           string `json:"system,omitempty"`
	User             string `json:"user,omitempty"`
	History          string `json:"history,omitempty"`
	Tools            string `json:"tools,omitempty"`
	FunctionResponse string `json:"functionResponse,omitempty"`
	Generation       string `json:"generation,omitempty"`
	Safety           string `json:"safety,omitempty"`
	Cached           string `json:"cached,omitempty"`
	Media            string `json:"media,omitempty"`
	WebSearch        string `json:"webSearch,omitempty"`
	Other            string `json:"other,omitempty"`
}

type Finding struct {
	Category string `json:"category"`
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Path     string `json:"path"`
	Snippet  string `json:"snippet"`
	Detail   string `json:"detail,omitempty"`
}

// Snapshot is one complete request captured during a request-difference audit.
type Snapshot struct {
	ID           int64        `json:"id"`
	RunID        string       `json:"runId"`
	RunKind      string       `json:"runKind"`
	ItemIndex    int          `json:"itemIndex"`
	CaseID       string       `json:"caseId"`
	CaseName     string       `json:"caseName"`
	TargetID     string       `json:"targetId"`
	TargetName   string       `json:"targetName"`
	TargetLabel  string       `json:"targetLabel"`
	Model        string       `json:"model"`
	Protocol     string       `json:"protocol"`
	Endpoint     string       `json:"endpoint"`
	RequestID    string       `json:"requestId"`
	Repetition   int          `json:"repetition"`
	RequestTime  int64        `json:"requestTimeMs"`
	BodySHA256   string       `json:"bodySha256"`
	BodySize     int          `json:"bodySize"`
	Fingerprints Fingerprints `json:"fingerprints"`
	Findings     []Finding    `json:"findings"`
	CreatedAt    int64        `json:"createdAt"`

	Body      string `json:"-"`
	Canonical string `json:"-"`
}

type SnapshotDetail struct {
	Snapshot
	Body      string `json:"body"`
	Canonical string `json:"canonical"`
}

func (s Snapshot) Detail() SnapshotDetail {
	return SnapshotDetail{Snapshot: s, Body: s.Body, Canonical: s.Canonical}
}

type RunView struct {
	Run     Run             `json:"run"`
	Summary RunSummary      `json:"summary"`
	Live    *Live           `json:"live,omitempty"`
	Config  any             `json:"config,omitempty"`
	Data    any             `json:"data,omitempty"`
	Playbook []RunPlaybook  `json:"playbook,omitempty"`
}

type RunPlaybook struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

func ValidProtocol(p Protocol) bool {
	switch p {
	case "", ProtocolAuto, ProtocolOpenAI, ProtocolClaude, ProtocolGemini:
		return true
	}
	return false
}

func InferProtocol(model string) Protocol {
	m := strings.ToLower(model)
	if i := strings.IndexAny(m, "/:"); i >= 0 {
		m = m[i+1:]
	}
	switch {
	case strings.Contains(m, "claude"):
		return ProtocolClaude
	case strings.Contains(m, "gemini") || strings.Contains(m, "learnlm"):
		return ProtocolGemini
	default:
		return ProtocolOpenAI
	}
}
