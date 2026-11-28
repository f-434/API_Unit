package claudesignature

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"apiunit/internal/engine"
	"apiunit/internal/model"
	"apiunit/internal/protocol"
	"apiunit/internal/store"
)

// Module 实现 engine.Module，注册为 claude_signature。
type Module struct {
	store *store.Store
}

func New(st *store.Store) *Module { return &Module{store: st} }

func (m *Module) Kind() model.RunKind { return model.KindClaudeSignature }
func (m *Module) Title() string       { return "signature 签名核验" }

func (m *Module) Validate(raw json.RawMessage) (any, error) {
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("配置解析失败: %w", err)
	}
	if len(c.Targets) == 0 {
		return nil, fmt.Errorf("请至少选择一个上游目标")
	}
	if len(c.Targets) > 8 {
		return nil, fmt.Errorf("上游目标不能超过 8 个")
	}
	if c.Count <= 0 {
		c.Count = 5
	}
	if c.Count > 100 {
		return nil, fmt.Errorf("每个模型的请求次数不能超过 100")
	}
	if c.Concurrency <= 0 {
		c.Concurrency = 2
	}
	if c.Concurrency > 16 {
		return nil, fmt.Errorf("模型并发不能超过 16")
	}
	if c.DelayMs < 0 {
		c.DelayMs = 0
	}
	if c.DelayMs > 60000 {
		return nil, fmt.Errorf("请求间隔不能超过 60 秒")
	}
	if c.TimeoutMs <= 0 {
		c.TimeoutMs = 180000
	}
	if c.TimeoutMs < 5000 || c.TimeoutMs > 900000 {
		return nil, fmt.Errorf("单请求超时需在 5 到 900 秒之间")
	}
	if c.MaxTokens <= 0 {
		c.MaxTokens = 16000
	}
	c.Thinking = strings.ToLower(strings.TrimSpace(c.Thinking))
	switch c.Thinking {
	case "":
		c.Thinking = "auto"
	case "auto", "adaptive", "budget", "off":
	default:
		return nil, fmt.Errorf("thinking 模式无效: %s", c.Thinking)
	}
	if c.Thinking != "off" && c.MaxTokens < 1025 {
		return nil, fmt.Errorf("启用 thinking 时 max_tokens 不能小于 1025")
	}
	if c.MaxTokens < 256 || c.MaxTokens > 128000 {
		return nil, fmt.Errorf("max_tokens 需在 256 到 128000 之间")
	}
	if c.BudgetTokens <= 0 {
		c.BudgetTokens = 4096
	}
	if c.BudgetTokens < 1024 {
		return nil, fmt.Errorf("budget_tokens 不能小于 1024")
	}
	if c.BudgetTokens >= c.MaxTokens {
		c.BudgetTokens = c.MaxTokens - 1
	}
	if c.BudgetTokens < 1024 {
		return nil, fmt.Errorf("max_tokens 过小，无法分配 thinking 预算")
	}
	c.Effort = strings.ToLower(strings.TrimSpace(c.Effort))
	switch c.Effort {
	case "", "low", "medium", "high", "xhigh", "max":
	default:
		return nil, fmt.Errorf("effort 无效: %s", c.Effort)
	}
	c.Prompt = strings.TrimSpace(c.Prompt)
	if len([]rune(c.Prompt)) > 4000 {
		return nil, fmt.Errorf("自定义提示词不能超过 4000 字")
	}
	if c.Retry.MaxRetries < 0 {
		c.Retry.MaxRetries = 0
	}
	if c.Retry.MaxRetries > 5 {
		return nil, fmt.Errorf("重试次数不能超过 5")
	}
	if c.Retry.BackoffMs <= 0 {
		c.Retry.BackoffMs = 800
	}
	if c.Retry.MaxBackoffMs <= 0 {
		c.Retry.MaxBackoffMs = 5000
	}

	type pair struct{ target, model string }
	seenPair := map[pair]bool{}
	totalModels := 0
	for i := range c.Targets {
		t := &c.Targets[i]
		t.TargetID = strings.TrimSpace(t.TargetID)
		if t.TargetID == "" {
			return nil, fmt.Errorf("第 %d 个目标缺少上游", i+1)
		}
		t.AuthMode = strings.ToLower(strings.TrimSpace(t.AuthMode))
		switch t.AuthMode {
		case "", "auto", "api-key", "bearer":
		default:
			return nil, fmt.Errorf("第 %d 个目标的认证方式无效: %s", i+1, t.AuthMode)
		}
		models := make([]string, 0, len(t.Models))
		for _, rawModel := range t.Models {
			name := strings.TrimSpace(rawModel)
			if name == "" || seenPair[pair{t.TargetID, name}] {
				continue
			}
			seenPair[pair{t.TargetID, name}] = true
			models = append(models, name)
		}
		if len(models) == 0 {
			return nil, fmt.Errorf("第 %d 个目标没有选择模型", i+1)
		}
		t.Models = models
		totalModels += len(models)
	}
	if totalModels > 32 {
		return nil, fmt.Errorf("模型总数不能超过 32 个")
	}
	return &c, nil
}

