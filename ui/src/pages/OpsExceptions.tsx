// Bank operations: bulk wire exceptions. Batches with NeedsAttention=true (a Search Attribute the workflow flips
// when a chunk parks), and for each one the parked items with the human actions: release the account hold (the
// real fix, in Core), then RETRY; or reject the chunk's wires; or cancel the batch (only while drafting).
import { useMemo, useState } from "react";
import { Link } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { X } from "lucide-react";
import { api, ApiError, newKey } from "../api";
import { age, dateTime } from "../format";
import type { AttentionBatch, ParkedItem } from "../types";
import { Annotation, Dialog, DOCS, Empty, Pill, Skeleton, useToast } from "../components/ui";

export function OpsExceptions() {
  const [selected, setSelected] = useState<string | null>(null);
  const list = useQuery({ queryKey: ["attention"], queryFn: api.attention, refetchInterval: 3000 });
  const now = Date.now();
  const batch = list.data?.find((b) => b.batchId === selected) ?? null;
  return (
    <div className="stack">
      <div className="page-title">
        <h1>Bulk wire exceptions</h1>
        <Annotation href={DOCS.searchAttributes}>Search Attribute: NeedsAttention</Annotation>
      </div>
      <section className="card">
        {list.isPending ? <Skeleton /> : list.isError ? <Empty>The exception queue is temporarily unavailable.</Empty> : list.data!.length === 0 ? (
          <Empty>No batches need attention.</Empty>
        ) : (
          <table>
            <thead><tr><th>Customer</th><th>Batch</th><th>Phase</th><th className="num">Parked items</th><th>Oldest since</th></tr></thead>
            <tbody>{list.data!.map((b) => {
              const oldest = b.parked.map((p) => p.since).sort()[0];
              return (
                <tr key={b.batchId} className="clickable" onClick={() => setSelected(b.batchId)}>
                  <td>{b.customerName}</td><td>{b.fileName || b.batchId}</td><td><Pill tone="info">{b.phase}</Pill></td>
                  <td className="num">{b.parked.length}</td><td>{age(oldest, now)}</td>
                </tr>
              );
            })}</tbody>
          </table>
        )}
        <p className="secondary" style={{ marginBottom: 0 }}>The list comes from Visibility and can lag a few seconds behind the batch itself.</p>
      </section>
      {batch && <Drawer batch={batch} onClose={() => setSelected(null)} />}
    </div>
  );
}

function Drawer({ batch, onClose }: { batch: AttentionBatch; onClose: () => void }) {
  return (
    <div className="drawer" role="dialog" aria-label={`Exceptions for ${batch.fileName}`}>
      <div className="row">
        <h2>{batch.customerName} · {batch.fileName || batch.batchId}</h2>
        <span className="spacer" />
        <button className="btn" onClick={onClose} aria-label="Close"><X size={16} /></button>
      </div>
      <p className="secondary">Batch {batch.batchId} · phase {batch.phase} · <Link to={`/payments/bulk-wire/${batch.batchId}`}>open batch</Link></p>
      <div className="stack">
        {batch.parked.length === 0 && <Empty>Nothing is parked any more.</Empty>}
        {batch.parked.map((p) => <ParkedCard key={p.chunkId} batch={batch} item={p} />)}
      </div>
    </div>
  );
}

function ParkedCard({ batch, item }: { batch: AttentionBatch; item: ParkedItem }) {
  const qc = useQueryClient();
  const toast = useToast();
  const keys = useMemo(() => ({ retry: newKey(), reject: newKey(), cancel: newKey() }), []);
  const [confirm, setConfirm] = useState<"REJECT_CHUNK" | "CANCEL_BATCH" | null>(null);
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const accounts = useQuery({ queryKey: ["accounts", batch.customerId], queryFn: () => api.accounts(batch.customerId), enabled: item.code === "ACCOUNT_ON_HOLD" });
  // The parked item carries only the masked account; match the last four digits against the customer's accounts.
  const account = accounts.data?.find((a) => a.accountNumberMasked === item.originatingAccountMasked);

  const resolve = async (decision: string, key: string) => {
    setBusy(true);
    try {
      await api.resolve(batch.batchId, key, item.chunkId, decision, note);
      toast(`${decision.replace("_", " ").toLowerCase()} sent for ${item.chunkId}`);
      setConfirm(null);
      qc.invalidateQueries({ queryKey: ["attention"] });
    } catch (e) {
      toast((e as ApiError).message, true);
    } finally {
      setBusy(false);
    }
  };

  const releaseHold = async () => {
    if (!account) return;
    setBusy(true);
    try {
      await api.setHold(account.accountNumber, false, "");
      toast(`Hold released on ${account.accountNumberMasked}`);
      qc.invalidateQueries({ queryKey: ["accounts", batch.customerId] });
    } catch (e) {
      toast((e as ApiError).message, true);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="card">
      <div className="row wrap">
        <strong className="mono">{item.chunkId === "_batch" ? `Batch step (${item.step})` : `Chunk ${item.chunkId}`}</strong>
        <Pill tone="danger">{item.code}</Pill>
        {item.originatingAccountMasked && <span className="mono">{item.originatingAccountMasked}</span>}
        <span className="secondary">since {dateTime(item.since)} · {item.phase.toLowerCase()} phase</span>
      </div>
      <p>{item.message}</p>
      <div className="row wrap">
        {item.code === "ACCOUNT_ON_HOLD" && (
          <button className="btn" disabled={busy || !account || account.status !== "OnHold"} onClick={releaseHold}>
            {account?.status === "Active" ? "Hold released" : "Release hold"}
          </button>
        )}
        {item.allowedDecisions.includes("RETRY") && <button className="btn primary" disabled={busy} onClick={() => resolve("RETRY", keys.retry)}>Retry {item.chunkId === "_batch" ? "step" : "chunk"}</button>}
        {item.allowedDecisions.includes("REJECT_CHUNK") && <button className="btn" disabled={busy} onClick={() => setConfirm("REJECT_CHUNK")}>Reject chunk wires</button>}
        {item.allowedDecisions.includes("CANCEL_BATCH") && <button className="btn danger" disabled={busy} onClick={() => setConfirm("CANCEL_BATCH")}>Cancel batch</button>}
        <Annotation href={DOCS.resumable}>Update `resolve`</Annotation>
      </div>
      {confirm && (
        <Dialog title={confirm === "REJECT_CHUNK" ? `Reject the wires in ${item.chunkId}?` : "Cancel the whole batch?"} onClose={() => setConfirm(null)}>
          <p>{confirm === "REJECT_CHUNK" ? "These wires are recorded in Core as Rejected; the rest of the batch continues." : "Chunks in flight finish, nothing new starts, and Core marks the batch Cancelled."}</p>
          <label className="field">Note (recorded with the decision)<textarea rows={3} value={note} onChange={(e) => setNote(e.target.value)} /></label>
          <div className="actions">
            <button className="btn" onClick={() => setConfirm(null)}>Back</button>
            <button className="btn danger" disabled={busy} onClick={() => resolve(confirm, confirm === "REJECT_CHUNK" ? keys.reject : keys.cancel)}>Confirm</button>
          </div>
        </Dialog>
      )}
    </div>
  );
}
