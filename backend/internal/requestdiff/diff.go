package requestdiff

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"apiunit/internal/model"
)

const (
	maxDiffChanges = 3000
	maxDiffDepth   = 64
	maxValueBytes  = 2048
)

type Change struct {
	Op        string `json:"op"` // add | remove | replace
	Path      string `json:"path"`
	Category  string `json:"category"`
	Severity  string `json:"severity"`
	Before    any    `json:"before,omitempty"`
	After     any    `json:"after,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

type SectionChange struct {
	Name    string `json:"name"`
	Label   string `json:"label"`
	Left    string `json:"left"`
	Right   string `json:"right"`
	Changed bool   `json:"changed"`
}

type Summary struct {
	Total      int            `json:"total"`
	Added      int            `json:"added"`
	Removed    int            `json:"removed"`
	Replaced   int            `json:"replaced"`
	Truncated  bool           `json:"truncated"`
	ByCategory map[string]int `json:"byCategory"`
	BySeverity map[string]int `json:"bySeverity"`
}

type Result struct {
	LeftSHA   string          `json:"leftSha"`
	RightSHA  string          `json:"rightSha"`
	LeftType  string          `json:"leftType,omitempty"`
	RightType string          `json:"rightType,omitempty"`
	Changes   []Change        `json:"changes"`
	Summary   Summary         `json:"summary"`
	Sections  []SectionChange `json:"sections,omitempty"`
}

func DiffJSON(leftRaw, rightRaw string) (*Result, error) {
	left, err := parseJSON(leftRaw)
	if err != nil {
		return nil, fmt.Errorf("左侧 JSON 解析失败: %w", err)
	}
	right, err := parseJSON(rightRaw)
	if err != nil {
		return nil, fmt.Errorf("右侧 JSON 解析失败: %w", err)
	}
	res := Diff(left, right)
	res.LeftSHA = sha256Hex(leftRaw)
	res.RightSHA = sha256Hex(rightRaw)
	return res, nil
}

func parseJSON(raw string) (any, error) {
	if strings.TrimSpace(raw) == "" {
		return map[string]any{}, nil
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, err
	}
	return v, nil
}

func Diff(left, right any) *Result {
	d := &differ{
		leftRoot: left, rightRoot: right,
		leftLastUser: lastUserIndex(left), rightLastUser: lastUserIndex(right),
	}
	d.walk(left, right, "$", 0)
	res := &Result{Changes: d.changes, LeftType: typeName(left), RightType: typeName(right)}
	res.Summary = summarize(d.changes, d.truncated)
	return res
}

type differ struct {
	leftRoot, rightRoot any
	leftLastUser        int
	rightLastUser       int
	changes             []Change
	truncated           bool
}

func (d *differ) walk(left, right any, path string, depth int) {
	if len(d.changes) >= maxDiffChanges {
		d.truncated = true
		return
	}
	if depth > maxDiffDepth {
		d.truncated = true
		return
	}
	switch l := left.(type) {
	case map[string]any:
		r, ok := right.(map[string]any)
		if !ok {
			d.addChange("replace", path, left, right)
			return
		}
		keys := make([]string, 0, len(l)+len(r))
		for k := range l {
			keys = append(keys, k)
		}
		for k := range r {
			if _, exists := l[k]; !exists {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			lv, lok := l[k]
			rv, rok := r[k]
			p := joinPath(path, k)
			switch {
			case lok && !rok:
				d.addChange("remove", p, lv, nil)
			case !lok && rok:
				d.addChange("add", p, nil, rv)
			default:
				d.walk(lv, rv, p, depth+1)
			}
		}
	case []any:
		r, ok := right.([]any)
		if !ok {
			d.addChange("replace", path, left, right)
			return
		}
		max := len(l)
		if len(r) > max {
			max = len(r)
		}
		for i := 0; i < max; i++ {
			p := fmt.Sprintf("%s[%d]", path, i)
			switch {
			case i >= len(l):
				d.addChange("add", p, nil, r[i])
			case i >= len(r):
				d.addChange("remove", p, l[i], nil)
			default:
				d.walk(l[i], r[i], p, depth+1)
			}
		}
	default:
		if !scalarEqual(left, right) {
			d.addChange("replace", path, left, right)
		}
	}
}

func (d *differ) addChange(op, path string, before, after any) {
	if len(d.changes) >= maxDiffChanges {
		d.truncated = true
		return
	}
	cat, sev := d.classify(path, before, after)
	bc, bTrunc := summarizeValue(before)
	ac, aTrunc := summarizeValue(after)
	d.changes = append(d.changes, Change{
		Op: op, Path: path, Category: cat, Severity: sev,
		Before: bc, After: ac, Truncated: bTrunc || aTrunc,
	})
}

func (d *differ) classify(path string, before, after any) (string, string) {
	cat := ""
	if isFunctionResponseValue(before) || isFunctionResponseValue(after) {
		cat = "function_response"
	} else if isToolsValue(before) || isToolsValue(after) {
		cat = "tools"
	} else if isMediaValue(before) || isMediaValue(after) {
		cat = "media"
	}
	if cat == "" {
		cat = classifyPath(path, d.leftRoot, d.rightRoot, d.leftLastUser, d.rightLastUser)
	}
	sev := "low"
	switch cat {
	case "system_prompt", "user_prompt", "history", "tools", "function_response", "media":
		sev = "high"
	case "generation_config", "safety_settings", "cached_content", "web_search":
		sev = "medium"
	}
	return cat, sev
}

func valueKeys(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func isFunctionResponseValue(v any) bool {
	m := valueKeys(v)
	if m == nil {
		return false
	}
	for _, k := range []string{"functionResponse", "tool_result", "toolCallId", "tool_call_id"} {
		if _, ok := m[k]; ok {
			return true
		}
	}
	return false
}

func isToolsValue(v any) bool {
	m := valueKeys(v)
	if m == nil {
		return false
	}
	for _, k := range []string{"functionDeclarations", "functionCall", "toolConfig", "tool_choice", "toolChoice", "tool_calls", "tool_use"} {
		if _, ok := m[k]; ok {
			return true
		}
	}
	return false
}

func isMediaValue(v any) bool {
	m := valueKeys(v)
	if m == nil {
		return false
	}
	for _, k := range []string{"inlineData", "inline_data", "fileData", "file_data", "image_url", "input_audio", "video_url", "image", "document", "file", "audio", "video"} {
		if _, ok := m[k]; ok {
			return true
		}
	}
	if t, ok := m["type"].(string); ok {
		switch strings.ToLower(t) {
		case "image", "image_url", "audio", "input_audio", "video", "video_url", "file", "document":
			return true
		}
	}
	return false
}

func scalarEqual(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	switch a.(type) {
	case string, float64, bool, json.Number:
		return reflect.DeepEqual(a, b)
	default:
		return reflect.DeepEqual(a, b)
	}
}

func summarizeValue(v any) (any, bool) {
	if v == nil {
		return nil, false
	}
	if s, ok := v.(string); ok {
		return summarizeString(s)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("<%T>", v), true
	}
	if len(b) <= maxValueBytes {
		return v, false
	}
	sum := sha256.Sum256(b)
	return map[string]any{
		"_type": typeName(v), "_bytes": len(b), "_sha256": hex.EncodeToString(sum[:]), "_note": "值过大，已省略",
	}, true
}

func summarizeString(s string) (any, bool) {
	trim := strings.TrimSpace(s)
	if len(trim) >= 400 && !strings.ContainsAny(trim, " \n\t") && base64RE.MatchString(trim) {
		sum := sha256.Sum256([]byte(trim))
		return fmt.Sprintf("<base64 len=%d sha256=%s>", len(trim), hex.EncodeToString(sum[:])), true
	}
	if len(s) > maxValueBytes {
		r := []rune(s)
		if len(r) > 800 {
			return string(r[:800]) + fmt.Sprintf("…(truncated, total %d chars)", len(r)), true
		}
	}
	return s, false
}

var identRE = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)
var msgIndexRE = regexp.MustCompile(`^\$\.(messages|contents)\[(\d+)\]`)

func joinPath(base, key string) string {
	if identRE.MatchString(key) {
		return base + "." + key
	}
	esc := strings.ReplaceAll(key, `'`, `\'`)
	return base + "['" + esc + "']"
}

