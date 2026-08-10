package replay

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
	"time"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/queries/goqueries"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/workflow"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type fakeStore struct {
	rows     []goqueries.ListUnfiredForwardsRow
	listArg  goqueries.ListUnfiredForwardsParams
	listErr  error
	fired    []uuid.UUID
	firedErr error
}

func (f *fakeStore) ListUnfiredForwards(_ context.Context, p goqueries.ListUnfiredForwardsParams) ([]goqueries.ListUnfiredForwardsRow, error) {
	f.listArg = p
	return f.rows, f.listErr
}
func (f *fakeStore) MarkWorkflowFired(_ context.Context, id uuid.UUID) error {
	if f.firedErr != nil {
		return f.firedErr
	}
	f.fired = append(f.fired, id)
	return nil
}

type fakeFwd struct {
	urls  []string
	sent  []workflow.Forward
	errAt map[int]error
	n     int
}

func (f *fakeFwd) Forward(_ context.Context, url string, fw workflow.Forward) error {
	i := f.n
	f.n++
	if err, ok := f.errAt[i]; ok {
		return err
	}
	f.urls = append(f.urls, url)
	f.sent = append(f.sent, fw)
	return nil
}

func row(id uuid.UUID, acctType string, received time.Time) goqueries.ListUnfiredForwardsRow {
	acct, ch := uuid.New(), uuid.New()
	return goqueries.ListUnfiredForwardsRow{
		ID: id, TenantID: uuid.New(), AccountID: &acct, ChannelID: &ch,
		SenderEndpoint: pgtype.Text{String: "+12897792824", Valid: true},
		BodyText:       "42 STATUS full",
		Parsed:         []byte(`{"command":"STATUS","payload":"full","matched_contact_id":null}`),
		ReceivedAt:     received,
		AccountType:    acctType,
		WorkflowUrl:    pgtype.Text{String: "https://consumer.example/inbound", Valid: true},
	}
}

func TestRunReplaysAndMarksFired(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	base := time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)
	fs := &fakeStore{rows: []goqueries.ListUnfiredForwardsRow{
		row(a, "sms-twilio", base),
		row(b, "whatsapp-twilio", base.Add(time.Minute)),
	}}
	ff := &fakeFwd{}

	res, err := NewService(fs, ff, discardLogger()).Run(context.Background(), Request{TenantID: uuid.New()})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Candidates != 2 || res.Replayed != 2 || res.Failed != 0 {
		t.Fatalf("result = %+v", res)
	}
	if len(fs.fired) != 2 || fs.fired[0] != a || fs.fired[1] != b {
		t.Errorf("fired = %v, want [%v %v] in order", fs.fired, a, b)
	}
	// The rebuilt payload must carry the transport, same as a live forward.
	if ff.sent[0].Platform != "sms" || ff.sent[0].AccountType != "sms-twilio" {
		t.Errorf("platform/account_type wrong on replay: %+v", ff.sent[0])
	}
	if ff.sent[1].Platform != "whatsapp" {
		t.Errorf("platform = %q, want whatsapp", ff.sent[1].Platform)
	}
	if ff.sent[0].MessageID != a.String() {
		t.Errorf("message_id = %q, want %q — consumers dedupe on this", ff.sent[0].MessageID, a)
	}
	if ff.sent[0].Command != "STATUS" || ff.sent[0].Payload != "full" || ff.sent[0].Text != "42 STATUS full" {
		t.Errorf("parsed fields not rebuilt: %+v", ff.sent[0])
	}
	if ff.sent[0].SenderEndpoint != "+12897792824" {
		t.Errorf("sender_endpoint = %q", ff.sent[0].SenderEndpoint)
	}
}

// A null matched_contact_id must not become the literal string "null" or a zero
// UUID — the consumer keys off contact_id being absent.
func TestBuildForwardOmitsNullContactID(t *testing.T) {
	f := buildForward(row(uuid.New(), "telegram", time.Now()))
	if f.ContactID != "" {
		t.Errorf("contact_id = %q, want empty", f.ContactID)
	}
	withContact := row(uuid.New(), "telegram", time.Now())
	cid := uuid.New()
	withContact.Parsed = []byte(`{"command":"X","matched_contact_id":"` + cid.String() + `"}`)
	if got := buildForward(withContact).ContactID; got != cid.String() {
		t.Errorf("contact_id = %q, want %q", got, cid)
	}
}

// One consumer failure must not abort the batch — the point is to deliver
// whatever can be delivered — but it must be reported and must not mark fired.
func TestRunContinuesPastFailureAndReportsIt(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	now := time.Now()
	fs := &fakeStore{rows: []goqueries.ListUnfiredForwardsRow{
		row(a, "telegram", now), row(b, "telegram", now), row(c, "telegram", now),
	}}
	ff := &fakeFwd{errAt: map[int]error{1: errors.New("consumer down")}}

	res, err := NewService(fs, ff, discardLogger()).Run(context.Background(), Request{TenantID: uuid.New()})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Replayed != 2 || res.Failed != 1 {
		t.Fatalf("result = %+v, want 2 replayed / 1 failed", res)
	}
	if len(res.Failures) != 1 || res.Failures[0].MessageID != b {
		t.Errorf("failure not reported for %v: %+v", b, res.Failures)
	}
	for _, id := range fs.fired {
		if id == b {
			t.Error("failed message must not be marked fired")
		}
	}
}

