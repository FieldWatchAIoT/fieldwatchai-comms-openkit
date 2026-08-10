package accounts

import (
	"context"
	"errors"
	"testing"
)

const sampleMap = `{"whatsapp-ultramsg":{"instance123":"acc_w7m2f4","instance999":"acc_zzz"},"sms-twilio":{"+12025550000":"acc_sms"}}`

// TestConfigResolver_ResolvesKnownIdentifier confirms a registered
// platform+identifier maps to its account_id.
func TestConfigResolver_ResolvesKnownIdentifier(t *testing.T) {
	r, err := NewConfigResolver(sampleMap)
	if err != nil {
		t.Fatalf("NewConfigResolver: %v", err)
	}
	acc, err := r.Resolve(context.Background(), "whatsapp-ultramsg", "instance123")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if acc.ID != "acc_w7m2f4" {
		t.Errorf("account id = %q, want acc_w7m2f4", acc.ID)
	}
}

// TestConfigResolver_UnknownIdentifierIsNotFound confirms an unregistered
// identifier returns ErrNotFound (the handler turns this into a drop-with-200).
func TestConfigResolver_UnknownIdentifierIsNotFound(t *testing.T) {
	r, _ := NewConfigResolver(sampleMap)
	_, err := r.Resolve(context.Background(), "whatsapp-ultramsg", "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// TestConfigResolver_UnknownPlatformIsNotFound confirms an unregistered
// platform returns ErrNotFound rather than panicking on a nil submap.
func TestConfigResolver_UnknownPlatformIsNotFound(t *testing.T) {
	r, _ := NewConfigResolver(sampleMap)
	_, err := r.Resolve(context.Background(), "telegram", "instance123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// TestNewConfigResolver_MalformedJSONErrors confirms a bad ACCOUNTS_MAP fails
// fast at startup rather than silently resolving nothing at runtime.
func TestNewConfigResolver_MalformedJSONErrors(t *testing.T) {
	if _, err := NewConfigResolver(`{not json`); err == nil {
		t.Error("expected error for malformed JSON, got nil")
	}
}

// TestNewConfigResolver_EmptyIsUsableButResolvesNothing confirms an empty map
// is valid (e.g. no accounts configured yet) and resolves to ErrNotFound.
func TestNewConfigResolver_EmptyIsUsableButResolvesNothing(t *testing.T) {
	r, err := NewConfigResolver("")
	if err != nil {
		t.Fatalf("empty map should be valid: %v", err)
	}
	if _, err := r.Resolve(context.Background(), "whatsapp-ultramsg", "instance123"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// TestConfigResolver_SatisfiesResolverInterface is a compile-time guard that
// the config impl satisfies the Resolver interface consumers depend on.
func TestConfigResolver_SatisfiesResolverInterface(t *testing.T) {
	var _ Resolver = (*ConfigResolver)(nil)
}
