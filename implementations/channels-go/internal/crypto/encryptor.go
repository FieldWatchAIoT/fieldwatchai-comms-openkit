// Package crypto provides pluggable encryption for per-account platform
// credentials stored in accounts.credentials_encrypted. It is the one net-new
// abstraction in comms-channels: no other FieldWatch service stores per-row
// secrets in its database.
//
// Two implementations, selected by config (CREDENTIALS_ENCRYPTION):
//   - LocalAES: AES-256-GCM with a key from the environment (dev/CI).
//   - KMS:      AWS KMS envelope encryption (prod).
//
// Only Encrypt is on the Week 1 path (account create/update). Decrypt is
// exercised at outbound-dispatch time (later milestone) but implemented now.
package crypto

import "context"

// Encryptor encrypts and decrypts small secrets (platform credentials). The
// returned ciphertext is opaque and self-describing for its implementation
// (e.g. nonce-prepended for LocalAES, an EncryptedDataKey envelope for KMS).
type Encryptor interface {
	Encrypt(ctx context.Context, plaintext []byte) ([]byte, error)
	Decrypt(ctx context.Context, ciphertext []byte) ([]byte, error)
}
