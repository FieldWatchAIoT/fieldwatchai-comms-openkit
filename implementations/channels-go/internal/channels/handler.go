package channels

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// serviceAPI is the handler's view of the service (for testability).
type serviceAPI interface {
	List(ctx context.Context, tenantID uuid.UUID) ([]Channel, error)
	Get(ctx context.Context, id, tenantID uuid.UUID) (Channel, error)
	ListLinks(ctx context.Context, channelID, tenantID uuid.UUID) ([]Link, error)
	Link(ctx context.Context, in LinkInput) (Link, error)
	Unlink(ctx context.Context, channelID, accountID, tenantID uuid.UUID) error
	Create(ctx context.Context, in CreateInput, now func() time.Time, newID func() uuid.UUID) (Channel, error)
	Update(ctx context.Context, id, tenantID uuid.UUID, in UpdateInput) (Channel, error)
}

// Handler serves the /v1/channels routes.
type Handler struct {
	svc    serviceAPI
	logger *slog.Logger
	now    func() time.Time
	newID  func() uuid.UUID
}

// NewHandler constructs a channels HTTP handler.
func NewHandler(svc serviceAPI, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, logger: logger, now: time.Now, newID: uuid.New}
}

// RegisterRoutes mounts the routes on mux. Auth is applied by the caller.
//
// Channels were read-only here on the grounds that a parser grammar and
// thresholds are deployment config. In practice that made the first run a trap:
// with no channel there is no workflow_url, so an account accepts traffic and
// forwards it nowhere, silently, and the only fix was hand-written SQL. Create
// and update are here now, with defaults that match what ingest already does.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/channels", h.list)
	mux.HandleFunc("POST /v1/channels", h.create)
	mux.HandleFunc("GET /v1/channels/{id}", h.get)
	mux.HandleFunc("PATCH /v1/channels/{id}", h.update)
	mux.HandleFunc("GET /v1/channels/{id}/accounts", h.listLinks)
	mux.HandleFunc("POST /v1/channels/{id}/accounts", h.link)
	mux.HandleFunc("DELETE /v1/channels/{id}/accounts/{account_id}", h.unlink)
}

type createRequest struct {
	Name                 string          `json:"name"`
	ParserConfig         json.RawMessage `json:"parser_config"`
	WorkflowURL          string          `json:"workflow_url"`
	ReplyPolicy          string          `json:"reply_policy"`
	ConfidenceThresholds json.RawMessage `json:"confidence_thresholds"`
	EchoBackEnabled      *bool           `json:"echo_back_enabled"`
	RecallWindowSeconds  *int32          `json:"recall_window_seconds"`
}

type updateRequest struct {
	Name                 *string         `json:"name"`
	ParserConfig         json.RawMessage `json:"parser_config"`
	WorkflowURL          *string         `json:"workflow_url"`
	ReplyPolicy          *string         `json:"reply_policy"`
	ConfidenceThresholds json.RawMessage `json:"confidence_thresholds"`
	EchoBackEnabled      *bool           `json:"echo_back_enabled"`
	RecallWindowSeconds  *int32          `json:"recall_window_seconds"`
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
	ch, err := h.svc.Create(r.Context(), CreateInput{
		TenantID:             tenant,
		Name:                 req.Name,
		ParserConfig:         req.ParserConfig,
		WorkflowURL:          req.WorkflowURL,
		ReplyPolicy:          req.ReplyPolicy,
		ConfidenceThresholds: req.ConfidenceThresholds,
		EchoBackEnabled:      req.EchoBackEnabled,
		RecallWindowSeconds:  req.RecallWindowSeconds,
	}, h.now, h.newID)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, ch)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	tenant, ok := tenantOf(w, r)
	if !ok {
		return
	}
	id, ok := idOf(w, r, "id")
	if !ok {
		return
	}
	var req updateRequest
	if !decode(w, r, &req) {
		return
	}
	ch, err := h.svc.Update(r.Context(), id, tenant, UpdateInput{
		Name:                 req.Name,
		ParserConfig:         req.ParserConfig,
		WorkflowURL:          req.WorkflowURL,
		ReplyPolicy:          req.ReplyPolicy,
		ConfidenceThresholds: req.ConfidenceThresholds,
		EchoBackEnabled:      req.EchoBackEnabled,
		RecallWindowSeconds:  req.RecallWindowSeconds,
	})
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ch)
}

