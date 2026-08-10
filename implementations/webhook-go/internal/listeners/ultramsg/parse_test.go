package ultramsg

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/accounts"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

var fixedTime = time.Date(2026, 6, 5, 19, 32, 11, 0, time.UTC)

// newTestListener builds a listener with instance123 registered to acc_test and
// a pinned clock so received_at is deterministic.
func newTestListener(t *testing.T) *Listener {
	t.Helper()
	r, err := accounts.NewConfigResolver(`{"whatsapp-ultramsg":{"instance123":"acc_test"}}`)
	if err != nil {
		t.Fatalf("resolver: %v", err)
	}
	l := New("sekret", r, discardLogger())
	l.now = func() time.Time { return fixedTime }
	return l
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

func strval(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

func TestParse_Text(t *testing.T) {
	l := newTestListener(t)
	msgs, err := l.Parse(fixture(t, "text.json"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	m := msgs[0]
	if m.Sender.Endpoint != "+12425550042" {
		t.Errorf("endpoint = %q, want +12425550042", m.Sender.Endpoint)
	}
	if m.Sender.Platform != "whatsapp" {
		t.Errorf("platform = %q, want whatsapp", m.Sender.Platform)
	}
	if strval(m.Sender.FullName) != "John Smith" {
		t.Errorf("full_name = %q, want John Smith", strval(m.Sender.FullName))
	}
	if strval(m.Body.Text) != "42 STATUS full" {
		t.Errorf("text = %q, want '42 STATUS full'", strval(m.Body.Text))
	}
	if m.Body.Attachments != nil || m.Body.Location != nil {
		t.Errorf("text msg should have no attachments/location, got %+v / %+v", m.Body.Attachments, m.Body.Location)
	}
	if m.Meta.PlatformMessageID != "false_12425550042@c.us_3EB0TEXT" {
		t.Errorf("platform_message_id = %q", m.Meta.PlatformMessageID)
	}
	if m.Meta.AccountID != "acc_test" {
		t.Errorf("account_id = %q, want acc_test", m.Meta.AccountID)
	}
	if m.Meta.ReceivedAt != "2026-06-05T19:32:11Z" {
		t.Errorf("received_at = %q, want pinned time", m.Meta.ReceivedAt)
	}
	if string(m.Meta.RawPayload) != string(fixture(t, "text.json")) {
		t.Errorf("raw_payload not verbatim")
	}
}

func TestParse_Image(t *testing.T) {
	l := newTestListener(t)
	msgs, err := l.Parse(fixture(t, "image.json"))
	if err != nil || len(msgs) != 1 {
		t.Fatalf("Parse: err=%v n=%d", err, len(msgs))
	}
	m := msgs[0]
	if len(m.Body.Attachments) != 1 {
		t.Fatalf("attachments = %d, want 1", len(m.Body.Attachments))
	}
	a := m.Body.Attachments[0]
	if a.Type != "image" || a.URL != "https://media.ultramsg.com/instance123/3EB0IMG.jpg" || a.Mime != nil {
		t.Errorf("attachment = %+v", a)
	}
	if strval(m.Body.Text) != "look at this" {
		t.Errorf("caption text = %q", strval(m.Body.Text))
	}
}

func TestParse_Location(t *testing.T) {
	l := newTestListener(t)
	msgs, err := l.Parse(fixture(t, "location.json"))
	if err != nil || len(msgs) != 1 {
		t.Fatalf("Parse: err=%v n=%d", err, len(msgs))
	}
	loc := msgs[0].Body.Location
	if loc == nil {
		t.Fatal("location is nil, want lat/lng set")
	}
	if loc.Lat != 25.018774032592773 || loc.Lng != -77.39595794677734 {
		t.Errorf("location = %+v, want {25.0187..,-77.3959..}", loc)
	}
	// For a location message UltraMSG puts a base64 map thumbnail in data.body;
	// it must NOT leak into body.text.
	if msgs[0].Body.Text != nil {
		t.Errorf("location msg text should be nil, got base64 leak: %q", strval(msgs[0].Body.Text))
	}
}

func TestParse_Reply_SetsInReplyTo(t *testing.T) {
	l := newTestListener(t)
	msgs, _ := l.Parse(fixture(t, "reply.json"))
	if len(msgs) != 1 {
		t.Fatalf("got %d, want 1", len(msgs))
	}
	if strval(msgs[0].Meta.InReplyToID) != "false_12425550042@c.us_3EB0ORIG" {
		t.Errorf("in_reply_to_id = %q", strval(msgs[0].Meta.InReplyToID))
	}
}

// TestParse_Document confirms a document becomes an attachment and the
// filename does NOT leak into body.text (UltraMSG puts the filename in
// data.body for documents).
func TestParse_Document(t *testing.T) {
	l := newTestListener(t)
	msgs, err := l.Parse(fixture(t, "document.json"))
	if err != nil || len(msgs) != 1 {
		t.Fatalf("Parse: err=%v n=%d", err, len(msgs))
	}
	m := msgs[0]
	if m.Body.Text != nil {
		t.Errorf("document text should be nil (filename must not leak), got %q", strval(m.Body.Text))
	}
	if len(m.Body.Attachments) != 1 || m.Body.Attachments[0].Type != "document" ||
		m.Body.Attachments[0].URL != "https://media.ultramsg.com/instance123/3EB0DOC.docx" {
		t.Errorf("attachment = %+v, want document with url", m.Body.Attachments)
	}
}

// TestParse_Vcard confirms a contact (vcard) carries its vCard text in
// body.text (there is no media URL for a contact).
func TestParse_Vcard(t *testing.T) {
	l := newTestListener(t)
	msgs, err := l.Parse(fixture(t, "vcard.json"))
	if err != nil || len(msgs) != 1 {
		t.Fatalf("Parse: err=%v n=%d", err, len(msgs))
	}
	m := msgs[0]
	if m.Body.Text == nil || !strings.HasPrefix(*m.Body.Text, "BEGIN:VCARD") {
		t.Errorf("vcard text = %q, want vCard content", strval(m.Body.Text))
	}
	if m.Body.Attachments != nil {
		t.Errorf("vcard should have no attachment, got %+v", m.Body.Attachments)
	}
}

// Drop cases: each should acknowledge (no error) but yield zero messages.
func TestParse_Drops(t *testing.T) {
	cases := []string{"fromme.json", "group.json", "reaction.json", "noid.json"}
	l := newTestListener(t)
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			msgs, err := l.Parse(fixture(t, name))
			if err != nil {
				t.Fatalf("Parse returned error (want drop): %v", err)
			}
			if len(msgs) != 0 {
				t.Errorf("got %d messages, want 0 (dropped)", len(msgs))
			}
		})
	}
}

// TestParse_UnregisteredInstanceDrops confirms an inbound on an instance we do
// not have an account for is dropped (acknowledged, not forwarded).
func TestParse_UnregisteredInstanceDrops(t *testing.T) {
	empty, _ := accounts.NewConfigResolver("")
	l := New("sekret", empty, discardLogger())
	l.now = func() time.Time { return fixedTime }
	msgs, err := l.Parse(fixture(t, "text.json"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("got %d, want 0 (unregistered instance dropped)", len(msgs))
	}
}

// TestParse_MalformedBodyDropped confirms unparseable JSON is dropped (acked,
// no error) rather than retried — retrying garbage won't help.
func TestParse_MalformedBodyDropped(t *testing.T) {
	l := newTestListener(t)
	msgs, err := l.Parse([]byte("not json"))
	if err != nil {
		t.Errorf("malformed body should drop, got error: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("malformed body should yield 0 messages, got %d", len(msgs))
	}
}

// errResolver is a resolver that always returns a configured error.
type errResolver struct{ err error }

func (e errResolver) Resolve(_ context.Context, _, _ string) (accounts.Account, error) {
	return accounts.Account{}, e.err
}

// TestParse_TransientResolveErrorPropagates confirms a transient lookup failure
// (channels down — NOT ErrNotFound) propagates as an error so the inbound is
// retried, never dropped.
func TestParse_TransientResolveErrorPropagates(t *testing.T) {
	l := New("sek", errResolver{err: errors.New("channels 502")}, discardLogger())
	l.now = func() time.Time { return fixedTime }
	if _, err := l.Parse(fixture(t, "text.json")); err == nil {
		t.Error("transient resolve failure must propagate (retry), got nil error")
	}
}
