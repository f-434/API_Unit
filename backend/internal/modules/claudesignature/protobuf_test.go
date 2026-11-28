package claudesignature

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func pbTag(field, wire int) []byte {
	return pbUvarint(uint64(field<<3 | wire))
}

func pbUvarint(v uint64) []byte {
	var out []byte
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			b |= 0x80
		}
		out = append(out, b)
		if v == 0 {
			return out
		}
	}
}

func pbVarint(field int, v uint64) []byte {
	return append(pbTag(field, wireVarint), pbUvarint(v)...)
}

func pbBytes(field int, data []byte) []byte {
	out := append(pbTag(field, wireLen), pbUvarint(uint64(len(data)))...)
	return append(out, data...)
}

func testSignature(t *testing.T, extra uint64) string {
	return testSignatureModel(t, extra, "claude-opus-5-5")
}

func testSignatureModel(t *testing.T, extra uint64, modelName string) string {
	t.Helper()
	return testSignatureCustom(t, extra, modelName, "thinking")
}

func testSignatureCustom(t *testing.T, extra uint64, modelName, blockType string) string {
	t.Helper()
	ts := time.Now().Add(-2 * time.Second).Unix()
	var msg []byte
	msg = append(msg, pbBytes(fieldBlockType, []byte(blockType))...)
	msg = append(msg, pbBytes(fieldModel, []byte(modelName))...)
	msg = append(msg, pbBytes(fieldUUID, []byte("123e4567-e89b-12d3-a456-426614174000"))...)
	msg = append(msg, pbVarint(fieldTimestamp, uint64(ts))...)
	// 嵌套 model 消息
	msg = append(msg, pbBytes(fieldModelNested, pbBytes(1, []byte(modelName)))...)
	// 一个真正的 binary 子消息，用于检查解析树。
	msg = append(msg, pbBytes(30, []byte{0x08, 0x01, 0x10, 0x02})...)
	// 变化的字段，避免两次采样生成相同签名。
	msg = append(msg, pbVarint(100, extra)...)
	return base64.StdEncoding.EncodeToString(msg)
}

func TestAnalyzeSignatureMetadata(t *testing.T) {
	info, err := analyzeSignature(testSignature(t, 1), "test")
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if info.Layout != LayoutFull || !info.Recognized {
		t.Fatalf("layout=%s recognized=%v", info.Layout, info.Recognized)
	}
	if info.Model != "claude-opus-5-5" || info.ModelSource != "meta" {
		t.Fatalf("model=%q source=%q", info.Model, info.ModelSource)
	}
	if info.EmbeddedBlockType != "thinking" || info.BlockTypeSource != "meta" {
		t.Fatalf("block=%q source=%q", info.EmbeddedBlockType, info.BlockTypeSource)
	}
	if info.UUID != "123e4567-e89b-12d3-a456-426614174000" || info.UUIDSource != "meta" {
		t.Fatalf("uuid=%q source=%q", info.UUID, info.UUIDSource)
	}
	if info.UnixTimestamp == 0 || info.SignedAtUTC8 == "" {
		t.Fatalf("timestamp=%d signedAt=%q", info.UnixTimestamp, info.SignedAtUTC8)
	}
	if info.MetaFields == nil || info.TotalFields == 0 {
		t.Fatalf("meta fields=%v total=%d", info.MetaFields, info.TotalFields)
	}
	if !strings.Contains(info.Tree, "<root>") || !strings.Contains(info.Tree, "/30[0]") {
		t.Fatalf("tree:\n%s", info.Tree)
	}
	if len(info.Warnings) != 0 {
		t.Fatalf("warnings=%v", info.Warnings)
	}
	if info.SignatureSHA256 == "" || info.SignatureChars == 0 || info.ProtoBytes == 0 {
		t.Fatalf("hash/chars/bytes missing: %+v", info)
	}
}