func classifyPath(path string, leftRoot, rightRoot any, leftLast, rightLast int) string {
	lp := strings.ToLower(path)
	switch {
	case strings.Contains(lp, "systeminstruction"), strings.HasPrefix(lp, "$.system"), strings.Contains(lp, "['system']"):
		return "system_prompt"
	case strings.Contains(lp, "safetysettings"):
		return "safety_settings"
	case strings.Contains(lp, "cachedcontent"), strings.Contains(lp, "cache_control"):
		return "cached_content"
	case strings.Contains(lp, "functionresponse"), strings.Contains(lp, "tool_result"), strings.Contains(lp, "toolcallid"):
		return "function_response"
	case strings.Contains(lp, "tools"), strings.Contains(lp, "toolchoice"), strings.Contains(lp, "tool_choice"), strings.Contains(lp, "functiondeclarations"),
		strings.Contains(lp, "toolconfig"), strings.Contains(lp, "tool_use"), strings.Contains(lp, "tool_calls"):
		return "tools"
	case strings.Contains(lp, "generationconfig"), strings.Contains(lp, "temperature"), strings.Contains(lp, "top_p"),
		strings.Contains(lp, "topp"), strings.Contains(lp, "top_k"), strings.Contains(lp, "topk"),
		strings.Contains(lp, "max_tokens"), strings.Contains(lp, "maxtokens"), strings.Contains(lp, "max_completion_tokens"),
		strings.Contains(lp, "max_output_tokens"), strings.Contains(lp, "maxoutputtokens"),
		strings.Contains(lp, "stop"), strings.Contains(lp, "seed"), strings.HasSuffix(lp, ".n"),
		strings.Contains(lp, "candidatecount"), strings.Contains(lp, "response_format"), strings.Contains(lp, "responsemimetype"),
		strings.Contains(lp, "responseschema"), strings.Contains(lp, "thinking"), strings.Contains(lp, "reasoning"),
		strings.Contains(lp, "presence_penalty"), strings.Contains(lp, "frequency_penalty"):
		return "generation_config"
	case strings.Contains(lp, "image_url"), strings.Contains(lp, "inlinedata"), strings.Contains(lp, "filedata"),
		strings.Contains(lp, "input_audio"), strings.Contains(lp, "video_url"), strings.Contains(lp, "document"),
		strings.Contains(lp, ".image"), strings.Contains(lp, ".audio"), strings.Contains(lp, ".video"),
		strings.Contains(lp, ".file"):
		return "media"
	case strings.Contains(lp, "google_search"), strings.Contains(lp, "googlesearch"), strings.Contains(lp, "web_search"),
		strings.Contains(lp, "websearch"), strings.Contains(lp, "server_tool_use"):
		return "web_search"
	}
	m := msgIndexRE.FindStringSubmatch(path)
	if len(m) == 3 {
		idx, _ := strconv.Atoi(m[2])
		field := m[1]
		role := roleAt(leftRoot, field, idx)
		if role == "" {
			role = roleAt(rightRoot, field, idx)
		}
		switch strings.ToLower(role) {
		case "system", "developer":
			return "system_prompt"
		case "user":
			if idx == leftLast || idx == rightLast {
				return "user_prompt"
			}
			return "history"
		case "tool", "function":
			return "function_response"
		default:
			return "history"
		}
	}
	if strings.Contains(lp, ".parts[") || strings.Contains(lp, ".content[") {
		return "history"
	}
	return "other"
}

