import type { Issue } from "./types";

/**
 * Pure move semantics for calendar drag-reschedule (C26T0). Kept UI-free
 * so it is unit-testable.
 *
 * Duration-preserving rule: delta = dropDay − placingDay (in days), applied
 * to whichever of `start_date`/`target_date` are set. The chip's placing day
 * is `target_date` when set, else `start_date` (same rule as
 * `issueDay` in ./calendar). Because every set date shifts by the same
 * delta, the server's start≤target invariant can never be violated.
 */

const DAY_MS = 86_400_000;

/** PATCH body for a schedule move: YYYY-MM-DD strings the server accepts. */
export interface ScheduleDates {
  start_date?: string;
  target_date?: string;
}

/** "YYYY-MM-DD" (or an RFC3339 prefix) → local-midnight Date. */
function parseDayKey(key: string): Date {
  const [y, m, d] = key.split("-").map(Number);
  return new Date(y, m - 1, d);
}

/** Local Date → "YYYY-MM-DD". */
function formatDayKey(d: Date): string {
  const m = String(d.getMonth() + 1).padStart(2, "0");
  const day = String(d.getDate()).padStart(2, "0");
  return `${d.getFullYear()}-${m}-${day}`;
}

function addDays(key: string, delta: number): string {
  const d = parseDayKey(key);
  d.setDate(d.getDate() + delta);
  return formatDayKey(d);
}

/**
 * Resolve the PATCH body for dropping a chip on `targetDayKey`.
 * Returns null when there is nothing to do: the chip has no placing date,
 * or the drop lands on the day it already sits on (no-op → no PATCH).
 */
export function resolveDropDates(
  chip: Pick<Issue, "start_date" | "target_date">,
  targetDayKey: string,
): ScheduleDates | null {
  const placingRaw = chip.target_date ?? chip.start_date;
  if (!placingRaw || placingRaw.length < 10) return null;
  const placingKey = placingRaw.slice(0, 10);
  if (placingKey === targetDayKey) return null;
  const delta = Math.round(
    (parseDayKey(targetDayKey).getTime() - parseDayKey(placingKey).getTime()) /
      DAY_MS,
  );
  if (delta === 0) return null;
  const out: ScheduleDates = {};
  if (chip.start_date && chip.start_date.length >= 10) {
    out.start_date = addDays(chip.start_date.slice(0, 10), delta);
  }
  if (chip.target_date && chip.target_date.length >= 10) {
    out.target_date = addDays(chip.target_date.slice(0, 10), delta);
  }
  return out;
}

export interface ResolvedScheduleDrop {
  uuid: string;
  dates: ScheduleDates;
  announcement: string;
}

/** "2026-10-14" → "Oct 14, 2026" (viewer locale) for the aria-live note. */
export function formatDropDay(dayKey: string): string {
  const [y, m, d] = dayKey.split("-").map(Number);
  return new Date(y, m - 1, d).toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
    year: "numeric",
  });
}

/**
 * Orchestrate a month-grid drop into a mutation call + announcement.
 * Returns null when the drop is a no-op (null `over`, or same-day), so the
 * caller skips the PATCH entirely.
 */
export function resolveScheduleDrop(
  issue: Issue,
  overDayKey: string | null,
): ResolvedScheduleDrop | null {
  if (!overDayKey) return null;
  const dates = resolveDropDates(issue, overDayKey);
  if (!dates) return null;
  return {
    uuid: issue.id,
    dates,
    announcement: `${issue.display_id} moved to ${formatDropDay(overDayKey)}`,
  };
}
