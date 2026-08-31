package export

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
)

type serviceAPI interface {
	Messages(ctx context.Context, w io.Writer, tenantID uuid.UUID, opt Options) (int, error)
	Contacts(ctx context.Context, w io.Writer, tenantID uuid.UUID) (int, error)
}

// Handler serves the export endpoints.
type Handler struct {
	svc    serviceAPI
	logger *slog.Logger
	now    func() time.Time
}

// NewHandler constructs an export HTTP handler.
func NewHandler(svc serviceAPI, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, logger: logger, now: time.Now}
}

// RegisterRoutes mounts the export routes. Auth is applied by the caller.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/export/messages", h.messages)
	mux.HandleFunc("GET /v1/export/contacts", h.contacts)
}

func (h *Handler) messages(w http.ResponseWriter, r *http.Request) {
	tenant, ok := tenantOf(w, r)
	if !ok {
		return
	}
	opt := Options{IncludeRaw: r.URL.Query().Get("include_raw") == "true"}
	h.stream(w, r, "messages", func(out io.Writer) (int, error) {
		return h.svc.Messages(r.Context(), out, tenant, opt)
	})
}

func (h *Handler) contacts(w http.ResponseWriter, r *http.Request) {
	tenant, ok := tenantOf(w, r)
	if !ok {
		return
	}
	h.stream(w, r, "contacts", func(out io.Writer) (int, error) {
		return h.svc.Contacts(r.Context(), out, tenant)
	})
}

// stream writes the export directly to the response.
//
// The status line is committed before the first row, so a failure partway
// through cannot be turned into a 500 — the client would otherwise receive a
// truncated file with a success status and no way to tell. Instead the error is
// logged and a final line carrying an "export_error" key is appended, which a
// JSON Lines reader will surface as an object rather than silently accept.
func (h *Handler) stream(w http.ResponseWriter, r *http.Request, what string, run func(io.Writer) (int, error)) {
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Content-Disposition",
		`attachment; filename="openkit-`+what+`-`+h.now().UTC().Format("20060102T150405Z")+`.jsonl"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)

	flusher, _ := w.(http.Flusher)
	n, err := run(w)
	if err != nil {
		h.logger.Error("export failed partway", "event", "export.error",
			"what", what, "written", n, "error", err.Error())
		_ = json.NewEncoder(w).Encode(map[string]any{
			"export_error": "export failed after " + itoa(n) + " records; the file is incomplete",
		})
	} else {
		h.logger.Info("export complete", "event", "export.complete", "what", what, "records", n)
	}
	if flusher != nil {
		flusher.Flush()
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func tenantOf(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.Header.Get("X-Tenant-ID"))
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "missing_or_invalid_tenant"})
		return uuid.Nil, false
	}
	return id, true
}
