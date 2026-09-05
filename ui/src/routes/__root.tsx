import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  Outlet,
  Link,
  createRootRouteWithContext,
  useRouter,
  HeadContent,
} from "@tanstack/react-router";

import { AppSidebar, AppTopBar } from "@/components/app-header";
import { DegradedBanner } from "@/components/degraded-banner";
import { useLiveEvents } from "@/lib/live-events";

function NotFoundComponent() {
  return (
    <div className="flex min-h-screen items-center justify-center bg-background px-4">
      <div className="max-w-md text-center">
        <h1 className="text-7xl font-bold text-foreground">404</h1>
        <h2 className="mt-4 text-xl font-semibold text-foreground">Page not found</h2>
        <p className="mt-2 text-sm text-muted-foreground">No route for this URL.</p>
        <div className="mt-6">
          <Link
            to="/"
            className="inline-flex items-center justify-center rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground transition-colors hover:bg-primary/90"
          >
            Home
          </Link>
        </div>
      </div>
    </div>
  );
}

function ErrorComponent({ error, reset }: { error: Error; reset: () => void }) {
  console.error(error);
  const router = useRouter();

  return (
    <div className="flex min-h-screen items-center justify-center bg-background px-4">
      <div className="max-w-md text-center">
        <h1 className="text-xl font-semibold tracking-tight text-foreground">
          Page failed to load
        </h1>
        <p className="mt-2 text-sm text-muted-foreground font-mono">
          {error.message || "unknown error"}
        </p>
        <div className="mt-6 flex flex-wrap justify-center gap-2">
          <button
            type="button"
            onClick={() => {
              router.invalidate();
              reset();
            }}
            className="inline-flex items-center justify-center rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground transition-colors hover:bg-primary/90"
          >
            Retry
          </button>
          <a
            href="/"
            className="inline-flex items-center justify-center rounded-md border border-input bg-background px-4 py-2 text-sm font-medium text-foreground transition-colors hover:bg-accent"
          >
            Home
          </a>
        </div>
      </div>
    </div>
  );
}

// Static <html>/<head>/<body> shell, favicon, google fonts, and the
// pre-paint theme-boot inline script live in index.html (Vite's SPA
// entry) — this is a plain client render into #root, not SSR, so there's
// no document shell to build here. `head()` below only supplies the
// per-route <title>/<meta description> that <HeadContent/> renders;
// React 19 hoists <title>/<meta>/<link> elements to the real
// document head no matter where they're rendered in the tree.
export const Route = createRootRouteWithContext<{ queryClient: QueryClient }>()({
  head: () => ({
    meta: [
      { title: "xdlc — xdlc-agent console" },
      {
        name: "description",
        content:
          "Ops console for xdlc-agent: activity, gates, repos, Fix / Promote / Revert.",
      },
    ],
  }),

  component: RootComponent,
  notFoundComponent: NotFoundComponent,
  errorComponent: ErrorComponent,
});

function RootComponent() {
  const { queryClient } = Route.useRouteContext();
  useLiveEvents();

  return (
    <QueryClientProvider client={queryClient}>
      <HeadContent />
      <div className="flex min-h-screen">
        <AppSidebar />
        <div className="flex min-w-0 flex-1 flex-col">
          <AppTopBar />
          <DegradedBanner />
          <main id="main" className="mx-auto w-full max-w-[1600px] flex-1">
            <Outlet />
          </main>
        </div>
      </div>
    </QueryClientProvider>
  );
}
