// Live batch detail: progress, the chunk grid with retry telemetry, group decisions, Core approval status,
// timeline, rejections, and the Wires tab read straight from Core (the system of record).
import { useEffect, useMemo, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, ExternalLink, Slash } from "lucide-react";
import { api, ApiError, newKey } from "../api";
import { count, dateTime, isTerminal, money, remaining, seconds, tileClass, time } from "../format";
import { session } from "../session";
import type { BatchStatus, BatchView, ChunkView, PendingActivity } from "../types";
import { ApprovalModal } from "../components/ApprovalModal";
import { Annotation, Dialog, DOCS, Empty, PhasePill, Pill, Skeleton, useToast } from "../components/ui";
import { useAnnotations } from "../annotations";

export function LiveDetail({ status }: { status: BatchStatus }) {
  const [lastView, setLastView] = useState<BatchView | null>(status.view);
  useEffect(() => {
    if (status.view) setLastView(status.view);
  }, [status.view]);
  const view = status.view ?? lastView;
  const degraded = status.degraded;
  const [tab, setTab] = useState<"overview" | "wires">("overview");
  const [annotations] = useAnnotations();
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, []);

  if (!view) {
    return (
      <div className="stack">
        {degraded && <DegradedBanner phase={degraded.phase} reason={degraded.reason} />}
        <div className="card"><Skeleton lines={6} /></div>
      </div>
    );
  }
  const phase = degraded ? (degraded.phase || view.phase) : view.phase;
  const live = !isTerminal(phase);
  const pending = status.view ? status.ops.pendingActivities : degraded?.pendingActivities ?? [];

  return (
    <div className="stack">
      <div className="page-title">
        <h1>{view.fileName}</h1>
        <PhasePill phase={phase} />
        {view.needsAttention && <Pill tone="danger"><AlertTriangle size={13} /> Needs attention</Pill>}
        {live && !degraded && <span className="row secondary"><span className="live-dot" aria-hidden="true" /> Live</span>}
        <Annotation href={DOCS.query}>Query `status` · 1 s poll</Annotation>
        <span className="spacer" />
        {annotations && <a className="btn" href={status.temporalUiUrl} target="_blank" rel="noreferrer"><ExternalLink size={15} /> Open in Temporal UI</a>}
        <CancelButton view={view} phase={phase} />
      </div>
      <div className="secondary">
        Created by {view.initiatedBy}{view.draft ? ` · drafted by ${view.draft.requestedBy} ${dateTime(view.draft.requestedAt)}` : ""}
        {view.genTxBatchId ? ` · Core batch ${view.genTxBatchId}` : ""}
        {phase === "DRAFTED" && view.authorizeBy && <> · <strong>Expires in {remaining(view.authorizeBy, now)}</strong> <Annotation href={DOCS.timer}>Durable timer</Annotation></>}
      </div>
      {degraded && <DegradedBanner phase={degraded.phase} reason={degraded.reason} />}
      {view.closure && <div className="banner warning">{view.closure.phase === "CANCELLED" ? "Cancelled" : "Denied"} by {view.closure.by}{view.closure.reason ? `: ${view.closure.reason}` : ""}</div>}

      <div className="tabs" role="tablist">
        <button role="tab" aria-selected={tab === "overview"} className={tab === "overview" ? "active" : ""} onClick={() => setTab("overview")}>Overview</button>
        <button role="tab" aria-selected={tab === "wires"} className={tab === "wires" ? "active" : ""} onClick={() => setTab("wires")}>Wires (bank records)</button>
      </div>

      <div style={{ opacity: degraded ? 0.55 : 1 }}>
        {tab === "overview" ? <Overview view={view} pending={pending} now={now} phase={phase} /> : <WiresTab view={view} />}
      </div>
    </div>
  );
}

// WHY two wordings: with no workers the batch resumes on its own once one returns; a failing Workflow Task (a
// broken release) also blocks the Query, but needs a fixed build first, so "continue automatically" would be untrue.
function DegradedBanner({ phase, reason }: { phase: string; reason: "NO_WORKERS" | "QUERY_FAILED" }) {
  const noWorkers = reason === "NO_WORKERS";
  return (
    <div className="banner info" role="status">
      <div>Live progress is temporarily unavailable. Your batch is safe{noWorkers ? " and will continue automatically" : " and nothing has been lost"}.{phase ? ` Current status: ${phase}.` : ""}</div>
      <Annotation href={DOCS.workers}>{noWorkers ? "Degraded: no workers polling" : "Degraded: the `status` Query failed (see the Temporal UI)"}</Annotation>
    </div>
  );
}

