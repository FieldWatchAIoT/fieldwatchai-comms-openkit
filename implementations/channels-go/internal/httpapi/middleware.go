package httpapi

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"net/http"
)

type ctxKey int

const requestIDKey ctxKey = iota

// RequestIDHeader is the canonical header for request correlation.
const RequestIDHeader = "X-Request-Id"

// WithRequestID mints a request id when absent, stores it in the context, and
// echoes it back in the response header.
func WithRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if id == "" {
			id = newRequestID()
		}
		w.Header().Set(RequestIDHeader, id)
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequestIDFromContext returns the request id, or "" if none was set.
func RequestIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey).(string); ok {
		return id
	}
	return ""
}

// WithRecover converts panics into 500 responses and logs them with the
// request id for correlation.
func WithRecover(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				logger.Error("recovered from panic",
					"event", "http.panic",
					"request_id", RequestIDFromContext(r.Context()),
					"panic", fmt.Sprint(rec),
					"method", r.Method,
					"path", r.URL.Path,
				)
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// newRequestID returns a random UUIDv4-formatted string using crypto/rand,
// avoiding an external dependency so offline builds keep working.
func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// rand.Read never returns an error on supported platforms; fall back
		// to a fixed marker rather than panicking in the request path.
		return "00000000-0000-0000-0000-000000000000"
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
