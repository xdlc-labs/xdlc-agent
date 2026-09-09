import { useQuery } from "@tanstack/react-query";
import { fetchActiveFixes, type ActiveFix, type FixState } from "@/lib/api";
import { formatAge } from "@/lib/utils";

/**
 * Live Fix states. Transitions arrive over the /api/events SSE stream as
 * the named "fix_state" event, which invalidates this query (see
 * lib/live-events.ts); the interval is a safety net for a dropped
 * stream, matching how the rest of the console polls.
 */
function useActiveFixes() {
  return useQuery({
    queryKey: ["fixes-active"],
    queryFn: fetchActiveFixes,
    refetchInterval: 10_000,
  });
}

/**
 * Phase ordering as a Fix passes through it, for the progress bar. The
 * terminal states are not here: a finished Fix leaves the live list, and
 * its outcome belongs to the audit row.
 */
const phases: FixState[] = ["queued", "cloning", "planning", "fixing", "pushing", "verifying"];

/** Colors track the phase's meaning, not its position: waiting is idle,
 * the agent working is "acting", and the two xdlc-owned steps around it
 * read as progress. */
const stateTone: Record<FixState, string> = {
  queued: "text-waiting",
  cloning: "text-muted-foreground",
  planning: "text-acting",
  fixing: "text-acting",
  pushing: "text-primary",
  verifying: "text-primary",
  ok: "text-pass",
  error: "text-breach",
};

function FixRow({ fix }: { fix: ActiveFix }) {
  // A skipped phase (no plan pass, no worktree push, no reverify) must
  // not read as "not reached yet", so the bar fills to the current
  // phase's position rather than counting phases completed.
  const idx = phases.indexOf(fix.state);
  const pct = idx < 0 ? 100 : Math.round(((idx + 1) / phases.length) * 100);

  return (
    <li className="flex flex-col gap-1.5 border-b border-border/40 pb-2.5 last:border-0 last:pb-0">
      <div className="flex items-center justify-between gap-2">
        <div className="min-w-0">
          <span className="truncate font-mono text-[12px] text-foreground">{fix.repo}</span>
          <span className="ml-2 font-mono text-[10px] text-muted-foreground">
            {fix.source}
            {fix.provider ? ` · ${fix.provider}` : ""}
            {fix.attempt && fix.attempt > 1 ? ` · attempt ${fix.attempt}` : ""}
          </span>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <span className={`font-mono text-[10px] uppercase tracking-wider ${stateTone[fix.state]}`}>
            {fix.state}
          </span>
          <span className="font-mono text-[10px] tabular-nums text-muted-foreground">{formatAge(fix.since)}</span>
        </div>
      </div>
      <div className="h-1 overflow-hidden rounded-sm bg-surface-2">
        <div
          className="h-full rounded-sm bg-primary/80 transition-[width] duration-700"
          style={{ width: `${pct}%` }}
        />
      </div>
      {fix.session_id ? (
        <span className="truncate font-mono text-[10px] text-muted-foreground/80">{fix.session_id}</span>
      ) : null}
    </li>
  );
}

/**
 * One row per Fix running right now, with the phase it is in and how
 * long it has been there. `fix_queue_depth` could only say how many
 * there were, so a Fix three minutes into a clone looked exactly like
 * one three minutes into an agent run.
 *
 * Renders nothing when nothing is running, unless `showEmpty` — the
 * Actions page says so explicitly, because there "no Fix is running" is
 * the answer to the button the operator just pressed.
 */
export function FixesInFlight({ showEmpty = false, delay = 0 }: { showEmpty?: boolean; delay?: number }) {
  const { data, isError, isPending } = useActiveFixes();
  const fixes = data ?? [];

  // A failed poll is not worth a banner here: the audit feed and the
  // degraded banner already report an unreachable daemon, and an error
  // box where a Fix list belongs would be the loudest thing on a page
  // that is otherwise fine.
  //
  // Pending is also nothing: "No Fix running" before the first response
  // would be a claim the console cannot yet make, and on the Actions
  // page it is the very question the operator is asking.
  if (isError || isPending || (fixes.length === 0 && !showEmpty)) return null;

  return (
    <section className="mc-panel fade-up" style={{ animationDelay: `${delay}ms` }}>
      <div className="flex items-center justify-between border-b border-border/80 px-4 py-2.5">
        <h2 className="font-display text-[11px] font-semibold uppercase tracking-[0.22em] text-muted-foreground">
          in flight
        </h2>
        <span className="font-mono text-[10px] tabular-nums text-muted-foreground">
          {fixes.length === 0 ? "idle" : `${fixes.length} fix${fixes.length === 1 ? "" : "es"}`}
        </span>
      </div>
      <div className="p-3.5">
        {fixes.length === 0 ? (
          <p className="font-mono text-[11px] text-muted-foreground">No Fix running.</p>
        ) : (
          <ul className="space-y-2.5">
            {fixes.map((f) => (
              <FixRow key={f.id} fix={f} />
            ))}
          </ul>
        )}
      </div>
    </section>
  );
}
