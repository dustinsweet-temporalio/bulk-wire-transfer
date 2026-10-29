// Tests for environment configuration: per-FI Core endpoints, durations, and reporting every malformed value at once.

package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadParsesEndpointsAndDurations(t *testing.T) {
	t.Setenv("CORE_ENDPOINTS", "lakeshore=http://core-lakeshore:8080/, prairie=http://core-prairie:8080")
	t.Setenv("CORE_RETRY_BUDGET", "60s")
	t.Setenv("DEFAULT_CONCURRENCY", "4")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Core.Endpoints["lakeshore"] != "http://core-lakeshore:8080" || c.Core.Endpoints["prairie"] != "http://core-prairie:8080" {
		t.Fatalf("endpoints: %v", c.Core.Endpoints)
	}
	if got := c.Core.FiIDs(); len(got) != 2 || got[0] != "lakeshore" {
		t.Fatalf("FiIDs: %v", got)
	}
	if c.Defaults.RetryBudget != time.Minute || c.Defaults.Concurrency != 4 {
		t.Fatalf("defaults: %+v", c.Defaults)
	}
}

func TestLoadReportsEveryMalformedValue(t *testing.T) {
	t.Setenv("DEFAULT_CHUNK_SIZE", "lots")
	t.Setenv("APPROVAL_WINDOW", "tomorrow")
	t.Setenv("CORE_ENDPOINTS", "lakeshore")
	_, err := Load()
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{"DEFAULT_CHUNK_SIZE", "APPROVAL_WINDOW", "CORE_ENDPOINTS"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %s: %v", want, err)
		}
	}
}
