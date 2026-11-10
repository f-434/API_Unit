package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"

	"apiunit/internal/model"
	"apiunit/internal/protocol"
	"apiunit/internal/store"
	"apiunit/internal/tokens"
)

const (
	maxTextCapture   = 64 << 10
	maxRequestCapture = 8 << 20
	maxBodyCapture   = 256 << 10
	itemChannelCap = 4096
	batchSize      = 300
)

type RetryPolicy struct {
	MaxRetries    int  `json:"maxRetries"`
	BackoffMs     int  `json:"backoffMs"`
	MaxBackoffMs  int  `json:"maxBackoffMs"`
	RetryOn429    bool `json:"retryOn429"`
	RetryOn5xx    bool `json:"retryOn5xx"`
	RetryOnNetwork bool `json:"retryOnNetwork"`
}

type ExecRequest struct {
	Target    protocol.TargetInfo
	Model     string
	Protocol  model.Protocol
	Messages  []protocol.ChatMessage
	MaxTokens int
	Temperature *float64
	TopP      *float64
	Stream    bool
	Stop      []string
	Seed      *int
	Timeout   time.Duration
	Retry     RetryPolicy
	Capture   bool

	TokenizerStrategy tokens.Strategy
	LocalPrompt       bool

	CaseID     string
	CaseName   string
	Category   string
	Severity   string
	Repetition int
	Meta       map[string]any

	// PostProcess runs after the response is parsed and before the item is
	// recorded/persisted. Modules use it to attach verdicts or judge results.
	PostProcess func(ctx context.Context, it *model.Item)

	// RequestOverride allows a module to send a fully customized request
	// (tools, history, multimodal parts, safety settings, ...).
	RequestOverride *protocol.ChatRequest
}

type Session struct {
	RunID string
	Kind  model.RunKind

	store   *store.Store
	client  *protocol.Client
	counter *tokens.Counter

	ctx    context.Context
	cancel context.CancelFunc
	started time.Time

	live *liveState
	hub  *Hub

	itemCh     chan model.Item
	itemsOpen  atomic.Bool
	writerDone chan struct{}
	pumpStop   chan struct{}
	pumpWG     sync.WaitGroup

	pumpOnce sync.Once
	stopFlag atomic.Bool
	finished atomic.Bool

	inflight atomic.Int64
	seq      atomic.Int64

	limiter   *rate.Limiter
	limiterMu sync.Mutex

	prices     []model.Price
	persistErr atomic.Value
}

func NewSession(runID string, kind model.RunKind, st *store.Store, client *protocol.Client, counter *tokens.Counter, prices []model.Price) *Session {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Session{
		RunID:     runID,
		Kind:      kind,
		store:     st,
		client:    client,
		counter:   counter,
		ctx:       ctx,
		cancel:    cancel,
		started:   time.Now(),
		live:      newLiveState(runID, string(kind)),
		hub:       newHub(),
		itemCh:     make(chan model.Item, itemChannelCap),
		writerDone: make(chan struct{}),
		pumpStop:   make(chan struct{}),
		prices:     prices,
	}
	s.itemsOpen.Store(true)
	return s
}

func (s *Session) StartPumps() {
	go s.writerLoop()
	s.pumpWG.Add(2)
	go func() { defer s.pumpWG.Done(); s.samplerLoop() }()
	go func() { defer s.pumpWG.Done(); s.broadcastLoop() }()
}

func (s *Session) Context() context.Context { return s.ctx }
func (s *Session) StartedAt() time.Time     { return s.started }
func (s *Session) ElapsedMs() int64         { return time.Since(s.started).Milliseconds() }

func (s *Session) SetRateLimit(rps float64) {
	s.limiterMu.Lock()
	defer s.limiterMu.Unlock()
	if rps > 0 {
		burst := int(rps)
		if burst < 1 {
			burst = 1
		}
		s.limiter = rate.NewLimiter(rate.Limit(rps), burst)
	} else {
		s.limiter = nil
	}
}

func (s *Session) waitLimit(ctx context.Context) error {
	s.limiterMu.Lock()
	lim := s.limiter
	s.limiterMu.Unlock()
	if lim == nil {
		return nil
	}
	return lim.Wait(ctx)
}

