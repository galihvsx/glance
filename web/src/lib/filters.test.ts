import { describe, expect, it } from "vitest";
import {
  activeFilterCount,
  EMPTY_FILTERS,
  parseFilters,
  serializeFilters,
  toApiParams,
  type IssueFilters,
} from "./filters";

const FULL: IssueFilters = {
  q: "login bug",
  state: "state-uuid-1",
  priorities: [3, 1],
  labels: ["label-uuid-1", "label-uuid-2"],
  assignees: ["user-uuid-1", "none"],
  estimates: ["none"],
  createdAfter: "2026-10-01",
  createdBefore: "2026-10-09",
  updatedAfter: "",
  updatedBefore: "",
  dueAfter: "2026-10-05",
  dueBefore: "",
  subscribed: true,
};

describe("parseFilters / serializeFilters", () => {
  it("round-trips a full filter set through the URL", () => {
    const params = serializeFilters(FULL);
    const back = parseFilters(params);
    // priorities serialize in canonical sorted order
    expect(back).toEqual({ ...FULL, priorities: [1, 3] });
  });

  it("round-trips empty filters to an empty query string", () => {
    expect(serializeFilters(EMPTY_FILTERS).toString()).toBe("");
    expect(parseFilters(new URLSearchParams(""))).toEqual(EMPTY_FILTERS);
  });

  it("sorts priorities canonically", () => {
    const p = serializeFilters({ ...EMPTY_FILTERS, priorities: [3, 1] });
    expect(p.get("priority")).toBe("1,3");
  });

  it("drops invalid priorities and malformed dates", () => {
    const back = parseFilters(
      new URLSearchParams(
        "priority=1,9,abc&created_after=not-a-date&due_before=2026-13-99",
      ),
    );
    // "9" and "abc" are not valid priorities; "not-a-date" and the
    // impossible "2026-13-99" are not valid dates.
    expect(back.priorities).toEqual([1]);
    expect(back.createdAfter).toBe("");
    expect(back.dueBefore).toBe("");
  });

  it("keeps the 'none' sentinel for assignees and estimates", () => {
    const back = parseFilters(new URLSearchParams("assignee=none&estimate=none"));
    expect(back.assignees).toEqual(["none"]);
    expect(back.estimates).toEqual(["none"]);
  });
});

describe("toApiParams", () => {
  it("converts YYYY-MM-DD dates to RFC3339 ranges", () => {
    const p = toApiParams({
      ...EMPTY_FILTERS,
      createdAfter: "2026-10-01",
      createdBefore: "2026-10-09",
      dueAfter: "2026-10-05",
    });
    expect(p.get("created_after")).toBe("2026-10-01T00:00:00Z");
    // before is exclusive start of next day -> Oct 9 fully included
    expect(p.get("created_before")).toBe("2026-10-10T00:00:00Z");
    expect(p.get("due_after")).toBe("2026-10-05T00:00:00Z");
  });

  it("passes multi-value filters through comma-joined", () => {
    const p = toApiParams(FULL);
    expect(p.get("priority")).toBe("1,3");
    expect(p.get("label")).toBe("label-uuid-1,label-uuid-2");
    expect(p.get("assignee")).toBe("user-uuid-1,none");
    expect(p.get("subscribed")).toBe("1");
  });

  it("omits empty dimensions", () => {
    const p = toApiParams(EMPTY_FILTERS);
    expect(p.toString()).toBe("");
  });
});

describe("activeFilterCount", () => {
  it("counts dimensions, not values", () => {
    expect(activeFilterCount(EMPTY_FILTERS)).toBe(0);
    // q, state, priority, label, assignee, estimate, created pair,
    // due pair, subscribed = 9 dimensions
    expect(activeFilterCount(FULL)).toBe(9);
  });
});
