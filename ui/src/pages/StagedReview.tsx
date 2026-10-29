// Batch review before drafting: summary, errors (Draft disabled until the file is clean), groups, and the
// records table read page by page from the claim check. Initiators "Save as draft"; approvers may
// "Approve & release" first (approve-before-draft: one workflow then drafts and authorizes).
import { useMemo, useState } from "react";
import { Link, useNavigate } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Download, RefreshCw } from "lucide-react";
import { api, ApiError, newKey } from "../api";
import { count, dateTime, money } from "../format";
import { session } from "../session";
import type { StagingSummary } from "../types";
import { ApprovalModal } from "../components/ApprovalModal";
import { Annotation, DOCS, Empty, Pill, Skeleton, useToast } from "../components/ui";

const PAGE = 50;

export function StagedReview({ summary }: { summary: StagingSummary }) {
  const user = session.user()!;
  const qc = useQueryClient();
  const toast = useToast();
  const navigate = useNavigate();
  // One Idempotency-Key per page load: double clicks and second tabs become the SAME Update.
  const draftKey = useMemo(() => newKey(), []);
  const [busy, setBusy] = useState(false);
  const [approving, setApproving] = useState(false);
  const invalid = summary.status === "INVALID";

  const draft = async () => {
    setBusy(true);
    try {
      await api.draft(summary.batchId, draftKey);
      await qc.invalidateQueries({ queryKey: ["batch", summary.batchId] });
    } catch (e) {
      toast((e as ApiError).message, true);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="stack">
      <div className="page-title">
        <h1>{summary.fileName}</h1>
        {invalid ? <Pill tone="danger">Needs corrections</Pill> : <Pill tone="neutral">Ready to draft</Pill>}
        <span className="secondary">Uploaded by {summary.uploadedBy} · {dateTime(summary.uploadedAt)}</span>
        <span className="spacer" />
        {user.role === "Initiator" && (
          <button className="btn primary" disabled={invalid || busy} onClick={draft}>{busy ? "Saving…" : "Save as draft"}</button>
        )}
        {user.role === "Approver" && !invalid && (
          <button className="btn primary" disabled={busy} onClick={() => setApproving(true)}>Approve &amp; release</button>
        )}
      </div>
      {user.role === "Initiator" && (
        <div className="row"><Annotation href={DOCS.updateWithStart}>Update-with-Start `draft` · Update ID = idempotency key · double clicks and second tabs reach the same workflow</Annotation></div>
      )}
      <div className="grid-4">
        <div className="card stat"><div className="label">Total wires</div><div className="value">{count(summary.validRowCount)}</div></div>
        <div className="card stat"><div className="label">Total amount</div><div className="value">{money(summary.totalAmountCents)}</div></div>
        <div className="card stat"><div className="label">Groups (account + date)</div><div className="value">{summary.groups.length}</div></div>
        <div className="card stat"><div className="label">Errors</div><div className="value" style={{ color: invalid ? "var(--danger)" : undefined }}>{count(summary.errorCount)}</div></div>
      </div>
      {invalid && (
        <>
          <div className="banner danger" role="alert">
            {count(summary.invalidRowCount)} rows need attention before this batch can be drafted. Correct the file and upload it again.
            <span className="spacer" />
            <a className="btn" href="#" onClick={(e) => { e.preventDefault(); downloadErrors(summary.batchId, summary.fileName); }}><Download size={15} /> Download error report</a>
            <button className="btn" onClick={() => navigate("/payments/bulk-wire")}><RefreshCw size={15} /> Replace file</button>
          </div>
          <ErrorsTable batchId={summary.batchId} />
        </>
      )}
      <section className="card">
        <h2>Groups</h2>
        <table>
          <thead><tr><th>Originating account</th><th>Process date</th><th className="num">Wires</th><th className="num">Amount</th><th className="num">Chunks</th></tr></thead>
          <tbody>{summary.groups.map((g) => (
            <tr key={g.groupKey}><td className="mono">{g.originatingAccountMasked}</td><td>{g.processDate}</td>
              <td className="num">{count(g.rowCount)}</td><td className="num">{money(g.totalAmountCents)}</td><td className="num">{g.chunkCount}</td></tr>
          ))}</tbody>
        </table>
      </section>
      <RecordsTable summary={summary} />
      {approving && (
        <ApprovalModal
          facts={{ batchId: summary.batchId, rows: summary.validRowCount, amountCents: summary.totalAmountCents,
            processDates: [...new Set(summary.groups.map((g) => g.processDate))], rejectionCount: 0, initiatedBy: summary.uploadedBy }}
          onClose={() => { setApproving(false); qc.invalidateQueries({ queryKey: ["batch", summary.batchId] }); }}
        />
      )}
      <p className="secondary"><Link to="/payments/bulk-wire">Upload another file</Link></p>
    </div>
  );
}

async function downloadErrors(batchId: string, fileName: string) {
  const res = await fetch(api.errorsCsvUrl(batchId), { headers: { Authorization: `Bearer ${session.token()}` } });
  const a = document.createElement("a");
  a.href = URL.createObjectURL(await res.blob());
  a.download = fileName.replace(/\.csv$/i, "") + "-errors.csv";
  a.click();
}

function ErrorsTable({ batchId }: { batchId: string }) {
  const errors = useQuery({ queryKey: ["errors", batchId], queryFn: () => api.errors(batchId, 0, 200) });
  return (
    <section className="card">
      <h2>Errors</h2>
      {errors.isPending ? <Skeleton /> : (
        <div className="table-wrap" style={{ maxHeight: 320 }}>
          <table>
            <thead><tr><th className="num">Row</th><th>Column</th><th>Problem</th></tr></thead>
            <tbody>{errors.data?.items.map((e, i) => (
              <tr key={i}><td className="num">{e.rowNumber}</td><td className="mono">{e.column}</td><td>{e.message} <span className="secondary mono">{e.code}</span></td></tr>
            ))}</tbody>
          </table>
        </div>
      )}
    </section>
  );
}

function RecordsTable({ summary }: { summary: StagingSummary }) {
  const [page, setPage] = useState(0);
  const [showInvalid, setShowInvalid] = useState(false);
  const total = showInvalid ? summary.invalidRowCount : summary.validRowCount;
  const valid = useQuery({
    queryKey: ["staged", summary.batchId, "valid", page],
    queryFn: () => api.stagedRows(summary.batchId, page * PAGE, PAGE),
    enabled: !showInvalid,
  });
  const invalid = useQuery({
    queryKey: ["staged", summary.batchId, "invalid", page],
    queryFn: () => api.invalidRows(summary.batchId, page * PAGE, PAGE),
    enabled: showInvalid,
  });
  const rows = showInvalid ? invalid : valid;
  const pages = Math.max(1, Math.ceil(total / PAGE));
  return (
    <section className="card">
      <div className="card-header">
        <h2>Records</h2>
        {summary.invalidRowCount > 0 && (
          <label className="row"><input type="checkbox" checked={showInvalid} onChange={(e) => { setShowInvalid(e.target.checked); setPage(0); }} /> Show invalid rows</label>
        )}
        <span className="spacer" />
        <button className="btn" disabled={page === 0} onClick={() => setPage(page - 1)}>Previous</button>
        <span className="secondary">Page {page + 1} of {pages}</span>
        <button className="btn" disabled={page + 1 >= pages} onClick={() => setPage(page + 1)}>Next</button>
      </div>
      {rows.isPending ? <Skeleton lines={8} /> : rows.isError ? <Empty>Records are temporarily unavailable.</Empty> : (
        <div className="table-wrap">
          <table>
            <thead><tr><th className="num">Row</th><th>From</th><th>Process date</th><th className="num">Amount</th><th>Beneficiary</th><th>Beneficiary account</th><th>Routing</th><th>Bank</th><th>Reference</th></tr></thead>
            <tbody>
              {showInvalid
                ? invalid.data!.items.map((r) => (
                  <tr key={r.rowNumber}><td className="num">{r.rowNumber}</td><td className="mono">{r.originatingAccount}</td><td>{r.processDate}</td>
                    <td className="num">{r.amount}</td><td>{r.beneficiaryName}</td><td className="mono">{r.beneficiaryAccount}</td><td className="mono">{r.beneficiaryRouting}</td>
                    <td>{r.beneficiaryBank}</td><td>{r.reference} <span className="pill danger">{r.errorCodes.join(", ")}</span></td></tr>))
                : valid.data!.items.map((r) => (
                  <tr key={r.rowNumber}><td className="num">{r.rowNumber}</td><td className="mono">{r.originatingAccountMasked}</td><td>{r.processDate}</td>
                    <td className="num">{money(r.amountCents)}</td><td>{r.beneficiaryName}</td><td className="mono">{r.beneficiaryAccountMasked}</td>
                    <td className="mono">{r.beneficiaryRouting}</td><td>{r.beneficiaryBank}</td><td>{r.reference}</td></tr>))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}
