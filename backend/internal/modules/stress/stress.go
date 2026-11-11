package stress

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
	"apiunit/internal/store"
	"apiunit/internal/tokens"
)

type ModelSpec struct {
	Model    string `json:"model"`
	Protocol string `json:"protocol,omitempty"`
}

type RequestConfig struct {
	SystemPrompt string   `json:"systemPrompt"`
	Prompt       string   `json:"prompt"`
	MaxTokens    int      `json:"maxTokens"`
	Temperature  *float64 `json:"temperature"`
	TopP         *float64 `json:"topP"`
	Stream       bool     `json:"stream"`
	Stop         []string `json:"stop,omitempty"`
	Seed         *int     `json:"seed,omitempty"`
}

type LoadConfig struct {
	Mode          string  `json:"mode"`
	TotalRequests int     `json:"totalRequests"`
	Concurrency   int     `json:"concurrency"`
	DurationSec   int     `json:"durationSec"`
	RampStart     int     `json:"rampStart"`
	RampEnd       int     `json:"rampEnd"`
	RampStep      int     `json:"rampStep"`
	RampStepSec   int     `json:"rampStepSec"`
	RPSLimit      float64 `json:"rpsLimit"`
}

type Config struct {
	TargetID          string             `json:"targetId"`
	Models            []ModelSpec        `json:"models"`
	PromptIDs         []string           `json:"promptIds,omitempty"`
	Request           RequestConfig      `json:"request"`
	Load              LoadConfig         `json:"load"`
	Retry             engine.RetryPolicy `json:"retry"`
	TimeoutMs         int                `json:"timeoutMs"`
	ParallelModels    bool               `json:"parallelModels"`
	CaptureBodies     bool               `json:"captureBodies"`
	TokenizerStrategy string             `json:"tokenizerStrategy"`
}

type Module struct {
	store *store.Store
}

func New(st *store.Store) *Module { return &Module{store: st} }

func (m *Module) Kind() model.RunKind { return model.KindStress }
func (m *Module) Title() string       { return "并发压测" }

