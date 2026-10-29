// Tests for staging: every validation rule, the canonical page layout in the claim-check store,
// the manifest-written-last guarantee, and chunk planning (groups split into chunks, never spanning groups).

package staging

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"bulkwire/internal/claimcheck"
	"bulkwire/internal/model"
)

var today = time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)

func TestParseCents(t *testing.T) {
	for in, want := range map[string]int64{"0.10": 10, "10": 1000, "10.5": 1050, "2345.67": 234567, "-125.00": -12500} {
		got, ok := ParseCents(in)
		if !ok || got != want {
			t.Errorf("ParseCents(%q) = %d, %v; want %d", in, got, ok, want)
		}
	}
	for _, in := range []string{"1,000.00", "10.555", "$10", "1e3", "", ".5", "10."} {
		if _, ok := ParseCents(in); ok {
			t.Errorf("ParseCents(%q) accepted", in)
		}
	}
}

func TestABAChecksum(t *testing.T) {
	for _, r := range []string{"021000021", "011000015", "121000358"} {
		if !ValidRoutingChecksum(r) {
			t.Errorf("%s should be valid", r)
		}
	}
	for _, r := range []string{"021000022", "123456789", "02100002"} {
		if ValidRoutingChecksum(r) {
			t.Errorf("%s should be invalid", r)
		}
	}
}

func row(mod func(*rawRow)) rawRow {
	r := rawRow{RowNumber: 1, OriginatingAccount: "4401220017", ProcessDate: "2026-11-03", Amount: "1500.00",
		BeneficiaryName: "Acme Co", BeneficiaryAccount: "12345678", BeneficiaryRouting: "021000021"}
	if mod != nil {
		mod(&r)
	}
	return r
}

func TestEveryValidationRule(t *testing.T) {
	cases := []struct {
		name   string
		mod    func(*rawRow)
		column string
		code   string
	}{
		{"required name", func(r *rawRow) { r.BeneficiaryName = "" }, "beneficiary_name", CodeRequired},
		{"required account", func(r *rawRow) { r.OriginatingAccount = "" }, "originating_account", CodeRequired},
		{"date format", func(r *rawRow) { r.ProcessDate = "11/03/2026" }, "process_date", CodeDateFormat},
		{"impossible date", func(r *rawRow) { r.ProcessDate = "2026-02-30" }, "process_date", CodeDateFormat},
		{"date in past", func(r *rawRow) { r.ProcessDate = "2026-11-01" }, "process_date", CodeDateInPast},
		{"date too far", func(r *rawRow) { r.ProcessDate = "2026-12-03" }, "process_date", CodeDateTooFar},
		{"amount format", func(r *rawRow) { r.Amount = "1,000.00" }, "amount", CodeAmountFormat},
		{"amount negative", func(r *rawRow) { r.Amount = "-125.00" }, "amount", CodeAmountRange},
		{"amount zero", func(r *rawRow) { r.Amount = "0" }, "amount", CodeAmountRange},
		{"amount too big", func(r *rawRow) { r.Amount = "10000000.00" }, "amount", CodeAmountRange},
		{"currency", func(r *rawRow) { r.Currency = "EUR" }, "currency", CodeCurrency},
		{"routing format", func(r *rawRow) { r.BeneficiaryRouting = "02100002" }, "beneficiary_routing", CodeRoutingFormat},
		{"routing checksum", func(r *rawRow) { r.BeneficiaryRouting = "021000022" }, "beneficiary_routing", CodeRoutingChecksum},
		{"account format", func(r *rawRow) { r.BeneficiaryAccount = "12a45" }, "beneficiary_account", CodeAccountFormat},
		{"account too short", func(r *rawRow) { r.BeneficiaryAccount = "123" }, "beneficiary_account", CodeAccountFormat},
		{"name length", func(r *rawRow) { r.BeneficiaryName = strings.Repeat("n", 71) }, "beneficiary_name", CodeLength},
		{"reference length", func(r *rawRow) { r.Reference = strings.Repeat("r", 141) }, "reference", CodeLength},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs, _, _ := validateRow(row(tc.mod), today)
			if len(errs) != 1 || errs[0].code != tc.code || errs[0].column != tc.column {
				t.Fatalf("got %+v, want one %s on %s", errs, tc.code, tc.column)
			}
		})
	}
	if errs, cents, cur := validateRow(row(nil), today); len(errs) != 0 || cents != 150000 || cur != "USD" {
		t.Fatalf("valid row: %+v %d %s", errs, cents, cur)
	}
	errs, _, _ := validateRow(row(func(r *rawRow) { r.BeneficiaryName = ""; r.Amount = "abc"; r.Currency = "GBP" }), today)
	if len(errs) != 3 {
		t.Fatalf("a row may produce several errors: %+v", errs)
	}
}

type fakeCore struct {
	statuses map[string]AccountStatus
	err      error
	calls    [][]string
}

