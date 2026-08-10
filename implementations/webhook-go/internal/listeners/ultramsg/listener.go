// Package ultramsg implements the inbound Listener for WhatsApp via the
// UltraMSG gateway. UltraMSG posts a JSON webhook per event; it sends no HMAC
// signature and cannot send custom headers, so authenticity is enforced with a
// shared secret carried in the webhook URL (?token=...), compared in constant
// time. Account resolution (UltraMSG instanceId -> FieldWatch account) happens
// inside Parse; messages we cannot resolve or do not support are dropped
// (acknowledged with no canonical output) rather than rejected.
package ultramsg

import (
	"log/slog"
	"time"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/accounts"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/listeners"
)

// Compile-time guard that the UltraMSG listener satisfies the registry contract.
var _ listeners.Listener = (*Listener)(nil)

const (
	listenerID   = "whatsapp-ultramsg"
	inboundPath  = "/inbound/whatsapp-ultramsg"
	platformName = "whatsapp"
)

// Listener is the UltraMSG WhatsApp inbound listener. Construct with New.
type Listener struct {
	secret   string
	resolver accounts.Resolver
	logger   *slog.Logger
	now      func() time.Time // injectable clock; defaults to time.Now
}

// New builds an UltraMSG listener with the webhook secret (the ?token= value),
// an account resolver, and a logger.
func New(secret string, resolver accounts.Resolver, logger *slog.Logger) *Listener {
	return &Listener{secret: secret, resolver: resolver, logger: logger, now: time.Now}
}

// ID returns the listener identifier.
func (l *Listener) ID() string { return listenerID }

// Path returns the inbound webhook path.
func (l *Listener) Path() string { return inboundPath }