// modelJob 是一个“上游 + 模型”的独立执行单元。
type modelJob struct {
	target   model.TargetSecret
	label    string
	model    string
	protocol model.Protocol
	endpoint string
	authMode string
	plans    []plan
}

func (m *Module) Run(ctx context.Context, sess *engine.Session, cfgAny any) error {
	cfg := cfgAny.(*Config)
	started := time.Now()

	jobs := make([]modelJob, 0, 8)
	reports := make([]ModelReport, 0, 8)
	seen := map[string]bool{}
	for _, spec := range cfg.Targets {
		t, err := m.store.GetTarget(spec.TargetID, true)
		if err != nil {
			return fmt.Errorf("目标 %s 不存在或读取失败: %w", spec.TargetID, err)
		}
		root, err := protocol.NormalizeBaseURL(t.BaseURL)
		if err != nil {
			return fmt.Errorf("目标 %s 的 Base URL 无效: %w", t.Name, err)
		}
		endpoint := root + "/v1/messages"
		displayName := strings.TrimSpace(spec.Label)
		if displayName == "" {
			displayName = t.Name
		}
		for _, name := range spec.Models {
			key := t.ID + "\x00" + name
			if seen[key] {
				continue
			}
			seen[key] = true
			proto := protocol.ResolveProtocol(t.Protocol, name)
			if proto != model.ProtocolClaude {
				reports = append(reports, ModelReport{
					TargetID: t.ID, TargetName: displayName, Model: name,
					Protocol: string(proto), Endpoint: endpoint,
					AuthMode: resolveAuthMode(spec.AuthMode, t.APIKey),
					Error:    "非 Claude Messages 协议，未执行（请使用 Claude 协议或 claude-* 模型）",
				})
				continue
			}
			jobs = append(jobs, modelJob{
				target:   *t,
				label:    displayName,
				model:    name,
				protocol: proto,
				endpoint: endpoint,
				authMode: resolveAuthMode(spec.AuthMode, t.APIKey),
				plans:    buildPlans(cfg, name),
			})
		}
	}
	if len(jobs) == 0 {
		return fmt.Errorf("没有可执行的 Claude 模型：请检查上游协议与模型名")
	}

	planned := len(jobs) * cfg.Count
	sess.SetPlanned(int64(planned))
	sess.SetExtra("claudeSignatureTotal", planned)
	sess.SetExtra("claudeSignatureDone", 0)

	var mu sync.Mutex
	requests := make([]RequestReport, 0, planned)
	sem := make(chan struct{}, cfg.Concurrency)
	var wg sync.WaitGroup

	for i := range jobs {
		job := jobs[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return
			}
			base, reqs := runModel(ctx, sess, cfg, job)
			mu.Lock()
			reports = append(reports, base)
			requests = append(requests, reqs...)
			mu.Unlock()
		}()
	}
	wg.Wait()

	sort.Slice(reports, func(i, j int) bool {
		if reports[i].TargetName == reports[j].TargetName {
			return reports[i].Model < reports[j].Model
		}
		return reports[i].TargetName < reports[j].TargetName
	})
	sort.Slice(requests, func(i, j int) bool {
		if requests[i].TargetName != requests[j].TargetName {
			return requests[i].TargetName < requests[j].TargetName
		}
		if requests[i].Model != requests[j].Model {
			return requests[i].Model < requests[j].Model
		}
		return requests[i].Index < requests[j].Index
	})
	for i := range requests {
		requests[i].GlobalIndex = i + 1
	}

	report := &Report{
		Models:   reports,
		Requests: requests,
		Summary: Summary{
			Models:  len(jobs),
			Planned: planned,
		},
		Notes: notes(),
	}
	finalizeReport(report)
	report.Summary.ElapsedMs = float64(time.Since(started).Microseconds()) / 1000.0
	sess.SetModuleResult(report)
	sess.SetExtra("claudeSignatureDone", len(requests))

	if ctx.Err() != nil {
		return ctx.Err()
	}
	return nil
}

