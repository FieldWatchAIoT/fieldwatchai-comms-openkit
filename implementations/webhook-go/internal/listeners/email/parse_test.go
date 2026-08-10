package email

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/accounts"
)

const testTopicARN = "arn:aws:sns:us-west-2:000000000000:comms-webhook-email"

type fakeResolver struct {
	gotType, gotIdent string
	acc               accounts.Account
	err               error
}

func (f *fakeResolver) Resolve(_ context.Context, typ, ident string) (accounts.Account, error) {
	f.gotType, f.gotIdent = typ, ident
	return f.acc, f.err
}

func newListener(r accounts.Resolver) *Listener {
	return New("sek", testTopicARN, r, discardLogger(), nil)
}

// snsNotification wraps a raw email in an SES "Received" SNS Notification
// envelope where the To: header and the delivered-to address are the same
// (direct mail, no forwarding).
func snsNotification(topicARN, rawEmail, from, to, subject, messageID string) []byte {
	return snsNotificationTo(topicARN, rawEmail, from, to, to, subject, messageID)
}

// snsNotificationForwarded models forwarded mail: the To: header keeps the
// business's public address (toHeader) while SES's receipt recipient is the
// hidden ingest address it was actually delivered to (deliveredTo).
func snsNotificationForwarded(topicARN, rawEmail, from, toHeader, deliveredTo, subject, messageID string) []byte {
	return snsNotificationTo(topicARN, rawEmail, from, toHeader, deliveredTo, subject, messageID)
}

func snsNotificationTo(topicARN, rawEmail, from, toHeader, deliveredTo, subject, messageID string) []byte {
	ses := map[string]any{
		"notificationType": "Received",
		"mail": map[string]any{
			"messageId":   messageID,
			"source":      from,
			"destination": []string{toHeader},
			"commonHeaders": map[string]any{
				"from":    []string{from},
				"to":      []string{toHeader},
				"subject": subject,
			},
		},
		"receipt": map[string]any{
			"recipients": []string{deliveredTo},
		},
		"content": base64.StdEncoding.EncodeToString([]byte(rawEmail)),
	}
	sesJSON, _ := json.Marshal(ses)
	sns := map[string]any{
		"Type":      "Notification",
		"MessageId": "sns-" + messageID,
		"TopicArn":  topicARN,
		"Message":   string(sesJSON),
		"Timestamp": "2026-06-26T10:00:00.000Z",
	}
	b, _ := json.Marshal(sns)
	return b
}

func TestParse_TextEmail(t *testing.T) {
	raw := "From: John Doe <john@example.com>\r\n" +
		"To: support@inbound.example.com\r\n" +
		"Subject: Need help\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n" +
		"\r\n" +
		"My sensor is offline."
	r := &fakeResolver{acc: accounts.Account{ID: "acct-1"}}
	body := snsNotification(testTopicARN, raw, "John Doe <john@example.com>", "support@inbound.example.com", "Need help", "msg-123")

	msgs, err := newListener(r).Parse(body)
	if err != nil {
		t.Fatalf("Parse err: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("want 1 msg, got %d", len(msgs))
	}
	m := msgs[0]
	if m.Sender.Platform != "email" {
		t.Errorf("platform = %q, want email", m.Sender.Platform)
	}
	if m.Sender.Endpoint != "john@example.com" {
		t.Errorf("endpoint = %q, want bare address", m.Sender.Endpoint)
	}
	if m.Sender.Email == nil || *m.Sender.Email != "john@example.com" {
		t.Errorf("email = %v, want john@example.com", m.Sender.Email)
	}
	if m.Sender.FullName == nil || *m.Sender.FullName != "John Doe" {
		t.Errorf("full_name = %v, want John Doe", m.Sender.FullName)
	}
	if m.Meta.PlatformMessageID != "msg-123" {
		t.Errorf("platform_message_id = %q", m.Meta.PlatformMessageID)
	}
	if m.Meta.AccountID != "acct-1" {
		t.Errorf("account_id = %q", m.Meta.AccountID)
	}
	if m.Body.Text == nil {
		t.Fatal("body.text is nil")
	}
	if !strings.Contains(*m.Body.Text, "My sensor is offline.") {
		t.Errorf("body missing message text: %q", *m.Body.Text)
	}
	if !strings.Contains(*m.Body.Text, "Need help") {
		t.Errorf("subject should be prepended to body: %q", *m.Body.Text)
	}
	if r.gotType != "email-ses" || r.gotIdent != "support@inbound.example.com" {
		t.Errorf("resolve(%q,%q), want (email-ses, support@inbound.example.com)", r.gotType, r.gotIdent)
	}
}

func TestParse_MultipartAlternativePrefersText(t *testing.T) {
	raw := "From: a@b.com\r\nTo: support@inbound.example.com\r\nSubject: Hi\r\n" +
		"Content-Type: multipart/alternative; boundary=\"BOUND\"\r\n\r\n" +
		"--BOUND\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\nplain body here\r\n" +
		"--BOUND\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n<p>html body</p>\r\n" +
		"--BOUND--\r\n"
	r := &fakeResolver{acc: accounts.Account{ID: "a"}}
	body := snsNotification(testTopicARN, raw, "a@b.com", "support@inbound.example.com", "Hi", "m1")

	msgs, err := newListener(r).Parse(body)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("want 1 msg, got %d err=%v", len(msgs), err)
	}
	txt := *msgs[0].Body.Text
	if !strings.Contains(txt, "plain body here") {
		t.Errorf("want text/plain part, got %q", txt)
	}
	if strings.Contains(txt, "html body") {
		t.Errorf("should not include html part: %q", txt)
	}
}

