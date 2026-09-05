import { useState } from "react";
import { createFileRoute } from "@tanstack/react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { fetchOverview } from "@/lib/api";
import { clearToken, getToken, setToken } from "@/lib/auth";
import { PageHeader } from "@/components/status";
import { QueryError, Skeleton } from "@/components/query-state";

export const Route = createFileRoute("/settings")({
  head: () => ({
    meta: [{ title: "Settings — agent provider and gates | xdlc-agent" }],
  }),
  component: Settings,
});

function Settings() {
  const queryClient = useQueryClient();
  const { data, isPending, isError, error, refetch } = useQuery({
    queryKey: ["overview"],
    queryFn: fetchOverview,
    refetchInterval: 10_000,
  });
  const daemon = data?.daemon;
  const gates = data?.gates ?? [];

  const [tokenInput, setTokenInput] = useState(() => getToken());
  const [tokenSaved, setTokenSaved] = useState(false);

  const saveApiToken = () => {
    const trimmed = tokenInput.trim();
    if (trimmed) setToken(trimmed);
    else clearToken();
    setTokenSaved(true);
    void queryClient.invalidateQueries({ queryKey: ["overview"] });
    void queryClient.invalidateQueries({ queryKey: ["history"] });
    void queryClient.invalidateQueries({ queryKey: ["backlog"] });
  };

  const clearApiToken = () => {
    clearToken();
    setTokenInput("");
    setTokenSaved(false);
    void queryClient.invalidateQueries({ queryKey: ["overview"] });
  };

  return (
    <div>
      <PageHeader
        title="settings"
        sub="Bearer token for /api/* plus read-only view of the running daemon. Config edits still go through config.yaml."
      />

      {isPending ? <Skeleton rows={4} /> : null}
      {isError ? (
        <QueryError
          message={error instanceof Error ? error.message : "Failed to load settings"}
          onRetry={() => void refetch()}
        />
      ) : null}

      <div className="grid gap-4 px-6 py-6 lg:grid-cols-2">
        <section className="border border-border bg-card p-4 lg:col-span-2">
          <h2 className="font-mono text-[13px] text-foreground">API token</h2>
          <p className="mt-1 font-mono text-[11px] text-muted-foreground">
            Stored in localStorage (`xdlc_api_token`). Optional default: `VITE_API_TOKEN`. Sent as{" "}
            <span className="text-foreground">Authorization: Bearer …</span>
          </p>
          <div className="mt-3 flex flex-wrap items-center gap-2">
            <input
              type="password"
              autoComplete="off"
              value={tokenInput}
              onChange={(e) => {
                setTokenInput(e.target.value);
                setTokenSaved(false);
              }}
              placeholder="XDL_API_TOKEN value"
              className="min-w-[16rem] flex-1 border border-border bg-surface px-3 py-1.5 font-mono text-[12px] text-foreground outline-none focus:border-primary"
            />
            <button
              type="button"
              onClick={saveApiToken}
              className="border border-primary bg-primary px-3 py-1.5 font-mono text-[11px] text-primary-foreground hover:opacity-90"
            >
              save
            </button>
            <button
              type="button"
              onClick={clearApiToken}
              className="border border-border px-3 py-1.5 font-mono text-[11px] text-muted-foreground hover:text-foreground"
            >
              clear
            </button>
            {tokenSaved && (
              <span className="font-mono text-[11px] text-pass">saved</span>
            )}
          </div>
        </section>

        {!isPending && !isError ? (
        <section className="border border-border bg-card p-4">
          <h2 className="font-mono text-[13px] text-foreground">Agent provider</h2>
          <p className="mt-1 font-mono text-[11px] text-muted-foreground">From config agent.provider</p>
          <div className="mt-3 border border-primary px-3 py-1 font-mono text-[11px] text-primary inline-block">
            {daemon?.agentProvider ?? "—"}
          </div>
        </section>
        ) : null}

        {!isPending && !isError ? (
        <section className="border border-border bg-card p-4">
          <h2 className="font-mono text-[13px] text-foreground">Gates (last status)</h2>
          <div className="mt-3 space-y-2">
            {gates.length === 0 ? (
              <p className="font-mono text-[11px] text-muted-foreground">No gates configured.</p>
            ) : (
              gates.map((g) => (
                <div key={g.name} className="flex items-center justify-between border border-border bg-surface px-3 py-2">
                  <span className="font-mono text-[12px]">{g.name}</span>
                  <span className="font-mono text-[11px] text-muted-foreground">{g.status}</span>
                </div>
              ))
            )}
          </div>
        </section>
        ) : null}

        <section className="border border-border bg-card p-4 lg:col-span-2">
          <h2 className="font-mono text-[13px] text-foreground">Daemon</h2>
          <dl className="mt-3 grid gap-x-8 gap-y-2 font-mono text-[11px] sm:grid-cols-2">
            {[
              ["config path", daemon?.configPath ?? "—"],
              ["gitops dir", daemon?.gitopsDir ?? "—"],
              ["webhook", daemon?.webhook ?? "—"],
              ["env", daemon?.env ?? "—"],
              ["status", daemon?.status ?? "stopped"],
              ["uptime", daemon?.uptime ?? "—"],
              ["version", daemon?.version ?? "—"],
            ].map(([k, v]) => (
              <div key={k} className="flex justify-between gap-4 border-b border-border py-1.5">
                <dt className="text-muted-foreground">{k}</dt>
                <dd className="truncate text-right text-foreground">{v}</dd>
              </div>
            ))}
          </dl>
        </section>
      </div>
    </div>
  );
}
