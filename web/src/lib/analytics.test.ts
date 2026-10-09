import { describe, expect, it } from "vitest";
import {
  barWidths,
  burndownLine,
  donutSectorPath,
  donutSlices,
  priorityColor,
  resolveCounts,
  shortDate,
  trendBars,
} from "./analytics";
import type { AnalyticsBurndownDay, AnalyticsTrendDay } from "./analytics";

describe("donutSectorPath", () => {
  it("produces a closed annular-sector path", () => {
    const p = donutSectorPath(100, 100, 80, 50, 0, Math.PI);
    expect(p).toMatch(/^M /);
    expect(p).toContain("A 80 80 0 0 1");
    expect(p).toContain("A 50 50 0 0 0");
    expect(p.endsWith("Z")).toBe(true);
  });
  it("uses the large-arc flag for > 180 degrees", () => {
    const p = donutSectorPath(100, 100, 80, 50, 0, Math.PI * 1.5);
    expect(p).toContain("A 80 80 0 1 1");
  });
});

describe("donutSlices", () => {
  it("splits a full circle proportionally", () => {
    const slices = donutSlices(
      [
        { label: "a", value: 1, color: "#111" },
        { label: "b", value: 3, color: "#222" },
      ],
      100,
      100,
      80,
      50,
    );
    expect(slices).toHaveLength(2);
    expect(slices[0].fraction).toBeCloseTo(0.25);
    expect(slices[1].fraction).toBeCloseTo(0.75);
    // fractions tile the circle
    const total = slices.reduce((s, x) => s + x.fraction, 0);
    expect(total).toBeCloseTo(1);
    expect(slices[0].path).toMatch(/^M /);
  });
  it("returns an empty path for a single 100% slice (caller draws a ring)", () => {
    const slices = donutSlices([{ label: "a", value: 5, color: "#111" }], 100, 100, 80, 50);
    expect(slices).toHaveLength(1);
    expect(slices[0].path).toBe("");
    expect(slices[0].fraction).toBe(1);
  });
  it("drops zero values and returns [] for empty input", () => {
    expect(
      donutSlices(
        [{ label: "a", value: 0, color: "#111" }],
        100,
        100,
        80,
        50,
      ),
    ).toEqual([]);
    expect(donutSlices([], 100, 100, 80, 50)).toEqual([]);
  });
});

describe("barWidths", () => {
  it("scales proportionally to the max", () => {
    expect(barWidths([2, 4, 0], 100)).toEqual([50, 100, 0]);
  });
  it("returns zeros when everything is zero", () => {
    expect(barWidths([0, 0], 100)).toEqual([0, 0]);
  });
});

describe("burndownLine", () => {
  const days: AnalyticsBurndownDay[] = [
    { date: "2026-10-05", remaining: 4, remaining_estimate: 8 },
    { date: "2026-10-06", remaining: 3, remaining_estimate: 6 },
    { date: "2026-10-07", remaining: null, remaining_estimate: null }, // future
  ];
  it("skips null (future) days and stays in the viewBox", () => {
    const l = burndownLine(days, (d) => d.remaining, 640, 260);
    expect(l.points).toHaveLength(2);
    expect(l.path.startsWith("M")).toBe(true);
    for (const p of l.points) {
      expect(p.x).toBeGreaterThanOrEqual(36);
      expect(p.x).toBeLessThanOrEqual(640 - 12);
      expect(p.y).toBeGreaterThanOrEqual(12);
      expect(p.y).toBeLessThanOrEqual(260 - 28);
    }
  });
  it("higher values sit higher (smaller y)", () => {
    const l = burndownLine(days, (d) => d.remaining);
    expect(l.points[0].y).toBeLessThan(l.points[1].y);
  });
  it("supports the estimate metric", () => {
    const l = burndownLine(days, (d) => d.remaining_estimate);
    expect(l.yMax).toBe(8);
    expect(l.points[0].value).toBe(8);
  });
  it("degenerates gracefully with a single point", () => {
    const l = burndownLine([days[0]], (d) => d.remaining);
    expect(l.points).toHaveLength(1);
    expect(l.path).toBe("");
  });
});

describe("trendBars", () => {
  const days: AnalyticsTrendDay[] = [
    { date: "2026-10-05", created: 4, closed: 1 },
    { date: "2026-10-06", created: 0, closed: 0 },
  ];
  it("lays out two bars per group with proportional heights", () => {
    const { bars, baseY } = trendBars(days, 640, 260);
    expect(bars).toHaveLength(2);
    expect(bars[0].createdH).toBeGreaterThan(bars[0].closedH);
    expect(bars[0].baseY).toBe(baseY);
    // zero day → zero heights
    expect(bars[1].createdH).toBe(0);
    expect(bars[1].closedH).toBe(0);
    // groups don't overlap
    expect(bars[1].x).toBeGreaterThan(bars[0].x + bars[0].barW * 2);
  });
});

describe("resolveCounts", () => {
  it("maps ids to names/colors and sorts by value desc", () => {
    const rows = resolveCounts(
      { "id-b": 2, "id-a": 5, "id-?": 1 },
      new Map([
        ["id-a", { name: "Todo", color: "#111" }],
        ["id-b", { name: "Done", color: "#222" }],
      ]),
    );
    expect(rows.map((r) => r.label)).toEqual(["Todo", "Done", "id-?"]);
    expect(rows[0].color).toBe("#111");
    expect(rows[2].color).toBe("#9ca3af"); // fallback
  });
});

describe("priorityColor", () => {
  it("returns a hex color for known priorities and falls back", () => {
    expect(priorityColor(4)).toMatch(/^#/);
    expect(priorityColor(99)).toBe("#9ca3af");
  });
});

describe("shortDate", () => {
  it("formats YYYY-MM-DD as Mon D", () => {
    expect(shortDate("2026-10-09")).toBe("Oct 9");
    expect(shortDate("2026-01-01")).toBe("Jan 1");
  });
  it("passes through garbage", () => {
    expect(shortDate("nope")).toBe("nope");
  });
});
