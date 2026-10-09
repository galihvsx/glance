// Time reports client (C6T8).
//
// Contract: GET .../projects/:identifier/time/summary?days=30&group_by=day
// → {days, group_by, total_seconds, buckets:[{key,label,seconds,entries}]}.
// Completed entries only (running timers excluded, same as the per-issue
// total_seconds convention).

import { api } from "./api";

export type TimeGroupBy = "day" | "week" | "user" | "issue";

export interface TimeSummaryBucket {
  key: string;
  label: string;
  seconds: number;
  entries: number;
}

export interface TimeSummary {
  days: number;
  group_by: TimeGroupBy;
  total_seconds: number;
  buckets: TimeSummaryBucket[];
}

export function fetchTimeSummary(
  slug: string,
  identifier: string,
  days: number,
  groupBy: TimeGroupBy,
): Promise<TimeSummary> {
  const q = new URLSearchParams({
    days: String(days),
    group_by: groupBy,
  });
  return api.get<TimeSummary>(
    `/api/v1/workspaces/${encodeURIComponent(slug)}` +
      `/projects/${encodeURIComponent(identifier)}/time/summary?${q}`,
  );
}

export function timeSummaryKey(
  slug: string,
  identifier: string,
  days: number,
  groupBy: TimeGroupBy,
) {
  return ["time-summary", slug, identifier, days, groupBy] as const;
}

/** formatDuration renders seconds as "3h 24m" / "45m" / "20s". */
export function formatDuration(totalSeconds: number): string {
  const s = Math.round(totalSeconds);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  const rest = m % 60;
  return rest === 0 ? `${h}h` : `${h}h ${rest}m`;
}
