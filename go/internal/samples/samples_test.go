// Tests that the sample generator is deterministic and that each sample has the shape the demo scenarios rely on.

package samples

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"testing"
	"time"

	"bulkwire/internal/claimcheck"
	"bulkwire/internal/model"
	"bulkwire/internal/staging"
)

var today = time.Date(2026, 11, 2, 15, 4, 5, 0, time.UTC)

// allActive answers like a healthy Core: every account exists and is Active, except the unknown one.
type allActive struct{}

func (allActive) ValidateAccounts(_ context.Context, _, _ string, accts []string) (map[string]staging.AccountStatus, error) {
	out := map[string]staging.AccountStatus{}
	for _, a := range accts {
		if a != UnknownAccount {
			out[a] = staging.AccountStatus{Exists: true, OwnedByCustomer: true, Status: "Active"}
		}
	}
	return out, nil
}

func stage(t *testing.T, name string) model.Manifest {
	t.Helper()
	data, err := Generate(name, today)
	if err != nil {
		t.Fatal(err)
	}
	info := infoOf(name)
	m, err := staging.Stage(context.Background(), claimcheck.NewMemory(), allActive{}, staging.Input{
		BatchID: "01JSAMPLE", FiID: info.FiID, CustomerID: info.CustomerID, UploadedBy: "test", FileName: name + ".csv",
		Data: data, UploadedAt: today, MaxRows: 10000, PageSize: 500,
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func infoOf(name string) Info {
	for _, i := range Catalog() {
		if i.Name == name {
			return i
		}
	}
	return Info{}
}

func TestDeterministicForAFixedDate(t *testing.T) {
	for _, info := range Catalog() {
		a, _ := Generate(info.Name, today)
		b, _ := Generate(info.Name, today.Add(5*time.Hour)) // same UTC date
		if !bytes.Equal(a, b) {
			t.Errorf("%s is not deterministic", info.Name)
		}
		c, _ := Generate(info.Name, today.AddDate(0, 0, 1))
		if bytes.Equal(a, c) {
			t.Errorf("%s ignores the date", info.Name)
		}
	}
}

func TestEverySampleHasItsRowCountAndValidRoutings(t *testing.T) {
	for _, info := range Catalog() {
		data, _ := Generate(info.Name, today)
		recs, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
		if err != nil {
			t.Fatal(err)
		}
		if len(recs)-1 != info.Rows {
			t.Errorf("%s: %d rows, want %d", info.Name, len(recs)-1, info.Rows)
		}
		bad := 0
		for _, r := range recs[1:] {
			if !staging.ValidRoutingChecksum(r[6]) {
				bad++
			}
		}
		want := 0
		if info.Name == "summit-vendors-with-errors" {
			want = 4
		}
		if bad != want {
			t.Errorf("%s: %d routing numbers fail the checksum, want %d", info.Name, bad, want)
		}
	}
}

func TestPayroll5000Shape(t *testing.T) {
	m := stage(t, "summit-payroll-5000")
	if m.Status != model.StagingValid || m.ValidRowCount != 5000 {
		t.Fatalf("manifest: %+v", m)
	}
	var sizes []int
	var coldTotal int64
	for _, g := range m.Groups {
		sizes = append(sizes, g.RowCount)
		if g.OriginatingAccount == coldChain {
			coldTotal += g.TotalAmountCents
		}
	}
	if fmt.Sprint(sizes) != "[1200 1200 1049 251 800 500]" {
		t.Fatalf("group sizes %v", sizes)
	}
	if m.TotalAmountCents < 1_200_000_000 || m.TotalAmountCents > 1_400_000_000 {
		t.Errorf("total $%d, want ≈ $12–14M", m.TotalAmountCents/100)
	}
	if coldTotal < 300_000_000 || coldTotal > 380_000_000 {
		t.Errorf("Cold Chain total $%d, want ≈ $3.4M", coldTotal/100)
	}
	if n := countBlocked(t, "summit-payroll-5000"); n != 3 {
		t.Errorf("%d blocked beneficiaries, want 3", n)
	}
}

func TestVendorsWithErrors(t *testing.T) {
	m := stage(t, "summit-vendors-with-errors")
	if m.Status != model.StagingInvalid || m.ErrorCount != 14 || m.RowCount-m.ValidRowCount != 14 {
		t.Fatalf("manifest: %+v", m)
	}
	data, _ := Generate("summit-vendors-with-errors", today)
	store := claimcheck.NewMemory()
	m, _ = staging.Stage(context.Background(), store, allActive{}, staging.Input{
		BatchID: "01JV", FiID: "lakeshore", CustomerID: "summit-logistics", Data: data, UploadedAt: today, MaxRows: 10000, PageSize: 500,
	})
	errs, _ := staging.ReadErrors(context.Background(), store, m)
	counts := map[string]int{}
	for _, e := range errs {
		counts[e.Code+"/"+e.Column]++
	}
	want := map[string]int{
		"ROUTING_CHECKSUM/beneficiary_routing": 4, "REQUIRED/beneficiary_name": 3, "AMOUNT_RANGE/amount": 2,
		"DATE_IN_PAST/process_date": 2, "ORIGINATING_ACCOUNT/originating_account": 2, "LENGTH/beneficiary_name": 1,
	}
	if fmt.Sprint(counts) != fmt.Sprint(want) {
		t.Fatalf("errors %v\nwant   %v", counts, want)
	}
}

func TestColdChainOverLimit(t *testing.T) {
	m := stage(t, "summit-cold-chain-over-limit")
	for _, g := range m.Groups {
		if g.OriginatingAccount == coldChain {
			if g.RowCount != 1200 || g.TotalAmountCents <= 1_500_000_000 {
				t.Fatalf("cold chain group %+v must exceed the $15M daily limit", g)
			}
			return
		}
	}
	t.Fatal("no cold chain group")
}

func TestTotalsAndLimitsOfOtherSamples(t *testing.T) {
	for name, want := range map[string][2]int64{
		"northline-payroll-10000": {2_500_000_000, 2_900_000_000},
		"harvest-2000":            {440_000_000, 520_000_000},
		"harvest-6000":            {850_000_000, 950_000_000},
	} {
		m := stage(t, name)
		if m.TotalAmountCents < want[0] || m.TotalAmountCents > want[1] {
			t.Errorf("%s total $%d", name, m.TotalAmountCents/100)
		}
		for _, g := range m.Groups {
			if name != "northline-payroll-10000" && g.TotalAmountCents >= 1_000_000_000 {
				t.Errorf("%s group %s is over Prairie's $10M daily limit", name, g.GroupKey)
			}
		}
	}
}

func countBlocked(t *testing.T, name string) int {
	data, _ := Generate(name, today)
	recs, _ := csv.NewReader(bytes.NewReader(data)).ReadAll()
	n := 0
	for _, r := range recs[1:] {
		for _, b := range BlockedBeneficiaries {
			if r[4] == b {
				n++
			}
		}
	}
	return n
}
