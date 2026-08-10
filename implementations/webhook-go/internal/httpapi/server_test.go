package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestRoutes_HealthzRunsBehindRequestID confirms the global middleware chain
// is wired: a request to /healthz comes back with an X-Request-Id header,
// which only WithRequestID can have set. This proves the chain wraps the mux.
func TestRoutes_HealthzRunsBehindMiddleware(t *testing.T) {
	srv := newTestServer(t)
	srv.MarkReady()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if rr.Header().Get("X-Request-Id") == "" {
		t.Error("no X-Request-Id header; request-id middleware not wired into Routes")
	}
}

// TestRoutes_PanicInRouteIsRecovered confirms the recover middleware wraps the
// mux: a registered route that panics yields 500, not a crashed server.
func TestRoutes_PanicInRouteIsRecovered(t *testing.T) {
	srv := newTestServer(t)
	srv.MarkReady()
	srv.Mux().HandleFunc("/boom", func(w http.ResponseWriter, r *http.Request) {
		panic("kaboom")
	})

	req := httptest.NewRequest(http.MethodGet, "/boom", nil)
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (recover should wrap the mux)", rr.Code)
	}
}
