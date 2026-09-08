import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Overview } from "@/lib/api";

// Route smoke test: renders the /settings route component end to end
// against a mocked overview. Guards the thing issue #29 was about — the
// browser-local Manual Fix override and the read-only daemon default
// provider must read as two separate settings, and disagreeing must not
// look like a failed save.

const overview: Overview = {
  daemon: {
    status: "running",
    version: "v1.0.0",
    env: "dev",
    uptime: "1h",
    webhook: "ok",
    configPath: "/etc/xdlc/config.yaml",
    gitopsDir: "gitops",
    agentProvider: "claude",
  },
  pipeline: [],
  kpis: { reposWatched: 1, fixes: 0, promotes: 0, reverts: 0, lastActionAt: "—", backlogOpen: 0 },
  gates: [],
  repos: [],
  events: [],
  backlogMd: "",
};

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    fetchOverview: vi.fn(async () => overview),
  };
});

async function renderSettings() {
  const { Route } = await import("./settings");
  const Settings = Route.options.component!;
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <Settings />
    </QueryClientProvider>,
  );
}

describe("routes/settings", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("renders the page header", async () => {
    await renderSettings();
    expect(screen.getByRole("heading", { name: "settings" })).toBeInTheDocument();
  });

  it("separates the browser-local override from the daemon default", async () => {
    await renderSettings();

    expect(
      screen.getByRole("heading", { name: /two separate settings/i }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: /this browser — manual fix override/i }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: /daemon default provider/i }),
    ).toBeInTheDocument();
    expect(screen.getByText("browser-local")).toBeInTheDocument();
    expect(screen.getByText("read-only here")).toBeInTheDocument();
  });

  it("shows the daemon default as read-only with edit instructions", async () => {
    await renderSettings();

    // One provider control on the page: the browser-local override.
    // The daemon default is a value, not an input.
    expect(screen.getAllByRole("combobox")).toHaveLength(1);

    await waitFor(() => {
      expect(screen.getByText("claude", { selector: "span" })).toBeInTheDocument();
    });
    expect(screen.getByText(/edit that file and restart the daemon/i)).toBeInTheDocument();
    // Named in the daemon-default card and again in the daemon panel below.
    expect(screen.getAllByText("/etc/xdlc/config.yaml").length).toBeGreaterThan(0);
  });

  it("explains a browser override that differs from the daemon default", async () => {
    await renderSettings();
    await waitFor(() => {
      expect(screen.getByRole("option", { name: /cursor/i })).toBeInTheDocument();
    });

    expect(screen.queryByRole("note")).not.toBeInTheDocument();

    const user = userEvent.setup();
    await user.selectOptions(
      screen.getByLabelText(/coding agent for manual fix from this browser/i),
      "cursor",
    );
    await user.click(
      screen.getByRole("button", { name: "Save coding agent for this browser" }),
    );

    const note = await screen.findByRole("note");
    expect(within(note).getByText(/expected — not a failed save/i)).toBeInTheDocument();
    expect(within(note).getByText("cursor")).toBeInTheDocument();
    // Calm, not an error.
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(localStorage.getItem("xdlc_agent_provider")).toBe("cursor");
  });

  it("drops the note again when the override is cleared", async () => {
    localStorage.setItem("xdlc_agent_provider", "cursor");
    await renderSettings();

    expect(await screen.findByRole("note")).toBeInTheDocument();

    const user = userEvent.setup();
    await user.click(
      screen.getByRole("button", { name: "Clear coding agent for this browser" }),
    );

    expect(screen.queryByRole("note")).not.toBeInTheDocument();
    expect(localStorage.getItem("xdlc_agent_provider")).toBeNull();
  });
});
