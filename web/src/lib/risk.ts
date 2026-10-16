/** Risk radar (C30T3). Backend shapes mirror internal/service/risk.go;
 *  pure helpers are covered by risk.test.ts. */

import { api } from "./api";

export type RiskLevel = "healthy" | "watch" | "at-risk" | "critical";

export interface RiskFactor {
  key: string;
  label: string;
  /** Share in [0,1], rounded to 3 decimals by the backend. */
  value: number;
  detail: string;
}

export interface ProjectRisk {
  project_id: string;
  identifier: string;
  name: string;
  open_count: number;
  risk_score: number;
  risk_level: RiskLevel;
  /** Descending by score contribution; empty for a fully healthy project. */
  factors: RiskFactor[];
}

export interface RiskReport {
  projects: ProjectRisk[];
  generated_at: string;
}

/** GET /api/v1/workspaces/:slug/risk — per-project risk report. */
export function fetchRisk(slug: string): Promise<RiskReport> {
  return api.get<RiskReport>(
    `/api/v1/workspaces/${encodeURIComponent(slug)}/risk`,
  );
}

/** Level metadata: prototype colors/bands (risk-radar.html). */
export const RISK_LEVEL_META: Record<
  RiskLevel,
  { label: string; band: string; color: string; bg: string }
> = {
  healthy: {
    label: "Healthy",
    band: "score 0–24",
    color: "#42c88a",
    bg: "rgba(66,200,138,0.18)",
  },
  watch: {
    label: "Watch",
    band: "score 25–49",
    color: "#e7ad42",
    bg: "rgba(231,173,66,0.18)",
  },
  "at-risk": {
    label: "At-risk",
    band: "score 50–74",
    color: "#f08c3c",
    bg: "rgba(240,140,60,0.18)",
  },
  critical: {
    label: "Critical",
    band: "score 75–100",
    color: "#df6b77",
    bg: "rgba(223,107,119,0.20)",
  },
};

/** Local YYYY-MM-DD (the Issues date filters take calendar days). */
function formatDay(d: Date): string {
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, "0");
  const day = String(d.getDate()).padStart(2, "0");
  return `${y}-${m}-${day}`;
}

function daysBefore(from: Date, n: number): Date {
  const d = new Date(from);
  d.setDate(d.getDate() - n);
  return d;
}

/**
 * Drill-down URL for one factor, expressed ONLY with the established
 * Issues filter schema (lib/filters.ts) — no new filter mechanism:
 * - overdue → due_before=yesterday (overdue = target_date < today, and
 *   due_before is inclusive of the whole day)
 * - stalled → updated_before=7 days ago (the updated_at side of the
 *   backend's activity-log staleness; an approximation — an issue with
 *   recent activity but a stale updated_at can differ)
 * - unassigned → assignee=none
 * - overload → the Workload page (member-level, not an issue filter)
 * Unknown keys → null (the row renders without a link).
 */
export function riskDrilldownHref(
  slug: string,
  project: { identifier: string },
  factorKey: string,
  now: Date = new Date(),
): string | null {
  const base = `/w/${encodeURIComponent(slug)}/p/${encodeURIComponent(project.identifier)}`;
  switch (factorKey) {
    case "overdue":
      return `${base}?due_before=${formatDay(daysBefore(now, 1))}`;
    case "stalled":
      return `${base}?updated_before=${formatDay(daysBefore(now, 7))}`;
    case "unassigned":
      return `${base}?assignee=none`;
    case "overload":
      return `/w/${encodeURIComponent(slug)}/workload`;
    default:
      return null;
  }
}

/** Drill-down link label per factor (prototype wording); null = no link. */
export function riskDrilldownLabel(factorKey: string): string | null {
  switch (factorKey) {
    case "overdue":
      return "View overdue issues";
    case "stalled":
      return "View stalled issues";
    case "unassigned":
      return "View unassigned issues";
    case "overload":
      return "Open workload view";
    default:
      return null;
  }
}
