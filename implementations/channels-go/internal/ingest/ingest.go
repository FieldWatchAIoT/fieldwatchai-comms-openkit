// Package ingest persists inbound canonical messages from the comms-webhook
// and drives them through the pipeline: parse -> resolve -> score -> policy
// gate -> (on echo_back) dispatch an echo reply. Idempotent on
// (account_id, platform_message_id).
package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/canonical"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/crypto"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/outbound"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/parser"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/policy"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/queries/goqueries"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/resolver"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/workflow"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const defaultRecallSeconds = 120

// Sentinel errors mapped to HTTP status by the handler.
var (
	ErrInvalidAccount  = errors.New("invalid account id")
	ErrAccountNotFound = errors.New("account not found")
	// ErrAccountInactive means the account exists but is suspended or banned.
	// Treated as not-found by the handler: the caller should drop the message,
	// and distinguishing the two would tell an unauthenticated prober which
	// identifiers are real.
	ErrAccountInactive = errors.New("account is not active")
)

// store is the narrow query API ingest needs; *goqueries.Queries satisfies it.
type store interface {
	GetAccountByID(ctx context.Context, id uuid.UUID) (goqueries.Account, error)
	CreateInboundMessage(ctx context.Context, arg goqueries.CreateInboundMessageParams) (uuid.UUID, error)
	GetMessageIDByPlatformID(ctx context.Context, arg goqueries.GetMessageIDByPlatformIDParams) (uuid.UUID, error)
	CreateOutboundMessage(ctx context.Context, arg goqueries.CreateOutboundMessageParams) (uuid.UUID, error)
	FindRecentEchoForSender(ctx context.Context, arg goqueries.FindRecentEchoForSenderParams) (goqueries.FindRecentEchoForSenderRow, error)
	MarkMessageRecalled(ctx context.Context, id uuid.UUID) error
	GetInboundChannelForAccount(ctx context.Context, accountID uuid.UUID) (goqueries.GetInboundChannelForAccountRow, error)
	MarkWorkflowFired(ctx context.Context, id uuid.UUID) error
}

// workflowForwarder delivers a routed message to a consumer product's webhook.
// *workflow.Forwarder satisfies it.
type workflowForwarder interface {
	Forward(ctx context.Context, url string, f workflow.Forward) error
}

// channelCtx is the per-message channel config used by ingest, resolved from the
// account's channel (or the service defaults when none is configured).
type channelCtx struct {
	ID            *uuid.UUID
	ParserCfg     parser.Config
	Thresholds    policy.Thresholds
	RecallSeconds int
	WorkflowURL   string // consumer product webhook; empty = no forward
	Mode          string // "" / "structured" (grammar) or "passthrough" (raw text)
}

// recallKeyword is the control word a sender texts to recall (within the
// window) the message they just had echoed back.
const recallKeyword = "OOPS"

// addressResolver resolves a short_id to a contact; *resolver.Resolver satisfies it.
type addressResolver interface {
	Resolve(ctx context.Context, tenantID uuid.UUID, shortID string) (resolver.Match, error)
}

// parsedDoc is the JSON written to messages.parsed.
type parsedDoc struct {
	OK           bool           `json:"ok"`
	ShortID      string         `json:"short_id,omitempty"`
	Command      string         `json:"command,omitempty"`
	KnownCommand bool           `json:"known_command"`
	Target       string         `json:"target,omitempty"`
	TargetKind   string         `json:"target_kind"`
	Payload      string         `json:"payload,omitempty"`
	ShortIDMatch string         `json:"short_id_match,omitempty"`
	ContactID    *uuid.UUID     `json:"matched_contact_id"`
	Alternatives []resolver.Alt `json:"alternatives"`
	Confidence   float64        `json:"confidence"`
	Recall       bool           `json:"recall,omitempty"`
	Passthrough  bool           `json:"passthrough,omitempty"`
}

// Deps are the ingest service's collaborators.
type Deps struct {
	Store      store
	Resolver   addressResolver
	Encryptor  crypto.Encryptor
	Dispatcher *outbound.Registry
	Forwarder  workflowForwarder
	ParserCfg  parser.Config
	Thresholds policy.Thresholds
	Logger     *slog.Logger
	Now        func() time.Time
	NewID      func() uuid.UUID
}

