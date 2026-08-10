package contacts

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestBulkImport(t *testing.T) {
	// "dup" already exists; everything else is new.
	fake := &fakeStore{existing: map[string]bool{"dup": true}}
	s := svc(t, fake)

	csv := strings.Join([]string{
		"short_id,display_name,role",
		"42,Marsh Harbour,shelter",
		"HMB,Hope Town,shelter",
		",No ShortID,staff", // missing short_id -> error
		"dup,Already Exists,eoc", // collision -> error
		"EOC,Ops Center,eoc",
	}, "\n")

	res, err := s.BulkImport(context.Background(), uuid.New(), strings.NewReader(csv))
	if err != nil {
		t.Fatalf("BulkImport: %v", err)
	}
	if res.Created != 3 {
		t.Errorf("Created = %d, want 3", res.Created)
	}
	if len(res.Errors) != 2 {
		t.Fatalf("Errors = %d (%+v), want 2", len(res.Errors), res.Errors)
	}
	// Error rows are the empty-short_id row (data row 3) and the dup (row 4).
	rows := map[int]bool{}
	for _, e := range res.Errors {
		rows[e.Row] = true
	}
	if !rows[3] || !rows[4] {
		t.Errorf("expected errors on rows 3 and 4, got %+v", res.Errors)
	}
}

func TestBulkImportRejectsMissingHeader(t *testing.T) {
	s := svc(t, &fakeStore{existing: map[string]bool{}})
	// No recognizable header columns.
	_, err := s.BulkImport(context.Background(), uuid.New(), strings.NewReader("foo,bar\n1,2"))
	if err == nil {
		t.Fatal("BulkImport with no short_id/display_name header = nil error, want error")
	}
}
