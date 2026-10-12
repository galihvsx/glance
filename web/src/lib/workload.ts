/** Workload & capacity (C29T1). Backend shapes mirror
 *  internal/service/workload.go; pure helpers are covered by
 *  workload.test.ts. */

import { api } from "./api";

export interface WorkloadPriority {
  urgent: number;
  high: number;
  medium: number;
  low: number;
  none: number;
}

export interface WorkloadMember {
  user_id: string;
  display_name: string;
  role: number;
  /** Null when unset — unset members never trip the overloaded flag. */
  weekly_capacity_points: number | null;
  open_count: number;
  open_points: number;
  overdue_count: number;
  priority: WorkloadPriority;
  completed_14d: number;
  overloaded: boolean;
  project_ids: string[];
}

export interface WorkloadUnassigned {
  project_id: string;
  project_identifier: string;
  open_count: number;
}

export interface WorkloadReport {
  members: WorkloadMember[];
  unassigned: WorkloadUnassigned[];
  generated_at: string;
}

/** GET /api/v1/workspaces/:slug/workload[?project=<uuid>...] — project ids
 *  are sent as repeatable `project` params. Empty list = all projects. */
export function fetchWorkload(
  slug: string,
  projectIds: string[],
): Promise<WorkloadReport> {
  const params = new URLSearchParams();
  for (const id of projectIds) params.append("project", id);
  const qs = params.toString();
  return api.get<WorkloadReport>(
    `/api/v1/workspaces/${encodeURIComponent(slug)}/workload${qs ? `?${qs}` : ""}`,
  );
}

/** 0–1+ load fraction of open estimate points vs weekly capacity.
 *  Null when capacity is unset or zero (no capacity bar to draw). */
export function loadFraction(member: WorkloadMember): number | null {
  const cap = member.weekly_capacity_points;
  if (cap == null || cap <= 0) return null;
  return member.open_points / cap;
}

/** Bar color for a load fraction: green → amber (≥80% of capacity) →
 *  red (overloaded); neutral blue when no capacity is set. */
export function workloadBarColor(fraction: number | null): string {
  if (fraction == null) return "#60a5fa"; // accent blue (no capacity set)
  if (fraction > 1) return "#f87171"; // overloaded — red
  if (fraction >= 0.8) return "#fbbf24"; // near capacity — amber
  return "#42c88a"; // healthy — green
}
