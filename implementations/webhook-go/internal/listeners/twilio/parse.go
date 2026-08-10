package twilio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/accounts"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/canonical"
)

// Parse normalizes a Twilio inbound messaging webhook (form-encoded) into a
// canonical message. It returns an empty slice (drop, acknowledged) for things
// that are not forwardable inbound messages — unparseable bodies, status
// callbacks (no From), messages with no MessageSid, and messages on an
// unregistered account. A transient account-lookup failure returns an error so
// the inbound is retried rather than dropped.
func (l *Listener) Parse(body []byte) ([]canonical.Message, error) {
	vals, err := url.ParseQuery(string(body))
	if err != nil {
		l.logger.Warn("dropping unparseable payload",
			"event", "dropped_unparseable", "platform", l.id, "error", err.Error())
		return nil, nil
	}

	from := vals.Get("From")
	if from == "" {
		return nil, nil // not an inbound message (e.g. a delivery status callback)
	}

	msgID := vals.Get("MessageSid")
	if msgID == "" {
		msgID = vals.Get("SmsSid")
	}
	if msgID == "" {
		l.logger.Warn("dropping message with no id",
			"event", "dropped_no_id", "platform", l.id)
		return nil, nil
	}

	// The To number (our FieldWatch sender) is the account-resolution key.
	to := stripScheme(vals.Get("To"))
	acc, err := l.resolver.Resolve(context.Background(), l.id, to)
	if errors.Is(err, accounts.ErrNotFound) {
		l.logger.Warn("dropping message for unregistered account",
			"event", "account_unresolved", "platform", l.id)
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("resolve account: %w", err)
	}

	// Twilio uses the same payload for SMS and WhatsApp; the "whatsapp:" scheme
	// on From/To is the only difference.
	platform := "sms"
	if strings.HasPrefix(from, "whatsapp:") {
		platform = "whatsapp"
	}

	m := canonical.Message{
		Sender: canonical.Sender{
			Endpoint: stripScheme(from),
			Platform: platform,
		},
		Meta: canonical.Meta{
			PlatformMessageID: msgID,
			ReceivedAt:        l.now().UTC().Format(time.RFC3339),
			AccountID:         acc.ID,
			RawPayload:        rawJSON(vals),
		},
	}
	if pn := vals.Get("ProfileName"); pn != "" {
		m.Sender.FullName = ptr(pn)
	}
	if wa := vals.Get("WaId"); wa != "" {
		m.Sender.Handle = ptr(wa)
	}
	if b := vals.Get("Body"); b != "" {
		m.Body.Text = ptr(b)
	}

	// Twilio's MessageType (text/image/video/audio/sticker/document/contacts/
	// location) is the authoritative type signal — cleaner than MIME-sniffing,
	// and it's the only thing that distinguishes e.g. a vCard (contacts) from a
	// plain text/* document. Fall back to the MIME major when it's absent (SMS).
	mt := vals.Get("MessageType")
	n, _ := strconv.Atoi(vals.Get("NumMedia"))
	for i := 0; i < n; i++ {
		u := vals.Get("MediaUrl" + strconv.Itoa(i))
		if u == "" {
			continue
		}
		ct := vals.Get("MediaContentType" + strconv.Itoa(i))
		att := canonical.Attachment{Type: attachmentType(mt, ct), URL: u}
		if ct != "" {
			att.Mime = ptr(ct)
		}
		m.Body.Attachments = append(m.Body.Attachments, att)
	}

	if lat := vals.Get("Latitude"); lat != "" {
		if lng := vals.Get("Longitude"); lng != "" {
			latF, e1 := strconv.ParseFloat(lat, 64)
			lngF, e2 := strconv.ParseFloat(lng, 64)
			if e1 == nil && e2 == nil {
				m.Body.Location = &canonical.Location{Lat: latF, Lng: lngF}
			}
		}
	}

	// Replies: Twilio surfaces the quoted message as OriginalRepliedMessageSid.
	if r := vals.Get("OriginalRepliedMessageSid"); r != "" {
		m.Meta.InReplyToID = ptr(r)
	}

	return []canonical.Message{m}, nil
}

// attachmentType maps to the coarse attachment vocabulary, preferring Twilio's
// MessageType (authoritative) and falling back to the MIME major part.
func attachmentType(messageType, mime string) string {
	switch messageType {
	case "image", "video", "audio", "sticker", "document":
		return messageType
	case "contacts":
		return "contact"
	}
	return typeFromMime(mime)
}

func ptr(s string) *string { return &s }

// stripScheme removes Twilio's "whatsapp:" prefix, leaving the bare endpoint.
func stripScheme(s string) string { return strings.TrimPrefix(s, "whatsapp:") }

// typeFromMime maps a MIME type to the coarse attachment vocabulary the other
// listeners use (image/video/audio/document).
func typeFromMime(ct string) string {
	major, _, _ := strings.Cut(ct, "/")
	switch major {
	case "image", "video", "audio":
		return major
	case "application", "text":
		return "document"
	case "":
		return "file"
	default:
		return major
	}
}

// rawJSON renders the form values as a JSON object for meta.raw_payload (Twilio
// posts form-encoded, not JSON; this preserves every field faithfully).
func rawJSON(vals url.Values) json.RawMessage {
	b, err := json.Marshal(vals)
	if err != nil {
		return nil
	}
	return b
}
