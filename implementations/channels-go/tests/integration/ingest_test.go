//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/canonical"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/crypto"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/ingest"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/integrations/ultramsg"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/outbound"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/parser"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/policy"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/queries/goqueries"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/resolver"
	"github.com/google/uuid"
)

func sp(s string) *string { return &s }

const aesKeyB64 = "MDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDA=" // 32 zero bytes

func TestIngestPersistsParsesAndDedups(t *testing.T) {
	ctx := context.Background()
	pool, tenant := freshSchemaWithTenant(t)

	acctID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO accounts (id, tenant_id, type, owner_type, label, platform_identifier, capabilities, status, created_at)
		VALUES ($1,$2,'whatsapp','platform','WA','179557',ARRAY['inbound','outbound'],'active',$3)`,
		acctID, tenant, time.Now().UTC()); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO contacts (id, tenant_id, short_id, display_name, status, metadata, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, '42', 'Marsh Harbour', 'active', '{}', $2, $2)`,
		tenant, time.Now().UTC()); err != nil {
		t.Fatalf("seed contact: %v", err)
	}

	q := goqueries.New(pool)
	enc, _ := crypto.NewLocalAES(aesKeyB64)
	svc := ingest.NewService(ingest.Deps{
		Store: q, Resolver: resolver.New(q), Encryptor: enc, Dispatcher: outbound.NewRegistry(),
		ParserCfg:  parser.Config{Commands: []string{"STATUS", "NEEDS", "SOS"}},
		Thresholds: policy.DefaultThresholds,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:        time.Now, NewID: uuid.New,
	})

	msg := canonical.Message{
		Sender: canonical.Sender{Endpoint: "+12425550042", Platform: "whatsapp"},
		Body:   canonical.Body{Text: sp("42 STATUS full")},
		Meta: canonical.Meta{
			PlatformMessageID: "false_dedup_1", ReceivedAt: "2026-06-06T04:00:00Z",
			AccountID: acctID.String(), RawPayload: json.RawMessage(`{"foo":"bar"}`),
		},
	}

	r1, err := svc.Ingest(ctx, msg)
	if err != nil {
		t.Fatalf("Ingest #1: %v", err)
	}
	if r1.IsReplay {
		t.Fatal("first ingest marked as replay")
	}
	// Exact match + known command -> confidence 1.0 -> execute.
	if r1.Action != policy.ActionExecute {
		t.Errorf("action = %q, want execute", r1.Action)
	}

	r2, err := svc.Ingest(ctx, msg)
	if err != nil {
		t.Fatalf("Ingest #2: %v", err)
	}
	if !r2.IsReplay || r2.MessageID != r1.MessageID {
		t.Errorf("replay mismatch: %+v vs %+v", r2, r1)
	}

	var count int
	pool.QueryRow(ctx, `SELECT count(*) FROM messages WHERE account_id=$1 AND direction='inbound'`, acctID).Scan(&count)
	if count != 1 {
		t.Fatalf("inbound message count = %d, want 1", count)
	}

	var parsed *string
	var policyAct *string
	if err := pool.QueryRow(ctx, `SELECT parsed::text, policy_action FROM messages WHERE id=$1`, r1.MessageID).Scan(&parsed, &policyAct); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if parsed == nil {
		t.Fatal("parsed not populated")
	}
	var doc struct {
		ShortID      string `json:"short_id"`
		Command      string `json:"command"`
		ShortIDMatch string `json:"short_id_match"`
		Confidence   float64 `json:"confidence"`
	}
	json.Unmarshal([]byte(*parsed), &doc)
	if doc.ShortID != "42" || doc.Command != "STATUS" || doc.ShortIDMatch != "exact" || doc.Confidence != 1.0 {
		t.Errorf("parsed wrong: %+v", doc)
	}
	if policyAct == nil || *policyAct != "execute" {
		t.Errorf("policy_action = %v, want execute", policyAct)
	}
}

