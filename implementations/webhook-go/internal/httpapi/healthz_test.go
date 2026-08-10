package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestServer builds a Server with a silenced logger for use in tests.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	return NewServer(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func decodeJSON(t *testing.T, r io.Reader) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.NewDecoder(r).Decode(&m); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	return m
}

// TestHealthz_ReadyReturns200 confirms /healthz reports healthy once the
// server has been marked ready (the normal serving state).
func TestHealthz_ReadyReturns200(t *testing.T) {
	srv := newTestServer(t)
	srv.MarkReady()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if got := decodeJSON(t, rr.Body)["status"]; got != "ok" {
		t.Errorf("status field = %q, want ok", got)
	}
}

// TestHealthz_NotReadyReturns503 confirms /healthz fails the health check
// after MarkNotReady (e.g. during graceful shutdown) so the load balancer
// drains the instance instead of routing new traffic to it.
func TestHealthz_NotReadyReturns503(t *testing.T) {
	srv := newTestServer(t)
	srv.MarkNotReady()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
}
