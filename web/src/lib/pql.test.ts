// C91T1: PQL frontend helpers + filter-state round-trip for `pql`.
import { describe, expect, it } from "vitest";
import { ApiError } from "./api";
import {
  activeFilterCount,
  EMPTY_FILTERS,
  isFilterParam,
  parseFilters,
  serializeFilters,
} from "./filters";
import {
  buildPqlRequestParams,
  formatPqlError,
  PQL_CHEAT_SHEET,
  pqlErrorPosition,
  pqlOf,
} from "./pql";

describe("buildPqlRequestParams (exclusivity rule)", () => {
  it("sends ONLY pql plus pagination/ordering — no visual params", () => {
    const p = buildPqlRequestParams("priority = High AND NOT hasNoAssignee()", {
      orderBy: "-updated_at",
      perPage: 25,
      cursor: "abc",
    });
    expect([...p.keys()].sort()).toEqual([
      "cursor",
      "order_by",
      "per_page",
      "pql",
    ]);
  });

  it("carries the exact query, pagination, ordering, and cursor", () => {
    const p = buildPqlRequestParams("state = Todo", {
      orderBy: "-updated_at",
      perPage: 25,
      cursor: "abc",
    });
    expect(p.get("pql")).toBe("state = Todo");
    expect(p.get("per_page")).toBe("25");
    expect(p.get("order_by")).toBe("-updated_at");
    expect(p.get("cursor")).toBe("abc");
    // No visual dimension may ride along, whatever the caller had set.
    for (const k of [
      "q",
      "state",
      "priority",
      "label",
      "assignee",
      "estimate",
      "parent",
      "epic",
      "created_after",
      "due_after",
      "subscribed",
      "archived",
    ]) {
      expect(p.has(k)).toBe(false);
    }
  });

  it("omits cursor when absent", () => {
    const p = buildPqlRequestParams("state = Todo", {
      orderBy: "-updated_at",
      perPage: 25,
    });
    expect(p.has("cursor")).toBe(false);
  });
});

describe("pqlOf", () => {
  it("normalizes absent/blank to empty and trims", () => {
    expect(pqlOf(undefined)).toBe("");
    expect(pqlOf(null)).toBe("");
    expect(pqlOf({})).toBe("");
    expect(pqlOf({ pql: "   " })).toBe("");
    expect(pqlOf({ pql: "  state = Todo " })).toBe("state = Todo");
  });
});

describe("formatPqlError", () => {
  it("appends the server column to the verbatim message", () => {
    const err = new ApiError(400, 'unknown state "Foo"', "bad_request", {
      position: 24,
    });
    expect(formatPqlError(err)).toBe('unknown state "Foo" (at column 24)');
    expect(pqlErrorPosition(err)).toBe(24);
  });

  it("leaves a message that already carries a column untouched", () => {
    const err = new ApiError(400, "expected value (at column 9)", "bad_request", {
      position: 9,
    });
    expect(formatPqlError(err)).toBe("expected value (at column 9)");
  });

  it("shows the message verbatim when the server names no column", () => {
    const err = new ApiError(400, "unknown label", "bad_request", undefined);
    expect(formatPqlError(err)).toBe("unknown label");
    expect(pqlErrorPosition(err)).toBeNull();
  });

  it("falls back to generic Error messages and a safe default", () => {
    expect(formatPqlError(new Error("boom"))).toBe("boom");
    expect(formatPqlError("weird")).toBe("Failed to apply the PQL query");
  });
});

describe("PQL_CHEAT_SHEET (documented field table)", () => {
  it("covers every T0 field, both functions, and the boolean operators", () => {
    const text = PQL_CHEAT_SHEET.map(
      (r) => `${r.example} ${r.meaning}`,
    ).join("\n");
    for (const field of [
      "state",
      "priority",
      "assignee",
      "label",
      "epic",
      "estimate",
      "parent",
      "due",
      "start",
      "created",
      "updated",
    ]) {
      expect(text).toContain(field);
    }
    expect(text).toContain("hasNoAssignee()");
    expect(text).toContain("hasAssignee()");
    expect(text).toContain("hasParent()");
    expect(text).toContain("hasChildren()");
    expect(text).toContain("AND");
    expect(text).toContain("OR");
    expect(text).toContain("NOT");
  });
});

describe("IssueFilters pql round-trip (view payload)", () => {
  it("parses ?pql= and serializes it back unchanged", () => {
    const parsed = parseFilters(
      new URLSearchParams("pql=priority+%3D+High&state=some-uuid"),
    );
    expect(parsed.pql).toBe("priority = High");
    const back = serializeFilters({ ...EMPTY_FILTERS, pql: "state = Todo" });
    expect(back.get("pql")).toBe("state = Todo");
    expect(parseFilters(back).pql).toBe("state = Todo");
  });

  it("treats blank pql as absent and old views as pql-free", () => {
    expect(pqlOf(parseFilters(new URLSearchParams("pql=")))).toBe("");
    expect(pqlOf(parseFilters(new URLSearchParams("")))).toBe("");
    // EMPTY_FILTERS (the shape of a pre-C91 saved view) serializes
    // with no pql key at all — old views load exactly as before.
    expect(serializeFilters(EMPTY_FILTERS).has("pql")).toBe(false);
    expect(serializeFilters({ ...EMPTY_FILTERS, pql: "  " }).has("pql")).toBe(
      false,
    );
  });

  it("counts a PQL query as one active filter dimension", () => {
    expect(activeFilterCount(EMPTY_FILTERS)).toBe(0);
    expect(activeFilterCount({ ...EMPTY_FILTERS, pql: "state = Todo" })).toBe(
      1,
    );
    expect(activeFilterCount({ ...EMPTY_FILTERS, pql: "  " })).toBe(0);
  });

  it("registers pql as a filter param (view apply + clear honor it)", () => {
    expect(isFilterParam("pql")).toBe(true);
  });
});
