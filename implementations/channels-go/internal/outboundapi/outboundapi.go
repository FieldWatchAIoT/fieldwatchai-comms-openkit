// Package outboundapi serves POST /v1/outbound — the product→user reply half of
// the consumer integration contract. A consumer product (or the console) calls
// it to send a message to a user; channels resolves the recipient + account and
// dispatches via the same outbound path the echo/recall use. The consumer never
// touches the platform or phone number.
//
// Two ways to address a message:
//
//   - in_reply_to_message_id — reply into an existing conversation. The endpoint
//     and account are inherited from the message being replied to.
//   - recipient {contact_id | endpoint} — initiate. There is no prior message, so
//     the destination is resolved from the address book (contact_endpoints) or
//     from the channel's outbound account.
package outboundapi

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/crypto"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/integrations/emailses"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/outbound"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/queries/goqueries"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Sentinel errors mapped to HTTP status by the handler.
var (
	ErrBadRequest = errors.New("bad request")
	ErrNotFound   = errors.New("recipient not resolvable")
)

// store is the narrow query API this service needs; *goqueries.Queries satisfies it.
type store interface {
	GetMessageForOutbound(ctx context.Context, id uuid.UUID) (goqueries.GetMessageForOutboundRow, error)
	GetAccountByID(ctx context.Context, id uuid.UUID) (goqueries.Account, error)
	CreateOutboundMessage(ctx context.Context, arg goqueries.CreateOutboundMessageParams) (uuid.UUID, error)
	GetOutboundEndpointForContact(ctx context.Context, arg goqueries.GetOutboundEndpointForContactParams) (goqueries.GetOutboundEndpointForContactRow, error)
	GetOutboundAccountForChannel(ctx context.Context, channelID uuid.UUID) (goqueries.GetOutboundAccountForChannelRow, error)
}

// Service sends consumer/console replies.
type Service struct {
	store store
	enc   crypto.Encryptor
	reg   *outbound.Registry
	now   func() time.Time
	newID func() uuid.UUID
}

// NewService constructs the outbound reply service.
func NewService(s store, enc crypto.Encryptor, reg *outbound.Registry, now func() time.Time, newID func() uuid.UUID) *Service {
	return &Service{store: s, enc: enc, reg: reg, now: now, newID: newID}
}

// Result is the outcome of a send.
type Result struct {
	MessageID    uuid.UUID
	DispatchedTo string
}

// target is a fully resolved destination: who to send to, and from which
// account. Both the reply path and the initiate path narrow to one of these
// before anything is dispatched.
type target struct {
	tenantID  uuid.UUID
	accountID uuid.UUID
	endpoint  string
	contactID *uuid.UUID
	inReplyTo *uuid.UUID
	// rawPayload is the replied-to message's platform payload, used for email
	// threading. nil when initiating — there is no thread yet.
	rawPayload []byte
}

// ReplyToMessage resolves the conversation referenced by inReplyTo, sends text to
// the original sender via that conversation's account, and records the outbound
// message. The caller waits for the result (synchronous, on the request ctx).
func (s *Service) ReplyToMessage(ctx context.Context, inReplyTo uuid.UUID, text string) (Result, error) {
	if text == "" {
		return Result{}, fmt.Errorf("%w: empty text", ErrBadRequest)
	}
	row, err := s.store.GetMessageForOutbound(ctx, inReplyTo)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Result{}, fmt.Errorf("%w: unknown in_reply_to_message_id", ErrNotFound)
		}
		return Result{}, fmt.Errorf("resolve conversation: %w", err)
	}
	if row.AccountID == nil || !row.SenderEndpoint.Valid || row.SenderEndpoint.String == "" {
		return Result{}, fmt.Errorf("%w: conversation has no account/sender", ErrNotFound)
	}

	return s.send(ctx, target{
		tenantID:   row.TenantID,
		accountID:  *row.AccountID,
		endpoint:   row.SenderEndpoint.String,
		contactID:  row.SenderContactID,
		inReplyTo:  &inReplyTo,
		rawPayload: row.RawPayload,
	}, text, "")
}

