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
  cycles: ["123e4567-e89b-12d3-a456-426614174003"],
  modules: ["123e4567-e89b-12d3-a456-426614174004", "none"],
  releases: ["123e4567-e89b-12d3-a456-426614174005", "none"],
  createdBy: ["123e4567-e89b-12d3-a456-426614174006"],
  mentions: ["123e4567-e89b-12d3-a456-426614174007", "none"],
  stateGroups: ["started", "completed"],
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
    // cycle, module, release, created by, mentions, created pair,
    // due pair, subscribed, archived = 17 dimensions (C120T1 adds
    // mentions; the comment's old "16" count predates it)
    expect(activeFilterCount(FULL)).toBe(18);
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
    expect(activeFilterCount(FULL)).toBe(18);
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

describe("cycle/module filters (C116T1)", () => {
  const C1 = "123e4567-e89b-12d3-a456-426614174010";
  const C2 = "123e4567-e89b-12d3-a456-426614174011";
  const M1 = "123e4567-e89b-12d3-a456-426614174012";

  it("round-trips cycles and modules through the URL", () => {
    const params = serializeFilters({
      ...EMPTY_FILTERS,
      cycles: [C1, "none"],
      modules: [M1],
    });
    expect(params.get("cycles")).toBe(`${C1},none`);
    expect(params.get("modules")).toBe(M1);
    const back = parseFilters(params);
    expect(back.cycles).toEqual([C1, "none"]);
    expect(back.modules).toEqual([M1]);
  });

  it("keeps the 'none' sentinel for cycle and module", () => {
    const back = parseFilters(
      new URLSearchParams("cycles=none&modules=none"),
    );
    expect(back.cycles).toEqual(["none"]);
    expect(back.modules).toEqual(["none"]);
  });

  it("supports repeatable and comma-separated forms together", () => {
    const back = parseFilters(
      new URLSearchParams(`cycles=${C1}&cycles=${C2},none&modules=${M1},${C1}`),
    );
    expect(back.cycles).toEqual([C1, C2, "none"]);
    expect(back.modules).toEqual([M1, C1]);
  });

  it("drops malformed cycle/module values and never throws", () => {
    const back = parseFilters(
      new URLSearchParams(
        "cycles=garbage,,none,123&modules=not-a-uuid,&q=still-fine",
      ),
    );
    expect(back.cycles).toEqual(["none"]);
    expect(back.modules).toEqual([]);
    expect(back.q).toBe("still-fine");
  });

  it("emits the plural API names (never the singular scope params)", () => {
    const p = toApiParams({
      ...EMPTY_FILTERS,
      cycles: [C1, "none"],
      modules: [M1],
    });
    expect(p.get("cycles")).toBe(`${C1},none`);
    expect(p.get("modules")).toBe(M1);
    expect(p.get("cycle")).toBeNull();
    expect(p.get("module")).toBeNull();
  });

  it("omits empty cycle/module dimensions from the query string", () => {
    expect(serializeFilters(EMPTY_FILTERS).get("cycles")).toBeNull();
    expect(serializeFilters(EMPTY_FILTERS).get("modules")).toBeNull();
    expect(toApiParams(EMPTY_FILTERS).get("cycles")).toBeNull();
    expect(toApiParams(EMPTY_FILTERS).get("modules")).toBeNull();
  });

  it("counts cycle and module as separate badge dimensions", () => {
    expect(activeFilterCount({ ...EMPTY_FILTERS, cycles: [C1] })).toBe(1);
    expect(
      activeFilterCount({
        ...EMPTY_FILTERS,
        cycles: ["none"],
        modules: [M1],
      }),
    ).toBe(2);
  });

  it("recognizes cycles/modules as filter params (peek= still preserved)", () => {
    expect(isFilterParam("cycles")).toBe(true);
    expect(isFilterParam("modules")).toBe(true);
    expect(isFilterParam("peek")).toBe(false);
  });

  it("defaults both dimensions to empty (pre-C116 filter objects)", () => {
    expect(EMPTY_FILTERS.cycles).toEqual([]);
    expect(EMPTY_FILTERS.modules).toEqual([]);
    // A filter object built before C116 (no cycles/modules keys) merges
    // over EMPTY_FILTERS at the saved-view boundary; the spread below
    // mirrors that merge and must never leave the keys undefined.
    const legacy = { q: "x" } as Partial<IssueFilters>;
    const merged: IssueFilters = { ...EMPTY_FILTERS, ...legacy };
    expect(merged.cycles).toEqual([]);
    expect(merged.modules).toEqual([]);
  });
});

