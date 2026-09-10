import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { SessionDetail, SessionFile } from "@/lib/api";

const fetchSession = vi.fn<(id: string) => Promise<SessionDetail>>();
const fetchSessionText = vi.fn<(id: string, file: SessionFile, attempt?: number) => Promise<string>>();

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    fetchSession: (id: string) => fetchSession(id),
    fetchSessionText: (id: string, file: SessionFile, attempt?: number) => fetchSessionText(id, file, attempt),
  };
});

const detail: SessionDetail = {
  session: {
    id: "20260909T101500Z-svc",
    repo: "svc",
    source: "ci",
    kind: "fail",
    provider: "claude",
    started_at: "2026-09-09T10:15:00Z",
    status: "ok",
    outcome: "fixed",
    summary: "Corrected the off-by-one in paginate().",
    attempts: 1,
    duration_ms: 123_000,
    changed_files: 2,
    cost: { total_cost_usd: 1.63 },
  },
  files: ["diff.patch", "meta.json", "output.txt", "prompt.txt"],
  output_tail: "…\nxdlc_outcome: fixed",
};

async function renderPanel(id = detail.session.id) {
  const { SessionPanel } = await import("./session-panel");
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <SessionPanel id={id} />
    </QueryClientProvider>,
  );
}

describe("components/session-panel", () => {
  beforeEach(() => {
    fetchSession.mockReset();
    fetchSessionText.mockReset();
  });

  it("shows the recording's verdict, cost and output tail", async () => {
    fetchSession.mockResolvedValue(detail);
    await renderPanel();

    await waitFor(() => {
      expect(screen.getByText("fixed")).toBeInTheDocument();
    });
    expect(screen.getByText("$1.63")).toBeInTheDocument();
    expect(screen.getByText("123s")).toBeInTheDocument();
    expect(screen.getByText("2 files")).toBeInTheDocument();
    expect(screen.getByText(/Corrected the off-by-one/)).toBeInTheDocument();
    expect(screen.getByText(/xdlc_outcome: fixed/)).toBeInTheDocument();
    expect(fetchSessionText).not.toHaveBeenCalled();
  });

  it("loads the diff on demand and colours added and removed lines", async () => {
    fetchSession.mockResolvedValue(detail);
    fetchSessionText.mockResolvedValue("--- a/x\n+++ b/x\n@@ -1 +1 @@\n-old\n+new\n");
    await renderPanel();
    await waitFor(() => expect(screen.getByRole("button", { name: "diff" })).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: "diff" }));

    await waitFor(() => expect(screen.getByText("+new")).toBeInTheDocument());
    expect(fetchSessionText).toHaveBeenCalledWith(detail.session.id, "diff", 1);
    expect(screen.getByText("+new").className).toContain("text-pass");
    expect(screen.getByText("-old").className).toContain("text-breach");
  });

  it("says a run changed nothing when the diff is 404", async () => {
    fetchSession.mockResolvedValue({ ...detail, files: ["meta.json", "output.txt", "prompt.txt"] });
    fetchSessionText.mockRejectedValue(new Error("/api/sessions/x/diff → 404"));
    await renderPanel();
    await waitFor(() => expect(screen.getByRole("button", { name: "diff" })).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: "diff" }));

    await waitFor(() => expect(screen.getByText("This run delivered no patch.")).toBeInTheDocument());
  });

  it("tells a viewer the recording needs an operator token", async () => {
    fetchSession.mockRejectedValue(new Error("/api/sessions/x → 403"));
    await renderPanel();

    await waitFor(() => expect(screen.getByText(/operator-only/)).toBeInTheDocument());
  });
});
