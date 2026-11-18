package authenticity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"apiunit/internal/engine"
	"apiunit/internal/model"
	"apiunit/internal/protocol"
	"apiunit/internal/tokens"
)

const realGeminiSignature = "QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVphYmNkZWZnaGlqa2xtbm9wcXJzdHV2d3h5ejAxMjM0NTY3ODk="

func TestGeminiThoughtSignatureEnforced(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		tools, _ := body["tools"].([]any)
		// Fingerprint probe: tools=[{"xxx":{}}]
		for _, tool := range tools {
			if m, ok := tool.(map[string]any); ok {
				if _, bad := m["xxx"]; bad {
					w.WriteHeader(400)
					_, _ = w.Write([]byte(`{"error":{"code":400,"message":"Invalid JSON payload received. Unknown name \"xxx\" at 'tools[0]': Cannot find field.","type":"upstream_error"}}`))
					return
				}
			}
		}
		contents, _ := body["contents"].([]any)
		if len(contents) >= 3 {
			// Replay. Inspect the model part's signature.
			sig := ""
			last, _ := contents[len(contents)-1].(map[string]any)
			_ = last
			modelPart, _ := contents[1].(map[string]any)
			parts, _ := modelPart["parts"].([]any)
			for _, p := range parts {
				pm, _ := p.(map[string]any)
				if s, ok := pm["thoughtSignature"].(string); ok {
					sig = s
				}
			}
			if sig != realGeminiSignature {
				w.WriteHeader(400)
				_, _ = w.Write([]byte(`{"error":{"code":400,"message":"thoughtSignature is invalid or has been tampered with","status":"INVALID_ARGUMENT"}}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"candidates": []any{map[string]any{
					"content": map[string]any{"role": "model", "parts": []any{map[string]any{
						"text": "The flight is delayed; departure is 12 PM.",
					}}},
					"finishReason": "STOP",
				}},
				"usageMetadata": map[string]any{"promptTokenCount": 30, "candidatesTokenCount": 10, "totalTokenCount": 40},
				"modelVersion":  "gemini-3.6-flash",
			})
			return
		}
		if len(tools) > 0 {
			// Signature first request.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"candidates": []any{map[string]any{
					"content": map[string]any{"role": "model", "parts": []any{map[string]any{
						"functionCall":     map[string]any{"name": "check_flight", "args": map[string]any{"flight": "AA100"}},
						"thoughtSignature": realGeminiSignature,
					}}},
					"finishReason": "STOP",
				}},
				"usageMetadata": map[string]any{"promptTokenCount": 20, "candidatesTokenCount": 10, "totalTokenCount": 30},
				"modelVersion":  "gemini-3.6-flash",
			})
			return
		}
		// Ping.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"candidates": []any{map[string]any{
				"content":      map[string]any{"role": "model", "parts": []any{map[string]any{"text": "pong"}}},
				"finishReason": "STOP",
			}},
			"usageMetadata": map[string]any{"promptTokenCount": 3, "candidatesTokenCount": 1, "totalTokenCount": 4},
			"modelVersion":  "gemini-3.6-flash",
		})
	}))
	defer srv.Close()

	report := runSignatureModule(t, srv.URL)
	r := report.Models[0]
	for _, c := range r.Checks {
		if strings.HasPrefix(c.ID, "SG") {
			t.Logf("%s %s %s", c.ID, c.Status, c.Evidence)
		}
	}
	if r.Verdict != VerdictAuthentic {
		t.Fatalf("verdict=%s, want authentic; checks=%+v", r.Verdict, r.Checks)
	}
	for _, id := range []string{"SG3", "SG4", "SG5", "SG6"} {
		found := false
		for _, c := range r.Checks {
			if c.ID == id && (c.Status == StatusPass || c.Status == StatusWarn) {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s not passed: %+v", id, r.Checks)
		}
	}
}

func runSignatureModule(t *testing.T, baseURL string) *Report {
	t.Helper()
	st, targetID := testStore(t)
	if err := st.SaveTarget(&model.Target{ID: targetID, Name: "mock", BaseURL: baseURL, Protocol: model.ProtocolGemini}, "gm-test"); err != nil {
		t.Fatal(err)
	}
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
		Targets: []TargetSpec{{TargetID: targetID, Models: []string{"gemini-3.6-flash"}, Protocol: "gemini"}},
		Checks: ChecksConfig{
			Fingerprint: true, Source: true, Capability: false,
			Identity: true, Consistency: true, Signature: true,
		},
	})
	validated, err := mod.Validate(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := mod.Run(context.Background(), sess, validated); err != nil {
		t.Fatal(err)
	}
	sess.FinishSession(model.StatusCompleted, "")
	return sess.ModuleResult().(*Report)
}

func TestGeminiTamperedSignatureAcceptedIsSuspicious(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		tools, _ := body["tools"].([]any)
		for _, tool := range tools {
			if m, ok := tool.(map[string]any); ok {
				if _, bad := m["xxx"]; bad {
					w.WriteHeader(400)
					_, _ = w.Write([]byte(`{"error":{"code":400,"message":"Invalid JSON payload received. Unknown name \"xxx\" at 'tools[0]': Cannot find field."}}`))
					return
				}
			}
		}
		contents, _ := body["contents"].([]any)
		if len(contents) >= 3 {
			// A fake relay accepts both real and tampered signatures.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"candidates": []any{map[string]any{
					"content":      map[string]any{"role": "model", "parts": []any{map[string]any{"text": "delayed, 12 PM"}}},
					"finishReason": "STOP",
				}},
				"usageMetadata": map[string]any{"promptTokenCount": 30, "candidatesTokenCount": 10, "totalTokenCount": 40},
				"modelVersion":  "gemini-3.6-flash",
			})
			return
		}
		if len(tools) > 0 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"candidates": []any{map[string]any{
					"content": map[string]any{"role": "model", "parts": []any{map[string]any{
						"functionCall":     map[string]any{"name": "check_flight", "args": map[string]any{"flight": "AA100"}},
						"thoughtSignature": realGeminiSignature,
					}}},
					"finishReason": "STOP",
				}},
				"usageMetadata": map[string]any{"promptTokenCount": 20, "candidatesTokenCount": 10, "totalTokenCount": 30},
				"modelVersion":  "gemini-3.6-flash",
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"candidates": []any{map[string]any{
				"content":      map[string]any{"role": "model", "parts": []any{map[string]any{"text": "pong"}}},
				"finishReason": "STOP",
			}},
			"usageMetadata": map[string]any{"promptTokenCount": 3, "candidatesTokenCount": 1, "totalTokenCount": 4},
			"modelVersion":  "gemini-3.6-flash",
		})
	}))
	defer srv.Close()

	report := runSignatureModule(t, srv.URL)
	r := report.Models[0]
	if r.Verdict != VerdictSuspicious {
		t.Fatalf("verdict=%s, want suspicious; checks=%+v", r.Verdict, r.Checks)
	}
	for _, c := range r.Checks {
		if c.ID == "SG6" && c.Status != StatusFail {
			t.Fatalf("SG6=%s, want fail", c.Status)
		}
	}
}
