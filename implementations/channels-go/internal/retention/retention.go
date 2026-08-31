// Package retention implements the two data-lifecycle obligations a public
// agency deploying this will be held to: deleting old data on a schedule, and
// removing one person's data on request.
//
// They are deliberately different operations.
//
// Purge is blunt and scheduled: everything older than a window the tenant sets
// is deleted outright.
//
// Erasure is targeted and redacting: a named contact's personal data is
// stripped from the messages they appear in, and the contact is deleted — but
// the message rows survive, anonymous. A disaster response's message history is
// an operational and often legal record; a missing-person report that vanishes
// entirely can make an after-action review impossible and, in some
// jurisdictions, breaks a retention duty sitting alongside the erasure right.
// Redaction satisfies the erasure right without destroying the record that
// traffic occurred. A tenant who genuinely needs rows gone uses purge.
//
// Both report what they would do before doing it: every entry point takes a
// dry-run, and dry-run is the default on the wire.
package retention

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/queries/goqueries"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// Sentinel errors mapped to HTTP status by the handler.
var (
	ErrInvalid  = errors.New("invalid")
	ErrNotFound = errors.New("contact not found")
)

// MinRetentionDays floors the purge window. Disaster traffic is routinely
// reviewed weeks later, and a fat-fingered `older_than_days: 0` would delete a
// live response's entire history — so the floor is a guard rail, not a policy.
const MinRetentionDays = 7

type store interface {
	CountMessagesOlderThan(ctx context.Context, arg goqueries.CountMessagesOlderThanParams) (int64, error)
	ClearReplyLinksToPurgedMessages(ctx context.Context, arg goqueries.ClearReplyLinksToPurgedMessagesParams) (int64, error)
	PurgeMessagesOlderThan(ctx context.Context, arg goqueries.PurgeMessagesOlderThanParams) (int64, error)

	CountMessagesForContact(ctx context.Context, arg goqueries.CountMessagesForContactParams) (int64, error)
	CountMessagesForEndpoint(ctx context.Context, arg goqueries.CountMessagesForEndpointParams) (int64, error)
	RedactMessagesForEndpoint(ctx context.Context, arg goqueries.RedactMessagesForEndpointParams) (int64, error)
	ListEndpointsForContact(ctx context.Context, contactID uuid.UUID) ([]string, error)
	RedactMessagesForContact(ctx context.Context, arg goqueries.RedactMessagesForContactParams) (int64, error)
	DetachMessagesFromContact(ctx context.Context, arg goqueries.DetachMessagesFromContactParams) (int64, error)
	DeleteEndpointsForContact(ctx context.Context, contactID uuid.UUID) (int64, error)
	DeleteContactRow(ctx context.Context, arg goqueries.DeleteContactRowParams) (int64, error)
}

// PurgeResult reports a purge, real or simulated.
type PurgeResult struct {
	DryRun          bool      `json:"dry_run"`
	OlderThanDays   int       `json:"older_than_days"`
	Cutoff          time.Time `json:"cutoff"`
	MessagesMatched int64     `json:"messages_matched"`
	MessagesDeleted int64     `json:"messages_deleted"`
}

// ErasureResult reports an erasure, real or simulated.
type ErasureResult struct {
	DryRun           bool       `json:"dry_run"`
	ContactID        *uuid.UUID `json:"contact_id,omitempty"`
	Endpoints        []string   `json:"endpoints"`
	MessagesMatched  int64      `json:"messages_matched"`
	MessagesRedacted int64      `json:"messages_redacted"`
	EndpointsDeleted int64      `json:"endpoints_deleted"`
	ContactDeleted   bool       `json:"contact_deleted"`
}

// EraseEndpoint redacts every message sent from one endpoint — a phone number,
// email address or handle. This is the operation a data-subject request maps
// onto: the request arrives as "delete my data, my number is …", and
// sender_endpoint is the identifying value actually stored on a message.
func (s *Service) EraseEndpoint(ctx context.Context, tenantID uuid.UUID, endpoint string, dryRun bool) (ErasureResult, error) {
	if strings.TrimSpace(endpoint) == "" {
		return ErasureResult{}, fmt.Errorf("%w: endpoint is required", ErrInvalid)
	}
	res := ErasureResult{DryRun: dryRun, Endpoints: []string{endpoint}}

	matched, err := s.store.CountMessagesForEndpoint(ctx, goqueries.CountMessagesForEndpointParams{
		TenantID: tenantID, SenderEndpoint: pgtype.Text{String: endpoint, Valid: true},
	})
	if err != nil {
		return ErasureResult{}, fmt.Errorf("count endpoint messages: %w", err)
	}
	res.MessagesMatched = matched
	if dryRun || matched == 0 {
		return res, nil
	}

	n, err := s.store.RedactMessagesForEndpoint(ctx, goqueries.RedactMessagesForEndpointParams{
		TenantID: tenantID, SenderEndpoint: pgtype.Text{String: endpoint, Valid: true},
	})
	if err != nil {
		return ErasureResult{}, fmt.Errorf("redact endpoint messages: %w", err)
	}
	res.MessagesRedacted = n
	return res, nil
}

// Service performs purge and erasure.
type Service struct {
	store store
	now   func() time.Time
}

// NewService constructs a retention Service.
func NewService(s store, now func() time.Time) *Service { return &Service{store: s, now: now} }

