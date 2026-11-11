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
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	rows, total, err := s.store.ListRuns(kind, limit, offset)
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for i := range rows {
		// List responses must stay small: drop the module report payload.
		rows[i].Summary.Data = nil
		backfillRPM(&rows[i].Summary)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows, "total": total})
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
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	filter := store.ItemFilter{
		RunID:    chi.URLParam(r, "id"),
		Model:    q.Get("model"),
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
