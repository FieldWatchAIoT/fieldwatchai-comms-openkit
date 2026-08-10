// Package resolver maps a parsed short_id to a contact using rule-based
// matching only (exact, then Levenshtein, with Soundex typo suggestions). No
// AI, ever — per the comms-hub design.
package resolver

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/queries/goqueries"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/strdist"
	"github.com/google/uuid"
)

// Match types for ParseResult.short_id_match.
const (
	MatchExact = "exact"
	MatchLev1  = "levenshtein_1"
	MatchLev2  = "levenshtein_2"
	MatchNone  = "none"
)

// store is the slice of generated queries the resolver needs.
type store interface {
	ListContactShortIDs(ctx context.Context, tenantID uuid.UUID) ([]goqueries.ListContactShortIDsRow, error)
}

// Alt is a candidate near-match.
type Alt struct {
	ShortID   string    `json:"short_id"`
	ContactID uuid.UUID `json:"contact_id"`
	Distance  int       `json:"distance"`
}

// Match is the resolution outcome for a short_id.
type Match struct {
	ShortIDMatch string     `json:"short_id_match"` // exact | levenshtein_1 | levenshtein_2 | none
	ContactID    *uuid.UUID `json:"matched_contact_id"`
	Alternatives []Alt      `json:"alternatives"`
}

// Resolver performs address-book resolution.
type Resolver struct {
	store store
}

// New constructs a Resolver.
func New(s store) *Resolver { return &Resolver{store: s} }

// Resolve finds the best contact for shortID within the tenant. Matching is
// case-insensitive. Exact wins; otherwise the closest Levenshtein candidate
// (distance 1 or 2) is the match, with all candidates within distance 2 (plus
// Soundex-equal typos) returned as ranked alternatives.
func (r *Resolver) Resolve(ctx context.Context, tenantID uuid.UUID, shortID string) (Match, error) {
	rows, err := r.store.ListContactShortIDs(ctx, tenantID)
	if err != nil {
		return Match{}, fmt.Errorf("list short_ids: %w", err)
	}

	needle := strings.ToLower(shortID)
	needleSoundex := strdist.Soundex(shortID)

	var alts []Alt
	bestDist := 1 << 30
	var bestID *uuid.UUID

	for _, row := range rows {
		cand := strings.ToLower(row.ShortID)
		id := row.ID
		if cand == needle {
			return Match{ShortIDMatch: MatchExact, ContactID: &id}, nil
		}
		d := strdist.Levenshtein(needle, cand)
		soundexEqual := needleSoundex != "" && strdist.Soundex(row.ShortID) == needleSoundex
		if d <= 2 || soundexEqual {
			alts = append(alts, Alt{ShortID: row.ShortID, ContactID: id, Distance: d})
		}
		if d < bestDist {
			bestDist = d
			cp := id
			bestID = &cp
		}
	}

	sort.SliceStable(alts, func(i, j int) bool { return alts[i].Distance < alts[j].Distance })

	m := Match{Alternatives: alts}
	switch {
	case bestDist == 1:
		m.ShortIDMatch = MatchLev1
		m.ContactID = bestID
	case bestDist == 2:
		m.ShortIDMatch = MatchLev2
		m.ContactID = bestID
	default:
		m.ShortIDMatch = MatchNone
	}
	return m, nil
}
