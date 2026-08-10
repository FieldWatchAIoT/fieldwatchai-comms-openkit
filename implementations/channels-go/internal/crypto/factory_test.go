package crypto

import (
	"context"
	"testing"
)

func TestNewLocalAESMode(t *testing.T) {
	enc, err := New(context.Background(), ModeLocalAES, "", key32(7), "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := enc.(*LocalAES); !ok {
		t.Fatalf("got %T, want *LocalAES", enc)
	}
	ct, err := enc.Encrypt(context.Background(), []byte("x"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if got, _ := enc.Decrypt(context.Background(), ct); string(got) != "x" {
		t.Errorf("round-trip failed")
	}
}

func TestNewKMSMode(t *testing.T) {
	enc, err := New(context.Background(), ModeKMS, "us-west-2", "", "alias/fieldwatchai-comms-credentials")
	if err != nil {
		t.Fatalf("New(kms): %v", err)
	}
	if _, ok := enc.(*KMS); !ok {
		t.Fatalf("got %T, want *KMS", enc)
	}
}

func TestNewRejectsUnknownMode(t *testing.T) {
	if _, err := New(context.Background(), "rot13", "", "", ""); err == nil {
		t.Error("New with unknown mode = nil error, want error")
	}
}

func TestNewPropagatesLocalAESKeyError(t *testing.T) {
	if _, err := New(context.Background(), ModeLocalAES, "", "not-base64!!!", ""); err == nil {
		t.Error("New with bad key = nil error, want error")
	}
}
