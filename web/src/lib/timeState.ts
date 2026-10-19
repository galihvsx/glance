/**
 * Time-in-state duration formatting (C70T1). Companion to the
 * TimeTracker's formatDuration, kept separate because the tracker's
 * format lacks day handling and its tests pin its current behavior.
 *
 * Boundaries: 0 -> "<1m", 1-59 -> "45s", 60-3599 -> "54m",
 * 3600-86399 -> "2h 15m", >=86400 -> "3d 4h".
 */
export function formatStateDuration(seconds: number): string {
  const s = Number.isFinite(seconds) ? Math.max(0, Math.floor(seconds)) : 0;
  if (s === 0) return "<1m";
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`;
  return `${Math.floor(s / 86400)}d ${Math.floor((s % 86400) / 3600)}h`;
}