func (s *Session) SetPlanned(n int64)    { s.live.setPlanned(n) }
func (s *Session) AddPlanned(n int64)    { s.live.mu.Lock(); s.live.planned += n; s.live.mu.Unlock() }
func (s *Session) SetPhase(p string)     { s.live.setPhase(p) }
func (s *Session) SetCurrentModel(m string) { s.live.setCurrentModel(m) }
func (s *Session) AddExtra(key string, delta int64) { s.live.addExtra(key, delta) }
func (s *Session) SetExtra(key string, v any)       { s.live.setExtra(key, v) }
func (s *Session) GetExtra(key string) any {
	s.live.mu.Lock()
	defer s.live.mu.Unlock()
	return s.live.extra[key]
}
func (s *Session) AddCanceled(n int64)   { s.live.addCanceled(n) }
func (s *Session) Stop() {
	s.stopFlag.Store(true)
	s.cancel()
}

func (s *Session) StopRequested() bool { return s.stopFlag.Load() }
func (s *Session) Err() error          { return s.ctx.Err() }
func (s *Session) Inflight() int64     { return s.inflight.Load() }

func (s *Session) BaseSummary() model.RunSummary { return s.live.headline(float64(s.ElapsedMs())) }

func (s *Session) Headline() model.RunSummary { return s.live.headline(float64(s.ElapsedMs())) }

func (s *Session) FinalAggregate() (model.Aggregate, []model.ModelAggregate) {
	return s.live.finalAggregate(float64(s.ElapsedMs()))
}

func (s *Session) LiveSnapshot() *model.Live {
	l := s.live.snapshot(false)
	return &l
}

func (s *Session) Subscribe() (int, chan []byte, []byte, bool) {
	id, ch, ok := s.hub.subscribe()
	if !ok {
		return 0, nil, nil, false
	}
	live := s.live.snapshot(false)
	live.Status = s.live.currentStatus()
	return id, ch, frame("snapshot", live), true
}

func (s *Session) Unsubscribe(id int) { s.hub.unsubscribe(id) }

func (s *Session) publishFinal(status model.RunStatus, errMsg string) {
	s.live.setStatus(string(status))
	if errMsg != "" {
		s.live.setError(errMsg)
	}
	s.live.sampleActive(0)
	live := s.live.snapshot(true)
	live.Status = string(status)
	live.ActiveConcurrency = 0
	s.hub.broadcast(frame("snapshot", live))
	done := map[string]any{"runId": s.RunID, "status": string(status), "error": errMsg}
	s.hub.broadcast(frame("done", done))
	s.hub.close()
}

func (s *Session) broadcastLoop() {
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-s.pumpStop:
			return
		case <-t.C:
			if !s.hub.hasSubscribers() {
				continue
			}
			live := s.live.snapshot(false)
			live.Status = s.live.currentStatus()
			if b, err := json.Marshal(live); err == nil {
				s.hub.broadcast([]byte("event: update\ndata: " + string(b) + "\n\n"))
			}
		}
	}
}

func (s *Session) samplerLoop() {
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-s.pumpStop:
			return
		case <-t.C:
			s.live.sampleActive(s.inflight.Load())
		}
	}
}

func (s *Session) writerLoop() {
	defer close(s.writerDone)
	batch := make([]model.Item, 0, batchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := s.store.InsertItems(batch); err != nil {
			log.Printf("persist run %s items: %v", s.RunID, err)
			s.persistErr.Store(err.Error())
		}
		batch = batch[:0]
	}
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case it, ok := <-s.itemCh:
			if !ok {
				flush()
				return
			}
			batch = append(batch, it)
			if len(batch) >= batchSize {
				flush()
			}
		case <-t.C:
			flush()
		}
	}
}

func (s *Session) closeItems() {
	if s.itemsOpen.CompareAndSwap(true, false) {
		close(s.itemCh)
	}
	<-s.writerDone
}

func (s *Session) stopPumps() {
	s.pumpOnce.Do(func() {
		close(s.pumpStop)
		s.pumpWG.Wait()
	})
}

