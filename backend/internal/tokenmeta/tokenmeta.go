// Package tokenmeta 维护 Token 口径（家族/平台）的动态树，以及提示词库中
// 按口径手工维护的本地 Token 基准。
package tokenmeta

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"apiunit/internal/model"
	"apiunit/internal/store"
)

// TaxonomyKey 是设置在 settings 表中的键名。
const TaxonomyKey = "token.families"

// nodeKeyRe 限制路径节点只能使用小写字母、数字、下划线和短横线，
// 保证 / 连接的路径可直接作为提示词基准的键。
var nodeKeyRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

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
			if count > 200 {
				return fmt.Errorf("口径节点过多（上限 200）")
			}
			key := strings.TrimSpace(n.Key)
			if !nodeKeyRe.MatchString(key) {
				return fmt.Errorf("标识 %q 只能包含小写字母、数字、下划线和短横线", n.Key)
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
