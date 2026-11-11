package tokens

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	sentencepiece "github.com/eliben/go-sentencepiece"
)

type geminiSpec struct {
	Name   string
	File   string
	URLs   []string
	SHA256 string
}

var geminiSpecs = []geminiSpec{
	{
		Name: "gemma3",
		File: "gemma3_262144.model",
		URLs: []string{
			"https://cdn.jsdelivr.net/gh/google/gemma_pytorch@014acb7ac4563a5f77c76d7ff98f31b568c16508/tokenizer/gemma3_cleaned_262144_v2.spiece.model",
			"https://raw.githubusercontent.com/google/gemma_pytorch/014acb7ac4563a5f77c76d7ff98f31b568c16508/tokenizer/gemma3_cleaned_262144_v2.spiece.model",
			"https://ghproxy.net/https://raw.githubusercontent.com/google/gemma_pytorch/014acb7ac4563a5f77c76d7ff98f31b568c16508/tokenizer/gemma3_cleaned_262144_v2.spiece.model",
		},
		SHA256: "1299c11d7cf632ef3b4e11937501358ada021bbdf7c47638d13c0ee982f2e79c",
	},
	{
		Name: "gemma2",
		File: "gemma2.model",
		URLs: []string{
			"https://cdn.jsdelivr.net/gh/google/gemma_pytorch@33b652c465537c6158f9a472ea5700e5e770ad3f/tokenizer/tokenizer.model",
			"https://raw.githubusercontent.com/google/gemma_pytorch/33b652c465537c6158f9a472ea5700e5e770ad3f/tokenizer/tokenizer.model",
			"https://ghproxy.net/https://raw.githubusercontent.com/google/gemma_pytorch/33b652c465537c6158f9a472ea5700e5e770ad3f/tokenizer/tokenizer.model",
		},
		SHA256: "61a7b147390c64585d6c3543dd6fc636906c9af3865a5548f27f31aee1d4c8e2",
	},
}

// downloadClient has a longer timeout than the online token-counting client;
// the SentencePiece models are several megabytes and may be fetched over a
// slow link. It uses the default transport, so HTTPS_PROXY is honored.
var downloadClient = &http.Client{Timeout: 5 * time.Minute}

type geminiProcessor struct {
	mu   sync.Mutex
	proc *sentencepiece.Processor
	name string
}

func geminiSpecFor(modelName string) geminiSpec {
	m := strings.ToLower(modelName)
	for _, prefix := range []string{"gemini-1.0", "gemini-1.5"} {
		if strings.Contains(m, prefix) {
			return geminiSpecs[1]
		}
	}
	return geminiSpecs[0]
}

func (c *Counter) geminiCount(modelName, text string) (int, string, error) {
	spec := geminiSpecFor(modelName)
	proc, err := c.loadGemini(spec)
	if err != nil {
		return 0, "", err
	}
	proc.mu.Lock()
	toks := proc.proc.Encode(text)
	proc.mu.Unlock()
	return len(toks), spec.Name, nil
}

// geminiLookup returns a loaded processor or a previously recorded error.
func (c *Counter) geminiLookup(name string) (*geminiProcessor, error, bool) {
	c.geminiMu.Lock()
	defer c.geminiMu.Unlock()
	if p, ok := c.processors[name]; ok {
		return p, nil, true
	}
	if e := c.geminiErrors[name]; e != "" {
		return nil, fmt.Errorf("%s", e), true
	}
	return nil, nil, false
}

func (c *Counter) setGeminiError(name, msg string) {
	c.geminiMu.Lock()
	defer c.geminiMu.Unlock()
	if msg == "" {
		delete(c.geminiErrors, name)
		return
	}
	c.geminiErrors[name] = msg
}

// loadAndStoreGemini parses the model file and caches the processor. It is used
// by both the lazy count path and the explicit download/verify path so that a
// successful download immediately shows "loaded" and surfaces parse errors.
func (c *Counter) loadAndStoreGemini(spec geminiSpec, path string) (*geminiProcessor, error) {
	c.geminiMu.Lock()
	if p, ok := c.processors[spec.Name]; ok {
		c.geminiMu.Unlock()
		return p, nil
	}
	c.geminiMu.Unlock()

	proc, err := sentencepiece.NewProcessorFromPath(path)
	if err != nil {
		return nil, err
	}
	p := &geminiProcessor{proc: proc, name: spec.Name}
	c.geminiMu.Lock()
	c.processors[spec.Name] = p
	c.geminiMu.Unlock()
	return p, nil
}

func (c *Counter) loadGemini(spec geminiSpec) (*geminiProcessor, error) {
	if p, err, ok := c.geminiLookup(spec.Name); ok {
		return p, err
	}
	// Serialize downloads/parses, but never hold geminiMu during network I/O
	// so status queries stay responsive.
	c.geminiDownloadMu.Lock()
	defer c.geminiDownloadMu.Unlock()
	if p, err, ok := c.geminiLookup(spec.Name); ok {
		return p, err
	}
	path := filepath.Join(c.tokenDir, spec.File)
	if err := verifySHA256(path, spec.SHA256); err != nil {
		if c.offline {
			err = fmt.Errorf("本地缺少有效的 %s 模型文件，且当前为离线模式", spec.File)
			c.setGeminiError(spec.Name, err.Error())
			return nil, err
		}
		_ = os.Remove(path)
		if err := c.download(context.Background(), spec, path); err != nil {
			c.setGeminiError(spec.Name, err.Error())
			return nil, err
		}
	}
	p, err := c.loadAndStoreGemini(spec, path)
	if err != nil {
		c.setGeminiError(spec.Name, err.Error())
		return nil, err
	}
	c.setGeminiError(spec.Name, "")
	return p, nil
}

