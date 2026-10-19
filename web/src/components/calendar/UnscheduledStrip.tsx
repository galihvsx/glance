import type { ReactNode } from "react";
import { IssueChip } from "./IssueChip";
import { DraggableIssueChip } from "./DraggableIssueChip";
import { DroppableUnscheduledStrip } from "./DroppableUnscheduledStrip";
import type { EpicIssueGroup } from "../issue/groupByEpic";
import type { Issue, IssueState } from "../../lib/types";
import { cn } from "../../lib/utils";

/**
 * The "Unscheduled (N)" strip at the bottom of the calendar (C26T2).
 *
 * Members get a live drop zone: chips are draggables (carrying `fromStrip`
 * so drops onto day cells schedule them), and the strip itself is a
 * droppable that unschedules dated chips. The section always renders for
 * members — even empty — so there is a drop target; the empty state shows
 * the unschedule hint.
 *
 * Guests keep the read-only behavior: the section only renders when it
 * has chips, as plain click-to-peek buttons with no drop affordance.
 *
 * C72T2: when `groups` is provided (Group: Epic), the strip renders lane
 * clusters (live epics + Ungrouped last, hidden when empty) instead of the
 * flat chip list. The clusters are render-only — the strip stays ONE
 * droppable (`resolveUnscheduleDrop` unchanged), and multi-appearance
 * chips get per-lane draggable ids so the dnd registry never collides.
 */
export function UnscheduledStrip({
  issues,
  statesById,
  canEdit,
  onOpen,
  groups,
}: {
  issues: Issue[];
  statesById: Map<string, IssueState>;
  canEdit: boolean;
  onOpen: (issue: Issue) => void;
  /** C72T2: pre-computed epic lane clusters; null/undefined keeps the
   *  flat strip. */
  groups?: EpicIssueGroup[] | null;
}) {
  if (!canEdit && issues.length === 0) return null;

  const chipFor = (issue: Issue, laneKey?: string): ReactNode => {
    const chip = canEdit ? (
      <DraggableIssueChip
        issue={issue}
        state={statesById.get(issue.state_id)}
        onOpen={() => onOpen(issue)}
        disabled={false}
        fromStrip
        dragId={laneKey ? `${issue.id}:${laneKey}` : undefined}
      />
    ) : (
      <IssueChip
        issue={issue}
        state={statesById.get(issue.state_id)}
        onOpen={() => onOpen(issue)}
      />
    );
    return (
      <span key={issue.id} className="max-w-64">
        {chip}
      </span>
    );
  };

  const grouped = groups != null && groups.length > 0;

  const laneClusters = grouped ? (
    <div className="w-full divide-y divide-border-hairline">
      {groups!.map((g) => (
        <div
          key={g.key}
          data-testid="strip-lane"
          data-lane-key={g.key}
          className="flex items-start gap-2.5 py-1.5"
        >
          <div
            className={cn(
              "flex w-40 shrink-0 items-baseline gap-1 pt-1 text-[11px]",
              g.isUngrouped
                ? "font-medium italic text-ink-muted"
                : "font-semibold text-ink-secondary",
            )}
          >
            <span className="truncate">{g.title}</span>
            <span className="shrink-0 font-mono text-[10px] tabular-nums text-ink-muted">
              {g.issues.length}
            </span>
          </div>
          <div className="flex flex-1 flex-wrap gap-1">
            {g.issues.map((issue) => chipFor(issue, g.key))}
          </div>
        </div>
      ))}
    </div>
  ) : null;

  const flatChips = issues.map((issue) => chipFor(issue));

  return (
    <section className="mt-3">
      <h3 className="mb-2 text-[12px] font-medium text-ink-secondary">
        Unscheduled ({issues.length})
      </h3>
      {canEdit ? (
        <DroppableUnscheduledStrip>
          {issues.length === 0 ? (
            <p className="text-[11px] text-ink-muted">
              Drag dated chips here to unschedule
            </p>
          ) : (
            (laneClusters ?? flatChips)
          )}
        </DroppableUnscheduledStrip>
      ) : (
        <div className="flex flex-wrap gap-1 rounded-md border border-border bg-surface p-2">
          {laneClusters ?? flatChips}
        </div>
      )}
    </section>
  );
}
