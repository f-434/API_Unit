package tokenaudit

import (
	"strings"
	"testing"

	"apiunit/internal/model"
)

// TestManualBaselineDrivesOverhead 验证手工基准被当作可信本地值参与注入判定。
func TestManualBaselineDrivesOverhead(t *testing.T) {
	acc := NewAccumulator()
	acc.Add(0, "p1", model.Item{
		OK: true, PromptTokens: 130, LocalPromptTokens: 100,
		Meta: map[string]any{"promptUsage": true, "localSource": "manual"},
	})
	rep := BuildReport(10, "min", []TargetRef{{
		TargetID: "t1", Label: "目标 1", Model: "gemini-2.5-flash",
		Protocol: "gemini", TokenFamily: "gemini/vt",
	}}, []model.Prompt{{ID: "p1", Name: "审计"}}, acc)

	if len(rep.Prompts) != 1 || len(rep.Prompts[0].Cells) != 1 {
		t.Fatalf("报告结构异常: %+v", rep.Prompts)
	}
	cell := rep.Prompts[0].Cells[0]
	if cell.LocalTokens != 100 || cell.LocalSource != "manual" {
		t.Fatalf("本地基准未生效: %+v", cell)
	}
	if cell.OverheadPct != 30 {
		t.Fatalf("偏差 = %v, want 30", cell.OverheadPct)
	}
	if cell.Status != "inflation" || cell.TokenFamily != "gemini/vt" {
		t.Fatalf("判定 = %s, family = %q", cell.Status, cell.TokenFamily)
	}
	if !strings.Contains(strings.Join(cell.Reasons, "；"), "手工基准") {
		t.Fatalf("原因应提到手工基准: %+v", cell.Reasons)
	}
	if rep.Targets[0].TokenFamily != "gemini/vt" {
		t.Fatalf("目标家族 = %q", rep.Targets[0].TokenFamily)
	}
}

func TestLocalComparableManual(t *testing.T) {
	if !localComparable("gpt-4o", "manual", "") || !localComparable("gemini-2.5-flash", "manual", "") {
		t.Fatal("手工基准应与任意模型家族可比")
	}
	if localComparable("gemini-2.5-flash", "tiktoken:o200k_base", "") {
		t.Fatal("tiktoken 不应与 Gemini 模型比较")
	}
	// 渠道口径优先于模型名，适配中转站把模型重命名的情况。
	if !localComparable("relay-alias", "gemini-local:gemma3", "gemini/vt") {
		t.Fatal("Gemini 口径应允许 Gemini 分词器比较")
	}
	if localComparable("gemini-2.5-flash", "tiktoken:o200k_base", "gemini/vt") {
		t.Fatal("Gemini 口径不应与 tiktoken 比较")
	}
	if !localComparable("relay-alias", "tiktoken:o200k_base", "gpt/az") {
		t.Fatal("GPT 口径应允许 tiktoken 比较")
	}
}
