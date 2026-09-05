import { Children, isValidElement, type ReactElement, type ReactNode } from "react";
import type { Components } from "react-markdown";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { Link } from "@tanstack/react-router";
import { DocAsciiDiagram, DocCodeBlock } from "@/components/doc-code";
import { DocMermaid } from "@/components/doc-mermaid";
import { docHrefToRoute, docImageSrc } from "@/lib/docs-links";

export { docHrefToRoute, docImageSrc } from "@/lib/docs-links";

function fenceLang(node: ReactElement<{ className?: string }>): string {
  const m = /language-([\w-]+)/.exec(node.props.className ?? "");
  return m?.[1] ?? "text";
}

function fenceBody(node: ReactElement<{ children?: ReactNode }>): string {
  const raw = node.props.children;
  if (typeof raw === "string") return raw.replace(/\n$/, "");
  if (Array.isArray(raw)) {
    return raw.map((c) => (typeof c === "string" ? c : "")).join("").replace(/\n$/, "");
  }
  return String(raw ?? "").replace(/\n$/, "");
}

const components: Components = {
  h1: ({ children }) => (
    <h1 className="font-mono text-sm uppercase tracking-[0.2em] text-primary">{children}</h1>
  ),
  h2: ({ children }) => (
    <h2 className="mt-8 border-b border-border/60 pb-2 font-mono text-[12px] uppercase tracking-[0.16em] text-foreground">
      {children}
    </h2>
  ),
  h3: ({ children }) => (
    <h3 className="mt-6 font-mono text-[11px] uppercase tracking-[0.14em] text-muted-foreground">{children}</h3>
  ),
  p: ({ children }) => <p className="mt-3 text-sm leading-relaxed text-foreground/90">{children}</p>,
  ul: ({ children }) => <ul className="mt-3 list-disc space-y-1 pl-5 text-sm text-foreground/90">{children}</ul>,
  ol: ({ children }) => <ol className="mt-3 list-decimal space-y-1 pl-5 text-sm text-foreground/90">{children}</ol>,
  li: ({ children }) => <li className="leading-relaxed">{children}</li>,
  a: ({ href, children }) => {
    const to = docHrefToRoute(href);
    if (to) {
      return (
        <Link to={to} className="text-primary underline-offset-2 hover:underline">
          {children}
        </Link>
      );
    }
    return (
      <a
        href={href}
        target={href?.startsWith("http") ? "_blank" : undefined}
        rel={href?.startsWith("http") ? "noreferrer" : undefined}
        className="text-primary underline-offset-2 hover:underline"
      >
        {children}
      </a>
    );
  },
  code: ({ className, children }) => {
    if (!className) {
      return (
        <code className="rounded border border-border bg-surface px-1 py-0.5 font-mono text-[12px] text-primary">
          {children}
        </code>
      );
    }
    return <code className={className}>{children}</code>;
  },
  pre: ({ children }) => {
    const child = Children.toArray(children).find((c) => isValidElement(c)) as
      | ReactElement<{ className?: string; children?: ReactNode }>
      | undefined;
    if (!child) return null;
    const lang = fenceLang(child);
    const body = fenceBody(child);
    if (lang === "mermaid") return <DocMermaid chart={body} />;
    if (lang === "text" || lang === "diagram" || lang === "ascii") {
      return <DocAsciiDiagram code={body} />;
    }
    return <DocCodeBlock {...(child.props.className ? { className: child.props.className } : {})} code={body} />;
  },
  table: ({ children }) => (
    <div className="mt-4 overflow-x-auto rounded border border-border">
      <table className="w-full border-collapse text-left text-sm">{children}</table>
    </div>
  ),
  thead: ({ children }) => <thead className="border-b border-border bg-surface/60">{children}</thead>,
  th: ({ children }) => (
    <th className="px-3 py-2.5 font-mono text-[11px] uppercase tracking-wider text-muted-foreground">{children}</th>
  ),
  td: ({ children }) => <td className="border-b border-border/50 px-3 py-2.5 align-top text-foreground/90">{children}</td>,
  blockquote: ({ children }) => (
    <blockquote className="mt-4 border-l-2 border-primary/50 bg-primary/5 px-4 py-2 text-sm text-muted-foreground">
      {children}
    </blockquote>
  ),
  hr: () => <hr className="my-8 border-border/70" />,
  strong: ({ children }) => <strong className="font-semibold text-foreground">{children}</strong>,
  img: ({ src, alt }) => {
    const resolved = docImageSrc(src);
    if (!resolved) return null;
    return (
      <figure className="mt-5">
        <img
          src={resolved}
          alt={alt ?? ""}
          className="w-full max-w-3xl rounded border border-border bg-[#070a0c] object-contain"
          loading="lazy"
        />
        {alt ? (
          <figcaption className="mt-2 font-mono text-[11px] text-muted-foreground">{alt}</figcaption>
        ) : null}
      </figure>
    );
  },
};

export function DocMarkdown({ source }: { source: string }) {
  return (
    <div className="docs-md max-w-3xl">
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={components}>
        {source}
      </ReactMarkdown>
    </div>
  );
}
