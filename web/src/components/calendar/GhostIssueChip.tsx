import type { CSSProperties } from "react";
import { stateDotClass } from "../../lib/stateDot";
import type { Issue, IssueState } from "../../lib/types";
import { cn } from "../../lib/utils";

/**
 * Mid-drag ghost preview chip (C159T1). IssueChip geometry with the
 * C146/C156 ghost token VERBATIM — the dragged/dependent issue's state
 * colour at fillOpacity 0.35, a 1px dashed stroke in the same colour,
 * `pointer-events: none` — so no new visual language is introduced.
 * Render-only: not a button, not draggable, never focusable, never
 * intercepts pointer events. The real chip stays on its current day;
 * the ghost is an additional chip on the prospective day.
 *
 * `kind` distinguishes the schedule ghost (the dragged chip's landing
 * day, T1) from a cascade ghost (a dependent's shifted day, T2) only
 * for the data attribute / aria-label — same token, same component.
 */
export function GhostIssueChip({
  issue,
  state,
  dayKey,
  kind = "schedule",
}: {
  issue: Issue;
  state?: IssueState;
  /** The previewed day ("YYYY-MM-DD") this ghost sits on. */
  dayKey: string;
  kind?: "schedule" | "cascade";
}) {
  const color = state?.color ?? "#6366f1";
  const style: CSSProperties = {
    // fillOpacity 0.35 verbatim (Gantt SVG `fillOpacity={0.35}`):
    // color-mix keeps the label text at full opacity — plain `opacity`
    // would fade the text too.
    backgroundColor: `color-mix(in srgb, ${color} 35%, transparent)`,
    border: `1px dashed ${color}`,
  };
  const label =
    kind === "schedule"
      ? `Schedule preview for ${issue.display_id} on ${dayKey}`
      : `Cascade preview for ${issue.display_id} on ${dayKey}`;
  const title =
    kind === "schedule"
      ? `${issue.display_id}: ${issue.name} (would schedule on ${dayKey})`
      : `${issue.display_id}: ${issue.name} (would shift to ${dayKey})`;
  return (
    <div
      style={style}
      data-schedule-ghost={kind === "schedule" ? "true" : undefined}
      data-cascade-ghost={kind === "cascade" ? "true" : undefined}
      aria-label={label}
      className={cn(
        "pointer-events-none flex w-full items-center gap-1.5 rounded-[4px] px-1.5 py-[3px] text-left text-[11px] text-ink-secondary",
      )}
    >
      <span
        className={cn("state-dot", stateDotClass(state?.group))}
        aria-hidden
      />
      <span className="shrink-0 font-mono text-[10px] text-ink-muted">
        {issue.display_id}
      </span>
      <span className="truncate">{issue.name}</span>
      <title>{title}</title>
    </div>
  );
}
