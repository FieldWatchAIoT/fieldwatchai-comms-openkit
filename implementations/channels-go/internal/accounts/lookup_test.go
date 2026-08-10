package accounts

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/queries/goqueries"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestServiceLookupReturnsIDs(t *testing.T) {
	acctID := uuid.New()
	tenant := uuid.New()
	fake := &fakeStore{lookupOut: goqueries.LookupAccountRow{ID: acctID, TenantID: tenant}}
	svc, _ := testService(t, fake)

	res, err := svc.Lookup(context.Background(), "whatsapp", "179557")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if res.AccountID != acctID || res.TenantID != tenant {
		t.Errorf("Lookup = %+v, want ids %v/%v", res, acctID, tenant)
	}
	if fake.lookupArg.Type != "whatsapp" || fake.lookupArg.PlatformIdentifier != "179557" {
		t.Errorf("lookup args = %+v", fake.lookupArg)
	}
}

func TestServiceLookupTranslatesListenerID(t *testing.T) {
	fake := &fakeStore{lookupOut: goqueries.LookupAccountRow{ID: uuid.New(), TenantID: uuid.New()}}
	svc, _ := testService(t, fake)

	// FCW sends the listener id; channels must query accounts.type="whatsapp".
	if _, err := svc.Lookup(context.Background(), "whatsapp-ultramsg", "instance118358"); err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if fake.lookupArg.Type != "whatsapp" {
		t.Errorf("store queried type=%q, want whatsapp (translated from whatsapp-ultramsg)", fake.lookupArg.Type)
	}
	if fake.lookupArg.PlatformIdentifier != "instance118358" {
		t.Errorf("identifier=%q", fake.lookupArg.PlatformIdentifier)
	}
}

func TestServiceLookupNotFound(t *testing.T) {
	fake := &fakeStore{lookupErr: pgx.ErrNoRows}
	svc, _ := testService(t, fake)
	if _, err := svc.Lookup(context.Background(), "sms", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Lookup = %v, want ErrNotFound", err)
	}
}

func TestHandlerLookup200(t *testing.T) {
	acctID := uuid.New()
	tenant := uuid.New()
	fake := &fakeSvc{lookupOut: LookupResult{AccountID: acctID, TenantID: tenant}}
	h := testHandler(fake)

	rec := do(h, http.MethodGet, "/v1/accounts/lookup?type=whatsapp&identifier=179557", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, acctID.String()) || !strings.Contains(body, tenant.String()) {
		t.Errorf("body missing ids: %s", body)
	}
	if strings.Contains(body, "credentials") {
		t.Errorf("lookup leaked credentials: %s", body)
	}
	if fake.lookupType != "whatsapp" || fake.lookupIdent != "179557" {
		t.Errorf("lookup args = %q/%q", fake.lookupType, fake.lookupIdent)
	}
}

func TestHandlerLookupMissingParams400(t *testing.T) {
	h := testHandler(&fakeSvc{})
	rec := do(h, http.MethodGet, "/v1/accounts/lookup?type=whatsapp", "", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandlerLookupNotFound404(t *testing.T) {
	h := testHandler(&fakeSvc{lookupErr: ErrNotFound})
	rec := do(h, http.MethodGet, "/v1/accounts/lookup?type=sms&identifier=zzz", "", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
