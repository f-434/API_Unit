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
