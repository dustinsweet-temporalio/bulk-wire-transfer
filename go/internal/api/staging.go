// Staging endpoints: upload (parse + validate + write the claim check), the review screen's paged rows and
// errors, the error report, and the sample files. No Temporal involved: staging is a synchronous request
// that completes or tells the user to try again.
package api

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"bulkwire/internal/auth"
	"bulkwire/internal/claimcheck"
	"bulkwire/internal/model"
	"bulkwire/internal/samples"
	"bulkwire/internal/staging"
)

const maxUploadBytes = 20 << 20

// StagingSummary is what the portal shows on the review screen.
type StagingSummary struct {
	BatchID          string         `json:"batchId"`
	FiID             string         `json:"fiId"`
	CustomerID       string         `json:"customerId"`
	FileName         string         `json:"fileName"`
	Status           string         `json:"status"`
	RowCount         int            `json:"rowCount"`
	ValidRowCount    int            `json:"validRowCount"`
	ErrorCount       int            `json:"errorCount"`
	InvalidRowCount  int            `json:"invalidRowCount"`
	TotalAmountCents int64          `json:"totalAmountCents"`
	Groups           []StagingGroup `json:"groups"`
	UploadedBy       string         `json:"uploadedBy"`
	UploadedAt       time.Time      `json:"uploadedAt"`
}

type StagingGroup struct {
	GroupKey                 string `json:"groupKey"`
	OriginatingAccountMasked string `json:"originatingAccountMasked"`
	ProcessDate              string `json:"processDate"`
	RowCount                 int    `json:"rowCount"`
	TotalAmountCents         int64  `json:"totalAmountCents"`
	ChunkCount               int    `json:"chunkCount"` // at the FI's current default chunk size (informational)
}

func (s *Server) summaryOf(m model.Manifest) StagingSummary {
	chunkSize := s.Cfg.Defaults.ChunkSize
	if d, err := s.Defaults.get(m.FiID); err == nil {
		chunkSize = d.chunkSize
	}
	out := StagingSummary{BatchID: m.BatchID, FiID: m.FiID, CustomerID: m.CustomerID, FileName: m.FileName, Status: m.Status,
		RowCount: m.RowCount, ValidRowCount: m.ValidRowCount, ErrorCount: m.ErrorCount, InvalidRowCount: m.InvalidRowCount(),
		TotalAmountCents: m.TotalAmountCents, UploadedBy: m.UploadedBy, UploadedAt: m.UploadedAt, Groups: []StagingGroup{}}
	for _, g := range m.Groups {
		out.Groups = append(out.Groups, StagingGroup{GroupKey: g.GroupKey, OriginatingAccountMasked: model.MaskAccount(g.OriginatingAccount),
			ProcessDate: g.ProcessDate, RowCount: g.RowCount, TotalAmountCents: g.TotalAmountCents,
			ChunkCount: (g.RowCount + chunkSize - 1) / chunkSize})
	}
	return out
}

// upload stages a CSV. 201 with the summary; 400 for file-level problems; 413 too large; 503 when Core cannot
// validate accounts or the claim-check store is down.
func (s *Server) upload(w http.ResponseWriter, r *http.Request, c auth.Claims) {
	if c.CustomerID == "" {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "only customer users upload wire files")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	file, header, err := r.FormFile("file")
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", "the file is larger than 20 MB")
			return
		}
		writeError(w, http.StatusBadRequest, "FILE", "attach the CSV file as form field \"file\"")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", "the file is larger than 20 MB")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	m, err := staging.Stage(ctx, s.Store, s.Core, staging.Input{
		BatchID: ulid.Make().String(), FiID: c.FiID, CustomerID: c.CustomerID, UploadedBy: c.UserID(),
		FileName: header.Filename, Data: data, UploadedAt: s.Now(), MaxRows: s.Cfg.Defaults.MaxRows,
		PageSize: s.Cfg.ClaimCheck.PageSize,
	})
	var fileErr *staging.FileError
	switch {
	case errors.As(err, &fileErr):
		writeJSON(w, http.StatusBadRequest, apiError{Code: "FILE", Message: fileErr.Errors[0].Message, Errors: fileErr.Errors})
		return
	case errors.Is(err, staging.ErrAccountsUnavailable):
		writeError(w, http.StatusServiceUnavailable, "CORE_UNAVAILABLE", "Bank system unavailable, try again in a moment.")
		return
	case err != nil:
		s.Log.Error("staging failed", "err", err)
		writeError(w, http.StatusServiceUnavailable, "CLAIM_CHECK_UNAVAILABLE", "Could not store the file right now, try again in a moment.")
		return
	}
	s.Log.Info("batch staged", "fi", m.FiID, "batchId", m.BatchID, "rows", m.RowCount, "status", m.Status)
	writeJSON(w, http.StatusCreated, s.summaryOf(m))
}

