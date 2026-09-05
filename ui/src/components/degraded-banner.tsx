import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { degradeWebhook, fetchOverview, isDegraded, lastFetchStatus } from "@/lib/api";
import { getToken, setToken, clearToken } from "@/lib/auth";

export function DegradedBanner() {
  const queryClient = useQueryClient();
  const { data, isError, error, refetch, isPending } = useQuery({
    queryKey: ["overview"],
    queryFn: fetchOverview,
    refetchInterval: 10_000,
    retry: 1,
  });

  const [tokenInput, setTokenInput] = useState(() => getToken());
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);

  const fetchFailed = isError && !isPending;
  const softDegraded = !!data && isDegraded(data);
  if (!fetchFailed && !softDegraded) return null;

  const webhook = data?.daemon.webhook ?? degradeWebhook(lastFetchStatus);
  const errMsg = error instanceof Error ? error.message : "";
  const needsToken =
    lastFetchStatus === 401 ||
    webhook.includes("401") ||
    webhook.includes("unauthorized") ||
    errMsg.includes("401");
  const is503 =
    lastFetchStatus === 503 || webhook.includes("503") || errMsg.includes("503");

  const refresh = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["overview"] }),
      queryClient.invalidateQueries({ queryKey: ["history"] }),
      queryClient.invalidateQueries({ queryKey: ["backlog"] }),
      queryClient.invalidateQueries({ queryKey: ["repos"] }),
      queryClient.invalidateQueries({ queryKey: ["prs"] }),
      queryClient.invalidateQueries({ queryKey: ["fix-prs"] }),
      queryClient.invalidateQueries({ queryKey: ["kpis"] }),
      queryClient.invalidateQueries({ queryKey: ["role"] }),
    ]);
    await refetch();
  };

  const saveToken = async () => {
    const trimmed = tokenInput.trim();
    setSaveError(null);
    setSaving(true);
    try {
      if (trimmed) setToken(trimmed);
      else clearToken();
      const res = await fetch("/api/whoami", {
        headers: trimmed ? { Authorization: `Bearer ${trimmed}` } : {},
      });
      if (!res.ok) {
        setSaveError(res.status === 401 ? "token rejected" : `whoami → ${res.status}`);
        return;
      }
      await refresh();
    } finally {
      setSaving(false);
    }
  };

  let title = "Daemon unreachable";
  let body = fetchFailed
    ? errMsg || "API request failed — check daemon and network."
    : webhook;
  if (needsToken) {
    title = "API token required";
    body = "Set the operator bearer token to talk to /api/*.";
  } else if (is503) {
    title = "API token not configured on daemon";
    body = "Export XDL_API_TOKEN (or your api_token_env) and restart the daemon.";
  }

  return (
    <div className="border-b border-breach/40 bg-breach/10 px-6 py-3" role="status">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <p className="font-mono text-[11px] uppercase tracking-[0.14em] text-breach">{title}</p>
          <p className="mt-1 font-mono text-[12px] text-foreground">{body}</p>
          {fetchFailed && !needsToken ? (
            <button
              type="button"
              onClick={() => void refresh()}
              className="mt-2 font-mono text-[11px] uppercase tracking-[0.12em] underline-offset-2 hover:underline"
            >
              retry
            </button>
          ) : null}
        </div>
        {needsToken ? (
          <div className="flex flex-wrap items-center gap-2">
            <input
              type="password"
              value={tokenInput}
              onChange={(e) => setTokenInput(e.target.value)}
              placeholder="XDL_API_TOKEN"
              className="border border-border bg-card px-2 py-1 font-mono text-[12px]"
            />
            <button
              type="button"
              disabled={saving}
              onClick={() => void saveToken()}
              className="border border-border bg-card px-2 py-1 font-mono text-[11px] uppercase tracking-[0.12em]"
            >
              {saving ? "…" : "save"}
            </button>
            <Link to="/settings" className="font-mono text-[11px] underline-offset-2 hover:underline">
              settings
            </Link>
            {saveError ? <span className="font-mono text-[11px] text-breach">{saveError}</span> : null}
          </div>
        ) : null}
      </div>
    </div>
  );
}
