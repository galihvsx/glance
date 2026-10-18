// Tests for lib/workflow.ts (C68T1): workflow transition controls client
// + helpers. The backend contract on main (C68T0):
//   GET/PUT /api/v1/workspaces/{slug}/projects/{identifier}/workflow
//     → {states, transitions: [{from_state_id, to_state_id}]}
//   denied moves → 409 code "state_transition_not_allowed",
//     details {"allowed": [state-id…]} naming permitted next-state IDs.
//   A project with zero transition rows is unrestricted.

import { describe, expect, it } from "vitest";
import {
  allowedNextStateIds,
  isTransitionAllowed,
  stateTransitionConflictMessage,
} from "./workflow";
import { ApiError } from "./api";

const wf = {
  states: [
    { id: "s1", name: "Backlog" },
    { id: "s2", name: "In Progress" },
    { id: "s3", name: "Done" },
  ] as never,
  transitions: [
    { from_state_id: "s1", to_state_id: "s2" },
    { from_state_id: "s2", to_state_id: "s3" },
  ],
};

const unrestricted = { states: wf.states, transitions: [] as never[] };

describe("allowedNextStateIds", () => {
  it("returns null when the project has no rules (unrestricted)", () => {
    expect(allowedNextStateIds(unrestricted, "s1")).toBeNull();
  });

  it("returns the allowed next-state IDs for a ruled from-state", () => {
    expect(allowedNextStateIds(wf, "s1")).toEqual(["s2"]);
  });

  it("returns [] when the from-state has no outgoing rules (everything denied)", () => {
    expect(allowedNextStateIds(wf, "s3")).toEqual([]);
  });

  it("treats the same-state 'move' as implicitly allowed", () => {
    expect(isTransitionAllowed(wf, "s2", "s2")).toBe(true);
  });

  it("denies unlisted transitions when rules exist", () => {
    expect(isTransitionAllowed(wf, "s1", "s3")).toBe(false);
    expect(isTransitionAllowed(wf, "s2", "s1")).toBe(false);
  });

  it("allows everything when unrestricted", () => {
    expect(isTransitionAllowed(unrestricted, "s1", "s3")).toBe(true);
  });
});

describe("stateTransitionConflictMessage", () => {
  it("names the permitted next states from 409 details", () => {
    const e = new ApiError(409, "state transition not allowed", "state_transition_not_allowed", {
      allowed: ["s2"],
    });
    const names = new Map([["s2", "In Progress"]]);
    expect(stateTransitionConflictMessage(e, names)).toBe(
      "State transition not allowed by the project's workflow. Permitted next states: In Progress",
    );
  });

  it("falls back to the ID when a state name is unknown", () => {
    const e = new ApiError(409, "state transition not allowed", "state_transition_not_allowed", {
      allowed: ["sx"],
    });
    expect(stateTransitionConflictMessage(e, new Map())).toBe(
      "State transition not allowed by the project's workflow. Permitted next states: sx",
    );
  });

  it("handles a missing/empty allowed list", () => {
    const e = new ApiError(409, "state transition not allowed", "state_transition_not_allowed", {
      allowed: [],
    });
    expect(stateTransitionConflictMessage(e, new Map())).toBe(
      "State transition not allowed by the project's workflow.",
    );
  });

  it("returns null for non-transition-conflict errors", () => {
    const e = new ApiError(409, "conflict", "open_blockers", { blockers: ["A-1"] });
    expect(stateTransitionConflictMessage(e, new Map())).toBeNull();
    expect(stateTransitionConflictMessage(new Error("boom"), new Map())).toBeNull();
  });
});
