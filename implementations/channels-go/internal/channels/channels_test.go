package channels

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/policy"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/queries/goqueries"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type fakeStore struct {
	channels   []goqueries.Channel
	getCh      goqueries.Channel
	getChErr   error
	getAcct    goqueries.Account
	getAcctEr  error
	links      []goqueries.ListAccountLinksForChannelRow
	linkParams goqueries.LinkAccountToChannelParams
	unlinkArg  goqueries.UnlinkAccountFromChannelParams
	unlinkN    int64

	createParams goqueries.CreateChannelParams
	updateParams goqueries.UpdateChannelParams
	updateErr    error
}

func (f *fakeStore) ListChannelsForTenant(_ context.Context, _ uuid.UUID) ([]goqueries.Channel, error) {
	return f.channels, nil
}
func (f *fakeStore) GetChannelForTenant(_ context.Context, _ goqueries.GetChannelForTenantParams) (goqueries.Channel, error) {
	return f.getCh, f.getChErr
}
func (f *fakeStore) GetAccountForTenant(_ context.Context, _ goqueries.GetAccountForTenantParams) (goqueries.Account, error) {
	return f.getAcct, f.getAcctEr
}
func (f *fakeStore) LinkAccountToChannel(_ context.Context, p goqueries.LinkAccountToChannelParams) (goqueries.ChannelAccount, error) {
	f.linkParams = p
	return goqueries.ChannelAccount{
		ChannelID: p.ChannelID, AccountID: p.AccountID,
		Direction: p.Direction, Priority: p.Priority, RoutingFilter: p.RoutingFilter,
	}, nil
}
func (f *fakeStore) UnlinkAccountFromChannel(_ context.Context, p goqueries.UnlinkAccountFromChannelParams) (int64, error) {
	f.unlinkArg = p
	return f.unlinkN, nil
}
func (f *fakeStore) ListAccountLinksForChannel(_ context.Context, _ uuid.UUID) ([]goqueries.ListAccountLinksForChannelRow, error) {
	return f.links, nil
}
func (f *fakeStore) CreateChannel(_ context.Context, p goqueries.CreateChannelParams) (goqueries.Channel, error) {
	f.createParams = p
	return goqueries.Channel{
		ID: p.ID, TenantID: p.TenantID, Name: p.Name, ParserConfig: p.ParserConfig,
		WorkflowUrl: p.WorkflowUrl, ReplyPolicy: p.ReplyPolicy,
		ConfidenceThresholds: p.ConfidenceThresholds, EchoBackEnabled: p.EchoBackEnabled,
		RecallWindowSeconds: p.RecallWindowSeconds, CreatedAt: p.CreatedAt,
	}, nil
}
func (f *fakeStore) UpdateChannel(_ context.Context, p goqueries.UpdateChannelParams) (goqueries.Channel, error) {
	f.updateParams = p
	if f.updateErr != nil {
		return goqueries.Channel{}, f.updateErr
	}
	return goqueries.Channel{ID: p.ID, TenantID: p.TenantID, Name: p.Name.String, WorkflowUrl: p.WorkflowUrl}, nil
}

// --- service ---

func TestLinkVerifiesBothSidesBelongToTenant(t *testing.T) {
	chID, acctID, tenant := uuid.New(), uuid.New(), uuid.New()
	fs := &fakeStore{getCh: goqueries.Channel{ID: chID, TenantID: tenant}, getAcct: goqueries.Account{ID: acctID}}

	link, err := NewService(fs).Link(context.Background(), LinkInput{
		ChannelID: chID, AccountID: acctID, TenantID: tenant, Direction: "both", Priority: 100,
	})
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if link.ChannelID != chID || link.AccountID != acctID || link.Direction != "both" || link.Priority != 100 {
		t.Fatalf("link wrong: %+v", link)
	}
}

