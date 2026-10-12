import { IssueChip } from "./IssueChip";
import { DraggableIssueChip } from "./DraggableIssueChip";
import { DroppableUnscheduledStrip } from "./DroppableUnscheduledStrip";
import type { Issue, IssueState } from "../../lib/types";

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
 */
export function UnscheduledStrip({
  issues,
  statesById,
  canEdit,
  onOpen,
}: {
  issues: Issue[];
  statesById: Map<string, IssueState>;
  canEdit: boolean;
  onOpen: (issue: Issue) => void;
}) {
  if (!canEdit && issues.length === 0) return null;

  const chips = issues.map((issue) => {
    const chip = canEdit ? (
      <DraggableIssueChip
        issue={issue}
        state={statesById.get(issue.state_id)}
        onOpen={() => onOpen(issue)}
        disabled={false}
        fromStrip
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
  });

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
            chips
          )}
        </DroppableUnscheduledStrip>
      ) : (
        <div className="flex flex-wrap gap-1 rounded-md border border-border bg-surface p-2">
          {chips}
        </div>
      )}
    </section>
  );
}
