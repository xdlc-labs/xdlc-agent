import { describe, expect, it } from "vitest";
import { formatAge } from "./utils";

describe("formatAge", () => {
  const now = Date.parse("2026-09-05T12:00:00Z");

  it("formats relative ages from audit at strings", () => {
    expect(formatAge("2026-09-05 11:59:30Z", now)).toBe("30s");
    expect(formatAge("2026-09-05 11:45:00Z", now)).toBe("15m");
    expect(formatAge("2026-09-05 10:00:00Z", now)).toBe("2h");
    expect(formatAge("2026-09-01 12:00:00Z", now)).toBe("4d");
  });
});
