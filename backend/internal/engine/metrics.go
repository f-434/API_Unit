package engine

import (
	"math"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"apiunit/internal/model"
)

const (
	liveSampleCap = 10000
	maxSeries     = 600
	maxRecent     = 50
)

type seriesAcc struct {
	started    int64
	completed  int64
	success    int64
	failed     int64
	latencySum float64
	latencyN   int64
	tokens     int64
}

type activeAcc struct {
	sum float64
	n   int64
}

type modelState struct {
	model      string
	targetID   string
	targetName string
	protocol   string
	endpoint   string

	completed int64
	success   int64
	failed    int64

	latencies    []float64
	latenciesSam []float64
	ttfts        []float64
	ttftsSam     []float64
	tpots        []float64
	tpotsSam     []float64
	errorLatency []float64

	prompt     int64
	completion int64
	total      int64
	cached     int64
	cacheWrite int64
	reasoning  int64
	estimated  int64
	cost       float64

	status map[int]int64
	errors map[string]*model.ErrorGroup

	first  time.Time
	last   time.Time
	tpsSum float64
	tpsN   int64
}

func newModelState(key string) *modelState {
	return &modelState{
		model:  key,
		status: map[int]int64{},
		errors: map[string]*model.ErrorGroup{},
	}
}

type liveState struct {
	mu sync.Mutex

	runID  string
	kind   string
	status string
	phase  string
	error  string

	started time.Time

	planned    int64
	canceled   int64
	completed  int64
	success    int64
	failed     int64
	active     int64
	lastActive int64
	err        string

	currentModel string
	models       map[string]*modelState
	order        []string

	recent []model.Item

	series        map[int]*seriesAcc
	activeSamples map[int]*activeAcc

	extra      map[string]any
	latencySam []float64
	ttftSam    []float64
	tpotSam    []float64
}

func newLiveState(runID, kind string) *liveState {
	return &liveState{
		runID:         runID,
		kind:          kind,
		status:        string(model.StatusRunning),
		started:       time.Now(),
		models:        map[string]*modelState{},
		series:        map[int]*seriesAcc{},
		activeSamples: map[int]*activeAcc{},
		extra:         map[string]any{},
	}
}

func (l *liveState) setStatus(s string) {
	l.mu.Lock()
	l.status = s
	l.mu.Unlock()
}

func (l *liveState) setPhase(s string) {
	l.mu.Lock()
	l.phase = s
	l.mu.Unlock()
}

func (l *liveState) setCurrentModel(m string) {
	l.mu.Lock()
	l.currentModel = m
	l.mu.Unlock()
}

func (l *liveState) setPlanned(n int64) {
	l.mu.Lock()
	l.planned = n
	l.mu.Unlock()
}

func (l *liveState) addCanceled(n int64) {
	l.mu.Lock()
	l.canceled += n
	l.mu.Unlock()
}

func (l *liveState) setError(msg string) {
	l.mu.Lock()
	l.err = msg
	l.mu.Unlock()
}

func (l *liveState) addExtra(key string, delta int64) {
	l.mu.Lock()
	if l.extra == nil {
		l.extra = map[string]any{}
	}
	cur, _ := l.extra[key].(int64)
	l.extra[key] = cur + delta
	l.mu.Unlock()
}

func (l *liveState) setExtra(key string, v any) {
	l.mu.Lock()
	if l.extra == nil {
		l.extra = map[string]any{}
	}
	l.extra[key] = v
	l.mu.Unlock()
}

func (l *liveState) sampleActive(active int64) {
	l.mu.Lock()
	l.active = active
	l.lastActive = active
	if elapsed := time.Since(l.started); elapsed > 0 {
		sec := int(elapsed / time.Second)
		acc := l.activeSamples[sec]
		if acc == nil {
			acc = &activeAcc{}
			l.activeSamples[sec] = acc
		}
		acc.sum += float64(active)
		acc.n++
	}
	l.mu.Unlock()
}

