package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestWithRequestID_MintsWhenAbsent confirms the middleware generates a
// correlation ID when the caller did not supply one, exposes it to the
// downstream handler via context, and echoes it back in the response header.
func TestWithRequestID_MintsWhenAbsent(t *testing.T) {
	var seen string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = RequestIDFromContext(r.Context())
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	WithRequestID(next).ServeHTTP(rr, req)

	if seen == "" {
		t.Fatal("handler saw empty request id; want a minted id")
	}
	if echoed := rr.Header().Get("X-Request-Id"); echoed != seen {
		t.Errorf("echoed header = %q, want %q (same as context id)", echoed, seen)
	}
}

// TestWithRequestID_PreservesSupplied confirms an upstream-provided
// X-Request-Id (e.g. from a load balancer) is kept rather than replaced, so
// correlation holds across the whole request path.
func TestWithRequestID_PreservesSupplied(t *testing.T) {
	const supplied = "lb-correlation-123"
	var seen string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = RequestIDFromContext(r.Context())
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("X-Request-Id", supplied)
	rr := httptest.NewRecorder()
	WithRequestID(next).ServeHTTP(rr, req)

	if seen != supplied {
		t.Errorf("handler saw id %q, want supplied %q", seen, supplied)
	}
}
