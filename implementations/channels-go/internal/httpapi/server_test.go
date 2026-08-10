package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	return NewServer(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestHealthzNotReady(t *testing.T) {
	s := testServer(t)
	// default: not ready

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestHealthzReady(t *testing.T) {
	s := testServer(t)
	s.MarkReady()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "ok") {
		t.Errorf("body = %q, want it to contain ok", rec.Body.String())
	}
}

func TestRequestIDMintedWhenAbsent(t *testing.T) {
	s := testServer(t)
	s.MarkReady()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Request-Id"); got == "" {
		t.Fatal("X-Request-Id response header missing, want a minted id")
	}
}

func TestRequestIDPreservedWhenProvided(t *testing.T) {
	s := testServer(t)
	s.MarkReady()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("X-Request-Id", "abc-123")
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Request-Id"); got != "abc-123" {
		t.Fatalf("X-Request-Id = %q, want abc-123", got)
	}
}

func TestRecoverTurnsPanicInto500(t *testing.T) {
	s := testServer(t)
	s.Mux().HandleFunc("/boom", func(http.ResponseWriter, *http.Request) {
		panic("kaboom")
	})

	req := httptest.NewRequest(http.MethodGet, "/boom", nil)
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestRequestIDAvailableInContext(t *testing.T) {
	s := testServer(t)
	var seen string
	s.Mux().HandleFunc("/echo", func(_ http.ResponseWriter, r *http.Request) {
		seen = RequestIDFromContext(r.Context())
	})

	req := httptest.NewRequest(http.MethodGet, "/echo", nil)
	req.Header.Set("X-Request-Id", "ctx-42")
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)

	if seen != "ctx-42" {
		t.Fatalf("RequestIDFromContext = %q, want ctx-42", seen)
	}
}
