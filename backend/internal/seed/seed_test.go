package seed

import "testing"

// TestRetiredPromptsNotSeeded 防止下线的内置提示词被重新加入种子列表。
func TestRetiredPromptsNotSeeded(t *testing.T) {
	retired := map[string]bool{}
	for _, id := range RetiredPromptIDs() {
		retired[id] = true
	}
	for _, p := range Prompts() {
		if retired[p.ID] {
			t.Fatalf("已下线的提示词仍在种子中: %s", p.ID)
		}
	}
	if len(Prompts()) != 2 {
		t.Fatalf("内置提示词应保留 2 条，实际 %d 条", len(Prompts()))
	}
}
