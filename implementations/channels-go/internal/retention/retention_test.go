package retention

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/queries/goqueries"
	"github.com/google/uuid"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
func fixedNow() time.Time         { return time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC) }

type fakeStore struct {
	older, forContact int64
	forEndpoint       int64
	endpoints         []string
	ops               []string
	cutoff            time.Time
	deletedContacts   int64
}

func (f *fakeStore) CountMessagesOlderThan(_ context.Context, p goqueries.CountMessagesOlderThanParams) (int64, error) {
	f.cutoff = p.ReceivedAt
	f.ops = append(f.ops, "count_old")
	return f.older, nil
}
func (f *fakeStore) ClearReplyLinksToPurgedMessages(context.Context, goqueries.ClearReplyLinksToPurgedMessagesParams) (int64, error) {
	f.ops = append(f.ops, "clear_links")
	return 0, nil
}
func (f *fakeStore) PurgeMessagesOlderThan(context.Context, goqueries.PurgeMessagesOlderThanParams) (int64, error) {
	f.ops = append(f.ops, "purge")
	return f.older, nil
}
func (f *fakeStore) CountMessagesForContact(context.Context, goqueries.CountMessagesForContactParams) (int64, error) {
	f.ops = append(f.ops, "count_contact")
	return f.forContact, nil
}
func (f *fakeStore) RedactMessagesForContact(context.Context, goqueries.RedactMessagesForContactParams) (int64, error) {
	f.ops = append(f.ops, "redact")
	return f.forContact, nil
}
func (f *fakeStore) DetachMessagesFromContact(context.Context, goqueries.DetachMessagesFromContactParams) (int64, error) {
	f.ops = append(f.ops, "detach")
	return f.forContact, nil
}
func (f *fakeStore) DeleteEndpointsForContact(context.Context, uuid.UUID) (int64, error) {
	f.ops = append(f.ops, "delete_endpoints")
	return 2, nil
}
func (f *fakeStore) CountMessagesForEndpoint(context.Context, goqueries.CountMessagesForEndpointParams) (int64, error) {
	f.ops = append(f.ops, "count_endpoint")
	return f.forEndpoint, nil
}
func (f *fakeStore) RedactMessagesForEndpoint(context.Context, goqueries.RedactMessagesForEndpointParams) (int64, error) {
	f.ops = append(f.ops, "redact_endpoint")
	return f.forEndpoint, nil
}
func (f *fakeStore) ListEndpointsForContact(context.Context, uuid.UUID) ([]string, error) {
	f.ops = append(f.ops, "list_endpoints")
	return f.endpoints, nil
}
func (f *fakeStore) DeleteContactRow(context.Context, goqueries.DeleteContactRowParams) (int64, error) {
	f.ops = append(f.ops, "delete_contact")
	if f.deletedContacts == 0 {
		return 1, nil
	}
	return f.deletedContacts, nil
}

// --- purge ---

func TestPurgeDryRunCountsAndDeletesNothing(t *testing.T) {
	fs := &fakeStore{older: 120}
	res, err := NewService(fs, fixedNow).Purge(context.Background(), uuid.New(), 30, true)
	if err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if res.MessagesMatched != 120 || res.MessagesDeleted != 0 {
		t.Errorf("dry run should count only: %+v", res)
	}
	if strings.Contains(strings.Join(fs.ops, ","), "purge") {
		t.Error("dry run must not delete")
	}
}

func TestPurgeCutoffIsCorrect(t *testing.T) {
	fs := &fakeStore{older: 1}
	if _, err := NewService(fs, fixedNow).Purge(context.Background(), uuid.New(), 30, true); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	want := fixedNow().UTC().AddDate(0, 0, -30)
	if !fs.cutoff.Equal(want) {
		t.Errorf("cutoff = %s, want %s", fs.cutoff, want)
	}
}

// Replies point at other messages via a self-referential FK, so the links must
// be cleared before the delete or the purge fails outright.
func TestPurgeClearsReplyLinksBeforeDeleting(t *testing.T) {
	fs := &fakeStore{older: 5}
	if _, err := NewService(fs, fixedNow).Purge(context.Background(), uuid.New(), 30, false); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	ops := strings.Join(fs.ops, ",")
	ci, pi := strings.Index(ops, "clear_links"), strings.Index(ops, "purge")
	if ci < 0 || pi < 0 || ci > pi {
		t.Errorf("clear_links must precede purge, got %s", ops)
	}
}

