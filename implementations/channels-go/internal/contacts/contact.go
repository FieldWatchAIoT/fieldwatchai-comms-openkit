// Package contacts implements the contacts domain: CRUD, short_id collision
// checking, and a short_id_check helper. Layering mirrors accounts:
// store (sqlc) -> Service -> Handler. Tenant-scoped throughout.
package contacts

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/queries/goqueries"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

var (
	ErrNotFound = errors.New("contact not found")
	ErrConflict = errors.New("short_id already in use")
	ErrInvalid  = errors.New("invalid contact")
)

// store is the slice of generated queries the service needs; *goqueries.Queries
// satisfies it.
type store interface {
	CreateContact(ctx context.Context, arg goqueries.CreateContactParams) (goqueries.Contact, error)
	GetContactForTenant(ctx context.Context, arg goqueries.GetContactForTenantParams) (goqueries.Contact, error)
	GetContactByShortID(ctx context.Context, arg goqueries.GetContactByShortIDParams) (goqueries.Contact, error)
	ListContactsForTenant(ctx context.Context, tenantID uuid.UUID) ([]goqueries.Contact, error)
	ListContactShortIDs(ctx context.Context, tenantID uuid.UUID) ([]goqueries.ListContactShortIDsRow, error)
	UpdateContact(ctx context.Context, arg goqueries.UpdateContactParams) (goqueries.Contact, error)
	DeleteContact(ctx context.Context, arg goqueries.DeleteContactParams) (int64, error)
}

// Contact is the API view of a contact.
type Contact struct {
	ID          uuid.UUID       `json:"id"`
	TenantID    uuid.UUID       `json:"tenant_id"`
	ShortID     string          `json:"short_id"`
	DisplayName string          `json:"display_name"`
	AOIID       *string         `json:"aoi_id"`
	Role        *string         `json:"role"`
	Status      string          `json:"status"`
	Metadata    json.RawMessage `json:"metadata"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// CreateInput is the data for a new contact.
type CreateInput struct {
	TenantID    uuid.UUID
	ShortID     string
	DisplayName string
	AOIID       *string
	Role        *string
	Status      string
	Metadata    json.RawMessage
}

// UpdateInput carries partial updates; nil fields are unchanged.
type UpdateInput struct {
	DisplayName *string
	AOIID       *string
	Role        *string
	Status      *string
	Metadata    json.RawMessage
}

// ShortIDCheckResult reports whether a short_id collides and near-miss
// suggestions.
type ShortIDCheckResult struct {
	Exists      bool     `json:"exists"`
	Suggestions []string `json:"suggestions"`
}

func toAPI(c goqueries.Contact) Contact {
	return Contact{
		ID:          c.ID,
		TenantID:    c.TenantID,
		ShortID:     c.ShortID,
		DisplayName: c.DisplayName,
		AOIID:       textPtr(c.AoiID),
		Role:        textPtr(c.Role),
		Status:      c.Status,
		Metadata:    c.Metadata,
		CreatedAt:   c.CreatedAt,
		UpdatedAt:   c.UpdatedAt,
	}
}

func textPtr(t pgtype.Text) *string {
	if !t.Valid {
		return nil
	}
	s := t.String
	return &s
}

func ptrText(s *string) pgtype.Text {
	if s == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *s, Valid: true}
}
