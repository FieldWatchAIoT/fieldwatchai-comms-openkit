package contacts

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"
)

// RowError reports why a single CSV row was skipped.
type RowError struct {
	Row    int    `json:"row"`
	Reason string `json:"reason"`
}

// BulkResult summarizes a bulk import. One bad row never fails the batch.
type BulkResult struct {
	Created int        `json:"created"`
	Skipped int        `json:"skipped"`
	Errors  []RowError `json:"errors"`
}

// BulkImport creates contacts from a CSV with a header row. Recognized columns:
// short_id, display_name, role, aoi_id, status (case-insensitive; extra columns
// ignored). short_id and display_name are required. Each data row is imported
// independently; failures are collected, not fatal.
func (s *Service) BulkImport(ctx context.Context, tenantID uuid.UUID, r io.Reader) (BulkResult, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1 // tolerate ragged rows; we validate per-row
	cr.TrimLeadingSpace = true

	header, err := cr.Read()
	if err != nil {
		return BulkResult{}, fmt.Errorf("read header: %w", err)
	}
	col := map[string]int{}
	for i, h := range header {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	if _, ok := col["short_id"]; !ok {
		return BulkResult{}, fmt.Errorf("csv missing required column: short_id")
	}
	if _, ok := col["display_name"]; !ok {
		return BulkResult{}, fmt.Errorf("csv missing required column: display_name")
	}

	get := func(rec []string, name string) string {
		if i, ok := col[name]; ok && i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}
	optPtr := func(v string) *string {
		if v == "" {
			return nil
		}
		return &v
	}

	var res BulkResult
	rowNum := 0
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		rowNum++
		if err != nil {
			res.Errors = append(res.Errors, RowError{Row: rowNum, Reason: "malformed csv: " + err.Error()})
			continue
		}
		in := CreateInput{
			TenantID:    tenantID,
			ShortID:     get(rec, "short_id"),
			DisplayName: get(rec, "display_name"),
			Role:        optPtr(get(rec, "role")),
			AOIID:       optPtr(get(rec, "aoi_id")),
			Status:      get(rec, "status"),
		}
		if _, err := s.Create(ctx, in); err != nil {
			res.Errors = append(res.Errors, RowError{Row: rowNum, Reason: err.Error()})
			continue
		}
		res.Created++
	}
	res.Skipped = len(res.Errors)
	return res, nil
}
