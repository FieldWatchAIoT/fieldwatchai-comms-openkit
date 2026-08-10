package crypto

import (
	"bytes"
	"context"
	"encoding/base64"
	"testing"
)

func key32(b byte) string {
	k := make([]byte, 32)
	for i := range k {
		k[i] = b
	}
	return base64.StdEncoding.EncodeToString(k)
}

func TestLocalAESRoundTrip(t *testing.T) {
	enc, err := NewLocalAES(key32(1))
	if err != nil {
		t.Fatalf("NewLocalAES: %v", err)
	}
	ctx := context.Background()
	plain := []byte("ultramsg-instance-token-xyz")

	ct, err := enc.Encrypt(ctx, plain)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Contains(ct, plain) {
		t.Fatal("ciphertext contains plaintext")
	}

	got, err := enc.Decrypt(ctx, ct)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("Decrypt = %q, want %q", got, plain)
	}
}

func TestLocalAESNonceIsRandom(t *testing.T) {
	enc, _ := NewLocalAES(key32(2))
	ctx := context.Background()
	a, _ := enc.Encrypt(ctx, []byte("same"))
	b, _ := enc.Encrypt(ctx, []byte("same"))
	if bytes.Equal(a, b) {
		t.Fatal("two encryptions of same plaintext are identical; nonce not random")
	}
}

func TestLocalAESWrongKeyFails(t *testing.T) {
	enc1, _ := NewLocalAES(key32(3))
	enc2, _ := NewLocalAES(key32(4))
	ctx := context.Background()

	ct, _ := enc1.Encrypt(ctx, []byte("secret"))
	if _, err := enc2.Decrypt(ctx, ct); err == nil {
		t.Fatal("Decrypt with wrong key succeeded, want auth failure")
	}
}

func TestLocalAESRejectsBadKey(t *testing.T) {
	if _, err := NewLocalAES(base64.StdEncoding.EncodeToString(make([]byte, 16))); err == nil {
		t.Error("NewLocalAES with 16-byte key = nil error, want error")
	}
	if _, err := NewLocalAES("not-base64!!!"); err == nil {
		t.Error("NewLocalAES with non-base64 = nil error, want error")
	}
}

func TestLocalAESDecryptRejectsGarbage(t *testing.T) {
	enc, _ := NewLocalAES(key32(5))
	if _, err := enc.Decrypt(context.Background(), []byte("too-short")); err == nil {
		t.Error("Decrypt of garbage = nil error, want error")
	}
}

// LocalAES must satisfy the Encryptor interface.
var _ Encryptor = (*LocalAES)(nil)
