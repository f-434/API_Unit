package authenticity

import "testing"

func TestAggregateReports(t *testing.T) {
	parts := []ModelReport{
		{TargetID: "t1", TargetName: "mock", Model: "m", Repetition: 1, Verdict: VerdictAuthentic, Confidence: "high", Passed: 2,
			Checks: []CheckReport{{ID: "P3-11", Status: StatusPass, Repetition: 1}, {ID: "P1-1", Status: StatusPass, Repetition: 1}}},
		{TargetID: "t1", TargetName: "mock", Model: "m", Repetition: 2, Verdict: VerdictAuthentic, Confidence: "high", Passed: 2,
			Checks: []CheckReport{{ID: "P3-11", Status: StatusPass, Repetition: 2}, {ID: "P1-1", Status: StatusPass, Repetition: 2}}},
	}
	out := aggregateReports(parts, 2)
	if len(out) != 1 {
		t.Fatalf("groups=%d", len(out))
	}
	r := out[0]
	if r.Repetitions != 2 || len(r.RepVerdicts) != 2 || len(r.Checks) != 4 {
		t.Fatalf("unexpected aggregate: %+v", r)
	}
	if r.Verdict != VerdictAuthentic {
		t.Fatalf("verdict=%s", r.Verdict)
	}
}

func TestAggregateMixedVerdictIsSuspicious(t *testing.T) {
	parts := []ModelReport{
		{TargetID: "t1", Model: "m", Repetition: 1, Verdict: VerdictAuthentic},
		{TargetID: "t1", Model: "m", Repetition: 2, Verdict: VerdictSuspicious},
	}
	out := aggregateReports(parts, 2)
	if out[0].Verdict != VerdictSuspicious {
		t.Fatalf("verdict=%s, want suspicious", out[0].Verdict)
	}
}