// Service persists inbound messages and drives the pipeline.
type Service struct {
	d  Deps
	wg sync.WaitGroup // tracks in-flight background echo dispatches
}

// NewService constructs an ingest Service.
func NewService(d Deps) *Service { return &Service{d: d} }

// Wait blocks until all in-flight background echo dispatches finish. Call it on
// shutdown (before closing the pool) and in tests after Ingest.
func (s *Service) Wait() { s.wg.Wait() }

// Result is the outcome of an ingest call.
type Result struct {
	MessageID uuid.UUID
	IsReplay  bool
	Action    policy.Action
}

// Ingest resolves the account, parses + resolves + scores the message,
// persists it with the policy decision, and on echo_back dispatches a reply.
func (s *Service) Ingest(ctx context.Context, msg canonical.Message) (Result, error) {
	accID, err := uuid.Parse(msg.Meta.AccountID)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %q", ErrInvalidAccount, msg.Meta.AccountID)
	}
	acct, err := s.d.Store.GetAccountByID(ctx, accID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Result{}, ErrAccountNotFound
		}
		return Result{}, fmt.Errorf("resolve account: %w", err)
	}
	// Defence in depth behind the lookup filter: /v1/ingest can be called
	// directly, and a suspended account must not persist or dispatch anything
	// on either path.
	if acct.Status != "active" {
		s.d.Logger.Warn("dropping message for inactive account",
			"event", "account_inactive", "account_id", accID, "status", acct.Status)
		return Result{}, ErrAccountInactive
	}

	text := deref(msg.Body.Text)
	ch := s.resolveChannel(ctx, accID)

	// Recall control word ("OOPS") is handled before the command grammar: it's
	// not a routable message, it cancels the sender's most recent echoed message
	// if still inside the window.
	if strings.EqualFold(strings.TrimSpace(text), recallKeyword) {
		return s.handleRecall(ctx, acct, accID, msg, ch.RecallSeconds)
	}

	// Passthrough channels (conversational products) skip the command grammar:
	// the raw text is routed straight to the consumer, which does its own NLU.
	if ch.Mode == "passthrough" {
		return s.handlePassthrough(ctx, acct, accID, msg, ch)
	}

	doc, action, targetDesc, err := s.decide(ctx, acct.TenantID, text, ch.ParserCfg, ch.Thresholds)
	if err != nil {
		return Result{}, err
	}
	parsedJSON, err := json.Marshal(doc)
	if err != nil {
		return Result{}, fmt.Errorf("marshal parsed: %w", err)
	}

	id, err := s.d.Store.CreateInboundMessage(ctx, goqueries.CreateInboundMessageParams{
		ID:                s.d.NewID(),
		TenantID:          acct.TenantID,
		AccountID:         &accID,
		ChannelID:         ch.ID,
		SenderEndpoint:    textOrNull(msg.Sender.Endpoint),
		BodyText:          text,
		BodyAttachments:   attachmentsJSON(msg.Body.Attachments),
		BodyLocation:      ewktPoint(msg.Body.Location),
		Parsed:            parsedJSON,
		PolicyAction:      pgtype.Text{String: string(action), Valid: true},
		PlatformMessageID: msg.Meta.PlatformMessageID,
		RawPayload:        rawOrNull(msg.Meta.RawPayload),
		ReceivedAt:        s.receivedAt(msg.Meta.ReceivedAt),
		ProcessedAt:       ptr(s.d.Now().UTC()),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) { // ON CONFLICT DO NOTHING -> replay
			existing, gerr := s.d.Store.GetMessageIDByPlatformID(ctx, goqueries.GetMessageIDByPlatformIDParams{
				AccountID: &accID, PlatformMessageID: msg.Meta.PlatformMessageID,
			})
			if gerr != nil {
				return Result{}, fmt.Errorf("resolve replayed message: %w", gerr)
			}
			return Result{MessageID: existing, IsReplay: true, Action: action}, nil
		}
		return Result{}, fmt.Errorf("store message: %w", err)
	}

	if action == policy.ActionEchoBack {
		body := policy.EchoText(targetDesc, text, ch.RecallSeconds)
		s.spawnDispatch(ctx, acct, id, msg.Sender.Endpoint, body, "echoed_back")
	}
	// On execute, forward the parsed command to the channel's consumer product
	// (if one is configured). The consumer does the domain work and replies via
	// POST /v1/outbound. Best-effort + async, like outbound dispatch.
	if action == policy.ActionExecute && ch.WorkflowURL != "" && s.d.Forwarder != nil {
		s.spawnForward(ctx, ch.WorkflowURL, id, s.buildForward(acct, ch, msg, doc, id))
	}
	return Result{MessageID: id, IsReplay: false, Action: action}, nil
}