func verifySHA256(path, want string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("文件校验失败（期望 %s…，实际 %s…）", shortHash(want), shortHash(got))
	}
	return nil
}

func shortHash(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func (c *Counter) download(ctx context.Context, spec geminiSpec, path string) error {
	var errs []string
	for _, u := range spec.URLs {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("下载 %s 超时或被取消（已完成的文件会保留，可稍后重试）", spec.Name)
		}
		if err := c.downloadOne(ctx, spec, path, u); err == nil {
			return nil
		} else {
			errs = append(errs, compactError(err.Error()))
		}
	}
	return fmt.Errorf("下载 %s 失败：%s", spec.Name, strings.Join(errs, "；"))
}

func (c *Counter) downloadOne(ctx context.Context, spec geminiSpec, path, rawURL string) error {
	uctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(uctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "apiunit/1.0")
	host := mirrorHost(rawURL)
	resp, err := downloadClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", host, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d", host, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return fmt.Errorf("%s: %w", host, err)
	}
	sum := sha256.Sum256(body)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), spec.SHA256) {
		return fmt.Errorf("%s: SHA-256 校验失败", host)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func mirrorHost(rawURL string) string {
	if i := strings.Index(rawURL, "://"); i >= 0 {
		rawURL = rawURL[i+3:]
	}
	if i := strings.Index(rawURL, "/"); i > 0 {
		rawURL = rawURL[:i]
	}
	if rawURL == "" {
		return "mirror"
	}
	return rawURL
}

func compactError(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	s = strings.ReplaceAll(s, "context deadline exceeded", "请求超时")
	s = strings.ReplaceAll(s, "context canceled", "请求已取消")
	s = strings.ReplaceAll(s, "Client.Timeout exceeded while awaiting headers", "等待响应超时")
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// EnsureGemini downloads and verifies the SentencePiece models. Existing files
// are re-checked so a corrupted or partial download is replaced automatically.
func (c *Counter) EnsureGemini(ctx context.Context) error {
	if c.offline {
		return fmt.Errorf("当前处于离线模式，无法下载 Gemini 分词器")
	}
	c.geminiDownloadMu.Lock()
	defer c.geminiDownloadMu.Unlock()
	var errs []error
	for _, spec := range geminiSpecs {
		path := filepath.Join(c.tokenDir, spec.File)
		if err := verifySHA256(path, spec.SHA256); err != nil {
			_ = os.Remove(path)
			if err := c.download(ctx, spec, path); err != nil {
				c.setGeminiError(spec.Name, err.Error())
				errs = append(errs, err)
				continue
			}
		}
		if _, err := c.loadAndStoreGemini(spec, path); err != nil {
			c.setGeminiError(spec.Name, err.Error())
			errs = append(errs, err)
			continue
		}
		c.setGeminiError(spec.Name, "")
	}
	return errors.Join(errs...)
}

// ResetGemini removes the local model files, loaded processors and cached
// errors. The next count or EnsureGemini call will download them again.
func (c *Counter) ResetGemini() error {
	c.geminiDownloadMu.Lock()
	defer c.geminiDownloadMu.Unlock()
	c.geminiMu.Lock()
	c.processors = map[string]*geminiProcessor{}
	c.geminiErrors = map[string]string{}
	c.geminiMu.Unlock()

	var errs []error
	for _, spec := range geminiSpecs {
		path := filepath.Join(c.tokenDir, spec.File)
		for _, p := range []string{path, path + ".tmp"} {
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				errs = append(errs, fmt.Errorf("删除 %s 失败: %w", filepath.Base(p), err))
			}
		}
	}
	return errors.Join(errs...)
}

type GeminiStatus struct {
	Name       string `json:"name"`
	File       string `json:"file"`
	Path       string `json:"path"`
	Downloaded bool   `json:"downloaded"`
	Loaded     bool   `json:"loaded"`
	Error      string `json:"error,omitempty"`
}

type Status struct {
	TokenizerDir string         `json:"tokenizerDir"`
	Offline      bool           `json:"offline"`
	Tiktoken     bool           `json:"tiktoken"`
	Gemini       []GeminiStatus `json:"gemini"`
}

func (c *Counter) Status() Status {
	st := Status{TokenizerDir: c.tokenDir, Offline: c.offline, Tiktoken: true}
	c.geminiMu.Lock()
	defer c.geminiMu.Unlock()
	for _, spec := range geminiSpecs {
		path := filepath.Join(c.tokenDir, spec.File)
		_, err := os.Stat(path)
		gs := GeminiStatus{Name: spec.Name, File: spec.File, Path: path, Downloaded: err == nil}
		if _, ok := c.processors[spec.Name]; ok {
			gs.Loaded = true
		}
		if e := c.geminiErrors[spec.Name]; e != "" {
			gs.Error = e
		}
		st.Gemini = append(st.Gemini, gs)
	}
	return st
}
