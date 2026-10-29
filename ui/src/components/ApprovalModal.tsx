// Approval modal: Core owns the decision. The approver enters the security code (TAC); THIS FI's Core runs
// determination and dual control. Only a final approval reaches the workflow, as an `authorize` Update whose
// Update ID is Core's approval reference (so "Retry notification" is safe to click).
import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api";
import { count, money } from "../format";
import type { Approval } from "../types";
import { Annotation, Dialog, DOCS } from "./ui";

const DUAL_CONTROL_CENTS = 100_000_000; // $1,000,000.00 (CORE_DUAL_CONTROL_THRESHOLD_CENTS)

export interface ApprovalFacts {
  batchId: string;
  rows: number;
  amountCents: number;
  processDates: string[];
  rejectionCount: number;
  initiatedBy: string;
}

export function ApprovalModal({ facts, onClose }: { facts: ApprovalFacts; onClose: () => void }) {
  const qc = useQueryClient();
  const [tac, setTac] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [result, setResult] = useState<Approval | null>(null);
  const [denying, setDenying] = useState(false);
  const [reason, setReason] = useState("");

  const approve = async () => {
    setBusy(true);
    setError("");
    try {
      setResult(await api.approve(facts.batchId, tac));
      // WHY only the approval: refreshing the batch here would swap a staged review for the live view the moment
      // a final approval starts the workflow, closing this dialog before the approver reads the result. The
      // page refreshes when the dialog closes (and the live view polls on its own).
      qc.invalidateQueries({ queryKey: ["approval", facts.batchId] });
    } catch (e) {
      setError((e as ApiError).message);
    } finally {
      setBusy(false);
    }
  };

  const deny = async () => {
    setBusy(true);
    setError("");
    try {
      setResult(await api.deny(facts.batchId, reason));
      qc.invalidateQueries({ queryKey: ["approval", facts.batchId] });
    } catch (e) {
      setError((e as ApiError).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog title={denying ? "Deny this batch" : "Approve and release"} onClose={onClose}>
      <table>
        <tbody>
          <tr><td>Wires</td><td className="num">{count(facts.rows)}</td></tr>
          <tr><td>Total amount</td><td className="num">{money(facts.amountCents)}</td></tr>
          <tr><td>Process dates</td><td className="num">{facts.processDates.join(", ")}</td></tr>
          <tr><td>Rejected wires</td><td className="num">{count(facts.rejectionCount)}</td></tr>
        </tbody>
      </table>
      {facts.amountCents >= DUAL_CONTROL_CENTS && <div className="banner info" style={{ marginTop: 12 }}>This batch requires two approvers.</div>}

      {result ? (
        <div style={{ marginTop: 14 }} className="stack">
          {result.status === "PENDING_SECOND_APPROVAL" && <div className="banner warning">Approval recorded: {result.signatures.length} of {result.requiredApprovers} approvals. A second approver must sign.</div>}
          {result.status === "APPROVED" && result.orchestration !== "NOTIFY_FAILED" && <div className="banner success">Approved ({result.approvalRef}). Release started.</div>}
          {result.status === "APPROVED" && result.orchestration === "NOTIFY_FAILED" && (
            <div className="banner danger">
              <div>Approval recorded. Release notification failed —{" "}
                <button className="link" disabled={busy} onClick={approve}>Retry</button>
                {" "}<Annotation href={DOCS.idempotency}>Update ID = approvalRef · safe to re-send</Annotation></div>
            </div>
          )}
          {result.status === "DENIED" && <div className="banner danger">Batch denied.</div>}
          <div className="actions"><button className="btn" onClick={onClose}>Close</button></div>
        </div>
      ) : denying ? (
        <div className="stack" style={{ marginTop: 14 }}>
          <label className="field">Reason<textarea rows={3} value={reason} onChange={(e) => setReason(e.target.value)} /></label>
          {error && <div className="banner danger" role="alert">{error}</div>}
          <div className="actions">
            <button className="btn" onClick={() => setDenying(false)}>Back</button>
            <button className="btn danger" disabled={busy || !reason} onClick={deny}>Deny batch</button>
          </div>
        </div>
      ) : (
        <div className="stack" style={{ marginTop: 14 }}>
          <label className="field">Enter the 6-digit security code
            <input inputMode="numeric" maxLength={6} value={tac} onChange={(e) => setTac(e.target.value.replace(/\D/g, ""))} autoComplete="one-time-code" />
          </label>
          <span className="secondary">Demo code: 246 810</span>
          {error && <div className="banner danger" role="alert">{error}</div>}
          <div className="actions">
            <button className="btn danger" onClick={() => setDenying(true)}>Deny…</button>
            <span className="spacer" />
            <button className="btn" onClick={onClose}>Cancel</button>
            <button className="btn primary" disabled={busy || tac.length !== 6} onClick={approve}>{busy ? "Approving…" : "Approve"}</button>
          </div>
          <Annotation href={DOCS.approval}>Core decides; workflow receives `authorize`</Annotation>
        </div>
      )}
    </Dialog>
  );
}