func roleAt(root any, field string, idx int) string {
	m, ok := root.(map[string]any)
	if !ok {
		return ""
	}
	arr, ok := m[field].([]any)
	if !ok || idx < 0 || idx >= len(arr) {
		return ""
	}
	obj, ok := arr[idx].(map[string]any)
	if !ok {
		return ""
	}
	if role, ok := obj["role"].(string); ok {
		return role
	}
	return ""
}

func lastUserIndex(root any) int {
	m, ok := root.(map[string]any)
	if !ok {
		return -1
	}
	for _, field := range []string{"messages", "contents"} {
		arr, ok := m[field].([]any)
		if !ok {
			continue
		}
		last := -1
		for i, v := range arr {
			obj, ok := v.(map[string]any)
			if !ok {
				continue
			}
			if role, _ := obj["role"].(string); strings.EqualFold(role, "user") {
				last = i
			}
		}
		if last >= 0 {
			return last
		}
	}
	return -1
}

func typeName(v any) string {
	if v == nil {
		return "null"
	}
	switch v.(type) {
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case float64, json.Number:
		return "number"
	case bool:
		return "boolean"
	default:
		return fmt.Sprintf("%T", v)
	}
}

func summarize(changes []Change, truncated bool) Summary {
	s := Summary{Total: len(changes), Truncated: truncated, ByCategory: map[string]int{}, BySeverity: map[string]int{}}
	for _, c := range changes {
		switch c.Op {
		case "add":
			s.Added++
		case "remove":
			s.Removed++
		case "replace":
			s.Replaced++
		}
		s.ByCategory[c.Category]++
		s.BySeverity[c.Severity]++
	}
	return s
}

// CompareFingerprints lists which canonical sections changed between two snapshots.
func CompareFingerprints(a, b model.Fingerprints) []SectionChange {
	list := []struct {
		name, label, l, r string
	}{
		{"system", "System Prompt", a.System, b.System},
		{"user", "User Prompt", a.User, b.User},
		{"history", "历史对话", a.History, b.History},
		{"tools", "Tools / Functions", a.Tools, b.Tools},
		{"functionResponse", "Function Response", a.FunctionResponse, b.FunctionResponse},
		{"generation", "Generation Config", a.Generation, b.Generation},
		{"safety", "Safety Settings", a.Safety, b.Safety},
		{"cached", "Cached Content", a.Cached, b.Cached},
		{"media", "图片/音频/视频/文件", a.Media, b.Media},
		{"webSearch", "Web Search", a.WebSearch, b.WebSearch},
		{"other", "其他字段", a.Other, b.Other},
	}
	out := make([]SectionChange, 0, len(list))
	for _, x := range list {
		out = append(out, SectionChange{Name: x.name, Label: x.label, Left: x.l, Right: x.r, Changed: x.l != x.r})
	}
	return out
}