// Exec performs one logical request (with optional retries) and records it.
// The boolean return is false when the request was canceled by a run stop.
func (s *Session) Exec(ctx context.Context, req ExecRequest) (model.Item, bool) {
	startOffset := float64(time.Since(s.started).Microseconds()) / 1000.0
	index := int(s.seq.Add(1) - 1)
	startedAt := model.NowMs()
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	strategy := req.TokenizerStrategy
	if strategy == "" {
		strategy = tokens.StrategyAuto
	}
	maxAttempts := req.Retry.MaxRetries + 1
	backoff := req.Retry.BackoffMs
	if backoff <= 0 {
		backoff = 500
	}
	maxBackoff := req.Retry.MaxBackoffMs
	if maxBackoff <= 0 {
		maxBackoff = 5000
	}

	var chatReq protocol.ChatRequest
	if req.RequestOverride != nil {
		chatReq = *req.RequestOverride
		if len(chatReq.Messages) == 0 {
			chatReq.Messages = req.Messages
		}
		if req.MaxTokens > 0 {
			chatReq.MaxTokens = req.MaxTokens
		}
		if req.Temperature != nil {
			chatReq.Temperature = req.Temperature
		}
		if req.TopP != nil {
			chatReq.TopP = req.TopP
		}
		if len(req.Stop) > 0 {
			chatReq.Stop = req.Stop
		}
		if req.Seed != nil {
			chatReq.Seed = req.Seed
		}
	} else {
		chatReq = protocol.ChatRequest{
			Model:       req.Model,
			Messages:    req.Messages,
			MaxTokens:   req.MaxTokens,
			Temperature: req.Temperature,
			TopP:        req.TopP,
			Stream:      req.Stream,
			Stop:        req.Stop,
			Seed:        req.Seed,
		}
	}
	chatReq.Model = req.Model
	chatReq.Stream = req.Stream
	msgs := chatReq.Messages
	target := req.Target
	if target.Protocol == "" {
		target.Protocol = req.Protocol
	}

	s.inflight.Add(1)
	defer s.inflight.Add(-1)

	attemptStart := time.Now()
	var out *protocol.Outcome
	var lastErr error
	attempts := 0
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		attempts = attempt
		if err := s.waitLimit(ctx); err != nil {
			lastErr = err
			break
		}
		actx, cancel := context.WithTimeout(ctx, timeout)
		attemptStart = time.Now()
		out, lastErr = s.client.Do(actx, target, chatReq)
		cancel()
		if lastErr == nil && out != nil && out.OK {
			break
		}
		if attempt >= maxAttempts || !shouldRetry(out, lastErr, req.Retry, ctx) {
			break
		}
		delay := time.Duration(backoff) * time.Millisecond
		for i := 1; i < attempt; i++ {
			delay *= 2
			if delay > time.Duration(maxBackoff)*time.Millisecond {
				delay = time.Duration(maxBackoff) * time.Millisecond
				break
			}
		}
		if ra := retryAfter(out); ra > delay {
			delay = ra
		}
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			lastErr = ctx.Err()
		}
		if ctx.Err() != nil {
			break
		}
	}

	canceled := errors.Is(ctx.Err(), context.Canceled) || errors.Is(lastErr, context.Canceled)
	if canceled {
		s.live.addCanceled(1)
		it := model.Item{
			RunID: s.RunID, Index: index, Attempts: attempts, TargetID: target.ID, TargetName: target.Name,
			Model: req.Model, Protocol: string(resolveProtocolName(target, req)), StartedAt: startedAt,
			StartOffsetMs: startOffset, Status: 0, OK: false, ErrorType: "canceled", ErrorMessage: "请求已取消",
			CaseID: req.CaseID, CaseName: req.CaseName, Category: req.Category, Severity: req.Severity,
			Repetition: req.Repetition, Meta: copyMeta(req.Meta),
		}
		return it, false
	}

	it := model.Item{
		RunID: s.RunID, Index: index, Attempts: attempts,
		TargetID: target.ID, TargetName: target.Name, Model: req.Model,
		Protocol: string(resolveProtocolName(target, req)),
		CaseID: req.CaseID, CaseName: req.CaseName, Category: req.Category, Severity: req.Severity,
		Repetition: req.Repetition, StartedAt: startedAt, StartOffsetMs: startOffset,
		Meta: copyMeta(req.Meta),
	}
	if out != nil {
		it.Status = out.Status
		it.OK = out.OK
		it.LatencyMs = out.LatencyMs
		it.TTFTMs = out.TTFTMs
		it.GenerationMs = out.GenerationMs
		it.RequestID = out.RequestID
		it.FinishReason = out.FinishReason
		it.PromptTokens = out.Usage.Prompt
		it.CompletionTokens = out.Usage.Completion
		it.TotalTokens = out.Usage.Total
		it.CachedTokens = out.Usage.Cached
		it.CacheWriteTokens = out.Usage.CacheWrite
		it.ReasoningTokens = out.Usage.Reasoning
	}
	if it.LatencyMs == 0 {
		it.LatencyMs = float64(time.Since(attemptStart).Microseconds()) / 1000.0
	}

	estimated := false
	localTokens := 0
	localSource := ""
	needLocal := req.LocalPrompt || it.PromptTokens == 0
	if needLocal && len(msgs) > 0 && strategy != tokens.StrategyNone {
		if strategy == tokens.StrategyOnline {
			octx, ocancel := context.WithTimeout(ctx, 20*time.Second)
			r, oerr := s.counter.CountOnline(octx, target, req.Model, msgs)
			ocancel()
			if oerr == nil {
				localTokens = r.Tokens
				localSource = r.Source
				if it.PromptTokens == 0 {
					it.PromptTokens = r.Tokens
					estimated = true
				}
			} else {
				r = s.counter.CountChat(req.Model, msgs, tokens.StrategyAuto)
				localTokens = r.Tokens
				localSource = r.Source
				it.Meta = ensureMeta(it.Meta)
				it.Meta["onlineCountError"] = oerr.Error()
				if it.PromptTokens == 0 {
					it.PromptTokens = r.Tokens
					estimated = true
				}
			}
		} else {
			r := s.counter.CountChat(req.Model, msgs, strategy)
			localTokens = r.Tokens
			localSource = r.Source
			if it.PromptTokens == 0 {
				it.PromptTokens = r.Tokens
				estimated = true
			}
		}
	}
	if it.CompletionTokens == 0 && out != nil {
		textToCount := out.Text
		if textToCount == "" {
			textToCount = out.Reasoning
		}
		if textToCount != "" {
			r := s.counter.CountText(req.Model, textToCount, strategy)
			it.CompletionTokens = r.Tokens
			estimated = true
		}
	}
	if it.TotalTokens < it.PromptTokens+it.CompletionTokens {
		it.TotalTokens = it.PromptTokens + it.CompletionTokens
	}
	switch {
	case out != nil && out.Usage.Reported && out.Usage.PromptKnown && out.Usage.CompKnown:
		if estimated {
			it.TokenSource = "mixed"
		} else {
			it.TokenSource = "usage"
		}
	case it.PromptTokens > 0 || it.CompletionTokens > 0:
		if estimated || (out != nil && !out.Usage.Reported) {
			it.TokenSource = "estimated"
		} else {
			it.TokenSource = "mixed"
		}
	default:
		it.TokenSource = "none"
	}
	it.LocalPromptTokens = localTokens
	it.Cost = s.costFor(req.Model, it)
	if out != nil && out.GenerationMs > 0 {
		if it.CompletionTokens > 1 {
			it.TPOTMs = out.GenerationMs / float64(it.CompletionTokens-1)
		} else if out.TextChunks > 1 {
			it.TPOTMs = out.GenerationMs / float64(out.TextChunks-1)
		}
	}
	if out != nil {
		text := out.Text
		if text == "" {
			text = out.Reasoning
		}
		it.OutputPreview = preview(text, 600)
		if req.Capture {
			it.OutputText = truncateBytes(text, maxTextCapture)
			it.RequestJSON = truncateBytes(out.RequestJSON, maxRequestCapture)
			it.ResponseJSON = truncateBytes(out.ResponseJSON, maxBodyCapture)
		}
	}

	if !it.OK {
		it.ErrorType, it.ErrorMessage = classifyFailure(out, lastErr, &it)
	}
	if it.Meta == nil {
		it.Meta = map[string]any{}
	}
	it.Meta["attempts"] = attempts
	it.Meta["localPromptTokens"] = localTokens
	if localSource != "" {
		it.Meta["localSource"] = localSource
	}
	if out != nil {
		it.Meta["endpoint"] = out.Endpoint
		it.Meta["responseBytes"] = out.ResponseBytes
		it.Meta["chunkCount"] = out.ChunkCount
		it.Meta["usageReported"] = out.Usage.Reported
		it.Meta["promptUsage"] = out.Usage.PromptKnown
		it.Meta["completionUsage"] = out.Usage.CompKnown
		if out.Truncated {
			it.Meta["truncated"] = true
		}
		if out.ErrorCode != "" {
			it.Meta["errorCode"] = out.ErrorCode
		}
		if out.UpstreamModel != "" && out.UpstreamModel != req.Model {
			it.Meta["upstreamModel"] = out.UpstreamModel
		}
	}
	if s.persistErr.Load() != nil {
		it.Meta["persistWarning"] = s.persistErr.Load()
	}

	endpoint := ""
	if out != nil {
		endpoint = out.Endpoint
	}
	if req.PostProcess != nil {
		req.PostProcess(ctx, &it)
	}
	s.live.record(it, endpoint)
	if s.itemsOpen.Load() {
		select {
		case s.itemCh <- it:
		default:
			// Never block generation on the writer; persistence is best-effort
			// once the buffer is saturated.
			s.live.addExtra("persistDropped", 1)
		}
	}
	return it, true
}

