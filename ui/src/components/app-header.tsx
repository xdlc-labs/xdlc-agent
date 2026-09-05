import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { fetchFixPRs, fetchOverview, lastFetchStatus } from "@/lib/api";
import { fetchAuthConfig, fetchRole } from "@/lib/auth";
import { t } from "@/lib/i18n";
import { resolveTheme, toggleTheme, type Theme } from "@/lib/theme";

const navOps = [
  { to: "/", key: "nav.overview" },
  { to: "/gates", key: "nav.gates" },
  { to: "/repos", key: "nav.repos" },
  { to: "/activity", key: "nav.activity" },
  { to: "/actions", key: "nav.actions" },
] as const;

const navMeta = [
  { to: "/docs", key: "nav.docs" },
  { to: "/settings", key: "nav.settings" },
] as const;

function useDaemon() {
  return useQuery({ queryKey: ["overview"], queryFn: fetchOverview, refetchInterval: 10_000 });
}

function useOpenFixPRCount() {
  const { data } = useQuery({
    queryKey: ["fix-prs", false],
    queryFn: () => fetchFixPRs(false),
    refetchInterval: 30_000,
  });
  return data?.length ?? 0;
}

/** Chip copy for the top-bar daemon status. Online only after a 200 overview. */
export function daemonChipLabel(args: {
  isPending: boolean;
  isError: boolean;
  daemonStatus?: string;
  fetchStatus: number | null;
}): string {
  if (args.fetchStatus === 401) {
    return "token rejected";
  }
  if (args.isPending) return "connecting";
  if (args.isError) return "stopped";
  const status = args.daemonStatus ?? "stopped";
  if (status === "running") return "online";
  return status;
}

function chipTone(label: string): "online" | "connecting" | "degraded" | "breach" {
  if (label === "online") return "online";
  if (label === "connecting") return "connecting";
  if (label === "degraded") return "degraded";
  return "breach";
}

/** Top strip — online status + env sit on the right. */
export function AppTopBar() {
  const { data, isError, isPending } = useDaemon();
  const daemon = data?.daemon;
  const label = daemonChipLabel({
    isPending,
    isError,
    daemonStatus: daemon?.status,
    fetchStatus: lastFetchStatus,
  });
  const tone = chipTone(label);
  const { data: authConfig } = useQuery({ queryKey: ["auth-config"], queryFn: fetchAuthConfig, staleTime: Infinity });
  const { data: role } = useQuery({ queryKey: ["role"], queryFn: fetchRole, refetchInterval: 60_000 });

  return (
    <div className="sticky top-0 z-20 flex items-center justify-end gap-3 border-b border-border/70 bg-background/80 px-6 py-2.5 backdrop-blur-md">
      <span className="rounded border border-border px-2.5 py-1 font-mono text-[11px] text-muted-foreground">
        {t("header.env")} <span className="text-foreground">{daemon?.env ?? "—"}</span>
      </span>

      <span
        className={`inline-flex items-center gap-2 rounded border px-2.5 py-1 font-mono text-[11px] ${
          tone === "online"
            ? "border-pass/40 bg-pass/10 text-pass"
            : tone === "connecting"
              ? "border-border bg-card text-muted-foreground"
              : tone === "degraded"
                ? "border-acting/40 bg-acting/10 text-acting"
                : "border-breach/40 bg-breach/10 text-breach"
        }`}
        aria-label={`${t("header.daemon")} ${label}`}
      >
        <span
          className={`size-1.5 rounded-full ${
            tone === "online" ? "bg-pass dot-live" : tone === "connecting" ? "bg-muted-foreground" : tone === "degraded" ? "bg-acting" : "bg-breach"
          }`}
          aria-hidden
        />
        {label}
      </span>

      {authConfig?.enabled && (
        <>
          <span className="rounded border border-border px-2 py-1 font-mono text-[11px] text-muted-foreground">
            {role === "operator"
              ? t("header.roleOperator")
              : role === "viewer"
                ? t("header.roleViewer")
                : t("header.notSignedIn")}
          </span>
          {role ? (
            <a
              href={authConfig.logoutUrl ?? "/auth/logout"}
              className="rounded border border-border px-2 py-1 font-mono text-[11px] text-muted-foreground hover:text-foreground"
            >
              {t("header.signOut")}
            </a>
          ) : (
            <a
              href={authConfig.loginUrl ?? "/auth/login"}
              className="rounded border border-primary/60 px-2 py-1 font-mono text-[11px] text-primary hover:bg-primary/10"
            >
              {t("header.signIn")}
            </a>
          )}
        </>
      )}
    </div>
  );
}

