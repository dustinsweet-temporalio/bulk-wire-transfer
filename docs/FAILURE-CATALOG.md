# Failure catalog — presenter prep sheet

Generated from `control-ui/src/catalog.ts` (the Control Center's Failure catalog section). Every entry is
triggered from the **Control Center** (http://localhost:3100) or the **portals** (Lakeshore http://localhost:3001,
Prairie http://localhost:3002) unless marked *Narrated*. Defaults: latency 600 ms, chunk size 250, concurrency 2,
retry max interval 20 s, retry budget 30 min, approval window 24 h. Run **Reset** (Control Center → Reset)
before each scenario. Users and passwords: see RUNBOOK-DEMO.md.


## Core

### F01 — Core outage (503)

- **Trigger:** Core faults → Outage 45 s
- **Click path:** Control Center → Failure catalog → F01 → **Outage 45 s (Lakeshore)** while a Lakeshore batch is drafting (Scenario A: set latency 1500 ms first so the draft lasts).
- **You'll see:** Retrying tiles turn amber and show “retry #N · in Xs” (Describe); Core failures climb; no tile turns red; the batch completes whole.
- **Handled by:** Retry Policy without an attempt cap; backoff capped by MaximumInterval (https://docs.temporal.io/encyclopedia/retry-policies)
- **Say this:** “Nothing failed: Temporal kept retrying each chunk in place until Core came back, and no wire was lost or duplicated.”

### F02 — SQL timeout (real 30 s)

- **Trigger:** Core faults → SQL timeout ×3
- **Click path:** Control Center → F02 → **SQL timeout ×3**, then Save as draft in the Lakeshore portal.
- **You'll see:** Three calls take ~30 s and fail 504, then retry and succeed.
- **Handled by:** StartToClose 60 s, just above the 30 s SQL timeout; retries (https://docs.temporal.io/encyclopedia/retry-policies)
- **Say this:** “That was a real SqlClient timeout; the Activity timeout sits just above it, so a slow statement is retried, not abandoned.”

### F03 — Core hangs (process frozen)

- **Trigger:** Containers → pause core-lakeshore 90 s → unpause
- **Click path:** Start a draft → F03 → **Pause core-lakeshore 90 s** (auto-unpause after the countdown). Watch tiles show attempt 2 with “activity StartToClose timeout”.
- **You'll see:** In-flight calls hang; after 60 s StartToClose fires and the attempt is retried; work resumes on unpause.
- **Handled by:** StartToClose detects hung attempts (https://docs.temporal.io/encyclopedia/retry-policies)
- **Say this:** “A hang is worse than a crash; the 60-second StartToClose turns it into an ordinary retry.”

### F04 — Core crashes mid-request

- **Trigger:** Containers → kill core-lakeshore during draft → start
- **Click path:** Start a draft (latency 1500 ms) → F04 → **Kill core-lakeshore** → wait ~10 s → **Start core-lakeshore**. Then the Wires tab: 5,000 rows.
- **You'll see:** Connection errors and retries; after start, alreadyPresent shows no duplicates; totals are exact.
- **Handled by:** Idempotent procedures (unique key + NOT EXISTS, transaction rollback) + retries (https://temporal.io/blog/idempotency-and-durable-execution)
- **Say this:** “Retries are safe because every Core write is idempotent by wire ID; the totals come back exact.”

### F05 — Response lost after commit

- **Trigger:** Core faults → Lose responses ×2
- **Click path:** F05 → **Lose responses ×2** → Save as draft. Core load chart shows 2 failures; Wires tab shows exactly 5,000 rows.
- **You'll see:** Core stats show failed calls; retries report alreadyPresent=250; Core row count is exact.
- **Handled by:** Idempotent Activity; chunk-level totals (https://temporal.io/blog/idempotency-and-durable-execution)
- **Say this:** “Core committed but the answer was lost; the retry saw the rows already there and reported the true totals.”

### F06 — Generic Core bug (500)

- **Trigger:** Core faults → Error 500 ×3
- **Click path:** F06 → **Error 500 ×3** → Save as draft.
- **You'll see:** Retries, then success.
- **Handled by:** Retry Policy (https://docs.temporal.io/encyclopedia/retry-policies)
- **Say this:** “A transient server error is just another retry.”

### F07 — Core slow

- **Trigger:** Core latency → 2000 ms
- **Click path:** F07 → **Latency 2000 ms** during a draft; **Back to 600 ms** afterwards.
- **You'll see:** Progress slows; nothing fails.
- **Handled by:** Activity timeouts sized to Core; concurrency is a setting (https://docs.temporal.io/encyclopedia/retry-policies)
- **Say this:** “Slow is not broken: timeouts are sized to Core, so a slow core just takes longer.”

### F08 — One FI's Core down, other FI busy

- **Trigger:** Outage on Lakeshore while a Prairie batch drafts
- **Click path:** F08 → **Outage 120 s (Lakeshore)** → Prairie portal (Riley) → upload harvest-2000 → Save as draft → DRAFTED while Lakeshore is down.
- **You'll see:** The Prairie batch is unaffected; Lakeshore retries.
- **Handled by:** One Core per FI; tenant context in workflow input (https://docs.temporal.io/develop/task-queue-priority-fairness)
- **Say this:** “Each FI has its own Core and the FI travels in the workflow input, so one bank's outage never touches another's.”


## Database

### F09 — Database down

- **Trigger:** Containers → stop sql-lakeshore 60 s → start
- **Click path:** Start a draft → F09 → **Stop sql-lakeshore 60 s** (auto-start). Core chip turns red, then green.
- **You'll see:** Core health red, 503s, retries; resumes after start.
- **Handled by:** Retry Policy (https://docs.temporal.io/encyclopedia/retry-policies)
- **Say this:** “The database went away and came back; the batch simply waited and carried on.”

### F10 — Database hang

- **Trigger:** Containers → pause sql-lakeshore 40 s → unpause
- **Click path:** Start a draft → F10 → **Pause sql-lakeshore 40 s** (auto-unpause). Core stats show 504 SQL_TIMEOUT.
- **You'll see:** Real 30 s SQL command timeouts → 504 → retries.
- **Handled by:** 30 s SQL timeout + StartToClose + retries (https://docs.temporal.io/encyclopedia/retry-policies)
- **Say this:** “A frozen database produces real SQL timeouts, and each one is just a retry.”

### F11 — Deadlock victim (1205)

- **Trigger:** Narrated *(Narrated)*
- **Click path:** Narrated: open `core/CoreApi.Tests/SqlErrorMapperTests.cs` (`Transport_and_transient_errors_are_503_and_retryable` includes 1205).
- **You'll see:** —
- **Handled by:** Classified transient, retried (https://docs.temporal.io/encyclopedia/retry-policies)
- **Say this:** “A deadlock victim maps to 503 retryable, so the Activity just tries again.”
- **Evidence:** Evidence: core/CoreApi.Tests/SqlErrorMapperTests.cs — Transport_and_transient_errors_are_503_and_retryable(1205).


## Workers

### F12 — Worker crashes mid-batch

- **Trigger:** Containers → kill worker-a
- **Click path:** Start a 10k draft → F12 → **Kill worker-a**; later **Start worker-a**.
- **You'll see:** Some tiles pause (≤ 60 s), then continue on worker-b; the batch completes.
- **Handled by:** Activity StartToClose + Workflow Task retry on another worker (https://docs.temporal.io/workflows)
- **Say this:** “The other worker picked up exactly where the crashed one stopped.”

### F13 — All workers down

- **Trigger:** Stop worker-a and worker-b 60 s
- **Click path:** Start a draft → F13 → **Stop both workers 60 s**. Show the portal's info banner and that Draft now answers “try again”; they restart automatically.
- **You'll see:** Portal: “Live progress temporarily unavailable — batch is safe”; Draft clicks say “try again”; pollers 0; after start it resumes exactly where it stopped.
- **Handled by:** State lives in Temporal; Queries/Updates need a worker (honest limitation) (https://docs.temporal.io/workflows)
- **Say this:** “With no workers nothing runs, but nothing is lost: the state is in Temporal, not in the workers.”

### F14 — Rolling restart

- **Trigger:** Restart worker-a, then worker-b
- **Click path:** During a draft → F14 → **Restart worker-a**, then **Restart worker-b**.
- **You'll see:** No visible interruption.
- **Handled by:** Graceful shutdown; two workers (https://docs.temporal.io/workflows)
- **Say this:** “Deploying workers is a non-event.”


## Temporal & claim check

### F15 — Temporal Service restart

- **Trigger:** Containers → restart temporal
- **Click path:** During a draft → F15 → **Restart temporal**.
- **You'll see:** A few seconds of pause; all batches resume; histories intact.
- **Handled by:** Durable execution (dev server persists to disk; Cloud is HA) (https://docs.temporal.io/workflows)
- **Say this:** “The orchestrator restarted and every batch resumed from its history.”

### F16 — Claim-check store down

- **Trigger:** Stop redis (or rustfs / fake-gcs) 45 s during draft
- **Click path:** During a draft → F16 → **Stop claim-check backend 45 s** (redis / rustfs / fake-gcs per `BACKEND`).
- **You'll see:** Draft chunks retry (ClaimCheckUnavailable); uploads get 503; resumes; data intact (AOF).
- **Handled by:** Retryable error; durable store configuration (https://docs.temporal.io/troubleshooting/blob-size-limit-error)
- **Say this:** “The claim check is read-only after staging, so an outage only delays the next chunk.”

### F17 — Staged data expired

- **Trigger:** Paste a staged batch ID → Expire staged rows → Draft
- **Click path:** Lakeshore portal: upload a file (do not draft) → copy the batch ID from the URL → Control Center F17: paste it in **Batch ID**, click **Expire staged rows** → portal: Save as draft → chunks park → Morgan → exceptions → **Cancel batch**.
- **You'll see:** Chunks park CLAIM_CHECK_NOT_FOUND; ops cancels; the user re-uploads.
- **Handled by:** Non-retryable error → park → human decision (https://docs.temporal.io/design-patterns/resumable-activity)
- **Say this:** “Missing data is not retried forever: the chunk parks and a person decides.”


## API & approvals plumbing

### F18 — bulkwire-service down

- **Trigger:** Stop bulkwire-service 30 s during draft
- **Click path:** During a draft → F18 → **Stop bulkwire-service 30 s**. The portal page errors; Control Center → Batches keeps advancing.
- **You'll see:** The portal batch page errors; the Control Center shows drafting continuing; on start the portal is further along.
- **Handled by:** Orchestration is independent of the API (https://docs.temporal.io/workflows)
- **Say this:** “The API is just a door; the work continued without it.”

### F19 — Approval notification lost

- **Trigger:** Core faults → Callback fail ×1, then approve
- **Click path:** F19 → **Callback fail ×1** → approver approves a DRAFTED batch → “Release notification failed — Retry” → **Retry**.
- **You'll see:** “Approval recorded. Release notification failed — Retry”; batch still DRAFTED; Retry → AUTHORIZING.
- **Handled by:** Idempotent re-send; Update ID = approvalRef (https://docs.temporal.io/encyclopedia/workflow-message-passing)
- **Say this:** “Re-sending the approval is safe: the approval reference is the Update ID.”

### F20 — Duplicate approval callback

- **Trigger:** Click Retry notification after success
- **Click path:** After F19 succeeded, click **Retry** again (or re-approve): still NOTIFIED, one authorization.
- **You'll see:** One authorization; no second run.
- **Handled by:** Update ID dedup (https://docs.temporal.io/encyclopedia/workflow-message-passing)
- **Say this:** “The duplicate attached to the same Update; nothing ran twice.”


## Business conditions

### F21 — Account hold during draft

- **Trigger:** Ops (Morgan) → place hold on ••0033 → Draft
- **Click path:** Jordan uploads summit-payroll-5000 → F21 **Place hold on ••0033** (or Morgan → Accounts) → Jordan **Save as draft** → Cold Chain tiles turn red → Morgan → Bulk wire exceptions → **Release hold** → **Retry chunk** on each.
- **You'll see:** Those chunks park ACCOUNT_ON_HOLD; others finish; ops releases the hold → Retry → resumes.
- **Handled by:** Non-retryable error → Resumable Activity (park + resolve Update) (https://docs.temporal.io/design-patterns/resumable-activity)
- **Say this:** “Retrying can't fix a hold, so the chunk waits for a person, then resumes on their decision.”

### F22 — Hold after draft, before authorization

- **Trigger:** Place hold when DRAFTED → approve
- **Click path:** With a DRAFTED batch: F22 **Place hold on ••0033** → approve twice → chunks park in AUTHORIZING → release + Retry.
- **You'll see:** Chunks park in AUTHORIZING; release + Retry.
- **Handled by:** Same, in the authorize phase (https://docs.temporal.io/design-patterns/resumable-activity)
- **Say this:** “The same park-and-resolve works after approval; only cancel is off the table.”

### F23 — Daily limit exceeded

- **Trigger:** Upload summit-cold-chain-over-limit → Draft
- **Click path:** Lakeshore portal → upload summit-cold-chain-over-limit → Save as draft → Groups panel: Denied “Daily limit exceeded”.
- **You'll see:** Group DENY, wires written as Rejected; nothing parks.
- **Handled by:** Business Outcome as Result (https://docs.temporal.io/design-patterns/non-retryable-errors)
- **Say this:** “A policy denial is an answer, not an error: it's recorded, not retried.”

### F24 — Blocked beneficiary

- **Trigger:** Any payroll sample
- **Click path:** Any payroll sample → Rejections panel: 3 BENEFICIARY_BLOCKED.
- **You'll see:** 3 wires Rejected BENEFICIARY_BLOCKED.
- **Handled by:** Business Outcome as Result (https://docs.temporal.io/design-patterns/non-retryable-errors)
- **Say this:** “Screening rejections come back as results with exact counts.”

### F25 — Invalid file

- **Trigger:** Upload summit-vendors-with-errors
- **Click path:** Lakeshore portal → upload summit-vendors-with-errors → red banner, errors table, Draft disabled.
- **You'll see:** INVALID, 14 errors, Draft refused, re-upload.
- **Handled by:** Synchronous staging (https://docs.temporal.io/troubleshooting/blob-size-limit-error)
- **Say this:** “Bad files are caught before anything is orchestrated.”

### F26 — Retry budget exhausted

- **Trigger:** Draft defaults → retry budget 60 s; Draft; then outage 120 s
- **Click path:** F26 → **Lakeshore retry budget 60 s** → Save as draft (Lakeshore latency 1500 ms helps) → **Outage 120 s** → the in-flight chunks park RETRY_BUDGET_EXHAUSTED (after ~60 s) → after the outage, Morgan **Retry chunk**. Reset draft defaults afterwards.
- **You'll see:** In-flight chunks park RETRY_BUDGET_EXHAUSTED with the last error after about a minute; ops Retry after the outage → completes. (Outage before Draft parks the batch's first Core step instead: Retry step.)
- **Handled by:** Fixed Wall-Time Retries + park (https://docs.temporal.io/design-patterns/fixed-wall-time-retries)
- **Say this:** “The budget is wall time, not attempts; when it runs out the chunk parks instead of failing.”

### F27 — Operator rejects a parked chunk

- **Trigger:** In F21, choose Reject chunk wires
- **Click path:** In F21, choose **Reject chunk wires** for one parked chunk (with a note).
- **You'll see:** Those wires Rejected in Core; the batch continues.
- **Handled by:** Resolve REJECT_CHUNK (https://docs.temporal.io/design-patterns/resumable-activity)
- **Say this:** “The operator's decision is an Update; the workflow writes the rejection through Core.”

### F28 — Cancel mid-draft

- **Trigger:** Initiator → Cancel batch while DRAFTING
- **Click path:** Jordan → a DRAFTING batch → **Cancel batch** (reason).
- **You'll see:** In-flight chunks finish; nothing new starts; Core marks Cancelled.
- **Handled by:** cancel Update + validator (https://docs.temporal.io/encyclopedia/workflow-message-passing)
- **Say this:** “Cancel is an Update the workflow validates; in-flight work finishes cleanly.”


## Approvals

### F29 — Dual control

- **Trigger:** Alex approves, then Sam
- **Click path:** Alex (window 2) → batch → **Review and approve** → TAC 246810 → “1 of 2”; Sam (window 3) → same → Approved.
- **You'll see:** “1 of 2” → Approved → AUTHORIZING.
- **Handled by:** Core decides; the workflow waits durably (https://docs.temporal.io/design-patterns/approval)
- **Say this:** “Core keeps the approval rules; the workflow just waits, for free, for Core's answer.”

### F30 — Denied

- **Trigger:** Approver → Deny
- **Click path:** Approver → **Review and approve** → **Deny…** → reason.
- **You'll see:** DENIED; Core wires Denied.
- **Handled by:** deny Update (https://docs.temporal.io/encyclopedia/workflow-message-passing)
- **Say this:** “A denial closes the batch everywhere, consistently.”

### F31 — Approval never arrives

- **Trigger:** Draft defaults → approval window 2 m
- **Click path:** F31 → **Lakeshore approval window 2 m** → draft a batch → after 2 minutes: Expired. Reset draft defaults afterwards.
- **You'll see:** EXPIRED after 2 min; Core wires Expired.
- **Handled by:** Durable timer (https://docs.temporal.io/design-patterns/approval)
- **Say this:** “The deadline is a durable timer inside the batch: no cron, no sweeper.”

### F32 — Late approval after close

- **Trigger:** Approve an expired/denied batch
- **Click path:** After F31 or F30: approver tries to approve → “batch is closed”.
- **You'll see:** Core refuses (“batch is closed”); even a forced callback can't restart it.
- **Handled by:** Core closed-batch guard; REJECT_DUPLICATE reuse policy (https://docs.temporal.io/workflow-execution/workflowid-runid)
- **Say this:** “A closed batch's Workflow ID can never be reused, so a late approval cannot resurrect it.”

### F33 — Approve before draft

- **Trigger:** Approver → “Approve & release” on a staged batch
- **Click path:** Alex → a staged batch (not drafted) → **Approve & release** → Sam does the same → one workflow drafts, then authorizes.
- **You'll see:** One workflow drafts, then authorizes.
- **Handled by:** Update-with-Start authorize (https://docs.temporal.io/sending-messages#update-with-start)
- **Say this:** “Whichever arrives first starts the one workflow; the other attaches to it.”


## Duplicates, load & tenancy

### F34 — Double click / two tabs

- **Trigger:** Click Save as draft in two tabs
- **Click path:** Open the same staged batch in two tabs → click **Save as draft** in both.
- **You'll see:** One workflow, same live view in both.
- **Handled by:** Workflow ID + USE_EXISTING + Update IDs (https://docs.temporal.io/sending-messages#update-with-start)
- **Say this:** “Both clicks land on the same execution.”

### F35 — Retry storm

- **Trigger:** Load → Retry storm (20 concurrent drafts, distinct keys)
- **Click path:** F35 → **Fire 20 drafts** (or Core load & fairness → Retry storm): 20 responses, 1 execution.
- **You'll see:** 20 responses, 1 execution.
- **Handled by:** Business Workflow ID (https://docs.temporal.io/workflow-execution/workflowid-runid)
- **Say this:** “Twenty requests with twenty different keys still produce exactly one workflow.”

### F36 — Payroll day, two users at one FI

- **Trigger:** Load → Payroll day; then enable the per-FI rate limit
- **Click path:** Core load & fairness → **Launch payroll day** → watch Lakeshore calls/s rise → **Enable limit** (3/s) → Lakeshore flattens at 3/s; Prairie unaffected → **Remove limit**.
- **You'll see:** Without the limit, Lakeshore Core calls/s and in-flight rise with each batch. With it, Lakeshore is capped at the set rate regardless of batch count; both Lakeshore batches keep progressing; Prairie runs on its own allowance, not queued behind Lakeshore. The batches finish in seconds without the limit, so launch once without it and once with it.
- **Handled by:** Fairness key = FI + per-key rate limit, enforced by the Temporal Service (https://docs.temporal.io/develop/task-queue-priority-fairness)
- **Say this:** “The cap is per bank and enforced by Temporal, across every batch and every worker.”

### F37 — Concurrency is a setting

- **Trigger:** Draft defaults concurrency 1, then 4
- **Click path:** F37 → **Lakeshore concurrency 1** → draft → Core max in-flight 1; then **concurrency 4** → draft → 4.
- **You'll see:** Core max in-flight 1 vs 4 (latency is fixed; real gains must be measured).
- **Handled by:** Semaphore (https://pkg.go.dev/go.temporal.io/sdk/workflow#NewSemaphore)
- **Say this:** “Parallelism is one number in the workflow input, not an architecture.”


## Deployments, security, observability

### F38 — Shipped a workflow bug

- **Trigger:** Versioning panel steps 1–7
- **Click path:** Versioning section: steps 1–7 (see RUNBOOK-DEMO.md Scenario F).
- **You'll see:** The v2 batch stops before authorize (Workflow Task failure with stack trace), no data lost, v1 batches unaffected; v3 + override resumes it.
- **Handled by:** Worker Versioning (Pinned) + versioning override (https://docs.temporal.io/production-deployment/worker-deployments/worker-versioning)
- **Say this:** “Pinned batches never run code they didn't start with, and a targeted override moves only the broken ones to the fix.”

### F39 — Non-determinism before deploy

- **Trigger:** Narrated *(Narrated)*
- **Click path:** Narrated: `make replay` (passes) and `make replay-nde` (fails with a non-determinism error).
- **You'll see:** —
- **Handled by:** Replay tests in CI (https://docs.temporal.io/develop/go/testing-suite#replay)
- **Say this:** “Replaying real histories in CI catches a breaking change before it ships.”
- **Evidence:** Evidence: `make replay` (passes on captured histories) and `make replay-nde` (fails on a reordered workflow: non-determinism error).

### F40 — Data at rest

- **Trigger:** Encryption → inspector; Rotate key; draft a new batch
- **Click path:** Encryption section → pick a batch → **Inspect** → **Rotate key** → draft a new batch → inspect both.
- **You'll see:** Stored payloads are ciphertext with a key id; after rotation new batches use the new key, old ones still decode.
- **Handled by:** Encrypting DataConverter + Codec Server (https://docs.temporal.io/payload-codec)
- **Say this:** “Temporal only ever stores ciphertext; the keys never leave your side.”

### F41 — See the whole wire flow

- **Trigger:** Batches → Open in Temporal UI
- **Click path:** Batches → **Temporal UI** link on any batch → read summary, details, Activity summaries, Updates, timer.
- **You'll see:** Summary, details, every chunk Activity with its summary, Updates (draft/authorize/resolve) with who/when, the approval timer, current details; one history.
- **Handled by:** Workflow user metadata; history in one execution (https://docs.temporal.io/workflow-execution)
- **Say this:** “The whole story of a batch is one readable history.”

### F42 — Retry alerting

- **Trigger:** During F01/F09
- **Click path:** During F01 or F09 → Alerts & metrics: the counter rises for that FI.
- **You'll see:** The alert counter rises per FI.
- **Handled by:** Retry Alerting via Metrics (https://docs.temporal.io/design-patterns/retry-metrics)
- **Say this:** “On-call hears about a stuck Core from a metric long before any retry budget runs out.”

### F43 — Payload and history limits

- **Trigger:** Batches table: history size and event count
- **Click path:** Batches table → Events / History columns.
- **You'll see:** ~300 events and about 100 KB (measured) for 5,000 wires; the rows stay in the claim check.
- **Handled by:** Claim Check; Basic Workflow Batch (https://docs.temporal.io/troubleshooting/blob-size-limit-error)
- **Say this:** “References in, counts out: 5,000 wires cost a few hundred events.”

### F44 — Operator pauses retries during a long outage

- **Trigger:** CLI (Public Preview) *(Narrated)*
- **Click path:** Narrated CLI (Public Preview; the CLI labels it Experimental): `temporal activity pause --namespace bulkwire -w <workflowId> --activity-id draft/g02-c01/r0`, then `temporal activity unpause …`.
- **You'll see:** Retries stop until unpaused (effective after the current attempt; no heartbeats).
- **Handled by:** Activity Operations (https://docs.temporal.io/activity-operations)
- **Say this:** “An operator can pause one Activity's retries without touching the rest of the batch.”
- **Evidence:** temporal activity pause --namespace bulkwire -w <workflowId> --activity-id <activityId>   (then: temporal activity unpause …). Takes effect after the current attempt: these Activities do not heartbeat.
