import { useQuery } from "@tanstack/react-query";
import { fetchOverview, isDegraded, lastFetchStatus } from "@/lib/api";
import { Link } from "@tanstack/react-router";

export function DegradedBanner() {
  const { data } = useQuery({
    queryKey: ["overview"],
    queryFn: fetchOverview,
    refetchInterval: 10_000,
  });

  if (!data || !isDegraded(data)) return null;

  const webhook = data.daemon.webhook;
  const statusHint =
    lastFetchStatus === 401 || webhook.includes("401")
      ? "401 unauthorized — add bearer token under Settings."
      : lastFetchStatus === 503 || webhook.includes("503")
        ? "503 — daemon API token not configured (XDL_API_TOKEN)."
        : "Backend unreachable — showing empty fallback (not an empty config).";

  return (
    <div
      role="status"
      className="border-b border-breach/50 bg-breach/10 px-6 py-2.5"
    >
      <p className="font-mono text-[12px] text-breach">
        <span className="uppercase tracking-wider">degraded</span>
        <span className="mx-2 text-breach/60">·</span>
        {statusHint}
        {(lastFetchStatus === 401 || webhook.includes("401")) && (
          <>
            {" "}
            <Link to="/settings" className="underline hover:text-foreground">
              Settings →
            </Link>
          </>
        )}
      </p>
    </div>
  );
}
