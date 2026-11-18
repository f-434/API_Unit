package authenticity

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"apiunit/internal/model"
)

func TestToolsUnsupportedIsNotAccused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["reasoning_effort"] == "xxx" {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":{"message":"Invalid value: 'xxx'. Supported values are: 'low'.","type":"invalid_request_error","param":"reasoning_effort","code":"invalid_value"}}`))
			return
		}
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":{"message":"tools are not supported for this model","type":"invalid_request_error"}}`))
	}))
	defer srv.Close()

	st, targetID := testStore(t)
	if err := st.SaveTarget(&model.Target{ID: targetID, Name: "mock", BaseURL: srv.URL, Protocol: model.ProtocolOpenAI}, "sk-test"); err != nil {
		t.Fatal(err)
	}
	report := runModule(t, st, targetID)
	r := report.Models[0]
	if r.Verdict == VerdictSuspicious || r.Verdict == VerdictCounterfeit {
		t.Fatalf("unsupported tools must not be accused, verdict=%s; checks=%+v", r.Verdict, r.Checks)
	}
	for _, c := range r.Checks {
		if c.Group == GroupCapability && c.Status == StatusFail {
			t.Fatalf("capability check %s should be N/A, got %+v", c.ID, c)
		}
	}
}

func TestAuthFailureIsErrorNotFake(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key","type":"authentication_error"}}`))
	}))
	defer srv.Close()

	st, targetID := testStore(t)
	if err := st.SaveTarget(&model.Target{ID: targetID, Name: "mock", BaseURL: srv.URL, Protocol: model.ProtocolOpenAI}, "sk-test"); err != nil {
		t.Fatal(err)
	}
	report := runModule(t, st, targetID)
	r := report.Models[0]
	if r.Verdict != VerdictError {
		t.Fatalf("verdict=%s, want error; checks=%+v", r.Verdict, r.Checks)
	}
}

func TestHostedStrongPassCanConcludeAuthenticWithoutLocalCapability(t *testing.T) {
	r := &modelRun{
		checks: []CheckReport{
			{ID: "H1", Group: GroupHosted, Weight: WeightHard, Status: StatusPass},
			{ID: "H2", Group: GroupHosted, Weight: WeightHard, Status: StatusPass},
			{ID: "H3", Group: GroupHosted, Weight: WeightHard, Status: StatusPass},
		},
		source: SourceReport{Name: "中转网关（上游未知）", Confidence: "medium"},
	}
	verdict, _ := judge(r)
	if verdict != VerdictAuthentic {
		t.Fatalf("verdict=%s, want authentic", verdict)
	}
}
