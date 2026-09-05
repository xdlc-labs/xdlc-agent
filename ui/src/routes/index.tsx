import { createFileRoute, Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { PipelineDiagram } from "@/components/pipeline";
import { ActivityFeed } from "@/components/activity-feed";
import { ActionTag, Dot } from "@/components/status";
import { fetchOverview, type Gate, type GateStatus, type Repo } from "@/lib/api";

export const Route = createFileRoute("/")({
  head: () => ({
    meta: [
      { title: "Overview — xdlc orchestrator console" },
      {
        name: "description",
        content:
          "Live loop status for xdlc-agent: CI, DEV smoke and PROD health gates with automatic Fix, Promote and Revert.",
      },
    ],
  }),
  component: Overview,
});

function Panel({
  title,
  action,
  children,
  delay = 0,
}: {
  title: string;
  action?: React.ReactNode;
  children: React.ReactNode;
  delay?: number;
}) {
  return (
    <section className="mc-panel flex min-h-[14rem] flex-col fade-up" style={{ animationDelay: `${delay}ms` }}>
      <div className="flex items-center justify-between border-b border-border/80 px-3.5 py-2.5">
        <h2 className="font-display text-[11px] font-semibold uppercase tracking-[0.2em] text-muted-foreground">
          {title}
        </h2>
        {action}
      </div>
      <div className="flex flex-1 flex-col p-3.5">{children}</div>
    </section>
  );
}

function healthTone(h: Repo["health"]): string {
  if (h === "healthy") return "text-pass";
  if (h === "degraded") return "text-acting";
  return "text-breach";
}

function ReposPanel({ repos }: { repos: Repo[] }) {
  if (repos.length === 0) {
    return <p className="font-mono text-[11px] text-muted-foreground">No repos configured.</p>;
  }
  return (
    <ul className="space-y-2">
      {repos.slice(0, 6).map((r) => (
        <li
          key={r.id}
          className="flex items-center justify-between gap-2 border-b border-border/40 pb-2 last:border-0 last:pb-0"
        >
          <div className="min-w-0">
            <div className="truncate font-mono text-[12px] text-foreground">{r.name}</div>
            <div className="font-mono text-[10px] text-muted-foreground">
              {r.branch} · {r.lastGate}
            </div>
          </div>
          <div className="flex shrink-0 flex-col items-end gap-1">
            <span className={`font-mono text-[10px] uppercase ${healthTone(r.health)}`}>{r.health}</span>
            <ActionTag action={r.lastAction} />
          </div>
        </li>
      ))}
    </ul>
  );
}

function gateCounts(gates: Gate[]) {
  const counts: Record<GateStatus, number> = { pass: 0, fail: 0, acting: 0, waiting: 0, idle: 0 };
  for (const g of gates) counts[g.status] += 1;
  return counts;
}

function GateHealthPanel({ gates }: { gates: Gate[] }) {
  const counts = gateCounts(gates);
  const total = gates.length || 1;
  const pct = Math.round((counts.pass / total) * 100);
  const r = 42;
  const c = 2 * Math.PI * r;
  const arc = c * 0.75;
  const offset = arc - (arc * pct) / 100;

  return (
    <div className="flex flex-1 flex-col items-center justify-center gap-4">
      <div className="relative size-[7.5rem]">
        <svg viewBox="0 0 120 120" className="size-full -rotate-[135deg]" aria-hidden>
          <circle
            cx="60"
            cy="60"
            r={r}
            fill="none"
            stroke="color-mix(in oklab, var(--border) 80%, transparent)"
            strokeWidth="8"
            strokeDasharray={`${arc} ${c}`}
            strokeLinecap="round"
          />
          <circle
            cx="60"
            cy="60"
            r={r}
            fill="none"
            stroke="var(--primary)"
            strokeWidth="8"
            strokeDasharray={`${arc} ${c}`}
            strokeDashoffset={offset}
            strokeLinecap="round"
            className="transition-[stroke-dashoffset] duration-700"
            style={{ filter: "drop-shadow(0 0 6px color-mix(in oklab, var(--primary) 60%, transparent))" }}
          />
        </svg>
        <div className="absolute inset-0 flex flex-col items-center justify-center pt-2">
          <span className="font-display text-2xl font-bold tabular-nums text-primary">{pct}%</span>
          <span className="font-mono text-[9px] uppercase tracking-wider text-muted-foreground">pass</span>
        </div>
      </div>
      <ul className="w-full space-y-1.5">
        {gates.map((g) => (
          <li key={g.name} className="flex items-center justify-between gap-2">
            <span className="flex items-center gap-1.5 font-mono text-[11px] text-foreground">
              <Dot status={g.status} pulse />
              {g.name}
            </span>
            <span className="font-mono text-[10px] uppercase text-muted-foreground">{g.status}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}

function BacklogPanel({ md, open }: { md: string; open: number }) {
  const lines = md
    .split("\n")
    .map((l) => l.trim())
    .filter((l) => l.length > 0 && !l.startsWith("#"))
    .slice(0, 6);

  return (
    <div className="flex flex-1 flex-col gap-3">
      <div className="font-mono text-[10px] uppercase tracking-wider text-muted-foreground">
        open{" "}
        <span className="font-display text-xl font-bold tabular-nums text-primary">{open}</span>
      </div>
      {lines.length === 0 ? (
        <p className="font-mono text-[11px] text-muted-foreground">BACKLOG.md empty.</p>
      ) : (
        <ul className="space-y-1.5">
          {lines.map((line, i) => (
            <li key={i} className="truncate font-mono text-[11px] text-foreground/90">
              {line.replace(/^[-*]\s*/, "")}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function ActionsPanel({
  fixes,
  promotes,
  reverts,
  lastActionAt,
}: {
  fixes: number;
  promotes: number;
  reverts: number;
  lastActionAt: string;
}) {
  const rows = [
    { label: "fix", value: fixes, tone: "text-acting", pct: Math.min(100, fixes * 20 || 6) },
    { label: "promote", value: promotes, tone: "text-pass", pct: Math.min(100, promotes * 20 || 6) },
    { label: "revert", value: reverts, tone: "text-breach", pct: Math.min(100, reverts * 20 || 6) },
  ];
  return (
    <div className="flex flex-1 flex-col gap-3">
      {rows.map((row) => (
        <div key={row.label}>
          <div className="mb-1 flex justify-between font-mono text-[10px] uppercase tracking-wider">
            <span className="text-muted-foreground">{row.label} today</span>
            <span className={`font-display text-sm tabular-nums ${row.tone}`}>{row.value}</span>
          </div>
          <div className="h-1.5 overflow-hidden rounded-sm bg-surface-2">
            <div
              className="h-full rounded-sm bg-primary/80 transition-[width] duration-700"
              style={{
                width: `${row.pct}%`,
                boxShadow: "0 0 8px color-mix(in oklab, var(--primary) 50%, transparent)",
              }}
            />
          </div>
        </div>
      ))}
      <p className="mt-auto font-mono text-[10px] text-muted-foreground">
        last · {(lastActionAt || "—").slice(11) || lastActionAt || "—"}
      </p>
    </div>
  );
}

function Overview() {
  const { data, isLoading } = useQuery({ queryKey: ["overview"], queryFn: fetchOverview, refetchInterval: 10_000 });
  const daemon = data?.daemon;
  const kpis = data?.kpis;
  const events = data?.events ?? [];
  const repos = data?.repos ?? [];
  const gates = data?.gates ?? [];

  return (
    <div className="px-6 py-7 lg:px-8">
      <div className="mb-6 flex flex-wrap items-end justify-between gap-3 fade-up">
        <div>
          <h1 className="font-display text-sm font-semibold uppercase tracking-[0.28em] text-primary glow-text">
            overview
          </h1>
          <p className="mt-2 font-mono text-[12px] text-muted-foreground">
            {isLoading
              ? "loading…"
              : `daemon ${daemon?.status ?? "—"} · uptime ${daemon?.uptime ?? "—"} · agent ${daemon?.agentProvider ?? "—"}`}
          </p>
        </div>
      </div>

      <PipelineDiagram pipeline={data?.pipeline ?? []} />

      <div className="mt-5 grid gap-3 md:grid-cols-2 xl:grid-cols-4">
        <Panel
          title="repos"
          delay={200}
          action={
            <Link to="/repos" className="font-mono text-[10px] text-primary hover:underline">
              all →
            </Link>
          }
        >
          <ReposPanel repos={repos} />
        </Panel>

        <Panel title="gate health" delay={260}>
          <GateHealthPanel gates={gates} />
        </Panel>

        <Panel
          title="backlog"
          delay={320}
          action={
            <Link to="/activity" className="font-mono text-[10px] text-primary hover:underline">
              view →
            </Link>
          }
        >
          <BacklogPanel md={data?.backlogMd ?? ""} open={kpis?.backlogOpen ?? 0} />
        </Panel>

        <Panel title="actions today" delay={380}>
          <ActionsPanel
            fixes={kpis?.fixes ?? 0}
            promotes={kpis?.promotes ?? 0}
            reverts={kpis?.reverts ?? 0}
            lastActionAt={kpis?.lastActionAt ?? "—"}
          />
        </Panel>
      </div>

      <section className="mt-5 mc-panel fade-up" style={{ animationDelay: "420ms" }}>
        <div className="flex items-center justify-between border-b border-border/80 px-4 py-2.5">
          <h2 className="font-display text-[11px] font-semibold uppercase tracking-[0.22em] text-muted-foreground">
            recent activity
          </h2>
          <Link to="/activity" className="font-mono text-[11px] text-primary hover:underline">
            full audit →
          </Link>
        </div>
        <ActivityFeed items={events.slice(0, 10)} dense />
      </section>
    </div>
  );
}