func resolveAuthMode(spec, key string) string {
	switch strings.ToLower(strings.TrimSpace(spec)) {
	case "api-key":
		return "api-key"
	case "bearer":
		return "bearer"
	}
	k := strings.ToLower(strings.TrimSpace(key))
	if strings.HasPrefix(k, "bearer ") || strings.HasPrefix(k, "sk-ant-oat") || strings.HasPrefix(k, "eyj") {
		return "bearer"
	}
	return "api-key"
}

// finalizeReport 计算重复签名并重算汇总。重复签名指同一上游、同一模型下，
// 两次不同提示词返回了完全相同的签名，通常意味着缓存回放或固定响应。
func finalizeReport(report *Report) {
	seen := map[string]int{}
	for i := range report.Requests {
		r := &report.Requests[i]
		for _, sig := range r.Signatures {
			if sig.Opaque || sig.ParseError != "" || sig.SignatureSHA256 == "" {
				continue
			}
			key := r.TargetID + "\x00" + r.Model + "\x00" + sig.SignatureSHA256
			if prev, ok := seen[key]; ok {
				markDuplicate(r, report.Requests[prev].GlobalIndex)
				markDuplicate(&report.Requests[prev], r.GlobalIndex)
			} else {
				seen[key] = i
			}
		}
	}

	byModel := map[string]*ModelReport{}
	for i := range report.Models {
		report.Models[i].Requests = 0
		report.Models[i].Passed = 0
		report.Models[i].Suspicious = 0
		report.Models[i].Inconclusive = 0
		report.Models[i].Errors = 0
		report.Models[i].Signatures = 0
		report.Models[i].Parsed = 0
		report.Models[i].Duplicates = 0
		report.Models[i].Cost = 0
		byModel[report.Models[i].TargetID+"\x00"+report.Models[i].Model] = &report.Models[i]
	}

	report.Summary = Summary{Models: len(report.Models), Planned: report.Summary.Planned}
	for i := range report.Requests {
		r := &report.Requests[i]
		report.Summary.Requests++
		report.Summary.Signatures += len(r.Signatures)
		if r.RequestID != "" {
			report.Summary.WithRequestID++
		}
		for _, sig := range r.Signatures {
			if sig.ParseError != "" {
				continue
			}
			if sig.Opaque {
				report.Summary.Opaque++
				continue
			}
			report.Summary.Parsed++
			switch sig.Layout {
			case LayoutFull:
				report.Summary.Full++
			case LayoutMinimal:
				report.Summary.Minimal++
			default:
				report.Summary.Unknown++
			}
		}
		switch r.Verdict {
		case VerdictPass:
			report.Summary.Passed++
		case VerdictSuspicious:
			report.Summary.Suspicious++
		case VerdictInconclusive:
			report.Summary.Inconclusive++
		default:
			report.Summary.Errors++
		}
		if r.Duplicate {
			report.Summary.Duplicates++
		}
		report.Summary.Cost += r.Cost

		if m := byModel[r.TargetID+"\x00"+r.Model]; m != nil {
			m.Requests++
			m.Signatures += len(r.Signatures)
			m.Cost += r.Cost
			switch r.Verdict {
			case VerdictPass:
				m.Passed++
			case VerdictSuspicious:
				m.Suspicious++
			case VerdictInconclusive:
				m.Inconclusive++
			default:
				m.Errors++
			}
			if r.Duplicate {
				m.Duplicates++
			}
			for _, sig := range r.Signatures {
				if sig.ParseError == "" && !sig.Opaque {
					m.Parsed++
				}
			}
		}
	}
}

func markDuplicate(r *RequestReport, otherIndex int) {
	if otherIndex <= 0 {
		return
	}
	r.Duplicate = true
	for _, c := range r.Checks {
		if c.ID == "signature_unique" {
			return
		}
	}
	r.Checks = append(r.Checks, CheckReport{
		ID:       "signature_unique",
		Name:     "签名唯一性",
		Status:   CheckFail,
		Evidence: fmt.Sprintf("与第 %d 条请求返回了完全相同的签名，可能是缓存回放或固定响应", otherIndex),
	})
	if r.Verdict != VerdictError {
		r.Verdict = VerdictSuspicious
		r.VerdictLabel = verdictLabel(VerdictSuspicious)
	}
}

