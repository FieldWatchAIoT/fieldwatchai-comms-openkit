package channels

import (
	"context"
	"errors"
	"fmt"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/queries/goqueries"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Service holds channel-routing logic: tenant scoping and link validation.
type Service struct {
	store store
}

// NewService constructs a Service.
func NewService(s store) *Service { return &Service{store: s} }

// List returns all channels for a tenant.
func (s *Service) List(ctx context.Context, tenantID uuid.UUID) ([]Channel, error) {
	rows, err := s.store.ListChannelsForTenant(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list channels: %w", err)
	}
	out := make([]Channel, 0, len(rows))
	for _, r := range rows {
		out = append(out, toAPI(r))
	}
	return out, nil
}

// Get returns one channel scoped to the tenant.
func (s *Service) Get(ctx context.Context, id, tenantID uuid.UUID) (Channel, error) {
	row, err := s.store.GetChannelForTenant(ctx, goqueries.GetChannelForTenantParams{ID: id, TenantID: tenantID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Channel{}, ErrNotFound
		}
		return Channel{}, fmt.Errorf("get channel: %w", err)
	}
	return toAPI(row), nil
}

// ListLinks returns the account links on a channel, with account detail.
func (s *Service) ListLinks(ctx context.Context, channelID, tenantID uuid.UUID) ([]Link, error) {
	if _, err := s.Get(ctx, channelID, tenantID); err != nil {
		return nil, err
	}
	rows, err := s.store.ListAccountLinksForChannel(ctx, channelID)
	if err != nil {
		return nil, fmt.Errorf("list links: %w", err)
	}
	out := make([]Link, 0, len(rows))
	for _, r := range rows {
		out = append(out, Link{
			ChannelID:          r.ChannelID,
			AccountID:          r.AccountID,
			Direction:          r.Direction,
			Priority:           r.Priority,
			RoutingFilter:      r.RoutingFilter,
			AccountType:        r.Type,
			PlatformIdentifier: r.PlatformIdentifier,
			AccountLabel:       r.Label,
			AccountStatus:      r.Status,
		})
	}
	return out, nil
}

// Link points an account's traffic at a channel. Both the channel and the
// account are verified to belong to the caller's tenant first — a link across
// tenants would silently route one tenant's messages into another's consumer.
// Re-linking an existing pair updates it rather than failing, so a console can
// express desired state without a read-modify-write.
func (s *Service) Link(ctx context.Context, in LinkInput) (Link, error) {
	if !validDirections[in.Direction] {
		return Link{}, fmt.Errorf("%w: direction must be inbound, outbound or both", ErrInvalid)
	}
	if _, err := s.Get(ctx, in.ChannelID, in.TenantID); err != nil {
		return Link{}, err
	}
	if _, err := s.store.GetAccountForTenant(ctx, goqueries.GetAccountForTenantParams{
		ID: in.AccountID, TenantID: in.TenantID,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Link{}, fmt.Errorf("%w: account not in this tenant", ErrNotFound)
		}
		return Link{}, fmt.Errorf("load account: %w", err)
	}

	row, err := s.store.LinkAccountToChannel(ctx, goqueries.LinkAccountToChannelParams{
		ChannelID:     in.ChannelID,
		AccountID:     in.AccountID,
		Direction:     in.Direction,
		Priority:      in.Priority,
		RoutingFilter: in.RoutingFilter,
	})
	if err != nil {
		return Link{}, fmt.Errorf("link account: %w", err)
	}
	return linkToAPI(row), nil
}

// Unlink removes a link. Removing an inbound link makes the account unroutable
// again — messages to it will fall back to service defaults and reach no
// consumer — so callers should treat this as a routing change, not a cleanup.
func (s *Service) Unlink(ctx context.Context, channelID, accountID, tenantID uuid.UUID) error {
	if _, err := s.Get(ctx, channelID, tenantID); err != nil {
		return err
	}
	n, err := s.store.UnlinkAccountFromChannel(ctx, goqueries.UnlinkAccountFromChannelParams{
		ChannelID: channelID, AccountID: accountID,
	})
	if err != nil {
		return fmt.Errorf("unlink account: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
