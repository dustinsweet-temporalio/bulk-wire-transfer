// Failure catalog content: every failure condition F01–F44 the demo can show (docs/FAILURE-CATALOG.md is generated
// from this file). Each card: trigger action(s), what you'll see, what handles it (Temporal primitive + link), and
// one sentence to say.
import type { FI } from "./api";

export type Action =
  | { kind: "fault"; fi: FI; mode: string; durationSeconds?: number; count?: number; label?: string }
  | { kind: "latency"; fi: FI; ms: number; label?: string }
  | { kind: "container"; service: string; action: string; revertAfter?: number; revertAction?: string; label?: string }
  | { kind: "containers"; services: string[]; action: string; revertAfter?: number; revertAction?: string; label: string }
  | { kind: "backend"; action: string; revertAfter: number; revertAction: string; label: string }
  | { kind: "defaults"; fi: FI | "both"; patch: Record<string, unknown>; label: string }
  | { kind: "hold"; fi: FI; account: string; onHold: boolean; label: string }
  | { kind: "expire"; label: string }
  | { kind: "retryStorm"; label: string }
  | { kind: "payrollDay"; label: string }
  | { kind: "fairness"; rpsPerFi: number; label: string }
  | { kind: "rotate"; label: string }
  | { kind: "goto"; section: string; label: string }
  | { kind: "link"; href: string; label: string };

export interface Entry {
  id: string;
  category: Category;
  title: string;
  trigger: string;
  actions: Action[];
  see: string;
  handled: string;
  link: string;
  say: string;
  narrated?: string;
}

export type Category = "Core" | "Database" | "Workers" | "Temporal & claim check" | "API & approvals plumbing" | "Business conditions" | "Approvals" | "Duplicates, load & tenancy" | "Deployments, security, observability";

const L3001 = "http://localhost:3001";
const L3002 = "http://localhost:3002";
const D = {
  retry: "https://docs.temporal.io/encyclopedia/retry-policies",
  wallTime: "https://docs.temporal.io/design-patterns/fixed-wall-time-retries",
  nonRetryable: "https://docs.temporal.io/design-patterns/non-retryable-errors",
  resumable: "https://docs.temporal.io/design-patterns/resumable-activity",
  idempotency: "https://temporal.io/blog/idempotency-and-durable-execution",
  approval: "https://docs.temporal.io/design-patterns/approval",
  uws: "https://docs.temporal.io/sending-messages#update-with-start",
  wfid: "https://docs.temporal.io/workflow-execution/workflowid-runid",
  fairness: "https://docs.temporal.io/develop/task-queue-priority-fairness",
  semaphore: "https://pkg.go.dev/go.temporal.io/sdk/workflow#NewSemaphore",
  versioning: "https://docs.temporal.io/production-deployment/worker-deployments/worker-versioning",
  codec: "https://docs.temporal.io/payload-codec",
  metadata: "https://docs.temporal.io/workflow-execution",
  metrics: "https://docs.temporal.io/design-patterns/retry-metrics",
  claimCheck: "https://docs.temporal.io/troubleshooting/blob-size-limit-error",
  activityOps: "https://docs.temporal.io/activity-operations",
  durable: "https://docs.temporal.io/workflows",
  updates: "https://docs.temporal.io/encyclopedia/workflow-message-passing",
  replay: "https://docs.temporal.io/develop/go/testing-suite#replay",
};

