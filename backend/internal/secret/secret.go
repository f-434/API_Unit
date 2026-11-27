// Package secret provides at-rest encryption for stored API keys and a small
// HMAC helper used to sign session cookies.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
)

type Box struct {
	aead cipher.AEAD
	key  []byte
}

// LoadOrCreate reads a 32-byte key from path, creating it when absent.
func LoadOrCreate(path string) (*Box, error) {
	key, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, key, 0o600); err != nil {
			return nil, err
		}
	}
	// 兼容历史数据：无论文件何时创建，都收回其他用户的读取权限。
	_ = os.Chmod(path, 0o600)
	return New(key)
}

// LoadOrCreateFrom 允许主密钥来自环境变量或独立的密钥文件，
// 从而与 DATA_DIR 中的 SQLite 数据分开存放（Docker secrets、K8s Secret）。
// 两者都为空时回退到 path 指定的密钥文件。
func LoadOrCreateFrom(path, envValue, envFile string) (*Box, error) {
	if raw := strings.TrimSpace(envValue); raw != "" {
		key, err := decodeKey(raw)
		if err != nil {
			return nil, fmt.Errorf("APIUNIT_MASTER_KEY 无效: %w", err)
		}
		return New(key)
	}
	if file := strings.TrimSpace(envFile); file != "" {
		raw, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("读取 APIUNIT_MASTER_KEY_FILE 失败: %w", err)
		}
		key, err := decodeKey(string(raw))
		if err != nil {
			return nil, fmt.Errorf("APIUNIT_MASTER_KEY_FILE 内容无效: %w", err)
		}
		return New(key)
	}
	return LoadOrCreate(path)
}

// New 用 32 字节原始密钥构造加解密器。
func New(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("secret key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead, key: key}, nil
}

// decodeKey 支持 32 字节原始内容、64 位十六进制以及常见 Base64 变体。
func decodeKey(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("密钥为空")
	}
	if b, err := hex.DecodeString(raw); err == nil && len(b) == 32 {
		return b, nil
	}
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding,
	} {
		if b, err := enc.DecodeString(raw); err == nil && len(b) == 32 {
			return b, nil
		}
	}
	if len(raw) == 32 {
		return []byte(raw), nil
	}
	return nil, fmt.Errorf("需要 32 字节密钥（原始/hex/base64）")
}

func (b *Box) Encrypt(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := b.aead.Seal(nonce, nonce, []byte(plain), nil)
	return base64.RawStdEncoding.EncodeToString(out), nil
}

func (b *Box) Decrypt(enc string) (string, error) {
	if enc == "" {
		return "", nil
	}
	raw, err := base64.RawStdEncoding.DecodeString(enc)
	if err != nil {
		return "", err
	}
	ns := b.aead.NonceSize()
	if len(raw) < ns {
		return "", fmt.Errorf("ciphertext too short")
	}
	plain, err := b.aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// Sign returns a hex HMAC-SHA256 over data using a key derived from the master key.
func (b *Box) Sign(data string) string {
	h := hmac.New(sha256.New, b.key)
	h.Write([]byte("sign:"))
	h.Write([]byte(data))
	return hex.EncodeToString(h.Sum(nil))
}

func (b *Box) Verify(data, sig string) bool {
	want := b.Sign(data)
	got, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}
	wantRaw, _ := hex.DecodeString(want)
	return hmac.Equal(wantRaw, got)
}