describe("release filter (C117T1)", () => {
  const R1 = "123e4567-e89b-12d3-a456-426614174030";
  const R2 = "123e4567-e89b-12d3-a456-426614174031";

  it("round-trips releases through the URL", () => {
    const params = serializeFilters({
      ...EMPTY_FILTERS,
      releases: [R1, "none"],
    });
    expect(params.get("releases")).toBe(`${R1},none`);
    const back = parseFilters(params);
    expect(back.releases).toEqual([R1, "none"]);
  });

  it("keeps the 'none' sentinel for releases", () => {
    const back = parseFilters(new URLSearchParams("releases=none"));
    expect(back.releases).toEqual(["none"]);
  });

  it("supports repeatable and comma-separated forms together", () => {
    const back = parseFilters(
      new URLSearchParams(`releases=${R1}&releases=${R2},none`),
    );
    expect(back.releases).toEqual([R1, R2, "none"]);
  });

  it("drops malformed release values and never throws", () => {
    const back = parseFilters(
      new URLSearchParams("releases=garbage,,none,123&q=still-fine"),
    );
    expect(back.releases).toEqual(["none"]);
    expect(back.q).toBe("still-fine");
  });

  it("emits the plural API name (never a singular release= param)", () => {
    const p = toApiParams({ ...EMPTY_FILTERS, releases: [R1, "none"] });
    expect(p.get("releases")).toBe(`${R1},none`);
    expect(p.get("release")).toBeNull();
  });

  it("omits the empty release dimension from the query string", () => {
    expect(serializeFilters(EMPTY_FILTERS).get("releases")).toBeNull();
    expect(toApiParams(EMPTY_FILTERS).get("releases")).toBeNull();
  });

  it("counts releases as one badge dimension", () => {
    expect(activeFilterCount({ ...EMPTY_FILTERS, releases: [R1] })).toBe(1);
    expect(
      activeFilterCount({
        ...EMPTY_FILTERS,
        releases: ["none"],
        modules: ["123e4567-e89b-12d3-a456-426614174004"],
      }),
    ).toBe(2);
  });

  it("recognizes releases as a filter param (peek= still preserved)", () => {
    expect(isFilterParam("releases")).toBe(true);
    expect(isFilterParam("peek")).toBe(false);
  });

  it("defaults the dimension to empty (pre-C117 filter objects)", () => {
    expect(EMPTY_FILTERS.releases).toEqual([]);
    const legacy = { q: "x" } as Partial<IssueFilters>;
    const merged: IssueFilters = { ...EMPTY_FILTERS, ...legacy };
    expect(merged.releases).toEqual([]);
  });
});

describe("created by filter (C118T1)", () => {
  const A1 = "123e4567-e89b-12d3-a456-426614174040";
  const A2 = "123e4567-e89b-12d3-a456-426614174041";

  it("round-trips createdBy through the URL", () => {
    const params = serializeFilters({
      ...EMPTY_FILTERS,
      createdBy: [A1, A2],
    });
    expect(params.get("created_by")).toBe(`${A1},${A2}`);
    const back = parseFilters(params);
    expect(back.createdBy).toEqual([A1, A2]);
  });

  it("supports repeatable and comma-separated forms together", () => {
    const back = parseFilters(
      new URLSearchParams(`created_by=${A1}&created_by=${A2},${A1}`),
    );
    expect(back.createdBy).toEqual([A1, A2, A1]);
  });

  it("drops malformed values — including 'none', which has no meaning for a NOT NULL column — and never throws", () => {
    const back = parseFilters(
      new URLSearchParams("created_by=garbage,,none,123&q=still-fine"),
    );
    expect(back.createdBy).toEqual([]);
    expect(back.q).toBe("still-fine");
  });

  it("emits the plan-contract API name (created_by) and no other form", () => {
    const p = toApiParams({ ...EMPTY_FILTERS, createdBy: [A1] });
    expect(p.get("created_by")).toBe(A1);
    expect(p.get("createdBy")).toBeNull();
    expect(p.get("author")).toBeNull();
  });

  it("omits the empty created-by dimension from the query string", () => {
    expect(serializeFilters(EMPTY_FILTERS).get("created_by")).toBeNull();
    expect(toApiParams(EMPTY_FILTERS).get("created_by")).toBeNull();
  });

  it("counts createdBy as one badge dimension", () => {
    expect(activeFilterCount({ ...EMPTY_FILTERS, createdBy: [A1] })).toBe(1);
    expect(
      activeFilterCount({
        ...EMPTY_FILTERS,
        createdBy: [A1],
        releases: ["123e4567-e89b-12d3-a456-426614174030"],
      }),
    ).toBe(2);
  });

  it("recognizes created_by as a filter param (peek= still preserved)", () => {
    expect(isFilterParam("created_by")).toBe(true);
    expect(isFilterParam("peek")).toBe(false);
  });

  it("defaults the dimension to empty (pre-C118 filter objects)", () => {
    expect(EMPTY_FILTERS.createdBy).toEqual([]);
    const legacy = { q: "x" } as Partial<IssueFilters>;
    const merged: IssueFilters = { ...EMPTY_FILTERS, ...legacy };
    expect(merged.createdBy).toEqual([]);
  });
});

