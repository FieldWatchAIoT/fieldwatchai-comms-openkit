package httpapi

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

// requestIDKey is the context key for the per-request correlation ID. It is
// intentionally unexported and typed via an empty struct so no third party
// can accidentally collide on the same context value.
type requestIDKey struct{}

const requestIDHeader = "X-Request-Id"

// WithRequestID is middleware that ensures every request carries a
// correlation ID in its context. A caller-supplied X-Request-Id (e.g. from an
// upstream load balancer) is preserved; otherwise a UUID is minted. The ID is
// echoed back in the response header so the caller can correlate too.
func WithRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(requestIDHeader)
		if id == "" {
			id = uuid.NewString()
		}
		w.Header().Set(requestIDHeader, id)
		ctx := context.WithValue(r.Context(), requestIDKey{}, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequestIDFromContext returns the correlation ID set by WithRequestID, or ""
// if the middleware did not run (e.g. a unit test that bypasses it).
func RequestIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey{}).(string); ok {
		return v
	}
	return ""
}
