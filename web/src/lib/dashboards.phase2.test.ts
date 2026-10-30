// C83T1: phase-2 lib helpers — my_access gating, pagination math, the
// semi-circular gauge arc geometry, intake tile definitions, duration
// formatting, and the CSV filename parse.
import { describe, expect, it } from "vitest";
import {
  canEditWidgets,
  canShareDashboard,
  dashboardAccess,
  exportFilename,
  formatDuration,
  INTAKE_TILES,
  EMAIL_INGESTION_NOTE,
  normalizeWidgetConfig,
  semiGaugeArc,
  widgetPageCount,
} from "./dashboards";

describe("dashboardAccess", () => {
  it("trusts the server's my_access when present", () => {
    expect(
      dashboardAccess(
        { created_by: "u-x", visibility: "public", my_access: "view" },
        "u-me",
        20,
      ),
    ).toBe("view");
    expect(
      dashboardAccess(
        { created_by: "u-x", visibility: "private", my_access: "edit" },
        "u-me",
        15,
      ),
    ).toBe("edit");
  });

  it("falls back to the creator/admin rule without my_access", () => {
    expect(
      dashboardAccess({ created_by: "u-me", visibility: "private" }, "u-me", 5),
    ).toBe("owner");
    expect(
      dashboardAccess({ created_by: "u-x", visibility: "public" }, "u-me", 20),
    ).toBe("edit");
    expect(
      dashboardAccess({ created_by: "u-x", visibility: "public" }, "u-me", 15),
    ).toBe("view");
    expect(
      dashboardAccess({ created_by: "u-x", visibility: "public" }, null, 20),
    ).toBe("view");
  });

  it("only the owner may share", () => {
    expect(canShareDashboard("owner")).toBe(true);
    expect(canShareDashboard("edit")).toBe(false);
    expect(canShareDashboard("view")).toBe(false);
  });

  it("owner and edit may edit widgets; view may not", () => {
    expect(canEditWidgets("owner")).toBe(true);
    expect(canEditWidgets("edit")).toBe(true);
    expect(canEditWidgets("view")).toBe(false);
  });
});

describe("widgetPageCount", () => {
  it("ceil-divides total_groups by max_rows", () => {
    expect(
      widgetPageCount({ groups: [], page: 1, max_rows: 10, total_groups: 25 }),
    ).toBe(3);
    expect(
      widgetPageCount({ groups: [], page: 1, max_rows: 10, total_groups: 10 }),
    ).toBe(1);
    expect(
      widgetPageCount({ groups: [], page: 1, max_rows: 10, total_groups: 0 }),
    ).toBe(1);
  });

  it("returns 1 when the envelope is absent", () => {
    expect(widgetPageCount(undefined)).toBe(1);
    expect(widgetPageCount({ value: 3 })).toBe(1);
  });
});

describe("semiGaugeArc", () => {
  it("clamps the percentage to 0..100", () => {
    const neg = semiGaugeArc(-5, 40);
    expect(neg.endX).toBeCloseTo(0, 6);
    expect(neg.endY).toBeCloseTo(40, 6);
    const over = semiGaugeArc(120, 40);
    expect(over.endX).toBeCloseTo(80, 6);
    expect(over.endY).toBeCloseTo(40, 6);
  });

  it("ends at the top for 50% and sweeps the full half for 100%", () => {
    const half = semiGaugeArc(50, 40);
    expect(half.endX).toBeCloseTo(40, 6);
    expect(half.endY).toBeCloseTo(0, 6);
    const full = semiGaugeArc(100, 40);
    expect(full.endX).toBeCloseTo(80, 6);
    expect(full.endY).toBeCloseTo(40, 6);
    // Large-arc flag flips past the halfway point.
    expect(full.path).toContain("A 40 40 0 1 1");
    expect(semiGaugeArc(25, 40).path).toContain("A 40 40 0 0 1");
  });

  it("starts the path at the left endpoint of the diameter", () => {
    expect(semiGaugeArc(33, 50).path.startsWith("M 0 50")).toBe(true);
  });
});

describe("INTAKE_TILES", () => {
  it("defines the nine tiles in prototype order (funnel tiles before avg)", () => {
    expect(INTAKE_TILES.map((t) => t.key)).toEqual([
      "in_app_volume",
      "form_volume",
      "email_volume",
      "total_accepted",
      "total_rejected",
      "total_duplicate",
      "pending",
      "snoozed",
      "avg_decide_seconds",
    ]);
  });

  it("maps every tile except avg to a drill-down metric", () => {
    expect(INTAKE_TILES.map((t) => t.metric ?? null)).toEqual([
      "in_app",
      "form",
      "email",
      "accepted",
      "rejected",
      "duplicate",
      "pending",
      "snoozed",
      null,
    ]);
  });

  it("marks only the email tile honest-empty and the duration tile", () => {
    expect(INTAKE_TILES.filter((t) => t.honestEmpty)).toHaveLength(1);
    expect(INTAKE_TILES.find((t) => t.honestEmpty)?.label).toBe(
      "Email volume",
    );
    expect(INTAKE_TILES.filter((t) => t.duration)).toHaveLength(1);
    expect(EMAIL_INGESTION_NOTE).toContain("no email ingestion");
  });
});

describe("formatDuration", () => {
  it("formats seconds compactly", () => {
    expect(formatDuration(45)).toBe("45s");
    expect(formatDuration(90)).toBe("1m 30s");
    expect(formatDuration(120)).toBe("2m");
    expect(formatDuration(5400)).toBe("1h 30m");
    expect(formatDuration(7200)).toBe("2h");
    expect(formatDuration(90000)).toBe("1d 1h");
    expect(formatDuration(86400)).toBe("1d");
  });

  it("returns an em dash for non-finite or negative input", () => {
    expect(formatDuration(NaN)).toBe("—");
    expect(formatDuration(-3)).toBe("—");
  });
});

describe("exportFilename", () => {
  it("parses the filename out of Content-Disposition", () => {
    expect(
      exportFilename(
        'attachment; filename="sprint-health-completion.csv"',
        "fallback.csv",
      ),
    ).toBe("sprint-health-completion.csv");
  });

  it("falls back when the header is missing or malformed", () => {
    expect(exportFilename(null, "fallback.csv")).toBe("fallback.csv");
    expect(exportFilename("attachment", "fallback.csv")).toBe("fallback.csv");
  });
});

describe("normalizeWidgetConfig (phase 2 defaults)", () => {
  it("applies the backend's presentation defaults", () => {
    const n = normalizeWidgetConfig({});
    expect(n.max_rows).toBe(10);
    expect(n.visualization).toBe("grid");
    expect(n.gauge_color).toBe("#3b82f6");
    expect(n.show_percentage).toBe(true);
    expect(n.view).toBe("grid");
  });

  it("keeps provided phase-2 values and clamps max_rows", () => {
    const n = normalizeWidgetConfig({
      metric: "estimate_points",
      max_rows: 25,
      visualization: "table",
      gauge_color: "#ef4444",
      show_percentage: false,
      view: "table",
    });
    expect(n.metric).toBe("estimate_points");
    expect(n.max_rows).toBe(25);
    expect(n.visualization).toBe("table");
    expect(n.gauge_color).toBe("#ef4444");
    expect(n.show_percentage).toBe(false);
    expect(n.view).toBe("table");
  });

  it("clamps out-of-range max_rows back to the default", () => {
    expect(normalizeWidgetConfig({ max_rows: 0 }).max_rows).toBe(10);
    expect(normalizeWidgetConfig({ max_rows: 101 }).max_rows).toBe(10);
  });
});
