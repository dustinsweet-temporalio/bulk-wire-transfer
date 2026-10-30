// Runs a catalog action against control-service. Timed actions (e.g. "pause 90 s → unpause") schedule their
// revert in this browser tab and report a countdown; the presenter can also revert by hand from Overview.
import { get, post, put, type Status } from "./api";
import type { Action } from "./catalog";

const BACKEND_SERVICE: Record<string, string> = { redis: "redis", s3: "rustfs", gcs: "fake-gcs" };

export type Countdown = { label: string; until: number };
export const countdowns = new Map<string, Countdown>();
const listeners = new Set<() => void>();
export const onCountdown = (fn: () => void) => { listeners.add(fn); return () => listeners.delete(fn); };
const notify = () => listeners.forEach((l) => l());

function scheduleRevert(key: string, label: string, seconds: number, revert: () => Promise<unknown>) {
  countdowns.set(key, { label, until: Date.now() + seconds * 1000 });
  notify();
  setTimeout(async () => {
    try {
      await revert();
    } finally {
      countdowns.delete(key);
      notify();
    }
  }, seconds * 1000);
}

const container = (service: string, action: string) => post(`/containers/${service}/${action}`);

export async function runAction(a: Action, ctx: { batchId?: string; fiId?: string; userId?: string }): Promise<unknown> {
  switch (a.kind) {
    case "fault":
      return post(`/core/${a.fi}/faults`, { mode: a.mode, durationSeconds: a.durationSeconds, count: a.count });
    case "latency":
      return post(`/core/${a.fi}/latency`, { chunkLatencyMs: a.ms });
    case "container":
      await container(a.service, a.action);
      if (a.revertAfter && a.revertAction) {
        scheduleRevert(`${a.service}`, `${a.service}: ${a.revertAction} in`, a.revertAfter, () => container(a.service, a.revertAction!));
      }
      return;
    case "containers":
      await Promise.all(a.services.map((s) => container(s, a.action)));
      if (a.revertAfter && a.revertAction) {
        scheduleRevert(a.services.join("+"), `${a.services.join(" + ")}: ${a.revertAction} in`, a.revertAfter,
          () => Promise.all(a.services.map((s) => container(s, a.revertAction!))));
      }
      return;
    case "backend": {
      const status = await get<Status>("/status");
      const svc = BACKEND_SERVICE[status.claimCheck?.backend ?? "redis"];
      await container(svc, a.action);
      scheduleRevert(svc, `${svc}: ${a.revertAction} in`, a.revertAfter, () => container(svc, a.revertAction));
      return;
    }
    case "defaults": {
      const fis = a.fi === "both" ? ["lakeshore", "prairie"] : [a.fi];
      for (const fi of fis) {
        const cur = await get<Record<string, unknown>>(`/draft-defaults/${fi}`);
        await put(`/draft-defaults/${fi}`, { ...cur, ...a.patch });
      }
      return;
    }
    case "hold":
      return post(`/core/${a.fi}/accounts/${a.account}/hold`, { onHold: a.onHold, reason: a.onHold ? "Fraud review" : "" });
    case "expire":
      if (!ctx.batchId) throw new Error("enter a staged batch ID first");
      return post("/claimcheck/expire", { fiId: ctx.fiId ?? "lakeshore", batchId: ctx.batchId });
    case "retryStorm":
      return post("/load/retry-storm", { fiId: ctx.fiId ?? "lakeshore", userId: ctx.userId ?? "jordan", batchId: ctx.batchId ?? "", requests: 20 });
    case "payrollDay":
      return post("/load/payroll-day", { batches: PAYROLL_DAY });
    case "ratelimit":
      return put(`/ratelimits/${a.fi}`, { rps: a.rps });
    case "fairnessTest":
      return post("/load/payroll-day", { batches: FAIRNESS_TEST });
    case "rotate":
      return post("/encryption/rotate");
    case "goto":
      window.location.hash = a.section;
      return;
    case "link":
      window.open(a.href, "_blank", "noopener");
      return;
  }
}

export const PAYROLL_DAY = [
  { fiId: "lakeshore", userId: "jordan", sample: "summit-payroll-10000", concurrency: 4 },
  { fiId: "lakeshore", userId: "taylor", sample: "northline-payroll-10000", concurrency: 4 },
  { fiId: "prairie", userId: "riley", sample: "harvest-6000", concurrency: 4 },
];

// The Fairness comparison (F48): three 5,000-wire Summit batches at Lakeshore, then a 500-wire Northline batch 3 s
// later, so the small batch arrives while the large ones already have Core Activities waiting in the queue.
export const FAIRNESS_TEST = [
  { fiId: "lakeshore", userId: "jordan", sample: "summit-payroll-5000", concurrency: 4 },
  { fiId: "lakeshore", userId: "jordan", sample: "summit-payroll-5000", concurrency: 4 },
  { fiId: "lakeshore", userId: "jordan", sample: "summit-payroll-5000", concurrency: 4 },
  { fiId: "lakeshore", userId: "taylor", sample: "northline-bonus-500", concurrency: 4, startAfterMs: 3000 },
];
