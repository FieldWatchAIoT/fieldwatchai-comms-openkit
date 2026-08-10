package replay

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
	Run(ctx context.Context, req Request) (Result, error)
}

// Handler serves POST /v1/workflows/replay.
type Handler struct {
	svc    serviceAPI
	logger *slog.Logger
}

// NewHandler constructs the replay HTTP handler.
func NewHandler(svc serviceAPI, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, logger: logger}
}

// RegisterRoutes mounts the route. Auth is applied by the caller.
//
// Replay is operator-triggered rather than automatic. A sweeper that re-fired
// on its own would change delivery timing for every consumer without anyone
// choosing it; a consumer coming back from an outage should decide when its
// backlog arrives.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/workflows/replay", h.run)
}

type replayRequest struct {
	ChannelID string `json:"channel_id"`
	Since     string `json:"since"` // RFC3339; omitted = no lower bound
	Limit     int32  `json:"limit"`
	DryRun    bool   `json:"dry_run"`
}

func (h *Handler) run(w http.ResponseWriter, r *http.Request) {
	tenant, err := uuid.Parse(r.Header.Get("X-Tenant-ID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing_or_invalid_tenant"})
		return
	}

	var req replayRequest
	// An empty body is a valid request: replay everything outstanding.
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json"})
			return
		}
	}

	in := Request{TenantID: tenant, Limit: req.Limit, DryRun: req.DryRun}
	if req.ChannelID != "" {
		id, perr := uuid.Parse(req.ChannelID)
		if perr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_channel_id"})
			return
		}
		in.ChannelID = &id
	}
	if req.Since != "" {
		ts, perr := time.Parse(time.RFC3339, req.Since)
		if perr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_since"})
			return
		}
		in.Since = ts
	}

	res, err := h.svc.Run(r.Context(), in)
	if err != nil {
		if errors.Is(err, ErrInvalid) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid"})
			return
		}
		if errors.Is(err, ErrAlreadyRunning) {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":  "replay_in_progress",
				"detail": "a replay is already running for this tenant; retry when it finishes, or use dry_run to inspect",
			})
			return
		}
		h.logger.Error("replay failed", "event", "replay.error", "error", err.Error())
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}

	h.logger.Info("replay run", "event", "replay.run", "tenant_id", tenant,
		"candidates", res.Candidates, "replayed", res.Replayed, "failed", res.Failed, "dry_run", res.DryRun)

	// Partial success is the normal outcome when a consumer is still recovering,
	// so it gets 200 with the counts rather than an error — the caller decides
	// whether to run again. Only an unusable request or a broken hub errors.
	writeJSON(w, http.StatusOK, res)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
