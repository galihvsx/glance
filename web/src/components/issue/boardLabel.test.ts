// C130T1: unit tests for the board's Group-by-Labels pure helpers
// (boardLabel.ts), mirroring boardAssignee.test.ts. Covers the plan's
// rulings: column grouping, multi-appearance, Ungrouped-last/
// hidden-when-empty, compound id parse/round-trip, the drop-plan noop
// matrix, quick-add→attach, and expand-to-full-form carry.
import { describe, expect, it } from "vitest";
import {
  LABEL_CARD_PREFIX,
  LABEL_COLUMN_PREFIX,
  LABEL_UNGROUPED_COLUMN_KEY,
  buildLabelBoardGroups,
  labelCardDragId,
  labelDragColumnKey,
  labelDragIssueId,
  labelDropColumnKey,
  planBoardLabelQuickExpand,
  planLabelDrop,
  planLabelQuickAdd,
} from "./boardLabel";
import type { Issue } from "../../lib/types";

function mkIssue(
  id: string,
  labels: { id: string; name: string; color: string }[],
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
    labels,
    epics: [],
  } as Issue;
}

const BUG = { id: "l-bug", name: "Bug", color: "#e22718" };
const FEAT = { id: "l-feat", name: "Feature", color: "#1c69d4" };

describe("buildLabelBoardGroups", () => {
  it("builds one column per label, first-appearance order, with the state stamp and row color", () => {
    const issues = [
      mkIssue("1", [BUG]),
      mkIssue("2", [FEAT]),
      mkIssue("3", [BUG]),
    ];
    const groups = buildLabelBoardGroups(issues, "s-backlog");
    expect(groups.map((g) => g.title)).toEqual(["Bug", "Feature"]);
    expect(groups[0].droppableId).toBe(`${LABEL_COLUMN_PREFIX}l-bug`);
    expect(groups[0].columnKey).toBe("l-bug");
    expect(groups[0].labelId).toBe("l-bug");
    expect(groups[0].color).toBe("#e22718");
    expect(groups[0].issues.map((i) => i.id)).toEqual(["1", "3"]);
    expect(groups[0].newStateId).toBe("s-backlog");
  });

  it("multi-appearance: an issue with N labels appears in EVERY label column", () => {
    const issues = [mkIssue("1", [BUG, FEAT])];
    const groups = buildLabelBoardGroups(issues);
    expect(groups).toHaveLength(2);
    expect(groups[0].issues.map((i) => i.id)).toEqual(["1"]);
    expect(groups[1].issues.map((i) => i.id)).toEqual(["1"]);
  });

  it("Ungrouped is always last and hidden when empty", () => {
    const issues = [mkIssue("1", []), mkIssue("2", [BUG])];
    const withUngrouped = buildLabelBoardGroups(issues);
    expect(withUngrouped.map((g) => g.title)).toEqual(["Bug", "Ungrouped"]);
    const ungrouped = withUngrouped.find(
      (g) => g.columnKey === LABEL_UNGROUPED_COLUMN_KEY,
    )!;
    expect(ungrouped.droppableId).toBe(`${LABEL_COLUMN_PREFIX}ungrouped`);
    expect(ungrouped.labelId).toBeNull();
    expect(ungrouped.color).toBeUndefined();
    expect(ungrouped.issues.map((i) => i.id)).toEqual(["1"]);

    const noUngrouped = buildLabelBoardGroups([mkIssue("2", [BUG])]);
    expect(noUngrouped.map((g) => g.title)).toEqual(["Bug"]);
    expect(
      noUngrouped.some((g) => g.columnKey === LABEL_UNGROUPED_COLUMN_KEY),
    ).toBe(false);
  });

  it("show-empty-groups synthesizes nothing: no label column without a loaded issue", () => {
    expect(buildLabelBoardGroups([])).toEqual([]);
  });
});

describe("labelDropColumnKey", () => {
  it("resolves column droppables and compound card ids", () => {
    expect(labelDropColumnKey(`${LABEL_COLUMN_PREFIX}l-bug`)).toBe("l-bug");
    expect(labelDropColumnKey(`${LABEL_COLUMN_PREFIX}ungrouped`)).toBe(
      "ungrouped",
    );
    expect(labelDropColumnKey(labelCardDragId("l-bug", "issue-1"))).toBe(
      "l-bug",
    );
    expect(
      labelDropColumnKey(
        labelCardDragId(LABEL_UNGROUPED_COLUMN_KEY, "issue-1"),
      ),
    ).toBe("ungrouped");
  });

  it("returns null for non-label drop targets", () => {
    expect(labelDropColumnKey("state-uuid")).toBeNull();
    expect(labelDropColumnKey("priority:3")).toBeNull();
    expect(labelDropColumnKey("epic:epic-a")).toBeNull();
    expect(labelDropColumnKey("assignee:u-alice")).toBeNull();
    expect(labelDropColumnKey("issue-1")).toBeNull();
  });
});

