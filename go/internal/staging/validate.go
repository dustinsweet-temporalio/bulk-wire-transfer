// Package staging turns an uploaded CSV into a staged batch in the claim-check store: parse, validate
// (synchronously, so the user sees every row and every error before anything is drafted), group, write
// pages in canonical order, and write the manifest last. It also reads staged data back (ReadRange) and
// plans chunks (Plan). Staging is deliberately NOT orchestrated by Temporal: it is a synchronous request
// that either completes or tells the user to try again.
package staging

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Validation codes.
const (
	CodeRequired           = "REQUIRED"
	CodeDateFormat         = "DATE_FORMAT"
	CodeDateInPast         = "DATE_IN_PAST"
	CodeDateTooFar         = "DATE_TOO_FAR"
	CodeAmountFormat       = "AMOUNT_FORMAT"
	CodeAmountRange        = "AMOUNT_RANGE"
	CodeCurrency           = "CURRENCY"
	CodeRoutingFormat      = "ROUTING_FORMAT"
	CodeRoutingChecksum    = "ROUTING_CHECKSUM"
	CodeAccountFormat      = "ACCOUNT_FORMAT"
	CodeLength             = "LENGTH"
	CodeOriginatingAccount = "ORIGINATING_ACCOUNT"
	CodeFile               = "FILE"
	CodeTooManyChunks      = "TOO_MANY_CHUNKS" // batch-level (row 0): the history guard, see ChunkLimitMessage
)

// MaxAmountCents is $9,999,999.99.
const MaxAmountCents int64 = 999_999_999

// MaxDaysAhead is how far in the future a process date may be.
const MaxDaysAhead = 30

var (
	amountPattern  = regexp.MustCompile(`^-?\d{1,13}(\.\d{1,2})?$`)
	datePattern    = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	digitsPattern  = regexp.MustCompile(`^\d+$`)
	routingPattern = regexp.MustCompile(`^\d{9}$`)
)

// ParseCents parses a decimal amount string directly into integer cents, never through floating point.
// "10" -> 1000, "10.5" -> 1050, "0.10" -> 10. Thousands separators ("1,000.00") are a format error.
// Negative amounts parse (and then fail the range check, AMOUNT_RANGE).
func ParseCents(s string) (int64, bool) {
	if !amountPattern.MatchString(s) {
		return 0, false
	}
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	whole, frac, _ := strings.Cut(s, ".")
	frac = (frac + "00")[:2]
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, false
	}
	f, _ := strconv.ParseInt(frac, 10, 64)
	cents := w*100 + f
	if neg {
		cents = -cents
	}
	return cents, true
}

// ValidRoutingChecksum applies the ABA checksum:
// (3*(d1+d4+d7) + 7*(d2+d5+d8) + (d3+d6+d9)) mod 10 == 0.
func ValidRoutingChecksum(r string) bool {
	if !routingPattern.MatchString(r) {
		return false
	}
	d := func(i int) int { return int(r[i] - '0') }
	sum := 3*(d(0)+d(3)+d(6)) + 7*(d(1)+d(4)+d(7)) + (d(2) + d(5) + d(8))
	return sum%10 == 0
}

// rawRow is one CSV data row, whitespace-trimmed, in header order.
type rawRow struct {
	RowNumber          int
	OriginatingAccount string
	ProcessDate        string
	Amount             string
	Currency           string
	BeneficiaryName    string
	BeneficiaryAccount string
	BeneficiaryRouting string
	BeneficiaryBank    string
	Reference          string
	Subsidiary         string
}

// validateRow applies every format rule to one row and returns its errors (empty = valid format) and, when
// valid, the parsed amount and normalized currency. A row may produce several errors.
func validateRow(r rawRow, today time.Time) (errs []fieldError, cents int64, currency string) {
	add := func(column, code, msg string) { errs = append(errs, fieldError{column, code, msg}) }

	required := []struct{ column, value string }{
		{"originating_account", r.OriginatingAccount},
		{"process_date", r.ProcessDate},
		{"amount", r.Amount},
		{"beneficiary_name", r.BeneficiaryName},
		{"beneficiary_account", r.BeneficiaryAccount},
		{"beneficiary_routing", r.BeneficiaryRouting},
	}
	for _, f := range required {
		if f.value == "" {
			add(f.column, CodeRequired, fmt.Sprintf("%s is required", f.column))
		}
	}

	if r.ProcessDate != "" {
		d, err := time.Parse(time.DateOnly, r.ProcessDate)
		switch {
		case !datePattern.MatchString(r.ProcessDate) || err != nil:
			add("process_date", CodeDateFormat, "process_date must be a date in YYYY-MM-DD format")
		case d.Before(today):
			add("process_date", CodeDateInPast, fmt.Sprintf("process_date %s is in the past", r.ProcessDate))
		case d.After(today.AddDate(0, 0, MaxDaysAhead)):
			add("process_date", CodeDateTooFar, fmt.Sprintf("process_date %s is more than %d days ahead", r.ProcessDate, MaxDaysAhead))
		}
	}

	if r.Amount != "" {
		c, ok := ParseCents(r.Amount)
		switch {
		case !ok:
			add("amount", CodeAmountFormat, "amount must be a number with at most 2 decimal places and no thousands separators")
		case c <= 0 || c > MaxAmountCents:
			add("amount", CodeAmountRange, "amount must be greater than 0 and at most 9,999,999.99")
		default:
			cents = c
		}
	}

	currency = strings.ToUpper(r.Currency)
	if currency == "" {
		currency = "USD"
	}
	if currency != "USD" {
		add("currency", CodeCurrency, "currency must be USD")
	}

	if r.BeneficiaryRouting != "" {
		switch {
		case !routingPattern.MatchString(r.BeneficiaryRouting):
			add("beneficiary_routing", CodeRoutingFormat, "beneficiary_routing must be exactly 9 digits")
		case !ValidRoutingChecksum(r.BeneficiaryRouting):
			add("beneficiary_routing", CodeRoutingChecksum, "beneficiary_routing fails the ABA checksum")
		}
	}

	if r.BeneficiaryAccount != "" {
		if n := len(r.BeneficiaryAccount); !digitsPattern.MatchString(r.BeneficiaryAccount) || n < 4 || n > 17 {
			add("beneficiary_account", CodeAccountFormat, "beneficiary_account must be 4 to 17 digits")
		}
	}

	for _, f := range []struct {
		column string
		value  string
		max    int
	}{
		{"beneficiary_name", r.BeneficiaryName, 70},
		{"beneficiary_bank", r.BeneficiaryBank, 70},
		{"reference", r.Reference, 140},
		{"subsidiary", r.Subsidiary, 70},
	} {
		if utf8.RuneCountInString(f.value) > f.max {
			add(f.column, CodeLength, fmt.Sprintf("%s is longer than %d characters", f.column, f.max))
		}
	}
	return errs, cents, currency
}

type fieldError struct{ column, code, message string }
