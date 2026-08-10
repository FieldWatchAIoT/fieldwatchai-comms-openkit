package outboundapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/outbound"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/queries/goqueries"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type fakeStore struct {
	row     goqueries.GetMessageForOutboundRow
	rowErr  error
	acct    goqueries.Account
	acctErr error
	out     goqueries.CreateOutboundMessageParams

	cep       goqueries.GetOutboundEndpointForContactRow
	cepErr    error
	gotCEP    goqueries.GetOutboundEndpointForContactParams
	chAcct    goqueries.GetOutboundAccountForChannelRow
	chAcctErr error
	gotChan   uuid.UUID
}

func (f *fakeStore) GetMessageForOutbound(_ context.Context, _ uuid.UUID) (goqueries.GetMessageForOutboundRow, error) {
	return f.row, f.rowErr
}
func (f *fakeStore) GetAccountByID(_ context.Context, _ uuid.UUID) (goqueries.Account, error) {
	return f.acct, f.acctErr
}
func (f *fakeStore) CreateOutboundMessage(_ context.Context, p goqueries.CreateOutboundMessageParams) (uuid.UUID, error) {
	f.out = p
	return p.ID, nil
}
func (f *fakeStore) GetOutboundEndpointForContact(_ context.Context, p goqueries.GetOutboundEndpointForContactParams) (goqueries.GetOutboundEndpointForContactRow, error) {
	f.gotCEP = p
	return f.cep, f.cepErr
}
func (f *fakeStore) GetOutboundAccountForChannel(_ context.Context, id uuid.UUID) (goqueries.GetOutboundAccountForChannelRow, error) {
	f.gotChan = id
	return f.chAcct, f.chAcctErr
}

type fakeEnc struct{}

func (fakeEnc) Encrypt(_ context.Context, p []byte) ([]byte, error) { return p, nil }
func (fakeEnc) Decrypt(_ context.Context, _ []byte) ([]byte, error) { return []byte("token"), nil }

type fakeAdapter struct {
	to, body, token string
	calls           int
}

func (a *fakeAdapter) Send(_ context.Context, acc outbound.Account, to, body string) (string, error) {
	a.to, a.body, a.token = to, body, acc.Token
	a.calls++
	return "pmid-9", nil
}

func newSvc(fs *fakeStore, ad *fakeAdapter) *Service {
	reg := outbound.NewRegistry()
	reg.Register("whatsapp", ad)
	return NewService(fs, fakeEnc{}, reg, func() time.Time { return time.Unix(0, 0).UTC() }, uuid.New)
}

func TestReplyToMessageSendsToOriginalSender(t *testing.T) {
	accID, cID := uuid.New(), uuid.New()
	inReplyTo := uuid.New()
	fs := &fakeStore{
		row: goqueries.GetMessageForOutboundRow{
			AccountID: &accID, TenantID: uuid.New(),
			SenderEndpoint: pgtype.Text{String: "+12428076373", Valid: true}, SenderContactID: &cID,
		},
		acct: goqueries.Account{ID: accID, Type: "whatsapp", PlatformIdentifier: "179557", CredentialsEncrypted: []byte("ct")},
	}
	ad := &fakeAdapter{}
	res, err := newSvc(fs, ad).ReplyToMessage(context.Background(), inReplyTo, "Your report is logged.")
	if err != nil {
		t.Fatalf("ReplyToMessage: %v", err)
	}
	if ad.calls != 1 || ad.to != "+12428076373" || ad.body != "Your report is logged." {
		t.Fatalf("adapter send wrong: calls=%d to=%q body=%q", ad.calls, ad.to, ad.body)
	}
	if ad.token != "token" {
		t.Errorf("token not decrypted/passed: %q", ad.token)
	}
	if res.DispatchedTo != "+12428076373" {
		t.Errorf("dispatched_to = %q", res.DispatchedTo)
	}
	if fs.out.InReplyToMessageID == nil || *fs.out.InReplyToMessageID != inReplyTo {
		t.Errorf("outbound not linked to in_reply_to")
	}
	if fs.out.PolicyAction.String != "sent" {
		t.Errorf("policy_action = %q, want sent", fs.out.PolicyAction.String)
	}
}