// A window of zero would wipe a live response's entire history.
func TestPurgeRefusesTooShortAWindow(t *testing.T) {
	for _, days := range []int{0, -1, MinRetentionDays - 1} {
		if _, err := NewService(&fakeStore{}, fixedNow).Purge(context.Background(), uuid.New(), days, false); err == nil {
			t.Errorf("older_than_days=%d should be refused", days)
		}
	}
}

func TestPurgeSkipsWorkWhenNothingMatches(t *testing.T) {
	fs := &fakeStore{older: 0}
	if _, err := NewService(fs, fixedNow).Purge(context.Background(), uuid.New(), 30, false); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if strings.Contains(strings.Join(fs.ops, ","), "purge") {
		t.Error("nothing matched; no delete should be issued")
	}
}

// --- erasure ---

// Redaction must happen while the contact link still exists. Detaching first
// would leave the personal data in place with nothing pointing at it — the
// worst outcome for an operation whose purpose is removing that data.
func TestEraseRedactsBeforeDetaching(t *testing.T) {
	fs := &fakeStore{forContact: 7}
	res, err := NewService(fs, fixedNow).Erase(context.Background(), uuid.New(), uuid.New(), false)
	if err != nil {
		t.Fatalf("Erase: %v", err)
	}
	ops := strings.Join(fs.ops, ",")
	ri, di := strings.Index(ops, "redact"), strings.Index(ops, "detach")
	if ri < 0 || di < 0 || ri > di {
		t.Fatalf("redact must precede detach, got %s", ops)
	}
	if res.MessagesRedacted != 7 || !res.ContactDeleted {
		t.Errorf("unexpected result: %+v", res)
	}
}

func TestEraseDryRunChangesNothing(t *testing.T) {
	fs := &fakeStore{forContact: 3}
	res, err := NewService(fs, fixedNow).Erase(context.Background(), uuid.New(), uuid.New(), true)
	if err != nil {
		t.Fatalf("Erase: %v", err)
	}
	if res.MessagesMatched != 3 || res.MessagesRedacted != 0 || res.ContactDeleted {
		t.Errorf("dry run should report only: %+v", res)
	}
	// The invariant is that a dry run performs no WRITES. Reads are expected —
	// it has to look up the contact's endpoints to report what it would touch.
	for _, op := range fs.ops {
		switch op {
		case "redact", "redact_endpoint", "detach", "delete_endpoints", "delete_contact", "purge", "clear_links":
			t.Errorf("dry run performed the write %q", op)
		}
	}
}

