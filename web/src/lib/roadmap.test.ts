import { describe, expect, it } from "vitest";
import {
  bucketIssues,
  currentMonthStart,
  monthOf,
  roadmapBucket,
  roadmapMonths,
} from "./roadmap";

interface T {
  id: string;
  sequence_id: number;
  target_date?: string | null;
}

const issue = (id: string, seq: number, target_date?: string | null): T => ({
  id,
  sequence_id: seq,
  target_date,
});

describe("roadmapMonths", () => {
  it("generates count months starting at the given month", () => {
    const ms = roadmapMonths("2026-10-01", 3);
    expect(ms.map((m) => m.month)).toEqual([
      "2026-10-01",
      "2026-11-01",
      "2026-12-01",
    ]);
  });

  it("rolls over year boundaries", () => {
    const ms = roadmapMonths("2026-11-01", 3);
    expect(ms.map((m) => m.month)).toEqual([
      "2026-11-01",
      "2026-12-01",
      "2027-01-01",
    ]);
  });

  it("labels months in the locale's long form", () => {
    const ms = roadmapMonths("2026-10-01", 1);
    expect(ms[0].label).toContain("2026");
  });
});

describe("monthOf", () => {
  it("maps a day to its month's first day", () => {
    expect(monthOf("2026-10-15")).toBe("2026-10-01");
  });

  it("slices the RFC3339 backend form", () => {
    expect(monthOf("2026-10-15T00:00:00Z")).toBe("2026-10-01");
  });
});

describe("roadmapBucket", () => {
  it("buckets by target_date", () => {
    expect(roadmapBucket("2026-11-20")).toBe("2026-11-01");
  });

  it("slices the RFC3339 backend form", () => {
    expect(roadmapBucket("2026-11-20T00:00:00Z")).toBe("2026-11-01");
  });

  it("returns null when there is no target date", () => {
    expect(roadmapBucket(null)).toBeNull();
    expect(roadmapBucket(undefined)).toBeNull();
    expect(roadmapBucket("")).toBeNull();
  });
});

describe("currentMonthStart", () => {
  it("returns the first day of the current month as YYYY-MM-DD", () => {
    const s = currentMonthStart();
    expect(s).toMatch(/^\d{4}-\d{2}-01$/);
    const now = new Date();
    expect(s).toBe(
      `${now.getUTCFullYear()}-${String(now.getUTCMonth() + 1).padStart(2, "0")}-01`,
    );
  });
});

describe("bucketIssues", () => {
  const buckets = roadmapMonths("2026-10-01", 3);

  it("places dated issues in the right month columns", () => {
    const { columns, unscheduled } = bucketIssues(
      [issue("a", 1, "2026-10-05"), issue("b", 2, "2026-12-25")],
      buckets,
    );
    expect(columns[0].issues.map((i) => i.id)).toEqual(["a"]);
    expect(columns[1].issues).toEqual([]);
    expect(columns[2].issues.map((i) => i.id)).toEqual(["b"]);
    expect(unscheduled).toEqual([]);
  });

  it("sends issues without a target date to the unscheduled lane", () => {
    const { unscheduled } = bucketIssues(
      [issue("a", 1, null), issue("b", 2, undefined), issue("c", 3, "")],
      buckets,
    );
    expect(unscheduled.map((i) => i.id)).toEqual(["a", "b", "c"]);
  });

  it("drops dated issues outside the window (not in unscheduled)", () => {
    const { columns, unscheduled } = bucketIssues(
      [issue("a", 1, "2025-01-01"), issue("b", 2, "2030-06-15")],
      buckets,
    );
    expect(columns.every((c) => c.issues.length === 0)).toBe(true);
    expect(unscheduled).toEqual([]);
  });

  it("sorts within a column by target_date then sequence_id", () => {
    const { columns } = bucketIssues(
      [
        issue("b", 5, "2026-10-20"),
        issue("a", 9, "2026-10-05"),
        issue("c", 1, "2026-10-20"),
      ],
      buckets,
    );
    expect(columns[0].issues.map((i) => i.id)).toEqual(["a", "c", "b"]);
  });

  it("sorts unscheduled by sequence_id", () => {
    const { unscheduled } = bucketIssues(
      [issue("b", 9, null), issue("a", 1, null)],
      buckets,
    );
    expect(unscheduled.map((i) => i.id)).toEqual(["a", "b"]);
  });
});
