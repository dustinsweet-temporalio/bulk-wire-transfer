// Stage: parse + validate + write the staged batch to the claim-check store.
package staging

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"bulkwire/internal/claimcheck"
	"bulkwire/internal/model"
)

// Header is the exact CSV header (compared case-insensitively, whitespace-trimmed).
var Header = []string{
	"originating_account", "process_date", "amount", "currency", "beneficiary_name",
	"beneficiary_account", "beneficiary_routing", "beneficiary_bank", "reference", "subsidiary",
}

// AccountStatus is Core's answer for one originating account.
type AccountStatus struct {
	Exists          bool
	OwnedByCustomer bool
	Status          string // Active | OnHold | Closed (empty when not owned)
}

// AccountChecker validates originating accounts with the FI's Core in ONE call (a TVP on the Core side).
type AccountChecker interface {
	ValidateAccounts(ctx context.Context, fiID, customerID string, accounts []string) (map[string]AccountStatus, error)
}

// Input is one upload.
type Input struct {
	BatchID    string
	FiID       string
	CustomerID string
	UploadedBy string
	FileName   string
	Data       []byte
	UploadedAt time.Time
	MaxRows    int
	PageSize   int
}

// FileError reports file-level problems (code FILE): the file is not staged at all.
type FileError struct{ Errors []model.ValidationError }

func (e *FileError) Error() string { return fmt.Sprintf("file rejected: %s", e.Errors[0].Message) }

// ErrAccountsUnavailable means Core could not be reached to validate originating accounts. Staging is
// synchronous, so the user is told to try again (HTTP 503); nothing is written.
var ErrAccountsUnavailable = errors.New("staging: bank system unavailable for account validation")

// Stage validates the file and writes the staged batch. Order matters: row pages, invalid-row pages, and
// errors.json first, manifest.json LAST, so a manifest's presence means staging completed.
func Stage(ctx context.Context, store claimcheck.Store, accounts AccountChecker, in Input) (model.Manifest, error) {
	today := in.UploadedAt.UTC().Truncate(24 * time.Hour)
	parsed, ferr := parse(in.Data, in.BatchID, today, in.MaxRows)
	if ferr != nil {
		return model.Manifest{}, ferr
	}

	if err := checkOriginatingAccounts(ctx, accounts, in, parsed); err != nil {
		return model.Manifest{}, err
	}

	valid, groups, total := canonicalize(parsed.valid)
	sort.Slice(parsed.invalid, func(i, j int) bool { return parsed.invalid[i].RowNumber < parsed.invalid[j].RowNumber })
	sort.SliceStable(parsed.errors, func(i, j int) bool { return parsed.errors[i].RowNumber < parsed.errors[j].RowNumber })

	sum := sha256.Sum256(in.Data)
	m := model.Manifest{
		BatchID: in.BatchID, FiID: in.FiID, CustomerID: in.CustomerID, UploadedBy: in.UploadedBy,
		FileName: in.FileName, FileSHA256: hex.EncodeToString(sum[:]), UploadedAt: in.UploadedAt.UTC(),
		Status:   model.StagingValid,
		RowCount: parsed.rowCount, ValidRowCount: len(valid), ErrorCount: len(parsed.errors),
		TotalAmountCents: total, PageSize: in.PageSize, Groups: groups,
	}
	if len(parsed.errors) > 0 {
		m.Status = model.StagingInvalid
	}

	if err := writePages(ctx, store, in.PageSize, len(valid), func(i int) string { return PageKey(in.FiID, in.BatchID, i) },
		func(lo, hi int) any { return valid[lo:hi] }); err != nil {
		return model.Manifest{}, err
	}
	if err := writePages(ctx, store, in.PageSize, len(parsed.invalid), func(i int) string { return InvalidPageKey(in.FiID, in.BatchID, i) },
		func(lo, hi int) any { return parsed.invalid[lo:hi] }); err != nil {
		return model.Manifest{}, err
	}
	if err := putJSON(ctx, store, ErrorsKey(in.FiID, in.BatchID), nonNil(parsed.errors)); err != nil {
		return model.Manifest{}, err
	}
	if err := putJSON(ctx, store, ManifestKey(in.FiID, in.BatchID), m); err != nil {
		return model.Manifest{}, err
	}
	return m, nil
}

