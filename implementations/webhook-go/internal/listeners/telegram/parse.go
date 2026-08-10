package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/accounts"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/canonical"
)

// update is the subset of the Telegram Bot API Update we consume.
type update struct {
	UpdateID int64    `json:"update_id"`
	Message  *message `json:"message"`
}

type message struct {
	MessageID      int64       `json:"message_id"`
	From           *user       `json:"from"`
	Chat           chat        `json:"chat"`
	Date           int64       `json:"date"`
	Text           string      `json:"text"`
	Caption        string      `json:"caption"`
	Photo          []photoSize `json:"photo"`
	Voice          *file       `json:"voice"`
	Audio          *file       `json:"audio"`
	Video          *file       `json:"video"`
	VideoNote      *file       `json:"video_note"`
	Animation      *file       `json:"animation"`
	Document       *file       `json:"document"`
	Sticker        *file       `json:"sticker"`
	Location       *geo        `json:"location"`
	ReplyToMessage *message    `json:"reply_to_message"`
}

type user struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

type chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type photoSize struct {
	FileID string `json:"file_id"`
}

type file struct {
	FileID   string `json:"file_id"`
	MimeType string `json:"mime_type"`
	FileName string `json:"file_name"`
}

type geo struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

// ParseRequest is the request-aware parse (RequestParser capability): it reads
// the ?bot=<id> query param to know which account the update belongs to.
func (l *Listener) ParseRequest(r *http.Request, body []byte) ([]canonical.Message, error) {
	bot := ""
	if r != nil {
		bot = r.URL.Query().Get("bot")
	}
	return l.parse(bot, body)
}

// Parse satisfies listeners.Listener; without request context there's no bot id,
// so it resolves against an empty identifier (drops as unregistered). The
// handler uses ParseRequest in practice.
func (l *Listener) Parse(body []byte) ([]canonical.Message, error) {
	return l.parse("", body)
}

func (l *Listener) parse(bot string, body []byte) ([]canonical.Message, error) {
	var u update
	if err := json.Unmarshal(body, &u); err != nil {
		l.logger.Warn("dropping unparseable payload",
			"event", "dropped_unparseable", "platform", listenerID, "error", err.Error())
		return nil, nil
	}

	msg := u.Message
	if msg == nil {
		return nil, nil // non-message update (edited_message, callback_query, channel_post, …)
	}
	if msg.From != nil && msg.From.IsBot {
		return nil, nil // ignore other bots (avoid loops)
	}
	if msg.MessageID == 0 {
		l.logger.Warn("dropping message with no id",
			"event", "dropped_no_id", "platform", listenerID)
		return nil, nil
	}

	acc, err := l.resolver.Resolve(context.Background(), listenerID, bot)
	if errors.Is(err, accounts.ErrNotFound) {
		l.logger.Warn("dropping message for unregistered account",
			"event", "account_unresolved", "platform", listenerID)
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("resolve account: %w", err)
	}

	m := canonical.Message{
		Sender: canonical.Sender{
			// chat.id is the reply target (Telegram has no phone/email address).
			Endpoint: strconv.FormatInt(msg.Chat.ID, 10),
			Platform: platformName,
		},
		Meta: canonical.Meta{
			PlatformMessageID: strconv.FormatInt(u.UpdateID, 10),
			ReceivedAt:        l.now().UTC().Format(time.RFC3339),
			AccountID:         acc.ID,
			RawPayload:        json.RawMessage(append([]byte(nil), body...)),
		},
	}
	if msg.From != nil {
		if msg.From.Username != "" {
			m.Sender.Handle = ptr(msg.From.Username)
		}
		if msg.From.FirstName != "" {
			m.Sender.FirstName = ptr(msg.From.FirstName)
		}
		if msg.From.LastName != "" {
			m.Sender.LastName = ptr(msg.From.LastName)
		}
		if full := strings.TrimSpace(msg.From.FirstName + " " + msg.From.LastName); full != "" {
			m.Sender.FullName = ptr(full)
		}
	}

	// text, or a media caption.
	if text := msg.Text; text != "" {
		m.Body.Text = ptr(text)
	} else if msg.Caption != "" {
		m.Body.Text = ptr(msg.Caption)
	}

	// Media: Telegram gives a file_id, not a URL — channels resolves it via
	// getFile (it holds the bot token). We pass the file_id as the attachment URL.
	if att, ok := mediaAttachment(msg); ok {
		m.Body.Attachments = []canonical.Attachment{att}
	}

	if msg.Location != nil {
		m.Body.Location = &canonical.Location{Lat: msg.Location.Latitude, Lng: msg.Location.Longitude}
	}

	if msg.ReplyToMessage != nil && msg.ReplyToMessage.MessageID != 0 {
		m.Meta.InReplyToID = ptr(strconv.FormatInt(msg.ReplyToMessage.MessageID, 10))
	}

	return []canonical.Message{m}, nil
}

// mediaAttachment picks the single media item on a message (Telegram messages
// carry at most one), typed to the coarse canonical vocabulary.
func mediaAttachment(m *message) (canonical.Attachment, bool) {
	switch {
	case len(m.Photo) > 0:
		// Photos arrive as ascending sizes; the last is the largest.
		return canonical.Attachment{Type: "image", URL: m.Photo[len(m.Photo)-1].FileID}, true
	case m.Voice != nil:
		return fileAttachment("audio", m.Voice), true
	case m.Audio != nil:
		return fileAttachment("audio", m.Audio), true
	case m.Video != nil:
		return fileAttachment("video", m.Video), true
	case m.VideoNote != nil:
		return fileAttachment("video", m.VideoNote), true
	case m.Animation != nil:
		return fileAttachment("gif", m.Animation), true
	case m.Document != nil:
		return fileAttachment("document", m.Document), true
	case m.Sticker != nil:
		return fileAttachment("sticker", m.Sticker), true
	}
	return canonical.Attachment{}, false
}

func fileAttachment(typ string, f *file) canonical.Attachment {
	a := canonical.Attachment{Type: typ, URL: f.FileID}
	if f.MimeType != "" {
		a.Mime = ptr(f.MimeType)
	}
	return a
}

func ptr(s string) *string { return &s }
