import { describe } from "vitest";
import { expect, it } from "vitest";
import {
  describeChange,
  fieldLabel,
  formatValue,
  matchesActivityFilter,
  scalarText,
  type ActivityEntry,
} from "./activity";

const states = new Map([["state-1", "In Progress"]]);

function entry(over: Partial<ActivityEntry> = {}): ActivityEntry {
  return {
    at: "2026-10-09T06:00:00Z",
    actor: "Ada Member",
    issue_uuid: "issue-uuid-1",
    issue_identifier: "ACT-1",
    field: "priority",
    old: "0",
    new: "3",
    ...over,
  };
}

describe("scalarText", () => {
  it("unquotes JSON strings", () => {
    expect(scalarText('"hello"')).toBe("hello");
  });
  it("passes numbers and dates through", () => {
    expect(scalarText("3")).toBe("3");
    expect(scalarText('"2026-10-09"')).toBe("2026-10-09");
  });
  it("maps NULL, JSON null, and empty to null", () => {
    expect(scalarText(null)).toBeNull();
    expect(scalarText(undefined)).toBeNull();
    expect(scalarText("null")).toBeNull();
    expect(scalarText("")).toBeNull();
  });
});

describe("fieldLabel", () => {
  it("maps known audit fields", () => {
    expect(fieldLabel("state_id")).toBe("state");
    expect(fieldLabel("target_date")).toBe("target date");
  });
  it("passes unknown fields through", () => {
    expect(fieldLabel("custom_xyz")).toBe("custom_xyz");
  });
});

describe("formatValue", () => {
  it("resolves state names, falls back to short id", () => {
    expect(formatValue("state_id", '"state-1"', states)).toBe("In Progress");
    expect(formatValue("state_id", '"gone-state-uuid-here"', states)).toBe(
      "gone-sta",
    );
  });
  it("maps priority numbers to labels", () => {
    expect(formatValue("priority", "3", states)).toBe("High");
    expect(formatValue("priority", "0", states)).toBe("None");
  });
  it("returns null for description docs and unset values", () => {
    expect(formatValue("description", '{"a":1}', states)).toBeNull();
    expect(formatValue("priority", null, states)).toBeNull();
    expect(formatValue("priority", "null", states)).toBeNull();
  });
  it("renders draft flags in words", () => {
    expect(formatValue("is_draft", "true", states)).toBe("draft");
    expect(formatValue("is_draft", "false", states)).toBe("not a draft");
  });
});

describe("describeChange", () => {
  it("classifies created / deleted", () => {
    expect(describeChange(entry({ field: "_created" }), states).kind).toBe(
      "created",
    );
    expect(describeChange(entry({ field: "_deleted" }), states).kind).toBe(
      "deleted",
    );
  });
  it("classifies a set (no old value)", () => {
    const d = describeChange(entry({ old: null, new: '"2026-10-12"', field: "target_date" }), states);
    expect(d.kind).toBe("set");
    expect(d.newText).toBe("2026-10-12");
  });
  it("classifies a clear (no new value)", () => {
    const d = describeChange(
      entry({ old: '"2026-10-12"', new: null, field: "target_date" }),
      states,
    );
    expect(d.kind).toBe("cleared");
  });
  it("classifies a plain change", () => {
    const d = describeChange(entry(), states);
    expect(d.kind).toBe("changed");
    expect(d.label).toBe("priority");
    expect(d.oldText).toBe("None");
    expect(d.newText).toBe("High");
  });
  it("resolves state ids through the lookup", () => {
    const d = describeChange(
      entry({ field: "state_id", old: '"s-old"', new: '"state-1"' }),
      states,
    );
    expect(d.oldText).toBe("s-old");
    expect(d.newText).toBe("In Progress");
  });
});

describe("matchesActivityFilter", () => {
  const e = entry();
  it("matches identifier, actor, and field label", () => {
    expect(matchesActivityFilter(e, "act-1")).toBe(true);
    expect(matchesActivityFilter(e, "ada")).toBe(true);
    expect(matchesActivityFilter(e, "PRIORITY")).toBe(true);
  });
  it("empty query matches everything; junk matches nothing", () => {
    expect(matchesActivityFilter(e, "  ")).toBe(true);
    expect(matchesActivityFilter(e, "zzz-nope")).toBe(false);
  });
});
