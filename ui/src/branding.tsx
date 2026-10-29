// White-label branding: one build, themed at runtime for this origin's FI (window.FI_ID from /config.js).
import { createContext, useContext, useEffect, useState, type ReactNode } from "react";
import { api } from "./api";
import type { Branding } from "./types";

declare global {
  interface Window {
    FI_ID?: string;
  }
}

export const fiId = (): string => window.FI_ID ?? "lakeshore";

const BrandingContext = createContext<Branding | null>(null);

export function useBranding(): Branding {
  const b = useContext(BrandingContext);
  if (!b) throw new Error("branding not loaded");
  return b;
}

export function BrandingProvider({ children }: { children: ReactNode }) {
  const [branding, setBranding] = useState<Branding | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api
      .branding(fiId())
      .then((b) => {
        const root = document.documentElement.style;
        root.setProperty("--brand-primary", b.primaryColor);
        root.setProperty("--brand-accent", b.accentColor);
        document.title = `${b.displayName} · Business Online Banking`;
        setBranding(b);
      })
      .catch(() => setError("The bank's online banking service is not reachable. Refresh in a moment."));
  }, []);

  if (error) return <div className="empty" role="alert">{error}</div>;
  if (!branding) return <div className="empty">Loading…</div>;
  return <BrandingContext.Provider value={branding}>{children}</BrandingContext.Provider>;
}

/** Our own simple SVG wordmark (no real institution's mark). */
export function Wordmark({ branding, size = 30 }: { branding: Branding; size?: number }) {
  return (
    <span className="wordmark">
      <svg width={size} height={size} viewBox="0 0 32 32" aria-hidden="true">
        <circle cx="16" cy="16" r="15" fill={branding.accentColor} />
        {branding.fiId === "prairie" ? (
          <path d="M6 21c4-6 8-6 10-2s6 4 10-3v8H6z" fill={branding.primaryColor} />
        ) : (
          <path d="M5 20c3-2 5-2 8 0s5 2 8 0 5-2 6-1v6H5z M9 15l7-6 7 6" fill={branding.primaryColor} stroke={branding.primaryColor} strokeWidth="1.5" />
        )}
      </svg>
      {branding.wordmark}
    </span>
  );
}
