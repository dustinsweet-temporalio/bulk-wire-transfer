// Home dashboard: account balances (from this FI's Core), approvals waiting, and recent bulk wire batches.
import { useState } from "react";
import { Link, useNavigate } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { AlertTriangle } from "lucide-react";
import { api } from "../api";
import { age, count, isTerminal, money } from "../format";
import { session } from "../session";
import type { BatchListItem } from "../types";
import { Annotation, DOCS, Empty, PhasePill, Skeleton } from "../components/ui";

export function Home() {
  const user = session.user()!;
  const accounts = useQuery({ queryKey: ["accounts"], queryFn: () => api.accounts() });
  const approvals = useQuery({ queryKey: ["batches", "approvals"], queryFn: () => api.batches("approvals"), enabled: user.role === "Approver", refetchInterval: 5000 });

  return (
    <div className="stack">
      <div className="page-title"><h1>Welcome, {user.name.split(",")[0]}</h1></div>
      <div className="grid-2">
        <section className="card">
          <h2>Account balances</h2>
          {accounts.isPending ? <Skeleton /> : accounts.isError ? <Empty>Balances are unavailable right now.</Empty> : (
            <table>
              <thead><tr><th>Account</th><th>Nickname</th><th className="num">Available balance</th></tr></thead>
              <tbody>
                {accounts.data!.map((a) => (
                  <tr key={a.accountNumberMasked}>
                    <td className="mono">{a.accountNumberMasked}</td>
                    <td>{a.nickname} {a.status !== "Active" && <span className="pill warning">{a.status === "OnHold" ? "On hold" : a.status}</span>}</td>
                    <td className="num">{money(a.availableBalanceCents)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </section>
        {user.role === "Approver" && (
          <section className="card">
            <div className="card-header"><h2>Approvals waiting</h2><span className="pill warning">{approvals.data?.length ?? 0}</span>
              <Annotation href={DOCS.searchAttributes}>Search Attribute: BulkWireStatus = DRAFTED</Annotation></div>
            {approvals.isPending ? <Skeleton /> : (approvals.data?.length ?? 0) === 0 ? <Empty>Nothing is waiting for your approval.</Empty> : (
              <BatchTable items={approvals.data!} />
            )}
          </section>
        )}
      </div>
      <RecentBatches />
    </div>
  );
}

function RecentBatches() {
  const [scope, setScope] = useState<"mine" | "company">("mine");
  const list = useQuery({ queryKey: ["batches", scope], queryFn: () => api.batches(scope), refetchInterval: 3000 });
  return (
    <section className="card">
      <div className="card-header">
        <h2>Recent bulk wire batches</h2>
        <div className="tabs" style={{ margin: 0, border: 0 }}>
          <button className={scope === "mine" ? "active" : ""} onClick={() => setScope("mine")}>My batches</button>
          <button className={scope === "company" ? "active" : ""} onClick={() => setScope("company")}>Company</button>
        </div>
        <Annotation href={DOCS.searchAttributes}>Search Attributes: InitiatedBy / CustomerId</Annotation>
        <span className="spacer" />
        <Link to="/payments/bulk-wire" className="btn primary">Upload a file</Link>
      </div>
      {list.isPending ? <Skeleton /> : list.isError ? <Empty>Batch lists are temporarily unavailable.</Empty> : list.data!.length === 0 ? (
        <Empty>No batches yet — <Link to="/payments/bulk-wire">upload a file</Link>.</Empty>
      ) : <BatchTable items={list.data!} />}
    </section>
  );
}

export function BatchTable({ items }: { items: BatchListItem[] }) {
  const navigate = useNavigate();
  const now = Date.now();
  return (
    <div className="table-wrap">
      <table>
        <thead><tr><th>File</th><th>Status</th><th className="num">Wires</th><th className="num">Amount</th><th>Progress</th><th>Created by</th><th>Started</th></tr></thead>
        <tbody>
          {items.map((b) => {
            const t = b.totals;
            // A terminated execution keeps its last BulkWireStatus; show that it was terminated instead.
            const status = b.executionStatus === "TERMINATED" ? "TERMINATED" : b.status;
            const pct = t && t.rows > 0 ? Math.round(((status === "AUTHORIZING" || status === "AUTHORIZED" ? t.authorized : t.drafted + t.rejected) / t.rows) * 100) : 0;
            return (
              <tr key={b.workflowId} className="clickable" onClick={() => navigate(`/payments/bulk-wire/${b.batchId}`)}>
                <td>{b.fileName ?? b.batchId} {b.needsAttention && <AlertTriangle size={15} color="var(--danger)" aria-label="Needs attention" />}</td>
                <td><PhasePill phase={status} /></td>
                <td className="num">{t ? count(t.rows) : "—"}</td>
                <td className="num">{t ? money(t.amountCents) : "—"}</td>
                <td style={{ minWidth: 120 }}>{t && !isTerminal(status) ? <div className="progress"><div style={{ width: `${pct}%` }} /></div> : t ? "Done" : ""}</td>
                <td>{b.initiatedBy}</td>
                <td>{age(b.startTime, now)}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
