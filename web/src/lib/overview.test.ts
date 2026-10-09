import { describe, expect, it } from "vitest";
import {
  completionStats,
  cyclePercent,
  priorityBreakdown,
  recentActivity,
  topContributors,
} from "./overview";
import type { ActivityEntry } from "./activity";

const LABELS = ["None", "Low", "Medium", "High", "Urgent"] as const;

const STATES = [
  { id: "s-backlog", name: "Backlog", group: "backlog", color: "#6b7280" },
  { id: "s-todo", name: "Todo", group: "unstarted", color: "#3b82f6" },
  { id: "s-doing", name: "In Progress", group: "started", color: "#f59e0b" },
  { id: "s-done", name: "Done", group: "completed", color: "#22c55e" },
  { id: "s-cancel", name: "Cancelled", group: "cancelled", color: "#ef4444" },
];

function entry(partial: Partial<ActivityEntry> & { actor: string }): ActivityEntry {
  return {
    at: "2026-10-09T10:00:00Z",
    issue_uuid: "i-1",
    issue_identifier: "ENG-1",
    field: "priority",
    old: null,
    new: null,
    ...partial,
  };
}

describe("completionStats", () => {
  it("splits done/open via state groups and rounds the percent", () => {
    const s = completionStats(
      { "s-todo": 4, "s-doing": 3, "s-done": 2, "s-cancel": 1 },
      STATES,
    );
    expect(s.total).toBe(10);
    expect(s.done).toBe(3);
    expect(s.open).toBe(7);
    expect(s.percent).toBe(30);
  });

  it("treats unknown state ids as open, never drops them", () => {
    const s = completionStats({ "s-todo": 1, "nope": 2 }, STATES);
    expect(s.total).toBe(3);
    expect(s.done).toBe(0);
    expect(s.open).toBe(3);
  });

  it("returns 0% on an empty project instead of NaN", () => {
    const s = completionStats({}, STATES);
    expect(s).toEqual({ total: 0, done: 0, open: 0, percent: 0 });
  });
});

describe("priorityBreakdown", () => {
  it("maps every bucket, defaulting missing ones to zero", () => {
    const rows = priorityBreakdown({ "4": 2, "2": 5 }, LABELS);
    expect(rows).toHaveLength(5);
    expect(rows[4]).toEqual({ priority: 4, label: "Urgent", count: 2 });
    expect(rows[0]).toEqual({ priority: 0, label: "None", count: 0 });
    expect(rows[2]).toEqual({ priority: 2, label: "Medium", count: 5 });
  });
});

describe("topContributors", () => {
  it("ranks by entry count, ties alphabetical, respects the limit", () => {
    const entries = [
      entry({ actor: "Zoe" }),
      entry({ actor: "amy" }),
      entry({ actor: "Zoe" }),
      entry({ actor: "ben" }),
      entry({ actor: "amy" }),
      entry({ actor: "amy" }),
    ];
    const top = topContributors(entries, 2);
    expect(top).toEqual([
      { actor: "amy", changes: 3 },
      { actor: "Zoe", changes: 2 },
    ]);
  });

  it("breaks exact ties alphabetically for determinism", () => {
    const top = topContributors([entry({ actor: "zed" }), entry({ actor: "ann" })], 5);
    expect(top.map((c) => c.actor)).toEqual(["ann", "zed"]);
  });

  it("returns an empty list for no activity", () => {
    expect(topContributors([], 5)).toEqual([]);
  });
});

describe("cyclePercent", () => {
  it("computes the completed share", () => {
    expect(
      cyclePercent({ id: "c", name: "S1", status: "current", completed: 1, total: 4 }),
    ).toBe(25);
  });

  it("is 0 when there is no cycle or no issues", () => {
    expect(cyclePercent(null)).toBe(0);
    expect(cyclePercent({ id: "c", name: "S1", status: "current", completed: 0, total: 0 })).toBe(0);
  });
});

describe("recentActivity", () => {
  it("slices the newest entries", () => {
    const entries = [entry({ actor: "a" }), entry({ actor: "b" }), entry({ actor: "c" })];
    expect(recentActivity(entries, 2).map((e) => e.actor)).toEqual(["a", "b"]);
  });
});
