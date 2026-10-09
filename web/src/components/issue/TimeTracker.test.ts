import { describe, expect, it } from "vitest";
import { formatDuration } from "./TimeTracker";

describe("formatDuration", () => {
  it("formats seconds", () => {
    expect(formatDuration(0)).toBe("0s");
    expect(formatDuration(30)).toBe("30s");
    expect(formatDuration(59)).toBe("59s");
  });
  it("formats minutes", () => {
    expect(formatDuration(60)).toBe("1m");
    expect(formatDuration(90)).toBe("1m");
    expect(formatDuration(3599)).toBe("59m");
  });
  it("formats hours", () => {
    expect(formatDuration(3600)).toBe("1h 0m");
    expect(formatDuration(5400)).toBe("1h 30m");
    expect(formatDuration(7384)).toBe("2h 3m");
  });
  it("clamps negatives and floors fractions", () => {
    expect(formatDuration(-5)).toBe("0s");
    expect(formatDuration(90.9)).toBe("1m");
  });
});
