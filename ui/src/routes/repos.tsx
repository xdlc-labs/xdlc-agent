import { Link, createFileRoute, useNavigate } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { fetchOverview } from "@/lib/api";
import { PageHeader, StatusTag, ActionTag } from "@/components/status";
import { EmptyState, QueryError, Skeleton } from "@/components/query-state";

export const Route = createFileRoute("/repos")({
  head: () => ({
    meta: [{ title: "Repos — watched services | xdlc-agent" }],
  }),
  component: Repos,
});

const healthTone = {
  healthy: "text-pass",
  degraded: "text-acting",
  breach: "text-breach",
} as const;

function Repos() {
  const navigate = useNavigate();
  const { data, isPending, isError, error, refetch } = useQuery({
    queryKey: ["overview"],
    queryFn: fetchOverview,
    refetchInterval: 30_000,
  });
  const repos = data?.repos ?? [];

  return (
    <div>
      <PageHeader title="repos" sub="Services the daemon watches. Open a row for timeline drill-down." />

      {isPending ? <Skeleton rows={5} /> : null}
      {isError ? (
        <QueryError
          message={error instanceof Error ? error.message : "Failed to load repos"}
          onRetry={() => void refetch()}
        />
      ) : null}
      {!isPending && !isError && repos.length === 0 ? (
        <EmptyState>No repos in config — check config.yaml.</EmptyState>
      ) : null}
      {!isPending && !isError && repos.length > 0 ? (
        <div className="overflow-x-auto px-6 py-6">
          <table className="w-full min-w-[900px] border border-border bg-card text-left">
            <thead>
              <tr className="border-b border-border font-mono text-[10px] uppercase tracking-[0.14em] text-muted-foreground">
                <th className="px-4 py-2.5 font-normal">repo</th>
                <th className="px-4 py-2.5 font-normal">branch</th>
                <th className="px-4 py-2.5 font-normal">last gate</th>
                <th className="px-4 py-2.5 font-normal">last action</th>
                <th className="px-4 py-2.5 font-normal">dev tag</th>
                <th className="px-4 py-2.5 font-normal">prod tag</th>
                <th className="px-4 py-2.5 font-normal">health</th>
              </tr>
            </thead>
            <tbody className="font-mono text-[12px]">
              {repos.map((r) => (
                <tr
                  key={r.id}
                  tabIndex={0}
                  role="link"
                  aria-label={`Open timeline for ${r.name}`}
                  onClick={() => void navigate({ to: "/repos/$id", params: { id: r.id } })}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" || e.key === " ") {
                      e.preventDefault();
                      void navigate({ to: "/repos/$id", params: { id: r.id } });
                    }
                  }}
                  className="cursor-pointer border-b border-border last:border-0 hover:bg-surface focus-visible:bg-surface focus-visible:outline focus-visible:outline-2 focus-visible:outline-ring"
                >
                  <td className="px-4 py-3 text-foreground">
                    <Link
                      to="/repos/$id"
                      params={{ id: r.id }}
                      className="text-primary hover:underline"
                      onClick={(e) => e.stopPropagation()}
                    >
                      {r.name}
                    </Link>
                  </td>
                  <td className="px-4 py-3 text-muted-foreground">{r.branch}</td>
                  <td className="px-4 py-3">
                    <span className="mr-2 text-muted-foreground">{r.lastGate}</span>
                    <StatusTag status={r.lastGateStatus} />
                  </td>
                  <td className="px-4 py-3">
                    <ActionTag action={r.lastAction} />
                    <div className="mt-1 text-[10px] text-muted-foreground">
                      {(r.lastActionAt ?? "").slice(11) || "—"}
                    </div>
                  </td>
                  <td className="px-4 py-3 text-muted-foreground">{r.devTag}</td>
                  <td className="px-4 py-3 text-muted-foreground">{r.prodTag}</td>
                  <td className={`px-4 py-3 ${healthTone[r.health]}`}>{r.health}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}
    </div>
  );
}
