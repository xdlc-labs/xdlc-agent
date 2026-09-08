import { useState } from "react";
import { createFileRoute } from "@tanstack/react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { fetchOverview } from "@/lib/api";
import { clearToken, getToken, setToken } from "@/lib/auth";
import {
  clearAgentCreds,
  getAgentAPIKey,
  getAgentProvider,
  setAgentAPIKey,
  setAgentProvider,
  type AgentProvider,
} from "@/lib/agent-creds";
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
  const [tokenSaving, setTokenSaving] = useState(false);
  const [tokenError, setTokenError] = useState<string | null>(null);

  const [agentProvider, setAgentProviderState] = useState<AgentProvider | "">(
    () => getAgentProvider(),
  );
  // What is actually persisted in this browser right now — drives the
  // "these two differ, and that is fine" note, so an unsaved dropdown
  // change never claims to be in effect.
  const [activeProvider, setActiveProvider] = useState<AgentProvider | "">(
    () => getAgentProvider(),
  );
  const [agentKeyInput, setAgentKeyInput] = useState(() => getAgentAPIKey());
  const [agentSaved, setAgentSaved] = useState(false);

  const daemonProvider = daemon?.agentProvider ?? "";
  const providersDiffer = !!activeProvider && !!daemonProvider && activeProvider !== daemonProvider;

  const saveApiToken = async () => {
    const trimmed = tokenInput.trim();
    setTokenError(null);
    setTokenSaved(false);
    if (trimmed) setToken(trimmed);
    else clearToken();
    setTokenSaving(true);
    try {
      const res = await fetch("/api/whoami", {
        headers: trimmed ? { Authorization: `Bearer ${trimmed}` } : {},
      });
      if (!res.ok) {
        setTokenError(res.status === 401 ? "token rejected" : `whoami → ${res.status}`);
        return;
      }
      setTokenSaved(true);
      void queryClient.invalidateQueries({ queryKey: ["overview"] });
      void queryClient.invalidateQueries({ queryKey: ["history"] });
      void queryClient.invalidateQueries({ queryKey: ["backlog"] });
      void queryClient.invalidateQueries({ queryKey: ["role"] });
    } finally {
      setTokenSaving(false);
    }
  };

  const clearApiToken = () => {
    clearToken();
    setTokenInput("");
    setTokenSaved(false);
    setTokenError(null);
    void queryClient.invalidateQueries({ queryKey: ["overview"] });
    void queryClient.invalidateQueries({ queryKey: ["role"] });
  };

  const saveAgentCreds = () => {
    setAgentProvider(agentProvider);
    setAgentAPIKey(agentKeyInput);
    setActiveProvider(agentProvider);
    setAgentSaved(true);
  };

  const clearAgent = () => {
    clearAgentCreds();
    setAgentProviderState("");
    setActiveProvider("");
    setAgentKeyInput("");
    setAgentSaved(false);
  };

  return (
    <div>
      <PageHeader
        title="settings"
        sub="Bearer token for /api/*, plus the coding agent used for Manual Fix from this browser. The daemon's own default provider lives in config.yaml and is read-only here — the two are separate settings."
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
            Save calls <span className="text-foreground">GET /api/whoami</span> so a mismatch shows{" "}
            <span className="text-foreground">token rejected</span> instead of a green saved chip.
          </p>
          <div className="mt-3 flex flex-wrap items-center gap-2">
            <input
              type="password"
              autoComplete="off"
              value={tokenInput}
              onChange={(e) => {
                setTokenInput(e.target.value);
                setTokenSaved(false);
                setTokenError(null);
              }}
              placeholder="XDLC_API_TOKEN value"
              className="min-w-[16rem] flex-1 border border-border bg-surface px-3 py-1.5 font-mono text-[12px] text-foreground outline-none focus:border-primary"
            />
            <button
              type="button"
              onClick={() => void saveApiToken()}
              disabled={tokenSaving}
              className="border border-primary bg-primary px-3 py-1.5 font-mono text-[11px] text-primary-foreground hover:opacity-90"
            >
              {tokenSaving ? "…" : "save"}
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
            {tokenError ? (
              <span className="font-mono text-[11px] text-breach">{tokenError}</span>
            ) : null}
          </div>
        </section>

        <section className="lg:col-span-2" aria-labelledby="coding-agent-heading">
          <div className="mb-2 flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
            <h2
              id="coding-agent-heading"
              className="font-mono text-xs uppercase tracking-[0.18em] text-muted-foreground"
            >
              Coding agent — two separate settings
            </h2>
            <p className="font-mono text-[11px] text-muted-foreground">
              Saving one never changes the other.
            </p>
          </div>

          <div className="grid gap-4 lg:grid-cols-2">
            <div className="border border-border bg-card p-4">
              <div className="flex flex-wrap items-center gap-2">
                <h3 className="font-mono text-[13px] text-foreground">
                  1 · This browser — Manual Fix override
                </h3>
                <span className="border border-border bg-surface px-2 py-0.5 font-mono text-[10px] uppercase tracking-[0.14em] text-muted-foreground">
                  browser-local
                </span>
              </div>
              <p className="mt-1 font-mono text-[11px] text-muted-foreground">
                Applies <span className="text-foreground">only</span> to Manual Fix requests you
                send from this browser. Kept in this browser&apos;s localStorage
                (`xdlc_agent_provider` / `xdlc_agent_api_key`) and attached per request as{" "}
                <span className="text-foreground">X-XDLC-Agent-Provider</span> /{" "}
                <span className="text-foreground">X-XDLC-Agent-Key</span> — the daemon injects them
                into that one run&apos;s subprocess and never writes them to disk, audit, or
                backlog. It does not change the daemon&apos;s default provider, and it never applies
                to webhook-driven Fixes or to any other browser. Raise{" "}
                <span className="text-foreground">agent.timeout</span> (examples use 10m) before a
                real Cursor/Claude/Codex run.
              </p>
              <div className="mt-3 flex flex-wrap items-center gap-2">
                <label className="sr-only" htmlFor="agent-provider">
                  Coding agent for Manual Fix from this browser
                </label>
                <select
                  id="agent-provider"
                  value={agentProvider}
                  onChange={(e) => {
                    setAgentProviderState(e.target.value as AgentProvider | "");
                    setAgentSaved(false);
                  }}
                  className="border border-border bg-surface px-3 py-1.5 font-mono text-[12px] text-foreground outline-none focus:border-primary"
                >
                  <option value="">
                    no override — use daemon default ({daemonProvider || "—"})
                  </option>
                  <option value="cursor">cursor</option>
                  <option value="claude">claude</option>
                  <option value="codex">codex</option>
                  <option value="gemini">gemini</option>
                </select>
                <input
                  type="password"
                  autoComplete="off"
                  value={agentKeyInput}
                  onChange={(e) => {
                    setAgentKeyInput(e.target.value);
                    setAgentSaved(false);
                  }}
                  placeholder="ANTHROPIC_API_KEY / OPENAI_API_KEY / CURSOR_API_KEY / GEMINI_API_KEY"
                  className="min-w-[16rem] flex-1 border border-border bg-surface px-3 py-1.5 font-mono text-[12px] text-foreground outline-none focus:border-primary"
                />
                <button
                  type="button"
                  onClick={saveAgentCreds}
                  aria-label="Save coding agent for this browser"
                  className="border border-primary bg-primary px-3 py-1.5 font-mono text-[11px] text-primary-foreground hover:opacity-90"
                >
                  save
                </button>
                <button
                  type="button"
                  onClick={clearAgent}
                  aria-label="Clear coding agent for this browser"
                  className="border border-border px-3 py-1.5 font-mono text-[11px] text-muted-foreground hover:text-foreground"
                >
                  clear
                </button>
                {agentSaved && (
                  <span className="font-mono text-[11px] text-pass">saved in this browser</span>
                )}
              </div>
            </div>

            <div className="border border-border bg-surface/40 p-4">
              <div className="flex flex-wrap items-center gap-2">
                <h3 className="font-mono text-[13px] text-foreground">
                  2 · Daemon default provider
                </h3>
                <span className="border border-border bg-surface px-2 py-0.5 font-mono text-[10px] uppercase tracking-[0.14em] text-muted-foreground">
                  read-only here
                </span>
              </div>
              <p className="mt-1 font-mono text-[11px] text-muted-foreground">
                The fallback the daemon uses whenever a request carries no browser override — every
                webhook-driven Fix, and any Manual Fix sent without one. It is not something this
                console can save: it comes from{" "}
                <span className="text-foreground">agent.provider</span> in the daemon&apos;s
                config.yaml on the host (API keys from the host env). To change it, edit that file
                and restart the daemon.
              </p>
              <div className="mt-3 flex flex-wrap items-center gap-2">
                <span className="inline-block border border-border bg-surface px-3 py-1 font-mono text-[11px] text-foreground">
                  {daemonProvider || (isPending ? "loading…" : "unavailable")}
                </span>
                {!daemonProvider && !isPending ? (
                  <span className="font-mono text-[11px] text-muted-foreground">
                    daemon not reachable — this is the daemon&apos;s value, not a failed save
                  </span>
                ) : null}
              </div>
              <p className="mt-3 font-mono text-[11px] text-muted-foreground">
                config file: <span className="text-foreground">{daemon?.configPath ?? "—"}</span>
              </p>
            </div>
          </div>

          {providersDiffer ? (
            <div className="mt-3 border border-primary/40 bg-primary/5 px-4 py-3" role="note">
              <p className="font-mono text-[11px] uppercase tracking-[0.14em] text-primary">
                Expected — not a failed save
              </p>
              <p className="mt-1 font-mono text-[11px] text-muted-foreground">
                These are two independent settings. A Manual Fix from this browser runs{" "}
                <span className="text-foreground">{activeProvider}</span>; everything else —
                webhook-driven Fixes and any client without an override — runs the daemon default{" "}
                <span className="text-foreground">{daemonProvider}</span>. To change the daemon
                default, set <span className="text-foreground">agent.provider</span> in
                config.yaml and restart the daemon.
              </p>
            </div>
          ) : null}
        </section>

        {!isPending && !isError ? (
          <section className="border border-border bg-card p-4 lg:col-span-2">
            <h2 className="font-mono text-[13px] text-foreground">Gates (last status)</h2>
            <div className="mt-3 space-y-2">
              {gates.length === 0 ? (
                <p className="font-mono text-[11px] text-muted-foreground">No gates configured.</p>
              ) : (
                gates.map((g) => (
                  <div
                    key={g.name}
                    className="flex items-center justify-between border border-border bg-surface px-3 py-2"
                  >
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
