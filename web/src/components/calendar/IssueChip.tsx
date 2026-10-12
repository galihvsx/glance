import type { CSSProperties } from "react";
import type {
  DraggableAttributes,
  DraggableSyntheticListeners,
} from "@dnd-kit/core";
import { stateDotClass } from "../../lib/stateDot";
import type { Issue, IssueState } from "../../lib/types";
import { cn } from "../../lib/utils";

/** Drag props injected by DraggableIssueChip; absent in read-only contexts. */
export interface ChipDragHandle {
  setNodeRef: (element: HTMLElement | null) => void;
  style?: CSSProperties;
  attributes?: DraggableAttributes;
  listeners?: DraggableSyntheticListeners;
  className?: string;
}

export function IssueChip({
  issue,
  state,
  onOpen,
  dragHandle,
}: {
  issue: Issue;
  state?: IssueState;
  onOpen: () => void;
  dragHandle?: ChipDragHandle;
}) {
  return (
    <button
      type="button"
      ref={dragHandle?.setNodeRef}
      style={dragHandle?.style}
      {...dragHandle?.attributes}
      {...dragHandle?.listeners}
      onClick={onOpen}
      title={`${issue.display_id}: ${issue.name}`}
      className={cn(
        "flex w-full items-center gap-1.5 rounded-[4px] px-1.5 py-[3px] text-left text-[11px] text-ink-secondary hover:bg-wash-hover hover:text-ink focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring",
        dragHandle?.className,
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
    </button>
  );
}