func (l *liveState) record(it model.Item, endpoint string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := it.Model
	if key == "" {
		key = "-"
	}
	ms := l.models[key]
	if ms == nil {
		ms = newModelState(key)
		ms.targetID = it.TargetID
		ms.targetName = it.TargetName
		ms.protocol = it.Protocol
		ms.endpoint = endpoint
		l.models[key] = ms
		l.order = append(l.order, key)
	}
	if it.OK {
		ms.success++
		l.success++
		ms.latencies = append(ms.latencies, it.LatencyMs)
		ms.latenciesSam = appendCapped(ms.latenciesSam, it.LatencyMs, liveSampleCap)
		l.latencySam = appendCapped(l.latencySam, it.LatencyMs, liveSampleCap)
		if it.TTFTMs > 0 {
			ms.ttfts = append(ms.ttfts, it.TTFTMs)
			ms.ttftsSam = appendCapped(ms.ttftsSam, it.TTFTMs, liveSampleCap)
			l.ttftSam = appendCapped(l.ttftSam, it.TTFTMs, liveSampleCap)
		}
		if it.TPOTMs > 0 {
			ms.tpots = append(ms.tpots, it.TPOTMs)
			ms.tpotsSam = appendCapped(ms.tpotsSam, it.TPOTMs, liveSampleCap)
			l.tpotSam = appendCapped(l.tpotSam, it.TPOTMs, liveSampleCap)
		}
	} else {
		ms.failed++
		l.failed++
		ms.errorLatency = append(ms.errorLatency, it.LatencyMs)
		groupError(ms.errors, it)
	}
	ms.completed++
	l.completed++
	ms.prompt += int64(it.PromptTokens)
	ms.completion += int64(it.CompletionTokens)
	ms.total += int64(it.TotalTokens)
	ms.cached += int64(it.CachedTokens)
	ms.cacheWrite += int64(it.CacheWriteTokens)
	ms.reasoning += int64(it.ReasoningTokens)
	ms.cost += it.Cost
	if it.TokenSource == "estimated" || it.TokenSource == "mixed" {
		ms.estimated++
	}
	ms.status[it.Status]++
	if ms.first.IsZero() {
		ms.first = time.Now()
	}
	ms.last = time.Now()
	if it.CompletionTokens > 0 && it.GenerationMs > 0 {
		ms.tpsSum += float64(it.CompletionTokens) / (it.GenerationMs / 1000.0)
		ms.tpsN++
	}
	l.currentModel = key

	startSec := maxInt(0, int(it.StartOffsetMs/1000))
	endSec := maxInt(startSec, int((it.StartOffsetMs+it.LatencyMs)/1000))
	ss := l.series[startSec]
	if ss == nil {
		ss = &seriesAcc{}
		l.series[startSec] = ss
	}
	ss.started++
	es := l.series[endSec]
	if es == nil {
		es = &seriesAcc{}
		l.series[endSec] = es
	}
	es.completed++
	if it.OK {
		es.success++
		es.latencySum += it.LatencyMs
		es.latencyN++
	} else {
		es.failed++
	}
	es.tokens += int64(it.CompletionTokens)

	l.recent = append(l.recent, it)
	if len(l.recent) > 200 {
		l.recent = l.recent[len(l.recent)-200:]
	}
}

func appendCapped(s []float64, v float64, cap int) []float64 {
	if len(s) >= cap {
		// Uniform replacement keeps the sample representative and O(1).
		s[rand.Intn(cap)] = v
		return s
	}
	return append(s, v)
}

func groupError(m map[string]*model.ErrorGroup, it model.Item) {
	typ := it.ErrorType
	if typ == "" {
		typ = "error"
	}
	line := firstLine(it.ErrorMessage, 160)
	if line == "" {
		line = "未知错误"
	}
	key := typ + "|" + line
	g := m[key]
	if g == nil {
		g = &model.ErrorGroup{Type: typ, Message: truncateRunes(it.ErrorMessage, 800)}
		m[key] = g
	}
	g.Count++
	if it.LatencyMs > 0 && g.FirstAtMs == 0 {
		g.FirstAtMs = it.StartOffsetMs
	}
	if !containsInt(g.Statuses, it.Status) && len(g.Statuses) < 8 {
		g.Statuses = append(g.Statuses, it.Status)
	}
	if it.Model != "" && !containsString(g.Models, it.Model) && len(g.Models) < 8 {
		g.Models = append(g.Models, it.Model)
	}
}

