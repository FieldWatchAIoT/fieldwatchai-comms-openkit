package telegram

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestVerify_ValidSecret(t *testing.T) {
	l := New("s3cr3t", nil, discardLogger())
	req := httptest.NewRequest("POST", "/inbound/telegram?bot=fwbot", strings.NewReader("{}"))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "s3cr3t")
	if err := l.Verify(req, []byte("{}")); err != nil {
		t.Fatalf("valid secret should verify, got %v", err)
	}
}

func TestVerify_WrongSecretFails(t *testing.T) {
	l := New("s3cr3t", nil, discardLogger())
	req := httptest.NewRequest("POST", "/inbound/telegram", strings.NewReader("{}"))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "nope")
	if l.Verify(req, []byte("{}")) == nil {
		t.Error("wrong secret must fail")
	}
}

func TestVerify_MissingHeaderFails(t *testing.T) {
	l := New("s3cr3t", nil, discardLogger())
	req := httptest.NewRequest("POST", "/inbound/telegram", strings.NewReader("{}"))
	if l.Verify(req, []byte("{}")) == nil {
		t.Error("missing header must fail closed")
	}
}

func TestVerify_NoSecretConfiguredFailsClosed(t *testing.T) {
	l := New("", nil, discardLogger())
	req := httptest.NewRequest("POST", "/inbound/telegram", strings.NewReader("{}"))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "anything")
	if l.Verify(req, []byte("{}")) == nil {
		t.Error("unset secret must fail closed")
	}
}
