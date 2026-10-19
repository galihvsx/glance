// C72T1: unit tests for the board's Group-by-Epic pure helpers
// (boardEpic.ts). Covers the coordinator's spec rulings: column grouping,
// multi-appearance, archived exclusion, Ungrouped-last/hidden-when-empty,
// drop→attach (with its two no-ops), quick-add→attach, drop-target
// parsing, and the unique-issue PageHeader count.
import { describe, expect, it } from "vitest";
import {
  EPIC_CARD_PREFIX,
  EPIC_COLUMN_PREFIX,
  UNGROUPED_COLUMN_KEY,
  buildEpicBoardGroups,
  epicCardDragId,
  epicDragIssueId,
  epicDropColumnKey,
  planEpicDrop,
  planEpicQuickAdd,
  uniqueIssueCount,
} from "./boardEpic";
import type { Issue } from "../../lib/types";

function mkIssue(
  id: string,
  epics: { id: string; name: string; archived_at?: string | null }[],
  stateId = "s-unstarted",
): Issue {
  return {
    id,
    project_id: "proj-1",
    sequence_id: 1,
    display_id: `ENG-${id}`,
    name: `issue ${id}`,
    priority: 0,
    state_id: stateId,
    sort_order: 0,
    is_draft: false,
    created_by: "u",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    assignees: [],
    labels: [],
    epics,
  } as Issue;
}

const STATE_GROUPS: Record<string, string> = {
  "s-backlog": "backlog",
  "s-unstarted": "unstarted",
  "s-started": "started",
  "s-completed": "completed",
  "s-cancelled": "cancelled",
};
const groupOf = (i: Issue) => STATE_GROUPS[i.state_id];

const EPIC_A = { id: "epic-a", name: "Alpha" };
const EPIC_B = { id: "epic-b", name: "Beta" };
const EPIC_ARCHIVED = {
  id: "epic-old",
  name: "Old",
  archived_at: "2026-05-01T00:00:00Z",
};

describe("buildEpicBoardGroups", () => {
  it("builds one column per live epic, first-appearance order, with counts", () => {
    const issues = [
      mkIssue("1", [EPIC_A]),
      mkIssue("2", [EPIC_B]),
      mkIssue("3", [EPIC_A]),
    ];
    const groups = buildEpicBoardGroups(issues, groupOf, "s-backlog");
    expect(groups.map((g) => g.title)).toEqual(["Alpha", "Beta"]);
    expect(groups[0].droppableId).toBe(`${EPIC_COLUMN_PREFIX}epic-a`);
    expect(groups[0].epicId).toBe("epic-a");
    expect(groups[0].issues.map((i) => i.id)).toEqual(["1", "3"]);
    // Quick-add stamp mirrors the priority-column convention.
    expect(groups[0].newStateId).toBe("s-backlog");
    // Per-state-group counts feed the shared EpicGroupBreakdown.
    expect(groups[0].byGroup).toEqual({ unstarted: 2 });
  });

  it("multi-appearance: an issue with N live epics appears in EVERY epic column", () => {
    const issues = [mkIssue("1", [EPIC_A, EPIC_B])];
    const groups = buildEpicBoardGroups(issues, groupOf);
    expect(groups).toHaveLength(2);
    expect(groups[0].issues.map((i) => i.id)).toEqual(["1"]);
    expect(groups[1].issues.map((i) => i.id)).toEqual(["1"]);
  });

  it("archived epics get no column; their issues fall to Ungrouped", () => {
    const issues = [
      mkIssue("1", [EPIC_ARCHIVED]),
      mkIssue("2", [EPIC_A]),
    ];
    const groups = buildEpicBoardGroups(issues, groupOf);
    expect(groups.map((g) => g.title)).toEqual(["Alpha", "Ungrouped"]);
    const ungrouped = groups.find((g) => g.columnKey === UNGROUPED_COLUMN_KEY)!;
    expect(ungrouped.droppableId).toBe(`${EPIC_COLUMN_PREFIX}ungrouped`);
    expect(ungrouped.epicId).toBeNull();
    expect(ungrouped.issues.map((i) => i.id)).toEqual(["1"]);
  });

  it("Ungrouped is always last and hidden when empty", () => {
    const issues = [mkIssue("1", []), mkIssue("2", [EPIC_A])];
    const withUngrouped = buildEpicBoardGroups(issues, groupOf);
    expect(withUngrouped.map((g) => g.title)).toEqual(["Alpha", "Ungrouped"]);

    const noUngrouped = buildEpicBoardGroups([mkIssue("2", [EPIC_A])], groupOf);
    expect(noUngrouped.map((g) => g.title)).toEqual(["Alpha"]);
    expect(noUngrouped.some((g) => g.columnKey === UNGROUPED_COLUMN_KEY)).toBe(
      false,
    );
  });
});

