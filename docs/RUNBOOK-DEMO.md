# Demo runbook

## Cheat sheet

| What | Where |
|---|---|
| Lakeshore Community Bank portal | http://localhost:3001 |
| Prairie Valley Credit Union portal | http://localhost:3002 |
| Demo Control Center (presenter only) | http://localhost:3100 |
| Temporal Web UI | http://localhost:8233/namespaces/bulkwire/workflows |
| Codec Server | http://localhost:8089 |
| Worker metrics | http://localhost:9464/metrics, http://localhost:9465/metrics |

**Password for every demo user: `demo`. Security code (TAC): `246810`.** Each browser window keeps its own session
(sessionStorage), so use one window per user.

| FI | Company ID | User ID | Who | Role |
|---|---|---|---|---|
| Lakeshore | SUMMIT | jordan | Jordan Ellis | Initiator |
| Lakeshore | SUMMIT | alex | Alex Rivera, Controller | Approver ($30M) |
| Lakeshore | SUMMIT | sam | Sam Patel, Treasurer | Approver ($50M) |
| Lakeshore | NORTHLINE | taylor | Taylor Brooks | Initiator |
| Lakeshore | NORTHLINE | quinn | Quinn Murphy, CFO | Approver ($40M) |
| Lakeshore | NORTHLINE | avery | Avery Collins, Controller | Approver ($40M) |
| Lakeshore | BANK | morgan | Morgan Lee, Wire Operations | Bank operations |
| Prairie | HARVEST | riley | Riley Chen | Initiator |
| Prairie | HARVEST | casey | Casey Ortiz | Approver ($10M) |
| Prairie | HARVEST | jamie | Jamie Fields | Approver ($10M) |
| Prairie | BANK | devon | Devon Brooks, Member Operations | Bank operations |

| Sample (portal → Bulk Wire Upload → Sample files) | Scenario |
|---|---|
| summit-payroll-5000 | A, B, E, F (3 blocked beneficiaries; a 251-row group → chunks of 250 + 1) |
| summit-payroll-10000 | C (Jordan), D |
| northline-payroll-10000 | C (Taylor) |
| harvest-6000 | C (Riley) |
| northline-bonus-500 | C step 4, F48 (Taylor) |
| harvest-2000 | F08 tenant isolation |
| summit-cold-chain-over-limit | F23 daily limit |
| summit-vendors-with-errors | F25 invalid file |

**Reset before every scenario:** Control Center → Reset → **Reset demo** (or `make demo-reset`). It unpauses and
restarts essential services, stops worker-v2/v3, makes v1 current, terminates running batches, clears Core data, faults,
latency and stats, clears any Core queue rate-limit override, and resets draft defaults. Each scenario then starts in under 30 s.

Useful Temporal UI saved queries (namespace `bulkwire`): `FiId = "lakeshore" AND BulkWireStatus = "DRAFTING"`,
`NeedsAttention = true`, `InitiatedBy = "taylor"`.

---

## A. Core goes down mid-draft (3 min) — F01, F42

1. Lakeshore portal, sign in as **SUMMIT / jordan**. Bulk Wire Upload → download and upload **summit-payroll-5000**.
   Point at the Groups table: the 251-row group becomes chunks of 250 + 1.
2. Control Center → Core load & queues → Core lakeshore → latency ms **1500** → **set** (slows each chunk call so
   the draft lasts long enough to show the outage).
3. Portal: **Save as draft**. When about 5 tiles are green, Control Center → F01 → **Outage 45 s (Lakeshore)**.
4. Watch the tiles turn amber "retry #N" (DescribeWorkflowExecution); none turns red. Control Center → Alerts &
   metrics: the Lakeshore counter rises (F42).
5. After the outage the batch reaches **Awaiting approval**. Wires tab: 5,000 wires — 4,997 Drafted, 3 Rejected.
6. User menu → Show engineering annotations → **Open in Temporal UI**: one execution, readable top to bottom.

Close with: *What happened* — Core was down for 45 seconds in the middle of a 5,000-wire draft and the batch finished
whole. *Which primitive* — an Activity RetryPolicy with no attempt cap, bounded by wall time. *What you no longer
need* — a dead-letter queue and a reconciliation job.

## B. A human has to fix it, then dual approval (5 min) — F21, F29

1. Window 1: Lakeshore as **jordan**, upload **summit-payroll-5000** (do not draft yet).
2. Window 2: Lakeshore as **BANK / morgan** → Accounts → customer Summit Logistics → **Place hold** on ••0033.
3. Window 1: **Save as draft**. The Cold Chain (••0033) tiles turn red (NEEDS_ATTENTION); all others finish.
4. Window 2: Bulk wire exceptions → open the batch → **Release hold** → **Retry chunk** on each parked chunk.
5. Window 1: the batch reaches **Awaiting approval**.
6. Window 3: **SUMMIT / alex** → open the batch → **Review and approve** → TAC 246810 → "1 of 2 approvals".
7. Window 4: **SUMMIT / sam** → same → "Approved … Release started" → window 1 shows **Authorized**.

Close with: *What happened* — a hold stopped exactly the affected chunks, a banker fixed it, and the batch resumed
where it stopped, then Core ran dual control. *Which primitive* — a non-retryable error parks a Resumable Activity
until a `resolve` Update; the approval wait is a durable timer. *What you no longer need* — dead-letter replays that
hit the same hold again, and a separate approval workflow.

## C. Payroll day: two people, one Core (5 min) — F36, F45, F48, F08

Each FI's Core Activities run on that FI's own Core task queue (`bulkwire-core-lakeshore`, `bulkwire-core-prairie`),
each with its own rate limit: 10 Activities/s from `config/routing.yaml`, overridable at runtime.

