package twilio

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/accounts"
)

// Real Twilio inbound field shapes (confirmed against a working WhatsApp bot:
// From/To/Body/NumMedia/MediaUrl{n}/MediaContentType{n}/ProfileName/WaId/
// MessageSid). From carries a "whatsapp:" scheme for WhatsApp, bare for SMS.
const (
	textBody  = "From=whatsapp%3A%2B15555550101&To=whatsapp%3A%2B15555550100&Body=Hello+from+the+field&NumMedia=0&ProfileName=TestUser&WaId=15555550101&MessageSid=SMtext1&SmsStatus=received"
	imageBody = "From=whatsapp%3A%2B15555550101&To=whatsapp%3A%2B15555550100&Body=&NumMedia=1&MediaContentType0=image%2Fjpeg&MediaUrl0=https%3A%2F%2Fapi.twilio.com%2Fmedia%2FMEimg&ProfileName=TestUser&WaId=15555550101&MessageSid=SMimg1"
	voiceBody = "From=whatsapp%3A%2B15555550101&To=whatsapp%3A%2B15555550100&NumMedia=1&MediaContentType0=audio%2Fogg&MediaUrl0=https%3A%2F%2Fapi.twilio.com%2Fmedia%2FMEaud&MessageSid=SMvoice1"
	locBody   = "From=whatsapp%3A%2B15555550101&To=whatsapp%3A%2B15555550100&NumMedia=0&Latitude=25.0187740&Longitude=-77.3959579&Label=Home&MessageSid=SMloc1"
	smsBody   = "From=%2B15555550101&To=%2B13055550199&Body=plain+sms&NumMedia=0&MessageSid=SMsms1"
)

type fakeResolver struct {
	acc accounts.Account
	err error
}

func (f fakeResolver) Resolve(_ context.Context, _, _ string) (accounts.Account, error) {
	return f.acc, f.err
}

func newListener(r accounts.Resolver) *Listener {
	l := New("whatsapp-twilio", "tok", "https://example.com", r, discardLogger())
	l.now = func() time.Time { return time.Date(2026, 6, 6, 21, 0, 0, 0, time.UTC) }
	return l
}

// capturingResolver records the type (listener id) it was called with.
type capturingResolver struct{ gotType string }

func (c *capturingResolver) Resolve(_ context.Context, platform, _ string) (accounts.Account, error) {
	c.gotType = platform
	return accounts.Account{ID: "acc_x"}, nil
}

// TestListener_IDAndPath: one type, two channels — id is the lookup type and the
// path is /inbound/<id>.
func TestListener_IDAndPath(t *testing.T) {
	wa := New("whatsapp-twilio", "tok", "https://x", nil, discardLogger())
	sms := New("sms-twilio", "tok", "https://x", nil, discardLogger())
	if wa.ID() != "whatsapp-twilio" || wa.Path() != "/inbound/whatsapp-twilio" {
		t.Errorf("wa id=%q path=%q", wa.ID(), wa.Path())
	}
	if sms.ID() != "sms-twilio" || sms.Path() != "/inbound/sms-twilio" {
		t.Errorf("sms id=%q path=%q", sms.ID(), sms.Path())
	}
}

// TestParse_ResolvesUnderListenerID: each listener resolves accounts under its
// own id, so an SMS resolves as sms-twilio (not whatsapp-twilio).
func TestParse_ResolvesUnderListenerID(t *testing.T) {
	c := &capturingResolver{}
	l := New("sms-twilio", "tok", "https://x", c, discardLogger())
	l.now = func() time.Time { return time.Date(2026, 6, 6, 21, 0, 0, 0, time.UTC) }
	if _, err := l.Parse([]byte(smsBody)); err != nil {
		t.Fatal(err)
	}
	if c.gotType != "sms-twilio" {
		t.Errorf("resolver called with type %q, want sms-twilio", c.gotType)
	}
}

func okResolver() fakeResolver { return fakeResolver{acc: accounts.Account{ID: "acc_x"}} }

