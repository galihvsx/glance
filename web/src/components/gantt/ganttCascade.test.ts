import { describe, expect, it } from "vitest";
import type { IssueLink } from "../../lib/types";
import {
  computeCascade,
  type CascadeIssue,
} from "./ganttCascade";

function issue(
  id: string,
  start_date: string | null,
  target_date: string | null,
): CascadeIssue {
  return { id, start_date, target_date };
}

function link(
  from: string,
  to: string,
  kind = "blocks",
  id = `link-${from}-${to}-${kind}`,
): IssueLink {
  return {
    id,
    issue_id: from,
    target_issue_id: to,
    kind,
    created_at: "2026-11-04T00:00:00Z",
  };
}

describe("computeCascade", () => {
  it("move-later chain: A blocks B blocks C, all shift by k", () => {
    const issues = [
      issue("a", "2026-11-02", "2026-11-04"),
      issue("b", "2026-11-05", "2026-11-06"),
      issue("c", "2026-11-07", "2026-11-08"),
    ];
    const links = [link("a", "b"), link("b", "c")];
    // A moves +3 (end 11-04 → 11-07).
    const shifts = computeCascade(
      issues,
      links,
      "a",
      "2026-11-05",
      "2026-11-07",
      "move",
    );
    expect(shifts).toEqual([
      { id: "b", start_date: "2026-11-08", target_date: "2026-11-09" },
      { id: "c", start_date: "2026-11-10", target_date: "2026-11-11" },
    ]);
  });

  it("move-earlier: empty cascade", () => {
    const issues = [
      issue("a", "2026-11-02", "2026-11-04"),
      issue("b", "2026-11-05", "2026-11-06"),
      issue("c", "2026-11-07", "2026-11-08"),
    ];
    const links = [link("a", "b"), link("b", "c")];
    // A moves −2 (end 11-04 → 11-02): gaps only grow.
    expect(
      computeCascade(issues, links, "a", "2026-10-31", "2026-11-02", "move"),
    ).toEqual([]);
  });

  it("resize-end push: k = end delta", () => {
    const issues = [
      issue("a", "2026-11-02", "2026-11-04"),
      issue("b", "2026-11-05", "2026-11-06"),
    ];
    const links = [link("a", "b")];
    // End dragged 11-04 → 11-06 (k = +2), start unchanged.
    const shifts = computeCascade(
      issues,
      links,
      "a",
      "2026-11-02",
      "2026-11-06",
      "resize-end",
    );
    expect(shifts).toEqual([
      { id: "b", start_date: "2026-11-07", target_date: "2026-11-08" },
    ]);
  });

  it("resize-start: empty cascade even though the start moved", () => {
    const issues = [
      issue("a", "2026-11-02", "2026-11-04"),
      issue("b", "2026-11-05", "2026-11-06"),
    ];
    const links = [link("a", "b")];
    // Start dragged 11-02 → 11-01, end unchanged: k = 0.
    expect(
      computeCascade(
        issues,
        links,
        "a",
        "2026-11-01",
        "2026-11-04",
        "resize-start",
      ),
    ).toEqual([]);
  });

  it("non-violating far dependent: no shift", () => {
    const issues = [
      issue("a", "2026-11-02", "2026-11-04"),
      issue("b", "2026-11-20", "2026-11-21"),
    ];
    const links = [link("a", "b")];
    // A moves +3 (new end 11-07); B starts 11-20, far past 11-08.
    expect(
      computeCascade(
        issues,
        links,
        "a",
        "2026-11-05",
        "2026-11-07",
        "move",
      ),
    ).toEqual([]);
  });

  it("diamond: one shift, not two", () => {
    const issues = [
      issue("a", "2026-11-02", "2026-11-04"),
      issue("b", "2026-11-05", "2026-11-05"),
      issue("c", "2026-11-05", "2026-11-06"),
      issue("d", "2026-11-07", "2026-11-08"),
    ];
    const links = [
      link("a", "b"),
      link("a", "c"),
      link("b", "d"),
      link("c", "d"),
    ];
    // A moves +3 (new end 11-07). D is reached via both B and C.
    const shifts = computeCascade(
      issues,
      links,
      "a",
      "2026-11-05",
      "2026-11-07",
      "move",
    );
    expect(shifts).toEqual([
      { id: "b", start_date: "2026-11-08", target_date: "2026-11-08" },
      { id: "c", start_date: "2026-11-08", target_date: "2026-11-09" },
      { id: "d", start_date: "2026-11-10", target_date: "2026-11-11" },
    ]);
    expect(shifts.filter((s) => s.id === "d")).toHaveLength(1);
  });

  it("diamond with a deeper blocker: evaluated against all shifted blockers", () => {
    // D is a direct dependent of A (BFS depth 1) but violates only
    // against Y (depth 2): D must still shift once Y shifts.
    const issues = [
      issue("a", "2026-11-02", "2026-11-04"),
      issue("x", "2026-11-05", "2026-11-05"),
      issue("y", "2026-11-07", "2026-11-07"),
      issue("d", "2026-11-09", "2026-11-10"),
    ];
    const links = [
      link("a", "d"),
      link("a", "x"),
      link("x", "y"),
      link("y", "d"),
    ];
    // A moves +2 (new end 11-06). X → 11-07, Y → 11-09, then D
    // violates against Y (11-09 < 11-09 + 1) and shifts +2.
    const shifts = computeCascade(
      issues,
      links,
      "a",
      "2026-11-04",
      "2026-11-06",
      "move",
    );
    expect(shifts).toEqual([
      { id: "d", start_date: "2026-11-11", target_date: "2026-11-12" },
      { id: "x", start_date: "2026-11-07", target_date: "2026-11-07" },
      { id: "y", start_date: "2026-11-09", target_date: "2026-11-09" },
    ]);
  });

  it("relates_to/duplicates edges are ignored", () => {
    const issues = [
      issue("a", "2026-11-02", "2026-11-04"),
      issue("b", "2026-11-05", "2026-11-06"),
      issue("c", "2026-11-05", "2026-11-06"),
    ];
    const links = [link("a", "b", "relates_to"), link("a", "c", "duplicates")];
    expect(
      computeCascade(
        issues,
        links,
        "a",
        "2026-11-05",
        "2026-11-07",
        "move",
      ),
    ).toEqual([]);
  });

  it("dateless dependent skipped; propagation stops on that path", () => {
    const issues = [
      issue("a", "2026-11-02", "2026-11-04"),
      issue("b", null, null),
      issue("c", "2026-11-05", "2026-11-06"),
      issue("d", "2026-11-05", "2026-11-06"),
    ];
    const links = [link("a", "b"), link("b", "c"), link("a", "d")];
    // B has nothing to shift, so C (reachable only through B) does
    // not shift; D, blocked directly by A, does.
    const shifts = computeCascade(
      issues,
      links,
      "a",
      "2026-11-05",
      "2026-11-07",
      "move",
    );
    expect(shifts).toEqual([
      { id: "d", start_date: "2026-11-08", target_date: "2026-11-09" },
    ]);
  });

  it("cycle input terminates (visited set)", () => {
    const issues = [
      issue("a", "2026-11-02", "2026-11-04"),
      issue("b", "2026-11-05", "2026-11-06"),
      issue("c", "2026-11-07", "2026-11-08"),
    ];
    const links = [
      link("a", "b"),
      link("b", "a"), // Back edge: backend rejects this, engine tolerates it.
      link("a", "a"), // Self edge: dragged issue is never shifted.
      link("b", "c"),
      link("c", "b"), // B ↔ C cycle.
    ];
    const shifts = computeCascade(
      issues,
      links,
      "a",
      "2026-11-05",
      "2026-11-07",
      "move",
    );
    expect(shifts).toEqual([
      { id: "b", start_date: "2026-11-08", target_date: "2026-11-09" },
      { id: "c", start_date: "2026-11-10", target_date: "2026-11-11" },
    ]);
  });

  it("single-date dependent shifts as a 1-day span", () => {
    const issues = [
      issue("a", "2026-11-02", "2026-11-04"),
      issue("b", "2026-11-05", null),
      issue("c", null, "2026-11-05"),
    ];
    const links = [link("a", "b"), link("a", "c")];
    // A moves +3; both single-date dependents shift by 3 as 1-day
    // spans (the caller keeps them single-date via rule 7 payloads).
    const shifts = computeCascade(
      issues,
      links,
      "a",
      "2026-11-05",
      "2026-11-07",
      "move",
    );
    expect(shifts).toEqual([
      { id: "b", start_date: "2026-11-08", target_date: "2026-11-08" },
      { id: "c", start_date: "2026-11-08", target_date: "2026-11-08" },
    ]);
  });

  it("pre-existing violation preserved: shifts by k, overlap unchanged", () => {
    const issues = [
      issue("a", "2026-11-02", "2026-11-04"),
      issue("b", "2026-11-03", "2026-11-05"),
    ];
    const links = [link("a", "b")];
    // B already overlaps A (starts 1 day before A ends). A moves +3.
    const shifts = computeCascade(
      issues,
      links,
      "a",
      "2026-11-05",
      "2026-11-07",
      "move",
    );
    expect(shifts).toEqual([
      { id: "b", start_date: "2026-11-06", target_date: "2026-11-08" },
    ]);
    // Overlap preserved exactly: B still starts 1 day before A ends.
    expect(shifts[0].start_date).toBe("2026-11-06");
  });

  it("violation against an unshifted blocker alone does not shift", () => {
    const issues = [
      issue("a", "2026-11-02", "2026-11-04"),
      issue("b", "2026-11-20", "2026-11-21"),
      issue("c", "2026-11-21", "2026-11-22"),
    ];
    const links = [link("a", "b"), link("b", "c")];
    // B is far from A so it does not shift; C already violates
    // against B, but B did not move, so C does not shift either.
    expect(
      computeCascade(
        issues,
        links,
        "a",
        "2026-11-05",
        "2026-11-07",
        "move",
      ),
    ).toEqual([]);
  });

  it("dependents outside the fetched set are skipped", () => {
    const issues = [
      issue("a", "2026-11-02", "2026-11-04"),
      issue("b", "2026-11-05", "2026-11-06"),
    ];
    const links = [link("a", "b"), link("a", "outside")];
    const shifts = computeCascade(
      issues,
      links,
      "a",
      "2026-11-05",
      "2026-11-07",
      "move",
    );
    expect(shifts).toEqual([
      { id: "b", start_date: "2026-11-08", target_date: "2026-11-09" },
    ]);
  });

  it("unknown dragged issue or no links: empty cascade", () => {
    const issues = [
      issue("a", "2026-11-02", "2026-11-04"),
      issue("b", "2026-11-05", "2026-11-06"),
    ];
    expect(
      computeCascade(issues, [], "a", "2026-11-05", "2026-11-07", "move"),
    ).toEqual([]);
    expect(
      computeCascade(
        issues,
        [link("a", "b")],
        "missing",
        "2026-11-05",
        "2026-11-07",
        "move",
      ),
    ).toEqual([]);
  });
});
