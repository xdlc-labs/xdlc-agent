import { createFileRoute, redirect } from "@tanstack/react-router";
import { DEFAULT_DOCS_SLUG } from "@/lib/docs-nav";

export const Route = createFileRoute("/docs/")({
  beforeLoad: () => {
    throw redirect({ to: "/docs/$slug", params: { slug: DEFAULT_DOCS_SLUG } });
  },
});
