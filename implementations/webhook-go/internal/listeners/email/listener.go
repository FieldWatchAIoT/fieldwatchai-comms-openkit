// Package email implements the inbound listener for email received via AWS SES.
//
// SES delivers an inbound message to an SNS topic; an SNS HTTPS subscription
// POSTs the notification to /inbound/email-ses. We verify a URL token and the
// originating topic ARN, auto-confirm the SNS subscription handshake, then
// normalize the email — From/To/Subject from SES's parsed common headers, the
// text/plain body from the raw MIME — into the canonical message. The account is
// resolved on the To address.
//
// V1 scope: SNS-inline content (the raw MIME rides in the notification, capped
// ~150KB by SNS); no attachment extraction. Hardening follow-ups: full SNS
// message-signature verification (currently a URL token + topic-ARN allowlist),
// and S3-backed content for large mail.
package email

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/accounts"
)

const (
	listenerID   = "email-ses"
	platformName = "email"
)

// Listener receives SES-via-SNS notifications and normalizes them.
type Listener struct {
	secret   string // shared URL token (?token=), constant-time compared
	topicARN string // expected SNS TopicArn; messages from other topics are dropped
	resolver accounts.Resolver
	logger   *slog.Logger
	client   *http.Client // used to confirm the SNS subscription handshake
	now      func() time.Time
}

// New builds the SES email listener. The client (nil → a sane default) is used
// only to GET the SubscribeURL on a SubscriptionConfirmation.
func New(secret, topicARN string, resolver accounts.Resolver, logger *slog.Logger, client *http.Client) *Listener {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &Listener{
		secret:   secret,
		topicARN: topicARN,
		resolver: resolver,
		logger:   logger,
		client:   client,
		now:      time.Now,
	}
}

func (l *Listener) ID() string   { return listenerID }
func (l *Listener) Path() string { return "/inbound/" + listenerID }
