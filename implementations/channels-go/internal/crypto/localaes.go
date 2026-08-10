package crypto

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

// LocalAES encrypts with AES-256-GCM using a single symmetric key from the
// environment. Intended for dev/CI; production uses KMS. The ciphertext layout
// is nonce || gcm_sealed, so it is self-contained.
type LocalAES struct {
	gcm cipher.AEAD
}

// NewLocalAES builds a LocalAES from a base64-encoded 32-byte key (AES-256).
func NewLocalAES(base64Key string) (*LocalAES, error) {
	key, err := base64.StdEncoding.DecodeString(base64Key)
	if err != nil {
		return nil, fmt.Errorf("local aes key must be base64: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("local aes key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new gcm: %w", err)
	}
	return &LocalAES{gcm: gcm}, nil
}

// Encrypt returns nonce || sealed(plaintext). A fresh random nonce is generated
// per call, so encrypting the same plaintext twice yields different ciphertext.
func (l *LocalAES) Encrypt(_ context.Context, plaintext []byte) ([]byte, error) {
	nonce := make([]byte, l.gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("read nonce: %w", err)
	}
	// Seal appends the ciphertext to nonce, giving nonce||ciphertext.
	return l.gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt reverses Encrypt, returning an error if the input is too short or
// authentication fails (wrong key or tampered ciphertext).
func (l *LocalAES) Decrypt(_ context.Context, ciphertext []byte) ([]byte, error) {
	ns := l.gcm.NonceSize()
	if len(ciphertext) < ns {
		return nil, errors.New("ciphertext too short")
	}
	nonce, sealed := ciphertext[:ns], ciphertext[ns:]
	plain, err := l.gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	return plain, nil
}
