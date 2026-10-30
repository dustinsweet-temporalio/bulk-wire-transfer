// Package config parses the environment shared by every Go binary (worker, bulkwire-service, codec-server,
// control-service). Values come from docker-compose.yml / .env.
//
// Temporal connection settings (address, namespace, API key, TLS) are NOT parsed here: every client loads
// them with go.temporal.io/sdk/contrib/envconfig, so the local dev server and Temporal Cloud differ only by
// configuration.
package config

import (
	"crypto/subtle"
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
	Concurrency      int // per-FI values in config/routing.yaml (fiDefaults) take precedence
	MaxRows          int
	MaxChunks        int // maxChunksPerBatch: the history guard (see MaxChunksPerBatchDefault)
	ApprovalWindow   time.Duration
	RetryBudget      time.Duration
	RetryMaxInterval time.Duration
}

// MaxChunksPerBatchDefault keeps one batch's workflow history far below Temporal's limits (51,200 events and
// 50 MB per history; the Service warns at 10,240 events). Chunk size has no artificial floor, so a small chunk
// size on a large batch is where the event count comes from.
//
// Measured from go/testdata/histories/scenario-a-outage.json (a 23-chunk, 6-group batch, drafted and
// authorized, through a Core outage): 324 events. 57 Activities x 3 events (scheduled, started, completed)
// = 171; 47 Workflow Tasks x 3 events = 141; 12 others (start, 2 Updates x 2, approval timer x 2,
// 4 Search Attribute upserts, completion). Retries add no events: only an Activity's final attempt is recorded.
//
// Upper-bound model (one Workflow Task per Activity completion, i.e. no batching of completions):
//   - each Activity  = 3 events + at most 3 Workflow Task events = 6
//   - each chunk     = 2 Activities (draft, authorize)            = 12
//   - each group     = 1 policy Activity                         = 6
//   - per batch      = 5 Activities (plan, Core batch, 3 statuses) = 30, plus the 12 others = 42
//
// Check against the measurement: 42 + 6x6 + 12x23 = 354 >= 324 (the model is an upper bound).
// Worst case for groups is one chunk per group: 42 + 18 x chunks < 25,000 -> chunks <= 1,386.
// Default 1,300: draft + authorization stay under 25,000 events (23,442 at the worst case) even with every
// chunk in its own group, leaving more than half the 51,200-event limit for park/resolve cycles.
// A typical batch (a handful of groups) at 1,300 chunks is about 15,700 events.
// (The 2,000 in-flight Activity limit is not a constraint: chunk concurrency is at most 8.)
const MaxChunksPerBatchDefault = 1300

// Core is how Go services reach each FI's own Core.
type Core struct {
	Endpoints             map[string]string // fiId -> base URL (one Core per FI)
	InternalTokens        map[string]string // fiId -> that FI's service-to-service token
	AlertAttemptThreshold int
}

// TokenFor returns the internal token for calls to (or on behalf of) one FI.
func (h Core) TokenFor(fiID string) string { return h.InternalTokens[fiID] }

// FIForToken returns the FI whose internal token this is. WHY per FI: each Core holds only its own FI's token,
// so a callback from one FI's Core can never act on another FI's batches.
func (h Core) FIForToken(token string) (string, bool) {
	if token == "" {
		return "", false
	}
	for fi, t := range h.InternalTokens {
		if subtle.ConstantTimeCompare([]byte(token), []byte(t)) == 1 {
			return fi, true
		}
	}
	return "", false
}

// Config is the union of every setting; each binary uses the parts it needs and calls the matching
// Require* method to fail fast on anything missing.
type Config struct {
	Temporal       Temporal
	ClaimCheck     ClaimCheck
	Defaults       Defaults
	Core           Core
	RoutingPath    string // config/routing.yaml: Core task queue per FI (internal/routing)
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
			Concurrency:      r.integer("DEFAULT_CONCURRENCY", 4),
			MaxRows:          r.integer("MAX_ROWS", 10000),
			MaxChunks:        r.integer("MAX_CHUNKS_PER_BATCH", MaxChunksPerBatchDefault),
			ApprovalWindow:   r.duration("APPROVAL_WINDOW", 24*time.Hour),
			RetryBudget:      r.duration("CORE_RETRY_BUDGET", 30*time.Minute),
			RetryMaxInterval: r.duration("CORE_RETRY_MAX_INTERVAL", 20*time.Second),
		},
		Core: Core{
			Endpoints:             r.endpoints("CORE_ENDPOINTS"),
			InternalTokens:        r.pairs("INTERNAL_TOKENS", "fiId=token"),
			AlertAttemptThreshold: r.integer("CORE_RETRY_ALERT_THRESHOLD", 5),
		},
		RoutingPath:    r.str("ROUTING_CONFIG_PATH", "/config/routing.yaml"),
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
	seen := map[string]string{}
	for _, fi := range c.Core.FiIDs() {
		t := c.Core.InternalTokens[fi]
		switch {
		case t == "":
			errs = append(errs, fmt.Errorf("INTERNAL_TOKENS has no token for FI %q", fi))
		case seen[t] != "":
			errs = append(errs, fmt.Errorf("INTERNAL_TOKENS: FIs %q and %q share a token; each FI needs its own", seen[t], fi))
		default:
			seen[t] = fi
		}
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
	out := r.pairs(key, "fiId=url")
	for fi, url := range out {
		out[fi] = strings.TrimRight(url, "/")
	}
	return out
}

// pairs parses "fiId=value,fiId=value".
func (r *reader) pairs(key, form string) map[string]string {
	out := map[string]string{}
	for _, pair := range r.list(key, "") {
		fi, v, ok := strings.Cut(pair, "=")
		if !ok || strings.TrimSpace(fi) == "" || strings.TrimSpace(v) == "" {
			r.errs = append(r.errs, fmt.Errorf("%s: an entry is not %s", key, form)) // never echo a token
			continue
		}
		out[strings.TrimSpace(fi)] = strings.TrimSpace(v)
	}
	return out
}
