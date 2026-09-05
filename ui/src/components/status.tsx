import type { ActionKind, GateStatus } from "@/lib/api";

const statusColor: Record<GateStatus, string> = {
  pass: "bg-pass",
  fail: "bg-breach",
  acting: "bg-acting",
  waiting: "bg-waiting",
  idle: "bg-idle",
};

const statusText: Record<GateStatus, string> = {
  pass: "text-pass",
  fail: "text-breach",
  acting: "text-acting",
  waiting: "text-waiting",
  idle: "text-idle",
};

export function Dot({ status, pulse }: { status: GateStatus; pulse?: boolean }) {
  return (
    <span
      className={`inline-block size-2 rounded-full ${statusColor[status]} ${
        pulse && status === "acting" ? "pulse-stage" : ""
      }`}
    />
  );
}

export function StatusTag({ status, label }: { status: GateStatus; label?: string }) {
  return (
    <span
      className={`inline-flex items-center gap-1.5 border border-border bg-surface px-2 py-0.5 font-mono text-[11px] uppercase tracking-wider ${statusText[status]}`}
    >
      <Dot status={status} pulse />
      {label ?? status}
    </span>
  );
}

const actionText: Record<ActionKind, string> = {
  Fix: "text-acting border-acting/40",
  Promote: "text-pass border-pass/40",
  Revert: "text-breach border-breach/40",
  Rerun: "text-waiting border-waiting/40",
  None: "text-muted-foreground border-border",
};

export function ActionTag({ action }: { action: ActionKind | "GitOps side-effect" }) {
  const cls = action === "GitOps side-effect" ? "text-primary border-primary/40" : actionText[action];
  return (
    <span className={`inline-block border px-2 py-0.5 font-mono text-[11px] tracking-wide ${cls}`}>
      {action === "None" ? "—" : action}
    </span>
  );
}

export function Mono({ children, className = "" }: { children: React.ReactNode; className?: string }) {
  return <span className={`font-mono text-[12px] text-muted-foreground ${className}`}>{children}</span>;
}

export function PageHeader({ title, sub }: { title: string; sub: string }) {
  return (
    <div className="border-b border-border/80 bg-surface/30 px-6 py-5">
      <h1 className="font-mono text-sm uppercase tracking-[0.2em] text-primary">{title}</h1>
      <p className="mt-1.5 max-w-2xl text-sm text-muted-foreground">{sub}</p>
    </div>
  );
}
