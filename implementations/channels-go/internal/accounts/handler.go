package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
)

// serviceAPI is the handler's view of the service (for testability).
type serviceAPI interface {
	Create(ctx context.Context, in CreateInput) (Account, error)
	List(ctx context.Context, tenantID uuid.UUID) ([]Account, error)
	ListWithRouting(ctx context.Context, tenantID uuid.UUID) ([]AccountWithRouting, error)
	Update(ctx context.Context, id, tenantID uuid.UUID, in UpdateInput) (Account, error)
	Delete(ctx context.Context, id, tenantID uuid.UUID) error
	Lookup(ctx context.Context, accType, identifier string) (LookupResult, error)
}

// Handler serves the /v1/accounts CRUD endpoints.
type Handler struct {
	svc    serviceAPI
	logger *slog.Logger
}

// NewHandler constructs an accounts HTTP handler.
func NewHandler(svc serviceAPI, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, logger: logger}
}

// RegisterRoutes mounts the CRUD routes on mux. Auth is applied by the caller
// (the server wraps these with the internal bearer middleware for Week 1).
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/accounts/lookup", h.lookup)
	mux.HandleFunc("GET /v1/accounts", h.list)
	mux.HandleFunc("POST /v1/accounts", h.create)
	mux.HandleFunc("PATCH /v1/accounts/{id}", h.update)
	mux.HandleFunc("DELETE /v1/accounts/{id}", h.delete)
}

type createRequest struct {
	Type               string   `json:"type"`
	OwnerType          string   `json:"owner_type"`
	Label              string   `json:"label"`
	PlatformIdentifier string   `json:"platform_identifier"`
	Status             string   `json:"status"`
	Capabilities       []string `json:"capabilities"`
	Credentials        string   `json:"credentials"`
}

type updateRequest struct {
	Label        *string   `json:"label"`
	Status       *string   `json:"status"`
	Capabilities *[]string `json:"capabilities"`
	Credentials  *string   `json:"credentials"`
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
	acc, err := h.svc.Create(r.Context(), CreateInput{
		TenantID:           tenant,
		Type:               req.Type,
		OwnerType:          req.OwnerType,
		Label:              req.Label,
		PlatformIdentifier: req.PlatformIdentifier,
		Status:             req.Status,
		Capabilities:       req.Capabilities,
		Credentials:        []byte(req.Credentials),
	})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, acc)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	tenant, ok := tenantOf(w, r)
	if !ok {
		return
	}
	accs, err := h.svc.ListWithRouting(r.Context(), tenant)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, accs)
}

// lookup resolves an account by ?type=&identifier= and returns only the id
// pair. No tenant scoping (the webhook is cross-tenant) and never credentials.
func (h *Handler) lookup(w http.ResponseWriter, r *http.Request) {
	accType := r.URL.Query().Get("type")
	identifier := r.URL.Query().Get("identifier")
	if accType == "" || identifier == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "type and identifier are required"})
		return
	}
	res, err := h.svc.Lookup(r.Context(), accType, identifier)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
			return
		}
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
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
	in := UpdateInput{Label: req.Label, Status: req.Status, Capabilities: req.Capabilities}
	if req.Credentials != nil {
		in.Credentials = []byte(*req.Credentials)
	}
	acc, err := h.svc.Update(r.Context(), id, tenant, in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, acc)
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
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// fail maps domain errors to HTTP status codes.
func (h *Handler) fail(w http.ResponseWriter, _ *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
	case errors.Is(err, ErrInvalid):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid"})
	default:
		h.logger.Error("accounts handler error", "event", "accounts.error", "error", err.Error())
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
	}
}

// tenantOf parses the X-Tenant-ID header (Week 1 stand-in until JWT carries it).
func tenantOf(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	raw := r.Header.Get("X-Tenant-ID")
	id, err := uuid.Parse(raw)
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
