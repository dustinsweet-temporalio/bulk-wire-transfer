// DEMO-ONLY Control Center API client (control-service behind the gateway at /api/control).
export class ControlError extends Error {}

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(`/api/control${path}`, {
    method,
    headers: body === undefined ? undefined : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await res.text();
  const data = text ? JSON.parse(text) : null;
  if (!res.ok) throw new ControlError(data?.error ?? `${res.status} ${res.statusText}`);
  return data as T;
}

export const get = <T>(path: string) => call<T>("GET", path);
export const post = <T>(path: string, body: unknown = {}) => call<T>("POST", path, body);
export const put = <T>(path: string, body: unknown) => call<T>("PUT", path, body);

export interface Container {
  service: string;
  state: string;
  health: string;
  status: string;
  startedAt: string;
  allowed: boolean;
}

export interface Status {
  containers?: Container[];
  temporal?: { reachable: boolean; namespace: string; searchAttributes?: Record<string, string> };
  pollers?: Record<string, { identity: string; buildId: string }[]>;
  deployment?: { name: string; current?: string; ramping?: string };
  core?: Record<string, { healthy: boolean; state?: CoreState; error?: string }>;
  claimCheck?: { backend: string; reachable: boolean };
  activeKeyId?: string;
  draftDefaults?: Record<string, Defaults>;
  coreQueues?: CoreQueue[];
}

// One FI's Core Activity task queue: the rate its workers set from routing.yaml, any server-side override, and what
// the Temporal Service enforces now.
export interface CoreQueue {
  taskQueue: string;
  fis: string[];
  configuredRps: number;
  maxConcurrentActivities: number;
  overrideRps?: number;
  overrideReason?: string;
  effectiveRps: number;
  effectiveSource: string;
  pollers: string[];
  error?: string;
}

export interface CoreState {
  fiId: string;
  fault: { mode: string; remainingSeconds?: number; remainingCount?: number };
  chunkLatencyMs: number;
  holds: { accountNumber: string; accountNumberMasked: string; customerName: string; status: string; holdReason: string }[];
  stats: CoreStats;
}

export interface CoreStats {
  fiId: string;
  inFlight: number;
  maxInFlight: number;
  callsTotal: number;
  callsFailed: number;
  callsPerSecond: { last1s: number; last10s: number };
  failuresPerSecond: { last1s: number; last10s: number };
  recent: { at: string; operation: string; status: number; ms: number; code: string | null }[];
}

export interface Defaults {
  chunkSize: number;
  concurrency: number;
  approvalWindow: string;
  retryBudget: string;
  retryMaxInterval: string;
}

export interface BatchRow {
  workflowId: string;
  batchId: string;
  fiId: string;
  customerId: string;
  initiatedBy: string;
  status: string;
  needsAttention: boolean;
  executionStatus: string;
  deploymentVersion: string;
  startTime: string | null;
  historyLength: number;
  historySizeBytes: number;
  pendingRetries: string[];
  temporalUiUrl: string;
  portalUrl: string;
}

export const FIS = ["lakeshore", "prairie"] as const;
export type FI = (typeof FIS)[number];
export const PORTAL: Record<FI, string> = { lakeshore: "http://localhost:3001", prairie: "http://localhost:3002" };
export const TEMPORAL_UI = "http://localhost:8233/namespaces/bulkwire/workflows";
