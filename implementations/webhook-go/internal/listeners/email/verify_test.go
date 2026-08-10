package email

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestVerify_ValidToken(t *testing.T) {
	l := New("sek", "arn:topic", nil, discardLogger(), nil)
	req := httptest.NewRequest("POST", "/inbound/email-ses?token=sek", strings.NewReader("{}"))
	if err := l.Verify(req, []byte("{}")); err != nil {
		t.Fatalf("valid token should verify, got %v", err)
	}
}

func TestVerify_WrongTokenFails(t *testing.T) {
	l := New("sek", "arn:topic", nil, discardLogger(), nil)
	req := httptest.NewRequest("POST", "/inbound/email-ses?token=nope", strings.NewReader("{}"))
	if l.Verify(req, []byte("{}")) == nil {
		t.Error("wrong token must fail")
	}
}

func TestVerify_MissingTokenFails(t *testing.T) {
	l := New("sek", "arn:topic", nil, discardLogger(), nil)
	req := httptest.NewRequest("POST", "/inbound/email-ses", strings.NewReader("{}"))
	if l.Verify(req, []byte("{}")) == nil {
		t.Error("missing token must fail closed")
	}
}

func TestVerify_NoSecretFailsClosed(t *testing.T) {
	l := New("", "arn:topic", nil, discardLogger(), nil)
	req := httptest.NewRequest("POST", "/inbound/email-ses?token=anything", strings.NewReader("{}"))
	if l.Verify(req, []byte("{}")) == nil {
		t.Error("unset secret must fail closed")
	}
}
