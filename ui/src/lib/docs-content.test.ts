import { describe, expect, it } from "vitest";
import { docHrefToRoute, docImageSrc } from "@/lib/docs-links";
import { hasDoc } from "@/lib/docs-content";
import { docsAdjacent } from "@/lib/docs-nav";

describe("docHrefToRoute", () => {
  it("maps markdown links to /docs/$slug", () => {
    expect(docHrefToRoute("getting-started.md")).toBe("/docs/getting-started");
    expect(docHrefToRoute("./production-loop.md")).toBe("/docs/production-loop");
    expect(docHrefToRoute("https://example.com")).toBeNull();
    expect(docHrefToRoute("#hash")).toBeNull();
  });
});

describe("docImageSrc", () => {
  it("maps assets/ paths to /docs-assets", () => {
    expect(docImageSrc("assets/architecture.jpg")).toBe("/docs-assets/architecture.jpg");
    expect(docImageSrc("./assets/screenshots/console-overview.jpg")).toBe(
      "/docs-assets/screenshots/console-overview.jpg",
    );
    expect(docImageSrc("https://cdn.example/x.png")).toBe("https://cdn.example/x.png");
  });
});

describe("docs-content", () => {
  it("loads curated production pages", () => {
    expect(hasDoc("install")).toBe(true);
    expect(hasDoc("getting-started")).toBe(true);
    expect(hasDoc("production-loop")).toBe(true);
    expect(hasDoc("github-webhooks")).toBe(true);
  });
});

describe("docsAdjacent", () => {
  it("orders install → getting started → api tokens → GitHub", () => {
    const { next: fromInstall } = docsAdjacent("install");
    expect(fromInstall?.slug).toBe("getting-started");
    const { next } = docsAdjacent("getting-started");
    expect(next?.slug).toBe("api-tokens");
    const { prev, next: n2 } = docsAdjacent("api-tokens");
    expect(prev?.slug).toBe("getting-started");
    expect(n2?.slug).toBe("github-webhooks");
  });
});
