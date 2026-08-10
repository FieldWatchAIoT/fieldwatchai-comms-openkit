// Package emailses implements the outbound adapter for email via Amazon SES —
// the fourth transport (alongside ultramsg, twilio, telegram). Email is
// different from the chat platforms: it authenticates via the task's IAM role
// (not a per-account token), and it needs Subject + RFC threading headers +
// From/Reply-To, so it implements outbound.RichOutbound rather than the plain
// Send(to, body). Inbound is produced by comms-webhook (platform "email").
//
// V1: text-only, sends from the fallback domain (reply-as-them via customer DKIM
// is Phase 2 / the onboarding subsystem). Replies thread off the original
// message's RFC Message-ID.
package emailses

import (
	"context"
	"fmt"
	"strings"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/outbound"
)

// rawSender sends a pre-rendered MIME message. The SES-backed implementation
// lives in sesclient.go; tests inject a fake.
type rawSender interface {
	SendRaw(ctx context.Context, from string, to []string, rawMIME []byte) (messageID string, err error)
}

// Sender is the email outbound adapter.
type Sender struct {
	raw rawSender
}

// New constructs a Sender over a rawSender (see NewSES for the SES-backed one).
func New(raw rawSender) *Sender { return &Sender{raw: raw} }

// Compile-time guard: email is a rich adapter, not a token Send adapter.
var _ outbound.RichOutbound = (*Sender)(nil)

// Send satisfies outbound.Outbound so the adapter can be registered, but email
// always goes through SendRich (the dispatcher prefers RichOutbound). A bare
// Send has no subject/threading, so it's a minimal best-effort.
func (s *Sender) Send(ctx context.Context, acct outbound.Account, to, body string) (string, error) {
	return s.SendRich(ctx, acct, outbound.Message{To: to, Body: body, ReplyTo: acct.Identifier})
}

// SendRich renders the reply as a MIME message and sends it via SES.
func (s *Sender) SendRich(ctx context.Context, acct outbound.Account, msg outbound.Message) (string, error) {
	if msg.To == "" {
		return "", fmt.Errorf("email send: missing recipient")
	}
	from := msg.From
	if from == "" {
		return "", fmt.Errorf("email send: missing From")
	}
	raw := renderMIME(msg)
	return s.raw.SendRaw(ctx, from, []string{msg.To}, raw)
}

// renderMIME builds an RFC 5322 text/plain message with threading headers.
func renderMIME(msg outbound.Message) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", msg.From)
	fmt.Fprintf(&b, "To: %s\r\n", msg.To)
	if msg.ReplyTo != "" {
		fmt.Fprintf(&b, "Reply-To: %s\r\n", msg.ReplyTo)
	}
	fmt.Fprintf(&b, "Subject: %s\r\n", msg.Subject)
	if msg.InReplyTo != "" {
		fmt.Fprintf(&b, "In-Reply-To: %s\r\n", msg.InReplyTo)
	}
	if msg.References != "" {
		fmt.Fprintf(&b, "References: %s\r\n", msg.References)
	}
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(msg.Body)
	return []byte(b.String())
}
