package auth

// S3 access keys (PLAN 16.4, ADR 0006). An access key belongs to one token:
// its id is "LMB" and the token's 20 character id in capitals, and its
// secret is 40 random characters. SigV4 signs with the secret, so the
// server keeps it, encrypted with AES-256-GCM under the instance's access
// key encryption key, with the token id as associated data so a sealed
// secret can't be moved to another token.

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const accessKeyPrefix = "LMB"

// AccessKeyID is the access key id of a token record id (tok_<id>).
func AccessKeyID(tokenID string) string {
	return accessKeyPrefix + strings.ToUpper(strings.TrimPrefix(tokenID, "tok_"))
}

// TokenIDOfAccessKey returns the token record id an access key id names.
func TokenIDOfAccessKey(akid string) (string, bool) {
	if len(akid) != len(accessKeyPrefix)+idLen || !strings.HasPrefix(akid, accessKeyPrefix) {
		return "", false
	}
	short := strings.ToLower(akid[len(accessKeyPrefix):])
	if strings.ToUpper(short) != akid[len(accessKeyPrefix):] {
		return "", false
	}
	for _, c := range short {
		if !strings.ContainsRune(idChars, c) {
			return "", false
		}
	}
	return "tok_" + short, true
}

// NewAccessSecret makes an access key secret.
func NewAccessSecret() string { return randomString(secretLen, alphabet) }

// Sealer encrypts and decrypts access key secrets.
type Sealer struct{ aead cipher.AEAD }

// NewSealer builds a sealer from a 32 byte key.
func NewSealer(key []byte) (*Sealer, error) {
	if len(key) != 32 {
		return nil, errors.New("auth: the access key encryption key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: aead}, nil
}

// Seal encrypts a secret for a token: nonce followed by ciphertext.
func (s *Sealer) Seal(tokenID, secret string) []byte {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic(err)
	}
	return s.aead.Seal(nonce, nonce, []byte(secret), []byte(tokenID))
}

// Open decrypts a sealed secret of a token.
func (s *Sealer) Open(tokenID string, sealed []byte) (string, error) {
	n := s.aead.NonceSize()
	if len(sealed) < n {
		return "", errors.New("auth: sealed secret too short")
	}
	plain, err := s.aead.Open(nil, sealed[:n], sealed[n:], []byte(tokenID))
	if err != nil {
		return "", errors.New("auth: can't decrypt the access key secret (wrong encryption key?)")
	}
	return string(plain), nil
}

// LoadOrCreateKey reads the hex key file, creating it with a new random key
// (mode 600, folder mode 700) when it doesn't exist.
func LoadOrCreateKey(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err == nil {
		key, err := hex.DecodeString(strings.TrimSpace(string(b)))
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("auth: %s doesn't hold a 64 character hex key", path)
		}
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(hex.EncodeToString(key)+"\n"), 0o600); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return nil, err
	}
	return key, nil
}
