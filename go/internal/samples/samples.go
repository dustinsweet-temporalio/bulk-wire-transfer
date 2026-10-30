// Package samples generates the demo CSV files deterministically: a fixed seed per sample, so the same
// date always yields byte-identical files, and process dates relative to "today" (today+1, today+2), so
// samples never go stale. bulkwire-service serves them fresh; `make samples` writes them to samples/.
//
// All names, banks, and accounts are fictional. Routing numbers satisfy the ABA checksum.
package samples

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"
)

// Info describes one sample for the portal's "Sample files" list.
type Info struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	FiID        string `json:"fiId"`
	CustomerID  string `json:"customerId"`
	Rows        int    `json:"rows"`
}

// Blocked beneficiary names on every FI's demo screening list (sql/04, sql/05).
var BlockedBeneficiaries = []string{"Northwind Shell Trading", "Blue Harbor Exports Ltd", "Granite Peak Holdings SA"}

const (
	freight   = "4401220017" // Summit Freight Operating, $25M/day
	coldChain = "4401220033" // Summit Cold Chain Operating, $15M/day
	payroll   = "4401220058" // Summit Logistics Payroll, $25M/day
	northOps  = "4409330014" // Northline Operating, $40M/day
	northPay  = "4409330022" // Northline Payroll, $40M/day
	harvOps   = "7702140011" // Harvest Operating, $10M/day
	harvPay   = "7702140029" // Harvest Payroll, $10M/day
	// UnknownAccount is not an account at Lakeshore (ORIGINATING_ACCOUNT errors in the vendors sample).
	UnknownAccount = "4401229999"
)

type group struct {
	account  string
	day      int // process date = today + day
	rows     int
	min, max int64 // cents, inclusive
}

type kind int

const (
	payrollFile kind = iota
	vendorFile
)

type spec struct {
	info    Info
	seed    uint64
	kind    kind
	groups  []group
	blocked int  // rows paying a blocked beneficiary (per-wire business rejections)
	errors  bool // inject the vendors sample's 14 invalid rows
}

const (
	payMin, payMax = 50_000, 480_000 // $500.00–$4,800.00
)

var specs = []spec{
	{
		info: Info{Name: "summit-payroll-5000", FiID: "lakeshore", CustomerID: "summit-logistics", Rows: 5000,
			Description: "Summit payroll: 5,000 wires in 6 groups (one of 251 rows → chunks of 250 + 1), ≈ $13M, 3 blocked beneficiaries"},
		seed: 5000, kind: payrollFile, blocked: 3,
		groups: []group{
			{freight, 1, 1200, payMin, payMax}, {freight, 2, 1200, payMin, payMax},
			{coldChain, 1, 1049, payMin, payMax}, {coldChain, 2, 251, payMin, payMax},
			{payroll, 1, 800, payMin, payMax}, {payroll, 2, 500, payMin, payMax},
		},
	},
	{
		info: Info{Name: "summit-payroll-10000", FiID: "lakeshore", CustomerID: "summit-logistics", Rows: 10000,
			Description: "Summit payroll at 10,000 wires: the 5,000-row shape doubled, ≈ $26M, 6 blocked beneficiaries"},
		seed: 10000, kind: payrollFile, blocked: 6,
		groups: []group{
			{freight, 1, 2400, payMin, payMax}, {freight, 2, 2400, payMin, payMax},
			{coldChain, 1, 2098, payMin, payMax}, {coldChain, 2, 502, payMin, payMax},
			{payroll, 1, 1600, payMin, payMax}, {payroll, 2, 1000, payMin, payMax},
		},
	},
	{
		info: Info{Name: "summit-vendors-with-errors", FiID: "lakeshore", CustomerID: "summit-logistics", Rows: 1200,
			Description: "Vendor payments with 14 invalid rows (checksum, missing name, negative amount, past date, unknown account, too long)"},
		seed: 1200, kind: vendorFile, errors: true,
		groups: []group{
			{freight, 1, 400, 100_000, 2_500_000}, {coldChain, 1, 393, 100_000, 2_500_000}, {payroll, 2, 393, 100_000, 2_500_000},
		},
	},
	{
		info: Info{Name: "summit-cold-chain-over-limit", FiID: "lakeshore", CustomerID: "summit-logistics", Rows: 1500,
			Description: "1,200 Cold Chain wires ≈ $16.5M on one day (> $15M daily limit → group denied); 300 other wires succeed"},
		seed: 1500, kind: vendorFile,
		groups: []group{
			{freight, 1, 150, payMin, payMax}, {coldChain, 1, 1200, 900_000, 1_850_000}, {payroll, 1, 150, payMin, payMax},
		},
	},
	{
		info: Info{Name: "northline-payroll-10000", FiID: "lakeshore", CustomerID: "northline-medical", Rows: 10000,
			Description: "Northline Medical payroll: 10,000 wires in 4 groups, ≈ $27M (payroll-day demo, second Lakeshore customer)"},
		seed: 10001, kind: payrollFile,
		groups: []group{
			{northOps, 1, 3000, 60_000, 480_000}, {northOps, 2, 2000, 60_000, 480_000},
			{northPay, 1, 2800, 60_000, 480_000}, {northPay, 2, 2200, 60_000, 480_000},
		},
	},
	{
		info: Info{Name: "northline-bonus-500", FiID: "lakeshore", CustomerID: "northline-medical", Rows: 500,
			Description: "Northline Medical bonus run: 500 wires in one group, ≈ $1.4M (the small batch in the Fairness comparison)"},
		seed: 500, kind: payrollFile,
		groups: []group{
			{northPay, 1, 500, 60_000, 480_000},
		},
	},
	{
		info: Info{Name: "harvest-2000", FiID: "prairie", CustomerID: "harvest-coop", Rows: 2000,
			Description: "Harvest Growers Co-op: 2,000 wires, ≈ $4.8M (second tenant; dual control applies)"},
		seed: 2000, kind: payrollFile,
		groups: []group{
			{harvOps, 1, 600, 50_000, 430_000}, {harvOps, 2, 400, 50_000, 430_000},
			{harvPay, 1, 500, 50_000, 430_000}, {harvPay, 2, 500, 50_000, 430_000},
		},
	},
	{
		info: Info{Name: "harvest-6000", FiID: "prairie", CustomerID: "harvest-coop", Rows: 6000,
			Description: "Harvest Growers Co-op: 6,000 wires, ≈ $9M across two accounts and two dates (payroll-day demo)"},
		seed: 6000, kind: payrollFile,
		groups: []group{
			{harvOps, 1, 1500, 40_000, 260_000}, {harvOps, 2, 1500, 40_000, 260_000},
			{harvPay, 1, 1500, 40_000, 260_000}, {harvPay, 2, 1500, 40_000, 260_000},
		},
	},
}

