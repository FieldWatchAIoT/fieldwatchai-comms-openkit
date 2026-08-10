package config

import (
	"encoding/base64"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// renderAttrs flattens a slog.Value (expected to be a group) into a string so
// tests can assert which fields are present or redacted when the config is logged.
func renderAttrs(v slog.Value) string {
	var b strings.Builder
	if v.Kind() != slog.KindGroup {
		return v.String()
	}
	for _, a := range v.Group() {
		fmt.Fprintf(&b, "%s=%s ", a.Key, a.Value.String())
	}
	return b.String()
}

// validAESKey is a base64-encoded 32-byte key for localaes tests.
var validAESKey = base64.StdEncoding.EncodeToString(make([]byte, 32))

// setMinimalEnv sets the env vars required for a valid localaes config so a test
// can override one variable at a time and still start from a passing baseline.
func setMinimalEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/fcc")
	t.Setenv("INTERNAL_API_TOKEN", "internal-secret")
	t.Setenv("CREDENTIALS_ENCRYPTION", "localaes")
	t.Setenv("LOCAL_AES_KEY", validAESKey)
}

func TestLoadDefaults(t *testing.T) {
	setMinimalEnv(t)
	c := Load()

	if c.Port != "8080" {
		t.Errorf("Port default = %q, want 8080", c.Port)
	}
	if c.LogLevel != "info" {
		t.Errorf("LogLevel default = %q, want info", c.LogLevel)
	}
	if c.Env != "dev" {
		t.Errorf("Env default = %q, want dev", c.Env)
	}
	if c.ShutdownTimeout != 10*time.Second {
		t.Errorf("ShutdownTimeout default = %v, want 10s", c.ShutdownTimeout)
	}
	if c.MaxBodyBytes != 256*1024 {
		t.Errorf("MaxBodyBytes default = %d, want %d", c.MaxBodyBytes, 256*1024)
	}
}

func TestLoadOverrides(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("PORT", "9090")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("ENV", "prod")
	t.Setenv("SHUTDOWN_TIMEOUT", "30s")
	t.Setenv("MAX_BODY_BYTES", "1048576")

	c := Load()

	if c.Port != "9090" {
		t.Errorf("Port = %q, want 9090", c.Port)
	}
	if c.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want debug", c.LogLevel)
	}
	if c.Env != "prod" {
		t.Errorf("Env = %q, want prod", c.Env)
	}
	if c.ShutdownTimeout != 30*time.Second {
		t.Errorf("ShutdownTimeout = %v, want 30s", c.ShutdownTimeout)
	}
	if c.MaxBodyBytes != 1048576 {
		t.Errorf("MaxBodyBytes = %d, want 1048576", c.MaxBodyBytes)
	}
}

func TestValidateOK(t *testing.T) {
	setMinimalEnv(t)
	if err := Load().Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

func TestValidateRequiresDatabaseURL(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("DATABASE_URL", "")
	err := Load().Validate()
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("Validate() = %v, want error mentioning DATABASE_URL", err)
	}
}

func TestValidateRequiresInternalToken(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("INTERNAL_API_TOKEN", "")
	err := Load().Validate()
	if err == nil || !strings.Contains(err.Error(), "INTERNAL_API_TOKEN") {
		t.Fatalf("Validate() = %v, want error mentioning INTERNAL_API_TOKEN", err)
	}
}

func TestValidateRejectsUnknownEncryption(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("CREDENTIALS_ENCRYPTION", "rot13")
	err := Load().Validate()
	if err == nil || !strings.Contains(err.Error(), "CREDENTIALS_ENCRYPTION") {
		t.Fatalf("Validate() = %v, want error mentioning CREDENTIALS_ENCRYPTION", err)
	}
}

func TestValidateLocalAESRequires32ByteKey(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("LOCAL_AES_KEY", base64.StdEncoding.EncodeToString(make([]byte, 16)))
	err := Load().Validate()
	if err == nil || !strings.Contains(err.Error(), "LOCAL_AES_KEY") {
		t.Fatalf("Validate() = %v, want error mentioning LOCAL_AES_KEY length", err)
	}
}

func TestValidateLocalAESRejectsMissingKey(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("LOCAL_AES_KEY", "")
	err := Load().Validate()
	if err == nil || !strings.Contains(err.Error(), "LOCAL_AES_KEY") {
		t.Fatalf("Validate() = %v, want error mentioning LOCAL_AES_KEY", err)
	}
}

func TestValidateKMSRequiresKeyID(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("CREDENTIALS_ENCRYPTION", "kms")
	t.Setenv("LOCAL_AES_KEY", "")
	t.Setenv("CREDENTIALS_KMS_KEY_ID", "")
	err := Load().Validate()
	if err == nil || !strings.Contains(err.Error(), "CREDENTIALS_KMS_KEY_ID") {
		t.Fatalf("Validate() = %v, want error mentioning CREDENTIALS_KMS_KEY_ID", err)
	}
}

func TestValidateKMSOK(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("CREDENTIALS_ENCRYPTION", "kms")
	t.Setenv("LOCAL_AES_KEY", "")
	t.Setenv("CREDENTIALS_KMS_KEY_ID", "arn:aws:kms:us-west-2:968062515009:key/b66fa656-849f-4e92-bde2-37af1da09be7")
	if err := Load().Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

// LogValue must never expose secret material when the config is logged.
func TestLogValueRedactsSecrets(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("INTERNAL_API_TOKEN", "super-secret-token")
	c := Load()

	rendered := renderAttrs(c.LogValue())
	for _, secret := range []string{"super-secret-token", validAESKey, "u:p"} {
		if strings.Contains(rendered, secret) {
			t.Errorf("LogValue() leaked secret %q in: %s", secret, rendered)
		}
	}
	// Non-secret fields should still be present for observability.
	if !strings.Contains(rendered, "8080") {
		t.Errorf("LogValue() missing non-secret Port; got: %s", rendered)
	}
}
