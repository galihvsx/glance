import { describe, expect, it } from "vitest";
import {
  addDays,
  barSpan,
  daysBetween,
  dayToTs,
  dayToX,
  ganttDays,
  ganttMonths,
  tsToDay,
  xToDay,
} from "./gantt";

describe("gantt date utils", () => {
  it("round-trips day <-> timestamp", () => {
    expect(tsToDay(dayToTs("2026-10-09"))).toBe("2026-10-09");
  });

  it("daysBetween counts whole days", () => {
    expect(daysBetween("2026-10-01", "2026-10-01")).toBe(0);
    expect(daysBetween("2026-10-01", "2026-10-05")).toBe(4);
    expect(daysBetween("2026-10-05", "2026-10-01")).toBe(-4);
  });

  it("addDays crosses month boundaries", () => {
    expect(addDays("2026-10-31", 1)).toBe("2026-11-01");
    expect(addDays("2026-10-01", -1)).toBe("2026-09-30");
  });

  it("ganttDays covers three months", () => {
    const days = ganttDays(2026, 9); // October (0-indexed 9)
    expect(days[0]).toBe("2026-09-01");
    expect(days[days.length - 1]).toBe("2026-11-30");
    expect(days.length).toBe(30 + 31 + 30);
  });

  it("ganttMonths groups by month", () => {
    const months = ganttMonths(ganttDays(2026, 9));
    expect(months.map((m) => m.day)).toEqual([
      "2026-09-01",
      "2026-10-01",
      "2026-11-01",
    ]);
    expect(months.map((m) => m.span)).toEqual([30, 31, 30]);
  });

  it("barSpan spans start→target", () => {
    const b = barSpan("2026-10-05T00:00:00Z", "2026-10-08T00:00:00Z");
    expect(b).toEqual({ start: "2026-10-05", end: "2026-10-08", duration: 4 });
  });

  it("barSpan defaults to a 1-day bar for a single date", () => {
    expect(barSpan("2026-10-05T00:00:00Z", null)).toEqual({
      start: "2026-10-05",
      end: "2026-10-05",
      duration: 1,
    });
    expect(barSpan(null, "2026-10-08T00:00:00Z")).toEqual({
      start: "2026-10-08",
      end: "2026-10-08",
      duration: 1,
    });
  });

  it("barSpan is null for undated issues", () => {
    expect(barSpan(null, null)).toEqual({ start: null, end: null, duration: 0 });
    expect(barSpan(undefined, "")).toEqual({
      start: null,
      end: null,
      duration: 0,
    });
  });

  it("barSpan clamps inverted ranges", () => {
    const b = barSpan("2026-10-10T00:00:00Z", "2026-10-05T00:00:00Z");
    expect(b.start).toBe("2026-10-05");
    expect(b.end).toBe("2026-10-10");
  });

  it("dayToX/xToDay are inverses and clamp", () => {
    const ws = "2026-09-01";
    expect(dayToX("2026-09-01", ws, 28)).toBe(0);
    expect(dayToX("2026-09-03", ws, 28)).toBe(56);
    expect(xToDay(56, ws, 28, 91)).toBe("2026-09-03");
    expect(xToDay(-100, ws, 28, 91)).toBe("2026-09-01");
    expect(xToDay(99999, ws, 28, 91)).toBe("2026-11-30");
  });
});
