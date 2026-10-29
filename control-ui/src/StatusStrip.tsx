// Always-visible status strip: Temporal, workers (with build IDs), each Core and its SQL Server, the claim-check
// backend, bulkwire-service, and the active encryption key. Color = healthy / degraded / down / paused.
import type { Container, Status } from "./api";
import { Chip, type Health } from "./components";

function containerHealth(c?: Container): Health {
  if (!c) return "off";
  if (c.state === "paused") return "paused";
  if (c.state !== "running") return "down";
  if (c.health === "unhealthy") return "degraded";
  return "ok";
}

export function StatusStrip({ status, error }: { status?: Status; error: boolean }) {
  if (error) return <header className="strip"><Chip label="control-service unreachable" health="down" /></header>;
  const cs = new Map((status?.containers ?? []).map((c) => [c.service, c]));
  const buildPollers = (status?.pollers?.workflow ?? []).reduce<Record<string, number>>((m, p) => ({ ...m, [p.buildId || "unversioned"]: (m[p.buildId || "unversioned"] ?? 0) + 1 }), {});
  const backend = status?.claimCheck?.backend ?? "redis";
  const backendSvc = { redis: "redis", s3: "rustfs", gcs: "fake-gcs" }[backend] ?? backend;
  const core = (fi: string): Health => {
    const c = cs.get(`core-${fi}`);
    const base = containerHealth(c);
    if (base !== "ok") return base;
    const h = status?.core?.[fi];
    if (!h?.healthy) return "degraded";
    return h.state && h.state.fault.mode !== "none" ? "degraded" : "ok";
  };
  const coreDetail = (fi: string) => {
    const s = status?.core?.[fi]?.state;
    return s ? `${s.fault.mode !== "none" ? s.fault.mode + " · " : ""}${s.chunkLatencyMs} ms` : "";
  };
  return (
    <header className="strip" aria-label="System status">
      <Chip label="Temporal" health={status?.temporal?.reachable ? containerHealth(cs.get("temporal")) : "down"} detail={status?.deployment?.current ? `current ${status.deployment.current}` : ""} />
      {["worker-a", "worker-b", "worker-v2", "worker-v3"].map((w) => {
        const optional = w === "worker-v2" || w === "worker-v3"; // versioning demo only: "off" until started
        const h = containerHealth(cs.get(w));
        return <Chip key={w} label={w} health={optional && h === "down" ? "off" : h} detail={optional ? w.slice(-2) : "v1"} />;
      })}
      <Chip label="pollers" health={Object.keys(buildPollers).length ? "ok" : "down"} detail={Object.entries(buildPollers).map(([b, n]) => `${b}×${n}`).join(" ")} />
      <Chip label="Core Lakeshore" health={core("lakeshore")} detail={coreDetail("lakeshore")} />
      <Chip label="Core Prairie" health={core("prairie")} detail={coreDetail("prairie")} />
      <Chip label="SQL Lakeshore" health={containerHealth(cs.get("sql-lakeshore"))} />
      <Chip label="SQL Prairie" health={containerHealth(cs.get("sql-prairie"))} />
      <Chip label={`Claim check (${backend})`} health={status?.claimCheck?.reachable ? containerHealth(cs.get(backendSvc)) : "down"} />
      <Chip label="bulkwire-service" health={containerHealth(cs.get("bulkwire-service"))} />
      <Chip label="key" health="ok" detail={status?.activeKeyId ?? ""} />
    </header>
  );
}