// Catalog lists every sample in a stable order.
func Catalog() []Info {
	out := make([]Info, len(specs))
	for i, s := range specs {
		out[i] = s.info
	}
	return out
}

// Generate renders a sample as CSV with process dates relative to `today` (pass the FI's local date, as from
// auth.LocalToday; only its calendar date is used).
func Generate(name string, today time.Time) ([]byte, error) {
	for _, s := range specs {
		if s.info.Name == name {
			return s.render(today.UTC().Truncate(24 * time.Hour))
		}
	}
	return nil, fmt.Errorf("samples: unknown sample %q", name)
}

func (s spec) render(today time.Time) ([]byte, error) {
	rng := rand.New(rand.NewPCG(s.seed, s.seed^0x9E3779B97F4A7C15))
	var rows [][]string
	for _, g := range s.groups {
		date := today.AddDate(0, 0, g.day).Format(time.DateOnly)
		for i := 0; i < g.rows; i++ {
			rows = append(rows, s.row(rng, g.account, date, g.min+rng.Int64N(g.max-g.min+1)))
		}
	}
	// Spread the special rows deterministically, then shuffle so the file is not pre-sorted by group
	// (staging computes canonical order itself).
	for _, i := range rng.Perm(len(rows))[:s.blocked] {
		rows[i][4] = BlockedBeneficiaries[i%len(BlockedBeneficiaries)]
	}
	if s.errors {
		rows = append(rows, invalidRows(rng, today)...)
	}
	rng.Shuffle(len(rows), func(i, j int) { rows[i], rows[j] = rows[j], rows[i] })

	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"originating_account", "process_date", "amount", "currency", "beneficiary_name",
		"beneficiary_account", "beneficiary_routing", "beneficiary_bank", "reference", "subsidiary"})
	if err := w.WriteAll(rows); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (s spec) row(rng *rand.Rand, account, date string, cents int64) []string {
	bank := banks[rng.IntN(len(banks))]
	var name, ref, sub string
	if s.kind == payrollFile {
		name = firstNames[rng.IntN(len(firstNames))] + " " + lastNames[rng.IntN(len(lastNames))]
		ref = fmt.Sprintf("PAYROLL %s EMP %05d", date, rng.IntN(100000))
		sub = subsidiaries[s.info.CustomerID][rng.IntN(len(subsidiaries[s.info.CustomerID]))]
	} else {
		name = vendorNames[rng.IntN(len(vendorNames))]
		ref = fmt.Sprintf("INV-%05d", rng.IntN(100000))
	}
	return []string{account, date, formatCents(cents), "USD", name, beneficiaryAccount(rng), bank.routing, bank.name, ref, sub}
}