describe("state group filter (C119T1)", () => {
  it("round-trips stateGroups through the URL", () => {
    const params = serializeFilters({
      ...EMPTY_FILTERS,
      stateGroups: ["started", "completed"],
    });
    expect(params.get("state_group")).toBe("started,completed");
    const back = parseFilters(params);
    expect(back.stateGroups).toEqual(["started", "completed"]);
  });

  it("supports repeatable and comma-separated forms together", () => {
    const back = parseFilters(
      new URLSearchParams("state_group=started&state_group=completed,backlog"),
    );
    expect(back.stateGroups).toEqual(["started", "completed", "backlog"]);
  });

  it("normalizes case and drops values outside the five-group vocabulary — including 'triage' and 'none' — and never throws", () => {
    const back = parseFilters(
      new URLSearchParams(
        "state_group=Started,TRIAGE,none,garbage,,unstarted&q=still-fine",
      ),
    );
    expect(back.stateGroups).toEqual(["started", "unstarted"]);
    expect(back.q).toBe("still-fine");
  });

  it("emits the plan-contract API name (state_group) and no other form", () => {
    const p = toApiParams({ ...EMPTY_FILTERS, stateGroups: ["started"] });
    expect(p.get("state_group")).toBe("started");
    expect(p.get("stateGroups")).toBeNull();
    expect(p.get("state_groups")).toBeNull();
  });

  it("omits the empty state-group dimension from the query string", () => {
    expect(serializeFilters(EMPTY_FILTERS).get("state_group")).toBeNull();
    expect(toApiParams(EMPTY_FILTERS).get("state_group")).toBeNull();
  });

  it("counts stateGroups as one badge dimension", () => {
    expect(
      activeFilterCount({ ...EMPTY_FILTERS, stateGroups: ["started"] }),
    ).toBe(1);
    expect(
      activeFilterCount({
        ...EMPTY_FILTERS,
        stateGroups: ["started"],
        createdBy: ["123e4567-e89b-12d3-a456-426614174040"],
      }),
    ).toBe(2);
  });

  it("recognizes state_group as a filter param (peek= still preserved)", () => {
    expect(isFilterParam("state_group")).toBe(true);
    expect(isFilterParam("peek")).toBe(false);
  });

  it("defaults the dimension to empty (pre-C119 filter objects)", () => {
    expect(EMPTY_FILTERS.stateGroups).toEqual([]);
    const legacy = { q: "x" } as Partial<IssueFilters>;
    const merged: IssueFilters = { ...EMPTY_FILTERS, ...legacy };
    expect(merged.stateGroups).toEqual([]);
  });
});

describe("mentions filter (C120T1)", () => {
  const M1 = "123e4567-e89b-12d3-a456-426614174060";
  const M2 = "123e4567-e89b-12d3-a456-426614174061";

  it("round-trips mentions through the URL, including 'none'", () => {
    const params = serializeFilters({
      ...EMPTY_FILTERS,
      mentions: [M1, "none"],
    });
    expect(params.get("mentions")).toBe(`${M1},none`);
    const back = parseFilters(params);
    expect(back.mentions).toEqual([M1, "none"]);
  });

  it("supports repeatable and comma-separated forms together", () => {
    const back = parseFilters(
      new URLSearchParams(`mentions=${M1}&mentions=${M2},${M1}`),
    );
    expect(back.mentions).toEqual([M1, M2, M1]);
  });

  it("passes 'none' through like assignees (not the created_by rejection) and drops malformed values, never throwing", () => {
    const back = parseFilters(
      new URLSearchParams("mentions=garbage,,none,123&q=still-fine"),
    );
    expect(back.mentions).toEqual(["none"]);
    expect(back.q).toBe("still-fine");
  });

  it("emits the plan-contract API name (mentions) and no other form", () => {
    const p = toApiParams({ ...EMPTY_FILTERS, mentions: [M1] });
    expect(p.get("mentions")).toBe(M1);
    expect(p.get("mentioned_by")).toBeNull();
    expect(p.get("mention")).toBeNull();
  });

  it("omits the empty mentions dimension from the query string", () => {
    expect(serializeFilters(EMPTY_FILTERS).get("mentions")).toBeNull();
    expect(toApiParams(EMPTY_FILTERS).get("mentions")).toBeNull();
  });

  it("counts mentions as one badge dimension", () => {
    expect(activeFilterCount({ ...EMPTY_FILTERS, mentions: [M1] })).toBe(1);
    expect(
      activeFilterCount({
        ...EMPTY_FILTERS,
        mentions: ["none"],
        createdBy: ["123e4567-e89b-12d3-a456-426614174040"],
      }),
    ).toBe(2);
  });

  it("recognizes mentions as a filter param (peek= still preserved)", () => {
    expect(isFilterParam("mentions")).toBe(true);
    expect(isFilterParam("peek")).toBe(false);
  });

  it("defaults the dimension to empty (pre-C120 filter objects)", () => {
    expect(EMPTY_FILTERS.mentions).toEqual([]);
    const legacy = { q: "x" } as Partial<IssueFilters>;
    const merged: IssueFilters = { ...EMPTY_FILTERS, ...legacy };
    expect(merged.mentions).toEqual([]);
  });
});
