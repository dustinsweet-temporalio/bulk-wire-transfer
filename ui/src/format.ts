// Formatting rules used everywhere in the portal: money from integer cents, masked account numbers, status
// pills, and relative times. Pure functions (unit-tested in format.test.ts).
import type { ChunkState, Phase } from "./types";

/** 1312151899 -> "$13,121,518.99". Integer arithmetic only: cents never pass through floating point. */
export function money(cents: number): string {
  const sign = cents < 0 ? "-" : "";
  const abs = Math.abs(cents);
  const whole = Math.trunc(abs / 100).toLocaleString("en-US");
  return `${sign}$${whole}.${String(abs % 100).padStart(2, "0")}`;
}

/** 1312151899 -> "$13.1M" for compact cards (rounded down to one decimal, integer arithmetic). */
export function moneyShort(cents: number): string {
  if (cents >= 100_000_000) {
    const tenths = Math.trunc(cents / 10_000_000);
    return `$${Math.trunc(tenths / 10)}.${tenths % 10}M`;
  }
  if (cents >= 100_000) {
    const tenths = Math.trunc(cents / 10_000);
    return `$${Math.trunc(tenths / 10)}.${tenths % 10}K`;
  }
  return money(cents);
}

/** Display rule: account numbers are shown masked to the last four digits. */
export function mask(account: string): string {
  if (account.startsWith("••")) return account;
  return `••${account.slice(-4)}`;
}

export function count(n: number): string {
  return n.toLocaleString("en-US");
}

export type PillTone = "neutral" | "info" | "success" | "warning" | "danger" | "muted";

/** Batch phase -> status pill. */
export function phasePill(phase: string): { label: string; tone: PillTone } {
  // TERMINATED is an execution status, not a phase: an operator ended the run (e.g. the demo reset).
  if (phase === "TERMINATED") return { label: "Terminated", tone: "muted" };
  const map: Record<Phase, { label: string; tone: PillTone }> = {
    DRAFTING: { label: "Drafting", tone: "info" },
    DRAFTED: { label: "Awaiting approval", tone: "warning" },
    AUTHORIZING: { label: "Releasing", tone: "info" },
    AUTHORIZED: { label: "Authorized", tone: "success" },
    EXPIRED: { label: "Expired", tone: "muted" },
    DENIED: { label: "Denied", tone: "danger" },
    CANCELLED: { label: "Cancelled", tone: "muted" },
    REJECTED: { label: "Rejected", tone: "danger" },
  };
  return map[phase as Phase] ?? { label: phase || "Staged", tone: "neutral" };
}

export function isTerminal(phase: string): boolean {
  return ["AUTHORIZED", "EXPIRED", "DENIED", "CANCELLED", "REJECTED", "TERMINATED"].includes(phase);
}

/** Chunk state -> tile style key (see .tile-* in styles.css). */
export function tileClass(state: ChunkState, retrying: boolean): string {
  if (retrying) return "tile-retrying";
  switch (state) {
    case "RUNNING":
      return "tile-running";
    case "DRAFTED":
      return "tile-drafted";
    case "AUTHORIZED":
      return "tile-authorized";
    case "REJECTED":
    case "SKIPPED":
      return "tile-rejected";
    case "NEEDS_ATTENTION":
      return "tile-attention";
    case "CANCELLED":
      return "tile-cancelled";
    default:
      return "tile-pending";
  }
}

/** "Expires in 23 h 12 m" countdown. */
export function remaining(untilIso: string, now: number): string {
  const ms = new Date(untilIso).getTime() - now;
  if (ms <= 0) return "expired";
  const mins = Math.floor(ms / 60000);
  const h = Math.floor(mins / 60);
  const m = mins % 60;
  if (h > 0) return `${h} h ${m} m`;
  const s = Math.floor((ms % 60000) / 1000);
  return m > 0 ? `${m} m ${s} s` : `${s} s`;
}

export function seconds(untilIso: string | undefined, now: number): number | null {
  if (!untilIso) return null;
  return Math.max(0, Math.round((new Date(untilIso).getTime() - now) / 1000));
}

export function dateTime(iso: string | null | undefined): string {
  if (!iso) return "";
  return new Date(iso).toLocaleString("en-US", { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" });
}

export function time(iso: string): string {
  return new Date(iso).toLocaleTimeString("en-US", { hour: "numeric", minute: "2-digit", second: "2-digit" });
}

export function age(iso: string | null | undefined, now: number): string {
  if (!iso) return "";
  const mins = Math.floor((now - new Date(iso).getTime()) / 60000);
  if (mins < 1) return "just now";
  if (mins < 60) return `${mins} min ago`;
  const h = Math.floor(mins / 60);
  return h < 24 ? `${h} h ago` : `${Math.floor(h / 24)} d ago`;
}
