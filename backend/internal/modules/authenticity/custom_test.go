package authenticity

import (
	"net/http"
	"testing"
)

func TestCustomProbeEvaluation(t *testing.T) {
	r := &modelRun{}
	res := httpResult{
		Status: 200,
		Header: http.Header{"X-Test": []string{"ok"}},
		Body:   []byte(`{"data":[{"id":"gpt-6-luna"}],"text":"hello world"}`),
	}
	p := CustomProbe{
		ID: "models", Name: "模型列表", Path: "/v1/models",
		Expected: []Expectation{
			{Type: "status", Value: "200"},
			{Type: "json_path", Path: "data.0.id", Value: "gpt-6-luna"},
			{Type: "contains", Value: "hello"},
			{Type: "regex", Value: `"id"\s*:\s*"gpt-`},
			{Type: "header", Path: "X-Test", Value: "ok"},
		},
	}
	checks := r.evaluateCustom(p, res)
	if len(checks) != len(p.Expected) {
		t.Fatalf("checks=%d want %d", len(checks), len(p.Expected))
	}
	for _, c := range checks {
		if c.Status != StatusPass {
			t.Fatalf("check %s status=%s evidence=%s", c.ID, c.Status, c.Evidence)
		}
	}
}

func TestLookupJSONPath(t *testing.T) {
	root := map[string]any{"a": []any{map[string]any{"b": "c"}}}
	if got := lookupJSONPath(root, "$.a.0.b"); got != "c" {
		t.Fatalf("got %v", got)
	}
	if got := lookupJSONPath(root, "a.1.b"); got != nil {
		t.Fatalf("out of range got %v", got)
	}
}
