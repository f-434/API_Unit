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
	"apiunit/internal/secret"
	"apiunit/internal/store"
	"apiunit/internal/tokens"
)

func testStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	box, err := secret.LoadOrCreate(dir + "/secret.key")
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(dir+"/test.db", box)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	target := &model.Target{Name: "mock", BaseURL: "http://example.invalid", Protocol: model.ProtocolOpenAI}
	if err := st.SaveTarget(target, "sk-test"); err != nil {
		t.Fatal(err)
	}
	return st, target.ID
}

func runModule(t *testing.T, st *store.Store, targetID string) *Report {
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
		Targets:   []TargetSpec{{TargetID: targetID, Models: []string{"gpt-6-luna"}, Protocol: "openai"}},
		Checks:    ChecksConfig{Fingerprint: true, Source: true, Capability: true, Identity: true, Consistency: true},
		Challenge: ChallengeConfig{ToolName: "proof_lookup", NonceLength: 16, MaxTokens: 256, ForceTool: true},
	})
	validated, err := mod.Validate(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := mod.Run(context.Background(), sess, validated); err != nil {
		t.Fatal(err)
	}
	sess.FinishSession(model.StatusCompleted, "")
	report, ok := sess.ModuleResult().(*Report)
	if !ok || report == nil {
		t.Fatal("missing report")
	}
	return report
}

func TestOpenAIChallengeAuthentic(t *testing.T) {
	nonceRe := regexp.MustCompile(`严格等于 "([^"]+)"`)
	var sawRound2 bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("OpenAI-Organization", "org-test")
		msgs, _ := body["messages"].([]any)
		// Fingerprint probe.
		if body["reasoning_effort"] == "xxx" {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":{"message":"Invalid value: 'xxx'. Supported values are: 'low', 'medium', 'high'.","type":"invalid_request_error","param":"reasoning_effort","code":"invalid_value"}}`))
			return
		}
		if len(msgs) >= 4 {
			// Round 2: read the tool result and echo its values.
			sawRound2 = true
			var toolContent string
			for _, item := range msgs {
				m, _ := item.(map[string]any)
				if m["role"] == "tool" {
					toolContent, _ = m["content"].(string)
				}
			}
			var tr map[string]any
			_ = json.Unmarshal([]byte(toolContent), &tr)
			out, _ := json.Marshal(map[string]any{
				"challenge": tr["challenge"],
				"secret":    tr["secret"],
				"sum":       int(tr["a"].(float64)) + int(tr["b"].(float64)),
			})
			resp := map[string]any{
				"id": "chatcmpl-round2", "model": "gpt-6-luna",
				"choices": []any{map[string]any{
					"message":       map[string]any{"role": "assistant", "content": string(out)},
					"finish_reason": "stop",
				}},
				"usage": map[string]any{"prompt_tokens": 40, "completion_tokens": 20, "total_tokens": 60},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		// Round 1: emit a tool call with the challenge parsed from the prompt.
		prompt := ""
		if len(msgs) > 0 {
			last, _ := msgs[len(msgs)-1].(map[string]any)
			prompt, _ = last["content"].(string)
		}
		m := nonceRe.FindStringSubmatch(prompt)
		if len(m) != 2 {
			t.Fatalf("round1 prompt missing nonce: %s", prompt)
		}
		resp := map[string]any{
			"id": "chatcmpl-round1", "model": "gpt-6-luna",
			"choices": []any{map[string]any{
				"message": map[string]any{
					"role": "assistant", "content": "",
					"tool_calls": []any{map[string]any{
						"id": "call_1", "type": "function",
						"function": map[string]any{"name": "proof_lookup", "arguments": `{"challenge":"` + m[1] + `"}`},
					}},
				},
				"finish_reason": "tool_calls",
			}},
			"usage": map[string]any{"prompt_tokens": 20, "completion_tokens": 10, "total_tokens": 30},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	st, targetID := testStore(t)
	if err := st.SaveTarget(&model.Target{ID: targetID, Name: "mock", BaseURL: srv.URL, Protocol: model.ProtocolOpenAI}, "sk-test"); err != nil {
		t.Fatal(err)
	}
	report := runModule(t, st, targetID)
	if !sawRound2 {
		t.Fatal("round 2 was not called")
	}
	r := report.Models[0]
	if r.Verdict != VerdictAuthentic {
		t.Fatalf("verdict=%s, want %s; checks=%+v", r.Verdict, VerdictAuthentic, r.Checks)
	}
	if r.Source.Name != "OpenAI 官方" || r.Source.Confidence != "high" {
		t.Fatalf("source=%+v", r.Source)
	}
}

func TestOpenAINoToolCallIsSuspicious(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Fingerprint "passes": a relay that echoes the official error but does
		// not actually support tools must not be reported as warn/pass.
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["reasoning_effort"] == "xxx" {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":{"message":"Invalid value: 'xxx'. Supported values are: 'low'.","type":"invalid_request_error","param":"reasoning_effort","code":"invalid_value"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-x", "model": "gpt-6-luna",
			"choices": []any{map[string]any{
				"message":       map[string]any{"role": "assistant", "content": "I cannot call tools."},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
		})
	}))
	defer srv.Close()

	st, targetID := testStore(t)
	if err := st.SaveTarget(&model.Target{ID: targetID, Name: "mock", BaseURL: srv.URL, Protocol: model.ProtocolOpenAI}, "sk-test"); err != nil {
		t.Fatal(err)
	}
	report := runModule(t, st, targetID)
	r := report.Models[0]
	if r.Verdict != VerdictSuspicious {
		t.Fatalf("verdict=%s, want %s; checks=%+v", r.Verdict, VerdictSuspicious, r.Checks)
	}
	for _, c := range r.Checks {
		if c.ID == "P3-11" && c.Status != StatusFail {
			t.Fatalf("P3-11 status=%s, want fail", c.Status)
		}
	}
}

func TestLegacyPromptDoesNotContainHardcodedSecrets(t *testing.T) {
	p := legacyOpenAIPromptForTest()
	if strings.Contains(p, "bfb15e6061e1430799302b9284df886f") || strings.Contains(p, "80024f51c0dd403f815d57f51075b0b2") {
		t.Fatal("legacy prompt leaked hardcoded credentials")
	}
}

func legacyOpenAIPromptForTest() string {
	r := &modelRun{hosted: HostedSettings{
		Legacy: LegacyConfig{Collection: "users", EnvVar: "MODEL_AUTHENTICITY_STATUS"},
	}}
	return r.legacyOpenAIPrompt("example.com")
}
