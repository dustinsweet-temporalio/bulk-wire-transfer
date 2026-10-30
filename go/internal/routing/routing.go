// Package routing reads config/routing.yaml: which task queue carries each FI's Core-calling Activities, that
// queue's limits, and each FI's chunk concurrency.
//
// PATTERN: Task Queue per Downstream — https://docs.temporal.io/task-queue
// Each FI's Core is a separate downstream system with its own capacity, so its Activities get their own task
// queue with its own server-side rate limit and worker capacity. A slow or stopped Core (or a stopped set of
// Core workers) then backs up only that FI's queue. The mapping from FI to queue is configuration, never derived
// from the FI ID in code, so several FIs whose Cores share a database cluster can share one queue.
//
// bulkwire-service resolves a batch's Route once, when its workflow starts, and passes it in the workflow
// input; workflow code never reads this file.
package routing

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"

	"go.yaml.in/yaml/v3"
)

// Queue is one Core Activity task queue.
type Queue struct {
	Name                    string   `yaml:"-"`
	FIs                     []string `yaml:"fis"`
	ActivitiesPerSecond     float64  `yaml:"activitiesPerSecond"`     // worker option TaskQueueActivitiesPerSecond (server-enforced)
	MaxConcurrentActivities int      `yaml:"maxConcurrentActivities"` // worker option MaxConcurrentActivityExecutionSize (per worker)
}

// Shared reports whether several FIs share this queue.
func (q Queue) Shared() bool { return len(q.FIs) > 1 }

// FIDefaults are an FI's per-batch settings that live with its routing.
type FIDefaults struct {
	ChunkConcurrency int `yaml:"chunkConcurrency"`
}

// Config is the parsed routing file.
type Config struct {
	CoreQueues map[string]Queue      `yaml:"coreQueues"`
	FIDefaults map[string]FIDefaults `yaml:"fiDefaults"`
}

// Route is what a batch's workflow needs to schedule its Core Activities. It is resolved once, at start.
type Route struct {
	CoreTaskQueue   string
	CoreFairnessKey string
}

// Load reads and parses the file. Call Validate before using it.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("routing: %w", err)
	}
	return Parse(data)
}

// Parse parses routing YAML.
func Parse(data []byte) (Config, error) {
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("routing: %w", err)
	}
	for name, q := range c.CoreQueues {
		q.Name = name
		c.CoreQueues[name] = q
	}
	return c, nil
}

// Validate fails fast on a routing file that cannot work with the configured FIs (fiIDs = CORE_ENDPOINTS): every
// FI in exactly one queue, every queue serving at least one known FI, positive limits.
func (c Config) Validate(fiIDs []string) error {
	var errs []error
	if len(c.CoreQueues) == 0 {
		errs = append(errs, errors.New("routing: coreQueues is empty; every FI needs a Core queue"))
	}
	queueOf := map[string]string{}
	for _, q := range c.Queues() {
		if len(q.FIs) == 0 {
			errs = append(errs, fmt.Errorf("routing: queue %q serves no FIs", q.Name))
		}
		if q.ActivitiesPerSecond <= 0 {
			errs = append(errs, fmt.Errorf("routing: queue %q: activitiesPerSecond must be positive", q.Name))
		}
		if q.MaxConcurrentActivities <= 0 {
			errs = append(errs, fmt.Errorf("routing: queue %q: maxConcurrentActivities must be positive", q.Name))
		}
		for _, fi := range q.FIs {
			switch {
			case !slices.Contains(fiIDs, fi):
				errs = append(errs, fmt.Errorf("routing: queue %q lists FI %q, which has no Core endpoint", q.Name, fi))
			case queueOf[fi] != "":
				errs = append(errs, fmt.Errorf("routing: FI %q is in two queues (%q and %q); it must be in exactly one", fi, queueOf[fi], q.Name))
			default:
				queueOf[fi] = q.Name
			}
		}
	}
	for _, fi := range fiIDs {
		if queueOf[fi] == "" {
			errs = append(errs, fmt.Errorf("routing: FI %q is in no Core queue; it must be in exactly one", fi))
		}
	}
	for fi, d := range c.FIDefaults {
		if !slices.Contains(fiIDs, fi) {
			errs = append(errs, fmt.Errorf("routing: fiDefaults lists unknown FI %q", fi))
		}
		if d.ChunkConcurrency < 1 || d.ChunkConcurrency > 8 {
			errs = append(errs, fmt.Errorf("routing: fiDefaults.%s.chunkConcurrency must be 1..8", fi))
		}
	}
	return errors.Join(errs...)
}

// Queues returns the Core queues sorted by name.
func (c Config) Queues() []Queue {
	out := make([]Queue, 0, len(c.CoreQueues))
	for _, q := range c.CoreQueues {
		out = append(out, q)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// QueueFor returns the Core queue that serves an FI.
func (c Config) QueueFor(fiID string) (Queue, bool) {
	for _, q := range c.Queues() {
		if slices.Contains(q.FIs, fiID) {
			return q, true
		}
	}
	return Queue{}, false
}

// Resolve returns a batch's Core queue and fairness key.
//
// PATTERN: Fairness — https://docs.temporal.io/develop/task-queue-priority-fairness
// On a queue that serves ONE FI, the fairness key is the batch ID: when the queue backs up (its rate limit is
// the bottleneck), the Temporal Service dispatches round-robin across that FI's batches, so a 500-wire batch is
// not stuck behind a 10,000-wire one. On a queue SHARED by several FIs, the key is the FI ID, so one FI's
// batches cannot crowd out another FI on the cluster they share.
func (c Config) Resolve(fiID, batchID string) (Route, error) {
	q, ok := c.QueueFor(fiID)
	if !ok {
		return Route{}, fmt.Errorf("routing: no Core queue for FI %q", fiID)
	}
	key := batchID
	if q.Shared() {
		key = fiID
	}
	return Route{CoreTaskQueue: q.Name, CoreFairnessKey: key}, nil
}

// ChunkConcurrency is the FI's configured chunk concurrency, or def when the FI has none.
func (c Config) ChunkConcurrency(fiID string, def int) int {
	if d, ok := c.FIDefaults[fiID]; ok && d.ChunkConcurrency > 0 {
		return d.ChunkConcurrency
	}
	return def
}

// Subset returns the entries of an fiId-keyed map (Core endpoints, internal tokens) for the given FIs only.
// WHY: a Core-queue worker gets credentials for exactly the FIs its queue serves, and no others.
func Subset(byFI map[string]string, fiIDs []string) map[string]string {
	out := map[string]string{}
	for _, fi := range fiIDs {
		if v, ok := byFI[fi]; ok {
			out[fi] = v
		}
	}
	return out
}
