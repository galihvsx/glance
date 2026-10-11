import type { Issue } from "./types";

/**
 * Pure date logic for the calendar view (C3T5). Kept UI-free so it is
 * unit-testable.
 *
 * Placement rule: an issue is placed by its due date (`target_date`); an
 * issue with no due date falls back to its start date. The backend stores
 * DATE columns (no time component) serialized as RFC3339 UTC midnight
 * ("2026-10-05T00:00:00Z") — the day is sliced off the string directly so
 * the calendar day never shifts with the viewer's timezone.
 */

/** "YYYY-MM-DD" for a local Date. */
export function dayKey(d: Date): string {
  const m = String(d.getMonth() + 1).padStart(2, "0");
  const day = String(d.getDate()).padStart(2, "0");
  return `${d.getFullYear()}-${m}-${day}`;
}

/** The day an issue lands on: due date, else start date, else null. */
export function issueDay(issue: Issue): string | null {
  const raw = issue.target_date ?? issue.start_date;
  if (!raw || raw.length < 10) return null;
  return raw.slice(0, 10);
}

/**
 * 42 cells (6 weeks × 7 days), Monday-first, covering the month view for
 * `month` (0-indexed). Leading/trailing cells belong to adjacent months.
 */
export function monthGrid(year: number, month: number): Date[] {
  const first = new Date(year, month, 1);
  // Monday-first offset: JS getDay() is Sunday-first.
  const lead = (first.getDay() + 6) % 7;
  const start = new Date(year, month, 1 - lead);
  return Array.from(
    { length: 42 },
    (_, i) => new Date(start.getFullYear(), start.getMonth(), start.getDate() + i),
  );
}

/** Monday (local midnight) of the week containing `d`. */
export function startOfWeek(d: Date): Date {
  const out = new Date(d.getFullYear(), d.getMonth(), d.getDate());
  // Monday-first offset: JS getDay() is Sunday-first.
  const lead = (out.getDay() + 6) % 7;
  out.setDate(out.getDate() - lead);
  return out;
}

/** 7 consecutive Monday-first days for the week containing `anchor`. */
export function weekGrid(anchor: Date): Date[] {
  const start = startOfWeek(anchor);
  return Array.from(
    { length: 7 },
    (_, i) =>
      new Date(
        start.getFullYear(),
        start.getMonth(),
        start.getDate() + i,
      ),
  );
}

/** Shift a date by whole weeks (positive or negative). */
export function shiftWeek(d: Date, delta: number): Date {
  const out = new Date(d.getFullYear(), d.getMonth(), d.getDate());
  out.setDate(out.getDate() + delta * 7);
  return out;
}

/**
 * Merge due-dated + start-dated issues into per-day buckets.
 *
 * - `dueIssues` (target_date in the visible range) are placed by due date.
 * - `startIssues` (start_date in the visible range) are placed by start
 *   date, but ONLY when they have no target_date at all — an issue with a
 *   due date is placed by that date (even if it falls outside the view).
 * - An issue present in both sets is placed once, by its due date.
 *
 * Within a day, issues sort by sequence_id (display_id order).
 */
export function bucketIssuesByDay(
  dueIssues: Issue[],
  startIssues: Issue[],
): Map<string, Issue[]> {
  const byDay = new Map<string, Issue[]>();
  const seen = new Set<string>();
  const push = (issue: Issue, day: string) => {
    if (seen.has(issue.id)) return;
    seen.add(issue.id);
    const list = byDay.get(day);
    if (list) list.push(issue);
    else byDay.set(day, [issue]);
  };
  for (const issue of dueIssues) {
    const day = issue.target_date?.slice(0, 10);
    if (day) push(issue, day);
  }
  for (const issue of startIssues) {
    if (seen.has(issue.id)) continue;
    if (issue.target_date) continue; // placed by its due date instead
    const day = issue.start_date?.slice(0, 10);
    if (day) push(issue, day);
  }
  for (const list of byDay.values()) {
    list.sort((a, b) => a.sequence_id - b.sequence_id);
  }
  return byDay;
}

/** First day of a month as "YYYY-MM-DD" (month 0-indexed). */
export function firstOfMonth(year: number, month: number): string {
  return `${year}-${String(month + 1).padStart(2, "0")}-01`;
}

/** Shift a (year, month) pair by `delta` months. */
export function shiftMonth(
  year: number,
  month: number,
  delta: number,
): { year: number; month: number } {
  const d = new Date(year, month + delta, 1);
  return { year: d.getFullYear(), month: d.getMonth() };
}
