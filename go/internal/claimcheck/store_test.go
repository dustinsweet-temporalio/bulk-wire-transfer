// Runs the claim-check contract against the in-memory store, and against a real backend when one is configured
// (make test-claimcheck runs it for Redis, S3, and GCS).

package claimcheck_test

import (
	"context"
	"os"
	"testing"

	"bulkwire/internal/claimcheck"
	"bulkwire/internal/claimcheck/claimchecktest"
	"bulkwire/internal/config"
)

func TestMemoryStoreContract(t *testing.T) {
	claimchecktest.RunStoreContract(t, claimcheck.NewMemory())
}

// TestBackendContract runs the same contract against the backend selected by CLAIMCHECK_BACKEND. It only
// runs when CLAIMCHECK_INTEGRATION=1, i.e. inside the compose network via `make test-claimcheck`.
func TestBackendContract(t *testing.T) {
	if os.Getenv("CLAIMCHECK_INTEGRATION") != "1" {
		t.Skip("set CLAIMCHECK_INTEGRATION=1 (make test-claimcheck) to run against a real backend")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	s, err := claimcheck.New(context.Background(), cfg.ClaimCheck)
	if err != nil {
		t.Fatal(err)
	}
	if s.Name() != cfg.ClaimCheck.Backend {
		t.Fatalf("backend %q, want %q", s.Name(), cfg.ClaimCheck.Backend)
	}
	claimchecktest.RunStoreContract(t, s)
}
