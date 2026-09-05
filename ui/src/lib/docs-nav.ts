/** Sidebar catalog for /docs — same theme as console, not a second site. */
export type DocsItem = { slug: string; title: string };
export type DocsGroup = {
  id: string;
  label: string;
  items: DocsItem[];
  /** Collapsed by default in the TOC (secondary reading). */
  collapsed?: boolean;
};

export const DOCS_NAV: DocsGroup[] = [
  {
    id: "start",
    label: "Start",
    items: [
      { slug: "getting-started", title: "Getting started" },
      { slug: "api-tokens", title: "API tokens" },
    ],
  },
  {
    id: "production",
    label: "Production loop",
    items: [
      { slug: "production-loop", title: "Overview" },
      { slug: "github-webhooks", title: "GitHub" },
      { slug: "fix-modes", title: "Fix modes" },
      { slug: "gitops-argo", title: "GitOps" },
      { slug: "prod-health", title: "Prod health" },
      { slug: "deployment", title: "Deploy" },
      { slug: "operations", title: "Operations" },
    ],
  },
  {
    id: "reference",
    label: "Reference",
    items: [
      { slug: "configuration", title: "Configuration" },
      { slug: "console", title: "Console" },
      { slug: "architecture", title: "Architecture" },
      { slug: "api-reference", title: "API" },
      { slug: "SECURITY", title: "Security" },
    ],
  },
  {
    id: "more",
    label: "More",
    collapsed: true,
    items: [
      { slug: "CONTRIBUTING", title: "Contributing" },
      { slug: "vs-alternatives", title: "vs alternatives" },
      { slug: "why-not-github-action", title: "Why not GitHub Action" },
    ],
  },
];

export const DEFAULT_DOCS_SLUG = "getting-started";

export function docsTitle(slug: string): string {
  for (const g of DOCS_NAV) {
    const hit = g.items.find((i) => i.slug === slug);
    if (hit) return hit.title;
  }
  return slug;
}

/** Flat reading order for prev/next. */
export function docsReadingOrder(): DocsItem[] {
  return DOCS_NAV.flatMap((g) => g.items);
}

export function docsAdjacent(slug: string): { prev?: DocsItem; next?: DocsItem } {
  const order = docsReadingOrder();
  const i = order.findIndex((d) => d.slug === slug);
  if (i < 0) return {};
  return {
    ...(i > 0 ? { prev: order[i - 1] } : {}),
    ...(i < order.length - 1 ? { next: order[i + 1] } : {}),
  };
}

export function allDocsSlugs(): string[] {
  return docsReadingOrder().map((i) => i.slug);
}