// ewktPoint renders a canonical location for the body_location geography
// column, or "" when the sender gave none (stored as NULL).
//
// PostGIS point order is (x, y) — LONGITUDE first. Reversing it silently
// relocates the sender to a different continent rather than erroring, so the
// order is asserted in tests rather than trusted.
func ewktPoint(l *canonical.Location) string {
	if l == nil {
		return ""
	}
	return "SRID=4326;POINT(" +
		strconv.FormatFloat(l.Lng, 'f', -1, 64) + " " +
		strconv.FormatFloat(l.Lat, 'f', -1, 64) + ")"
}

// buildForward assembles the consumer-webhook payload from the resolved message.
func (s *Service) buildForward(acct goqueries.Account, ch channelCtx, msg canonical.Message, doc parsedDoc, msgID uuid.UUID) workflow.Forward {
	f := workflow.Forward{
		MessageID: msgID.String(),
		TenantID:  acct.TenantID.String(),
		AccountID: acct.ID.String(),
		// Sourced from the account, not msg.Sender.Platform: the account type is
		// what the hub actually routes and dispatches on, and it is what a
		// consumer would otherwise resolve via GET /v1/accounts. Deriving both
		// from one authority keeps the forward and that lookup from disagreeing.
		AccountType:    acct.Type,
		Platform:       outbound.Platform(acct.Type),
		SenderEndpoint: msg.Sender.Endpoint,
		Command:        doc.Command,
		Payload:        doc.Payload,
		Text:           deref(msg.Body.Text),
		ReceivedAt:     s.receivedAt(msg.Meta.ReceivedAt).Format(time.RFC3339),
	}
	if ch.ID != nil {
		f.ChannelID = ch.ID.String()
	}
	if doc.ContactID != nil {
		f.ContactID = doc.ContactID.String()
	}
	if b := attachmentsJSON(msg.Body.Attachments); b != nil {
		f.Attachments = b
	}
	if msg.Body.Location != nil {
		if b, err := json.Marshal(msg.Body.Location); err == nil {
			f.Location = b
		}
	}
	return f
}

// spawnForward delivers a Forward to the consumer webhook in the background
// (detached + WaitGroup, like outbound), marking workflow_fired on success.
func (s *Service) spawnForward(reqCtx context.Context, url string, msgID uuid.UUID, f workflow.Forward) {
	detached := context.WithoutCancel(reqCtx)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ctx, cancel := context.WithTimeout(detached, 30*time.Second)
		defer cancel()
		if err := s.d.Forwarder.Forward(ctx, url, f); err != nil {
			s.d.Logger.Error("workflow forward failed", "event", "workflow_failed", "message_id", msgID, "error", err.Error())
			return
		}
		if err := s.d.Store.MarkWorkflowFired(ctx, msgID); err != nil {
			s.d.Logger.Error("mark workflow_fired failed", "event", "workflow_failed", "message_id", msgID, "error", err.Error())
			return
		}
		s.d.Logger.Info("workflow fired", "event", "workflow_fired", "message_id", msgID)
	}()
}

