package imagedetection

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"apiunit/internal/engine"
	"apiunit/internal/model"
	"apiunit/internal/store"
)

var sizeRe = regexp.MustCompile(`^\d+x\d+$`)

type Module struct {
	store     *store.Store
	imageRoot string
}

func New(st *store.Store, imageRoot string) *Module {
	return &Module{store: st, imageRoot: imageRoot}
}

func (m *Module) Kind() model.RunKind { return model.KindImageDetection }
func (m *Module) Title() string       { return "生图检测" }

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
	if c.Concurrency <= 0 {
		c.Concurrency = 3
	}
	if c.Concurrency > 16 {
		return nil, fmt.Errorf("模型并发不能超过 16")
	}
	if c.TimeoutMs <= 0 {
		c.TimeoutMs = 180000
	}
	if c.TimeoutMs > 900000 {
		return nil, fmt.Errorf("单请求超时不能超过 900 秒")
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
	c.GenerationPrompt = strings.TrimSpace(c.GenerationPrompt)
	if c.GenerationPrompt == "" {
		c.GenerationPrompt = "A cute cat sitting on a sofa"
	}
	c.EditPrompt = strings.TrimSpace(c.EditPrompt)
	if c.EditPrompt == "" {
		c.EditPrompt = "Change the sofa color to blue, keep the cat and composition unchanged"
	}
	c.OutputFormat = normalizeFormat(c.OutputFormat)
	if c.OutputFormat == "" {
		c.OutputFormat = "png"
	}
	switch c.OutputFormat {
	case "png", "jpeg", "webp":
	default:
		return nil, fmt.Errorf("输出格式仅支持 png / jpeg / webp")
	}
	// 本地 C2PA 解析不依赖外部服务，默认保持开启。
	if !c.Signature.Local && !c.Signature.Online {
		c.Signature.Local = true
	}

	phases, err := normalizePhaseSet(c.Phases)
	if err != nil {
		return nil, err
	}
	c.Phases = phaseNames(phases)

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
		models := make([]ModelSpec, 0, len(t.Models))
		for _, spec := range t.Models {
			name := strings.TrimSpace(spec.Name)
			if name == "" || seen[name] {
				continue
			}
			kind, err := normalizeModelKind(spec.Kind)
			if err != nil {
				return nil, fmt.Errorf("模型 %s: %w", name, err)
			}
			size, err := normalizeModelSize(spec.Size, name)
			if err != nil {
				return nil, fmt.Errorf("模型 %s: %w", name, err)
			}
			seen[name] = true
			models = append(models, ModelSpec{Name: name, Kind: kind, Size: size})
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
	return &c, nil
}

func normalizeModelKind(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "auto", "image", "images", "generations":
		return ModelKindImages, nil
	case "chat", "gemini", "completions":
		return ModelKindChat, nil
	default:
		return "", fmt.Errorf("模型类型无效: %s", raw)
	}
}

func normalizeModelSize(raw, modelName string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	switch s {
	case "", "auto":
		return detectSize(modelName), nil
	case "1k":
		return "1024x1024", nil
	case "2k":
		return "2048x2048", nil
	case "4k":
		return "4096x4096", nil
	}
	if sizeRe.MatchString(s) {
		if _, _, err := parseSize(s); err != nil {
			return "", err
		}
		return s, nil
	}
	return "", fmt.Errorf("尺寸格式无效（示例 1024x1024 / 1k / 2k / 4k）: %s", raw)
}

func normalizePhaseSet(in []string) (map[string]bool, error) {
	set := map[string]bool{}
	for _, name := range in {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if !isKnownPhase(name) {
			return nil, fmt.Errorf("未知的测试项: %s", name)
		}
		set[name] = true
	}
	if len(set) == 0 {
		return nil, nil
	}
	return set, nil
}

