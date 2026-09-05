import { createFileRoute } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { fetchOverview } from "@/lib/api";
import { PageHeader, StatusTag } from "@/components/status";
import { EmptyState, QueryError, Skeleton } from "@/components/query-state";

export const Route = createFileRoute("/gates")({
  head: () => ({
    meta: [{ title: "Gates — CI, DEV smoke, PROD health | xdlc-agent" }],
  }),
  component: Gates,
});

function Gates() {
  const { data, isPending, isError, error, refetch } = useQuery({
    queryKey: ["overview"],
    queryFn: fetchOverview,
    refetchInterval: 10_000,
  });
  const gates = data?.gates ?? [];

  return (
    <div>
      <PageHeader
        title="gates"
        sub="Three pluggable gates feed the loop. Each reports a signal; the daemon maps it to Fix, Promote or Revert."
      />
      {isPending ? <Skeleton rows={4} /> : null}
      {isError ? (
        <QueryError
          message={error instanceof Error ? error.message : "Failed to load gates"}
          onRetry={() => void refetch()}
        />
      ) : null}
      {!isPending && !isError && gates.length === 0 ? (
        <EmptyState>No gate data — start `xdlc daemon` (proxied via /api).</EmptyState>
      ) : null}
      {!isPending && !isError && gates.length > 0 ? (
      <div className="grid gap-4 px-6 py-6 lg:grid-cols-3">
        {gates.map((g) => (
          <article key={g.name} className="mc-panel flex flex-col overflow-hidden rounded-md">
            <div className="flex items-center justify-between border-b border-border px-4 py-3">
              <div>
                <h2 className="font-mono text-[13px] text-foreground">{g.name}</h2>
                <p className="font-mono text-[11px] text-muted-foreground">{g.provider}</p>
              </div>
              <StatusTag status={g.status} />
            </div>
            <dl className="space-y-2 px-4 py-3 font-mono text-[11px]">
              <div className="flex justify-between gap-2">
                <dt className="text-muted-foreground">last check</dt>
                <dd>{g.lastCheck}</dd>
              </div>
              <div className="flex justify-between gap-2">
                <dt className="text-muted-foreground">interval</dt>
                <dd className="text-right">{g.interval}</dd>
              </div>
              <div className="flex justify-between gap-2">
                <dt className="text-muted-foreground">trigger</dt>
                <dd className="text-right">{g.trigger}</dd>
              </div>
            </dl>
            <div className="mx-4 mb-3 border border-border bg-surface p-3">
              <div className="mb-1 font-mono text-[10px] uppercase tracking-wider text-muted-foreground">
                last evidence
              </div>
              <pre className="whitespace-pre-wrap font-mono text-[11px] leading-relaxed text-foreground">
                {g.evidence}
              </pre>
            </div>
            {g.url ? (
              <a
                href={g.url}
                className="mt-auto block truncate border-t border-border px-4 py-2.5 font-mono text-[11px] text-primary hover:bg-surface"
              >
                {g.url}
              </a>
            ) : null}
          </article>
        ))}
      </div>
      ) : null}
    </div>
  );
}
