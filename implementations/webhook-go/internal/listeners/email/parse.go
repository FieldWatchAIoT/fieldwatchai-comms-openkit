package email

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"time"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/accounts"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/canonical"
)

// snsEnvelope is the subset of the SNS HTTP message we consume. SNS posts three
// message Types: Notification (the email), SubscriptionConfirmation (handshake),
// and UnsubscribeConfirmation. Message holds the inner SES JSON for Notifications.
type snsEnvelope struct {
	Type         string `json:"Type"`
	MessageID    string `json:"MessageId"`
	TopicArn     string `json:"TopicArn"`
	Message      string `json:"Message"`
	SubscribeURL string `json:"SubscribeURL"`
}

// sesNotification is the SES "Received" action payload SNS wraps. SES pre-parses
// the headers into commonHeaders, so From/Subject come from there; the raw MIME
// (for the body) rides in Content, base64-encoded, when the receipt rule includes
// it (SNS-inline, <=150KB).
//
// The account is resolved on Receipt.Recipients — the envelope recipient(s) that
// matched the SES receipt rule, i.e. the address the mail was actually delivered
// to. For forwarded mail (customer → support@acme.com → forward to
// acme@ingest.example.com) the To: header still shows the business's public
// address, so it must NOT be used for routing; the receipt recipient is our
// hidden ingest address and the true account key.
type sesNotification struct {
	NotificationType string `json:"notificationType"`
	Mail             struct {
		MessageID     string   `json:"messageId"`
		Source        string   `json:"source"`
		Destination   []string `json:"destination"`
		CommonHeaders struct {
			From    []string `json:"from"`
			To      []string `json:"to"`
			Subject string   `json:"subject"`
		} `json:"commonHeaders"`
	} `json:"mail"`
	Receipt struct {
		Recipients []string `json:"recipients"`
	} `json:"receipt"`
	Content string `json:"content"`
}

// Parse decodes the SNS envelope and dispatches by message Type: it auto-confirms
// a SubscriptionConfirmation (and drops), normalizes a Received Notification into
// the canonical message, and drops everything else with 200.
func (l *Listener) Parse(body []byte) ([]canonical.Message, error) {
	var env snsEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		l.logger.Warn("dropping unparseable SNS payload",
			"event", "dropped_unparseable", "platform", listenerID, "error", err.Error())
		return nil, nil
	}

	// Topic-ARN allowlist: only accept messages from our configured SES topic.
	if l.topicARN != "" && env.TopicArn != l.topicARN {
		l.logger.Warn("dropping message from unexpected topic",
			"event", "dropped_topic", "platform", listenerID)
		return nil, nil
	}

	switch env.Type {
	case "SubscriptionConfirmation":
		l.confirmSubscription(env.SubscribeURL)
		return nil, nil
	case "Notification":
		// handled below
	default:
		return nil, nil // UnsubscribeConfirmation, etc.
	}

	var ses sesNotification
	if err := json.Unmarshal([]byte(env.Message), &ses); err != nil {
		l.logger.Warn("dropping unparseable SES notification",
			"event", "dropped_unparseable", "platform", listenerID, "error", err.Error())
		return nil, nil
	}
	if ses.NotificationType != "Received" {
		return nil, nil // delivery/bounce/complaint notifications are not inbound mail
	}

	// Route on the address the mail was delivered to (the SES receipt recipient),
	// falling back to the envelope destination, then the To: header only if those
	// are absent. See the sesNotification doc for why the To: header is unsafe.
	deliveredTo := firstAddr(ses.Receipt.Recipients, ses.Mail.Destination, ses.Mail.CommonHeaders.To)
	acc, err := l.resolver.Resolve(context.Background(), listenerID, normalizeEmail(deliveredTo))
	if errors.Is(err, accounts.ErrNotFound) {
		l.logger.Warn("dropping message for unregistered account",
			"event", "account_unresolved", "platform", listenerID)
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("resolve account: %w", err)
	}

	// The sender is the original customer; a straight forward preserves the From:
	// header (only the envelope/Return-Path changes). raw_payload keeps everything.
	from := firstAddr(ses.Mail.CommonHeaders.From, []string{ses.Mail.Source})
	addr, name := splitAddress(from)

	m := canonical.Message{
		Sender: canonical.Sender{
			Endpoint: addr,
			Platform: platformName,
			Email:    ptr(addr),
		},
		Meta: canonical.Meta{
			PlatformMessageID: ses.Mail.MessageID,
			ReceivedAt:        l.now().UTC().Format(time.RFC3339),
			AccountID:         acc.ID,
			RawPayload:        json.RawMessage(append([]byte(nil), []byte(env.Message)...)),
		},
	}
	if name != "" {
		m.Sender.FullName = ptr(name)
	}

	// Subject + the text/plain body. There's no canonical subject field, so the
	// subject is prepended to the body text.
	var bodyText string
	if ses.Content != "" {
		if raw, decErr := base64.StdEncoding.DecodeString(ses.Content); decErr == nil {
			bodyText = extractTextBody(raw)
		}
	}
	if combined := combineSubjectBody(ses.Mail.CommonHeaders.Subject, bodyText); combined != "" {
		m.Body.Text = ptr(combined)
	}

	return []canonical.Message{m}, nil
}