func (m *Module) Validate(raw json.RawMessage) (any, error) {
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("配置解析失败: %w", err)
	}
	c.TargetID = strings.TrimSpace(c.TargetID)
	if c.TargetID == "" {
		return nil, fmt.Errorf("请选择上游目标")
	}
	seen := map[string]bool{}
	models := make([]ModelSpec, 0, len(c.Models))
	for _, ms := range c.Models {
		ms.Model = strings.TrimSpace(ms.Model)
		if ms.Model == "" || seen[ms.Model] {
			continue
		}
		seen[ms.Model] = true
		ms.Protocol = string(modcommon.NormProtocol(ms.Protocol))
		models = append(models, ms)
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("请至少选择一个模型")
	}
	if len(models) > 50 {
		return nil, fmt.Errorf("一次最多测试 50 个模型")
	}
	c.Models = models
	if strings.TrimSpace(c.Request.Prompt) == "" {
		return nil, fmt.Errorf("请输入测试提示词")
	}
	if c.Request.MaxTokens <= 0 {
		c.Request.MaxTokens = 256
	}
	if c.Request.MaxTokens > 32000 {
		return nil, fmt.Errorf("maxTokens 不能超过 32000")
	}
	if c.Request.Temperature != nil && (*c.Request.Temperature < 0 || *c.Request.Temperature > 2) {
		return nil, fmt.Errorf("temperature 必须在 0-2 之间")
	}
	if c.Request.TopP != nil && (*c.Request.TopP < 0 || *c.Request.TopP > 1) {
		return nil, fmt.Errorf("topP 必须在 0-1 之间")
	}
	if c.TimeoutMs <= 0 {
		c.TimeoutMs = 120000
	}
	if c.TimeoutMs < 1000 || c.TimeoutMs > 600000 {
		return nil, fmt.Errorf("超时时间必须在 1-600 秒之间")
	}
	if c.Load.Mode == "" {
		c.Load.Mode = "fixed"
	}
	switch c.Load.Mode {
	case "fixed":
		if c.Load.TotalRequests <= 0 {
			c.Load.TotalRequests = 20
		}
		if c.Load.TotalRequests > 200000 {
			return nil, fmt.Errorf("固定请求数不能超过 200000")
		}
	case "duration":
		if c.Load.DurationSec < 5 || c.Load.DurationSec > 3600 {
			return nil, fmt.Errorf("持续时长必须在 5-3600 秒之间")
		}
	case "ramp":
		if c.Load.RampStart < 1 {
			c.Load.RampStart = 1
		}
		if c.Load.RampEnd < c.Load.RampStart {
			return nil, fmt.Errorf("爬坡结束并发必须大于等于起始并发")
		}
		if c.Load.RampStep < 1 {
			c.Load.RampStep = 1
		}
		if c.Load.RampStepSec < 3 || c.Load.RampStepSec > 600 {
			return nil, fmt.Errorf("每个爬坡阶段必须为 3-600 秒")
		}
		if c.Load.RampEnd > 500 {
			return nil, fmt.Errorf("并发不能超过 500")
		}
	default:
		return nil, fmt.Errorf("未知压测模式: %s", c.Load.Mode)
	}
	if c.Load.Concurrency <= 0 {
		c.Load.Concurrency = 5
	}
	if c.Load.Concurrency > 500 {
		return nil, fmt.Errorf("并发不能超过 500")
	}
	if c.Load.RPSLimit < 0 {
		return nil, fmt.Errorf("RPS 限制不能为负数")
	}
	if c.Load.Mode == "fixed" && int64(len(c.Models))*int64(c.Load.TotalRequests) > 200000 {
		return nil, fmt.Errorf("总请求数（模型数×请求数）不能超过 200000")
	}
	if len(c.PromptIDs) > 200 {
		return nil, fmt.Errorf("提示词数量不能超过 200")
	}
	if c.TokenizerStrategy == "" {
		c.TokenizerStrategy = string(tokens.StrategyAuto)
	}
	return &c, nil
}

type promptItem struct {
	ID   string
	Name string
	Text string
}

