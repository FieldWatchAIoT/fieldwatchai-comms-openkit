package listeners

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/webhook-go/internal/canonical"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// fakeListener is a configurable Listener for exercising the registry.
type fakeListener struct {
	id        string
	path      string
	verifyErr error
	parseMsgs []canonical.Message
	parseErr  error
	gotBody   []byte
}

func (f *fakeListener) ID() string   { return f.id }
func (f *fakeListener) Path() string { return f.path }
func (f *fakeListener) Verify(r *http.Request, body []byte) error { return f.verifyErr }
func (f *fakeListener) Parse(body []byte) ([]canonical.Message, error) {
	f.gotBody = body
	return f.parseMsgs, f.parseErr
}

// fakeEnqueuer captures enqueued messages and can simulate a queue failure.
type fakeEnqueuer struct {
	msgs []canonical.Message
	err  error
}

func (f *fakeEnqueuer) Enqueue(_ context.Context, m canonical.Message) error {
	if f.err != nil {
		return f.err
	}
	f.msgs = append(f.msgs, m)
	return nil
}

func post(body string) *http.Request {
	return httptest.NewRequest(http.MethodPost, "/inbound/x", strings.NewReader(body))
}

func msg(id string) canonical.Message {
	return canonical.Message{Meta: canonical.Meta{PlatformMessageID: id}}
}

// TestHandler_VerifyFailReturns401 confirms a failed signature/secret check is
// rejected with 401 and nothing is enqueued or parsed downstream.
func TestHandler_VerifyFailReturns401(t *testing.T) {
	l := &fakeListener{verifyErr: errors.New("bad secret")}
	enq := &fakeEnqueuer{}
	rr := httptest.NewRecorder()

	Handler(l, enq, discardLogger()).ServeHTTP(rr, post(`{}`))

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
	if len(enq.msgs) != 0 {
		t.Errorf("enqueued %d messages on verify failure, want 0", len(enq.msgs))
	}
}

// TestHandler_EnqueuesParsedMessages confirms every message a listener parses
// is enqueued, and the handler returns 200.
func TestHandler_EnqueuesParsedMessages(t *testing.T) {
	l := &fakeListener{parseMsgs: []canonical.Message{msg("a"), msg("b")}}
	enq := &fakeEnqueuer{}
	rr := httptest.NewRecorder()

	Handler(l, enq, discardLogger()).ServeHTTP(rr, post(`{"k":1}`))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if len(enq.msgs) != 2 || enq.msgs[0].Meta.PlatformMessageID != "a" || enq.msgs[1].Meta.PlatformMessageID != "b" {
		t.Errorf("enqueued = %+v, want messages a,b", enq.msgs)
	}
}

// TestHandler_DropReturns200 confirms a listener that parses nothing (fromMe,
// group, unregistered, non-message) results in 200 with nothing enqueued —
// platforms must not be told to retry.
func TestHandler_DropReturns200(t *testing.T) {
	l := &fakeListener{parseMsgs: nil}
	enq := &fakeEnqueuer{}
	rr := httptest.NewRecorder()

	Handler(l, enq, discardLogger()).ServeHTTP(rr, post(`{}`))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if len(enq.msgs) != 0 {
		t.Errorf("enqueued %d, want 0", len(enq.msgs))
	}
}

// TestHandler_ParseErrorReturns500Retry confirms a Parse error (a retryable
// failure, e.g. the account lookup is transiently down) returns 500 so the
// platform retries — not a silent drop. (Deliberate drops use an empty result;
// see TestHandler_DropReturns200.)
func TestHandler_ParseErrorReturns500Retry(t *testing.T) {
	l := &fakeListener{parseErr: errors.New("account lookup transient")}
	enq := &fakeEnqueuer{}
	rr := httptest.NewRecorder()

	Handler(l, enq, discardLogger()).ServeHTTP(rr, post(`{}`))

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (retry)", rr.Code)
	}
	if len(enq.msgs) != 0 {
		t.Errorf("enqueued %d on parse error, want 0", len(enq.msgs))
	}
}

// TestHandler_EnqueueFailureReturns500 is the durability guarantee: if the
// message could not be durably enqueued, return 5xx so the platform retries.
// We must never 200 a message we failed to buffer.
func TestHandler_EnqueueFailureReturns500(t *testing.T) {
	l := &fakeListener{parseMsgs: []canonical.Message{msg("a")}}
	enq := &fakeEnqueuer{err: errors.New("sqs down")}
	rr := httptest.NewRecorder()

	Handler(l, enq, discardLogger()).ServeHTTP(rr, post(`{}`))

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (enqueue failure must signal retry)", rr.Code)
	}
}

// TestHandler_PassesRawBodyToListener confirms the listener receives the exact
// request body bytes (needed for signature verification and verbatim capture).
func TestHandler_PassesRawBodyToListener(t *testing.T) {
	l := &fakeListener{}
	Handler(l, &fakeEnqueuer{}, discardLogger()).ServeHTTP(httptest.NewRecorder(), post(`{"raw":true}`))
	if string(l.gotBody) != `{"raw":true}` {
		t.Errorf("listener got body %q, want %q", l.gotBody, `{"raw":true}`)
	}
}

// TestHandler_EmitsContractLogEvents confirms the observability contract:
// a successful inbound emits webhook_received, enqueued, and webhook_accepted
// as structured JSON `event` fields (the metric filters key on these).
func TestHandler_EmitsContractLogEvents(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	l := &fakeListener{id: "whatsapp-ultramsg", parseMsgs: []canonical.Message{msg("a")}}

	Handler(l, &fakeEnqueuer{}, logger).ServeHTTP(httptest.NewRecorder(), post(`{}`))

	out := buf.String()
	for _, ev := range []string{"webhook_received", "enqueued", "webhook_accepted"} {
		if !strings.Contains(out, `"event":"`+ev+`"`) {
			t.Errorf("missing contract event %q in logs:\n%s", ev, out)
		}
	}
}

// TestHandler_VerifyFailEmitsInvalidSignature confirms a bearer mismatch logs
// event=invalid_signature — the exact value the CloudWatch alarm filters on.
func TestHandler_VerifyFailEmitsInvalidSignature(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	l := &fakeListener{id: "whatsapp-ultramsg", verifyErr: errors.New("bad token")}

	Handler(l, &fakeEnqueuer{}, logger).ServeHTTP(httptest.NewRecorder(), post(`{}`))

	if !strings.Contains(buf.String(), `"event":"invalid_signature"`) {
		t.Errorf("verify failure must log event=invalid_signature; got:\n%s", buf.String())
	}
}

// TestRegister_RoutesPathToHandler confirms Register mounts each listener at
// its Path() so a POST there flows through to enqueue.
func TestRegister_RoutesPathToHandler(t *testing.T) {
	l := &fakeListener{id: "fake", path: "/inbound/fake", parseMsgs: []canonical.Message{msg("z")}}
	enq := &fakeEnqueuer{}
	mux := http.NewServeMux()
	Register(mux, enq, discardLogger(), l)

	req := httptest.NewRequest(http.MethodPost, "/inbound/fake", strings.NewReader(`{}`))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if len(enq.msgs) != 1 || enq.msgs[0].Meta.PlatformMessageID != "z" {
		t.Errorf("enqueued = %+v, want [z]", enq.msgs)
	}
}
