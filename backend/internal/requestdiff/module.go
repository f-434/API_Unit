package requestdiff

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"apiunit/internal/engine"
	"apiunit/internal/model"
	"apiunit/internal/modules/modcommon"
	"apiunit/internal/modules/tokenaudit"
	"apiunit/internal/protocol"
	"apiunit/internal/store"
	"apiunit/internal/tokens"
)

type Case struct {
	ID      string               `json:"id"`
	Name    string               `json:"name"`
	Notes   string               `json:"notes,omitempty"`
	Request protocol.ChatRequest `json:"request"`
}

type Config struct {
	Targets            []tokenaudit.TargetSpec `json:"targets"`
	Cases              []Case                  `json:"cases"`
	Repetitions        int                     `json:"repetitions"`
	Concurrency        int                     `json:"concurrency"`
	TimeoutMs          int                     `json:"timeoutMs"`
	MaxOutputTokens    int                     `json:"maxOutputTokens"`
	Stream             bool                    `json:"stream"`
	AnomalyThreshold   float64                 `json:"anomalyThresholdPct"`
	Baseline           string                  `json:"baseline"`
	CheckLocal         bool                    `json:"checkLocal"`
	Retry              engine.RetryPolicy      `json:"retry"`
}

type Module struct {
	store *store.Store
}

func New(st *store.Store) *Module { return &Module{store: st} }

func (m *Module) Kind() model.RunKind { return model.KindRequestDiff }
func (m *Module) Title() string       { return "请求差异审计" }

func (m *Module) Validate(raw json.RawMessage) (any, error) {
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("配置解析失败: %w", err)
	}
	if len(c.Targets) == 0 {
		return nil, fmt.Errorf("请至少选择一个对比目标")
	}
	if len(c.Targets) > 12 {
		return nil, fmt.Errorf("对比目标不能超过 12 个")
	}
	for i := range c.Targets {
		c.Targets[i].TargetID = strings.TrimSpace(c.Targets[i].TargetID)
		c.Targets[i].Model = strings.TrimSpace(c.Targets[i].Model)
		if c.Targets[i].TargetID == "" || c.Targets[i].Model == "" {
			return nil, fmt.Errorf("第 %d 个目标缺少上游或模型", i+1)
		}
		c.Targets[i].Protocol = string(modcommon.NormProtocol(c.Targets[i].Protocol))
		if c.Targets[i].Tokenizer == "" {
			c.Targets[i].Tokenizer = string(tokens.StrategyAuto)
		}
		if c.Targets[i].Label == "" {
			c.Targets[i].Label = fmt.Sprintf("目标 %d", i+1)
		}
	}
	if len(c.Cases) == 0 {
		return nil, fmt.Errorf("请至少创建一个请求用例")
	}
	if len(c.Cases) > 50 {
		return nil, fmt.Errorf("请求用例不能超过 50 个")
	}
	seen := map[string]bool{}
	for i := range c.Cases {
		cs := &c.Cases[i]
		cs.ID = strings.TrimSpace(cs.ID)
		if cs.ID == "" {
			cs.ID = fmt.Sprintf("case_%d", i+1)
		}
		if seen[cs.ID] {
			return nil, fmt.Errorf("用例 ID 重复: %s", cs.ID)
		}
		seen[cs.ID] = true
		if strings.TrimSpace(cs.Name) == "" {
			cs.Name = cs.ID
		}
		if len(cs.Request.Messages) == 0 {
			return nil, fmt.Errorf("用例 %s 没有任何消息", cs.Name)
		}
		hasUser := false
		for _, msg := range cs.Request.Messages {
			if strings.EqualFold(msg.Role, "user") {
				hasUser = true
			}
		}
		if !hasUser {
			return nil, fmt.Errorf("用例 %s 缺少 user 消息", cs.Name)
		}
	}
	if c.Repetitions <= 0 {
		c.Repetitions = 1
	}
	if c.Repetitions > 20 {
		return nil, fmt.Errorf("重复次数不能超过 20")
	}
	if c.Concurrency <= 0 {
		c.Concurrency = 4
	}
	if c.Concurrency > 32 {
		return nil, fmt.Errorf("并发不能超过 32")
	}
	if c.TimeoutMs <= 0 {
		c.TimeoutMs = 120000
	}
	if c.MaxOutputTokens < 0 || c.MaxOutputTokens > 8192 {
		return nil, fmt.Errorf("输出 Token 上限不能超过 8192")
	}
	if c.AnomalyThreshold <= 0 {
		c.AnomalyThreshold = 10
	}
	if c.AnomalyThreshold > 100 {
		return nil, fmt.Errorf("异常阈值不能超过 100%%")
	}
	if c.Baseline == "" {
		c.Baseline = "min"
	}
	if c.Baseline != "min" && c.Baseline != "median" {
		return nil, fmt.Errorf("baseline 只能是 min 或 median")
	}
	if int64(len(c.Targets))*int64(len(c.Cases))*int64(c.Repetitions) > 100000 {
		return nil, fmt.Errorf("总请求数过大（上限 100000）")
	}
	return &c, nil
}