// handlePassthrough routes a conversational channel's raw text straight to the
// consumer product (no grammar, no echo/clarify): command is null, payload is
// the full text. The product does its own NLU and replies via /v1/outbound.
func (s *Service) handlePassthrough(ctx context.Context, acct goqueries.Account, accID uuid.UUID, msg canonical.Message, ch channelCtx) (Result, error) {
	text := deref(msg.Body.Text)
	doc := parsedDoc{OK: true, Passthrough: true, Payload: text, TargetKind: "none", Alternatives: []resolver.Alt{}}
	parsedJSON, _ := json.Marshal(doc)

	id, err := s.d.Store.CreateInboundMessage(ctx, goqueries.CreateInboundMessageParams{
		ID:                s.d.NewID(),
		TenantID:          acct.TenantID,
		AccountID:         &accID,
		ChannelID:         ch.ID,
		SenderEndpoint:    textOrNull(msg.Sender.Endpoint),
		BodyText:          text,
		BodyAttachments:   attachmentsJSON(msg.Body.Attachments),
		BodyLocation:      ewktPoint(msg.Body.Location),
		Parsed:            parsedJSON,
		PolicyAction:      pgtype.Text{String: "routed", Valid: true},
		PlatformMessageID: msg.Meta.PlatformMessageID,
		RawPayload:        rawOrNull(msg.Meta.RawPayload),
		ReceivedAt:        s.receivedAt(msg.Meta.ReceivedAt),
		ProcessedAt:       ptr(s.d.Now().UTC()),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) { // replay
			existing, gerr := s.d.Store.GetMessageIDByPlatformID(ctx, goqueries.GetMessageIDByPlatformIDParams{
				AccountID: &accID, PlatformMessageID: msg.Meta.PlatformMessageID,
			})
			if gerr != nil {
				return Result{}, fmt.Errorf("resolve replayed passthrough: %w", gerr)
			}
			return Result{MessageID: existing, IsReplay: true, Action: policy.Action("routed")}, nil
		}
		return Result{}, fmt.Errorf("store passthrough message: %w", err)
	}

	if ch.WorkflowURL != "" && s.d.Forwarder != nil {
		s.spawnForward(ctx, ch.WorkflowURL, id, s.buildForward(acct, ch, msg, doc, id))
	}
	return Result{MessageID: id, IsReplay: false, Action: policy.Action("routed")}, nil
}

// handleRecall processes an "OOPS" control message: if the sender has an echoed
// message still inside the recall window, mark it recalled and confirm. The
// OOPS message itself is persisted for audit. (The Week-4 workflow engine will
// additionally cancel the held consumer-forward; today the echo is the only
// prior action.)
func (s *Service) handleRecall(ctx context.Context, acct goqueries.Account, accID uuid.UUID, msg canonical.Message, recallSeconds int) (Result, error) {
	to := msg.Sender.Endpoint
	cutoff := s.d.Now().Add(-time.Duration(recallSeconds) * time.Second).UTC()

	rec, ferr := s.d.Store.FindRecentEchoForSender(ctx, goqueries.FindRecentEchoForSenderParams{
		AccountID:      &accID,
		SenderEndpoint: textOrNull(to),
		ReceivedAt:     cutoff,
	})
	found := ferr == nil
	if ferr != nil && !errors.Is(ferr, pgx.ErrNoRows) {
		return Result{}, fmt.Errorf("find recallable message: %w", ferr)
	}

	action := policy.ActionClarify // nothing to recall
	if found {
		action = policy.Action("recalled")
	}
	parsedJSON, _ := json.Marshal(parsedDoc{OK: false, Recall: true, TargetKind: "none", Alternatives: []resolver.Alt{}})

	id, err := s.d.Store.CreateInboundMessage(ctx, goqueries.CreateInboundMessageParams{
		ID:                s.d.NewID(),
		TenantID:          acct.TenantID,
		AccountID:         &accID,
		SenderEndpoint:    textOrNull(to),
		BodyText:          deref(msg.Body.Text),
		Parsed:            parsedJSON,
		PolicyAction:      pgtype.Text{String: string(action), Valid: true},
		PlatformMessageID: msg.Meta.PlatformMessageID,
		RawPayload:        rawOrNull(msg.Meta.RawPayload),
		ReceivedAt:        s.receivedAt(msg.Meta.ReceivedAt),
		ProcessedAt:       ptr(s.d.Now().UTC()),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) { // replay of the OOPS itself
			existing, gerr := s.d.Store.GetMessageIDByPlatformID(ctx, goqueries.GetMessageIDByPlatformIDParams{
				AccountID: &accID, PlatformMessageID: msg.Meta.PlatformMessageID,
			})
			if gerr != nil {
				return Result{}, fmt.Errorf("resolve replayed recall: %w", gerr)
			}
			return Result{MessageID: existing, IsReplay: true, Action: action}, nil
		}
		return Result{}, fmt.Errorf("store recall message: %w", err)
	}

	if found {
		if err := s.d.Store.MarkMessageRecalled(ctx, rec.ID); err != nil {
			s.d.Logger.Error("mark recalled failed", "event", "recall_failed", "error", err.Error())
		} else {
			s.d.Logger.Info("message recalled", "event", "recalled", "recalled_message_id", rec.ID)
			ack := fmt.Sprintf("RECALLED — %q was not forwarded.", rec.BodyText)
			s.spawnDispatch(ctx, acct, rec.ID, to, ack, "recalled")
		}
	}
	return Result{MessageID: id, IsReplay: false, Action: action}, nil
}

