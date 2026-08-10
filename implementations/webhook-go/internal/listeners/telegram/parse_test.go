package telegram

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/accounts"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/canonical"
)

// Telegram Update fixtures (shape per the Bot API docs).
const (
	textUpdate  = `{"update_id":1001,"message":{"message_id":42,"from":{"id":111,"is_bot":false,"first_name":"Test","last_name":"User","username":"testuser"},"chat":{"id":111,"type":"private"},"date":1700000000,"text":"hello from telegram"}}`
	photoUpdate = `{"update_id":1002,"message":{"message_id":43,"from":{"id":111,"is_bot":false,"first_name":"O"},"chat":{"id":111,"type":"private"},"photo":[{"file_id":"small123"},{"file_id":"large456"}],"caption":"a pic"}}`
	voiceUpdate = `{"update_id":1003,"message":{"message_id":44,"from":{"id":111,"is_bot":false},"chat":{"id":111,"type":"private"},"voice":{"file_id":"voice789","mime_type":"audio/ogg"}}}`
	docUpdate   = `{"update_id":1004,"message":{"message_id":45,"from":{"id":111,"is_bot":false},"chat":{"id":111,"type":"private"},"document":{"file_id":"doc111","file_name":"report.pdf","mime_type":"application/pdf"}}}`
	locUpdate   = `{"update_id":1005,"message":{"message_id":46,"from":{"id":111,"is_bot":false},"chat":{"id":111,"type":"private"},"location":{"latitude":25.0188,"longitude":-77.396}}}`
	replyUpdate = `{"update_id":1006,"message":{"message_id":47,"from":{"id":111,"is_bot":false},"chat":{"id":111,"type":"private"},"text":"replying","reply_to_message":{"message_id":40}}}`
	botUpdate   = `{"update_id":1007,"message":{"message_id":48,"from":{"id":999,"is_bot":true},"chat":{"id":111,"type":"private"},"text":"bot msg"}}`
	editedOnly  = `{"update_id":1008,"edited_message":{"message_id":42,"text":"edited"}}`
)

type fakeResolver struct {
	acc            accounts.Account
	err            error
	gotType, gotID string
}

func (f *fakeResolver) Resolve(_ context.Context, platform, identifier string) (accounts.Account, error) {
	f.gotType, f.gotID = platform, identifier
	return f.acc, f.err
}

func newListener(r accounts.Resolver) *Listener {
	l := New("sek", r, discardLogger())
	l.now = func() time.Time { return time.Date(2026, 6, 12, 21, 0, 0, 0, time.UTC) }
	return l
}

func okResolver() *fakeResolver { return &fakeResolver{acc: accounts.Account{ID: "acc_tg"}} }

// req builds a webhook request carrying ?bot=<bot> (how the listener learns which bot).
func req(bot string) *http.Request {
	return httptest.NewRequest("POST", "/inbound/telegram?bot="+bot, nil)
}

func TestParse_Text(t *testing.T) {
	r := okResolver()
	msgs, err := newListener(r).ParseRequest(req("fwbot"), []byte(textUpdate))
	if err != nil || len(msgs) != 1 {
		t.Fatalf("got %d msgs err %v", len(msgs), err)
	}
	m := msgs[0]
	if m.Sender.Platform != "telegram" || m.Sender.Endpoint != "111" {
		t.Errorf("sender = %+v", m.Sender)
	}
	if m.Sender.Handle == nil || *m.Sender.Handle != "testuser" {
		t.Errorf("handle = %v", m.Sender.Handle)
	}
	if m.Sender.FullName == nil || *m.Sender.FullName != "Test User" {
		t.Errorf("full_name = %v", m.Sender.FullName)
	}
	if m.Body.Text == nil || *m.Body.Text != "hello from telegram" {
		t.Errorf("text = %v", m.Body.Text)
	}
	if m.Meta.PlatformMessageID != "1001" || m.Meta.AccountID != "acc_tg" {
		t.Errorf("meta = %+v", m.Meta)
	}
	if r.gotType != "telegram" || r.gotID != "fwbot" {
		t.Errorf("resolved with %q/%q, want telegram/fwbot (the ?bot param)", r.gotType, r.gotID)
	}
}

