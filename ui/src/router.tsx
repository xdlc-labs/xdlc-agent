import { QueryClient } from "@tanstack/react-query";
import { createRouter } from "@tanstack/react-router";
import { routeTree } from "./routeTree.gen";

export const getRouter = () => {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: {
        // Route changes remount observers of the same key within seconds;
        // a short staleTime turns those into cache reads instead of a
        // refetch burst. The SSE stream and the per-query intervals keep
        // the data fresh, so focus-driven refetches add nothing.
        staleTime: 5_000,
        refetchOnWindowFocus: false,
      },
    },
  });

  const router = createRouter({
    routeTree,
    context: { queryClient },
    scrollRestoration: true,
  });

  return router;
};
