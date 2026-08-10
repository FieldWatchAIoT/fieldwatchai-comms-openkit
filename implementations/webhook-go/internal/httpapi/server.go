// Package httpapi is the inbound HTTP surface for the comms webhook receiver:
// the Server, its middleware (recover, request-id, body-cap), and the health
// endpoint. Per-platform listeners register their routes onto the Server's mux
// (see the listeners package); this package itself knows nothing about any
// specific platform.
package httpapi

import (
	"log/slog"
	"net/http"
	"sync/atomic"
)

// Server holds the receiver's HTTP wiring and shared configuration.
//
// The zero value is not usable — construct one with NewServer.
type Server struct {
	logger *slog.Logger

	// MaxBodyBytes caps the size of an inbound request body before parsing.
	// Zero means "no cap"; callers should set it from config at boot.
	MaxBodyBytes int64

	// ready reflects load-balancer readiness. It starts false so an instance
	// does not receive traffic until boot wiring completes (MarkReady), and is
	// flipped back to false on shutdown (MarkNotReady) so the LB drains it.
	ready atomic.Bool

	// serveMux holds the route table. Listeners register their inbound paths
	// onto it (see the listeners package); Routes wraps it in the global
	// middleware chain.
	serveMux *http.ServeMux
}

// NewServer constructs a Server with the given logger and its base routes.
func NewServer(logger *slog.Logger) *Server {
	s := &Server{logger: logger, serveMux: http.NewServeMux()}
	s.serveMux.HandleFunc("/healthz", s.handleHealthz)
	return s
}

// Mux exposes the route table so listeners can register their inbound handlers
// (called by the composition root in cmd/server before Routes).
func (s *Server) Mux() *http.ServeMux { return s.serveMux }

// MarkReady flips the readiness flag on so /healthz reports healthy.
func (s *Server) MarkReady() { s.ready.Store(true) }

// MarkNotReady flips the readiness flag off so /healthz fails the health
// check and the load balancer stops routing new traffic to this instance.
func (s *Server) MarkNotReady() { s.ready.Store(false) }

// Routes returns the fully-wired HTTP handler: the route table wrapped in the
// global middleware chain. Recover is outermost so it catches panics from any
// inner layer; request-id runs before the routes so the correlation ID is in
// context for both handlers and the recover log line.
func (s *Server) Routes() http.Handler {
	return WithRecover(s.logger, WithRequestID(s.serveMux))
}
