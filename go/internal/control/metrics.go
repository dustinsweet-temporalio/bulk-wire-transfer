// DEMO-ONLY: scrapes both workers' Prometheus endpoints for the Control Center's alert panel.
// PATTERN: Retry Alerting via Metrics — https://docs.temporal.io/design-patterns/retry-metrics
// bulkwire_core_attempts_over_threshold{activity,fi} rises while an FI's Core keeps failing, long before any retry
// budget runs out; in production an alert fires on its rate.
package control

import (
	"bufio"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"bulkwire/internal/activities"
)

// sdkMetrics are the SDK counters summed per worker for context next to the alert counter.
var sdkMetrics = []string{
	"temporal_activity_execution_failed",
	"temporal_workflow_task_execution_failed",
	"temporal_activity_execution_latency_seconds_count",
}

var labelPattern = regexp.MustCompile(`(\w+)="([^"]*)"`)

type alertSample struct {
	Worker   string  `json:"worker"`
	Activity string  `json:"activity"`
	FiID     string  `json:"fi"`
	Value    float64 `json:"value"`
}

func (s *Service) metrics(w http.ResponseWriter, r *http.Request) {
	alerts := []alertSample{}
	sdk := map[string]map[string]float64{}
	errs := map[string]string{}
	names := make([]string, 0, len(s.WorkerMetrics))
	for n := range s.WorkerMetrics {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		ctx, cancel := timeout(r.Context(), 3*time.Second)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.WorkerMetrics[name], nil)
		resp, err := s.HTTP.Do(req)
		if err != nil {
			errs[name] = err.Error()
			cancel()
			continue
		}
		a, totals := parseMetrics(name, resp.Body)
		resp.Body.Close()
		cancel()
		alerts = append(alerts, a...)
		sdk[name] = totals
	}
	writeJSON(w, http.StatusOK, map[string]any{"alerts": alerts, "sdk": sdk, "errors": errs, "at": time.Now().UTC()})
}

// parseMetrics reads Prometheus text format: the alert counter by labels, and SDK counters summed.
func parseMetrics(worker string, body io.Reader) ([]alertSample, map[string]float64) {
	var alerts []alertSample
	totals := map[string]float64{}
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, rest, _ := strings.Cut(line, "{")
		labels, valueStr := "", ""
		if rest != "" {
			labels, valueStr, _ = strings.Cut(rest, "}")
		} else {
			name, valueStr, _ = strings.Cut(line, " ")
		}
		value, err := strconv.ParseFloat(strings.TrimSpace(strings.Fields(valueStr + " 0")[0]), 64)
		if err != nil {
			continue
		}
		base := strings.TrimSuffix(name, "_total")
		if base == activities.MetricCoreAttemptsOverThreshold {
			a := alertSample{Worker: worker, Value: value}
			for _, m := range labelPattern.FindAllStringSubmatch(labels, -1) {
				switch m[1] {
				case "activity":
					a.Activity = m[2]
				case "fi":
					a.FiID = m[2]
				}
			}
			alerts = append(alerts, a)
			continue
		}
		for _, n := range sdkMetrics {
			if base == n {
				totals[n] += value
			}
		}
	}
	return alerts, totals
}
