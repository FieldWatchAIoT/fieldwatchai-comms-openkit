package channels

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
}

func (f *fakeSvc) List(_ context.Context, _ uuid.UUID) ([]Channel, error) { return f.chans, f.err }
func (f *fakeSvc) Get(_ context.Context, _, _ uuid.UUID) (Channel, error) { return f.ch, f.err }
func (f *fakeSvc) ListLinks(_ context.Context, _, _ uuid.UUID) ([]Link, error) {
	return f.links, f.err
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
