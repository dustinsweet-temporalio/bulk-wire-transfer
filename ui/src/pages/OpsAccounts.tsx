// Bank operations: accounts. Pick a customer, see account status, and place or release a hold. This is the
// legitimate bank-staff action that creates and clears the "account on hold" condition in the demo.
import { useMemo, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api";
import { money } from "../format";
import type { Account } from "../types";
import { Dialog, Empty, Pill, Skeleton, useToast } from "../components/ui";

export function OpsAccounts() {
  const accounts = useQuery({ queryKey: ["accounts", "all"], queryFn: () => api.accounts(), refetchInterval: 5000 });
  const customers = useMemo(() => [...new Map((accounts.data ?? []).map((a) => [a.customerId, a.customerName])).entries()], [accounts.data]);
  const [customer, setCustomer] = useState<string>("");
  const selected = customer || customers[0]?.[0] || "";
  const [holding, setHolding] = useState<Account | null>(null);

  return (
    <div className="stack">
      <div className="page-title">
        <h1>Accounts</h1>
        <label className="row">Customer
          <select value={selected} onChange={(e) => setCustomer(e.target.value)}>
            {customers.map(([id, name]) => <option key={id} value={id}>{name}</option>)}
          </select>
        </label>
      </div>
      <section className="card">
        {accounts.isPending ? <Skeleton /> : accounts.isError ? <Empty>Accounts are temporarily unavailable.</Empty> : (
          <table>
            <thead><tr><th>Account</th><th>Nickname</th><th>Status</th><th className="num">Daily wire limit</th><th className="num">Available balance</th><th /></tr></thead>
            <tbody>{accounts.data!.filter((a) => a.customerId === selected).map((a) => (
              <tr key={a.accountNumber}>
                <td className="mono">{a.accountNumberMasked}</td><td>{a.nickname}</td>
                <td>{a.status === "Active" ? <Pill tone="success">Active</Pill> : <><Pill tone="warning">{a.status === "OnHold" ? "On hold" : a.status}</Pill> <span className="secondary">{a.holdReason}</span></>}</td>
                <td className="num">{money(a.dailyWireLimitCents)}</td><td className="num">{money(a.availableBalanceCents)}</td>
                <td>{a.status !== "Closed" && <button className="btn" onClick={() => setHolding(a)}>{a.status === "OnHold" ? "Release hold" : "Place hold"}</button>}</td>
              </tr>
            ))}</tbody>
          </table>
        )}
      </section>
      {holding && <HoldDialog account={holding} onClose={() => setHolding(null)} />}
    </div>
  );
}

function HoldDialog({ account, onClose }: { account: Account; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const placing = account.status !== "OnHold";
  const [reason, setReason] = useState(placing ? "Fraud review" : "");
  const [busy, setBusy] = useState(false);
  const submit = async () => {
    setBusy(true);
    try {
      await api.setHold(account.accountNumber, placing, reason);
      toast(`${placing ? "Hold placed on" : "Hold released on"} ${account.accountNumberMasked}`);
      qc.invalidateQueries({ queryKey: ["accounts"] });
      onClose();
    } catch (e) {
      toast((e as ApiError).message, true);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog title={`${placing ? "Place a hold on" : "Release the hold on"} ${account.accountNumberMasked}`} onClose={onClose}>
      <p>{account.nickname} · {account.customerName}</p>
      {placing && <label className="field">Reason<input value={reason} onChange={(e) => setReason(e.target.value)} /></label>}
      <div className="actions">
        <button className="btn" onClick={onClose}>Cancel</button>
        <button className="btn primary" disabled={busy || (placing && !reason)} onClick={submit}>{placing ? "Place hold" : "Release hold"}</button>
      </div>
    </Dialog>
  );
}
