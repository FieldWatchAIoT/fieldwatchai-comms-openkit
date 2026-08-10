package crypto

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/kms"
)

// fakeKMS is an in-memory stand-in for the KMS client: it "encrypts" by
// prefixing, and records the KeyId it was called with.
type fakeKMS struct {
	lastKeyID string
	failNext  bool
}

func (f *fakeKMS) Encrypt(_ context.Context, in *kms.EncryptInput, _ ...func(*kms.Options)) (*kms.EncryptOutput, error) {
	if f.failNext {
		return nil, errors.New("boom")
	}
	if in.KeyId != nil {
		f.lastKeyID = *in.KeyId
	}
	blob := append([]byte("enc:"), in.Plaintext...)
	return &kms.EncryptOutput{CiphertextBlob: blob}, nil
}

func (f *fakeKMS) Decrypt(_ context.Context, in *kms.DecryptInput, _ ...func(*kms.Options)) (*kms.DecryptOutput, error) {
	if f.failNext {
		return nil, errors.New("boom")
	}
	if !bytes.HasPrefix(in.CiphertextBlob, []byte("enc:")) {
		return nil, errors.New("bad blob")
	}
	return &kms.DecryptOutput{Plaintext: bytes.TrimPrefix(in.CiphertextBlob, []byte("enc:"))}, nil
}

func TestKMSEncryptPassesKeyAndReturnsBlob(t *testing.T) {
	f := &fakeKMS{}
	enc := newKMSWithClient(f, "alias/fieldwatchai-comms-credentials")

	blob, err := enc.Encrypt(context.Background(), []byte("tok"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if f.lastKeyID != "alias/fieldwatchai-comms-credentials" {
		t.Errorf("KeyId = %q, want the alias", f.lastKeyID)
	}
	if !bytes.Equal(blob, []byte("enc:tok")) {
		t.Errorf("blob = %q", blob)
	}
}

func TestKMSRoundTrip(t *testing.T) {
	enc := newKMSWithClient(&fakeKMS{}, "alias/x")
	ct, err := enc.Encrypt(context.Background(), []byte("secret"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	pt, err := enc.Decrypt(context.Background(), ct)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(pt, []byte("secret")) {
		t.Errorf("Decrypt = %q, want secret", pt)
	}
}

func TestKMSPropagatesError(t *testing.T) {
	enc := newKMSWithClient(&fakeKMS{failNext: true}, "alias/x")
	if _, err := enc.Encrypt(context.Background(), []byte("x")); err == nil {
		t.Error("Encrypt error not propagated")
	}
}

var _ Encryptor = (*KMS)(nil)
