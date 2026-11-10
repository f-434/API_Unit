package tokenaudit

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"apiunit/internal/engine"
	"apiunit/internal/model"
	"apiunit/internal/modules/modcommon"
	"apiunit/internal/protocol"
	"apiunit/internal/store"
	"apiunit/internal/tokens"
)

type TargetSpec struct {
	TargetID   string `json:"targetId"`
	Model      string `json:"model"`
	Protocol   string `json:"protocol,omitempty"`
	Tokenizer  string `json:"tokenizer,omitempty"`
	Label      string `json:"label,omitempty"`
}

type Config struct {
	Targets           []TargetSpec        `json:"targets"`
	PromptIDs         []string            `json:"promptIds"`
	Repetitions       int                 `json:"repetitions"`
	MaxOutputTokens   int                 `json:"maxOutputTokens"`
	Temperature       *float64            `json:"temperature"`
	Stream            bool                `json:"stream"`
	Concurrency       int                 `json:"concurrency"`
	TimeoutMs         int                 `json:"timeoutMs"`
	AnomalyThreshold  float64             `json:"anomalyThresholdPct"`
	Baseline          string              `json:"baseline"`
	CheckLocal        bool                `json:"checkLocal"`
	Retry             engine.RetryPolicy  `json:"retry"`
}

type Module struct {
	store *store.Store
}

func New(st *store.Store) *Module { return &Module{store: st} }

func (m *Module) Kind() model.RunKind { return model.KindTokenAudit }
func (m *Module) Title() string       { return "Token 审计 / 注入检测" }

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
	if len(c.PromptIDs) == 0 {
		return nil, fmt.Errorf("请至少选择一个审计提示词")
	}
	if len(c.PromptIDs) > 100 {
		return nil, fmt.Errorf("提示词不能超过 100 个")
	}
	if c.Repetitions <= 0 {
		c.Repetitions = 3
	}
	if c.Repetitions > 20 {
		return nil, fmt.Errorf("重复次数不能超过 20")
	}
	if c.MaxOutputTokens <= 0 {
		c.MaxOutputTokens = 8
	}
	if c.MaxOutputTokens > 4096 {
		return nil, fmt.Errorf("输出 Token 上限不能超过 4096")
	}
	if c.Temperature == nil {
		zero := 0.0
		c.Temperature = &zero
	}
	if *c.Temperature < 0 || *c.Temperature > 2 {
		return nil, fmt.Errorf("temperature 必须在 0-2 之间")
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
	if int64(len(c.Targets))*int64(len(c.PromptIDs))*int64(c.Repetitions) > 100000 {
		return nil, fmt.Errorf("总请求数过大（上限 100000）")
	}
	return &c, nil
}

type cellKey struct {
	Target int
	Prompt string
}

type cellAcc struct {
	inputs      []float64
	local       int
	localSource string
	count       int
	success     int
	errors      int
	missing     int
	cost        float64
	latSum      float64
	latN        int
}

type Accumulator struct {
	mu    sync.Mutex
	cells map[cellKey]*cellAcc
}

func NewAccumulator() *Accumulator { return &Accumulator{cells: map[cellKey]*cellAcc{}} }

func (b *Accumulator) Add(ti int, promptID string, it model.Item) {
	b.mu.Lock()
	defer b.mu.Unlock()
	key := cellKey{Target: ti, Prompt: promptID}
	c := b.cells[key]
	if c == nil {
		c = &cellAcc{}
		b.cells[key] = c
	}
	c.count++
	if it.OK {
		c.success++
		fromUsage, _ := it.Meta["promptUsage"].(bool)
		if fromUsage && it.PromptTokens > 0 {
			c.inputs = append(c.inputs, float64(it.PromptTokens))
		} else {
			c.missing++
		}
	} else {
		c.errors++
	}
	if it.LocalPromptTokens > 0 && c.local == 0 {
		c.local = it.LocalPromptTokens
		if src, ok := it.Meta["localSource"].(string); ok {
			c.localSource = src
		} else {
			c.localSource = it.TokenSource
		}
	}
	c.cost += it.Cost
	if it.LatencyMs > 0 {
		c.latSum += it.LatencyMs
		c.latN++
	}
}

