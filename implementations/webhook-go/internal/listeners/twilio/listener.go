// Package twilio implements the inbound listener for Twilio's messaging webhook.
// One listener type serves both WhatsApp and SMS — Twilio posts the same
// form-encoded shape for each, and the "whatsapp:" scheme on From/To
// distinguishes them. It is registered once per channel (ids "whatsapp-twilio"
// and "sms-twilio", each served at /inbound/<id>), which is also the account
// lookup type. Verifies the X-Twilio-Signature, then normalizes into the
// canonical message. No outbound here — that's comms-channels.
package twilio

import (
	"log/slog"
	"time"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/accounts"
)

// Listener verifies + parses Twilio inbound webhooks into canonical messages.
type Listener struct {
	id            string // also the account lookup type, e.g. "whatsapp-twilio" / "sms-twilio"
	authToken     string
	publicBaseURL string // public origin Twilio signs against, e.g. https://webhook.example.com
	resolver      accounts.Resolver
	logger        *slog.Logger
	now           func() time.Time
}

// New builds a Twilio listener for the given channel id (e.g. "whatsapp-twilio"
// or "sms-twilio") — served at /inbound/<id> and used as the account lookup
// type. authToken is the Twilio Auth Token (signature verification);
// publicBaseURL is the externally-visible origin (so the signed URL matches
// behind the ALB).
func New(id, authToken, publicBaseURL string, resolver accounts.Resolver, logger *slog.Logger) *Listener {
	return &Listener{
		id:            id,
		authToken:     authToken,
		publicBaseURL: publicBaseURL,
		resolver:      resolver,
		logger:        logger,
		now:           time.Now,
	}
}

func (l *Listener) ID() string   { return l.id }
func (l *Listener) Path() string { return "/inbound/" + l.id }
