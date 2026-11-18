package authenticity

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"apiunit/internal/model"
)

// TemplateSpec is the versioned expectation for an expensive/non-obvious
// official response. It is evaluated against the real raw response, so a
// gateway that rewrites the envelope can be described without changing code.
type TemplateSpec struct {
	Status    int                `json:"status,omitempty"`
	JSONPaths []TemplateJSONPath `json:"jsonPaths,omitempty"`
	Contains  []string           `json:"contains,omitempty"`
	Usage     *UsageTemplate     `json:"usage,omitempty"`
}

type TemplateJSONPath struct {
	Path   string `json:"path"`
	Equals string `json:"equals"`
}

type UsageTemplate struct {
	IncludeReasoningInTotal    *bool `json:"includeReasoningInTotal,omitempty"`
	AllowReasoningGTCompletion *bool `json:"allowReasoningGtCompletion,omitempty"`
}

type templateOutcome struct {
	Status   string
	Evidence string
	Version  int
	Name     string
}

func (r *modelRun) activeTemplate(probeID string) *model.AuthenticityTemplate {
	if r.store == nil {
		return nil
	}
	t, err := r.store.ActiveAuthenticityTemplate(probeID, string(r.protocolName))
	if err != nil {
		return nil
	}
	return t
}

func (r *modelRun) templateOutcome(probeID string, res httpResult) (*templateOutcome, bool) {
	t := r.activeTemplate(probeID)
	if t == nil {
		return nil, false
	}
	var spec TemplateSpec
	if err := json.Unmarshal([]byte(t.Spec), &spec); err != nil {
		return &templateOutcome{
			Status:   StatusFail,
			Evidence: fmt.Sprintf("模板 %s v%d 解析失败: %v", t.ID, t.Version, err),
			Version:  t.Version, Name: t.Name,
		}, true
	}
	ok, detail := evaluateTemplateSpec(spec, res)
	status := StatusPass
	if !ok {
		status = StatusFail
	}
	return &templateOutcome{
		Status:   status,
		Evidence: fmt.Sprintf("模板 %s v%d: %s", t.Name, t.Version, detail),
		Version:  t.Version,
		Name:     t.Name,
	}, true
}

func evaluateTemplateSpec(spec TemplateSpec, res httpResult) (bool, string) {
	parts := make([]string, 0, 4)
	ok := true
	if spec.Status > 0 {
		if res.Status != spec.Status {
			ok = false
		}
		parts = append(parts, fmt.Sprintf("status 期望 %d 实际 %d", spec.Status, res.Status))
	}
	for _, jp := range spec.JSONPaths {
		if strings.TrimSpace(jp.Path) == "" {
			continue
		}
		var root any
		if err := json.Unmarshal(res.Body, &root); err != nil {
			ok = false
			parts = append(parts, "响应不是合法 JSON")
			continue
		}
		got := fmt.Sprintf("%v", lookupJSONPath(root, jp.Path))
		if got != jp.Equals {
			ok = false
		}
		parts = append(parts, fmt.Sprintf("%s 期望 %q 实际 %q", jp.Path, jp.Equals, got))
	}
	for _, needle := range spec.Contains {
		if !strings.Contains(string(res.Body), needle) {
			ok = false
			parts = append(parts, "缺少 "+needle)
		}
	}
	if len(parts) == 0 {
		return false, "模板没有配置任何期望"
	}
	return ok, strings.Join(parts, "；")
}

// useTemplateCheck applies the active template for probeID, records the item
// and returns true when a template exists. The built-in expectation is skipped
// in that case.
func (r *modelRun) useTemplateCheck(probeID, checkID, name, caseID string, res httpResult, start time.Time) bool {
	out, ok := r.templateOutcome(probeID, res)
	if !ok {
		return false
	}
	c := CheckReport{
		ID: checkID, Group: GroupFingerprint, Name: name,
		Status: out.Status, Weight: WeightHard, Evidence: out.Evidence, CaseID: caseID,
		DurationMs: time.Since(start).Milliseconds(),
	}
	r.add(c)
	r.record(res, caseID, name, GroupFingerprint, "", c)
	return true
}

func (r *modelRun) usageTemplate() *UsageTemplate {
	probe := "usage." + string(r.protocolName)
	if t := r.activeTemplate(probe); t != nil {
		var spec TemplateSpec
		if json.Unmarshal([]byte(t.Spec), &spec) == nil && spec.Usage != nil {
			return spec.Usage
		}
	}
	return nil
}

func usageIncludesReasoning(proto model.Protocol, tpl *UsageTemplate) bool {
	if tpl != nil && tpl.IncludeReasoningInTotal != nil {
		return *tpl.IncludeReasoningInTotal
	}
	return proto == model.ProtocolGemini
}

func usageAllowsReasoningGTCompletion(proto model.Protocol, tpl *UsageTemplate) bool {
	if tpl != nil && tpl.AllowReasoningGTCompletion != nil {
		return *tpl.AllowReasoningGTCompletion
	}
	return proto == model.ProtocolGemini
}
