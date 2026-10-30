# Changes: one Core task queue per FI, chunk size without a floor, concurrency 4

This report covers the changes that bring the code and the repository's docs in line with the revised design:

- Every Activity that calls Core runs on a task queue for its FI.
- Each Core queue is rate limited, with Fairness keyed by batch ID. The fairness key is the FI only on a queue
  shared by several FIs.
- Chunk size is configuration with no artificial floor.
- Default chunk concurrency is 4.
- The encrypting DataConverter is presented as an example.

The FI IDs in this repository are `lakeshore` and `prairie`, so the queues are `bulkwire-core-lakeshore` and
`bulkwire-core-prairie`. The workflow queue `bulkwire` is unchanged.

## What changed, by task

### Task 1: one Core task queue per FI

**1a. Routing configuration**

- New `config/routing.yaml` holds:
  - `coreQueues`, with `fis`, `activitiesPerSecond`, and `maxConcurrentActivities` for each queue;
  - the commented-out shared-cluster example (1g);
  - `fiDefaults.<fi>.chunkConcurrency` (task 3).
- New package `go/internal/routing/routing.go` provides `Load`, `Parse`, `Validate`, `Queues`, `QueueFor`,
  `Resolve`, `ChunkConcurrency`, and `Subset`.
- The file is read by `go/cmd/worker/main.go`, `go/cmd/bulkwire-service/main.go`, and
  `go/cmd/control-service/main.go`, using the path in `ROUTING_CONFIG_PATH` (`go/internal/config/config.go`,
  `docker-compose.yml`).
- `go.yaml.in/yaml/v3 v3.0.5` and `google.golang.org/grpc v1.84.0` were already in the module graph. They are now
  direct dependencies (`go/go.mod`), at the same versions.

**1b. Route resolved at workflow start**

- `go/internal/api/commands.go:batchInput` resolves the queue and fairness key on both Update-with-Start paths
  (the portal's Draft and Core's approval callback).
- `go/internal/workflow/types.go:BatchInput` gains `CoreTaskQueue` and `CoreFairnessKey`, with a comment saying a
  batch keeps the queue it started with.
- `go/internal/workflow/state.go` carries both into the workflow's settings.

**1c. Which Activities moved**

Activity options are now built in one function, `go/internal/workflow/options.go:activityOptions(settings, route,
…)`, with a route of `onSharedQueue` or `onCoreQueue`. Its callers are:

- `go/internal/workflow/chunks.go`: `InsertDraftChunk`, `AuthorizeChunk`, `RejectChunk`.
- `go/internal/workflow/park.go:mustStep`.
- `go/internal/workflow/workflow.go`: the steps routed through `mustStep`:
  - plan → shared;
  - Core batch, policy checks, and every status change → Core.

The Core Activities were found by searching for calls into `coreclient` (`go/internal/activities/activities.go`):

- `EnsureGenTxBatch`
- `CheckGroupPolicy`
- `InsertDraftChunk`
- `AuthorizeChunk`
- `RejectChunk`
- `SetGenTxBatchStatus`, which covers Drafted, Authorizing, Authorized, Expired, Denied, Cancelled, and Rejected.

`PlanChunks` stays on `bulkwire`.

**1d. Fairness and priority**

- Every Core Activity sets `Priority{FairnessKey: CoreFairnessKey}`. The key is the batch ID, or the FI on a
  queue shared by several FIs (`routing.Resolve`).
- `startOptions` in `go/internal/api/commands.go` no longer sets `Priority{FairnessKey: fiID}`.
- `go/internal/control/fairness.go`, the per-FI fairness-key limit on the shared queue, was deleted.
- Nothing else relied on the inherited key: `Priority` and `FairnessKey` now appear only in `options.go` and the
  routing code.
- The shared queue has no leftover fairness configuration (`task-queue config get` returns nothing).
- Fairness was already enabled on the dev server (`matching.enableFairness=true`); only the comment in
  `docker-compose.yml` changed.

**1e. Workers**

Each worker process in `go/cmd/worker/main.go` runs:

