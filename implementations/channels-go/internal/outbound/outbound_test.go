package outbound

import (
	"context"
	"testing"
)

type fakeAdapter struct{ id string }

func (f fakeAdapter) Send(_ context.Context, _ Account, _, _ string) (string, error) {
	return f.id, nil
}

func TestRegistryGet(t *testing.T) {
	r := NewRegistry()
	r.Register("whatsapp", fakeAdapter{id: "wa"})

	got, err := r.Get("whatsapp")
	if err != nil {
		t.Fatalf("Get(whatsapp): %v", err)
	}
	id, _ := got.Send(context.Background(), Account{}, "+1", "hi")
	if id != "wa" {
		t.Errorf("got adapter id = %q, want wa", id)
	}
}

func TestRegistryUnknownType(t *testing.T) {
	r := NewRegistry()
	if _, err := r.Get("telegram"); err == nil {
		t.Fatal("Get(unknown) = nil error, want error")
	}
}

func TestPlatformNormalisesProviderAway(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"whatsapp", "whatsapp"},
		{"whatsapp-twilio", "whatsapp"},
		{"sms-twilio", "sms"},
		{"telegram", "telegram"},
		{"email-ses", "email"},
		// An adapter we haven't classified yet must pass through, not be
		// guessed at — a wrong label is worse than an unfamiliar one.
		{"signal-future", "signal-future"},
		{"", ""},
	} {
		if got := Platform(tc.in); got != tc.want {
			t.Errorf("Platform(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
