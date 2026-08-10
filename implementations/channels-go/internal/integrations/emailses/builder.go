package emailses

import (
	"encoding/json"
	"strings"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/outbound"
)

// FallbackDomain is the SES sending domain used for the From address until a
// customer's own domain is DKIM-verified (Phase 2 reply-as-them). Package var so
// it can be overridden from config at wiring time.
var FallbackDomain = "mail.fieldwatchai.io"

// BuildReply constructs an email reply Message from the inbound account's ingest
// address, the recipient (original customer), the reply body, and the inbound
// message's SES raw_payload (for subject + threading).
//
//   - From:       <ingest-local-part>@<FallbackDomain>   (fallback sender; Phase 2 = reply-as-them)
//   - Reply-To:   the ingest address                     (loops the customer's reply back to us)
//   - Subject:    "Re: <original subject>"
//   - In-Reply-To/References: the original RFC Message-ID
func BuildReply(ingestAddress, to, body string, rawPayload []byte) outbound.Message {
	subject, messageID, references := parseThreading(rawPayload)

	re := strings.TrimSpace(subject)
	if re == "" {
		re = "Re:"
	} else if !strings.HasPrefix(strings.ToLower(re), "re:") {
		re = "Re: " + re
	}

	local := ingestAddress
	if i := strings.IndexByte(ingestAddress, '@'); i > 0 {
		local = ingestAddress[:i]
	}

	refs := strings.TrimSpace(strings.Join([]string{references, messageID}, " "))

	return outbound.Message{
		To:         to,
		Body:       body,
		Subject:    re,
		From:       local + "@" + FallbackDomain,
		ReplyTo:    ingestAddress,
		InReplyTo:  messageID,
		References: refs,
	}
}

// DefaultSubject is used when a product initiates an email conversation without
// supplying one. Package var so it can be overridden at wiring time.
var DefaultSubject = "New message"

// BuildNew constructs an email that opens a new thread — the product-initiated
// counterpart to BuildReply. There is no prior message, so there is nothing to
// thread off and nothing to quote a subject from: the caller supplies one, or
// DefaultSubject is used. Deliberately does not set In-Reply-To/References;
// forging them onto a thread that doesn't exist breaks threading in most
// clients rather than creating it.
func BuildNew(ingestAddress, to, subject, body string) outbound.Message {
	if strings.TrimSpace(subject) == "" {
		subject = DefaultSubject
	}

	local := ingestAddress
	if i := strings.IndexByte(ingestAddress, '@'); i > 0 {
		local = ingestAddress[:i]
	}

	return outbound.Message{
		To:      to,
		Body:    body,
		Subject: subject,
		From:    local + "@" + FallbackDomain,
		ReplyTo: ingestAddress,
	}
}

type commonHeaders struct {
	MessageID  string   `json:"messageId"`
	Subject    string   `json:"subject"`
	References []string `json:"references"`
}

// parseThreading pulls subject + the RFC Message-ID (+ any References) from the
// SES notification's commonHeaders, tolerating both {mail:{commonHeaders}} and a
// top-level {commonHeaders} shape. Best-effort: missing fields → empty strings.
func parseThreading(raw []byte) (subject, messageID, references string) {
	var p struct {
		Mail struct {
			CommonHeaders commonHeaders `json:"commonHeaders"`
		} `json:"mail"`
		CommonHeaders commonHeaders `json:"commonHeaders"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &p) != nil {
		return "", "", ""
	}
	ch := p.Mail.CommonHeaders
	if ch.MessageID == "" && ch.Subject == "" {
		ch = p.CommonHeaders
	}
	return ch.Subject, ch.MessageID, strings.Join(ch.References, " ")
}
