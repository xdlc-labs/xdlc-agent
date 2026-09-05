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
  const [agentKeyInput, setAgentKeyInput] = useState(() => getAgentAPIKey());
  const [agentSaved, setAgentSaved] = useState(false);

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
    setAgentSaved(true);
  };

  const clearAgent = () => {
    clearAgentCreds();
    setAgentProviderState("");
    setAgentKeyInput("");
    setAgentSaved(false);
  };

  return (
    <div>
      <PageHeader
        title="settings"
        sub="Bearer token for /api/* plus optional browser-local coding-agent key for Manual Fix. Config.yaml still owns the daemon default provider."
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

        <section className="border border-border bg-card p-4 lg:col-span-2">
          <h2 className="font-mono text-[13px] text-foreground">Coding agent (browser-local)</h2>
          <p className="mt-1 font-mono text-[11px] text-muted-foreground">
            localStorage only (`xdlc_agent_provider` / `xdlc_agent_api_key`). On Manual Fix, sent as{" "}
            <span className="text-foreground">X-XDLC-Agent-Provider</span> /{" "}
            <span className="text-foreground">X-XDLC-Agent-Key</span> — daemon injects into the
            subprocess for that run, never writes the key to audit/backlog/disk. Leave empty to use
            the daemon host env + config provider. Webhook-driven Fixes always use config.yaml; this
            override is Manual Fix only. Raise <span className="text-foreground">agent.timeout</span>{" "}
            (examples use 10m) before a real Cursor/Claude/Codex run.
          </p>
          <div className="mt-3 flex flex-wrap items-center gap-2">
            <label className="sr-only" htmlFor="agent-provider">
              Agent provider
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
              <option value="">daemon default ({daemon?.agentProvider ?? "—"})</option>
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
              className="border border-primary bg-primary px-3 py-1.5 font-mono text-[11px] text-primary-foreground hover:opacity-90"
            >
              save
            </button>
            <button
              type="button"
              onClick={clearAgent}
              className="border border-border px-3 py-1.5 font-mono text-[11px] text-muted-foreground hover:text-foreground"
            >
              clear
            </button>
            {agentSaved && (
              <span className="font-mono text-[11px] text-pass">saved locally</span>
            )}
          </div>
        </section>

        {!isPending && !isError ? (
          <section className="border border-border bg-card p-4">
            <h2 className="font-mono text-[13px] text-foreground">Daemon provider</h2>
            <p className="mt-1 font-mono text-[11px] text-muted-foreground">
              From config agent.provider (host env keys)
            </p>
            <div className="mt-3 inline-block border border-primary px-3 py-1 font-mono text-[11px] text-primary">
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