// SendToContact initiates a message to a contact addressed by id, resolving the
// contact's highest-priority outbound endpoint (optionally constrained to one
// channel) and the account that endpoint belongs to.
func (s *Service) SendToContact(ctx context.Context, contactID uuid.UUID, channelID *uuid.UUID, text, subject string) (Result, error) {
	if text == "" {
		return Result{}, fmt.Errorf("%w: empty text", ErrBadRequest)
	}
	row, err := s.store.GetOutboundEndpointForContact(ctx, goqueries.GetOutboundEndpointForContactParams{
		ContactID: contactID,
		ChannelID: channelID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Result{}, fmt.Errorf("%w: contact has no outbound endpoint", ErrNotFound)
		}
		return Result{}, fmt.Errorf("resolve contact endpoint: %w", err)
	}
	cID := contactID
	return s.send(ctx, target{
		tenantID:  row.TenantID,
		accountID: row.AccountID,
		endpoint:  row.Endpoint,
		contactID: &cID,
	}, text, subject)
}

// SendToEndpoint initiates a message to a raw platform address (a phone number,
// email, or handle not yet in the address book), sent from the channel's
// highest-priority outbound account. channelID is required: without a prior
// message or a contact row it is the only thing that says which account — and
// therefore which platform and which sending identity — to use.
func (s *Service) SendToEndpoint(ctx context.Context, channelID uuid.UUID, endpoint, text, subject string) (Result, error) {
	if text == "" {
		return Result{}, fmt.Errorf("%w: empty text", ErrBadRequest)
	}
	if endpoint == "" {
		return Result{}, fmt.Errorf("%w: empty endpoint", ErrBadRequest)
	}
	row, err := s.store.GetOutboundAccountForChannel(ctx, channelID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Result{}, fmt.Errorf("%w: channel has no outbound account", ErrNotFound)
		}
		return Result{}, fmt.Errorf("resolve channel account: %w", err)
	}
	return s.send(ctx, target{
		tenantID:  row.TenantID,
		accountID: row.AccountID,
		endpoint:  endpoint,
	}, text, subject)
}

// send loads the account for tgt, decrypts its credential, dispatches, and
// records the outbound message. Shared by the reply and initiate paths so both
// use the identical proven dispatch + persistence behaviour.
func (s *Service) send(ctx context.Context, tgt target, text, subject string) (Result, error) {
	acct, err := s.store.GetAccountByID(ctx, tgt.accountID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Result{}, fmt.Errorf("%w: account gone", ErrNotFound)
		}
		return Result{}, fmt.Errorf("load account: %w", err)
	}
	// Email (SES) authenticates via the task IAM role — no per-account token.
	// Token platforms require their decrypted credential.
	var token string
	if !outbound.Tokenless(acct.Type) {
		if len(acct.CredentialsEncrypted) == 0 {
			return Result{}, fmt.Errorf("account %s has no credentials", acct.ID)
		}
		tok, err := s.enc.Decrypt(ctx, acct.CredentialsEncrypted)
		if err != nil {
			return Result{}, fmt.Errorf("decrypt credentials: %w", err)
		}
		token = string(tok)
	}

	to := tgt.endpoint
	acctVal := outbound.Account{Type: acct.Type, Identifier: acct.PlatformIdentifier, Token: token}
	msg := outbound.Message{To: to, Body: text}
	if acct.Type == outbound.TypeEmailSES {
		if tgt.inReplyTo != nil {
			// Build subject + RFC threading + From/Reply-To from the original message.
			msg = emailses.BuildReply(acct.PlatformIdentifier, to, text, tgt.rawPayload)
		} else {
			msg = emailses.BuildNew(acct.PlatformIdentifier, to, subject, text)
		}
	}
	pmid, err := s.reg.Dispatch(ctx, acctVal, msg)
	if err != nil {
		return Result{}, fmt.Errorf("send: %w", err)
	}
	if pmid == "" {
		pmid = s.newID().String()
	}

	accID := tgt.accountID
	outID, err := s.store.CreateOutboundMessage(ctx, goqueries.CreateOutboundMessageParams{
		ID:                 s.newID(),
		TenantID:           tgt.tenantID,
		AccountID:          &accID,
		RecipientContactID: tgt.contactID,
		BodyText:           text,
		PolicyAction:       pgtype.Text{String: "sent", Valid: true},
		InReplyToMessageID: tgt.inReplyTo,
		PlatformMessageID:  pmid,
		ReceivedAt:         s.now().UTC(),
	})
	if err != nil {
		return Result{}, fmt.Errorf("persist outbound: %w", err)
	}
	return Result{MessageID: outID, DispatchedTo: to}, nil
}
