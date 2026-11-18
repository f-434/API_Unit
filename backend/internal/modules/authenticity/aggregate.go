package authenticity

import (
	"sort"
	"strings"
)

func aggregateReports(reports []ModelReport, repetitions int) []ModelReport {
	type key struct{ target, model string }
	groups := map[key][]ModelReport{}
	order := make([]key, 0)
	for _, r := range reports {
		k := key{r.TargetID, r.Model}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], r)
	}
	out := make([]ModelReport, 0, len(order))
	for _, k := range order {
		out = append(out, mergeModelReports(groups[k], repetitions))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TargetName == out[j].TargetName {
			return out[i].Model < out[j].Model
		}
		return out[i].TargetName < out[j].TargetName
	})
	return out
}

func mergeModelReports(parts []ModelReport, repetitions int) ModelReport {
	if len(parts) == 0 {
		return ModelReport{}
	}
	out := parts[0]
	out.Repetition = 0
	out.Repetitions = repetitions
	out.Checks = nil
	out.RepVerdicts = nil
	out.Passed, out.Failed, out.NA, out.Total, out.DurationMs = 0, 0, 0, 0, 0
	out.ResponseModel = ""
	out.Challenge = ChallengeReport{}
	out.Error = ""
	errs := map[string]bool{}
	verdicts := map[string]int{}
	bestSource := SourceReport{}
	bestRank := -1

	for _, p := range parts {
		out.Checks = append(out.Checks, p.Checks...)
		out.Passed += p.Passed
		out.Failed += p.Failed
		out.NA += p.NA
		out.Total += p.Total
		out.DurationMs += p.DurationMs
		if p.ResponseModel != "" {
			out.ResponseModel = p.ResponseModel
		}
		if p.Challenge.Nonce != "" {
			out.Challenge = p.Challenge
		}
		if p.Error != "" {
			errs[p.Error] = true
		}
		verdicts[p.Verdict]++
		out.RepVerdicts = append(out.RepVerdicts, RepVerdict{
			Repetition: repetitionOf(p),
			Verdict:    p.Verdict,
			Confidence: p.Confidence,
			Passed:     p.Passed,
			Failed:     p.Failed,
			NA:         p.NA,
			DurationMs: p.DurationMs,
			Error:      p.Error,
		})
		if rank := confidenceRank(p.Source.Confidence); rank > bestRank && p.Source.Name != "" {
			bestRank = rank
			bestSource = p.Source
		}
	}
	if bestSource.Name != "" {
		out.Source = bestSource
	}
	out.Verdict = aggregateVerdict(verdicts)
	out.VerdictLabel = verdictLabel(out.Verdict)
	out.Confidence = aggregateConfidence(parts, out.Verdict)
	out.Dimensions = dimensions(out.Checks)
	out.Error = joinErrors(errs)
	sort.SliceStable(out.Checks, func(i, j int) bool {
		if out.Checks[i].Repetition != out.Checks[j].Repetition {
			return out.Checks[i].Repetition < out.Checks[j].Repetition
		}
		return out.Checks[i].ID < out.Checks[j].ID
	})
	sort.Slice(out.RepVerdicts, func(i, j int) bool {
		return out.RepVerdicts[i].Repetition < out.RepVerdicts[j].Repetition
	})
	return out
}

func repetitionOf(r ModelReport) int {
	if r.Repetition > 0 {
		return r.Repetition
	}
	if len(r.RepVerdicts) > 0 {
		return r.RepVerdicts[0].Repetition
	}
	// Per-run reports do not carry a repetition field; derive it from the
	// first check, which inherits the job repetition.
	for _, c := range r.Checks {
		if c.Repetition > 0 {
			return c.Repetition
		}
	}
	return 1
}

func aggregateVerdict(counts map[string]int) string {
	total := 0
	for _, n := range counts {
		total += n
	}
	if counts[VerdictCounterfeit] > 0 {
		return VerdictCounterfeit
	}
	if counts[VerdictSuspicious] > 0 {
		return VerdictSuspicious
	}
	if total > 0 && counts[VerdictAuthentic] == total {
		return VerdictAuthentic
	}
	if counts[VerdictLikely] > 0 {
		return VerdictLikely
	}
	if counts[VerdictError] > 0 {
		return VerdictError
	}
	return VerdictInconclusive
}

func aggregateConfidence(parts []ModelReport, verdict string) string {
	best := "low"
	for _, p := range parts {
		if confidenceRank(p.Confidence) > confidenceRank(best) {
			best = p.Confidence
		}
	}
	if verdict == VerdictCounterfeit {
		return "high"
	}
	return best
}

func confidenceRank(c string) int {
	switch c {
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	}
	return 0
}

func joinErrors(set map[string]bool) string {
	if len(set) == 0 {
		return ""
	}
	out := make([]string, 0, len(set))
	for e := range set {
		out = append(out, e)
	}
	sort.Strings(out)
	return strings.Join(out, "；")
}