// invalidRows builds exactly the vendors sample's 14 invalid rows; each row breaks exactly ONE rule.
func invalidRows(rng *rand.Rand, today time.Time) [][]string {
	date := today.AddDate(0, 0, 1).Format(time.DateOnly)
	past := today.AddDate(0, 0, -3).Format(time.DateOnly)
	base := func() []string {
		b := banks[rng.IntN(len(banks))]
		return []string{freight, date, formatCents(100_000 + rng.Int64N(900_000)), "USD",
			vendorNames[rng.IntN(len(vendorNames))], beneficiaryAccount(rng), b.routing, b.name, fmt.Sprintf("INV-%05d", rng.IntN(100000)), ""}
	}
	var out [][]string
	for i := 0; i < 4; i++ { // ROUTING_CHECKSUM: right length, wrong check digit
		r := base()
		r[6] = breakChecksum(r[6])
		out = append(out, r)
	}
	for i := 0; i < 3; i++ { // REQUIRED beneficiary_name
		r := base()
		r[4] = ""
		out = append(out, r)
	}
	for i := 0; i < 2; i++ { // AMOUNT_RANGE (negative)
		r := base()
		r[2] = "-" + r[2]
		out = append(out, r)
	}
	for i := 0; i < 2; i++ { // DATE_IN_PAST
		r := base()
		r[1] = past
		out = append(out, r)
	}
	for i := 0; i < 2; i++ { // ORIGINATING_ACCOUNT (unknown to Core)
		r := base()
		r[0] = UnknownAccount
		out = append(out, r)
	}
	r := base() // LENGTH: beneficiary name over 70 characters
	r[4] = "Consolidated Refrigerated Transport and Cold Storage Services of the Great Lakes"
	return append(out, r)
}

func beneficiaryAccount(rng *rand.Rand) string {
	n := 8 + rng.IntN(5) // 8–12 digits
	var b strings.Builder
	b.WriteByte(byte('1' + rng.IntN(9)))
	for i := 1; i < n; i++ {
		b.WriteByte(byte('0' + rng.IntN(10)))
	}
	return b.String()
}

func formatCents(c int64) string { return fmt.Sprintf("%d.%02d", c/100, c%100) }

// routing builds a 9-digit routing number from an 8-digit prefix by computing the ABA check digit.
func routing(prefix8 string) string {
	d := func(i int) int { return int(prefix8[i] - '0') }
	partial := 3*(d(0)+d(3)+d(6)) + 7*(d(1)+d(4)+d(7)) + (d(2) + d(5))
	return fmt.Sprintf("%s%d", prefix8, (10-partial%10)%10)
}

func breakChecksum(r string) string {
	last := int(r[8]-'0'+1) % 10
	return r[:8] + fmt.Sprint(last)
}

type bank struct{ name, routing string }

var banks = []bank{
	{"First Plains National Bank", routing("07120041")},
	{"Harborview Savings Bank", routing("02110378")},
	{"Cedar Ridge State Bank", routing("10190552")},
	{"Great Lakes Farmers Bank", routing("07200619")},
	{"Silver Mesa Federal Credit Union", routing("32227018")},
	{"Bayside Commerce Bank", routing("12100248")},
	{"Iron Range Community Bank", routing("09130114")},
	{"Tidewater Trust Company", routing("05140028")},
}

var firstNames = []string{"Avery", "Blake", "Camila", "Dario", "Elena", "Farah", "Gabriel", "Hana", "Isaac", "Jalen",
	"Keiko", "Luis", "Maya", "Nikhil", "Olivia", "Priya", "Quentin", "Rosa", "Samir", "Tessa", "Umar", "Vera",
	"Wesley", "Ximena", "Yusuf", "Zoe", "Andre", "Bianca", "Connor", "Dana"}

var lastNames = []string{"Abernathy", "Baptiste", "Castellano", "Delacroix", "Eriksen", "Fairbanks", "Galloway",
	"Hollister", "Iwasaki", "Jablonski", "Kowalczyk", "Lindqvist", "Marchetti", "Nakamura", "Okonkwo", "Pemberton",
	"Quintero", "Rasmussen", "Sorensen", "Thibodeaux", "Underwood", "Valdivia", "Whitfield", "Yamamoto", "Zielinski"}

var vendorNames = []string{"Apex Pallet Supply", "Brightline Fuel Services", "Cobalt Cold Storage", "Delta Tire & Axle",
	"Evergreen Packaging Co", "Foothill Dock Equipment", "Granite State Insurance", "Harbor Freight Brokers",
	"Ironwood Fleet Maintenance", "Juniper Logistics Software", "Keystone Refrigeration", "Lakeview Fuel Cards",
	"Meridian Safety Training", "Northstar Telematics", "Oakridge Uniform Rental", "Pinecrest Tolling Services",
	"Quarry Road Asphalt", "Riverside Crate Works", "Summitview Office Supply", "Timberline Trailer Leasing"}

var subsidiaries = map[string][]string{
	"summit-logistics":  {"Summit Freight East", "Summit Freight West", "Summit Cold Chain", ""},
	"northline-medical": {"Northline Clinics", "Northline Imaging", "Northline Home Health", ""},
	"harvest-coop":      {"Harvest Grain", "Harvest Dairy", ""},
}
