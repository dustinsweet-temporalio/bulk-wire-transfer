// Typed API client. /api/bulkwire/* goes to bulkwire-service; /api/core/* goes to THIS portal's own FI's Core (the
// gateway routes each origin to its own Core). The bearer token comes from sessionStorage; a 401 signs out.
import { session } from "./session";
import type {
  Account, Approval, AttentionBatch, BatchListItem, BatchStatus, BatchView, Branding, GenTxBatch, InvalidRow,
  Page, SampleInfo, StagedRow, StagingSummary, User, ValidationError, Wire,
} from "./types";

export class ApiError extends Error {
  constructor(public status: number, public code: string, message: string, public details?: unknown) {
    super(message);
  }
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  const token = session.token();
  if (token) headers.set("Authorization", `Bearer ${token}`);
  if (init.body && !(init.body instanceof FormData)) headers.set("Content-Type", "application/json");
  const res = await fetch(path, { ...init, headers });
  if (res.status === 401 && token) {
    session.signOut();
    window.location.assign("/login");
  }
  const text = await res.text();
  const body = text ? safeJSON(text) : null;
  if (!res.ok) {
    const b = (body ?? {}) as { code?: string; message?: string; errors?: unknown };
    throw new ApiError(res.status, b.code ?? `HTTP_${res.status}`, b.message ?? res.statusText, b.errors);
  }
  return body as T;
}

function safeJSON(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    return { message: text };
  }
}

const bw = (p: string) => `/api/bulkwire${p}`;
const core = (p: string) => `/api/core${p}`;
const post = (body?: unknown, headers?: Record<string, string>): RequestInit => ({
  method: "POST",
  body: body === undefined ? undefined : JSON.stringify(body),
  headers,
});

export const api = {
  branding: (fiId: string) => request<Branding>(bw(`/fi/${fiId}/branding`)),
  login: (fiId: string, companyId: string, userId: string, password: string) =>
    request<{ token: string; user: User }>(bw("/auth/login"), post({ fiId, companyId, userId, password })),

  samples: () => request<SampleInfo[]>(bw("/samples")),
  sampleUrl: (name: string) => bw(`/samples/${name}.csv`),

  upload: (file: File) => {
    const form = new FormData();
    form.append("file", file);
    return request<StagingSummary>(bw("/batches"), { method: "POST", body: form });
  },
  stagedRows: (batchId: string, offset: number, limit: number) =>
    request<Page<StagedRow>>(bw(`/batches/${batchId}/staging?view=valid&offset=${offset}&limit=${limit}`)),
  invalidRows: (batchId: string, offset: number, limit: number) =>
    request<Page<InvalidRow>>(bw(`/batches/${batchId}/staging?view=invalid&offset=${offset}&limit=${limit}`)),
  errors: (batchId: string, offset: number, limit: number) =>
    request<Page<ValidationError>>(bw(`/batches/${batchId}/staging?view=errors&offset=${offset}&limit=${limit}`)),
  errorsCsvUrl: (batchId: string) => bw(`/batches/${batchId}/errors.csv`),

  // Update-with-Start `draft`; the Idempotency-Key becomes the Update ID.
  draft: (batchId: string, key: string) =>
    request<{ batchId: string; workflowId: string; view: BatchView }>(bw(`/batches/${batchId}/draft`), post(undefined, { "Idempotency-Key": key })),
  cancel: (batchId: string, key: string, reason: string) =>
    request<{ view: BatchView }>(bw(`/batches/${batchId}/cancel`), post({ reason }, { "Idempotency-Key": key })),
  batch: (batchId: string) => request<BatchStatus>(bw(`/batches/${batchId}`)),
  batches: (scope: "mine" | "company" | "approvals") => request<BatchListItem[]>(bw(`/batches?scope=${scope}`)),

  attention: () => request<AttentionBatch[]>(bw("/ops/attention")),
  resolve: (batchId: string, key: string, chunkId: string, decision: string, note: string) =>
    request<{ view: BatchView }>(bw(`/ops/batches/${batchId}/resolve`), post({ chunkId, decision, note }, { "Idempotency-Key": key })),

  // Core: the FI's system of record
  accounts: (customerId?: string) => request<Account[]>(core(`/accounts${customerId ? `?customerId=${customerId}` : ""}`)),
  setHold: (accountNumber: string, onHold: boolean, reason: string) =>
    request<Account>(core(`/accounts/${accountNumber}/hold`), post({ onHold, reason })),
  approval: (batchId: string) => request<Approval>(core(`/approvals/${batchId}`)),
  approve: (batchId: string, tac: string, amountCents: number, rowCount: number, initiatedBy: string) =>
    request<Approval>(core("/approvals"), post({ batchId, tac, amountCents, rowCount, initiatedBy })),
  deny: (batchId: string, reason: string) => request<Approval>(core("/approvals/deny"), post({ batchId, reason })),
  genTxBatch: (batchId: string) => request<GenTxBatch>(core(`/gentx-batches/by-external/${batchId}`)),
  wires: (genTxBatchId: number, status: string, offset: number, limit: number) =>
    request<Page<Wire>>(core(`/gentx-batches/${genTxBatchId}/wires?offset=${offset}&limit=${limit}${status ? `&status=${status}` : ""}`)),
};

export function newKey(): string {
  return crypto.randomUUID();
}
