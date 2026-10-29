# Traceability

Every named pattern and every design rule of this implementation, mapped to the code that implements it. The
pattern table is built from the `PATTERN:` comments across `go/`, `core/`, `sql/`, `ui/`, `control-ui/`, and
`scripts/`; every pattern appears in code at least once.

## Pattern tags

| Pattern tag | What it does here | Where (file:function) | Official reference |
|---|---|---|---|
| Single Workflow per Business Process | One `BulkWireBatch` per uploaded file owns draft, the durable wait for approval, and authorization. | `go/internal/workflow/workflow.go:BulkWireBatch` | https://docs.temporal.io/child-workflows#when-to-use-child-workflows |
| Approval | Core decides (TAC, determination, dual control); the workflow waits for its `authorize` Update. | `go/internal/workflow/workflow.go:BulkWireBatch` (wait phase); `core/CoreApi/Endpoints/ApprovalsEndpoints.cs` | https://docs.temporal.io/design-patterns/approval |
| Update-with-Start | Draft and Core's approval callback start the workflow if needed and deliver the Update in one call. | `go/internal/api/commands.go:updateWithStart` | https://docs.temporal.io/sending-messages#update-with-start |
| Update Validator | Every Update (`draft`, `authorize`, `deny`, `cancel`, `resolve`) has a non-mutating validator. | `go/internal/workflow/handlers.go:registerHandlers`, `validate*` | https://docs.temporal.io/encyclopedia/workflow-message-passing |
| Query for State | The portal's live view is the `status` Query, polled every second. | `go/internal/workflow/handlers.go:registerHandlers`; `go/internal/workflow/state.go:view`; `go/internal/api/reads.go:batchStatus` | https://docs.temporal.io/evaluate/features/workflow-message-passing |
| Bounded Parallelism (Semaphore) | At most `concurrency` chunk Activities in flight; a parked chunk releases its slot. | `go/internal/workflow/chunks.go:runChunks` | https://pkg.go.dev/go.temporal.io/sdk/workflow#NewSemaphore |
| Basic Workflow Batch | Chunks are Activities inside one workflow (no children). | `go/internal/workflow/chunks.go:runChunks` | https://docs.temporal.io/design-patterns/batch-processing-patterns |
| Claim Check | Rows live in Redis/S3/GCS; Activities get references and read only their pages. | `go/internal/claimcheck/store.go`; `go/internal/staging/layout.go:ReadRange`; `go/internal/activities/activities.go:InsertDraftChunk` | https://docs.temporal.io/troubleshooting/blob-size-limit-error |
| Retry Policy (no attempt cap) | One RetryPolicy for every Activity: backoff 1 s × 2, capped at MaximumInterval, no MaximumAttempts. | `go/internal/workflow/options.go:coreActivityOptions` | https://docs.temporal.io/encyclopedia/retry-policies |
| Fixed Wall-Time Retries | ScheduleToClose = the retry budget; exhaustion parks the chunk (`RETRY_BUDGET_EXHAUSTED`). | `go/internal/workflow/options.go:coreActivityOptions`; `go/internal/workflow/park.go:shouldPark` | https://docs.temporal.io/design-patterns/fixed-wall-time-retries |
| Retry Alerting via Metrics | `bulkwire_core_attempts_over_threshold{activity,fi}` counts attempts past the threshold. | `go/internal/activities/activities.go:alertOnRetries`; `go/internal/control/metrics.go` | https://docs.temporal.io/design-patterns/retry-metrics |
| Non-Retryable Errors | Core 422/409/404/400 and missing staged data become non-retryable ApplicationErrors with details. | `go/internal/coreclient/errors.go:AsActivityError`; `go/internal/model/errors.go` | https://docs.temporal.io/design-patterns/non-retryable-errors |
| Resumable Activity | A structurally failed chunk parks inside the workflow until an operator's `resolve` Update. | `go/internal/workflow/park.go:parkChunk`, `parkBatch`, `mustStep`; `go/internal/workflow/chunks.go:runChunks` | https://docs.temporal.io/design-patterns/resumable-activity |
| Business Outcome as Result | Policy DENY, screening rejections, and "already present" are results, not errors. | `go/internal/activities/activities.go:CheckGroupPolicy`; `go/internal/workflow/chunks.go:draftChunk`; `sql/03-procs.sql:usp_InsertDraftWires` | https://docs.temporal.io/design-patterns/non-retryable-errors |
| Idempotent Activity | Every Core procedure is idempotent by batch/wire/chunk ID and returns chunk-level totals. | `go/internal/activities/activities.go` (package doc); `sql/03-procs.sql:usp_InsertDraftWires` | https://temporal.io/blog/idempotency-and-durable-execution |
| Business Workflow ID | `bulkwire/{fiId}/{batchId}` with USE_EXISTING + REJECT_DUPLICATE. | `go/internal/temporalx/searchattrs.go:WorkflowID`; `go/internal/api/commands.go:startOptions` | https://docs.temporal.io/workflow-execution/workflowid-runid |
| Search Attributes for Operations | `BulkWireStatus`, `FiId`, `CustomerId`, `InitiatedBy`, `NeedsAttention` power lists and the exception queue. | `go/internal/temporalx/searchattrs.go`; `go/internal/workflow/state.go:setPhase`, `setParked`; `scripts/temporal-init.sh` | https://docs.temporal.io/search-attribute |
| Durable Timer | The approval window is `AwaitWithOptions` with a labeled timer; expiry closes the batch. | `go/internal/workflow/workflow.go:BulkWireBatch` | https://docs.temporal.io/design-patterns/approval |
| Fairness by Tenant | Fairness key = FI on every start; Activities inherit it. | `go/internal/api/commands.go:startOptions`; `go/internal/control/fairness.go` | https://docs.temporal.io/develop/task-queue-priority-fairness |
| Worker Versioning (Pinned) | Workers join deployment `bulkwire`; the workflow is registered Pinned; v2 bug / v3 fix walkthrough. | `go/cmd/worker/main.go:run`; `go/internal/control/versioning.go`; `scripts/worker-activate.sh` | https://docs.temporal.io/production-deployment/worker-deployments/worker-versioning |
| Payload Encryption Codec | AES-256-GCM codec with key id as AAD and a hot-reloading keyring. | `go/internal/codec/codec.go:Encode`, `Decode`; `go/internal/codec/watch.go:Watch` | https://docs.temporal.io/payload-codec |
| Describe for Retry Telemetry | Pending Activities' attempt and last failure are mapped back to chunks. | `go/internal/api/reads.go:batchStatus`, `pendingActivities` | https://docs.temporal.io/encyclopedia/retry-policies |
| Tenant Context in Input | The FI travels in the workflow and Activity inputs; workers map it to that FI's own Core. | `go/internal/workflow/types.go:BatchInput`; `go/internal/activities/activities.go`; `go/internal/coreclient/client.go:Do` | (design principle) |
| Encrypting Data Converter | Default payload converter + the AES codec, on every client and worker; the failure converter uses it too, so failure details are encrypted. | `go/internal/codec/codec.go:NewDataConverter`, `NewFailureConverter`; `go/internal/temporalx/dial.go:Dial`; `go/cmd/worker/main.go`, `go/cmd/bulkwire-service/main.go`, `go/cmd/control-service/main.go` | https://docs.temporal.io/dataconversion |
| Codec Server | Decrypts payloads for the Web UI and CLI, with CORS for the UI origin. | `go/cmd/codec-server/main.go` | https://docs.temporal.io/codec-server |
| Per-Tenant Rate Limit | Fairness-key rate limit on the Activity task queue via `UpdateTaskQueueConfig`. | `go/internal/control/fairness.go:setFairness` | https://docs.temporal.io/develop/task-queue-priority-fairness#set-rate-limits-at-the-task-queue-level |
| Workflow User Metadata | Static summary/details at start, current details on every change, Activity and timer summaries. | `go/internal/api/commands.go:startOptions`; `go/internal/workflow/state.go:publishDetails`; `go/internal/workflow/options.go`; `go/internal/workflow/chunks.go` | https://docs.temporal.io/workflow-execution |

