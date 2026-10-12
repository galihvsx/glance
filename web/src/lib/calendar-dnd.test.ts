import { describe, expect, it } from "vitest";
import {
  formatDropDay,
  resolveDropDates,
  resolveScheduleDrop,
  resolveUnscheduleDrop,
} from "./calendar-dnd";
import type { Issue } from "./types";

function chip(
  over: Partial<Pick<Issue, "start_date" | "target_date">>,
): Pick<Issue, "start_date" | "target_date"> {
  return { start_date: null, target_date: null, ...over };
}

function fullIssue(
  over: Partial<Issue> & { id: string },
): Issue {
  return {
    project_id: "p",
    sequence_id: 1,
    display_id: "ENG-123",
    name: "Drag me",
    priority: 0,
    state_id: "s",
    sort_order: 0,
    is_draft: false,
    created_by: "u",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    assignees: [],
    labels: [],
    start_date: null,
    target_date: null,
    ...over,
  };
}

describe("resolveDropDates", () => {
  it("moves a target-placed chip by the day delta", () => {
    const out = resolveDropDates(
      chip({ target_date: "2026-10-10T00:00:00Z" }),
      "2026-10-14",
    );
    expect(out).toEqual({ target_date: "2026-10-14" });
  });

  it("moves a start-placed chip (no due date) by the day delta", () => {
    const out = resolveDropDates(
      chip({ start_date: "2026-10-10T00:00:00Z" }),
      "2026-10-12",
    );
    expect(out).toEqual({ start_date: "2026-10-12" });
  });

  it("shifts both dates by the same delta, preserving duration", () => {
    const out = resolveDropDates(
      chip({
        start_date: "2026-10-10T00:00:00Z",
        target_date: "2026-10-12T00:00:00Z",
      }),
      "2026-10-15",
    );
    expect(out).toEqual({
      start_date: "2026-10-13",
      target_date: "2026-10-15",
    });
  });

  it("returns null for a same-day drop (no-op, no PATCH)", () => {
    expect(
      resolveDropDates(chip({ target_date: "2026-10-14T00:00:00Z" }), "2026-10-14"),
    ).toBeNull();
  });

  it("handles negative deltas (drag backwards)", () => {
    const out = resolveDropDates(
      chip({
        start_date: "2026-10-12T00:00:00Z",
        target_date: "2026-10-14T00:00:00Z",
      }),
      "2026-10-10",
    );
    expect(out).toEqual({
      start_date: "2026-10-08",
      target_date: "2026-10-10",
    });
  });

  it("crosses month boundaries correctly", () => {
    const out = resolveDropDates(
      chip({ target_date: "2026-10-31T00:00:00Z" }),
      "2026-11-02",
    );
    expect(out).toEqual({ target_date: "2026-11-02" });
  });

  it("returns null when the chip has no placing date", () => {
    expect(resolveDropDates(chip({}), "2026-10-14")).toBeNull();
  });

  it("never produces start_date after target_date (server invariant)", () => {
    const out = resolveDropDates(
      chip({
        start_date: "2026-10-01T00:00:00Z",
        target_date: "2026-10-05T00:00:00Z",
      }),
      "2026-11-20",
    );
    expect(out).not.toBeNull();
    expect(out!.start_date! <= out!.target_date!).toBe(true);
  });
});

describe("resolveScheduleDrop", () => {
  it("resolves a drop on a new day into uuid + PATCH body", () => {
    const issue = fullIssue({ id: "i1", target_date: "2026-10-10T00:00:00Z" });
    const out = resolveScheduleDrop(issue, "2026-10-14");
    expect(out?.uuid).toBe("i1");
    expect(out?.dates).toEqual({ target_date: "2026-10-14" });
    expect(out?.announcement).toMatch(/^ENG-123 moved to .*2026$/);
  });

  it("returns null for a same-day drop (no PATCH)", () => {
    const issue = fullIssue({ id: "i1", target_date: "2026-10-14T00:00:00Z" });
    expect(resolveScheduleDrop(issue, "2026-10-14")).toBeNull();
  });

  it("returns null when dropped outside any day cell", () => {
    const issue = fullIssue({ id: "i1", target_date: "2026-10-10T00:00:00Z" });
    expect(resolveScheduleDrop(issue, null)).toBeNull();
  });

  it("announces the drop in the viewer's locale", () => {
    expect(formatDropDay("2026-10-14")).toBe(
      new Date(2026, 9, 14).toLocaleDateString(undefined, {
        month: "short",
        day: "numeric",
        year: "numeric",
      }),
    );
  });

  it("still returns null for a dateless chip without fromStrip", () => {
    const issue = fullIssue({ id: "i1" });
    expect(resolveScheduleDrop(issue, "2026-10-14")).toBeNull();
  });

  it("schedules a dateless strip chip (fromStrip) onto the drop day", () => {
    const issue = fullIssue({ id: "i1" });
    const out = resolveScheduleDrop(issue, "2026-10-14", true);
    expect(out?.uuid).toBe("i1");
    expect(out?.dates).toEqual({ target_date: "2026-10-14" });
    expect(out?.announcement).toMatch(/^ENG-123 scheduled for /);
  });

  it("does not schedule a dateless chip when over is null, even with fromStrip", () => {
    const issue = fullIssue({ id: "i1" });
    expect(resolveScheduleDrop(issue, null, true)).toBeNull();
  });
});

describe("resolveUnscheduleDrop", () => {
  it("clears target_date (null) for a target-placed chip", () => {
    const issue = fullIssue({ id: "i1", target_date: "2026-10-10T00:00:00Z" });
    const out = resolveUnscheduleDrop(issue);
    expect(out?.uuid).toBe("i1");
    expect(out?.dates).toEqual({ target_date: null });
    expect(out?.announcement).toBe("ENG-123 unscheduled");
  });

  it("clears start_date (null) for a start-placed chip", () => {
    const issue = fullIssue({ id: "i1", start_date: "2026-10-10T00:00:00Z" });
    const out = resolveUnscheduleDrop(issue);
    expect(out?.dates).toEqual({ start_date: null });
    expect(out?.announcement).toBe("ENG-123 unscheduled");
  });

  it("prefers clearing target_date when both dates are set (placing rule)", () => {
    const issue = fullIssue({
      id: "i1",
      start_date: "2026-10-08T00:00:00Z",
      target_date: "2026-10-10T00:00:00Z",
    });
    expect(resolveUnscheduleDrop(issue)?.dates).toEqual({
      target_date: null,
    });
  });

  it("returns null when the chip has no placing date (already unscheduled)", () => {
    const issue = fullIssue({ id: "i1" });
    expect(resolveUnscheduleDrop(issue)).toBeNull();
  });
});
