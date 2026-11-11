package tokens

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"apiunit/internal/protocol"
)

func TestTiktokenCount(t *testing.T) {
	c := New(t.TempDir(), true)
	r := c.CountText("gpt-4o-mini", "hello world, this is a tokenizer test.", StrategyAuto)
	if r.Tokens <= 0 || r.Source != "tiktoken:o200k_base" {
		t.Fatalf("unexpected result: %+v", r)
	}
	r2 := c.CountText("gpt-4", "hello world", StrategyTiktoken)
	if r2.Source != "tiktoken:cl100k_base" {
		t.Errorf("expected cl100k, got %+v", r2)
	}
	if r.Tokens == 0 {
		t.Error("zero tokens")
	}
}

func TestHeuristicAndChatOverhead(t *testing.T) {
	c := New(t.TempDir(), true)
	text := c.CountText("claude-3-5-sonnet", "你好，世界", StrategyHeuristic)
	if !text.Estimated || text.Tokens <= 0 {
		t.Errorf("bad heuristic: %+v", text)
	}
	chat := c.CountChat("claude-3-5-sonnet", []protocol.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hello"},
	}, StrategyHeuristic)
	if chat.Tokens <= text.Tokens {
		t.Errorf("chat count should include overhead: %+v", chat)
	}
}

func TestGeminiSpecSelection(t *testing.T) {
	if spec := geminiSpecFor("gemini-1.5-pro"); spec.Name != "gemma2" {
		t.Errorf("expected gemma2, got %s", spec.Name)
	}
	if spec := geminiSpecFor("gemini-2.5-flash"); spec.Name != "gemma3" {
		t.Errorf("expected gemma3, got %s", spec.Name)
	}
}

func TestFamily(t *testing.T) {
	cases := map[string]string{
		"gpt-4o": "openai", "openai/o3-mini": "openai", "gemini-2.5-pro": "gemini",
		"claude-sonnet": "claude", "deepseek-chat": "other",
	}
	for model, want := range cases {
		if got := family(model); got != want {
			t.Errorf("family(%s)=%s, want %s", model, got, want)
		}
	}
}

func TestGeminiUnavailableFallsBack(t *testing.T) {
	c := New(t.TempDir(), true) // offline and no model file
	r := c.CountText("gemini-2.5-flash", "hello", StrategyAuto)
	if !r.Estimated || r.Source != "heuristic" {
		t.Errorf("expected heuristic fallback, got %+v", r)
	}
}

func TestVerifySHA256(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model.bin")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("hello"))
	want := hex.EncodeToString(sum[:])
	if err := verifySHA256(path, want); err != nil {
		t.Fatalf("valid file rejected: %v", err)
	}
	if err := verifySHA256(path, "deadbeef"); err == nil {
		t.Fatal("expected checksum mismatch")
	}
	if err := verifySHA256(filepath.Join(t.TempDir(), "missing"), want); !os.IsNotExist(err) {
		t.Fatalf("expected not-exist error, got %v", err)
	}
}

func TestResetGeminiClearsState(t *testing.T) {
	dir := t.TempDir()
	c := New(dir, false)
	for _, spec := range geminiSpecs {
		if err := os.WriteFile(filepath.Join(dir, spec.File), []byte("stale"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c.processors["gemma3"] = &geminiProcessor{name: "gemma3"}
	c.geminiErrors["gemma3"] = "download failed"
	c.geminiErrors["gemma2"] = "checksum mismatch"

	if err := c.ResetGemini(); err != nil {
		t.Fatal(err)
	}
	st := c.Status()
	for _, g := range st.Gemini {
		if g.Downloaded || g.Loaded || g.Error != "" {
			t.Errorf("status not cleared: %+v", g)
		}
		if _, err := os.Stat(filepath.Join(dir, g.File)); !os.IsNotExist(err) {
			t.Errorf("%s was not removed", g.File)
		}
	}
}

func TestEnsureGeminiOffline(t *testing.T) {
	c := New(t.TempDir(), true)
	if err := c.EnsureGemini(t.Context()); err == nil {
		t.Fatal("expected offline error")
	}
}
