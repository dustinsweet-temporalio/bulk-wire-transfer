// DEMO-ONLY: the Worker Versioning walkthrough ("we shipped a bug", catalog F38).
//
// PATTERN: Worker Versioning (Pinned) — https://docs.temporal.io/production-deployment/worker-deployments/worker-versioning
// Steps: start worker-v2 (built with a deliberate workflow bug) → make v2 current → a NEW batch is pinned to v2 and
// stops right before authorize (Workflow Task failures, no data lost; batches on v1 are unaffected) → start
// worker-v3 (the fix) → make v3 current → move the pinned v2 batches to v3 with a versioning override → they resume
// where they stopped → stop v2. Pinned workflows never run code they did not start with; a targeted override moves
// only the affected executions.
package control

import (
	"fmt"
	"net/http"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"bulkwire/internal/temporalx"
)

var demoBuilds = []string{"v1", "v2", "v3"}

func (s *Service) versioning(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := timeout(r.Context(), 10*time.Second)
	defer cancel()
	name := s.Cfg.Temporal.DeploymentName
	out := map[string]any{"deployment": name}
	if desc, err := s.Temporal.WorkerDeploymentClient().GetHandle(name).Describe(ctx, client.WorkerDeploymentDescribeOptions{}); err == nil {
		rc := desc.Info.RoutingConfig
		if rc.CurrentVersion != nil {
			out["current"] = rc.CurrentVersion.BuildID
		}
		if rc.RampingVersion != nil {
			out["ramping"], out["rampingPercentage"] = rc.RampingVersion.BuildID, rc.RampingVersionPercentage
		}
		var known []string
		for _, v := range desc.Info.VersionSummaries {
			known = append(known, v.Version.BuildID)
		}
		out["versions"] = known
	} else {
		out["error"] = err.Error()
	}

	pollers := map[string]int{}
	if tq, err := s.Temporal.DescribeTaskQueue(ctx, s.Cfg.Temporal.TaskQueue, enumspb.TASK_QUEUE_TYPE_WORKFLOW); err == nil {
		for _, p := range tq.GetPollers() {
			pollers[p.GetDeploymentOptions().GetBuildId()]++
		}
	}
	type buildInfo struct {
		BuildID        string `json:"buildId"`
		Pollers        int    `json:"pollers"`
		RunningBatches int64  `json:"runningBatches"`
		ContainerState string `json:"containerState"`
	}
	containers := map[string]string{}
	if cs, err := s.Docker.List(ctx); err == nil {
		for _, c := range cs {
			containers[c.Service] = c.State
		}
	}
	var builds []buildInfo
	for _, b := range demoBuilds {
		bi := buildInfo{BuildID: b, Pollers: pollers[b]}
		if c, err := s.Temporal.CountWorkflow(ctx, &workflowservice.CountWorkflowExecutionsRequest{Namespace: s.Cfg.Temporal.Namespace,
			Query: fmt.Sprintf("WorkflowType = '%s' AND ExecutionStatus = 'Running' AND %s = '%s:%s'",
				temporalx.WorkflowType, temporalx.SAWorkerDeploymentVersion, name, b)}); err == nil {
			bi.RunningBatches = c.GetCount()
		}
		switch b {
		case "v1":
			bi.ContainerState = containers["worker-a"] + "/" + containers["worker-b"]
		default:
			bi.ContainerState = containers["worker-"+b]
		}
		builds = append(builds, bi)
	}
	out["builds"] = builds
	writeJSON(w, http.StatusOK, out)
}

func (s *Service) setCurrent(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BuildID string `json:"buildId"`
	}
	if err := decode(r, &body); err != nil || body.BuildID == "" {
		writeError(w, http.StatusBadRequest, "send {buildId}")
		return
	}
	ctx, cancel := timeout(r.Context(), 15*time.Second)
	defer cancel()
	_, err := s.Temporal.WorkerDeploymentClient().GetHandle(s.Cfg.Temporal.DeploymentName).SetCurrentVersion(ctx,
		client.WorkerDeploymentSetCurrentVersionOptions{BuildID: body.BuildID, Identity: "control-service"})
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.Log.Info("DEMO-ONLY current version set", "buildId", body.BuildID)
	writeJSON(w, http.StatusOK, map[string]string{"current": body.BuildID})
}

// movePinned applies a versioning override (Pinned to toBuildId) to every RUNNING batch pinned to fromBuildId.
func (s *Service) movePinned(w http.ResponseWriter, r *http.Request) {
	var body struct {
		FromBuildID string `json:"fromBuildId"`
		ToBuildID   string `json:"toBuildId"`
	}
	if err := decode(r, &body); err != nil || body.FromBuildID == "" || body.ToBuildID == "" {
		writeError(w, http.StatusBadRequest, "send {fromBuildId, toBuildId}")
		return
	}
	ctx, cancel := timeout(r.Context(), 30*time.Second)
	defer cancel()
	name := s.Cfg.Temporal.DeploymentName
	rows, err := s.list(ctx, fmt.Sprintf("WorkflowType = '%s' AND ExecutionStatus = 'Running' AND %s = '%s:%s'",
		temporalx.WorkflowType, temporalx.SAWorkerDeploymentVersion, name, body.FromBuildID), 200)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	moved, failed := []string{}, map[string]string{}
	for _, row := range rows {
		_, err := s.Temporal.UpdateWorkflowExecutionOptions(ctx, client.UpdateWorkflowExecutionOptionsRequest{
			WorkflowId: row.WorkflowID,
			WorkflowExecutionOptionsChanges: client.WorkflowExecutionOptionsChanges{
				VersioningOverride: &client.VersioningOverrideChange{Value: &client.PinnedVersioningOverride{
					Version: worker.WorkerDeploymentVersion{DeploymentName: name, BuildID: body.ToBuildID}}},
			},
		})
		if err != nil {
			failed[row.WorkflowID] = err.Error()
			continue
		}
		moved = append(moved, row.WorkflowID)
	}
	s.Log.Info("DEMO-ONLY moved pinned batches", "from", body.FromBuildID, "to", body.ToBuildID, "moved", len(moved))
	writeJSON(w, http.StatusOK, map[string]any{"moved": moved, "failed": failed})
}
