package contacts

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/queries/goqueries"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/strdist"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Service holds contact business logic: validation, collision checks, tenant
// scoping. now and newID are injected for tests.
type Service struct {
	store store
	now   func() time.Time
	newID func() uuid.UUID
}

// NewService constructs a contacts Service.
func NewService(s store, now func() time.Time, newID func() uuid.UUID) *Service {
	return &Service{store: s, now: now, newID: newID}
}

// Create validates, checks for a short_id collision within the tenant, and
// inserts.
func (s *Service) Create(ctx context.Context, in CreateInput) (Contact, error) {
	if in.TenantID == uuid.Nil || in.ShortID == "" || in.DisplayName == "" {
		return Contact{}, fmt.Errorf("%w: tenant_id, short_id, display_name are required", ErrInvalid)
	}

	// Collision check (UNIQUE(tenant_id, short_id) also backstops this).
	_, err := s.store.GetContactByShortID(ctx, goqueries.GetContactByShortIDParams{TenantID: in.TenantID, ShortID: in.ShortID})
	switch {
	case err == nil:
		return Contact{}, fmt.Errorf("%w: %q", ErrConflict, in.ShortID)
	case !errors.Is(err, pgx.ErrNoRows):
		return Contact{}, fmt.Errorf("collision check: %w", err)
	}

	status := in.Status
	if status == "" {
		status = "active"
	}
	meta := in.Metadata
	if len(meta) == 0 {
		meta = []byte("{}")
	}

	now := s.now().UTC()
	row, err := s.store.CreateContact(ctx, goqueries.CreateContactParams{
		ID:          s.newID(),
		TenantID:    in.TenantID,
		ShortID:     in.ShortID,
		DisplayName: in.DisplayName,
		AoiID:       ptrText(in.AOIID),
		Role:        ptrText(in.Role),
		Status:      status,
		Metadata:    meta,
		CreatedAt:   now,
	})
	if err != nil {
		return Contact{}, fmt.Errorf("create contact: %w", err)
	}
	return toAPI(row), nil
}

// Get returns one contact scoped to the tenant.
func (s *Service) Get(ctx context.Context, id, tenantID uuid.UUID) (Contact, error) {
	row, err := s.store.GetContactForTenant(ctx, goqueries.GetContactForTenantParams{ID: id, TenantID: tenantID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Contact{}, ErrNotFound
		}
		return Contact{}, fmt.Errorf("get contact: %w", err)
	}
	return toAPI(row), nil
}

// List returns all contacts for a tenant.
func (s *Service) List(ctx context.Context, tenantID uuid.UUID) ([]Contact, error) {
	rows, err := s.store.ListContactsForTenant(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list contacts: %w", err)
	}
	out := make([]Contact, 0, len(rows))
	for _, r := range rows {
		out = append(out, toAPI(r))
	}
	return out, nil
}

// Update applies a partial update within the tenant.
func (s *Service) Update(ctx context.Context, id, tenantID uuid.UUID, in UpdateInput) (Contact, error) {
	cur, err := s.store.GetContactForTenant(ctx, goqueries.GetContactForTenantParams{ID: id, TenantID: tenantID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Contact{}, ErrNotFound
		}
		return Contact{}, fmt.Errorf("load contact: %w", err)
	}

	params := goqueries.UpdateContactParams{
		ID:          id,
		TenantID:    tenantID,
		DisplayName: cur.DisplayName,
		AoiID:       cur.AoiID,
		Role:        cur.Role,
		Status:      cur.Status,
		Metadata:    cur.Metadata,
		UpdatedAt:   s.now().UTC(),
	}
	if in.DisplayName != nil {
		params.DisplayName = *in.DisplayName
	}
	if in.AOIID != nil {
		params.AoiID = ptrText(in.AOIID)
	}
	if in.Role != nil {
		params.Role = ptrText(in.Role)
	}
	if in.Status != nil {
		params.Status = *in.Status
	}
	if len(in.Metadata) > 0 {
		params.Metadata = in.Metadata
	}

	row, err := s.store.UpdateContact(ctx, params)
	if err != nil {
		return Contact{}, fmt.Errorf("update contact: %w", err)
	}
	return toAPI(row), nil
}

// Delete removes a contact within the tenant.
func (s *Service) Delete(ctx context.Context, id, tenantID uuid.UUID) error {
	n, err := s.store.DeleteContact(ctx, goqueries.DeleteContactParams{ID: id, TenantID: tenantID})
	if err != nil {
		return fmt.Errorf("delete contact: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ShortIDCheck reports whether a short_id already exists in the tenant and
// returns near-miss suggestions (existing short_ids within edit distance 2).
func (s *Service) ShortIDCheck(ctx context.Context, tenantID uuid.UUID, shortID string) (ShortIDCheckResult, error) {
	rows, err := s.store.ListContactShortIDs(ctx, tenantID)
	if err != nil {
		return ShortIDCheckResult{}, fmt.Errorf("list short_ids: %w", err)
	}
	res := ShortIDCheckResult{}
	for _, r := range rows {
		if r.ShortID == shortID {
			res.Exists = true
			continue
		}
		if d := strdist.Levenshtein(shortID, r.ShortID); d > 0 && d <= 2 {
			res.Suggestions = append(res.Suggestions, r.ShortID)
		}
	}
	return res, nil
}

