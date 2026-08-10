package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/canonical"
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

// fakeResolver returns a configurable match (defaults to none).
type fakeResolver struct{ m resolver.Match }

func (f fakeResolver) Resolve(_ context.Context, _ uuid.UUID, _ string) (resolver.Match, error) {
	if f.m.ShortIDMatch == "" {
		return resolver.Match{ShortIDMatch: resolver.MatchNone, Alternatives: []resolver.Alt{}}, nil
	}
	return f.m, nil
}

// fakeEnc returns a fixed plaintext token regardless of ciphertext.
type fakeEnc struct{}

func (fakeEnc) Encrypt(_ context.Context, p []byte) ([]byte, error) { return p, nil }
func (fakeEnc) Decrypt(_ context.Context, _ []byte) ([]byte, error) { return []byte("token"), nil }

// fakeAdapter records the last Send for assertions.
type fakeAdapter struct {
	to, body string
	calls    int
}

func (f *fakeAdapter) Send(_ context.Context, _ outbound.Account, to, body string) (string, error) {
	f.to, f.body = to, body
	f.calls++
	return "pmid-1", nil
}

type fakeStore struct {
	acct      goqueries.Account
	acctErr   error
	createID  uuid.UUID
	createErr error
	replayID  uuid.UUID

	gotCreate   goqueries.CreateInboundMessageParams
	gotByID     uuid.UUID
	gotOutbound goqueries.CreateOutboundMessageParams
	outCalls    int

	recallRow      goqueries.FindRecentEchoForSenderRow
	recallErr      error
	markedRecalled uuid.UUID

	channelRow    goqueries.GetInboundChannelForAccountRow // zero ID => no channel (defaults)
	workflowFired uuid.UUID
}

func (f *fakeStore) CreateOutboundMessage(_ context.Context, p goqueries.CreateOutboundMessageParams) (uuid.UUID, error) {
	f.gotOutbound = p
	f.outCalls++
	return p.ID, nil
}
func (f *fakeStore) FindRecentEchoForSender(_ context.Context, _ goqueries.FindRecentEchoForSenderParams) (goqueries.FindRecentEchoForSenderRow, error) {
	return f.recallRow, f.recallErr
}
func (f *fakeStore) MarkMessageRecalled(_ context.Context, id uuid.UUID) error {
	f.markedRecalled = id
	return nil
}
func (f *fakeStore) GetInboundChannelForAccount(_ context.Context, _ uuid.UUID) (goqueries.GetInboundChannelForAccountRow, error) {
	if f.channelRow.ID == (uuid.UUID{}) {
		return goqueries.GetInboundChannelForAccountRow{}, pgx.ErrNoRows
	}
	return f.channelRow, nil
}
func (f *fakeStore) MarkWorkflowFired(_ context.Context, id uuid.UUID) error {
	f.workflowFired = id
	return nil
}

// fakeForwarder records the forward call.
type fakeForwarder struct {
	url string
	f   workflow.Forward
	n   int
	err error
}

func (ff *fakeForwarder) Forward(_ context.Context, url string, f workflow.Forward) error {
	ff.url, ff.f, ff.n = url, f, ff.n+1
	return ff.err
}

func (f *fakeStore) GetAccountByID(_ context.Context, id uuid.UUID) (goqueries.Account, error) {
	f.gotByID = id
	return f.acct, f.acctErr
}
func (f *fakeStore) CreateInboundMessage(_ context.Context, p goqueries.CreateInboundMessageParams) (uuid.UUID, error) {
	f.gotCreate = p
	return f.createID, f.createErr
}
func (f *fakeStore) GetMessageIDByPlatformID(_ context.Context, _ goqueries.GetMessageIDByPlatformIDParams) (uuid.UUID, error) {
	return f.replayID, nil
}

func sp(s string) *string { return &s }