// A link across tenants would route one tenant's messages into another's
// consumer, so an account outside the caller's tenant must never link.
func TestLinkRejectsAccountFromAnotherTenant(t *testing.T) {
	fs := &fakeStore{getCh: goqueries.Channel{ID: uuid.New()}, getAcctEr: pgx.ErrNoRows}
	_, err := NewService(fs).Link(context.Background(), LinkInput{
		ChannelID: uuid.New(), AccountID: uuid.New(), TenantID: uuid.New(), Direction: "both",
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if fs.linkParams.AccountID != uuid.Nil {
		t.Error("link must not be written when the account is out of tenant")
	}
}

func TestLinkRejectsUnknownChannel(t *testing.T) {
	fs := &fakeStore{getChErr: pgx.ErrNoRows}
	_, err := NewService(fs).Link(context.Background(), LinkInput{
		ChannelID: uuid.New(), AccountID: uuid.New(), TenantID: uuid.New(), Direction: "both",
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestLinkRejectsBadDirection(t *testing.T) {
	fs := &fakeStore{getCh: goqueries.Channel{ID: uuid.New()}}
	_, err := NewService(fs).Link(context.Background(), LinkInput{
		ChannelID: uuid.New(), AccountID: uuid.New(), TenantID: uuid.New(), Direction: "sideways",
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestUnlinkNotFoundWhenNoRowRemoved(t *testing.T) {
	fs := &fakeStore{getCh: goqueries.Channel{ID: uuid.New()}, unlinkN: 0}
	err := NewService(fs).Unlink(context.Background(), uuid.New(), uuid.New(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestListLiftsModeAndWorkflowURL(t *testing.T) {
	fs := &fakeStore{channels: []goqueries.Channel{
		{ID: uuid.New(), Name: "Globe", ParserConfig: []byte(`{"mode":"passthrough"}`),
			WorkflowUrl: pgtype.Text{String: "https://globe/api", Valid: true}},
		{ID: uuid.New(), Name: "Resilience", ParserConfig: []byte(`{"commands":["STATUS"]}`)},
	}}
	out, err := NewService(fs).List(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if out[0].Mode != "passthrough" || out[0].WorkflowURL != "https://globe/api" {
		t.Errorf("passthrough channel wrong: %+v", out[0])
	}
	// A channel with no explicit mode is the grammar path; say so rather than "".
	if out[1].Mode != "structured" {
		t.Errorf("mode = %q, want structured", out[1].Mode)
	}
	if out[1].WorkflowURL != "" {
		t.Errorf("workflow_url should be empty when NULL, got %q", out[1].WorkflowURL)
	}
}

// --- handler ---

type fakeSvc struct {
	chans    []Channel
	ch       Channel
	links    []Link
	link     Link
	err      error
	gotIn    LinkInput
	unlinked [2]uuid.UUID
	called   string

	createIn CreateInput
	updateIn UpdateInput
}

func (f *fakeSvc) List(_ context.Context, _ uuid.UUID) ([]Channel, error) { return f.chans, f.err }
func (f *fakeSvc) Get(_ context.Context, _, _ uuid.UUID) (Channel, error) { return f.ch, f.err }
func (f *fakeSvc) ListLinks(_ context.Context, _, _ uuid.UUID) ([]Link, error) {
	return f.links, f.err
}
func (f *fakeSvc) Create(_ context.Context, in CreateInput, _ func() time.Time, _ func() uuid.UUID) (Channel, error) {
	f.createIn, f.called = in, "create"
	return f.ch, f.err
}
func (f *fakeSvc) Update(_ context.Context, _, _ uuid.UUID, in UpdateInput) (Channel, error) {
	f.updateIn, f.called = in, "update"
	return f.ch, f.err
}
func (f *fakeSvc) Link(_ context.Context, in LinkInput) (Link, error) {
	f.gotIn, f.called = in, "link"
	return f.link, f.err
}
func (f *fakeSvc) Unlink(_ context.Context, ch, acct, _ uuid.UUID) error {
	f.unlinked, f.called = [2]uuid.UUID{ch, acct}, "unlink"
	return f.err
}

func do(t *testing.T, svc serviceAPI, method, path, body, tenant string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	NewHandler(svc, discardLogger()).RegisterRoutes(mux)
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if tenant != "" {
		req.Header.Set("X-Tenant-ID", tenant)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestHandlerLink201(t *testing.T) {
	chID, acctID, tenant := uuid.New(), uuid.New(), uuid.New()
	fs := &fakeSvc{link: Link{ChannelID: chID, AccountID: acctID, Direction: "both", Priority: 100}}

	rec := do(t, fs, http.MethodPost, "/v1/channels/"+chID.String()+"/accounts",
		`{"account_id":"`+acctID.String()+`","direction":"inbound","priority":50}`, tenant.String())

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if fs.gotIn.ChannelID != chID || fs.gotIn.AccountID != acctID {
		t.Errorf("ids not threaded through: %+v", fs.gotIn)
	}
	if fs.gotIn.Direction != "inbound" || fs.gotIn.Priority != 50 {
		t.Errorf("direction/priority wrong: %+v", fs.gotIn)
	}
	if fs.gotIn.TenantID != tenant {
		t.Errorf("tenant not taken from the header")
	}
}

func TestHandlerLinkDefaults(t *testing.T) {
	chID, acctID := uuid.New(), uuid.New()
	fs := &fakeSvc{}
	rec := do(t, fs, http.MethodPost, "/v1/channels/"+chID.String()+"/accounts",
		`{"account_id":"`+acctID.String()+`"}`, uuid.New().String())
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
	if fs.gotIn.Direction != "both" || fs.gotIn.Priority != 100 {
		t.Errorf("defaults wrong: direction=%q priority=%d", fs.gotIn.Direction, fs.gotIn.Priority)
	}
}

func TestHandlerLinkBadInput(t *testing.T) {
	chID := uuid.New()
	for _, tc := range []struct{ name, path, body, tenant string }{
		{"missing tenant", "/v1/channels/" + chID.String() + "/accounts", `{"account_id":"` + uuid.New().String() + `"}`, ""},
		{"bad account id", "/v1/channels/" + chID.String() + "/accounts", `{"account_id":"nope"}`, uuid.New().String()},
		{"bad channel id", "/v1/channels/nope/accounts", `{"account_id":"` + uuid.New().String() + `"}`, uuid.New().String()},
		{"bad json", "/v1/channels/" + chID.String() + "/accounts", `{`, uuid.New().String()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := &fakeSvc{}
			rec := do(t, fs, http.MethodPost, tc.path, tc.body, tc.tenant)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			if fs.called != "" {
				t.Errorf("service must not be called, got %q", fs.called)
			}
		})
	}
}

func TestHandlerUnlink204(t *testing.T) {
	chID, acctID := uuid.New(), uuid.New()
	fs := &fakeSvc{}
	rec := do(t, fs, http.MethodDelete, "/v1/channels/"+chID.String()+"/accounts/"+acctID.String(), "", uuid.New().String())
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if fs.unlinked != [2]uuid.UUID{chID, acctID} {
		t.Errorf("unlink ids wrong: %v", fs.unlinked)
	}
}

func TestHandlerUnlinkNotFound(t *testing.T) {
	fs := &fakeSvc{err: ErrNotFound}
	rec := do(t, fs, http.MethodDelete,
		"/v1/channels/"+uuid.New().String()+"/accounts/"+uuid.New().String(), "", uuid.New().String())
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestHandlerListChannels(t *testing.T) {
	fs := &fakeSvc{chans: []Channel{{ID: uuid.New(), Name: "Globe", Mode: "passthrough"}}}
	rec := do(t, fs, http.MethodGet, "/v1/channels", "", uuid.New().String())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got []Channel
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].Name != "Globe" || got[0].Mode != "passthrough" {
		t.Errorf("body = %s", rec.Body.String())
	}
}

func TestHandlerListLinks(t *testing.T) {
	fs := &fakeSvc{links: []Link{{AccountID: uuid.New(), Direction: "both", AccountType: "telegram"}}}
	rec := do(t, fs, http.MethodGet, "/v1/channels/"+uuid.New().String()+"/accounts", "", uuid.New().String())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "telegram") {
		t.Errorf("account detail missing from links: %s", rec.Body.String())
	}
}

// --- create / update ---

func fixedTime() time.Time { return time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC) }

// The headline promise of Create: a name is enough. If this regresses, the
// "no SQL required" setup path quietly stops working.
func TestCreateOnlyNeedsAName(t *testing.T) {
	fs := &fakeStore{}
	id := uuid.New()
	ch, err := NewService(fs).Create(context.Background(),
		CreateInput{TenantID: uuid.New(), Name: "Field Ops"},
		fixedTime, func() uuid.UUID { return id })
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if ch.ID != id || ch.Name != "Field Ops" {
		t.Fatalf("unexpected channel: %+v", ch)
	}
	// Asserted against literals, not against the Default* vars themselves —
	// comparing a constant to itself can never fail, and these values are a
	// contract with internal/ingest (defaultRecallSeconds) and the spec, not
	// free parameters.
	if ch.ReplyPolicy != "reply_to_sender" {
		t.Errorf("reply_policy = %q, want reply_to_sender", ch.ReplyPolicy)
	}
	if ch.RecallWindowSeconds != 120 {
		t.Errorf("recall window = %d, want 120 (matches ingest defaultRecallSeconds)", ch.RecallWindowSeconds)
	}
	if !ch.EchoBackEnabled {
		t.Error("echo_back should default on")
	}
	if ch.Mode != "structured" {
		t.Errorf("mode = %q, want structured", ch.Mode)
	}
	// The default command set must match what ingest falls back to, or creating
	// a channel would silently change how existing traffic parses.
	var pc struct {
		Commands []string `json:"commands"`
	}
	if err := json.Unmarshal(fs.createParams.ParserConfig, &pc); err != nil {
		t.Fatalf("default parser_config is not valid JSON: %v", err)
	}
	const wantCommands = "STATUS,NEEDS,DAMAGE,MISSING,RESOURCE,HERE,NOTE,SOS"
	if got := strings.Join(pc.Commands, ","); got != wantCommands {
		t.Errorf("default commands = %q, want %q (must match cmd/server's parserCfg)", got, wantCommands)
	}
}

// The default thresholds are a contract with the policy gate, so tie the two
// together rather than trusting two hand-copied literals to stay in step.
func TestDefaultThresholdsMatchPolicyGate(t *testing.T) {
	var got struct {
		High   float64 `json:"high"`
		Medium float64 `json:"medium"`
	}
	if err := json.Unmarshal(DefaultConfidenceThresholds, &got); err != nil {
		t.Fatalf("DefaultConfidenceThresholds is not valid JSON: %v", err)
	}
	if got.High != policy.DefaultThresholds.High || got.Medium != policy.DefaultThresholds.Medium {
		t.Errorf("channel defaults {high:%v medium:%v} do not match policy.DefaultThresholds {high:%v medium:%v}",
			got.High, got.Medium, policy.DefaultThresholds.High, policy.DefaultThresholds.Medium)
	}
}

func TestCreateRejectsBlankName(t *testing.T) {
	_, err := NewService(&fakeStore{}).Create(context.Background(),
		CreateInput{TenantID: uuid.New(), Name: "   "}, fixedTime, uuid.New)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
}

// A typo'd mode is the dangerous case: ingest treats anything that is not
// "passthrough" as the command grammar, so "passthru" would silently run the
// wrong pipeline over live traffic rather than erroring.
func TestCreateRejectsUnknownParserMode(t *testing.T) {
	_, err := NewService(&fakeStore{}).Create(context.Background(), CreateInput{
		TenantID: uuid.New(), Name: "Ops", ParserConfig: json.RawMessage(`{"mode":"passthru"}`),
	}, fixedTime, uuid.New)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("want ErrInvalid for bad mode, got %v", err)
	}
}

func TestCreateRejectsInvertedThresholds(t *testing.T) {
	_, err := NewService(&fakeStore{}).Create(context.Background(), CreateInput{
		TenantID: uuid.New(), Name: "Ops",
		ConfidenceThresholds: json.RawMessage(`{"high":0.4,"medium":0.9}`),
	}, fixedTime, uuid.New)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("want ErrInvalid for medium > high, got %v", err)
	}
}

func TestCreateRejectsBadReplyPolicy(t *testing.T) {
	_, err := NewService(&fakeStore{}).Create(context.Background(), CreateInput{
		TenantID: uuid.New(), Name: "Ops", ReplyPolicy: "shout",
	}, fixedTime, uuid.New)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
}

func TestCreateStoresWorkflowURL(t *testing.T) {
	fs := &fakeStore{}
	ch, err := NewService(fs).Create(context.Background(), CreateInput{
		TenantID: uuid.New(), Name: "Ops", WorkflowURL: "https://consumer.example/hook",
	}, fixedTime, uuid.New)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if ch.WorkflowURL != "https://consumer.example/hook" {
		t.Errorf("workflow_url = %q", ch.WorkflowURL)
	}
	if !fs.createParams.WorkflowUrl.Valid {
		t.Error("workflow_url should be stored as non-NULL")
	}
}

// Omitting workflow_url must store NULL, not "": GetInboundChannelForAccount
// and the diagnostics both treat empty and NULL as "nowhere to forward", and a
// stored empty string would make the column's meaning ambiguous.
func TestCreateWithoutWorkflowURLStoresNull(t *testing.T) {
	fs := &fakeStore{}
	if _, err := NewService(fs).Create(context.Background(),
		CreateInput{TenantID: uuid.New(), Name: "Ops"}, fixedTime, uuid.New); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if fs.createParams.WorkflowUrl.Valid {
		t.Error("absent workflow_url should be NULL")
	}
}

// The reason Update exists: pointing an existing channel at a consumer must not
// require restating parser_config, thresholds, or anything else.
func TestUpdateOnlyWorkflowURLLeavesOtherColumnsAlone(t *testing.T) {
	fs := &fakeStore{}
	url := "https://new.example/hook"
	if _, err := NewService(fs).Update(context.Background(), uuid.New(), uuid.New(),
		UpdateInput{WorkflowURL: &url}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	p := fs.updateParams
	if !p.WorkflowUrl.Valid || p.WorkflowUrl.String != url {
		t.Errorf("workflow_url not set: %+v", p.WorkflowUrl)
	}
	for name, valid := range map[string]bool{
		"name": p.Name.Valid, "reply_policy": p.ReplyPolicy.Valid,
		"echo_back_enabled": p.EchoBackEnabled.Valid, "recall_window_seconds": p.RecallWindowSeconds.Valid,
	} {
		if valid {
			t.Errorf("%s should have been left untouched", name)
		}
	}
	if p.ParserConfig != nil || p.ConfidenceThresholds != nil {
		t.Error("json columns should have been left untouched")
	}
}

func TestUpdateUnknownChannelIsNotFound(t *testing.T) {
	fs := &fakeStore{updateErr: pgx.ErrNoRows}
	name := "x"
	_, err := NewService(fs).Update(context.Background(), uuid.New(), uuid.New(), UpdateInput{Name: &name})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// --- create / update handlers ---

func TestHandlerCreate201(t *testing.T) {
	tenant := uuid.New()
	fs := &fakeSvc{ch: Channel{ID: uuid.New(), Name: "Field Ops"}}
	rec := do(t, fs, http.MethodPost, "/v1/channels", `{"name":"Field Ops"}`, tenant.String())
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", rec.Code, rec.Body)
	}
	if fs.called != "create" || fs.createIn.Name != "Field Ops" {
		t.Errorf("service not called correctly: %s %+v", fs.called, fs.createIn)
	}
	if fs.createIn.TenantID != tenant {
		t.Errorf("tenant not scoped: got %s want %s", fs.createIn.TenantID, tenant)
	}
}

func TestHandlerCreateRequiresTenant(t *testing.T) {
	rec := do(t, &fakeSvc{}, http.MethodPost, "/v1/channels", `{"name":"x"}`, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 without a tenant header", rec.Code)
	}
}

// A validation failure must say what was wrong. A bare "invalid" turns channel
// setup into guesswork, which is the friction this endpoint exists to remove.
func TestHandlerCreateInvalidReturnsDetail(t *testing.T) {
	fs := &fakeSvc{err: fmt.Errorf("%w: reply_policy must be reply_to_sender, broadcast or custom", ErrInvalid)}
	rec := do(t, fs, http.MethodPost, "/v1/channels", `{"name":"x","reply_policy":"shout"}`, uuid.New().String())
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var body struct {
		Error  string `json:"error"`
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	if !strings.Contains(body.Detail, "reply_policy") {
		t.Errorf("detail should name the offending field, got %q", body.Detail)
	}
}

func TestHandlerUpdate200(t *testing.T) {
	id := uuid.New()
	fs := &fakeSvc{ch: Channel{ID: id}}
	rec := do(t, fs, http.MethodPatch, "/v1/channels/"+id.String(),
		`{"workflow_url":"https://consumer.example/hook"}`, uuid.New().String())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body)
	}
	if fs.called != "update" {
		t.Fatalf("service not called: %s", fs.called)
	}
	if fs.updateIn.WorkflowURL == nil || *fs.updateIn.WorkflowURL != "https://consumer.example/hook" {
		t.Errorf("workflow_url not passed through: %+v", fs.updateIn.WorkflowURL)
	}
	// Everything the caller omitted must arrive as nil so the SQL leaves those
	// columns alone.
	if fs.updateIn.Name != nil || fs.updateIn.ReplyPolicy != nil || fs.updateIn.EchoBackEnabled != nil {
		t.Error("omitted fields should be nil, not zero values")
	}
}

func TestHandlerUpdateRejectsBadID(t *testing.T) {
	rec := do(t, &fakeSvc{}, http.MethodPatch, "/v1/channels/not-a-uuid", `{}`, uuid.New().String())
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandlerUpdateNotFound404(t *testing.T) {
	fs := &fakeSvc{err: ErrNotFound}
	rec := do(t, fs, http.MethodPatch, "/v1/channels/"+uuid.New().String(), `{"name":"x"}`, uuid.New().String())
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
