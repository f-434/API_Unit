package authenticity

import (
	"fmt"
	"strings"
)

// runGeminiSignature verifies that a Gemini thought signature is not merely
// present but enforced by the upstream:
//
//  1. ask the model to call a function and capture the functionCall part with
//     its thoughtSignature;
//  2. replay the same call with the real signature + a functionResponse. A
//     real endpoint accepts it and uses the tool result;
//  3. replay with a deliberately corrupted signature. A real Gemini endpoint
//     returns 4xx because the signature cannot be validated. A relay that
//     ignores signatures typically returns 200.
func (r *modelRun) runGeminiSignature() {
	first, res1 := r.geminiSignatureFirst()
	r.observe(res1)
	funcOK := first.Name == "check_flight" && strings.EqualFold(argString(first.Args, "flight"), "AA100")
	sig := first.Signature
	checks := []CheckReport{
		{
			ID: "SG1", Group: GroupSignature, Name: "签名探针第 1 轮请求成功",
			Status: boolStatus(res1.Err == nil && res1.statusOK(), httpEvidence(res1), fmt.Sprintf("HTTP %d", res1.Status)),
			Weight: WeightHard, Evidence: httpEvidence(res1), CaseID: "sig.gemini.turn1",
			DurationMs: int64(res1.LatencyMs),
		},
		{
			ID: "SG2", Group: GroupSignature, Name: "实际产生 check_flight 工具调用",
			Status: boolStatus(funcOK, "未产生 check_flight(AA100) 调用", "check_flight(AA100)"),
			Weight: WeightCapability, Evidence: fmt.Sprintf("name=%q args=%v", first.Name, first.Args), CaseID: "sig.gemini.turn1",
		},
		{
			ID: "SG3", Group: GroupSignature, Name: "functionCall 携带 thoughtSignature",
			Status: boolStatus(strings.TrimSpace(sig) != "", "functionCall 没有 thoughtSignature", "存在 thoughtSignature"),
			Weight: WeightCapability, Evidence: fmt.Sprintf("signature_len=%d", len(sig)), CaseID: "sig.gemini.turn1",
		},
		{
			ID: "SG3b", Group: GroupSignature, Name: "thoughtSignature 格式合理",
			Status: signatureFormatStatus(sig), Weight: WeightMetadata,
			Evidence: fmt.Sprintf("signature_len=%d", len(sig)), CaseID: "sig.gemini.turn1",
		},
	}
	if signatureUnsupported(res1) {
		for i := range checks {
			checks[i].Status = StatusNA
			checks[i].Evidence = "上游声明不支持 function calling / includeThoughts: " + truncateText(strings.TrimSpace(string(res1.Body)), 240)
		}
		r.add(checks...)
		r.record(res1, "sig.gemini.turn1", "思维签名：第 1 轮", GroupSignature, "", checks...)
		return
	}
	r.add(checks...)
	r.record(res1, "sig.gemini.turn1", "思维签名：第 1 轮工具调用", GroupSignature, "", checks...)

	if !funcOK || strings.TrimSpace(sig) == "" {
		r.add(CheckReport{
			ID: "SG6", Group: GroupSignature, Name: "伪造 thoughtSignature 应被上游拒绝",
			Status: StatusNA, Weight: WeightHard, Evidence: "未拿到真实 signature，无法执行伪造回放",
		})
		return
	}

	// Valid replay: the upstream must accept the real signature and consume the
	// functionResponse.
	validRes := r.geminiSignatureReplay(sig)
	r.observe(validRes)
	validParsed := parseGemini(validRes.Body)
	r.captureGemini(validParsed)
	validOK := validRes.Err == nil && validRes.statusOK()
	text := strings.ToLower(r.lastText)
	consumed := strings.Contains(text, "delay") || strings.Contains(text, "12") ||
		strings.Contains(text, "延误") || strings.Contains(text, "晚点")
	sg4 := StatusPass
	if !validOK {
		sg4 = StatusWarn
	}
	sg5 := StatusNA
	if validOK {
		sg5 = boolStatus(consumed, "最终文本未体现 delayed / 12 PM", truncateText(r.lastText, 160))
	}
	validChecks := []CheckReport{
		{
			ID: "SG4", Group: GroupSignature, Name: "真实 signature 回放被接受",
			Status: sg4, Weight: WeightMetadata, Evidence: httpEvidence(validRes), CaseID: "sig.gemini.turn2.valid",
			DurationMs: int64(validRes.LatencyMs),
		},
		{
			ID: "SG5", Group: GroupSignature, Name: "回放使用了 functionResponse 的结果",
			Status: sg5, Weight: WeightCapability, Evidence: truncateText(r.lastText, 240), CaseID: "sig.gemini.turn2.valid",
		},
	}
	r.add(validChecks...)
	r.record(validRes, "sig.gemini.turn2.valid", "思维签名：真实 signature 回放", GroupSignature, r.lastText, validChecks...)

	// Tampered replay: a genuine Gemini validates the signature and rejects it.
	tamperRes := r.geminiSignatureReplay("xxxxxxxxxxxx")
	r.observe(tamperRes)
	tamperStatus := StatusFail
	tamperEvidence := httpEvidence(tamperRes)
	switch {
	case tamperRes.Err != nil || tamperRes.Status == 0 || tamperRes.Status >= 500:
		tamperStatus = StatusNA
		tamperEvidence = "无法判断签名校验（网络/上游错误）: " + tamperEvidence
	case tamperRes.Status == 400:
		tamperStatus = StatusPass
		tamperEvidence = "HTTP 400（符合官方签名校验错误）"
	case tamperRes.Status >= 400 && tamperRes.Status < 500:
		tamperStatus = StatusWarn
		tamperEvidence = fmt.Sprintf("HTTP %d（4xx，但非 400）", tamperRes.Status)
	}
	tamperChecks := []CheckReport{
		{
			ID: "SG6", Group: GroupSignature, Name: "伪造 thoughtSignature 应被上游拒绝",
			Status: tamperStatus, Weight: WeightHard, Evidence: tamperEvidence, CaseID: "sig.gemini.turn2.tamper",
			DurationMs: int64(tamperRes.LatencyMs),
		},
	}
	if tamperRes.Status >= 400 && tamperRes.Status < 500 {
		body := strings.ToLower(string(tamperRes.Body))
		mentions := strings.Contains(body, "signature") || strings.Contains(body, "thought")
		status := StatusPass
		if !mentions {
			status = StatusWarn
		}
		tamperChecks = append(tamperChecks, CheckReport{
			ID: "SG7", Group: GroupSignature, Name: "拒绝原因指向 thoughtSignature",
			Status: status, Weight: WeightMetadata, Evidence: truncateText(strings.TrimSpace(string(tamperRes.Body)), 300),
			CaseID: "sig.gemini.turn2.tamper",
		})
	}
	r.add(tamperChecks...)
	r.record(tamperRes, "sig.gemini.turn2.tamper", "思维签名：伪造 signature 回放", GroupSignature, "", tamperChecks...)
}

