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

func TestModelKey(t *testing.T) {
	cases := map[string]string{
		"gemini-2.5-pro":          "gemini-2.5-pro",
		"models/gemini-2.5-flash": "gemini-2.5-flash",
		"Claude-3.5-Sonnet":       "claude-3.5-sonnet",
		"anthropic/claude-3.5":    "anthropic-claude-3.5",
		"gpt-4o:free":             "gpt-4o:free",
		"  Meta Llama 3.1  ":      "meta-llama-3.1",
		"":                        "",
	}
	for in, want := range cases {
		if got := ModelKey(in); got != want {
			t.Fatalf("ModelKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestJoinModelRequiresPlatform(t *testing.T) {
	if got := JoinModel("gemini/vt", "gemini-2.5-pro"); got != "gemini/vt/gemini-2.5-pro" {
		t.Fatalf("平台级应拼接模型: %q", got)
	}
	if got := JoinModel("gemini", "gemini-2.5-pro"); got != "gemini" {
		t.Fatalf("家族级不应拼接模型: %q", got)
	}
	if got := JoinModel("", "gemini-2.5-pro"); got != "" {
		t.Fatalf("空口径不应拼接模型: %q", got)
	}
	if got := JoinModel("gemini/vt", " "); got != "gemini/vt" {
		t.Fatalf("空模型应保持平台: %q", got)
	}
}

func TestResolveForModelFallsBackUp(t *testing.T) {
	content := "审计提示词"
	p := model.Prompt{
		Content: content,
		TokenOverrides: map[string]int{
			"gemini": 30, "gemini/vt": 34, "gemini/vt/gemini-2.5-pro": 40,
		},
		TokenOverridesHash: model.HashText(content),
	}
	cases := []struct {
		model string
		want  int
		key   string
	}{
		{"gemini-2.5-pro", 40, "gemini/vt/gemini-2.5-pro"},
		{"gemini-2.5-flash", 34, "gemini/vt"},
		{"models/Gemini-2.5-Pro", 40, "gemini/vt/gemini-2.5-pro"},
	}
	for _, c := range cases {
		got, key, ok := ResolveForModel(p, "gemini/vt", c.model)
		if !ok || got != c.want || key != c.key {
			t.Fatalf("ResolveForModel(%q) = (%d,%q,%v), want (%d,%q)", c.model, got, key, ok, c.want, c.key)
		}
	}
}

func TestAcceptsPathModelLevel(t *testing.T) {
	tax := DefaultTaxonomy()
	if !tax.AcceptsPath("gemini/vt") {
		t.Fatal("平台路径应被接受")
	}
	if !tax.AcceptsPath("gemini/vt/gemini-2.5-pro") {
		t.Fatal("平台下的模型路径应被接受")
	}
	if tax.AcceptsPath("gemini/vt/gemini-2.5-pro/extra") {
		t.Fatal("超过三级的路径应被拒绝")
	}
	if tax.AcceptsPath("gemini/unknown") {
		t.Fatal("父级不存在时应拒绝")
	}
}

func TestTaxonomyValidateMaxDepth(t *testing.T) {
	tax := Taxonomy{{Key: "a", Label: "A", Children: []Node{
		{Key: "b", Label: "B", Children: []Node{
			{Key: "c", Label: "C", Children: []Node{{Key: "d", Label: "D"}}},
		}},
	}}}
	if err := tax.ValidateDepth(); err == nil {
		t.Fatal("超过三级应校验失败")
	}
	// 历史数据仍可读取，避免超过三级的旧树被整体回退。
	if err := tax.Validate(); err != nil {
		t.Fatalf("读取旧树不应因层级失败: %v", err)
	}
}

func TestAttachModelNode(t *testing.T) {
	tax := DefaultTaxonomy()
	next := attachModelNode(tax, "gemini", "vt", "gemini-2.5-pro")
	if !next.HasPath("gemini/vt/gemini-2.5-pro") {
		t.Fatal("模型节点未挂载")
	}
	if tax.HasPath("gemini/vt/gemini-2.5-pro") {
		t.Fatal("不应修改原树")
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
