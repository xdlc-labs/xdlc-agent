import { act, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import { liveFixes } from "@/lib/live-events";

describe("routes/fixes live store + card", () => {
  beforeEach(() => {
    liveFixes.reset();
  });

  it("appends output chunks and replaces on snapshot", () => {
    liveFixes.onState({
      id: "run-1",
      repo: "svc-a",
      source: "ci",
      provider: "claude",
      state: "fixing",
      since: new Date().toISOString(),
    });
    liveFixes.onOutput({ id: "run-1", text: "▸ Bash go test ./...\n" });
    liveFixes.onOutput({ id: "run-1", text: "The test expects 3.\n" });
    expect(liveFixes.getSnapshot()[0]?.output).toBe("▸ Bash go test ./...\nThe test expects 3.\n");

    liveFixes.onOutput({ id: "run-1", text: "whole tail\n", snapshot: true });
    expect(liveFixes.getSnapshot()[0]?.output).toBe("whole tail\n");
  });

  it("keeps a finished Fix on the grid, after the running ones", () => {
    const since = new Date().toISOString();
    liveFixes.onState({ id: "a", repo: "svc-a", source: "ci", state: "fixing", since });
    liveFixes.onState({ id: "b", repo: "svc-b", source: "ci", state: "cloning", since });
    liveFixes.onState({ id: "a", repo: "svc-a", source: "ci", state: "ok", since });
    const ids = liveFixes.getSnapshot().map((f) => f.fix.id);
    expect(ids).toEqual(["b", "a"]);
    expect(liveFixes.getSnapshot()[1]?.endedAt).toBeDefined();
  });

  it("makes a card for output whose state event was missed", () => {
    liveFixes.onOutput({ id: "ghost", repo: "svc-g", text: "hello\n" });
    const [f] = liveFixes.getSnapshot();
    expect(f?.fix.repo).toBe("svc-g");
    expect(f?.output).toBe("hello\n");
  });

  it("renders a card with repo, state and the tail", async () => {
    const { FixCard } = await import("@/components/fix-card");
    const live = {
      fix: {
        id: "run-1",
        repo: "svc-a",
        source: "ci",
        provider: "cursor",
        state: "fixing" as const,
        since: new Date(Date.now() - 65_000).toISOString(),
        attempt: 2,
        session_id: "20260909T101500Z-svc-a",
      },
      output: "▸ Edit internal/x.go\n■ done, 4 turns, $0.41\n",
    };
    await act(async () => {
      render(
        <ul>
          <FixCard live={live} />
        </ul>,
      );
    });
    expect(screen.getByText("svc-a")).toBeInTheDocument();
    expect(screen.getByText("fixing")).toBeInTheDocument();
    expect(screen.getByText(/attempt 2/)).toBeInTheDocument();
    expect(screen.getByText(/■ done, 4 turns/)).toBeInTheDocument();
    expect(screen.getByText("20260909T101500Z-svc-a")).toBeInTheDocument();
  });

  it("says the agent has not started while cloning", async () => {
    const { FixCard } = await import("@/components/fix-card");
    await act(async () => {
      render(
        <ul>
          <FixCard
            live={{
              fix: { id: "r", repo: "svc", source: "ci", state: "cloning", since: new Date().toISOString() },
              output: "",
            }}
          />
        </ul>,
      );
    });
    expect(screen.getByText(/waiting for the agent to start/)).toBeInTheDocument();
  });
});