/** Left rail: brand + primary nav. */
export function AppSidebar() {
  const { data } = useDaemon();
  const daemon = data?.daemon;
  const openPRs = useOpenFixPRCount();
  const [theme, setThemeState] = useState<Theme>(() =>
    typeof window === "undefined" ? "dark" : resolveTheme(),
  );

  return (
    <aside className="sticky top-0 flex h-screen w-[15.5rem] shrink-0 flex-col border-r border-border/70 bg-[#070a0c]/90 backdrop-blur-md">
      <div className="border-b border-border/70 px-5 py-6">
        <Link to="/" className="group block">
          <span className="brand-mark font-display text-[1.7rem] font-bold uppercase text-foreground group-hover:text-primary">
            xdlc
          </span>
          <span className="mt-1.5 block font-mono text-[11px] tracking-wide text-primary">
            xdlc-agent {daemon?.version ?? "—"}
          </span>
        </Link>
      </div>

      <nav aria-label={t("header.nav")} className="flex flex-1 flex-col px-3 py-5">
        <div className="flex flex-col gap-1">
          {navOps.map((n) => (
            <Link
              key={n.to}
              to={n.to}
              activeOptions={{ exact: n.to === "/" }}
              activeProps={{
                className: "text-primary bg-primary/15 border-primary/40",
              }}
              inactiveProps={{
                className: "text-muted-foreground border-transparent hover:bg-surface/80 hover:text-foreground",
              }}
              className="nav-link flex items-center justify-between gap-2 rounded border px-3 py-2.5 font-mono text-[11px] uppercase tracking-[0.14em]"
            >
              <span>{t(n.key)}</span>
              {n.to === "/actions" && openPRs > 0 ? (
                <span
                  className="rounded bg-primary/20 px-1.5 py-0.5 font-mono text-[10px] normal-case tracking-normal text-primary"
                  aria-label={`${openPRs} open Fix PRs`}
                >
                  {openPRs}
                </span>
              ) : null}
            </Link>
          ))}
        </div>

        <div className="my-4 border-t border-border/50" role="separator" />

        <div className="flex flex-col gap-1">
          {navMeta.map((n) => (
            <Link
              key={n.to}
              to={n.to}
              activeOptions={{ exact: n.to === "/settings" }}
              activeProps={{
                className: "text-primary bg-primary/15 border-primary/40",
              }}
              inactiveProps={{
                className: "text-muted-foreground border-transparent hover:bg-surface/80 hover:text-foreground",
              }}
              className="nav-link flex items-center justify-between gap-2 rounded border px-3 py-2.5 font-mono text-[11px] uppercase tracking-[0.14em]"
            >
              <span>{t(n.key)}</span>
            </Link>
          ))}
        </div>
      </nav>

      <div className="mt-auto flex flex-wrap items-center gap-2 border-t border-border/70 px-4 py-4">
        <button
          type="button"
          onClick={() => setThemeState(toggleTheme())}
          aria-label={t("header.theme")}
          className="rounded border border-border px-2 py-0.5 font-mono text-[11px] text-muted-foreground hover:border-primary/40 hover:text-foreground focus-visible:outline focus-visible:outline-2 focus-visible:outline-ring"
        >
          {theme === "dark" ? "light" : "dark"}
        </button>
        <a
          href="https://github.com/xdlc-labs/xdlc-agent"
          target="_blank"
          rel="noreferrer"
          className="font-mono text-[11px] text-muted-foreground hover:text-primary"
        >
          {t("header.github")}
        </a>
      </div>
    </aside>
  );
}

export const AppHeader = AppSidebar;
