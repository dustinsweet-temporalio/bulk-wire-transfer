// "Show engineering annotations" toggle (user menu, under "Demo"). Stored in sessionStorage per window.
import { useSyncExternalStore } from "react";
import { session } from "./session";

const listeners = new Set<() => void>();

export function useAnnotations(): [boolean, (on: boolean) => void] {
  const on = useSyncExternalStore(
    (cb) => {
      listeners.add(cb);
      return () => listeners.delete(cb);
    },
    () => session.annotations(),
  );
  return [
    on,
    (value: boolean) => {
      session.setAnnotations(value);
      listeners.forEach((l) => l());
    },
  ];
}
