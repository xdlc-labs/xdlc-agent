/** Pure helpers for docs links/images (no React / mermaid — safe for unit tests). */

export function docHrefToRoute(href: string | undefined): string | null {
  if (!href || href.startsWith("http") || href.startsWith("mailto:") || href.startsWith("#")) {
    return null;
  }
  const clean = href.replace(/^\.\//, "").replace(/^\//, "");
  if (clean.endsWith(".md")) {
    const slug = clean.replace(/\.md$/i, "").split("/").pop()!;
    if (slug.toLowerCase() === "readme") return "/docs";
    return `/docs/${slug}`;
  }
  return null;
}

export function docImageSrc(src: string | undefined): string | undefined {
  if (!src) return undefined;
  if (src.startsWith("http") || src.startsWith("data:") || src.startsWith("/")) return src;
  const cleaned = src.replace(/^\.\//, "");
  if (cleaned.startsWith("assets/")) return `/docs-assets/${cleaned.slice("assets/".length)}`;
  if (cleaned.includes("/assets/")) {
    const i = cleaned.indexOf("/assets/");
    return `/docs-assets/${cleaned.slice(i + "/assets/".length)}`;
  }
  return src;
}
