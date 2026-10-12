import { useDraggable } from "@dnd-kit/core";
import { CSS } from "@dnd-kit/utilities";
import { IssueChip } from "./IssueChip";
import type { Issue, IssueState } from "../../lib/types";

/**
 * The calendar's IssueChip wrapped in a dnd-kit draggable (C26T0). Drag is
 * disabled for guests (`!canEdit`); the chip then renders as a plain
 * click-to-peek button with no drag affordances.
 */
export function DraggableIssueChip({
  issue,
  state,
  onOpen,
  disabled,
  fromStrip,
}: {
  issue: Issue;
  state?: IssueState;
  onOpen: () => void;
  disabled: boolean;
  /**
   * C26T2: marks chips dragged out of the unscheduled strip so the
   * drag-end handler schedules them onto the drop day (`fromStrip` is
   * carried in the draggable's data; existing call sites don't pass it
   * and keep the delta-move behavior).
   */
  fromStrip?: boolean;
}) {
  const { attributes, listeners, setNodeRef, transform, isDragging } =
    useDraggable({
      id: issue.id,
      data: { issue, fromStrip },
      disabled,
    });
  return (
    <IssueChip
      issue={issue}
      state={state}
      onOpen={onOpen}
      dragHandle={
        disabled
          ? undefined
          : {
              setNodeRef,
              style: transform
                ? { transform: CSS.Translate.toString(transform) }
                : undefined,
              attributes,
              listeners,
              // Board card drag language: grab cursor + touch-action lock
              // while dragging, dimmed chip left behind under the overlay
              // ghost.
              className: `touch-none cursor-grab active:cursor-grabbing${
                isDragging ? " opacity-40" : ""
              }`,
            }
      }
    />
  );
}
