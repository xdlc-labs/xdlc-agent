import type { GateStatus } from "@/lib/api";
import { StatusTag } from "@/components/status";

function hexClass(status: GateStatus): string {
  switch (status) {
    case "fail":
      return "hex-fail";
    case "acting":
      return "hex-acting pulse-stage";
    case "pass":
      return "hex-pass";
    case "waiting":
      return "hex-waiting";
    default:
      return "hex-idle";
  }
}

function statusLabel(status: GateStatus): string {
  switch (status) {
    case "fail":
      return "breach";
    case "acting":
      return "acting";
    case "pass":
      return "pass";
    case "waiting":
      return "waiting";
    default:
      return "idle";
  }
}

function statusTone(status: GateStatus): string {
  switch (status) {
    case "fail":
      return "text-breach";
    case "acting":
      return "text-acting";
    case "pass":
      return "text-pass";
    case "waiting":
      return "text-waiting";
    default:
      return "text-idle";
  }
}

function railActive(status: GateStatus, next?: GateStatus): boolean {
  return status === "acting" || status === "pass" || next === "acting" || next === "pass";
}

export function PipelineDiagram({
  pipeline,
}: {
  pipeline: { stage: string; label: string; status: GateStatus; detail: string }[];
}) {
  return (
    <div className="relative mc-panel grid-lines overflow-hidden px-5 py-8 fade-up sm:px-8">
      <div
        className="pointer-events-none absolute inset-0 opacity-50"
        style={{
          background:
            "radial-gradient(ellipse 70% 55% at 50% 45%, color-mix(in oklab, var(--primary) 28%, transparent), transparent 68%)",
        }}
        aria-hidden
      />

      <div className="relative mb-8 flex flex-wrap items-baseline justify-between gap-2">
        <h2 className="font-display text-[11px] font-semibold uppercase tracking-[0.28em] text-primary">
          pipeline flow
        </h2>
        <span className="font-mono text-[11px] text-muted-foreground">
          stages execute in sequence · fail fast · Fix / Promote / Revert
        </span>
      </div>

      <div className="relative flex flex-col items-stretch gap-4 sm:flex-row sm:items-center sm:gap-1">
        {pipeline.map((s, i) => {
          const next = pipeline[i + 1]?.status;
          const active = railActive(s.status, next);
          return (
            <div
              key={s.stage}
              className="flex flex-1 flex-col items-center gap-2 fade-scale sm:flex-row sm:items-center"
              style={{ animationDelay: `${120 + i * 90}ms` }}
            >
              <div className="w-full min-w-0 flex-1">
                <div className={`hex-node ${hexClass(s.status)}`}>
                  <span className="font-display text-[11px] font-semibold uppercase tracking-[0.12em] text-foreground">
                    {s.label}
                  </span>
                  <span className="mt-1 max-w-[9rem] truncate font-mono text-[10px] text-muted-foreground">
                    {s.detail}
                  </span>
                </div>
                <div className={`mt-1.5 text-center font-mono text-[10px] uppercase tracking-[0.16em] ${statusTone(s.status)}`}>
                  {statusLabel(s.status)}
                </div>
              </div>
              {i < pipeline.length - 1 && (
                <div
                  className={`my-1 rotate-90 sm:my-0 sm:rotate-0 flow-rail ${active ? "flow-rail-active" : ""}`}
                  aria-hidden
                />
              )}
            </div>
          );
        })}
      </div>

      <div className="relative mt-7 flex flex-wrap items-center gap-3 border-t border-border/70 pt-4">
        <span className="font-mono text-[11px] uppercase tracking-wider text-breach">↺ revert loop</span>
        <span className="font-mono text-[11px] text-muted-foreground">
          PROD breach → git revert on main → rollback-first, then Fix on develop
        </span>
      </div>
      <div className="relative mt-3 flex flex-wrap gap-2">
        <StatusTag status="pass" label="pass" />
        <StatusTag status="acting" label="acting" />
        <StatusTag status="fail" label="breach" />
        <StatusTag status="waiting" label="waiting" />
        <StatusTag status="idle" label="idle" />
      </div>
    </div>
  );
}
