// GoalProgressBar (C35T1): the shared OKR progress bar.
//
// Mirrors InitiativeProgressBar's language (6px track, accent-blue fill,
// tabular-nums pct label) but takes the API's 0–1 progress fraction — the
// OKR endpoints ship `progress` as a fraction, never a 0–100 pct — and an
// optional caption line (e.g. "3 of 5 issues done", "34 / 40 users").

import { cn } from "../../lib/utils";

export interface GoalProgressBarProps {
  /** Computed progress, 0–1. Values outside 0–1 are clamped. */
  progress: number;
  /** Optional caption under the bar (counts, values). */
  caption?: string;
  /** Extra class names for the wrapper. */
  className?: string;
}

function clampProgress(p: number): number {
  if (!Number.isFinite(p)) return 0;
  return Math.min(1, Math.max(0, p));
}

export default function GoalProgressBar({
  progress,
  caption,
  className,
}: GoalProgressBarProps) {
  const pct = clampProgress(progress) * 100;
  const label = `${Math.round(pct)}%`;
  return (
    <div className={cn("w-full", className)}>
      <div className="flex items-center gap-2">
        <div
          role="progressbar"
          aria-label={`Goal progress ${label}`}
          aria-valuenow={Math.round(pct)}
          aria-valuemin={0}
          aria-valuemax={100}
          className="h-[6px] flex-1 overflow-hidden rounded-full bg-wash-active"
        >
          <div
            className="h-full rounded-full bg-accent-blue transition-[width]"
            style={{ width: `${pct}%` }}
          />
        </div>
        <span className="shrink-0 text-[11px] font-semibold tabular-nums text-ink">
          {label}
        </span>
      </div>
      {caption && (
        <p className="mt-1 text-[10px] tabular-nums text-ink-muted">{caption}</p>
      )}
    </div>
  );
}
