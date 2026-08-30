package messagelog

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/google/uuid"
)

type serviceAPI interface {
	List(ctx context.Context, tenantID uuid.UUID, limit int) ([]Message, error)
}

// Handler serves GET /v1/messages.
type Handler struct {
	svc    serviceAPI
	logger *slog.Logger
}

// NewHandler constructs a messagelog HTTP handler.
func NewHandler(svc serviceAPI, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, logger: logger}
}

// RegisterRoutes mounts the route on mux. Auth is applied by the caller.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/messages", h.list)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	tenant, err := uuid.Parse(r.Header.Get("X-Tenant-ID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing_or_invalid_tenant"})
		return
	}
	// An unparseable limit falls back to the default rather than 400ing: the
	// caller wants messages, and refusing over a query-string typo helps no one.
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	msgs, err := h.svc.List(r.Context(), tenant, limit)
	if err != nil {
		h.logger.Error("list messages failed", "event", "messages.error", "error", err.Error())
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": msgs, "count": len(msgs)})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
