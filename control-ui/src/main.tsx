// DEMO-ONLY Demo Control Center: the presenter's control surface (never shown to the audience as a bank UI).
// Status strip on top, sections on the left rail, the selected section in the main area (#hash routing).
import { StrictMode, useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import "@fontsource/ibm-plex-sans/400.css";
import "@fontsource/ibm-plex-sans/600.css";
import "./styles.css";
import { get, type Status } from "./api";
import { StatusStrip } from "./StatusStrip";
import { Overview, Batches, CatalogSection, Load, DraftDefaults, Encryption, Versioning, Alerts, Reset } from "./sections";

const SECTIONS = [
  ["overview", "Overview", Overview],
  ["batches", "Batches", Batches],
  ["catalog", "Failure catalog", CatalogSection],
  ["load", "Core load & queues", Load],
  ["defaults", "Draft defaults", DraftDefaults],
  ["encryption", "Encryption", Encryption],
  ["versioning", "Versioning", Versioning],
  ["alerts", "Alerts & metrics", Alerts],
  ["reset", "Reset", Reset],
] as const;

function useHash(): string {
  const [hash, setHash] = useState(window.location.hash.slice(1) || "overview");
  useEffect(() => {
    const on = () => setHash(window.location.hash.slice(1) || "overview");
    window.addEventListener("hashchange", on);
    return () => window.removeEventListener("hashchange", on);
  }, []);
  return hash;
}

function App() {
  const section = useHash();
  const status = useQuery({ queryKey: ["status"], queryFn: () => get<Status>("/status"), refetchInterval: 2000 });
  const active = SECTIONS.find(([id]) => id === section) ?? SECTIONS[0];
  const Section = active[2];
  return (
    <>
      <StatusStrip status={status.data} error={status.isError} />
      <div className="layout">
        <nav className="rail" aria-label="Sections">
          <div className="brand">Demo Control Center<small>presenter only · not part of any bank's UI</small></div>
          {SECTIONS.map(([id, label]) => (
            <a key={id} href={`#${id}`} className={id === active[0] ? "active" : ""}>{label}</a>
          ))}
        </nav>
        <main className="main">
          <Section status={status.data} />
        </main>
      </div>
    </>
  );
}

const queryClient = new QueryClient({ defaultOptions: { queries: { retry: 0, refetchOnWindowFocus: false } } });

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}><App /></QueryClientProvider>
  </StrictMode>,
);