func (m *Module) Run(ctx context.Context, sess *engine.Session, cfgAny any) error {
	cfg := cfgAny.(*Config)
	prompts, err := m.store.GetPrompts(cfg.PromptIDs)
	if err != nil {
		return err
	}
	if len(prompts) == 0 {
		return fmt.Errorf("没有找到可用的提示词")
	}
	resolved := make([]protocol.TargetInfo, len(cfg.Targets))
	for i, t := range cfg.Targets {
		info, err := modcommon.ResolveTarget(m.store, t.TargetID)
		if err != nil {
			return fmt.Errorf("%s: %w", t.Label, err)
		}
		resolved[i] = info
	}
	sess.SetPlanned(int64(len(cfg.Targets)) * int64(len(prompts)) * int64(cfg.Repetitions))
	b := NewAccumulator()

	type job struct {
		ti int
		p  model.Prompt
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
				msgs := []protocol.ChatMessage{{Role: "user", Content: j.p.Content}}
				sess.SetCurrentModel(spec.Model)
				sess.SetPhase(fmt.Sprintf("审计 %s · %s", spec.Label, j.p.Name))
				it, ok := sess.Exec(ctx, engine.ExecRequest{
					Target:       resolved[j.ti],
					Model:        spec.Model,
					Protocol:     modcommon.NormProtocol(spec.Protocol),
					Messages:     msgs,
					MaxTokens:    cfg.MaxOutputTokens,
					Temperature:  cfg.Temperature,
					Stream:       cfg.Stream,
					Timeout:      time.Duration(cfg.TimeoutMs) * time.Millisecond,
					Retry:        cfg.Retry,
					Capture:      true,
					TokenizerStrategy: tokens.Strategy(spec.Tokenizer),
					LocalPrompt:  true,
					CaseID:       j.p.ID,
					CaseName:     j.p.Name,
					Repetition:   j.rep,
					Meta: map[string]any{
						"targetIndex": j.ti,
						"targetLabel": spec.Label,
						"repetition":  j.rep,
					},
				})
				if !ok {
					continue
				}
				b.Add(j.ti, j.p.ID, it)
			}
		}()
	}

feed:
	for ti, spec := range cfg.Targets {
		_ = spec
		for _, p := range prompts {
			for rep := 1; rep <= cfg.Repetitions; rep++ {
				select {
				case jobs <- job{ti: ti, p: p, rep: rep}:
				case <-ctx.Done():
					break feed
				}
			}
		}
	}
	close(jobs)
	wg.Wait()

	refs := make([]TargetRef, len(cfg.Targets))
	for i, t := range cfg.Targets {
		refs[i] = TargetRef{TargetID: t.TargetID, Label: t.Label, Model: t.Model, Protocol: t.Protocol}
	}
	report := BuildReport(cfg.AnomalyThreshold, cfg.Baseline, refs, prompts, b)
	sess.SetExtra("auditReport", report)
	return ctx.Err()
}

type Cell struct {
	TargetIndex   int      `json:"targetIndex"`
	TargetID      string   `json:"targetId"`
	TargetLabel   string   `json:"targetLabel"`
	Model         string   `json:"model"`
	Protocol      string   `json:"protocol"`
	PromptID      string   `json:"promptId"`
	PromptName    string   `json:"promptName"`
	Count         int      `json:"count"`
	Success       int      `json:"success"`
	Errors        int      `json:"errors"`
	MissingUsage  int      `json:"missingUsage"`
	MinInput      float64  `json:"minInput"`
	MedianInput   float64  `json:"medianInput"`
	MaxInput      float64  `json:"maxInput"`
	AvgInput      float64  `json:"avgInput"`
	LocalTokens   int      `json:"localTokens"`
	LocalSource   string   `json:"localSource,omitempty"`
	Overhead      float64  `json:"overhead"`
	OverheadPct   float64  `json:"overheadPct"`
	SpreadPct     float64  `json:"spreadPct"`
	DeltaPct      float64  `json:"deltaPct"`
	Status        string   `json:"status"`
	Reasons       []string `json:"reasons,omitempty"`
	Cost          float64  `json:"cost"`
	AvgLatencyMs  float64  `json:"avgLatencyMs"`
}

