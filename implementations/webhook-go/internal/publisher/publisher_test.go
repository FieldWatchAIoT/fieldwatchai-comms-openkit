package publisher

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/canonical"
)

func msg(id string) canonical.Message {
	return canonical.Message{Meta: canonical.Meta{PlatformMessageID: id}}
}

// TestPublish_PostsToIngestWithAuthAndIdempotency verifies the wire contract
// with comms-channels: POST {url}/v1/ingest, Bearer auth, Idempotency-Key =
// platform_message_id, JSON canonical body.
func TestPublish_PostsToIngestWithAuthAndIdempotency(t *testing.T) {
	var gotPath, gotAuth, gotIdem, gotCT string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotIdem = r.Header.Get("Idempotency-Key")
		gotCT = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	p := NewHTTP(srv.URL, "tok123", srv.Client())
	if err := p.Publish(context.Background(), msg("wamid.X")); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if gotPath != "/v1/ingest" {
		t.Errorf("path = %q, want /v1/ingest", gotPath)
	}
	if gotAuth != "Bearer tok123" {
		t.Errorf("authorization = %q, want 'Bearer tok123'", gotAuth)
	}
	if gotIdem != "wamid.X" {
		t.Errorf("idempotency-key = %q, want wamid.X", gotIdem)
	}
	if !strings.Contains(gotCT, "application/json") {
		t.Errorf("content-type = %q, want application/json", gotCT)
	}
	var back canonical.Message
	if err := json.Unmarshal(gotBody, &back); err != nil {
		t.Fatalf("body not canonical JSON: %v", err)
	}
	if back.Meta.PlatformMessageID != "wamid.X" {
		t.Errorf("body platform_message_id = %q", back.Meta.PlatformMessageID)
	}
}

func TestPublish_2xxReturnsNil(t *testing.T) {
	for _, code := range []int{200, 201, 202} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
		}))
		p := NewHTTP(srv.URL, "t", srv.Client())
		if err := p.Publish(context.Background(), msg("a")); err != nil {
			t.Errorf("status %d: Publish err = %v, want nil", code, err)
		}
		srv.Close()
	}
}

func TestPublish_4xxIsPermanent(t *testing.T) {
	for _, code := range []int{400, 404, 409, 422} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
		}))
		err := NewHTTP(srv.URL, "t", srv.Client()).Publish(context.Background(), msg("a"))
		if err == nil || !IsPermanent(err) {
			t.Errorf("status %d: err = %v, want permanent", code, err)
		}
		srv.Close()
	}
}

func TestPublish_5xxIsTransient(t *testing.T) {
	for _, code := range []int{500, 502, 503} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
		}))
		err := NewHTTP(srv.URL, "t", srv.Client()).Publish(context.Background(), msg("a"))
		if err == nil || IsPermanent(err) {
			t.Errorf("status %d: err = %v, want transient (retryable)", code, err)
		}
		srv.Close()
	}
}

func TestPublish_429IsTransient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	err := NewHTTP(srv.URL, "t", srv.Client()).Publish(context.Background(), msg("a"))
	if err == nil || IsPermanent(err) {
		t.Errorf("429: err = %v, want transient", err)
	}
}

// TestPublish_NetworkErrorIsTransient confirms an unreachable channels endpoint
// is a transient (retryable) failure, not permanent.
func TestPublish_NetworkErrorIsTransient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	client := srv.Client()
	srv.Close() // now unreachable

	err := NewHTTP(url, "t", client).Publish(context.Background(), msg("a"))
	if err == nil || IsPermanent(err) {
		t.Errorf("network error: err = %v, want transient", err)
	}
}

func TestHTTP_SatisfiesPublisher(t *testing.T) {
	var _ Publisher = (*HTTP)(nil)
}
