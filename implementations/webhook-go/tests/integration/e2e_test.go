// Package integration exercises the full inbound pipe end-to-end with no
// external dependencies: a real HTTP server wired with the in-memory queue, the
// UltraMSG listener, the drain worker, and a fake comms-channels server. It
// proves an inbound webhook lands at channels as a canonical message with the
// idempotency key — neither AWS nor the real channels repo required.
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/accounts"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/canonical"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/drain"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/httpapi"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/listeners"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/listeners/ultramsg"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/publisher"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/queue"
)

const (
	webhookSecret = "e2e-secret"
	fixturePath   = "../../internal/listeners/ultramsg/testdata/text.json"
)

func TestEndToEnd_UltraMSGWebhookReachesChannels(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Fake comms-channels: capture what the publisher sends.
	type received struct {
		idem string
		body []byte
	}
	got := make(chan received, 1)
	channels := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/ingest" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		b, _ := io.ReadAll(r.Body)
		got <- received{idem: r.Header.Get("Idempotency-Key"), body: b}
		w.WriteHeader(http.StatusCreated)
	}))
	defer channels.Close()

	// Wire the pipe.
	resolver, err := accounts.NewConfigResolver(`{"whatsapp-ultramsg":{"instance123":"acc_e2e"}}`)
	if err != nil {
		t.Fatalf("resolver: %v", err)
	}
	q := queue.NewMemory()
	pub := publisher.NewHTTP(channels.URL, "internal-token", channels.Client())
	um := ultramsg.New(webhookSecret, resolver, logger)

	srv := httpapi.NewServer(logger)
	srv.MaxBodyBytes = 256 * 1024
	listeners.Register(srv.Mux(), q, logger, um)
	srv.MarkReady()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker := drain.New(q, pub, logger, 2)
	go func() { _ = worker.Run(ctx) }()

	front := httptest.NewServer(srv.Routes())
	defer front.Close()

	// Send a real captured UltraMSG fixture.
	body, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	resp, err := front.Client().Post(
		front.URL+"/inbound/whatsapp-ultramsg?token="+webhookSecret,
		"application/json", bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("POST webhook: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("webhook status = %d, want 200", resp.StatusCode)
	}

	// The drain worker should forward it to channels.
	select {
	case r := <-got:
		if r.idem != "false_12425550042@c.us_3EB0TEXT" {
			t.Errorf("Idempotency-Key = %q", r.idem)
		}
		var m canonical.Message
		if err := json.Unmarshal(r.body, &m); err != nil {
			t.Fatalf("channels body not canonical JSON: %v", err)
		}
		if m.Sender.Endpoint != "+12425550042" {
			t.Errorf("endpoint = %q, want +12425550042", m.Sender.Endpoint)
		}
		if m.Sender.Platform != "whatsapp" {
			t.Errorf("platform = %q, want whatsapp", m.Sender.Platform)
		}
		if m.Meta.AccountID != "acc_e2e" {
			t.Errorf("account_id = %q, want acc_e2e", m.Meta.AccountID)
		}
		if m.Body.Text == nil || *m.Body.Text != "42 STATUS full" {
			t.Errorf("text = %v, want '42 STATUS full'", m.Body.Text)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("channels never received the message within 5s")
	}
}

// TestEndToEnd_BadTokenRejected confirms a wrong webhook token is rejected with
// 401 and nothing is forwarded.
func TestEndToEnd_BadTokenRejected(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	resolver, _ := accounts.NewConfigResolver(`{"whatsapp-ultramsg":{"instance123":"acc_e2e"}}`)
	q := queue.NewMemory()
	um := ultramsg.New(webhookSecret, resolver, logger)
	srv := httpapi.NewServer(logger)
	listeners.Register(srv.Mux(), q, logger, um)
	srv.MarkReady()
	front := httptest.NewServer(srv.Routes())
	defer front.Close()

	body, _ := os.ReadFile(fixturePath)
	resp, err := front.Client().Post(
		front.URL+"/inbound/whatsapp-ultramsg?token=WRONG",
		"application/json", bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	if ds, _ := q.Receive(context.Background()); len(ds) != 0 {
		t.Errorf("rejected webhook still enqueued %d messages", len(ds))
	}
}