type parsedFile struct {
	rowCount int
	valid    []model.WireRow
	invalid  []model.InvalidRow
	errors   []model.ValidationError
}

func parse(data []byte, batchID string, today time.Time, maxRows int) (*parsedFile, *FileError) {
	fileErr := func(format string, a ...any) *FileError {
		return &FileError{Errors: []model.ValidationError{{Code: CodeFile, Message: fmt.Sprintf(format, a...)}}}
	}
	if !utf8.Valid(data) {
		return nil, fileErr("the file is not UTF-8 text")
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")) // spreadsheet tools often prepend a UTF-8 BOM

	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = -1
	header, err := r.Read()
	if err == io.EOF {
		return nil, fileErr("the file is empty")
	}
	if err != nil {
		return nil, fileErr("the file is not valid CSV: %v", err)
	}
	if !headerMatches(header) {
		return nil, fileErr("the header row must be exactly: %s", strings.Join(Header, ","))
	}

	p := &parsedFile{}
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fileErr("the file is not valid CSV: %v", err)
		}
		p.rowCount++
		if p.rowCount > maxRows {
			return nil, fileErr("the file has more than %d data rows", maxRows)
		}
		if len(rec) != len(Header) {
			return nil, fileErr("data row %d has %d columns; every row needs %d", p.rowCount, len(rec), len(Header))
		}
		for i := range rec {
			rec[i] = strings.TrimSpace(rec[i])
		}
		raw := rawRow{p.rowCount, rec[0], rec[1], rec[2], rec[3], rec[4], rec[5], rec[6], rec[7], rec[8], rec[9]}
		ferrs, cents, currency := validateRow(raw, today)
		if len(ferrs) > 0 {
			p.addInvalid(raw, ferrs)
			continue
		}
		p.valid = append(p.valid, model.WireRow{
			RowNumber: raw.RowNumber, ExternalWireID: ExternalWireID(batchID, raw.RowNumber),
			OriginatingAccount: raw.OriginatingAccount, ProcessDate: raw.ProcessDate, AmountCents: cents,
			Currency: currency, BeneficiaryName: raw.BeneficiaryName, BeneficiaryAccount: raw.BeneficiaryAccount,
			BeneficiaryRouting: raw.BeneficiaryRouting, BeneficiaryBank: raw.BeneficiaryBank,
			Reference: raw.Reference, Subsidiary: raw.Subsidiary,
		})
	}
	if p.rowCount == 0 {
		return nil, fileErr("the file has a header but no data rows")
	}
	return p, nil
}

func (p *parsedFile) addInvalid(raw rawRow, ferrs []fieldError) {
	codes := make([]string, 0, len(ferrs))
	for _, e := range ferrs {
		p.errors = append(p.errors, model.ValidationError{RowNumber: raw.RowNumber, Column: e.column, Code: e.code, Message: e.message})
		codes = append(codes, e.code)
	}
	p.invalid = append(p.invalid, model.InvalidRow{
		RowNumber: raw.RowNumber, OriginatingAccount: raw.OriginatingAccount, ProcessDate: raw.ProcessDate,
		Amount: raw.Amount, Currency: raw.Currency, BeneficiaryName: raw.BeneficiaryName,
		BeneficiaryAccount: raw.BeneficiaryAccount, BeneficiaryRouting: raw.BeneficiaryRouting,
		BeneficiaryBank: raw.BeneficiaryBank, Reference: raw.Reference, Subsidiary: raw.Subsidiary, ErrorCodes: codes,
	})
}

