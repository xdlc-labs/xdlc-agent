import { useCallback, useState } from "react";
import { createFileRoute } from "@tanstack/react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { fetchFixPRs, fetchOverview, policy, postAction, type ManualAction } from "@/lib/api";
import { fetchRole } from "@/lib/auth";
import { PageHeader, ActionTag } from "@/components/status";
import { Dialog } from "@/components/dialog";
import { QueryError, Skeleton } from "@/components/query-state";
import { t } from "@/lib/i18n";

export const Route = createFileRoute("/actions")({
  head: () => ({
    meta: [{ title: "Actions — manual intervene and policy | xdlc-agent" }],
  }),
  component: Actions,
});

type Pending = {
  action: ManualAction;
  title: string;
  body: string;
} | null;

function Actions() {
  const queryClient = useQueryClient();
  const { data, isPending, isError, error, refetch } = useQuery({
    queryKey: ["overview"],
    queryFn: fetchOverview,
    refetchInterval: 10_000,
  });
  const { data: role } = useQuery({ queryKey: ["role"], queryFn: fetchRole, refetchInterval: 60_000 });
  const canOperate = role === "operator";
  const { data: fixPRs } = useQuery({ queryKey: ["fix-prs"], queryFn: fetchFixPRs, refetchInterval: 30_000 });
  const repos = data?.repos ?? [];
  const [repoPick, setRepoPick] = useState("");
  const [pending, setPending] = useState<Pending>(null);
  const [busy, setBusy] = useState(false);
  const [log, setLog] = useState<string[]>([]);

  const repo =
    repoPick && repos.some((r) => r.name === repoPick) ? repoPick : (repos[0]?.name ?? "");

  const close = useCallback(() => {
    if (!busy) setPending(null);
  }, [busy]);

  const pushLog = (line: string) => {
    setLog((l) => [`${new Date().toISOString().slice(0, 19)}Z — ${line}`, ...l]);
  };

  const confirm = async () => {
    if (!pending || !repo) return;
    setBusy(true);
    try {
      const res = await postAction(pending.action, repo);
      pushLog(
        res.ok
          ? `${pending.action} ${repo}: ok — ${res.message}`
          : `${pending.action} ${repo}: error (${res.status}) — ${res.message}`,
      );
      if (res.ok) {
        void queryClient.invalidateQueries({ queryKey: ["overview"] });
        void queryClient.invalidateQueries({ queryKey: ["history"] });
      }
      setPending(null);
    } catch (e) {
      pushLog(`${pending.action} ${repo}: error — ${e instanceof Error ? e.message : String(e)}`);
      setPending(null);
    } finally {
      setBusy(false);
    }
  };

  const controls: {
    action: ManualAction;
    title: string;
    desc: string;
    body: string;
    tone: string;
  }[] = [
    {
      action: "fix",
      title: "Manual Fix",
      desc: "Dispatch coding-agent Fix for the selected repo (same path as CI/smoke fail).",
      body: `POST /api/actions/fix with confirm for "${repo || "…"}". Runs the Fix subagent against the configured branch.`,
      tone: "text-acting border-acting/40",
    },
    {
      action: "promote",
      title: "Manual Promote",
      desc: "Fast-forward develop→main for the selected repo (refused if non-FF).",
      body: `POST /api/actions/promote with confirm for "${repo || "…"}". Same as CLI promote — develop must be FF of main.`,
      tone: "text-pass border-pass/40",
    },
    {
      action: "revert",
      title: "Manual Revert",
      desc: "Git revert on main for the selected repo (rollback-first).",
      body: `POST /api/actions/revert with confirm for "${repo || "…"}". Same as prod-health breach Revert.`,
      tone: "text-breach border-breach/40",
    },
  ];

  if (isPending) {
    return (
      <div>
        <PageHeader
          title="actions"
          sub="Policy from Decide(). Manual Fix / Promote / Revert hit the daemon write API with bearer or SSO session auth."
        />
        <Skeleton rows={4} />
      </div>
    );
  }
  if (isError) {
    return (
      <div>
        <PageHeader
          title="actions"
          sub="Policy from Decide(). Manual Fix / Promote / Revert hit the daemon write API with bearer or SSO session auth."
        />
        <QueryError
          message={error instanceof Error ? error.message : "Failed to load actions"}
          onRetry={() => void refetch()}
        />
      </div>
    );
  }

  return (
    <div>
      <PageHeader
        title="actions"
        sub="Policy from Decide(). Manual Fix / Promote / Revert hit the daemon write API with bearer or SSO session auth."
      />

      {!canOperate && (
        <p className="border-b border-border bg-surface px-6 py-2 font-mono text-[11px] text-muted-foreground">
          {role === "viewer"
            ? "Signed in as viewer — manual actions need the operator role."
            : "Not signed in as an operator — manual actions are disabled. Set an API token or sign in via SSO (see header)."}
        </p>
      )}

      <div className="flex flex-wrap items-center gap-3 border-b border-border px-6 py-3">
        <label className="font-mono text-[11px] uppercase tracking-wider text-muted-foreground" htmlFor="action-repo">
          repo
        </label>
        <select
          id="action-repo"
          value={repo}
          onChange={(e) => setRepoPick(e.target.value)}
          disabled={repos.length === 0}
          aria-label="Select repository for manual action"
          className="border border-border bg-surface px-3 py-1.5 font-mono text-[12px] text-foreground outline-none focus:border-primary focus-visible:outline focus-visible:outline-2 focus-visible:outline-ring disabled:opacity-50"
        >
          {repos.length === 0 ? (
            <option value="">no repos (daemon down?)</option>
          ) : (
            repos.map((r) => (
              <option key={r.id || r.name} value={r.name}>
                {r.name}
              </option>
            ))
          )}
        </select>
      </div>

      <div className="grid gap-4 px-6 py-6 lg:grid-cols-3">
        {controls.map((c) => (
          <div key={c.action} className="flex items-start justify-between gap-4 border border-border bg-card p-4">
            <div>
              <h2 className="font-mono text-[13px] text-foreground">{c.title}</h2>
              <p className="mt-1 font-mono text-[11px] text-muted-foreground">{c.desc}</p>
            </div>
            <button
              type="button"
              disabled={!repo || busy || !canOperate}
              aria-label={`Run ${c.title}`}
              onClick={() => setPending({ action: c.action, title: c.title, body: c.body })}
              className={`shrink-0 border px-3 py-1 font-mono text-[11px] hover:bg-surface focus-visible:outline focus-visible:outline-2 focus-visible:outline-ring disabled:opacity-40 ${c.tone}`}
            >
              run
            </button>
          </div>
        ))}
      </div>

      <section className="px-6 pb-6">
        <h2 className="mb-2 font-mono text-xs uppercase tracking-[0.18em] text-muted-foreground">
          {t("prs.title")}
        </h2>
        {fixPRs && fixPRs.length > 0 ? (
          <>
            <table className="w-full border border-border bg-card text-left font-mono text-[12px]">
              <thead>
                <tr className="border-b border-border text-[10px] uppercase tracking-[0.14em] text-muted-foreground">
                  <th className="px-4 py-2.5 font-normal">repo</th>
                  <th className="px-4 py-2.5 font-normal">branch</th>
                  <th className="px-4 py-2.5 font-normal">pr</th>
                  <th className="px-4 py-2.5 font-normal">state</th>
                  <th className="px-4 py-2.5 font-normal">at</th>
                </tr>
              </thead>
              <tbody>
                {fixPRs.map((pr) => (
                  <tr key={`${pr.repo}-${pr.branch}`} className="border-b border-border last:border-0">
                    <td className="px-4 py-2.5 text-foreground">{pr.repo}</td>
                    <td className="px-4 py-2.5 text-muted-foreground">{pr.branch}</td>
                    <td className="px-4 py-2.5">
                      <a href={pr.url} target="_blank" rel="noreferrer" className="text-primary hover:underline">
                        #{pr.number}
                      </a>
                    </td>
                    <td className="px-4 py-2.5 text-muted-foreground">
                      {pr.state}
                      {pr.stale ? " (stale)" : ""}
                    </td>
                    <td className="px-4 py-2.5 text-muted-foreground">{pr.at}</td>
                  </tr>
                ))}
              </tbody>
            </table>
            <p className="mt-1 font-mono text-[10px] text-muted-foreground">
              {fixPRs.some((p) => p.stale) ? t("prs.stale") : t("prs.live")}
            </p>
          </>
        ) : (
          <p className="border border-border bg-card p-4 font-mono text-[11px] text-muted-foreground">
            {t("prs.empty")}
          </p>
        )}
      </section>

      <section className="px-6 pb-6">
        <h2 className="mb-2 font-mono text-xs uppercase tracking-[0.18em] text-muted-foreground">policy</h2>
        <table className="w-full border border-border bg-card text-left font-mono text-[12px]">
          <thead>
            <tr className="border-b border-border text-[10px] uppercase tracking-[0.14em] text-muted-foreground">
              <th className="px-4 py-2.5 font-normal">signal</th>
              <th className="px-4 py-2.5 font-normal">source</th>
              <th className="px-4 py-2.5 font-normal">action</th>
              <th className="px-4 py-2.5 font-normal">note</th>
            </tr>
          </thead>
          <tbody>
            {policy.map((p) => (
              <tr key={p.signal} className="border-b border-border last:border-0">
                <td className="px-4 py-2.5 text-foreground">{p.signal}</td>
                <td className="px-4 py-2.5 text-muted-foreground">{p.source}</td>
                <td className="px-4 py-2.5">
                  <ActionTag action={p.action} />
                </td>
                <td className="px-4 py-2.5 text-muted-foreground">{p.note}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>

      <section className="px-6 pb-10">
        <h2 className="mb-2 font-mono text-xs uppercase tracking-[0.18em] text-muted-foreground">
          intervention log
        </h2>
        <div className="border border-border bg-card p-4 font-mono text-[11px] text-muted-foreground">
          {log.length === 0 ? "No manual interventions this session." : null}
          {log.map((l) => (
            <div key={l}>{l}</div>
          ))}
        </div>
      </section>

      <Dialog
        open={pending !== null}
        onClose={close}
        title={pending?.title ?? ""}
        footer={
          <>
            <button
              type="button"
              disabled={busy}
              onClick={close}
              aria-label={t("dialog.close")}
              className="border border-border px-3 py-1 font-mono text-[11px] text-muted-foreground hover:text-foreground disabled:opacity-40"
            >
              cancel
            </button>
            <button
              type="button"
              disabled={busy || !repo}
              onClick={() => void confirm()}
              className="border border-primary bg-primary px-3 py-1 font-mono text-[11px] text-primary-foreground disabled:opacity-40"
            >
              {busy ? "running…" : "confirm"}
            </button>
          </>
        }
      >
        <p className="font-mono text-[11px] leading-relaxed text-muted-foreground">{pending?.body}</p>
      </Dialog>
    </div>
  );
}
