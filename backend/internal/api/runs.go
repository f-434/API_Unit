package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"apiunit/internal/model"
	"apiunit/internal/modules/imagedetection"
	"apiunit/internal/store"
)

func (s *Server) handleRunCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind   string          `json:"kind"`
		Config json.RawMessage `json:"config"`
	}
	if err := decodeLenient(r, &req, 8<<20); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	kind := model.RunKind(strings.TrimSpace(req.Kind))
	if kind == "" {
		apiError(w, http.StatusBadRequest, "缺少测试类型")
		return
	}
	if len(req.Config) == 0 || string(req.Config) == "null" {
		apiError(w, http.StatusBadRequest, "缺少测试配置")
		return
	}
	run, err := s.manager.Start(kind, req.Config)
	if err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": run.ID, "run": run})
}

func (s *Server) handleRunsList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	kind := q.Get("kind")
	status := q.Get("status")
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	rows, total, err := s.store.ListRuns(kind, status, limit, offset)
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for i := range rows {
		// List responses must stay small: drop the module report payload.
		rows[i].Summary.Data = nil
		backfillRPM(&rows[i].Summary)
	}
	s.fillRunTargets(rows)
	writeJSON(w, http.StatusOK, map[string]any{"items": rows, "total": total})
}

// fillRunTargets adds upstream names for runs whose summary does not carry
// them yet (queued/running runs and records created before the field existed).
func (s *Server) fillRunTargets(rows []store.RunRow) {
	need := false
	for i := range rows {
		if len(rows[i].Summary.Targets) == 0 {
			need = true
			break
		}
	}
	if !need {
		return
	}
	names, err := s.store.TargetNames()
	if err != nil {
		names = map[string]string{}
	}
	for i := range rows {
		if len(rows[i].Summary.Targets) > 0 {
			continue
		}
		rows[i].Summary.Targets = targetsFromConfig(rows[i].Config, names)
	}
}

type runTargetConfig struct {
	TargetID string `json:"targetId"`
	Targets  []struct {
		TargetID string `json:"targetId"`
		Label    string `json:"label"`
	} `json:"targets"`
}

func targetsFromConfig(raw string, names map[string]string) []model.TargetRef {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var cfg runTargetConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil
	}
	type entry struct{ id, label string }
	entries := make([]entry, 0, 1+len(cfg.Targets))
	if id := strings.TrimSpace(cfg.TargetID); id != "" {
		entries = append(entries, entry{id: id})
	}
	for _, t := range cfg.Targets {
		if id := strings.TrimSpace(t.TargetID); id != "" {
			entries = append(entries, entry{id: id, label: t.Label})
		}
	}
	seen := map[string]bool{}
	out := make([]model.TargetRef, 0, len(entries))
	for _, e := range entries {
		if seen[e.id] {
			continue
		}
		seen[e.id] = true
		name := names[e.id]
		if name == "" {
			name = strings.TrimSpace(e.label)
		}
		if name == "" {
			short := e.id
			if len(short) > 12 {
				short = short[:12]
			}
			name = "已删除上游 (" + short + ")"
		}
		out = append(out, model.TargetRef{ID: e.id, Name: name})
	}
	return out
}

