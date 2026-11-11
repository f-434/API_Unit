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
		AuthPassword:      os.Getenv("AUTH_PASSWORD"),
		RetentionDays:     envInt("RETENTION_DAYS", 30),
		MaxConcurrentRuns: envInt("MAX_CONCURRENT_RUNS", 4),
		MaxBodyBytes:      int64(envInt("MAX_REQUEST_BODY_MB", 32)) << 20,
		TokenizerDir:      env("TOKENIZER_DIR", ""),
		Offline:           envBool("OFFLINE", false),
		Version:           env("API_UNIT_VERSION", "1.0.0"),

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
	if err := os.MkdirAll(c.DataDir, 0o755); err != nil {
		return err
	}
	return os.MkdirAll(c.TokenizerDir, 0o755)
}

var _ = hex.EncodeToString
