package resolver

import (
	"context"
	"testing"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/queries/goqueries"
	"github.com/google/uuid"
)

type fakeStore struct {
	rows []goqueries.ListContactShortIDsRow
}

func (f *fakeStore) ListContactShortIDs(_ context.Context, _ uuid.UUID) ([]goqueries.ListContactShortIDsRow, error) {
	return f.rows, nil
}

func candidates() *fakeStore {
	return &fakeStore{rows: []goqueries.ListContactShortIDsRow{
		{ID: uuid.New(), ShortID: "42", DisplayName: "Marsh Harbour"},
		{ID: uuid.New(), ShortID: "43", DisplayName: "Hope Town"},
		{ID: uuid.New(), ShortID: "HMB", DisplayName: "Hope Mills Base"},
	}}
}

func TestResolveExact(t *testing.T) {
	r := New(candidates())
	m, err := r.Resolve(context.Background(), uuid.New(), "42")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if m.ShortIDMatch != "exact" {
		t.Errorf("match = %q, want exact", m.ShortIDMatch)
	}
	if m.ContactID == nil {
		t.Error("exact match should set ContactID")
	}
}

func TestResolveExactCaseInsensitive(t *testing.T) {
	r := New(candidates())
	m, _ := r.Resolve(context.Background(), uuid.New(), "hmb")
	if m.ShortIDMatch != "exact" || m.ContactID == nil {
		t.Errorf("case-insensitive exact failed: %+v", m)
	}
}

func TestResolveLevenshtein1(t *testing.T) {
	r := New(candidates())
	m, _ := r.Resolve(context.Background(), uuid.New(), "HMW") // 1 from HMB
	if m.ShortIDMatch != "levenshtein_1" {
		t.Fatalf("match = %q, want levenshtein_1", m.ShortIDMatch)
	}
	if m.ContactID == nil {
		t.Error("levenshtein_1 should pick a best ContactID")
	}
	found := false
	for _, a := range m.Alternatives {
		if a.ShortID == "HMB" && a.Distance == 1 {
			found = true
		}
	}
	if !found {
		t.Errorf("expected HMB@1 in alternatives: %+v", m.Alternatives)
	}
}

func TestResolveNone(t *testing.T) {
	r := New(candidates())
	m, _ := r.Resolve(context.Background(), uuid.New(), "ZZZZZ")
	if m.ShortIDMatch != "none" {
		t.Errorf("match = %q, want none", m.ShortIDMatch)
	}
	if m.ContactID != nil {
		t.Error("none should not set ContactID")
	}
}
