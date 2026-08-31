package export

import (
	"bytes"
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
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type fakeStore struct {
	msgs     []goqueries.ExportMessagesPageRow
	contacts []goqueries.Contact
	eps      []goqueries.ExportEndpointsForContactsRow
	calls    []goqueries.ExportMessagesPageParams
	err      error
}

// Pages from the in-memory slice using the same keyset the SQL does, so the
// cursor logic is genuinely exercised rather than stubbed away.
func (f *fakeStore) ExportMessagesPage(_ context.Context, p goqueries.ExportMessagesPageParams) ([]goqueries.ExportMessagesPageRow, error) {
	f.calls = append(f.calls, p)
	if f.err != nil {
		return nil, f.err
	}
	out := []goqueries.ExportMessagesPageRow{}
	for _, m := range f.msgs {
		after := m.ReceivedAt.After(p.AfterReceivedAt) ||
			(m.ReceivedAt.Equal(p.AfterReceivedAt) && m.ID.String() > p.AfterID.String())
		if after && int32(len(out)) < p.Limit {
			out = append(out, m)
		}
	}
	return out, nil
}
func (f *fakeStore) ExportContactsPage(_ context.Context, p goqueries.ExportContactsPageParams) ([]goqueries.Contact, error) {
	out := []goqueries.Contact{}
	for _, c := range f.contacts {
		if c.CreatedAt.After(p.AfterCreatedAt) && int32(len(out)) < p.Limit {
			out = append(out, c)
		}
	}
	return out, nil
}
func (f *fakeStore) ExportEndpointsForContacts(_ context.Context, _ []uuid.UUID) ([]goqueries.ExportEndpointsForContactsRow, error) {
	return f.eps, nil
}

func msg(i int, at time.Time) goqueries.ExportMessagesPageRow {
	return goqueries.ExportMessagesPageRow{
		ID: uuid.New(), TenantID: uuid.New(), Direction: "inbound",
		BodyText:   "message " + string(rune('a'+i)),
		ReceivedAt: at, PlatformMessageID: "pm", RawPayload: []byte(`{"secret":"envelope"}`),
		Parsed: []byte(`{"command":"STATUS"}`),
	}
}

func lines(t *testing.T, b *bytes.Buffer) []map[string]any {
	t.Helper()
	out := []map[string]any{}
	for _, l := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("line is not valid JSON: %q (%v)", l, err)
		}
		out = append(out, m)
	}
	return out
}

// Every line must independently parse — that is the whole contract of JSON
// Lines, and what lets a reader stream a file larger than memory.
func TestMessagesEmitsOneValidJSONObjectPerLine(t *testing.T) {
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	fs := &fakeStore{msgs: []goqueries.ExportMessagesPageRow{
		msg(0, base), msg(1, base.Add(time.Minute)), msg(2, base.Add(2*time.Minute)),
	}}
	var buf bytes.Buffer
	n, err := NewService(fs).Messages(context.Background(), &buf, uuid.New(), Options{})
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	got := lines(t, &buf)
	if n != 3 || len(got) != 3 {
		t.Fatalf("wrote %d records, %d lines", n, len(got))
	}
	if got[0]["body_text"] != "message a" || got[2]["body_text"] != "message c" {
		t.Errorf("wrong order or content: %v", got)
	}
}

// raw_payload is the verbatim provider envelope. It must stay out unless asked
// for: it is the bulk of an export and the most sensitive part of it.
func TestRawPayloadExcludedUnlessRequested(t *testing.T) {
	fs := &fakeStore{msgs: []goqueries.ExportMessagesPageRow{msg(0, time.Now())}}

	var off bytes.Buffer
	if _, err := NewService(fs).Messages(context.Background(), &off, uuid.New(), Options{}); err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if strings.Contains(off.String(), "envelope") {
		t.Error("raw_payload leaked into a default export")
	}

	var on bytes.Buffer
	if _, err := NewService(fs).Messages(context.Background(), &on, uuid.New(), Options{IncludeRaw: true}); err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if !strings.Contains(on.String(), "envelope") {
		t.Error("include_raw did not include raw_payload")
	}
}

// The point of the keyset cursor: it must advance, or a large export loops on
// the first page forever.
func TestCursorAdvancesAcrossPages(t *testing.T) {
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	msgs := make([]goqueries.ExportMessagesPageRow, 0, 1200)
	for i := 0; i < 1200; i++ {
		msgs = append(msgs, msg(0, base.Add(time.Duration(i)*time.Second)))
	}
	fs := &fakeStore{msgs: msgs}
	var buf bytes.Buffer
	n, err := NewService(fs).Messages(context.Background(), &buf, uuid.New(), Options{})
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if n != 1200 {
		t.Fatalf("exported %d of 1200 — the cursor did not cover the set", n)
	}
	if len(fs.calls) < 3 {
		t.Errorf("expected paging, got %d queries", len(fs.calls))
	}
	if fs.calls[1].AfterReceivedAt.Equal(fs.calls[0].AfterReceivedAt) {
		t.Error("cursor did not advance between pages — a real export would loop forever")
	}
}

func TestContactsInlineTheirEndpoints(t *testing.T) {
	cid := uuid.New()
	fs := &fakeStore{
		contacts: []goqueries.Contact{{
			ID: cid, ShortID: "42", DisplayName: "Marsh Harbour Shelter",
			Status: "active", CreatedAt: time.Now(), Metadata: []byte(`{}`),
			AoiID: pgtype.Text{String: "abaco", Valid: true},
		}},
		eps: []goqueries.ExportEndpointsForContactsRow{{
			ID: uuid.New(), ContactID: cid, Endpoint: "+12425550042",
			Priority: 100, Capabilities: []string{"inbound"},
		}},
	}
	var buf bytes.Buffer
	if _, err := NewService(fs).Contacts(context.Background(), &buf, uuid.New()); err != nil {
		t.Fatalf("Contacts: %v", err)
	}
	got := lines(t, &buf)
	eps, _ := got[0]["endpoints"].([]any)
	if len(eps) != 1 {
		t.Fatalf("endpoints not inlined: %v", got[0])
	}
	if got[0]["aoi_id"] != "abaco" {
		t.Errorf("aoi_id not mapped: %v", got[0]["aoi_id"])
	}
}

// --- handler ---

func serve(t *testing.T, svc serviceAPI, path, tenant string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	NewHandler(svc, discardLogger()).RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if tenant != "" {
		req.Header.Set("X-Tenant-ID", tenant)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestHandlerServesNDJSONAsAnAttachment(t *testing.T) {
	fs := &fakeStore{msgs: []goqueries.ExportMessagesPageRow{msg(0, time.Now())}}
	rec := serve(t, NewService(fs), "/v1/export/messages", uuid.New().String())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/x-ndjson") {
		t.Errorf("content-type = %q, want ndjson", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, ".jsonl") {
		t.Errorf("content-disposition = %q, want a .jsonl filename", cd)
	}
}

func TestHandlerRequiresTenant(t *testing.T) {
	if rec := serve(t, NewService(&fakeStore{}), "/v1/export/messages", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// A failure partway through cannot become a 500 — the status is already sent.
// It must be visible in the file instead, or the caller keeps a truncated
// export believing it is complete.
func TestPartialFailureIsMarkedInTheStream(t *testing.T) {
	fs := &fakeStore{err: errors.New("connection lost")}
	rec := serve(t, NewService(fs), "/v1/export/messages", uuid.New().String())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d — the header is committed before the first row", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "export_error") {
		t.Errorf("a truncated export must say so in the stream, got %q", rec.Body.String())
	}
}
