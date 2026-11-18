package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"apiunit/internal/model"
)

type authenticityTemplateReq struct {
	ProbeID      string          `json:"probeId"`
	Provider     string          `json:"provider"`
	Name         string          `json:"name"`
	Note         string          `json:"note"`
	Spec         json.RawMessage `json:"spec"`
	SourceRunID  string          `json:"sourceRunId"`
	SourceItemID int64           `json:"sourceItemId"`
	CreatedBy    string          `json:"createdBy"`
}

func (s *Server) handleAuthenticityTemplatesList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	items, err := s.store.ListAuthenticityTemplates(q.Get("probeId"), q.Get("includeArchived") == "1")
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleAuthenticityTemplateGet(w http.ResponseWriter, r *http.Request) {
	t, err := s.store.GetAuthenticityTemplate(chi.URLParam(r, "id"))
	if err != nil {
		apiError(w, http.StatusNotFound, "模板不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"item": t})
}

func (s *Server) handleAuthenticityTemplateCreate(w http.ResponseWriter, r *http.Request) {
	var req authenticityTemplateReq
	if err := decode(r, &req, 4<<20); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.ProbeID = strings.TrimSpace(req.ProbeID)
	req.Provider = strings.ToLower(strings.TrimSpace(req.Provider))
	if req.ProbeID == "" {
		apiError(w, http.StatusBadRequest, "缺少 probeId")
		return
	}
	if req.Provider != "" && req.Provider != "openai" && req.Provider != "claude" && req.Provider != "gemini" {
		apiError(w, http.StatusBadRequest, "provider 只能是 openai / claude / gemini 或留空")
		return
	}
	spec := strings.TrimSpace(string(req.Spec))
	if spec == "" || spec == "null" {
		spec = "{}"
	}
	var specObj map[string]any
	if err := json.Unmarshal([]byte(spec), &specObj); err != nil {
		apiError(w, http.StatusBadRequest, "spec 不是合法 JSON 对象: "+err.Error())
		return
	}
	t := &model.AuthenticityTemplate{
		ProbeID:      req.ProbeID,
		Provider:     req.Provider,
		Name:         strings.TrimSpace(req.Name),
		Spec:         spec,
		Note:         strings.TrimSpace(req.Note),
		SourceRunID:  strings.TrimSpace(req.SourceRunID),
		SourceItemID: req.SourceItemID,
		CreatedBy:    strings.TrimSpace(req.CreatedBy),
		Status:       "draft",
	}
	if t.Name == "" {
		t.Name = t.ProbeID
	}
	if t.SourceRunID != "" && t.SourceItemID > 0 {
		it, err := s.store.GetItem(t.SourceRunID, t.SourceItemID)
		if err == nil {
			t.SampleRequest = it.RequestJSON
			t.SampleResponse = it.ResponseJSON
			if len(specObj) == 0 {
				t.Spec = autoTemplateSpec(it)
			}
		}
	}
	if err := s.store.CreateAuthenticityTemplate(t); err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"item": t})
}

// autoTemplateSpec derives a conservative expectation from a captured official
// response. The caller reviews/edits it before publishing.
func autoTemplateSpec(it *model.ItemDetail) string {
	spec := map[string]any{}
	if it.Status > 0 {
		spec["status"] = it.Status
	}
	var body map[string]any
	if json.Unmarshal([]byte(it.ResponseJSON), &body) == nil {
		if e, ok := body["error"].(map[string]any); ok {
			paths := []any{}
			for _, key := range []string{"code", "status", "type", "param"} {
				if v, exists := e[key]; exists && v != "" {
					paths = append(paths, map[string]any{
						"path":   "error." + key,
						"equals": valueString(v),
					})
				}
			}
			if len(paths) > 0 {
				spec["jsonPaths"] = paths
			}
			if msg, ok := e["message"].(string); ok && strings.TrimSpace(msg) != "" && len(msg) <= 300 {
				spec["contains"] = []string{msg}
			}
		}
	}
	raw, _ := json.MarshalIndent(spec, "", "  ")
	return string(raw)
}

func valueString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	}
	raw, _ := json.Marshal(v)
	return string(raw)
}

func (s *Server) handleAuthenticityTemplatePublish(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.store.GetAuthenticityTemplate(id); err != nil {
		apiError(w, http.StatusNotFound, "模板不存在")
		return
	}
	if err := s.store.PublishAuthenticityTemplate(id); err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleAuthenticityTemplateArchive(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.store.ArchiveAuthenticityTemplate(id); err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