type PromptRow struct {
	PromptID      string  `json:"promptId"`
	PromptName    string  `json:"promptName"`
	BaselineInput float64 `json:"baselineInput"`
	Cells         []Cell  `json:"cells"`
}

type TargetAgg struct {
	TargetIndex  int     `json:"targetIndex"`
	TargetID     string  `json:"targetId"`
	TargetLabel  string  `json:"targetLabel"`
	Model        string  `json:"model"`
	Protocol     string  `json:"protocol"`
	Count        int     `json:"count"`
	Success      int     `json:"success"`
	Errors       int     `json:"errors"`
	MedianInput  float64 `json:"medianInput"`
	AvgInput     float64 `json:"avgInput"`
	LocalTokens  float64 `json:"localTokens"`
	OverheadPct  float64 `json:"overheadPct"`
	Anomalies    int     `json:"anomalies"`
	Cost         float64 `json:"cost"`
}

type Anomaly struct {
	PromptID    string  `json:"promptId"`
	PromptName  string  `json:"promptName"`
	TargetLabel string  `json:"targetLabel"`
	Model       string  `json:"model"`
	Status      string  `json:"status"`
	DeltaPct    float64 `json:"deltaPct"`
	SpreadPct   float64 `json:"spreadPct"`
	OverheadPct float64 `json:"overheadPct"`
	Reasons     []string `json:"reasons"`
}

type Report struct {
	Threshold       float64     `json:"threshold"`
	BaselineMethod  string      `json:"baselineMethod"`
	TotalRequests   int         `json:"totalRequests"`
	Success         int         `json:"success"`
	Errors          int         `json:"errors"`
	MissingUsage    int         `json:"missingUsage"`
	LocalComparable int         `json:"localComparable"`
	Targets         []TargetAgg `json:"targets"`
	Prompts         []PromptRow `json:"prompts"`
	Anomalies       []Anomaly   `json:"anomalies"`
	Notes           []string    `json:"notes"`
}

// TargetRef is the protocol-independent identity of one comparison target.
type TargetRef struct {
	TargetID string `json:"targetId"`
	Label    string `json:"label"`
	Model    string `json:"model"`
	Protocol string `json:"protocol"`
}

