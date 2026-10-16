import { describe, expect, it } from "vitest";
import {
  formatKRValue,
  formatProgressPct,
  objectiveStatusColor,
} from "./okrFormat";

describe("formatKRValue", () => {
  it("strips float noise and trailing zeros", () => {
    expect(formatKRValue(34)).toBe("34");
    expect(formatKRValue(1.5)).toBe("1.5");
    expect(formatKRValue(0.1 + 0.2)).toBe("0.3");
    expect(formatKRValue(100)).toBe("100");
  });

  it("returns 0 for non-finite input", () => {
    expect(formatKRValue(NaN)).toBe("0");
    expect(formatKRValue(Infinity)).toBe("0");
  });
});

describe("formatProgressPct", () => {
  it("formats a 0–1 fraction as a whole percent", () => {
    expect(formatProgressPct(0.62)).toBe("62%");
    expect(formatProgressPct(0)).toBe("0%");
    expect(formatProgressPct(1)).toBe("100%");
  });

  it("clamps out-of-range and non-finite input", () => {
    expect(formatProgressPct(1.5)).toBe("100%");
    expect(formatProgressPct(-0.2)).toBe("0%");
    expect(formatProgressPct(NaN)).toBe("0%");
  });
});

describe("objectiveStatusColor", () => {
  it("matches the initiative status colors", () => {
    expect(objectiveStatusColor("active")).toBe("#4f8ef7");
    expect(objectiveStatusColor("completed")).toBe("#42c88a");
    expect(objectiveStatusColor("archived")).toBe("#777780");
    expect(objectiveStatusColor("unknown")).toBe("#777780");
  });
});
