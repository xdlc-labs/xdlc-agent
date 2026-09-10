import { useEffect, useSyncExternalStore } from "react";
import type { QueryClient } from "@tanstack/react-query";
import { getToken } from "@/lib/auth";
import { eventsURL, type ActiveFix, type FixOutput } from "@/lib/api";

/**
 * Subscribe to GET /api/events SSE and invalidate console queries (issue #6).
 * Keeps slow refetchInterval as a safety net.
 *
 * Takes queryClient explicitly so root can call this before wrapping
 * children in QueryClientProvider (useQueryClient would throw there).
 */
export function useLiveEvents(queryClient: QueryClient) {
  useEffect(() => {
    let es: EventSource | null = null;
    let stopped = false;
    let reconnectTimer: number | null = null;

    // A burst of SSE events (one Fix emits several state changes and
    // dozens of output chunks in a second) used to fire a refetch of
    // every console query per event. Collect the keys instead and
    // invalidate once, 250ms after the first event of the burst. Later
    // events within that window join the batch rather than resetting the
    // timer, so a continuous stream still refreshes every 250ms.
    const pending = new Set<string>();
    let flushTimer: number | null = null;
    const flush = () => {
      flushTimer = null;
      const keys = [...pending];
      pending.clear();
      for (const key of keys) {
        void queryClient.invalidateQueries({ queryKey: [key] });
      }
    };
    const invalidateSoon = (...keys: string[]) => {
      for (const key of keys) pending.add(key);
      if (flushTimer === null) flushTimer = window.setTimeout(flush, 250);
    };

    const connect = () => {
      if (stopped) return;
      // EventSource cannot set Authorization; pass token via query when present.
      const tok = getToken();
      const url = tok ? `${eventsURL()}?access_token=${encodeURIComponent(tok)}` : eventsURL();
      es = new EventSource(url);
      // Fix state transitions ride the same stream under a named event.
      // The live list is small and cheap to refetch, so a transition
      // just invalidates it rather than patching a local copy that
      // could drift from what the daemon actually has. The /fixes grid
      // is the exception: it keeps finished Fixes on screen with their
      // output, which no endpoint can hand back, so it reads the payload.
      es.addEventListener("fix_state", (ev) => {
        liveFixes.onState(parse<ActiveFix>((ev as MessageEvent).data));
        invalidateSoon("fixes-active");
      });
      es.addEventListener("fix_output", (ev) => {
        liveFixes.onOutput(parse<FixOutput>((ev as MessageEvent).data));
      });
      es.onmessage = () => {
        invalidateSoon("overview", "history", "fix-prs", "backlog", "kpis", "repo", "fixes-active");
      };
      es.onerror = () => {
        es?.close();
        es = null;
        if (!stopped) {
          reconnectTimer = window.setTimeout(connect, 3000);
        }
      };
    };

    connect();
    return () => {
      stopped = true;
      es?.close();
      if (reconnectTimer !== null) window.clearTimeout(reconnectTimer);
      if (flushTimer !== null) window.clearTimeout(flushTimer);
      pending.clear();
    };
  }, [queryClient]);
}

function parse<T>(data: unknown): T | null {
  if (typeof data !== "string") return null;
  try {
    return JSON.parse(data) as T;
  } catch {
    return null;
  }
}

/** A Fix as the grid shows it: the last state seen plus the output tail. */
export interface LiveFix {
  fix: ActiveFix;
  output: string;
  /** Set when a terminal fix_state arrived; the card stays, marked done. */
  endedAt?: number;
}

/** How long a finished Fix stays on the grid before it is dropped. */
const keepFinishedMs = 10 * 60_000;

/** Bound on the client-side tail, matching the daemon's per-Fix cap. */
const maxTailChars = 16 * 1024;

/**
 * liveFixes is the client-side mirror of the daemon's fix tracker plus
 * each Fix's output tail, fed by the SSE listeners above. Module-level
 * so a route mounted after the stream connected still sees what has
 * already arrived, and read through useLiveFixes() so React re-renders
 * on change.
 *
 * Entries are never mutated in place: every change replaces the LiveFix
 * object, so a card whose Fix did not change keeps the same reference
 * and React.memo can skip re-rendering it.
 */
class LiveFixStore {
  private fixes = new Map<string, LiveFix>();
  private listeners = new Set<() => void>();
  private snapshot: LiveFix[] = [];

  subscribe = (fn: () => void) => {
    this.listeners.add(fn);
    return () => {
      this.listeners.delete(fn);
    };
  };

  getSnapshot = () => this.snapshot;

  /** Seed from GET /api/fixes/active, for the poll fallback and first paint. */
  seed(active: ActiveFix[]) {
    let changed = false;
    for (const f of active) {
      const cur = this.fixes.get(f.id);
      if (!cur) {
        this.fixes.set(f.id, { fix: f, output: "" });
        changed = true;
      } else if (!cur.endedAt && (cur.fix.state !== f.state || cur.fix.since !== f.since)) {
        this.fixes.set(f.id, { ...cur, fix: f });
        changed = true;
      }
    }
    if (changed) this.publish();
  }

  onState(f: ActiveFix | null) {
    if (!f) return;
    const cur = this.fixes.get(f.id);
    const terminal = f.state === "ok" || f.state === "error";
    const next: LiveFix = { fix: f, output: cur?.output ?? "" };
    if (cur?.endedAt) next.endedAt = cur.endedAt;
    if (terminal) next.endedAt = Date.now();
    this.fixes.set(f.id, next);
    this.publish();
  }

  onOutput(o: FixOutput | null) {
    if (!o) return;
    // Output for a Fix whose state event was missed is still worth a
    // card, with the state unknown until the next transition.
    const cur: LiveFix = this.fixes.get(o.id) ?? {
      fix: { id: o.id, repo: o.repo ?? "", source: "", state: "fixing", since: new Date().toISOString() },
      output: "",
    };
    let output = o.snapshot ? o.text : cur.output + o.text;
    if (output.length > maxTailChars) {
      const cut = output.length - maxTailChars;
      const nl = output.indexOf("\n", cut);
      output = output.slice(nl >= 0 ? nl + 1 : cut);
    }
    this.fixes.set(o.id, { ...cur, output });
    this.publish();
  }

  /** Test and sign-out hook. */
  reset() {
    this.fixes.clear();
    this.publish();
  }

  private publish() {
    const now = Date.now();
    for (const [id, f] of this.fixes) {
      if (f.endedAt && now - f.endedAt > keepFinishedMs) this.fixes.delete(id);
    }
    // Running Fixes first, oldest first; finished ones after, newest first.
    this.snapshot = [...this.fixes.values()].sort((a, b) => {
      if (!!a.endedAt !== !!b.endedAt) return a.endedAt ? 1 : -1;
      if (a.endedAt && b.endedAt) return b.endedAt - a.endedAt;
      return a.fix.since.localeCompare(b.fix.since);
    });
    for (const fn of this.listeners) fn();
  }
}

export const liveFixes = new LiveFixStore();

export function useLiveFixes(): LiveFix[] {
  return useSyncExternalStore(liveFixes.subscribe, liveFixes.getSnapshot, liveFixes.getSnapshot);
}