func TestParse_Text(t *testing.T) {
	msgs, err := newListener(okResolver()).Parse([]byte(textBody))
	if err != nil || len(msgs) != 1 {
		t.Fatalf("got %d msgs, err %v", len(msgs), err)
	}
	m := msgs[0]
	if m.Sender.Endpoint != "+15555550101" || m.Sender.Platform != "whatsapp" {
		t.Errorf("sender = %+v", m.Sender)
	}
	if m.Sender.FullName == nil || *m.Sender.FullName != "TestUser" {
		t.Errorf("full_name = %v", m.Sender.FullName)
	}
	if m.Sender.Handle == nil || *m.Sender.Handle != "15555550101" {
		t.Errorf("handle (WaId) = %v", m.Sender.Handle)
	}
	if m.Body.Text == nil || *m.Body.Text != "Hello from the field" {
		t.Errorf("text = %v", m.Body.Text)
	}
	if m.Meta.PlatformMessageID != "SMtext1" || m.Meta.AccountID != "acc_x" {
		t.Errorf("meta = %+v", m.Meta)
	}
	if m.Body.Attachments != nil || m.Body.Location != nil {
		t.Errorf("text msg should have no attachments/location")
	}
}

func TestParse_Image(t *testing.T) {
	msgs, _ := newListener(okResolver()).Parse([]byte(imageBody))
	if len(msgs) != 1 {
		t.Fatalf("got %d msgs", len(msgs))
	}
	m := msgs[0]
	if m.Body.Text != nil {
		t.Errorf("image with empty body should have nil text, got %v", m.Body.Text)
	}
	if len(m.Body.Attachments) != 1 {
		t.Fatalf("attachments = %d", len(m.Body.Attachments))
	}
	a := m.Body.Attachments[0]
	if a.Type != "image" || a.URL != "https://api.twilio.com/media/MEimg" || a.Mime == nil || *a.Mime != "image/jpeg" {
		t.Errorf("attachment = %+v mime=%v", a, a.Mime)
	}
}

func TestParse_Voice(t *testing.T) {
	msgs, _ := newListener(okResolver()).Parse([]byte(voiceBody))
	a := msgs[0].Body.Attachments[0]
	if a.Type != "audio" || a.Mime == nil || *a.Mime != "audio/ogg" {
		t.Errorf("voice attachment = %+v", a)
	}
}

func TestParse_Location(t *testing.T) {
	msgs, _ := newListener(okResolver()).Parse([]byte(locBody))
	loc := msgs[0].Body.Location
	if loc == nil || loc.Lat < 25.0 || loc.Lat > 25.1 || loc.Lng > -77.3 || loc.Lng < -77.5 {
		t.Errorf("location = %+v", loc)
	}
	if msgs[0].Body.Text != nil {
		t.Errorf("location msg should have nil text")
	}
}

func TestParse_SmsPlatformNoPrefix(t *testing.T) {
	msgs, _ := newListener(okResolver()).Parse([]byte(smsBody))
	m := msgs[0]
	if m.Sender.Platform != "sms" || m.Sender.Endpoint != "+15555550101" {
		t.Errorf("sms sender = %+v", m.Sender)
	}
	if m.Body.Text == nil || *m.Body.Text != "plain sms" {
		t.Errorf("sms text = %v", m.Body.Text)
	}
}

func TestParse_MalformedDropped(t *testing.T) {
	msgs, err := newListener(okResolver()).Parse([]byte("x=%ZZ"))
	if err != nil || len(msgs) != 0 {
		t.Errorf("malformed should drop (nil,nil), got %d msgs err %v", len(msgs), err)
	}
}

func TestParse_NoFromDropped(t *testing.T) {
	msgs, err := newListener(okResolver()).Parse([]byte("MessageStatus=delivered&MessageSid=SMx"))
	if err != nil || len(msgs) != 0 {
		t.Errorf("no From (e.g. status callback) should drop, got %d err %v", len(msgs), err)
	}
}

func TestParse_NoMessageSidDropped(t *testing.T) {
	msgs, err := newListener(okResolver()).Parse([]byte("From=whatsapp%3A%2B15555550101&To=whatsapp%3A%2B15555550100&Body=hi"))
	if err != nil || len(msgs) != 0 {
		t.Errorf("no MessageSid should drop, got %d err %v", len(msgs), err)
	}
}

func TestParse_UnregisteredDropped(t *testing.T) {
	r := fakeResolver{err: accounts.ErrNotFound}
	msgs, err := newListener(r).Parse([]byte(textBody))
	if err != nil || len(msgs) != 0 {
		t.Errorf("unregistered account should drop, got %d err %v", len(msgs), err)
	}
}