type snapshotBuilder struct {
	mu   sync.Mutex
	list []model.Snapshot
}

func (b *snapshotBuilder) add(s model.Snapshot) {
	b.mu.Lock()
	b.list = append(b.list, s)
	b.mu.Unlock()
}

func (b *snapshotBuilder) all() []model.Snapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]model.Snapshot(nil), b.list...)
}

func (m *Module) Run(ctx context.Context, sess *engine.Session, cfgAny any) error {
	cfg := cfgAny.(*Config)
	resolved := make([]protocol.TargetInfo, len(cfg.Targets))
	for i, t := range cfg.Targets {
		info, err := modcommon.ResolveTarget(m.store, t.TargetID)
		if err != nil {
			return fmt.Errorf("%s: %w", t.Label, err)
		}
		resolved[i] = info
	}
	prompts := make([]model.Prompt, len(cfg.Cases))
	for i, cs := range cfg.Cases {
		prompts[i] = model.Prompt{ID: cs.ID, Name: cs.Name, Content: lastUserText(cs.Request.Messages)}
	}
	sess.SetPlanned(int64(len(cfg.Targets)) * int64(len(cfg.Cases)) * int64(cfg.Repetitions))
	acc := tokenaudit.NewAccumulator()
	sb := &snapshotBuilder{}

	type job struct {
		ti  int
		ci  int
		rep int
	}
	jobs := make(chan job, 64)
	var wg sync.WaitGroup
	for w := 0; w < cfg.Concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				if ctx.Err() != nil {
					return
				}
				spec := cfg.Targets[j.ti]
				target := resolved[j.ti]
				cs := cfg.Cases[j.ci]
				req := cloneRequest(cs.Request)
				req.Model = spec.Model
				req.Stream = cfg.Stream
				if cfg.MaxOutputTokens > 0 {
					req.MaxTokens = cfg.MaxOutputTokens
				}
				if req.MaxTokens <= 0 {
					req.MaxTokens = 32
				}
				sess.SetCurrentModel(spec.Model)
				sess.SetPhase(fmt.Sprintf("请求审计 %s · %s", spec.Label, cs.Name))
				post := func(_ context.Context, it *model.Item) {
					snap := BuildSnapshot(sess.RunID, string(model.KindRequestDiff), *it, target, spec.Label, req)
					sb.add(snap)
					acc.Add(j.ti, cs.ID, *it)
				}
				sess.Exec(ctx, engine.ExecRequest{
					Target:       target,
					Model:        spec.Model,
					Protocol:     modcommon.NormProtocol(spec.Protocol),
					Messages:     req.Messages,
					MaxTokens:    req.MaxTokens,
					Stream:       cfg.Stream,
					Timeout:      time.Duration(cfg.TimeoutMs) * time.Millisecond,
					Retry:        cfg.Retry,
					Capture:      true,
					TokenizerStrategy: tokens.Strategy(spec.Tokenizer),
					LocalPrompt:  true,
					CaseID:       cs.ID,
					CaseName:     cs.Name,
					Repetition:   j.rep,
					Meta: map[string]any{
						"targetIndex": j.ti,
						"targetLabel": spec.Label,
						"repetition":  j.rep,
					},
					RequestOverride: &req,
					PostProcess:     post,
				})
			}
		}()
	}

feed:
	for ti := range cfg.Targets {
		for ci := range cfg.Cases {
			for rep := 1; rep <= cfg.Repetitions; rep++ {
				select {
				case jobs <- job{ti: ti, ci: ci, rep: rep}:
				case <-ctx.Done():
					break feed
				}
			}
		}
	}
	close(jobs)
	wg.Wait()

	snaps := sb.all()
	var warnings []string
	if err := m.store.InsertSnapshots(snaps); err != nil {
		warnings = append(warnings, "请求快照保存失败: "+err.Error())
	}
	refs := make([]tokenaudit.TargetRef, len(cfg.Targets))
	for i, t := range cfg.Targets {
		refs[i] = tokenaudit.TargetRef{TargetID: t.TargetID, Label: t.Label, Model: t.Model, Protocol: t.Protocol}
	}
	audit := tokenaudit.BuildReport(cfg.AnomalyThreshold, cfg.Baseline, refs, prompts, acc)
	report := buildReport(cfg, audit, snaps, warnings)
	sess.SetExtra("requestDiffReport", report)
	return ctx.Err()
}