// BuildReport converts raw per-request accumulators into the audit report.
// It is shared with the request-difference module so token statistics are
// implemented only once.
func BuildReport(threshold float64, baseline string, targets []TargetRef, prompts []model.Prompt, b *Accumulator) *Report {
	rep := &Report{
		Threshold:      threshold,
		BaselineMethod: baseline,
		Targets:        make([]TargetAgg, len(targets)),
		Prompts:        make([]PromptRow, 0, len(prompts)),
		Anomalies:      []Anomaly{},
	}
	for i, t := range targets {
		rep.Targets[i] = TargetAgg{
			TargetIndex: i, TargetID: t.TargetID, TargetLabel: t.Label,
			Model: t.Model, Protocol: t.Protocol,
		}
	}
	// target-wide input pools
	pools := make([][]float64, len(targets))

	for _, p := range prompts {
		row := PromptRow{PromptID: p.ID, PromptName: p.Name}
		cells := make([]Cell, 0, len(targets))
		medians := make([]float64, 0, len(targets))
		for ti, t := range targets {
			acc := b.cells[cellKey{Target: ti, Prompt: p.ID}]
			if acc == nil {
				acc = &cellAcc{}
			}
			cell := Cell{
				TargetIndex: ti, TargetID: t.TargetID, TargetLabel: t.Label, Model: t.Model,
				Protocol: t.Protocol, PromptID: p.ID, PromptName: p.Name,
				Count: acc.count, Success: acc.success, Errors: acc.errors, MissingUsage: acc.missing,
				LocalTokens: acc.local, LocalSource: acc.localSource, Cost: acc.cost,
			}
			if acc.latN > 0 {
				cell.AvgLatencyMs = acc.latSum / float64(acc.latN)
			}
			if len(acc.inputs) > 0 {
				sorted := append([]float64(nil), acc.inputs...)
				sort.Float64s(sorted)
				cell.MinInput = sorted[0]
				cell.MaxInput = sorted[len(sorted)-1]
				cell.MedianInput = medianSorted(sorted)
				var sum float64
				for _, v := range sorted {
					sum += v
				}
				cell.AvgInput = sum / float64(len(sorted))
				pools[ti] = append(pools[ti], sorted...)
				medians = append(medians, cell.MedianInput)
				if cell.MinInput > 0 {
					cell.SpreadPct = (cell.MaxInput - cell.MinInput) / cell.MinInput * 100
				}
				comparable := localComparable(t.Model, acc.localSource)
				if comparable && cell.LocalTokens > 0 {
					cell.Overhead = cell.AvgInput - float64(cell.LocalTokens)
					cell.OverheadPct = cell.Overhead / float64(cell.LocalTokens) * 100
					rep.LocalComparable++
				}
			}
			rep.TotalRequests += cell.Count
			rep.Success += cell.Success
			rep.Errors += cell.Errors
			rep.MissingUsage += cell.MissingUsage
			cells = append(cells, cell)
		}
		baseline := baselineOf(medians, baseline)
		row.BaselineInput = baseline
		row.Cells = cells
		for i := range row.Cells {
			if baseline > 0 && row.Cells[i].MedianInput > 0 {
				row.Cells[i].DeltaPct = (row.Cells[i].MedianInput - baseline) / baseline * 100
			}
			row.Cells[i].Status, row.Cells[i].Reasons = classifyCell(&row.Cells[i], rep.Threshold)
			if row.Cells[i].Status != "normal" {
				rep.Anomalies = append(rep.Anomalies, Anomaly{
					PromptID: p.ID, PromptName: p.Name, TargetLabel: row.Cells[i].TargetLabel,
					Model: row.Cells[i].Model, Status: row.Cells[i].Status,
					DeltaPct: row.Cells[i].DeltaPct, SpreadPct: row.Cells[i].SpreadPct,
					OverheadPct: row.Cells[i].OverheadPct, Reasons: row.Cells[i].Reasons,
				})
			}
		}
		rep.Prompts = append(rep.Prompts, row)
	}

	for ti := range targets {
		agg := &rep.Targets[ti]
		sorted := append([]float64(nil), pools[ti]...)
		sort.Float64s(sorted)
		agg.Count = 0
		var localSum float64
		var localN int
		for _, row := range rep.Prompts {
			for _, c := range row.Cells {
				if c.TargetIndex != ti {
					continue
				}
				agg.Count += c.Count
				agg.Success += c.Success
				agg.Errors += c.Errors
				agg.Cost += c.Cost
				if c.Status != "normal" {
					agg.Anomalies++
				}
				if c.LocalTokens > 0 {
					localSum += float64(c.LocalTokens)
					localN++
				}
			}
		}
		if len(sorted) > 0 {
			agg.MedianInput = medianSorted(sorted)
			var sum float64
			for _, v := range sorted {
				sum += v
			}
			agg.AvgInput = sum / float64(len(sorted))
		}
		if localN > 0 {
			agg.LocalTokens = localSum / float64(localN)
			if agg.LocalTokens > 0 {
				agg.OverheadPct = (agg.AvgInput - agg.LocalTokens) / agg.LocalTokens * 100
			}
		}
	}

	rep.Notes = []string{
		"跨模型家族的 tokenizer 天然存在差异（例如 Gemini 与 GPT），Delta 只能横向参考。",
		"判断“注入”应优先看本地分词器偏差（OverheadPct）：OpenAI 模型使用 tiktoken，Gemini 使用 Gemma SentencePiece。",
		"若响应未返回 usage，则该行会标记为缺少 usage，Token 数由本地估算，结论仅供参考。",
	}
	return rep
}

