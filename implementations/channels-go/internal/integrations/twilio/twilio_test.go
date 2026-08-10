package twilio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/outbound"
)

func creds(sid, tok string) string {
	b, _ := json.Marshal(map[string]string{"account_sid": sid, "auth_token": tok})
	return string(b)
}

func TestSendPostsToTwilioWithBasicAuthAndWhatsAppPrefixes(t *testing.T) {
	var gotPath, gotUser, gotPass string
	var gotForm url.Values
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotUser, gotPass, _ = r.BasicAuth()
		_ = r.ParseForm()
		gotForm = r.PostForm
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"sid":"SM999","status":"queued"}`))
	}))
	defer ts.Close()

	s := &Sender{Client: ts.Client(), BaseURL: ts.URL}
	acct := outbound.Account{Type: "whatsapp-twilio", Identifier: "+12897792824", Token: creds("AC123", "tok456")}

	pmid, err := s.Send(context.Background(), acct, "+15551234567", "hello there")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if pmid != "SM999" {
		t.Errorf("pmid = %q, want SM999", pmid)
	}
	if gotPath != "/2010-04-01/Accounts/AC123/Messages.json" {
		t.Errorf("path = %q", gotPath)
	}
	if gotUser != "AC123" || gotPass != "tok456" {
		t.Errorf("basic auth = %q:%q, want AC123:tok456", gotUser, gotPass)
	}
	if gotForm.Get("From") != "whatsapp:+12897792824" {
		t.Errorf("From = %q, want whatsapp:+12897792824", gotForm.Get("From"))
	}
	if gotForm.Get("To") != "whatsapp:+15551234567" {
		t.Errorf("To = %q, want whatsapp:+15551234567", gotForm.Get("To"))
	}
	if gotForm.Get("Body") != "hello there" {
		t.Errorf("Body = %q", gotForm.Get("Body"))
	}
}

func TestSendDoesNotDoublePrefixWhatsApp(t *testing.T) {
	var gotForm url.Values
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.PostForm
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"sid":"SM1"}`))
	}))
	defer ts.Close()

	s := &Sender{Client: ts.Client(), BaseURL: ts.URL}
	acct := outbound.Account{Identifier: "whatsapp:+12897792824", Token: creds("AC1", "t")}
	if _, err := s.Send(context.Background(), acct, "whatsapp:+15551234567", "x"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotForm.Get("From") != "whatsapp:+12897792824" || gotForm.Get("To") != "whatsapp:+15551234567" {
		t.Errorf("double-prefixed: From=%q To=%q", gotForm.Get("From"), gotForm.Get("To"))
	}
}

func TestSendSMSUsesBareNumbers(t *testing.T) {
	var gotForm url.Values
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.PostForm
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"sid":"SM-sms"}`))
	}))
	defer ts.Close()

	s := &Sender{Client: ts.Client(), BaseURL: ts.URL}
	// type=sms-twilio → SMS, no whatsapp: prefix on From/To.
	acct := outbound.Account{Type: "sms-twilio", Identifier: "+12897792824", Token: creds("AC1", "t")}
	pmid, err := s.Send(context.Background(), acct, "+15551234567", "sms reply")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if pmid != "SM-sms" {
		t.Errorf("pmid = %q", pmid)
	}
	if gotForm.Get("From") != "+12897792824" {
		t.Errorf("From = %q, want bare +12897792824 (no whatsapp:)", gotForm.Get("From"))
	}
	if gotForm.Get("To") != "+15551234567" {
		t.Errorf("To = %q, want bare +15551234567 (no whatsapp:)", gotForm.Get("To"))
	}
}

func TestSendSMSStripsIncomingWhatsAppPrefix(t *testing.T) {
	var gotForm url.Values
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.PostForm
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"sid":"SM2"}`))
	}))
	defer ts.Close()

	s := &Sender{Client: ts.Client(), BaseURL: ts.URL}
	// defensive: even if a whatsapp:-prefixed endpoint is passed for an SMS account, strip it.
	acct := outbound.Account{Type: "sms-twilio", Identifier: "whatsapp:+12897792824", Token: creds("AC1", "t")}
	if _, err := s.Send(context.Background(), acct, "whatsapp:+15551234567", "x"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotForm.Get("From") != "+12897792824" || gotForm.Get("To") != "+15551234567" {
		t.Errorf("sms should strip whatsapp:: From=%q To=%q", gotForm.Get("From"), gotForm.Get("To"))
	}
}

func TestSendErrorStatusReturnsTwilioMessage(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":21211,"message":"Invalid 'To' Phone Number"}`))
	}))
	defer ts.Close()

	s := &Sender{Client: ts.Client(), BaseURL: ts.URL}
	acct := outbound.Account{Identifier: "+12897792824", Token: creds("AC1", "t")}
	_, err := s.Send(context.Background(), acct, "bad", "x")
	if err == nil {
		t.Fatal("expected error on 400")
	}
	if !strings.Contains(err.Error(), "21211") || !strings.Contains(err.Error(), "Invalid 'To'") {
		t.Errorf("error should surface twilio code+message: %v", err)
	}
}

func TestSendBadCredentialsFailsBeforeHTTP(t *testing.T) {
	called := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusCreated)
	}))
	defer ts.Close()

	s := &Sender{Client: ts.Client(), BaseURL: ts.URL}
	// missing account_sid
	acct := outbound.Account{Identifier: "+12897792824", Token: `{"auth_token":"t"}`}
	if _, err := s.Send(context.Background(), acct, "+1555", "x"); err == nil {
		t.Fatal("expected error on missing account_sid")
	}
	if called {
		t.Error("must not call Twilio with incomplete credentials")
	}
}

// compile-time interface guard mirrored in test for clarity.
var _ outbound.Outbound = (*Sender)(nil)
