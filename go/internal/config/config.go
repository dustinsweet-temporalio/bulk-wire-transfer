// Package config parses the environment shared by every Go binary (worker, bulkwire-service, codec-server,
// control-service). Values come from docker-compose.yml / .env.
//
// Temporal connection settings (address, namespace, API key, TLS) are NOT parsed here: every client loads
// them with go.temporal.io/sdk/contrib/envconfig, so the local dev server and Temporal Cloud differ only by
// configuration.
package config

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Temporal holds the non-connection Temporal settings.
type Temporal struct {
	Namespace         string
	TaskQueue         string
	UIURL             string
	DeploymentName    string
	BuildID           string
	VersioningEnabled bool
}

// ClaimCheck selects and configures the claim-check backend.
type ClaimCheck struct {
	Backend     string // redis | s3 | gcs | memory
	TTL         time.Duration
	PageSize    int
	RedisAddr   string
	S3Endpoint  string
	S3Bucket    string
	S3AccessKey string
	S3SecretKey string
	GCSEndpoint string
	GCSBucket   string
}

// Defaults are the per-batch settings copied into a workflow's input when it starts.
type Defaults struct {
	ChunkSize        int
	Concurrency      int
	MaxRows          int
	ApprovalWindow   time.Duration
	RetryBudget      time.Duration
	RetryMaxInterval time.Duration
}

// Core is how Go services reach each FI's own Core.
type Core struct {
	Endpoints             map[string]string // fiId -> base URL (one Core per FI)
	InternalToken         string
	AlertAttemptThreshold int
}

// Config is the union of every setting; each binary uses the parts it needs and calls the matching
// Require* method to fail fast on anything missing.
type Config struct {
	Temporal       Temporal
	ClaimCheck     ClaimCheck
	Defaults       Defaults
	Core           Core
	KeyringPath    string
	CORSOrigins    []string
	AuthSigningKey string
	DemoPassword   string
	BulkwireURL    string
}

// Load reads the environment. It reports malformed values; missing required values are checked by the
// Require* methods.
func Load() (Config, error) {
	r := &reader{}
	c := Config{
		Temporal: Temporal{
			Namespace:         r.str("TEMPORAL_NAMESPACE", "bulkwire"),
			TaskQueue:         r.str("TEMPORAL_TASK_QUEUE", "bulkwire"),
			UIURL:             r.str("TEMPORAL_UI_URL", "http://localhost:8233"),
			DeploymentName:    r.str("WORKER_DEPLOYMENT_NAME", "bulkwire"),
			BuildID:           r.str("WORKER_BUILD_ID", "v1"),
			VersioningEnabled: r.boolean("WORKER_VERSIONING_ENABLED", true),
		},
		ClaimCheck: ClaimCheck{
			Backend:     r.str("CLAIMCHECK_BACKEND", "redis"),
			TTL:         r.duration("CLAIMCHECK_TTL", 168*time.Hour),
			PageSize:    r.integer("CLAIMCHECK_PAGE_SIZE", 500),
			RedisAddr:   r.str("REDIS_ADDR", "redis:6379"),
			S3Endpoint:  r.str("S3_ENDPOINT", ""),
			S3Bucket:    r.str("S3_BUCKET", "bulkwire-claimcheck"),
			S3AccessKey: r.str("S3_ACCESS_KEY", ""),
			S3SecretKey: r.str("S3_SECRET_KEY", ""),
			GCSEndpoint: r.str("GCS_ENDPOINT", ""),
			GCSBucket:   r.str("GCS_BUCKET", "bulkwire-claimcheck"),
		},
		Defaults: Defaults{
			ChunkSize:        r.integer("DEFAULT_CHUNK_SIZE", 250),
			Concurrency:      r.integer("DEFAULT_CONCURRENCY", 2),
			MaxRows:          r.integer("MAX_ROWS", 10000),
			ApprovalWindow:   r.duration("APPROVAL_WINDOW", 24*time.Hour),
			RetryBudget:      r.duration("CORE_RETRY_BUDGET", 30*time.Minute),
			RetryMaxInterval: r.duration("CORE_RETRY_MAX_INTERVAL", 20*time.Second),
		},
		Core: Core{
			Endpoints:             r.endpoints("CORE_ENDPOINTS"),
			InternalToken:         r.str("INTERNAL_TOKEN", ""),
			AlertAttemptThreshold: r.integer("CORE_RETRY_ALERT_THRESHOLD", 5),
		},
		KeyringPath:    r.str("CODEC_KEYRING_PATH", "/config/keyring.json"),
		CORSOrigins:    r.list("CODEC_CORS_ORIGINS", "http://localhost:8233"),
		AuthSigningKey: r.str("AUTH_SIGNING_KEY", ""),
		DemoPassword:   r.str("DEMO_PASSWORD", "demo"),
		BulkwireURL:    r.str("BULKWIRE_INTERNAL_URL", "http://bulkwire-service:8090"),
	}
	return c, errors.Join(r.errs...)
}

// RequireCore checks the settings needed to call Core.
func (c Config) RequireCore() error {
	var errs []error
	if len(c.Core.Endpoints) == 0 {
		errs = append(errs, errors.New("CORE_ENDPOINTS is required (e.g. lakeshore=http://core-lakeshore:8080)"))
	}
	if c.Core.InternalToken == "" {
		errs = append(errs, errors.New("INTERNAL_TOKEN is required"))
	}
	return errors.Join(errs...)
}

// RequireAuth checks the settings needed to mint or validate portal tokens.
func (c Config) RequireAuth() error {
	if len(c.AuthSigningKey) < 32 {
		return errors.New("AUTH_SIGNING_KEY is required (32+ bytes)")
	}
	return nil
}

// FiIDs returns the configured FIs in a stable order.
func (h Core) FiIDs() []string {
	ids := make([]string, 0, len(h.Endpoints))
	for id := range h.Endpoints {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// reader accumulates parse errors so a misconfigured service reports every problem at once.
type reader struct{ errs []error }

func (r *reader) str(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func (r *reader) integer(key string, def int) int {
	v := r.str(key, "")
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		r.errs = append(r.errs, fmt.Errorf("%s: %q is not an integer", key, v))
		return def
	}
	return n
}

func (r *reader) boolean(key string, def bool) bool {
	v := r.str(key, "")
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		r.errs = append(r.errs, fmt.Errorf("%s: %q is not a boolean", key, v))
		return def
	}
	return b
}

func (r *reader) duration(key string, def time.Duration) time.Duration {
	v := r.str(key, "")
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		r.errs = append(r.errs, fmt.Errorf("%s: %q is not a duration (e.g. 30m, 24h)", key, v))
		return def
	}
	return d
}

func (r *reader) list(key, def string) []string {
	var out []string
	for _, s := range strings.Split(r.str(key, def), ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// endpoints parses "fi1=url1,fi2=url2".
func (r *reader) endpoints(key string) map[string]string {
	out := map[string]string{}
	for _, pair := range r.list(key, "") {
		fi, url, ok := strings.Cut(pair, "=")
		if !ok || strings.TrimSpace(fi) == "" || strings.TrimSpace(url) == "" {
			r.errs = append(r.errs, fmt.Errorf("%s: %q is not fiId=url", key, pair))
			continue
		}
		out[strings.TrimSpace(fi)] = strings.TrimRight(strings.TrimSpace(url), "/")
	}
	return out
}
