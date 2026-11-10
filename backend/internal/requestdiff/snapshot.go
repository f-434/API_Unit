// Package requestdiff implements request snapshots, structured JSON diffing and
// hidden-instruction scanning for the request-difference audit module.
package requestdiff

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"apiunit/internal/model"
	"apiunit/internal/protocol"
)

const (
	maxFindings     = 500
	snippetRadius   = 60
	maxBase64Report = 200
)

type SectionSet struct {
	System           []protocol.ChatMessage `json:"system,omitempty"`
	User             any                    `json:"user,omitempty"`
	History          []protocol.ChatMessage `json:"history,omitempty"`
	Tools            map[string]any         `json:"tools,omitempty"`
	FunctionResponse []protocol.ChatMessage `json:"functionResponse,omitempty"`
	Generation       map[string]any         `json:"generation,omitempty"`
	Safety           []protocol.SafetySetting `json:"safety,omitempty"`
	Cached           string                 `json:"cached,omitempty"`
	Media            []MediaRef             `json:"media,omitempty"`
	WebSearch        []string               `json:"webSearch,omitempty"`
	Other            map[string]any         `json:"other,omitempty"`
}

type MediaRef struct {
	MessageIndex int                 `json:"messageIndex"`
	Role         string              `json:"role"`
	Part         protocol.ContentPart `json:"part"`
	Path         string              `json:"path"`
}

