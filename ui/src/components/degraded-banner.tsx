import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { fetchOverview, isDegraded, lastFetchStatus } from "@/lib/api";
import { getToken, setToken, clearToken } from "@/lib/auth";

export function DegradedBanner() {
  const queryClient = useQueryClient();
  const { data } = useQuery({
    queryKey: ["overview"],
    queryFn: fetchOverview,
    refetchInterval: 10_000,
  });

  const [tokenInput, setTokenInput] = useState(() => getToken());
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);

  if (!data || !isDegraded(data)) return null;

  const webhook = data.daemon.webhook;
  const needsToken =
    lastFetchStatus === 401 ||
    webhook.includes("401") ||
    webhook.includes("unauthorized");
  const is503 =
    lastFetchStatus === 503 || webhook.includes("503");

  const refresh = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["overview"] }),
      queryClient.invalidateQueries({ queryKey: ["history"] }),
      queryClient.invalidateQueries({ queryKey: ["backlog"] }),
      queryClient.invalidateQueries({ queryKey: ["repos"] }),
      queryClient.invalidateQueries({ queryKey: ["prs"] }),
      queryClient.invalidateQueries({ queryKey: ["kpis"] }),
      queryClient.invalidateQueries({ queryKey: ["role"] }),
    ]);
  };

  const saveToken = async () => {
    const trimmed = tokenInput.trim();
    setSaveError(null);
    setSaving(true);
    try {
      if (trimmed) setToken(trimmed);
      else clearToken();
      // Probe whoami before declaring success
      const res = await fetch("/api/whoami", {
        headers: trimmed ? { Authorization: `Bearer ${trimmed}` } : {},
      });
      if (!res.ok) {
        setSaveError(
          res.status === 401
            ? "still 401 — check token matches XDL_API_TOKEN on the daemon"
            : `whoami → ${res.status}`,
        );
        return;
      }
      await refresh();
    } finally {
      setSaving(false);
    }
  };

  return (
    <div
      role="status"
      className="border-b border-breach/50 bg-breach/10 px-6 py-3"
    >
      {needsToken ? (
        <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:gap-3">
          <p className="shrink-0 font-mono text-[12px] text-breach">
            <span className="uppercase tracking-wider">401</span>
            <span className="mx-2 text-breach/60">·</span>
            add bearer token
          </p>
          <div className="flex min-w-0 flex-1 flex-wrap items-center gap-2">
            <input
              type="password"
              autoComplete="off"
              value={tokenInput}
              onChange={(e) => {
                setTokenInput(e.target.value);
                setSaveError(null);
              }}
              onKeyDown={(e) => {
                if (e.key === "Enter") void saveToken();
              }}
              placeholder="XDL_API_TOKEN / Bearer value"
              className="min-w-[14rem] flex-1 border border-breach/40 bg-background px-3 py-1.5 font-mono text-[12px] text-foreground outline-none focus:border-primary"
              aria-label="API bearer token"
            />
            <button
              type="button"
              disabled={saving}
              onClick={() => void saveToken()}
              className="border border-primary bg-primary px-3 py-1.5 font-mono text-[11px] text-primary-foreground hover:opacity-90 disabled:opacity-60"
            >
              {saving ? "…" : "save"}
            </button>
            <Link
              to="/settings"
              className="font-mono text-[11px] text-breach underline hover:text-foreground"
            >
              Settings
            </Link>
          </div>
          {saveError && (
            <p className="w-full font-mono text-[11px] text-breach">{saveError}</p>
          )}
        </div>
      ) : (
        <p className="font-mono text-[12px] text-breach">
          <span className="uppercase tracking-wider">degraded</span>
          <span className="mx-2 text-breach/60">·</span>
          {is503
            ? "503 — daemon API token not configured (set XDL_API_TOKEN on the server)."
            : "Backend unreachable — showing empty fallback (not an empty config)."}
        </p>
      )}
    </div>
  );
}
