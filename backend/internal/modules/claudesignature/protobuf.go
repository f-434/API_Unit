package claudesignature

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// protobuf wire type 常量。
const (
	wireVarint     = 0
	wireFixed64    = 1
	wireLen        = 2
	wireStartGroup = 3
	wireEndGroup   = 4
	wireFixed32    = 5
)

const (
	maxVarintBytes = 10
	maxFields      = 200_000
	maxNestDepth   = 8
	minSubmessage  = 2
	maxSubmessage  = 4 << 20
	maxDecoded     = 16 << 20
	// maxNodes 限制整棵解析树的节点数，避免恶意载荷导致指数级展开。
	maxNodes = 4096
)

// 这些字段号来自迄今为止观察到的签名样本，只作为“最可能的元数据位置”的提示。
// 解析器不依赖固定路径：字段缺失时会回退到整棵树的结构化搜索。
const (
	fieldBlockType   = 8
	fieldModel       = 6
	fieldUUID        = 11
	fieldTimestamp   = 21
	fieldModelNested = 19
)

var knownBlockTypes = map[string]bool{
	"thinking":                        true,
	"redacted_thinking":               true,
	"text":                            true,
	"tool_use":                        true,
	"tool_result":                     true,
	"server_tool_use":                 true,
	"web_search_tool_result":          true,
	"bash_code_execution_tool_result": true,
	"code_execution_tool_result":      true,
	"document":                        true,
	"image":                           true,
	"search_result":                   true,
}

var (
	modelRe = regexp.MustCompile(`(?i)^claude[-_][A-Za-z0-9._-]+$`)
	uuidRe  = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	b64Re   = regexp.MustCompile(`^[A-Za-z0-9+/=_-]+$`)
)

// 时间戳合理范围：2000-01-01 到当前时间 + 10 年。
var (
	tsMin = int64(946_684_800)
	tsMax = time.Now().AddDate(10, 0, 0).Unix()
)

// protoValue 保存一个 wire 值；未使用的分支保持零值。
type protoValue struct {
	wire  int
	u     uint64
	data  []byte
	fixed []byte
}

// protoMessage 是解析树中的一个节点。
type protoMessage struct {
	path     string
	depth    int
	size     int
	fields   map[int][]protoValue
	warnings []string
	children []*protoMessage
}

func (m *protoMessage) walk(fn func(*protoMessage)) {
	if m == nil {
		return
	}
	fn(m)
	for _, c := range m.children {
		c.walk(fn)
	}
}

func readVarint(buf []byte, pos int) (uint64, int, error) {
	var value uint64
	var shift uint
	start := pos
	for i := 0; i < maxVarintBytes; i++ {
		if pos >= len(buf) {
			return 0, pos, fmt.Errorf("varint 在偏移 %d 处被截断", start)
		}
		b := buf[pos]
		pos++
		value |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return value, pos, nil
		}
		shift += 7
	}
	return 0, pos, fmt.Errorf("偏移 %d 处的 varint 超过 %d 字节", start, maxVarintBytes)
}

// skipGroup 跳过已废弃的 group wire 类型，返回 group 结束后的偏移。
func skipGroup(buf []byte, pos, fieldNumber int) (int, error) {
	depth := 1
	total := len(buf)
	for depth > 0 {
		if pos >= total {
			return pos, fmt.Errorf("字段 %d 的 group 未正常结束", fieldNumber)
		}
		tag, next, err := readVarint(buf, pos)
		if err != nil {
			return pos, err
		}
		pos = next
		switch int(tag & 7) {
		case wireStartGroup:
			depth++
		case wireEndGroup:
			depth--
		case wireVarint:
			_, next, err = readVarint(buf, pos)
			if err != nil {
				return pos, err
			}
			pos = next
		case wireFixed64:
			if pos+8 > total {
				return pos, fmt.Errorf("group 内的 fixed64 被截断")
			}
			pos += 8
		case wireLen:
			length, next, err := readVarint(buf, pos)
			if err != nil {
				return pos, err
			}
			pos = next
			if length > uint64(total-pos) {
				return pos, fmt.Errorf("group 内的 length-delimited 字段越界")
			}
			pos += int(length)
		case wireFixed32:
			if pos+4 > total {
				return pos, fmt.Errorf("group 内的 fixed32 被截断")
			}
			pos += 4
		default:
			return pos, fmt.Errorf("group 内出现非法 wire type %d", tag&7)
		}
	}
	return pos, nil
}

