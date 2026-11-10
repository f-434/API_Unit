package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"apiunit/internal/model"
	"apiunit/internal/requestdiff"
	"apiunit/internal/store"
)

func (s *Server) handleSnapshotsList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	if limit <= 0 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	filter := store.SnapshotFilter{
		RunID:    q.Get("runId"),
		CaseID:   q.Get("caseId"),
		TargetID: q.Get("targetId"),
		Model:    q.Get("model"),
		Protocol: q.Get("protocol"),
		Search:   strings.TrimSpace(q.Get("q")),
		Limit:    limit,
		Offset:   offset,
		OrderAsc: q.Get("order") == "asc",
	}
	if id := chi.URLParam(r, "id"); id != "" {
		filter.RunID = id
	}
	list, total, err := s.store.ListSnapshots(filter)
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list, "total": total, "limit": filter.Limit, "offset": filter.Offset})
}

func (s *Server) handleSnapshotGet(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "snapshotID"), 10, 64)
	if err != nil {
		apiError(w, http.StatusBadRequest, "无效的快照 ID")
		return
	}
	sn, err := s.store.GetSnapshot(id)
	if err != nil {
		apiError(w, http.StatusNotFound, "快照不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"item": sn})
}

type diffRequest struct {
	LeftID  int64  `json:"leftId"`
	RightID int64  `json:"rightId"`
	Mode    string `json:"mode"` // wire | canonical
}

func (s *Server) loadSnapshotPair(r *http.Request) (*model.SnapshotDetail, *model.SnapshotDetail, string, int, error) {
	leftID, _ := strconv.ParseInt(r.URL.Query().Get("left"), 10, 64)
	rightID, _ := strconv.ParseInt(r.URL.Query().Get("right"), 10, 64)
	mode := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("mode")))
	return s.snapshotPair(leftID, rightID, mode)
}

func (s *Server) snapshotPair(leftID, rightID int64, mode string) (*model.SnapshotDetail, *model.SnapshotDetail, string, int, error) {
	if leftID <= 0 || rightID <= 0 {
		return nil, nil, "", http.StatusBadRequest, fmt.Errorf("left/right 快照 ID 不能为空")
	}
	if mode == "" {
		mode = "wire"
	}
	if mode != "wire" && mode != "canonical" {
		return nil, nil, "", http.StatusBadRequest, fmt.Errorf("mode 只能是 wire 或 canonical")
	}
	left, err := s.store.GetSnapshot(leftID)
	if err != nil {
		return nil, nil, "", http.StatusNotFound, fmt.Errorf("左侧快照不存在")
	}
	right, err := s.store.GetSnapshot(rightID)
	if err != nil {
		return nil, nil, "", http.StatusNotFound, fmt.Errorf("右侧快照不存在")
	}
	return left, right, mode, http.StatusOK, nil
}

func diffText(sn *model.SnapshotDetail, mode string) string {
	if mode == "canonical" {
		return sn.Canonical
	}
	return sn.Body
}

func (s *Server) handleDiff(w http.ResponseWriter, r *http.Request) {
	var req diffRequest
	if err := decodeLenient(r, &req, 1<<20); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	left, right, mode, status, err := s.snapshotPair(req.LeftID, req.RightID, strings.ToLower(req.Mode))
	if err != nil {
		apiError(w, status, err.Error())
		return
	}
	result, err := requestdiff.DiffJSON(diffText(left, mode), diffText(right, mode))
	if err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	result.Sections = requestdiff.CompareFingerprints(left.Fingerprints, right.Fingerprints)
	warnings := []string{}
	if mode == "wire" && left.Protocol != right.Protocol {
		warnings = append(warnings, "两侧协议不同（"+left.Protocol+" vs "+right.Protocol+"），wire 模式会产生大量结构性差异，建议改用 canonical 模式。")
	}
	if result.Summary.Truncated {
		warnings = append(warnings, "差异数量超过上限，已截断。")
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"mode": mode, "left": left, "right": right, "result": result, "warnings": warnings,
	})
}

func (s *Server) handleDiffExport(w http.ResponseWriter, r *http.Request) {
	left, right, mode, status, err := s.loadSnapshotPair(r)
	if err != nil {
		apiError(w, status, err.Error())
		return
	}
	result, err := requestdiff.DiffJSON(diffText(left, mode), diffText(right, mode))
	if err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	result.Sections = requestdiff.CompareFingerprints(left.Fingerprints, right.Fingerprints)
	format := strings.ToLower(r.URL.Query().Get("format"))
	switch format {
	case "", "json":
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="apiunit-diff-%d-%d.json"`, left.ID, right.ID))
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(map[string]any{
			"mode": mode, "left": left.Snapshot, "right": right.Snapshot, "result": result,
			"leftBody": diffText(left, mode), "rightBody": diffText(right, mode),
		})
	case "html":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="apiunit-diff-%d-%d.html"`, left.ID, right.ID))
		renderDiffHTML(w, left, right, mode, result)
	default:
		apiError(w, http.StatusBadRequest, "不支持的导出格式")
	}
}
