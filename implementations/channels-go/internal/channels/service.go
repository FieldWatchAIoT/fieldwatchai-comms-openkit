package channels

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
	"github.com/jackc/pgx/v5/pgtype"
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

// Create makes a channel. Only Name is required; every other field falls back
// to the same defaults ingest already applies to an unlinked account, so a
// caller who supplies nothing else gets a channel that behaves exactly as the
// system did before they created it — plus somewhere to hang a workflow_url.
func (s *Service) Create(ctx context.Context, in CreateInput, now func() time.Time, newID func() uuid.UUID) (Channel, error) {
	if strings.TrimSpace(in.Name) == "" {
		return Channel{}, fmt.Errorf("%w: name is required", ErrInvalid)
	}
	if in.ReplyPolicy == "" {
		in.ReplyPolicy = DefaultReplyPolicy
	}
	if !validReplyPolicies[in.ReplyPolicy] {
		return Channel{}, fmt.Errorf("%w: reply_policy must be reply_to_sender, broadcast or custom", ErrInvalid)
	}
	if len(in.ParserConfig) == 0 {
		cfg, err := json.Marshal(map[string]any{"mode": "structured", "commands": DefaultCommands})
		if err != nil {
			return Channel{}, fmt.Errorf("build default parser config: %w", err)
		}
		in.ParserConfig = cfg
	}
	if err := validateParserConfig(in.ParserConfig); err != nil {
		return Channel{}, err
	}
	if len(in.ConfidenceThresholds) == 0 {
		in.ConfidenceThresholds = DefaultConfidenceThresholds
	}
	if err := validateThresholds(in.ConfidenceThresholds); err != nil {
		return Channel{}, err
	}

	echo := true
	if in.EchoBackEnabled != nil {
		echo = *in.EchoBackEnabled
	}
	recall := DefaultRecallWindowSeconds
	if in.RecallWindowSeconds != nil {
		recall = *in.RecallWindowSeconds
	}

	row, err := s.store.CreateChannel(ctx, goqueries.CreateChannelParams{
		ID:                   newID(),
		TenantID:             in.TenantID,
		Name:                 in.Name,
		ParserConfig:         in.ParserConfig,
		WorkflowUrl:          pgtype.Text{String: in.WorkflowURL, Valid: in.WorkflowURL != ""},
		ReplyPolicy:          in.ReplyPolicy,
		ConfidenceThresholds: in.ConfidenceThresholds,
		EchoBackEnabled:      echo,
		RecallWindowSeconds:  recall,
		CreatedAt:            now().UTC(),
	})
	if err != nil {
		return Channel{}, fmt.Errorf("create channel: %w", err)
	}
	return toAPI(row), nil
}

// Update applies a partial change, scoped to the tenant. Absent fields are left
// alone so setting a workflow_url does not require restating the parser config.
func (s *Service) Update(ctx context.Context, id, tenantID uuid.UUID, in UpdateInput) (Channel, error) {
	if in.Name != nil && strings.TrimSpace(*in.Name) == "" {
		return Channel{}, fmt.Errorf("%w: name cannot be empty", ErrInvalid)
	}
	if in.ReplyPolicy != nil && !validReplyPolicies[*in.ReplyPolicy] {
		return Channel{}, fmt.Errorf("%w: reply_policy must be reply_to_sender, broadcast or custom", ErrInvalid)
	}
	if len(in.ParserConfig) > 0 {
		if err := validateParserConfig(in.ParserConfig); err != nil {
			return Channel{}, err
		}
	}
	if len(in.ConfidenceThresholds) > 0 {
		if err := validateThresholds(in.ConfidenceThresholds); err != nil {
			return Channel{}, err
		}
	}

	row, err := s.store.UpdateChannel(ctx, goqueries.UpdateChannelParams{
		ID:                   id,
		TenantID:             tenantID,
		Name:                 textPtr(in.Name),
		ParserConfig:         in.ParserConfig,
		WorkflowUrl:          textPtr(in.WorkflowURL),
		ReplyPolicy:          textPtr(in.ReplyPolicy),
		ConfidenceThresholds: in.ConfidenceThresholds,
		EchoBackEnabled:      boolPtr(in.EchoBackEnabled),
		RecallWindowSeconds:  int4Ptr(in.RecallWindowSeconds),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Channel{}, ErrNotFound
		}
		return Channel{}, fmt.Errorf("update channel: %w", err)
	}
	return toAPI(row), nil
}

// validateParserConfig rejects a config ingest would silently misread. An
// unknown mode is the dangerous one: ingest treats anything that is not
// "passthrough" as the command grammar, so a typo like "passthru" would quietly
// run the wrong pipeline over live traffic.
func validateParserConfig(raw json.RawMessage) error {
	var cfg struct {
		Mode     string   `json:"mode"`
		Commands []string `json:"commands"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return fmt.Errorf("%w: parser_config must be a JSON object", ErrInvalid)
	}
	if cfg.Mode != "" && !validModes[cfg.Mode] {
		return fmt.Errorf("%w: parser_config.mode must be structured or passthrough", ErrInvalid)
	}
	return nil
}

// validateThresholds rejects thresholds that would make the policy gate
// nonsensical — medium above high inverts the echo-back band.
func validateThresholds(raw json.RawMessage) error {
	var t struct {
		High   *float64 `json:"high"`
		Medium *float64 `json:"medium"`
	}
	if err := json.Unmarshal(raw, &t); err != nil {
		return fmt.Errorf("%w: confidence_thresholds must be a JSON object", ErrInvalid)
	}
	if t.High == nil || t.Medium == nil {
		return fmt.Errorf("%w: confidence_thresholds needs both high and medium", ErrInvalid)
	}
	// Checked in a fixed order so the error a caller sees is deterministic.
	for _, f := range []struct {
		name string
		val  float64
	}{{"high", *t.High}, {"medium", *t.Medium}} {
		if f.val < 0 || f.val > 1 {
			return fmt.Errorf("%w: confidence_thresholds.%s must be between 0 and 1", ErrInvalid, f.name)
		}
	}
	if *t.Medium > *t.High {
		return fmt.Errorf("%w: confidence_thresholds.medium cannot exceed high", ErrInvalid)
	}
	return nil
}

func textPtr(s *string) pgtype.Text {
	if s == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *s, Valid: true}
}

func boolPtr(b *bool) pgtype.Bool {
	if b == nil {
		return pgtype.Bool{}
	}
	return pgtype.Bool{Bool: *b, Valid: true}
}

func int4Ptr(i *int32) pgtype.Int4 {
	if i == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: *i, Valid: true}
}
