// Package config loads typed service configuration from environment variables.
//
// Secrets are injected into the container as env vars by the ECS task
// definition's secrets block (sourced from Secrets Manager), so there is no
// runtime Secrets Manager call here. Config implements slog.LogValuer to keep
// secret material out of logs.
package config

import (
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// Encryption modes for accounts.credentials_encrypted.
const (
	EncryptionLocalAES = "localaes" // AES-256-GCM, key from LOCAL_AES_KEY (dev/CI)
	EncryptionKMS      = "kms"      // AWS KMS envelope encryption (prod)
)

// Config is the fully resolved service configuration.
type Config struct {
	Env             string        // dev | staging | prod
	Port            string        // HTTP listen port
	LogLevel        string        // debug | info | warn | error
	ShutdownTimeout time.Duration // graceful shutdown budget
	MaxBodyBytes    int64         // request body cap

	DatabaseURL string // postgres DSN
	AWSRegion   string

	// CredentialsEncryption selects the Encryptor implementation for
	// per-account platform credentials.
	CredentialsEncryption string
	LocalAESKey           string // base64 of a 32-byte key (localaes mode)
	KMSKeyID              string // KMS key id/ARN/alias (kms mode), from CREDENTIALS_KMS_KEY_ID

	// InternalAPIToken authenticates internal callers (the webhook) on
	// /v1/ingest and /v1/accounts/lookup.
	InternalAPIToken string
}

// Load reads configuration from the environment, applying defaults. It does not
// validate; call Validate after Load.
func Load() Config {
	return Config{
		Env:                   getenv("ENV", "dev"),
		Port:                  getenv("PORT", "8080"),
		LogLevel:              getenv("LOG_LEVEL", "info"),
		ShutdownTimeout:       getenvDuration("SHUTDOWN_TIMEOUT", 10*time.Second),
		MaxBodyBytes:          getenvInt64("MAX_BODY_BYTES", 256*1024),
		DatabaseURL:           os.Getenv("DATABASE_URL"),
		AWSRegion:             getenv("AWS_REGION", "us-west-2"),
		CredentialsEncryption: getenv("CREDENTIALS_ENCRYPTION", EncryptionLocalAES),
		LocalAESKey:           os.Getenv("LOCAL_AES_KEY"),
		KMSKeyID:              os.Getenv("CREDENTIALS_KMS_KEY_ID"),
		InternalAPIToken:      os.Getenv("INTERNAL_API_TOKEN"),
	}
}

// Validate reports the first configuration problem, if any.
func (c Config) Validate() error {
	if c.DatabaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	if c.InternalAPIToken == "" {
		return fmt.Errorf("INTERNAL_API_TOKEN is required")
	}

	switch c.CredentialsEncryption {
	case EncryptionLocalAES:
		if c.LocalAESKey == "" {
			return fmt.Errorf("LOCAL_AES_KEY is required when CREDENTIALS_ENCRYPTION=localaes")
		}
		key, err := base64.StdEncoding.DecodeString(c.LocalAESKey)
		if err != nil {
			return fmt.Errorf("LOCAL_AES_KEY must be base64: %w", err)
		}
		if len(key) != 32 {
			return fmt.Errorf("LOCAL_AES_KEY must decode to 32 bytes, got %d", len(key))
		}
	case EncryptionKMS:
		if c.KMSKeyID == "" {
			return fmt.Errorf("CREDENTIALS_KMS_KEY_ID is required when CREDENTIALS_ENCRYPTION=kms")
		}
	default:
		return fmt.Errorf("CREDENTIALS_ENCRYPTION must be %q or %q, got %q",
			EncryptionLocalAES, EncryptionKMS, c.CredentialsEncryption)
	}

	return nil
}

// LogValue implements slog.LogValuer, redacting secret material.
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("env", c.Env),
		slog.String("port", c.Port),
		slog.String("log_level", c.LogLevel),
		slog.Duration("shutdown_timeout", c.ShutdownTimeout),
		slog.Int64("max_body_bytes", c.MaxBodyBytes),
		slog.String("aws_region", c.AWSRegion),
		slog.String("credentials_encryption", c.CredentialsEncryption),
		slog.String("kms_key_id", c.KMSKeyID),
		slog.String("database_url", redactDSN(c.DatabaseURL)),
		slog.String("internal_api_token", redact(c.InternalAPIToken)),
		slog.String("local_aes_key", redact(c.LocalAESKey)),
	)
}

// redact replaces any non-empty secret with a fixed marker.
func redact(s string) string {
	if s == "" {
		return ""
	}
	return "***"
}

// redactDSN strips userinfo (credentials) from a postgres URL, keeping the
// rest for observability.
func redactDSN(dsn string) string {
	if dsn == "" {
		return ""
	}
	scheme, rest, ok := strings.Cut(dsn, "://")
	if !ok {
		return "***"
	}
	if at := strings.IndexByte(rest, '@'); at >= 0 {
		rest = "***@" + rest[at+1:]
	}
	return scheme + "://" + rest
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getenvInt64(key string, fallback int64) int64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return fallback
}

func getenvDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}
