package ultramsg

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/outbound"
)

func TestSendSuccess(t *testing.T) {
	var gotPath, gotToken, gotTo, gotBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = r.ParseForm()
		gotToken = r.PostFormValue("token")
		gotTo = r.PostFormValue("to")
		gotBody = r.PostFormValue("body")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sent":"true","message":"ok","id":"99887"}`))
	}))
	defer ts.Close()

	s := &Sender{Client: ts.Client(), BaseURL: ts.URL}
	id, err := s.Send(context.Background(), outbound.Account{Identifier: "179557", Token: "tok"}, "+12425550042", "hello there")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if id != "99887" {
		t.Errorf("id = %q, want 99887", id)
	}
	// Bare numeric identifier must be prefixed to UltraMSG's instance path form.
	if gotPath != "/instance179557/messages/chat" {
		t.Errorf("path = %q, want /instance179557/messages/chat", gotPath)
	}
	if gotToken != "tok" || gotTo != "+12425550042" || gotBody != "hello there" {
		t.Errorf("form = token:%q to:%q body:%q", gotToken, gotTo, gotBody)
	}
}

func TestSendDoesNotDoublePrefixInstance(t *testing.T) {
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"sent":"true","id":"1"}`))
	}))
	defer ts.Close()
	s := &Sender{Client: ts.Client(), BaseURL: ts.URL}
	if _, err := s.Send(context.Background(), outbound.Account{Identifier: "instance179557", Token: "t"}, "+1", "x"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotPath != "/instance179557/messages/chat" {
		t.Errorf("path = %q, want /instance179557/messages/chat (no double prefix)", gotPath)
	}
}

func TestSendNon2xxIsError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer ts.Close()

	s := &Sender{Client: ts.Client(), BaseURL: ts.URL}
	if _, err := s.Send(context.Background(), outbound.Account{Identifier: "i", Token: "t"}, "+1", "x"); err == nil {
		t.Fatal("expected error on non-2xx")
	}
}

func TestSendNotAcceptedIsError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"sent":"false","error":"invalid number"}`))
	}))
	defer ts.Close()

	s := &Sender{Client: ts.Client(), BaseURL: ts.URL}
	if _, err := s.Send(context.Background(), outbound.Account{Identifier: "i", Token: "t"}, "+1", "x"); err == nil {
		t.Fatal("expected error when sent != true")
	}
}
