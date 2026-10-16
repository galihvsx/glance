// OKR number formatting (C35T1): KR values are backend numerics
// (floats). Display them without float noise — "34", not "34.000000",
// "1.5", not "1.500000" — and cap precision so a computed value like
// 0.333333333333 doesn't leak into the UI.

/** Format a KR value (start / target / current) for display. */
export function formatKRValue(v: number): string {
  if (!Number.isFinite(v)) return "0";
  // Round to 2 decimals first: kills float noise (0.1 + 0.2), then
  // strip trailing zeros ("34.00" -> "34", "1.50" -> "1.5").
  const rounded = Math.round(v * 100) / 100;
  return String(rounded);
}

/** Format a 0–1 progress fraction as a whole-number percent label. */
export function formatProgressPct(progress: number): string {
  if (!Number.isFinite(progress)) return "0%";
  return `${Math.round(Math.min(1, Math.max(0, progress)) * 100)}%`;
}

/** Status chip dot colors for objectives (same vocabulary + colors as
 *  initiatives: active blue, completed green, archived muted). */
export function objectiveStatusColor(status: string): string {
  switch (status) {
    case "active":
      return "#4f8ef7"; // accent blue
    case "completed":
      return "#42c88a"; // accent green
    default:
      return "#777780"; // muted (archived)
  }
}
