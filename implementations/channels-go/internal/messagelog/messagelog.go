// Package messagelog is the read side of the message table: recent traffic for
// a tenant, in the shape a console needs to answer "did my message arrive, and
// what did the system make of it?".
//
// Read-only on purpose. Messages are written by ingest and the outbound API;
// nothing here mutates. The raw provider envelope (messages.raw_payload) is
// never returned — it is the largest and most sensitive column, and confirming
// traffic flows does not require it.
package messagelog

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/queries/goqueries"
	"github.com/google/uuid"
)

const (
	// DefaultLimit is a screenful.
	DefaultLimit = 50
	// MaxLimit caps an unbounded ?limit=, which would otherwise let one request
	// pull the whole message history into memory.
	MaxLimit = 500
)

type store interface {
	ListRecentMessagesForTenant(ctx context.Context, arg goqueries.ListRecentMessagesForTenantParams) ([]goqueries.ListRecentMessagesForTenantRow, error)
}

// Parsed is the subset of the parsed doc a console renders. The full document
// carries resolver internals that are noise outside the pipeline.
type Parsed struct {
	Command     string  `json:"command,omitempty"`
	ShortID     string  `json:"short_id,omitempty"`
	Target      string  `json:"target,omitempty"`
	Payload     string  `json:"payload,omitempty"`
	Confidence  float64 `json:"confidence"`
	MatchKind   string  `json:"short_id_match,omitempty"`
	Passthrough bool    `json:"passthrough,omitempty"`
}

// Message is the API-facing view of one message.
type Message struct {
	ID             uuid.UUID `json:"id"`
	Direction      string    `json:"direction"`
	SenderEndpoint string    `json:"sender_endpoint,omitempty"`
	BodyText       string    `json:"body_text"`
	PolicyAction   string    `json:"policy_action,omitempty"`
	ChannelName    string    `json:"channel_name,omitempty"`
	WorkflowFired  bool      `json:"workflow_fired"`
	ReceivedAt     time.Time `json:"received_at"`
	Parsed         *Parsed   `json:"parsed,omitempty"`
}

// Service reads recent messages.
type Service struct{ store store }

// NewService constructs a messagelog Service.
func NewService(s store) *Service { return &Service{store: s} }

// List returns the most recent messages for a tenant, newest first. A limit of
// zero or less uses DefaultLimit; anything above MaxLimit is clamped rather
// than rejected, so a console asking for too much still renders.
func (s *Service) List(ctx context.Context, tenantID uuid.UUID, limit int) ([]Message, error) {
	switch {
	case limit <= 0:
		limit = DefaultLimit
	case limit > MaxLimit:
		limit = MaxLimit
	}

	rows, err := s.store.ListRecentMessagesForTenant(ctx, goqueries.ListRecentMessagesForTenantParams{
		TenantID: tenantID, Limit: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}

	out := make([]Message, 0, len(rows))
	for _, r := range rows {
		m := Message{
			ID:             r.ID,
			Direction:      r.Direction,
			SenderEndpoint: r.SenderEndpoint.String,
			BodyText:       r.BodyText,
			PolicyAction:   r.PolicyAction.String,
			ChannelName:    r.ChannelName.String,
			WorkflowFired:  r.WorkflowFired,
			ReceivedAt:     r.ReceivedAt,
		}
		// A malformed parsed doc must not blank the whole row — the message
		// text and its policy action are still the useful part.
		if len(r.Parsed) > 0 {
			var p Parsed
			if err := json.Unmarshal(r.Parsed, &p); err == nil {
				m.Parsed = &p
			}
		}
		out = append(out, m)
	}
	return out, nil
}
