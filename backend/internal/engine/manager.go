package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"

	"apiunit/internal/model"
	"apiunit/internal/protocol"
	"apiunit/internal/store"
	"apiunit/internal/tokens"
)

type Module interface {
	Kind() model.RunKind
	Title() string
	Validate(raw json.RawMessage) (any, error)
	Run(ctx context.Context, sess *Session, cfg any) error
	Summary(sess *Session) model.RunSummary
}

type Manager struct {
	mu       sync.Mutex
	store    *store.Store
	client   *protocol.Client
	counter  *tokens.Counter
	maxRuns  int
	modules  map[model.RunKind]Module
	sessions map[string]*Session
	active   int
}

func NewManager(st *store.Store, client *protocol.Client, counter *tokens.Counter, maxRuns int) *Manager {
	if maxRuns < 1 {
		maxRuns = 1
	}
	return &Manager{
		store:    st,
		client:   client,
		counter:  counter,
		maxRuns:  maxRuns,
		modules:  map[model.RunKind]Module{},
		sessions: map[string]*Session{},
	}
}

func (m *Manager) Register(mod Module) { m.modules[mod.Kind()] = mod }

func (m *Manager) Kinds() []map[string]string {
	out := make([]map[string]string, 0, len(m.modules))
	for _, mod := range m.modules {
		out = append(out, map[string]string{"kind": string(mod.Kind()), "title": mod.Title()})
	}
	return out
}

func (m *Manager) Counter() *tokens.Counter { return m.counter }
func (m *Manager) Store() *store.Store      { return m.store }

func (m *Manager) Start(kind model.RunKind, raw json.RawMessage) (*model.Run, error) {
	mod, ok := m.modules[kind]
	if !ok {
		return nil, fmt.Errorf("未知测试类型: %s", kind)
	}
	cfg, err := mod.Validate(raw)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	if m.active >= m.maxRuns {
		m.mu.Unlock()
		return nil, fmt.Errorf("当前已有 %d 个测试在运行，请等待结束或先停止", m.active)
	}
	m.active++
	m.mu.Unlock()

	runID := model.NewID("r_")
	run := &model.Run{ID: runID, Kind: kind, Status: model.StatusQueued, Config: string(raw)}
	if err := m.store.CreateRun(run); err != nil {
		m.mu.Lock()
		m.active--
		m.mu.Unlock()
		return nil, err
	}
	prices, _ := m.store.ListPrices()
	sess := NewSession(runID, kind, m.store, m.client, m.counter, prices)
	sess.StartPumps()

	m.mu.Lock()
	m.sessions[runID] = sess
	m.mu.Unlock()

	go m.execute(mod, sess, cfg)
	return run, nil
}

func (m *Manager) execute(mod Module, sess *Session, cfg any) {
	status := model.StatusCompleted
	errMsg := ""

	defer func() {
		if r := recover(); r != nil {
			status = model.StatusFailed
			errMsg = fmt.Sprintf("测试内部错误: %v", r)
			log.Printf("run %s panic: %v", sess.RunID, r)
		}
		if v := sess.persistErr.Load(); v != nil && errMsg == "" {
			errMsg = "部分结果持久化失败: " + v.(string)
		}
		sess.live.setStatus(string(status))
		if errMsg != "" {
			sess.live.setError(errMsg)
		}
		var summary model.RunSummary
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("run %s summary panic: %v", sess.RunID, r)
					summary = sess.Headline()
				}
			}()
			summary = mod.Summary(sess)
		}()
		base := sess.Headline()
		if summary.Total == 0 && base.Total > 0 {
			summary.Total = base.Total
			summary.Success = base.Success
			summary.Failed = base.Failed
			summary.Canceled = base.Canceled
			summary.SuccessRate = base.SuccessRate
			summary.QPS = base.QPS
			summary.RPM = base.RPM
			summary.ElapsedMs = base.ElapsedMs
			summary.Cost = base.Cost
			summary.Planned = base.Planned
			summary.Models = base.Models
		}
		if summary.ElapsedMs == 0 {
			summary.ElapsedMs = base.ElapsedMs
		}
		sess.FinishSession(status, errMsg)
		if err := m.store.FinishRun(sess.RunID, status, summary, errMsg); err != nil {
			log.Printf("finish run %s: %v", sess.RunID, err)
		}
		m.mu.Lock()
		delete(m.sessions, sess.RunID)
		m.active--
		m.mu.Unlock()
	}()

	if err := m.store.StartRun(sess.RunID); err != nil {
		status = model.StatusFailed
		errMsg = "更新测试状态失败: " + err.Error()
		return
	}
	sess.live.setStatus(string(model.StatusRunning))

	err := mod.Run(sess.ctx, sess, cfg)
	switch {
	case err == nil && sess.StopRequested():
		status = model.StatusStopped
	case err == nil:
		status = model.StatusCompleted
	case sess.StopRequested() || errors.Is(err, context.Canceled):
		status = model.StatusStopped
	default:
		status = model.StatusFailed
		errMsg = err.Error()
	}
}

func (m *Manager) Session(id string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[id]
}

func (m *Manager) Live(id string) (*model.Live, bool) {
	s := m.Session(id)
	if s == nil {
		return nil, false
	}
	return s.LiveSnapshot(), true
}

func (m *Manager) Stop(id string) bool {
	s := m.Session(id)
	if s == nil {
		return false
	}
	s.Stop()
	return true
}

func (m *Manager) ActiveCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active
}

func (m *Manager) Shutdown() {
	m.mu.Lock()
	list := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		list = append(list, s)
	}
	m.mu.Unlock()
	for _, s := range list {
		s.Stop()
	}
}