func TestParse_TransientResolveErrorPropagates(t *testing.T) {
	r := fakeResolver{err: errors.New("channels 502")}
	if _, err := newListener(r).Parse([]byte(textBody)); err == nil {
		t.Error("transient resolve failure must propagate (retry), got nil")
	}
}

// Real captured Twilio WhatsApp payloads (form-encoded reconstructions of the
// stored raw_payload), used to validate the parser against ground truth.
const (
	// Row 7 — a WhatsApp location: flat top-level Latitude/Longitude + MessageType=location.
	realLocationBody = "To=whatsapp%3A%2B15555550100&Body=&From=whatsapp%3A%2B15555550101&WaId=15555550101&Latitude=25.018766403198&Longitude=-77.395950317383&NumMedia=0&MessageSid=SMed5ffcfab3da6bbbccdd8866fe793744&MessageType=location&ProfileName=Test+User"
	// Row 2 — a reply ("Nice"): OriginalRepliedMessageSid is the quoted message.
	realReplyBody = "To=whatsapp%3A%2B15555550100&Body=Nice&From=whatsapp%3A%2B15555550101&WaId=15555550101&NumMedia=0&MessageSid=SM70cd0bbe75ce4540a2a8d0491f924c02&MessageType=text&ProfileName=Test+User&OriginalRepliedMessageSid=MM3c146d6483ab26bddabeeedf9565afea&OriginalRepliedMessageSender=whatsapp%3A%2B15555550100"
	// Row 3 — a shared contact (vCard): MessageType=contacts, MediaContentType0=text/vcard.
	realContactBody = "To=whatsapp%3A%2B15555550100&Body=&From=whatsapp%3A%2B15555550101&WaId=15555550101&NumMedia=1&MediaUrl0=https%3A%2F%2Fapi.twilio.com%2F2010-04-01%2FAccounts%2FAC8b%2FMessages%2FMM3d%2FMedia%2FME60&MessageSid=MM3d3cd4db06d6f0e7b7d4f7012fd99f06&MessageType=contacts&MediaContentType0=text%2Fvcard&ProfileName=Test+User"
)

// TestParse_RealLocation_EmitsLocation proves the parser DOES normalize
// Twilio's flat Latitude/Longitude into body.location. (So a NULL body_location
// downstream is channels' ingest mapping, not this parser.)
func TestParse_RealLocation_EmitsLocation(t *testing.T) {
	msgs, err := newListener(okResolver()).Parse([]byte(realLocationBody))
	if err != nil || len(msgs) != 1 {
		t.Fatalf("got %d msgs, err %v", len(msgs), err)
	}
	loc := msgs[0].Body.Location
	if loc == nil {
		t.Fatal("listener must emit body.location for a Twilio location message")
	}
	if loc.Lat < 25.0 || loc.Lat > 25.1 || loc.Lng > -77.3 || loc.Lng < -77.5 {
		t.Errorf("location = %+v, want ~(25.0188, -77.3960)", loc)
	}
}

// TestParse_RealReply_CapturesInReplyTo: a reply must surface the quoted message
// id (OriginalRepliedMessageSid) as meta.in_reply_to_id.
func TestParse_RealReply_CapturesInReplyTo(t *testing.T) {
	msgs, _ := newListener(okResolver()).Parse([]byte(realReplyBody))
	got := msgs[0].Meta.InReplyToID
	if got == nil || *got != "MM3c146d6483ab26bddabeeedf9565afea" {
		t.Errorf("in_reply_to_id = %v, want the OriginalRepliedMessageSid", got)
	}
}

// TestParse_RealContact_TypedContact: a shared vCard must type as "contact"
// (via Twilio's MessageType=contacts), not "document".
func TestParse_RealContact_TypedContact(t *testing.T) {
	msgs, _ := newListener(okResolver()).Parse([]byte(realContactBody))
	a := msgs[0].Body.Attachments[0]
	if a.Type != "contact" {
		t.Errorf("contact typed as %q, want \"contact\"", a.Type)
	}
	if a.Mime == nil || *a.Mime != "text/vcard" {
		t.Errorf("mime = %v, want text/vcard", a.Mime)
	}
}
