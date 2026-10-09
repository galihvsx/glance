import { describe, expect, it } from "vitest";
import { planHasActions, planTriageApply } from "./triagePlan";
import type {
  Issue,
  IssueState,
  Label as ProjectLabel,
} from "../../lib/types";

const states: IssueState[] = [
  {
    id: "s-todo",
    project_id: "p1",
    name: "Todo",
    group: "unstarted",
    color: "#888",
    sequence: 0,
    created_at: "x",
    updated_at: "x",
  },
  {
    id: "s-prog",
    project_id: "p1",
    name: "In Progress",
    group: "started",
    color: "#00f",
    sequence: 1,
    created_at: "x",
    updated_at: "x",
  },
];

const labels: ProjectLabel[] = [
  {
    id: "l-bug",
    name: "bug",
    color: "#f00",
    created_at: "x",
    updated_at: "x",
  },
  {
    id: "l-ui",
    name: "ui",
    color: "#0f0",
    created_at: "x",
    updated_at: "x",
  },
];

const issue: Pick<Issue, "priority" | "state_id" | "labels"> = {
  priority: 1,
  state_id: "s-todo",
  labels: [{ id: "l-bug", name: "bug", color: "#f00" }],
};

describe("planTriageApply", () => {
  it("resolves state name and unmatched labels, skipping applied labels", () => {
    const plan = planTriageApply(
      {
        priority: 3,
        label_names: ["bug", "ui", "ghost"],
        state_name: "In Progress",
      },
      issue,
      states,
      labels,
    );
    expect(plan.priority).toBe(3);
    // "bug" is already applied — only "ui" is added.
    expect(plan.labelIdsToAdd).toEqual(["l-ui"]);
    expect(plan.unmatchedLabelNames).toEqual(["ghost"]);
    expect(plan.stateId).toBe("s-prog");
    expect(plan.unmatchedStateName).toBeUndefined();
  });

  it("matches names case-insensitively", () => {
    const plan = planTriageApply(
      { priority: 2, label_names: ["UI"], state_name: "todo" },
      { ...issue, labels: [] },
      states,
      labels,
    );
    expect(plan.labelIdsToAdd).toEqual(["l-ui"]);
    expect(plan.stateId).toBe("s-todo");
  });

  it("deduplicates repeated label suggestions", () => {
    const plan = planTriageApply(
      { priority: 2, label_names: ["ui", "UI", "ui"] },
      { ...issue, labels: [] },
      states,
      labels,
    );
    expect(plan.labelIdsToAdd).toEqual(["l-ui"]);
  });

  it("handles a null state and empty labels", () => {
    const plan = planTriageApply(
      { priority: 0, label_names: [], state_name: null },
      issue,
      states,
      labels,
    );
    expect(plan.stateId).toBeUndefined();
    expect(plan.unmatchedStateName).toBeUndefined();
    expect(planHasActions(plan)).toBe(false);
  });

  it("flags an unmatched state name instead of guessing", () => {
    const plan = planTriageApply(
      { priority: 4, label_names: [], state_name: "Dreaming" },
      issue,
      states,
      labels,
    );
    expect(plan.stateId).toBeUndefined();
    expect(plan.unmatchedStateName).toBe("Dreaming");
  });

  it("reports actions only when there is something to apply", () => {
    expect(
      planHasActions({
        priority: 3,
        labelIdsToAdd: ["l-ui"],
        unmatchedLabelNames: [],
      }),
    ).toBe(true);
    expect(
      planHasActions({
        priority: 3,
        labelIdsToAdd: [],
        unmatchedLabelNames: [],
        stateId: "s-prog",
      }),
    ).toBe(true);
    expect(
      planHasActions({
        priority: 3,
        labelIdsToAdd: [],
        unmatchedLabelNames: ["ghost"],
      }),
    ).toBe(false);
  });
});