// checkOriginatingAccounts asks Core once about every distinct originating account among the rows that passed
// format checks, and moves rows whose account is unknown, not the customer's, closed, or on hold to invalid.
func checkOriginatingAccounts(ctx context.Context, checker AccountChecker, in Input, p *parsedFile) error {
	seen := map[string]bool{}
	var distinct []string
	for _, w := range p.valid {
		if !seen[w.OriginatingAccount] {
			seen[w.OriginatingAccount] = true
			distinct = append(distinct, w.OriginatingAccount)
		}
	}
	if len(distinct) == 0 {
		return nil
	}
	sort.Strings(distinct)
	statuses, err := checker.ValidateAccounts(ctx, in.FiID, in.CustomerID, distinct)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrAccountsUnavailable, err)
	}

	kept := p.valid[:0]
	for _, w := range p.valid {
		msg := accountProblem(w.OriginatingAccount, statuses[w.OriginatingAccount])
		if msg == "" {
			kept = append(kept, w)
			continue
		}
		p.addInvalid(rawFromWire(w), []fieldError{{"originating_account", CodeOriginatingAccount, msg}})
	}
	p.valid = kept
	return nil
}

func accountProblem(account string, s AccountStatus) string {
	masked := "ending " + model.AccountLast4(account)
	switch {
	case !s.Exists || !s.OwnedByCustomer:
		return fmt.Sprintf("originating account %s is not one of your company's accounts at this bank", masked)
	case s.Status == "Closed":
		return fmt.Sprintf("originating account %s is closed", masked)
	case s.Status == "OnHold":
		return fmt.Sprintf("originating account %s is on hold; contact the bank", masked)
	case s.Status != "Active":
		return fmt.Sprintf("originating account %s cannot send wires", masked)
	}
	return ""
}

func rawFromWire(w model.WireRow) rawRow {
	return rawRow{w.RowNumber, w.OriginatingAccount, w.ProcessDate, centsString(w.AmountCents), w.Currency,
		w.BeneficiaryName, w.BeneficiaryAccount, w.BeneficiaryRouting, w.BeneficiaryBank, w.Reference, w.Subsidiary}
}

// canonicalize sorts valid rows into CANONICAL ORDER (groupKey ascending, then rowNumber ascending) and
// builds the group index. Chunk planning and ReadRange both depend on this exact order.
func canonicalize(rows []model.WireRow) ([]model.WireRow, []model.GroupInfo, int64) {
	sort.Slice(rows, func(i, j int) bool {
		gi, gj := model.GroupKey(rows[i].OriginatingAccount, rows[i].ProcessDate), model.GroupKey(rows[j].OriginatingAccount, rows[j].ProcessDate)
		if gi != gj {
			return gi < gj
		}
		return rows[i].RowNumber < rows[j].RowNumber
	})
	var groups []model.GroupInfo
	var total int64
	for i, w := range rows {
		key := model.GroupKey(w.OriginatingAccount, w.ProcessDate)
		if len(groups) == 0 || groups[len(groups)-1].GroupKey != key {
			groups = append(groups, model.GroupInfo{GroupKey: key, OriginatingAccount: w.OriginatingAccount, ProcessDate: w.ProcessDate, StartOffset: i})
		}
		g := &groups[len(groups)-1]
		g.RowCount++
		g.TotalAmountCents += w.AmountCents
		total += w.AmountCents
	}
	return rows, nonNil(groups), total
}

func headerMatches(h []string) bool {
	if len(h) != len(Header) {
		return false
	}
	for i := range h {
		if !strings.EqualFold(strings.TrimSpace(h[i]), Header[i]) {
			return false
		}
	}
	return true
}

func writePages(ctx context.Context, s claimcheck.Store, pageSize, n int, key func(int) string, slice func(lo, hi int) any) error {
	for page, lo := 0, 0; lo < n; page, lo = page+1, lo+pageSize {
		if err := putJSON(ctx, s, key(page), slice(lo, min(lo+pageSize, n))); err != nil {
			return err
		}
	}
	return nil
}

func putJSON(ctx context.Context, s claimcheck.Store, key string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := s.Put(ctx, key, data, "application/json"); err != nil {
		return fmt.Errorf("staging: write %s: %w", key, err)
	}
	return nil
}

// ExternalWireID is the wire's idempotency key in Core: "{batchId}-{rowNumber:05d}".
func ExternalWireID(batchID string, rowNumber int) string {
	return fmt.Sprintf("%s-%05d", batchID, rowNumber)
}

func centsString(c int64) string { return fmt.Sprintf("%d.%02d", c/100, c%100) }

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
