// Package telegram implements the Outbound adapter for Telegram via the Bot API —
// the third platform the comms hub can send over (alongside ultramsg + twilio).
// Per-bot token model (like UltraMSG's instance token): the account's decrypted
// credential IS the bot token, and `to` is the Telegram chat_id.
//
// Send: POST https://api.telegram.org/bot<token>/sendMessage
//
//	form: chat_id=<to>, text=<body>  ->  {"ok":true,"result":{"message_id":N}}
//
// (Inbound media file_id resolution via getFile is a separate concern handled in
// the ingest path, not this Outbound adapter.)
package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/outbound"
)

const defaultBaseURL = "https://api.telegram.org"

// Sender sends Telegram messages via the Bot API. BaseURL + Client are exported
// so tests can point at an httptest server.
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

// tgResp captures what we need from a Bot API response.
type tgResp struct {
	OK     bool `json:"ok"`
	Result struct {
		MessageID int64 `json:"message_id"`
	} `json:"result"`
	ErrorCode   int    `json:"error_code"`
	Description string `json:"description"`
}

// Send posts a message via the bot's sendMessage and returns the message id.
// acct.Token is the bot token; `to` is the Telegram chat_id.
func (s *Sender) Send(ctx context.Context, acct outbound.Account, to, body string) (string, error) {
	if acct.Token == "" {
		return "", fmt.Errorf("telegram send: missing bot token")
	}
	base := s.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	endpoint := strings.TrimRight(base, "/") + "/bot" + acct.Token + "/sendMessage"

	form := url.Values{"chat_id": {to}, "text": {body}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("telegram send: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.Client.Do(req)
	if err != nil {
		// the URL embeds the bot token — scrub it from transport errors.
		return "", fmt.Errorf("telegram send: %s", scrub(err.Error(), acct.Token))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	var r tgResp
	_ = json.Unmarshal(raw, &r)
	if !r.OK {
		return "", fmt.Errorf("telegram send: status %d: code %d: %s", resp.StatusCode, r.ErrorCode, strings.TrimSpace(r.Description))
	}
	if r.Result.MessageID == 0 {
		return "", fmt.Errorf("telegram send: no message_id in response")
	}
	return strconv.FormatInt(r.Result.MessageID, 10), nil
}

// scrub removes the bot token from a string (transport errors embed the URL).
func scrub(s, token string) string {
	if token == "" {
		return s
	}
	return strings.ReplaceAll(s, token, "<token>")
}
