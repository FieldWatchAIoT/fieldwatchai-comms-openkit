// Package listeners defines the per-platform inbound seam. Each platform
// implements Listener (verify + parse, with account resolution done inside the
// listener), and Register mounts it onto the HTTP mux. Adding a platform is a
// new Listener implementation plus a Register call — no changes to the core.
package listeners

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/canonical"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/httpapi"
)

// Listener is a self-contained per-platform inbound handler.
//
//   - Verify checks the request authenticity (signature/secret) against the
//     raw body. A non-nil error means the request is rejected with 401.
//   - Parse normalizes the raw body into zero or more canonical messages.
//     Returning an empty slice is how a listener drops a message that should
//     be acknowledged but not forwarded (own echo, group chat, unregistered
//     account, non-message event). Account resolution happens inside Parse.
type Listener interface {
	ID() string
	Path() string
	Verify(r *http.Request, body []byte) error
	Parse(body []byte) ([]canonical.Message, error)
}

// RequestParser is an optional capability for listeners that need request
// context to parse — e.g. a query param identifying which bot/account the
// webhook is for (Telegram updates carry no bot id). When a Listener also
// implements RequestParser, the handler calls ParseRequest instead of Parse.
// Listeners that parse from the body alone (UltraMSG, Twilio) need only Listener.
type RequestParser interface {
	ParseRequest(r *http.Request, body []byte) ([]canonical.Message, error)
}

// Enqueuer is the durable sink the registry forwards canonical messages to.
// It is satisfied by the queue adapter (SQS in prod, in-memory in tests).
type Enqueuer interface {
	Enqueue(ctx context.Context, msg canonical.Message) error
}

// Handler builds the HTTP handler for a single listener. The response codes
// encode the inbound contract:
//   - 401: verify failed (bad signature/secret).
//   - 200: accepted (messages enqueued) OR deliberately dropped (parse yielded
//     nothing, or the payload was unparseable). Platforms retry on non-2xx, so
//     drops must be 200 to avoid retry storms.
//   - 500: a message parsed but could not be durably enqueued — signal the
//     platform to retry so the message is not lost. (Redelivery is safe:
//     comms-channels dedupes on platform_message_id.)
func Handler(l Listener, enq Enqueuer, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Event names and fields follow the observability contract; the
		// CloudWatch metric filters key on the JSON `event` field.
		log := logger.With("platform", l.ID(), "request_id", httpapi.RequestIDFromContext(r.Context()))
		log.Info("inbound webhook received", "event", "webhook_received")

		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeStatus(w, http.StatusBadRequest, "bad_request")
			return
		}

		if err := l.Verify(r, body); err != nil {
			log.Warn("invalid signature", "event", "invalid_signature")
			writeStatus(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		var msgs []canonical.Message
		if rp, ok := l.(RequestParser); ok {
			msgs, err = rp.ParseRequest(r, body)
		} else {
			msgs, err = l.Parse(body)
		}
		if err != nil {
			// A Parse error is a *retryable* failure (e.g. the account lookup is
			// transiently down). Deliberate drops return (nil, nil) instead, so
			// the platform is told to retry rather than losing the message.
			log.Error("parse failed; signaling retry", "event", "parse_failed", "error", err.Error())
			writeStatus(w, http.StatusInternalServerError, "internal_error")
			return
		}

		for _, m := range msgs {
			if err := enq.Enqueue(r.Context(), m); err != nil {
				log.Error("enqueue failed",
					"event", "enqueue_failed", "platform_message_id", m.Meta.PlatformMessageID, "error", err.Error())
				writeStatus(w, http.StatusInternalServerError, "internal_error")
				return
			}
			log.Info("message enqueued", "event", "enqueued", "platform_message_id", m.Meta.PlatformMessageID)
		}
		log.Info("inbound webhook accepted", "event", "webhook_accepted", "count", len(msgs))
		writeStatus(w, http.StatusOK, "accepted")
	}
}

// Register mounts each listener's handler at its Path() on the mux.
func Register(mux *http.ServeMux, enq Enqueuer, logger *slog.Logger, ls ...Listener) {
	for _, l := range ls {
		mux.HandleFunc(l.Path(), Handler(l, enq, logger))
		logger.Info("registered listener",
			"event", "listener.registered", "id", l.ID(), "path", l.Path())
	}
}

func writeStatus(w http.ResponseWriter, code int, status string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": status})
}
