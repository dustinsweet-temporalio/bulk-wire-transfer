// Small shared UI pieces: status pill, engineering annotation, dialog, empty/loading states, toasts.
import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { ExternalLink } from "lucide-react";
import { useAnnotations } from "../annotations";
import { phasePill, type PillTone } from "../format";

export function Pill({ tone, children }: { tone: PillTone; children: ReactNode }) {
  return <span className={`pill ${tone}`}>{children}</span>;
}

export function PhasePill({ phase }: { phase: string }) {
  const p = phasePill(phase);
  return <Pill tone={p.tone}>{p.label}</Pill>;
}

// Official references for each pattern (docs/TRACEABILITY.md).
export const DOCS = {
  query: "https://docs.temporal.io/evaluate/features/workflow-message-passing",
  updateWithStart: "https://docs.temporal.io/sending-messages#update-with-start",
  searchAttributes: "https://docs.temporal.io/search-attribute",
  describe: "https://docs.temporal.io/encyclopedia/retry-policies",
  update: "https://docs.temporal.io/encyclopedia/workflow-message-passing",
  approval: "https://docs.temporal.io/design-patterns/approval",
  timer: "https://docs.temporal.io/design-patterns/approval",
  idempotency: "https://docs.temporal.io/workflow-execution/workflowid-runid",
  coreTvp: "https://learn.microsoft.com/sql/relational-databases/tables/use-table-valued-parameters-database-engine",
  workers: "https://docs.temporal.io/workers",
  resumable: "https://docs.temporal.io/design-patterns/resumable-activity",
} as const;

/** Engineering annotation: names the Temporal primitive behind a UI element (study aid; toggle in user menu). */
export function Annotation({ children, href }: { children: ReactNode; href: string }) {
  const [on] = useAnnotations();
  if (!on) return null;
  return (
    <a className="annotation" href={href} target="_blank" rel="noreferrer" title="Engineering annotation (demo study aid)">
      {children} <ExternalLink size={11} aria-hidden="true" />
    </a>
  );
}

export function Dialog({ title, onClose, children }: { title: string; onClose: () => void; children: ReactNode }) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const prev = document.activeElement as HTMLElement | null;
    ref.current?.querySelector<HTMLElement>("input, textarea, button")?.focus();
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("keydown", onKey);
      prev?.focus();
    };
  }, [onClose]);
  return (
    <div className="overlay" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="dialog" role="dialog" aria-modal="true" aria-label={title} ref={ref}>
        <h2>{title}</h2>
        {children}
      </div>
    </div>
  );
}

export function Empty({ children }: { children: ReactNode }) {
  return <div className="empty">{children}</div>;
}

export function Skeleton({ lines = 4 }: { lines?: number }) {
  return (
    <div aria-busy="true" aria-label="Loading">
      {Array.from({ length: lines }, (_, i) => (
        <div key={i} className="skeleton" style={{ width: `${90 - i * 12}%` }} />
      ))}
    </div>
  );
}

type Toast = { id: number; text: string; error: boolean };
const ToastContext = createContext<(text: string, error?: boolean) => void>(() => {});

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const push = useCallback((text: string, error = false) => {
    const id = Date.now() + Math.random();
    setToasts((t) => [...t, { id, text, error }]);
    setTimeout(() => setToasts((t) => t.filter((x) => x.id !== id)), 6000);
  }, []);
  return (
    <ToastContext.Provider value={push}>
      {children}
      <div className="toasts" role="status" aria-live="polite">
        {toasts.map((t) => (
          <div key={t.id} className={`toast ${t.error ? "error" : ""}`}>{t.text}</div>
        ))}
      </div>
    </ToastContext.Provider>
  );
}

export const useToast = () => useContext(ToastContext);