func (f *fakeCore) ValidateAccounts(_ context.Context, _, _ string, accts []string) (map[string]AccountStatus, error) {
	f.calls = append(f.calls, accts)
	return f.statuses, f.err
}

var activeCore = &fakeCore{statuses: map[string]AccountStatus{
	"4401220017": {true, true, "Active"}, "4401220033": {true, true, "Active"}, "4401220058": {true, true, "Active"},
	"4401220099": {true, true, "OnHold"},
}}

const header = "originating_account,process_date,amount,currency,beneficiary_name,beneficiary_account,beneficiary_routing,beneficiary_bank,reference,subsidiary\n"

func input(csv string) Input {
	return Input{BatchID: "01JBATCH", FiID: "lakeshore", CustomerID: "summit-logistics", UploadedBy: "jordan",
		FileName: "f.csv", Data: []byte(csv), UploadedAt: today.Add(9 * time.Hour), MaxRows: 10000, PageSize: 2}
}

func TestStageCanonicalOrderAndLayout(t *testing.T) {
	csv := header +
		"4401220058,2026-11-03,1.00,USD,A,12345678,021000021,,,\n" + // row 1: group 3
		"4401220017,2026-11-04,2.00,,B,12345678,021000021,,,\n" + // row 2: group 2
		"4401220017,2026-11-03,3.00,USD,C,12345678,021000021,,,\n" + // row 3: group 1
		"4401220017,2026-11-03,4.00,USD,D,12345678,021000022,,,\n" + // row 4: invalid checksum
		"4401220017,2026-11-03,5.00,USD,E,12345678,021000021,,,\n" + // row 5: group 1
		"4401229999,2026-11-03,6.00,USD,F,12345678,021000021,,,\n" + // row 6: unknown account
		"4401220099,2026-11-03,7.00,USD,G,12345678,021000021,,,\n" // row 7: on hold
	store := claimcheck.NewMemory()
	core := &fakeCore{statuses: activeCore.statuses}
	m, err := Stage(context.Background(), store, core, input(csv))
	if err != nil {
		t.Fatal(err)
	}
	if m.Status != model.StagingInvalid || m.RowCount != 7 || m.ValidRowCount != 4 || m.ErrorCount != 3 || m.TotalAmountCents != 1100 {
		t.Fatalf("manifest: %+v", m)
	}
	if len(core.calls) != 1 || strings.Join(core.calls[0], ",") != "4401220017,4401220058,4401220099,4401229999" {
		t.Fatalf("Core must be called once with distinct accounts: %v", core.calls)
	}
	var order []int
	rows, err := ReadRange(context.Background(), store, m, 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		order = append(order, r.RowNumber)
	}
	if fmt.Sprint(order) != "[3 5 2 1]" {
		t.Fatalf("canonical order = %v, want [3 5 2 1]", order)
	}
	if rows[0].ExternalWireID != "01JBATCH-00003" {
		t.Fatalf("external id %q", rows[0].ExternalWireID)
	}
	if len(m.Groups) != 3 || m.Groups[0].RowCount != 2 || m.Groups[1].StartOffset != 2 || m.Groups[2].GroupKey != "4401220058|2026-11-03" {
		t.Fatalf("groups: %+v", m.Groups)
	}
	errs, _ := ReadErrors(context.Background(), store, m)
	codes := map[int]string{}
	for _, e := range errs {
		codes[e.RowNumber] = e.Code
	}
	if codes[4] != CodeRoutingChecksum || codes[6] != CodeOriginatingAccount || codes[7] != CodeOriginatingAccount {
		t.Fatalf("errors: %+v", errs)
	}
	if !strings.Contains(errs[1].Message+errs[2].Message, "ending 9999") || strings.Contains(fmt.Sprint(errs), "4401229999") {
		t.Fatalf("account numbers must be masked in messages: %+v", errs)
	}
	invalid, _ := ReadInvalidRange(context.Background(), store, m, 0, 3)
	if len(invalid) != 3 || invalid[0].RowNumber != 4 || invalid[0].BeneficiaryRouting != "021000022" {
		t.Fatalf("invalid rows: %+v", invalid)
	}
	keys := store.Reads()
	if keys[len(keys)-1] != InvalidPageKey("lakeshore", "01JBATCH", 1) {
		t.Fatalf("last read %s", keys[len(keys)-1])
	}
}

func TestManifestIsWrittenLast(t *testing.T) {
	store := &orderedStore{Memory: claimcheck.NewMemory()}
	if _, err := Stage(context.Background(), store, activeCore, input(header+"4401220017,2026-11-03,1.00,USD,A,12345678,021000021,,,\n")); err != nil {
		t.Fatal(err)
	}
	if store.puts[len(store.puts)-1] != ManifestKey("lakeshore", "01JBATCH") {
		t.Fatalf("puts: %v", store.puts)
	}
}

type orderedStore struct {
	*claimcheck.Memory
	puts []string
}

