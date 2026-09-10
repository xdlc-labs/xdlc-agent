import { useEffect, useRef } from "react";
import type { FixState } from "@/lib/api";
import type { LiveFix } from "@/lib/live-events";
import { formatAge } from "@/lib/utils";

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

/** One Fix on the /fixes grid: header with phase and age, then the live tail. */
export function FixCard({ live }: { live: LiveFix }) {
  const { fix, output, endedAt } = live;
  const pre = useRef<HTMLPreElement>(null);
  // Follow the tail unless the operator has scrolled up to read.
  useEffect(() => {
    const el = pre.current;
    if (!el) return;
    const nearBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
    if (nearBottom) el.scrollTop = el.scrollHeight;
  }, [output]);

  return (
    <li
      className={`mc-panel flex flex-col ${endedAt ? "opacity-70" : ""}`}
      aria-label={`${fix.repo} ${fix.state}`}
      data-state={fix.state}
    >
      <div className="flex flex-wrap items-center gap-2 border-b border-border/80 px-4 py-2.5">
        <span className="font-mono text-[12px] text-foreground">{fix.repo}</span>
        <span className="font-mono text-[10px] text-muted-foreground">
          {fix.source}
          {fix.provider ? ` · ${fix.provider}` : ""}
          {fix.attempt && fix.attempt > 1 ? ` · attempt ${fix.attempt}` : ""}
        </span>
        <span className={`ml-auto font-mono text-[10px] uppercase tracking-wider ${stateTone[fix.state]}`}>
          {fix.state}
        </span>
        <span className="font-mono text-[10px] tabular-nums text-muted-foreground">
          {endedAt ? "ended" : formatAge(fix.since)}
        </span>
      </div>
      <pre
        ref={pre}
        className="h-56 overflow-auto whitespace-pre-wrap px-4 py-3 font-mono text-[11px] leading-relaxed text-foreground"
      >
        {output || (
          <span className="text-muted-foreground">
            {fix.state === "queued" || fix.state === "cloning" ? "waiting for the agent to start…" : "no output yet"}
          </span>
        )}
      </pre>
      {fix.session_id ? (
        <div className="border-t border-border/60 px-4 py-1.5 font-mono text-[10px] text-muted-foreground">
          {fix.session_id}
        </div>
      ) : null}
    </li>
  );
}