// Delivered-but-unmarked must count as a failure, not a success: the message
// will be sent again, and the operator should know the batch isn't clean.
func TestRunReportsMarkFailureSoResendIsExpected(t *testing.T) {
	fs := &fakeStore{rows: []goqueries.ListUnfiredForwardsRow{row(uuid.New(), "telegram", time.Now())},
		firedErr: errors.New("db down")}
	res, err := NewService(fs, &fakeFwd{}, discardLogger()).Run(context.Background(), Request{TenantID: uuid.New()})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Replayed != 0 || res.Failed != 1 {
		t.Fatalf("result = %+v, want 0 replayed / 1 failed", res)
	}
	if !strings.Contains(res.Failures[0].Error, "will resend") {
		t.Errorf("failure should flag the resend: %q", res.Failures[0].Error)
	}
}

func TestRunDryRunSendsNothing(t *testing.T) {
	fs := &fakeStore{rows: []goqueries.ListUnfiredForwardsRow{row(uuid.New(), "telegram", time.Now())}}
	ff := &fakeFwd{}
	res, err := NewService(fs, ff, discardLogger()).Run(context.Background(), Request{TenantID: uuid.New(), DryRun: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Candidates != 1 || res.Replayed != 0 || ff.n != 0 || len(fs.fired) != 0 {
		t.Fatalf("dry run sent something: res=%+v forwards=%d fired=%d", res, ff.n, len(fs.fired))
	}
}

func TestRunClampsLimit(t *testing.T) {
	for _, tc := range []struct{ in, want int32 }{{0, defaultLimit}, {-5, defaultLimit}, {50, 50}, {99999, maxLimit}} {
		fs := &fakeStore{}
		if _, err := NewService(fs, &fakeFwd{}, discardLogger()).Run(context.Background(),
			Request{TenantID: uuid.New(), Limit: tc.in}); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if fs.listArg.Limit != tc.want {
			t.Errorf("limit %d -> %d, want %d", tc.in, fs.listArg.Limit, tc.want)
		}
	}
}

func TestRunRequiresTenant(t *testing.T) {
	_, err := NewService(&fakeStore{}, &fakeFwd{}, discardLogger()).Run(context.Background(), Request{})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

// --- handler ---

type fakeSvc struct {
	got Request
	res Result
	err error
	n   int
}

func (f *fakeSvc) Run(_ context.Context, req Request) (Result, error) {
	f.got, f.n = req, f.n+1
	return f.res, f.err
}

func post(t *testing.T, svc serviceAPI, body, tenant string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	NewHandler(svc, discardLogger()).RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodPost, "/v1/workflows/replay", strings.NewReader(body))
	if tenant != "" {
		req.Header.Set("X-Tenant-ID", tenant)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestHandlerThreadsRequestThrough(t *testing.T) {
	tenant, ch := uuid.New(), uuid.New()
	fs := &fakeSvc{res: Result{Candidates: 3, Replayed: 3}}
	body := `{"channel_id":"` + ch.String() + `","since":"2026-08-01T00:00:00Z","limit":25,"dry_run":true}`

	rec := post(t, fs, body, tenant.String())

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if fs.got.TenantID != tenant || fs.got.ChannelID == nil || *fs.got.ChannelID != ch {
		t.Errorf("ids wrong: %+v", fs.got)
	}
	if fs.got.Limit != 25 || !fs.got.DryRun || fs.got.Since.IsZero() {
		t.Errorf("options wrong: %+v", fs.got)
	}
	var res Result
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Replayed != 3 {
		t.Errorf("body = %s", rec.Body.String())
	}
}

// No body means "replay everything outstanding" — a valid operator request.
func TestHandlerAcceptsEmptyBody(t *testing.T) {
	fs := &fakeSvc{}
	rec := post(t, fs, "", uuid.New().String())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if fs.n != 1 {
		t.Error("service not called")
	}
}

func TestHandlerBadInput(t *testing.T) {
	for _, tc := range []struct{ name, body, tenant, wantErr string }{
		{"no tenant", `{}`, "", "missing_or_invalid_tenant"},
		{"bad channel", `{"channel_id":"nope"}`, uuid.New().String(), "invalid_channel_id"},
		{"bad since", `{"since":"yesterday"}`, uuid.New().String(), "invalid_since"},
		{"bad json", `{`, uuid.New().String(), "invalid_json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := &fakeSvc{}
			rec := post(t, fs, tc.body, tc.tenant)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), tc.wantErr) {
				t.Errorf("body = %s, want %s", rec.Body.String(), tc.wantErr)
			}
			if fs.n != 0 {
				t.Error("service must not be called")
			}
		})
	}
}

// A replayed forward must carry location identically to a live one. Dropping it
// silently is the failure this test exists to prevent: a consumer's "show on
// map" action would just not appear, and an operator cannot distinguish "no
// location sent" from "location lost in transit".
func TestBuildForwardRebuildsLocation(t *testing.T) {
	r := row(uuid.New(), "whatsapp", time.Now())
	r.BodyLocation = `{"type":"Point","coordinates":[-77.3963,25.0343]}`

	var got struct {
		Lat float64 `json:"lat"`
		Lng float64 `json:"lng"`
	}
	f := buildForward(r)
	if f.Location == nil {
		t.Fatal("location dropped on replay")
	}
	if err := json.Unmarshal(f.Location, &got); err != nil {
		t.Fatalf("location not canonical {lat,lng}: %s", f.Location)
	}
	// GeoJSON is [lng, lat]; canonical is {lat, lng}. Swapping them moves the
	// sender from the Bahamas to Somalia without any error.
	if got.Lat != 25.0343 || got.Lng != -77.3963 {
		t.Errorf("lat/lng transposed: got %+v", got)
	}
}

func TestBuildForwardOmitsAbsentOrBadLocation(t *testing.T) {
	for _, tc := range []struct{ name, geo string }{
		{"absent", ""},
		{"malformed", "not json"},
		{"empty coordinates", `{"type":"Point","coordinates":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := row(uuid.New(), "whatsapp", time.Now())
			r.BodyLocation = tc.geo
			if loc := buildForward(r).Location; loc != nil {
				// 0,0 is a real place in the Atlantic; never send it by accident.
				t.Errorf("location should be omitted, got %s", loc)
			}
		})
	}
}

// Two concurrent runs would select the same unfired rows and deliver each one
// twice. A consumer idempotent on message_id absorbs that silently, so the
// only symptom is a count that doesn't match — which is exactly why the hub
// must not emit the duplicates in the first place.
func TestRunSingleFlightsPerTenant(t *testing.T) {
	tenant := uuid.New()
	release := make(chan struct{})
	entered := make(chan struct{})

	fs := &fakeStore{rows: []goqueries.ListUnfiredForwardsRow{row(uuid.New(), "telegram", time.Now())}}
	blocking := &blockingFwd{entered: entered, release: release}
	svc := NewService(fs, blocking, discardLogger())

	go func() {
		_, _ = svc.Run(context.Background(), Request{TenantID: tenant})
	}()
	<-entered // first run is mid-delivery, holding the slot

	if _, err := svc.Run(context.Background(), Request{TenantID: tenant}); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second concurrent run err = %v, want ErrAlreadyRunning", err)
	}

	// A different tenant is unaffected — the guard is per tenant, not global.
	other := NewService(&fakeStore{}, &fakeFwd{}, discardLogger())
	if _, err := other.Run(context.Background(), Request{TenantID: uuid.New()}); err != nil {
		t.Errorf("unrelated tenant blocked: %v", err)
	}

	close(release)
}

// A dry run sends nothing, so it must be able to inspect a run in progress.
func TestDryRunNotBlockedByRunningReplay(t *testing.T) {
	tenant := uuid.New()
	release := make(chan struct{})
	entered := make(chan struct{})
	fs := &fakeStore{rows: []goqueries.ListUnfiredForwardsRow{row(uuid.New(), "telegram", time.Now())}}
	svc := NewService(fs, &blockingFwd{entered: entered, release: release}, discardLogger())

	go func() { _, _ = svc.Run(context.Background(), Request{TenantID: tenant}) }()
	<-entered

	if _, err := svc.Run(context.Background(), Request{TenantID: tenant, DryRun: true}); err != nil {
		t.Errorf("dry run blocked by in-flight replay: %v", err)
	}
	close(release)
}

// The slot must be freed once a run finishes, including when it fails.
func TestSlotReleasedAfterRun(t *testing.T) {
	tenant := uuid.New()
	fs := &fakeStore{listErr: errors.New("db down")}
	svc := NewService(fs, &fakeFwd{}, discardLogger())

	if _, err := svc.Run(context.Background(), Request{TenantID: tenant}); err == nil {
		t.Fatal("expected the seeded error")
	}
	fs.listErr = nil
	if _, err := svc.Run(context.Background(), Request{TenantID: tenant}); err != nil {
		t.Fatalf("slot not released after a failed run: %v", err)
	}
}

type blockingFwd struct {
	entered chan struct{}
	release chan struct{}
	once    bool
}

func (b *blockingFwd) Forward(_ context.Context, _ string, _ workflow.Forward) error {
	if !b.once {
		b.once = true
		close(b.entered)
	}
	<-b.release
	return nil
}
