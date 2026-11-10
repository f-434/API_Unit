package tokens

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	Name     string
	File     string
	URL      string
	SHA256   string
	Families []string
}

var geminiSpecs = []geminiSpec{
	{
		Name: "gemma3", File: "gemma3_262144.model",
		URL:    "https://raw.githubusercontent.com/google/gemma_pytorch/014acb7ac4563a5f77c76d7ff98f31b568c16508/tokenizer/gemma3_cleaned_262144_v2.spiece.model",
		SHA256: "1299c11d7cf632ef3b4e11937501358ada021bbdf7c47638d13c0ee982f2e79c",
	},
	{
		Name: "gemma2", File: "gemma2.model",
		URL:    "https://raw.githubusercontent.com/google/gemma_pytorch/33b652c465537c6158f9a472ea5700e5e770ad3f/tokenizer/tokenizer.model",
		SHA256: "61a7b147390c64585d6c3543dd6fc636906c9af3865a5548f27f31aee1d4c8e2",
	},
}

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

func (c *Counter) loadGemini(spec geminiSpec) (*geminiProcessor, error) {
	c.geminiMu.Lock()
	defer c.geminiMu.Unlock()
	if p, ok := c.processors[spec.Name]; ok {
		return p, nil
	}
	if errStr, ok := c.geminiErrors[spec.Name]; ok {
		return nil, fmt.Errorf("%s", errStr)
	}
	path := filepath.Join(c.tokenDir, spec.File)
	if _, err := os.Stat(path); err != nil {
		if c.offline {
			err = fmt.Errorf("本地缺少 %s 模型文件且 OFFLINE=1", spec.File)
			c.geminiErrors[spec.Name] = err.Error()
			return nil, err
		}
		if err := c.download(spec, path); err != nil {
			c.geminiErrors[spec.Name] = err.Error()
			return nil, err
		}
	}
	proc, err := sentencepiece.NewProcessorFromPath(path)
	if err != nil {
		c.geminiErrors[spec.Name] = err.Error()
		return nil, err
	}
	p := &geminiProcessor{proc: proc, name: spec.Name}
	c.processors[spec.Name] = p
	return p, nil
}

func (c *Counter) download(spec geminiSpec, path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, spec.URL, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.client.Do(req)
	if err != nil {
		return fmt.Errorf("下载 %s 失败: %w", spec.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载 %s 失败: HTTP %d", spec.Name, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != spec.SHA256 {
		return fmt.Errorf("%s 校验失败（文件可能被篡改）", spec.Name)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// EnsureGemini downloads the SentencePiece models if they are missing.
func (c *Counter) EnsureGemini(ctx context.Context) error {
	if c.offline {
		return fmt.Errorf("当前处于离线模式，无法下载 Gemini 分词器")
	}
	for _, spec := range geminiSpecs {
		path := filepath.Join(c.tokenDir, spec.File)
		if _, err := os.Stat(path); err == nil {
			continue
		}
		if err := c.download(spec, path); err != nil {
			return err
		}
	}
	return nil
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
		if e, ok := c.geminiErrors[spec.Name]; ok {
			gs.Error = e
		}
		st.Gemini = append(st.Gemini, gs)
	}
	return st
}