func msgFor(accountID string) canonical.Message {
	return canonical.Message{
		Sender: canonical.Sender{Endpoint: "+12425550042", Platform: "whatsapp"},
		Body:   canonical.Body{Text: sp("42 STATUS full")},
		Meta: canonical.Meta{
			PlatformMessageID: "false_abc123",
			ReceivedAt:        "2026-06-06T04:00:00Z",
			AccountID:         accountID,
			RawPayload:        json.RawMessage(`{"foo":"bar"}`),
		},
	}
}

func newService(store store) *Service {
	return newServiceWith(store, fakeResolver{}, outbound.NewRegistry())
}

func newServiceWith(store store, res addressResolver, reg *outbound.Registry) *Service {
	return NewService(Deps{
		Store:      store,
		Resolver:   res,
		Encryptor:  fakeEnc{},
		Dispatcher: reg,
		ParserCfg:  parser.Config{Commands: []string{"STATUS", "NEEDS", "SOS", "DAMAGE", "MISSING"}},
		Thresholds: policy.DefaultThresholds,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:        func() time.Time { return time.Unix(0, 0).UTC() },
		NewID:      func() uuid.UUID { return uuid.MustParse("99999999-9999-9999-9999-999999999999") },
	})
}

func TestIngestEchoBackDispatches(t *testing.T) {
	accID := uuid.New()
	contactID := uuid.New()
	fake := &fakeStore{
		acct: goqueries.Account{
			ID: accID, TenantID: uuid.New(), Type: "whatsapp",
			PlatformIdentifier: "inst123", CredentialsEncrypted: []byte("ciphertext"),
		},
		createID: uuid.MustParse("99999999-9999-9999-9999-999999999999"),
	}
	res := fakeResolver{m: resolver.Match{ShortIDMatch: resolver.MatchExact, ContactID: &contactID, Alternatives: []resolver.Alt{}}}
	adapter := &fakeAdapter{}
	reg := outbound.NewRegistry()
	reg.Register("whatsapp", adapter)
	svc := newServiceWith(fake, res, reg)

	// DAMAGE always echoes back regardless of confidence.
	msg := msgFor(accID.String())
	body := "42 DAMAGE roof collapsed"
	msg.Body.Text = &body

	r, err := svc.Ingest(context.Background(), msg)
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	svc.Wait() // echo dispatch is async; wait for the background send to finish
	if r.Action != policy.ActionEchoBack {
		t.Fatalf("action = %q, want echo_back", r.Action)
	}
	if adapter.calls != 1 {
		t.Fatalf("adapter.calls = %d, want 1", adapter.calls)
	}
	if adapter.to != "+12425550042" {
		t.Errorf("echo sent to %q, want the sender endpoint", adapter.to)
	}
	for _, want := range []string{"42 DAMAGE roof collapsed", "OOPS"} {
		if !contains(adapter.body, want) {
			t.Errorf("echo body missing %q; got %q", want, adapter.body)
		}
	}
	if fake.outCalls != 1 {
		t.Errorf("outbound message rows = %d, want 1", fake.outCalls)
	}
	if fake.gotOutbound.InReplyToMessageID == nil || *fake.gotOutbound.InReplyToMessageID != r.MessageID {
		t.Errorf("outbound not linked to inbound: %+v", fake.gotOutbound.InReplyToMessageID)
	}
}

