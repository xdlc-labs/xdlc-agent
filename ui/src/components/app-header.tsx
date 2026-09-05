import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { fetchOverview } from "@/lib/api";
import { fetchAuthConfig, fetchRole } from "@/lib/auth";
import { t } from "@/lib/i18n";
import { resolveTheme, toggleTheme, type Theme } from "@/lib/theme";

const nav = [
  { to: "/", key: "nav.overview" },
  { to: "/gates", key: "nav.gates" },
  { to: "/repos", key: "nav.repos" },
  { to: "/activity", key: "nav.activity" },
  { to: "/actions", key: "nav.actions" },
  { to: "/settings", key: "nav.settings" },
] as const;

function useDaemon() {
  return useQuery({ queryKey: ["overview"], queryFn: fetchOverview, refetchInterval: 10_000 });
}

/** Top strip — online status + env sit on the right. */
export function AppTopBar() {
  const { data } = useDaemon();
  const daemon = data?.daemon;
  const status = daemon?.status ?? "stopped";
  const online = status === "running";
  const { data: authConfig } = useQuery({ queryKey: ["auth-config"], queryFn: fetchAuthConfig, staleTime: Infinity });
  const { data: role } = useQuery({ queryKey: ["role"], queryFn: fetchRole, refetchInterval: 60_000 });

  return (
    <div className="sticky top-0 z-20 flex items-center justify-end gap-3 border-b border-border/70 bg-background/80 px-6 py-2.5 backdrop-blur-md">
      <span className="rounded border border-border px-2.5 py-1 font-mono text-[11px] text-muted-foreground">
        {t("header.env")} <span className="text-foreground">{daemon?.env ?? "—"}</span>
      </span>

      <span
        className={`inline-flex items-center gap-2 rounded border px-2.5 py-1 font-mono text-[11px] ${
          online
            ? "border-pass/40 bg-pass/10 text-pass"
            : status === "degraded"
              ? "border-acting/40 bg-acting/10 text-acting"
              : "border-breach/40 bg-breach/10 text-breach"
        }`}
        aria-label={`${t("header.daemon")} ${online ? "online" : status}`}
      >
        <span
          className={`size-1.5 rounded-full ${online ? "bg-pass dot-live" : status === "degraded" ? "bg-acting" : "bg-breach"}`}
          aria-hidden
        />
        {online ? "online" : status}
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

      <nav aria-label={t("header.nav")} className="flex flex-1 flex-col gap-1 px-3 py-5">
        {nav.map((n) => (
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
            className="nav-link rounded border px-3 py-2.5 font-mono text-[11px] uppercase tracking-[0.14em]"
          >
            {t(n.key)}
          </Link>
        ))}
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
        <a href="#" className="font-mono text-[11px] text-muted-foreground hover:text-primary">
          {t("header.docs")}
        </a>
        <a href="#" className="font-mono text-[11px] text-muted-foreground hover:text-primary">
          {t("header.github")}
        </a>
      </div>
    </aside>
  );
}

export const AppHeader = AppSidebar;
