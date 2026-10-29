// Shared Control Center pieces: section cards, status chips, action buttons with feedback, and SVG charts.
import { useState, type ReactNode } from "react";

export function Card({ title, accent, children, actions }: { title: ReactNode; accent?: string; children: ReactNode; actions?: ReactNode }) {
  return (
    <section className="card" style={accent ? { borderTopColor: accent } : undefined}>
      <div className="card-head"><h3>{title}</h3><span className="spacer" />{actions}</div>
      {children}
    </section>
  );
}

export type Health = "ok" | "degraded" | "down" | "paused" | "off";

export function Chip({ label, health, detail }: { label: string; health: Health; detail?: string }) {
  return (
    <span className={`chip ${health}`} title={detail}>
      <i aria-hidden="true" /> {label}{detail ? <small> {detail}</small> : null}
    </span>
  );
}

/** A button that runs an async action and shows its outcome inline. */
export function Act({ label, run, tone, disabled }: { label: string; run: () => Promise<unknown>; tone?: "danger" | "primary"; disabled?: boolean }) {
  const [state, setState] = useState<{ busy: boolean; msg?: string; ok?: boolean }>({ busy: false });
  return (
    <span className="act">
      <button className={`btn ${tone ?? ""}`} disabled={disabled || state.busy} onClick={async () => {
        setState({ busy: true });
        try {
          await run();
          setState({ busy: false, ok: true, msg: "done" });
        } catch (e) {
          setState({ busy: false, ok: false, msg: (e as Error).message });
        }
      }}>{state.busy ? "…" : label}</button>
      {state.msg && <small className={state.ok ? "ok-text" : "err-text"}>{state.msg}</small>}
    </span>
  );
}

/** Multi-series line chart over the last N samples (1 s resolution). */
export function LineChart({ series, height = 120, max }: { series: { name: string; color: string; values: number[] }[]; height?: number; max?: number }) {
  const width = 600;
  const n = Math.max(...series.map((s) => s.values.length), 2);
  const top = Math.max(max ?? 0, ...series.flatMap((s) => s.values), 1);
  const x = (i: number) => (i / (n - 1)) * width;
  const y = (v: number) => height - (v / top) * (height - 8) - 4;
  return (
    <div className="chart">
      <svg viewBox={`0 0 ${width} ${height}`} preserveAspectRatio="none" role="img" aria-label={series.map((s) => s.name).join(", ")}>
        {[0.25, 0.5, 0.75].map((f) => <line key={f} x1={0} x2={width} y1={height * f} y2={height * f} className="gridline" />)}
        {series.map((s) => (
          <polyline key={s.name} fill="none" stroke={s.color} strokeWidth={2.2} vectorEffect="non-scaling-stroke"
            points={s.values.map((v, i) => `${x(i + (n - s.values.length))},${y(v)}`).join(" ")} />
        ))}
      </svg>
      <div className="chart-legend">
        <span className="muted">max {Math.round(top * 10) / 10}</span>
        {series.map((s) => <span key={s.name}><i style={{ background: s.color }} /> {s.name} <b>{s.values.at(-1) ?? 0}</b></span>)}
      </div>
    </div>
  );
}

export function Sparkline({ values, color }: { values: number[]; color: string }) {
  const w = 120, h = 28;
  const top = Math.max(...values, 1);
  const pts = values.map((v, i) => `${(i / Math.max(values.length - 1, 1)) * w},${h - (v / top) * (h - 4) - 2}`).join(" ");
  return <svg width={w} height={h} aria-hidden="true"><polyline fill="none" stroke={color} strokeWidth={2} points={pts} /></svg>;
}

export function bytes(n: number): string {
  if (n >= 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)} MB`;
  if (n >= 1024) return `${Math.round(n / 1024)} KB`;
  return `${n} B`;
}