func isKnownPhase(name string) bool {
	if name == PhaseEditSource {
		return false // 由程序在仅勾选改图时自动补齐。
	}
	for _, p := range PhaseInfos {
		if p.Name == name {
			return true
		}
	}
	return false
}

func phaseNames(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for _, p := range PhaseInfos {
		if set[p.Name] {
			out = append(out, p.Name)
		}
	}
	return out
}

func wantPhase(set map[string]bool, name string) bool {
	if len(set) == 0 {
		return true
	}
	return set[name]
}

func phasePlan(spec ModelSpec, selected map[string]bool) []string {
	if spec.Kind == ModelKindChat {
		if wantPhase(selected, PhaseChatGeneration) {
			return []string{PhaseChatGeneration}
		}
		return nil
	}
	gens := make([]string, 0, 3)
	for _, p := range []string{PhaseGenTransparent, PhaseGenOpaque, PhaseGenAuto} {
		if wantPhase(selected, p) {
			gens = append(gens, p)
		}
	}
	edits := make([]string, 0, 2)
	for _, p := range []string{PhaseEditImages, PhaseEditAlias} {
		if wantPhase(selected, p) {
			edits = append(edits, p)
		}
	}
	out := append([]string{}, gens...)
	if len(edits) > 0 && len(gens) == 0 {
		out = append(out, PhaseEditSource)
	}
	out = append(out, edits...)
	return out
}

