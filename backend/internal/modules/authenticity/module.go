package authenticity

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"apiunit/internal/engine"
	"apiunit/internal/model"
	"apiunit/internal/store"
)

type Module struct {
	store *store.Store
}

func New(st *store.Store) *Module { return &Module{store: st} }

func (m *Module) Kind() model.RunKind { return model.KindAuthenticity }
func (m *Module) Title() string       { return "模型真实性" }

func (m *Module) Validate(raw json.RawMessage) (any, error) {
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("配置解析失败: %w", err)
	}
	if len(c.Targets) == 0 {
		return nil, fmt.Errorf("请至少选择一个上游目标")
	}
	if len(c.Targets) > 16 {
		return nil, fmt.Errorf("上游目标不能超过 16 个")
	}
	totalModels := 0
	for i := range c.Targets {
		t := &c.Targets[i]
		t.TargetID = strings.TrimSpace(t.TargetID)
		t.Protocol = strings.ToLower(strings.TrimSpace(t.Protocol))
		t.AuthMode = strings.ToLower(strings.TrimSpace(t.AuthMode))
		if t.TargetID == "" {
			return nil, fmt.Errorf("第 %d 个目标缺少上游", i+1)
		}
		switch t.Protocol {
		case "", "auto", "openai", "claude", "gemini":
		default:
			return nil, fmt.Errorf("第 %d 个目标的协议无效: %s", i+1, t.Protocol)
		}
		switch t.AuthMode {
		case "", "api-key", "bearer":
		default:
			return nil, fmt.Errorf("第 %d 个目标的认证方式无效: %s", i+1, t.AuthMode)
		}
		seen := map[string]bool{}
		models := make([]string, 0, len(t.Models))
		for _, name := range t.Models {
			name = strings.TrimSpace(name)
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			models = append(models, name)
		}
		if len(models) == 0 {
			return nil, fmt.Errorf("第 %d 个目标没有选择模型", i+1)
		}
		t.Models = models
		totalModels += len(models)
	}
	if totalModels > 64 {
		return nil, fmt.Errorf("模型总数不能超过 64 个")
	}

	if c.Concurrency <= 0 {
		c.Concurrency = 3
	}
	if c.Concurrency > 16 {
		return nil, fmt.Errorf("模型并发不能超过 16")
	}
	if c.Repetitions <= 0 {
		c.Repetitions = 1
	}
	if c.Repetitions > 5 {
		return nil, fmt.Errorf("测试次数不能超过 5")
	}
	if c.TimeoutMs <= 0 {
		c.TimeoutMs = 180000
	}
	if c.TimeoutMs > 900000 {
		return nil, fmt.Errorf("单模型超时不能超过 900 秒")
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

	// Defaults: all evidence groups on; the tokenizer comparison is opt-in
	// because it is family-level evidence and the most prone to noise.
	if !c.Checks.Fingerprint && !c.Checks.Source && !c.Checks.Capability &&
		!c.Checks.Identity && !c.Checks.Consistency && !c.Checks.Signature && !c.Checks.Tokenizer {
		c.Checks = ChecksConfig{
			Fingerprint: true, Source: true, Capability: true,
			Identity: true, Consistency: true, Signature: true, Tokenizer: false,
		}
	}

	if c.Challenge.ToolName = strings.TrimSpace(c.Challenge.ToolName); c.Challenge.ToolName == "" {
		c.Challenge.ToolName = "proof_lookup"
	}
	if len(c.Challenge.ToolName) > 64 {
		return nil, fmt.Errorf("工具名不能超过 64 个字符")
	}
	if c.Challenge.NonceLength <= 0 {
		c.Challenge.NonceLength = 24
	}
	if c.Challenge.NonceLength < 8 || c.Challenge.NonceLength > 96 {
		return nil, fmt.Errorf("nonce 长度必须在 8-96 之间")
	}
	if c.Challenge.MaxTokens <= 0 {
		c.Challenge.MaxTokens = 512
	}
	if c.Challenge.MaxTokens > 8192 {
		return nil, fmt.Errorf("输出上限不能超过 8192")
	}
	if c.Challenge.Temperature == nil {
		zero := 0.0
		c.Challenge.Temperature = &zero
	}
	if *c.Challenge.Temperature < 0 || *c.Challenge.Temperature > 2 {
		return nil, fmt.Errorf("temperature 必须在 0-2 之间")
	}

	c.Hosted.Profile = strings.ToLower(strings.TrimSpace(c.Hosted.Profile))
	switch c.Hosted.Profile {
	case "":
		c.Hosted.Profile = hostedProfileGeneric
	case hostedProfileGeneric, hostedProfileLegacy, hostedProfileCustom:
	default:
		return nil, fmt.Errorf("未知的工具调用 profile: %s", c.Hosted.Profile)
	}
	if c.Hosted.TimeoutMs <= 0 {
		c.Hosted.TimeoutMs = 300000
	}
	if c.Hosted.TimeoutMs > 900000 {
		return nil, fmt.Errorf("工具调用超时不能超过 900 秒")
	}
	if c.Hosted.Enabled {
		settings, err := loadHostedSettings(m.store)
		if err != nil {
			return nil, fmt.Errorf("读取 MCP / 证明页配置失败: %w", err)
		}
		if strings.TrimSpace(settings.MCP.ServerURL) == "" && strings.TrimSpace(settings.Proof.BaseURL) == "" {
			return nil, fmt.Errorf("已启用工具调用，但尚未配置 MCP 地址或证明页地址")
		}
	}

	if c.Thresholds.TokenizerPct <= 0 {
		c.Thresholds.TokenizerPct = 25
	}
	if c.Thresholds.TokenizerPct > 200 {
		return nil, fmt.Errorf("分词器偏差阈值不能超过 200%%")
	}
	if c.Thresholds.MaxResponseBytes <= 0 {
		c.Thresholds.MaxResponseBytes = 8 << 20
	}
	if len(c.CustomProbes) > 50 {
		return nil, fmt.Errorf("自定义探针不能超过 50 个")
	}
	seenProbe := map[string]bool{}
	for i := range c.CustomProbes {
		p := &c.CustomProbes[i]
		p.ID = strings.TrimSpace(p.ID)
		if p.ID == "" {
			p.ID = fmt.Sprintf("custom_%d", i+1)
		}
		if seenProbe[p.ID] {
			return nil, fmt.Errorf("自定义探针 ID 重复: %s", p.ID)
		}
		seenProbe[p.ID] = true
		p.Name = strings.TrimSpace(p.Name)
		if p.Name == "" {
			p.Name = p.ID
		}
		p.Path = strings.TrimSpace(p.Path)
		if p.Path == "" {
			return nil, fmt.Errorf("自定义探针 %s 缺少 path", p.ID)
		}
		p.Method = strings.ToUpper(strings.TrimSpace(p.Method))
		if p.Method == "" {
			p.Method = "POST"
		}
		if p.Method != "POST" && p.Method != "GET" {
			return nil, fmt.Errorf("自定义探针 %s 仅支持 POST/GET", p.ID)
		}
		p.Protocol = strings.ToLower(strings.TrimSpace(p.Protocol))
		switch p.Protocol {
		case "", "auto", "openai", "claude", "gemini":
		default:
			return nil, fmt.Errorf("自定义探针 %s 的协议无效: %s", p.ID, p.Protocol)
		}
		for _, e := range p.Expected {
			switch e.Type {
			case "status", "json_path", "contains", "regex", "header":
			default:
				return nil, fmt.Errorf("自定义探针 %s 的期望类型无效: %s", p.ID, e.Type)
			}
		}
	}
	return &c, nil
}

