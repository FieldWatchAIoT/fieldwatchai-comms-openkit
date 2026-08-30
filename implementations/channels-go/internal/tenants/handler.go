package tenants

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
)

type serviceAPI interface {
	Create(ctx context.Context, in CreateInput, now func() time.Time, newID func() uuid.UUID) (Tenant, error)
	Get(ctx context.Context, id uuid.UUID) (Tenant, error)
	List(ctx context.Context) ([]Tenant, error)
}

// Handler serves the /v1/tenants routes.
type Handler struct {
	svc    serviceAPI
	logger *slog.Logger
	now    func() time.Time
	newID  func() uuid.UUID
}

// NewHandler constructs a tenants HTTP handler.
func NewHandler(svc serviceAPI, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, logger: logger, now: time.Now, newID: uuid.New}
}

// RegisterRoutes mounts the routes on mux. Auth is applied by the caller.
// These take no X-Tenant-ID — they are what a caller uses before they have one.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/tenants", h.list)
	mux.HandleFunc("POST /v1/tenants", h.create)
	mux.HandleFunc("GET /v1/tenants/{id}", h.get)
}

type createRequest struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Plan     string          `json:"plan"`
	Settings json.RawMessage `json:"settings"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json"})
		return
	}
	in := CreateInput{Name: req.Name, Plan: req.Plan, Settings: req.Settings}
	if req.ID != "" {
		id, err := uuid.Parse(req.ID)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid", "detail": "id must be a UUID"})
			return
		}
		in.ID = &id
	}
	t, err := h.svc.Create(r.Context(), in, h.now, h.newID)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_id"})
		return
	}
	t, err := h.svc.Get(r.Context(), id)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	ts, err := h.svc.List(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ts)
}

func (h *Handler) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
	case errors.Is(err, ErrInvalid):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid", "detail": err.Error()})
	default:
		h.logger.Error("tenants handler error", "event", "tenants.error", "error", err.Error())
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
