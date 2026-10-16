// @vitest-environment jsdom
// C30T3: risk lib — level metadata + drill-down hrefs onto the established
// Issues filter schema (filters.ts). No new filter mechanism: overdue maps
// to due_before (target_date < today), stalled to updated_before (7d ago,
// the updated_at side of the backend's activity-log staleness), unassigned
// to assignee=none, overload to the Workload page.
import { describe, expect, it } from "vitest";
import {
  RISK_LEVEL_META,
  riskDrilldownHref,
  riskDrilldownLabel,
  type ProjectRisk,
} from "./risk";

const project: Pick<ProjectRisk, "identifier"> = { identifier: "API" };
// Local noon — date math below is timezone-safe.
const NOW = new Date(2026, 9, 16, 12, 0, 0);

describe("RISK_LEVEL_META", () => {
  it("labels and bands all four levels (prototype values)", () => {
    expect(RISK_LEVEL_META.healthy).toMatchObject({
      label: "Healthy",
      band: "score 0–24",
    });
    expect(RISK_LEVEL_META.watch).toMatchObject({
      label: "Watch",
      band: "score 25–49",
    });
    expect(RISK_LEVEL_META["at-risk"]).toMatchObject({
      label: "At-risk",
      band: "score 50–74",
    });
    expect(RISK_LEVEL_META.critical).toMatchObject({
      label: "Critical",
      band: "score 75–100",
    });
  });
});

describe("riskDrilldownHref", () => {
  it("overdue → Issues with due_before=yesterday (target_date < today)", () => {
    expect(riskDrilldownHref("acme", project, "overdue", NOW)).toBe(
      "/w/acme/p/API?due_before=2026-10-15",
    );
  });

  it("stalled → Issues with updated_before=7 days ago", () => {
    expect(riskDrilldownHref("acme", project, "stalled", NOW)).toBe(
      "/w/acme/p/API?updated_before=2026-10-09",
    );
  });

  it("unassigned → Issues with assignee=none", () => {
    expect(riskDrilldownHref("acme", project, "unassigned", NOW)).toBe(
      "/w/acme/p/API?assignee=none",
    );
  });

  it("overload → the Workload page", () => {
    expect(riskDrilldownHref("acme", project, "overload", NOW)).toBe(
      "/w/acme/workload",
    );
  });

  it("unknown factor key → null (no link rendered)", () => {
    expect(riskDrilldownHref("acme", project, "nope", NOW)).toBeNull();
  });
});

describe("riskDrilldownLabel", () => {
  it("labels each factor's drill-down (prototype wording)", () => {
    expect(riskDrilldownLabel("overdue")).toBe("View overdue issues");
    expect(riskDrilldownLabel("stalled")).toBe("View stalled issues");
    expect(riskDrilldownLabel("unassigned")).toBe("View unassigned issues");
    expect(riskDrilldownLabel("overload")).toBe("Open workload view");
    expect(riskDrilldownLabel("nope")).toBeNull();
  });
});
