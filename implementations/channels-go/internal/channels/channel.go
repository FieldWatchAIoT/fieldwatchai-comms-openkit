// Package channels exposes the routing surface: reading channels and writing the
// channel_accounts links that decide where an account's inbound traffic goes.
//
// Channel rows themselves are still created out of band (their parser_config,
// thresholds and mode are deployment config, not product data). What a product
// or console genuinely needs is the link — without it an account exists but
// reaches no consumer, and it does so silently. Layering matches the accounts
// package: store (sqlc goqueries) -> Service -> Handler.
package channels

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/queries/goqueries"
	"github.com/google/uuid"
)

// Sentinel errors mapped to HTTP status by the handler.
var (
	ErrNotFound = errors.New("channel not found")
	ErrInvalid  = errors.New("invalid link")
)

// Valid link directions. 'inbound' and 'both' are what ingest resolves on;
// 'outbound' only affects which account /v1/outbound sends from.
var validDirections = map[string]bool{"inbound": true, "outbound": true, "both": true}

// store is the narrow slice of the generated query API the service needs.
type store interface {
	ListChannelsForTenant(ctx context.Context, tenantID uuid.UUID) ([]goqueries.Channel, error)
	GetChannelForTenant(ctx context.Context, arg goqueries.GetChannelForTenantParams) (goqueries.Channel, error)
	GetAccountForTenant(ctx context.Context, arg goqueries.GetAccountForTenantParams) (goqueries.Account, error)
	LinkAccountToChannel(ctx context.Context, arg goqueries.LinkAccountToChannelParams) (goqueries.ChannelAccount, error)
	UnlinkAccountFromChannel(ctx context.Context, arg goqueries.UnlinkAccountFromChannelParams) (int64, error)
	ListAccountLinksForChannel(ctx context.Context, channelID uuid.UUID) ([]goqueries.ListAccountLinksForChannelRow, error)
}

// Channel is the API-facing view of a channel.
type Channel struct {
	ID                   uuid.UUID       `json:"id"`
	TenantID             uuid.UUID       `json:"tenant_id"`
	Name                 string          `json:"name"`
	ParserConfig         json.RawMessage `json:"parser_config"`
	Mode                 string          `json:"mode"`
	WorkflowURL          string          `json:"workflow_url,omitempty"`
	ReplyPolicy          string          `json:"reply_policy"`
	ConfidenceThresholds json.RawMessage `json:"confidence_thresholds"`
	EchoBackEnabled      bool            `json:"echo_back_enabled"`
	RecallWindowSeconds  int32           `json:"recall_window_seconds"`
	CreatedAt            time.Time       `json:"created_at"`
}

// Link is the API-facing view of a channel_accounts row.
type Link struct {
	ChannelID     uuid.UUID       `json:"channel_id"`
	AccountID     uuid.UUID       `json:"account_id"`
	Direction     string          `json:"direction"`
	Priority      int32           `json:"priority"`
	RoutingFilter json.RawMessage `json:"routing_filter,omitempty"`

	// Account detail, populated when listing a channel's links.
	AccountType        string `json:"account_type,omitempty"`
	PlatformIdentifier string `json:"platform_identifier,omitempty"`
	AccountLabel       string `json:"account_label,omitempty"`
	AccountStatus      string `json:"account_status,omitempty"`
}

// LinkInput is the data needed to link an account to a channel.
type LinkInput struct {
	ChannelID     uuid.UUID
	AccountID     uuid.UUID
	TenantID      uuid.UUID
	Direction     string
	Priority      int32
	RoutingFilter json.RawMessage
}

// toAPI maps a stored channel row to the API view, lifting parser_config.mode
// to a top-level field so a console doesn't have to parse the blob to learn
// whether the channel is passthrough.
func toAPI(c goqueries.Channel) Channel {
	out := Channel{
		ID:                   c.ID,
		TenantID:             c.TenantID,
		Name:                 c.Name,
		ParserConfig:         c.ParserConfig,
		ReplyPolicy:          c.ReplyPolicy,
		ConfidenceThresholds: c.ConfidenceThresholds,
		EchoBackEnabled:      c.EchoBackEnabled,
		RecallWindowSeconds:  c.RecallWindowSeconds,
		CreatedAt:            c.CreatedAt,
	}
	if c.WorkflowUrl.Valid {
		out.WorkflowURL = c.WorkflowUrl.String
	}
	var pc struct {
		Mode string `json:"mode"`
	}
	if len(c.ParserConfig) > 0 && json.Unmarshal(c.ParserConfig, &pc) == nil {
		out.Mode = pc.Mode
	}
	if out.Mode == "" {
		out.Mode = "structured"
	}
	return out
}

func linkToAPI(l goqueries.ChannelAccount) Link {
	return Link{
		ChannelID:     l.ChannelID,
		AccountID:     l.AccountID,
		Direction:     l.Direction,
		Priority:      l.Priority,
		RoutingFilter: l.RoutingFilter,
	}
}