type job struct {
	target           model.TargetSecret
	spec             TargetSpec
	model            string
	repetition       int
	totalRepetitions int
}

func (m *Module) Run(ctx context.Context, sess *engine.Session, cfgAny any) error {
	cfg := cfgAny.(*Config)
	hosted, err := loadHostedSettings(m.store)
	if err != nil {
		return fmt.Errorf("读取 MCP / 证明页配置失败: %w", err)
	}
	if cfg.Hosted.Profile != "" {
		hosted.Profile = cfg.Hosted.Profile
	}
	if len(cfg.Hosted.Providers) == 0 {
		cfg.Hosted.Providers = hosted.Providers
	}

	jobs := make([]job, 0)
	for _, spec := range cfg.Targets {
		t, err := m.store.GetTarget(spec.TargetID, true)
		if err != nil {
			return fmt.Errorf("目标 %s 不存在或读取失败: %w", spec.TargetID, err)
		}
		for _, name := range spec.Models {
			for rep := 1; rep <= cfg.Repetitions; rep++ {
				jobs = append(jobs, job{
					target: *t, spec: spec, model: name,
					repetition: rep, totalRepetitions: cfg.Repetitions,
				})
			}
		}
	}
	if len(jobs) == 0 {
		return fmt.Errorf("没有可测试的模型")
	}
	sess.SetPlanned(int64(len(jobs)))
	sess.SetExtra("authenticityTotal", len(jobs))
	sess.SetExtra("authenticityDone", 0)
	pub := newLivePublisher(sess, jobs)

	started := time.Now()
	reports := make([]ModelReport, 0, len(jobs))
	var mu sync.Mutex
	sem := make(chan struct{}, cfg.Concurrency)
	var wg sync.WaitGroup

	for _, j := range jobs {
		j := j
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
			sess.SetCurrentModel(j.model)
			rep := runModel(ctx, sess, m.store, cfg, hosted, j, pub)
			mu.Lock()
			reports = append(reports, rep)
			mu.Unlock()
			sess.AddExtra("authenticityDone", 1)
		}()
	}
	wg.Wait()

	reports = aggregateReports(reports, cfg.Repetitions)

	report := &Report{Models: reports, Notes: notes(cfg)}
	for i := range reports {
		r := &reports[i]
		report.Summary.Models++
		report.Summary.Passed += r.Passed
		report.Summary.Failed += r.Failed
		report.Summary.NA += r.NA
		switch r.Verdict {
		case VerdictAuthentic:
			report.Summary.Authentic++
		case VerdictLikely:
			report.Summary.Likely++
		case VerdictSuspicious:
			report.Summary.Suspicious++
		case VerdictCounterfeit:
			report.Summary.Counterfeit++
		case VerdictError:
			report.Summary.Error++
		default:
			report.Summary.Inconclusive++
		}
	}
	report.Summary.Models = len(reports)
	sess.SetModuleResult(report)
	sess.SetExtra("authenticityElapsedMs", time.Since(started).Milliseconds())

	if ctx.Err() != nil {
		return ctx.Err()
	}
	return nil
}

