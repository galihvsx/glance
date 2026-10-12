import { describe, expect, it } from "vitest";
import { computeMovePreview } from "./MoveIssueDialog";
import type { Estimate } from "../../lib/taxonomy";
import type { IssueState } from "../../lib/types";

function state(id: string, name: string, group: string, sequence: number): IssueState {
  return {
    id,
    project_id: "p",
    name,
    group,
    color: "#fff",
    sequence,
    created_at: "",
    updated_at: "",
  };
}

function scale(name: string, points: { id: string; key: string; value: number }[]): Estimate {
  return {
    id: `est-${name}`,
    project_id: "p",
    name,
    points: points.map((p) => ({ ...p, description: null })),
  };
}

describe("computeMovePreview", () => {
  const targetStates = [
    state("t-todo", "Todo", "unstarted", 10000),
    state("t-backlog", "Backlog", "backlog", 0),
    state("t-done", "Done", "completed", 30000),
  ];

  it("matches state by name case-insensitively", () => {
    const p = computeMovePreview({
      sourceStateName: "todo",
      targetStates,
      sourceEstimate: null,
      targetEstimates: [],
      sourceLabelNames: [],
    });
    expect(p.targetState).toEqual({ id: "t-todo", name: "Todo" });
    expect(p.stateFallback).toBe(false);
  });

  it("falls back to the backlog state when the name has no match", () => {
    const p = computeMovePreview({
      sourceStateName: "Review",
      targetStates,
      sourceEstimate: null,
      targetEstimates: [],
      sourceLabelNames: [],
    });
    expect(p.targetState).toEqual({ id: "t-backlog", name: "Backlog" });
    expect(p.stateFallback).toBe(true);
  });

  it("returns null target state when there is nothing to fall back to", () => {
    const p = computeMovePreview({
      sourceStateName: "Review",
      targetStates: [],
      sourceEstimate: null,
      targetEstimates: [],
      sourceLabelNames: [],
    });
    expect(p.targetState).toBeNull();
    expect(p.stateFallback).toBe(false);
  });

  it("keeps the estimate when a value match exists, drops otherwise", () => {
    const target = [scale("Points", [{ id: "pt-s", key: "S", value: 1 }])];
    const kept = computeMovePreview({
      sourceStateName: "Todo",
      targetStates,
      sourceEstimate: { scaleName: "Fib", key: "Small", value: 1 },
      targetEstimates: target,
      sourceLabelNames: [],
    });
    expect(kept.estimate).toEqual({ kind: "kept", label: "Points · S" });

    const dropped = computeMovePreview({
      sourceStateName: "Todo",
      targetStates,
      sourceEstimate: { scaleName: "Fib", key: "M", value: 2 },
      targetEstimates: target,
      sourceLabelNames: [],
    });
    expect(dropped.estimate).toEqual({ kind: "dropped" });

    const none = computeMovePreview({
      sourceStateName: "Todo",
      targetStates,
      sourceEstimate: null,
      targetEstimates: target,
      sourceLabelNames: [],
    });
    expect(none.estimate).toEqual({ kind: "none" });
  });

  it("keeps all labels (workspace-scoped, never dropped)", () => {
    const p = computeMovePreview({
      sourceStateName: "Todo",
      targetStates,
      sourceEstimate: null,
      targetEstimates: [],
      sourceLabelNames: ["bug", "frontend"],
    });
    expect(p.labelsKept).toEqual(["bug", "frontend"]);
    expect(p.labelsDropped).toEqual([]);
  });
});
