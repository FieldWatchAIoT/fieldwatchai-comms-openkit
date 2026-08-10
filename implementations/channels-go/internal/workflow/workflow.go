// Package workflow forwards routed messages to consumer products' webhooks —
// the channels → product half of the integration contract. Consumers own the
// domain logic and reply via POST /v1/outbound; channels never embeds product
// logic. New consumers are registered as data (channel workflow_url / workflows
// rows), not code.
package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Forward is the payload channels POSTs to a consumer webhook (the contract).
type Forward struct {
	MessageID string `json:"message_id"`
	TenantID  string `json:"tenant_id"`
	ChannelID string `json:"channel_id,omitempty"`
	AccountID string `json:"account_id"`
	// AccountType is the hub's account type verbatim ("sms-twilio",
	// "whatsapp-twilio", "telegram"…) — provider included. Platform is that
	// normalised to the transport ("sms", "whatsapp", "telegram", "email").
	// Both are sent because account_id alone cannot disambiguate them: one
	// number can serve SMS and WhatsApp, so the same sender_endpoint arrives
	// over two transports. Without these a consumer must call /v1/accounts just
	// to label a message, which needs hub credentials it may not have.
	AccountType    string          `json:"account_type"`
	Platform       string          `json:"platform"`
	ContactID      string          `json:"contact_id,omitempty"`
	SenderEndpoint string          `json:"sender_endpoint"`
	Command        string          `json:"command,omitempty"`
	Payload        string          `json:"payload,omitempty"`
	Text           string          `json:"text"`
	Attachments    json.RawMessage `json:"attachments,omitempty"`
	Location       json.RawMessage `json:"location,omitempty"`
	ReceivedAt     string          `json:"received_at"`
}

// Forwarder delivers Forward payloads to consumer webhooks with bounded retry.
type Forwarder struct {
	Client   *http.Client
	Attempts int           // total tries (>=1); default 3
	Backoff  time.Duration // base backoff; default 500ms
}

// New returns a Forwarder with sane defaults.
func New() *Forwarder {
	return &Forwarder{Client: &http.Client{Timeout: 15 * time.Second}, Attempts: 3, Backoff: 500 * time.Millisecond}
}

// permanent marks a non-retryable (4xx) consumer response.
type permanent struct{ status int }

func (e *permanent) Error() string { return fmt.Sprintf("consumer rejected: HTTP %d", e.status) }

// retryableClientError reports whether a 4xx should be retried anyway.
//
// Treating every 4xx as permanent conflates two different failures. A 400 or a
// 422 is a content fault: the payload is wrong and will be wrong again, so
// retrying is pointless. But 401 and 403 are configuration faults — the payload
// is perfectly valid and would land a minute later once the consumer's
// credential loads. Dropping it means a consumer's momentary misconfiguration
// silently destroys messages instead of delaying them. 408 and 429 are
// transient by definition.
func retryableClientError(code int) bool {
	switch code {
	case http.StatusUnauthorized, http.StatusForbidden,
		http.StatusRequestTimeout, http.StatusTooManyRequests:
		return true
	}
	return false
}

// Forward POSTs f to url. 2xx = ok; 4xx = permanent (no retry); 5xx/timeout =
// retried up to Attempts with linear backoff. sleep is injectable for tests.
func (fw *Forwarder) Forward(ctx context.Context, url string, f Forward) error {
	body, err := json.Marshal(f)
	if err != nil {
		return fmt.Errorf("marshal forward: %w", err)
	}
	attempts := fw.Attempts
	if attempts < 1 {
		attempts = 1
	}

	var last error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(i) * fw.Backoff):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("build forward request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := fw.Client.Do(req)
		if err != nil {
			last = fmt.Errorf("forward: %w", err) // transient — retry
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		_ = resp.Body.Close()

		switch {
		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			return nil
		case retryableClientError(resp.StatusCode):
			last = fmt.Errorf("forward: consumer status %d", resp.StatusCode) // retry
		case resp.StatusCode >= 400 && resp.StatusCode < 500:
			return &permanent{status: resp.StatusCode} // don't retry
		default:
			last = fmt.Errorf("forward: consumer status %d", resp.StatusCode) // retry
		}
	}
	return last
}