1. Control Center → Core load & queues (keep this page open: the charts only record while it is). Both queue lines
   say override **none**, enforced **10/s (WORKER)**.
2. **Launch payroll day** (Jordan/Summit 10k + Taylor/Northline 10k at Lakeshore, Riley/Harvest 6k at Prairie,
   concurrency 4). Lakeshore runs at its queue's 10 calls/s (about 6 in flight); both Lakeshore batches are drafted
   in about 10 s, Prairie's in about 6 s.
3. **Reset demo**, then on the Core lakeshore card: rps **3** → **Set override** (the line now says override 3/s,
   enforced 3/s (API)) → **Launch payroll day** again. Lakeshore stays flat at 3 calls/s (about 2 in flight at
   600 ms latency) and both Lakeshore batches progress side by side (about 33 s). Prairie's queue is still at 10/s:
   its batch drafts in about 8 s, not queued behind Lakeshore's backlog. (The override cannot be shown mid-run at
   10/s: the batches finish before you can click.) The same override from a terminal:
   `make ratelimit FI=lakeshore RPS=3`.
4. Fairness inside one FI (F48): **Reset demo** → **Lakeshore queue 2/s** → **Launch comparison** (three
   summit-payroll-5000, then Taylor's northline-bonus-500 3 s later). The 500-wire batch reaches Awaiting approval
   in about 9 s while the large ones take about 47 s. Measured against the same run with all Lakeshore batches on
   one fairness key: 22.5 s for the small batch. Say what it is: Fairness shortens the small batch's wait behind
   tasks already in the queue; it does not make the queue faster.
5. Optional (F08): Reset; upload at both banks; F08 → **Outage 120 s (Lakeshore)**; draft both. Prairie drafts in
   about 5 s while Lakeshore retries.
6. **Clear override** (or Reset).

Close with: *What happened* — two big batches from two customers of one bank shared that bank's Core capacity, a
small batch at the same bank was not stuck behind them, and the other bank never noticed. *Which primitive* — a
Core task queue per FI with a server-enforced rate limit (changeable without a deployment), and Fairness keyed by
batch ID within it. *What you no longer need* — custom throttling and cross-batch coordination code.

## D. Chaos tour (4 min) — F12, F13, F15, F10, F16

1. Jordan: upload **summit-payroll-10000** → Save as draft (Core load & queues → Lakeshore latency **5000** ms
   so it lasts; at 1500 ms the draft finishes during step 2). Run the steps back to back without waiting for each
   to recover.
2. F12 → **Kill worker-a**. Tiles running on worker-a stall until StartToClose (60 s), then continue on worker-b.
3. F13 → **Stop both workers 60 s**. Portal: "Live progress is temporarily unavailable. Your batch is safe…";
   Draft clicks say try again. They restart automatically; the batch resumes exactly where it stopped.
4. Overview: **restart** temporal. A few seconds of pause; everything resumes.
5. F10 → **Pause sql-lakeshore 40 s**: real 30 s SQL timeouts, retried.
6. F16 → **Stop claim-check backend 45 s**: chunks retry, then continue.
7. Wires tab: exactly 10,000 rows (9,994 Drafted, 6 Rejected). Control Center → Batches: one execution.

Close with: *What happened* — we crashed, stopped, froze, and restarted every moving part during a 10,000-wire
draft and the bank's records came out exact. *Which primitive* — durable execution: state lives in Temporal, every
Core write is idempotent, timeouts detect hangs. *What you no longer need* — recovery state kept outside
the orchestrator, and manual clean-up runbooks.

## E. Encryption and history tour (3 min) — F40, F41, F43

1. Control Center → Encryption → pick a batch → **Inspect**: left is what the Temporal Service stores
   (`binary/encrypted`, key `k2026a`), right is the decoded payload.
2. **Rotate key**. Draft a new batch; inspect it: the new key id. Inspect the old batch: still decodes.
3. CLI: `temporal workflow show -n bulkwire -w <id> --detailed` (ciphertext) vs `… --detailed --codec-endpoint
   http://localhost:8089`. Without `--detailed` the CLI lists event types only, not payloads.
4. Batches → **Temporal UI**: summary and details, every chunk Activity with its summary, the `draft`/`authorize`/
   `resolve` Updates with who and when, the "Approval window" timer, current details.
5. Batches table: an authorized 5,000-wire batch is ~320 events and about 85 KB of history; the ~2 MB of staged
   rows never enter it.

After the demo, `make demo-reset` first (it terminates batches encrypted with the temporary key), then restore the
committed keyring: `git checkout config/keyring.json`.

Close with: *What happened* — the orchestrator only ever saw ciphertext, yet engineers can read every batch end to
end. *Which primitive* — an encrypting DataConverter with a Codec Server, plus a claim check. *What you no longer
need* — plaintext payloads, and history-size workarounds like child workflows.

## F. We shipped a bug (4 min) — F38

Control Center → Versioning (guided steps):

1. **Start worker-v2** (built with a deliberate bug).
2. **Make v2 current**.
3. In the Lakeshore portal, draft a new batch and approve it (alex, then sam). It is pinned to v2. After Core moves to
   Authorizing, the Workflow Task fails repeatedly with "demo bug: authorize step crashed (worker-v2)" (Temporal UI
   → Pending Workflow Task, with stack trace). The batch keeps its approval and every drafted wire. Batches already
   running on v1 continue on v1.
4. **Start worker-v3** (the fix). 5. **Make v3 current**.
6. **Move pinned v2 batches to v3**: the stuck batch resumes and authorizes.
7. **Stop worker-v2**.

Close with: *What happened* — a broken release stopped one new batch, safely, and a fix moved only that batch
forward. *Which primitive* — Worker Versioning with Pinned workflows and a versioning override. *What you no longer
need* — patching in-flight workflows or restarting batches by hand.
