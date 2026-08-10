// Package ultramsg implements the Outbound adapter for WhatsApp via the
// UltraMSG gateway — the only platform the comms hub can currently send over.
//
// Send: POST https://api.ultramsg.com/<instance>/messages/chat
//
//	form: token=<instance token>, to=<endpoint>, body=<text>
//
// The instance id is the account identifier; the token is the account's
// decrypted credential (the dispatcher decrypts before calling Send).
package ultramsg

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/outbound"
)

const defaultBaseURL = "https://api.ultramsg.com"

// Sender sends WhatsApp messages via UltraMSG. BaseURL + Client are exported so
// tests can point at an httptest server.
type Sender struct {
	Client  *http.Client
	BaseURL string
}

// New returns a Sender with sane defaults.
func New() *Sender {
	return &Sender{Client: &http.Client{Timeout: 15 * time.Second}, BaseURL: defaultBaseURL}
}

// Compile-time guard.
var _ outbound.Outbound = (*Sender)(nil)

// ultraResp is UltraMSG's chat response. Fields are RawMessage because the API
// returns sent/id as either strings or non-strings depending on the path.
type ultraResp struct {
	Sent    json.RawMessage `json:"sent"`
	ID      json.RawMessage `json:"id"`
	Message string          `json:"message"`
	Error   json.RawMessage `json:"error"`
}

// Send posts a chat message and returns the UltraMSG message id.
func (s *Sender) Send(ctx context.Context, acct outbound.Account, to, body string) (string, error) {
	base := s.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	// The account identifier is the bare numeric instance id (from the webhook
	// payload's data.instanceId, used for account lookup), but UltraMSG's API
	// path uses the "instance<numeric>" form. Prefix it unless already prefixed.
	instance := acct.Identifier
	if !strings.HasPrefix(instance, "instance") {
		instance = "instance" + instance
	}
	endpoint := strings.TrimRight(base, "/") + "/" + instance + "/messages/chat"

	form := url.Values{"token": {acct.Token}, "to": {to}, "body": {body}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("ultramsg send: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("ultramsg send: status %d: %s", resp.StatusCode, raw)
	}

	var r ultraResp
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", fmt.Errorf("ultramsg send: decode response: %w (%s)", err, raw)
	}
	if !strings.Contains(strings.ToLower(string(r.Sent)), "true") {
		return "", fmt.Errorf("ultramsg send not accepted: sent=%s error=%s", r.Sent, r.Error)
	}
	return strings.Trim(string(r.ID), `"`), nil
}
