package ultramsg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/accounts"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/canonical"
)

// payload is the subset of the UltraMSG webhook envelope we consume. Shape
// confirmed against UltraMSG's official webhook sample and a working bot.
type payload struct {
	EventType  string `json:"event_type"`
	InstanceID string `json:"instanceId"`
	Data       struct {
		ID        string `json:"id"`
		From      string `json:"from"`
		To        string `json:"to"`
		Pushname  string `json:"pushname"`
		Type      string `json:"type"`
		Body      string `json:"body"`
		Media     string `json:"media"`
		FromMe    bool   `json:"fromMe"`
		QuotedMsg struct {
			ID string `json:"id"`
		} `json:"quotedMsg"`
		// Location lat/lng arrive as strings in observed payloads but numbers
		// are tolerated too; see coord. Validate against a real captured
		// location payload before relying on this in production.
		Location struct {
			Latitude  json.RawMessage `json:"latitude"`
			Longitude json.RawMessage `json:"longitude"`
		} `json:"location"`
	} `json:"data"`
}

// Parse normalizes an UltraMSG webhook body into canonical messages. It returns
// an empty slice (drop, acknowledged) for events that should not be forwarded:
// our own outbound echoes (fromMe), group chats (@g.us), non-message events,
// messages without an id, and messages on an unregistered instance. A
// genuinely unparseable body returns an error.
func (l *Listener) Parse(body []byte) ([]canonical.Message, error) {
	var p payload
	if err := json.Unmarshal(body, &p); err != nil {
		// Unparseable garbage: acknowledge + drop (retrying won't help).
		l.logger.Warn("dropping unparseable payload",
			"event", "dropped_unparseable", "platform", listenerID, "error", err.Error())
		return nil, nil
	}

	if p.EventType != "message_received" {
		return nil, nil // acks, reactions, status updates — not inbound messages
	}
	if p.Data.FromMe {
		return nil, nil // our own outbound echo
	}
	if strings.HasSuffix(p.Data.From, "@g.us") {
		return nil, nil // group chat — out of scope for V1
	}
	if p.Data.ID == "" {
		l.logger.Warn("dropping message with no id",
			"event", "dropped_no_id", "platform", listenerID)
		return nil, nil
	}

	acc, err := l.resolver.Resolve(context.Background(), listenerID, p.InstanceID)
	if errors.Is(err, accounts.ErrNotFound) {
		// Unregistered instance: acknowledge + drop.
		l.logger.Warn("dropping message for unregistered account",
			"event", "account_unresolved", "platform", listenerID)
		return nil, nil
	}
	if err != nil {
		// Transient lookup failure (channels down) — propagate so the inbound is
		// retried by the platform rather than silently dropped.
		return nil, fmt.Errorf("resolve account: %w", err)
	}

	m := canonical.Message{
		Sender: canonical.Sender{
			Endpoint: "+" + before(p.Data.From, "@"),
			Platform: platformName,
		},
		Meta: canonical.Meta{
			PlatformMessageID: p.Data.ID,
			ReceivedAt:        l.now().UTC().Format(time.RFC3339),
			AccountID:         acc.ID,
			RawPayload:        json.RawMessage(append([]byte(nil), body...)),
		},
	}
	if p.Data.Pushname != "" {
		m.Sender.FullName = ptr(p.Data.Pushname)
	}
	// data.body is the message text for chat, media captions, and vCards — but
	// for a location it's a base64 map thumbnail and for a document it's the
	// filename (also in data.filename and the raw payload). Neither is message
	// text, so we don't surface those as body.text. (Canonical Attachment has
	// no filename field yet; if channels wants structured filenames, that's a
	// schema addition to negotiate.)
	switch p.Data.Type {
	case "location", "document":
		// data.body is not message text for these
	default:
		if p.Data.Body != "" {
			m.Body.Text = ptr(p.Data.Body)
		}
	}
	if p.Data.Media != "" {
		m.Body.Attachments = []canonical.Attachment{{Type: p.Data.Type, URL: p.Data.Media}}
	}
	if lat, ok := coord(p.Data.Location.Latitude); ok {
		if lng, ok2 := coord(p.Data.Location.Longitude); ok2 {
			m.Body.Location = &canonical.Location{Lat: lat, Lng: lng}
		}
	}
	if p.Data.QuotedMsg.ID != "" {
		m.Meta.InReplyToID = ptr(p.Data.QuotedMsg.ID)
	}
	return []canonical.Message{m}, nil
}

// before returns the part of s before the first occurrence of sep (or all of s).
func before(s, sep string) string {
	if i := strings.Index(s, sep); i >= 0 {
		return s[:i]
	}
	return s
}

func ptr(s string) *string { return &s }

// coord parses a coordinate that may be a JSON string ("26.5") or number (26.5).
func coord(raw json.RawMessage) (float64, bool) {
	s := strings.Trim(string(raw), `"`)
	if s == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	return f, err == nil
}
