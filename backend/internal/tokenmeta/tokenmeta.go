// Package tokenmeta 维护 Token 口径（家族/平台）的动态树，以及提示词库中
// 按口径手工维护的本地 Token 基准。
package tokenmeta

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"apiunit/internal/model"
	"apiunit/internal/store"
)

// TaxonomyKey 是设置在 settings 表中的键名。
const TaxonomyKey = "token.families"

// MaxDepth 是口径树的层级上限：家族 → 平台 → 模型。
const MaxDepth = 3

// PlatformDepth 是平台所在层级；渠道只能标注到该层级，模型级由审计请求自动拼接。
const PlatformDepth = 2

// nodeKeyRe 限制路径节点只能使用小写字母、数字、点、下划线、短横线、冒号和 @。
// 允许点是因为模型 ID 常含版本号（如 gemini-2.5-pro）；
// 斜杠会与路径分隔符冲突，统一由 ModelKey 规范为短横线。
var nodeKeyRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._:@-]*$`)

// Node 是 Token 口径树的一个节点。
type Node struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Children []Node `json:"children,omitempty"`
}

// Taxonomy 是口径树的根节点列表。
type Taxonomy []Node

// DefaultTaxonomy 返回内置的口径树。用户可在设置页动态增删。
func DefaultTaxonomy() Taxonomy {
	return Taxonomy{
		{Key: "gemini", Label: "Gemini", Children: []Node{
			{Key: "as", Label: "AI Studio"},
			{Key: "vt", Label: "Vertex AI"},
		}},
		{Key: "gpt", Label: "GPT", Children: []Node{
			{Key: "openai", Label: "OpenAI"},
			{Key: "az", Label: "Azure"},
		}},
		{Key: "claude", Label: "Claude", Children: []Node{
			{Key: "an", Label: "Anthropic"},
			{Key: "awsb", Label: "AWS Bedrock"},
		}},
	}
}

// Load 读取已保存的口径树；未配置或内容损坏时回退内置树。
func Load(st *store.Store) Taxonomy {
	if st == nil {
		return DefaultTaxonomy()
	}
	raw := strings.TrimSpace(st.GetSetting(TaxonomyKey, ""))
	if raw == "" {
		return DefaultTaxonomy()
	}
	var t Taxonomy
	if err := json.Unmarshal([]byte(raw), &t); err != nil || t.Validate() != nil {
		return DefaultTaxonomy()
	}
	return t
}

// Save 校验并保存口径树，首尾空白会被清理。
func Save(st *store.Store, t Taxonomy) error {
	t = normalizeTaxonomy(t)
	if err := t.Validate(); err != nil {
		return err
	}
	if err := t.ValidateDepth(); err != nil {
		return err
	}
	raw, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return st.SetSetting(TaxonomyKey, string(raw))
}

func normalizeTaxonomy(nodes []Node) Taxonomy {
	out := make(Taxonomy, len(nodes))
	for i, n := range nodes {
		out[i] = Node{Key: strings.TrimSpace(n.Key), Label: strings.TrimSpace(n.Label)}
		if len(n.Children) > 0 {
			out[i].Children = normalizeTaxonomy(n.Children)
		}
	}
	return out
}

// Validate 检查标识、名称是否合法，以及同级标识是否重复。
// 为兼容历史数据，读取时不限制层级；新增保存由 ValidateDepth 把关。
func (t Taxonomy) Validate() error {
	if len(t) == 0 {
		return fmt.Errorf("至少保留一个根节点")
	}
	count := 0
	var walk func(nodes []Node, prefix string) error
	walk = func(nodes []Node, prefix string) error {
		seen := map[string]bool{}
		for _, n := range nodes {
			count++
			if count > 500 {
				return fmt.Errorf("口径节点过多（上限 500）")
			}
			key := strings.TrimSpace(n.Key)
			if !nodeKeyRe.MatchString(key) {
				return fmt.Errorf("标识 %q 只能包含小写字母、数字、点、下划线、短横线、冒号和 @", n.Key)
			}
			if seen[key] {
				return fmt.Errorf("同级标识重复：%s%s", prefix, key)
			}
			seen[key] = true
			if strings.TrimSpace(n.Label) == "" {
				return fmt.Errorf("节点 %s 缺少名称", prefix+key)
			}
			if err := walk(n.Children, prefix+key+"/"); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(t, "")
}

// ValidateDepth 检查口径树不超过“家族 → 平台 → 模型”三级。
func (t Taxonomy) ValidateDepth() error {
	var walk func(nodes []Node, depth int) bool
	walk = func(nodes []Node, depth int) bool {
		if len(nodes) == 0 {
			return true
		}
		if depth > MaxDepth {
			return false
		}
		for _, n := range nodes {
			if !walk(n.Children, depth+1) {
				return false
			}
		}
		return true
	}
	if !walk(t, 1) {
		return fmt.Errorf("口径最多 %d 级：家族 → 平台 → 模型", MaxDepth)
	}
	return nil
}

// Paths 返回所有节点的完整路径，例如 gemini、gemini/vt。
func (t Taxonomy) Paths() []string {
	out := make([]string, 0, len(t))
	var walk func(nodes []Node, prefix string)
	walk = func(nodes []Node, prefix string) {
		for _, n := range nodes {
			p := n.Key
			if prefix != "" {
				p = prefix + "/" + n.Key
			}
			out = append(out, p)
			walk(n.Children, p)
		}
	}
	walk(t, "")
	return out
}

// HasPath 判断路径是否对应树中的真实节点。
func (t Taxonomy) HasPath(path string) bool {
	path = strings.Trim(strings.TrimSpace(path), "/")
	if path == "" {
		return false
	}
	for _, p := range t.Paths() {
		if p == path {
			return true
		}
	}
	return false
}

// PathDepth 返回路径层级；空路径为 0。
func PathDepth(path string) int {
	path = strings.Trim(strings.TrimSpace(path), "/")
	if path == "" {
		return 0
	}
	return len(strings.Split(path, "/"))
}

// ModelKey 把模型 ID 规范为口径路径的末级标识：
// 转小写、去掉 Gemini 的 models/ 前缀、把非法字符折叠成短横线。
// 审计请求与提示词基准都经同一规则，保证末级可以直接比对。
func ModelKey(modelName string) string {
	s := strings.ToLower(strings.TrimSpace(modelName))
	s = strings.TrimPrefix(s, "models/")
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') ||
			r == '.' || r == '_' || r == '-' || r == ':' || r == '@'
		if ok {
			b.WriteRune(r)
			lastDash = r == '-'
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// JoinModel 在平台级口径后拼接模型级，形成审计实际使用的末级路径。
// 只有两级（家族/平台）的口径才拼模型；家族级或空口径保持原样。
func JoinModel(familyPath, modelName string) string {
	family := strings.Trim(strings.TrimSpace(familyPath), "/")
	if family == "" {
		return ""
	}
	if PathDepth(family) != PlatformDepth {
		return family
	}
	key := ModelKey(modelName)
	if key == "" {
		return family
	}
	return family + "/" + key
}

// ResolveForModel 从模型级开始逐级向上匹配手工基准。
func ResolveForModel(p model.Prompt, familyPath, modelName string) (tokens int, matched string, ok bool) {
	return ResolveOverride(p, JoinModel(familyPath, modelName))
}

// AcceptsPath 判断路径能否作为提示词基准的键：
// 命中口径树节点，或恰好是平台节点下的模型级路径（模型可由回填自动登记）。
func (t Taxonomy) AcceptsPath(path string) bool {
	path = strings.Trim(strings.TrimSpace(path), "/")
	if path == "" {
		return false
	}
	if t.HasPath(path) {
		return true
	}
	parts := strings.Split(path, "/")
	if len(parts) != MaxDepth || !nodeKeyRe.MatchString(parts[len(parts)-1]) {
		return false
	}
	return t.HasPath(strings.Join(parts[:len(parts)-1], "/"))
}

// taxonomyMu 保护“读取-修改-写回”的口径树更新。
var taxonomyMu sync.Mutex

// EnsureModelPath 把模型级路径挂到口径树上，便于设置页直接维护。
// 只在父级平台存在时追加；失败不影响调用方的主流程。
func EnsureModelPath(st *store.Store, path string) error {
	if st == nil {
		return nil
	}
	path = strings.Trim(strings.TrimSpace(path), "/")
	parts := strings.Split(path, "/")
	if len(parts) != MaxDepth || !nodeKeyRe.MatchString(parts[len(parts)-1]) {
		return nil
	}

	taxonomyMu.Lock()
	defer taxonomyMu.Unlock()

	tax := Load(st)
	if tax.HasPath(path) {
		return nil
	}
	parent := strings.Join(parts[:len(parts)-1], "/")
	if !tax.HasPath(parent) {
		return nil
	}
	return Save(st, attachModelNode(tax, parts[0], parts[1], parts[2]))
}

// attachModelNode 返回追加模型节点后的新树，不修改入参。
func attachModelNode(nodes []Node, family, platform, modelKey string) Taxonomy {
	out := make(Taxonomy, len(nodes))
	for i, n := range nodes {
		out[i] = n
		if n.Key != family {
			continue
		}
		children := make([]Node, len(n.Children))
		copy(children, n.Children)
		for j := range children {
			if children[j].Key != platform {
				continue
			}
			grand := append([]Node(nil), children[j].Children...)
			children[j].Children = append(grand, Node{Key: modelKey, Label: modelKey})
		}
		out[i].Children = children
	}
	return out
}

// NormalizeOverrides 丢弃非法或非正数的基准，并统一路径写法。
func NormalizeOverrides(in map[string]int) map[string]int {
	out := map[string]int{}
	for k, v := range in {
		path := strings.Trim(strings.TrimSpace(k), "/")
		if path == "" || v <= 0 {
			continue
		}
		out[path] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// OverridesActive 判断手工基准是否仍然适用于当前内容。
func OverridesActive(p model.Prompt) bool {
	return len(p.TokenOverrides) > 0 &&
		p.TokenOverridesHash != "" &&
		p.TokenOverridesHash == model.HashText(p.Content)
}

// ResolveOverride 按“完整路径 → 逐级父路径”的顺序匹配手工基准。
func ResolveOverride(p model.Prompt, familyPath string) (tokens int, matched string, ok bool) {
	if !OverridesActive(p) {
		return 0, "", false
	}
	parts := strings.Split(strings.Trim(strings.TrimSpace(familyPath), "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return 0, "", false
	}
	for i := len(parts); i >= 1; i-- {
		key := strings.Join(parts[:i], "/")
		if v := p.TokenOverrides[key]; v > 0 {
			return v, key, true
		}
	}
	return 0, "", false
}

// OverridesEqual 判断两份基准是否完全一致，用于识别用户是否主动重新校准。
func OverridesEqual(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