func TestIngestExecuteFiresWorkflow(t *testing.T) {
	accID := uuid.New()
	chID := uuid.New()
	contactID := uuid.New()
	fake := &fakeStore{
		acct:     goqueries.Account{ID: accID, TenantID: uuid.New(), Type: "whatsapp", PlatformIdentifier: "179557"},
		createID: uuid.New(),
		channelRow: goqueries.GetInboundChannelForAccountRow{
			ID:                   chID,
			ParserConfig:         []byte(`{"commands":["STATUS"]}`),
			ConfidenceThresholds: []byte(`{"high":0.9,"medium":0.5}`),
			WorkflowUrl:          pgtype.Text{String: "https://consumer.example/inbound", Valid: true},
		},
	}
	ff := &fakeForwarder{}
	svc := NewService(Deps{
		Store: fake, Resolver: fakeResolver{m: resolver.Match{ShortIDMatch: resolver.MatchExact, ContactID: &contactID, Alternatives: []resolver.Alt{}}},
		Encryptor: fakeEnc{}, Dispatcher: outbound.NewRegistry(), Forwarder: ff,
		ParserCfg: parser.Config{Commands: []string{"STATUS"}}, Thresholds: policy.DefaultThresholds,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:    func() time.Time { return time.Unix(0, 0).UTC() }, NewID: uuid.New,
	})

	msg := msgFor(accID.String())
	body := "42 STATUS full" // exact match + known command -> confidence 1.0 -> execute
	msg.Body.Text = &body

	r, err := svc.Ingest(context.Background(), msg)
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	svc.Wait()
	if r.Action != policy.ActionExecute {
		t.Fatalf("action = %q, want execute", r.Action)
	}
	if ff.n != 1 || ff.url != "https://consumer.example/inbound" {
		t.Fatalf("forward not called correctly: n=%d url=%q", ff.n, ff.url)
	}
	if ff.f.Command != "STATUS" || ff.f.Payload != "full" || ff.f.Text != "42 STATUS full" {
		t.Errorf("forward payload wrong: %+v", ff.f)
	}
	if ff.f.ContactID != contactID.String() {
		t.Errorf("forward contact_id = %q, want %q", ff.f.ContactID, contactID)
	}
	if fake.workflowFired != r.MessageID {
		t.Errorf("workflow_fired marked on %v, want %v", fake.workflowFired, r.MessageID)
	}
}

func TestIngestPassthroughForwardsRawText(t *testing.T) {
	accID := uuid.New()
	chID := uuid.New()
	fake := &fakeStore{
		acct:     goqueries.Account{ID: accID, TenantID: uuid.New(), Type: "whatsapp", PlatformIdentifier: "179557"},
		createID: uuid.New(),
		channelRow: goqueries.GetInboundChannelForAccountRow{
			ID:           chID,
			ParserConfig: []byte(`{"mode":"passthrough"}`),
			WorkflowUrl:  pgtype.Text{String: "https://bot.example/inbound", Valid: true},
		},
	}
	ff := &fakeForwarder{}
	svc := NewService(Deps{
		Store: fake, Resolver: fakeResolver{}, Encryptor: fakeEnc{}, Dispatcher: outbound.NewRegistry(), Forwarder: ff,
		ParserCfg: parser.Config{Commands: []string{"STATUS"}}, Thresholds: policy.DefaultThresholds,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:    func() time.Time { return time.Unix(0, 0).UTC() }, NewID: uuid.New,
	})

	msg := msgFor(accID.String())
	body := "hi there, anyone around?" // not grammar — would be clarify on a structured channel
	msg.Body.Text = &body

	r, err := svc.Ingest(context.Background(), msg)
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	svc.Wait()
	if string(r.Action) != "routed" {
		t.Fatalf("action = %q, want routed", r.Action)
	}
	if ff.n != 1 || ff.url != "https://bot.example/inbound" {
		t.Fatalf("forward not called: n=%d url=%q", ff.n, ff.url)
	}
	if ff.f.Command != "" || ff.f.Payload != body || ff.f.Text != body {
		t.Errorf("passthrough payload wrong: command=%q payload=%q text=%q", ff.f.Command, ff.f.Payload, ff.f.Text)
	}
	if fake.gotCreate.ChannelID == nil || *fake.gotCreate.ChannelID != chID {
		t.Errorf("channel_id not set")
	}
}

