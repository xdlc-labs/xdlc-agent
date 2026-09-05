import { useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { getToken } from "@/lib/auth";
import { eventsURL } from "@/lib/api";

/**
 * Subscribe to GET /api/events SSE and invalidate console queries (issue #6).
 * Keeps slow refetchInterval as a safety net.
 */
export function useLiveEvents() {
  const queryClient = useQueryClient();

  useEffect(() => {
    let es: EventSource | null = null;
    let stopped = false;

    const connect = () => {
      if (stopped) return;
      // EventSource cannot set Authorization; pass token via query when present.
      const tok = getToken();
      const url = tok ? `${eventsURL()}?access_token=${encodeURIComponent(tok)}` : eventsURL();
      es = new EventSource(url);
      es.onmessage = () => {
        void queryClient.invalidateQueries({ queryKey: ["overview"] });
        void queryClient.invalidateQueries({ queryKey: ["history"] });
        void queryClient.invalidateQueries({ queryKey: ["fix-prs"] });
        void queryClient.invalidateQueries({ queryKey: ["backlog"] });
        void queryClient.invalidateQueries({ queryKey: ["kpis"] });
        void queryClient.invalidateQueries({ queryKey: ["repo"] });
      };
      es.onerror = () => {
        es?.close();
        es = null;
        if (!stopped) {
          window.setTimeout(connect, 3000);
        }
      };
    };

    connect();
    return () => {
      stopped = true;
      es?.close();
    };
  }, [queryClient]);
}