// parseFields 解析一条 protobuf 消息且不向上抛错；已解析字段与警告会一并返回。
// length-delimited 字段返回原始字节，varint 返回整数，定宽字段返回原始字节。
func parseFields(buf []byte) (map[int][]protoValue, []string) {
	fields := map[int][]protoValue{}
	warnings := make([]string, 0, 2)
	pos := 0
	total := len(buf)
	parsed := 0

	for pos < total {
		parsed++
		if parsed > maxFields {
			warnings = append(warnings, "字段数超过上限，载荷可能不是 protobuf")
			break
		}
		start := pos
		tag, next, err := readVarint(buf, pos)
		if err != nil {
			warnings = append(warnings, err.Error())
			break
		}
		pos = next
		number := int(tag >> 3)
		wire := int(tag & 7)
		if number == 0 {
			warnings = append(warnings, fmt.Sprintf("偏移 %d 处出现非法字段号 0", start))
			break
		}

		var value protoValue
		value.wire = wire
		switch wire {
		case wireVarint:
			v, next, err := readVarint(buf, pos)
			if err != nil {
				warnings = append(warnings, err.Error())
				pos = total
				continue
			}
			pos = next
			value.u = v
		case wireLen:
			length, next, err := readVarint(buf, pos)
			if err != nil {
				warnings = append(warnings, err.Error())
				pos = total
				continue
			}
			pos = next
			if length > uint64(total-pos) {
				warnings = append(warnings, fmt.Sprintf(
					"字段 %d: 长度 %d 超过偏移 %d 处剩余的 %d 字节",
					number, length, start, total-pos))
				fields[number] = append(fields[number], protoValue{wire: wire, data: buf[pos:total]})
				pos = total
				continue
			}
			value.data = buf[pos : pos+int(length)]
			pos += int(length)
		case wireFixed64:
			if pos+8 > total {
				warnings = append(warnings, fmt.Sprintf("字段 %d: 偏移 %d 处的 fixed64 被截断", number, start))
				pos = total
				continue
			}
			value.fixed = buf[pos : pos+8]
			pos += 8
		case wireFixed32:
			if pos+4 > total {
				warnings = append(warnings, fmt.Sprintf("字段 %d: 偏移 %d 处的 fixed32 被截断", number, start))
				pos = total
				continue
			}
			value.fixed = buf[pos : pos+4]
			pos += 4
		case wireStartGroup:
			// group 是废弃 wire 类型，Claude 不会使用；跳过而不是中止整条消息。
			end, err := skipGroup(buf, pos, number)
			if err != nil {
				warnings = append(warnings, err.Error())
				pos = total
				continue
			}
			pos = end
			value.fixed = []byte("<group>")
		case wireEndGroup:
			warnings = append(warnings, fmt.Sprintf("偏移 %d 处出现意外的 end-group", start))
			pos = total
			continue
		default:
			warnings = append(warnings, fmt.Sprintf("偏移 %d 处出现非法 wire type %d", start, wire))
			pos = total
			continue
		}
		fields[number] = append(fields[number], value)
	}
	return fields, warnings
}

// isText 判断字节是否是可打印 UTF-8 文本。可打印文本几乎不可能是
// length-delimited 子消息，因此不会继续向下展开。
func isText(data []byte) bool {
	if len(data) == 0 || !utf8.Valid(data) {
		return false
	}
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		switch {
		case r == utf8.RuneError && size == 1:
			return false
		case r == '\t' || r == '\r' || r == '\n':
		case r >= 0x20 && r != 0x7f:
		default:
			return false
		}
		data = data[size:]
	}
	return true
}

func plausibleSubmessage(data []byte) bool {
	return len(data) >= minSubmessage && len(data) <= maxSubmessage && !isText(data)
}

// buildTree 递归向下展开。只有“干净解析且至少含一个字段”的子消息才会被保留，
// 带警告的节点基本是碰巧能解析的随机字节。
func buildTree(buf []byte, path string, depth int, nodes *int) *protoMessage {
	fields, warnings := parseFields(buf)
	node := &protoMessage{path: path, depth: depth, size: len(buf), fields: fields, warnings: warnings}
	*nodes = *nodes + 1
	if depth >= maxNestDepth || *nodes >= maxNodes {
		return node
	}
	numbers := make([]int, 0, len(fields))
	for n := range fields {
		numbers = append(numbers, n)
	}
	sort.Ints(numbers)
	for _, number := range numbers {
		values := fields[number]
		for idx, value := range values {
			if value.wire != wireLen || len(value.data) == 0 {
				continue
			}
			if !plausibleSubmessage(value.data) {
				continue
			}
			if *nodes >= maxNodes {
				return node
			}
			child := buildTree(value.data, fmt.Sprintf("%s/%d[%d]", path, number, idx), depth+1, nodes)
			if len(child.fields) > 0 && len(child.warnings) == 0 {
				node.children = append(node.children, child)
			}
		}
	}
	return node
}

