// Tests for Core error classification (transient vs structural vs business outcome) and per-FI routing to each FI's own Core.

package coreclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.temporal.io/sdk/temporal"

	"bulkwire/internal/model"
)

// TestClassificationTable pins the classification table: which Core answers retry and which park.
func TestClassificationTable(t *testing.T) {
	cases := []struct {
		status    int
		code      string
		kind      Kind
		errType   string
		retryable bool
	}{
		{503, "CORE_UNAVAILABLE", Transient, model.ErrTypeCoreTransient, true},
		{504, "SQL_TIMEOUT", Transient, model.ErrTypeCoreTransient, true},
		{500, "CORE_INTERNAL", Transient, model.ErrTypeCoreTransient, true},
		{429, "", Transient, model.ErrTypeCoreTransient, true},
		{422, "ACCOUNT_ON_HOLD", Structural, model.ErrTypeCoreStructural, false},
		{422, "ACCOUNT_CLOSED", Structural, model.ErrTypeCoreStructural, false},
		{422, "ACCOUNT_NOT_FOUND", Structural, model.ErrTypeCoreStructural, false},
		{409, "GENTX_BATCH_CLOSED", Structural, model.ErrTypeCoreStructural, false},
		{409, "INVALID_TRANSITION", Structural, model.ErrTypeCoreStructural, false},
		{404, "GENTX_BATCH_NOT_FOUND", Structural, model.ErrTypeCoreStructural, false},
		{400, "CORE_CONTRACT", Contract, model.ErrTypeCoreContract, false},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%d %s", tc.status, tc.code), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get(InternalTokenHeader) != "tok" {
					t.Error("internal token not sent")
				}
				w.WriteHeader(tc.status)
				if tc.code != "" {
					fmt.Fprintf(w, `{"code":%q,"message":"account ending 0033 is on hold","retryable":%v}`, tc.code, tc.retryable)
				}
			}))
			defer srv.Close()
			c := New(map[string]string{"lakeshore": srv.URL}, map[string]string{"lakeshore": "tok", "prairie": "tok-p", "down": "tok-d"})
			_, err := c.DraftChunk(context.Background(), "lakeshore", 100000, DraftChunkRequest{ChunkID: "g00-c00"})

			var e *Error
			if !errors.As(err, &e) || e.Kind != tc.kind || e.HTTPStatus != tc.status {
				t.Fatalf("got %#v", err)
			}
			var appErr *temporal.ApplicationError
			if !errors.As(AsActivityError(err), &appErr) {
				t.Fatal("not an ApplicationError")
			}
			if appErr.Type() != tc.errType || appErr.NonRetryable() == tc.retryable {
				t.Fatalf("type %s nonRetryable %v", appErr.Type(), appErr.NonRetryable())
			}
			var details model.CoreErrorDetails
			if err := appErr.Details(&details); err != nil || details.HTTPStatus != tc.status {
				t.Fatalf("details %+v %v", details, err)
			}
		})
	}
}

func TestNetworkErrorsAndDeadlinesAreTransient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond) // a hung Core
	}))
	defer srv.Close()
	c := New(map[string]string{"lakeshore": srv.URL, "down": "http://127.0.0.1:1"}, map[string]string{"lakeshore": "tok", "prairie": "tok-p", "down": "tok-d"})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := c.EnsureGenTxBatch(ctx, "lakeshore", EnsureGenTxBatchRequest{}); !IsTransient(err) {
		t.Fatalf("deadline: %v", err)
	}
	if _, err := c.EnsureGenTxBatch(context.Background(), "down", EnsureGenTxBatchRequest{}); !IsTransient(err) {
		t.Fatalf("connection refused: %v", err)
	}
}

func TestUnknownFIIsAContractError(t *testing.T) {
	_, err := New(map[string]string{}, map[string]string{"lakeshore": "tok", "prairie": "tok-p", "down": "tok-d"}).EnsureGenTxBatch(context.Background(), "nowhere", EnsureGenTxBatchRequest{})
	var appErr *temporal.ApplicationError
	if !errors.As(AsActivityError(err), &appErr) || appErr.Type() != model.ErrTypeCoreContract || !appErr.NonRetryable() {
		t.Fatalf("got %v", err)
	}
}

func TestBusinessOutcomesAreResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"decision":"DENY","code":"DAILY_LIMIT_EXCEEDED","limitCents":1500000000,"usedCents":0,"requestedCents":1650000000}`)
	}))
	defer srv.Close()
	got, err := New(map[string]string{"lakeshore": srv.URL}, map[string]string{"lakeshore": "tok", "prairie": "tok-p", "down": "tok-d"}).CheckGroupPolicy(context.Background(), "lakeshore", 1, PolicyCheckRequest{})
	if err != nil || got.Decision != "DENY" || got.Code != "DAILY_LIMIT_EXCEEDED" {
		t.Fatalf("%+v %v", got, err)
	}
}

// Each FI's calls go to that FI's Core, carrying that FI's token (and never another FI's).
func TestEachFIGoesToItsOwnCore(t *testing.T) {
	hits := map[string]int{}
	tokens := map[string]string{}
	mk := func(fi string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits[fi]++
			tokens[fi] = r.Header.Get(InternalTokenHeader)
			fmt.Fprint(w, `{"genTxBatchId":1,"created":true,"status":"Drafting"}`)
		}))
	}
	l, p := mk("lakeshore"), mk("prairie")
	defer l.Close()
	defer p.Close()
	c := New(map[string]string{"lakeshore": l.URL, "prairie": p.URL}, map[string]string{"lakeshore": "tok", "prairie": "tok-p", "down": "tok-d"})
	_, _ = c.EnsureGenTxBatch(context.Background(), "prairie", EnsureGenTxBatchRequest{})
	if hits["prairie"] != 1 || hits["lakeshore"] != 0 {
		t.Fatalf("hits %v", hits)
	}
	if tokens["prairie"] != "tok-p" {
		t.Fatalf("prairie's Core got token %q, want prairie's own", tokens["prairie"])
	}
}

// Failure messages are stored unencrypted, so account-shaped numbers in Core's messages are masked on the way in.
func TestCoreMessagesAreRedacted(t *testing.T) {
	e := classify(http.StatusUnprocessableEntity, []byte(`{"code":"ACCOUNT_ON_HOLD","message":"account 4401220033 is on hold; routing 021000021"}`))
	if e.Message != "account ••0033 is on hold; routing ••0021" {
		t.Fatalf("message not redacted: %q", e.Message)
	}
	if got := classify(http.StatusConflict, []byte(`{"code":"GENTX_BATCH_CLOSED","message":"batch 100014 is closed"}`)).Message; got != "batch 100014 is closed" {
		t.Fatalf("short numbers must stay readable: %q", got)
	}
}