- one SDK worker on `bulkwire`, with the workflow and `PlanChunks`;
- one SDK worker per Core queue, configured with:
  - `DisableWorkflowWorker`;
  - `TaskQueueActivitiesPerSecond` and `MaxConcurrentActivityExecutionSize` from routing.yaml;
  - Core clients built from `routing.Subset` of the endpoints and tokens, so a Lakeshore worker holds no Prairie
    credentials.

All of them run under `runAll`, which uses one shared stop channel.

Supporting changes:

- `activities.RegisterShared` and `RegisterCore` register each method under its existing name.
- `checkRouting`, the first call in every Core Activity, returns the non-retryable `WrongFiRouting` error
  (`go/internal/model/errors.go`). The message names only the FI and the queue.
- `go/internal/workflow/park.go:shouldPark` parks that error with code `WRONG_FI_ROUTING` and source `ROUTING`.
- `coreclient.Client.Serves` is new.

**1f. Worker Versioning**

- Every SDK worker in a process has the same deployment name and build ID.
- The versioning scenario was run end to end. After the override, B's authorize chunks ran on
  `bulkwire-core-lakeshore` and were picked up by the v3 container (see Verification, item 6).

**1g. Shared clusters**

- The routing file has the commented-out example.
- `go/internal/routing/routing_test.go` covers:
  - a two-FI queue resolves to fairness key = FI;
  - its worker subset holds both FIs' clients;
  - startup validation: each FI in exactly one queue, no empty queue, positive limits, unknown FIs, and every
    problem reported at once.

**1h. Rate limit without a deployment**

- `temporal task-queue config set --queue-rps-limit` is supported by the bundled CLI.
- `make ratelimit FI=lakeshore RPS=2` runs it (`scripts/ratelimit.sh`, `Makefile`). `RPS=default` clears it.
- `make test-integration` was also added (see Verification, item 5).

### Task 2: chunk size without an artificial floor

- `go/internal/api/defaults.go:parse` accepts any chunk size from 1 to `MAX_ROWS`. The default stays 250.
- `go/internal/staging/plan.go` adds `ChunkCount` and `ChunkLimitMessage`. The planner is unchanged; tests cover
  chunk size 1 and a size larger than any group.
- The new history guard `MAX_CHUNKS_PER_BATCH` (`go/internal/config/config.go:MaxChunksPerBatchDefault`, `.env`,
  compose) is enforced at three points:
  - **At staging** (`go/internal/staging/stage.go`). A batch over the limit is INVALID, with a batch-level
    `TOO_MANY_CHUNKS` error and `Manifest.BatchError`. Nobody can draft it, and Core refuses to approve an invalid
    staged batch. The review screen shows the reason: `ui/src/pages/StagedReview.tsx` and `ui/src/types.ts`, the
    only portal change.
  - **At Draft, as a backstop** (`go/internal/api/commands.go:checkChunkLimit`). It runs before Update-with-Start,
    and only when the workflow does not exist yet, so a running batch is never blocked by a later chunk-size
    change. It returns 409 `BATCH_TOO_MANY_CHUNKS` with the user-facing message. It logs that batches of this shape
    need Continue-As-New or child workflows that each own a segment of the batch.
  - **On Core's approval callback.** It returns 409 to Core rather than dropping the approval silently; Core
    records the notification as `NOTIFY_FAILED`. It logs at ERROR with the batch ID and approval reference.
- Tests:
  - 10,000 rows at chunk size 10 → 1,000 chunks, accepted;
  - 10,000 rows at chunk size 1 → rejected;
  - one test for each enforcement point.
  - Files: `go/internal/staging/staging_test.go`, `go/internal/api/api_test.go`.

### Task 3: default concurrency of 4

- `DEFAULT_CONCURRENCY` is now 4 (`.env`, `config.go`). The per-FI value is read from `routing.yaml`
  `fiDefaults` (`api.NewDraftDefaults`, `control/core.go:envDefaults`).
- It is copied into the workflow input together with the queue name.
- Shown in the Control Center's Draft defaults card and in the docs.

### Task 4: repository docs

- **`README.md`:**
  - a new "Task queues" section, including chunk size and the history guard;
  - scenario C;
  - catalog range F01–F49 and tests W1–W20;
  - Cloud-mode Fairness note;
  - encryption presented as an example.