// manifestFor loads a staged batch for the caller, enforcing tenant and customer scope (404 otherwise).
func (s *Server) manifestFor(ctx context.Context, w http.ResponseWriter, c auth.Claims, batchID string) (model.Manifest, bool) {
	m, err := staging.ReadManifest(ctx, s.Store, c.FiID, batchID)
	switch {
	case errors.Is(err, claimcheck.ErrNotFound):
		writeError(w, http.StatusNotFound, "NOT_FOUND", "batch not found (or its staged data has expired)")
		return m, false
	case err != nil:
		writeError(w, http.StatusServiceUnavailable, "CLAIM_CHECK_UNAVAILABLE", "staged data is temporarily unavailable")
		return m, false
	case !c.IsBankOps() && m.CustomerID != c.CustomerID:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "batch not found")
		return m, false
	}
	return m, true
}

// StagedRow is a valid row for display: account numbers masked.
type StagedRow struct {
	RowNumber                int    `json:"rowNumber"`
	OriginatingAccountMasked string `json:"originatingAccountMasked"`
	ProcessDate              string `json:"processDate"`
	AmountCents              int64  `json:"amountCents"`
	Currency                 string `json:"currency"`
	BeneficiaryName          string `json:"beneficiaryName"`
	BeneficiaryAccountMasked string `json:"beneficiaryAccountMasked"`
	BeneficiaryRouting       string `json:"beneficiaryRouting"`
	BeneficiaryBank          string `json:"beneficiaryBank"`
	Reference                string `json:"reference"`
	Subsidiary               string `json:"subsidiary"`
}

// stagingView pages the claim check for the review screen: view=valid (canonical order) | invalid | errors.
func (s *Server) stagingView(w http.ResponseWriter, r *http.Request, c auth.Claims) {
	m, ok := s.manifestFor(r.Context(), w, c, r.PathValue("batchId"))
	if !ok {
		return
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 100
	}
	limit, offset = min(limit, 200), max(offset, 0)
	page := func(total int) (int, int) { lo := min(offset, total); return lo, min(limit, total-lo) }

	var items any
	var total int
	var err error
	switch view := r.URL.Query().Get("view"); view {
	case "", "valid":
		total = m.ValidRowCount
		lo, n := page(total)
		var rows []model.WireRow
		rows, err = staging.ReadRange(r.Context(), s.Store, m, lo, n)
		out := make([]StagedRow, 0, len(rows))
		for _, w := range rows {
			out = append(out, StagedRow{RowNumber: w.RowNumber, OriginatingAccountMasked: model.MaskAccount(w.OriginatingAccount),
				ProcessDate: w.ProcessDate, AmountCents: w.AmountCents, Currency: w.Currency, BeneficiaryName: w.BeneficiaryName,
				BeneficiaryAccountMasked: model.MaskAccount(w.BeneficiaryAccount), BeneficiaryRouting: w.BeneficiaryRouting,
				BeneficiaryBank: w.BeneficiaryBank, Reference: w.Reference, Subsidiary: w.Subsidiary})
		}
		items = out
	case "invalid":
		total = m.InvalidRowCount()
		lo, n := page(total)
		var rows []model.InvalidRow
		rows, err = staging.ReadInvalidRange(r.Context(), s.Store, m, lo, n)
		for i := range rows {
			rows[i].OriginatingAccount = model.MaskAccount(rows[i].OriginatingAccount)
			rows[i].BeneficiaryAccount = model.MaskAccount(rows[i].BeneficiaryAccount)
		}
		items = rows
	case "errors":
		var all []model.ValidationError
		all, err = staging.ReadErrors(r.Context(), s.Store, m)
		total = len(all)
		lo, n := page(total)
		items = all[lo : lo+n]
	default:
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "view must be valid, invalid, or errors")
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "CLAIM_CHECK_UNAVAILABLE", "staged data is temporarily unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"total": total, "offset": offset, "limit": limit, "items": items})
}

func (s *Server) errorsCSV(w http.ResponseWriter, r *http.Request, c auth.Claims) {
	m, ok := s.manifestFor(r.Context(), w, c, r.PathValue("batchId"))
	if !ok {
		return
	}
	errs, err := staging.ReadErrors(r.Context(), s.Store, m)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "CLAIM_CHECK_UNAVAILABLE", "staged data is temporarily unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", strings.TrimSuffix(m.FileName, ".csv")+"-errors.csv"))
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"row_number", "column", "code", "message"})
	for _, e := range errs {
		_ = cw.Write([]string{strconv.Itoa(e.RowNumber), e.Column, e.Code, e.Message})
	}
	cw.Flush()
}

// ---- samples ----------------------------------------------------------------------------------------------

func (s *Server) listSamples(w http.ResponseWriter, _ *http.Request, c auth.Claims) {
	var out []samples.Info
	for _, info := range samples.Catalog() {
		if info.FiID == c.FiID {
			out = append(out, info)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// sampleCSV serves a freshly generated sample (process dates relative to today).
func (s *Server) sampleCSV(w http.ResponseWriter, r *http.Request, _ auth.Claims) {
	name, ok := strings.CutSuffix(r.PathValue("file"), ".csv")
	data, err := samples.Generate(name, s.Now())
	if !ok || err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "unknown sample")
		return
	}
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name+".csv"))
	_, _ = w.Write(data)
}
