import { createFileRoute } from "@tanstack/react-router";
import { PageHeader } from "@/components/status";

export const Route = createFileRoute("/docs")({
  head: () => ({
    meta: [{ title: "Docs | xdlc-agent" }],
  }),
  component: DocsLanding,
});

const DOCS = "https://xdlc.dev";

const groups: { label: string; items: { href: string; title: string; blurb: string }[] }[] = [
  {
    label: "Start",
    items: [
      { href: `${DOCS}/agent/docs`, title: "Install", blurb: "curl-install, Docker, or source" },
      { href: `${DOCS}/agent/docs/getting-started`, title: "Getting started", blurb: "Demo or a local CI Fix daemon" },
      { href: `${DOCS}/agent/docs/api-tokens`, title: "API tokens", blurb: "Create XDLC_API_TOKEN" },
    ],
  },
  {
    label: "CI Fix",
    items: [
      { href: `${DOCS}/agent/docs/github-webhooks`, title: "GitHub", blurb: "workflow_run → Fix" },
      { href: `${DOCS}/agent/docs/fix-modes`, title: "Fix modes", blurb: "Direct push vs pull request" },
      { href: `${DOCS}/agent/docs/sessions`, title: "Fix sessions", blurb: "Prompt, output, and diff" },
    ],
  },
  {
    label: "Optional",
    items: [
      { href: `${DOCS}/agent/docs/production-loop`, title: "Profiles", blurb: "ci, gitops, full" },
      { href: `${DOCS}/agent/docs/gitops-argo`, title: "GitOps", blurb: "DEV smoke → Promote" },
      { href: `${DOCS}/agent/docs/prod-health`, title: "Prod health", blurb: "SLO breach → Revert" },
    ],
  },
];

function DocsLanding() {
  return (
    <div>
      <PageHeader title="docs" sub="Guides are hosted with the rest of the org. This console links out." />
      <div className="max-w-3xl px-5 py-6 sm:px-8">
        <a
          href={`${DOCS}/docs`}
          target="_blank"
          rel="noreferrer"
          className="inline-flex rounded border border-primary/50 bg-primary/10 px-3 py-2 font-mono text-[12px] text-primary hover:bg-primary/20"
        >
          Open docs site →
        </a>
        <div className="mt-8 flex flex-col gap-8">
          {groups.map((g) => (
            <section key={g.label}>
              <h2 className="mb-3 font-mono text-[10px] uppercase tracking-[0.16em] text-muted-foreground">
                {g.label}
              </h2>
              <ul className="flex flex-col gap-2">
                {g.items.map((item) => (
                  <li key={item.href}>
                    <a
                      href={item.href}
                      target="_blank"
                      rel="noreferrer"
                      className="block rounded border border-border bg-surface/40 px-4 py-3 hover:border-primary/40"
                    >
                      <div className="font-mono text-[12px] text-foreground">{item.title}</div>
                      <div className="mt-1 text-[12px] text-muted-foreground">{item.blurb}</div>
                    </a>
                  </li>
                ))}
              </ul>
            </section>
          ))}
        </div>
      </div>
    </div>
  );
}
