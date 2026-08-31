// Package export streams a tenant's data out in a non-proprietary format.
//
// This exists so an adopter can leave. A tool that holds a disaster response's
// message history in a shape only its own code can read is not something a
// public agency should adopt, whatever its licence says — and the Digital
// Public Goods Standard asks for exactly this (indicator 6, "mechanism for
// extracting data").
//
// The format is JSON Lines: one complete JSON object per line, newline
// delimited. It is streamable, appendable, readable by every data tool in
// common use, and — unlike a single JSON array — does not require the reader
// or the writer to hold the whole dataset in memory.
package export

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/queries/goqueries"
	"github.com/google/uuid"
)

// pageSize is the rows fetched per round trip. Large enough that a big export
// is not chatty, small enough that one page is a trivial allocation.
const pageSize = 500

type store interface {
	ExportMessagesPage(ctx context.Context, arg goqueries.ExportMessagesPageParams) ([]goqueries.ExportMessagesPageRow, error)
	ExportContactsPage(ctx context.Context, arg goqueries.ExportContactsPageParams) ([]goqueries.Contact, error)
	ExportEndpointsForContacts(ctx context.Context, contactIds []uuid.UUID) ([]goqueries.ExportEndpointsForContactsRow, error)
}

// Service streams exports.
type Service struct{ store store }

// NewService constructs an export Service.
func NewService(s store) *Service { return &Service{store: s} }

// Message is one exported message. Field names match the database columns so an
// export round-trips against the schema without a translation table.
type Message struct {
	ID                  uuid.UUID       `json:"id"`
	TenantID            uuid.UUID       `json:"tenant_id"`
	AccountID           *uuid.UUID      `json:"account_id"`
	ChannelID           *uuid.UUID      `json:"channel_id"`
	Direction           string          `json:"direction"`
	SenderContactID     *uuid.UUID      `json:"sender_contact_id"`
	RecipientContactID  *uuid.UUID      `json:"recipient_contact_id"`
	SenderEndpoint      string          `json:"sender_endpoint,omitempty"`
	BodyText            string          `json:"body_text"`
	BodyAttachments     json.RawMessage `json:"body_attachments,omitempty"`
	BodyLocationGeoJSON json.RawMessage `json:"body_location,omitempty"`
	Parsed              json.RawMessage `json:"parsed,omitempty"`
	PolicyAction        string          `json:"policy_action,omitempty"`
	WorkflowFired       bool            `json:"workflow_fired"`
	InReplyToMessageID  *uuid.UUID      `json:"in_reply_to_message_id"`
	PlatformMessageID   string          `json:"platform_message_id"`
	RawPayload          json.RawMessage `json:"raw_payload,omitempty"`
	ReceivedAt          time.Time       `json:"received_at"`
	ProcessedAt         *time.Time      `json:"processed_at"`
}

// Endpoint is one address a contact can be reached on.
type Endpoint struct {
	ID           uuid.UUID  `json:"id"`
	ChannelID    uuid.UUID  `json:"channel_id"`
	AccountID    uuid.UUID  `json:"account_id"`
	Endpoint     string     `json:"endpoint"`
	Priority     int32      `json:"priority"`
	Capabilities []string   `json:"capabilities"`
	LastSeenAt   *time.Time `json:"last_seen_at"`
}

