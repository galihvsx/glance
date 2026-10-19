import { describe, expect, it } from "vitest";
import { formatStateDuration } from "./timeState";

describe("formatStateDuration", () => {
  it("renders 0 seconds as <1m", () => {
    expect(formatStateDuration(0)).toBe("<1m");
  });
  it("clamps negative input to <1m", () => {
    expect(formatStateDuration(-42)).toBe("<1m");
  });
  it("renders 45s in the 1-59s band", () => {
    expect(formatStateDuration(45)).toBe("45s");
  });
  it("renders 59s at the top of the seconds band", () => {
    expect(formatStateDuration(59)).toBe("59s");
  });
  it("renders 1m at 60s", () => {
    expect(formatStateDuration(60)).toBe("1m");
  });
  it("renders 54m in the minutes band", () => {
    expect(formatStateDuration(54 * 60)).toBe("54m");
  });
  it("renders 59m at the top of the minutes band", () => {
    expect(formatStateDuration(3599)).toBe("59m");
  });
  it("renders 2h 15m in the hours band", () => {
    expect(formatStateDuration(2 * 3600 + 15 * 60)).toBe("2h 15m");
  });
  it("renders 23h 59m at the top of the hours band", () => {
    expect(formatStateDuration(86399)).toBe("23h 59m");
  });
  it("renders 1d 0h at 86400s", () => {
    expect(formatStateDuration(86400)).toBe("1d 0h");
  });
  it("renders 3d 4h in the days band", () => {
    expect(formatStateDuration(3 * 86400 + 4 * 3600)).toBe("3d 4h");
  });
});
