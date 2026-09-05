import { createFileRoute, Link } from "@tanstack/react-router";
import { DocMarkdown } from "@/components/doc-markdown";
import { getDocMarkdown, hasDoc } from "@/lib/docs-content";
import { docsAdjacent, docsTitle } from "@/lib/docs-nav";
import { EmptyState } from "@/components/query-state";

export const Route = createFileRoute("/docs/$slug")({
  head: ({ params }) => ({
    meta: [{ title: `${docsTitle(params.slug)} — Docs | xdlc-agent` }],
  }),
  component: DocPage,
});

function DocPage() {
  const { slug } = Route.useParams();
  if (!hasDoc(slug)) {
    return (
      <EmptyState>
        No doc for <span className="font-mono text-primary">{slug}</span>.{" "}
        <Link to="/docs/$slug" params={{ slug: "getting-started" }} className="text-primary hover:underline">
          Getting started
        </Link>
      </EmptyState>
    );
  }
  const source = getDocMarkdown(slug)!;
  const { prev, next } = docsAdjacent(slug);

  return (
    <article>
      <div className="mc-panel rounded-md px-5 py-6 sm:px-8">
        <DocMarkdown source={source} />
      </div>

      <nav
        aria-label="Doc pager"
        className="mt-6 flex flex-wrap items-stretch justify-between gap-3 border-t border-border/60 pt-4"
      >
        {prev ? (
          <Link
            to="/docs/$slug"
            params={{ slug: prev.slug }}
            className="group min-w-[10rem] flex-1 rounded border border-border bg-surface/40 px-4 py-3 hover:border-primary/40"
          >
            <div className="font-mono text-[10px] uppercase tracking-wider text-muted-foreground">Previous</div>
            <div className="mt-1 font-mono text-[12px] text-foreground group-hover:text-primary">
              ← {prev.title}
            </div>
          </Link>
        ) : (
          <span className="flex-1" />
        )}
        {next ? (
          <Link
            to="/docs/$slug"
            params={{ slug: next.slug }}
            className="group min-w-[10rem] flex-1 rounded border border-border bg-surface/40 px-4 py-3 text-right hover:border-primary/40"
          >
            <div className="font-mono text-[10px] uppercase tracking-wider text-muted-foreground">Next</div>
            <div className="mt-1 font-mono text-[12px] text-foreground group-hover:text-primary">
              {next.title} →
            </div>
          </Link>
        ) : (
          <span className="flex-1" />
        )}
      </nav>
    </article>
  );
}
