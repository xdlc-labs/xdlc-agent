import { render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import type { Overview } from "@/lib/api";

// Route smoke test: renders the /actions route component end to end
// (data loading, manual-action controls, fix-PR queue, policy table)
// against mocked API + auth modules, without needing a full router.

const overview: Overview = {
  daemon: {
    status: "running",
    version: "v1.0.0",
    env: "dev",
    uptime: "1h",
    webhook: "ok",
    configPath: "config.yaml",
    gitopsDir: "gitops",
    agentProvider: "claude",
  },
  pipeline: [],
  kpis: { reposWatched: 1, fixes: 0, promotes: 0, reverts: 0, lastActionAt: "—", backlogOpen: 0 },
  gates: [],
  repos: [
    {
      id: "svc-a",
      name: "svc-a",
      branch: "main",
      lastGate: "CI",
      lastGateStatus: "pass",
      lastAction: "None",
      lastActionAt: "",
      devTag: "v1",
      prodTag: "v1",
      health: "healthy",
      cloneStatus: "clean",
      lastPromote: "—",
      lastRevert: "—",
      argocdApp: "svc-a",
      sloQueries: [],
    },
  ],
  events: [],
  backlogMd: "",
};

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    fetchOverview: vi.fn(async () => overview),
    fetchFixPRs: vi.fn(async () => []),
    postAction: vi.fn(async () => ({ ok: true, message: "done", status: 200 })),
  };
});

vi.mock("@/lib/auth", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/auth")>();
  return {
    ...actual,
    fetchRole: vi.fn(async () => "operator" as const),
  };
});

async function renderActions() {
  const { Route } = await import("./actions");
  const Actions = Route.options.component!;
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <Actions />
    </QueryClientProvider>,
  );
}

describe("routes/actions", () => {
  it("renders without crashing and shows the page header", async () => {
    await renderActions();
    expect(screen.getByRole("heading", { name: "actions" })).toBeInTheDocument();
  });

  it("shows manual action controls once repo data has loaded", async () => {
    await renderActions();

    await waitFor(() => {
      expect(screen.getByRole("option", { name: "svc-a" })).toBeInTheDocument();
    });

    expect(screen.getByRole("button", { name: "Run Manual Fix" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Run Manual Promote" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Run Manual Revert" })).toBeInTheDocument();
  });

  it("shows the policy table with known signal rows", async () => {
    await renderActions();
    expect(await screen.findByText("CI fail")).toBeInTheDocument();
    expect(screen.getByText("PROD p95 breach")).toBeInTheDocument();
  });

  it("opens the confirm dialog for a manual action", async () => {
    await renderActions();
    await waitFor(() => {
      expect(screen.getByRole("option", { name: "svc-a" })).toBeInTheDocument();
    });

    screen.getByRole("button", { name: "Run Manual Promote" }).click();

    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText("Manual Promote")).toBeInTheDocument();
  });
});
