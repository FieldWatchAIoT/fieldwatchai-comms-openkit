package accounts

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

// fakeSvc implements serviceAPI.
type fakeSvc struct {
	createIn   CreateInput
	updateID   uuid.UUID
	updateIn   UpdateInput
	listTenant uuid.UUID
	createOut  Account
	listOut    []Account
	routingOut []AccountWithRouting
	updateOut  Account
	createErr  error
	updateErr  error
	deleteErr  error

	lookupType  string
	lookupIdent string
	lookupOut   LookupResult
	lookupErr   error
}

func (f *fakeSvc) Lookup(_ context.Context, accType, identifier string) (LookupResult, error) {
	f.lookupType, f.lookupIdent = accType, identifier
	return f.lookupOut, f.lookupErr
}

func (f *fakeSvc) Create(_ context.Context, in CreateInput) (Account, error) {
	f.createIn = in
	return f.createOut, f.createErr
}
func (f *fakeSvc) List(_ context.Context, t uuid.UUID) ([]Account, error) {
	f.listTenant = t
	return f.listOut, nil
}
func (f *fakeSvc) ListWithRouting(_ context.Context, t uuid.UUID) ([]AccountWithRouting, error) {
	f.listTenant = t
	if f.routingOut != nil {
		return f.routingOut, nil
	}
	out := make([]AccountWithRouting, 0, len(f.listOut))
	for _, a := range f.listOut {
		out = append(out, AccountWithRouting{Account: a})
	}
	return out, nil
}
func (f *fakeSvc) Update(_ context.Context, id, _ uuid.UUID, in UpdateInput) (Account, error) {
	f.updateID, f.updateIn = id, in
	return f.updateOut, f.updateErr
}
func (f *fakeSvc) Delete(_ context.Context, _, _ uuid.UUID) error { return f.deleteErr }

func testHandler(svc serviceAPI) http.Handler {
	h := NewHandler(svc, slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return mux
}

func do(h http.Handler, method, path, tenant, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if tenant != "" {
		req.Header.Set("X-Tenant-ID", tenant)
	}
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestCreateReturns201AndNeverLeaksCredentials(t *testing.T) {
	fake := &fakeSvc{createOut: Account{ID: uuid.New(), Type: "whatsapp", Label: "L"}}
	h := testHandler(fake)

	body := `{"type":"whatsapp","owner_type":"platform","label":"L","platform_identifier":"179557","status":"active","capabilities":["inbound"],"credentials":"ultramsg-token"}`
	rec := do(h, http.MethodPost, "/v1/accounts", testTenant, body)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "credentials") || strings.Contains(rec.Body.String(), "ultramsg-token") {
		t.Errorf("response leaked credentials: %s", rec.Body.String())
	}
	// Service received the plaintext credentials + tenant from header.
	if string(fake.createIn.Credentials) != "ultramsg-token" {
		t.Errorf("credentials not passed to service: %q", fake.createIn.Credentials)
	}
	if fake.createIn.TenantID != uuid.MustParse(testTenant) {
		t.Errorf("tenant not from header: %v", fake.createIn.TenantID)
	}
}

func TestCreateMissingTenantHeaderReturns400(t *testing.T) {
	h := testHandler(&fakeSvc{})
	rec := do(h, http.MethodPost, "/v1/accounts", "", `{"type":"whatsapp"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestCreateInvalidReturns400(t *testing.T) {
	h := testHandler(&fakeSvc{createErr: ErrInvalid})
	rec := do(h, http.MethodPost, "/v1/accounts", testTenant, `{"type":""}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestListReturns200AndScopesToTenant(t *testing.T) {
	fake := &fakeSvc{listOut: []Account{{Label: "a"}, {Label: "b"}}}
	h := testHandler(fake)
	rec := do(h, http.MethodGet, "/v1/accounts", testTenant, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if fake.listTenant != uuid.MustParse(testTenant) {
		t.Errorf("list not tenant-scoped: %v", fake.listTenant)
	}
}

func TestPatchReturns200(t *testing.T) {
	id := uuid.New()
	fake := &fakeSvc{updateOut: Account{ID: id, Label: "new"}}
	h := testHandler(fake)
	rec := do(h, http.MethodPatch, "/v1/accounts/"+id.String(), testTenant, `{"label":"new"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if fake.updateID != id || fake.updateIn.Label == nil || *fake.updateIn.Label != "new" {
		t.Errorf("update args wrong: id=%v in=%+v", fake.updateID, fake.updateIn)
	}
}

func TestPatchNotFoundReturns404(t *testing.T) {
	h := testHandler(&fakeSvc{updateErr: ErrNotFound})
	rec := do(h, http.MethodPatch, "/v1/accounts/"+uuid.New().String(), testTenant, `{"label":"x"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestDeleteReturns204(t *testing.T) {
	h := testHandler(&fakeSvc{})
	rec := do(h, http.MethodDelete, "/v1/accounts/"+uuid.New().String(), testTenant, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
}

func TestDeleteNotFoundReturns404(t *testing.T) {
	h := testHandler(&fakeSvc{deleteErr: ErrNotFound})
	rec := do(h, http.MethodDelete, "/v1/accounts/"+uuid.New().String(), testTenant, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestBadIDReturns400(t *testing.T) {
	h := testHandler(&fakeSvc{})
	rec := do(h, http.MethodDelete, "/v1/accounts/not-a-uuid", testTenant, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
