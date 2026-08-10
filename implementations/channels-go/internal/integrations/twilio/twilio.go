// Package twilio implements the Outbound adapter for WhatsApp via Twilio — the
// second platform the comms hub can send over (alongside ultramsg). Inbound
// Twilio WhatsApp is received by comms-webhook and resolved to a whatsapp-twilio
// account; this adapter closes the loop for echo/replies.
//
// Send: POST https://api.twilio.com/2010-04-01/Accounts/<AccountSid>/Messages.json
//
//	basic auth: AccountSid:AuthToken
//	form: From=whatsapp:<account identifier>, To=whatsapp:<endpoint>, Body=<text>
//
// The account identifier is the Twilio WhatsApp number (the From). The decrypted
// credential is a JSON blob {"account_sid","auth_token"} — Twilio needs both the
// SID (in the URL + basic-auth user) and the token (basic-auth password), so a
// single opaque token (as UltraMSG uses) isn't enough.
package twilio

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

const defaultBaseURL = "https://api.twilio.com"

// Sender sends WhatsApp messages via Twilio. BaseURL + Client are exported so
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

// credentials is the decrypted credential shape for a whatsapp-twilio account.
type credentials struct {
	AccountSID string `json:"account_sid"`
	AuthToken  string `json:"auth_token"`
}

// twilioResp captures the fields we need from Twilio's Messages response: sid on
// success, code+message on error.
type twilioResp struct {
	SID     string `json:"sid"`
	Status  string `json:"status"`
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Send posts a WhatsApp message via Twilio and returns the message SID.
func (s *Sender) Send(ctx context.Context, acct outbound.Account, to, body string) (string, error) {
	var c credentials
	if err := json.Unmarshal([]byte(acct.Token), &c); err != nil {
		return "", fmt.Errorf("twilio send: parse credentials: %w", err)
	}
	if c.AccountSID == "" || c.AuthToken == "" {
		return "", fmt.Errorf("twilio send: credentials missing account_sid or auth_token")
	}

	base := s.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	endpoint := strings.TrimRight(base, "/") + "/2010-04-01/Accounts/" + c.AccountSID + "/Messages.json"

	form := url.Values{
		"From": {twilioAddr(acct.Type, acct.Identifier)},
		"To":   {twilioAddr(acct.Type, to)},
		"Body": {body},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("twilio send: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(c.AccountSID, c.AuthToken)

	resp, err := s.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("twilio send: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	var r twilioResp
	_ = json.Unmarshal(raw, &r) // best-effort; raw is included in errors below

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if r.Message != "" {
			return "", fmt.Errorf("twilio send: status %d: code %d: %s", resp.StatusCode, r.Code, r.Message)
		}
		return "", fmt.Errorf("twilio send: status %d: %s", resp.StatusCode, raw)
	}
	if r.SID == "" {
		return "", fmt.Errorf("twilio send: no message sid in response: %s", raw)
	}
	return r.SID, nil
}

// twilioAddr formats an address for the account's channel: SMS (sms-twilio)
// sends a bare +E.164; WhatsApp (whatsapp-twilio, and the default) sends
// "whatsapp:<e164>". The same Twilio adapter serves both — the account type
// selects the scheme. channels stores the bare +E.164 endpoint, but any
// incoming "whatsapp:" prefix is normalized first so the scheme is consistent.
func twilioAddr(acctType, addr string) string {
	bare := strings.TrimPrefix(addr, "whatsapp:")
	if strings.Contains(acctType, "sms") {
		return bare
	}
	return "whatsapp:" + bare
}
