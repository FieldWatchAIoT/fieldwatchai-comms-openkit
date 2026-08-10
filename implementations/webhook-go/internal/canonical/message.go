// Package canonical defines the canonical message schema — the contract this
// repo produces and fieldwatchai-comms-channels consumes. Every per-platform
// listener normalizes its payload into a Message.
//
// Contract rules:
//   - The schema is owned by comms-channels. Do not change it here unilaterally.
//   - Every field is always emitted; a field the source platform did not
//     provide serializes as JSON null, never as an empty value and never
//     omitted. Nullable fields are therefore pointers (or nil slices/maps),
//     and no field carries `omitempty`.
//   - RawPayload preserves the original inbound payload verbatim, for debugging
//     parser bugs that strip data.
package canonical

import "encoding/json"

// Message is the canonical normalized inbound message.
type Message struct {
	Sender Sender `json:"sender"`
	Body   Body   `json:"body"`
	Meta   Meta   `json:"meta"`
}

// Sender identifies who sent the message. Endpoint and Platform are always
// present; the remaining identity fields are best-effort per platform and are
// null when unavailable.
type Sender struct {
	// Endpoint is the platform-specific address (phone number, email,
	// Telegram chat ID). Required.
	Endpoint string `json:"endpoint"`
	// Platform is the normalized platform name, e.g. "whatsapp". Required.
	Platform  string  `json:"platform"`
	Handle    *string `json:"handle"`
	FullName  *string `json:"full_name"`
	FirstName *string `json:"first_name"`
	LastName  *string `json:"last_name"`
	Email     *string `json:"email"`
	AvatarURL *string `json:"avatar_url"`
}

// Body is the message content. Each part is null/empty when not present.
type Body struct {
	Text        *string      `json:"text"`
	Attachments []Attachment `json:"attachments"`
	Location    *Location    `json:"location"`
}

// Attachment is a single media item. The URL is a passthrough to the platform's
// hosted media; this repo does not re-host. Mime is best-effort.
type Attachment struct {
	Type string  `json:"type"`
	URL  string  `json:"url"`
	Mime *string `json:"mime"`
}

// Location is a geographic point, set only when the message carries one.
type Location struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

// Meta carries delivery and provenance metadata.
type Meta struct {
	// PlatformMessageID is the platform's unique message ID. Required — used by
	// comms-channels as the idempotency key.
	PlatformMessageID string `json:"platform_message_id"`
	// ReceivedAt is when this service received the message, RFC3339 UTC.
	ReceivedAt string `json:"received_at"`
	// InReplyToID is the platform message ID this is a reply to, if any.
	InReplyToID *string `json:"in_reply_to_id"`
	// AccountID is the FieldWatch-internal account the message arrived on.
	AccountID string `json:"account_id"`
	// RawPayload is the original inbound payload, preserved verbatim. A nil
	// value serializes as JSON null.
	RawPayload json.RawMessage `json:"raw_payload"`
}