type signatureCall struct {
	Name      string
	Args      map[string]any
	Signature string
}

func (r *modelRun) geminiSignatureFirst() (signatureCall, httpResult) {
	contents := []any{map[string]any{
		"role":  "user",
		"parts": []any{map[string]any{"text": "查一下航班 AA100 的状态"}},
	}}
	body := geminiSignatureBody(contents)
	res := r.postWithRetry(r.ctx, r.endpoint, r.headers, body, r.timeout())
	parsed := parseGemini(res.Body)
	r.captureGemini(parsed)
	if len(parsed.FunctionCalls) == 0 {
		return signatureCall{}, res
	}
	call := parsed.FunctionCalls[0]
	args := parseArgsValue(call.Args)
	if call.Signature == "" {
		// Some Gemini versions attach the signature to a sibling thought part.
		// Fall back to that signature only for replay; SG3 reports the exact
		// functionCall part separately.
		if len(parsed.ThoughtSignatures) > 0 {
			call.Signature = parsed.ThoughtSignatures[0]
		}
	}
	return signatureCall{Name: call.Name, Args: args, Signature: call.Signature}, res
}

func (r *modelRun) geminiSignatureReplay(signature string) httpResult {
	contents := []any{
		map[string]any{
			"role":  "user",
			"parts": []any{map[string]any{"text": "查一下航班 AA100 的状态"}},
		},
		map[string]any{
			"role": "model",
			"parts": []any{map[string]any{
				"functionCall":     map[string]any{"name": "check_flight", "args": map[string]any{"flight": "AA100"}},
				"thoughtSignature": signature,
			}},
		},
		map[string]any{
			"role": "user",
			"parts": []any{map[string]any{
				"functionResponse": map[string]any{
					"name":     "check_flight",
					"response": map[string]any{"status": "delayed", "departure_time": "12 PM"},
				},
			}},
		},
	}
	return r.postWithRetry(r.ctx, r.endpoint, r.headers, geminiSignatureBody(contents), r.timeout())
}

func geminiSignatureBody(contents []any) map[string]any {
	return map[string]any{
		"contents": contents,
		"tools": []any{map[string]any{
			"functionDeclarations": []any{map[string]any{
				"name":        "check_flight",
				"description": "查询航班状态",
				"parameters": map[string]any{
					"type":       "OBJECT",
					"properties": map[string]any{"flight": map[string]any{"type": "STRING"}},
					"required":   []string{"flight"},
				},
			}},
		}},
		"generationConfig": map[string]any{
			"thinkingConfig": map[string]any{"includeThoughts": true},
		},
	}
}

func signatureUnsupported(res httpResult) bool {
	if res.Status != 400 && res.Status != 422 {
		return false
	}
	s := strings.ToLower(string(res.Body))
	if !strings.Contains(s, "thinking") && !strings.Contains(s, "thought") &&
		!strings.Contains(s, "function") && !strings.Contains(s, "tool") {
		return false
	}
	for _, phrase := range []string{"not support", "unsupported", "does not support", "不支持", "unknown field", "cannot find field"} {
		if strings.Contains(s, phrase) {
			return true
		}
	}
	return false
}

func signatureFormatStatus(sig string) string {
	s := strings.TrimSpace(sig)
	if s == "" {
		return StatusNA
	}
	if len(s) < 32 {
		return StatusWarn
	}
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '+' || r == '/' || r == '=' || r == '-' || r == '_':
		default:
			return StatusWarn
		}
	}
	return StatusPass
}

func argString(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	if v, ok := args[key]; ok {
		return fmt.Sprintf("%v", v)
	}
	return ""
}
