// mywork lib tests (C6T3): filter labels and the state-group grouping.

import { describe, expect, it } from "vitest";
import {
  filterLabel,
  groupMyWorkByState,
  type MyWorkItem,
} from "./mywork";

function item(over: Partial<MyWorkItem> & { id: string }): MyWorkItem {
  return {
    display_id: "ENG-1",
    name: "task",
    priority: 0,
    state_id: "s1",
    state_name: "Todo",
    state_group: "unstarted",
    project_id: "p1",
    project_identifier: "ENG",
    project_name: "Engineering",
    updated_at: "2026-10-09T00:00:00Z",
    ...over,
  };
}

describe("filterLabel", () => {
  it("labels the three filters", () => {
    expect(filterLabel("assigned")).toBe("Assigned to me");
    expect(filterLabel("created")).toBe("Created by me");
    expect(filterLabel("watched")).toBe("Watched");
  });
});

describe("groupMyWorkByState", () => {
  it("groups by state group in kanban order", () => {
    const items = [
      item({ id: "a", state_name: "Done", state_group: "completed" }),
      item({ id: "b", state_name: "Todo", state_group: "unstarted" }),
      item({ id: "c", state_name: "In Progress", state_group: "started" }),
      item({ id: "d", state_name: "Backlog", state_group: "backlog" }),
    ];
    const groups = groupMyWorkByState(items);
    expect(groups.map((g) => g.group)).toEqual([
      "backlog",
      "unstarted",
      "started",
      "completed",
    ]);
    expect(groups[0].states[0].items.map((i) => i.id)).toEqual(["d"]);
  });

  it("nests multiple states under one group", () => {
    const items = [
      item({ id: "a", state_name: "Todo", state_group: "unstarted" }),
      item({ id: "b", state_name: "Next", state_group: "unstarted" }),
    ];
    const groups = groupMyWorkByState(items);
    expect(groups).toHaveLength(1);
    expect(groups[0].states.map((s) => s.name)).toEqual(["Todo", "Next"]);
  });

  it("buckets unknown groups into Other, last", () => {
    const items = [
      item({ id: "a", state_name: "Todo", state_group: "unstarted" }),
      item({ id: "b", state_name: "Weird", state_group: "limbo" }),
    ];
    const groups = groupMyWorkByState(items);
    expect(groups.map((g) => g.group)).toEqual(["unstarted", "other"]);
  });

  it("returns empty for no items", () => {
    expect(groupMyWorkByState([])).toEqual([]);
  });
});