func TestAnalyzeSignatureURLSafeAndDataURI(t *testing.T) {
	raw := testSignature(t, 2)
	// 转成 URL-safe 且去掉填充。
	urlSafe := strings.TrimRight(base64.URLEncoding.EncodeToString(mustDecode(t, raw)), "=")
	info, err := analyzeSignature("data:application/octet-stream;base64,"+urlSafe, "test")
	if err != nil {
		t.Fatalf("analyze url-safe: %v", err)
	}
	if info.Model != "claude-opus-5-5" {
		t.Fatalf("model=%q", info.Model)
	}
}

func TestAnalyzeSignatureErrors(t *testing.T) {
	if _, err := analyzeSignature("", "test"); err == nil {
		t.Fatal("empty signature should fail")
	}
	if _, err := analyzeSignature("!!!not-base64!!!", "test"); err == nil {
		t.Fatal("invalid alphabet should fail")
	}
	if _, err := analyzeSignature(base64.StdEncoding.EncodeToString([]byte{0x00}), "test"); err == nil {
		t.Fatal("payload with no fields should not be a protobuf message")
	}
}

func TestAnalyzeSignatureWithoutMetaNode(t *testing.T) {
	// 只有字段 1 的 varint（恰好是合理时间戳），找不到元数据节点时应回退到全树搜索。
	raw := pbVarint(1, uint64(time.Now().Add(-time.Second).Unix()))
	info, err := analyzeSignature(base64.StdEncoding.EncodeToString(raw), "test")
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if info.UnixTimestamp == 0 || info.TimestampSource != "search" {
		t.Fatalf("timestamp=%d source=%q", info.UnixTimestamp, info.TimestampSource)
	}
}

func TestAnalyzeSignatureWeakTimestampIsSearchOnly(t *testing.T) {
	// 元数据节点只有时间戳，缺少强提示；应回退到全树搜索而不是把它当成字段 21。
	raw := pbVarint(fieldTimestamp, uint64(time.Now().Add(-time.Second).Unix()))
	info, err := analyzeSignature(base64.StdEncoding.EncodeToString(raw), "test")
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if info.UnixTimestamp == 0 || info.TimestampSource != "search" {
		t.Fatalf("timestamp=%d source=%q", info.UnixTimestamp, info.TimestampSource)
	}
}

func TestAnalyzeSignatureTruncatedKeepsPartial(t *testing.T) {
	// 字段 8 正常，随后声明一个超长 length-delimited 字段。
	raw := append(pbBytes(fieldBlockType, []byte("thinking")), 0x12, 0xff, 0x01)
	info, err := analyzeSignature(base64.StdEncoding.EncodeToString(raw), "test")
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if info.EmbeddedBlockType != "thinking" {
		t.Fatalf("block=%q", info.EmbeddedBlockType)
	}
	if len(info.Warnings) == 0 {
		t.Fatal("expected truncation warning")
	}
}

func TestModelMatch(t *testing.T) {
	cases := []struct {
		requested, embedded, source, want string
	}{
		{"claude-opus-5-5", "claude-opus-5-5", "meta", CheckPass},
		{"claude-sonnet-4-5-20250929", "claude-sonnet-4-5", "meta", CheckPass},
		{"anthropic/claude-opus-5-5", "claude-opus-5-5", "meta", CheckPass},
		{"claude-opus-5-5", "claude-opus-4-6", "meta", CheckWarn},
		{"claude-opus-5-5", "gpt-5", "meta", CheckFail},
		{"claude-opus-5-5", "gpt-5", "search", CheckWarn},
	}
	for _, c := range cases {
		got, evidence := modelMatch(c.requested, c.embedded, c.source)
		if got != c.want {
			t.Errorf("modelMatch(%q,%q,%q)=%s evidence=%s, want %s", c.requested, c.embedded, c.source, got, evidence, c.want)
		}
	}
}