func BuildSections(req protocol.ChatRequest) SectionSet {
	var s SectionSet
	lastUser := -1
	for i, m := range req.Messages {
		if strings.EqualFold(m.Role, "user") {
			lastUser = i
		}
	}
	for i, m := range req.Messages {
		role := strings.ToLower(m.Role)
		switch {
		case role == "system" || role == "developer":
			s.System = append(s.System, m)
		case role == "tool" || role == "function":
			s.FunctionResponse = append(s.FunctionResponse, m)
		case i == lastUser:
			cp := m
			s.User = cp
		default:
			s.History = append(s.History, m)
		}
		for j, p := range m.Parts {
			if strings.EqualFold(p.Type, "text") {
				continue
			}
			s.Media = append(s.Media, MediaRef{
				MessageIndex: i, Role: m.Role, Part: p,
				Path: fmt.Sprintf("$.messages[%d].parts[%d]", i, j),
			})
		}
	}
	if len(req.Tools) > 0 || req.ToolChoice != "" || len(req.BuiltinTools) > 0 {
		s.Tools = map[string]any{
			"tools":        req.Tools,
			"toolChoice":   req.ToolChoice,
			"builtinTools": req.BuiltinTools,
		}
	}
	s.Generation = map[string]any{
		"maxTokens": req.MaxTokens, "temperature": req.Temperature, "topP": req.TopP, "topK": req.TopK,
		"stop": req.Stop, "seed": req.Seed, "candidateCount": req.CandidateCount,
		"responseMimeType": req.ResponseMimeType, "responseSchema": req.ResponseSchema,
		"thinkingBudget": req.ThinkingBudget, "reasoningEffort": req.ReasoningEffort,
		"presencePenalty": req.PresencePenalty, "frequencyPenalty": req.FrequencyPenalty,
		"extra": req.ExtraGeneration,
	}
	s.Safety = req.SafetySettings
	s.Cached = req.CachedContent
	for _, b := range req.BuiltinTools {
		if b == "web_search" || b == "web_search_preview" || b == "google_search" {
			s.WebSearch = append(s.WebSearch, b)
		}
	}
	if len(req.ExtraBody) > 0 {
		s.Other = req.ExtraBody
	}
	return s
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func hashValue(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	switch string(b) {
	case "null", "{}", "[]", `""`:
		return ""
	}
	return sha256Hex(string(b))
}

func ComputeFingerprints(req protocol.ChatRequest) model.Fingerprints {
	s := BuildSections(req)
	return model.Fingerprints{
		System:           hashValue(s.System),
		User:             hashValue(s.User),
		History:          hashValue(s.History),
		Tools:            hashValue(s.Tools),
		FunctionResponse: hashValue(s.FunctionResponse),
		Generation:       hashValue(s.Generation),
		Safety:           hashValue(s.Safety),
		Cached:           hashValue(s.Cached),
		Media:            hashValue(s.Media),
		WebSearch:        hashValue(s.WebSearch),
		Other:            hashValue(s.Other),
	}
}

type hiddenRule struct {
	ID       string
	Severity string
	Detail   string
	Re       *regexp.Regexp
}

var hiddenRules = []hiddenRule{
	{"ignore_instructions", "high", "疑似要求忽略系统/先前指令", regexp.MustCompile(`(?i)(ignore|disregard|forget).{0,30}(previous|prior|above|system).{0,20}(instruction|prompt|message)|忽略.{0,12}(之前|以上|前面|系统).{0,8}(指令|提示|消息)|无视.{0,10}(指令|限制|规则)`)},
	{"role_override", "high", "疑似角色覆盖 / 越狱设定", regexp.MustCompile(`(?i)(you are now|from now on|act as|pretend (to be|you are)|你现在是|从现在起|扮演.{0,10}(角色|AI))`)},
	{"system_reference", "medium", "疑似引用系统提示词或隐藏指令", regexp.MustCompile(`(?i)(system prompt|system instruction|developer message|hidden instruction|系统提示词|系统指令|隐藏指令|开发者消息)`)},
	{"secret_exfiltration", "high", "疑似诱导泄露密钥、口令或提示词", regexp.MustCompile(`(?i)(reveal|print|output|leak|show).{0,24}(secret|api.?key|password|token|prompt)|(泄露|输出|打印|告诉我).{0,16}(密钥|口令|密码|校验码|提示词)`)},
	{"do_not_tell", "medium", "疑似隐蔽指令（要求对用户隐瞒）", regexp.MustCompile(`(?i)(do not tell|don't tell|keep this secret|不要告诉|不要提醒|不要让用户知道|保密)`)},
	{"role_marker", "high", "疑似伪造对话角色标记", regexp.MustCompile(`(?i)(<\|im_start\|>|<\|system\|>|\[system\]|###\s*(system|instruction)|BEGIN SYSTEM PROMPT)`)},
	{"html_comment", "medium", "文本中夹带 HTML 注释，可能隐藏指令", regexp.MustCompile(`(?s)<!--.*?-->`)},
	{"zero_width", "medium", "包含零宽或不可见 Unicode 字符", regexp.MustCompile("[\u200B-\u200F\u2060-\u2064\uFEFF]")},
	{"bidi_override", "high", "包含双向文本控制字符，可能隐藏/重排内容", regexp.MustCompile("[\u202A-\u202E\u2066-\u2069]")},
	{"tool_hijack", "high", "工具描述/返回中疑似包含劫持或外发指令", regexp.MustCompile(`(?i)(always (call|use)|when (the )?tool|send (the )?(data|result|response)|forward (the )?(data|result)|upload (the )?(data|result)|exfiltrat|始终调用|发送到|上传到|外发到)`)},
	{"external_url", "low", "包含外部 URL，需确认是否可信", regexp.MustCompile(`(?i)https?://[^\s"'<>]+`)},
}

var base64RE = regexp.MustCompile(`^[A-Za-z0-9+/_=\-]{200,}$`)

// ScanRequest applies deterministic hidden-instruction rules to the canonical
// request. It returns findings with JSONPath locations and categories.
func ScanRequest(req protocol.ChatRequest) []model.Finding {
	var out []model.Finding
	seen := map[string]bool{}
	add := func(category, rule, severity, path, snippet, detail string) {
		if len(out) >= maxFindings {
			return
		}
		key := category + "|" + rule + "|" + path + "|" + snippet
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, model.Finding{
			Category: category, Rule: rule, Severity: severity,
			Path: path, Snippet: snippet, Detail: detail,
		})
	}

	for i, m := range req.Messages {
		role := strings.ToLower(m.Role)
		cat := "history"
		switch {
		case role == "system" || role == "developer":
			cat = "system_prompt"
		case role == "tool" || role == "function":
			cat = "function_response"
		case role == "user" && isLastUser(req.Messages, i):
			cat = "user_prompt"
		}
		base := fmt.Sprintf("$.messages[%d]", i)
		scanText(protocol.MessageText(m), base+".content", cat, add)
		for j, p := range m.Parts {
			if strings.EqualFold(p.Type, "text") {
				scanText(p.Text, fmt.Sprintf("%s.parts[%d].text", base, j), cat, add)
			} else {
				scanMedia(p, fmt.Sprintf("%s.parts[%d]", base, j), add)
			}
		}
	}
	for i, t := range req.Tools {
		base := fmt.Sprintf("$.tools[%d]", i)
		scanText(t.Description, base+".description", "tools", add)
		if t.Parameters != nil {
			if b, err := json.Marshal(t.Parameters); err == nil {
				scanText(string(b), base+".parameters", "tools", add)
			}
		}
	}
	for i, p := range req.BuiltinTools {
		scanText(p, fmt.Sprintf("$.builtinTools[%d]", i), "web_search", add)
	}
	if len(req.ExtraBody) > 0 {
		if b, err := json.Marshal(req.ExtraBody); err == nil {
			scanText(string(b), "$.extraBody", "other", add)
		}
	}
	return out
}