func TestEraseUnknownContactIsNotFound(t *testing.T) {
	fs := &fakeStore{forContact: 0, deletedContacts: -1}
	// -1 is coerced to 0 rows by the fake, standing in for "no such contact".
	fs.deletedContacts = 0
	fsNoRow := &noRowStore{fakeStore: fs}
	if _, err := NewService(fsNoRow, fixedNow).Erase(context.Background(), uuid.New(), uuid.New(), false); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

type noRowStore struct{ *fakeStore }

func (n *noRowStore) DeleteContactRow(context.Context, goqueries.DeleteContactRowParams) (int64, error) {
	return 0, nil
}

// --- handler ---

func post(t *testing.T, svc serviceAPI, path, body, tenant string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	NewHandler(svc, discardLogger()).RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if tenant != "" {
		req.Header.Set("X-Tenant-ID", tenant)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

type spySvc struct{ purgeDry, eraseDry, endpointDry bool }

func (s *spySvc) EraseEndpoint(_ context.Context, _ uuid.UUID, _ string, dry bool) (ErasureResult, error) {
	s.endpointDry = dry
	return ErasureResult{DryRun: dry}, nil
}

func (s *spySvc) Purge(_ context.Context, _ uuid.UUID, _ int, dry bool) (PurgeResult, error) {
	s.purgeDry = dry
	return PurgeResult{DryRun: dry}, nil
}
func (s *spySvc) Erase(_ context.Context, _, _ uuid.UUID, dry bool) (ErasureResult, error) {
	s.eraseDry = dry
	return ErasureResult{DryRun: dry}, nil
}

// Both operations are irreversible. Omitting dry_run must not delete anything.
func TestDryRunDefaultsToTrueOnBothRoutes(t *testing.T) {
	s := &spySvc{}
	post(t, s, "/v1/retention/purge", `{"older_than_days":30}`, uuid.New().String())
	if !s.purgeDry {
		t.Error("purge defaulted to a real delete")
	}
	post(t, s, "/v1/contacts/"+uuid.New().String()+"/erase", `{}`, uuid.New().String())
	if !s.eraseDry {
		t.Error("erase defaulted to a real erasure")
	}
}

func TestExplicitDryRunFalseIsHonoured(t *testing.T) {
	s := &spySvc{}
	post(t, s, "/v1/retention/purge", `{"older_than_days":30,"dry_run":false}`, uuid.New().String())
	if s.purgeDry {
		t.Error("dry_run:false was ignored")
	}
}

func TestPurgeInvalidWindowReturns400WithDetail(t *testing.T) {
	rec := post(t, NewService(&fakeStore{}, fixedNow), "/v1/retention/purge",
		`{"older_than_days":1,"dry_run":false}`, uuid.New().String())
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if d, _ := body["detail"].(string); !strings.Contains(d, "older_than_days") {
		t.Errorf("detail should name the field, got %v", body)
	}
}

func TestRoutesRequireTenant(t *testing.T) {
	for _, p := range []string{"/v1/retention/purge", "/v1/contacts/" + uuid.New().String() + "/erase"} {
		if rec := post(t, &spySvc{}, p, `{}`, ""); rec.Code != http.StatusBadRequest {
			t.Errorf("%s without tenant = %d, want 400", p, rec.Code)
		}
	}
}

// A contact erasure must follow the contact's endpoints, because messages carry
// sender_endpoint and not sender_contact_id. Without this an erasure would
// report success having redacted nothing.
func TestEraseFollowsContactEndpoints(t *testing.T) {
	fs := &fakeStore{forContact: 0, forEndpoint: 4, endpoints: []string{"+12425550042"}}
	res, err := NewService(fs, fixedNow).Erase(context.Background(), uuid.New(), uuid.New(), false)
	if err != nil {
		t.Fatalf("Erase: %v", err)
	}
	if res.MessagesRedacted != 4 {
		t.Errorf("redacted %d, want 4 via the endpoint", res.MessagesRedacted)
	}
	if len(res.Endpoints) != 1 || res.Endpoints[0] != "+12425550042" {
		t.Errorf("endpoints not reported: %v", res.Endpoints)
	}
	if !strings.Contains(strings.Join(fs.ops, ","), "redact_endpoint") {
		t.Errorf("endpoint redaction never ran: %v", fs.ops)
	}
}

func TestEraseEndpointRequiresAnEndpoint(t *testing.T) {
	if _, err := NewService(&fakeStore{}, fixedNow).EraseEndpoint(context.Background(), uuid.New(), "  ", false); err == nil {
		t.Fatal("blank endpoint should be refused")
	}
}

func TestEraseEndpointDryRunChangesNothing(t *testing.T) {
	fs := &fakeStore{forEndpoint: 9}
	res, err := NewService(fs, fixedNow).EraseEndpoint(context.Background(), uuid.New(), "+1242", true)
	if err != nil {
		t.Fatalf("EraseEndpoint: %v", err)
	}
	if res.MessagesMatched != 9 || res.MessagesRedacted != 0 {
		t.Errorf("dry run should report only: %+v", res)
	}
	if strings.Contains(strings.Join(fs.ops, ","), "redact_endpoint") {
		t.Error("dry run redacted")
	}
}

func TestEraseEndpointDefaultsToDryRunOverHTTP(t *testing.T) {
	s := &spySvc{}
	post(t, s, "/v1/retention/erase-endpoint", `{"endpoint":"+1242"}`, uuid.New().String())
	if !s.endpointDry {
		t.Error("endpoint erasure defaulted to a real redaction")
	}
}
