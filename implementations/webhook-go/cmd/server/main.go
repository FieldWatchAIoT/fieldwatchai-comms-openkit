// Command server runs the comms-openkit webhook receiver: the inbound transport
// layer for the FieldWatch Comms Hub reference stack. It receives platform
// webhooks, normalizes them to the canonical schema, buffers them durably, and
// forwards them to a downstream "channels" service (see CHANNELS_URL). This
// file is the composition root — it wires the internal packages together and
// owns process lifecycle.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/accounts"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/config"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/drain"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/httpapi"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/listeners"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/listeners/email"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/listeners/telegram"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/listeners/twilio"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/listeners/ultramsg"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/publisher"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/queue"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "event", "fatal", "error", err.Error())
		os.Exit(1)
	}
}

func run() error {
	cfg := config.Load()
	logger := newLogger(cfg.LogLevel, cfg.Env)
	slog.SetDefault(logger)

	if err := cfg.Validate(); err != nil {
		return err
	}
	logger.Info("config loaded", "event", "boot", "config", cfg)

	// Account resolution is delegated to comms-channels' lookup endpoint
	// (channels owns the account DB). Results cache ~60s; a transient lookup
	// failure surfaces as an error so the inbound is retried, not dropped.
	resolver := accounts.NewHTTPResolver(cfg.ChannelsURL, cfg.InternalAPIToken, &http.Client{Timeout: 10 * time.Second})

	// rootCtx is cancelled on SIGINT/SIGTERM; the drain worker stops with it.
	rootCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	q, err := buildQueue(rootCtx, cfg, logger)
	if err != nil {
		return err
	}

	pub := publisher.NewHTTP(cfg.ChannelsURL, cfg.InternalAPIToken, &http.Client{Timeout: 15 * time.Second})

	// Listeners — drop a new platform in here. Twilio is opt-in: registered only
	// when its auth token is configured (Validate guarantees PUBLIC_BASE_URL too).
	ls := []listeners.Listener{ultramsg.New(cfg.UltraMSGWebhookSecret, resolver, logger)}
	if cfg.TwilioAuthToken != "" {
		// One Twilio account auth-token serves both channels; each is its own
		// listener (id = account lookup type, served at /inbound/<id>).
		ls = append(ls,
			twilio.New("whatsapp-twilio", cfg.TwilioAuthToken, cfg.PublicBaseURL, resolver, logger),
			twilio.New("sms-twilio", cfg.TwilioAuthToken, cfg.PublicBaseURL, resolver, logger),
		)
		logger.Info("twilio listeners enabled", "event", "boot.twilio_enabled")
	}
	if cfg.TelegramWebhookSecret != "" {
		ls = append(ls, telegram.New(cfg.TelegramWebhookSecret, resolver, logger))
		logger.Info("telegram listener enabled", "event", "boot.telegram_enabled")
	}
	if cfg.EmailSESWebhookSecret != "" {
		// SES → SNS → HTTPS subscription. The client confirms the SNS handshake.
		ls = append(ls, email.New(cfg.EmailSESWebhookSecret, cfg.EmailSESTopicARN, resolver, logger, &http.Client{Timeout: 10 * time.Second}))
		logger.Info("email-ses listener enabled", "event", "boot.email_ses_enabled")
	}

	srv := httpapi.NewServer(logger)
	srv.MaxBodyBytes = cfg.MaxBodyBytes
	listeners.Register(srv.Mux(), q, logger, ls...)

	// Drain worker forwards buffered messages to channels.
	worker := drain.New(q, pub, logger, cfg.DrainConcurrency)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = worker.Run(rootCtx)
	}()

	srv.MarkReady()
	httpServer := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("server start", "event", "server.start", "addr", httpServer.Addr, "env", cfg.Env)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		stop()
		wg.Wait()
		return err
	case <-rootCtx.Done():
		logger.Info("shutdown signal", "event", "server.shutdown_start")
	}

	// Fail readiness first so the LB drains us, then shut down within the budget.
	srv.MarkNotReady()
	shutCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := httpServer.Shutdown(shutCtx); err != nil {
		logger.Error("shutdown error", "event", "server.shutdown_error", "error", err.Error())
	}
	wg.Wait() // wait for the drain worker to stop
	logger.Info("shutdown done", "event", "server.shutdown_done")
	return nil
}

// buildQueue selects the durable SQS queue when configured, else an in-memory
// queue for local/dev (logged loudly because it is not durable).
func buildQueue(ctx context.Context, cfg config.Config, logger *slog.Logger) (queue.Queue, error) {
	if cfg.SQSQueueURL == "" {
		logger.Warn("no SQS_QUEUE_URL set — using in-memory queue (NOT durable; dev/local only)",
			"event", "boot.queue_memory")
		return queue.NewMemory(), nil
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.AWSRegion))
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	logger.Info("using SQS queue", "event", "boot.queue_sqs", "url", cfg.SQSQueueURL)
	return queue.NewSQS(sqs.NewFromConfig(awsCfg), cfg.SQSQueueURL), nil
}

func newLogger(level, env string) *slog.Logger {
	var l slog.Level
	switch level {
	case "debug", "DEBUG":
		l = slog.LevelDebug
	case "warn", "WARN":
		l = slog.LevelWarn
	case "error", "ERROR":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	// Rename the default time/msg keys to ts/message — a common log-shape
	// convention that CloudWatch metric filters (and most log aggregators) parse
	// cleanly.
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: l,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			switch a.Key {
			case slog.TimeKey:
				a.Key = "ts"
			case slog.MessageKey:
				a.Key = "message"
			}
			return a
		},
	})
	return slog.New(h).With("service", "comms-webhook", "env", env)
}