- **`docs/ARCHITECTURE.md`:**
  - topology and services;
  - a new "Task queues and Core load" section, covering what limits what, Fairness, shared clusters, overrides,
    and Worker Versioning with a per-FI container sketch;
  - a new "Chunk size and the history guard" section;
  - tenancy;
  - encryption as an example.
- **`docs/TRACEABILITY.md`:**
  - new pattern rows Task Queue per Downstream, Fairness, Task Queue Rate Limit, and History guard;
  - updated rules 3, 4, 11, 11a and the Non-Retryable Errors row;
  - removed Fairness by Tenant and Per-Tenant Rate Limit.
- **`docs/FAILURE-CATALOG.md` and `control-ui/src/catalog.ts`:**
  - concurrency default;
  - F08 and F36 rewritten;
  - new F45–F49.
- **Rate-limit wording.** Every place that said the limit "limits concurrent load" now says it caps how fast
  Activities start, and that in-flight work is bounded by the semaphore and each worker's cap.
- **Approved additions:**
  - `docs/RUNBOOK-DEMO.md` scenario C;
  - `docs/DEMO-GUIDE-PAYROLL-DAY.md`, with a new phase 2b for the Fairness comparison;
  - `docs/DEMO-GUIDE-CATALOG.md`: F45–F49 recipes and index;
  - `scripts/e2e.sh`: step 14 rewritten, step 15 added;
  - the "Core load & queues" label in every guide;
  - `docs/VERSIONS.md`;
  - a new sample, `northline-bonus-500`, in `go/internal/samples/samples.go`.
- Your note asked for "catalog F38". In this repository the payroll-day entry is **F36** (F38 is the versioning
  entry), so F36 is the one rewritten.

### Task 5: Control Center

- **Core load & queues** (`control-ui/src/sections.tsx`) has a card per FI showing:
  - routing.yaml rate and per-worker cap;
  - any override;
  - the enforced rate and its source;
  - pollers;
  - **Set override** and **Clear override**.

  The status strip shows each Core queue (`StatusStrip.tsx`). Both use the new `GET /api/ratelimits` and
  `PUT /api/ratelimits/{fiId}` (`go/internal/control/ratelimit.go`), which call `UpdateTaskQueueConfig`, the same
  RPC the CLI uses.
- **Reset** clears every override (`control/reset.go`), so each queue returns to the workers' rate.
- **Fairness comparison launcher.** The payroll-day loader accepts `startAfterMs` (`control/load.go`,
  `control-ui/src/actions.ts`).
- **Draft defaults** shows the new chunk-size and concurrency ranges.
- **Stopping one FI's Core-queue workers was not built.** It needs separate containers; see "Not done" below.

## The history guard: measurement and arithmetic

Measured from `go/testdata/histories/scenario-a-outage.json`: a 23-chunk, 6-group batch, drafted and authorized
through a Core outage, with **324 events**.

| Source of events | Count |
|---|---|
| 57 Activities × 3 events (scheduled, started, completed) | 171 |
| 47 Workflow Tasks × 3 events | 141 |
| Other: start, 2 Updates × 2, approval timer × 2, 4 Search Attribute upserts, completion | 12 |
| **Total** | **324** |

Retries add no events: only an Activity's final attempt is recorded.

Upper-bound model (one Workflow Task per Activity completion):

| Unit | Events |
|---|---|
| Activity | 3 + at most 3 Workflow Task events = 6 |
| Chunk (draft + authorize) | 12 |
| Group (one policy check) | 6 |
| Batch (5 single Activities = 30, plus 12 others) | 42 |

Check against the measurement: 42 + 6 × 6 + 12 × 23 = 354 ≥ 324.

The worst case for groups is one chunk per group: 42 + 18 × chunks < 25,000, so chunks ≤ 1,386.

**Default `MAX_CHUNKS_PER_BATCH` = 1,300.**

- At the worst case that is 23,442 events.
- A typical batch with a handful of groups is about 15,700.
- The 2,000 in-flight Activity limit doesn't apply, because chunk concurrency is at most 8.
- 10,000 rows at chunk size 10 (1,000 chunks) is accepted. At chunk size 1 (10,000 chunks) it is rejected.

