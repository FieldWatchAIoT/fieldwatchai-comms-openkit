package tenants

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
	"github.com/jackc/pgx/v5"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
func fixedTime() time.Time        { return time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC) }

type fakeStore struct {
	created  goqueries.CreateTenantParams
	createEr error
	got      goqueries.Tenant
	getErr   error
	list     []goqueries.Tenant
}

func (f *fakeStore) CreateTenant(_ context.Context, p goqueries.CreateTenantParams) (goqueries.Tenant, error) {
	f.created = p
	if f.createEr != nil {
		return goqueries.Tenant{}, f.createEr
	}
	return goqueries.Tenant{ID: p.ID, Name: p.Name, Plan: p.Plan, CreatedAt: p.CreatedAt, Settings: p.Settings}, nil
}
func (f *fakeStore) GetTenant(_ context.Context, _ uuid.UUID) (goqueries.Tenant, error) {
	return f.got, f.getErr
}
func (f *fakeStore) ListTenants(_ context.Context) ([]goqueries.Tenant, error) { return f.list, nil }

func TestCreateDefaultsPlanAndSettings(t *testing.T) {
	fs := &fakeStore{}
	tn, err := NewService(fs).Create(context.Background(), CreateInput{Name: "Demo Agency"}, fixedTime, uuid.New)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if tn.Plan != "starter" {
		t.Errorf("plan = %q, want starter", tn.Plan)
	}
	if string(fs.created.Settings) != "{}" {
		t.Errorf("settings = %s, want {}", fs.created.Settings)
	}
}

// A caller-supplied id is what makes a setup script re-runnable.
func TestCreateHonoursSuppliedID(t *testing.T) {
	fs := &fakeStore{}
	want := uuid.New()
	tn, err := NewService(fs).Create(context.Background(),
		CreateInput{ID: &want, Name: "Demo"}, fixedTime, uuid.New)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if tn.ID != want {
		t.Errorf("id = %s, want %s", tn.ID, want)
	}
}

// Re-running setup must return the existing tenant, not fail. ON CONFLICT DO
// NOTHING surfaces as ErrNoRows, which would otherwise become a 500.
func TestCreateIsIdempotentOnExistingID(t *testing.T) {
	existing := uuid.New()
	fs := &fakeStore{
		createEr: pgx.ErrNoRows,
		got:      goqueries.Tenant{ID: existing, Name: "Demo Agency", Plan: "starter"},
	}
	tn, err := NewService(fs).Create(context.Background(),
		CreateInput{ID: &existing, Name: "Demo Agency"}, fixedTime, uuid.New)
	if err != nil {
		t.Fatalf("re-running create should not error, got %v", err)
	}
	if tn.ID != existing || tn.Name != "Demo Agency" {
		t.Errorf("should have returned the existing tenant, got %+v", tn)
	}
}

func TestCreateRejectsBlankName(t *testing.T) {
	_, err := NewService(&fakeStore{}).Create(context.Background(), CreateInput{Name: " "}, fixedTime, uuid.New)
	if err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("want name error, got %v", err)
	}
}

func TestCreateRejectsUnknownPlan(t *testing.T) {
	_, err := NewService(&fakeStore{}).Create(context.Background(),
		CreateInput{Name: "x", Plan: "free"}, fixedTime, uuid.New)
	if err == nil || !strings.Contains(err.Error(), "plan must be") {
		t.Fatalf("want plan error, got %v", err)
	}
}

func TestHandlerCreate201(t *testing.T) {
	mux := http.NewServeMux()
	NewHandler(NewService(&fakeStore{}), discardLogger()).RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/tenants",
		strings.NewReader(`{"name":"Demo Agency"}`)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", rec.Code, rec.Body)
	}
	var tn Tenant
	if err := json.Unmarshal(rec.Body.Bytes(), &tn); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	if tn.ID == uuid.Nil {
		t.Error("response should carry the generated id — it is the value the caller needs next")
	}
}

// No X-Tenant-ID here on purpose: this is the call made before one exists.
func TestHandlerCreateNeedsNoTenantHeader(t *testing.T) {
	mux := http.NewServeMux()
	NewHandler(NewService(&fakeStore{}), discardLogger()).RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/tenants", strings.NewReader(`{"name":"x"}`))
	mux.ServeHTTP(rec, req)
	if rec.Code == http.StatusBadRequest {
		t.Fatal("tenant creation must not require an X-Tenant-ID header")
	}
}

func TestHandlerGetNotFound(t *testing.T) {
	mux := http.NewServeMux()
	NewHandler(NewService(&fakeStore{getErr: pgx.ErrNoRows}), discardLogger()).RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/tenants/"+uuid.New().String(), nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