func isLastUser(msgs []protocol.ChatMessage, idx int) bool {
	for i := len(msgs) - 1; i >= 0; i-- {
		if strings.EqualFold(msgs[i].Role, "user") {
			return i == idx
		}
	}
	return false
}

func scanMedia(p protocol.ContentPart, path string, add func(string, string, string, string, string, string)) {
	scanText(p.URL, path+".url", "media", add)
	scanText(p.Text, path+".text", "media", add)
	if p.Data != "" && len(p.Data) >= 200 && base64RE.MatchString(strings.TrimSpace(p.Data)) {
		snippet := fmt.Sprintf("base64 data len=%d prefix=%s", len(p.Data), truncate(p.Data, 48))
		add("media", "base64_blob", "low", path+".data", snippet, "外部数据以 base64 内嵌，无法直接扫描语义，需人工复核")
	}
}

func scanText(text, path, category string, add func(string, string, string, string, string, string)) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	for _, r := range hiddenRules {
		if r.ID == "external_url" && category != "tools" && category != "function_response" && category != "media" && category != "other" {
			continue
		}
		matches := r.Re.FindAllStringIndex(text, 3)
		for _, m := range matches {
			snippet := snippetAround(text, m[0], m[1], r.ID)
			add(category, r.ID, r.Severity, path, snippet, r.Detail)
		}
	}
	if len(text) >= 200 && !strings.ContainsAny(text, " \n\t") && base64RE.MatchString(text) {
		add(category, "base64_blob", "low", path, fmt.Sprintf("base64 text len=%d prefix=%s", len(text), truncate(text, 48)), "长 base64 文本可能编码了隐藏指令")
	}
}

func snippetAround(s string, start, end int, rule string) string {
	if rule == "zero_width" || rule == "bidi_override" {
		return "包含不可见/控制字符，建议用十六进制查看"
	}
	from := start - snippetRadius
	if from < 0 {
		from = 0
	}
	to := end + snippetRadius
	if to > len(s) {
		to = len(s)
	}
	return strings.TrimSpace(s[from:to])
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func ComputeBodySHA(body string) string { return sha256Hex(body) }

// BuildSnapshot assembles a snapshot from a completed engine item.
func BuildSnapshot(runID, kind string, item model.Item, target protocol.TargetInfo, label string, req protocol.ChatRequest) model.Snapshot {
	body := item.RequestJSON
	canonical, _ := json.Marshal(req)
	if strings.TrimSpace(body) == "" {
		// The request failed before a wire body was produced; keep the
		// canonical request so the snapshot is still auditable.
		body = string(canonical)
	}
	endpoint := ""
	if v, ok := item.Meta["endpoint"].(string); ok {
		endpoint = v
	}
	requestID := item.RequestID
	if requestID == "" {
		requestID = fmt.Sprintf("%s-%d", runID, item.Index)
	}
	reqTime := item.StartedAt
	if reqTime == 0 {
		reqTime = model.NowMs()
	}
	return model.Snapshot{
		RunID: runID, RunKind: kind, ItemIndex: item.Index,
		CaseID: item.CaseID, CaseName: item.CaseName,
		TargetID: target.ID, TargetName: target.Name, TargetLabel: label,
		Model: item.Model, Protocol: item.Protocol, Endpoint: endpoint,
		RequestID: requestID, Repetition: item.Repetition, RequestTime: reqTime,
		BodySHA256: ComputeBodySHA(body), BodySize: len(body),
		Fingerprints: ComputeFingerprints(req), Findings: ScanRequest(req),
		Body: body, Canonical: string(canonical), CreatedAt: model.NowMs(),
	}
}
