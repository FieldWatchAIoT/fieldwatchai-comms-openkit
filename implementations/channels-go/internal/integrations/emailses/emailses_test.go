package emailses

import (
	"context"
	"strings"
	"testing"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/outbound"
)

type fakeRaw struct {
	from string
	to   []string
	mime string
}

func (f *fakeRaw) SendRaw(_ context.Context, from string, to []string, raw []byte) (string, error) {
	f.from, f.to, f.mime = from, to, string(raw)
	return "ses-msg-1", nil
}

func TestSendRichRendersHeadersAndThreading(t *testing.T) {
	f := &fakeRaw{}
	s := New(f)
	msg := outbound.Message{
		To: "customer@gmail.com", Body: "Your unit is back online.",
		Subject: "Re: My unit is down", From: "acme@mail.fieldwatchai.io",
		ReplyTo: "acme@fieldwatchai.io", InReplyTo: "<abc@mail.gmail.com>",
		References: "<abc@mail.gmail.com>",
	}
	pmid, err := s.SendRich(context.Background(), outbound.Account{Type: "email-ses"}, msg)
	if err != nil {
		t.Fatalf("SendRich: %v", err)
	}
	if pmid != "ses-msg-1" {
		t.Errorf("pmid = %q", pmid)
	}
	if f.from != "acme@mail.fieldwatchai.io" || len(f.to) != 1 || f.to[0] != "customer@gmail.com" {
		t.Errorf("envelope wrong: from=%q to=%v", f.from, f.to)
	}
	for _, want := range []string{
		"From: acme@mail.fieldwatchai.io\r\n",
		"To: customer@gmail.com\r\n",
		"Reply-To: acme@fieldwatchai.io\r\n",
		"Subject: Re: My unit is down\r\n",
		"In-Reply-To: <abc@mail.gmail.com>\r\n",
		"References: <abc@mail.gmail.com>\r\n",
		"\r\n\r\nYour unit is back online.",
	} {
		if !strings.Contains(f.mime, want) {
			t.Errorf("MIME missing %q\n---\n%s", want, f.mime)
		}
	}
}

func TestSendRichRequiresFromAndTo(t *testing.T) {
	s := New(&fakeRaw{})
	if _, err := s.SendRich(context.Background(), outbound.Account{}, outbound.Message{Body: "x", From: "a@b"}); err == nil {
		t.Error("expected error on missing To")
	}
	if _, err := s.SendRich(context.Background(), outbound.Account{}, outbound.Message{To: "c@d", Body: "x"}); err == nil {
		t.Error("expected error on missing From")
	}
}

func TestBuildReplyThreadsAndSetsFallbackFrom(t *testing.T) {
	raw := []byte(`{"mail":{"commonHeaders":{"messageId":"<orig@mail.gmail.com>","subject":"My unit is down"}}}`)
	msg := BuildReply("acme@fieldwatchai.io", "jane@gmail.com", "On it.", raw)

	if msg.From != "acme@mail.fieldwatchai.io" {
		t.Errorf("From = %q, want acme@mail.fieldwatchai.io (fallback)", msg.From)
	}
	if msg.ReplyTo != "acme@fieldwatchai.io" {
		t.Errorf("ReplyTo = %q, want the ingest address (loop-back)", msg.ReplyTo)
	}
	if msg.Subject != "Re: My unit is down" {
		t.Errorf("Subject = %q", msg.Subject)
	}
	if msg.InReplyTo != "<orig@mail.gmail.com>" {
		t.Errorf("InReplyTo = %q", msg.InReplyTo)
	}
	if !strings.Contains(msg.References, "<orig@mail.gmail.com>") {
		t.Errorf("References = %q", msg.References)
	}
}

func TestBuildReplyHandlesMissingSubjectAndBadPayload(t *testing.T) {
	msg := BuildReply("acme@fieldwatchai.io", "j@x.com", "hi", []byte("not json"))
	if msg.Subject != "Re:" {
		t.Errorf("Subject = %q, want bare Re:", msg.Subject)
	}
	if msg.From != "acme@mail.fieldwatchai.io" {
		t.Errorf("From = %q", msg.From)
	}
	// already-Re: subject shouldn't double-prefix
	raw := []byte(`{"mail":{"commonHeaders":{"subject":"Re: ticket","messageId":"<m@x>"}}}`)
	if got := BuildReply("acme@fieldwatchai.io", "j@x.com", "hi", raw).Subject; got != "Re: ticket" {
		t.Errorf("Subject = %q, want no double Re:", got)
	}
}

func TestBuildNewOpensAThreadWithoutForgingHeaders(t *testing.T) {
	msg := BuildNew("acme@fieldwatchai.io", "jane@gmail.com", "Status check", "Report your status.")

	if msg.Subject != "Status check" {
		t.Errorf("Subject = %q, want the caller's subject verbatim (no Re: prefix)", msg.Subject)
	}
	if msg.From != "acme@mail.fieldwatchai.io" {
		t.Errorf("From = %q, want the fallback sender", msg.From)
	}
	if msg.ReplyTo != "acme@fieldwatchai.io" {
		t.Errorf("ReplyTo = %q, want the ingest address (loop-back)", msg.ReplyTo)
	}
	// There is no prior message: threading headers must be absent, not invented.
	if msg.InReplyTo != "" || msg.References != "" {
		t.Errorf("threading headers set on a new thread: InReplyTo=%q References=%q", msg.InReplyTo, msg.References)
	}
	if msg.To != "jane@gmail.com" || msg.Body != "Report your status." {
		t.Errorf("To/Body wrong: %q / %q", msg.To, msg.Body)
	}
}

func TestBuildNewFallsBackToDefaultSubject(t *testing.T) {
	if got := BuildNew("acme@fieldwatchai.io", "j@x.com", "   ", "hi").Subject; got != DefaultSubject {
		t.Errorf("Subject = %q, want %q", got, DefaultSubject)
	}
}
