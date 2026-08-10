package httpapi

import (
	"log/slog"
	"net/http"
)

// WithRecover is middleware that catches a panic from any downstream handler,
// logs it, and returns a 500 instead of letting the panic crash the process.
//
// This is the core availability guarantee for the receiver: a single
// malformed inbound payload that trips a parser bug must never take the
// server down. The correlation ID (if WithRequestID ran first) is included
// in the log line so the failure can be traced.
func WithRecover(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				logger.Error("panic recovered",
					"event", "http.panic",
					"request_id", RequestIDFromContext(r.Context()),
					"method", r.Method,
					"path", r.URL.Path,
					"panic", rec,
				)
				writeJSON(w, http.StatusInternalServerError, map[string]any{"status": "internal_error"})
			}
		}()
		next.ServeHTTP(w, r)
	})
}