function Overview({ view, pending, now, phase }: { view: BatchView; pending: PendingActivity[]; now: number; phase: string }) {
  const t = view.totals;
  const retrying = useMemo(() => new Map(pending.filter((p) => p.chunkId).map((p) => [p.chunkId, p])), [pending]);
  const batchRetry = pending.find((p) => !p.chunkId);
  return (
    <div className="stack">
      <section className="card">
        <div className="grid-4">
          <div className="stat"><div className="label">Wires</div><div className="value">{count(t.rows)}</div></div>
          <div className="stat"><div className="label">Amount</div><div className="value">{money(t.amountCents)}</div></div>
          <div className="stat"><div className="label">Rejected</div><div className="value">{count(t.rejected)} {t.rejected > 0 && <a href="#rejections" style={{ fontSize: "0.85rem" }}>view</a>}</div></div>
          <div className="stat"><div className="label">Chunks</div><div className="value">{view.chunks.length}</div></div>
        </div>
        <div className="stack" style={{ marginTop: 14, gap: 10 }}>
          <div><div className="row"><span>Drafted</span><span className="spacer" /><span className="num">{count(t.drafted)} / {count(t.rows)}</span></div>
            <div className="progress"><div style={{ width: `${t.rows ? (t.drafted / t.rows) * 100 : 0}%` }} /></div></div>
          <div><div className="row"><span>Authorized</span><span className="spacer" /><span className="num">{count(t.authorized)} / {count(t.rows)}</span></div>
            <div className="progress authorized"><div style={{ width: `${t.rows ? (t.authorized / t.rows) * 100 : 0}%` }} /></div></div>
        </div>
      </section>

      {view.batchAttention && (
        <div className="banner danger"><AlertTriangle size={18} /> Waiting for bank operations: {view.batchAttention.step} — {view.batchAttention.code}: {view.batchAttention.message}</div>
      )}
      {batchRetry && <div className="banner warning">Retrying {batchRetry.phase} · attempt {batchRetry.attempt}{batchRetry.lastFailure ? ` · ${batchRetry.lastFailure}` : ""}</div>}

      <section className="card">
        <div className="card-header"><h2>Chunks</h2>
          <span className="secondary">{view.settings.concurrency} in flight at most · {view.settings.chunkSize} wires per chunk</span>
          <Annotation href={DOCS.describe}>DescribeWorkflowExecution · retrying tiles</Annotation></div>
        <div className="grid-groups">
          {view.groups.map((g) => (
            <div className="grid-group" key={g.groupKey}>
              <div className="label"><span className="mono">{g.originatingAccountMasked}</span> · {g.processDate}<br />{count(g.rowCount)} wires</div>
              <div className="tiles">
                {view.chunks.filter((c) => c.groupKey === g.groupKey).map((c) => <Tile key={c.chunkId} chunk={c} retry={retrying.get(c.chunkId)} now={now} />)}
              </div>
            </div>
          ))}
          {view.groups.length === 0 && <Empty>Planning chunks…</Empty>}
        </div>
        <div className="legend">
          {[["tile-pending", "Pending"], ["tile-running", "Running"], ["tile-retrying", "Retrying"], ["tile-drafted", "Drafted"], ["tile-authorized", "Authorized"], ["tile-rejected", "Rejected"], ["tile-attention", "Needs attention"], ["tile-cancelled", "Cancelled"]].map(([c, l]) => (
            <span key={c}><i className={c} /> {l}</span>
          ))}
        </div>
      </section>

      <div className="grid-2">
        <section className="card">
          <h2>Groups</h2>
          <table>
            <thead><tr><th>Account</th><th>Date</th><th className="num">Amount</th><th>Decision</th></tr></thead>
            <tbody>{view.groups.map((g) => (
              <tr key={g.groupKey}><td className="mono">{g.originatingAccountMasked}</td><td>{g.processDate}</td><td className="num">{money(g.totalAmountCents)}</td>
                <td>{g.decision === "DENY" ? <><Pill tone="danger">Denied</Pill> <span className="secondary">{g.message}</span></> : g.decision === "ALLOW" ? <Pill tone="success">Allowed</Pill> : <Pill tone="neutral">Pending</Pill>}</td></tr>
            ))}</tbody>
          </table>
        </section>
        <ApprovalsPanel view={view} phase={phase} />
      </div>

      <div className="grid-2">
        <section className="card">
          <h2>Timeline</h2>
          <ul style={{ listStyle: "none", padding: 0, margin: 0 }}>
            {[...view.timeline].reverse().map((e, i) => (
              <li key={i} style={{ padding: "6px 0", borderBottom: "1px solid var(--border)" }}>
                <span className="secondary num" style={{ marginRight: 10 }}>{time(e.at)}</span><strong>{e.type.replaceAll("_", " ").toLowerCase()}</strong> <span className="secondary">{e.detail}</span>
              </li>
            ))}
          </ul>
        </section>
        <section className="card" id="rejections">
          <h2>Rejections <span className="secondary">({count(view.rejectionCount)})</span></h2>
          {view.rejections.length === 0 ? <Empty>No wires were rejected.</Empty> : (
            <div className="table-wrap" style={{ maxHeight: 340 }}>
              <table>
                <thead><tr><th className="num">Row</th><th>Code</th><th>Reason</th></tr></thead>
                <tbody>{view.rejections.map((r) => <tr key={r.externalWireId}><td className="num">{r.rowNumber}</td><td className="mono">{r.code}</td><td>{r.message}</td></tr>)}</tbody>
              </table>
            </div>
          )}
          {view.rejectionCount > view.rejections.length && <p className="secondary">Showing the first {view.rejections.length}. All rejected wires are in the Wires tab (bank records).</p>}
        </section>
      </div>
      <p className="secondary">Settings fixed when this batch started: retry budget {duration(view.settings.retryBudgetSeconds)} · approval window {duration(view.settings.approvalWindowSeconds)}</p>
    </div>
  );
}