## Demo rate limit and per-worker cap

**10 Activities/s and 8 per worker, for each Core queue.**

- Each Core call takes about 0.6 s with the demo's simulated processing time, so 10/s is about 6 calls in flight at
  steady state.
- That is enough for a payroll day's two or three batches at concurrency 4, and well below what one SQL Server
  handles.
- The cap of 8 protects each worker process. With two worker processes, at most 16 calls per FI can be in flight.

Measured on payroll day (two 10k batches at Lakeshore, one 6k at Prairie, concurrency 4):

| Lakeshore queue | Lakeshore calls/s | Lakeshore in flight | Lakeshore 10k batches drafted | Prairie 6k drafted |
|---|---|---|---|---|
| No override (10/s) | ≈ 10 | ≈ 6 | ≈ 10 s | ≈ 6 s |
| Override 3/s | 3 | ≈ 2 | ≈ 33 s | ≈ 8 s |

## Temporal version and feature support

- Image `temporalio/temporal:1.9.1`: CLI 1.9.1, Server 1.32.0, UI 2.54.1.
- **Fairness: supported.** It was already enabled for the whole dev server with
  `--dynamic-config-value=matching.enableFairness=true`. Single partition is kept.
- **`temporal task-queue config set`: supported**, with `--queue-rps-limit`, `default` to unset, and
  `config get`.
- `DescribeTaskQueue` reports the effective rate limit and its source (API or WORKER).
- No upgrade was needed or made.

## Verification

**1. `make test`: pass.**

- Go: all packages, including W1–W20, the routing, guard, and set/reset tests.
- .NET: passed.
- Portal UI: 7/7. Control Center UI: 3/3.

**`make replay`: pass on both captured histories, with no recapture needed.**

Those histories predate the change. Their inputs have no `CoreTaskQueue`, so on replay the Core Activities target
the workflow's queue exactly as recorded.

Operational note: a batch started before this change and still running when the new code ships under the same
build ID would schedule Core Activities on the shared queue, where they are no longer registered. Ship this change
as a new build ID (Pinned) and keep the old build until its batches drain.

**2. Activity options: pass.**

- `grep MaximumAttempts`: only the two comments in `options.go`.
- Activity options are built in exactly one place, `options.go:activityOptions`. All four call sites pass a route.
- Every Core Activity gets:
  - `StartToCloseTimeout: 60 s`;
  - `ScheduleToCloseTimeout: RetryBudget`;
  - `TaskQueue: CoreTaskQueue`;
  - `Priority{FairnessKey: CoreFairnessKey}`.
- W19 checks the queue and fairness key per Activity type.
- e2e step 14 checks the real history:
  - Lakeshore's Core Activities on `bulkwire-core-lakeshore` and `PlanChunks` on `bulkwire`;
  - fairness key = the batch ID.

**3. Temporal UI, checked with Playwright: pass.**

- A Lakeshore batch's `ActivityTaskScheduled` event shows **Task Queue Name `bulkwire-core-lakeshore`** and
  **Priority = the batch ID**. The input shows `CoreTaskQueue` and `CoreFairnessKey`.
- The task-queue pages for `bulkwire`, `bulkwire-core-lakeshore`, and `bulkwire-core-prairie` each list the
  worker-a and worker-b pollers.
- The workflow's **Workers** tab lists one worker per queue with its build ID.

**4. README exercises: pass.**

- `scripts/e2e.sh`: **63 passed, 0 failed**.
  - Core outage mid-draft (step 3): 5,000 rows, 4,997 drafted, 3 rejected, never parked.
  - Hold and resolve (step 5).
  - Worker crash and all workers down during a 10k draft (steps 10 and 11): exact 10,000.
- A separate chaos-tour run on a 10k draft, at 5,000 ms latency, applied in sequence:
  1. kill worker-a;
  2. stop both workers;
  3. restart Temporal;
  4. pause sql-lakeshore 40 s;
  5. stop redis 30 s.

  Result: DRAFTED with **9,994 drafted + 6 rejected = 10,000**, one execution, never parked. After the Temporal
  restart both queues were back at 10/s (WORKER) with pollers.

**5. New catalog entries: pass.**

