// Package config loads the service configuration from environment variables.
// Secrets (the channels token and the UltraMSG webhook secret) are injected
// into the container as env vars by the ECS task definition's secrets block
// (sourced from Secrets Manager), so there is no runtime Secrets Manager call
// here. Config redacts secrets when logged.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the typed service configuration.
type Config struct {
	Env             string
	Port            string
	LogLevel        string
	MaxBodyBytes    int64
	ShutdownTimeout time.Duration

	ChannelsURL string
	AWSRegion   string
	SQSQueueURL string
	SQSDLQURL   string

	// Secrets — never logged in the clear (see LogValue).
	InternalAPIToken      string
	UltraMSGWebhookSecret string
	TwilioAuthToken       string
	TelegramWebhookSecret string
	EmailSESWebhookSecret string

	// EmailSESTopicARN is the SNS topic SES publishes inbound mail to; the
	// email-ses listener drops messages from any other topic. Required when
	// EmailSESWebhookSecret is set (the allowlist is load-bearing security).
	EmailSESTopicARN string

	// PublicBaseURL is the externally-visible origin (e.g.
	// https://webhook.example.com) used to reconstruct the URL
	// Twilio signed against, behind the ALB. Required when TwilioAuthToken is set.
	PublicBaseURL string

	AccountsMap      string
	DrainConcurrency int
}

// Load reads configuration from the environment, applying defaults.
func Load() Config {
	return Config{
		Env:                   getenv("ENV", "dev"),
		Port:                  getenv("PORT", "8080"),
		LogLevel:              getenv("LOG_LEVEL", "info"),
		MaxBodyBytes:          getenvInt64("MAX_BODY_BYTES", 256*1024),
		ShutdownTimeout:       getenvDuration("SHUTDOWN_TIMEOUT", 10*time.Second),
		ChannelsURL:           os.Getenv("CHANNELS_URL"),
		AWSRegion:             getenv("AWS_REGION", "us-west-2"),
		SQSQueueURL:           os.Getenv("SQS_QUEUE_URL"),
		SQSDLQURL:             os.Getenv("SQS_DLQ_URL"),
		InternalAPIToken:      os.Getenv("INTERNAL_API_TOKEN"),
		UltraMSGWebhookSecret: os.Getenv("WHATSAPP_ULTRAMSG_WEBHOOK_SECRET"),
		TwilioAuthToken:       os.Getenv("TWILIO_AUTH_TOKEN"),
		TelegramWebhookSecret: os.Getenv("TELEGRAM_WEBHOOK_SECRET"),
		EmailSESWebhookSecret: os.Getenv("EMAIL_SES_WEBHOOK_SECRET"),
		EmailSESTopicARN:      os.Getenv("EMAIL_SES_TOPIC_ARN"),
		PublicBaseURL:         os.Getenv("PUBLIC_BASE_URL"),
		AccountsMap:           os.Getenv("ACCOUNTS_MAP"),
		DrainConcurrency:      getenvInt("DRAIN_CONCURRENCY", 8),
	}
}

// Validate returns an error naming any missing required configuration.
func (c Config) Validate() error {
	var missing []string
	if c.ChannelsURL == "" {
		missing = append(missing, "CHANNELS_URL")
	}
	if c.InternalAPIToken == "" {
		missing = append(missing, "INTERNAL_API_TOKEN")
	}
	if c.UltraMSGWebhookSecret == "" {
		missing = append(missing, "WHATSAPP_ULTRAMSG_WEBHOOK_SECRET")
	}
	// The Twilio listener is opt-in (registered only when its token is set), but
	// signature verification needs the public origin Twilio signed against.
	if c.TwilioAuthToken != "" && c.PublicBaseURL == "" {
		missing = append(missing, "PUBLIC_BASE_URL (required when TWILIO_AUTH_TOKEN is set)")
	}
	// The email-ses listener is opt-in (registered only when its token is set),
	// but the topic-ARN allowlist is required so only our SES topic can post.
	if c.EmailSESWebhookSecret != "" && c.EmailSESTopicARN == "" {
		missing = append(missing, "EMAIL_SES_TOPIC_ARN (required when EMAIL_SES_WEBHOOK_SECRET is set)")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required config: %s", strings.Join(missing, ", "))
	}
	return nil
}

// LogValue implements slog.LogValuer so logging a Config redacts secrets,
// emitting only whether each secret is set.
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("env", c.Env),
		slog.String("port", c.Port),
		slog.String("log_level", c.LogLevel),
		slog.Int64("max_body_bytes", c.MaxBodyBytes),
		slog.Duration("shutdown_timeout", c.ShutdownTimeout),
		slog.String("channels_url", c.ChannelsURL),
		slog.String("aws_region", c.AWSRegion),
		slog.String("sqs_queue_url", c.SQSQueueURL),
		slog.String("sqs_dlq_url", c.SQSDLQURL),
		slog.String("public_base_url", c.PublicBaseURL),
		slog.String("email_ses_topic_arn", c.EmailSESTopicARN),
		slog.Int("drain_concurrency", c.DrainConcurrency),
		slog.Bool("internal_api_token_set", c.InternalAPIToken != ""),
		slog.Bool("ultramsg_webhook_secret_set", c.UltraMSGWebhookSecret != ""),
		slog.Bool("twilio_auth_token_set", c.TwilioAuthToken != ""),
		slog.Bool("telegram_webhook_secret_set", c.TelegramWebhookSecret != ""),
		slog.Bool("email_ses_webhook_secret_set", c.EmailSESWebhookSecret != ""),
		slog.Bool("accounts_map_set", c.AccountsMap != ""),
	)
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getenvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
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