function duration(secs: number): string {
  if (secs >= 3600 && secs % 3600 === 0) return `${secs / 3600} h`;
  if (secs >= 60 && secs % 60 === 0) return `${secs / 60} min`;
  return `${secs} s`;
}

function Tile({ chunk, retry, now }: { chunk: ChunkView; retry?: PendingActivity; now: number }) {
  const retrying = !!retry && retry.attempt > 1;
  const next = seconds(retry?.nextAttemptAt, now);
  const tooltip = [
    `Chunk ${chunk.chunkId} · ${chunk.length} wires`,
    `drafted ${chunk.drafted} · rejected ${chunk.rejected} · authorized ${chunk.authorized}`,
    `Activity ${chunk.activityId || "—"}`,
    chunk.attention ? `Needs attention: ${chunk.attention.code} — ${chunk.attention.message}` : "",
    retrying ? `Retrying · attempt ${retry!.attempt}${retry!.lastFailure ? ` · last failure: ${retry!.lastFailure}` : ""}` : "",
  ].filter(Boolean).join("\n");
  return (
    <div className={`tile ${tileClass(chunk.state, retrying)}`} title={tooltip} tabIndex={0} aria-label={tooltip}>
      <span className="id">{chunk.chunkId}</span>
      {retrying ? <span>retry #{retry!.attempt}{next !== null && next > 0 && retry!.state === "SCHEDULED" ? ` · in ${next}s` : ""}</span>
        : chunk.state === "REJECTED" || chunk.state === "SKIPPED" ? <Slash size={12} aria-label="rejected" />
        : chunk.state === "NEEDS_ATTENTION" ? <AlertTriangle size={12} aria-label="needs attention" />
        : <span>{chunk.length}</span>}
    </div>
  );
}

