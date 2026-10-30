// API shapes used by the portal. They mirror bulkwire-service (go/internal/api, go/internal/workflow types)
// and core-api (core/CoreApi/Contracts). Money is integer cents; account numbers arrive masked unless noted.

export interface Branding {
  fiId: string;
  displayName: string;
  wordmark: string;
  primaryColor: string;
  accentColor: string;
  demoUsers: { companyId: string; userId: string; name: string; role: string }[];
}

export type Role = "Initiator" | "Approver" | "BankOps";

export interface User {
  userId: string;
  name: string;
  role: Role;
  fiId: string;
  customerId: string;
  companyId: string;
  companyName: string;
}

export interface StagingGroup {
  groupKey: string;
  originatingAccountMasked: string;
  processDate: string;
  rowCount: number;
  totalAmountCents: number;
  chunkCount: number;
}

export interface StagingSummary {
  batchId: string;
  fiId: string;
  customerId: string;
  fileName: string;
  status: "VALID" | "INVALID";
  rowCount: number;
  validRowCount: number;
  errorCount: number;
  invalidRowCount: number;
  totalAmountCents: number;
  groups: StagingGroup[];
  uploadedBy: string;
  uploadedAt: string;
  batchError?: string; // a batch-level reason it cannot be drafted (e.g. too many chunks)
}

export interface StagedRow {
  rowNumber: number;
  originatingAccountMasked: string;
  processDate: string;
  amountCents: number;
  currency: string;
  beneficiaryName: string;
  beneficiaryAccountMasked: string;
  beneficiaryRouting: string;
  beneficiaryBank: string;
  reference: string;
  subsidiary: string;
}

export interface InvalidRow {
  rowNumber: number;
  originatingAccount: string;
  processDate: string;
  amount: string;
  currency: string;
  beneficiaryName: string;
  beneficiaryAccount: string;
  beneficiaryRouting: string;
  beneficiaryBank: string;
  reference: string;
  subsidiary: string;
  errorCodes: string[];
}

export interface ValidationError {
  rowNumber: number;
  column: string;
  code: string;
  message: string;
}

export interface Page<T> {
  total: number;
  offset: number;
  limit: number;
  items: T[];
}

export type Phase = "DRAFTING" | "DRAFTED" | "AUTHORIZING" | "AUTHORIZED" | "EXPIRED" | "DENIED" | "CANCELLED" | "REJECTED";
export type ChunkState = "PENDING" | "RUNNING" | "DRAFTED" | "REJECTED" | "NEEDS_ATTENTION" | "AUTHORIZED" | "CANCELLED" | "SKIPPED";

export interface Attention {
  code: string;
  message: string;
  phase: Phase;
  since: string;
  source: string;
  step?: string;
  allowedDecisions: string[];
}

export interface Totals {
  rows: number;
  drafted: number;
  authorized: number;
  rejected: number;
  pending: number;
  amountCents: number;
}

export interface ChunkView {
  chunkId: string;
  groupKey: string;
  originatingAccountMasked: string;
  length: number;
  state: ChunkState;
  cycle: number;
  activityId: string;
  drafted: number;
  rejected: number;
  authorized: number;
  attention: Attention | null;
}

export interface GroupView {
  groupKey: string;
  originatingAccountMasked: string;
  processDate: string;
  rowCount: number;
  totalAmountCents: number;
  decision: "ALLOW" | "DENY" | "PENDING";
  code: string | null;
  message: string | null;
}

export interface Approver {
  userId: string;
  displayName: string;
  signedAt: string;
}

export interface BatchView {
  batchId: string;
  fiId: string;
  customerId: string;
  initiatedBy: string;
  fileName: string;
  genTxBatchId: number;
  phase: Phase;
  needsAttention: boolean;
  settings: { chunkSize: number; concurrency: number; retryBudgetSeconds: number; approvalWindowSeconds: number };
  totals: Totals;
  groups: GroupView[];
  chunks: ChunkView[];
  batchAttention: Attention | null;
  rejections: { rowNumber: number; externalWireId: string; code: string; message: string }[];
  rejectionCount: number;
  draft: { requestedBy: string; requestedAt: string } | null;
  authorization: { approvalRef: string; approvers: Approver[]; approvedAt: string } | null;
  closure: { phase: Phase; by: string; reason: string } | null;
  authorizeBy: string | null;
  timeline: { at: string; type: string; detail: string }[];
}

export interface PendingActivity {
  activityId: string;
  chunkId: string;
  phase: string;
  attempt: number;
  state: string;
  lastFailure?: string;
  nextAttemptAt?: string;
  lastStartedAt?: string;
}

export interface BatchStatus {
  source: "workflow" | "staging";
  staging: StagingSummary | null;
  view: BatchView | null;
  ops: { pendingActivities: PendingActivity[] };
  degraded?: { reason: "NO_WORKERS" | "QUERY_FAILED"; phase: string; needsAttention: boolean; pendingActivities: PendingActivity[]; pollers: number };
  workflowId: string;
  temporalUiUrl: string;
}

export interface BatchListItem {
  batchId: string;
  workflowId: string;
  status: string;
  needsAttention: boolean;
  customerId: string;
  initiatedBy: string;
  executionStatus: string;
  startTime: string | null;
  closeTime: string | null;
  fileName?: string;
  totals?: Totals;
}

export interface ParkedItem {
  chunkId: string;
  code: string;
  message: string;
  phase: string;
  source: string;
  since: string;
  originatingAccountMasked: string;
  allowedDecisions: string[];
  step?: string;
}

export interface AttentionBatch {
  batchId: string;
  workflowId: string;
  customerId: string;
  customerName: string;
  fileName: string;
  phase: string;
  parked: ParkedItem[];
}

export interface SampleInfo {
  name: string;
  description: string;
  fiId: string;
  customerId: string;
  rows: number;
}

// ---- Core (the FI's system of record) ----

export interface Account {
  accountNumber: string; // identifier for API calls only; display accountNumberMasked
  accountNumberMasked: string;
  customerId: string;
  customerName: string;
  nickname: string;
  status: "Active" | "OnHold" | "Closed";
  holdReason: string | null;
  availableBalanceCents: number;
  dailyWireLimitCents: number;
}

export interface Approval {
  status: "NONE" | "PENDING_SECOND_APPROVAL" | "APPROVED" | "DENIED";
  approvalRef: string | null;
  requiredApprovers: number;
  signatures: Approver[];
  deniedBy: string | null;
  denyReason: string | null;
  orchestration: "NOTIFIED" | "NOTIFY_FAILED" | "NOT_APPLICABLE";
}

export interface GenTxBatch {
  genTxBatchId: number;
  status: string;
  counts: { drafted: number; authorized: number; rejected: number; expired: number; denied: number; cancelled: number; total: number };
}

export interface Wire {
  wireId: number;
  externalWireId: string;
  chunkId: string;
  rowNumber: number;
  originatingAccountMasked: string;
  processDate: string;
  amountCents: number;
  beneficiaryName: string;
  beneficiaryAccountMasked: string;
  beneficiaryRouting: string;
  status: string;
  rejectCode: string | null;
  rejectMessage: string | null;
  approvalRef: string | null;
}