func textOf(v protoValue) (string, bool) {
	if v.wire != wireLen || len(v.data) == 0 || !utf8.Valid(v.data) {
		return "", false
	}
	return string(v.data), true
}

func isModelName(s string) bool {
	return len(s) < 128 && modelRe.MatchString(s)
}

func isUUID(s string) bool {
	return uuidRe.MatchString(s)
}

func normalizeTimestamp(v int64) (int64, bool) {
	switch {
	case v > 1_000_000_000_000_000: // 微秒
		v /= 1_000_000
	case v > 1_000_000_000_000: // 毫秒
		v /= 1_000
	}
	if v < tsMin || v > tsMax {
		return 0, false
	}
	return v, true
}

func scoreMeta(node *protoMessage) int {
	if node == nil {
		return 0
	}
	score := 0
	for _, v := range node.fields[fieldBlockType] {
		if s, ok := textOf(v); ok && knownBlockTypes[s] {
			score += 8
			break
		}
	}
	for _, v := range node.fields[fieldUUID] {
		if s, ok := textOf(v); ok && isUUID(s) {
			score += 7
			break
		}
	}
	for _, v := range node.fields[fieldModel] {
		if s, ok := textOf(v); ok && isModelName(s) {
			score += 7
			break
		}
	}
	for _, v := range node.fields[fieldTimestamp] {
		if v.wire == wireVarint {
			if _, ok := normalizeTimestamp(int64(v.u)); ok {
				score += 5
				break
			}
		}
	}
	texts := messageTexts(node)
	for _, t := range texts {
		if isModelName(t) {
			score += 2
			break
		}
	}
	for _, t := range texts {
		if isUUID(t) {
			score += 2
			break
		}
	}
	if len(node.fields) >= 5 {
		score++
	}
	return score
}

func findMeta(root *protoMessage) *protoMessage {
	var best *protoMessage
	bestScore := 0
	root.walk(func(n *protoMessage) {
		score := scoreMeta(n)
		if score <= 0 {
			return
		}
		// 分数相同时优先更深的节点，再优先字段更多的节点；与 Python 版一致，
		// 完全并列时保留先遍历到的节点。
		if best == nil {
			best, bestScore = n, score
			return
		}
		if score > bestScore ||
			(score == bestScore && n.depth > best.depth) ||
			(score == bestScore && n.depth == best.depth && len(n.fields) > len(best.fields)) {
			best, bestScore = n, score
		}
	})
	// 阈值 7 要求至少命中一个“强提示”（block type / model / uuid），
	// 避免把只有随机时间戳的节点当成元数据；弱提示仍可通过全树搜索使用，
	// 但来源会标记为 search，只产生警告而不直接判可疑。
	if bestScore < 7 {
		return nil
	}
	return best
}