func (m *Module) Summary(sess *engine.Session) model.RunSummary {
	base := sess.Headline()
	report, ok := sess.ModuleResult().(*Report)
	if !ok || report == nil {
		return base
	}
	denom := report.Summary.Passed + report.Summary.Suspicious + report.Summary.Errors
	rate := 0.0
	if denom > 0 {
		rate = float64(report.Summary.Passed) / float64(denom) * 100
	}
	out := model.RunSummary{
		Planned:     int64(report.Summary.Planned),
		Total:       int64(report.Summary.Requests),
		Success:     int64(report.Summary.Passed),
		Failed:      int64(report.Summary.Suspicious + report.Summary.Errors),
		Canceled:    base.Canceled,
		SuccessRate: rate,
		QPS:         base.QPS,
		RPM:         base.RPM,
		ElapsedMs:   report.Summary.ElapsedMs,
		Cost:        report.Summary.Cost,
		Data:        report,
	}
	if out.ElapsedMs == 0 {
		out.ElapsedMs = float64(sess.ElapsedMs())
	}
	seenModel := map[string]bool{}
	seenTarget := map[string]bool{}
	for _, r := range report.Models {
		if r.Model != "" && !seenModel[r.Model] {
			seenModel[r.Model] = true
			out.Models = append(out.Models, r.Model)
		}
		if r.TargetID != "" && !seenTarget[r.TargetID] {
			seenTarget[r.TargetID] = true
			out.Targets = append(out.Targets, model.TargetRef{ID: r.TargetID, Name: r.TargetName})
		}
	}
	return out
}

func notes() []string {
	return []string{
		"signature 字段是 base64 编码的 protobuf，不是加密数据；本模块做的是结构解包，不是解密。",
		"字段号（block type=8、model=6、uuid=11、timestamp=21）来自已知样本，只作为提示；字段缺失时会回退到整棵树搜索，因此元数据属于启发式结果。",
		"无法验证密码学签名：客户端拿不到 Anthropic 的验签公钥。结论“通过”只表示签名存在且元数据未发现矛盾，不等于证明模型真实。",
		"部分签名版本（minimal）按设计只含 block type，不含 model/uuid/时间戳；缺少这些字段会显示“证据不足”，不能据此判假。",
		"redacted_thinking 的内容是上游加密载荷，按设计不可解析；仅计为证据不足，不计为失败。",
		"同一上游、同一模型下，不同提示词返回完全相同的签名会标记为可疑，通常意味着缓存回放或固定响应。",
		"模型未返回 thinking 块时不会产生签名，请确认模型支持 thinking、参数未被中转站丢弃。",
	}
}

// buildPlans 复刻脚本的回退顺序：首选模式 → 去掉 effort → budget → adaptive → off。
func buildPlans(cfg *Config, modelName string) []plan {
	initial := cfg.Thinking
	if initial == "auto" {
		if supportsAdaptive(modelName) {
			initial = "adaptive"
		} else {
			initial = "budget"
		}
	}
	plans := make([]plan, 0, 6)
	if cfg.Effort != "" {
		plans = append(plans, plan{Thinking: initial, Effort: cfg.Effort})
	}
	plans = append(plans, plan{Thinking: initial})
	for _, alt := range []string{"budget", "adaptive", "off"} {
		if alt != initial {
			plans = append(plans, plan{Thinking: alt})
		}
	}
	out := plans[:0]
	seen := map[plan]bool{}
	for _, p := range plans {
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

func supportsAdaptive(modelName string) bool {
	text := strings.ToLower(strings.TrimSpace(modelName))
	family := adaptiveFamilyRe.FindStringSubmatch(text)
	if family == nil {
		family = adaptiveVersionRe.FindStringSubmatch(text)
	}
	if family == nil {
		// 未知别名默认按 adaptive 处理，失败时会自动回退到 budget/off。
		return true
	}
	major, _ := parseInt(family[1])
	minor, _ := parseInt(family[2])
	return major > 4 || (major == 4 && minor >= 6)
}

func parseInt(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, true
}

// 把时间差格式化成便于阅读的中文描述。
func humanDuration(ms float64) string {
	abs := math.Abs(ms)
	switch {
	case abs < 1000:
		return fmt.Sprintf("%.0f 毫秒", abs)
	case abs < 60_000:
		return fmt.Sprintf("%.1f 秒", abs/1000)
	case abs < 3_600_000:
		return fmt.Sprintf("%.1f 分钟", abs/60000)
	default:
		return fmt.Sprintf("%.1f 小时", abs/3_600_000)
	}
}
