package telegram

import (
	"crypto/subtle"
	"errors"
	"net/http"
)

var (
	errInvalidSecret = errors.New("telegram: invalid secret token")
	errNoSecret      = errors.New("telegram: webhook secret not configured")
)

// Verify authenticates an inbound Telegram webhook. Telegram echoes the
// secret_token set on setWebhook back in the X-Telegram-Bot-Api-Secret-Token
// header; we constant-time compare it to the configured shared secret. A
// listener with no secret fails closed. The error never contains the secret.
func (l *Listener) Verify(r *http.Request, _ []byte) error {
	if l.secret == "" {
		return errNoSecret
	}
	got := r.Header.Get("X-Telegram-Bot-Api-Secret-Token")
	if subtle.ConstantTimeCompare([]byte(got), []byte(l.secret)) != 1 {
		return errInvalidSecret
	}
	return nil
}
