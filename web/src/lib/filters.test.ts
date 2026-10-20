import { describe, expect, it } from "vitest";
import {
  activeFilterCount,
  EMPTY_FILTERS,
  isFilterParam,
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
  parents: ["123e4567-e89b-12d3-a456-426614174000", "none"],
  epics: ["123e4567-e89b-12d3-a456-426614174001"],
  createdAfter: "2026-10-01",
  createdBefore: "2026-10-09",
  updatedAfter: "",
  updatedBefore: "",
  dueAfter: "2026-10-05",
  dueBefore: "",
  dueDateEmpty: false,
  subscribed: true,
  archived: true,
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
    expect(p.get("parent")).toBe(
      "123e4567-e89b-12d3-a456-426614174000,none",
    );
    expect(p.get("epic")).toBe("123e4567-e89b-12d3-a456-426614174001");
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
    // q, state, priority, label, assignee, estimate, parent, epic,
    // created pair, due pair, subscribed, archived = 12 dimensions
    expect(activeFilterCount(FULL)).toBe(12);
  });
});

describe("due_date_empty is-empty chip (C80T1)", () => {
  it("serializes the chip to ?due_date_empty=1", () => {
    const p = serializeFilters({ ...EMPTY_FILTERS, dueDateEmpty: true });
    expect(p.get("due_date_empty")).toBe("1");
  });

  it("parses '1' and 'true'; absent or other values are off", () => {
    expect(parseFilters(new URLSearchParams("due_date_empty=1")).dueDateEmpty).toBe(true);
    expect(parseFilters(new URLSearchParams("due_date_empty=true")).dueDateEmpty).toBe(true);
    expect(parseFilters(new URLSearchParams("due_date_empty=0")).dueDateEmpty).toBe(false);
    expect(parseFilters(new URLSearchParams("")).dueDateEmpty).toBe(false);
  });

  it("round-trips the chip through the URL", () => {
    const back = parseFilters(serializeFilters({ ...EMPTY_FILTERS, dueDateEmpty: true }));
    expect(back.dueDateEmpty).toBe(true);
    expect(back.dueAfter).toBe("");
    expect(back.dueBefore).toBe("");
  });

  it("passes due_date_empty through toApiParams unchanged (no date conversion)", () => {
    const p = toApiParams({ ...EMPTY_FILTERS, dueDateEmpty: true });
    expect(p.get("due_date_empty")).toBe("1");
    expect(p.get("due_after")).toBeNull();
    expect(p.get("due_before")).toBeNull();
  });

  it("omits due_date_empty when the chip is off", () => {
    expect(serializeFilters(EMPTY_FILTERS).get("due_date_empty")).toBeNull();
    expect(toApiParams(EMPTY_FILTERS).get("due_date_empty")).toBeNull();
  });

  it("counts the chip in the due dimension (badge display)", () => {
    expect(activeFilterCount({ ...EMPTY_FILTERS, dueDateEmpty: true })).toBe(1);
    // Chip or range both live in the same "due" dimension — one badge slot.
    expect(
      activeFilterCount({ ...EMPTY_FILTERS, dueDateEmpty: true, dueAfter: "2026-10-01" }),
    ).toBe(1);
    expect(activeFilterCount(FULL)).toBe(12);
  });

  it("recognizes due_date_empty as a filter param so clear-all drops it", () => {
    expect(isFilterParam("due_date_empty")).toBe(true);
  });
});

describe("parent/epic filters (C75T1)", () => {
  const P1 = "123e4567-e89b-12d3-a456-426614174000";
  const P2 = "123e4567-e89b-12d3-a456-426614174001";
  const E1 = "123e4567-e89b-12d3-a456-426614174002";

  it("round-trips parents and epics through the URL", () => {
    const params = serializeFilters({
      ...EMPTY_FILTERS,
      parents: [P1, "none"],
      epics: [E1],
    });
    expect(params.get("parent")).toBe(`${P1},none`);
    expect(params.get("epic")).toBe(E1);
    const back = parseFilters(params);
    expect(back.parents).toEqual([P1, "none"]);
    expect(back.epics).toEqual([E1]);
  });

  it("keeps the 'none' sentinel for parent and epic", () => {
    const back = parseFilters(new URLSearchParams("parent=none&epic=none"));
    expect(back.parents).toEqual(["none"]);
    expect(back.epics).toEqual(["none"]);
  });

  it("supports repeatable and comma-separated forms together", () => {
    const back = parseFilters(
      new URLSearchParams(`parent=${P1}&parent=${P2},none&epic=${E1},${P1}`),
    );
    expect(back.parents).toEqual([P1, P2, "none"]);
    expect(back.epics).toEqual([E1, P1]);
  });

  it("drops malformed parent/epic values and never throws", () => {
    const back = parseFilters(
      new URLSearchParams(
        "parent=garbage,,none,123&epic=not-a-uuid,&q=still-fine",
      ),
    );
    expect(back.parents).toEqual(["none"]);
    expect(back.epics).toEqual([]);
    // The rest of the schema keeps parsing around the garbage.
    expect(back.q).toBe("still-fine");
  });

  it("rejects almost-UUIDs (wrong length, non-hex)", () => {
    const back = parseFilters(
      new URLSearchParams(
        "parent=123e4567-e89b-12d3-a456-42661417400,zzzzzzzz-zzzz-zzzz-zzzz-zzzzzzzzzzzz",
      ),
    );
    expect(back.parents).toEqual([]);
  });

  it("passes parent/epic through toApiParams comma-joined", () => {
    const p = toApiParams({
      ...EMPTY_FILTERS,
      parents: [P1, "none"],
      epics: [E1],
    });
    expect(p.get("parent")).toBe(`${P1},none`);
    expect(p.get("epic")).toBe(E1);
  });

  it("omits empty parent/epic dimensions from the query string", () => {
    const p = serializeFilters(EMPTY_FILTERS);
    expect(p.get("parent")).toBeNull();
    expect(p.get("epic")).toBeNull();
    const api = toApiParams(EMPTY_FILTERS);
    expect(api.get("parent")).toBeNull();
    expect(api.get("epic")).toBeNull();
  });

  it("counts parent and epic as separate badge dimensions", () => {
    expect(activeFilterCount({ ...EMPTY_FILTERS, parents: [P1] })).toBe(1);
    expect(
      activeFilterCount({ ...EMPTY_FILTERS, epics: [E1], parents: ["none"] }),
    ).toBe(2);
  });

  it("recognizes parent/epic as filter params (peek= etc. still preserved)", () => {
    expect(isFilterParam("parent")).toBe(true);
    expect(isFilterParam("epic")).toBe(true);
    expect(isFilterParam("peek")).toBe(false);
  });
});