// Purge deletes messages older than olderThanDays. With dryRun it only counts.
func (s *Service) Purge(ctx context.Context, tenantID uuid.UUID, olderThanDays int, dryRun bool) (PurgeResult, error) {
	if olderThanDays < MinRetentionDays {
		return PurgeResult{}, fmt.Errorf("%w: older_than_days must be at least %d", ErrInvalid, MinRetentionDays)
	}
	cutoff := s.now().UTC().AddDate(0, 0, -olderThanDays)
	res := PurgeResult{DryRun: dryRun, OlderThanDays: olderThanDays, Cutoff: cutoff}

	matched, err := s.store.CountMessagesOlderThan(ctx, goqueries.CountMessagesOlderThanParams{
		TenantID: tenantID, ReceivedAt: cutoff,
	})
	if err != nil {
		return PurgeResult{}, fmt.Errorf("count purgeable: %w", err)
	}
	res.MessagesMatched = matched
	if dryRun || matched == 0 {
		return res, nil
	}

	// Replies point at other messages. Detach the ones aimed at rows about to
	// go, or the delete fails the self-referential foreign key.
	if _, err := s.store.ClearReplyLinksToPurgedMessages(ctx, goqueries.ClearReplyLinksToPurgedMessagesParams{
		TenantID: tenantID, ReceivedAt: cutoff,
	}); err != nil {
		return PurgeResult{}, fmt.Errorf("clear reply links: %w", err)
	}
	deleted, err := s.store.PurgeMessagesOlderThan(ctx, goqueries.PurgeMessagesOlderThanParams{
		TenantID: tenantID, ReceivedAt: cutoff,
	})
	if err != nil {
		return PurgeResult{}, fmt.Errorf("purge messages: %w", err)
	}
	res.MessagesDeleted = deleted
	return res, nil
}

// Erase removes a contact's personal data. With dryRun it reports what would
// change and touches nothing.
//
// Order matters: redact the message content first, then detach the contact
// link, then delete the endpoints, then the contact. Detaching before redacting
// would lose the ability to find the messages at all, leaving the personal data
// behind with nothing pointing at it — the worst possible outcome for an
// operation whose entire purpose is removing that data.
func (s *Service) Erase(ctx context.Context, tenantID, contactID uuid.UUID, dryRun bool) (ErasureResult, error) {
	res := ErasureResult{DryRun: dryRun, ContactID: &contactID, Endpoints: []string{}}

	matched, err := s.store.CountMessagesForContact(ctx, goqueries.CountMessagesForContactParams{
		TenantID: tenantID, SenderContactID: &contactID,
	})
	if err != nil {
		return ErasureResult{}, fmt.Errorf("count contact messages: %w", err)
	}
	res.MessagesMatched = matched

	// Messages carry sender_endpoint, not sender_contact_id — ingest does not
	// resolve an inbound sender to an address-book entry. So a contact erasure
	// that only followed the contact id would redact nothing. Walk the
	// contact's registered endpoints and erase by those too.
	eps, err := s.store.ListEndpointsForContact(ctx, contactID)
	if err != nil {
		return ErasureResult{}, fmt.Errorf("list contact endpoints: %w", err)
	}
	for _, e := range eps {
		if strings.TrimSpace(e) == "" {
			continue
		}
		res.Endpoints = append(res.Endpoints, e)
		n, err := s.store.CountMessagesForEndpoint(ctx, goqueries.CountMessagesForEndpointParams{
			TenantID: tenantID, SenderEndpoint: pgtype.Text{String: e, Valid: true},
		})
		if err != nil {
			return ErasureResult{}, fmt.Errorf("count endpoint messages: %w", err)
		}
		res.MessagesMatched += n
	}

	if dryRun {
		return res, nil
	}

	for _, e := range eps {
		if strings.TrimSpace(e) == "" {
			continue
		}
		n, err := s.store.RedactMessagesForEndpoint(ctx, goqueries.RedactMessagesForEndpointParams{
			TenantID: tenantID, SenderEndpoint: pgtype.Text{String: e, Valid: true},
		})
		if err != nil {
			return ErasureResult{}, fmt.Errorf("redact endpoint messages: %w", err)
		}
		res.MessagesRedacted += n
	}

	redacted, err := s.store.RedactMessagesForContact(ctx, goqueries.RedactMessagesForContactParams{
		TenantID: tenantID, SenderContactID: &contactID,
	})
	if err != nil {
		return ErasureResult{}, fmt.Errorf("redact messages: %w", err)
	}
	res.MessagesRedacted += redacted

	if _, err := s.store.DetachMessagesFromContact(ctx, goqueries.DetachMessagesFromContactParams{
		TenantID: tenantID, SenderContactID: &contactID,
	}); err != nil {
		return ErasureResult{}, fmt.Errorf("detach messages: %w", err)
	}

	deletedEps, err := s.store.DeleteEndpointsForContact(ctx, contactID)
	if err != nil {
		return ErasureResult{}, fmt.Errorf("delete endpoints: %w", err)
	}
	res.EndpointsDeleted = deletedEps

	n, err := s.store.DeleteContactRow(ctx, goqueries.DeleteContactRowParams{ID: contactID, TenantID: tenantID})
	if err != nil {
		return ErasureResult{}, fmt.Errorf("delete contact: %w", err)
	}
	if n == 0 {
		return ErasureResult{}, ErrNotFound
	}
	res.ContactDeleted = true
	return res, nil
}