func (s *Server) handleRunGet(w http.ResponseWriter, r *http.Request) {
	view, err := s.loadRunView(chi.URLParam(r, "id"))
	if err != nil {
		apiError(w, http.StatusNotFound, "测试不存在")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) loadRunView(id string) (*model.RunView, error) {
	run, err := s.store.GetRun(id)
	if err != nil {
		return nil, err
	}
	var summary model.RunSummary
	if run.Summary != "" {
		_ = json.Unmarshal([]byte(run.Summary), &summary)
	}
	backfillRPM(&summary)
	backfillReportRPM(summary.Data)
	var cfg any
	if run.Config != "" {
		_ = json.Unmarshal([]byte(run.Config), &cfg)
	}
	view := &model.RunView{Run: *run, Summary: summary, Config: cfg}
	if live, ok := s.manager.Live(id); ok {
		view.Live = live
	}
	return view, nil
}

// backfillRPM derives RPM for summaries persisted before the metric existed.
func backfillRPM(s *model.RunSummary) {
	if s == nil || s.RPM > 0 || s.QPS <= 0 {
		return
	}
	s.RPM = s.QPS * 60
}

func backfillReportRPM(data any) {
	m, ok := data.(map[string]any)
	if !ok {
		return
	}
	if agg, ok := m["aggregate"].(map[string]any); ok {
		backfillRPMValue(agg)
	}
	if models, ok := m["models"].([]any); ok {
		for _, row := range models {
			if mm, ok := row.(map[string]any); ok {
				backfillRPMValue(mm)
			}
		}
	}
}

func backfillRPMValue(m map[string]any) {
	if numeric(m["rpm"]) > 0 {
		return
	}
	if qps := numeric(m["qps"]); qps > 0 {
		m["rpm"] = qps * 60
	}
}

func numeric(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case json.Number:
		f, _ := x.Float64()
		return f
	}
	return 0
}

// retestSelection 是二次测试的勾选粒度：一个上游下的一个模型。
type retestSelection struct {
	TargetID string `json:"targetId"`
	Model    string `json:"model"`
}

// handleRunRetest 按原测试配置克隆一次新测试，只保留勾选的模型。
// 检测项、并发、重试、超时、repetitions 等其它字段全部原样复用。
func (s *Server) handleRunRetest(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	src, err := s.store.GetRun(id)
	if err != nil {
		apiError(w, http.StatusNotFound, "测试不存在")
		return
	}
	if s.manager.Session(id) != nil {
		apiError(w, http.StatusConflict, "原测试仍在运行，请结束后再重测")
		return
	}
	var req struct {
		Selections []retestSelection `json:"selections"`
	}
	if err := decodeLenient(r, &req, 1<<20); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	filtered, models, err := filterRetestConfig(src.Kind, json.RawMessage(src.Config), req.Selections)
	if err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	run, err := s.manager.Start(src.Kind, filtered)
	if err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": run.ID, "run": run, "models": models})
}

// filterRetestConfig 返回只包含勾选模型的原配置副本。
// 这里使用 map 而非各模块的配置结构体，避免模块以后新增字段时被重测逻辑丢弃。
func filterRetestConfig(kind model.RunKind, raw json.RawMessage, sels []retestSelection) (json.RawMessage, int, error) {
	switch kind {
	case model.KindAuthenticity, model.KindImageDetection:
	default:
		return nil, 0, fmt.Errorf("该测试类型不支持二次测试")
	}

	// targetId -> 勾选的模型集合
	wanted := make(map[string]map[string]bool)
	for _, sel := range sels {
		tid := strings.TrimSpace(sel.TargetID)
		name := strings.TrimSpace(sel.Model)
		if tid == "" || name == "" {
			continue
		}
		set := wanted[tid]
		if set == nil {
			set = map[string]bool{}
			wanted[tid] = set
		}
		set[name] = true
	}
	if len(wanted) == 0 {
		return nil, 0, fmt.Errorf("请选择要重测的模型")
	}

	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, 0, fmt.Errorf("原测试配置解析失败: %w", err)
	}
	targets, ok := cfg["targets"].([]any)
	if !ok {
		return nil, 0, fmt.Errorf("原测试配置缺少 targets")
	}

	keptTargets := make([]any, 0, len(targets))
	kept := 0
	for _, item := range targets {
		target, ok := item.(map[string]any)
		if !ok {
			continue
		}
		tid, _ := target["targetId"].(string)
		set := wanted[strings.TrimSpace(tid)]
		if len(set) == 0 {
			continue
		}
		models, ok := target["models"].([]any)
		if !ok {
			continue
		}
		next := make([]any, 0, len(models))
		switch kind {
		case model.KindAuthenticity:
			// 模型真实性的 targets[].models 是字符串数组。
			for _, m := range models {
				name, ok := m.(string)
				if ok && set[strings.TrimSpace(name)] {
					next = append(next, m)
				}
			}
		case model.KindImageDetection:
			// 生图检测的 targets[].models 是 {name,kind,size} 对象。
			for _, m := range models {
				obj, ok := m.(map[string]any)
				if !ok {
					continue
				}
				name, _ := obj["name"].(string)
				if set[strings.TrimSpace(name)] {
					next = append(next, m)
				}
			}
		}
		if len(next) == 0 {
			continue
		}
		target["models"] = next
		keptTargets = append(keptTargets, target)
		kept += len(next)
	}
	if kept == 0 {
		return nil, 0, fmt.Errorf("勾选的模型不在原测试配置中")
	}

	cfg["targets"] = keptTargets
	out, err := json.Marshal(cfg)
	if err != nil {
		return nil, 0, fmt.Errorf("生成重测配置失败: %w", err)
	}
	return out, kept, nil
}

