import { describe, expect, it } from "vitest";
import {
  EMPTY_FILTERS,
  parseFilters,
  serializeFilters,
  type IssueFilters,
} from "../../lib/filters";
import {
  sanitizeView,
  type SavedView,
} from "./useSavedViews";
import { DEFAULT_DISPLAY_SETTINGS } from "./useDisplaySettings";

const FULL_FILTERS: IssueFilters = {
  ...EMPTY_FILTERS,
  q: "login bug",
  priorities: [3, 1],
  assignees: ["user-uuid-1", "none"],
  createdAfter: "2026-10-01",
  subscribed: true,
};

function makeView(overrides: Partial<SavedView> = {}): SavedView {
  return {
    id: "view-1",
    name: "My bugs",
    filterQuery: serializeFilters(FULL_FILTERS).toString(),
    display: {
      ...DEFAULT_DISPLAY_SETTINGS,
      groupBy: "priority",
      showEmptyGroups: false,
    },
    isDefault: false,
    ...overrides,
  };
}

describe("sanitizeView", () => {
  it("keeps a well-formed view intact", () => {
    const v = makeView();
    expect(sanitizeView(v)).toEqual(v);
  });

  it("drops garbage (non-objects, missing id/name, bad query)", () => {
    expect(sanitizeView(null)).toBeNull();
    expect(sanitizeView("nope")).toBeNull();
    expect(sanitizeView({ ...makeView(), id: "" })).toBeNull();
    expect(sanitizeView({ ...makeView(), name: "   " })).toBeNull();
    expect(sanitizeView({ ...makeView(), filterQuery: 42 })).toBeNull();
  });

  it("trims and caps the name", () => {
    const v = sanitizeView(
      makeView({ name: "  padded  " }),
    );
    expect(v?.name).toBe("padded");
    const long = sanitizeView(makeView({ name: "x".repeat(200) }));
    expect(long?.name).toHaveLength(64);
  });

  it("sanitizes a corrupt display object back to safe values", () => {
    const v = sanitizeView(
      makeView({
        display: {
          fields: { state: "yes", priority: true } as never,
          groupBy: "nonsense",
          orderBy: "hacked",
          showEmptyGroups: "maybe",
        } as never,
      }),
    );
    expect(v?.display).toEqual({
      ...DEFAULT_DISPLAY_SETTINGS,
      fields: { ...DEFAULT_DISPLAY_SETTINGS.fields },
    });
  });

  it("accepts a null display (spreadsheet-saved view)", () => {
    const v = sanitizeView(makeView({ display: null }));
    expect(v?.display).toBeNull();
  });

  it("round-trips filters through the stored verbatim query string", () => {
    const v = makeView();
    const restored = parseFilters(new URLSearchParams(v.filterQuery));
    expect(restored).toEqual({
      ...FULL_FILTERS,
      // serializeFilters sorts priorities; parse restores the sorted order
      priorities: [1, 3],
    });
  });
});