// resolveChannel loads the per-message channel config from the account's
// channel, falling back to the service defaults when no channel is configured
// (or the lookup fails — channel resolution is best-effort, never fatal).
func (s *Service) resolveChannel(ctx context.Context, accID uuid.UUID) channelCtx {
	cc := channelCtx{ParserCfg: s.d.ParserCfg, Thresholds: s.d.Thresholds, RecallSeconds: defaultRecallSeconds}
	row, err := s.d.Store.GetInboundChannelForAccount(ctx, accID)
	if err != nil {
		// Falling back to defaults is safe but consequential: with no channel
		// there is no workflow_url, so nothing is forwarded to any consumer and
		// a message on an unlinked account effectively disappears. Say so, or
		// the only symptom is silence on the sender's phone.
		if errors.Is(err, pgx.ErrNoRows) {
			s.d.Logger.Warn("account is not linked to a channel; using defaults and forwarding nowhere",
				"event", "channel.unlinked_account", "account_id", accID)
		} else {
			s.d.Logger.Error("channel resolution failed; using defaults",
				"event", "channel.resolve_failed", "account_id", accID, "error", err.Error())
		}
		return cc
	}
	id := row.ID
	cc.ID = &id
	if row.WorkflowUrl.Valid {
		cc.WorkflowURL = row.WorkflowUrl.String
	}

	var pc struct {
		Commands []string `json:"commands"`
		Mode     string   `json:"mode"`
	}
	if len(row.ParserConfig) > 0 && json.Unmarshal(row.ParserConfig, &pc) == nil {
		cc.Mode = pc.Mode
		if len(pc.Commands) > 0 {
			cc.ParserCfg = parser.Config{Commands: pc.Commands}
		}
	}
	var th struct {
		High   *float64 `json:"high"`
		Medium *float64 `json:"medium"`
	}
	if len(row.ConfidenceThresholds) > 0 && json.Unmarshal(row.ConfidenceThresholds, &th) == nil {
		if th.High != nil {
			cc.Thresholds.High = *th.High
		}
		if th.Medium != nil {
			cc.Thresholds.Medium = *th.Medium
		}
	}
	if row.RecallWindowSeconds > 0 {
		cc.RecallSeconds = int(row.RecallWindowSeconds)
	}
	return cc
}

// decide parses + resolves + scores the text, returning the parsed doc, the
// gate action, and a human description of the target for the echo text.
func (s *Service) decide(ctx context.Context, tenantID uuid.UUID, text string, cfg parser.Config, th policy.Thresholds) (parsedDoc, policy.Action, string, error) {
	pr := parser.Parse(text, cfg)
	if !pr.OK {
		doc := parsedDoc{OK: false, TargetKind: "none", Alternatives: []resolver.Alt{}}
		return doc, policy.ActionClarify, "", nil
	}

	doc := parsedDoc{
		OK: true, ShortID: pr.ShortID, Command: pr.Command, KnownCommand: pr.KnownCommand,
		Target: pr.Target, TargetKind: "none", Payload: pr.Payload, Alternatives: []resolver.Alt{},
	}
	targetDesc := pr.ShortID
	if pr.HasTarget {
		doc.TargetKind = "target" // contact-vs-group resolved in a later milestone
		targetDesc = "@" + pr.Target
	}

	m, err := s.d.Resolver.Resolve(ctx, tenantID, pr.ShortID)
	if err != nil {
		return parsedDoc{}, "", "", fmt.Errorf("resolve short_id: %w", err)
	}
	doc.ShortIDMatch = m.ShortIDMatch
	doc.ContactID = m.ContactID
	if m.Alternatives != nil {
		doc.Alternatives = m.Alternatives
	}

	doc.Confidence = policy.Score(policy.ScoreInput{
		ShortIDMatch: m.ShortIDMatch,
		HasTarget:    pr.HasTarget,
		TargetMatch:  "", // targets aren't resolved yet (Week 5) -> conservative
		HasCommand:   pr.Command != "",
		KnownCommand: pr.KnownCommand,
	})
	action := policy.Decide(doc.Confidence, pr.Command, doc.TargetKind, th)
	return doc, action, targetDesc, nil
}

