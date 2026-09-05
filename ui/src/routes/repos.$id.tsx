import { createFileRoute } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { fetchRepo } from "@/lib/api";
import { PageHeader, ActionTag } from "@/components/status";
import { QueryError, Skeleton, EmptyState } from "@/components/query-state";

export const Route = createFileRoute("/repos/$id")({
  head: ({ params }) => ({
    meta: [{ title: `${params.id} — repo | xdlc-agent` }],
  }),
  component: RepoDetail,
});

function RepoDetail() {
  const { id } = Route.useParams();
  const { data, isPending, isError, error, refetch } = useQuery({
    queryKey: ["repo", id],
    queryFn: () => fetchRepo(id),
    refetchInterval: 30_000,
  });
  const repo = data?.repo;
  const timeline = data?.timeline ?? [];

  return (
    <div>
      <PageHeader title={repo?.name ?? id} sub="Timeline of signals → actions (ChainID). Back via repos nav." />

      {isPending ? <Skeleton rows={6} /> : null}
      {isError ? (
        <QueryError
          message={error instanceof Error ? error.message : "Failed to load repo"}
          onRetry={() => void refetch()}
        />
      ) : null}

      {repo ? (
        <div className="grid gap-6 px-6 pb-10 lg:grid-cols-[1fr_1.4fr]">
          <dl className="space-y-3 border border-border bg-card p-4 font-mono text-[11px]">
            {[
              ["branch", repo.branch],
              ["health", repo.health],
              ["last gate", `${repo.lastGate} (${repo.lastGateStatus})`],
              ["last action", `${repo.lastAction} @ ${repo.lastActionAt}`],
              ["dev tag", repo.devTag],
              ["prod tag", repo.prodTag],
              ["last promote", repo.lastPromote],
              ["last revert", repo.lastRevert],
              ["argocd", repo.argocdApp],
              ["clone", repo.cloneStatus],
            ].map(([k, v]) => (
              <div key={k}>
                <dt className="text-[10px] uppercase tracking-wider text-muted-foreground">{k}</dt>
                <dd className="mt-0.5 text-foreground">{v}</dd>
              </div>
            ))}
          </dl>

          <div>
            <h2 className="mb-2 font-mono text-xs uppercase tracking-[0.18em] text-muted-foreground">
              timeline
            </h2>
            {timeline.length === 0 ? (
              <EmptyState>No audit history for this repo yet.</EmptyState>
            ) : (
              <ol className="border border-border bg-card font-mono text-[11px]">
                {timeline.map((e) => (
                  <li key={e.id} className="border-b border-border px-4 py-3 last:border-0">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="text-muted-foreground">{e.ts}</span>
                      <ActionTag action={e.action} />
                      <span className="text-muted-foreground">{e.gate}</span>
                      <span className="text-foreground">{e.signal}</span>
                      <span className={e.ok ? "text-pass" : "text-breach"}>{e.ok ? "ok" : "err"}</span>
                    </div>
                    {e.chain_id ? (
                      <div className="mt-1 text-[10px] text-muted-foreground">chain {e.chain_id}</div>
                    ) : null}
                    {e.evidence ? (
                      <div className="mt-1 truncate text-muted-foreground" title={e.evidence}>
                        {e.evidence}
                      </div>
                    ) : null}
                  </li>
                ))}
              </ol>
            )}
          </div>
        </div>
      ) : null}
    </div>
  );
}
