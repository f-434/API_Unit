package store

import (
	"path/filepath"
	"testing"

	"apiunit/internal/model"
	"apiunit/internal/secret"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	box, err := secret.LoadOrCreate(filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatalf("secret: %v", err)
	}
	st, err := Open(filepath.Join(dir, "apiunit.db"), box)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// TestDeleteBuiltinPrompts 验证下线的内置提示词会被清理，其余提示词不受影响。
func TestDeleteBuiltinPrompts(t *testing.T) {
	st := newTestStore(t)
	if err := st.EnsureBuiltinPrompts([]model.Prompt{
		{ID: "p_retired", Name: "下线", Category: "builtin", Builtin: true},
		{ID: "p_keep", Name: "保留", Category: "builtin", Builtin: true},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := st.DeleteBuiltinPrompts([]string{"p_retired"}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	list, err := st.ListPrompts()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	got := map[string]bool{}
	for _, p := range list {
		got[p.ID] = true
	}
	if got["p_retired"] {
		t.Fatal("下线的内置提示词应被删除")
	}
	if !got["p_keep"] {
		t.Fatal("其他内置提示词不应受影响")
	}
}
