package config

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// clearEnv blanks every config env var for a test so Load sees defaults.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"ENV", "PORT", "LOG_LEVEL", "MAX_BODY_BYTES", "SHUTDOWN_TIMEOUT",
		"CHANNELS_URL", "AWS_REGION", "SQS_QUEUE_URL", "SQS_DLQ_URL",
		"INTERNAL_API_TOKEN", "WHATSAPP_ULTRAMSG_WEBHOOK_SECRET",
		"DRAIN_CONCURRENCY",
		"TWILIO_AUTH_TOKEN", "PUBLIC_BASE_URL", "TELEGRAM_WEBHOOK_SECRET",
		"EMAIL_SES_WEBHOOK_SECRET", "EMAIL_SES_TOPIC_ARN",
	} {
		t.Setenv(k, "")
	}
}

func TestLoad_Defaults(t *testing.T) {
	clearEnv(t)
	c := Load()
	if c.Env != "dev" || c.Port != "8080" || c.LogLevel != "info" {
		t.Errorf("defaults wrong: env=%q port=%q log=%q", c.Env, c.Port, c.LogLevel)
	}
	if c.MaxBodyBytes != 262144 {
		t.Errorf("MaxBodyBytes = %d, want 262144", c.MaxBodyBytes)
	}
	if c.ShutdownTimeout != 10*time.Second {
		t.Errorf("ShutdownTimeout = %v, want 10s", c.ShutdownTimeout)
	}
	if c.DrainConcurrency < 1 {
		t.Errorf("DrainConcurrency = %d, want >= 1", c.DrainConcurrency)
	}
}

func TestLoad_ParsesEnv(t *testing.T) {
	clearEnv(t)
	t.Setenv("ENV", "prod")
	t.Setenv("PORT", "9090")
	t.Setenv("MAX_BODY_BYTES", "1024")
	t.Setenv("SHUTDOWN_TIMEOUT", "30s")
	t.Setenv("CHANNELS_URL", "https://channels.internal")
	t.Setenv("INTERNAL_API_TOKEN", "tok")
	t.Setenv("WHATSAPP_ULTRAMSG_WEBHOOK_SECRET", "sek")
	t.Setenv("DRAIN_CONCURRENCY", "16")
	c := Load()
	if c.Env != "prod" || c.Port != "9090" || c.MaxBodyBytes != 1024 || c.ShutdownTimeout != 30*time.Second {
		t.Errorf("parse wrong: %+v", c)
	}
	if c.ChannelsURL != "https://channels.internal" || c.DrainConcurrency != 16 {
		t.Errorf("parse wrong: %+v", c)
	}
}

func TestValidate_MissingRequiredErrors(t *testing.T) {
	clearEnv(t)
	c := Load()
	err := c.Validate()
	if err == nil {
		t.Fatal("expected validation error for missing required fields")
	}
	for _, want := range []string{"CHANNELS_URL", "INTERNAL_API_TOKEN", "WHATSAPP_ULTRAMSG_WEBHOOK_SECRET"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %s", err.Error(), want)
		}
	}
}

func TestValidate_OK(t *testing.T) {
	clearEnv(t)
	t.Setenv("CHANNELS_URL", "https://c")
	t.Setenv("INTERNAL_API_TOKEN", "tok")
	t.Setenv("WHATSAPP_ULTRAMSG_WEBHOOK_SECRET", "sek")
	if err := Load().Validate(); err != nil {
		t.Errorf("Validate = %v, want nil", err)
	}
}

func TestLoad_ParsesTwilio(t *testing.T) {
	clearEnv(t)
	t.Setenv("TWILIO_AUTH_TOKEN", "twtok")
	t.Setenv("PUBLIC_BASE_URL", "https://webhook.example.com")
	c := Load()
	if c.TwilioAuthToken != "twtok" || c.PublicBaseURL != "https://webhook.example.com" {
		t.Errorf("twilio config not parsed: %+v", c)
	}
}

// A Twilio auth token is useless without the public base URL it signs against,
// so configuring one without the other is a validation error.
func TestValidate_TwilioAuthTokenRequiresPublicBaseURL(t *testing.T) {
	clearEnv(t)
	t.Setenv("CHANNELS_URL", "https://c")
	t.Setenv("INTERNAL_API_TOKEN", "tok")
	t.Setenv("WHATSAPP_ULTRAMSG_WEBHOOK_SECRET", "sek")
	t.Setenv("TWILIO_AUTH_TOKEN", "twtok") // but no PUBLIC_BASE_URL
	err := Load().Validate()
	if err == nil || !strings.Contains(err.Error(), "PUBLIC_BASE_URL") {
		t.Errorf("expected error naming PUBLIC_BASE_URL, got %v", err)
	}
}

func TestLoad_ParsesEmailSES(t *testing.T) {
	clearEnv(t)
	t.Setenv("EMAIL_SES_WEBHOOK_SECRET", "emsek")
	t.Setenv("EMAIL_SES_TOPIC_ARN", "arn:aws:sns:us-west-2:1:topic")
	c := Load()
	if c.EmailSESWebhookSecret != "emsek" || c.EmailSESTopicARN != "arn:aws:sns:us-west-2:1:topic" {
		t.Errorf("email-ses config not parsed: %+v", c)
	}
}

// The SES topic-ARN allowlist is load-bearing security (only our SES topic may
// post inbound mail), so enabling the listener without it is a validation error.
func TestValidate_EmailSESSecretRequiresTopicARN(t *testing.T) {
	clearEnv(t)
	t.Setenv("CHANNELS_URL", "https://c")
	t.Setenv("INTERNAL_API_TOKEN", "tok")
	t.Setenv("WHATSAPP_ULTRAMSG_WEBHOOK_SECRET", "sek")
	t.Setenv("EMAIL_SES_WEBHOOK_SECRET", "emsek") // but no EMAIL_SES_TOPIC_ARN
	err := Load().Validate()
	if err == nil || !strings.Contains(err.Error(), "EMAIL_SES_TOPIC_ARN") {
		t.Errorf("expected error naming EMAIL_SES_TOPIC_ARN, got %v", err)
	}
}

// TestConfig_LogValueRedactsSecrets is the no-secret-in-logs guard: logging the
// config must never emit the token or webhook secret values.
func TestConfig_LogValueRedactsSecrets(t *testing.T) {
	clearEnv(t)
	t.Setenv("CHANNELS_URL", "https://c")
	t.Setenv("INTERNAL_API_TOKEN", "SUPERSECRETTOKEN")
	t.Setenv("WHATSAPP_ULTRAMSG_WEBHOOK_SECRET", "SUPERSECRETHOOK")
	t.Setenv("TWILIO_AUTH_TOKEN", "SUPERSECRETTWILIO")
	t.Setenv("TELEGRAM_WEBHOOK_SECRET", "SUPERSECRETTG")
	t.Setenv("EMAIL_SES_WEBHOOK_SECRET", "SUPERSECRETEMAIL")
	c := Load()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	logger.Info("boot config", "config", c)

	out := buf.String()
	if strings.Contains(out, "SUPERSECRETTOKEN") || strings.Contains(out, "SUPERSECRETHOOK") || strings.Contains(out, "SUPERSECRETTWILIO") || strings.Contains(out, "SUPERSECRETTG") || strings.Contains(out, "SUPERSECRETEMAIL") {
		t.Errorf("log leaked a secret: %s", out)
	}
	if !strings.Contains(out, "channels_url") {
		t.Errorf("expected non-secret fields in log, got: %s", out)
	}
}