## Design rules

These hold everywhere in the codebase; breaking one is a defect.

| # | Rule | Where it is enforced | Evidence |
|---|---|---|---|
| 1 | One workflow per batch (`BulkWireBatch`) | `go/internal/workflow/workflow.go`; `go/cmd/worker/main.go` registers only it | `grep -rn RegisterWorkflow go/` → one registration |
| 2 | No child workflows, Signals, Continue-As-New; chunks are Activities under `workflow.NewSemaphore`; inbound = Updates with validators; reads = Query `status` | `go/internal/workflow/handlers.go`, `chunks.go` | grep for `ExecuteChildWorkflow`, `GetSignalChannel`, `NewContinueAsNewError` in `go/` → none |
| 3 | No `MaximumAttempts` on Core Activities | `go/internal/workflow/options.go:coreActivityOptions` | grep `MaximumAttempts` → only comments |
| 4 | Failures never leave Temporal: park + `resolve`; a parked item never holds a concurrency slot; a batch whose first instruction never arrives closes instead of waiting forever | `go/internal/workflow/park.go`, `workflow.go` | W4–W7, W13, W15, W16, W18; catalog F17, F21, F26 |
| 5 | Business outcomes are results | `go/internal/activities/activities.go:CheckGroupPolicy`, `InsertDraftChunk`; `sql/03-procs.sql` | W11, W14; F23, F24 |
| 6 | No heartbeats; StartToClose 60 s | `go/internal/workflow/options.go:StartToClose` | grep `HeartbeatTimeout`, `RecordHeartbeat` → none |
| 7 | References, not data | `go/internal/activities/types.go` | Activity tests: only the chunk's pages are read |
| 8 | Workflow ID `bulkwire/{fiId}/{batchId}`; Update-with-Start USE_EXISTING + REJECT_DUPLICATE | `go/internal/temporalx/searchattrs.go`, `go/internal/api/commands.go` | `TestDraftUsesUpdateWithStartWithTheSpecifiedOptions`; F34, F35 |
| 9 | Workflow determinism | `go/internal/workflow/*` (`DETERMINISM:` comments) | review grep for `time.Now`, `rand.`, `go func`, `select {`, `net/http`, `os.Getenv` → empty; `make replay` |
| 10 | Worker Versioning (Pinned); encrypting DataConverter everywhere + Codec Server | `go/cmd/worker`, `go/internal/codec`, `go/cmd/codec-server`, `go/internal/temporalx/dial.go` (refuses a nil converter) | Scenarios E and F; `TestFailureDetailsAreEncrypted` |
| 11 | One namespace `bulkwire`; five Search Attributes created by init and verified at startup; no PII in SAs/IDs | `scripts/temporal-init.sh`; `go/internal/temporalx/dial.go:VerifySearchAttributes` | e2e step 1 |
| 11a | One Core (API + SQL Server) per FI, each with its own service-to-service token | `docker-compose.yml` (`core-*`, `sql-*`); `go/internal/api/auth.go:internal`, `sameFI` | e2e step 9; `TestInternalCallsAreScopedToTheTokensFI`; catalog F08 |
| 11b | Demo code isolated | `go/internal/control`, `control-ui/`; labeled `DEMO-ONLY` hooks in `core/CoreApi/Demo`, `go/internal/api/defaults.go`, `go/internal/workflow/workflow.go` | `grep -rn DEMO-ONLY` |
| 12 | Claim check only as an immutable read-only store | `go/internal/claimcheck/store.go` (no Update/Delete); Redis imported only there and in `go/internal/control/expire.go` | `grep -rln go-redis go/` |
| 13 | Core is the system of record (approval facts come from Core's records or the server-side staged batch, never the browser); stored procedures with TVPs; 30 s SQL timeout | `sql/03-procs.sql`; `core/CoreApi/Data/Tvp.cs`, `SqlExecutor.cs`; `core/CoreApi/Endpoints/ApprovalsEndpoints.cs:SubjectAsync` | catalog F02 and F10 (real 504 after 30 s); `TvpTests`; e2e steps 4 and 4b |
| 14 | Honesty: fault injection and latency only in DEMO-ONLY paths, external conditions only | `core/CoreApi/Demo/*`, `go/internal/control` | no concurrency-dependent slowdown exists anywhere |