func TestParse_PhotoTakesLargest_CaptionToText(t *testing.T) {
	msgs, _ := newListener(okResolver()).ParseRequest(req("b"), []byte(photoUpdate))
	m := msgs[0]
	if m.Body.Text == nil || *m.Body.Text != "a pic" {
		t.Errorf("caption should become text, got %v", m.Body.Text)
	}
	if len(m.Body.Attachments) != 1 {
		t.Fatalf("attachments = %d", len(m.Body.Attachments))
	}
	a := m.Body.Attachments[0]
	if a.Type != "image" || a.URL != "large456" {
		t.Errorf("attachment = %+v (want largest photo file_id)", a)
	}
}

func TestParse_Voice(t *testing.T) {
	a := mustOneAttachment(t, voiceUpdate)
	if a.Type != "audio" || a.URL != "voice789" || a.Mime == nil || *a.Mime != "audio/ogg" {
		t.Errorf("voice attachment = %+v", a)
	}
}

func TestParse_Document(t *testing.T) {
	a := mustOneAttachment(t, docUpdate)
	if a.Type != "document" || a.URL != "doc111" || a.Mime == nil || *a.Mime != "application/pdf" {
		t.Errorf("document attachment = %+v", a)
	}
}

func TestParse_Location(t *testing.T) {
	msgs, _ := newListener(okResolver()).ParseRequest(req("b"), []byte(locUpdate))
	loc := msgs[0].Body.Location
	if loc == nil || loc.Lat < 25.0 || loc.Lat > 25.1 || loc.Lng > -77.3 || loc.Lng < -77.5 {
		t.Errorf("location = %+v", loc)
	}
}

func TestParse_Reply(t *testing.T) {
	msgs, _ := newListener(okResolver()).ParseRequest(req("b"), []byte(replyUpdate))
	got := msgs[0].Meta.InReplyToID
	if got == nil || *got != "40" {
		t.Errorf("in_reply_to_id = %v, want 40", got)
	}
}

func TestParse_DropsBotSender(t *testing.T) {
	msgs, err := newListener(okResolver()).ParseRequest(req("b"), []byte(botUpdate))
	if err != nil || len(msgs) != 0 {
		t.Errorf("a bot sender should drop, got %d msgs", len(msgs))
	}
}

func TestParse_DropsNonMessageUpdate(t *testing.T) {
	msgs, err := newListener(okResolver()).ParseRequest(req("b"), []byte(editedOnly))
	if err != nil || len(msgs) != 0 {
		t.Errorf("non-message update (edited_message) should drop, got %d", len(msgs))
	}
}

func TestParse_UnregisteredDrops(t *testing.T) {
	msgs, err := newListener(&fakeResolver{err: accounts.ErrNotFound}).ParseRequest(req("b"), []byte(textUpdate))
	if err != nil || len(msgs) != 0 {
		t.Errorf("unregistered bot should drop, got %d err %v", len(msgs), err)
	}
}

func TestParse_TransientResolveErrorPropagates(t *testing.T) {
	_, err := newListener(&fakeResolver{err: errors.New("channels down")}).ParseRequest(req("b"), []byte(textUpdate))
	if err == nil {
		t.Error("transient resolve failure must propagate (retry)")
	}
}

func TestParse_MalformedDropped(t *testing.T) {
	msgs, err := newListener(okResolver()).ParseRequest(req("b"), []byte("not json"))
	if err != nil || len(msgs) != 0 {
		t.Errorf("malformed should drop, got %d err %v", len(msgs), err)
	}
}

func mustOneAttachment(t *testing.T, body string) canonical.Attachment {
	t.Helper()
	msgs, err := newListener(okResolver()).ParseRequest(req("b"), []byte(body))
	if err != nil || len(msgs) != 1 || len(msgs[0].Body.Attachments) != 1 {
		t.Fatalf("want 1 msg with 1 attachment, got %d msgs err %v", len(msgs), err)
	}
	return msgs[0].Body.Attachments[0]
}
