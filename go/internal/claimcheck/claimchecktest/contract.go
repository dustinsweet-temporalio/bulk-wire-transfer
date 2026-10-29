// Package claimchecktest holds the shared contract test every claim-check backend must pass. It runs against
// the memory store in unit tests and against Redis, S3 (RustFS), and GCS (fake-gcs-server) inside the
// compose network (`make test-claimcheck`).
package claimchecktest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"bulkwire/internal/claimcheck"
)

// RunStoreContract checks: put/get round trip, ErrNotFound for a missing key, idempotent Put, and a
// realistically sized page object.
func RunStoreContract(t *testing.T, s claimcheck.Store) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	prefix := fmt.Sprintf("contract-test/%s/%d/", s.Name(), time.Now().UnixNano())

	if err := s.Ping(ctx); err != nil {
		t.Fatalf("%s: ping: %v", s.Name(), err)
	}

	t.Run("round trip", func(t *testing.T) {
		data := []byte(`{"batchId":"01JTEST","rows":[1,2,3]}`)
		if err := s.Put(ctx, prefix+"manifest.json", data, "application/json"); err != nil {
			t.Fatalf("put: %v", err)
		}
		got, err := s.Get(ctx, prefix+"manifest.json")
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("get: %q, %v", got, err)
		}
	})

	t.Run("missing key is ErrNotFound", func(t *testing.T) {
		if _, err := s.Get(ctx, prefix+"does-not-exist.json"); !errors.Is(err, claimcheck.ErrNotFound) {
			t.Fatalf("want ErrNotFound, got %v", err)
		}
	})

	t.Run("idempotent put", func(t *testing.T) {
		data := []byte(`[{"rowNumber":1}]`)
		for i := 0; i < 2; i++ {
			if err := s.Put(ctx, prefix+"rows/000000.json", data, "application/json"); err != nil {
				t.Fatalf("put #%d: %v", i+1, err)
			}
		}
		got, err := s.Get(ctx, prefix+"rows/000000.json")
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("get after second put: %q, %v", got, err)
		}
	})

	t.Run("page-sized object", func(t *testing.T) {
		data := bytes.Repeat([]byte("x"), 200*1024) // ~one 500-row page
		if err := s.Put(ctx, prefix+"rows/000001.json", data, "application/json"); err != nil {
			t.Fatalf("put: %v", err)
		}
		got, err := s.Get(ctx, prefix+"rows/000001.json")
		if err != nil || len(got) != len(data) {
			t.Fatalf("get: %d bytes, %v", len(got), err)
		}
	})
}
