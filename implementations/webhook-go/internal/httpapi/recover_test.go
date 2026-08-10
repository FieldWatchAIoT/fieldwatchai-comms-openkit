package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestWithRecover_PanicBecomes500 confirms a panic in a downstream handler is
// caught and turned into a 500 response — the process must survive one bad
// inbound payload, since availability is the whole point of this service.
func TestWithRecover_PanicBecomes500(t *testing.T) {
	panicking := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})

	req := httptest.NewRequest(http.MethodPost, "/inbound/whatever", nil)
	rr := httptest.NewRecorder()

	// If the panic escaped the middleware, this call would crash the test.
	WithRecover(discardLogger(), panicking).ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}
	if got := decodeJSON(t, rr.Body)["status"]; got != "internal_error" {
		t.Errorf("status field = %q, want internal_error", got)
	}
}

// TestWithRecover_PassesThroughWhenNoPanic confirms the middleware is
// transparent to handlers that behave normally.
func TestWithRecover_PassesThroughWhenNoPanic(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})

	req := httptest.NewRequest(http.MethodGet, "/inbound/whatever", nil)
	rr := httptest.NewRecorder()
	WithRecover(discardLogger(), ok).ServeHTTP(rr, req)

	if rr.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want 418 (passthrough)", rr.Code)
	}
}
