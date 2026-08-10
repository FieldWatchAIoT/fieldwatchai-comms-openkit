package httpapi

import (
	"crypto/subtle"
	"log/slog"
	"net/http"
	"strings"
)

const bearerPrefix = "Bearer "

// WithBearerAuth guards next with a shared bearer token, compared in constant
// time. Used for internal endpoints (/v1/ingest, /v1/accounts/lookup). It is a
// swappable auth seam — user-facing endpoints will later substitute a JWT
// middleware behind the same shape.
func WithBearerAuth(token string, logger *slog.Logger, next http.Handler) http.Handler {
	want := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		if !strings.HasPrefix(h, bearerPrefix) {
			reject(w, logger, r, "missing_bearer")
			return
		}
		got := []byte(strings.TrimPrefix(h, bearerPrefix))
		if subtle.ConstantTimeCompare(got, want) != 1 {
			reject(w, logger, r, "bad_token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func reject(w http.ResponseWriter, logger *slog.Logger, r *http.Request, reason string) {
	logger.Warn("auth rejected",
		"event", "auth.rejected",
		"request_id", RequestIDFromContext(r.Context()),
		"reason", reason,
		"path", r.URL.Path,
	)
	writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
}
