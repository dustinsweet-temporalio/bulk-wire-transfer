// Package claimcheck is the immutable, read-only-after-staging store for parsed rows and the staging
// manifest.
//
// PATTERN: Claim Check — https://docs.temporal.io/troubleshooting/blob-size-limit-error
// A 5,000-row batch is ~3.8 MB parsed (7.6 MB at 10,000): above Temporal's 2 MB per-payload limit and the
// Query result limit. Rows therefore live here, outside Temporal; Activities receive references
// (batch ID + offset/length) and read only the pages they need.
//
// Hard rules:
//   - Used ONLY by bulkwire-service (writes at staging; reads for the pre-draft review screen and the error
//     report) and worker Activities (PlanChunks reads the manifest; InsertDraftChunk reads row pages).
//   - NEVER used for workflow state, progress, DLQs, retry queues, locks, pub/sub, caching, or UI
//     projections. Live progress comes only from the workflow Query.
//   - Objects are immutable: there is no Update and no Delete in this interface. Expiry is the backend's
//     job (Redis TTL, S3 lifecycle rule, GCS Object Lifecycle Management). The only deletion in the codebase
//     is the DEMO-ONLY "expire staged data" action in internal/control (catalog F17).
//   - After a batch is drafted, its rows are in Core and the claim check is no longer needed. Retention must
//     exceed the longest time between staging and draft completion (default 7 days).
package claimcheck

import (
	"context"
	"errors"
	"fmt"
	"time"

	"bulkwire/internal/config"
)

// Store is an immutable object store for staged batch data.
type Store interface {
	// Put writes an object. Writing the same key twice with identical content is allowed (idempotent retries).
	Put(ctx context.Context, key string, data []byte, contentType string) error
	// Get returns ErrNotFound if the object does not exist (expired, never staged, wrong key).
	Get(ctx context.Context, key string) ([]byte, error)
	// Ping verifies connectivity at startup (fail fast).
	Ping(ctx context.Context) error
	Name() string // "redis" | "s3" | "gcs" | "memory"
}

// ErrNotFound means the object does not exist. Activities treat it as NON-retryable (the staged data is gone;
// a human must decide). Every other error is treated as retryable (the store is temporarily unavailable).
var ErrNotFound = errors.New("claimcheck: object not found")

// New selects the backend from CLAIMCHECK_BACKEND and pings it.
func New(ctx context.Context, cfg config.ClaimCheck) (Store, error) {
	var (
		s   Store
		err error
	)
	switch cfg.Backend {
	case "redis":
		s = NewRedis(cfg.RedisAddr, cfg.TTL)
	case "s3":
		s, err = NewS3(ctx, cfg.S3Endpoint, cfg.S3Bucket, cfg.S3AccessKey, cfg.S3SecretKey)
	case "gcs":
		s, err = NewGCS(ctx, cfg.GCSEndpoint, cfg.GCSBucket)
	case "memory":
		s = NewMemory()
	default:
		return nil, fmt.Errorf("claimcheck: unknown CLAIMCHECK_BACKEND %q (redis | s3 | gcs)", cfg.Backend)
	}
	if err != nil {
		return nil, err
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := s.Ping(pingCtx); err != nil {
		return nil, fmt.Errorf("claimcheck: %s backend unreachable: %w", s.Name(), err)
	}
	return s, nil
}