func (l *liveState) snapshot(final bool) model.Live {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(l.started)
	total := l.completed
	status := l.status
	if status == "" {
		status = string(model.StatusRunning)
	}
	if final && status == string(model.StatusRunning) {
		status = string(model.StatusCompleted)
	}
	out := model.Live{
		RunID:             l.runID,
		Kind:              l.kind,
		Status:            string(status),
		Phase:             l.phase,
		ElapsedMs:         elapsed.Milliseconds(),
		Planned:           l.planned,
		Completed:         l.completed,
		Success:           l.success,
		Failed:            l.failed,
		Canceled:          l.canceled,
		SuccessRate:       pct(l.success, total),
		QPS:               perSecond(l.success, elapsed),
		RPM:               perMinute(l.success, elapsed),
		ActiveConcurrency: int(l.lastActive),
		CurrentModel:      l.currentModel,
		StatusCodes:       statusCodes(l.totalStatus()),
		Errors:            topErrors(l.allErrors(), 20),
		Series:            l.buildSeries(),
		Models:            l.modelLives(elapsed),
		Recent:            l.recentItems(),
		Error:             l.err,
		Extra:             copyExtra(l.extra),
	}
	sample := l.latencySam
	if final {
		sample = l.latencies()
	}
	out.Latency = statsOf(sample)
	out.TTFT = statsOf(l.ttftsFor(final))
	out.TPOT = statsOf(l.tpotsFor(final))
	out.Tokens = l.tokens()
	out.Histogram = histogram(sample)
	return out
}

func (l *liveState) latencies() []float64 {
	all := make([]float64, 0, l.completed)
	for _, k := range l.order {
		all = append(all, l.models[k].latencies...)
	}
	return all
}

func (l *liveState) ttftsFor(final bool) []float64 {
	if !final {
		return l.ttftSam
	}
	var all []float64
	for _, k := range l.order {
		all = append(all, l.models[k].ttfts...)
	}
	return all
}

func (l *liveState) tpotsFor(final bool) []float64 {
	if !final {
		return l.tpotSam
	}
	var all []float64
	for _, k := range l.order {
		all = append(all, l.models[k].tpots...)
	}
	return all
}

func (l *liveState) totalStatus() map[int]int64 {
	m := map[int]int64{}
	for _, k := range l.order {
		for st, n := range l.models[k].status {
			m[st] += n
		}
	}
	return m
}

func (l *liveState) allErrors() map[string]*model.ErrorGroup {
	m := map[string]*model.ErrorGroup{}
	for _, k := range l.order {
		for key, g := range l.models[k].errors {
			if ex, ok := m[key]; ok {
				ex.Count += g.Count
			} else {
				cp := *g
				m[key] = &cp
			}
		}
	}
	return m
}

func (l *liveState) tokens() model.TokenStats {
	var t model.TokenStats
	for _, k := range l.order {
		ms := l.models[k]
		t.Prompt += ms.prompt
		t.Completion += ms.completion
		t.Total += ms.total
		t.Cached += ms.cached
		t.CacheWrite += ms.cacheWrite
		t.Reasoning += ms.reasoning
		t.Estimated += ms.estimated
		t.Cost += ms.cost
		if ms.completed > 0 {
			t.AvgCompletion += float64(ms.completion)
		}
	}
	if l.completed > 0 {
		t.AvgCompletion /= float64(l.completed)
	}
	var tpsSum float64
	var tpsN int64
	for _, k := range l.order {
		tpsSum += l.models[k].tpsSum
		tpsN += l.models[k].tpsN
	}
	if tpsN > 0 {
		t.AvgTPS = tpsSum / float64(tpsN)
	}
	return t
}

func (l *liveState) modelLives(elapsed time.Duration) []model.ModelLive {
	out := make([]model.ModelLive, 0, len(l.order))
	for _, k := range l.order {
		ms := l.models[k]
		d := elapsed
		if !ms.first.IsZero() && !ms.last.IsZero() {
			d = ms.last.Sub(ms.first)
		}
		if d <= 0 {
			d = elapsed
		}
		ml := model.ModelLive{
			Model:       ms.model,
			TargetName:  ms.targetName,
			Protocol:    ms.protocol,
			Completed:   ms.completed,
			Success:     ms.success,
			Failed:      ms.failed,
			SuccessRate: pct(ms.success, ms.completed),
			QPS:         perSecond(ms.success, d),
			RPM:         perMinute(ms.success, d),
		}
		if ms.success > 0 && len(ms.latenciesSam) > 0 {
			ml.AvgLatency = sum(ms.latenciesSam) / float64(len(ms.latenciesSam))
		}
		if len(ms.ttftsSam) > 0 {
			ml.TTFT = sum(ms.ttftsSam) / float64(len(ms.ttftsSam))
		}
		out = append(out, ml)
	}
	return out
}

func (l *liveState) recentItems() []model.Item {
	n := len(l.recent)
	if n == 0 {
		return nil
	}
	limit := maxRecent
	if n < limit {
		limit = n
	}
	out := make([]model.Item, 0, limit)
	for i := n - 1; i >= n-limit; i-- {
		it := l.recent[i]
		it.OutputText = ""
		it.RequestJSON = ""
		it.ResponseJSON = ""
		if len([]rune(it.OutputPreview)) > 600 {
			it.OutputPreview = truncateRunes(it.OutputPreview, 600)
		}
		out = append(out, it)
	}
	return out
}