func resolveProtocolName(t protocol.TargetInfo, req ExecRequest) model.Protocol {
	if req.Protocol != "" && req.Protocol != model.ProtocolAuto {
		return req.Protocol
	}
	if t.Protocol != "" && t.Protocol != model.ProtocolAuto {
		return t.Protocol
	}
	return model.InferProtocol(req.Model)
}

func (s *Session) EstimateCost(modelName string, prompt, completion, cached, write int) float64 {
	return s.costFor(modelName, model.Item{
		PromptTokens: prompt, CompletionTokens: completion,
		CachedTokens: cached, CacheWriteTokens: write,
	})
}

func (s *Session) costFor(modelName string, it model.Item) float64 {
	price, ok := matchPrice(s.prices, modelName)
	if !ok {
		return 0
	}
	cached := float64(it.CachedTokens)
	write := float64(it.CacheWriteTokens)
	prompt := float64(it.PromptTokens) - cached - write
	if prompt < 0 {
		prompt = 0
	}
	cost := prompt/1e6*price.InputPerM + cached/1e6*price.CacheReadPerM +
		write/1e6*price.CacheWritePerM + float64(it.CompletionTokens)/1e6*price.OutputPerM
	return cost
}

func matchPrice(prices []model.Price, modelName string) (model.Price, bool) {
	m := strings.ToLower(strings.TrimSpace(modelName))
	last := m
	if i := strings.LastIndexAny(m, "/:"); i >= 0 {
		last = m[i+1:]
	}
	for _, p := range prices {
		pat := strings.ToLower(strings.TrimSpace(p.Pattern))
		if pat == "" {
			continue
		}
		if pat == m || pat == last {
			return p, true
		}
		if strings.Contains(pat, "*") && (wildcardMatch(pat, m) || wildcardMatch(pat, last)) {
			return p, true
		}
	}
	return model.Price{}, false
}

