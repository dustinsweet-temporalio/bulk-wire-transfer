// DEMO-ONLY: one-click reset back to a clean demo from any scenario. Steps run in a fixed order, because later
// steps need the services earlier steps bring back (e.g. terminating batches needs Temporal and the workers up;
// Core data reset needs SQL Server up). Each step retries briefly while services come back, and the response is a
// step-by-step report.
package control

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"

	"bulkwire/internal/temporalx"
)

type resetStep struct {
	Name string
	Run  func(ctx context.Context, r *http.Request) (string, error)
}

type stepReport struct {
	Step   string `json:"step"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	Ms     int64  `json:"ms"`
}

// coreServices must be running for the demo; the claim-check backend depends on CLAIMCHECK_BACKEND.
func (s *Service) coreServices() []string {
	backend := map[string]string{"redis": "redis", "s3": "rustfs", "gcs": "fake-gcs"}[s.Cfg.ClaimCheck.Backend]
	return []string{"temporal", "sql-lakeshore", "sql-prairie", "core-lakeshore", "core-prairie", backend,
		"codec-server", "worker-a", "worker-b", "bulkwire-service"}
}

func (s *Service) resetSteps() []resetStep {
	return []resetStep{
		{"unpause paused containers", s.unpauseAll},
		{"start stopped essential services", s.startEssential},
		{"stop worker-v2 and worker-v3", s.stopDemoWorkers},
		{"set current version v1", func(ctx context.Context, _ *http.Request) (string, error) {
			_, err := s.Temporal.WorkerDeploymentClient().GetHandle(s.Cfg.Temporal.DeploymentName).SetCurrentVersion(ctx,
				client.WorkerDeploymentSetCurrentVersionOptions{BuildID: "v1", Identity: "control-service reset"})
			return "v1 is current", err
		}},
		{"terminate running batches", s.terminateRunning},
		{"reset Core data, faults, latency, stats", func(ctx context.Context, _ *http.Request) (string, error) {
			for _, fi := range s.Cfg.Core.FiIDs() {
				if err := s.Core.Do(ctx, fi, http.MethodPost, "/admin/reset-data", map[string]any{}, nil); err != nil {
					return "", fmt.Errorf("%s: %w", fi, err)
				}
			}
			return "transactions cleared, accounts active, faults none, latency default, stats zeroed", nil
		}},
		{"clear Core queue rate-limit overrides", s.clearRateLimits},
		{"reset draft defaults", func(_ context.Context, r *http.Request) (string, error) {
			for _, fi := range s.Cfg.Core.FiIDs() { // one call per FI: each carries that FI's token
				if err := s.bulkwire(r, fi, http.MethodPut, "/internal/draft-defaults", map[string]any{fi: s.envDefaults(fi)}, nil); err != nil {
					return "", err
				}
			}
			return "defaults from .env and routing.yaml", nil
		}},
	}
}

func (s *Service) reset(w http.ResponseWriter, r *http.Request) {
	var report []stepReport
	allOK := true
	for _, st := range s.resetSteps() {
		start := time.Now()
		detail, err := retryStep(r.Context(), 90*time.Second, func(ctx context.Context) (string, error) { return st.Run(ctx, r) })
		rep := stepReport{Step: st.Name, OK: err == nil, Detail: detail, Ms: time.Since(start).Milliseconds()}
		if err != nil {
			rep.Detail, allOK = err.Error(), false
		}
		report = append(report, rep)
	}
	s.Log.Info("DEMO-ONLY reset", "ok", allOK)
	writeJSON(w, http.StatusOK, map[string]any{"ok": allOK, "steps": report})
}

func retryStep(ctx context.Context, limit time.Duration, fn func(context.Context) (string, error)) (string, error) {
	deadline := time.Now().Add(limit)
	for {
		sctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		detail, err := fn(sctx)
		cancel()
		if err == nil || time.Now().After(deadline) {
			return detail, err
		}
		select {
		case <-ctx.Done():
			return detail, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func (s *Service) unpauseAll(ctx context.Context, _ *http.Request) (string, error) {
	cs, err := s.Docker.List(ctx)
	if err != nil {
		return "", err
	}
	var done []string
	for _, c := range cs {
		if c.State == "paused" && c.Allowed {
			if err := s.Docker.Act(ctx, c.Service, "unpause"); err != nil {
				return "", err
			}
			done = append(done, c.Service)
		}
	}
	return fmt.Sprintf("unpaused %v", done), nil
}

func (s *Service) startEssential(ctx context.Context, _ *http.Request) (string, error) {
	cs, err := s.Docker.List(ctx)
	if err != nil {
		return "", err
	}
	var started []string
	for _, c := range cs {
		if slices.Contains(s.coreServices(), c.Service) && c.State != "running" {
			if err := s.Docker.Act(ctx, c.Service, "start"); err != nil {
				return "", err
			}
			started = append(started, c.Service)
		}
	}
	// Temporal must answer before later steps talk to it.
	if _, err := s.Temporal.CheckHealth(ctx, &client.CheckHealthRequest{}); err != nil {
		return "", fmt.Errorf("waiting for Temporal: %w", err)
	}
	return fmt.Sprintf("started %v", started), nil
}

func (s *Service) stopDemoWorkers(ctx context.Context, _ *http.Request) (string, error) {
	cs, err := s.Docker.List(ctx)
	if err != nil {
		return "", err
	}
	var stopped []string
	for _, c := range cs {
		if (c.Service == "worker-v2" || c.Service == "worker-v3") && c.State == "running" {
			if err := s.Docker.Act(ctx, c.Service, "stop"); err != nil {
				return "", err
			}
			stopped = append(stopped, c.Service)
		}
	}
	return fmt.Sprintf("stopped %v", stopped), nil
}

// terminateRunning ends every running batch. Termination is an OPERATOR tool used here only to clean up a demo;
// it is never part of the business flow (a batch is closed by its own workflow: cancel, deny, expire).
func (s *Service) terminateRunning(ctx context.Context, _ *http.Request) (string, error) {
	rows, err := s.list(ctx, fmt.Sprintf("WorkflowType = '%s' AND ExecutionStatus = 'Running'", temporalx.WorkflowType), 500)
	if err != nil {
		return "", err
	}
	for _, row := range rows {
		err := s.Temporal.TerminateWorkflow(ctx, row.WorkflowID, "", "demo reset")
		if err != nil && !isNotRunning(ctx, s, row.WorkflowID) {
			return "", fmt.Errorf("terminate %s: %w", row.WorkflowID, err)
		}
	}
	return fmt.Sprintf("terminated %d", len(rows)), nil
}

func isNotRunning(ctx context.Context, s *Service, wfID string) bool {
	d, err := s.Temporal.DescribeWorkflowExecution(ctx, wfID, "")
	return err == nil && d.GetWorkflowExecutionInfo().GetStatus() != enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING
}