// Contact is one exported address-book entry, with its endpoints inlined so the
// export is usable without a join the reader has to reconstruct.
type Contact struct {
	ID               uuid.UUID       `json:"id"`
	TenantID         uuid.UUID       `json:"tenant_id"`
	ShortID          string          `json:"short_id"`
	DisplayName      string          `json:"display_name"`
	AOIID            string          `json:"aoi_id,omitempty"`
	Role             string          `json:"role,omitempty"`
	DefaultChannelID *uuid.UUID      `json:"default_channel_id"`
	Status           string          `json:"status"`
	Metadata         json.RawMessage `json:"metadata,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
	Endpoints        []Endpoint      `json:"endpoints"`
}

// Options control what an export contains.
type Options struct {
	// IncludeRaw emits meta.raw_payload — the verbatim provider envelope.
	// Off by default: it is the bulk of the export and the most sensitive part,
	// but it is also the only way to reconstruct a message exactly as the
	// platform sent it, so a migration needs the option to include it.
	IncludeRaw bool
}

// Messages streams every message for a tenant as JSON Lines, oldest first.
//
// Writes as it reads rather than buffering: an export must not be bounded by
// how much memory the process has, and an operator watching a large export
// should see it progress. Ordering is stable and the cursor is a keyset, so
// rows arriving mid-export are never skipped or duplicated.
func (s *Service) Messages(ctx context.Context, w io.Writer, tenantID uuid.UUID, opt Options) (int, error) {
	enc := json.NewEncoder(w)
	cursorTime, cursorID := time.Time{}, uuid.Nil
	total := 0

	for {
		rows, err := s.store.ExportMessagesPage(ctx, goqueries.ExportMessagesPageParams{
			TenantID: tenantID, Limit: pageSize,
			AfterReceivedAt: cursorTime, AfterID: cursorID,
		})
		if err != nil {
			return total, fmt.Errorf("export messages page: %w", err)
		}
		if len(rows) == 0 {
			return total, nil
		}
		for _, r := range rows {
			m := Message{
				ID: r.ID, TenantID: r.TenantID, AccountID: r.AccountID, ChannelID: r.ChannelID,
				Direction: r.Direction, SenderContactID: r.SenderContactID,
				RecipientContactID: r.RecipientContactID, SenderEndpoint: r.SenderEndpoint.String,
				BodyText: r.BodyText, BodyAttachments: json.RawMessage(r.BodyAttachments),
				Parsed: json.RawMessage(r.Parsed), PolicyAction: r.PolicyAction.String,
				WorkflowFired: r.WorkflowFired, InReplyToMessageID: r.InReplyToMessageID,
				PlatformMessageID: r.PlatformMessageID, ReceivedAt: r.ReceivedAt,
				ProcessedAt: r.ProcessedAt,
			}
			if r.BodyLocationGeojson != "" {
				m.BodyLocationGeoJSON = json.RawMessage(r.BodyLocationGeojson)
			}
			if opt.IncludeRaw {
				m.RawPayload = json.RawMessage(r.RawPayload)
			}
			if err := enc.Encode(m); err != nil {
				return total, fmt.Errorf("write message %s: %w", r.ID, err)
			}
			total++
			cursorTime, cursorID = r.ReceivedAt, r.ID
		}
		if len(rows) < pageSize {
			return total, nil
		}
	}
}

// Contacts streams the address book as JSON Lines, each contact carrying its
// endpoints.
func (s *Service) Contacts(ctx context.Context, w io.Writer, tenantID uuid.UUID) (int, error) {
	enc := json.NewEncoder(w)
	cursorTime, cursorID := time.Time{}, uuid.Nil
	total := 0

	for {
		rows, err := s.store.ExportContactsPage(ctx, goqueries.ExportContactsPageParams{
			TenantID: tenantID, Limit: pageSize,
			AfterCreatedAt: cursorTime, AfterID: cursorID,
		})
		if err != nil {
			return total, fmt.Errorf("export contacts page: %w", err)
		}
		if len(rows) == 0 {
			return total, nil
		}

		ids := make([]uuid.UUID, 0, len(rows))
		for _, r := range rows {
			ids = append(ids, r.ID)
		}
		eps, err := s.store.ExportEndpointsForContacts(ctx, ids)
		if err != nil {
			return total, fmt.Errorf("export endpoints: %w", err)
		}
		byContact := map[uuid.UUID][]Endpoint{}
		for _, e := range eps {
			byContact[e.ContactID] = append(byContact[e.ContactID], Endpoint{
				ID: e.ID, ChannelID: e.ChannelID, AccountID: e.AccountID,
				Endpoint: e.Endpoint, Priority: e.Priority,
				Capabilities: e.Capabilities, LastSeenAt: e.LastSeenAt,
			})
		}

		for _, r := range rows {
			c := Contact{
				ID: r.ID, TenantID: r.TenantID, ShortID: r.ShortID, DisplayName: r.DisplayName,
				DefaultChannelID: r.DefaultChannelID, Status: r.Status,
				Metadata: json.RawMessage(r.Metadata), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
				Endpoints: byContact[r.ID],
			}
			if c.Endpoints == nil {
				c.Endpoints = []Endpoint{}
			}
			c.AOIID, c.Role = r.AoiID.String, r.Role.String
			if err := enc.Encode(c); err != nil {
				return total, fmt.Errorf("write contact %s: %w", r.ID, err)
			}
			total++
			cursorTime, cursorID = r.CreatedAt, r.ID
		}
		if len(rows) < pageSize {
			return total, nil
		}
	}
}
