// C81T1: dashboards lib — types/API surface, edit-rule gate, and the
// widget-data transforms the charts render.
import { describe, expect, it } from "vitest";
import {
  EMPTY_WIDGET_FILTERS,
  WIDGET_TYPES,
  canEditDashboard,
  chartSeries,
  filterSummary,
  isChartType,
  isGroupedType,
  isPaginatedType,
  layoutPatchBody,
  normalizeWidgetConfig,
  numberValue,
  seriesColor,
} from "./dashboards";

const PUB = { created_by: "u-creator", visibility: "public" } as const;
const PRIV = { created_by: "u-creator", visibility: "private" } as const;

describe("canEditDashboard", () => {
  it("lets the creator edit regardless of visibility or role", () => {
    expect(canEditDashboard(PRIV, "u-creator", 5)).toBe(true);
    expect(canEditDashboard(PUB, "u-creator", 5)).toBe(true);
  });

  it("lets a workspace admin edit public dashboards", () => {
    expect(canEditDashboard(PUB, "u-admin", 20)).toBe(true);
  });

  it("denies an admin on a private dashboard they did not create", () => {
    expect(canEditDashboard(PRIV, "u-admin", 20)).toBe(false);
  });

  it("denies a plain member on someone else's public dashboard", () => {
    expect(canEditDashboard(PUB, "u-member", 15)).toBe(false);
  });

  it("denies viewers and guests", () => {
    expect(canEditDashboard(PUB, "u-viewer", 10)).toBe(false);
    expect(canEditDashboard(PUB, "u-guest", 5)).toBe(false);
  });

  it("denies when there is no signed-in user", () => {
    expect(canEditDashboard(PUB, null, 20)).toBe(false);
  });
});

describe("normalizeWidgetConfig", () => {
  it("fills empty filters and project scope", () => {
    const n = normalizeWidgetConfig({});
    expect(n.filters).toEqual(EMPTY_WIDGET_FILTERS);
    expect(n.project_ids).toEqual([]);
    expect(n.metric).toBe("count");
    expect(n.group_by).toBeUndefined();
  });

  it("keeps provided values", () => {
    const n = normalizeWidgetConfig({
      group_by: "state",
      filters: { priorities: [3, 4] },
      project_ids: ["p1"],
    });
    expect(n.group_by).toBe("state");
    expect(n.filters.priorities).toEqual([3, 4]);
    expect(n.filters.assignees).toEqual([]);
    expect(n.project_ids).toEqual(["p1"]);
  });
});

describe("chartSeries", () => {
  it("maps groups to recharts rows", () => {
    expect(
      chartSeries([
        { key: "s1", label: "Backlog", value: 4 },
        { key: "s2", label: "Done", value: 9 },
      ]),
    ).toEqual([
      { name: "Backlog", value: 4 },
      { name: "Done", value: 9 },
    ]);
  });

  it("returns [] for undefined groups", () => {
    expect(chartSeries(undefined)).toEqual([]);
  });
});

describe("numberValue", () => {
  it("returns the scalar", () => {
    expect(numberValue({ value: 42 })).toBe(42);
  });

  it("returns null when the backend sent no value", () => {
    expect(numberValue({})).toBeNull();
    expect(numberValue(undefined)).toBeNull();
  });
});

describe("layoutPatchBody", () => {
  it("commits exactly the four grid keys on drop", () => {
    expect(
      layoutPatchBody({
        grid_x: 2,
        grid_y: 1,
        grid_w: 6,
        grid_h: 4,
        // @ts-expect-error — extra keys must not leak into the body
        title: "nope",
      }),
    ).toEqual({ grid_x: 2, grid_y: 1, grid_w: 6, grid_h: 4 });
  });
});

describe("isChartType / WIDGET_TYPES", () => {
  it("lists all twelve widget types", () => {
    expect(WIDGET_TYPES).toEqual([
      "bar",
      "line",
      "area",
      "donut",
      "pie",
      "number",
      "smart_counter",
      "smart_gauge",
      "table_2d",
      "intake_breakdown",
      "work_items_table",
      "assigned_to_me",
    ]);
  });

  it("treats exactly the five recharts types as charts", () => {
    for (const t of WIDGET_TYPES) {
      expect(isChartType(t)).toBe(
        ["bar", "line", "area", "donut", "pie"].includes(t),
      );
    }
  });

  it("requires group_by for charts and the grouped phase-2 types", () => {
    for (const t of [
      "bar",
      "line",
      "area",
      "donut",
      "pie",
      "smart_counter",
      "smart_gauge",
      "table_2d",
    ] as const) {
      expect(isGroupedType(t)).toBe(true);
    }
    expect(isGroupedType("number")).toBe(false);
    expect(isGroupedType("intake_breakdown")).toBe(false);
  });

  it("paginates exactly the smart types and table_2d", () => {
    for (const t of [
      "smart_counter",
      "smart_gauge",
      "table_2d",
    ] as const) {
      expect(isPaginatedType(t)).toBe(true);
    }
    for (const t of [
      "bar",
      "line",
      "area",
      "donut",
      "pie",
      "number",
      "intake_breakdown",
    ] as const) {
      expect(isPaginatedType(t)).toBe(false);
    }
  });
});

describe("seriesColor", () => {
  it("cycles the palette deterministically", () => {
    expect(seriesColor(0)).toBe(seriesColor(10));
    expect(seriesColor(1)).not.toBe(seriesColor(0));
  });
});

describe("filterSummary", () => {
  const lookups = {
    assigneeName: (id: string) => (id === "u1" ? "Galih" : undefined),
    stateName: (id: string) => (id === "s1" ? "Backlog" : undefined),
    labelName: (id: string) => (id === "l1" ? "bug" : undefined),
    projectName: (id: string) => (id === "p1" ? "Web" : undefined),
  };

  it("describes an unfiltered widget", () => {
    expect(filterSummary({}, lookups)).toBe("All issues, all projects");
  });

  it("summarizes set filters with resolved names", () => {
    const s = filterSummary(
      {
        project_ids: ["p1"],
        filters: { assignees: ["u1"], priorities: [4], states: ["s1"], labels: ["l1"] },
      },
      lookups,
    );
    expect(s).toContain("Projects: Web");
    expect(s).toContain("Assignees: Galih");
    expect(s).toContain("Priorities: 4");
    expect(s).toContain("States: Backlog");
    expect(s).toContain("Labels: bug");
  });

  it("falls back to raw ids for unknown lookups", () => {
    expect(
      filterSummary({ filters: { assignees: ["unknown-id"] } }, lookups),
    ).toContain("unknown-id");
  });
});
