package ingest

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/canonical"
	"github.com/google/uuid"
)

type fakeSvc struct {
	out Result
	err error
}

func (f *fakeSvc) Ingest(_ context.Context, _ canonical.Message) (Result, error) {
	return f.out, f.err
}

func testHandler(svc serviceAPI) http.Handler {
	h := NewHandler(svc, slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return mux
}

const validBody = `{"sender":{"endpoint":"+1242","platform":"whatsapp"},"body":{"text":"hi"},"meta":{"platform_message_id":"x","received_at":"2026-06-06T04:00:00Z","account_id":"11111111-1111-1111-1111-111111111111","raw_payload":{"a":1}}}`

func post(h http.Handler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/ingest", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestIngestNewReturns201(t *testing.T) {
	id := uuid.New()
	h := testHandler(&fakeSvc{out: Result{MessageID: id, IsReplay: false, Action: "echo_back"}})
	rec := post(h, validBody)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, id.String()) {
		t.Errorf("missing message_id: %s", body)
	}
	if !strings.Contains(body, `"policy_action":"echo_back"`) {
		t.Errorf("expected policy_action in response: %s", body)
	}
}

func TestIngestReplayReturns200WithFlag(t *testing.T) {
	h := testHandler(&fakeSvc{out: Result{MessageID: uuid.New(), IsReplay: true}})
	rec := post(h, validBody)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"is_replay":true`) {
		t.Errorf("missing is_replay: %s", rec.Body.String())
	}
}

func TestIngestInvalidAccountReturns400(t *testing.T) {
	h := testHandler(&fakeSvc{err: ErrInvalidAccount})
	if rec := post(h, validBody); rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestIngestAccountNotFoundReturns404(t *testing.T) {
	h := testHandler(&fakeSvc{err: ErrAccountNotFound})
	if rec := post(h, validBody); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestIngestBadJSONReturns400(t *testing.T) {
	h := testHandler(&fakeSvc{})
	if rec := post(h, `{not json`); rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