func TestIngestUsesChannelConfig(t *testing.T) {
	accID := uuid.New()
	chID := uuid.New()
	fake := &fakeStore{
		acct:     goqueries.Account{ID: accID, TenantID: uuid.New(), Type: "whatsapp", PlatformIdentifier: "179557"},
		createID: uuid.New(),
		channelRow: goqueries.GetInboundChannelForAccountRow{
			ID:                   chID,
			ParserConfig:         []byte(`{"commands":["PING"]}`),
			ConfidenceThresholds: []byte(`{"high":0.9,"medium":0.5}`),
			RecallWindowSeconds:  60,
		},
	}
	// Default service command set is STATUS/DAMAGE/etc — but the channel says PING.
	svc := newServiceWith(fake, fakeResolver{}, outbound.NewRegistry())

	msg := msgFor(accID.String())
	body := "42 PING hello"
	msg.Body.Text = &body
	if _, err := svc.Ingest(context.Background(), msg); err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	if fake.gotCreate.ChannelID == nil || *fake.gotCreate.ChannelID != chID {
		t.Errorf("channel_id not set from resolved channel: %v", fake.gotCreate.ChannelID)
	}
	var doc struct {
		Command      string `json:"command"`
		KnownCommand bool   `json:"known_command"`
	}
	if err := json.Unmarshal(fake.gotCreate.Parsed, &doc); err != nil {
		t.Fatalf("parsed not json: %v", err)
	}
	if doc.Command != "PING" || !doc.KnownCommand {
		t.Errorf("channel command set not applied: %+v (PING should be known)", doc)
	}
}

func TestIngestRecallCancelsRecentEcho(t *testing.T) {
	accID := uuid.New()
	recalledID := uuid.New()
	fake := &fakeStore{
		acct: goqueries.Account{
			ID: accID, TenantID: uuid.New(), Type: "whatsapp",
			PlatformIdentifier: "179557", CredentialsEncrypted: []byte("ct"),
		},
		createID:  uuid.New(),
		recallRow: goqueries.FindRecentEchoForSenderRow{ID: recalledID, BodyText: "42 DAMAGE test"},
	}
	adapter := &fakeAdapter{}
	reg := outbound.NewRegistry()
	reg.Register("whatsapp", adapter)
	svc := newServiceWith(fake, fakeResolver{}, reg)

	msg := msgFor(accID.String())
	body := "OOPS"
	msg.Body.Text = &body

	r, err := svc.Ingest(context.Background(), msg)
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	svc.Wait()
	if string(r.Action) != "recalled" {
		t.Fatalf("action = %q, want recalled", r.Action)
	}
	if fake.markedRecalled != recalledID {
		t.Errorf("marked %v recalled, want %v", fake.markedRecalled, recalledID)
	}
	if adapter.calls != 1 || !contains(adapter.body, "RECALLED") || !contains(adapter.body, "42 DAMAGE test") {
		t.Errorf("recall ack not sent correctly: calls=%d body=%q", adapter.calls, adapter.body)
	}
}

