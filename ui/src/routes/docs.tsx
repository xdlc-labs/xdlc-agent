import { Link, Outlet, createFileRoute, useRouterState } from "@tanstack/react-router";
import { PageHeader } from "@/components/status";
import { DOCS_NAV } from "@/lib/docs-nav";

export const Route = createFileRoute("/docs")({
  head: () => ({
    meta: [{ title: "Docs | xdlc-agent" }],
  }),
  component: DocsLayout,
});

function DocsLayout() {
  const pathname = useRouterState({ select: (s) => s.location.pathname });

  return (
    <div>
      <PageHeader title="docs" sub="Start local, then wire the production loop." />
      <div className="flex flex-col lg:flex-row">
        <aside className="lg:sticky lg:top-12 lg:max-h-[calc(100vh-3rem)] lg:w-52 lg:shrink-0 lg:overflow-y-auto lg:border-r lg:border-border/60">
          {/* Mobile: horizontal section chips */}
          <div className="flex gap-1 overflow-x-auto border-b border-border/60 px-4 py-3 lg:hidden">
            {DOCS_NAV.flatMap((g) => g.items).map((item) => {
              const active = pathname === `/docs/${item.slug}`;
              return (
                <Link
                  key={item.slug}
                  to="/docs/$slug"
                  params={{ slug: item.slug }}
                  className={`shrink-0 rounded border px-2.5 py-1 font-mono text-[10px] uppercase tracking-wider ${
                    active
                      ? "border-primary/50 bg-primary/15 text-primary"
                      : "border-border text-muted-foreground"
                  }`}
                >
                  {item.title}
                </Link>
              );
            })}
          </div>

          <nav aria-label="Docs" className="hidden flex-col gap-5 px-3 py-5 lg:flex">
            {DOCS_NAV.map((group) => {
              const body = (
                <ul className="flex flex-col">
                  {group.items.map((item) => (
                    <li key={item.slug}>
                      <Link
                        to="/docs/$slug"
                        params={{ slug: item.slug }}
                        activeProps={{ className: "text-primary border-primary bg-primary/10" }}
                        inactiveProps={{
                          className:
                            "border-transparent text-muted-foreground hover:border-border/80 hover:text-foreground",
                        }}
                        className="block border-l-2 py-1.5 pl-3 font-mono text-[11px] tracking-wide transition-colors"
                      >
                        {item.title}
                      </Link>
                    </li>
                  ))}
                </ul>
              );

              if (group.collapsed) {
                return (
                  <details key={group.id} className="group/more">
                    <summary className="cursor-pointer list-none px-1 font-mono text-[10px] uppercase tracking-[0.16em] text-muted-foreground hover:text-foreground [&::-webkit-details-marker]:hidden">
                      <span className="inline-flex items-center gap-1.5">
                        <span className="text-primary/70 group-open/more:rotate-90 transition-transform">›</span>
                        {group.label}
                      </span>
                    </summary>
                    <div className="mt-2">{body}</div>
                  </details>
                );
              }

              return (
                <div key={group.id}>
                  <div className="mb-2 px-1 font-mono text-[10px] uppercase tracking-[0.16em] text-muted-foreground">
                    {group.label}
                  </div>
                  {body}
                </div>
              );
            })}
          </nav>
        </aside>

        <div className="min-w-0 flex-1 px-5 py-5 sm:px-8 sm:py-6">
          <Outlet />
        </div>
      </div>
    </div>
  );
}
