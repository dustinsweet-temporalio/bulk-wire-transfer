// DEMO-ONLY: GET /api/status — everything the Control Center's status strip and overview show, gathered in
// parallel with short timeouts so one stopped or paused component never blocks the rest.
package control

import (
	"context"
	"net/http"
	"sort"
	"sync"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/operatorservice/v1"
	"go.temporal.io/sdk/client"

	"bulkwire/internal/temporalx"
)

func (s *Service) status(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{}
	var mu sync.Mutex
	set := func(k string, v any) { mu.Lock(); out[k] = v; mu.Unlock() }
	var wg sync.WaitGroup
	run := func(f func(ctx context.Context)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := timeout(r.Context(), 3*time.Second)
			defer cancel()
			f(ctx)
		}()
	}

	run(func(ctx context.Context) {
		cs, err := s.Docker.List(ctx)
		if err != nil {
			set("containersError", err.Error())
			return
		}
		sort.Slice(cs, func(i, j int) bool { return cs[i].Service < cs[j].Service })
		set("containers", cs)
	})
	run(func(ctx context.Context) {
		_, err := s.Temporal.CheckHealth(ctx, &client.CheckHealthRequest{})
		t := map[string]any{"reachable": err == nil, "namespace": s.Cfg.Temporal.Namespace}
		if err == nil {
			sa := map[string]string{}
			if resp, err := s.Temporal.OperatorService().ListSearchAttributes(ctx, &operatorservice.ListSearchAttributesRequest{Namespace: s.Cfg.Temporal.Namespace}); err == nil {
				for name := range temporalx.RequiredSearchAttributes {
					sa[name] = enumspb.IndexedValueType_name[int32(resp.GetCustomAttributes()[name])]
				}
			}
			t["searchAttributes"] = sa
		}
		set("temporal", t)
	})
	run(func(ctx context.Context) {
		pollers := map[string][]map[string]string{}
		for name, tqType := range map[string]enumspb.TaskQueueType{"workflow": enumspb.TASK_QUEUE_TYPE_WORKFLOW, "activity": enumspb.TASK_QUEUE_TYPE_ACTIVITY} {
			resp, err := s.Temporal.DescribeTaskQueue(ctx, s.Cfg.Temporal.TaskQueue, tqType)
			if err != nil {
				continue
			}
			for _, p := range resp.GetPollers() {
				pollers[name] = append(pollers[name], map[string]string{"identity": p.GetIdentity(), "buildId": p.GetDeploymentOptions().GetBuildId()})
			}
		}
		set("pollers", pollers)
	})
	run(func(ctx context.Context) {
		desc, err := s.Temporal.WorkerDeploymentClient().GetHandle(s.Cfg.Temporal.DeploymentName).Describe(ctx, client.WorkerDeploymentDescribeOptions{})
		d := map[string]any{"name": s.Cfg.Temporal.DeploymentName}
		if err == nil {
			if v := desc.Info.RoutingConfig.CurrentVersion; v != nil {
				d["current"] = v.BuildID
			}
			if v := desc.Info.RoutingConfig.RampingVersion; v != nil {
				d["ramping"] = v.BuildID
			}
		}
		set("deployment", d)
	})
	run(func(ctx context.Context) {
		core := map[string]any{}
		for _, fi := range s.Cfg.Core.FiIDs() {
			entry := map[string]any{"healthy": s.Core.Healthz(ctx, fi) == nil}
			var state any
			if err := s.Core.Do(ctx, fi, http.MethodGet, "/admin/state", nil, &state); err == nil {
				entry["state"] = state
			} else {
				entry["error"] = err.Error()
			}
			core[fi] = entry
		}
		set("core", core)
	})
	run(func(ctx context.Context) {
		set("claimCheck", map[string]any{"backend": s.Cfg.ClaimCheck.Backend, "reachable": s.Store.Ping(ctx) == nil})
	})
	run(func(ctx context.Context) {
		var d any
		if err := s.bulkwire(r.WithContext(ctx), http.MethodGet, "/internal/draft-defaults", nil, &d); err == nil {
			set("draftDefaults", d)
		}
	})
	set("activeKeyId", s.Codec.ActiveKeyID())
	wg.Wait()
	writeJSON(w, http.StatusOK, out)
}
