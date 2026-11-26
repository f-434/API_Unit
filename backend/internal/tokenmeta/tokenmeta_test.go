package tokenmeta

import (
	"testing"

	"apiunit/internal/model"
)

func TestDefaultTaxonomyContainsPlatforms(t *testing.T) {
	tax := DefaultTaxonomy()
	if err := tax.Validate(); err != nil {
		t.Fatalf("内置口径树不合法: %v", err)
	}
	for _, path := range []string{"gemini/as", "gemini/vt", "gpt/openai", "gpt/az", "claude/an", "claude/awsb"} {
		if !tax.HasPath(path) {
			t.Fatalf("缺少节点 %s", path)
		}
	}
	for _, path := range []string{"gemini/12", "gemini/3", "gemini/unknown"} {
		if tax.HasPath(path) {
			t.Fatalf("不应内置示例节点 %s", path)
		}
	}
}

func TestResolveOverrideMostSpecific(t *testing.T) {
	content := "审计提示词"
	p := model.Prompt{
		Content:            content,
		TokenOverrides:     map[string]int{"gemini": 30, "gemini/vt": 34, "gpt/openai": 20},
		TokenOverridesHash: model.HashText(content),
	}
	cases := []struct {
		path string
		want int
		key  string
		ok   bool
	}{
		{"gemini/vt", 34, "gemini/vt", true},
		{"gemini/as", 30, "gemini", true},
		{"gemini", 30, "gemini", true},
		{"gpt/openai", 20, "gpt/openai", true},
		{"gpt/az", 0, "", false},
		{"", 0, "", false},
	}
	for _, c := range cases {
		got, key, ok := ResolveOverride(p, c.path)
		if got != c.want || key != c.key || ok != c.ok {
			t.Fatalf("ResolveOverride(%q) = (%d,%q,%v), want (%d,%q,%v)", c.path, got, key, ok, c.want, c.key, c.ok)
		}
	}
}

func TestResolveOverrideInvalidatedByContentChange(t *testing.T) {
	p := model.Prompt{
		Content:            "旧内容",
		TokenOverrides:     map[string]int{"gemini": 30},
		TokenOverridesHash: model.HashText("旧内容"),
	}
	if _, _, ok := ResolveOverride(p, "gemini"); !ok {
		t.Fatal("内容未变时应命中基准")
	}
	p.Content = "新内容"
	if _, _, ok := ResolveOverride(p, "gemini"); ok {
		t.Fatal("内容变更后旧基准必须失效")
	}
}

func TestNormalizeOverrides(t *testing.T) {
	got := NormalizeOverrides(map[string]int{" /gemini/vt/ ": 12, "": 3, "gpt": 0, "claude": -1})
	if len(got) != 1 || got["gemini/vt"] != 12 {
		t.Fatalf("normalize = %+v", got)
	}
	if NormalizeOverrides(nil) != nil {
		t.Fatal("空映射应返回 nil")
	}
}

func TestTaxonomyValidateRejectsDuplicates(t *testing.T) {
	tax := Taxonomy{
		{Key: "gemini", Label: "Gemini", Children: []Node{
			{Key: "as", Label: "A"}, {Key: "as", Label: "B"},
		}},
	}
	if err := tax.Validate(); err == nil {
		t.Fatal("同级重复键应校验失败")
	}
	if err := (Taxonomy{{Key: "Bad Key", Label: "x"}}).Validate(); err == nil {
		t.Fatal("非法键应校验失败")
	}
}
