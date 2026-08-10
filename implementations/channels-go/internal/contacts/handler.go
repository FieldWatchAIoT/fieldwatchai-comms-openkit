package contacts

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
)

// serviceAPI is the handler's view of the service (for testability).
type serviceAPI interface {
	Create(ctx context.Context, in CreateInput) (Contact, error)
	Get(ctx context.Context, id, tenantID uuid.UUID) (Contact, error)
	List(ctx context.Context, tenantID uuid.UUID) ([]Contact, error)
	Update(ctx context.Context, id, tenantID uuid.UUID, in UpdateInput) (Contact, error)
	Delete(ctx context.Context, id, tenantID uuid.UUID) error
	ShortIDCheck(ctx context.Context, tenantID uuid.UUID, shortID string) (ShortIDCheckResult, error)
	BulkImport(ctx context.Context, tenantID uuid.UUID, r io.Reader) (BulkResult, error)
}

// Handler serves the /v1/contacts endpoints.
type Handler struct {
	svc    serviceAPI
	logger *slog.Logger
}

// NewHandler constructs a contacts HTTP handler.
func NewHandler(svc serviceAPI, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, logger: logger}
}

// RegisterRoutes mounts the contacts routes; auth is applied by the caller.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/contacts/short_id_check", h.shortIDCheck)
	mux.HandleFunc("POST /v1/contacts/bulk-import", h.bulkImport)
	mux.HandleFunc("GET /v1/contacts", h.list)
	mux.HandleFunc("POST /v1/contacts", h.create)
	mux.HandleFunc("PATCH /v1/contacts/{id}", h.update)
	mux.HandleFunc("DELETE /v1/contacts/{id}", h.delete)
}

type createRequest struct {
	ShortID     string          `json:"short_id"`
	DisplayName string          `json:"display_name"`
	AOIID       *string         `json:"aoi_id"`
	Role        *string         `json:"role"`
	Status      string          `json:"status"`
	Metadata    json.RawMessage `json:"metadata"`
}

type updateRequest struct {
	DisplayName *string         `json:"display_name"`
	AOIID       *string         `json:"aoi_id"`
	Role        *string         `json:"role"`
	Status      *string         `json:"status"`
	Metadata    json.RawMessage `json:"metadata"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	tenant, ok := tenantOf(w, r)
	if !ok {
		return
	}
	var req createRequest
	if !decode(w, r, &req) {
		return
	}
	c, err := h.svc.Create(r.Context(), CreateInput{
		TenantID: tenant, ShortID: req.ShortID, DisplayName: req.DisplayName,
		AOIID: req.AOIID, Role: req.Role, Status: req.Status, Metadata: req.Metadata,
	})
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	tenant, ok := tenantOf(w, r)
	if !ok {
		return
	}
	cs, err := h.svc.List(r.Context(), tenant)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, cs)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	tenant, ok := tenantOf(w, r)
	if !ok {
		return
	}
	id, ok := idOf(w, r)
	if !ok {
		return
	}
	var req updateRequest
	if !decode(w, r, &req) {
		return
	}
	c, err := h.svc.Update(r.Context(), id, tenant, UpdateInput{
		DisplayName: req.DisplayName, AOIID: req.AOIID, Role: req.Role,
		Status: req.Status, Metadata: req.Metadata,
	})
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	tenant, ok := tenantOf(w, r)
	if !ok {
		return
	}
	id, ok := idOf(w, r)
	if !ok {
		return
	}
	if err := h.svc.Delete(r.Context(), id, tenant); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// bulkImport reads a CSV body and creates contacts row-by-row, returning a
// summary. Individual bad rows are reported, not fatal.
func (h *Handler) bulkImport(w http.ResponseWriter, r *http.Request) {
	tenant, ok := tenantOf(w, r)
	if !ok {
		return
	}
	res, err := h.svc.BulkImport(r.Context(), tenant, r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *Handler) shortIDCheck(w http.ResponseWriter, r *http.Request) {
	tenant, ok := tenantOf(w, r)
	if !ok {
		return
	}
	shortID := r.URL.Query().Get("short_id")
	if shortID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "short_id required"})
		return
	}
	res, err := h.svc.ShortIDCheck(r.Context(), tenant, shortID)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *Handler) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
	case errors.Is(err, ErrConflict):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "short_id_conflict"})
	case errors.Is(err, ErrInvalid):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid"})
	default:
		h.logger.Error("contacts handler error", "event", "contacts.error", "error", err.Error())
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
	}
}

func tenantOf(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.Header.Get("X-Tenant-ID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing_or_invalid_tenant"})
		return uuid.Nil, false
	}
	return id, true
}

func idOf(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_id"})
		return uuid.Nil, false
	}
	return id, true
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json"})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
