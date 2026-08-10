package accounts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestHTTPResolver_ResolvesWithAuthAndQuery confirms the lookup contract with
// channels: GET /v1/accounts/lookup?type=&identifier= with Bearer auth, and a
// 200 {account_id,tenant_id} maps to Account{ID}.
func TestHTTPResolver_ResolvesWithAuthAndQuery(t *testing.T) {
	var gotPath, gotType, gotIdent, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotType = r.URL.Query().Get("type")
		gotIdent = r.URL.Query().Get("identifier")
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"account_id":"acc_x","tenant_id":"t_y"}`))
	}))
	defer srv.Close()

	r := NewHTTPResolver(srv.URL, "tok123", srv.Client())
	acc, err := r.Resolve(context.Background(), "whatsapp-ultramsg", "179557")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if acc.ID != "acc_x" {
		t.Errorf("account id = %q, want acc_x", acc.ID)
	}
	if gotPath != "/v1/accounts/lookup" {
		t.Errorf("path = %q", gotPath)
	}
	if gotType != "whatsapp-ultramsg" || gotIdent != "179557" {
		t.Errorf("query type=%q identifier=%q", gotType, gotIdent)
	}
	if gotAuth != "Bearer tok123" {
		t.Errorf("auth = %q, want 'Bearer tok123'", gotAuth)
	}
}

// TestHTTPResolver_404IsNotFound: an unregistered identifier -> ErrNotFound
// (the listener's drop path).
func TestHTTPResolver_404IsNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	_, err := NewHTTPResolver(srv.URL, "t", srv.Client()).Resolve(context.Background(), "p", "i")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// TestHTTPResolver_5xxIsTransient: a channels error must be a transient error
// (NOT ErrNotFound) so the caller retries instead of dropping.
func TestHTTPResolver_5xxIsTransient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	_, err := NewHTTPResolver(srv.URL, "t", srv.Client()).Resolve(context.Background(), "p", "i")
	if err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want a transient (non-NotFound) error", err)
	}
}

// TestHTTPResolver_NetworkIsTransient: an unreachable channels is transient.
func TestHTTPResolver_NetworkIsTransient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url, client := srv.URL, srv.Client()
	srv.Close()
	_, err := NewHTTPResolver(url, "t", client).Resolve(context.Background(), "p", "i")
	if err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want transient", err)
	}
}

// TestHTTPResolver_CachesPositive: repeated lookups for the same key within TTL
// hit channels only once (resolver is hot per inbound).
func TestHTTPResolver_CachesPositive(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		_, _ = w.Write([]byte(`{"account_id":"acc_x","tenant_id":"t"}`))
	}))
	defer srv.Close()
	r := NewHTTPResolver(srv.URL, "t", srv.Client())
	for i := 0; i < 3; i++ {
		if _, err := r.Resolve(context.Background(), "p", "same"); err != nil {
			t.Fatal(err)
		}
	}
	if got := atomic.LoadInt64(&hits); got != 1 {
		t.Errorf("channels hit %d times, want 1 (cached)", got)
	}
}

func TestHTTPResolver_SatisfiesResolver(t *testing.T) {
	var _ Resolver = (*HTTPResolver)(nil)
	_ = time.Second
}

func TestHTTPResolver_StripsTrailingSlash(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"account_id":"a","tenant_id":"t"}`))
	}))
	defer srv.Close()
	_, _ = NewHTTPResolver(srv.URL+"/", "t", srv.Client()).Resolve(context.Background(), "p", "i")
	if strings.Contains(gotPath, "//") {
		t.Errorf("path has double slash: %q", gotPath)
	}
}
