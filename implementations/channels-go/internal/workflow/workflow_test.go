package workflow

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func testFwd(ts *httptest.Server) *Forwarder {
	return &Forwarder{Client: ts.Client(), Attempts: 3, Backoff: time.Millisecond}
}

func TestForwardSuccess(t *testing.T) {
	var gotBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(200)
	}))
	defer ts.Close()

	err := testFwd(ts).Forward(context.Background(), ts.URL, Forward{MessageID: "m1", Command: "DAMAGE", Text: "42 DAMAGE x"})
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if !contains(gotBody, `"command":"DAMAGE"`) || !contains(gotBody, `"message_id":"m1"`) {
		t.Errorf("payload missing fields: %s", gotBody)
	}
}

func TestForwardRetriesThenSucceeds(t *testing.T) {
	var n int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&n, 1) < 3 {
			w.WriteHeader(503) // transient
			return
		}
		w.WriteHeader(200)
	}))
	defer ts.Close()

	if err := testFwd(ts).Forward(context.Background(), ts.URL, Forward{MessageID: "m"}); err != nil {
		t.Fatalf("Forward should succeed on 3rd try: %v", err)
	}
	if atomic.LoadInt32(&n) != 3 {
		t.Errorf("attempts = %d, want 3", n)
	}
}

func TestForwardPermanentNoRetry(t *testing.T) {
	var n int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&n, 1)
		w.WriteHeader(400) // permanent
	}))
	defer ts.Close()

	if err := testFwd(ts).Forward(context.Background(), ts.URL, Forward{MessageID: "m"}); err == nil {
		t.Fatal("expected error on 4xx")
	}
	if atomic.LoadInt32(&n) != 1 {
		t.Errorf("4xx must not retry; attempts = %d", n)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// A consumer whose credential hasn't loaded returns 401/403. That is a config
// fault, not a content fault: the payload is valid and would land a minute
// later. Treating it as permanent silently destroyed the message.
func TestForwardRetriesTransientClientErrors(t *testing.T) {
	for _, code := range []int{401, 403, 408, 429} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			var calls int32
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if atomic.AddInt32(&calls, 1) < 3 {
					w.WriteHeader(code)
					return
				}
				w.WriteHeader(200)
			}))
			defer ts.Close()

			if err := testFwd(ts).Forward(context.Background(), ts.URL, Forward{MessageID: "m1"}); err != nil {
				t.Fatalf("Forward: %v, want success after retries", err)
			}
			if got := atomic.LoadInt32(&calls); got != 3 {
				t.Errorf("calls = %d, want 3 (retried then succeeded)", got)
			}
		})
	}
}

// Content faults must still be permanent — retrying a malformed payload just
// wastes attempts on something that will never succeed.
func TestForwardStillPermanentOnContentFaults(t *testing.T) {
	for _, code := range []int{400, 404, 422} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			var calls int32
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&calls, 1)
				w.WriteHeader(code)
			}))
			defer ts.Close()

			if err := testFwd(ts).Forward(context.Background(), ts.URL, Forward{MessageID: "m1"}); err == nil {
				t.Fatal("Forward succeeded, want permanent error")
			}
			if got := atomic.LoadInt32(&calls); got != 1 {
				t.Errorf("calls = %d, want 1 (no retry on a content fault)", got)
			}
		})
	}
}
