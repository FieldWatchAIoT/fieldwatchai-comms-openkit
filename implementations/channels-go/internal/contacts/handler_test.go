package contacts

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

const testTenant = "22222222-2222-2222-2222-222222222222"

type fakeSvc struct {
	createErr error
	listOut   []Contact
	updateErr error
	deleteErr error
	checkOut  ShortIDCheckResult
	bulkOut   BulkResult
	bulkErr   error
}

func (f *fakeSvc) Create(_ context.Context, _ CreateInput) (Contact, error) {
	return Contact{ID: uuid.New(), ShortID: "42"}, f.createErr
}
func (f *fakeSvc) Get(_ context.Context, _, _ uuid.UUID) (Contact, error) { return Contact{}, nil }
func (f *fakeSvc) List(_ context.Context, _ uuid.UUID) ([]Contact, error) { return f.listOut, nil }
func (f *fakeSvc) Update(_ context.Context, _, _ uuid.UUID, _ UpdateInput) (Contact, error) {
	return Contact{ShortID: "42"}, f.updateErr
}
func (f *fakeSvc) Delete(_ context.Context, _, _ uuid.UUID) error { return f.deleteErr }
func (f *fakeSvc) ShortIDCheck(_ context.Context, _ uuid.UUID, _ string) (ShortIDCheckResult, error) {
	return f.checkOut, nil
}
func (f *fakeSvc) BulkImport(_ context.Context, _ uuid.UUID, _ io.Reader) (BulkResult, error) {
	return f.bulkOut, f.bulkErr
}

func h(svc serviceAPI) http.Handler {
	hd := NewHandler(svc, slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	hd.RegisterRoutes(mux)
	return mux
}

func req(handler http.Handler, method, path, tenant, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if tenant != "" {
		r.Header.Set("X-Tenant-ID", tenant)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)
	return rec
}

func TestCreate201(t *testing.T) {
	rec := req(h(&fakeSvc{}), http.MethodPost, "/v1/contacts", testTenant, `{"short_id":"42","display_name":"Marsh"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; %s", rec.Code, rec.Body.String())
	}
}

func TestCreateConflict409(t *testing.T) {
	rec := req(h(&fakeSvc{createErr: ErrConflict}), http.MethodPost, "/v1/contacts", testTenant, `{"short_id":"42","display_name":"X"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

func TestCreateMissingTenant400(t *testing.T) {
	rec := req(h(&fakeSvc{}), http.MethodPost, "/v1/contacts", "", `{"short_id":"42"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestList200(t *testing.T) {
	rec := req(h(&fakeSvc{listOut: []Contact{{ShortID: "42"}}}), http.MethodGet, "/v1/contacts", testTenant, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestPatchNotFound404(t *testing.T) {
	rec := req(h(&fakeSvc{updateErr: ErrNotFound}), http.MethodPatch, "/v1/contacts/"+uuid.New().String(), testTenant, `{"display_name":"x"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestDelete204(t *testing.T) {
	rec := req(h(&fakeSvc{}), http.MethodDelete, "/v1/contacts/"+uuid.New().String(), testTenant, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
}

func TestShortIDCheck200(t *testing.T) {
	rec := req(h(&fakeSvc{checkOut: ShortIDCheckResult{Exists: true}}), http.MethodGet, "/v1/contacts/short_id_check?short_id=42", testTenant, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"exists":true`) {
		t.Errorf("body = %s", rec.Body.String())
	}
}

func TestBulkImport200(t *testing.T) {
	fake := &fakeSvc{bulkOut: BulkResult{Created: 3, Skipped: 1, Errors: []RowError{{Row: 2, Reason: "x"}}}}
	rec := req(h(fake), http.MethodPost, "/v1/contacts/bulk-import", testTenant, "short_id,display_name\n42,Marsh")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"created":3`) {
		t.Errorf("body = %s", rec.Body.String())
	}
}

func TestBulkImportMissingTenant400(t *testing.T) {
	rec := req(h(&fakeSvc{}), http.MethodPost, "/v1/contacts/bulk-import", "", "short_id\n42")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestShortIDCheckMissingParam400(t *testing.T) {
	rec := req(h(&fakeSvc{}), http.MethodGet, "/v1/contacts/short_id_check", testTenant, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
