package authenticity

import (
	"fmt"
	"sync"
	"time"

	"apiunit/internal/engine"
)

// LiveModel is a compact, progress-oriented view of one model. It is pushed
// through Session.Extra so the UI can show the current stage and the evidence
// of every completed/failed check while the run is still in flight.
type LiveModel struct {
	TargetID   string        `json:"targetId"`
	TargetName string        `json:"targetName"`
	Model      string        `json:"model"`
	Protocol   string        `json:"protocol"`
	Repetition int           `json:"repetition"`
	TotalReps  int           `json:"totalRepetitions"`
	Stage      string        `json:"stage"`
	Done       bool          `json:"done"`
	Verdict    string        `json:"verdict,omitempty"`
	Error      string        `json:"error,omitempty"`
	StartedAt  int64         `json:"startedAt"`
	ElapsedMs  int64         `json:"elapsedMs"`
	Checks     []CheckReport `json:"checks"`
}

type livePublisher struct {
	mu      sync.Mutex
	sess    *engine.Session
	started time.Time
	order   []string
	entries map[string]*LiveModel
}

func liveKey(j job) string {
	return j.target.ID + "\x00" + j.model + "\x00" + fmt.Sprintf("%d", j.repetition)
}

func newLivePublisher(sess *engine.Session, jobs []job) *livePublisher {
	p := &livePublisher{
		sess:    sess,
		started: time.Now(),
		order:   make([]string, 0, len(jobs)),
		entries: make(map[string]*LiveModel, len(jobs)),
	}
	for _, j := range jobs {
		k := liveKey(j)
		p.order = append(p.order, k)
		p.entries[k] = &LiveModel{
			TargetID:   j.target.ID,
			TargetName: j.target.Name,
			Model:      j.model,
			Protocol:   string(resolveProtocol(j.target, j.spec, j.model)),
			Repetition: j.repetition,
			TotalReps:  j.totalRepetitions,
			Stage:      "等待调度",
			StartedAt:  0,
			Checks:     []CheckReport{},
		}
	}
	p.publishLocked()
	return p
}

func (p *livePublisher) publishLocked() {
	if p == nil || p.sess == nil {
		return
	}
	out := make([]LiveModel, 0, len(p.order))
	now := time.Now()
	for _, k := range p.order {
		e := p.entries[k]
		if e == nil {
			continue
		}
		cp := *e
		cp.Checks = append([]CheckReport(nil), e.Checks...)
		if cp.StartedAt > 0 {
			cp.ElapsedMs = now.Sub(time.UnixMilli(cp.StartedAt)).Milliseconds()
		}
		out = append(out, cp)
	}
	p.sess.SetExtra("authenticityLive", out)
}

func (p *livePublisher) start(j job, stage string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if e := p.entries[liveKey(j)]; e != nil {
		e.StartedAt = time.Now().UnixMilli()
		e.Stage = stage
	}
	p.publishLocked()
}

func (p *livePublisher) setStage(j job, stage string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if e := p.entries[liveKey(j)]; e != nil {
		e.Stage = stage
	}
	p.publishLocked()
}

func (p *livePublisher) addCheck(j job, c CheckReport) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if e := p.entries[liveKey(j)]; e != nil {
		e.Checks = append(e.Checks, c)
	}
	p.publishLocked()
}

func (p *livePublisher) setError(j job, msg string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if e := p.entries[liveKey(j)]; e != nil {
		e.Error = msg
	}
	p.publishLocked()
}

func (p *livePublisher) finish(j job, verdict, errMsg string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if e := p.entries[liveKey(j)]; e != nil {
		e.Done = true
		e.Verdict = verdict
		e.Stage = "完成"
		if errMsg != "" {
			e.Error = errMsg
		}
	}
	p.publishLocked()
}