func (m *Module) Summary(sess *engine.Session) model.RunSummary {
	base := sess.Headline()
	report, ok := sess.ModuleResult().(*Report)
	if !ok || report == nil {
		return base
	}
	planned := int64(report.Summary.Models)
	if v, ok := sess.GetExtra("authenticityTotal").(int); ok {
		planned = int64(v)
	}
	out := model.RunSummary{
		Planned:     planned,
		Total:       int64(report.Summary.Passed + report.Summary.Failed + report.Summary.NA),
		Success:     int64(report.Summary.Passed),
		Failed:      int64(report.Summary.Failed),
		SuccessRate: 0,
		ElapsedMs:   float64(base.ElapsedMs),
		Data:        report,
	}
	if report.Summary.Passed+report.Summary.Failed > 0 {
		out.SuccessRate = float64(report.Summary.Passed) / float64(report.Summary.Passed+report.Summary.Failed) * 100
	}
	seenTarget := map[string]bool{}
	for _, r := range report.Models {
		out.Models = append(out.Models, r.Model)
		if r.TargetID != "" && !seenTarget[r.TargetID] {
			seenTarget[r.TargetID] = true
			out.Targets = append(out.Targets, model.TargetRef{ID: r.TargetID, Name: r.TargetName})
		}
	}
	out.Models = dedupe(out.Models)
	if out.ElapsedMs == 0 {
		out.ElapsedMs = float64(sess.ElapsedMs())
	}
	return out
}

func notes(cfg *Config) []string {
	out := []string{
		"N/A 表示目标或模型不支持该探针，不参与通过率，也不等于造假。",
		"官方指纹文案需要与目标 API 版本标定；不匹配只表示“未确认官方”，不会单独判为仿冒。",
		"本地 nonce 挑战每次运行随机生成，可防止固定答案回放；同家族同能力的模型降级无法仅靠黑盒完全识别。",
		"响应来源按上游特征推断：Claude 只看响应头；Gemini / OpenAI 在没有明确上游特征时按 model-proof 口径回落为 AI Studio / OpenAI 官方，置信度为低。X-Oneapi 等中转特征单独展示，不代表来源本身。",
	}
	if cfg.Repetitions > 1 {
		out = append(out, fmt.Sprintf("每个模型完整重复 %d 轮；结论按各轮综合（任一仿冒即仿冒，出现可疑则可疑，全部通过才通过）。", cfg.Repetitions))
	}
	if cfg.Hosted.Enabled {
		out = append(out, "工具调用检测已启用；MCP / 证明页请求中的凭据只在本次运行使用，报告会脱敏。hosted 全通过可作为强证据，但恶意端点仍可改写 MCP 请求或伪造工具结果，请结合指纹与来源证据一起看。")
	} else {
		out = append(out, "工具调用检测未启用，MCP / 联网搜索相关检查记为 N/A；本地挑战仍会执行。")
	}
	return out
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
