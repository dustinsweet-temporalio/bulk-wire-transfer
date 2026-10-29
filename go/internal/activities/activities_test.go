// Activity tests: a chunk reads only its own pages (references, not data), missing staged data parks while a store
// outage retries, a policy DENY is a result, and the retry-alert metric fires only past its threshold.

package activities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"

	"bulkwire/internal/claimcheck"
	"bulkwire/internal/coreclient"
	"bulkwire/internal/model"
	"bulkwire/internal/staging"
)

// stageRows stages n valid rows with a small page size so chunks span pages.
func stageRows(t *testing.T, store claimcheck.Store, n, pageSize int) model.Manifest {
	t.Helper()
	var b strings.Builder
	b.WriteString(strings.Join(staging.Header, ",") + "\n")
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "4401220017,2026-11-03,%d.00,USD,Payee %d,12345678,021000021,,,\n", i, i)
	}
	m, err := staging.Stage(context.Background(), store, okAccounts{}, staging.Input{
		BatchID: "01JACT", FiID: "lakeshore", CustomerID: "summit-logistics", UploadedBy: "jordan", Data: []byte(b.String()),
		UploadedAt: time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC), MaxRows: 10000, PageSize: pageSize,
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

type okAccounts struct{}

func (okAccounts) ValidateAccounts(_ context.Context, _, _ string, accts []string) (map[string]staging.AccountStatus, error) {
	out := map[string]staging.AccountStatus{}
	for _, a := range accts {
		out[a] = staging.AccountStatus{Exists: true, OwnedByCustomer: true, Status: "Active"}
	}
	return out, nil
}

// TestInsertDraftChunkReadsOnlyItsPagesAndSendsExactlyItsRows: the Activity resolves the reference into rows
// itself, reading only the overlapping claim-check pages, and sends Core exactly `length` rows in canonical order.
func TestInsertDraftChunkReadsOnlyItsPagesAndSendsExactlyItsRows(t *testing.T) {
	store := claimcheck.NewMemory()
	stageRows(t, store, 10, 4) // pages: rows [1-4] [5-8] [9-10]

	var mu sync.Mutex
	var got coreclient.DraftChunkRequest
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if !strings.HasSuffix(r.URL.Path, "/api/gentx-batches/100000/draft-chunks") {
			t.Errorf("path %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		fmt.Fprintf(w, `{"chunkDrafted":%d,"chunkRejected":0,"insertedThisCall":%d,"alreadyPresent":0,"rejected":[]}`, len(got.Wires), len(got.Wires))
	}))
	defer core.Close()

	acts := &Activities{Core: coreclient.New(map[string]string{"lakeshore": core.URL}, "tok"), ClaimCheck: store, AlertAttemptThreshold: 5}
	env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	env.RegisterActivity(acts)

	before := len(store.Reads())
	val, err := env.ExecuteActivity(acts.InsertDraftChunk, DraftChunkInput{FiID: "lakeshore", BatchID: "01JACT", GenTxBatchID: 100000,
		Chunk: model.ChunkRef{ChunkID: "g00-c01", Offset: 3, Length: 3}, Actor: Actor})
	if err != nil {
		t.Fatal(err)
	}
	var res ChunkDraftResult
	_ = val.Get(&res)
	if res.ChunkDrafted != 3 {
		t.Fatalf("result %+v", res)
	}
	var rows []int
	for _, w := range got.Wires {
		rows = append(rows, w.RowNumber)
	}
	if fmt.Sprint(rows) != "[4 5 6]" || got.ChunkID != "g00-c01" {
		t.Fatalf("sent rows %v chunk %s", rows, got.ChunkID)
	}
	reads := store.Reads()[before:]
	want := []string{staging.ManifestKey("lakeshore", "01JACT"), staging.PageKey("lakeshore", "01JACT", 0), staging.PageKey("lakeshore", "01JACT", 1)}
	if fmt.Sprint(reads) != fmt.Sprint(want) {
		t.Fatalf("reads %v\nwant  %v", reads, want)
	}
}

