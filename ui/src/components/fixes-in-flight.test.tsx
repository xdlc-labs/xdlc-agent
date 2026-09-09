import { render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ActiveFix } from "@/lib/api";

// The live Fix strip against a mocked /api/fixes/active. What matters
// here is what an operator can read off it: which phase, for how long,
// and whether an idle daemon shows a panel at all.

const fetchActiveFixes = vi.fn<() => Promise<ActiveFix[]>>();

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return { ...actual, fetchActiveFixes: () => fetchActiveFixes() };
});

async function renderStrip(props: { showEmpty?: boolean } = {}) {
  const { FixesInFlight } = await import("./fixes-in-flight");
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <FixesInFlight {...props} />
    </QueryClientProvider>,
  );
}

describe("components/fixes-in-flight", () => {
  beforeEach(() => {
    fetchActiveFixes.mockReset();
  });

  it("shows the repo, phase, provider and how long it has been there", async () => {
    fetchActiveFixes.mockResolvedValue([
      {
        id: "run-1",
        session_id: "20260909T101500Z-svc-a",
        repo: "svc-a",
        source: "ci",
        provider: "claude",
        state: "fixing",
        since: new Date(Date.now() - 120_000).toISOString(),
        attempt: 2,
      },
    ]);
    await renderStrip();

    await waitFor(() => {
      expect(screen.getByText("svc-a")).toBeInTheDocument();
    });
    expect(screen.getByText("fixing")).toBeInTheDocument();
    expect(screen.getByText(/claude/)).toBeInTheDocument();
    // A retry is worth seeing while it climbs, not only in the audit row.
    expect(screen.getByText(/attempt 2/)).toBeInTheDocument();
    expect(screen.getByText("2m")).toBeInTheDocument();
    // The session id is the link to the recording once one exists.
    expect(screen.getByText("20260909T101500Z-svc-a")).toBeInTheDocument();
  });

  it("renders nothing on an idle daemon so the overview keeps its shape", async () => {
    fetchActiveFixes.mockResolvedValue([]);
    await renderStrip();

    await waitFor(() => {
      expect(fetchActiveFixes).toHaveBeenCalled();
    });
    expect(screen.queryByText("in flight")).not.toBeInTheDocument();
  });

  it("says so explicitly with showEmpty, where that is the answer", async () => {
    fetchActiveFixes.mockResolvedValue([]);
    await renderStrip({ showEmpty: true });

    await waitFor(() => {
      expect(screen.getByText("No Fix running.")).toBeInTheDocument();
    });
    expect(screen.getByText("idle")).toBeInTheDocument();
  });

  it("stays quiet when the poll fails, since the daemon banner already reports that", async () => {
    fetchActiveFixes.mockRejectedValue(new Error("/api/fixes/active → 503"));
    await renderStrip({ showEmpty: true });

    await waitFor(() => {
      expect(fetchActiveFixes).toHaveBeenCalled();
    });
    expect(screen.queryByText("in flight")).not.toBeInTheDocument();
    expect(screen.queryByText(/503/)).not.toBeInTheDocument();
  });

  it("counts more than one Fix, which is the case worktrees made possible", async () => {
    const since = new Date().toISOString();
    fetchActiveFixes.mockResolvedValue([
      { id: "a", repo: "svc-a", source: "ci", state: "queued", since },
      { id: "b", repo: "svc-b", source: "prometheus", state: "verifying", since },
    ]);
    await renderStrip();

    await waitFor(() => {
      expect(screen.getByText("2 fixes")).toBeInTheDocument();
    });
    expect(screen.getByText("queued")).toBeInTheDocument();
    expect(screen.getByText("verifying")).toBeInTheDocument();
  });
});
