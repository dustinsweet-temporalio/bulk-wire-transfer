// Client dialing and startup verification.
package temporalx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/operatorservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/contrib/envconfig"
	"go.temporal.io/sdk/converter"
	sdklog "go.temporal.io/sdk/log"

	"bulkwire/internal/codec"
)

// StartupWait bounds how long a service waits for Temporal at startup before giving up.
const StartupWait = 60 * time.Second

// DialOptions customizes Dial.
type DialOptions struct {
	DataConverter  converter.DataConverter // REQUIRED for every client: the encrypting converter
	Logger         *slog.Logger
	MetricsHandler client.MetricsHandler // optional (workers)
	Identity       string                // optional
}

// Dial connects to Temporal with options from the environment (go.temporal.io/sdk/contrib/envconfig:
// TEMPORAL_ADDRESS, TEMPORAL_NAMESPACE, TEMPORAL_API_KEY, TLS settings), so the local dev server and Temporal
// Cloud differ only by configuration. It retries with backoff for up to StartupWait, because in compose the
// dev server may still be starting.
func Dial(ctx context.Context, o DialOptions) (client.Client, error) {
	if o.DataConverter == nil {
		return nil, errors.New("temporalx: a DataConverter is required (every client must encrypt payloads)")
	}
	opts, err := envconfig.LoadDefaultClientOptions()
	if err != nil {
		return nil, fmt.Errorf("temporalx: load client options: %w", err)
	}
	if opts.Namespace == "" {
		opts.Namespace = Namespace
	}
	opts.DataConverter = o.DataConverter
	opts.FailureConverter = codec.NewFailureConverter(o.DataConverter)
	if o.Logger != nil {
		opts.Logger = sdklog.NewStructuredLogger(o.Logger)
	}
	if o.MetricsHandler != nil {
		opts.MetricsHandler = o.MetricsHandler
	}
	if o.Identity != "" {
		opts.Identity = o.Identity
	}

	var c client.Client
	err = retry(ctx, StartupWait, o.Logger, "dial Temporal", func() error {
		var dialErr error
		c, dialErr = client.DialContext(ctx, opts)
		return dialErr
	})
	return c, err
}

// VerifySearchAttributes checks that every attribute in RequiredSearchAttributes exists on the namespace with
// the right type. Services exit with a clear message instead of failing later on the first upsert or list.
func VerifySearchAttributes(ctx context.Context, c client.Client, namespace string, log *slog.Logger) error {
	var resp *operatorservice.ListSearchAttributesResponse
	err := retry(ctx, StartupWait, log, "list Search Attributes", func() error {
		var listErr error
		resp, listErr = c.OperatorService().ListSearchAttributes(ctx, &operatorservice.ListSearchAttributesRequest{Namespace: namespace})
		return listErr
	})
	if err != nil {
		return err
	}
	var problems []string
	for name, want := range RequiredSearchAttributes {
		got, ok := resp.GetCustomAttributes()[name]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s (%s) is missing", name, want))
			continue
		}
		if got != indexedType(want) {
			problems = append(problems, fmt.Sprintf("%s is %s, want %s", name, got, want))
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("namespace %q is not ready: %s (run temporal-init; see scripts/temporal-init.sh)",
			namespace, strings.Join(problems, "; "))
	}
	return nil
}

func indexedType(t string) enumspb.IndexedValueType {
	if t == "Bool" {
		return enumspb.INDEXED_VALUE_TYPE_BOOL
	}
	return enumspb.INDEXED_VALUE_TYPE_KEYWORD
}

// retry runs fn with exponential backoff (500 ms doubling to 5 s) until it succeeds or `limit` passes.
func retry(ctx context.Context, limit time.Duration, log *slog.Logger, what string, fn func() error) error {
	deadline := time.Now().Add(limit)
	wait := 500 * time.Millisecond
	for {
		err := fn()
		if err == nil {
			return nil
		}
		if time.Now().Add(wait).After(deadline) {
			return fmt.Errorf("temporalx: %s: gave up after %s: %w", what, limit, err)
		}
		if log != nil {
			log.Warn("temporalx: retrying", "what", what, "in", wait, "err", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		wait = min(wait*2, 5*time.Second)
	}
}
