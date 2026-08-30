package messagelog

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/queries/goqueries"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type fakeStore struct {
	rows []goqueries.ListRecentMessagesForTenantRow
	got  goqueries.ListRecentMessagesForTenantParams
}

func (f *fakeStore) ListRecentMessagesForTenant(_ context.Context, p goqueries.ListRecentMessagesForTenantParams) ([]goqueries.ListRecentMessagesForTenantRow, error) {
	f.got = p
	return f.rows, nil
}

func row(text, action string, parsed string) goqueries.ListRecentMessagesForTenantRow {
	return goqueries.ListRecentMessagesForTenantRow{
		ID: uuid.New(), Direction: "inbound", BodyText: text,
		SenderEndpoint: pgtype.Text{String: "+12425550042", Valid: true},
		PolicyAction:   pgtype.Text{String: action, Valid: true},
		Parsed:         []byte(parsed), ReceivedAt: time.Now(),
	}
}

func TestListMapsParsedDoc(t *testing.T) {
	fs := &fakeStore{rows: []goqueries.ListRecentMessagesForTenantRow{
		row("42 STATUS full", "execute", `{"command":"STATUS","short_id":"42","confidence":1,"short_id_match":"exact"}`),
	}}
	msgs, err := NewService(fs).List(context.Background(), uuid.New(), 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages", len(msgs))
	}
	m := msgs[0]
	if m.Parsed == nil || m.Parsed.Command != "STATUS" || m.Parsed.Confidence != 1 {
		t.Errorf("parsed doc not mapped: %+v", m.Parsed)
	}
	if m.SenderEndpoint != "+12425550042" || m.PolicyAction != "execute" {
		t.Errorf("row not mapped: %+v", m)
	}
}

// A malformed parsed doc must not blank the row — the body text and policy
// action are still what the operator came to see.
func TestListSurvivesUnparseableParsedDoc(t *testing.T) {
	fs := &fakeStore{rows: []goqueries.ListRecentMessagesForTenantRow{
		row("hello", "clarify", `{not json`),
	}}
	msgs, err := NewService(fs).List(context.Background(), uuid.New(), 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(msgs) != 1 || msgs[0].BodyText != "hello" || msgs[0].PolicyAction != "clarify" {
		t.Fatalf("row should survive: %+v", msgs)
	}
	if msgs[0].Parsed != nil {
		t.Error("parsed should be nil, not partially populated")
	}
}

func TestListDefaultsAndClampsLimit(t *testing.T) {
	for _, tc := range []struct {
		in   int
		want int32
	}{
		{0, DefaultLimit}, {-5, DefaultLimit}, {10, 10}, {MaxLimit + 1000, MaxLimit},
	} {
		fs := &fakeStore{}
		if _, err := NewService(fs).List(context.Background(), uuid.New(), tc.in); err != nil {
			t.Fatalf("List(%d): %v", tc.in, err)
		}
		if fs.got.Limit != tc.want {
			t.Errorf("limit %d -> %d, want %d", tc.in, fs.got.Limit, tc.want)
		}
	}
}

func TestHandlerRequiresTenant(t *testing.T) {
	mux := http.NewServeMux()
	NewHandler(NewService(&fakeStore{}), discardLogger()).RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/messages", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// A junk limit should still return messages rather than 400 — the caller wants
// data, and failing over a query-string typo helps nobody.
func TestHandlerIgnoresJunkLimit(t *testing.T) {
	fs := &fakeStore{rows: []goqueries.ListRecentMessagesForTenantRow{row("x", "execute", `{}`)}}
	mux := http.NewServeMux()
	NewHandler(NewService(fs), discardLogger()).RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, "/v1/messages?limit=banana", nil)
	req.Header.Set("X-Tenant-ID", uuid.New().String())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Count != 1 {
		t.Errorf("want 1 message, got %d (%v)", body.Count, err)
	}
}
