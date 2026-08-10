// Package outbound defines the transport-agnostic Outbound interface and an
// adapter registry keyed by account type. New platforms we can send over are
// added as additive adapters (e.g. internal/integrations/ultramsg) — the one
// recurring piece of core work the open/closed design predicts. The registry
// mirrors the comms-webhook's listener registry.
package outbound

import (
	"context"
	"fmt"
)

// TypeEmailSES is the account type for email via Amazon SES.
const TypeEmailSES = "email-ses"

// Tokenless reports whether a platform authenticates without a per-account
// credential. Email via SES uses the task's IAM role, so its account has no
// credentials_encrypted; every other platform carries a decrypted token.
func Tokenless(accountType string) bool { return accountType == TypeEmailSES }

// Platform normalises an account type to the transport a person would name.
// Account types encode the provider as well as the transport ("whatsapp" is
// UltraMSG, "whatsapp-twilio" is Twilio) but to whoever sent the message both
// are WhatsApp, and that is what a consumer's UI wants to show.
//
// The provider still matters — it selects the adapter — so consumers get the
// raw account type alongside this. Unknown types pass through unchanged rather
// than falling back to a guess, so a newly added adapter is never silently
// mislabelled as something it isn't.
func Platform(accountType string) string {
	switch accountType {
	case "whatsapp", "whatsapp-twilio":
		return "whatsapp"
	case "sms-twilio":
		return "sms"
	case TypeEmailSES:
		return "email"
	default:
		return accountType
	}
}

// Account is the minimal, already-decrypted credential set an adapter needs to
// send. The dispatcher decrypts credentials_encrypted before calling Send, so
// adapters stay free of DB/KMS concerns.
type Account struct {
	Type       string // e.g. "whatsapp" — selects the adapter
	Identifier string // platform identifier (UltraMSG instance id, etc.)
	Token      string // decrypted platform secret (UltraMSG instance token)
}

// Outbound sends a text message to an endpoint and returns the platform's
// message id.
type Outbound interface {
	Send(ctx context.Context, acct Account, to, body string) (platformMessageID string, err error)
}

// Message is the richer outbound payload. Chat platforms use only To + Body; the
// email adapter also uses Subject/From/ReplyTo and the RFC threading headers.
type Message struct {
	To         string
	Body       string
	Subject    string // email
	From       string // email — sender address (reply-as-them or fallback)
	ReplyTo    string // email — where replies should go
	InReplyTo  string // email — original RFC Message-ID
	References string // email — RFC References chain
}

// RichOutbound is implemented by adapters that need the full Message (email).
// The dispatcher prefers it when present; token-based chat adapters don't
// implement it and keep using Send(to, body).
type RichOutbound interface {
	SendRich(ctx context.Context, acct Account, msg Message) (platformMessageID string, err error)
}

// Dispatch routes a Message to the account's adapter: RichOutbound.SendRich when
// the adapter supports it (email), else the plain Send(to, body) (chat). This is
// the single send path both the echo and /v1/outbound flows use.
func (r *Registry) Dispatch(ctx context.Context, acct Account, msg Message) (string, error) {
	o, err := r.Get(acct.Type)
	if err != nil {
		return "", err
	}
	if rich, ok := o.(RichOutbound); ok {
		return rich.SendRich(ctx, acct, msg)
	}
	return o.Send(ctx, acct, msg.To, msg.Body)
}

// Registry resolves an Outbound adapter by account type.
type Registry struct {
	byType map[string]Outbound
}

// NewRegistry builds an empty registry.
func NewRegistry() *Registry { return &Registry{byType: map[string]Outbound{}} }

// Register associates an adapter with an account type.
func (r *Registry) Register(accountType string, o Outbound) {
	r.byType[accountType] = o
}

// Get returns the adapter for an account type, or an error if none is
// registered (a platform we can receive from but not yet send over).
func (r *Registry) Get(accountType string) (Outbound, error) {
	o, ok := r.byType[accountType]
	if !ok {
		return nil, fmt.Errorf("no outbound adapter registered for account type %q", accountType)
	}
	return o, nil
}