func TestIngestEchoBackSendsViaUltraMSG(t *testing.T) {
	ctx := context.Background()
	pool, tenant := freshSchemaWithTenant(t)

	// Fake UltraMSG endpoint captures the outbound echo.
	var gotPath, gotTo, gotBody, gotToken string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = r.ParseForm()
		gotTo, gotBody, gotToken = r.PostFormValue("to"), r.PostFormValue("body"), r.PostFormValue("token")
		_, _ = w.Write([]byte(`{"sent":"true","id":"echo-999"}`))
	}))
	defer ts.Close()

	enc, _ := crypto.NewLocalAES(aesKeyB64)
	tok, _ := enc.Encrypt(ctx, []byte("ultramsg-token"))

	acctID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO accounts (id, tenant_id, type, owner_type, label, platform_identifier, credentials_encrypted, capabilities, status, created_at)
		VALUES ($1,$2,'whatsapp','platform','WA','179557',$3,ARRAY['inbound','outbound'],'active',$4)`,
		acctID, tenant, tok, time.Now().UTC()); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO contacts (id, tenant_id, short_id, display_name, status, metadata, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, '42', 'Marsh Harbour', 'active', '{}', $2, $2)`,
		tenant, time.Now().UTC()); err != nil {
		t.Fatalf("seed contact: %v", err)
	}

	q := goqueries.New(pool)
	reg := outbound.NewRegistry()
	reg.Register("whatsapp", &ultramsg.Sender{Client: ts.Client(), BaseURL: ts.URL})
	svc := ingest.NewService(ingest.Deps{
		Store: q, Resolver: resolver.New(q), Encryptor: enc, Dispatcher: reg,
		ParserCfg:  parser.Config{Commands: []string{"STATUS", "DAMAGE", "SOS"}},
		Thresholds: policy.DefaultThresholds,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:        time.Now, NewID: uuid.New,
	})

	// DAMAGE always echoes back.
	msg := canonical.Message{
		Sender: canonical.Sender{Endpoint: "+12425550042", Platform: "whatsapp"},
		Body:   canonical.Body{Text: sp("42 DAMAGE roof collapsed in dorm B")},
		Meta: canonical.Meta{
			PlatformMessageID: "echo_msg_1", ReceivedAt: "2026-06-06T04:00:00Z",
			AccountID: acctID.String(), RawPayload: json.RawMessage(`{}`),
		},
	}
	r, err := svc.Ingest(ctx, msg)
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	svc.Wait() // echo dispatch is async; wait for the background send + persist
	if r.Action != policy.ActionEchoBack {
		t.Fatalf("action = %q, want echo_back", r.Action)
	}

	// The fake UltraMSG received the echo, addressed to the sender, decrypted token.
	if gotPath != "/instance179557/messages/chat" {
		t.Errorf("path = %q, want /instance179557/messages/chat", gotPath)
	}
	if gotTo != "+12425550042" {
		t.Errorf("echo to %q, want sender", gotTo)
	}
	if gotToken != "ultramsg-token" {
		t.Errorf("token = %q, want decrypted ultramsg-token", gotToken)
	}
	for _, want := range []string{"roof collapsed", "OOPS"} {
		if !containsStr(gotBody, want) {
			t.Errorf("echo body missing %q; got %q", want, gotBody)
		}
	}

	// An outbound message row was persisted, linked to the inbound.
	var n int
	var inReplyTo *uuid.UUID
	pool.QueryRow(ctx, `SELECT count(*) FROM messages WHERE direction='outbound'`).Scan(&n)
	if n != 1 {
		t.Errorf("outbound rows = %d, want 1", n)
	}
	pool.QueryRow(ctx, `SELECT in_reply_to_message_id FROM messages WHERE direction='outbound' LIMIT 1`).Scan(&inReplyTo)
	if inReplyTo == nil || *inReplyTo != r.MessageID {
		t.Errorf("outbound not linked to inbound %v: %v", r.MessageID, inReplyTo)
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
