// Package secrets is Phase 5 encryption-at-rest for control-plane
// secrets (application env values).
//
// What: AES-256-GCM with a key from env or a gitignored file.
// Why: Postgres is not a vault, but plaintext env in the table is a
// backup leak. How: Seal prefixes ciphertext so legacy plaintext rows
// still Open. The key must never be logged or committed.
//
// This is not a secret manager (no rotation UI, no leases). Team vault
// products stay later. The worker must use the same key as the API or
// docker -e would receive ciphertext.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	prefix     = "enc:v1:"
	keyBytes   = 32
	defaultKey = ".forge/data.key"
)

// Box seals and opens strings. A nil Box leaves values unchanged so
// unit tests that do not care about encryption keep working.
type Box struct {
	gcm cipher.AEAD
}

// LoadOrCreate returns a Box.
//
// rawKey is hex or base64 of 32 bytes (FORGE_DATA_KEY). If empty, the
// file at keyFile is read or created. Creating a key on first start is
// the local operator story: no secret in git, no silent plaintext.
func LoadOrCreate(keyFile, rawKey string) (*Box, error) {
	key, err := resolveKey(keyFile, rawKey)
	if err != nil {
		return nil, err
	}
	return New(key)
}

// New builds a Box from a raw 32-byte key.
func New(key []byte) (*Box, error) {
	if len(key) != keyBytes {
		return nil, fmt.Errorf("data key must be %d bytes", keyBytes)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	return &Box{gcm: gcm}, nil
}

func resolveKey(keyFile, rawKey string) ([]byte, error) {
	rawKey = strings.TrimSpace(rawKey)
	if rawKey != "" {
		return parseKey(rawKey)
	}
	if strings.TrimSpace(keyFile) == "" {
		keyFile = defaultKey
	}
	if b, err := os.ReadFile(keyFile); err == nil {
		return parseKey(string(b))
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read data key: %w", err)
	}

	key := make([]byte, keyBytes)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate data key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(keyFile), 0o700); err != nil {
		return nil, fmt.Errorf("data key dir: %w", err)
	}
	// Hex on disk so the file is greppable as "not a password" and
	// parseKey accepts it. Mode 0600: group/world must not read it.
	if err := os.WriteFile(keyFile, []byte(hex.EncodeToString(key)+"\n"), 0o600); err != nil {
		return nil, fmt.Errorf("write data key: %w", err)
	}
	return key, nil
}

func parseKey(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if b, err := hex.DecodeString(raw); err == nil && len(b) == keyBytes {
		return b, nil
	}
	if b, err := base64.StdEncoding.DecodeString(raw); err == nil && len(b) == keyBytes {
		return b, nil
	}
	return nil, fmt.Errorf("data key must be 32-byte hex or base64")
}

// Seal encrypts plaintext. Empty stays empty (no nonce to store).
func (b *Box) Seal(plain string) (string, error) {
	if b == nil || plain == "" {
		return plain, nil
	}
	nonce := make([]byte, b.gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("nonce: %w", err)
	}
	out := b.gcm.Seal(nonce, nonce, []byte(plain), nil)
	return prefix + base64.StdEncoding.EncodeToString(out), nil
}

// Open decrypts a Seal result. Strings without the prefix are returned
// unchanged so Phase 3 plaintext rows still inject into docker -e.
func (b *Box) Open(stored string) (string, error) {
	if stored == "" || !strings.HasPrefix(stored, prefix) {
		return stored, nil
	}
	if b == nil {
		return "", fmt.Errorf("encrypted env value present but data key is not configured")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, prefix))
	if err != nil {
		return "", fmt.Errorf("decode secret: %w", err)
	}
	ns := b.gcm.NonceSize()
	if len(raw) < ns {
		return "", fmt.Errorf("ciphertext too short")
	}
	plain, err := b.gcm.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", fmt.Errorf("open secret: %w", err)
	}
	return string(plain), nil
}

// Sealed reports whether the stored cell is ciphertext.
func Sealed(stored string) bool {
	return strings.HasPrefix(stored, prefix)
}