func messageTexts(node *protoMessage) []string {
	if node == nil {
		return nil
	}
	out := make([]string, 0, len(node.fields))
	numbers := make([]int, 0, len(node.fields))
	for n := range node.fields {
		numbers = append(numbers, n)
	}
	sort.Ints(numbers)
	for _, n := range numbers {
		for _, v := range node.fields[n] {
			if s, ok := textOf(v); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// orderedNodes 返回搜索顺序：先最可能的元数据节点及其子树，再整棵树。
func orderedNodes(meta, root *protoMessage) []*protoMessage {
	seen := map[*protoMessage]bool{}
	out := make([]*protoMessage, 0, 16)
	add := func(n *protoMessage) {
		if n == nil || seen[n] {
			return
		}
		seen[n] = true
		out = append(out, n)
	}
	meta.walk(add)
	root.walk(add)
	return out
}

func strField(node *protoMessage, number int, pred func(string) bool) (string, bool) {
	if node == nil {
		return "", false
	}
	for _, v := range node.fields[number] {
		if s, ok := textOf(v); ok && (pred == nil || pred(s)) {
			return s, true
		}
	}
	return "", false
}

func intField(node *protoMessage, number int, pred func(int64) bool) (int64, bool) {
	if node == nil {
		return 0, false
	}
	for _, v := range node.fields[number] {
		if v.wire == wireVarint {
			n := int64(v.u)
			if pred == nil || pred(n) {
				return n, true
			}
		}
	}
	return 0, false
}

func firstStr(nodes []*protoMessage, pred func(string) bool) string {
	for _, n := range nodes {
		for _, t := range messageTexts(n) {
			if pred == nil || pred(t) {
				return t
			}
		}
	}
	return ""
}

func firstInt(nodes []*protoMessage, pred func(int64) bool) (int64, bool) {
	for _, n := range nodes {
		if n == nil {
			continue
		}
		numbers := make([]int, 0, len(n.fields))
		for x := range n.fields {
			numbers = append(numbers, x)
		}
		sort.Ints(numbers)
		for _, x := range numbers {
			for _, v := range n.fields[x] {
				if v.wire == wireVarint {
					nv := int64(v.u)
					if pred == nil || pred(nv) {
						return nv, true
					}
				}
			}
		}
	}
	return 0, false
}

func nestedModel(meta *protoMessage) string {
	if meta == nil {
		return ""
	}
	for _, v := range meta.fields[fieldModelNested] {
		if v.wire != wireLen {
			continue
		}
		fields, _ := parseFields(v.data)
		for _, nested := range fields[1] {
			if s, ok := textOf(nested); ok && isModelName(s) {
				return s
			}
		}
	}
	return ""
}

// describeTree 输出可读的解析树；深度、行数、字段列表和总字节数都受限，
// 避免恶意载荷把报告撑大。
func describeTree(root *protoMessage, maxDepth int) string {
	const (
		maxTreeBytes = 8 << 10
		maxTreeLines = 120
		maxFieldList = 80
	)
	var b strings.Builder
	lines := 0
	stopped := false
	var visit func(n *protoMessage)
	visit = func(n *protoMessage) {
		if stopped || n == nil || n.depth > maxDepth {
			return
		}
		if b.Len() > maxTreeBytes || lines >= maxTreeLines {
			stopped = true
			return
		}
		numbers := make([]int, 0, len(n.fields))
		for x := range n.fields {
			numbers = append(numbers, x)
		}
		sort.Ints(numbers)
		truncated := false
		if len(numbers) > maxFieldList {
			numbers = numbers[:maxFieldList]
			truncated = true
		}
		parts := make([]string, 0, len(numbers)+1)
		for _, x := range numbers {
			parts = append(parts, fmt.Sprintf("%d", x))
		}
		if truncated {
			parts = append(parts, "…")
		}
		label := n.path
		if label == "" {
			label = "<root>"
		}
		var line strings.Builder
		line.WriteString(strings.Repeat("  ", n.depth))
		fmt.Fprintf(&line, "%s (%dB): fields %s", label, n.size, orDash(strings.Join(parts, ",")))
		if len(n.warnings) > 0 {
			fmt.Fprintf(&line, " warnings=%d", len(n.warnings))
		}
		if b.Len()+line.Len()+1 > maxTreeBytes {
			stopped = true
			return
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(line.String())
		lines++
		for _, c := range n.children {
			visit(c)
		}
	}
	visit(root)
	if stopped {
		b.WriteString("\n…（结构过大，已截断）")
	}
	return b.String()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// analyzeSignature 解码并解析一个 signature / redacted_thinking data。
func analyzeSignature(signature, label string) (*SignatureInfo, error) {
	compact, err := compactBase64(signature, label)
	if err != nil {
		return nil, err
	}
	padded := compact
	if rem := len(padded) % 4; rem != 0 {
		padded += strings.Repeat("=", 4-rem)
	}
	padded = strings.ReplaceAll(padded, "-", "+")
	padded = strings.ReplaceAll(padded, "_", "/")
	decoded, err := base64.StdEncoding.DecodeString(padded)
	if err != nil {
		return nil, fmt.Errorf("%s 不是有效的 base64: %w", label, err)
	}
	if len(decoded) == 0 {
		return nil, fmt.Errorf("%s 解码后为空", label)
	}
	if len(decoded) > maxDecoded {
		return nil, fmt.Errorf("%s 解码后体积异常（%d 字节）", label, len(decoded))
	}

	nodes := 0
	root := buildTree(decoded, "", 0, &nodes)
	if len(root.fields) == 0 {
		return nil, fmt.Errorf("%s 解码得到 %d 字节，但不是 protobuf 消息", label, len(decoded))
	}

	meta := findMeta(root)
	ordered := orderedNodes(meta, root)

	info := &SignatureInfo{
		SignatureChars:  len(compact),
		ProtoBytes:      len(decoded),
		MetaPath:        "",
		Warnings:        append([]string(nil), root.warnings...),
		SignaturePrefix: prefix(compact, 64),
		SignatureSHA256: sha256Hex(decoded),
		Tree:            describeTree(root, 4),
	}
	if meta != nil {
		info.MetaPath = meta.path
		info.MetaFields = sortedKeys(meta.fields)
		if len(info.MetaFields) > 128 {
			info.MetaFields = info.MetaFields[:128]
		}
	}
	total := 0
	root.walk(func(n *protoMessage) {
		for _, values := range n.fields {
			total += len(values)
		}
	})
	info.TotalFields = total

	// block type
	if s, ok := strField(meta, fieldBlockType, func(v string) bool { return knownBlockTypes[v] }); ok {
		info.EmbeddedBlockType, info.BlockTypeSource = s, "meta"
	} else if s := firstStr(ordered, func(v string) bool { return knownBlockTypes[v] }); s != "" {
		info.EmbeddedBlockType, info.BlockTypeSource = s, "search"
	}

	// model：优先字段 6，其次字段 19 的嵌套消息，最后全树搜索。
	if s, ok := strField(meta, fieldModel, isModelName); ok {
		info.Model, info.ModelSource = s, "meta"
	} else if s := nestedModel(meta); s != "" {
		info.Model, info.ModelSource = s, "nested"
	} else if s := firstStr(ordered, isModelName); s != "" {
		info.Model, info.ModelSource = s, "search"
	}

	// uuid
	if s, ok := strField(meta, fieldUUID, isUUID); ok {
		info.UUID, info.UUIDSource = s, "meta"
	} else if s := firstStr(ordered, isUUID); s != "" {
		info.UUID, info.UUIDSource = s, "search"
	}

	// 时间戳：优先字段 21，其次元数据节点里的任意整数，最后全树搜索。
	validTS := func(n int64) bool { _, ok := normalizeTimestamp(n); return ok }
	if v, ok := intField(meta, fieldTimestamp, validTS); ok {
		info.UnixTimestamp, info.TimestampSource = normalizeOrZero(v), "field"
	} else {
		found := false
		if meta != nil {
			if v, ok := firstInt([]*protoMessage{meta}, validTS); ok {
				info.UnixTimestamp, info.TimestampSource = normalizeOrZero(v), "meta"
				found = true
			}
		}
		if !found {
			if v, ok := firstInt(ordered, validTS); ok {
				info.UnixTimestamp, info.TimestampSource = normalizeOrZero(v), "search"
			}
		}
	}
	if info.UnixTimestamp > 0 {
		t := time.Unix(info.UnixTimestamp, 0)
		info.SignedAtUTC = t.UTC().Format("2006-01-02 15:04:05 UTC")
		info.SignedAtUTC8 = t.In(time.FixedZone("UTC+8", 8*3600)).Format("2006-01-02 15:04:05")
	}

	info.Recognized = info.Model != "" || info.EmbeddedBlockType != "" || info.UUID != "" || info.UnixTimestamp > 0
	switch {
	case info.Model != "" || info.UUID != "" || info.UnixTimestamp > 0:
		info.Layout = LayoutFull
	case info.Recognized:
		info.Layout = LayoutMinimal
	default:
		info.Layout = LayoutUnknown
	}
	return info, nil
}

func normalizeOrZero(v int64) int64 {
	n, ok := normalizeTimestamp(v)
	if !ok {
		return 0
	}
	return n
}

func compactBase64(signature, label string) (string, error) {
	text := strings.TrimSpace(signature)
	if text == "" {
		return "", fmt.Errorf("%s 为空", label)
	}
	if strings.HasPrefix(strings.ToLower(text), "data:") {
		if i := strings.IndexByte(text, ','); i >= 0 {
			text = text[i+1:]
		}
	}
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		if r == ' ' || r == '\t' || r == '\r' || r == '\n' {
			continue
		}
		b.WriteRune(r)
	}
	compact := b.String()
	if compact == "" {
		return "", fmt.Errorf("%s 为空", label)
	}
	if !b64Re.MatchString(compact) {
		return "", fmt.Errorf("%s 含 base64 字母表之外的字符", label)
	}
	return compact, nil
}

func prefix(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func sortedKeys(m map[int][]protoValue) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
