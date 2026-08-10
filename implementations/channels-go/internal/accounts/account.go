// Package accounts implements the accounts domain: CRUD and resolution, with
// per-account platform credentials encrypted at rest. Layering is
// store (sqlc goqueries) -> Service (business logic) -> Handler (HTTP).
package accounts

import (
	"context"
	"errors"
	"time"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/queries/goqueries"
	"github.com/google/uuid"
)

// Sentinel errors mapped to HTTP status by the handler.
var (
	ErrNotFound = errors.New("account not found")
	ErrInvalid  = errors.New("invalid account")
)

// store is the narrow slice of the generated query API the service needs.
// *goqueries.Queries satisfies it.
type store interface {
	CreateAccount(ctx context.Context, arg goqueries.CreateAccountParams) (goqueries.Account, error)
	GetAccountForTenant(ctx context.Context, arg goqueries.GetAccountForTenantParams) (goqueries.Account, error)
	ListAccountsForTenant(ctx context.Context, tenantID uuid.UUID) ([]goqueries.Account, error)
	ListAccountsWithRoutingForTenant(ctx context.Context, tenantID uuid.UUID) ([]goqueries.ListAccountsWithRoutingForTenantRow, error)
	UpdateAccount(ctx context.Context, arg goqueries.UpdateAccountParams) (goqueries.Account, error)
	DeleteAccount(ctx context.Context, arg goqueries.DeleteAccountParams) (int64, error)
	LookupAccount(ctx context.Context, arg goqueries.LookupAccountParams) (goqueries.LookupAccountRow, error)
}

// LookupResult is the id pair returned by account resolution. Never includes
// credentials — the webhook is inbound-only and must not receive secrets.
type LookupResult struct {
	AccountID uuid.UUID `json:"account_id"`
	TenantID  uuid.UUID `json:"tenant_id"`
}

// Account is the API-facing view of an account. It deliberately has NO
// credentials field — credentials are write-only and never leave the service.
type Account struct {
	ID                 uuid.UUID `json:"id"`
	TenantID           uuid.UUID `json:"tenant_id"`
	Type               string    `json:"type"`
	OwnerType          string    `json:"owner_type"`
	Label              string    `json:"label"`
	PlatformIdentifier string    `json:"platform_identifier"`
	Capabilities       []string  `json:"capabilities"`
	Status             string    `json:"status"`
	CreatedAt          time.Time `json:"created_at"`
}

// AccountWithRouting is the list view: an account plus where its inbound
// traffic actually goes. InboundChannelID is nil when the account is linked to
// no channel — messages to it fall back to the service defaults, land on
// clarify, and reach no consumer. That state is legitimate (an account can
// exist before its channel does) but it must never be invisible, which is the
// only reason this type is separate from Account.
type AccountWithRouting struct {
	Account
	InboundChannelID *uuid.UUID `json:"inbound_channel_id"`
}

// CreateInput is the data needed to create an account. Credentials is plaintext;
// the service encrypts it before storage.
type CreateInput struct {
	TenantID           uuid.UUID
	Type               string
	OwnerType          string
	Label              string
	PlatformIdentifier string
	Status             string
	Capabilities       []string
	Credentials        []byte
}

// UpdateInput carries partial updates. Nil fields are left unchanged. Credentials
// nil means "keep existing"; non-nil replaces (re-encrypted).
type UpdateInput struct {
	Label        *string
	Status       *string
	Capabilities *[]string
	Credentials  []byte
}

// toAPI maps a stored row to the API view, dropping credentials.
func toAPI(a goqueries.Account) Account {
	return Account{
		ID:                 a.ID,
		TenantID:           a.TenantID,
		Type:               a.Type,
		OwnerType:          a.OwnerType,
		Label:              a.Label,
		PlatformIdentifier: a.PlatformIdentifier,
		Capabilities:       a.Capabilities,
		Status:             a.Status,
		CreatedAt:          a.CreatedAt,
	}
}
