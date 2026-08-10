package twilio

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// The official Twilio worked example (docs: Validating Signatures from Twilio).
// URL https://example.com/myapp.php?foo=1&bar=2, AuthToken "12345" →
// X-Twilio-Signature L/OH5YylLD5NRKLltdqwSvS0BnU=. This is our spec oracle.
const (
	vectorToken = "12345"
	vectorBase  = "https://example.com"
	vectorPath  = "/myapp.php?foo=1&bar=2"
	vectorBody  = "CallSid=CA1234567890ABCDE&Caller=%2B14158675310&Digits=1234&From=%2B14158675310&To=%2B18005551212"
	vectorSig   = "L/OH5YylLD5NRKLltdqwSvS0BnU="
)

func TestVerify_OfficialTwilioVector(t *testing.T) {
	l := New("whatsapp-twilio", vectorToken, vectorBase, nil, discardLogger())
	req := httptest.NewRequest("POST", vectorBase+vectorPath, strings.NewReader(vectorBody))
	req.Header.Set("X-Twilio-Signature", vectorSig)
	if err := l.Verify(req, []byte(vectorBody)); err != nil {
		t.Fatalf("official Twilio test vector must validate, got %v", err)
	}
}

func TestVerify_WrongSignatureFails(t *testing.T) {
	l := New("whatsapp-twilio", vectorToken, vectorBase, nil, discardLogger())
	req := httptest.NewRequest("POST", vectorBase+vectorPath, strings.NewReader(vectorBody))
	req.Header.Set("X-Twilio-Signature", "deadbeefdeadbeefdeadbeefdeadbeef=")
	if l.Verify(req, []byte(vectorBody)) == nil {
		t.Error("a wrong signature must not validate")
	}
}

func TestVerify_TamperedBodyFails(t *testing.T) {
	l := New("whatsapp-twilio", vectorToken, vectorBase, nil, discardLogger())
	tampered := vectorBody + "&Injected=evil"
	req := httptest.NewRequest("POST", vectorBase+vectorPath, strings.NewReader(tampered))
	req.Header.Set("X-Twilio-Signature", vectorSig) // signature of the original body
	if l.Verify(req, []byte(tampered)) == nil {
		t.Error("tampered body must not validate against the original signature")
	}
}

func TestVerify_MissingSignatureHeaderFails(t *testing.T) {
	l := New("whatsapp-twilio", vectorToken, vectorBase, nil, discardLogger())
	req := httptest.NewRequest("POST", vectorBase+vectorPath, strings.NewReader(vectorBody))
	if l.Verify(req, []byte(vectorBody)) == nil {
		t.Error("missing X-Twilio-Signature must fail closed")
	}
}

func TestVerify_EmptyAuthTokenFailsClosed(t *testing.T) {
	l := New("whatsapp-twilio", "", vectorBase, nil, discardLogger())
	req := httptest.NewRequest("POST", vectorBase+vectorPath, strings.NewReader(vectorBody))
	req.Header.Set("X-Twilio-Signature", vectorSig)
	if l.Verify(req, []byte(vectorBody)) == nil {
		t.Error("an unset auth token must fail closed, never open")
	}
}
