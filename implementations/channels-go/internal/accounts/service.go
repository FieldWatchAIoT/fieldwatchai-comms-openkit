package accounts

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/crypto"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/queries/goqueries"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Service holds account business logic: validation, credential encryption, and
// tenant scoping. now and newID are injected for deterministic tests.
type Service struct {
	store store
	enc   crypto.Encryptor
	now   func() time.Time
	newID func() uuid.UUID
}

// NewService constructs a Service. Pass time.Now and uuid.New in production.
func NewService(s store, enc crypto.Encryptor, now func() time.Time, newID func() uuid.UUID) *Service {
	return &Service{store: s, enc: enc, now: now, newID: newID}
}

// Create validates, encrypts credentials, and persists a new account.
func (s *Service) Create(ctx context.Context, in CreateInput) (Account, error) {
	if in.TenantID == uuid.Nil || in.Type == "" || in.OwnerType == "" ||
		in.PlatformIdentifier == "" || in.Status == "" {
		return Account{}, fmt.Errorf("%w: tenant_id, type, owner_type, platform_identifier, status are required", ErrInvalid)
	}

	var encrypted []byte
	if len(in.Credentials) > 0 {
		ct, err := s.enc.Encrypt(ctx, in.Credentials)
		if err != nil {
			return Account{}, fmt.Errorf("encrypt credentials: %w", err)
		}
		encrypted = ct
	}

	row, err := s.store.CreateAccount(ctx, goqueries.CreateAccountParams{
		ID:                   s.newID(),
		TenantID:             in.TenantID,
		Type:                 in.Type,
		OwnerType:            in.OwnerType,
		Label:                in.Label,
		PlatformIdentifier:   in.PlatformIdentifier,
		CredentialsEncrypted: encrypted,
		Capabilities:         in.Capabilities,
		Status:               in.Status,
		CreatedAt:            s.now().UTC(),
	})
	if err != nil {
		return Account{}, fmt.Errorf("create account: %w", err)
	}
	return toAPI(row), nil
}

// Get returns one account scoped to the tenant.
func (s *Service) Get(ctx context.Context, id, tenantID uuid.UUID) (Account, error) {
	row, err := s.store.GetAccountForTenant(ctx, goqueries.GetAccountForTenantParams{ID: id, TenantID: tenantID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Account{}, ErrNotFound
		}
		return Account{}, fmt.Errorf("get account: %w", err)
	}
	return toAPI(row), nil
}

// List returns all accounts for a tenant.
func (s *Service) List(ctx context.Context, tenantID uuid.UUID) ([]Account, error) {
	rows, err := s.store.ListAccountsForTenant(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	out := make([]Account, 0, len(rows))
	for _, r := range rows {
		out = append(out, toAPI(r))
	}
	return out, nil
}

// ListWithRouting returns all accounts for a tenant, each annotated with the
// channel its inbound traffic resolves to (nil when it resolves to none). The
// channel is computed with the same rule ingest uses, so the answer here is the
// answer at runtime rather than an approximation of it.
func (s *Service) ListWithRouting(ctx context.Context, tenantID uuid.UUID) ([]AccountWithRouting, error) {
	rows, err := s.store.ListAccountsWithRoutingForTenant(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list accounts with routing: %w", err)
	}
	out := make([]AccountWithRouting, 0, len(rows))
	for _, r := range rows {
		item := AccountWithRouting{Account: Account{
			ID:                 r.ID,
			TenantID:           r.TenantID,
			Type:               r.Type,
			OwnerType:          r.OwnerType,
			Label:              r.Label,
			PlatformIdentifier: r.PlatformIdentifier,
			Capabilities:       r.Capabilities,
			Status:             r.Status,
			CreatedAt:          r.CreatedAt,
		}}
		// The query emits '' rather than NULL; see its comment for why.
		if r.InboundChannelID != "" {
			if id, perr := uuid.Parse(r.InboundChannelID); perr == nil {
				item.InboundChannelID = &id
			}
		}
		out = append(out, item)
	}
	return out, nil
}

// Update applies a partial update within the tenant, re-encrypting credentials
// when provided and preserving existing ciphertext otherwise.
func (s *Service) Update(ctx context.Context, id, tenantID uuid.UUID, in UpdateInput) (Account, error) {
	cur, err := s.store.GetAccountForTenant(ctx, goqueries.GetAccountForTenantParams{ID: id, TenantID: tenantID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Account{}, ErrNotFound
		}
		return Account{}, fmt.Errorf("load account: %w", err)
	}

	params := goqueries.UpdateAccountParams{
		ID:                   id,
		TenantID:             tenantID,
		Label:                cur.Label,
		Status:               cur.Status,
		Capabilities:         cur.Capabilities,
		CredentialsEncrypted: cur.CredentialsEncrypted,
	}
	if in.Label != nil {
		params.Label = *in.Label
	}
	if in.Status != nil {
		params.Status = *in.Status
	}
	if in.Capabilities != nil {
		params.Capabilities = *in.Capabilities
	}
	if in.Credentials != nil {
		ct, err := s.enc.Encrypt(ctx, in.Credentials)
		if err != nil {
			return Account{}, fmt.Errorf("encrypt credentials: %w", err)
		}
		params.CredentialsEncrypted = ct
	}

	row, err := s.store.UpdateAccount(ctx, params)
	if err != nil {
		return Account{}, fmt.Errorf("update account: %w", err)
	}
	return toAPI(row), nil
}

// listenerToAccountType maps a webhook listener id (what FCW sends as `type`,
// e.g. "whatsapp-ultramsg") to the account platform type stored in the DB
// (e.g. "whatsapp"). Unmapped values pass through unchanged, so callers may
// also send the account type directly.
var listenerToAccountType = map[string]string{
	"whatsapp-ultramsg": "whatsapp",
}

// Lookup resolves an account by type + identifier, returning only the id pair
// (used by the webhook). `accType` may be a webhook listener id or an account
// platform type. Returns ErrNotFound when no account matches.
func (s *Service) Lookup(ctx context.Context, accType, identifier string) (LookupResult, error) {
	if mapped, ok := listenerToAccountType[accType]; ok {
		accType = mapped
	}
	row, err := s.store.LookupAccount(ctx, goqueries.LookupAccountParams{
		Type:               accType,
		PlatformIdentifier: identifier,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return LookupResult{}, ErrNotFound
		}
		return LookupResult{}, fmt.Errorf("lookup account: %w", err)
	}
	return LookupResult{AccountID: row.ID, TenantID: row.TenantID}, nil
}

// Delete removes an account within the tenant, returning ErrNotFound if no row
// matched.
func (s *Service) Delete(ctx context.Context, id, tenantID uuid.UUID) error {
	n, err := s.store.DeleteAccount(ctx, goqueries.DeleteAccountParams{ID: id, TenantID: tenantID})
	if err != nil {
		return fmt.Errorf("delete account: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
