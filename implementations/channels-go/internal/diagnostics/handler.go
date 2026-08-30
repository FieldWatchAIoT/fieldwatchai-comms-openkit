package diagnostics

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
)

type serviceAPI interface {
	Run(ctx context.Context, tenantID uuid.UUID) (Report, error)
}

// Handler serves GET /v1/diagnostics.
type Handler struct {
	svc    serviceAPI
	logger *slog.Logger
}

// NewHandler constructs a diagnostics HTTP handler.
func NewHandler(svc serviceAPI, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, logger: logger}
}

// RegisterRoutes mounts the route on mux. Auth is applied by the caller.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/diagnostics", h.run)
}

// run always returns 200 when it can compute a report, including when that
// report is full of blocking findings. The findings are the payload; a non-2xx
// would make a misconfigured tenant indistinguishable from a broken service,
// which is the confusion this endpoint exists to end.
func (h *Handler) run(w http.ResponseWriter, r *http.Request) {
	raw := r.Header.Get("X-Tenant-ID")
	tenant, err := uuid.Parse(raw)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing_or_invalid_tenant"})
		return
	}
	rep, err := h.svc.Run(r.Context(), tenant)
	if err != nil {
		h.logger.Error("diagnostics failed", "event", "diagnostics.error", "error", err.Error())
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