func (s *Server) handleRunDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if s.manager.Session(id) != nil {
		apiError(w, http.StatusConflict, "测试正在运行，请先停止")
		return
	}
	if _, err := s.store.GetRun(id); err != nil {
		apiError(w, http.StatusNotFound, "测试不存在")
		return
	}
	if err := s.store.DeleteRun(id); err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 生图检测的图片不存 SQLite，删除测试记录时一并清理。
	_ = imagedetection.RemoveRunImages(imagedetection.ImageDir(s.cfg.DataDir), id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleRunStop(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	ok := s.manager.Stop(id)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "reason": "测试不在运行中"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleRunItems(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	if limit <= 0 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	filter := store.ItemFilter{
		RunID:    chi.URLParam(r, "id"),
		Model:    q.Get("model"),
		CaseID:   q.Get("caseId"),
		OnlyFail: q.Get("onlyFail") == "1" || q.Get("onlyFail") == "true",
		OnlyOK:   q.Get("onlyOk") == "1",
		Verdict:  q.Get("verdict"),
		Category: q.Get("category"),
		Search:   strings.TrimSpace(q.Get("q")),
		Limit:    limit,
		Offset:   offset,
		OrderAsc: q.Get("order") != "desc",
	}
	items, total, err := s.store.ListItems(filter)
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "limit": filter.Limit, "offset": filter.Offset})
}

func (s *Server) handleRunItem(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "itemID"), 10, 64)
	if err != nil {
		apiError(w, http.StatusBadRequest, "无效的结果 ID")
		return
	}
	item, err := s.store.GetItem(chi.URLParam(r, "id"), id)
	if err != nil {
		apiError(w, http.StatusNotFound, "结果不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"item": item})
}

func (s *Server) handleRunEvents(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	sess := s.manager.Session(id)
	if sess == nil {
		run, err := s.store.GetRun(id)
		if err != nil {
			apiError(w, http.StatusNotFound, "测试不存在")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		data, _ := json.Marshal(map[string]any{"runId": id, "status": run.Status, "error": run.Error})
		_, _ = fmt.Fprintf(w, "event: done\ndata: %s\n\n", data)
		return
	}
	subID, ch, snapshot, ok := sess.Subscribe()
	if !ok {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		_, _ = fmt.Fprintf(w, "event: done\ndata: {\"runId\":%q,\"status\":\"completed\"}\n\n", id)
		return
	}
	defer sess.Unsubscribe(subID)

	flusher, ok := w.(http.Flusher)
	if !ok {
		apiError(w, http.StatusInternalServerError, "当前服务器不支持 SSE")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(snapshot)
	flusher.Flush()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case frame, open := <-ch:
			if !open {
				return
			}
			if _, err := w.Write(frame); err != nil {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