func (l *liveState) buildSeries() []model.SeriesPoint {
	if len(l.series) == 0 && len(l.activeSamples) == 0 {
		return nil
	}
	maxSec := 0
	for s := range l.series {
		if s > maxSec {
			maxSec = s
		}
	}
	for s := range l.activeSamples {
		if s > maxSec {
			maxSec = s
		}
	}
	stride := 1
	if maxSec > maxSeries {
		stride = int(math.Ceil(float64(maxSec) / float64(maxSeries)))
	}
	points := make([]model.SeriesPoint, 0, maxSec/stride+1)
	for base := 0; base <= maxSec; base += stride {
		var p model.SeriesPoint
		p.Sec = base
		var latSum float64
		var latN int64
		var activeSum float64
		var activeN int64
		buckets := 1
		for s := base; s < base+stride; s++ {
			if s > base {
				buckets++
			}
			if a := l.series[s]; a != nil {
				p.Started += a.started
				p.Completed += a.completed
				p.Success += a.success
				p.Failed += a.failed
				latSum += a.latencySum
				latN += a.latencyN
			}
			if a := l.activeSamples[s]; a != nil {
				activeSum += a.sum
				activeN += a.n
			}
		}
		if activeN > 0 {
			p.ActiveConcurrency = activeSum / float64(activeN)
		}
		if latN > 0 {
			p.AvgLatencyMs = latSum / float64(latN)
		}
		if stride == 1 {
			p.RPS = float64(p.Success)
		} else {
			p.RPS = float64(p.Success) / float64(stride)
		}
		points = append(points, p)
	}
	return points
}

func (l *liveState) finalAggregate(elapsedMs float64) (model.Aggregate, []model.ModelAggregate) {
	l.mu.Lock()
	defer l.mu.Unlock()
	agg := model.Aggregate{
		Total:       l.completed,
		Success:     l.success,
		Failed:      l.failed,
		Canceled:    l.canceled,
		SuccessRate: pct(l.success, l.completed),
		ErrorRate:   pct(l.failed, l.completed),
		QPS:         perSecond(l.success, time.Duration(elapsedMs)*time.Millisecond),
		RPM:         perMinute(l.success, time.Duration(elapsedMs)*time.Millisecond),
		ElapsedMs:   elapsedMs,
		Latency:     statsOf(l.latencies()),
		TTFT:        statsOf(l.ttftsFor(true)),
		TPOT:        statsOf(l.tpotsFor(true)),
		Tokens:      l.tokens(),
		StatusCodes: statusCodes(l.totalStatus()),
		Errors:      topErrors(l.allErrors(), 60),
		Series:      l.buildSeries(),
		Histogram:   histogram(l.latencies()),
	}
	models := make([]model.ModelAggregate, 0, len(l.order))
	for _, k := range l.order {
		ms := l.models[k]
		m := model.ModelAggregate{
			Model:      ms.model,
			TargetID:   ms.targetID,
			TargetName: ms.targetName,
			Protocol:   ms.protocol,
			Endpoint:   ms.endpoint,
			Aggregate: model.Aggregate{
				Total:       ms.completed,
				Success:     ms.success,
				Failed:      ms.failed,
				SuccessRate: pct(ms.success, ms.completed),
				ErrorRate:   pct(ms.failed, ms.completed),
				Latency:     statsOf(ms.latencies),
				TTFT:        statsOf(ms.ttfts),
				TPOT:        statsOf(ms.tpots),
				Tokens: model.TokenStats{
					Prompt: ms.prompt, Completion: ms.completion, Total: ms.total,
					Cached: ms.cached, CacheWrite: ms.cacheWrite, Reasoning: ms.reasoning,
					Estimated: ms.estimated, Cost: ms.cost,
				},
				StatusCodes: statusCodes(ms.status),
				Errors:      topErrors(ms.errors, 60),
				Histogram:   histogram(ms.latencies),
			},
		}
		d := time.Duration(elapsedMs) * time.Millisecond
		if !ms.first.IsZero() && !ms.last.IsZero() {
			d = ms.last.Sub(ms.first)
		}
		if d <= 0 {
			d = time.Duration(elapsedMs) * time.Millisecond
		}
		m.QPS = perSecond(ms.success, d)
		m.RPM = perMinute(ms.success, d)
		if ms.completed > 0 {
			m.Tokens.AvgCompletion = float64(ms.completion) / float64(ms.completed)
		}
		if ms.tpsN > 0 {
			m.Tokens.AvgTPS = ms.tpsSum / float64(ms.tpsN)
		}
		models = append(models, m)
	}
	return agg, models
}

