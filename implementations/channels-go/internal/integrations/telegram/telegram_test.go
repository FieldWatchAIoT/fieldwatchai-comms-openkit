package telegram

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/outbound"
)

func TestSendPostsToTelegramSendMessage(t *testing.T) {
	var gotPath string
	var gotForm url.Values
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = r.ParseForm()
		gotForm = r.PostForm
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":555}}`))
	}))
	defer ts.Close()

	s := &Sender{Client: ts.Client(), BaseURL: ts.URL}
	acct := outbound.Account{Type: "telegram", Identifier: "fieldwatch_comms_bot", Token: "12345:ABCdef"}
	pmid, err := s.Send(context.Background(), acct, "987654321", "hello telegram")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if pmid != "555" {
		t.Errorf("pmid = %q, want 555", pmid)
	}
	if gotPath != "/bot12345:ABCdef/sendMessage" {
		t.Errorf("path = %q, want /bot<token>/sendMessage", gotPath)
	}
	if gotForm.Get("chat_id") != "987654321" {
		t.Errorf("chat_id = %q (the `to` is the Telegram chat id)", gotForm.Get("chat_id"))
	}
	if gotForm.Get("text") != "hello telegram" {
		t.Errorf("text = %q", gotForm.Get("text"))
	}
}

func TestSendTelegramErrorReturnsDescription(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`))
	}))
	defer ts.Close()
	s := &Sender{Client: ts.Client(), BaseURL: ts.URL}
	_, err := s.Send(context.Background(), outbound.Account{Token: "12345:ABCdef"}, "0", "x")
	if err == nil {
		t.Fatal("expected error on ok:false")
	}
	if !strings.Contains(err.Error(), "chat not found") {
		t.Errorf("error should surface telegram description: %v", err)
	}
	if strings.Contains(err.Error(), "12345:ABCdef") {
		t.Errorf("error must not leak the bot token: %v", err)
	}
}

func TestSendTelegramMissingToken(t *testing.T) {
	s := &Sender{Client: http.DefaultClient, BaseURL: "http://unused.invalid"}
	if _, err := s.Send(context.Background(), outbound.Account{}, "1", "x"); err == nil {
		t.Fatal("expected error on missing bot token")
	}
}

var _ outbound.Outbound = (*Sender)(nil)