describe("epicDropColumnKey", () => {
  it("resolves column droppables and compound card ids", () => {
    expect(epicDropColumnKey(`${EPIC_COLUMN_PREFIX}epic-a`)).toBe("epic-a");
    expect(epicDropColumnKey(`${EPIC_COLUMN_PREFIX}ungrouped`)).toBe(
      "ungrouped",
    );
    expect(epicDropColumnKey(epicCardDragId("epic-a", "issue-1"))).toBe(
      "epic-a",
    );
    expect(
      epicDropColumnKey(epicCardDragId(UNGROUPED_COLUMN_KEY, "issue-1")),
    ).toBe("ungrouped");
  });

  it("returns null for non-epic drop targets", () => {
    expect(epicDropColumnKey("state-uuid")).toBeNull();
    expect(epicDropColumnKey("priority:3")).toBeNull();
    expect(epicDropColumnKey("issue-1")).toBeNull();
  });
});

describe("epicDragIssueId", () => {
  it("extracts the issue id from compound epic drag ids", () => {
    expect(epicDragIssueId(epicCardDragId("epic-a", "issue-1"))).toBe("issue-1");
  });

  it("is identity for plain (state/priority mode) ids", () => {
    expect(epicDragIssueId("issue-1")).toBe("issue-1");
  });
});

describe("planEpicDrop", () => {
  it("drop into an epic column plans an attach", () => {
    const plan = planEpicDrop("epic-a", mkIssue("1", [EPIC_B]));
    expect(plan).toEqual({ kind: "attach", epicId: "epic-a" });
  });

  it("drop into Ungrouped is a deliberate no-op (detach-from-all is destructive)", () => {
    const plan = planEpicDrop(UNGROUPED_COLUMN_KEY, mkIssue("1", [EPIC_A]));
    expect(plan).toEqual({ kind: "noop", reason: "ungrouped" });
  });

  it("re-dropping into an already-attached epic is a no-op (idempotent)", () => {
    const plan = planEpicDrop("epic-a", mkIssue("1", [EPIC_A]));
    expect(plan).toEqual({ kind: "noop", reason: "already-attached" });
  });

  it("multi-epic aware: dropping a multi-epic issue into a NEW epic attaches without detaching", () => {
    const plan = planEpicDrop("epic-b", mkIssue("1", [EPIC_A]));
    expect(plan).toEqual({ kind: "attach", epicId: "epic-b" });
  });
});

describe("planEpicQuickAdd", () => {
  it("epic column quick-add creates AND attaches to the epic", () => {
    const plan = planEpicQuickAdd({ newStateId: "s-backlog", epicId: "epic-a" });
    expect(plan).toEqual({ stateId: "s-backlog", attachEpicId: "epic-a" });
  });

  it("Ungrouped quick-add is a plain create (no attach)", () => {
    const plan = planEpicQuickAdd({ newStateId: "s-backlog", epicId: null });
    expect(plan).toEqual({ stateId: "s-backlog", attachEpicId: null });
  });

  it("state/priority columns are unaffected (no attach)", () => {
    const plan = planEpicQuickAdd({ newStateId: "s-1" });
    expect(plan).toEqual({ stateId: "s-1", attachEpicId: null });
  });
});

describe("uniqueIssueCount", () => {
  it("counts unique issues, not column memberships", () => {
    // Same row appearing twice (multi-appearance) counts once.
    const row = mkIssue("1", [EPIC_A]);
    expect(uniqueIssueCount([row, row, mkIssue("2", [EPIC_B])])).toBe(2);
  });
});

describe("epicCardDragId", () => {
  it("prefixes with the epic card marker", () => {
    expect(epicCardDragId("epic-a", "issue-1").startsWith(EPIC_CARD_PREFIX)).toBe(
      true,
    );
  });
});
