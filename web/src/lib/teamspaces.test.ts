// C89T1: sidebar grouping unit tests — membership + order, archived
// exclusion, unknown-teamspace fallback, empty-teamspace headers.

import { describe, expect, it } from "vitest";
import { groupProjects } from "./teamspaces";
import type { Project, Teamspace } from "./types";

function ts(id: string, name: string): Teamspace {
  return { id, workspace_id: "w1", name, project_count: 0, created_at: "", updated_at: "" };
}

function p(id: string, teamspace_id: string | null, archived = false): Project {
  return {
    id,
    workspace_id: "w1",
    identifier: id.toUpperCase(),
    name: id,
    description: "",
    labels: [],
    parallel_cycles: false,
    teamspace_id,
    ...(archived ? { archived_at: "2026-01-01T00:00:00Z" } : {}),
    created_at: "",
    updated_at: "",
  };
}

describe("groupProjects", () => {
  it("groups by teamspace in list order with Ungrouped always last", () => {
    const groups = groupProjects(
      [p("a", "ts-b"), p("b", null), p("c", "ts-a"), p("d", "ts-b")],
      [ts("ts-a", "Alpha"), ts("ts-b", "Beta")],
    );
    expect(groups.map((g) => g.key)).toEqual(["ts-a", "ts-b", "ungrouped"]);
    expect(groups[0].projects.map((x) => x.id)).toEqual(["c"]);
    expect(groups[1].projects.map((x) => x.id)).toEqual(["a", "d"]);
    expect(groups[2].projects.map((x) => x.id)).toEqual(["b"]);
  });

  it("excludes archived projects and keeps empty teamspace headers", () => {
    const groups = groupProjects(
      [p("a", "ts-a", true), p("b", "ts-a")],
      [ts("ts-a", "Alpha"), ts("ts-empty", "Empty")],
    );
    expect(groups.find((g) => g.key === "ts-a")!.projects.map((x) => x.id)).toEqual(["b"]);
    expect(groups.find((g) => g.key === "ts-empty")!.projects).toEqual([]);
    expect(groups.some((g) => g.key === "ungrouped")).toBe(false);
  });

  it("falls back to Ungrouped for an unknown teamspace id", () => {
    const groups = groupProjects([p("a", "ts-gone")], [ts("ts-a", "Alpha")]);
    expect(groups.find((g) => g.key === "ungrouped")!.projects.map((x) => x.id)).toEqual(["a"]);
  });

  it("falls back to Ungrouped for an unknown id with excludeAbsent explicitly false", () => {
    const groups = groupProjects([p("a", "ts-gone")], [ts("ts-a", "Alpha")], {
      excludeAbsent: false,
    });
    expect(groups.find((g) => g.key === "ungrouped")!.projects.map((x) => x.id)).toEqual(["a"]);
  });
});

describe("groupProjects visibility (C90T1)", () => {
  it("excludes projects in teamspaces absent from the visible list, Ungrouped last", () => {
    // ts-hidden is absent from the visible list (a teamspace the viewer
    // cannot see): its project must NOT fall back to Ungrouped.
    const groups = groupProjects(
      [p("a", "ts-hidden"), p("b", null), p("c", "ts-a")],
      [ts("ts-a", "Alpha")],
      { excludeAbsent: true },
    );
    expect(groups.map((g) => g.key)).toEqual(["ts-a", "ungrouped"]);
    expect(groups[0].projects.map((x) => x.id)).toEqual(["c"]);
    expect(groups[1].projects.map((x) => x.id)).toEqual(["b"]);
    expect(groups.flatMap((g) => g.projects).map((x) => x.id)).not.toContain("a");
  });

  it("still excludes archived projects when excluding absent teamspaces", () => {
    const groups = groupProjects(
      [p("a", "ts-a", true), p("b", "ts-hidden", true), p("c", "ts-a")],
      [ts("ts-a", "Alpha")],
      { excludeAbsent: true },
    );
    expect(groups.flatMap((g) => g.projects).map((x) => x.id)).toEqual(["c"]);
  });
});
