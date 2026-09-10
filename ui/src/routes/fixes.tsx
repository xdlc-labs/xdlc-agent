import { useEffect } from "react";
import { createFileRoute } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { fetchActiveFixes } from "@/lib/api";
import { liveFixes, useLiveFixes } from "@/lib/live-events";
import { PageHeader } from "@/components/status";
import { EmptyState } from "@/components/query-state";
import { FixCard } from "@/components/fix-card";

export const Route = createFileRoute("/fixes")({
  head: () => ({ meta: [{ title: "fixes — live | xdlc-agent" }] }),
  component: FixesGrid,
});

/**
 * One card per Fix, with what its agent is printing right now. Read-only:
 * this is for watching a burst of Fixes across the fleet at once. State
 * and output arrive over /api/events; the poll below is the safety net
 * that seeds the grid before the stream connects and after it drops.
 */
function FixesGrid() {
  const fixes = useLiveFixes();
  const { data } = useQuery({
    queryKey: ["fixes-active"],
    queryFn: fetchActiveFixes,
    refetchInterval: 10_000,
  });
  useEffect(() => {
    if (data) liveFixes.seed(data);
  }, [data]);

  const running = fixes.filter((f) => !f.endedAt).length;
  return (
    <div>
      <PageHeader
        title="fixes"
        sub={
          running === 0
            ? "Live agent output per Fix. Nothing running."
            : `Live agent output per Fix. ${running} running.`
        }
      />
      <div className="px-6 pb-10">
        {fixes.length === 0 ? (
          <EmptyState>
            No Fix running. Cards appear here the moment one is queued, and stay for ten minutes after it ends.
          </EmptyState>
        ) : (
          <ul className="grid gap-4 md:grid-cols-2 2xl:grid-cols-3">
            {fixes.map((f) => (
              <FixCard key={f.fix.id} live={f} />
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}