func (o *orderedStore) Put(ctx context.Context, key string, data []byte, ct string) error {
	o.puts = append(o.puts, key)
	return o.Memory.Put(ctx, key, data, ct)
}

func TestFileLevelErrors(t *testing.T) {
	cases := map[string]string{
		"wrong header": "acct,date\n1,2\n",
		"no rows":      header,
		"empty":        "",
		"not utf8":     header + "4401220017,2026-11-03,1.00,USD,\xff\xfe,12345678,021000021,,,\n",
		"column count": header + "4401220017,2026-11-03\n",
	}
	for name, csv := range cases {
		_, err := Stage(context.Background(), claimcheck.NewMemory(), activeCore, input(csv))
		var fe *FileError
		if !errors.As(err, &fe) || fe.Errors[0].Code != CodeFile {
			t.Errorf("%s: got %v", name, err)
		}
	}
	in := input(header + strings.Repeat("4401220017,2026-11-03,1.00,USD,A,12345678,021000021,,,\n", 3))
	in.MaxRows = 2
	if _, err := Stage(context.Background(), claimcheck.NewMemory(), activeCore, in); err == nil || !strings.Contains(err.Error(), "more than 2") {
		t.Errorf("too many rows: %v", err)
	}
	// Header comparison ignores case and surrounding whitespace; a UTF-8 BOM is tolerated.
	ok := "\xef\xbb\xbf" + strings.ToUpper(strings.ReplaceAll(header, ",", " , ")) + "4401220017,2026-11-03,1.00,USD,A,12345678,021000021,,,\n"
	if _, err := Stage(context.Background(), claimcheck.NewMemory(), activeCore, input(ok)); err != nil {
		t.Errorf("header variant rejected: %v", err)
	}
}

func TestCoreUnavailableAtStaging(t *testing.T) {
	_, err := Stage(context.Background(), claimcheck.NewMemory(), &fakeCore{err: errors.New("503")},
		input(header+"4401220017,2026-11-03,1.00,USD,A,12345678,021000021,,,\n"))
	if !errors.Is(err, ErrAccountsUnavailable) {
		t.Fatalf("got %v", err)
	}
}

func TestPlanSplitsGroupsAndNeverSpansThem(t *testing.T) {
	m := model.Manifest{Groups: []model.GroupInfo{
		{GroupKey: "a|1", StartOffset: 0, RowCount: 500},
		{GroupKey: "a|2", StartOffset: 500, RowCount: 251}, // 251 -> 250 + 1
		{GroupKey: "b|1", StartOffset: 751, RowCount: 30},
	}}
	chunks, err := Plan(m, 250)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(chunks))
	for i, c := range chunks {
		got[i] = fmt.Sprintf("%s:%d+%d", c.ChunkID, c.Offset, c.Length)
	}
	want := "[g00-c00:0+250 g00-c01:250+250 g01-c00:500+250 g01-c01:750+1 g02-c00:751+30]"
	if fmt.Sprint(got) != want {
		t.Fatalf("plan = %v\nwant  %s", got, want)
	}
	if _, err := Plan(m, 0); err == nil {
		t.Fatal("chunk size 0 accepted")
	}
}

func TestReadRangeReadsOnlyOverlappingPages(t *testing.T) {
	store := claimcheck.NewMemory()
	var rows []string
	for i := 1; i <= 7; i++ {
		rows = append(rows, fmt.Sprintf("4401220017,2026-11-03,%d.00,USD,N%d,12345678,021000021,,,", i, i))
	}
	in := input(header + strings.Join(rows, "\n") + "\n")
	in.PageSize = 3 // pages: [1 2 3] [4 5 6] [7]
	m, err := Stage(context.Background(), store, activeCore, in)
	if err != nil {
		t.Fatal(err)
	}
	before := len(store.Reads())
	got, err := ReadRange(context.Background(), store, m, 2, 3) // rows 3..5 span pages 0 and 1
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].RowNumber != 3 || got[2].RowNumber != 5 {
		t.Fatalf("rows: %+v", got)
	}
	reads := store.Reads()[before:]
	if fmt.Sprint(reads) != fmt.Sprint([]string{PageKey("lakeshore", "01JBATCH", 0), PageKey("lakeshore", "01JBATCH", 1)}) {
		t.Fatalf("pages read: %v", reads)
	}
	if _, err := ReadRange(context.Background(), store, m, 6, 2); err == nil {
		t.Fatal("out-of-range read accepted")
	}
	tail, _ := ReadRange(context.Background(), store, m, 6, 1)
	if len(tail) != 1 || tail[0].RowNumber != 7 {
		t.Fatalf("last page: %+v", tail)
	}
}

func TestMissingStagedDataIsNotFound(t *testing.T) {
	_, err := ReadManifest(context.Background(), claimcheck.NewMemory(), "lakeshore", "nope")
	if !errors.Is(err, claimcheck.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}
