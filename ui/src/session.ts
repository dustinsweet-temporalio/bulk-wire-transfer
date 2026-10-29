// Portal session. WHY sessionStorage (not localStorage): each browser window/tab keeps its own session, so the
// presenter can be Jordan in one window and Taylor in another at the same FI; separate origins (3001, 3002)
// keep Lakeshore and Prairie sessions apart. Every storage access is guarded (private windows can throw).
import type { User } from "./types";

const TOKEN = "bulkwire.token";
const USER = "bulkwire.user";
const ANNOTATIONS = "bulkwire.annotations";

function read(key: string): string | null {
  try {
    return sessionStorage.getItem(key);
  } catch {
    return null;
  }
}

function write(key: string, value: string | null) {
  try {
    if (value === null) sessionStorage.removeItem(key);
    else sessionStorage.setItem(key, value);
  } catch {
    // storage unavailable: the session simply does not survive a reload
  }
}

export const session = {
  token: () => read(TOKEN),
  user: (): User | null => {
    const raw = read(USER);
    return raw ? (JSON.parse(raw) as User) : null;
  },
  signIn(token: string, user: User) {
    write(TOKEN, token);
    write(USER, JSON.stringify(user));
  },
  signOut() {
    write(TOKEN, null);
    write(USER, null);
  },
  // DEMO-ONLY study aid: engineering annotations naming the Temporal primitive behind each UI element.
  annotations: () => read(ANNOTATIONS) === "on",
  setAnnotations(on: boolean) {
    write(ANNOTATIONS, on ? "on" : null);
  },
};
