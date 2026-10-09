import { describe, expect, it } from "vitest";
import { formatDayLabel, layoutBurndown } from "./burndown";
import type { BurndownDay } from "./types";

const days: BurndownDay[] = [
  { date: "2026-10-05", remaining: 4, ideal: 4 },
  { date: "2026-10-06", remaining: 3, ideal: 3 },
  { date: "2026-10-07", remaining: 3, ideal: 2 },
  { date: "2026-10-08", remaining: 1, ideal: 1 },
  { date: "2026-10-09", remaining: null, ideal: 0 }, // future
];

describe("formatDayLabel", () => {
  it("formats YYYY-MM-DD without timezone shifts", () => {
    expect(formatDayLabel("2026-10-09")).toBe("Oct 9");
    expect(formatDayLabel("2026-01-01")).toBe("Jan 1");
  });
  it("passes through garbage", () => {
    expect(formatDayLabel("nope")).toBe("nope");
  });
});

describe("layoutBurndown", () => {
  it("maps points inside the 640x280 viewBox", () => {
    const l = layoutBurndown(days, 4);
    expect(l.width).toBe(640);
    expect(l.height).toBe(280);
    for (const p of [...l.ideal, ...l.actual]) {
      expect(p.x).toBeGreaterThanOrEqual(36);
      expect(p.x).toBeLessThanOrEqual(628);
      expect(p.y).toBeGreaterThanOrEqual(12);
      expect(p.y).toBeLessThanOrEqual(250);
    }
  });

  it("draws the ideal line across all days, actual only where data exists", () => {
    const l = layoutBurndown(days, 4);
    expect(l.ideal).toHaveLength(5);
    expect(l.actual).toHaveLength(4); // future day excluded
    // ideal descends 4 -> 0, so later points are lower (larger y)
    expect(l.ideal[0].y).toBeLessThan(l.ideal[4].y);
    // remaining 4 -> 1 also descends
    expect(l.actual[0].y).toBeLessThan(l.actual[3].y);
  });

  it("handles a single-day cycle without dividing by zero", () => {
    const l = layoutBurndown(
      [{ date: "2026-10-09", remaining: 2, ideal: 2 }],
      2,
    );
    expect(l.ideal).toHaveLength(1);
    expect(l.actual).toHaveLength(1);
    expect(Number.isFinite(l.ideal[0].x)).toBe(true);
  });

  it("handles empty scope", () => {
    const l = layoutBurndown([], 0);
    expect(l.ideal).toHaveLength(0);
    expect(l.actual).toHaveLength(0);
    expect(l.yMax).toBe(1);
  });

  it("thins x ticks and keeps integer y ticks", () => {
    const many: BurndownDay[] = Array.from({ length: 30 }, (_, i) => ({
      date: `2026-10-${String(i + 1).padStart(2, "0")}`,
      remaining: 30 - i,
      ideal: 30 - i,
    }));
    const l = layoutBurndown(many, 30);
    expect(l.xTicks.length).toBeLessThanOrEqual(7);
    expect(l.xTicks[0].label).toBe("Oct 1");
    expect(l.yTicks.length).toBeLessThanOrEqual(7);
    for (const t of l.yTicks) expect(Number.isInteger(Number(t.label))).toBe(true);
  });
});
