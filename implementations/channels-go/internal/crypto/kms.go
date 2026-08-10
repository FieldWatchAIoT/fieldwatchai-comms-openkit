package crypto

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kms"
)

// kmsClient is the subset of the AWS KMS API that KMS uses, so it can be faked
// in tests.
type kmsClient interface {
	Encrypt(ctx context.Context, in *kms.EncryptInput, opts ...func(*kms.Options)) (*kms.EncryptOutput, error)
	Decrypt(ctx context.Context, in *kms.DecryptInput, opts ...func(*kms.Options)) (*kms.DecryptOutput, error)
}

// KMS encrypts credentials with AWS KMS. Credentials are far below KMS's 4 KB
// plaintext limit, so it uses KMS Encrypt/Decrypt directly (the CiphertextBlob
// is the stored value) rather than client-side envelope encryption. If a cache
// or larger payloads are ever needed, switch to GenerateDataKey envelopes
// behind this same interface.
type KMS struct {
	client kmsClient
	keyID  string // KMS key id or alias, e.g. alias/fieldwatchai-comms-credentials
}

// NewKMS loads AWS config for the region and returns a KMS encryptor bound to
// the given key alias/id.
func NewKMS(ctx context.Context, region, keyID string) (*KMS, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	return newKMSWithClient(kms.NewFromConfig(cfg), keyID), nil
}

func newKMSWithClient(c kmsClient, keyID string) *KMS {
	return &KMS{client: c, keyID: keyID}
}

// Encrypt returns the KMS CiphertextBlob for plaintext.
func (k *KMS) Encrypt(ctx context.Context, plaintext []byte) ([]byte, error) {
	out, err := k.client.Encrypt(ctx, &kms.EncryptInput{
		KeyId:     aws.String(k.keyID),
		Plaintext: plaintext,
	})
	if err != nil {
		return nil, fmt.Errorf("kms encrypt: %w", err)
	}
	return out.CiphertextBlob, nil
}

// Decrypt returns the plaintext for a KMS CiphertextBlob. The key id is passed
// explicitly so decryption is pinned to the expected key (defense in depth for
// symmetric keys).
func (k *KMS) Decrypt(ctx context.Context, ciphertext []byte) ([]byte, error) {
	out, err := k.client.Decrypt(ctx, &kms.DecryptInput{
		CiphertextBlob: ciphertext,
		KeyId:          aws.String(k.keyID),
	})
	if err != nil {
		return nil, fmt.Errorf("kms decrypt: %w", err)
	}
	return out.Plaintext, nil
}
