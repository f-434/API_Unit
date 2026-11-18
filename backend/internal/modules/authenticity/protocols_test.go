package authenticity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"apiunit/internal/engine"
	"apiunit/internal/model"
	"apiunit/internal/protocol"
	"apiunit/internal/store"
	"apiunit/internal/tokens"
)

func TestClaudeChallengeAuthentic(t *testing.T) {
	nonceRe := regexp.MustCompile(`严格等于 "([^"]+)"`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Anthropic-Organization-Id", "org-test")
		if body["model"] == probeClaudeModel {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"not_found_error","message":"model: ` + probeClaudeModel + `"}}`))
			return
		}
		if strings.Contains(r.Header.Get("anthropic-beta"), probeBeta) {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"Unexpected value(s) for anthropic-beta: ` + probeBeta + `"}}`))
			return
		}
		msgs, _ := body["messages"].([]any)
		if len(msgs) >= 3 {
			var toolContent string
			for _, item := range msgs {
				m, _ := item.(map[string]any)
				if m["role"] != "user" {
					continue
				}
				blocks, _ := m["content"].([]any)
				for _, b := range blocks {
					bm, _ := b.(map[string]any)
					if bm["type"] == "tool_result" {
						toolContent, _ = bm["content"].(string)
					}
				}
			}
			var tr map[string]any
			_ = json.Unmarshal([]byte(toolContent), &tr)
			out, _ := json.Marshal(map[string]any{
				"challenge": tr["challenge"], "secret": tr["secret"],
				"sum": int(tr["a"].(float64)) + int(tr["b"].(float64)),
			})
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "msg_round2", "type": "message", "role": "assistant", "model": "claude-opus-5-5",
				"content":     []any{map[string]any{"type": "text", "text": string(out)}},
				"stop_reason": "end_turn",
				"usage":       map[string]any{"input_tokens": 40, "output_tokens": 20},
			})
			return
		}
		prompt := ""
		if len(msgs) > 0 {
			last, _ := msgs[len(msgs)-1].(map[string]any)
			prompt, _ = last["content"].(string)
		}
		m := nonceRe.FindStringSubmatch(prompt)
		if len(m) != 2 {
			t.Fatalf("round1 prompt missing nonce: %s", prompt)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "msg_round1", "type": "message", "role": "assistant", "model": "claude-opus-5-5",
			"content": []any{map[string]any{
				"type": "tool_use", "id": "toolu_1", "name": "proof_lookup",
				"input": map[string]any{"challenge": m[1]},
			}},
			"stop_reason": "tool_use",
			"usage":       map[string]any{"input_tokens": 20, "output_tokens": 10},
		})
	}))
	defer srv.Close()

	st, targetID := testStore(t)
	if err := st.SaveTarget(&model.Target{ID: targetID, Name: "mock", BaseURL: srv.URL, Protocol: model.ProtocolClaude}, "sk-ant-test"); err != nil {
		t.Fatal(err)
	}
	report := runProtocolModule(t, st, targetID, "claude", "claude-opus-5-5")
	r := report.Models[0]
	if r.Verdict != VerdictAuthentic {
		t.Fatalf("verdict=%s, want %s; checks=%+v", r.Verdict, VerdictAuthentic, r.Checks)
	}
}

