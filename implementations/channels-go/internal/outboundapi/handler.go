package outboundapi

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
	ReplyToMessage(ctx context.Context, inReplyTo uuid.UUID, text string) (Result, error)
	SendToContact(ctx context.Context, contactID uuid.UUID, channelID *uuid.UUID, text, subject string) (Result, error)
	SendToEndpoint(ctx context.Context, channelID uuid.UUID, endpoint, text, subject string) (Result, error)
}

// Handler serves POST /v1/outbound.
type Handler struct {
	svc    serviceAPI
	logger *slog.Logger
}

// NewHandler constructs the outbound HTTP handler.
func NewHandler(svc serviceAPI, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, logger: logger}
}

// RegisterRoutes mounts the route. Auth (internal bearer) is applied by the caller.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/outbound", h.send)
}

// recipientSpec addresses a message with no prior inbound to reply to. Exactly
// one of the two fields is set.
type recipientSpec struct {
	ContactID string `json:"contact_id"`
	Endpoint  string `json:"endpoint"`
}

type sendRequest struct {
	InReplyToMessageID string         `json:"in_reply_to_message_id"`
	Recipient          *recipientSpec `json:"recipient"`
	ChannelID          string         `json:"channel_id"`
	Subject            string         `json:"subject"` // email only; ignored elsewhere
	Text               string         `json:"text"`
}

func (h *Handler) send(w http.ResponseWriter, r *http.Request) {
	var req sendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json"})
		return
	}

	hasReply := req.InReplyToMessageID != ""
	hasRecipient := req.Recipient != nil && (req.Recipient.ContactID != "" || req.Recipient.Endpoint != "")
	if hasReply == hasRecipient { // neither, or both
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "invalid_target", "detail": "set exactly one of in_reply_to_message_id or recipient",
		})
		return
	}

	// channel_id is optional for a reply (the message determines the channel) and
	// for recipient.contact_id (it narrows which of the contact's endpoints to
	// use), but required for recipient.endpoint.
	var channelID *uuid.UUID
	if req.ChannelID != "" {
		id, err := uuid.Parse(req.ChannelID)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_channel_id"})
			return
		}
		channelID = &id
	}

	var (
		res  Result
		err  error
		kind string
	)
	switch {
	case hasReply:
		inReplyTo, perr := uuid.Parse(req.InReplyToMessageID)
		if perr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_in_reply_to_message_id"})
			return
		}
		kind = "reply"
		res, err = h.svc.ReplyToMessage(r.Context(), inReplyTo, req.Text)

	case req.Recipient.ContactID != "" && req.Recipient.Endpoint != "":
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "invalid_recipient", "detail": "set exactly one of recipient.contact_id or recipient.endpoint",
		})
		return

	case req.Recipient.ContactID != "":
		contactID, perr := uuid.Parse(req.Recipient.ContactID)
		if perr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_contact_id"})
			return
		}
		kind = "initiated"
		res, err = h.svc.SendToContact(r.Context(), contactID, channelID, req.Text, req.Subject)

	default: // recipient.endpoint
		if channelID == nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": "channel_id_required", "detail": "recipient.endpoint needs channel_id to resolve the sending account",
			})
			return
		}
		kind = "initiated"
		res, err = h.svc.SendToEndpoint(r.Context(), *channelID, req.Recipient.Endpoint, req.Text, req.Subject)
	}

	if err != nil {
		switch {
		case errors.Is(err, ErrBadRequest):
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad_request"})
		case errors.Is(err, ErrNotFound):
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "recipient_not_found"})
		default:
			h.logger.Error("outbound send failed", "event", "outbound_failed", "kind", kind, "error", err.Error())
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "dispatch_failed"})
		}
		return
	}
	h.logger.Info("outbound sent", "event", "outbound_sent", "message_id", res.MessageID, "to", res.DispatchedTo, "kind", kind)
	writeJSON(w, http.StatusAccepted, map[string]any{"message_id": res.MessageID, "dispatched_to": res.DispatchedTo})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
