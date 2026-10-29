// Package model holds the data types shared across the bulk wire services: staged rows and the staging
// manifest (claim-check contents), chunk descriptors (the only "rows" the workflow ever sees), and the Core
// error details attached to Activity failures.
//
// Money is always integer cents (int64). Account numbers in these types are full numbers because they are
// identifiers for Core; anything displayed or logged uses MaskAccount.
package model

import (
	"regexp"
	"time"
)

// WireRow is one valid, parsed wire as staged in the claim-check store. Activities read these; the
// workflow never does.
type WireRow struct {
	RowNumber          int    `json:"rowNumber"`
	ExternalWireID     string `json:"externalWireId"` // "{batchId}-{rowNumber:05d}"; Core idempotency key
	OriginatingAccount string `json:"originatingAccount"`
	ProcessDate        string `json:"processDate"` // YYYY-MM-DD
	AmountCents        int64  `json:"amountCents"`
	Currency           string `json:"currency"`
	BeneficiaryName    string `json:"beneficiaryName"`
	BeneficiaryAccount string `json:"beneficiaryAccount"`
	BeneficiaryRouting string `json:"beneficiaryRouting"`
	BeneficiaryBank    string `json:"beneficiaryBank"`
	Reference          string `json:"reference"`
	Subsidiary         string `json:"subsidiary"`
}

// InvalidRow keeps an invalid row's raw field values so the review screen can show everything the user
// uploaded. Invalid rows never enter canonical order and can never be drafted.
type InvalidRow struct {
	RowNumber          int      `json:"rowNumber"`
	OriginatingAccount string   `json:"originatingAccount"`
	ProcessDate        string   `json:"processDate"`
	Amount             string   `json:"amount"`
	Currency           string   `json:"currency"`
	BeneficiaryName    string   `json:"beneficiaryName"`
	BeneficiaryAccount string   `json:"beneficiaryAccount"`
	BeneficiaryRouting string   `json:"beneficiaryRouting"`
	BeneficiaryBank    string   `json:"beneficiaryBank"`
	Reference          string   `json:"reference"`
	Subsidiary         string   `json:"subsidiary"`
	ErrorCodes         []string `json:"errorCodes"`
}

// ValidationError is one problem found at staging. RowNumber 0 means a file-level problem (code FILE).
type ValidationError struct {
	RowNumber int    `json:"rowNumber"`
	Column    string `json:"column"`
	Code      string `json:"code"`
	Message   string `json:"message"`
}

// Staging statuses.
const (
	StagingValid   = "VALID"
	StagingInvalid = "INVALID"
)

// Manifest describes a staged batch. It is written LAST, so its presence means staging completed.
type Manifest struct {
	BatchID          string      `json:"batchId"`
	FiID             string      `json:"fiId"`
	CustomerID       string      `json:"customerId"`
	UploadedBy       string      `json:"uploadedBy"`
	FileName         string      `json:"fileName"`
	FileSHA256       string      `json:"fileSha256"`
	UploadedAt       time.Time   `json:"uploadedAt"`
	Status           string      `json:"status"`   // VALID | INVALID
	RowCount         int         `json:"rowCount"` // all data rows
	ValidRowCount    int         `json:"validRowCount"`
	ErrorCount       int         `json:"errorCount"`
	TotalAmountCents int64       `json:"totalAmountCents"` // valid rows only
	PageSize         int         `json:"pageSize"`
	Groups           []GroupInfo `json:"groups"` // canonical order
}

// InvalidRowCount is the number of rows that failed validation.
func (m Manifest) InvalidRowCount() int { return m.RowCount - m.ValidRowCount }

// GroupInfo is one group: all valid rows sharing originating account + process date. Core limits are
// evaluated per group, and a chunk never mixes groups.
type GroupInfo struct {
	GroupKey           string `json:"groupKey"` // "{originatingAccount}|{processDate}"
	OriginatingAccount string `json:"originatingAccount"`
	ProcessDate        string `json:"processDate"`
	StartOffset        int    `json:"startOffset"` // index into canonical order
	RowCount           int    `json:"rowCount"`
	TotalAmountCents   int64  `json:"totalAmountCents"`
}

// ChunkRef is a REFERENCE to a slice of one group in canonical order. This is all the workflow keeps
// about rows: a few dozen of these are safe in history; the rows themselves are not.
type ChunkRef struct {
	ChunkID  string `json:"chunkId"` // "g{groupIndex:02d}-c{chunkIndex:02d}", e.g. "g03-c01"
	GroupKey string `json:"groupKey"`
	Offset   int    `json:"offset"` // canonical-order index of the chunk's first row
	Length   int    `json:"length"`
}

// GroupKey builds the group key for an originating account and process date.
func GroupKey(originatingAccount, processDate string) string {
	return originatingAccount + "|" + processDate
}

// MaskAccount renders an account number for display: "4401220033" -> "••0033". Full numbers never appear
// in Search Attributes, Workflow IDs, logs, or timeline text.
func MaskAccount(account string) string { return "••" + AccountLast4(account) }

// longDigitRun matches account- and routing-number-shaped runs: 8 or more digits not inside a longer word.
var longDigitRun = regexp.MustCompile(`\b\d{8,}\b`)

// RedactAccountNumbers masks every account- or routing-number-shaped run of digits in free text ("account
// 4401220033 ..." -> "account ••0033 ..."). WHY: failure messages are not payloads, so they are NOT encrypted
// (the Temporal Service stores them in plaintext). Core already masks its own messages; this is the guard at
// the boundary in case a future message does not.
func RedactAccountNumbers(text string) string {
	return longDigitRun.ReplaceAllStringFunc(text, MaskAccount)
}

// AccountLast4 returns the last four characters of an account number (for "ending 0033" messages).
func AccountLast4(account string) string {
	if len(account) <= 4 {
		return account
	}
	return account[len(account)-4:]
}
