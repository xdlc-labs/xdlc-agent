import { useCallback, useState } from "react";
import { createFileRoute } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { fetchOverview, type Repo } from "@/lib/api";
import { PageHeader, StatusTag, ActionTag } from "@/components/status";
import { Dialog } from "@/components/dialog";

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
  const { data } = useQuery({ queryKey: ["overview"], queryFn: fetchOverview, refetchInterval: 10_000 });
  const repos = data?.repos ?? [];
  const [open, setOpen] = useState<Repo | null>(null);
  const close = useCallback(() => setOpen(null), []);

  return (
    <div>
      <PageHeader title="repos" sub="Services the daemon watches. Select a row for clone state and SLO queries." />

      <div className="overflow-x-auto px-6 py-6">
        {repos.length === 0 ? (
          <p className="font-mono text-[12px] text-muted-foreground">No repos in config — check config.yaml.</p>
        ) : (
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
                  role="button"
                  aria-label={`Open details for ${r.name}`}
                  onClick={() => setOpen(r)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" || e.key === " ") {
                      e.preventDefault();
                      setOpen(r);
                    }
                  }}
                  className="cursor-pointer border-b border-border last:border-0 hover:bg-surface focus-visible:bg-surface focus-visible:outline focus-visible:outline-2 focus-visible:outline-ring"
                >
                  <td className="px-4 py-3 text-foreground">{r.name}</td>
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
        )}
      </div>

      <Dialog open={open !== null} onClose={close} title={open?.name ?? ""} variant="drawer">
        <dl className="space-y-3 px-5 py-4 font-mono text-[11px]">
          {[
            ["clone status", open?.cloneStatus],
            ["argocd apps", open?.argocdApp],
            ["last promote", open?.lastPromote],
            ["last revert", open?.lastRevert],
            ["dev / prod tag", open ? `${open.devTag} / ${open.prodTag}` : undefined],
          ].map(([k, v]) => (
            <div key={k}>
              <dt className="text-[10px] uppercase tracking-wider text-muted-foreground">{k}</dt>
              <dd className="mt-0.5 text-foreground">{v}</dd>
            </div>
          ))}
        </dl>
        <div className="border-t border-border px-5 py-4">
          <div className="mb-2 font-mono text-[10px] uppercase tracking-wider text-muted-foreground">
            SLO queries
          </div>
          {open?.sloQueries?.map((q) => (
            <div key={q.label} className="mb-3">
              <div className="font-mono text-[11px] text-primary">{q.label}</div>
              <pre className="mt-1 whitespace-pre-wrap break-all border border-border bg-surface p-2 font-mono text-[11px] text-muted-foreground">
                {q.query}
              </pre>
            </div>
          ))}
        </div>
      </Dialog>
    </div>
  );
}