export const catalog: Entry[] = [
  // ---- Core banking system, per FI ----
  { id: "F01", category: "Core", title: "Core outage (503)", trigger: "Core faults → Outage 45 s",
    actions: [{ kind: "fault", fi: "lakeshore", mode: "outage", durationSeconds: 45, label: "Outage 45 s (Lakeshore)" }],
    see: "Tiles show “Retrying · attempt N · next in Xs” (Describe); Core failures climb; no tile turns red; the batch completes whole.",
    handled: "Retry Policy without an attempt cap; backoff capped by MaximumInterval", link: D.retry,
    say: "Nothing failed: Temporal kept retrying each chunk in place until Core came back, and no wire was lost or duplicated." },
  { id: "F02", category: "Core", title: "SQL timeout (real 30 s)", trigger: "Core faults → SQL timeout ×3",
    actions: [{ kind: "fault", fi: "lakeshore", mode: "sql-timeout", count: 3, label: "SQL timeout ×3" }],
    see: "Three calls take ~30 s and fail 504, then retry and succeed.",
    handled: "StartToClose 60 s, just above the 30 s SQL timeout; retries", link: D.retry,
    say: "That was a real SqlClient timeout; the Activity timeout sits just above it, so a slow statement is retried, not abandoned." },
  { id: "F03", category: "Core", title: "Core hangs (process frozen)", trigger: "Containers → pause core-lakeshore 90 s → unpause",
    actions: [{ kind: "container", service: "core-lakeshore", action: "pause", revertAfter: 90, revertAction: "unpause", label: "Pause core-lakeshore 90 s" }],
    see: "In-flight calls hang; after 60 s StartToClose fires and the attempt is retried; work resumes on unpause.",
    handled: "StartToClose detects hung attempts", link: D.retry,
    say: "A hang is worse than a crash; the 60-second StartToClose turns it into an ordinary retry." },
  { id: "F04", category: "Core", title: "Core crashes mid-request", trigger: "Containers → kill core-lakeshore during draft → start",
    actions: [{ kind: "container", service: "core-lakeshore", action: "kill", label: "Kill core-lakeshore" }, { kind: "container", service: "core-lakeshore", action: "start", label: "Start core-lakeshore" }],
    see: "Connection errors and retries; after start, alreadyPresent shows no duplicates; totals are exact.",
    handled: "Idempotent procedures (unique key + NOT EXISTS, transaction rollback) + retries", link: D.idempotency,
    say: "Retries are safe because every Core write is idempotent by wire ID; the totals come back exact." },
  { id: "F05", category: "Core", title: "Response lost after commit", trigger: "Core faults → Lose responses ×2",
    actions: [{ kind: "fault", fi: "lakeshore", mode: "lose-response", count: 2, label: "Lose responses ×2" }],
    see: "Core stats show failed calls; retries report alreadyPresent=250; Core row count is exact.",
    handled: "Idempotent Activity; chunk-level totals", link: D.idempotency,
    say: "Core committed but the answer was lost; the retry saw the rows already there and reported the true totals." },
  { id: "F06", category: "Core", title: "Generic Core bug (500)", trigger: "Core faults → Error 500 ×3",
    actions: [{ kind: "fault", fi: "lakeshore", mode: "error-500", count: 3, label: "Error 500 ×3" }],
    see: "Retries, then success.", handled: "Retry Policy", link: D.retry,
    say: "A transient server error is just another retry." },
  { id: "F07", category: "Core", title: "Core slow", trigger: "Core latency → 2000 ms",
    actions: [{ kind: "latency", fi: "lakeshore", ms: 2000, label: "Latency 2000 ms" }, { kind: "latency", fi: "lakeshore", ms: 600, label: "Back to 600 ms" }],
    see: "Progress slows; nothing fails.", handled: "Activity timeouts sized to Core; concurrency is a setting", link: D.retry,
    say: "Slow is not broken: timeouts are sized to Core, so a slow core just takes longer." },
  { id: "F08", category: "Core", title: "One FI's Core down, other FI busy", trigger: "Outage on Lakeshore while a Prairie batch drafts",
    actions: [{ kind: "fault", fi: "lakeshore", mode: "outage", durationSeconds: 120, label: "Outage 120 s (Lakeshore)" }, { kind: "link", href: `${L3002}/payments/bulk-wire`, label: "Prairie portal: upload harvest-2000" }],
    see: "The Prairie batch is unaffected; Lakeshore retries.", handled: "One Core per FI; tenant context in workflow input", link: D.fairness,
    say: "Each FI has its own Core and the FI travels in the workflow input, so one bank's outage never touches another's." },
  // ---- Core database, per FI ----
  { id: "F09", category: "Database", title: "Database down", trigger: "Containers → stop sql-lakeshore 60 s → start",
    actions: [{ kind: "container", service: "sql-lakeshore", action: "stop", revertAfter: 60, revertAction: "start", label: "Stop sql-lakeshore 60 s" }],
    see: "Core health red, 503s, retries; resumes after start.", handled: "Retry Policy", link: D.retry,
    say: "The database went away and came back; the batch simply waited and carried on." },
  { id: "F10", category: "Database", title: "Database hang", trigger: "Containers → pause sql-lakeshore 40 s → unpause",
    actions: [{ kind: "container", service: "sql-lakeshore", action: "pause", revertAfter: 40, revertAction: "unpause", label: "Pause sql-lakeshore 40 s" }],
    see: "Real 30 s SQL command timeouts → 504 → retries.", handled: "30 s SQL timeout + StartToClose + retries", link: D.retry,
    say: "A frozen database produces real SQL timeouts, and each one is just a retry." },
  { id: "F11", category: "Database", title: "Deadlock victim (1205)", trigger: "Narrated", actions: [],
    narrated: "Evidence: core/CoreApi.Tests/SqlErrorMapperTests.cs — Transport_and_transient_errors_are_503_and_retryable(1205).",
    see: "—", handled: "Classified transient, retried", link: D.retry,
    say: "A deadlock victim maps to 503 retryable, so the Activity just tries again." },
  // ---- Workers ----
  { id: "F12", category: "Workers", title: "Worker crashes mid-batch", trigger: "Containers → kill worker-a",
    actions: [{ kind: "container", service: "worker-a", action: "kill", label: "Kill worker-a" }, { kind: "container", service: "worker-a", action: "start", label: "Start worker-a" }],
    see: "Some tiles pause (≤ 60 s), then continue on worker-b; the batch completes.", handled: "Activity StartToClose + Workflow Task retry on another worker", link: D.durable,
    say: "The other worker picked up exactly where the crashed one stopped." },
  { id: "F13", category: "Workers", title: "All workers down", trigger: "Stop worker-a and worker-b 60 s",
    actions: [{ kind: "containers", services: ["worker-a", "worker-b"], action: "stop", revertAfter: 60, revertAction: "start", label: "Stop both workers 60 s" }],
    see: "Portal: “Live progress temporarily unavailable — batch is safe”; Draft clicks say “try again”; pollers 0; after start it resumes exactly where it stopped.",
    handled: "State lives in Temporal; Queries/Updates need a worker (honest limitation)", link: D.durable,
    say: "With no workers nothing runs, but nothing is lost: the state is in Temporal, not in the workers." },
  { id: "F14", category: "Workers", title: "Rolling restart", trigger: "Restart worker-a, then worker-b",
    actions: [{ kind: "container", service: "worker-a", action: "restart", label: "Restart worker-a" }, { kind: "container", service: "worker-b", action: "restart", label: "Restart worker-b" }],
    see: "No visible interruption.", handled: "Graceful shutdown; two workers", link: D.durable,
    say: "Deploying workers is a non-event." },
  // ---- Temporal and claim check ----
  { id: "F15", category: "Temporal & claim check", title: "Temporal Service restart", trigger: "Containers → restart temporal",
    actions: [{ kind: "container", service: "temporal", action: "restart", label: "Restart temporal" }],
    see: "A few seconds of pause; all batches resume; histories intact.", handled: "Durable execution (dev server persists to disk; Cloud is HA)", link: D.durable,
    say: "The orchestrator restarted and every batch resumed from its history." },
  { id: "F16", category: "Temporal & claim check", title: "Claim-check store down", trigger: "Stop redis (or rustfs / fake-gcs) 45 s during draft",
    actions: [{ kind: "backend", action: "stop", revertAfter: 45, revertAction: "start", label: "Stop claim-check backend 45 s" }],
    see: "Draft chunks retry (ClaimCheckUnavailable); uploads get 503; resumes; data intact (AOF).", handled: "Retryable error; durable store configuration", link: D.claimCheck,
    say: "The claim check is read-only after staging, so an outage only delays the next chunk." },
  { id: "F17", category: "Temporal & claim check", title: "Staged data expired", trigger: "Claim check → Expire staged data for a staged batch → Draft",
    actions: [{ kind: "expire", label: "Expire staged rows" }],
    see: "Chunks park CLAIM_CHECK_NOT_FOUND; ops cancels; the user re-uploads.", handled: "Non-retryable error → park → human decision", link: D.resumable,
    say: "Missing data is not retried forever: the chunk parks and a person decides." },
  // ---- Orchestration API and approvals plumbing ----
  { id: "F18", category: "API & approvals plumbing", title: "bulkwire-service down", trigger: "Stop bulkwire-service 30 s during draft",
    actions: [{ kind: "container", service: "bulkwire-service", action: "stop", revertAfter: 30, revertAction: "start", label: "Stop bulkwire-service 30 s" }],
    see: "The portal batch page errors; the Control Center shows drafting continuing; on start the portal is further along.", handled: "Orchestration is independent of the API", link: D.durable,
    say: "The API is just a door; the work continued without it." },
  { id: "F19", category: "API & approvals plumbing", title: "Approval notification lost", trigger: "Core faults → Callback fail ×1, then approve",
    actions: [{ kind: "fault", fi: "lakeshore", mode: "callback-fail", count: 1, label: "Callback fail ×1" }],
    see: "“Approval recorded. Release notification failed — Retry”; batch still DRAFTED; Retry → AUTHORIZING.", handled: "Idempotent re-send; Update ID = approvalRef", link: D.updates,
    say: "Re-sending the approval is safe: the approval reference is the Update ID." },
  { id: "F20", category: "API & approvals plumbing", title: "Duplicate approval callback", trigger: "Click Retry notification after success",
    actions: [{ kind: "link", href: L3001, label: "Lakeshore portal" }],
    see: "One authorization; no second run.", handled: "Update ID dedup", link: D.updates,
    say: "The duplicate attached to the same Update; nothing ran twice." },
  // ---- Business conditions ----
  { id: "F21", category: "Business conditions", title: "Account hold during draft", trigger: "Ops (Morgan) → place hold on ••0033 → Draft",
    actions: [{ kind: "hold", fi: "lakeshore", account: "4401220033", onHold: true, label: "Place hold on ••0033" }, { kind: "hold", fi: "lakeshore", account: "4401220033", onHold: false, label: "Release hold" }],
    see: "Those chunks park ACCOUNT_ON_HOLD; others finish; ops releases the hold → Retry → resumes.", handled: "Non-retryable error → Resumable Activity (park + resolve Update)", link: D.resumable,
    say: "Retrying can't fix a hold, so the chunk waits for a person, then resumes on their decision." },
  { id: "F22", category: "Business conditions", title: "Hold after draft, before authorization", trigger: "Place hold when DRAFTED → approve",
    actions: [{ kind: "hold", fi: "lakeshore", account: "4401220033", onHold: true, label: "Place hold on ••0033" }, { kind: "hold", fi: "lakeshore", account: "4401220033", onHold: false, label: "Release hold" }],
    see: "Chunks park in AUTHORIZING; release + Retry.", handled: "Same, in the authorize phase", link: D.resumable,
    say: "The same park-and-resolve works after approval; only cancel is off the table." },
  { id: "F23", category: "Business conditions", title: "Daily limit exceeded", trigger: "Upload summit-cold-chain-over-limit → Draft",
    actions: [{ kind: "link", href: `${L3001}/payments/bulk-wire`, label: "Lakeshore portal: upload" }],
    see: "Group DENY, wires written as Rejected; nothing parks.", handled: "Business Outcome as Result", link: D.nonRetryable,
    say: "A policy denial is an answer, not an error: it's recorded, not retried." },
  { id: "F24", category: "Business conditions", title: "Blocked beneficiary", trigger: "Any payroll sample",
    actions: [{ kind: "link", href: `${L3001}/payments/bulk-wire`, label: "Lakeshore portal" }],
    see: "3 wires Rejected BENEFICIARY_BLOCKED.", handled: "Business Outcome as Result", link: D.nonRetryable,
    say: "Screening rejections come back as results with exact counts." },
  { id: "F25", category: "Business conditions", title: "Invalid file", trigger: "Upload summit-vendors-with-errors",
    actions: [{ kind: "link", href: `${L3001}/payments/bulk-wire`, label: "Lakeshore portal" }],
    see: "INVALID, 14 errors, Draft refused, re-upload.", handled: "Synchronous staging", link: D.claimCheck,
    say: "Bad files are caught before anything is orchestrated." },
  { id: "F26", category: "Business conditions", title: "Retry budget exhausted", trigger: "Draft defaults → retry budget 60 s; outage 120 s; Draft",
    actions: [{ kind: "defaults", fi: "lakeshore", patch: { retryBudget: "60s" }, label: "Lakeshore retry budget 60 s" }, { kind: "fault", fi: "lakeshore", mode: "outage", durationSeconds: 120, label: "Outage 120 s" }],
    see: "Chunks park RETRY_BUDGET_EXHAUSTED with the last error; ops Retry after the outage → completes.", handled: "Fixed Wall-Time Retries + park", link: D.wallTime,
    say: "The budget is wall time, not attempts; when it runs out the chunk parks instead of failing." },
  { id: "F27", category: "Business conditions", title: "Operator rejects a parked chunk", trigger: "In F21, choose Reject chunk wires",
    actions: [{ kind: "link", href: `${L3001}/ops/exceptions`, label: "Morgan: exceptions" }],
    see: "Those wires Rejected in Core; the batch continues.", handled: "Resolve REJECT_CHUNK", link: D.resumable,
    say: "The operator's decision is an Update; the workflow writes the rejection through Core." },
  { id: "F28", category: "Business conditions", title: "Cancel mid-draft", trigger: "Initiator → Cancel batch while DRAFTING",
    actions: [{ kind: "link", href: L3001, label: "Lakeshore portal" }],
    see: "In-flight chunks finish; nothing new starts; Core marks Cancelled.", handled: "cancel Update + validator", link: D.updates,
    say: "Cancel is an Update the workflow validates; in-flight work finishes cleanly." },
  // ---- Approvals ----
  { id: "F29", category: "Approvals", title: "Dual control", trigger: "Alex approves, then Sam",
    actions: [{ kind: "link", href: L3001, label: "Lakeshore portal" }],
    see: "“1 of 2” → Approved → AUTHORIZING.", handled: "Core decides; the workflow waits durably", link: D.approval,
    say: "Core keeps the approval rules; the workflow just waits, for free, for Core's answer." },
  { id: "F30", category: "Approvals", title: "Denied", trigger: "Approver → Deny",
    actions: [{ kind: "link", href: L3001, label: "Lakeshore portal" }],
    see: "DENIED; Core wires Denied.", handled: "deny Update", link: D.updates, say: "A denial closes the batch everywhere, consistently." },
  { id: "F31", category: "Approvals", title: "Approval never arrives", trigger: "Draft defaults → approval window 2 m",
    actions: [{ kind: "defaults", fi: "lakeshore", patch: { approvalWindow: "2m" }, label: "Lakeshore approval window 2 m" }],
    see: "EXPIRED after 2 min; Core wires Expired.", handled: "Durable timer", link: D.approval,
    say: "The deadline is a durable timer inside the batch: no cron, no sweeper." },
  { id: "F32", category: "Approvals", title: "Late approval after close", trigger: "Approve an expired/denied batch",
    actions: [{ kind: "link", href: L3001, label: "Lakeshore portal" }],
    see: "Core refuses (“batch is closed”); even a forced callback can't restart it.", handled: "Core closed-batch guard; REJECT_DUPLICATE reuse policy", link: D.wfid,
    say: "A closed batch's Workflow ID can never be reused, so a late approval cannot resurrect it." },
  { id: "F33", category: "Approvals", title: "Approve before draft", trigger: "Approver → “Approve & release” on a staged batch",
    actions: [{ kind: "link", href: L3001, label: "Lakeshore portal" }],
    see: "One workflow drafts, then authorizes.", handled: "Update-with-Start authorize", link: D.uws,
    say: "Whichever arrives first starts the one workflow; the other attaches to it." },
  // ---- Duplicates, load, and tenancy ----
  { id: "F34", category: "Duplicates, load & tenancy", title: "Double click / two tabs", trigger: "Click Save as draft in two tabs",
    actions: [{ kind: "link", href: L3001, label: "Lakeshore portal" }],
    see: "One workflow, same live view in both.", handled: "Workflow ID + USE_EXISTING + Update IDs", link: D.uws,
    say: "Both clicks land on the same execution." },
  { id: "F35", category: "Duplicates, load & tenancy", title: "Retry storm", trigger: "Load → Retry storm (20 concurrent drafts, distinct keys)",
    actions: [{ kind: "retryStorm", label: "Fire 20 drafts" }],
    see: "20 responses, 1 execution.", handled: "Business Workflow ID", link: D.wfid,
    say: "Twenty requests with twenty different keys still produce exactly one workflow." },
  { id: "F36", category: "Duplicates, load & tenancy", title: "Payroll day, two users at one FI", trigger: "Load → Payroll day; then enable the per-FI rate limit",
    actions: [{ kind: "payrollDay", label: "Launch payroll day" }, { kind: "fairness", rpsPerFi: 3, label: "Enable 3/s per FI" }, { kind: "fairness", rpsPerFi: 0, label: "Remove limit" }],
    see: "Without the limit, Lakeshore Core calls/s and in-flight rise with each batch. With it, Lakeshore is capped at the set rate regardless of batch count; both Lakeshore batches keep progressing; Prairie is unaffected.",
    handled: "Fairness key = FI + per-key rate limit, enforced by the Temporal Service", link: D.fairness,
    say: "The cap is per bank and enforced by Temporal, across every batch and every worker." },
  { id: "F37", category: "Duplicates, load & tenancy", title: "Concurrency is a setting", trigger: "Draft defaults concurrency 1, then 4",
    actions: [{ kind: "defaults", fi: "lakeshore", patch: { concurrency: 1 }, label: "Lakeshore concurrency 1" }, { kind: "defaults", fi: "lakeshore", patch: { concurrency: 4 }, label: "Lakeshore concurrency 4" }],
    see: "Core max in-flight 1 vs 4 (latency is fixed; real gains must be measured).", handled: "Semaphore", link: D.semaphore,
    say: "Parallelism is one number in the workflow input, not an architecture." },
  // ---- Deployments, security, observability ----
  { id: "F38", category: "Deployments, security, observability", title: "Shipped a workflow bug", trigger: "Versioning panel steps 1–7",
    actions: [{ kind: "goto", section: "versioning", label: "Open versioning panel" }],
    see: "The v2 batch stops before authorize (Workflow Task failure with stack trace), no data lost, v1 batches unaffected; v3 + override resumes it.",
    handled: "Worker Versioning (Pinned) + versioning override", link: D.versioning,
    say: "Pinned batches never run code they didn't start with, and a targeted override moves only the broken ones to the fix." },
  { id: "F39", category: "Deployments, security, observability", title: "Non-determinism before deploy", trigger: "Narrated", actions: [],
    narrated: "Evidence: `make replay` (passes on captured histories) and `make replay-nde` (fails on a reordered workflow: non-determinism error).",
    see: "—", handled: "Replay tests in CI", link: D.replay, say: "Replaying real histories in CI catches a breaking change before it ships." },
  { id: "F40", category: "Deployments, security, observability", title: "Data at rest", trigger: "Encryption → inspector; Rotate key; draft a new batch",
    actions: [{ kind: "goto", section: "encryption", label: "Open encryption" }, { kind: "rotate", label: "Rotate key" }],
    see: "Stored payloads are ciphertext with a key id; after rotation new batches use the new key, old ones still decode.", handled: "Encrypting DataConverter + Codec Server", link: D.codec,
    say: "Temporal only ever stores ciphertext; the keys never leave your side." },
  { id: "F41", category: "Deployments, security, observability", title: "See the whole wire flow", trigger: "Batches → Open in Temporal UI",
    actions: [{ kind: "goto", section: "batches", label: "Open batches" }],
    see: "Summary, details, every chunk Activity with its summary, Updates (draft/authorize/resolve) with who/when, the approval timer, current details; one history.",
    handled: "Workflow user metadata; history in one execution", link: D.metadata,
    say: "The whole story of a batch is one readable history." },
  { id: "F42", category: "Deployments, security, observability", title: "Retry alerting", trigger: "During F01/F09",
    actions: [{ kind: "goto", section: "alerts", label: "Open alerts" }],
    see: "The alert counter rises per FI.", handled: "Retry Alerting via Metrics", link: D.metrics,
    say: "On-call hears about a stuck Core from a metric long before any retry budget runs out." },
  { id: "F43", category: "Deployments, security, observability", title: "Payload and history limits", trigger: "Batches table: history size and event count",
    actions: [{ kind: "goto", section: "batches", label: "Open batches" }],
    see: "~300 events and about 100 KB (measured) for 5,000 wires; the rows stay in the claim check.", handled: "Claim Check; Basic Workflow Batch", link: D.claimCheck,
    say: "References in, counts out: 5,000 wires cost a few hundred events." },
  { id: "F44", category: "Deployments, security, observability", title: "Operator pauses retries during a long outage", trigger: "CLI (Public Preview)", actions: [],
    narrated: "temporal activity pause --namespace bulkwire -w <workflowId> --activity-id <activityId>   (then: temporal activity unpause …). Takes effect after the current attempt: these Activities do not heartbeat.",
    see: "Retries stop until unpaused (effective after the current attempt; no heartbeats).", handled: "Activity Operations", link: D.activityOps,
    say: "An operator can pause one Activity's retries without touching the rest of the batch." },
];
