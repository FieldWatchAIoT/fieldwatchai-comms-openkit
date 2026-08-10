// Package telegram implements the inbound listener for the Telegram Bot API
// (webhook mode). Telegram POSTs JSON Update objects; we verify the
// X-Telegram-Bot-Api-Secret-Token header, then normalize an Update's message
// into the canonical message. Updates carry no bot id, so the webhook URL
// carries ?bot=<id> (read via the RequestParser capability) to identify which
// account the message is for. No outbound here — sending + media getFile are
// comms-channels' job (they hold the per-bot token).
package telegram

import (
	"log/slog"
	"time"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/accounts"
)

const (
	listenerID   = "telegram"
	platformName = "telegram"
)

// Listener verifies + parses Telegram webhook updates into canonical messages.
type Listener struct {
	secret   string // shared webhook secret_token (verifies the inbound header)
	resolver accounts.Resolver
	logger   *slog.Logger
	now      func() time.Time
}

// New builds the Telegram listener. secret is the shared webhook secret set on
// setWebhook (Telegram returns it in the X-Telegram-Bot-Api-Secret-Token header).
func New(secret string, resolver accounts.Resolver, logger *slog.Logger) *Listener {
	return &Listener{secret: secret, resolver: resolver, logger: logger, now: time.Now}
}

func (l *Listener) ID() string   { return listenerID }
func (l *Listener) Path() string { return "/inbound/telegram" }