func wildcardMatch(pattern, s string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == s
	}
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	for i := 1; i < len(parts)-1; i++ {
		if parts[i] == "" {
			continue
		}
		idx := strings.Index(s, parts[i])
		if idx < 0 {
			return false
		}
		s = s[idx+len(parts[i]):]
	}
	tail := parts[len(parts)-1]
	return strings.HasSuffix(s, tail)
}

func shouldRetry(out *protocol.Outcome, err error, p RetryPolicy, ctx context.Context) bool {
	if p.MaxRetries <= 0 || ctx.Err() != nil {
		return false
	}
	if err != nil {
		if !p.RetryOnNetwork {
			return false
		}
		return !errors.Is(err, context.Canceled)
	}
	if out == nil {
		return false
	}
	switch out.Status {
	case 429:
		return p.RetryOn429
	case 408, 409, 425:
		return p.RetryOn5xx
	}
	if out.Status >= 500 {
		return p.RetryOn5xx
	}
	if out.Status == 0 {
		return p.RetryOnNetwork
	}
	// Protocol/stream hiccups on a 2xx are worth one retry when enabled.
	if out.Status >= 200 && out.Status < 300 && out.ErrorMessage != "" && (out.StreamError || out.ErrorCode == "protocol") {
		return p.RetryOn5xx
	}
	return false
}

