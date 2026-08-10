package accounts

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/crypto"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/queries/goqueries"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// fakeStore captures params and returns canned values/errors.
type fakeStore struct {
	createParams goqueries.CreateAccountParams
	updateParams goqueries.UpdateAccountParams
	getArg       goqueries.GetAccountForTenantParams
	delArg       goqueries.DeleteAccountParams

	createOut goqueries.Account
	getOut    goqueries.Account
	getErr    error
	updateOut goqueries.Account
	delRows   int64

	lookupArg goqueries.LookupAccountParams
	lookupOut goqueries.LookupAccountRow
	lookupErr error

	routingOut []goqueries.ListAccountsWithRoutingForTenantRow
}

func (f *fakeStore) LookupAccount(_ context.Context, p goqueries.LookupAccountParams) (goqueries.LookupAccountRow, error) {
	f.lookupArg = p
	return f.lookupOut, f.lookupErr
}

func (f *fakeStore) CreateAccount(_ context.Context, p goqueries.CreateAccountParams) (goqueries.Account, error) {
	f.createParams = p
	// Echo the params back as the stored row (id/created_at set by service).
	f.createOut = goqueries.Account{
		ID: p.ID, TenantID: p.TenantID, Type: p.Type, OwnerType: p.OwnerType,
		Label: p.Label, PlatformIdentifier: p.PlatformIdentifier,
		CredentialsEncrypted: p.CredentialsEncrypted, Capabilities: p.Capabilities,
		Status: p.Status, CreatedAt: p.CreatedAt,
	}
	return f.createOut, nil
}
func (f *fakeStore) GetAccountForTenant(_ context.Context, p goqueries.GetAccountForTenantParams) (goqueries.Account, error) {
	f.getArg = p
	return f.getOut, f.getErr
}
func (f *fakeStore) ListAccountsForTenant(_ context.Context, _ uuid.UUID) ([]goqueries.Account, error) {
	return []goqueries.Account{f.getOut}, nil
}
func (f *fakeStore) ListAccountsWithRoutingForTenant(_ context.Context, _ uuid.UUID) ([]goqueries.ListAccountsWithRoutingForTenantRow, error) {
	return f.routingOut, nil
}
func (f *fakeStore) UpdateAccount(_ context.Context, p goqueries.UpdateAccountParams) (goqueries.Account, error) {
	f.updateParams = p
	f.updateOut = goqueries.Account{
		ID: p.ID, TenantID: p.TenantID, Label: p.Label, Status: p.Status,
		Capabilities: p.Capabilities, CredentialsEncrypted: p.CredentialsEncrypted,
	}
	return f.updateOut, nil
}
func (f *fakeStore) DeleteAccount(_ context.Context, p goqueries.DeleteAccountParams) (int64, error) {
	f.delArg = p
	return f.delRows, nil
}

func testService(t *testing.T, store store) (*Service, crypto.Encryptor) {
	t.Helper()
	enc, err := crypto.NewLocalAES("MDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDA=") // 32 zero bytes
	if err != nil {
		t.Fatalf("enc: %v", err)
	}
	fixedNow := time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC)
	fixedID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	return NewService(store, enc, func() time.Time { return fixedNow }, func() uuid.UUID { return fixedID }), enc
}

func validCreate() CreateInput {
	return CreateInput{
		TenantID:           uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		Type:               "whatsapp",
		OwnerType:          "platform",
		Label:              "WhatsApp #1",
		PlatformIdentifier: "179557",
		Status:             "active",
		Capabilities:       []string{"inbound", "outbound"},
		Credentials:        []byte("ultramsg-token"),
	}
}

func TestCreateEncryptsCredentialsAndNeverReturnsThem(t *testing.T) {
	fake := &fakeStore{}
	svc, enc := testService(t, fake)

	acc, err := svc.Create(context.Background(), validCreate())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Stored credentials must be ciphertext, decryptable back to the plaintext.
	stored := fake.createParams.CredentialsEncrypted
	if len(stored) == 0 || string(stored) == "ultramsg-token" {
		t.Fatalf("credentials not encrypted at rest: %q", stored)
	}
	got, err := enc.Decrypt(context.Background(), stored)
	if err != nil || string(got) != "ultramsg-token" {
		t.Fatalf("stored creds don't decrypt: %q err=%v", got, err)
	}

	if acc.ID != uuid.MustParse("11111111-1111-1111-1111-111111111111") {
		t.Errorf("ID = %v, want fixed id", acc.ID)
	}
	if acc.CreatedAt.IsZero() {
		t.Error("CreatedAt not set")
	}
	if acc.Label != "WhatsApp #1" || acc.Type != "whatsapp" {
		t.Errorf("fields not mapped: %+v", acc)
	}
}