func cloneRequest(r protocol.ChatRequest) protocol.ChatRequest {
	// Shallow copy is sufficient: the module never mutates nested values.
	return r
}

func lastUserText(msgs []protocol.ChatMessage) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if strings.EqualFold(msgs[i].Role, "user") {
			return protocol.MessageText(msgs[i])
		}
	}
	return ""
}

type CaseInfo struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	Notes               string `json:"notes,omitempty"`
	HasHistory          bool   `json:"hasHistory"`
	HasTools            bool   `json:"hasTools"`
	HasMedia            bool   `json:"hasMedia"`
	HasWebSearch        bool   `json:"hasWebSearch"`
	HasFunctionResponse bool   `json:"hasFunctionResponse"`
	MessageCount        int    `json:"messageCount"`
}

type Report struct {
	Audit              *tokenaudit.Report  `json:"audit"`
	Targets            []tokenaudit.TargetRef `json:"targets"`
	Cases              []CaseInfo          `json:"cases"`
	SnapshotCount      int                 `json:"snapshotCount"`
	FindingCount       int                 `json:"findingCount"`
	FindingsByCategory map[string]int      `json:"findingsByCategory"`
	FindingsBySeverity map[string]int      `json:"findingsBySeverity"`
	Warnings           []string            `json:"warnings,omitempty"`
	Notes              []string            `json:"notes"`
}

func buildReport(cfg *Config, audit *tokenaudit.Report, snaps []model.Snapshot, warnings []string) *Report {
	refs := make([]tokenaudit.TargetRef, len(cfg.Targets))
	for i, t := range cfg.Targets {
		refs[i] = tokenaudit.TargetRef{TargetID: t.TargetID, Label: t.Label, Model: t.Model, Protocol: t.Protocol}
	}
	rep := &Report{
		Audit: audit, Targets: refs, SnapshotCount: len(snaps), Warnings: warnings,
		FindingsByCategory: map[string]int{}, FindingsBySeverity: map[string]int{},
	}
	for _, cs := range cfg.Cases {
		sections := BuildSections(cs.Request)
		rep.Cases = append(rep.Cases, CaseInfo{
			ID: cs.ID, Name: cs.Name, Notes: cs.Notes,
			HasHistory:          len(sections.History) > 0,
			HasTools:            len(cs.Request.Tools) > 0 || len(cs.Request.BuiltinTools) > 0,
			HasMedia:            len(sections.Media) > 0,
			HasWebSearch:        len(sections.WebSearch) > 0,
			HasFunctionResponse: len(sections.FunctionResponse) > 0,
			MessageCount:        len(cs.Request.Messages),
		})
	}
	for _, sn := range snaps {
		for _, f := range sn.Findings {
			rep.FindingCount++
			rep.FindingsByCategory[f.Category]++
			rep.FindingsBySeverity[f.Severity]++
		}
	}
	rep.Notes = []string{
		"快照保存的是本工具实际发出的完整请求正文，并同时保存协议无关的 canonical 请求与 SHA-256 指纹。",
		"差异对照支持 wire（协议原生字段，如 systemInstruction/contents/tools/generationConfig）与 canonical 两种模式。",
		"不同上游/协议会对同一逻辑请求产生不同的 JSON 结构，跨协议对比建议使用 canonical 模式。",
		"Token 统计完全复用 Token 审计模块，不重复实现。",
		"本系统独立运行，不修改也不依赖 New API 源码；快照反映的是本工具发出的请求，中转层若在服务端改写请求，需要结合网关日志另行核对。",
	}
	return rep
}

func (m *Module) Summary(sess *engine.Session) model.RunSummary {
	base := sess.Headline()
	if v := sess.GetExtra("requestDiffReport"); v != nil {
		if rep, ok := v.(*Report); ok {
			base.Data = rep
			if rep.Audit != nil && len(rep.Audit.Targets) > 0 {
				models := make([]string, 0, len(rep.Audit.Targets))
				for _, t := range rep.Audit.Targets {
					models = append(models, t.Model)
				}
				base.Models = models
			}
		}
	}
	if sess.StopRequested() && base.Planned > base.Total {
		base.Canceled = base.Planned - base.Total
	}
	return base
}
