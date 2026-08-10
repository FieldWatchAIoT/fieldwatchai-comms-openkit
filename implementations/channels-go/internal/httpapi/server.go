// Package httpapi provides the HTTP server, middleware, and health endpoint for
// the comms-channels service. It mirrors the conventions proven in
// fieldwatchai-comms-webhook: stdlib net/http + ServeMux, a recover/request-id
// middleware chain, and a readiness flag toggled by the composition root.
package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync/atomic"
)

// Server holds the mux, logger, and readiness state. Register routes via Mux(),
// then serve Routes() which wraps the mux with middleware.
type Server struct {
	logger  *slog.Logger
	mux     *http.ServeMux
	ready   atomic.Bool
	started bool

	// MaxBodyBytes caps request bodies when > 0. Handlers opt in via LimitBody.
	MaxBodyBytes int64
}

// NewServer constructs a Server with the health endpoint registered.
func NewServer(logger *slog.Logger) *Server {
	s := &Server{logger: logger, mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	return s
}

// Mux exposes the underlying mux for route registration.
func (s *Server) Mux() *http.ServeMux { return s.mux }

// Routes returns the fully wrapped handler (outermost: recover, then request-id).
func (s *Server) Routes() http.Handler {
	return WithRecover(s.logger, WithRequestID(s.mux))
}

// MarkReady flips the readiness flag so /healthz returns 200.
func (s *Server) MarkReady() { s.ready.Store(true) }

// MarkNotReady flips the readiness flag so the load balancer drains traffic.
func (s *Server) MarkNotReady() { s.ready.Store(false) }

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	if s.ready.Load() {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
		return
	}
	writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "unavailable"})
}

// writeJSON writes v as a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
