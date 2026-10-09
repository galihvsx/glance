// C6T8: formatDuration unit tests.
import { describe, expect, it } from "vitest";
import { formatDuration } from "./timeReports";

describe("formatDuration", () => {
  it("renders sub-minute as seconds", () => {
    expect(formatDuration(0)).toBe("0s");
    expect(formatDuration(45)).toBe("45s");
  });
  it("renders sub-hour as minutes", () => {
    expect(formatDuration(60)).toBe("1m");
    expect(formatDuration(3599)).toBe("59m");
  });
  it("renders hours with remainder", () => {
    expect(formatDuration(3600)).toBe("1h");
    expect(formatDuration(3600 + 24 * 60)).toBe("1h 24m");
    expect(formatDuration(3 * 3600)).toBe("3h");
  });
});
