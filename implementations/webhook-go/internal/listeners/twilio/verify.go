package twilio

import (
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // Twilio mandates HMAC-SHA1 for X-Twilio-Signature
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

var (
	errInvalidSignature = errors.New("twilio: invalid X-Twilio-Signature")
	errNoSignature      = errors.New("twilio: missing X-Twilio-Signature header")
	errNoAuthToken      = errors.New("twilio: auth token not configured")
)

// Verify authenticates an inbound Twilio webhook via X-Twilio-Signature. Per
// Twilio's spec the signature is base64(HMAC-SHA1(authToken, S)), where S is the
// full public request URL followed by every POST parameter — sorted by key —
// appended as key then value with no delimiters. Returns nil when valid; a
// non-nil error (→ 401) otherwise. Fails closed and compares in constant time.
// No error contains the secret.
func (l *Listener) Verify(r *http.Request, body []byte) error {
	if l.authToken == "" {
		return errNoAuthToken
	}
	sig := r.Header.Get("X-Twilio-Signature")
	if sig == "" {
		return errNoSignature
	}

	// Behind the ALB the app sees an internal host, so sign against the
	// configured public origin + the request URI (path[+query]) Twilio used.
	signed := strings.TrimRight(l.publicBaseURL, "/") + r.URL.RequestURI()

	vals, err := url.ParseQuery(string(body))
	if err != nil {
		return errInvalidSignature
	}
	keys := make([]string, 0, len(vals))
	for k := range vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	sb.WriteString(signed)
	for _, k := range keys {
		sb.WriteString(k)
		for _, v := range vals[k] {
			sb.WriteString(v)
		}
	}

	mac := hmac.New(sha1.New, []byte(l.authToken))
	mac.Write([]byte(sb.String()))
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(sig)) {
		return errInvalidSignature
	}
	return nil
}