func TestCreateValidatesRequiredFields(t *testing.T) {
	svc, _ := testService(t, &fakeStore{})
	in := validCreate()
	in.Type = ""
	if _, err := svc.Create(context.Background(), in); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Create with empty Type = %v, want ErrInvalid", err)
	}
}

func TestGetScopesToTenantAndMapsFields(t *testing.T) {
	tenant := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	id := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	fake := &fakeStore{getOut: goqueries.Account{
		ID: id, TenantID: tenant, Type: "sms", Label: "L",
		CredentialsEncrypted: []byte("ciphertext"), Status: "active",
	}}
	svc, _ := testService(t, fake)

	acc, err := svc.Get(context.Background(), id, tenant)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if fake.getArg.ID != id || fake.getArg.TenantID != tenant {
		t.Errorf("Get not tenant-scoped: %+v", fake.getArg)
	}
	if acc.Type != "sms" {
		t.Errorf("fields not mapped: %+v", acc)
	}
}

func TestGetNotFound(t *testing.T) {
	fake := &fakeStore{getErr: pgx.ErrNoRows}
	svc, _ := testService(t, fake)
	_, err := svc.Get(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get = %v, want ErrNotFound", err)
	}
}

func TestUpdateMergesAndReEncrypts(t *testing.T) {
	tenant := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	id := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	fake := &fakeStore{getOut: goqueries.Account{
		ID: id, TenantID: tenant, Label: "old", Status: "active",
		Capabilities: []string{"inbound"}, CredentialsEncrypted: []byte("old-ct"),
	}}
	svc, enc := testService(t, fake)

	newLabel := "new"
	_, err := svc.Update(context.Background(), id, tenant, UpdateInput{
		Label:       &newLabel,
		Credentials: []byte("new-token"),
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if fake.updateParams.Label != "new" {
		t.Errorf("Label not updated: %q", fake.updateParams.Label)
	}
	if fake.updateParams.Status != "active" {
		t.Errorf("unchanged Status not preserved: %q", fake.updateParams.Status)
	}
	got, err := enc.Decrypt(context.Background(), fake.updateParams.CredentialsEncrypted)
	if err != nil || string(got) != "new-token" {
		t.Fatalf("creds not re-encrypted: %q err=%v", got, err)
	}
}

func TestUpdateKeepsCredentialsWhenNotProvided(t *testing.T) {
	tenant := uuid.New()
	id := uuid.New()
	fake := &fakeStore{getOut: goqueries.Account{
		ID: id, TenantID: tenant, Label: "old", Status: "active",
		CredentialsEncrypted: []byte("keep-me"),
	}}
	svc, _ := testService(t, fake)

	if _, err := svc.Update(context.Background(), id, tenant, UpdateInput{}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if string(fake.updateParams.CredentialsEncrypted) != "keep-me" {
		t.Errorf("existing creds not preserved: %q", fake.updateParams.CredentialsEncrypted)
	}
}

func TestDeleteNotFound(t *testing.T) {
	fake := &fakeStore{delRows: 0}
	svc, _ := testService(t, fake)
	if err := svc.Delete(context.Background(), uuid.New(), uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete = %v, want ErrNotFound", err)
	}
}

// The list view must distinguish "routed to channel X" from "reaches nobody".
// The unlinked case is the one that matters: it looks identical to a healthy
// account everywhere else in the API.
func TestListWithRoutingMarksUnlinkedAccounts(t *testing.T) {
	linked, unlinked, chID := uuid.New(), uuid.New(), uuid.New()
	fs := &fakeStore{routingOut: []goqueries.ListAccountsWithRoutingForTenantRow{
		{ID: linked, Type: "telegram", Label: "bot", InboundChannelID: chID.String()},
		{ID: unlinked, Type: "sms-twilio", Label: "new number", InboundChannelID: ""},
	}}

	svc, _ := testService(t, fs)
	out, err := svc.ListWithRouting(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("ListWithRouting: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2", len(out))
	}
	if out[0].InboundChannelID == nil || *out[0].InboundChannelID != chID {
		t.Errorf("linked account lost its channel: %+v", out[0])
	}
	if out[1].InboundChannelID != nil {
		t.Errorf("unlinked account must report nil channel, got %v", *out[1].InboundChannelID)
	}
	if out[1].Label != "new number" {
		t.Errorf("account fields not mapped: %+v", out[1])
	}
}
