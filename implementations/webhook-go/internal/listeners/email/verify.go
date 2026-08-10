package email

import (
	"crypto/subtle"
	"errors"
	"net/http"
)

var (
	errInvalidToken = errors.New("email-ses: invalid token")
	errNoSecret     = errors.New("email-ses: webhook secret not configured")
)

// Verify authenticates the SNS delivery by the shared URL token (?token=),
// constant-time compared, fail-closed. SNS can't add custom headers to its HTTP
// deliveries, so — as with UltraMSG — the secret rides in the subscription URL.
// (The TopicArn allowlist is enforced in Parse, where the envelope is decoded.)
//
// Hardening TODO: verify the SNS message signature (SigningCertURL + Signature)
// in addition to the token.
func (l *Listener) Verify(r *http.Request, _ []byte) error {
	if l.secret == "" {
		return errNoSecret
	}
	if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("token")), []byte(l.secret)) != 1 {
		return errInvalidToken
	}
	return nil
}