func TestBuildPlansFallback(t *testing.T) {
	cfg := &Config{Thinking: "adaptive", Effort: "high"}
	plans := buildPlans(cfg, "claude-opus-5-5")
	if len(plans) < 3 {
		t.Fatalf("plans=%v", plans)
	}
	if plans[0] != (plan{Thinking: "adaptive", Effort: "high"}) {
		t.Fatalf("first plan=%+v", plans[0])
	}
	foundBudget, foundOff := false, false
	for _, p := range plans {
		if p.Thinking == "budget" {
			foundBudget = true
		}
		if p.Thinking == "off" {
			foundOff = true
		}
	}
	if !foundBudget || !foundOff {
		t.Fatalf("fallback plans missing: %v", plans)
	}
}

func TestSupportsAdaptive(t *testing.T) {
	cases := map[string]bool{
		"claude-opus-5-5":    true,
		"claude-sonnet-4-6":  true,
		"claude-sonnet-4-5":  false,
		"claude-3-7-sonnet":  false,
		"some-unknown-alias": true,
	}
	for name, want := range cases {
		if got := supportsAdaptive(name); got != want {
			t.Errorf("supportsAdaptive(%q)=%v, want %v", name, got, want)
		}
	}
}

func TestAnalyzeResponsePass(t *testing.T) {
	sig := testSignature(t, 3)
	content := []claudeBlock{
		{Type: "thinking", Thinking: "internal reasoning", Signature: sig},
		{Type: "text", Text: "final answer"},
	}
	a := analyzeResponse("claude-opus-5-5", modelNowMs(), content)
	if a.verdict != VerdictPass {
		t.Fatalf("verdict=%s checks=%+v", a.verdict, a.checks)
	}
	if len(a.signatures) != 1 || a.signatures[0].Model != "claude-opus-5-5" {
		t.Fatalf("signatures=%+v", a.signatures)
	}
	if a.outputText != "internal reasoning" {
		t.Fatalf("output=%q", a.outputText)
	}
}

func TestAnalyzeResponseMismatch(t *testing.T) {
	// 签名内声明 text，但实际来自 thinking 块。
	sig := testSignatureCustom(t, 4, "claude-opus-5-5", "text")
	content := []claudeBlock{{Type: "thinking", Thinking: "x", Signature: sig}}
	a := analyzeResponse("claude-opus-5-5", modelNowMs(), content)
	if a.verdict != VerdictSuspicious {
		t.Fatalf("verdict=%s checks=%+v", a.verdict, a.checks)
	}
}

func TestAnalyzeResponseModelFamilyMismatchIsWarning(t *testing.T) {
	sig := testSignatureCustom(t, 5, "claude-opus-4-6", "thinking")
	content := []claudeBlock{{Type: "thinking", Thinking: "x", Signature: sig}}
	a := analyzeResponse("claude-opus-5-5", modelNowMs(), content)
	if a.verdict != VerdictPass {
		t.Fatalf("verdict=%s checks=%+v", a.verdict, a.checks)
	}
	foundWarn := false
	for _, c := range a.checks {
		if c.ID == "model" && c.Status == CheckWarn {
			foundWarn = true
		}
	}
	if !foundWarn {
		t.Fatalf("expected model warning, checks=%+v", a.checks)
	}
}

func TestAnalyzeResponseNoThinking(t *testing.T) {
	a := analyzeResponse("claude-opus-5-5", modelNowMs(), []claudeBlock{{Type: "text", Text: "hi"}})
	if a.verdict != VerdictInconclusive {
		t.Fatalf("verdict=%s checks=%+v", a.verdict, a.checks)
	}
}

func TestAnalyzeResponseRedacted(t *testing.T) {
	data := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x08, 0x01}, 8))
	a := analyzeResponse("claude-opus-5-5", modelNowMs(), []claudeBlock{{Type: "redacted_thinking", Data: data}})
	if a.verdict != VerdictInconclusive {
		t.Fatalf("verdict=%s checks=%+v", a.verdict, a.checks)
	}
	if len(a.signatures) != 1 || !a.signatures[0].Opaque {
		t.Fatalf("signatures=%+v", a.signatures)
	}
}

func mustDecode(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func modelNowMs() int64 { return time.Now().UnixMilli() }
