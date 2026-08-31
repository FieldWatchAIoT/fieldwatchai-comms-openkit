package retention

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
)

type serviceAPI interface {
	Purge(ctx context.Context, tenantID uuid.UUID, olderThanDays int, dryRun bool) (PurgeResult, error)
	Erase(ctx context.Context, tenantID, contactID uuid.UUID, dryRun bool) (ErasureResult, error)
	EraseEndpoint(ctx context.Context, tenantID uuid.UUID, endpoint string, dryRun bool) (ErasureResult, error)
}

// Handler serves the retention and erasure routes.
type Handler struct {
	svc    serviceAPI
	logger *slog.Logger
}

// NewHandler constructs a retention HTTP handler.
func NewHandler(svc serviceAPI, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, logger: logger}
}

// RegisterRoutes mounts the routes. Auth is applied by the caller.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/retention/purge", h.purge)
	mux.HandleFunc("POST /v1/contacts/{id}/erase", h.erase)
	// Keyed on the endpoint, because that is how a data-subject request
	// actually arrives — "delete my data, my number is …" — and it is the
	// identifying value stored on a message.
	mux.HandleFunc("POST /v1/retention/erase-endpoint", h.eraseEndpoint)
}

// dryRun defaults to TRUE on both routes. Both operations are irreversible, and
// a caller who forgets the field should get a report, not a deletion.
type purgeRequest struct {
	OlderThanDays int   `json:"older_than_days"`
	DryRun        *bool `json:"dry_run"`
}

type eraseRequest struct {
	DryRun *bool `json:"dry_run"`
}

type eraseEndpointRequest struct {
	Endpoint string `json:"endpoint"`
	DryRun   *bool  `json:"dry_run"`
}

func (h *Handler) eraseEndpoint(w http.ResponseWriter, r *http.Request) {
	tenant, ok := tenantOf(w, r)
	if !ok {
		return
	}
	var req eraseEndpointRequest
	if !decode(w, r, &req) {
		return
	}
	res, err := h.svc.EraseEndpoint(r.Context(), tenant, req.Endpoint, dryRunOrDefault(req.DryRun))
	if err != nil {
		h.fail(w, err)
		return
	}
	if !res.DryRun {
		// Records that an erasure happened and how much it touched — never the
		// endpoint itself, which would re-create the data in the log.
		h.logger.Info("endpoint erased", "event", "retention.erased_endpoint",
			"tenant_id", tenant, "messages_redacted", res.MessagesRedacted)
	}
	writeJSON(w, http.StatusOK, res)
}

func dryRunOrDefault(v *bool) bool { return v == nil || *v }

func (h *Handler) purge(w http.ResponseWriter, r *http.Request) {
	tenant, ok := tenantOf(w, r)
	if !ok {
		return
	}
	var req purgeRequest
	if !decode(w, r, &req) {
		return
	}
	res, err := h.svc.Purge(r.Context(), tenant, req.OlderThanDays, dryRunOrDefault(req.DryRun))
	if err != nil {
		h.fail(w, err)
		return
	}
	if !res.DryRun {
		h.logger.Info("retention purge applied", "event", "retention.purged",
			"tenant_id", tenant, "cutoff", res.Cutoff, "deleted", res.MessagesDeleted)
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *Handler) erase(w http.ResponseWriter, r *http.Request) {
	tenant, ok := tenantOf(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_id"})
		return
	}
	var req eraseRequest
	if !decode(w, r, &req) {
		return
	}
	res, err := h.svc.Erase(r.Context(), tenant, id, dryRunOrDefault(req.DryRun))
	if err != nil {
		h.fail(w, err)
		return
	}
	if !res.DryRun {
		// Deliberately records that an erasure happened, and never what was
		// erased: an audit line naming the person defeats the erasure.
		h.logger.Info("contact erased", "event", "retention.erased",
			"tenant_id", tenant, "contact_id", id, "messages_redacted", res.MessagesRedacted)
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *Handler) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
	case errors.Is(err, ErrInvalid):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid", "detail": err.Error()})
	default:
		h.logger.Error("retention handler error", "event", "retention.error", "error", err.Error())
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

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if r.ContentLength == 0 {
		return true // an empty body means "all defaults", which is a dry run
	}
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
