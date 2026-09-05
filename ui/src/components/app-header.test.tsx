import { describe, expect, it } from "vitest";
import { daemonChipLabel } from "./app-header";

describe("daemonChipLabel", () => {
  it("is connecting while the first overview is in flight", () => {
    expect(
      daemonChipLabel({ isPending: true, isError: false, fetchStatus: null }),
    ).toBe("connecting");
  });

  it("is token rejected on 401, not online", () => {
    expect(
      daemonChipLabel({
        isPending: false,
        isError: true,
        daemonStatus: "running",
        fetchStatus: 401,
      }),
    ).toBe("token rejected");
  });

  it("is online only after a successful running overview", () => {
    expect(
      daemonChipLabel({
        isPending: false,
        isError: false,
        daemonStatus: "running",
        fetchStatus: null,
      }),
    ).toBe("online");
  });

  it("is stopped on other fetch errors", () => {
    expect(
      daemonChipLabel({ isPending: false, isError: true, fetchStatus: 503 }),
    ).toBe("stopped");
  });
});
