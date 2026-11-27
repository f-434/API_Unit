package config

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config holds process-level configuration. Everything has a safe local default.
type Config struct {
	Addr              string
	DataDir           string
	WebDir            string
	AuthPassword      string
	CookieSecret      []byte
	RetentionDays     int
	MaxConcurrentRuns int
	MaxBodyBytes      int64
	TokenizerDir      string
	Offline           bool
	Version           string

	// CookieSecure 强制会话 Cookie 带 Secure 标记；为 false 时按请求是否为 HTTPS 自动判断。
	CookieSecure bool
	// AllowInsecureUpstream 允许通过明文 HTTP 向公网地址发送 API Key，默认关闭。
	AllowInsecureUpstream bool
	// MasterKey / MasterKeyFile 用于把主密钥与 SQLite 数据分开存放；
	// 留空时沿用 DATA_DIR/secret.key。
	MasterKey     string
	MasterKeyFile string

	HTTPDialTimeout           time.Duration
	HTTPTLSHandshakeTimeout   time.Duration
	HTTPResponseHeaderTimeout time.Duration
	DiscoverTimeout           time.Duration
	DiscoverTotalTimeout      time.Duration
	DiscoverRetries           int
}

func Load() *Config {
	cfg := &Config{
		Addr:              env("ADDR", "127.0.0.1:8788"),
		DataDir:           env("DATA_DIR", "./data"),
		WebDir:            env("WEB_DIR", ""),
		AuthPassword:      loadPassword(),
		RetentionDays:     envInt("RETENTION_DAYS", 30),
		MaxConcurrentRuns: envInt("MAX_CONCURRENT_RUNS", 4),
		MaxBodyBytes:      int64(envInt("MAX_REQUEST_BODY_MB", 32)) << 20,
		TokenizerDir:      env("TOKENIZER_DIR", ""),
		Offline:           envBool("OFFLINE", false),
		Version:           env("API_UNIT_VERSION", "1.0.0"),

		CookieSecure:          envBool("COOKIE_SECURE", false),
		AllowInsecureUpstream: envBool("ALLOW_INSECURE_UPSTREAM", false),
		MasterKey:             strings.TrimSpace(os.Getenv("APIUNIT_MASTER_KEY")),
		MasterKeyFile:         strings.TrimSpace(os.Getenv("APIUNIT_MASTER_KEY_FILE")),

		HTTPDialTimeout:           time.Duration(envInt("HTTP_DIAL_TIMEOUT_SEC", 15)) * time.Second,
		HTTPTLSHandshakeTimeout:   time.Duration(envInt("HTTP_TLS_TIMEOUT_SEC", 30)) * time.Second,
		HTTPResponseHeaderTimeout: time.Duration(envInt("HTTP_RESPONSE_TIMEOUT_SEC", 60)) * time.Second,
		DiscoverTimeout:           time.Duration(envInt("DISCOVER_TIMEOUT_SEC", 45)) * time.Second,
		DiscoverTotalTimeout:      time.Duration(envInt("DISCOVER_TOTAL_TIMEOUT_SEC", 120)) * time.Second,
		DiscoverRetries:           envInt("DISCOVER_RETRIES", 2),
	}
	if cfg.TokenizerDir == "" {
		cfg.TokenizerDir = filepath.Join(cfg.DataDir, "tokenizers")
	}
	if cfg.WebDir == "" {
		cfg.WebDir = "./web"
		for _, candidate := range []string{"web", "backend/web"} {
			if _, err := os.Stat(filepath.Join(candidate, "index.html")); err == nil {
				cfg.WebDir = candidate
				break
			}
		}
	}
	if cfg.RetentionDays < 0 {
		cfg.RetentionDays = 0
	}
	if cfg.MaxConcurrentRuns < 1 {
		cfg.MaxConcurrentRuns = 1
	}
	if cfg.HTTPDialTimeout < time.Second {
		cfg.HTTPDialTimeout = 15 * time.Second
	}
	if cfg.HTTPTLSHandshakeTimeout < time.Second {
		cfg.HTTPTLSHandshakeTimeout = 30 * time.Second
	}
	if cfg.HTTPResponseHeaderTimeout < time.Second {
		cfg.HTTPResponseHeaderTimeout = 60 * time.Second
	}
	if cfg.DiscoverTimeout < time.Second {
		cfg.DiscoverTimeout = 45 * time.Second
	}
	if cfg.DiscoverTotalTimeout < time.Second {
		cfg.DiscoverTotalTimeout = 120 * time.Second
	}
	if cfg.DiscoverRetries < 0 {
		cfg.DiscoverRetries = 2
	}
	if cfg.DiscoverRetries > 10 {
		cfg.DiscoverRetries = 10
	}
	sum := sha256.Sum256([]byte("apiunit-cookie:" + cfg.AuthPassword))
	cfg.CookieSecret = sum[:]
	return cfg
}

// loadPassword 优先读取 AUTH_PASSWORD；为空时尝试从 AUTH_PASSWORD_FILE 读取，
// 便于 Docker/K8s 用文件挂载密码，避免密码出现在 docker inspect 的环境变量里。
func loadPassword() string {
	if v := strings.TrimSpace(os.Getenv("AUTH_PASSWORD")); v != "" {
		return v
	}
	path := strings.TrimSpace(os.Getenv("AUTH_PASSWORD_FILE"))
	if path == "" {
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func envBool(key string, def bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func (c *Config) DBPath() string { return filepath.Join(c.DataDir, "apiunit.db") }

func (c *Config) SecretPath() string { return filepath.Join(c.DataDir, "secret.key") }

func (c *Config) AuthEnabled() bool { return strings.TrimSpace(c.AuthPassword) != "" }

func (c *Config) EnsureDirs() error {
	// 数据目录含加密后的密钥与测试结果，只允许属主访问。
	// Docker 命名卷的挂载点由 Docker 创建为 0755，需要显式收紧。
	if err := os.MkdirAll(c.DataDir, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(c.DataDir, 0o700)
	if err := os.MkdirAll(c.TokenizerDir, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(c.TokenizerDir, 0o700)
	return nil
}

var _ = hex.EncodeToString
