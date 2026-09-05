import { useMemo, useState } from "react";
import { createFileRoute } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { fetchOverview, fetchHistory, fetchBacklog, fetchCostKPIs } from "@/lib/api";
import { PageHeader, ActionTag } from "@/components/status";
import { QueryError, Skeleton } from "@/components/query-state";

export const Route = createFileRoute("/activity")({
  head: () => ({
    meta: [{ title: "Activity — signal and action audit | xdlc-agent" }],
  }),
  component: Activity,
});

function Activity() {
  const overviewQ = useQuery({ queryKey: ["overview"], queryFn: fetchOverview, refetchInterval: 10_000 });
  const historyQ = useQuery({
    queryKey: ["history"],
    queryFn: () => fetchHistory(200),
    refetchInterval: 10_000,
  });
  const backlogQ = useQuery({ queryKey: ["backlog"], queryFn: fetchBacklog, refetchInterval: 15_000 });
  const kpisQ = useQuery({ queryKey: ["kpis"], queryFn: fetchCostKPIs, refetchInterval: 30_000 });

  const overview = overviewQ.data;
  const events = historyQ.data ?? [];
  const backlogMd = backlogQ.data ?? "";
  const costKPIs = kpisQ.data;

  const [view, setView] = useState<"history" | "backlog">("history");
  const [repo, setRepo] = useState("all");
  const [action, setAction] = useState("all");
  const [expanded, setExpanded] = useState<string | null>(null);

  const repos = overview?.repos ?? [];
  const filtered = useMemo(
    () =>
      events.filter(
        (e) => (repo === "all" || e.repo === repo) && (action === "all" || e.action === action),
      ),
    [events, repo, action],
  );

  const selectCls =
    "border border-border bg-surface px-2 py-1 font-mono text-[11px] text-foreground outline-none focus:border-primary";

  const t = costKPIs?.totals;
  const successPct =
    t?.fix_success_rate != null ? `${Math.round(t.fix_success_rate * 100)}%` : "—";

  if (historyQ.isPending && overviewQ.isPending) {
    return (
      <div>
        <PageHeader title="activity / audit" sub="Every signal and action is written to the history store and BACKLOG.md." />
        <Skeleton rows={6} />
      </div>
    );
  }
  if (historyQ.isError) {
    return (
      <div>
        <PageHeader title="activity / audit" sub="Every signal and action is written to the history store and BACKLOG.md." />
        <QueryError
          message={historyQ.error instanceof Error ? historyQ.error.message : "Failed to load history"}
          onRetry={() => void historyQ.refetch()}
        />
      </div>
    );
  }

  return (
    <div>
      <PageHeader title="activity / audit" sub="Every signal and action is written to the history store and BACKLOG.md." />

      {t && t.fixes > 0 && (
        <div className="flex flex-wrap gap-6 border-b border-border px-6 py-3 font-mono text-[11px]">
          <div>
            <span className="text-muted-foreground">fix cost </span>
            <span className="text-foreground">${t.total_cost_usd.toFixed(4)}</span>
          </div>
          <div>
            <span className="text-muted-foreground">fix success </span>
            <span className="text-foreground">{successPct}</span>
          </div>
          <div>
            <span className="text-muted-foreground">fixes / reverts </span>
            <span className="text-foreground">
              {t.fixes} / {t.reverts}
            </span>
          </div>
          {t.duration_p95_ms != null && (
            <div>
              <span className="text-muted-foreground">p95 fix ms </span>
              <span className="text-foreground">{Math.round(t.duration_p95_ms)}</span>
            </div>
          )}
        </div>
      )}

      <div className="flex flex-wrap items-center gap-3 border-b border-border px-6 py-3">
        <div className="flex">
          {(["history", "backlog"] as const).map((v) => (
            <button
              key={v}
              onClick={() => setView(v)}
              className={`border px-3 py-1 font-mono text-[11px] ${
                view === v ? "border-primary text-primary" : "border-border text-muted-foreground"
              }`}
            >
              {v === "history" ? "History" : "BACKLOG.md"}
            </button>
          ))}
        </div>
        {view === "history" && (
          <>
            <select value={repo} onChange={(e) => setRepo(e.target.value)} className={selectCls}>
              <option value="all">all repos</option>
              {repos.map((r) => (
                <option key={r.id} value={r.id}>
                  {r.id}
                </option>
              ))}
            </select>
            <select value={action} onChange={(e) => setAction(e.target.value)} className={selectCls}>
              {["all", "Fix", "Promote", "Revert", "Rerun", "None"].map((a) => (
                <option key={a} value={a}>
                  {a === "all" ? "all actions" : a}
                </option>
              ))}
            </select>
            <span className="font-mono text-[11px] text-muted-foreground">{filtered.length} records</span>
          </>
        )}
      </div>

      {view === "history" ? (
        <div className="px-6 py-6">
          {filtered.length === 0 ? (
            <p className="border border-border bg-card px-4 py-10 text-center text-sm text-muted-foreground">
              No records yet — run daemon; gates write here when they fire.
            </p>
          ) : (
            <ol className="border border-border bg-card">
              {filtered.map((e) => (
                <li key={e.id} className="border-b border-border last:border-0">
                  <button
                    onClick={() => setExpanded(expanded === e.id ? null : e.id)}
                    className="flex w-full flex-wrap items-center gap-3 px-4 py-3 text-left hover:bg-surface"
                  >
                    <span className="w-40 font-mono text-[11px] text-muted-foreground">{e.ts}</span>
                    <span className="w-36 font-mono text-[12px] text-foreground">{e.repo}</span>
                    <span className="w-28 font-mono text-[11px] text-muted-foreground">{e.gate}</span>
                    <span className="w-24 font-mono text-[11px] text-muted-foreground">{e.source}</span>
                    <span className="w-28 font-mono text-[11px] text-muted-foreground">{e.signal}</span>
                    <ActionTag action={e.action} />
                    <span className={`font-mono text-[11px] ${e.ok ? "text-pass" : "text-breach"}`}>
                      {e.ok ? "ok" : "fail"}
                    </span>
                    <span className="ml-auto font-mono text-[11px] text-muted-foreground">
                      {expanded === e.id ? "−" : "+"}
                    </span>
                  </button>
                  {expanded === e.id && (
                    <div className="border-t border-border bg-surface px-4 py-3">
                      <div className="mb-1 font-mono text-[10px] uppercase tracking-wider text-muted-foreground">
                        evidence
                      </div>
                      <pre className="whitespace-pre-wrap font-mono text-[11px] leading-relaxed text-foreground">
                        {e.evidence}
                      </pre>
                      {e.url && (
                        <a href={e.url} className="mt-2 inline-block font-mono text-[11px] text-primary hover:underline">
                          {e.url}
                        </a>
                      )}
                    </div>
                  )}
                </li>
              ))}
            </ol>
          )}
        </div>
      ) : (
        <div className="px-6 py-6">
          <pre className="overflow-x-auto border border-border bg-card p-5 font-mono text-[12px] leading-relaxed text-muted-foreground">
            {backlogMd}
          </pre>
        </div>
      )}
    </div>
  );
}
