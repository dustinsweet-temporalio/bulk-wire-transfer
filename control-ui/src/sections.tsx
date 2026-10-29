// The Control Center's sections. Every action calls control-service; nothing here talks to a bank system directly.
import { useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { useQuery } from "@tanstack/react-query";
import { FIS, PORTAL, TEMPORAL_UI, get, post, put, type BatchRow, type Defaults, type CoreStats, type Status } from "./api";
import { catalog, type Category, type Entry } from "./catalog";
import { countdowns, onCountdown, PAYROLL_DAY, runAction } from "./actions";
import { Act, bytes, Card, LineChart, Sparkline } from "./components";

const ACCENT = { core: "#F2A65A", db: "#E07A9B", workers: "#8FA8FF", temporal: "#B79CFF", claim: "#7CC4FF", approvals: "#9BE08A", load: "#FFD166", security: "#C3A6FF" };
const CATEGORY_ACCENT: Record<Category, string> = {
  Core: ACCENT.core, Database: ACCENT.db, Workers: ACCENT.workers, "Temporal & claim check": ACCENT.claim,
  "API & approvals plumbing": ACCENT.temporal, "Business conditions": ACCENT.approvals, Approvals: ACCENT.approvals,
  "Duplicates, load & tenancy": ACCENT.load, "Deployments, security, observability": ACCENT.security,
};
type Props = { status?: Status };

function useCountdowns() {
  const [, force] = useState(0);
  useSyncExternalStore(onCountdown, () => countdowns.size);
  useEffect(() => {
    const t = setInterval(() => force((n) => n + 1), 1000);
    return () => clearInterval(t);
  }, []);
  return [...countdowns.values()].map((c) => `${c.label} ${Math.max(0, Math.ceil((c.until - Date.now()) / 1000))} s`);
}

function Countdowns() {
  const list = useCountdowns();
  return list.length ? <div className="countdowns">{list.map((c) => <span key={c} className="pill warn">{c}</span>)}</div> : null;
}

// ---- 1. Overview ---------------------------------------------------------------------------------------------
export function Overview({ status }: Props) {
  return (
    <div className="stack">
      <h2>Overview</h2>
      <Countdowns />
      <Card title="Containers" accent={ACCENT.workers}>
        <table>
          <thead><tr><th>Service</th><th>State</th><th>Health</th><th>Status</th><th>Actions</th></tr></thead>
          <tbody>{(status?.containers ?? []).filter((c) => !/init|activate/.test(c.service)).map((c) => (
            <tr key={c.service}>
              <td className="mono">{c.service}</td><td><span className={`pill ${c.state === "running" ? "ok" : c.state === "paused" ? "warn" : "bad"}`}>{c.state}</span></td>
              <td>{c.health || "—"}</td><td className="muted">{c.status}</td>
              <td className="row wrap">{c.allowed ? ["start", "stop", "kill", "pause", "unpause", "restart"].map((a) => (
                <Act key={a} label={a} tone={a === "kill" ? "danger" : undefined} run={() => post(`/containers/${c.service}/${a}`)} />
              )) : <span className="muted">not in allowlist</span>}</td>
            </tr>
          ))}</tbody>
        </table>
      </Card>
      <Card title="Links">
        <div className="row wrap">
          <a className="btn" href={PORTAL.lakeshore} target="_blank" rel="noreferrer">Lakeshore portal :3001</a>
          <a className="btn" href={PORTAL.prairie} target="_blank" rel="noreferrer">Prairie portal :3002</a>
          <a className="btn" href={TEMPORAL_UI} target="_blank" rel="noreferrer">Temporal UI (namespace bulkwire)</a>
          <a className="btn" href="http://localhost:8089/healthz" target="_blank" rel="noreferrer">Codec Server :8089</a>
          <a className="btn" href="http://localhost:9464/metrics" target="_blank" rel="noreferrer">worker-a metrics</a>
          <a className="btn" href="http://localhost:9465/metrics" target="_blank" rel="noreferrer">worker-b metrics</a>
        </div>
      </Card>
    </div>
  );
}

// ---- 2. Batches --------------------------------------------------------------------------------------------------
export function Batches() {
  const [fi, setFi] = useState("");
  const rows = useQuery({ queryKey: ["batches", fi], queryFn: () => get<BatchRow[]>(`/batches${fi ? `?fi=${fi}` : ""}`), refetchInterval: 2000 });
  return (
    <div className="stack">
      <h2>Batches</h2>
      <Card title="Every BulkWireBatch (Visibility; running batches enriched with Describe)" accent={ACCENT.temporal}
        actions={<select value={fi} onChange={(e) => setFi(e.target.value)}><option value="">All FIs</option>{FIS.map((f) => <option key={f}>{f}</option>)}</select>}>
        <div className="table-wrap">
          <table>
            <thead><tr><th>FI</th><th>Customer</th><th>Initiator</th><th>Phase</th><th>Attn</th><th>Build</th><th className="num">Events</th><th className="num">History</th><th>Retrying</th><th>Links</th></tr></thead>
            <tbody>{(rows.data ?? []).map((b) => (
              <tr key={b.workflowId}>
                <td>{b.fiId}</td><td>{b.customerId}</td><td>{b.initiatedBy}</td>
                <td><span className={`pill ${b.executionStatus === "RUNNING" ? "info" : "ok"}`}>{b.status}</span>{b.executionStatus !== "RUNNING" && b.executionStatus !== "COMPLETED" ? <small className="muted"> {b.executionStatus}</small> : null}</td>
                <td>{b.needsAttention ? <span className="pill bad">yes</span> : ""}</td>
                <td className="mono">{b.deploymentVersion.replace("bulkwire:", "")}</td>
                <td className="num">{b.historyLength}</td><td className="num">{bytes(b.historySizeBytes)}</td>
                <td className="mono small">{b.pendingRetries.join(", ")}</td>
                <td className="row"><a href={b.temporalUiUrl} target="_blank" rel="noreferrer">Temporal UI</a><a href={b.portalUrl} target="_blank" rel="noreferrer">portal</a></td>
              </tr>
            ))}</tbody>
          </table>
          {rows.data?.length === 0 && <p className="muted">No batches yet.</p>}
        </div>
      </Card>
    </div>
  );
}

// ---- 3. Failure catalog --------------------------------------------------------------------------------------
export function CatalogSection() {
  const [batchId, setBatchId] = useState("");
  const [fiId, setFiId] = useState("lakeshore");
  const [filter, setFilter] = useState("");
  const groups = useMemo(() => {
    const m = new Map<Category, Entry[]>();
    for (const e of catalog.filter((e) => !filter || `${e.id} ${e.title}`.toLowerCase().includes(filter.toLowerCase()))) {
      m.set(e.category, [...(m.get(e.category) ?? []), e]);
    }
    return [...m.entries()];
  }, [filter]);
  return (
    <div className="stack">
      <h2>Failure catalog <small className="muted">F01–F44 · docs/FAILURE-CATALOG.md</small></h2>
      <Countdowns />
      <div className="row wrap toolbar">
        <input placeholder="filter (e.g. F21, hold)" value={filter} onChange={(e) => setFilter(e.target.value)} />
        <label>FI for batch-scoped actions <select value={fiId} onChange={(e) => setFiId(e.target.value)}>{FIS.map((f) => <option key={f}>{f}</option>)}</select></label>
        <label>Batch ID (F17 expire, F35 storm) <input className="mono" size={30} value={batchId} onChange={(e) => setBatchId(e.target.value.trim())} placeholder="staged batch ID (optional for F35)" /></label>
      </div>
      {groups.map(([cat, entries]) => (
        <div key={cat} className="stack">
          <h3 className="cat" style={{ color: CATEGORY_ACCENT[cat] }}>{cat}</h3>
          <div className="cards">
            {entries.map((e) => (
              <Card key={e.id} accent={CATEGORY_ACCENT[cat]} title={<><span className="mono">{e.id}</span> {e.title}</>}>
                <p className="trigger">{e.trigger}</p>
                {e.narrated ? <p className="narrated"><span className="pill info">Narrated</span> {e.narrated}</p> : (
                  <div className="row wrap">{e.actions.map((a, i) => (
                    <Act key={i} label={a.label ?? `${a.kind}`} tone={i === 0 ? "primary" : undefined}
                      run={() => runAction(a, { batchId: batchId || undefined, fiId, userId: fiId === "prairie" ? "riley" : "jordan" })} />
                  ))}</div>
                )}
                <dl>
                  <dt>What you'll see</dt><dd>{e.see}</dd>
                  <dt>What handles it</dt><dd>{e.handled} · <a href={e.link} target="_blank" rel="noreferrer">docs</a></dd>
                  <dt>Say this</dt><dd className="say">“{e.say}”</dd>
                </dl>
              </Card>
            ))}
          </div>
        </div>
      ))}
    </div>
  );
}

// ---- 4. Core load & fairness ---------------------------------------------------------------------------------
type Sample = { inFlight: number; cps: number; fps: number; max: number };

export function Load() {
  const history = useRef<Record<string, Sample[]>>({});
  const stats = useQuery({
    queryKey: ["core-stats"],
    queryFn: async () => {
      const s = await get<Record<string, CoreStats | { error: string }>>("/core/stats");
      for (const [fi, v] of Object.entries(s)) {
        const st = v as CoreStats;
        const sample = "error" in v ? { inFlight: 0, cps: 0, fps: 0, max: 0 } : { inFlight: st.inFlight, cps: st.callsPerSecond.last1s, fps: st.failuresPerSecond.last1s, max: st.maxInFlight };
        history.current[fi] = [...(history.current[fi] ?? []), sample].slice(-60);
      }
      return s;
    },
    refetchInterval: 1000,
  });
  const fairness = useQuery({ queryKey: ["fairness"], queryFn: () => get<{ enabled: boolean; rpsPerFi: number }>("/fairness"), refetchInterval: 3000 });
  const [rps, setRps] = useState(3);
  const [storm, setStorm] = useState<unknown>(null);
  const [launch, setLaunch] = useState<unknown>(null);
  const [concurrency, setConcurrency] = useState(4);
  return (
    <div className="stack">
      <h2>Core load & fairness</h2>
      <Card title="Per-FI fairness rate limit (Activity task queue)" accent={ACCENT.load}
        actions={<span className={`pill ${fairness.data?.enabled ? "warn" : "muted"}`}>{fairness.data?.enabled ? `ON · ${fairness.data.rpsPerFi}/s per FI` : "OFF"}</span>}>
        <p className="explain">Caps Core call <b>rate</b> per FI across every batch and worker. Steady-state in-flight ≈ rate × call latency. Enforced by the Temporal Service.</p>
        <div className="row wrap">
          <label>rps per FI <input type="number" min={0.5} step={0.5} value={rps} onChange={(e) => setRps(Number(e.target.value))} style={{ width: 80 }} /></label>
          <Act label="Enable limit" tone="primary" run={() => put("/fairness", { enabled: true, rpsPerFi: rps }).then(() => fairness.refetch())} />
          <Act label="Remove limit" run={() => put("/fairness", { enabled: false, rpsPerFi: 0 }).then(() => fairness.refetch())} />
        </div>
      </Card>
      <div className="cards two">
        {FIS.map((fi) => {
          const h = history.current[fi] ?? [];
          const st = stats.data?.[fi] as CoreStats | undefined;
          return (
            <Card key={fi} title={`Core ${fi}`} accent={ACCENT.core} actions={<><LatencyControl fi={fi} /><Act label="reset stats" run={() => post(`/core/${fi}/stats/reset`)} /></>}>
              <LineChart series={[
                { name: "calls/s", color: "#FFD166", values: h.map((x) => x.cps) },
                { name: "in-flight", color: "#8FA8FF", values: h.map((x) => x.inFlight) },
                { name: "failures/s", color: "#FF7A7A", values: h.map((x) => x.fps) },
              ]} max={fairness.data?.enabled ? fairness.data.rpsPerFi : undefined} />
              <p className="muted small">max in-flight {st?.maxInFlight ?? 0} · total {st?.callsTotal ?? 0} · failed {st?.callsFailed ?? 0} · 10 s avg {st?.callsPerSecond?.last10s ?? 0}/s</p>
            </Card>
          );
        })}
      </div>
      <Card title="Payroll day launcher" accent={ACCENT.load}>
        <p className="muted">Jordan/Summit 10k + Taylor/Northline 10k at Lakeshore, Riley/Harvest 6k at Prairie. Concurrency is applied as each FI's draft default before the batches start.</p>
        <div className="row wrap">
          <label>concurrency <input type="number" min={1} max={8} value={concurrency} onChange={(e) => setConcurrency(Number(e.target.value))} style={{ width: 60 }} /></label>
          <Act label="Launch payroll day" tone="primary" run={async () => setLaunch(await post("/load/payroll-day", { batches: PAYROLL_DAY.map((b) => ({ ...b, concurrency })) }))} />
        </div>
        {launch ? <pre>{JSON.stringify(launch, null, 2)}</pre> : null}
      </Card>
      <Card title="Retry storm (F35)" accent={ACCENT.load}>
        <p className="muted">20 concurrent Draft requests for one freshly staged Lakeshore batch, each with a different Idempotency-Key.</p>
        <Act label="Fire retry storm" tone="primary" run={async () => setStorm(await post("/load/retry-storm", { fiId: "lakeshore", userId: "jordan", requests: 20 }))} />
        {storm ? <pre>{JSON.stringify(storm, null, 2)}</pre> : null}
      </Card>
    </div>
  );
}

// DEMO-ONLY "simulated core processing time" per chunk call (fixed per call; never varies with concurrency).
function LatencyControl({ fi }: { fi: string }) {
  const [ms, setMs] = useState(600);
  return (
    <span className="row">
      <label className="small">latency ms <input type="number" min={0} step={100} value={ms} onChange={(e) => setMs(Number(e.target.value))} style={{ width: 80 }} /></label>
      <Act label="set" run={() => post(`/core/${fi}/latency`, { chunkLatencyMs: ms })} />
    </span>
  );
}

// ---- 5. Draft defaults -------------------------------------------------------------------------------------
export function DraftDefaults() {
  return (
    <div className="stack">
      <h2>Draft defaults <small className="muted">apply to the next batch that starts; running batches never change</small></h2>
      <div className="cards two">{FIS.map((fi) => <DefaultsCard key={fi} fi={fi} />)}</div>
    </div>
  );
}

function DefaultsCard({ fi }: { fi: string }) {
  const q = useQuery({ queryKey: ["defaults", fi], queryFn: () => get<Defaults>(`/draft-defaults/${fi}`) });
  const [form, setForm] = useState<Defaults | null>(null);
  useEffect(() => { if (q.data) setForm(q.data); }, [q.data]);
  if (!form) return <Card title={fi}><p className="muted">loading…</p></Card>;
  const field = (k: keyof Defaults, type: "number" | "text") => (
    <label key={k}>{k}<input type={type} value={form[k]} onChange={(e) => setForm({ ...form, [k]: type === "number" ? Number(e.target.value) : e.target.value })} /></label>
  );
  return (
    <Card title={fi} accent={ACCENT.temporal}>
      <div className="form">
        {field("chunkSize", "number")}{field("concurrency", "number")}{field("approvalWindow", "text")}{field("retryBudget", "text")}{field("retryMaxInterval", "text")}
      </div>
      <Act label="Save" tone="primary" run={() => put(`/draft-defaults/${fi}`, form).then(() => q.refetch())} />
    </Card>
  );
}

// ---- 6. Encryption -----------------------------------------------------------------------------------------------
type Inspect = { workflowId: string; activeKeyId: string; payloads: { event: string; metadata: Record<string, string>; dataBase64: string; sizeBytes: number; decoded: unknown; error?: string }[] };

export function Encryption() {
  const keys = useQuery({ queryKey: ["keys"], queryFn: () => get<{ activeKeyId: string; keys: { id: string; createdAt: string; active: boolean }[] }>("/encryption/keys"), refetchInterval: 3000 });
  const batches = useQuery({ queryKey: ["batches", ""], queryFn: () => get<BatchRow[]>("/batches") });
  const [wf, setWf] = useState("");
  const [result, setResult] = useState<Inspect | null>(null);
  return (
    <div className="stack">
      <h2>Encryption</h2>
      <Card title="Keyring" accent={ACCENT.security} actions={<Act label="Rotate key" tone="primary" run={() => post("/encryption/rotate").then(() => keys.refetch())} />}>
        <table><thead><tr><th>Key ID</th><th>Created</th><th>Status</th></tr></thead>
          <tbody>{keys.data?.keys.map((k) => <tr key={k.id}><td className="mono">{k.id}</td><td>{new Date(k.createdAt).toLocaleString()}</td><td>{k.active ? <span className="pill ok">active</span> : <span className="pill muted">decrypt only</span>}</td></tr>)}</tbody>
        </table>
        <p className="muted small">New payloads use the active key; old keys stay so existing histories still decode. Every process hot-reloads the file within ~2 s.</p>
      </Card>
      <Card title="Payload inspector: stored ciphertext vs decoded" accent={ACCENT.security}>
        <div className="row wrap">
          <select value={wf} onChange={(e) => setWf(e.target.value)}><option value="">pick a batch…</option>{batches.data?.map((b) => <option key={b.workflowId} value={b.workflowId}>{b.workflowId} · {b.status}</option>)}</select>
          <Act label="Inspect" tone="primary" disabled={!wf} run={async () => setResult(await get<Inspect>(`/encryption/inspect?workflowId=${encodeURIComponent(wf)}`))} />
        </div>
        {result?.payloads.map((p) => (
          <div key={p.event} className="cards two inspect">
            <div><h4>As stored by the Temporal Service</h4><p className="muted small">{p.event}</p>
              <p>encoding <b className="mono">{p.metadata.encoding}</b> · key <b className="mono hl">{p.metadata["encryption-key-id"] ?? "—"}</b> · {p.sizeBytes} bytes</p>
              <pre className="cipher">{p.dataBase64}…</pre></div>
            <div><h4>Decoded with the keyring</h4><pre>{p.error ?? JSON.stringify(p.decoded, null, 2)}</pre></div>
          </div>
        ))}
      </Card>
      <Card title="Temporal UI and CLI">
        <p>The Web UI decodes through the Codec Server (<span className="mono">--ui-codec-endpoint http://localhost:8089</span>). In the CLI compare:</p>
        <pre>{`temporal workflow show -n bulkwire -w <workflowId>                                         # ciphertext
temporal workflow show -n bulkwire -w <workflowId> --codec-endpoint http://localhost:8089  # decoded`}</pre>
      </Card>
    </div>
  );
}

// ---- 7. Versioning -------------------------------------------------------------------------------------------
type VInfo = { current?: string; ramping?: string; versions?: string[]; builds?: { buildId: string; pollers: number; runningBatches: number; containerState: string }[] };

export function Versioning() {
  const v = useQuery({ queryKey: ["versioning"], queryFn: () => get<VInfo>("/versioning"), refetchInterval: 2000 });
  const steps: [string, () => Promise<unknown>][] = [
    ["1. Start worker-v2 (buggy build)", () => post("/containers/worker-v2/start")],
    ["2. Make v2 current", () => post("/versioning/set-current", { buildId: "v2" })],
    ["4. Start worker-v3 (the fix)", () => post("/containers/worker-v3/start")],
    ["5. Make v3 current", () => post("/versioning/set-current", { buildId: "v3" })],
    ["6. Move pinned v2 batches to v3", () => post("/versioning/move-pinned", { fromBuildId: "v2", toBuildId: "v3" })],
    ["7. Stop worker-v2", () => post("/containers/worker-v2/stop")],
  ];
  return (
    <div className="stack">
      <h2>Versioning <small className="muted">“a run crashes midway because we shipped a bug” (F38)</small></h2>
      <Card title={`Deployment bulkwire · current ${v.data?.current ?? "?"}${v.data?.ramping ? ` · ramping ${v.data.ramping}` : ""}`} accent={ACCENT.workers}>
        <table><thead><tr><th>Build</th><th>Container</th><th className="num">Pollers</th><th className="num">Running batches pinned</th></tr></thead>
          <tbody>{v.data?.builds?.map((b) => <tr key={b.buildId}><td className="mono">{b.buildId}{b.buildId === v.data?.current ? " ★" : ""}</td><td>{b.containerState || "not created"}</td><td className="num">{b.pollers}</td><td className="num">{b.runningBatches}</td></tr>)}</tbody>
        </table>
      </Card>
      <Card title="Guided steps" accent={ACCENT.workers}>
        <ol className="steps">
          {steps.slice(0, 2).map(([l, run]) => <li key={l}><Act label={l} tone="primary" run={run} /></li>)}
          <li><b>3. Draft and approve a new batch</b> in the <a href={PORTAL.lakeshore} target="_blank" rel="noreferrer">Lakeshore portal</a>. It is pinned to v2 and stops right before authorize: the Workflow Task fails repeatedly with the demo panic (visible in the Temporal UI), the batch stays DRAFTED with all progress intact, and batches already running on v1 continue on v1.</li>
          {steps.slice(2).map(([l, run]) => <li key={l}><Act label={l} tone="primary" run={run} /></li>)}
        </ol>
        <p className="explain">Pinned workflows never run code they did not start with, so a bad deployment can't corrupt in-flight batches; a fix plus a targeted override moves only the affected executions.</p>
      </Card>
    </div>
  );
}

// ---- 8. Alerts & metrics ---------------------------------------------------------------------------------------
type Metrics = { alerts: { worker: string; activity: string; fi: string; value: number }[]; sdk: Record<string, Record<string, number>>; errors: Record<string, string> };

export function Alerts() {
  const hist = useRef<Record<string, number[]>>({});
  const m = useQuery({
    queryKey: ["metrics"],
    queryFn: async () => {
      const r = await get<Metrics>("/metrics");
      const sums: Record<string, number> = {};
      for (const a of r.alerts) sums[`${a.fi} · ${a.activity}`] = (sums[`${a.fi} · ${a.activity}`] ?? 0) + a.value;
      for (const [k, v] of Object.entries(sums)) hist.current[k] = [...(hist.current[k] ?? []), v].slice(-60);
      return { ...r, sums };
    },
    refetchInterval: 2000,
  });
  return (
    <div className="stack">
      <h2>Alerts & metrics</h2>
      <Card title="bulkwire_core_attempts_over_threshold{activity, fi}" accent={ACCENT.load}>
        <p className="explain">Counts Core Activity attempts beyond CORE_RETRY_ALERT_THRESHOLD (5). Retries are invisible to workflow code by design; this metric is how on-call learns about a stuck Core long before any retry budget runs out. In production, alert on its rate.</p>
        {Object.keys(m.data?.sums ?? {}).length === 0 ? <p className="muted">No attempts past the threshold yet (run F01 or F09).</p> : (
          <table><thead><tr><th>FI · Activity</th><th className="num">Count</th><th>Last 2 min</th></tr></thead>
            <tbody>{Object.entries(m.data!.sums).map(([k, v]) => <tr key={k}><td>{k}</td><td className="num">{v}</td><td><Sparkline values={hist.current[k] ?? [v]} color="#FFD166" /></td></tr>)}</tbody></table>
        )}
      </Card>
      <Card title="SDK metrics per worker">
        <table><thead><tr><th>Worker</th><th>Metric</th><th className="num">Value</th></tr></thead>
          <tbody>{Object.entries(m.data?.sdk ?? {}).flatMap(([w, vals]) => Object.entries(vals).map(([k, v]) => <tr key={w + k}><td>{w}</td><td className="mono small">{k}</td><td className="num">{v}</td></tr>))}</tbody></table>
        {Object.entries(m.data?.errors ?? {}).map(([w, e]) => <p key={w} className="err-text">{w}: {e}</p>)}
      </Card>
    </div>
  );
}

// ---- 9. Reset ----------------------------------------------------------------------------------------------------
export function Reset() {
  const [report, setReport] = useState<{ ok: boolean; steps: { step: string; ok: boolean; detail: string; ms: number }[] } | null>(null);
  return (
    <div className="stack">
      <h2>Reset</h2>
      <Card title="Return everything to a clean demo state" accent="#FF7A7A">
        <p className="muted">Unpause and start essential services, stop worker-v2/v3, make v1 current, terminate running batches (an operator tool, never part of the business flow), reset Core data/faults/latency/stats, remove the fairness limit, and reset draft defaults.</p>
        <Act label="Reset demo" tone="danger" run={async () => setReport(await post("/reset"))} />
        {report && (
          <table><thead><tr><th>Step</th><th>Result</th><th>Detail</th><th className="num">ms</th></tr></thead>
            <tbody>{report.steps.map((s) => <tr key={s.step}><td>{s.step}</td><td>{s.ok ? <span className="pill ok">ok</span> : <span className="pill bad">failed</span>}</td><td className="small">{s.detail}</td><td className="num">{s.ms}</td></tr>)}</tbody></table>
        )}
      </Card>
    </div>
  );
}