func (m *Module) Run(ctx context.Context, sess *engine.Session, cfgAny any) error {
	cfg := cfgAny.(*Config)
	settings, settingsErr := loadSettings(m.store)
	if settingsErr != nil {
		settings = defaultSettings()
	}
	selected, err := normalizePhaseSet(cfg.Phases)
	if err != nil {
		return err
	}
	if len(cfg.Phases) == 0 {
		selected = nil
	}

	jobs := make([]job, 0)
	skipped := make([]ModelReport, 0)
	for _, spec := range cfg.Targets {
		t, err := m.store.GetTarget(spec.TargetID, true)
		if err != nil {
			return fmt.Errorf("目标 %s 不存在或读取失败: %w", spec.TargetID, err)
		}
		for _, ms := range spec.Models {
			plan := phasePlan(ms, selected)
			if len(plan) == 0 {
				skipped = append(skipped, ModelReport{
					TargetID: t.ID, TargetName: t.Name, Model: ms.Name,
					Kind: ms.Kind, Size: ms.Size, Protocol: string(resolveProtocol(t.Protocol, spec.Protocol, ms.Name)),
					Endpoint: modelEndpointFor(t.BaseURL, ms.Kind),
					Error:    "所选测试项与模型类型不匹配，未执行",
				})
				continue
			}
			jobs = append(jobs, job{
				target:   *t,
				spec:     spec,
				model:    ms,
				protocol: resolveProtocol(t.Protocol, spec.Protocol, ms.Name),
				phases:   plan,
			})
		}
	}
	if len(jobs) == 0 {
		return fmt.Errorf("没有可执行的模型：请检查模型类型与所选测试项是否匹配")
	}

	totalPhases := 0
	for _, j := range jobs {
		totalPhases += len(j.phases)
	}
	sess.SetPlanned(int64(totalPhases))
	sess.SetExtra("imageDetectionTotal", totalPhases)
	sess.SetExtra("imageDetectionDone", 0)
	sess.SetExtra("imageDetectionOnlineReady", strings.TrimSpace(settings.APIKey) != "")
	if settingsErr != nil {
		sess.SetExtra("imageDetectionSettingsError", settingsErr.Error())
	}
	pub := newLivePublisher(sess, jobs)

	started := time.Now()
	reports := make([]ModelReport, 0, len(jobs)+len(skipped))
	reports = append(reports, skipped...)
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
			r := newRunner(cfg, settings, sess.RunID, m.imageRoot, sess, pub)
			rep := r.runModel(ctx, j)
			pub.finish(j, rep.Error)
			mu.Lock()
			reports = append(reports, rep)
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

	report := &Report{Models: reports, Notes: notes(cfg, settings, settingsErr)}
	for i := range reports {
		r := &reports[i]
		report.Summary.Models++
		report.Summary.Phases += r.Total
		report.Summary.Passed += r.Passed
		report.Summary.Failed += r.Failed
		report.Summary.NA += r.NA
		for _, pr := range r.Phases {
			if pr.Image == nil {
				continue
			}
			sig := pr.Image.Signature
			if sig.C2PA.Status == SignatureDetected ||
				(sig.Online != nil && sig.Online.C2PA.Outcome == "detected") {
				report.Summary.C2PADetected++
			}
			if sig.Online != nil && sig.Online.SynthID.Outcome == "detected" {
				report.Summary.SynthIDDetected++
			}
			if sig.Online != nil && sig.Online.Status == OnlineChecked {
				report.Summary.OnlineChecked++
			}
		}
	}
	sess.SetModuleResult(report)
	sess.SetExtra("imageDetectionElapsedMs", time.Since(started).Milliseconds())

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
	planned := int64(0)
	if v, ok := sess.GetExtra("imageDetectionTotal").(int); ok {
		planned = int64(v)
	}
	out := model.RunSummary{
		Planned:   planned,
		Total:     int64(report.Summary.Passed + report.Summary.Failed + report.Summary.NA),
		Success:   int64(report.Summary.Passed),
		Failed:    int64(report.Summary.Failed),
		ElapsedMs: float64(base.ElapsedMs),
		Data:      report,
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

func notes(cfg *Config, settings Settings, settingsErr error) []string {
	out := []string{
		"图片按上游返回的原始字节保存，没有解码后重新编码，避免破坏 C2PA 元数据与 SynthID 水印。",
		"本地 C2PA 检查只判断清单/JUMBF 是否存在，不做证书链验签；完整验签请使用 Adobe、Azure 或 OpenAI 在线校验。",
		"SynthID 是像素级不可见水印，本地只能检查显式标记字符串；是否完整必须通过在线服务确认。",
		"签名缺失不等于造假：平台转码、缩放、截图或上游重编码都可能移除元数据和水印。",
		"改图结果以人工审核为主；程序会保存改图前后的图片、提示词与原始响应。",
	}
	if cfg.Signature.Online {
		if strings.TrimSpace(settings.APIKey) == "" {
			out = append(out, "本次已启用在线验签，但未配置 OpenAI API Key；报告只展示本地结果和在线校验入口。请在「生图检测」页面底部配置 Key 后重新运行。")
		} else {
			out = append(out, "在线验签调用 OpenAI /v1/content_provenance_checks，同时返回 C2PA 与 SynthID 结果；该接口只能识别其支持的来源信号，not_detected 不代表图片一定不是 AI 生成。")
		}
	} else {
		out = append(out, "本次未启用在线验签；可使用每张图片旁的 Adobe / OpenAI / Azure 在线校验入口手动确认两类签名。")
	}
	if settingsErr != nil {
		out = append(out, "读取在线验签配置失败，已使用默认值: "+settingsErr.Error())
	}
	return out
}

func resolveProtocol(target model.Protocol, spec, modelName string) model.Protocol {
	switch model.Protocol(strings.ToLower(strings.TrimSpace(spec))) {
	case model.ProtocolOpenAI:
		return model.ProtocolOpenAI
	case model.ProtocolClaude:
		return model.ProtocolClaude
	case model.ProtocolGemini:
		return model.ProtocolGemini
	}
	if target != "" && target != model.ProtocolAuto {
		return target
	}
	return model.InferProtocol(modelName)
}

func modelEndpointFor(baseURL, kind string) string {
	base := strings.TrimRight(baseURL, "/")
	if kind == ModelKindChat {
		return base + "/v1/chat/completions"
	}
	return base + "/v1/images/generations"
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
