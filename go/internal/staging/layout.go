// Claim-check layout and reads. Key prefix: bulkwire/{fiId}/{batchId}/
//
//	manifest.json                 Manifest, written last (presence = staging completed)
//	errors.json                   []ValidationError (may be empty)
//	rows/000000.json, ...         pages of valid WireRows in canonical order (CLAIMCHECK_PAGE_SIZE each)
//	invalid/000000.json, ...      pages of InvalidRows (raw values) for the review screen
//
// WHY pages instead of one blob: a chunk Activity reads only the pages that overlap its [offset,
// offset+length) range, so object size stays bounded (~200 KB) on every backend and chunk size is
// independent of page size.
package staging

import (
	"context"
	"encoding/json"
	"fmt"

	"bulkwire/internal/claimcheck"
	"bulkwire/internal/model"
)

func prefix(fiID, batchID string) string { return fmt.Sprintf("bulkwire/%s/%s/", fiID, batchID) }

// ManifestKey is bulkwire/{fi}/{batch}/manifest.json.
func ManifestKey(fiID, batchID string) string { return prefix(fiID, batchID) + "manifest.json" }

// ErrorsKey is bulkwire/{fi}/{batch}/errors.json.
func ErrorsKey(fiID, batchID string) string { return prefix(fiID, batchID) + "errors.json" }

// PageKey is bulkwire/{fi}/{batch}/rows/NNNNNN.json.
func PageKey(fiID, batchID string, page int) string {
	return fmt.Sprintf("%srows/%06d.json", prefix(fiID, batchID), page)
}

// InvalidPageKey is bulkwire/{fi}/{batch}/invalid/NNNNNN.json.
func InvalidPageKey(fiID, batchID string, page int) string {
	return fmt.Sprintf("%sinvalid/%06d.json", prefix(fiID, batchID), page)
}

// ReadManifest loads a batch's manifest. claimcheck.ErrNotFound (wrapped) means "never staged or expired".
func ReadManifest(ctx context.Context, s claimcheck.Store, fiID, batchID string) (model.Manifest, error) {
	var m model.Manifest
	return m, getJSON(ctx, s, ManifestKey(fiID, batchID), &m)
}

// ReadErrors loads a batch's validation errors.
func ReadErrors(ctx context.Context, s claimcheck.Store, m model.Manifest) ([]model.ValidationError, error) {
	var errs []model.ValidationError
	return errs, getJSON(ctx, s, ErrorsKey(m.FiID, m.BatchID), &errs)
}

// ReadRange returns canonical-order rows [offset, offset+length) by fetching only the pages that overlap.
func ReadRange(ctx context.Context, s claimcheck.Store, m model.Manifest, offset, length int) ([]model.WireRow, error) {
	return readPaged[model.WireRow](ctx, s, m.PageSize, m.ValidRowCount, offset, length,
		func(p int) string { return PageKey(m.FiID, m.BatchID, p) })
}

// ReadInvalidRange returns invalid rows [offset, offset+length) in row-number order.
func ReadInvalidRange(ctx context.Context, s claimcheck.Store, m model.Manifest, offset, length int) ([]model.InvalidRow, error) {
	return readPaged[model.InvalidRow](ctx, s, m.PageSize, m.InvalidRowCount(), offset, length,
		func(p int) string { return InvalidPageKey(m.FiID, m.BatchID, p) })
}

func readPaged[T any](ctx context.Context, s claimcheck.Store, pageSize, count, offset, length int, key func(int) string) ([]T, error) {
	if pageSize <= 0 || offset < 0 || length < 0 || offset+length > count {
		return nil, fmt.Errorf("staging: range [%d,%d) outside %d rows (page size %d)", offset, offset+length, count, pageSize)
	}
	if length == 0 {
		return []T{}, nil
	}
	first, last := offset/pageSize, (offset+length-1)/pageSize
	out := make([]T, 0, length)
	for p := first; p <= last; p++ {
		var page []T
		if err := getJSON(ctx, s, key(p), &page); err != nil {
			return nil, err
		}
		lo, hi := max(offset-p*pageSize, 0), min(offset+length-p*pageSize, len(page))
		if lo > hi {
			return nil, fmt.Errorf("staging: page %d is shorter than the manifest says", p)
		}
		out = append(out, page[lo:hi]...)
	}
	return out, nil
}

func getJSON(ctx context.Context, s claimcheck.Store, key string, v any) error {
	data, err := s.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("staging: read %s: %w", key, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("staging: decode %s: %w", key, err)
	}
	return nil
}
