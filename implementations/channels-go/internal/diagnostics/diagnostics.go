// Package diagnostics answers one question for an operator: "I set everything
// up, I sent a message, and nothing happened — why?"
//
// That question is hard to answer from the outside because the most common
// misconfiguration is silent by construction. An account with no inbound
// channel link still accepts messages, parses them, and stores them; it just
// forwards them nowhere. The webhook returns 200, a row lands in the database,
// and the only evidence is one warning line in a JSON container log. An
// operator with no Postgres access and no log aggregation has nothing to go on.
//
// So this package reports the shape of a tenant's configuration and names the
// specific gaps that cause silence, in the order they bite.
package diagnostics

import (
	"context"
	"fmt"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/queries/goqueries"
	"github.com/google/uuid"
)

// store is the narrow slice of the generated query API this package needs.
type store interface {
	CountAccountsForTenant(ctx context.Context, tenantID uuid.UUID) (int64, error)
	CountChannelsForTenant(ctx context.Context, tenantID uuid.UUID) (int64, error)
	CountContactsForTenant(ctx context.Context, tenantID uuid.UUID) (int64, error)
	CountMessagesForTenant(ctx context.Context, tenantID uuid.UUID) (int64, error)
	ListUnroutableAccounts(ctx context.Context, tenantID uuid.UUID) ([]goqueries.ListUnroutableAccountsRow, error)
	ListChannelsWithoutWorkflow(ctx context.Context, tenantID uuid.UUID) ([]goqueries.ListChannelsWithoutWorkflowRow, error)
}

// Severity orders findings by how completely they break the pipeline.
type Severity string

const (
	// SeverityBlocking means inbound traffic is being discarded or stranded.
	SeverityBlocking Severity = "blocking"
	// SeverityWarning means traffic flows but something downstream is missing.
	SeverityWarning Severity = "warning"
)

// Finding is one named problem, with the action that resolves it. Remedy is
// deliberately a concrete instruction rather than a description: an operator
// reading this is already stuck.
type Finding struct {
	Severity Severity `json:"severity"`
	Code     string   `json:"code"`
	Summary  string   `json:"summary"`
	Remedy   string   `json:"remedy"`
}

// Counts is the shape of a tenant's configuration.
type Counts struct {
	Accounts int64 `json:"accounts"`
	Channels int64 `json:"channels"`
	Contacts int64 `json:"contacts"`
	Messages int64 `json:"messages"`
}

// Report is the diagnostics payload.
type Report struct {
	TenantID uuid.UUID `json:"tenant_id"`
	Healthy  bool      `json:"healthy"`
	Counts   Counts    `json:"counts"`
	Findings []Finding `json:"findings"`
}

// Service computes a Report.
type Service struct{ store store }

// NewService constructs a diagnostics Service.
func NewService(s store) *Service { return &Service{store: s} }

// Run gathers the counts and derives findings. Checks are ordered so the first
// finding is the one to fix first.
func (s *Service) Run(ctx context.Context, tenantID uuid.UUID) (Report, error) {
	rep := Report{TenantID: tenantID, Findings: []Finding{}}

	var err error
	if rep.Counts.Accounts, err = s.store.CountAccountsForTenant(ctx, tenantID); err != nil {
		return Report{}, fmt.Errorf("count accounts: %w", err)
	}
	if rep.Counts.Channels, err = s.store.CountChannelsForTenant(ctx, tenantID); err != nil {
		return Report{}, fmt.Errorf("count channels: %w", err)
	}
	if rep.Counts.Contacts, err = s.store.CountContactsForTenant(ctx, tenantID); err != nil {
		return Report{}, fmt.Errorf("count contacts: %w", err)
	}
	if rep.Counts.Messages, err = s.store.CountMessagesForTenant(ctx, tenantID); err != nil {
		return Report{}, fmt.Errorf("count messages: %w", err)
	}

	// No account at all: the webhook's lookup 404s and every inbound message is
	// acknowledged and dropped before it ever reaches this service.
	if rep.Counts.Accounts == 0 {
		rep.Findings = append(rep.Findings, Finding{
			Severity: SeverityBlocking,
			Code:     "no_accounts",
			Summary:  "No accounts. The webhook cannot match an inbound message to this tenant, so every message is acknowledged and dropped.",
			Remedy:   "POST /v1/accounts with the platform identifier your provider sends (for UltraMSG that is the instance id). Note the account type is 'whatsapp', not the listener id 'whatsapp-ultramsg'.",
		})
	}

	unroutable, err := s.store.ListUnroutableAccounts(ctx, tenantID)
	if err != nil {
		return Report{}, fmt.Errorf("list unroutable accounts: %w", err)
	}
	for _, a := range unroutable {
		rep.Findings = append(rep.Findings, Finding{
			Severity: SeverityBlocking,
			Code:     "account_not_linked_to_channel",
			Summary: fmt.Sprintf("Account %q (%s %s) is not linked to any inbound channel. Messages arriving on it are stored and forwarded nowhere.",
				a.Label, a.Type, a.PlatformIdentifier),
			Remedy: fmt.Sprintf("POST /v1/channels to create a channel, then POST /v1/channels/{id}/accounts with account_id=%s and direction=\"both\".", a.ID),
		})
	}

	noWorkflow, err := s.store.ListChannelsWithoutWorkflow(ctx, tenantID)
	if err != nil {
		return Report{}, fmt.Errorf("list channels without workflow: %w", err)
	}
	for _, c := range noWorkflow {
		rep.Findings = append(rep.Findings, Finding{
			Severity: SeverityWarning,
			Code:     "channel_has_no_workflow_url",
			Summary:  fmt.Sprintf("Channel %q has no workflow_url. Messages on it are parsed and stored, but nothing is forwarded to a consumer.", c.Name),
			Remedy:   fmt.Sprintf("PATCH /v1/channels/%s with {\"workflow_url\":\"https://your-service/hook\"}. Harmless to leave unset if you read the database directly.", c.ID),
		})
	}

	// Contacts are not required to receive traffic, but without them no short
	// id resolves, so every command lands as 'clarify' and looks broken.
	if rep.Counts.Contacts == 0 {
		rep.Findings = append(rep.Findings, Finding{
			Severity: SeverityWarning,
			Code:     "no_contacts",
			Summary:  "No contacts. Messages will be received, but no short id can resolve, so commands land as 'clarify' rather than 'execute'.",
			Remedy:   "POST /v1/contacts, or POST /v1/contacts/bulk-import to load an address book.",
		})
	}

	rep.Healthy = !hasBlocking(rep.Findings)
	return rep, nil
}

// hasBlocking reports whether any finding stops traffic outright. Warnings do
// not clear health on their own — a deployment that reads the database directly
// and has not loaded contacts yet is legitimately fine.
func hasBlocking(fs []Finding) bool {
	for _, f := range fs {
		if f.Severity == SeverityBlocking {
			return true
		}
	}
	return false
}