func (m *Module) Run(ctx context.Context, sess *engine.Session, cfgAny any) error {
	cfg := cfgAny.(*Config)
	target, err := modcommon.ResolveTarget(m.store, cfg.TargetID)
	if err != nil {
		return err
	}
	prompts := []promptItem{{Name: "默认提示词", Text: cfg.Request.Prompt}}
	if len(cfg.PromptIDs) > 0 {
		list, err := m.store.GetPrompts(cfg.PromptIDs)
		if err != nil {
			return err
		}
		if len(list) > 0 {
			prompts = make([]promptItem, 0, len(list))
			for _, p := range list {
				prompts = append(prompts, promptItem{ID: p.ID, Name: p.Name, Text: p.Content})
			}
		}
	}
	if cfg.Load.Mode == "fixed" {
		sess.SetPlanned(int64(len(cfg.Models)) * int64(cfg.Load.TotalRequests))
	}
	sess.SetRateLimit(cfg.Load.RPSLimit)

	makeReq := func(ms ModelSpec, prompt promptItem, idx, worker, conc int) engine.ExecRequest {
		p := prompt
		if len(prompts) > 1 {
			p = prompts[idx%len(prompts)]
		}
		meta := map[string]any{
			"worker":      worker,
			"concurrency": conc,
			"mode":        cfg.Load.Mode,
			"promptId":    p.ID,
			"promptName":  p.Name,
		}
		proto := modcommon.NormProtocol(ms.Protocol)
		return engine.ExecRequest{
			Target:            target,
			Model:             ms.Model,
			Protocol:          proto,
			Messages:          modcommon.Messages(cfg.Request.SystemPrompt, p.Text),
			MaxTokens:         cfg.Request.MaxTokens,
			Temperature:       cfg.Request.Temperature,
			TopP:              cfg.Request.TopP,
			Stream:            cfg.Request.Stream,
			Stop:              cfg.Request.Stop,
			Seed:              cfg.Request.Seed,
			Timeout:           time.Duration(cfg.TimeoutMs) * time.Millisecond,
			Retry:             cfg.Retry,
			Capture:           cfg.CaptureBodies,
			TokenizerStrategy: tokens.Strategy(cfg.TokenizerStrategy),
			CaseID:            p.ID,
			CaseName:          p.Name,
			Meta:              meta,
		}
	}

	runModel := func(ms ModelSpec) {
		sess.SetCurrentModel(ms.Model)
		switch cfg.Load.Mode {
		case "fixed":
			m.runFixed(ctx, sess, ms.Model, cfg.Load.TotalRequests, cfg.Load.Concurrency, func(idx, worker int) engine.ExecRequest {
				return makeReq(ms, prompts[0], idx, worker, cfg.Load.Concurrency)
			})
		case "duration":
			sess.SetPhase("持续压测 " + ms.Model)
			m.runDuration(ctx, sess, ms.Model, time.Duration(cfg.Load.DurationSec)*time.Second, cfg.Load.Concurrency, func(idx, worker int) engine.ExecRequest {
				return makeReq(ms, prompts[0], idx, worker, cfg.Load.Concurrency)
			})
		case "ramp":
			for level := cfg.Load.RampStart; level <= cfg.Load.RampEnd; level += cfg.Load.RampStep {
				if ctx.Err() != nil {
					break
				}
				sess.SetPhase(fmt.Sprintf("%s 并发 %d", ms.Model, level))
				m.runDuration(ctx, sess, ms.Model, time.Duration(cfg.Load.RampStepSec)*time.Second, level, func(idx, worker int) engine.ExecRequest {
					return makeReq(ms, prompts[0], idx, worker, level)
				})
			}
		}
	}

	if cfg.ParallelModels {
		var wg sync.WaitGroup
		for _, ms := range cfg.Models {
			ms := ms
			wg.Add(1)
			go func() {
				defer wg.Done()
				runModel(ms)
			}()
		}
		wg.Wait()
	} else {
		for _, ms := range cfg.Models {
			if ctx.Err() != nil {
				break
			}
			runModel(ms)
		}
	}
	return ctx.Err()
}

func (m *Module) runFixed(ctx context.Context, sess *engine.Session, modelName string, total, conc int, makeReq func(idx, worker int) engine.ExecRequest) {
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < conc; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for idx := range jobs {
				if ctx.Err() != nil {
					return
				}
				sess.Exec(ctx, makeReq(idx, worker))
			}
		}(w)
	}
feed:
	for i := 0; i < total; i++ {
		select {
		case jobs <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
}

func (m *Module) runDuration(ctx context.Context, sess *engine.Session, modelName string, dur time.Duration, conc int, makeReq func(idx, worker int) engine.ExecRequest) {
	deadline := time.Now().Add(dur)
	var wg sync.WaitGroup
	var counter int64
	var mu sync.Mutex
	for w := 0; w < conc; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for {
				if ctx.Err() != nil || time.Now().After(deadline) {
					return
				}
				mu.Lock()
				idx := int(counter)
				counter++
				mu.Unlock()
				sess.Exec(ctx, makeReq(idx, worker))
			}
		}(w)
	}
	wg.Wait()
}

func (m *Module) Summary(sess *engine.Session) model.RunSummary {
	agg, models := sess.FinalAggregate()
	base := sess.Headline()
	canceled := agg.Canceled
	if sess.StopRequested() && base.Planned > 0 {
		left := base.Planned - agg.Total
		if left > canceled {
			canceled = left
		}
	}
	agg.Canceled = canceled
	base.Canceled = canceled
	base.QPS = agg.QPS
	base.RPM = agg.RPM
	base.SuccessRate = agg.SuccessRate
	base.ElapsedMs = agg.ElapsedMs
	base.Cost = agg.Tokens.Cost
	base.Data = map[string]any{
		"aggregate": agg,
		"models":    models,
		"planned":   base.Planned,
	}
	return base
}