- **F36 and F45** (e2e step 14): with Lakeshore's queue overridden to 3/s, Lakeshore peaked at 3.0 calls/s. Prairie
  drafted while Lakeshore was still drafting, and reset cleared the override.
- **F46**, `make test-integration`, real dev server with no poller on `bulkwire-core-lakeshore`:
  - the Prairie batch reached AUTHORIZED;
  - the Lakeshore batch stayed RUNNING with `EnsureGenTxBatch` SCHEDULED;
  - it completed once a Lakeshore Core worker started.
- **F47:** `TestCoreActivityOnTheWrongFIsQueueParks` (no Core call made) and W20 (parks, RETRY completes).
- **F48**, Lakeshore queue at 2/s, three 5,000-row batches, then the 500-row batch 3 s later:

  | Fairness key | 500-row batch to DRAFTED | 5,000-row batches |
  |---|---|---|
  | Per batch (as built) | 9.0 s, 9.2 s | ≈ 47 s |
  | One key for every Lakeshore batch (FIFO among them) | 22.5 s, 22.5 s | ≈ 47 s |

  The single-key runs used a temporary routing.yaml that put both FIs on one queue (a supported shared-queue
  configuration whose key is the FI). The file was restored afterwards.

  The difference is clear, and the catalog says what it is: Fairness shortens the small batch's wait behind tasks
  already queued. It does not make the queue faster, and without a backlog it changes nothing. Per-FI isolation is
  still the main point of scenario C.
- **F49:** e2e step 15 and a browser check.
  - Chunk size 1 makes summit-payroll-5000 INVALID, and the review banner gives the reason.
  - Save as draft is disabled, and Draft returns 409.
  - The Draft backstop returns `BATCH_TOO_MANY_CHUNKS`.
  - On the approval-callback backstop, Core shows `APPROVED NOTIFY_FAILED`, and bulkwire-service logs at ERROR with
    the batch ID.

**6. Worker Versioning scenario: pass.**

- Batch A drafted on v1.
- v2 was started and made current, with 3 pollers on each Core queue.
- Batch B drafted on v2. It was approved, and v2 crashed before authorize.
- A authorized on v1.
- v3 was started and made current, and B was moved v2 → v3. B reached AUTHORIZED 19 s later (4,997 authorized).
- After the override, B's history shows 23 `AuthorizeChunk` and 1 `SetGenTxBatchStatus` on
  `bulkwire-core-lakeshore`, all started by the worker-v3 container.

## Not done, or done differently

- **Stopping one FI's Core-queue workers from the Control Center: not built.** Each worker process polls every
  queue (task 1e), so this needs a separate container per FI. F46 is Narrated, with `make test-integration` as
  evidence. The proposed design:
  - `worker-shared`, polling only `bulkwire`;
  - one `worker-core-<fi>` per Core queue, the same image with a setting such as `CORE_QUEUES=bulkwire-core-lakeshore`
    to select which queues it polls;
  - all deployed with the same build ID for every release;
  - `worker-activate` changed to make a build Current only once its version has pollers on `bulkwire` and on every
    Core queue;
  - `CHAOS_ALLOWLIST` gains the per-FI containers.

  A pinned version needs at least one poller in that version on each queue, so this satisfies Worker Versioning.
- **F47 is Narrated (test-only).** It requires bulkwire-service and the workers to disagree about routing.yaml, and
  there is no production code path for that.
- **Additions beyond the requested schema:**
  - `fiDefaults.chunkConcurrency` in routing.yaml (task 3 asked for per-FI concurrency in that file);
  - a small portal change, a batch-level reason on the review banner, so the user sees why a batch was rejected at
    staging.
- **SDK test environment limitation.** The test environment does not copy `Priority` into `activity.Info`, so W19
  reads the fairness key from the scheduled options with a workflow interceptor.
- **Pre-existing behavior, not changed.** For a batch whose Workflow Task is failing, the degraded reason is
  `NO_WORKERS` for the first ~10 s (the Query times out), then `QUERY_FAILED`.
- **Project instructions and design specs** (kept outside version control) were updated to the new design, and
  the decision was recorded there so later work doesn't revert it.
- **Nothing was committed.**