func TestIngestRecallNothingToRecall(t *testing.T) {
	accID := uuid.New()
	fake := &fakeStore{
		acct:      goqueries.Account{ID: accID, TenantID: uuid.New(), Type: "whatsapp", PlatformIdentifier: "179557", CredentialsEncrypted: []byte("ct")},
		createID:  uuid.New(),
		recallErr: pgx.ErrNoRows, // no recent echo in the window
	}
	adapter := &fakeAdapter{}
	reg := outbound.NewRegistry()
	reg.Register("whatsapp", adapter)
	svc := newServiceWith(fake, fakeResolver{}, reg)

	msg := msgFor(accID.String())
	body := "oops" // case-insensitive
	msg.Body.Text = &body

	r, err := svc.Ingest(context.Background(), msg)
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	svc.Wait()
	if r.Action != policy.ActionClarify {
		t.Errorf("action = %q, want clarify (nothing to recall)", r.Action)
	}
	if adapter.calls != 0 {
		t.Errorf("no ack should be sent when nothing to recall; calls=%d", adapter.calls)
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestIngestStoresNewMessage(t *testing.T) {
	accID := uuid.New()
	tenant := uuid.New()
	fake := &fakeStore{
		acct:     goqueries.Account{ID: accID, TenantID: tenant},
		createID: uuid.MustParse("99999999-9999-9999-9999-999999999999"),
	}
	svc := newService(fake)

	res, err := svc.Ingest(context.Background(), msgFor(accID.String()))
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if res.IsReplay {
		t.Error("new message marked as replay")
	}
	if res.MessageID != fake.createID {
		t.Errorf("MessageID = %v, want %v", res.MessageID, fake.createID)
	}
	if fake.gotCreate.TenantID != tenant {
		t.Errorf("tenant not derived from account: %v", fake.gotCreate.TenantID)
	}
	if fake.gotCreate.AccountID == nil || *fake.gotCreate.AccountID != accID {
		t.Errorf("account_id not set: %v", fake.gotCreate.AccountID)
	}
	if fake.gotCreate.PlatformMessageID != "false_abc123" {
		t.Errorf("platform id = %q", fake.gotCreate.PlatformMessageID)
	}
	if fake.gotCreate.BodyText != "42 STATUS full" {
		t.Errorf("body_text = %q", fake.gotCreate.BodyText)
	}
	if string(fake.gotCreate.RawPayload) != `{"foo":"bar"}` {
		t.Errorf("raw_payload = %s", fake.gotCreate.RawPayload)
	}
}

func TestIngestReplayOnConflict(t *testing.T) {
	accID := uuid.New()
	existing := uuid.New()
	fake := &fakeStore{
		acct:      goqueries.Account{ID: accID, TenantID: uuid.New()},
		createErr: pgx.ErrNoRows, // ON CONFLICT DO NOTHING returns no row
		replayID:  existing,
	}
	svc := newService(fake)

	res, err := svc.Ingest(context.Background(), msgFor(accID.String()))
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if !res.IsReplay {
		t.Error("duplicate not marked as replay")
	}
	if res.MessageID != existing {
		t.Errorf("MessageID = %v, want existing %v", res.MessageID, existing)
	}
}

func TestIngestInvalidAccountID(t *testing.T) {
	svc := newService(&fakeStore{})
	_, err := svc.Ingest(context.Background(), msgFor("not-a-uuid"))
	if !errors.Is(err, ErrInvalidAccount) {
		t.Fatalf("Ingest = %v, want ErrInvalidAccount", err)
	}
}

func TestIngestAccountNotFound(t *testing.T) {
	fake := &fakeStore{acctErr: pgx.ErrNoRows}
	svc := newService(fake)
	_, err := svc.Ingest(context.Background(), msgFor(uuid.New().String()))
	if !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("Ingest = %v, want ErrAccountNotFound", err)
	}
}

// An account with no channel_accounts row still ingests, but nothing is
// forwarded and no reply goes out — the message effectively vanishes. That is
// the failure mode a console can't see, so ingest must at least say it happened.
func TestIngestWarnsWhenAccountHasNoChannel(t *testing.T) {
	accID := uuid.New()
	fake := &fakeStore{acct: goqueries.Account{ID: accID, TenantID: uuid.New(), Type: "whatsapp"}}

	var logs strings.Builder
	svc := NewService(Deps{
		Store:      fake,
		Resolver:   fakeResolver{},
		Encryptor:  fakeEnc{},
		Dispatcher: outbound.NewRegistry(),
		ParserCfg:  parser.Config{Commands: []string{"STATUS"}},
		Thresholds: policy.DefaultThresholds,
		Logger:     slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Now:        func() time.Time { return time.Unix(0, 0).UTC() },
		NewID:      uuid.New,
	})

	if _, err := svc.Ingest(context.Background(), msgFor(accID.String())); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	svc.Wait()

	if !strings.Contains(logs.String(), "channel.unlinked_account") {
		t.Errorf("no unlinked-account warning logged; got: %s", logs.String())
	}
	if !strings.Contains(logs.String(), accID.String()) {
		t.Errorf("warning does not identify the account: %s", logs.String())
	}
}

// The forward must say which transport a message arrived on. account_id alone
// can't: one Twilio number serves both SMS and WhatsApp, so the same
// sender_endpoint reaches the consumer over two platforms and the payload is
// otherwise identical. Consumers were having to call /v1/accounts to label a
// message, which needs hub credentials they may not have.
func TestIngestForwardCarriesPlatformAndAccountType(t *testing.T) {
	for _, tc := range []struct{ acctType, wantPlatform string }{
		{"sms-twilio", "sms"},
		{"whatsapp-twilio", "whatsapp"},
		{"whatsapp", "whatsapp"},
		{"telegram", "telegram"},
		{"email-ses", "email"},
	} {
		t.Run(tc.acctType, func(t *testing.T) {
			accID := uuid.New()
			fake := &fakeStore{
				acct:     goqueries.Account{ID: accID, TenantID: uuid.New(), Type: tc.acctType, PlatformIdentifier: "+12897792824"},
				createID: uuid.New(),
				channelRow: goqueries.GetInboundChannelForAccountRow{
					ID:           uuid.New(),
					ParserConfig: []byte(`{"mode":"passthrough"}`),
					WorkflowUrl:  pgtype.Text{String: "https://consumer.example/inbound", Valid: true},
				},
			}
			ff := &fakeForwarder{}
			svc := NewService(Deps{
				Store: fake, Resolver: fakeResolver{}, Encryptor: fakeEnc{},
				Dispatcher: outbound.NewRegistry(), Forwarder: ff,
				ParserCfg: parser.Config{Commands: []string{"STATUS"}}, Thresholds: policy.DefaultThresholds,
				Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
				Now:    func() time.Time { return time.Unix(0, 0).UTC() }, NewID: uuid.New,
			})

			if _, err := svc.Ingest(context.Background(), msgFor(accID.String())); err != nil {
				t.Fatalf("Ingest: %v", err)
			}
			svc.Wait()

			if ff.n != 1 {
				t.Fatalf("forward not called: n=%d", ff.n)
			}
			if ff.f.AccountType != tc.acctType {
				t.Errorf("account_type = %q, want %q", ff.f.AccountType, tc.acctType)
			}
			if ff.f.Platform != tc.wantPlatform {
				t.Errorf("platform = %q, want %q", ff.f.Platform, tc.wantPlatform)
			}
		})
	}
}

// PostGIS point order is (x, y) = (lng, lat). Getting it backwards does not
// error — it silently relocates the sender — so the order is asserted, not
// trusted. Nassau is 25.06N 77.34W: latitude out of range for a longitude slot
// would be the only symptom, and only sometimes.
func TestEWKTPointPutsLongitudeFirst(t *testing.T) {
	got := ewktPoint(&canonical.Location{Lat: 25.0343, Lng: -77.3963})
	if got != "SRID=4326;POINT(-77.3963 25.0343)" {
		t.Errorf("ewktPoint = %q, want lng first", got)
	}
	if ewktPoint(nil) != "" {
		t.Errorf("nil location must render empty (stored NULL), got %q", ewktPoint(nil))
	}
}

func TestIngestPersistsLocation(t *testing.T) {
	accID := uuid.New()
	fake := &fakeStore{
		acct:     goqueries.Account{ID: accID, TenantID: uuid.New(), Type: "whatsapp"},
		createID: uuid.New(),
	}
	svc := newService(fake)

	msg := msgFor(accID.String())
	msg.Body.Location = &canonical.Location{Lat: 25.0343, Lng: -77.3963}
	if _, err := svc.Ingest(context.Background(), msg); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if fake.gotCreate.BodyLocation != "SRID=4326;POINT(-77.3963 25.0343)" {
		t.Errorf("body_location not persisted: %q", fake.gotCreate.BodyLocation)
	}

	// No location must store NULL, not a point at 0,0 in the Gulf of Guinea.
	plain := msgFor(accID.String())
	plain.Meta.PlatformMessageID = "other"
	if _, err := svc.Ingest(context.Background(), plain); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if fake.gotCreate.BodyLocation != "" {
		t.Errorf("absent location must be empty, got %q", fake.gotCreate.BodyLocation)
	}
}