function ApprovalsPanel({ view, phase }: { view: BatchView; phase: string }) {
  const user = session.user()!;
  const [open, setOpen] = useState(false);
  const approval = useQuery({ queryKey: ["approval", view.batchId], queryFn: () => api.approval(view.batchId), refetchInterval: isTerminal(phase) ? false : 3000 });
  const a = approval.data;
  const signers = a?.signatures.map((s) => `${s.displayName} signed ${time(s.signedAt)}`).join("; ");
  const canApprove = user.role === "Approver" && user.userId !== view.initiatedBy && ["DRAFTING", "DRAFTED"].includes(phase) && a?.status !== "APPROVED" && a?.status !== "DENIED";
  return (
    <section className="card">
      <div className="card-header"><h2>Approvals</h2><Annotation href={DOCS.approval}>Core decides; workflow receives `authorize`</Annotation></div>
      {!a ? <Skeleton lines={2} /> : (
        <div className="stack" style={{ gap: 8 }}>
          {a.status === "NONE" && <div>Awaiting approval{view.totals.amountCents >= 100_000_000 ? " · two approvers required" : ""}</div>}
          {a.status === "PENDING_SECOND_APPROVAL" && <div><strong>{a.signatures.length} of {a.requiredApprovers} approvals</strong> — {signers}</div>}
          {a.status === "APPROVED" && <div><Pill tone="success">Approved</Pill> {a.approvalRef} — {signers}</div>}
          {a.status === "DENIED" && <div><Pill tone="danger">Denied</Pill> by {a.deniedBy}{a.denyReason ? `: ${a.denyReason}` : ""}</div>}
          {view.authorization && <div className="secondary">The batch received Core's authorization {view.authorization.approvalRef}.</div>}
          {canApprove && <div><button className="btn primary" onClick={() => setOpen(true)}>Review and approve</button></div>}
        </div>
      )}
      {open && (
        <ApprovalModal
          facts={{ batchId: view.batchId, rows: view.totals.rows, amountCents: view.totals.amountCents,
            processDates: [...new Set(view.groups.map((g) => g.processDate))], rejectionCount: view.rejectionCount, initiatedBy: view.initiatedBy }}
          onClose={() => setOpen(false)}
        />
      )}
    </section>
  );
}

function CancelButton({ view, phase }: { view: BatchView; phase: string }) {
  const user = session.user()!;
  const qc = useQueryClient();
  const toast = useToast();
  const key = useMemo(() => newKey(), []);
  const [open, setOpen] = useState(false);
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  // Once the bank has approved the batch it can't be cancelled (the workflow's validator enforces this too).
  if (user.role !== "Initiator" || !["DRAFTING", "DRAFTED"].includes(phase) || view.authorization) return null;
  const cancel = async () => {
    setBusy(true);
    try {
      await api.cancel(view.batchId, key, reason);
      setOpen(false);
      qc.invalidateQueries({ queryKey: ["batch", view.batchId] });
    } catch (e) {
      toast((e as ApiError).message, true);
    } finally {
      setBusy(false);
    }
  };
  return (
    <>
      <button className="btn danger" onClick={() => setOpen(true)}>Cancel batch</button>
      {open && (
        <Dialog title="Cancel this batch?" onClose={() => setOpen(false)}>
          <p>Chunks already being written finish; nothing new starts; the bank marks the batch Cancelled.</p>
          <label className="field">Reason<textarea rows={3} value={reason} onChange={(e) => setReason(e.target.value)} /></label>
          <div className="actions">
            <button className="btn" onClick={() => setOpen(false)}>Keep batch</button>
            <button className="btn danger" disabled={busy} onClick={cancel}>Cancel batch</button>
          </div>
          <Annotation href={DOCS.update}>Update `cancel` · validator checks the phase</Annotation>
        </Dialog>
      )}
    </>
  );
}

function WiresTab({ view }: { view: BatchView }) {
  const [status, setStatus] = useState("");
  const [page, setPage] = useState(0);
  const PAGE = 100;
  const genTx = useQuery({ queryKey: ["genTx", view.batchId], queryFn: () => api.genTxBatch(view.batchId), refetchInterval: 3000, retry: false });
  const wires = useQuery({
    queryKey: ["wires", view.genTxBatchId, status, page],
    queryFn: () => api.wires(view.genTxBatchId, status, page * PAGE, PAGE),
    enabled: view.genTxBatchId > 0,
  });
  if (!view.genTxBatchId) return <Empty>Core has no record of this batch yet.</Empty>;
  return (
    <section className="card">
      <div className="card-header">
        <h2>Bank records</h2>
        <Annotation href={DOCS.coreTvp}>Core stored procedure + TVP</Annotation>
        {genTx.data && <span className="secondary">Core batch {genTx.data.genTxBatchId} · {genTx.data.status} · {Object.entries(genTx.data.counts).filter(([k, v]) => k !== "total" && v > 0).map(([k, v]) => `${count(v)} ${k}`).join(", ")}</span>}
        <span className="spacer" />
        <select value={status} onChange={(e) => { setStatus(e.target.value); setPage(0); }} aria-label="Filter by status">
          <option value="">All statuses</option>
          {["Drafted", "Authorized", "Rejected", "Expired", "Denied", "Cancelled"].map((s) => <option key={s}>{s}</option>)}
        </select>
        <button className="btn" disabled={page === 0} onClick={() => setPage(page - 1)}>Previous</button>
        <button className="btn" disabled={!wires.data || (page + 1) * PAGE >= wires.data.total} onClick={() => setPage(page + 1)}>Next</button>
      </div>
      {wires.isPending ? <Skeleton lines={8} /> : wires.isError ? <Empty>Bank records are temporarily unavailable.</Empty> : (
        <>
          <p className="secondary">{count(wires.data!.total)} wires{status ? ` with status ${status}` : ""}</p>
          <div className="table-wrap">
            <table>
              <thead><tr><th className="num">Row</th><th>Chunk</th><th>From</th><th>Date</th><th className="num">Amount</th><th>Beneficiary</th><th>Account</th><th>Status</th><th>Approval / reason</th></tr></thead>
              <tbody>{wires.data!.items.map((w) => (
                <tr key={w.wireId}><td className="num">{w.rowNumber}</td><td className="mono">{w.chunkId}</td><td className="mono">{w.originatingAccountMasked}</td><td>{w.processDate}</td>
                  <td className="num">{money(w.amountCents)}</td><td>{w.beneficiaryName}</td><td className="mono">{w.beneficiaryAccountMasked}</td>
                  <td>{w.status}</td><td className="secondary">{w.rejectCode ? `${w.rejectCode}${w.rejectMessage ? ` — ${w.rejectMessage}` : ""}` : w.approvalRef ?? ""}</td></tr>
              ))}</tbody>
            </table>
          </div>
        </>
      )}
    </section>
  );
}
