package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/canonical"
)

// serviceAPI is the handler's view of the ingest service (for testability).
type serviceAPI interface {
	Ingest(ctx context.Context, msg canonical.Message) (Result, error)
}

// Handler serves POST /v1/ingest.
type Handler struct {
	svc    serviceAPI
	logger *slog.Logger
}

// NewHandler constructs an ingest HTTP handler.
func NewHandler(svc serviceAPI, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, logger: logger}
}

// RegisterRoutes mounts the ingest route. Auth (internal bearer) is applied by
// the caller.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/ingest", h.ingest)
}

func (h *Handler) ingest(w http.ResponseWriter, r *http.Request) {
	h.logger.Info("ingest received", "event", "ingest_received")

	var msg canonical.Message
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json"})
		return
	}

	res, err := h.svc.Ingest(r.Context(), msg)
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidAccount):
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_account"})
		case errors.Is(err, ErrAccountNotFound):
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "account_not_found"})
		default:
			h.logger.Error("ingest failed", "event", "ingest_failed", "error", err.Error())
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		}
		return
	}

	body := map[string]any{
		"message_id":    res.MessageID,
		"policy_action": string(res.Action),
	}
	if res.IsReplay {
		body["is_replay"] = true
		h.logger.Info("ingest replay", "event", "ingest_replay", "message_id", res.MessageID)
		writeJSON(w, http.StatusOK, body)
		return
	}
	h.logger.Info("ingest stored", "event", "ingest_stored", "message_id", res.MessageID, "policy_action", string(res.Action))
	writeJSON(w, http.StatusCreated, body)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
