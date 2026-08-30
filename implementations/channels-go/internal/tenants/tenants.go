// Package tenants provides the bootstrap path into an empty database.
//
// Tenancy is provisioned out of band in FieldWatch's own deployment, which is
// why no tenant endpoint existed. For anyone else that made the first step
// impossible without SQL: accounts.tenant_id is a foreign key onto tenants, so
// against an empty database every POST /v1/accounts fails the constraint and
// returns a bare internal_error with the real cause visible only in the
// container log.
//
// These routes sit behind the same internal bearer token as everything else.
// That token is already an admin credential — it authorizes cross-tenant
// account lookup and message ingest — so letting it create a tenant does not
// widen the trust boundary.
package tenants

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/queries/goqueries"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Sentinel errors mapped to HTTP status by the handler.
var (
	ErrNotFound = errors.New("tenant not found")
	ErrInvalid  = errors.New("invalid")
)

// validPlans guards the column, which is a bare TEXT in the schema.
var validPlans = map[string]bool{"starter": true, "pro": true, "enterprise": true}

// DefaultPlan is what a caller who does not care gets.
const DefaultPlan = "starter"

type store interface {
	CreateTenant(ctx context.Context, arg goqueries.CreateTenantParams) (goqueries.Tenant, error)
	GetTenant(ctx context.Context, id uuid.UUID) (goqueries.Tenant, error)
	ListTenants(ctx context.Context) ([]goqueries.Tenant, error)
}

// Tenant is the API-facing view.
type Tenant struct {
	ID        uuid.UUID       `json:"id"`
	Name      string          `json:"name"`
	Plan      string          `json:"plan"`
	Settings  json.RawMessage `json:"settings"`
	CreatedAt time.Time       `json:"created_at"`
}

// CreateInput is the data needed to create a tenant. ID is optional: supplying
// one makes a setup script idempotent and re-runnable, which matters more here
// than anywhere else because this is the very first call an adopter makes.
type CreateInput struct {
	ID       *uuid.UUID
	Name     string
	Plan     string
	Settings json.RawMessage
}

// Service holds tenant bootstrap logic.
type Service struct{ store store }

// NewService constructs a tenants Service.
func NewService(s store) *Service { return &Service{store: s} }

// Create makes a tenant, or returns the existing one when the caller supplied
// an ID that already exists. Re-running setup must not be an error.
func (s *Service) Create(ctx context.Context, in CreateInput, now func() time.Time, newID func() uuid.UUID) (Tenant, error) {
	if strings.TrimSpace(in.Name) == "" {
		return Tenant{}, fmt.Errorf("%w: name is required", ErrInvalid)
	}
	if in.Plan == "" {
		in.Plan = DefaultPlan
	}
	if !validPlans[in.Plan] {
		return Tenant{}, fmt.Errorf("%w: plan must be starter, pro or enterprise", ErrInvalid)
	}
	if len(in.Settings) == 0 {
		in.Settings = json.RawMessage(`{}`)
	}

	id := newID()
	if in.ID != nil {
		id = *in.ID
	}

	row, err := s.store.CreateTenant(ctx, goqueries.CreateTenantParams{
		ID: id, Name: in.Name, Plan: in.Plan, CreatedAt: now().UTC(), Settings: in.Settings,
	})
	if err != nil {
		// ON CONFLICT DO NOTHING returns no row when the id already exists.
		// Return the existing tenant rather than an error so a setup script is
		// safe to re-run.
		if errors.Is(err, pgx.ErrNoRows) {
			return s.Get(ctx, id)
		}
		return Tenant{}, fmt.Errorf("create tenant: %w", err)
	}
	return toAPI(row), nil
}

// Get returns one tenant.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Tenant, error) {
	row, err := s.store.GetTenant(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Tenant{}, ErrNotFound
		}
		return Tenant{}, fmt.Errorf("get tenant: %w", err)
	}
	return toAPI(row), nil
}

// List returns every tenant. Deliberately not tenant-scoped: it exists so an
// operator who has lost the id they created can find it again.
func (s *Service) List(ctx context.Context) ([]Tenant, error) {
	rows, err := s.store.ListTenants(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tenants: %w", err)
	}
	out := make([]Tenant, 0, len(rows))
	for _, r := range rows {
		out = append(out, toAPI(r))
	}
	return out, nil
}

func toAPI(t goqueries.Tenant) Tenant {
	return Tenant{ID: t.ID, Name: t.Name, Plan: t.Plan, Settings: t.Settings, CreatedAt: t.CreatedAt}
}
