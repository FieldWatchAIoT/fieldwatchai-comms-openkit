// Package channels exposes the routing surface: reading channels and writing the
// channel_accounts links that decide where an account's inbound traffic goes.
//
// Channels are created here too. They were once written by hand in SQL on the
// grounds that parser_config and thresholds are deployment config rather than
// product data — but that made the first-run experience a trap: without a
// channel there is no workflow_url, so an account accepts messages, stores
// them, and forwards them nowhere, silently. Sensible defaults plus a create
// endpoint mean a working deployment needs no SQL at all. Layering matches the
// accounts package: store (sqlc goqueries) -> Service -> Handler.
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
	ErrInvalid  = errors.New("invalid")
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
	CreateChannel(ctx context.Context, arg goqueries.CreateChannelParams) (goqueries.Channel, error)
	UpdateChannel(ctx context.Context, arg goqueries.UpdateChannelParams) (goqueries.Channel, error)
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

// CreateInput is the data needed to create a channel. Everything except Name
// has a working default — the point is that `{"name":"Field Ops"}` is enough.
type CreateInput struct {
	TenantID             uuid.UUID
	Name                 string
	ParserConfig         json.RawMessage
	WorkflowURL          string
	ReplyPolicy          string
	ConfidenceThresholds json.RawMessage
	EchoBackEnabled      *bool
	RecallWindowSeconds  *int32
}

// UpdateInput is a partial update; nil fields are left unchanged. Setting
// WorkflowURL to a pointer-to-empty-string is how a caller clears it.
type UpdateInput struct {
	Name                 *string
	ParserConfig         json.RawMessage
	WorkflowURL          *string
	ReplyPolicy          *string
	ConfidenceThresholds json.RawMessage
	EchoBackEnabled      *bool
	RecallWindowSeconds  *int32
}

// Defaults applied on create. These mirror the service-level fallbacks in
// internal/ingest so that a channel created with only a name behaves exactly
// like the unlinked-account path an operator has already seen working, rather
// than changing behaviour the moment they create their first channel.
var (
	DefaultCommands             = []string{"STATUS", "NEEDS", "DAMAGE", "MISSING", "RESOURCE", "HERE", "NOTE", "SOS"}
	DefaultReplyPolicy          = "reply_to_sender"
	DefaultConfidenceThresholds = json.RawMessage(`{"high":0.9,"medium":0.5}`)
	DefaultRecallWindowSeconds  = int32(120)
)

// validReplyPolicies guards the column, which is a bare TEXT in the schema.
var validReplyPolicies = map[string]bool{"reply_to_sender": true, "broadcast": true, "custom": true}

// validModes are the parser modes ingest understands. "structured" runs the
// command grammar; "passthrough" skips it and routes raw text to the consumer.
var validModes = map[string]bool{"structured": true, "passthrough": true}

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