func TestGeminiChallengeAuthentic(t *testing.T) {
	nonceRe := regexp.MustCompile(`严格等于 "([^"]+)"`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if tools, ok := body["tools"].([]any); ok && len(tools) > 0 {
			if _, bad := tools[0].(map[string]any)["xxx"]; bad {
				w.WriteHeader(400)
				// New API gateways rewrite the official envelope to
				// type=upstream_error and omit status, while preserving the
				// message. The fingerprint must still pass.
				_, _ = w.Write([]byte(`{"error":{"code":400,"message":"Invalid JSON payload received. Unknown name \"xxx\" at 'tools[0]': Cannot find field.","type":"upstream_error","param":""}}`))
				return
			}
		}
		contents, _ := body["contents"].([]any)
		if len(contents) >= 3 {
			var resp map[string]any
			last, _ := contents[len(contents)-1].(map[string]any)
			parts, _ := last["parts"].([]any)
			for _, p := range parts {
				pm, _ := p.(map[string]any)
				if fr, ok := pm["functionResponse"].(map[string]any); ok {
					resp, _ = fr["response"].(map[string]any)
				}
			}
			out, _ := json.Marshal(map[string]any{
				"challenge": resp["challenge"], "secret": resp["secret"],
				"sum": int(resp["a"].(float64)) + int(resp["b"].(float64)),
			})
			_ = json.NewEncoder(w).Encode(map[string]any{
				"candidates": []any{map[string]any{
					"content":      map[string]any{"role": "model", "parts": []any{map[string]any{"text": string(out)}}},
					"finishReason": "STOP",
				}},
				// Gemini reports thoughts separately and includes them in the
				// total: 32 + 3 + 5 = 40. reasoning may exceed candidates.
				"usageMetadata": map[string]any{"promptTokenCount": 32, "candidatesTokenCount": 3, "totalTokenCount": 40, "thoughtsTokenCount": 5},
				"modelVersion":  "gemini-3.6-flash",
				"responseId":    "resp-gemini-1",
			})
			return
		}
		prompt := ""
		if len(contents) > 0 {
			last, _ := contents[len(contents)-1].(map[string]any)
			parts, _ := last["parts"].([]any)
			for _, p := range parts {
				pm, _ := p.(map[string]any)
				if s, ok := pm["text"].(string); ok {
					prompt = s
				}
			}
		}
		m := nonceRe.FindStringSubmatch(prompt)
		if len(m) != 2 {
			t.Fatalf("round1 prompt missing nonce: %s", prompt)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"candidates": []any{map[string]any{
				"content": map[string]any{"role": "model", "parts": []any{map[string]any{
					"functionCall": map[string]any{"name": "proof_lookup", "args": map[string]any{"challenge": m[1]}},
				}}},
				"finishReason": "STOP",
			}},
			"usageMetadata": map[string]any{"promptTokenCount": 20, "candidatesTokenCount": 10, "totalTokenCount": 30},
			"modelVersion":  "gemini-3.6-flash",
			"responseId":    "resp-gemini-0",
		})
	}))
	defer srv.Close()

	st, targetID := testStore(t)
	if err := st.SaveTarget(&model.Target{ID: targetID, Name: "mock", BaseURL: srv.URL, Protocol: model.ProtocolGemini}, "gm-test"); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateAuthenticityTemplate(&model.AuthenticityTemplate{
		ProbeID: "fp.gemini.invalid_tools", Provider: "gemini", Name: "gateway-envelope",
		Status: "active",
		Spec:   `{"status":400,"jsonPaths":[{"path":"error.code","equals":"400"}],"contains":["tools[0]"]}`,
	}); err != nil {
		t.Fatal(err)
	}
	report := runProtocolModule(t, st, targetID, "gemini", "gemini-3.6-flash")
	r := report.Models[0]
	if r.Verdict != VerdictAuthentic {
		t.Fatalf("verdict=%s, want %s; checks=%+v", r.Verdict, VerdictAuthentic, r.Checks)
	}
	foundTemplate := false
	for _, c := range r.Checks {
		if c.ID == "P1-7" && strings.Contains(c.Evidence, "模板") {
			foundTemplate = true
		}
	}
	if !foundTemplate {
		t.Fatalf("fingerprint did not use the active template: %+v", r.Checks)
	}
}

func runProtocolModule(t *testing.T, st *store.Store, targetID, protocolName, modelName string) *Report {
	t.Helper()
	runID := model.NewID("r_")
	if err := st.CreateRun(&model.Run{ID: runID, Kind: model.KindAuthenticity, Status: model.StatusQueued}); err != nil {
		t.Fatal(err)
	}
	if err := st.StartRun(runID); err != nil {
		t.Fatal(err)
	}
	sess := engine.NewSession(runID, model.KindAuthenticity, st, protocol.NewClient(), tokens.New(t.TempDir(), true), nil)
	sess.StartPumps()
	mod := New(st)
	raw, _ := json.Marshal(Config{
		Targets:   []TargetSpec{{TargetID: targetID, Models: []string{modelName}, Protocol: protocolName}},
		Checks:    ChecksConfig{Fingerprint: true, Source: true, Capability: true, Identity: true, Consistency: true},
		Challenge: ChallengeConfig{ToolName: "proof_lookup", NonceLength: 16, MaxTokens: 512, ForceTool: true},
	})
	validated, err := mod.Validate(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := mod.Run(context.Background(), sess, validated); err != nil {
		t.Fatal(err)
	}
	sess.FinishSession(model.StatusCompleted, "")
	report := sess.ModuleResult().(*Report)
	return report
}
