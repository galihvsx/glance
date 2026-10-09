import { describe, expect, it } from "vitest";
import { progressCounts } from "./Releases";
import type { Issue, IssueState } from "../lib/types";

function issue(id: string, state_id: string): Issue {
  return {
    id,
    project_id: "p",
    sequence_id: 1,
    display_id: "T-1",
    name: "x",
    priority: 0,
    state_id,
    sort_order: 0,
    is_draft: false,
    created_by: "u",
    created_at: "2026-01-01",
    updated_at: "2026-01-01",
    assignees: [],
    labels: [],
  };
}

function state(id: string, group: string): IssueState {
  return {
    id,
    name: id,
    group,
    color: "#000",
    sequence: 0,
    project_id: "p",
    created_at: "2026-01-01",
    updated_at: "2026-01-01",
  };
}

describe("progressCounts", () => {
  const states = [
    state("s1", "backlog"),
    state("s2", "started"),
    state("s3", "completed"),
    state("s4", "cancelled"),
  ];

  it("buckets by state group, cancelled counts as done", () => {
    const issues = [
      issue("a", "s1"),
      issue("b", "s2"),
      issue("c", "s3"),
      issue("d", "s4"),
    ];
    expect(progressCounts(issues, states)).toEqual({
      open: 1,
      inProgress: 1,
      done: 2,
    });
  });

  it("unknown state ids are ignored", () => {
    expect(progressCounts([issue("a", "nope")], states)).toEqual({
      open: 0,
      inProgress: 0,
      done: 0,
    });
  });

  it("empty inputs give zeros", () => {
    expect(progressCounts([], [])).toEqual({ open: 0, inProgress: 0, done: 0 });
  });
});
