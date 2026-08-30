package diagnostics

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/queries/goqueries"
	"github.com/google/uuid"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type fakeStore struct {
	accounts, channels, contacts, messages int64
	unroutable                             []goqueries.ListUnroutableAccountsRow
	noWorkflow                             []goqueries.ListChannelsWithoutWorkflowRow
}

func (f *fakeStore) CountAccountsForTenant(context.Context, uuid.UUID) (int64, error) {
	return f.accounts, nil
}
func (f *fakeStore) CountChannelsForTenant(context.Context, uuid.UUID) (int64, error) {
	return f.channels, nil
}
func (f *fakeStore) CountContactsForTenant(context.Context, uuid.UUID) (int64, error) {
	return f.contacts, nil
}
func (f *fakeStore) CountMessagesForTenant(context.Context, uuid.UUID) (int64, error) {
	return f.messages, nil
}
func (f *fakeStore) ListUnroutableAccounts(context.Context, uuid.UUID) ([]goqueries.ListUnroutableAccountsRow, error) {
	return f.unroutable, nil
}
func (f *fakeStore) ListChannelsWithoutWorkflow(context.Context, uuid.UUID) ([]goqueries.ListChannelsWithoutWorkflowRow, error) {
	return f.noWorkflow, nil
}

func codes(r Report) []string {
	out := make([]string, 0, len(r.Findings))
	for _, f := range r.Findings {
		out = append(out, f.Code)
	}
	return out
}

// An empty database is what every adopter starts with, and it is the state in
// which the system looks like it is working while dropping everything.
func TestEmptyTenantIsUnhealthyAndSaysWhy(t *testing.T) {
	rep, err := NewService(&fakeStore{}).Run(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Healthy {
		t.Error("an empty tenant must not report healthy")
	}
	if got := strings.Join(codes(rep), ","); !strings.Contains(got, "no_accounts") {
		t.Errorf("findings = %v, want no_accounts", got)
	}
}

// The headline case: an account exists and looks configured, but nothing is
// linked, so traffic is stored and forwarded nowhere with no error anywhere.
func TestUnlinkedAccountIsBlockingAndNamesTheAccount(t *testing.T) {
	acctID := uuid.New()
	fs := &fakeStore{
		accounts: 1, contacts: 1,
		unroutable: []goqueries.ListUnroutableAccountsRow{{
			ID: acctID, Type: "whatsapp", PlatformIdentifier: "instance123", Label: "Demo WhatsApp",
		}},
	}
	rep, err := NewService(fs).Run(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Healthy {
		t.Error("an unlinked account must not report healthy")
	}
	var f *Finding
	for i := range rep.Findings {
		if rep.Findings[i].Code == "account_not_linked_to_channel" {
			f = &rep.Findings[i]
		}
	}
	if f == nil {
		t.Fatalf("missing finding, got %v", codes(rep))
	}
	if f.Severity != SeverityBlocking {
		t.Errorf("severity = %q, want blocking", f.Severity)
	}
	// The operator has to act on this without reading the source, so the
	// summary must identify which account and the remedy must carry its id.
	if !strings.Contains(f.Summary, "Demo WhatsApp") || !strings.Contains(f.Summary, "instance123") {
		t.Errorf("summary does not identify the account: %q", f.Summary)
	}
	if !strings.Contains(f.Remedy, acctID.String()) {
		t.Errorf("remedy should carry the account id, got %q", f.Remedy)
	}
}

// A channel with no workflow_url is a warning, not a blocker: a deployment that
// reads the database directly is legitimately configured this way.
func TestChannelWithoutWorkflowIsWarningOnly(t *testing.T) {
	fs := &fakeStore{
		accounts: 1, channels: 1, contacts: 1,
		noWorkflow: []goqueries.ListChannelsWithoutWorkflowRow{{ID: uuid.New(), Name: "Field Ops"}},
	}
	rep, err := NewService(fs).Run(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !rep.Healthy {
		t.Error("a missing workflow_url alone must not mark the tenant unhealthy")
	}
	if got := strings.Join(codes(rep), ","); !strings.Contains(got, "channel_has_no_workflow_url") {
		t.Errorf("findings = %v, want the warning", got)
	}
}

func TestNoContactsIsWarningOnly(t *testing.T) {
	fs := &fakeStore{accounts: 1, channels: 1}
	rep, err := NewService(fs).Run(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !rep.Healthy {
		t.Error("missing contacts alone must not mark the tenant unhealthy")
	}
	if got := strings.Join(codes(rep), ","); !strings.Contains(got, "no_contacts") {
		t.Errorf("findings = %v, want no_contacts", got)
	}
}

func TestFullyConfiguredTenantIsHealthyWithNoFindings(t *testing.T) {
	fs := &fakeStore{accounts: 1, channels: 1, contacts: 3, messages: 12}
	rep, err := NewService(fs).Run(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !rep.Healthy || len(rep.Findings) != 0 {
		t.Errorf("want healthy with no findings, got healthy=%v findings=%v", rep.Healthy, codes(rep))
	}
	if rep.Counts.Messages != 12 {
		t.Errorf("counts not reported: %+v", rep.Counts)
	}
}

// Blocking findings must come before warnings — the first line an operator
// reads should be the one to act on.
func TestBlockingFindingsSortBeforeWarnings(t *testing.T) {
	fs := &fakeStore{
		accounts:   1,
		unroutable: []goqueries.ListUnroutableAccountsRow{{ID: uuid.New(), Label: "A"}},
		noWorkflow: []goqueries.ListChannelsWithoutWorkflowRow{{ID: uuid.New(), Name: "C"}},
	}
	rep, err := NewService(fs).Run(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	seenWarning := false
	for _, f := range rep.Findings {
		if f.Severity == SeverityWarning {
			seenWarning = true
		} else if seenWarning {
			t.Fatalf("blocking finding %q appears after a warning: %v", f.Code, codes(rep))
		}
	}
}

// --- handler ---

// A misconfigured tenant is a 200 with findings, not an error status: a non-2xx
// would be indistinguishable from the service itself being broken.
func TestHandlerReturns200EvenWhenUnhealthy(t *testing.T) {
	mux := http.NewServeMux()
	NewHandler(NewService(&fakeStore{}), discardLogger()).RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, "/v1/diagnostics", nil)
	req.Header.Set("X-Tenant-ID", uuid.New().String())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var rep Report
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	if rep.Healthy || len(rep.Findings) == 0 {
		t.Errorf("expected an unhealthy report with findings, got %+v", rep)
	}
}

func TestHandlerRejectsMissingTenant(t *testing.T) {
	mux := http.NewServeMux()
	NewHandler(NewService(&fakeStore{}), discardLogger()).RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/diagnostics", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
