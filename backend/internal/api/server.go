package api

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"apiunit/internal/config"
	"apiunit/internal/engine"
	"apiunit/internal/protocol"
	"apiunit/internal/secret"
	"apiunit/internal/store"
	"apiunit/internal/tokens"
)

const sessionCookie = "apiunit_session"

type Server struct {
	cfg     *config.Config
	store   *store.Store
	manager *engine.Manager
	counter *tokens.Counter
	client  *protocol.Client
	box     *secret.Box
	started time.Time
}

func New(cfg *config.Config, st *store.Store, manager *engine.Manager, counter *tokens.Counter, client *protocol.Client, box *secret.Box) *Server {
	return &Server{cfg: cfg, store: st, manager: manager, counter: counter, client: client, box: box, started: time.Now()}
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.RealIP)
	r.Use(s.logging)
	r.Use(s.securityHeaders)

	r.Get("/api/health", s.handleHealth)
	r.Get("/api/auth/status", s.handleAuthStatus)
	r.Post("/api/auth/login", s.handleLogin)
	r.Post("/api/auth/logout", s.handleLogout)

	r.Group(func(pr chi.Router) {
		pr.Use(s.authMiddleware)
		pr.Get("/api/meta", s.handleMeta)
		pr.Get("/api/settings", s.handleSettings)
		pr.Post("/api/settings/purge", s.handlePurge)

		pr.Get("/api/targets", s.handleTargetsList)
		pr.Post("/api/targets", s.handleTargetCreate)
		pr.Put("/api/targets/{id}", s.handleTargetUpdate)
		pr.Delete("/api/targets/{id}", s.handleTargetDelete)
		pr.Post("/api/targets/test", s.handleTargetTest)
		pr.Post("/api/targets/{id}/models", s.handleTargetModels)

		pr.Get("/api/prompts", s.handlePromptsList)
		pr.Post("/api/prompts", s.handlePromptCreate)
		pr.Put("/api/prompts/{id}", s.handlePromptUpdate)
		pr.Delete("/api/prompts/{id}", s.handlePromptDelete)
		pr.Post("/api/prompts/import", s.handlePromptsImport)
		pr.Get("/api/prompts/export", s.handlePromptsExport)

		pr.Get("/api/attacks", s.handleAttacksList)
		pr.Post("/api/attacks", s.handleAttackCreate)
		pr.Put("/api/attacks/{id}", s.handleAttackUpdate)
		pr.Delete("/api/attacks/{id}", s.handleAttackDelete)
		pr.Post("/api/attacks/import", s.handleAttacksImport)
		pr.Get("/api/attacks/export", s.handleAttacksExport)
		pr.Post("/api/attacks/reset-builtin", s.handleAttacksReset)

		pr.Get("/api/prices", s.handlePricesList)
		pr.Post("/api/prices", s.handlePriceCreate)
		pr.Put("/api/prices/{id}", s.handlePriceUpdate)
		pr.Delete("/api/prices/{id}", s.handlePriceDelete)

		pr.Post("/api/tokenizers/count", s.handleTokenizerCount)
		pr.Get("/api/tokenizers/status", s.handleTokenizerStatus)
		pr.Post("/api/tokenizers/ensure", s.handleTokenizerEnsure)
		pr.Post("/api/tokenizers/reset", s.handleTokenizerReset)

		pr.Post("/api/runs", s.handleRunCreate)
		pr.Get("/api/runs", s.handleRunsList)
		pr.Get("/api/runs/{id}", s.handleRunGet)
		pr.Delete("/api/runs/{id}", s.handleRunDelete)
		pr.Post("/api/runs/{id}/stop", s.handleRunStop)
		pr.Get("/api/runs/{id}/items", s.handleRunItems)
		pr.Get("/api/runs/{id}/items/{itemID}", s.handleRunItem)
		pr.Get("/api/runs/{id}/events", s.handleRunEvents)
		pr.Get("/api/runs/{id}/export", s.handleRunExport)

		pr.Get("/api/runs/{id}/snapshots", s.handleSnapshotsList)
		pr.Get("/api/snapshots", s.handleSnapshotsList)
		pr.Get("/api/snapshots/{snapshotID}", s.handleSnapshotGet)
		pr.Post("/api/diffs", s.handleDiff)
		pr.Get("/api/diffs/export", s.handleDiffExport)
	})

	r.Handle("/*", spaHandler(s.cfg.WebDir))
	return r
}

func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			start := time.Now()
			next.ServeHTTP(w, r)
			log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.cfg.AuthEnabled() || s.sessionValid(r) {
			next.ServeHTTP(w, r)
			return
		}
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "需要登录", "code": "auth_required"})
	})
}

func (s *Server) sessionValid(r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return false
	}
	parts := strings.SplitN(c.Value, ".", 2)
	if len(parts) != 2 {
		return false
	}
	exp, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return false
	}
	return s.box.Verify(parts[0], parts[1])
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "version": s.cfg.Version, "time": time.Now().Format(time.RFC3339),
		"uptimeSec": int(time.Since(s.started).Seconds()),
	})
}

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"authEnabled":   s.cfg.AuthEnabled(),
		"authenticated": !s.cfg.AuthEnabled() || s.sessionValid(r),
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.AuthEnabled() {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := decode(r, &req, 1<<20); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	if subtle.ConstantTimeCompare([]byte(req.Password), []byte(s.cfg.AuthPassword)) != 1 {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "密码错误"})
		return
	}
	exp := time.Now().Add(7 * 24 * time.Hour).Unix()
	value := fmt.Sprintf("%d.%s", exp, s.box.Sign(strconv.FormatInt(exp, 10)))
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: value, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteStrictMode, MaxAge: 7 * 24 * 3600,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write json: %v", err)
	}
}

func apiError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

func decode(r *http.Request, dst any, limit int64) error {
	if limit <= 0 {
		limit = 4 << 20
	}
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, limit))
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("请求体解析失败: %w", err)
	}
	return nil
}

// decodeLenient ignores unknown fields (handy for import payloads).
func decodeLenient(r *http.Request, dst any, limit int64) error {
	if limit <= 0 {
		limit = 8 << 20
	}
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, limit))
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("请求体解析失败: %w", err)
	}
	return nil
}

func spaHandler(webDir string) http.HandlerFunc {
	fs := http.FileServer(http.Dir(webDir))
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		clean := strings.TrimPrefix(filepath.ToSlash(filepath.Clean("/"+r.URL.Path)), "/")
		if clean == "" || clean == "." {
			clean = "index.html"
		}
		full := filepath.Join(webDir, filepath.FromSlash(clean))
		if st, err := os.Stat(full); err == nil && !st.IsDir() {
			if strings.HasPrefix(clean, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache")
			}
			fs.ServeHTTP(w, r)
			return
		}
		index := filepath.Join(webDir, "index.html")
		if _, err := os.Stat(index); err != nil {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte("API Unit backend is running. Frontend build not found in " + webDir + "\n"))
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, index)
	}
}
