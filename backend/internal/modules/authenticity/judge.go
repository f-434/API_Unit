package authenticity

import "strings"

func judge(r *modelRun) (string, string) {
	if len(r.checks) == 0 {
		return VerdictInconclusive, "low"
	}
	hasAnyReal := 0
	for _, c := range r.checks {
		if c.Status != StatusNA {
			hasAnyReal++
		}
	}
	if hasAnyReal == 0 {
		return VerdictInconclusive, "low"
	}
	capFail := hasGroupFail(r.checks, GroupCapability)
	capPass := hasCapabilityLoopPass(r.checks)
	if r.errorMsg != "" && !capPass {
		return VerdictError, "low"
	}
	nonceFail := hasAnyFail(r.checks, "P3-7", "P3-8", "P3-9")
	identityMismatch := hasAnyFail(r.checks, "P4-1")
	sourceConflict := hasAnyFail(r.checks, "P2-2") &&
		(r.source.Confidence == "high" || r.source.Confidence == "medium")
	hardPass := hasAnyPass(r.checks, "P1-1", "P1-5", "P1-6", "P1-7")
	sigTamperFail := hasAnyFail(r.checks, "SG6")
	signatureStrong := hasAnyPass(r.checks, "SG4") && hasAnyPass(r.checks, "SG6")
	officialSource := r.source.Confidence == "high" &&
		!strings.Contains(r.source.Name, "网关") &&
		!strings.Contains(r.source.Name, "中转")

	if sigTamperFail {
		if identityMismatch || sourceConflict {
			return VerdictCounterfeit, "high"
		}
		return VerdictSuspicious, "medium"
	}

	if capFail {
		if nonceFail && (identityMismatch || sourceConflict) {
			return VerdictCounterfeit, "high"
		}
		if nonceFail {
			return VerdictSuspicious, "medium"
		}
		if identityMismatch || sourceConflict {
			return VerdictSuspicious, "medium"
		}
		return VerdictSuspicious, "medium"
	}

	if capPass {
		if identityMismatch || sourceConflict {
			return VerdictSuspicious, "medium"
		}
		if hardPass || officialSource || signatureStrong || hostedStrongPass(r.checks) {
			return VerdictAuthentic, "high"
		}
		return VerdictLikely, "medium"
	}

	// Capability was disabled or not applicable: fall back to source/identity.
	if identityMismatch || sourceConflict {
		return VerdictSuspicious, "low"
	}
	if hardPass || officialSource || signatureStrong || hostedStrongPass(r.checks) {
		return VerdictAuthentic, "medium"
	}
	return VerdictInconclusive, "low"
}

// hostedStrongPass treats a fully successful hosted MCP / proof-page exchange as
// strong evidence, but only when every hosted hard check passed. The report
// still notes that a malicious endpoint can rewrite hosted traffic, so this is
// not treated as an absolute guarantee.
func hostedStrongPass(checks []CheckReport) bool {
	passes := 0
	for _, c := range checks {
		if c.Group != GroupHosted || c.Weight != WeightHard {
			continue
		}
		if c.Status == StatusFail {
			return false
		}
		if c.Status == StatusPass {
			passes++
		}
	}
	return passes >= 2
}

func hasGroupFail(checks []CheckReport, group string) bool {
	for _, c := range checks {
		if c.Group == group && c.Status == StatusFail {
			return true
		}
	}
	return false
}

func hasCapabilityLoopPass(checks []CheckReport) bool {
	for _, c := range checks {
		if c.ID == "P3-11" && c.Status == StatusPass {
			return true
		}
	}
	return false
}

func hasAnyFail(checks []CheckReport, ids ...string) bool {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	for _, c := range checks {
		if want[c.ID] && c.Status == StatusFail {
			return true
		}
	}
	return false
}

func hasAnyPass(checks []CheckReport, ids ...string) bool {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	for _, c := range checks {
		if want[c.ID] && c.Status == StatusPass {
			return true
		}
	}
	return false
}