func retryAfter(out *protocol.Outcome) time.Duration {
	if out == nil || out.Header == nil {
		return 0
	}
	v := strings.TrimSpace(out.Header.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if sec, err := strconv.Atoi(v); err == nil && sec > 0 && sec <= 10 {
		return time.Duration(sec) * time.Second
	}
	if t, err := time.Parse(time.RFC1123, v); err == nil {
		d := time.Until(t)
		if d > 0 && d <= 10*time.Second {
			return d
		}
	}
	return 0
}

func classifyFailure(out *protocol.Outcome, err error, it *model.Item) (string, string) {
	if err != nil {
		msg := err.Error()
		if out != nil && out.ErrorMessage != "" {
			msg = out.ErrorMessage
		}
		if errors.Is(err, context.DeadlineExceeded) || protocol.IsTimeout(err) {
			return "timeout", msg
		}
		return "network", msg
	}
	if out == nil {
		return "network", "没有收到响应"
	}
	if out.Status >= 400 {
		msg := out.ErrorMessage
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", out.Status)
		}
		if out.ErrorCode != "" && !strings.Contains(msg, out.ErrorCode) {
			msg = "[" + out.ErrorCode + "] " + msg
		}
		switch out.Status {
		case 401, 403:
			if strings.Contains(strings.ToLower(out.ErrorCode+" "+msg), "quota") {
				return "quota", msg
			}
			return "auth", msg
		case 429:
			return "rate_limit", msg
		case 404:
			return "not_found", msg
		case 503:
			if strings.Contains(strings.ToLower(out.ErrorCode+" "+msg), "model_not_found") ||
				strings.Contains(strings.ToLower(msg), "no available channel") {
				return "no_channel", msg
			}
			return "server", msg
		case 400, 422:
			return "bad_request", msg
		}
		if out.Status >= 500 {
			return "server", msg
		}
		return "client", msg
	}
	msg := out.ErrorMessage
	if out.ErrorCode != "" && !strings.Contains(msg, out.ErrorCode) {
		msg = "[" + out.ErrorCode + "] " + msg
	}
	switch {
	case out.StreamError:
		return "stream", msg
	case out.ErrorCode == "parse":
		return "parse", msg
	case out.ErrorCode == "prompt_blocked":
		return "blocked", msg
	case out.ErrorCode == "protocol":
		return "protocol", msg
	default:
		return "api", msg
	}
}

func preview(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	return truncateRunes(s, n)
}

func truncateBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func ensureMeta(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func copyMeta(m map[string]any) map[string]any {
	if len(m) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(m)+4)
	for k, v := range m {
		out[k] = v
	}
	return out
}

func frame(event string, payload any) []byte {
	b, err := json.Marshal(payload)
	if err != nil {
		b = []byte(`{}`)
	}
	return []byte("event: " + event + "\ndata: " + string(b) + "\n\n")
}

func (l *liveState) currentStatus() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.status == "" {
		return string(model.StatusRunning)
	}
	return l.status
}

// FinishSession stops pumps, flushes results and publishes the final state.
func (s *Session) FinishSession(status model.RunStatus, errMsg string) {
	s.finished.Store(true)
	s.stopPumps()
	s.closeItems()
	s.publishFinal(status, errMsg)
}