describe("labelDragIssueId / labelDragColumnKey", () => {
  it("round-trips compound card ids: issue id via last colon, column key via prefix", () => {
    const id = labelCardDragId("l-bug", "issue-1");
    expect(id.startsWith(LABEL_CARD_PREFIX)).toBe(true);
    expect(labelDragIssueId(id)).toBe("issue-1");
    expect(labelDragColumnKey(id)).toBe("l-bug");
  });

  it("is identity / null for plain (state/priority mode) ids", () => {
    expect(labelDragIssueId("issue-1")).toBe("issue-1");
    expect(labelDragColumnKey("issue-1")).toBeNull();
    expect(labelDragColumnKey("assignee-card:u-alice:issue-1")).toBeNull();
  });
});

describe("planLabelDrop", () => {
  it("drop into a label column plans an attach", () => {
    const plan = planLabelDrop("l-bug", mkIssue("1", [FEAT]));
    expect(plan).toEqual({ kind: "attach", labelId: "l-bug" });
  });

  it("drop into Ungrouped is a deliberate no-op (detach-from-all is destructive)", () => {
    const plan = planLabelDrop(LABEL_UNGROUPED_COLUMN_KEY, mkIssue("1", [BUG]));
    expect(plan).toEqual({ kind: "noop", reason: "ungrouped" });
  });

  it("cross-column drop onto an already-labeled label is a no-op (idempotent)", () => {
    const plan = planLabelDrop("l-bug", mkIssue("1", [BUG, FEAT]));
    expect(plan).toEqual({ kind: "noop", reason: "already-labeled" });
  });

  it("multi-label aware: dropping a multi-label issue into a NEW label attaches without detaching", () => {
    const plan = planLabelDrop("l-feat", mkIssue("1", [BUG]));
    expect(plan).toEqual({ kind: "attach", labelId: "l-feat" });
  });

  it("attach-only: the plan never names a detach — no other membership is touched", () => {
    const plan = planLabelDrop("l-feat", mkIssue("1", [BUG]));
    expect(plan.kind).toBe("attach");
    if (plan.kind === "attach") {
      expect(Object.keys(plan).sort()).toEqual(["kind", "labelId"]);
    }
  });
});

describe("planLabelQuickAdd", () => {
  it("label column quick-add creates AND attaches to the label", () => {
    const plan = planLabelQuickAdd({
      newStateId: "s-backlog",
      labelId: "l-bug",
    });
    expect(plan).toEqual({ stateId: "s-backlog", attachLabelId: "l-bug" });
  });

  it("Ungrouped quick-add is a plain create (no attach)", () => {
    const plan = planLabelQuickAdd({
      newStateId: "s-backlog",
      labelId: null,
    });
    expect(plan).toEqual({ stateId: "s-backlog", attachLabelId: null });
  });

  it("state/priority columns are unaffected (no attach)", () => {
    const plan = planLabelQuickAdd({ newStateId: "s-1" });
    expect(plan).toEqual({ stateId: "s-1", attachLabelId: null });
  });
});

describe("planBoardLabelQuickExpand", () => {
  it("label column expand carries title + state + label", () => {
    expect(
      planBoardLabelQuickExpand(
        { newStateId: "s-backlog", labelId: "l-bug" },
        "New thing",
      ),
    ).toEqual({
      newIssue: true,
      newIssueName: "New thing",
      newIssueStateId: "s-backlog",
      newIssueLabelId: "l-bug",
    });
  });

  it("Ungrouped column expand carries title + state only (no label)", () => {
    const state = planBoardLabelQuickExpand(
      { newStateId: "s-backlog", labelId: null },
      "New thing",
    );
    expect(state.newIssueLabelId).toBeUndefined();
    expect(state.newIssueStateId).toBe("s-backlog");
  });

  it("priority carry still passes through alongside the label rule", () => {
    expect(
      planBoardLabelQuickExpand(
        { newStateId: "s-backlog", newPriority: 3 },
        "New thing",
      ),
    ).toEqual({
      newIssue: true,
      newIssueName: "New thing",
      newIssueStateId: "s-backlog",
      newIssuePriority: 3,
    });
  });
});