type linkRequest struct {
	AccountID     string          `json:"account_id"`
	Direction     string          `json:"direction"`
	Priority      *int32          `json:"priority"`
	RoutingFilter json.RawMessage `json:"routing_filter"`
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	tenant, ok := tenantOf(w, r)
	if !ok {
		return
	}
	chs, err := h.svc.List(r.Context(), tenant)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, chs)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	tenant, ok := tenantOf(w, r)
	if !ok {
		return
	}
	id, ok := idOf(w, r, "id")
	if !ok {
		return
	}
	ch, err := h.svc.Get(r.Context(), id, tenant)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ch)
}

func (h *Handler) listLinks(w http.ResponseWriter, r *http.Request) {
	tenant, ok := tenantOf(w, r)
	if !ok {
		return
	}
	id, ok := idOf(w, r, "id")
	if !ok {
		return
	}
	links, err := h.svc.ListLinks(r.Context(), id, tenant)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, links)
}

func (h *Handler) link(w http.ResponseWriter, r *http.Request) {
	tenant, ok := tenantOf(w, r)
	if !ok {
		return
	}
	channelID, ok := idOf(w, r, "id")
	if !ok {
		return
	}
	var req linkRequest
	if !decode(w, r, &req) {
		return
	}
	accountID, err := uuid.Parse(req.AccountID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_account_id"})
		return
	}
	// Default to the direction that actually matters: an account linked for
	// inbound is the whole point, and 'both' also lets the channel send from it.
	direction := req.Direction
	if direction == "" {
		direction = "both"
	}
	var priority int32 = 100
	if req.Priority != nil {
		priority = *req.Priority
	}

	link, err := h.svc.Link(r.Context(), LinkInput{
		ChannelID:     channelID,
		AccountID:     accountID,
		TenantID:      tenant,
		Direction:     direction,
		Priority:      priority,
		RoutingFilter: req.RoutingFilter,
	})
	if err != nil {
		h.fail(w, err)
		return
	}
	h.logger.Info("account linked to channel", "event", "channel.linked",
		"channel_id", channelID, "account_id", accountID, "direction", direction, "priority", priority)
	writeJSON(w, http.StatusCreated, link)
}

func (h *Handler) unlink(w http.ResponseWriter, r *http.Request) {
	tenant, ok := tenantOf(w, r)
	if !ok {
		return
	}
	channelID, ok := idOf(w, r, "id")
	if !ok {
		return
	}
	accountID, ok := idOf(w, r, "account_id")
	if !ok {
		return
	}
	if err := h.svc.Unlink(r.Context(), channelID, accountID, tenant); err != nil {
		h.fail(w, err)
		return
	}
	h.logger.Info("account unlinked from channel", "event", "channel.unlinked",
		"channel_id", channelID, "account_id", accountID)
	w.WriteHeader(http.StatusNoContent)
}

// fail maps domain errors to HTTP status codes.
func (h *Handler) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
	case errors.Is(err, ErrInvalid):
		// Return the detail, not a bare "invalid": these messages are written
		// for the operator setting the channel up, and withholding them turns
		// configuration into guesswork.
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid", "detail": err.Error()})
	default:
		h.logger.Error("channels handler error", "event", "channels.error", "error", err.Error())
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
	}
}

// tenantOf parses the X-Tenant-ID header (same stand-in the accounts API uses).
func tenantOf(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.Header.Get("X-Tenant-ID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing_or_invalid_tenant"})
		return uuid.Nil, false
	}
	return id, true
}

func idOf(w http.ResponseWriter, r *http.Request, field string) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue(field))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_" + field})
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
