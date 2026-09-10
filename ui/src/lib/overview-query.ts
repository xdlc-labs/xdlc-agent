// The one definition of the ["overview"] query. Every screen and the
// header/banner read the same daemon snapshot, so they must agree on the
// key, the poll interval and the retry policy — otherwise the first
// mounted observer's options silently win for all of them.
//
// This lives outside api.ts on purpose: tests mock fetchOverview through
// vi.mock("@/lib/api"), which only intercepts imports of that module. A
// queryFn defined inside api.ts would call the module-local original and
// bypass the mock.
import { queryOptions, useQuery } from "@tanstack/react-query";
import { fetchOverview } from "@/lib/api";

export const overviewQueryOptions = queryOptions({
  queryKey: ["overview"],
  queryFn: () => fetchOverview(),
  // The SSE stream invalidates this on every event; the interval is the
  // safety net for when the stream is down.
  refetchInterval: 10_000,
  retry: 1,
});

export function useOverview() {
  return useQuery(overviewQueryOptions);
}
