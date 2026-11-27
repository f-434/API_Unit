package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"apiunit/internal/config"
	"apiunit/internal/secret"
)

func newSecurityTestServer(t *testing.T) *Server {
	t.Helper()
	box, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatalf("secret: %v", err)
	}
	return &Server{
		cfg:   &config.Config{AuthPassword: "correct-password"},
		box:   box,
		login: newLoginGuard(),
	}
}

func doLogin(s *Server, password, proto string) *httptest.ResponseRecorder {
	body := strings.NewReader(`{"password":"` + password + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", body)
	if proto != "" {
		req.Header.Set("X-Forwarded-Proto", proto)
	}
	rec := httptest.NewRecorder()
	s.handleLogin(rec, req)
	return rec
}

func TestLoginCookieSecurityFlags(t *testing.T) {
	s := newSecurityTestServer(t)
	rec := doLogin(s, "correct-password", "https")
	if rec.Code != http.StatusOK {
		t.Fatalf("登录失败: %d %s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("期望 1 个 Cookie，得到 %d", len(cookies))
	}
	c := cookies[0]
	if !c.Secure {
		t.Fatal("HTTPS 请求下会话 Cookie 必须带 Secure")
	}
	if !c.HttpOnly {
		t.Fatal("会话 Cookie 必须带 HttpOnly")
	}
	if c.SameSite != http.SameSiteStrictMode {
		t.Fatal("会话 Cookie 必须是 SameSite=Strict")
	}
}

func TestLoginRateLimit(t *testing.T) {
	s := newSecurityTestServer(t)
	for i := 0; i < loginMaxFailures; i++ {
		if rec := doLogin(s, "wrong", ""); rec.Code != http.StatusUnauthorized {
			t.Fatalf("第 %d 次错误密码应返回 401，得到 %d", i+1, rec.Code)
		}
	}
	rec := doLogin(s, "correct-password", "")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("封禁期内应返回 429，得到 %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("429 响应应带 Retry-After")
	}
}

func TestSecurityHeaders(t *testing.T) {
	s := newSecurityTestServer(t)
	var got http.Header
	h := s.securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = w.Header().Clone()
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if got.Get("Content-Security-Policy") == "" {
		t.Fatal("缺少 CSP 头")
	}
	if got.Get("Strict-Transport-Security") == "" {
		t.Fatal("HTTPS 请求缺少 HSTS")
	}
}
