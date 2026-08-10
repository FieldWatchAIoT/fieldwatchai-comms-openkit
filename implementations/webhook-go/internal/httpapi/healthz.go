package httpapi

import "net/http"

// handleHealthz is the load-balancer health check. It reports 200 once the
// server is ready and 503 otherwise (pre-boot or during graceful shutdown),
// which is what lets the LB drain an instance cleanly on SIGTERM.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if s.ready.Load() {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
		return
	}
	writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "unavailable"})
}