func TestReplyToMessageNotFound(t *testing.T) {
	fs := &fakeStore{rowErr: pgx.ErrNoRows}
	_, err := newSvc(fs, &fakeAdapter{}).ReplyToMessage(context.Background(), uuid.New(), "hi")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestReplyToMessageEmptyText(t *testing.T) {
	_, err := newSvc(&fakeStore{}, &fakeAdapter{}).ReplyToMessage(context.Background(), uuid.New(), "")
	if !errors.Is(err, ErrBadRequest) {
		t.Fatalf("err = %v, want ErrBadRequest", err)
	}
}

func TestReplyToMessageUnresolvableConversation(t *testing.T) {
	// message exists but has no account/sender to reply to.
	fs := &fakeStore{row: goqueries.GetMessageForOutboundRow{TenantID: uuid.New()}}
	_, err := newSvc(fs, &fakeAdapter{}).ReplyToMessage(context.Background(), uuid.New(), "hi")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// --- initiate: recipient.contact_id ---

func TestSendToContactResolvesEndpointAndAccount(t *testing.T) {
	accID, contactID, tenantID := uuid.New(), uuid.New(), uuid.New()
	fs := &fakeStore{
		cep: goqueries.GetOutboundEndpointForContactRow{
			Endpoint: "+12428076373", AccountID: accID, ChannelID: uuid.New(), TenantID: tenantID,
		},
		acct: goqueries.Account{ID: accID, Type: "whatsapp", PlatformIdentifier: "179557", CredentialsEncrypted: []byte("ct")},
	}
	ad := &fakeAdapter{}
	res, err := newSvc(fs, ad).SendToContact(context.Background(), contactID, nil, "Report your status.", "")
	if err != nil {
		t.Fatalf("SendToContact: %v", err)
	}
	if ad.calls != 1 || ad.to != "+12428076373" || ad.body != "Report your status." {
		t.Fatalf("adapter send wrong: calls=%d to=%q body=%q", ad.calls, ad.to, ad.body)
	}
	if res.DispatchedTo != "+12428076373" {
		t.Errorf("dispatched_to = %q", res.DispatchedTo)
	}
	// An initiated message has nothing to link to, but must still record who it
	// went to and stay inside the resolved tenant.
	if fs.out.InReplyToMessageID != nil {
		t.Errorf("initiated message must not set in_reply_to, got %v", *fs.out.InReplyToMessageID)
	}
	if fs.out.RecipientContactID == nil || *fs.out.RecipientContactID != contactID {
		t.Errorf("recipient_contact_id not recorded")
	}
	if fs.out.TenantID != tenantID {
		t.Errorf("tenant = %v, want %v (must come from the resolved contact, not the caller)", fs.out.TenantID, tenantID)
	}
	if fs.out.AccountID == nil || *fs.out.AccountID != accID {
		t.Errorf("account_id not recorded")
	}
	if fs.out.PolicyAction.String != "sent" {
		t.Errorf("policy_action = %q, want sent", fs.out.PolicyAction.String)
	}
}

func TestSendToContactPassesChannelFilter(t *testing.T) {
	accID, chID := uuid.New(), uuid.New()
	fs := &fakeStore{
		cep:  goqueries.GetOutboundEndpointForContactRow{Endpoint: "+1242", AccountID: accID, TenantID: uuid.New()},
		acct: goqueries.Account{ID: accID, Type: "whatsapp", CredentialsEncrypted: []byte("ct")},
	}
	if _, err := newSvc(fs, &fakeAdapter{}).SendToContact(context.Background(), uuid.New(), &chID, "hi", ""); err != nil {
		t.Fatalf("SendToContact: %v", err)
	}
	if fs.gotCEP.ChannelID == nil || *fs.gotCEP.ChannelID != chID {
		t.Errorf("channel filter not passed to the query: %v", fs.gotCEP.ChannelID)
	}
}

func TestSendToContactNoOutboundEndpoint(t *testing.T) {
	fs := &fakeStore{cepErr: pgx.ErrNoRows}
	_, err := newSvc(fs, &fakeAdapter{}).SendToContact(context.Background(), uuid.New(), nil, "hi", "")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestSendToContactEmptyText(t *testing.T) {
	_, err := newSvc(&fakeStore{}, &fakeAdapter{}).SendToContact(context.Background(), uuid.New(), nil, "", "")
	if !errors.Is(err, ErrBadRequest) {
		t.Fatalf("err = %v, want ErrBadRequest", err)
	}
}

// --- initiate: recipient.endpoint ---

func TestSendToEndpointUsesChannelOutboundAccount(t *testing.T) {
	accID, chID, tenantID := uuid.New(), uuid.New(), uuid.New()
	fs := &fakeStore{
		chAcct: goqueries.GetOutboundAccountForChannelRow{AccountID: accID, TenantID: tenantID},
		acct:   goqueries.Account{ID: accID, Type: "whatsapp", PlatformIdentifier: "179557", CredentialsEncrypted: []byte("ct")},
	}
	ad := &fakeAdapter{}
	res, err := newSvc(fs, ad).SendToEndpoint(context.Background(), chID, "+12428076373", "Report your status.", "")
	if err != nil {
		t.Fatalf("SendToEndpoint: %v", err)
	}
	if fs.gotChan != chID {
		t.Errorf("channel not passed through: %v", fs.gotChan)
	}
	if ad.calls != 1 || ad.to != "+12428076373" {
		t.Fatalf("adapter send wrong: calls=%d to=%q", ad.calls, ad.to)
	}
	if res.DispatchedTo != "+12428076373" {
		t.Errorf("dispatched_to = %q", res.DispatchedTo)
	}
	// No contact row exists for a raw endpoint — the message must still persist.
	if fs.out.RecipientContactID != nil {
		t.Errorf("recipient_contact_id should be nil for a raw endpoint")
	}
	if fs.out.TenantID != tenantID {
		t.Errorf("tenant = %v, want %v", fs.out.TenantID, tenantID)
	}
}

func TestSendToEndpointChannelHasNoOutboundAccount(t *testing.T) {
	fs := &fakeStore{chAcctErr: pgx.ErrNoRows}
	_, err := newSvc(fs, &fakeAdapter{}).SendToEndpoint(context.Background(), uuid.New(), "+1242", "hi", "")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestSendToEndpointEmptyEndpoint(t *testing.T) {
	_, err := newSvc(&fakeStore{}, &fakeAdapter{}).SendToEndpoint(context.Background(), uuid.New(), "", "hi", "")
	if !errors.Is(err, ErrBadRequest) {
		t.Fatalf("err = %v, want ErrBadRequest", err)
	}
}

// --- handler ---

type fakeSvc struct {
	res Result
	err error
	got uuid.UUID

	gotContact  uuid.UUID
	gotChannel  *uuid.UUID
	gotEndpoint string
	gotSubject  string
	calledKind  string
}

func (f *fakeSvc) ReplyToMessage(_ context.Context, inReplyTo uuid.UUID, _ string) (Result, error) {
	f.got = inReplyTo
	f.calledKind = "reply"
	return f.res, f.err
}

func (f *fakeSvc) SendToContact(_ context.Context, contactID uuid.UUID, channelID *uuid.UUID, _, subject string) (Result, error) {
	f.gotContact, f.gotChannel, f.gotSubject = contactID, channelID, subject
	f.calledKind = "contact"
	return f.res, f.err
}

func (f *fakeSvc) SendToEndpoint(_ context.Context, channelID uuid.UUID, endpoint, _, subject string) (Result, error) {
	f.gotChannel, f.gotEndpoint, f.gotSubject = &channelID, endpoint, subject
	f.calledKind = "endpoint"
	return f.res, f.err
}

func postOutbound(t *testing.T, svc serviceAPI, body string) *httptest.ResponseRecorder {
	t.Helper()
	h := NewHandler(svc, discardLogger())
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/outbound", strings.NewReader(body)))
	return rec
}

func TestHandlerReply202(t *testing.T) {
	id := uuid.New()
	fs := &fakeSvc{res: Result{MessageID: uuid.New(), DispatchedTo: "+12428076373"}}
	h := NewHandler(fs, discardLogger())
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	body := `{"in_reply_to_message_id":"` + id.String() + `","text":"ok"}`
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/outbound", strings.NewReader(body)))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	if fs.got != id {
		t.Errorf("service got %v, want %v", fs.got, id)
	}
}

func TestHandlerBadUUID(t *testing.T) {
	h := NewHandler(&fakeSvc{}, discardLogger())
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/outbound", strings.NewReader(`{"in_reply_to_message_id":"nope","text":"x"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandlerNotFound(t *testing.T) {
	h := NewHandler(&fakeSvc{err: ErrNotFound}, discardLogger())
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/outbound", strings.NewReader(`{"in_reply_to_message_id":"`+uuid.New().String()+`","text":"x"}`)))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// --- handler: the recipient variant ---

func TestHandlerRecipientContactID202(t *testing.T) {
	contactID, chID := uuid.New(), uuid.New()
	fs := &fakeSvc{res: Result{MessageID: uuid.New(), DispatchedTo: "+12428076373"}}
	body := `{"recipient":{"contact_id":"` + contactID.String() + `"},"channel_id":"` + chID.String() + `","text":"Report your status.","subject":"Status check"}`

	rec := postOutbound(t, fs, body)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	if fs.calledKind != "contact" {
		t.Fatalf("routed to %q, want contact", fs.calledKind)
	}
	if fs.gotContact != contactID {
		t.Errorf("contact = %v, want %v", fs.gotContact, contactID)
	}
	if fs.gotChannel == nil || *fs.gotChannel != chID {
		t.Errorf("channel filter not forwarded")
	}
	if fs.gotSubject != "Status check" {
		t.Errorf("subject = %q", fs.gotSubject)
	}
}

func TestHandlerRecipientContactIDWithoutChannel(t *testing.T) {
	fs := &fakeSvc{res: Result{MessageID: uuid.New(), DispatchedTo: "+1242"}}
	rec := postOutbound(t, fs, `{"recipient":{"contact_id":"`+uuid.New().String()+`"},"text":"hi"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (channel_id is optional for contact_id); body=%s", rec.Code, rec.Body.String())
	}
	if fs.gotChannel != nil {
		t.Errorf("channel should be nil when omitted, got %v", *fs.gotChannel)
	}
}

func TestHandlerRecipientEndpoint202(t *testing.T) {
	chID := uuid.New()
	fs := &fakeSvc{res: Result{MessageID: uuid.New(), DispatchedTo: "+12428076373"}}
	body := `{"recipient":{"endpoint":"+12428076373"},"channel_id":"` + chID.String() + `","text":"hi"}`

	rec := postOutbound(t, fs, body)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	if fs.calledKind != "endpoint" {
		t.Fatalf("routed to %q, want endpoint", fs.calledKind)
	}
	if fs.gotEndpoint != "+12428076373" {
		t.Errorf("endpoint = %q", fs.gotEndpoint)
	}
}

// A raw endpoint carries no account, so without channel_id there is nothing to
// say which platform or sending identity to use. Fail loudly rather than guess.
func TestHandlerRecipientEndpointRequiresChannelID(t *testing.T) {
	fs := &fakeSvc{}
	rec := postOutbound(t, fs, `{"recipient":{"endpoint":"+12428076373"},"text":"hi"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "channel_id_required") {
		t.Errorf("body = %s, want channel_id_required", rec.Body.String())
	}
	if fs.calledKind != "" {
		t.Errorf("service must not be called, got %q", fs.calledKind)
	}
}

func TestHandlerRejectsAmbiguousTargets(t *testing.T) {
	cases := []struct {
		name, body, wantErr string
	}{
		{
			"neither", `{"text":"hi"}`, "invalid_target",
		},
		{
			"both reply and recipient",
			`{"in_reply_to_message_id":"` + uuid.New().String() + `","recipient":{"contact_id":"` + uuid.New().String() + `"},"text":"hi"}`,
			"invalid_target",
		},
		{
			"both contact_id and endpoint",
			`{"recipient":{"contact_id":"` + uuid.New().String() + `","endpoint":"+1242"},"channel_id":"` + uuid.New().String() + `","text":"hi"}`,
			"invalid_recipient",
		},
		{
			"empty recipient object", `{"recipient":{},"text":"hi"}`, "invalid_target",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := &fakeSvc{}
			rec := postOutbound(t, fs, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantErr) {
				t.Errorf("body = %s, want %s", rec.Body.String(), tc.wantErr)
			}
			if fs.calledKind != "" {
				t.Errorf("service must not be called, got %q", fs.calledKind)
			}
		})
	}
}

func TestHandlerRecipientBadUUIDs(t *testing.T) {
	for _, tc := range []struct{ name, body, wantErr string }{
		{"bad contact id", `{"recipient":{"contact_id":"nope"},"text":"hi"}`, "invalid_contact_id"},
		{"bad channel id", `{"recipient":{"endpoint":"+1242"},"channel_id":"nope","text":"hi"}`, "invalid_channel_id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := postOutbound(t, &fakeSvc{}, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), tc.wantErr) {
				t.Errorf("body = %s, want %s", rec.Body.String(), tc.wantErr)
			}
		})
	}
}
