package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestWithBodyCap_OverLimitRejected confirms an oversized body is rejected
// with 413 before the downstream handler runs — a cheap guard against a
// malicious or runaway payload exhausting memory during parse.
func TestWithBodyCap_OverLimitRejected(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })

	body := strings.Repeat("A", 200)
	req := httptest.NewRequest(http.MethodPost, "/inbound/x", strings.NewReader(body))
	rr := httptest.NewRecorder()
	WithBodyCap(64, next).ServeHTTP(rr, req)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rr.Code)
	}
	if got := decodeJSON(t, rr.Body)["status"]; got != "payload_too_large" {
		t.Errorf("status field = %q, want payload_too_large", got)
	}
	if called {
		t.Error("downstream handler ran; it must be skipped for oversized bodies")
	}
}

// TestWithBodyCap_UnderLimitPassesThrough confirms a within-cap body reaches
// the downstream handler intact and fully readable.
func TestWithBodyCap_UnderLimitPassesThrough(t *testing.T) {
	var read string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		read = string(b)
		w.WriteHeader(http.StatusOK)
	})

	body := `{"ok":true}`
	req := httptest.NewRequest(http.MethodPost, "/inbound/x", strings.NewReader(body))
	rr := httptest.NewRecorder()
	WithBodyCap(1024, next).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if read != body {
		t.Errorf("downstream read %q, want %q", read, body)
	}
}

// TestWithBodyCap_ZeroMeansNoCap confirms a cap of 0 disables enforcement.
func TestWithBodyCap_ZeroMeansNoCap(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	body := strings.Repeat("A", 10_000)
	req := httptest.NewRequest(http.MethodPost, "/inbound/x", strings.NewReader(body))
	rr := httptest.NewRecorder()
	WithBodyCap(0, next).ServeHTTP(rr, req)

	if !called || rr.Code != http.StatusOK {
		t.Fatalf("zero cap should pass through; called=%v code=%d", called, rr.Code)
	}
}
