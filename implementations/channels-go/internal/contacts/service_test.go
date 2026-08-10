package contacts

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/queries/goqueries"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type fakeStore struct {
	created    goqueries.CreateContactParams
	byShortOut goqueries.Contact
	byShortErr error
	existing   map[string]bool // when non-nil, GetContactByShortID hits on these short_ids
	getOut     goqueries.Contact
	getErr     error
	shortIDs   []goqueries.ListContactShortIDsRow
	delRows    int64
}

func (f *fakeStore) CreateContact(_ context.Context, p goqueries.CreateContactParams) (goqueries.Contact, error) {
	f.created = p
	return goqueries.Contact{ID: p.ID, TenantID: p.TenantID, ShortID: p.ShortID, DisplayName: p.DisplayName, AoiID: p.AoiID, Role: p.Role, Status: p.Status, Metadata: p.Metadata, CreatedAt: p.CreatedAt, UpdatedAt: p.CreatedAt}, nil
}
func (f *fakeStore) GetContactForTenant(_ context.Context, _ goqueries.GetContactForTenantParams) (goqueries.Contact, error) {
	return f.getOut, f.getErr
}
func (f *fakeStore) GetContactByShortID(_ context.Context, p goqueries.GetContactByShortIDParams) (goqueries.Contact, error) {
	if f.existing != nil {
		if f.existing[p.ShortID] {
			return goqueries.Contact{ShortID: p.ShortID}, nil
		}
		return goqueries.Contact{}, pgx.ErrNoRows
	}
	return f.byShortOut, f.byShortErr
}
func (f *fakeStore) ListContactsForTenant(_ context.Context, _ uuid.UUID) ([]goqueries.Contact, error) {
	return []goqueries.Contact{f.getOut}, nil
}
func (f *fakeStore) ListContactShortIDs(_ context.Context, _ uuid.UUID) ([]goqueries.ListContactShortIDsRow, error) {
	return f.shortIDs, nil
}
func (f *fakeStore) UpdateContact(_ context.Context, p goqueries.UpdateContactParams) (goqueries.Contact, error) {
	return goqueries.Contact{ID: p.ID, TenantID: p.TenantID, DisplayName: p.DisplayName, Status: p.Status}, nil
}
func (f *fakeStore) DeleteContact(_ context.Context, _ goqueries.DeleteContactParams) (int64, error) {
	return f.delRows, nil
}

func svc(t *testing.T, s store) *Service {
	t.Helper()
	return NewService(s,
		func() time.Time { return time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC) },
		func() uuid.UUID { return uuid.MustParse("11111111-1111-1111-1111-111111111111") })
}

func validCreate() CreateInput {
	return CreateInput{TenantID: uuid.New(), ShortID: "42", DisplayName: "Marsh Harbour Shelter"}
}

func TestCreateValidatesShortID(t *testing.T) {
	s := svc(t, &fakeStore{byShortErr: pgx.ErrNoRows})
	in := validCreate()
	in.ShortID = ""
	if _, err := s.Create(context.Background(), in); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Create empty short_id = %v, want ErrInvalid", err)
	}
}

func TestCreateRejectsCollision(t *testing.T) {
	// GetContactByShortID finds an existing row -> conflict.
	s := svc(t, &fakeStore{byShortOut: goqueries.Contact{ShortID: "42"}, byShortErr: nil})
	if _, err := s.Create(context.Background(), validCreate()); !errors.Is(err, ErrConflict) {
		t.Fatalf("Create collision = %v, want ErrConflict", err)
	}
}

func TestCreateSuccessMapsFields(t *testing.T) {
	fake := &fakeStore{byShortErr: pgx.ErrNoRows}
	s := svc(t, fake)
	in := validCreate()
	aoi, role := "abaco", "shelter"
	in.AOIID, in.Role = &aoi, &role

	c, err := s.Create(context.Background(), in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if c.ShortID != "42" || c.DisplayName != "Marsh Harbour Shelter" {
		t.Errorf("bad mapping: %+v", c)
	}
	if c.AOIID == nil || *c.AOIID != "abaco" || c.Role == nil || *c.Role != "shelter" {
		t.Errorf("aoi/role not mapped: %+v", c)
	}
	if !fake.created.AoiID.Valid || fake.created.AoiID.String != "abaco" {
		t.Errorf("aoi not passed to store as pgtype.Text: %+v", fake.created.AoiID)
	}
}

func TestGetNotFound(t *testing.T) {
	s := svc(t, &fakeStore{getErr: pgx.ErrNoRows})
	if _, err := s.Get(context.Background(), uuid.New(), uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get = %v, want ErrNotFound", err)
	}
}

func TestDeleteNotFound(t *testing.T) {
	s := svc(t, &fakeStore{delRows: 0})
	if err := s.Delete(context.Background(), uuid.New(), uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete = %v, want ErrNotFound", err)
	}
}

func TestShortIDCheck(t *testing.T) {
	fake := &fakeStore{shortIDs: []goqueries.ListContactShortIDsRow{
		{ShortID: "42", DisplayName: "A"},
		{ShortID: "43", DisplayName: "B"},
		{ShortID: "HMB", DisplayName: "C"},
	}}
	s := svc(t, fake)

	// Exact existing short_id collides.
	res, err := s.ShortIDCheck(context.Background(), uuid.New(), "42")
	if err != nil {
		t.Fatalf("ShortIDCheck: %v", err)
	}
	if !res.Exists {
		t.Error("expected Exists=true for 42")
	}

	// A near-miss should be suggested (edit distance 1 from 43).
	res2, _ := s.ShortIDCheck(context.Background(), uuid.New(), "4")
	if res2.Exists {
		t.Error("4 should not exist")
	}
	found := false
	for _, sug := range res2.Suggestions {
		if sug == "42" || sug == "43" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected near-miss suggestions for 4, got %v", res2.Suggestions)
	}
}