func TestParse_QuotedPrintableDecoded(t *testing.T) {
	raw := "From: a@b.com\r\nTo: support@inbound.example.com\r\nSubject: S\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n" +
		"Caf=C3=A9 =3D done"
	r := &fakeResolver{acc: accounts.Account{ID: "a"}}
	body := snsNotification(testTopicARN, raw, "a@b.com", "support@inbound.example.com", "S", "m2")

	msgs, err := newListener(r).Parse(body)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("want 1 msg, got %d err=%v", len(msgs), err)
	}
	if txt := *msgs[0].Body.Text; !strings.Contains(txt, "Café = done") {
		t.Errorf("quoted-printable not decoded: %q", txt)
	}
}

func TestParse_WrongTopicARNDrops(t *testing.T) {
	r := &fakeResolver{acc: accounts.Account{ID: "a"}}
	body := snsNotification("arn:aws:sns:us-west-2:000000000000:other", "x", "a@b.com", "support@inbound.example.com", "S", "m3")
	msgs, err := newListener(r).Parse(body)
	if err != nil || msgs != nil {
		t.Errorf("wrong topic must drop (nil,nil), got msgs=%v err=%v", msgs, err)
	}
}

func TestParse_SubscriptionConfirmationConfirms(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	env := map[string]any{
		"Type":         "SubscriptionConfirmation",
		"MessageId":    "sub-1",
		"TopicArn":     testTopicARN,
		"SubscribeURL": srv.URL,
		"Token":        "tok",
		"Timestamp":    "2026-06-26T10:00:00.000Z",
	}
	body, _ := json.Marshal(env)

	l := New("sek", testTopicARN, &fakeResolver{}, discardLogger(), srv.Client())
	msgs, err := l.Parse(body)
	if err != nil || msgs != nil {
		t.Errorf("confirmation must drop (nil,nil), got msgs=%v err=%v", msgs, err)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Errorf("SubscribeURL should be GET-ed exactly once, hits=%d", hits)
	}
}

func TestParse_SubscriptionConfirmationLogsSubscribeURL(t *testing.T) {
	// Deployments with locked-down egress may fail the auto-confirm GET to SNS;
	// the documented fallback is an operator confirming from the logged
	// SubscribeURL. The URL must therefore appear in the logs.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	subURL := srv.URL + "/?Action=ConfirmSubscription&Token=abc123"
	env := map[string]any{
		"Type":         "SubscriptionConfirmation",
		"TopicArn":     testTopicARN,
		"SubscribeURL": subURL,
		"MessageId":    "s1",
		"Timestamp":    "t",
	}
	body, _ := json.Marshal(env)

	l := New("sek", testTopicARN, &fakeResolver{}, logger, srv.Client())
	l.Parse(body)

	if !strings.Contains(buf.String(), subURL) {
		t.Errorf("SubscribeURL must be logged for the manual-confirm fallback; log=%s", buf.String())
	}
}

