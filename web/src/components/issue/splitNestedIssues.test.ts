// C124T0: shared split (extracted verbatim from C122T1's
// splitBoardIssues) — nested rows under top-level parents, flat
// child rows hidden while nested, orphans and grandchildren stay
// flat (a child NEVER vanishes). Same cases as the old board split
// tests, plus the epic multi-appearance parent case.
import { describe, expect, it } from "vitest";
import { splitNestedIssues } from "./splitNestedIssues";
import type { Issue, SubtaskRow } from "../../lib/types";

function makeIssue(id: string, extra: Partial<Issue> = {}): Issue {
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

describe("splitNestedIssues (C124T0)", () => {
  it("toggle off returns the flat layout untouched", () => {
    const parent = makeIssue("i-p", { subtasks: [row("i-c", 1)] });
    const child = makeIssue("i-c");
    const { visibleIssues, nestedByParent } = splitNestedIssues(
      [parent, child],
      false,
    );
    expect(visibleIssues.map((i) => i.id)).toEqual(["i-p", "i-c"]);
    expect(nestedByParent.size).toBe(0);
  });

  it("groups children under parents, sorted by sort_order, hiding flat rows", () => {
    const parent = makeIssue("i-p", {
      subtasks: [row("i-c2", 2), row("i-c1", 1)],
    });
    const { visibleIssues, nestedByParent } = splitNestedIssues(
      [parent, makeIssue("i-c1"), makeIssue("i-c2")],
      true,
    );
    expect(visibleIssues.map((i) => i.id)).toEqual(["i-p"]);
    expect(nestedByParent.get("i-p")?.map((r) => r.id)).toEqual([
      "i-c1",
      "i-c2",
    ]);
  });

  it("orphan child (parent not in the result set) stays a flat row", () => {
    const orphan = makeIssue("i-orphan");
    const { visibleIssues, nestedByParent } = splitNestedIssues([orphan], true);
    expect(visibleIssues.map((i) => i.id)).toEqual(["i-orphan"]);
    expect(nestedByParent.size).toBe(0);
  });

  it("undefined subtasks renders nothing nested and never crashes", () => {
    const plain = makeIssue("i-p");
    const { visibleIssues, nestedByParent } = splitNestedIssues([plain], true);
    expect(visibleIssues.map((i) => i.id)).toEqual(["i-p"]);
    expect(nestedByParent.size).toBe(0);
  });

  it("grandchild stays flat: only top-level parents nest rows (one level)", () => {
    const parent = makeIssue("i-p", { subtasks: [row("i-c", 1)] });
    const child = makeIssue("i-c", { subtasks: [row("i-g", 1)] });
    const grandchild = makeIssue("i-g");
    const { visibleIssues, nestedByParent } = splitNestedIssues(
      [parent, child, grandchild],
      true,
    );
    expect(visibleIssues.map((i) => i.id).sort()).toEqual(["i-g", "i-p"]);
    expect(nestedByParent.has("i-c")).toBe(false);
    expect(nestedByParent.get("i-p")?.map((r) => r.id)).toEqual(["i-c"]);
  });

  it("empty subtasks array nests nothing for a childless parent", () => {
    const parent = makeIssue("i-p", { subtasks: [] });
    const { visibleIssues, nestedByParent } = splitNestedIssues([parent], true);
    expect(visibleIssues.map((i) => i.id)).toEqual(["i-p"]);
    expect(nestedByParent.size).toBe(0);
  });

  it("epic multi-appearance parent: returned once with its rows — the grouping layer duplicates the appearance, not the split", () => {
    // The split sees the fetched set once; epic grouping (which
    // runs on visibleIssues) renders the parent under each epic.
    // The split must therefore keep the parent visible exactly once
    // with its full nested rows, keyed by parent id for every
    // appearance to look up.
    const parent = makeIssue("i-p", {
      subtasks: [row("i-c", 1)],
      epics: [
        { id: "e-1", name: "Auth" },
        { id: "e-2", name: "Checkout" },
      ],
    });
    const { visibleIssues, nestedByParent } = splitNestedIssues(
      [parent, makeIssue("i-c")],
      true,
    );
    expect(visibleIssues.map((i) => i.id)).toEqual(["i-p"]);
    expect(nestedByParent.get("i-p")?.map((r) => r.id)).toEqual(["i-c"]);
  });
});
