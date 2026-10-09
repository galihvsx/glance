/**
 * Pure date/x-mapping logic for the Gantt view (C4T1). Kept UI-free so it
 * is unit-testable.
 *
 * The backend stores DATE columns (no time component) serialized as
 * RFC3339 UTC midnight ("2026-10-05T00:00:00Z") — the day is sliced off
 * the string directly so the timeline never shifts with the viewer's
 * timezone (same convention as calendar.ts).
 */

/** "YYYY-MM-DD" → UTC-midnight timestamp. */
export function dayToTs(day: string): number {
  const [y, m, d] = day.split("-").map(Number);
  return Date.UTC(y, m - 1, d);
}

/** UTC-midnight timestamp → "YYYY-MM-DD". */
export function tsToDay(ts: number): string {
  const d = new Date(ts);
  const m = String(d.getUTCMonth() + 1).padStart(2, "0");
  const day = String(d.getUTCDate()).padStart(2, "0");
  return `${d.getUTCFullYear()}-${m}-${day}`;
}

/** Whole days between two "YYYY-MM-DD" strings (b - a). */
export function daysBetween(a: string, b: string): number {
  return Math.round((dayToTs(b) - dayToTs(a)) / 86_400_000);
}

/** Add n days to a "YYYY-MM-DD" string. */
export function addDays(day: string, n: number): string {
  return tsToDay(dayToTs(day) + n * 86_400_000);
}

/**
 * The visible window: three months centered on the given month
 * (previous, current, next), as a list of "YYYY-MM-DD" days.
 */
export function ganttDays(year: number, month: number): string[] {
  const start = new Date(Date.UTC(year, month - 1, 1));
  const end = new Date(Date.UTC(year, month + 2, 1)); // exclusive
  const out: string[] = [];
  for (let ts = start.getTime(); ts < end.getTime(); ts += 86_400_000) {
    out.push(tsToDay(ts));
  }
  return out;
}

/** First day of each month spanned by the window (for column headers). */
export function ganttMonths(days: string[]): { day: string; span: number }[] {
  const out: { day: string; span: number }[] = [];
  for (const d of days) {
    if (d.endsWith("-01") || out.length === 0) {
      out.push({ day: d, span: 1 });
    } else {
      out[out.length - 1].span += 1;
    }
  }
  return out;
}

export interface BarSpan {
  /** First visible day of the bar ("YYYY-MM-DD"), or null for undated. */
  start: string | null;
  /** Last visible day of the bar (inclusive), or null for undated. */
  end: string | null;
  /** Duration in days (end - start + 1), 0 when undated. */
  duration: number;
}

/**
 * Bar placement for an issue: start_date → target_date.
 * - both dates: the span between them (clamped to >= 1 day).
 * - one date: a 1-day bar on that date.
 * - neither: null (the issue goes to the "unscheduled" lane).
 */
export function barSpan(
  startDate: string | null | undefined,
  targetDate: string | null | undefined,
): BarSpan {
  const s = startDate && startDate.length >= 10 ? startDate.slice(0, 10) : null;
  const t =
    targetDate && targetDate.length >= 10 ? targetDate.slice(0, 10) : null;
  if (!s && !t) return { start: null, end: null, duration: 0 };
  const start = s ?? t!;
  const end = t ?? s!;
  // Guard against inverted ranges (bad data): clamp to a 1-day bar.
  const lo = start <= end ? start : end;
  const hi = start <= end ? end : start;
  return { start: lo, end: hi, duration: daysBetween(lo, hi) + 1 };
}

/** x (px) of a day's left edge, given the window's first day and day width. */
export function dayToX(day: string, windowStart: string, dayWidth: number): number {
  return daysBetween(windowStart, day) * dayWidth;
}

/** Day ("YYYY-MM-DD") for an x (px), clamped into the window. */
export function xToDay(
  x: number,
  windowStart: string,
  dayWidth: number,
  windowDays: number,
): string {
  const idx = Math.max(0, Math.min(windowDays - 1, Math.floor(x / dayWidth)));
  return addDays(windowStart, idx);
}