func TestMissingStagedDataParksAndStoreOutageRetries(t *testing.T) {
	acts := &Activities{Core: coreclient.New(nil, "tok"), ClaimCheck: claimcheck.NewMemory()}
	env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	env.RegisterActivity(acts)
	_, err := env.ExecuteActivity(acts.PlanChunks, PlanInput{FiID: "lakeshore", BatchID: "gone", ChunkSize: 250})
	var appErr *temporal.ApplicationError
	if !errors.As(err, &appErr) || appErr.Type() != model.ErrTypeClaimCheckNotFound || !appErr.NonRetryable() {
		t.Fatalf("got %v", err)
	}

	acts.ClaimCheck = brokenStore{claimcheck.NewMemory()}
	_, err = env.ExecuteActivity(acts.PlanChunks, PlanInput{FiID: "lakeshore", BatchID: "x", ChunkSize: 250})
	if !errors.As(err, &appErr) || appErr.Type() != model.ErrTypeClaimCheckUnavailable || appErr.NonRetryable() {
		t.Fatalf("got %v", err)
	}
}

type brokenStore struct{ *claimcheck.Memory }

func (brokenStore) Get(context.Context, string) ([]byte, error) {
	return nil, errors.New("connection refused")
}

func TestPlanChunksRefusesInvalidStaging(t *testing.T) {
	store := claimcheck.NewMemory()
	m := stageRows(t, store, 2, 500)
	m.Status = model.StagingInvalid
	data, _ := json.Marshal(m)
	_ = store.Put(context.Background(), staging.ManifestKey("lakeshore", "01JACT"), data, "application/json")
	acts := &Activities{ClaimCheck: store}
	env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	env.RegisterActivity(acts)
	_, err := env.ExecuteActivity(acts.PlanChunks, PlanInput{FiID: "lakeshore", BatchID: "01JACT", ChunkSize: 250})
	var appErr *temporal.ApplicationError
	if !errors.As(err, &appErr) || appErr.Type() != model.ErrTypeStagingInvalid {
		t.Fatalf("got %v", err)
	}
}

func TestPolicyDenyIsAResult(t *testing.T) {
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"decision":"DENY","code":"DAILY_LIMIT_EXCEEDED","limitCents":1500000000,"usedCents":280000000,"requestedCents":1650000000}`)
	}))
	defer core.Close()
	acts := &Activities{Core: coreclient.New(map[string]string{"lakeshore": core.URL}, "tok")}
	env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	env.RegisterActivity(acts)
	val, err := env.ExecuteActivity(acts.CheckGroupPolicy, PolicyInput{FiID: "lakeshore", GenTxBatchID: 1})
	if err != nil {
		t.Fatal(err)
	}
	var d PolicyDecision
	_ = val.Get(&d)
	if d.Decision != DecisionDeny || d.Message != "Daily limit exceeded: $16.5M requested, $15.0M limit, $2.8M already used today" {
		t.Fatalf("%+v", d)
	}
}

// capturing is a minimal client.MetricsHandler that records counter increments.
type capturing struct {
	tags   map[string]string
	counts map[string]int64
}

func (c *capturing) WithTags(t map[string]string) client.MetricsHandler {
	return &capturing{tags: t, counts: c.counts}
}
func (c *capturing) Counter(name string) client.MetricsCounter {
	return counterFunc(func(n int64) { c.counts[fmt.Sprintf("%s%v", name, c.tags)] += n })
}
func (c *capturing) Gauge(string) client.MetricsGauge { return nil }
func (c *capturing) Timer(string) client.MetricsTimer { return nil }

type counterFunc func(int64)

func (f counterFunc) Inc(n int64) { f(n) }

func TestRetryAlertMetricFiresOnlyPastThreshold(t *testing.T) {
	h := &capturing{counts: map[string]int64{}}
	for attempt := 1; attempt <= 7; attempt++ {
		recordAttempt(h, "InsertDraftChunk", "lakeshore", attempt, 5)
	}
	key := "bulkwire_core_attempts_over_threshold" + fmt.Sprint(map[string]string{"activity": "InsertDraftChunk", "fi": "lakeshore"})
	if h.counts[key] != 2 || len(h.counts) != 1 {
		t.Fatalf("counts %v", h.counts)
	}
}
