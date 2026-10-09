import { describe, expect, it } from "vitest";
import {
  bucketIssuesByDay,
  dayKey,
  firstOfMonth,
  issueDay,
  monthGrid,
  shiftMonth,
} from "./calendar";
import type { Issue } from "./types";

function mkIssue(over: Partial<Issue> & { id: string }): Issue {
  return {
    project_id: "p",
    sequence_id: 1,
    display_id: "ENG-1",
    name: "test",
    priority: 0,
    state_id: "s",
    sort_order: 0,
    is_draft: false,
    created_by: "u",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    assignees: [],
    labels: [],
    ...over,
  };
}

describe("dayKey", () => {
  it("formats a local date as YYYY-MM-DD", () => {
    expect(dayKey(new Date(2026, 9, 5))).toBe("2026-10-05");
  });
});

describe("issueDay", () => {
  it("prefers target_date, falls back to start_date", () => {
    expect(
      issueDay(mkIssue({ id: "a", target_date: "2026-10-12T00:00:00Z", start_date: "2026-10-05T00:00:00Z" })),
    ).toBe("2026-10-12");
    expect(issueDay(mkIssue({ id: "b", start_date: "2026-10-05T00:00:00Z" }))).toBe(
      "2026-10-05",
    );
    expect(issueDay(mkIssue({ id: "c" }))).toBeNull();
  });

  it("never shifts with viewer timezone (slices the stored DATE)", () => {
    // UTC midnight must stay Oct 5 even for UTC-12 viewers.
    expect(issueDay(mkIssue({ id: "a", target_date: "2026-10-05T00:00:00Z" }))).toBe(
      "2026-10-05",
    );
  });
});

describe("monthGrid", () => {
  it("returns 42 consecutive Monday-first days", () => {
    // Oct 1 2026 is a Thursday -> grid starts Mon Sep 28.
    const cells = monthGrid(2026, 9);
    expect(cells).toHaveLength(42);
    expect(dayKey(cells[0])).toBe("2026-09-28");
    expect(dayKey(cells[41])).toBe("2026-11-08");
    for (let i = 1; i < cells.length; i++) {
      expect(cells[i].getTime() - cells[i - 1].getTime()).toBe(86_400_000);
    }
    expect(cells[0].getDay()).toBe(1); // Monday
  });
});

describe("bucketIssuesByDay", () => {
  const due = mkIssue({ id: "due", sequence_id: 2, target_date: "2026-10-12T00:00:00Z" });
  const startOnly = mkIssue({ id: "start", sequence_id: 1, start_date: "2026-10-05T00:00:00Z" });
  const both = mkIssue({
    id: "both",
    target_date: "2026-11-03T00:00:00Z",
    start_date: "2026-10-05T00:00:00Z",
  });

  it("places due-dated issues by target_date", () => {
    const byDay = bucketIssuesByDay([due], []);
    expect([...byDay.keys()]).toEqual(["2026-10-12"]);
    expect(byDay.get("2026-10-12")!.map((i) => i.id)).toEqual(["due"]);
  });

  it("places start-only issues by start_date", () => {
    const byDay = bucketIssuesByDay([], [startOnly]);
    expect(byDay.get("2026-10-05")!.map((i) => i.id)).toEqual(["start"]);
  });

  it("dedupes: an issue in both sets is placed once, by due date", () => {
    const bothInOct = mkIssue({
      id: "both-oct",
      target_date: "2026-10-20T00:00:00Z",
      start_date: "2026-10-05T00:00:00Z",
    });
    const byDay = bucketIssuesByDay([bothInOct], [bothInOct]);
    expect([...byDay.keys()]).toEqual(["2026-10-20"]);
  });

  it("skips start-dated issues that have a target_date (placed by due instead)", () => {
    // `both` is due in November: it must NOT appear on October's grid.
    const byDay = bucketIssuesByDay([due], [startOnly, both]);
    expect(byDay.get("2026-10-05")!.map((i) => i.id)).toEqual(["start"]);
    expect([...byDay.keys()].sort()).toEqual(["2026-10-05", "2026-10-12"]);
  });

  it("sorts within a day by sequence_id", () => {
    const b = mkIssue({ id: "b", sequence_id: 9, target_date: "2026-10-12T00:00:00Z" });
    const a = mkIssue({ id: "a", sequence_id: 3, target_date: "2026-10-12T00:00:00Z" });
    const byDay = bucketIssuesByDay([b, a], []);
    expect(byDay.get("2026-10-12")!.map((i) => i.id)).toEqual(["a", "b"]);
  });

  it("ignores issues with no usable day", () => {
    const byDay = bucketIssuesByDay([mkIssue({ id: "nodate" })], []);
    expect(byDay.size).toBe(0);
  });
});

describe("firstOfMonth / shiftMonth", () => {
  it("formats and shifts months across year boundaries", () => {
    expect(firstOfMonth(2026, 9)).toBe("2026-10-01");
    expect(shiftMonth(2026, 0, -1)).toEqual({ year: 2025, month: 11 });
    expect(shiftMonth(2026, 11, 1)).toEqual({ year: 2027, month: 0 });
  });
});
