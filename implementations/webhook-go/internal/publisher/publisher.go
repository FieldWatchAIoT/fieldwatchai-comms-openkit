// Package publisher forwards canonical messages to fieldwatchai-comms-channels.
// It POSTs to {CHANNELS_URL}/v1/ingest with a bearer token and an
// Idempotency-Key (the platform message id) so redelivery from the queue is
// safe. Failures are classified as permanent (4xx — the drain worker should not
// keep retrying) or transient (5xx/429/network — retry).
package publisher

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/canonical"
)

// Publisher forwards a canonical message downstream.
type Publisher interface {
	Publish(ctx context.Context, msg canonical.Message) error
}

// PermanentError indicates channels rejected the message in a way retrying
// will not fix (a 4xx). Transient failures are returned as plain errors.
type PermanentError struct{ Status int }

func (e *PermanentError) Error() string {
	return fmt.Sprintf("channels rejected message permanently: HTTP %d", e.Status)
}

// IsPermanent reports whether err is a PermanentError.
func IsPermanent(err error) bool {
	var pe *PermanentError
	return errors.As(err, &pe)
}

// HTTP is the HTTP-backed Publisher.
type HTTP struct {
	client    *http.Client
	ingestURL string
	token     string
}

// NewHTTP builds a publisher targeting baseURL (channels root). A nil client
// uses http.DefaultClient.
func NewHTTP(baseURL, token string, client *http.Client) *HTTP {
	if client == nil {
		client = http.DefaultClient
	}
	return &HTTP{
		client:    client,
		ingestURL: strings.TrimRight(baseURL, "/") + "/v1/ingest",
		token:     token,
	}
}

// Publish POSTs the canonical message to channels' ingest endpoint.
func (p *HTTP) Publish(ctx context.Context, msg canonical.Message) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal canonical message: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.ingestURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build ingest request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Idempotency-Key", msg.Meta.PlatformMessageID)

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("post ingest: %w", err) // network failure — transient
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body) // drain so the connection can be reused
		_ = resp.Body.Close()
	}()

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusTooManyRequests:
		return fmt.Errorf("channels throttled: HTTP %d", resp.StatusCode) // transient
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		return &PermanentError{Status: resp.StatusCode}
	default:
		return fmt.Errorf("channels server error: HTTP %d", resp.StatusCode) // 5xx — transient
	}
}
