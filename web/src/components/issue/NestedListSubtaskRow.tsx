import type { ReactNode } from "react";
import { useSortable } from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { GripVertical, User } from "lucide-react";
import type { IssueState, SubtaskRow } from "../../lib/types";
import { priorityLabel } from "../../lib/types";
import { stateDotClass } from "../../lib/stateDot";
import { Avatar, AvatarFallback } from "../ui/avatar";
import PriorityBars from "./PriorityBars";
import { subtaskPriorityLevel } from "./boardSubtasks";

/** C124T1: one nested sub-work row under a parent list row — the
 *  list-view mirror of the board's NestedSubtaskRow (Board.tsx):
 *  state-dot with the child's real state, title button opening peek
 *  via the parent's onOpen, generic assignee glyphs (ids only, no
 *  fabricated names), priority bars when the row carries a priority.
 *
 *  Deliberately NOT a bulk-select or j/k target: no checkbox, no
 *  row role, no keyboard-selection ring — selection, peek, and
 *  counts operate on the parent rows only. The parent render site
 *  owns the indent wrapper (mirroring SortableCard's nested block);
 *  rows come from T0's listSplit.nestedByParent, so this component
 *  renders one row only.
 *
 *  C141T1: `dragHandle` renders the C126 grip inside the row when the
 *  page renders the row sortable; when it is absent the row renders
 *  exactly as before (no handle, no group class, no extra DOM). */
export default function NestedListSubtaskRow({
  row,
  state,
  onOpen,
  dragHandle,
}: {
  row: SubtaskRow;
  state: IssueState | undefined;
  onOpen: (id: string) => void;
  /** C141T1 grip handle, rendered by SortableNestedSubtaskRow only. */
  dragHandle?: ReactNode;
}) {
  const level = subtaskPriorityLevel(row.priority);
  return (
    <div
      data-nested-subtask={row.id}
      className={`flex items-center gap-2 py-[3px]${dragHandle ? " group/nested" : ""}`}
    >
      {dragHandle}
      <span
        className={`state-dot ${stateDotClass(state?.group)}`}
        aria-hidden="true"
        title={state?.name}
      />
      <button
        type="button"
        onClick={(e) => {
          e.stopPropagation();
          onOpen(row.id);
        }}
        className="min-w-0 flex-1 truncate text-left text-[13px] leading-[18px] text-ink hover:underline"
      >
        {row.title}
      </button>
      {row.assignee_ids.length > 0 && (
        <span
          className="flex flex-none -space-x-1"
          aria-label={`${row.assignee_ids.length} assignee${row.assignee_ids.length === 1 ? "" : "s"}`}
        >
          {row.assignee_ids.map((id) => (
            <Avatar key={id} size="sm" aria-hidden="true">
              <AvatarFallback>
                <User className="h-3 w-3" aria-hidden />
              </AvatarFallback>
            </Avatar>
          ))}
        </span>
      )}
      {level !== null && (
        <span
          className="flex flex-none items-center"
          aria-label={`Priority: ${priorityLabel(level)}`}
        >
          <PriorityBars priority={level} />
        </span>
      )}
    </div>
  );
}

/**
 * C141T1: a nested subtask row made draggable by its parent
 * appearance's nested SortableContext (ListRowStack in the page's
 * renderListRow). SortableIssueRow precedent, nested shape: the
 * sortable node is a wrapper div carrying the transform/transition/
 * drag opacity, while the drag listeners live on the grip handle only
 * — so a nested drag never opens peek, never toggles selection, and
 * never starts a parent drag: the grip stops propagation exactly like
 * the C126 handle, and dnd-kit's own handlers are still invoked after
 * the stop. The wrapper is independent of the parent row's
 * draggability: when the parent is not draggable the page renders
 * this inside a bare fragment, and the nested drag still works —
 * the grip is the nested item's own activator, not the parent's.
 * Keyboard drag comes from ListDragRoot's KeyboardSensor +
 * sortableKeyboardCoordinates, shared with the top-level rows — the
 * nested item is in the same list-wide DndContext, so no separate
 * sensor wiring exists here (see the live-drag caveat: mocked tests
 * cannot prove the live keyboard-drag sequence, only that the grip
 * carries the sortable listeners).
 */
export function SortableNestedSubtaskRow({
  row,
  state,
  onOpen,
  sortableId,
}: {
  row: SubtaskRow;
  state: IssueState | undefined;
  onOpen: (id: string) => void;
  /** Stack-scoped dnd-kit id (C141T1 nestedSortableId). */
  sortableId: string;
}) {
  const {
    attributes,
    listeners,
    setNodeRef,
    transform,
    transition,
    isDragging,
  } = useSortable({ id: sortableId });

  return (
    <div
      ref={setNodeRef}
      style={{
        transform: CSS.Transform.toString(transform),
        transition,
        opacity: isDragging ? 0.35 : 1,
      }}
    >
      <NestedListSubtaskRow
        row={row}
        state={state}
        onOpen={onOpen}
        dragHandle={
          <button
            type="button"
            {...attributes}
            {...listeners}
            aria-label={`Drag ${row.display_id} to reorder subtask`}
            title="Drag to reorder subtask"
            onClick={(e) => e.stopPropagation()}
            onPointerDown={(e) => {
              e.stopPropagation();
              listeners?.onPointerDown?.(e);
            }}
            onKeyDown={(e) => {
              e.stopPropagation();
              listeners?.onKeyDown?.(e);
            }}
            className="inline-flex shrink-0 cursor-grab touch-none items-center justify-center text-ink-muted opacity-0 transition-opacity hover:text-ink group-hover/nested:opacity-100 focus-visible:opacity-100 active:cursor-grabbing"
          >
            <GripVertical className="h-3 w-3" aria-hidden />
          </button>
        }
      />
    </div>
  );
}