func TestParse_UnregisteredDrops(t *testing.T) {
	r := &fakeResolver{err: accounts.ErrNotFound}
	raw := "From: a@b.com\r\nTo: nobody@inbound.example.com\r\nSubject: S\r\nContent-Type: text/plain\r\n\r\nhi"
	body := snsNotification(testTopicARN, raw, "a@b.com", "nobody@inbound.example.com", "S", "m4")
	msgs, err := newListener(r).Parse(body)
	if err != nil || msgs != nil {
		t.Errorf("unregistered must drop, got msgs=%v err=%v", msgs, err)
	}
}

func TestParse_TransientResolveErrorRetries(t *testing.T) {
	r := &fakeResolver{err: errors.New("channels down")}
	raw := "From: a@b.com\r\nTo: support@inbound.example.com\r\nSubject: S\r\nContent-Type: text/plain\r\n\r\nhi"
	body := snsNotification(testTopicARN, raw, "a@b.com", "support@inbound.example.com", "S", "m5")
	if _, err := newListener(r).Parse(body); err == nil {
		t.Error("transient resolve error must surface (retry/500)")
	}
}

func TestParse_MalformedDrops(t *testing.T) {
	msgs, err := newListener(&fakeResolver{}).Parse([]byte("not json"))
	if err != nil || msgs != nil {
		t.Errorf("malformed must drop, got msgs=%v err=%v", msgs, err)
	}
}

func TestParse_NonReceivedNotificationDrops(t *testing.T) {
	sesJSON, _ := json.Marshal(map[string]any{"notificationType": "AmazonSnsSubscriptionSucceeded"})
	env := map[string]any{"Type": "Notification", "TopicArn": testTopicARN, "Message": string(sesJSON), "MessageId": "x", "Timestamp": "t"}
	body, _ := json.Marshal(env)
	msgs, err := newListener(&fakeResolver{}).Parse(body)
	if err != nil || msgs != nil {
		t.Errorf("non-Received notification must drop, got msgs=%v err=%v", msgs, err)
	}
}

func TestParse_ResolvesOnDeliveredToNotToHeader(t *testing.T) {
	// Forwarded mail: the To: header is the business's public address, but SES
	// delivered it to our hidden ingest address (the receipt recipient). The
	// account MUST resolve on the delivered-to address, not the To: header.
	r := &fakeResolver{acc: accounts.Account{ID: "acct-acme"}}
	raw := "From: customer@gmail.com\r\nTo: support@acme.com\r\nSubject: Help\r\nContent-Type: text/plain\r\n\r\nmy unit is down"
	body := snsNotificationForwarded(testTopicARN, raw, "customer@gmail.com", "support@acme.com", "acme@ingest.example.com", "Help", "m7")

	msgs, err := newListener(r).Parse(body)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("want 1 msg, got %d err=%v", len(msgs), err)
	}
	if r.gotIdent != "acme@ingest.example.com" {
		t.Errorf("account must resolve on delivered-to (envelope recipient), got %q", r.gotIdent)
	}
	// The sender is the original customer, preserved through the forward.
	if msgs[0].Sender.Endpoint != "customer@gmail.com" {
		t.Errorf("sender should be the original customer, got %q", msgs[0].Sender.Endpoint)
	}
}

func TestParse_ToAddressNormalizedForResolution(t *testing.T) {
	r := &fakeResolver{acc: accounts.Account{ID: "a"}}
	raw := "From: a@b.com\r\nTo: x\r\nSubject: S\r\nContent-Type: text/plain\r\n\r\nhi"
	body := snsNotification(testTopicARN, raw, "a@b.com", "Support Desk <Support@Inbound.Example.Com>", "S", "m6")
	newListener(r).Parse(body)
	if r.gotIdent != "support@inbound.example.com" {
		t.Errorf("To should normalize to bare lowercase address, got %q", r.gotIdent)
	}
}
