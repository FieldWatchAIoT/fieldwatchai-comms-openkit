// Command server is the comms-channels HTTP service: the brain of the FieldWatch
// Comms Hub. This is the composition root — it wires config, logging, the HTTP
// server, and graceful shutdown. Feature wiring (DB pool, ingest, accounts) is
// layered in by subsequent stories.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/accounts"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/channels"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/config"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/contacts"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/crypto"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/db"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/httpapi"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/ingest"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/integrations/emailses"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/integrations/telegram"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/integrations/twilio"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/integrations/ultramsg"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/outbound"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/outboundapi"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/parser"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/policy"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/queries/goqueries"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/replay"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/resolver"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/workflow"
	"github.com/google/uuid"
)

func main() {
	cfg := config.Load()
	logger := newLogger(cfg.LogLevel, cfg.Env)
	slog.SetDefault(logger)

	if err := cfg.Validate(); err != nil {
		logger.Error("invalid configuration", "event", "boot", "error", err.Error())
		os.Exit(1)
	}
	logger.Info("starting comms-channels", "event", "boot", "config", cfg)

	rootCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.NewPool(rootCtx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("database unavailable", "event", "boot", "error", err.Error())
		os.Exit(1)
	}
	defer pool.Close()
	logger.Info("database pool ready", "event", "db.pool_ready", "max_conns", pool.Config().MaxConns)

	// Apply embedded migrations on boot (idempotent; advisory-locked).
	if err := db.RunMigrations(cfg.DatabaseURL); err != nil {
		logger.Error("migrations failed", "event", "migrate.failed", "error", err.Error())
		os.Exit(1)
	}
	logger.Info("migrations applied", "event", "migrate.applied")

	enc, err := crypto.New(rootCtx, cfg.CredentialsEncryption, cfg.AWSRegion, cfg.LocalAESKey, cfg.KMSKeyID)
	if err != nil {
		logger.Error("encryptor init failed", "event", "boot", "error", err.Error())
		os.Exit(1)
	}
	queries := goqueries.New(pool)

	srv := httpapi.NewServer(logger)
	srv.MaxBodyBytes = cfg.MaxBodyBytes

	// Internal-authenticated v1 routes. Account CRUD uses the shared bearer for
	// Week 1 (JWT replaces it later). Mounted under both the exact and subtree
	// paths so /v1/accounts and /v1/accounts/{id} both resolve.
	acctHandler := accounts.NewHandler(accounts.NewService(queries, enc, time.Now, uuid.New), logger)
	// Outbound adapter registry — UltraMSG WhatsApp is the only live transport.
	dispatcher := outbound.NewRegistry()
	dispatcher.Register("whatsapp", ultramsg.New())
	dispatcher.Register("whatsapp-twilio", twilio.New())
	dispatcher.Register("sms-twilio", twilio.New())
	dispatcher.Register("telegram", telegram.New())
	// Email via SES — authenticates via the task IAM role (no per-account token).
	emailSender, err := emailses.NewSES(rootCtx, cfg.AWSRegion)
	if err != nil {
		logger.Error("email adapter init failed", "event", "boot", "error", err.Error())
		os.Exit(1)
	}
	dispatcher.Register(outbound.TypeEmailSES, emailSender)

	// Default command set + thresholds until per-channel parser_config + channel
	// resolution land.
	parserCfg := parser.Config{Commands: []string{"STATUS", "NEEDS", "DAMAGE", "MISSING", "RESOURCE", "HERE", "NOTE", "SOS"}}
	ingestSvc := ingest.NewService(ingest.Deps{
		Store:      queries,
		Resolver:   resolver.New(queries),
		Encryptor:  enc,
		Dispatcher: dispatcher,
		Forwarder:  workflow.New(),
		ParserCfg:  parserCfg,
		Thresholds: policy.DefaultThresholds,
		Logger:     logger,
		Now:        time.Now,
		NewID:      uuid.New,
	})
	ingestHandler := ingest.NewHandler(ingestSvc, logger)
	v1 := http.NewServeMux()
	contactHandler := contacts.NewHandler(contacts.NewService(queries, time.Now, uuid.New), logger)
	outboundHandler := outboundapi.NewHandler(outboundapi.NewService(queries, enc, dispatcher, time.Now, uuid.New), logger)
	channelHandler := channels.NewHandler(channels.NewService(queries), logger)
	// Forward delivery is best-effort in-process; replay re-fires anything a
	// consumer never acknowledged (operator-triggered, not automatic).
	forwarder := workflow.New()
	replayHandler := replay.NewHandler(replay.NewService(queries, forwarder, logger), logger)
	acctHandler.RegisterRoutes(v1)
	ingestHandler.RegisterRoutes(v1)
	contactHandler.RegisterRoutes(v1)
	outboundHandler.RegisterRoutes(v1)
	channelHandler.RegisterRoutes(v1)
	replayHandler.RegisterRoutes(v1)
	authed := httpapi.WithBearerAuth(cfg.InternalAPIToken, logger, v1)
	srv.Mux().Handle("/v1/accounts", authed)
	srv.Mux().Handle("/v1/accounts/", authed)
	srv.Mux().Handle("/v1/ingest", authed)
	srv.Mux().Handle("/v1/contacts", authed)
	srv.Mux().Handle("/v1/contacts/", authed)
	srv.Mux().Handle("/v1/outbound", authed)
	srv.Mux().Handle("/v1/channels", authed)
	srv.Mux().Handle("/v1/channels/", authed)
	srv.Mux().Handle("/v1/workflows/replay", authed)

	httpServer := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	srv.MarkReady()

	go func() {
		logger.Info("http server listening", "event", "server.start", "addr", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("http server failed", "event", "server.start", "error", err.Error())
			stop()
		}
	}()

	<-rootCtx.Done()
	logger.Info("shutdown signal received", "event", "server.shutdown_start")
	srv.MarkNotReady()

	shutCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := httpServer.Shutdown(shutCtx); err != nil {
		logger.Error("graceful shutdown failed", "event", "server.shutdown_error", "error", err.Error())
	}
	ingestSvc.Wait() // drain in-flight echo dispatches before the pool closes
	logger.Info("shutdown complete", "event", "server.shutdown_done")
}

// newLogger builds a JSON slog logger matching the webhook/iot-webhook house
// style: ts/message keys, a service+env base context, and an event field on
// every line for CloudWatch metric filters.
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
	return slog.New(h).With("service", "comms-channels", "env", env)
}