func (l *liveState) headline(elapsedMs float64) model.RunSummary {
	l.mu.Lock()
	defer l.mu.Unlock()
	var cost float64
	models := make([]string, 0, len(l.order))
	for _, k := range l.order {
		cost += l.models[k].cost
		models = append(models, k)
	}
	return model.RunSummary{
		Planned:     l.planned,
		Total:       l.completed,
		Success:     l.success,
		Failed:      l.failed,
		Canceled:    l.canceled,
		SuccessRate: pct(l.success, l.completed),
		QPS:         perSecond(l.success, time.Duration(elapsedMs)*time.Millisecond),
		RPM:         perMinute(l.success, time.Duration(elapsedMs)*time.Millisecond),
		ElapsedMs:   elapsedMs,
		Cost:        cost,
		Models:      models,
	}
}

func statsOf(v []float64) model.Stats {
	if len(v) == 0 {
		return model.Stats{}
	}
	cp := append([]float64(nil), v...)
	sort.Float64s(cp)
	st := model.Stats{Count: len(cp), Min: cp[0], Max: cp[len(cp)-1]}
	var sum float64
	for _, x := range cp {
		sum += x
	}
	st.Sum = sum
	st.Avg = sum / float64(len(cp))
	st.P50 = percentile(cp, 50)
	st.P90 = percentile(cp, 90)
	st.P95 = percentile(cp, 95)
	st.P99 = percentile(cp, 99)
	return st
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(p/100*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func histogram(v []float64) []model.HistBucket {
	if len(v) == 0 {
		return nil
	}
	maxV := 0.0
	for _, x := range v {
		if x > maxV {
			maxV = x
		}
	}
	if maxV <= 0 {
		return nil
	}
	ladder := []float64{1, 2, 5, 10, 20, 50, 100, 200, 500, 1000, 2000, 5000, 10000, 20000, 50000}
	step := ladder[len(ladder)-1]
	for _, s := range ladder {
		if maxV/s <= 40 {
			step = s
			break
		}
	}
	n := int(math.Ceil(maxV/step)) + 1
	if n > 60 {
		n = 60
		step = maxV / 60
	}
	buckets := make([]model.HistBucket, n)
	for i := 0; i < n; i++ {
		from := float64(i) * step
		to := from + step
		buckets[i] = model.HistBucket{
			Label: formatBucket(from, to),
			From:  from,
			To:    to,
		}
	}
	for _, x := range v {
		i := int(x / step)
		if i >= n {
			i = n - 1
		}
		if i < 0 {
			i = 0
		}
		buckets[i].Count++
	}
	return buckets
}

func formatBucket(from, to float64) string {
	return trimFloat(from) + "-" + trimFloat(to) + "ms"
}

func trimFloat(f float64) string {
	if f == math.Trunc(f) {
		return strconv.FormatFloat(f, 'f', 0, 64)
	}
	return strconv.FormatFloat(f, 'f', 1, 64)
}

func statusCodes(m map[int]int64) []model.StatusCount {
	out := make([]model.StatusCount, 0, len(m))
	for st, n := range m {
		out = append(out, model.StatusCount{Status: st, Count: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Status < out[j].Status })
	return out
}

func topErrors(m map[string]*model.ErrorGroup, limit int) []model.ErrorGroup {
	out := make([]model.ErrorGroup, 0, len(m))
	for _, g := range m {
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return out[i].Type < out[j].Type
		}
		return out[i].Count > out[j].Count
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func copyExtra(m map[string]any) map[string]any {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func pct(a, b int64) float64 {
	if b <= 0 {
		return 0
	}
	return float64(a) / float64(b) * 100
}

func perSecond(n int64, d time.Duration) float64 {
	if d <= 0 {
		return 0
	}
	return float64(n) / d.Seconds()
}

func perMinute(n int64, d time.Duration) float64 {
	if d <= 0 {
		return 0
	}
	return float64(n) / d.Minutes()
}

func sum(v []float64) float64 {
	var s float64
	for _, x := range v {
		s += x
	}
	return s
}

func firstLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	return truncateRunes(s, max)
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func containsInt(v []int, x int) bool {
	for _, y := range v {
		if y == x {
			return true
		}
	}
	return false
}

func containsString(v []string, x string) bool {
	for _, y := range v {
		if y == x {
			return true
		}
	}
	return false
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
