package ultramsg

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/accounts"
)

func verifyListener() *Listener {
	r, _ := accounts.NewConfigResolver("")
	return New("sekret", r, discardLogger())
}

func TestVerify_AcceptsMatchingToken(t *testing.T) {
	l := verifyListener()
	req := httptest.NewRequest("POST", "/inbound/whatsapp-ultramsg?token=sekret", nil)
	if err := l.Verify(req, nil); err != nil {
		t.Errorf("Verify rejected matching token: %v", err)
	}
}

func TestVerify_RejectsWrongToken(t *testing.T) {
	l := verifyListener()
	req := httptest.NewRequest("POST", "/inbound/whatsapp-ultramsg?token=wrong", nil)
	if err := l.Verify(req, nil); err == nil {
		t.Error("Verify accepted a wrong token")
	}
}

func TestVerify_RejectsMissingToken(t *testing.T) {
	l := verifyListener()
	req := httptest.NewRequest("POST", "/inbound/whatsapp-ultramsg", nil)
	if err := l.Verify(req, nil); err == nil {
		t.Error("Verify accepted a missing token")
	}
}

// TestVerify_RejectsWhenSecretUnconfigured ensures a misconfigured listener
// (no secret) fails closed rather than accepting empty tokens.
func TestVerify_RejectsWhenSecretUnconfigured(t *testing.T) {
	r, _ := accounts.NewConfigResolver("")
	l := New("", r, discardLogger())
	req := httptest.NewRequest("POST", "/inbound/whatsapp-ultramsg?token=", nil)
	if err := l.Verify(req, nil); err == nil {
		t.Error("Verify accepted request with no configured secret")
	}
}

// TestVerify_ErrorDoesNotLeakSecret ensures the rejection error never contains
// the secret value (no-secret-in-logs guard).
func TestVerify_ErrorDoesNotLeakSecret(t *testing.T) {
	l := verifyListener()
	req := httptest.NewRequest("POST", "/inbound/whatsapp-ultramsg?token=wrong", nil)
	err := l.Verify(req, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "sekret") {
		t.Errorf("verify error leaked the secret: %q", err.Error())
	}
}

func TestListener_IDAndPath(t *testing.T) {
	l := verifyListener()
	if l.ID() != "whatsapp-ultramsg" {
		t.Errorf("ID = %q", l.ID())
	}
	if l.Path() != "/inbound/whatsapp-ultramsg" {
		t.Errorf("Path = %q", l.Path())
	}
}
