/**
 * Pure date-bucketing logic for the Roadmap view (C9T0). Kept UI-free so it
 * is unit-testable.
 *
 * The backend stores DATE columns serialized as RFC3339 UTC midnight
 * ("2026-10-05T00:00:00Z") — the day is sliced off the string directly so
 * bucketing never shifts with the viewer's timezone (same convention as
 * calendar.ts / gantt.ts).
 */

export interface MonthBucket {
  /** First day of the month ("YYYY-MM-01"), the sort key. */
  month: string;
  /** Human label, e.g. "October 2026". */
  label: string;
}

/** First day ("YYYY-MM-01") of each month in [startMonth, startMonth + count). */
export function roadmapMonths(startMonth: string, count: number): MonthBucket[] {
  const [y, m] = startMonth.split("-").map(Number);
  const out: MonthBucket[] = [];
  for (let i = 0; i < count; i++) {
    const d = new Date(Date.UTC(y, m - 1 + i, 1));
    const key = `${d.getUTCFullYear()}-${String(d.getUTCMonth() + 1).padStart(2, "0")}-01`;
    const label = d.toLocaleDateString(undefined, {
      month: "long",
      year: "numeric",
      timeZone: "UTC",
    });
    out.push({ month: key, label });
  }
  return out;
}

/** "YYYY-MM-01" first-day key for any "YYYY-MM-DD" day string. */
export function monthOf(day: string): string {
  return day.slice(0, 7) + "-01";
}

/**
 * The month bucket for an issue, or null for the "Unscheduled" lane.
 *
 * Bucketing is by `target_date` only: Roadmap groups by when work is due,
 * not when it starts. An issue with only a start_date lands in the
 * Unscheduled lane (a deliberate simplification vs Gantt, which needs both
 * dates for bar spans; document the choice here rather than silently
 * falling back).
 */
export function roadmapBucket(
  targetDate: string | null | undefined,
): string | null {
  const t =
    targetDate && targetDate.length >= 10 ? targetDate.slice(0, 10) : null;
  return t ? monthOf(t) : null;
}

/** Day key ("YYYY-MM-DD") of the first day of the current month (UTC). */
export function currentMonthStart(): string {
  const d = new Date();
  return `${d.getUTCFullYear()}-${String(d.getUTCMonth() + 1).padStart(2, "0")}-01`;
}

/**
 * Bucket a list of issues into month columns, in bucket order. Issues are
 * sorted within each column by target_date, then sequence_id.
 *
 * - Issues whose target month is outside the window are dropped from the
 *   columns (they remain in `unscheduled` only when undated).
 * - Undated issues (no target_date) land in `unscheduled`, also sorted by
 *   sequence_id.
 */
export function bucketIssues<T extends {
  id: string;
  sequence_id: number;
  target_date?: string | null;
}>(
  issues: T[],
  buckets: MonthBucket[],
): { columns: { bucket: MonthBucket; issues: T[] }[]; unscheduled: T[] } {
  const order = new Map(buckets.map((b, i) => [b.month, i]));
  const cols: T[][] = buckets.map(() => []);
  const unscheduled: T[] = [];
  for (const issue of issues) {
    const b = roadmapBucket(issue.target_date);
    if (b === null) {
      unscheduled.push(issue);
      continue;
    }
    const idx = order.get(b);
    if (idx !== undefined) cols[idx].push(issue);
  }
  const byDate = (a: T, b: T) => {
    const ad = a.target_date?.slice(0, 10) ?? "";
    const bd = b.target_date?.slice(0, 10) ?? "";
    if (ad !== bd) return ad < bd ? -1 : 1;
    return a.sequence_id - b.sequence_id;
  };
  for (const c of cols) c.sort(byDate);
  unscheduled.sort((a, b) => a.sequence_id - b.sequence_id);
  return {
    columns: buckets.map((bucket, i) => ({ bucket, issues: cols[i] })),
    unscheduled,
  };
}