func classifyCell(c *Cell, threshold float64) (string, []string) {
	var reasons []string
	if c.Count == 0 {
		return "error", []string{"没有请求结果"}
	}
	if c.Success == 0 {
		return "error", []string{"全部请求失败"}
	}
	if c.Success == c.MissingUsage && c.MissingUsage > 0 {
		return "no_usage", []string{"响应中缺少 usage.prompt_tokens"}
	}
	status := "normal"
	if c.MissingUsage > 0 {
		reasons = append(reasons, fmt.Sprintf("%d 次响应缺少 usage", c.MissingUsage))
	}
	if c.DeltaPct > threshold {
		status = "inflation"
		reasons = append(reasons, fmt.Sprintf("输入 Token 比基线高 %.1f%%", c.DeltaPct))
	}
	if strings.HasPrefix(c.LocalSource, "tiktoken") || strings.HasPrefix(c.LocalSource, "gemini-local") || c.LocalSource == "openai-online" {
		if c.OverheadPct > threshold {
			status = "inflation"
			reasons = append(reasons, fmt.Sprintf("上报值比本地分词器高 %.1f%%（疑似注入）", c.OverheadPct))
		}
	}
	if c.SpreadPct > threshold && status == "normal" {
		status = "unstable"
		reasons = append(reasons, fmt.Sprintf("同一提示词重复请求波动 %.1f%%", c.SpreadPct))
	}
	if status == "normal" && len(reasons) > 0 {
		status = "warning"
	}
	if len(reasons) == 0 {
		reasons = []string{"正常"}
	}
	return status, reasons
}

func localComparable(modelName, source string) bool {
	fam := modelFamily(modelName)
	switch {
	case strings.HasPrefix(source, "tiktoken"):
		return fam == "openai" || fam == "other"
	case strings.HasPrefix(source, "gemini-local"):
		return fam == "gemini"
	case source == "openai-online":
		return fam == "openai"
	default:
		return false
	}
}

func modelFamily(name string) string {
	m := strings.ToLower(name)
	if i := strings.LastIndexAny(m, "/:"); i >= 0 {
		m = m[i+1:]
	}
	switch {
	case strings.Contains(m, "claude"):
		return "claude"
	case strings.Contains(m, "gemini") || strings.Contains(m, "learnlm"):
		return "gemini"
	case strings.HasPrefix(m, "gpt-") || strings.HasPrefix(m, "o1") || strings.HasPrefix(m, "o3") ||
		strings.HasPrefix(m, "o4") || strings.HasPrefix(m, "chatgpt"):
		return "openai"
	default:
		return "other"
	}
}

func baselineOf(values []float64, method string) float64 {
	if len(values) == 0 {
		return 0
	}
	cp := append([]float64(nil), values...)
	sort.Float64s(cp)
	if method == "median" {
		return medianSorted(cp)
	}
	return cp[0]
}

func medianSorted(sorted []float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

func (m *Module) Summary(sess *engine.Session) model.RunSummary {
	base := sess.Headline()
	if v := sess.GetExtra("auditReport"); v != nil {
		if rep, ok := v.(*Report); ok {
			base.Data = rep
			base.ASR = 0
			if len(rep.Targets) > 0 {
				models := make([]string, 0, len(rep.Targets))
				for _, t := range rep.Targets {
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