// spawnDispatch sends an outbound message in the background, detached from the
// request context so it isn't canceled when the ingest response returns or FCW
// disconnects. Tracked by the WaitGroup so shutdown can drain in-flight sends.
// (River-backed durable dispatch is the production path — arrives with Week 4.)
func (s *Service) spawnDispatch(reqCtx context.Context, acct goqueries.Account, inReplyTo uuid.UUID, to, body, label string) {
	detached := context.WithoutCancel(reqCtx)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ctx, cancel := context.WithTimeout(detached, 20*time.Second)
		defer cancel()
		s.dispatchOutbound(ctx, acct, inReplyTo, to, body, label)
	}()
}

// dispatchOutbound decrypts the account credential, sends body to `to` via the
// account's adapter, and records the outbound message. Failures are logged,
// never fatal to ingest.
func (s *Service) dispatchOutbound(ctx context.Context, acct goqueries.Account, inReplyTo uuid.UUID, to, body, label string) {
	log := s.d.Logger
	if len(acct.CredentialsEncrypted) == 0 {
		log.Warn("no credentials to send", "event", "outbound_failed", "account_id", acct.ID, "reason", "no_credentials")
		return
	}
	token, err := s.d.Encryptor.Decrypt(ctx, acct.CredentialsEncrypted)
	if err != nil {
		log.Error("decrypt credentials failed", "event", "outbound_failed", "account_id", acct.ID, "error", err.Error())
		return
	}
	adapter, err := s.d.Dispatcher.Get(acct.Type)
	if err != nil {
		log.Error("no outbound adapter", "event", "outbound_failed", "account_type", acct.Type, "error", err.Error())
		return
	}

	pmid, err := adapter.Send(ctx, outbound.Account{Type: acct.Type, Identifier: acct.PlatformIdentifier, Token: string(token)}, to, body)
	if err != nil {
		log.Error("send failed", "event", "outbound_failed", "account_id", acct.ID, "error", err.Error())
		return
	}
	if pmid == "" {
		pmid = s.d.NewID().String()
	}

	accID := acct.ID
	if _, err := s.d.Store.CreateOutboundMessage(ctx, goqueries.CreateOutboundMessageParams{
		ID:                 s.d.NewID(),
		TenantID:           acct.TenantID,
		AccountID:          &accID,
		BodyText:           body,
		PolicyAction:       pgtype.Text{String: label, Valid: true},
		InReplyToMessageID: &inReplyTo,
		PlatformMessageID:  pmid,
		ReceivedAt:         s.d.Now().UTC(),
	}); err != nil {
		log.Error("persist outbound failed", "event", "outbound_failed", "error", err.Error())
		return
	}
	log.Info("outbound sent", "event", "outbound_sent", "in_reply_to", inReplyTo, "to", to, "kind", label)
}

func (s *Service) receivedAt(raw string) time.Time {
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC()
	}
	return s.d.Now().UTC()
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func ptr(t time.Time) *time.Time { return &t }

func textOrNull(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

func attachmentsJSON(atts []canonical.Attachment) []byte {
	if len(atts) == 0 {
		return nil
	}
	b, err := json.Marshal(atts)
	if err != nil {
		return nil
	}
	return b
}

func rawOrNull(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("null")
	}
	return raw
}