// confirmSubscription completes the SNS handshake by GET-ing the SubscribeURL.
// Best-effort: a failure is logged, not fatal (SNS retries the confirmation).
func (l *Listener) confirmSubscription(url string) {
	if url == "" {
		return
	}
	// Log the SubscribeURL up front: if this GET can't reach SNS (e.g. the task
	// has egress restricted to the VPC + S3), an operator can confirm the
	// subscription manually from this logged URL — the documented fallback.
	l.logger.Info("confirming SNS subscription",
		"event", "sns_confirming", "platform", listenerID, "subscribe_url", url)
	resp, err := l.client.Get(url)
	if err != nil {
		l.logger.Error("SNS subscription confirmation failed (confirm manually from subscribe_url)",
			"event", "sns_confirm_error", "platform", listenerID, "subscribe_url", url, "error", err.Error())
		return
	}
	_ = resp.Body.Close()
	l.logger.Info("SNS subscription confirmed",
		"event", "sns_confirmed", "platform", listenerID, "status", resp.StatusCode)
}

// combineSubjectBody renders the email as a single text block, prepending the
// subject when present.
func combineSubjectBody(subject, body string) string {
	subject = strings.TrimSpace(subject)
	body = strings.TrimSpace(body)
	switch {
	case subject != "" && body != "":
		return "Subject: " + subject + "\n\n" + body
	case subject != "":
		return "Subject: " + subject
	default:
		return body
	}
}

// extractTextBody parses raw MIME and returns the first text/plain part's decoded
// content, walking multipart containers. Returns "" if none is found.
func extractTextBody(raw []byte) string {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return ""
	}
	body, err := io.ReadAll(msg.Body)
	if err != nil {
		return ""
	}
	return findTextPlain(msg.Header.Get("Content-Type"), msg.Header.Get("Content-Transfer-Encoding"), body)
}

func findTextPlain(contentType, cte string, body []byte) string {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType == "" {
		mediaType = "text/plain" // no/invalid Content-Type → treat as plain text
	}

	if strings.HasPrefix(mediaType, "multipart/") {
		mr := multipart.NewReader(bytes.NewReader(body), params["boundary"])
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			partBody, _ := io.ReadAll(part)
			pmt, _, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
			if strings.HasPrefix(pmt, "multipart/") {
				if t := findTextPlain(part.Header.Get("Content-Type"), part.Header.Get("Content-Transfer-Encoding"), partBody); t != "" {
					return t
				}
				continue
			}
			if pmt == "text/plain" {
				return decodeBody(part.Header.Get("Content-Transfer-Encoding"), partBody)
			}
			// skip text/html and attachments (V1)
		}
		return ""
	}

	if mediaType == "text/plain" {
		return decodeBody(cte, body)
	}
	return ""
}

// decodeBody reverses the Content-Transfer-Encoding for a part body.
func decodeBody(cte string, body []byte) string {
	switch strings.ToLower(strings.TrimSpace(cte)) {
	case "base64":
		if dec, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(body))); err == nil {
			return string(dec)
		}
	case "quoted-printable":
		if dec, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(body))); err == nil {
			return string(dec)
		}
	}
	return string(body)
}

// normalizeEmail extracts the bare address from a possibly-display-named header
// value and lowercases it — the stable identifier for account resolution.
func normalizeEmail(v string) string {
	addr, _ := splitAddress(v)
	return addr
}

// splitAddress returns (bare-lowercased-address, display-name) from a header
// value like `John Doe <john@example.com>`. Falls back to the trimmed input.
func splitAddress(v string) (addr, name string) {
	v = strings.TrimSpace(v)
	if a, err := mail.ParseAddress(v); err == nil {
		return strings.ToLower(a.Address), a.Name
	}
	return strings.ToLower(v), ""
}

// firstAddr returns the first non-blank string across the given lists, in order.
func firstAddr(lists ...[]string) string {
	for _, list := range lists {
		for _, s := range list {
			if strings.TrimSpace(s) != "" {
				return s
			}
		}
	}
	return ""
}

func ptr(s string) *string { return &s }
