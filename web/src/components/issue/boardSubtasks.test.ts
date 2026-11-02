// C122T1: board split — nested rows under top-level parent cards,
// flat child cards hidden while nested, orphans and grandchildren
// stay flat (a child NEVER vanishes).

import { describe, expect, it } from "vitest";
import { splitBoardIssues, subtaskPriorityLevel } from "./boardSubtasks";
import type { Issue, SubtaskRow } from "../../lib/types";

function makeIssue(
  id: string,
  extra: Partial<Issue> = {},
): Issue {
  return {
    id,
    project_id: "p-eng",
    sequence_id: 1,
    display_id: id.toUpperCase(),
    name: `Issue ${id}`,
    priority: 3,
    state_id: "s-todo",
    sort_order: 1,
    is_draft: false,
    created_by: "u-1",
    created_at: "",
    updated_at: "",
    assignees: [],
    labels: [],
    ...extra,
  };
}

function row(
  id: string,
  sortOrder: number,
  extra: Partial<SubtaskRow> = {},
): SubtaskRow {
  return {
    id,
    display_id: id.toUpperCase(),
    title: `Subtask ${id}`,
    state_id: "s-done",
    priority: 3,
    assignee_ids: [],
    sort_order: sortOrder,
    ...extra,
  };
}

describe("splitBoardIssues (C122T1)", () => {
  it("toggle off returns the flat layout untouched", () => {
    const parent = makeIssue("i-p", { subtasks: [row("i-c", 1)] });
    const child = makeIssue("i-c");
    const { visibleIssues, nestedByParent } = splitBoardIssues(
      [parent, child],
      false,
    );
    expect(visibleIssues.map((i) => i.id)).toEqual(["i-p", "i-c"]);
    expect(nestedByParent.size).toBe(0);
  });

  it("groups children under parents, sorted by sort_order, hiding flat cards", () => {
    const parent = makeIssue("i-p", {
      subtasks: [row("i-c2", 2), row("i-c1", 1)],
    });
    const { visibleIssues, nestedByParent } = splitBoardIssues(
      [parent, makeIssue("i-c1"), makeIssue("i-c2")],
      true,
    );
    expect(visibleIssues.map((i) => i.id)).toEqual(["i-p"]);
    expect(nestedByParent.get("i-p")?.map((r) => r.id)).toEqual([
      "i-c1",
      "i-c2",
    ]);
  });

  it("orphan child (parent not in the result set) stays a flat card", () => {
    const orphan = makeIssue("i-orphan");
    const { visibleIssues, nestedByParent } = splitBoardIssues([orphan], true);
    expect(visibleIssues.map((i) => i.id)).toEqual(["i-orphan"]);
    expect(nestedByParent.size).toBe(0);
  });

  it("undefined subtasks renders nothing nested and never crashes", () => {
    const plain = makeIssue("i-p");
    const { visibleIssues, nestedByParent } = splitBoardIssues([plain], true);
    expect(visibleIssues.map((i) => i.id)).toEqual(["i-p"]);
    expect(nestedByParent.size).toBe(0);
  });

  it("grandchild stays flat: only top-level parents nest rows (one level)", () => {
    const parent = makeIssue("i-p", { subtasks: [row("i-c", 1)] });
    const child = makeIssue("i-c", { subtasks: [row("i-g", 1)] });
    const grandchild = makeIssue("i-g");
    const { visibleIssues, nestedByParent } = splitBoardIssues(
      [parent, child, grandchild],
      true,
    );
    expect(visibleIssues.map((i) => i.id).sort()).toEqual([
      "i-g",
      "i-p",
    ]);
    expect(nestedByParent.has("i-c")).toBe(false);
    expect(nestedByParent.get("i-p")?.map((r) => r.id)).toEqual(["i-c"]);
  });

  it("empty subtasks array nests nothing for a childless parent", () => {
    const parent = makeIssue("i-p", { subtasks: [] });
    const { visibleIssues, nestedByParent } = splitBoardIssues([parent], true);
    expect(visibleIssues.map((i) => i.id)).toEqual(["i-p"]);
    expect(nestedByParent.size).toBe(0);
  });
});

describe("subtaskPriorityLevel (C122T1)", () => {
  it("passes numeric priorities through", () => {
    expect(subtaskPriorityLevel(4)).toBe(4);
    expect(subtaskPriorityLevel(0)).toBe(0);
  });

  it("maps plan-contract label strings to levels", () => {
    expect(subtaskPriorityLevel("High")).toBe(3);
    expect(subtaskPriorityLevel("Urgent")).toBe(4);
    expect(subtaskPriorityLevel("None")).toBe(0);
  });

  it("returns null for null / unknown strings", () => {
    expect(subtaskPriorityLevel(null)).toBeNull();
    expect(subtaskPriorityLevel("bogus")).toBeNull();
  });
});
