package ultramsg

import (
	"crypto/subtle"
	"errors"
	"net/http"
)

var (
	errInvalidToken = errors.New("ultramsg: invalid webhook token")
	errNoSecret     = errors.New("ultramsg: webhook secret not configured")
)

// Verify authenticates an inbound UltraMSG webhook. UltraMSG sends no signature
// and cannot add custom headers, so we require a shared secret in the URL query
// (?token=...) and compare it in constant time. A listener with no configured
// secret fails closed. The error never contains the secret value.
func (l *Listener) Verify(r *http.Request, _ []byte) error {
	if l.secret == "" {
		return errNoSecret
	}
	token := r.URL.Query().Get("token")
	if subtle.ConstantTimeCompare([]byte(token), []byte(l.secret)) != 1 {
		return errInvalidToken
	}
	return nil
}
