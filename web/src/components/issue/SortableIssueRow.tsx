import type { ReactNode } from "react";
import { useSortable } from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { GripVertical } from "lucide-react";
import type { Issue, IssueState } from "../../lib/types";
import IssueRow from "./IssueRow";
import type { DisplayFields } from "./useDisplaySettings";

/**
 * C126T0: a list-view row made draggable by its stack's
 * SortableContext (ListRowStack). Board SortableCard precedent: the
 * sortable node is a wrapper div carrying the transform/transition/
 * drag opacity, while the drag listeners live on the grip handle only
 * — so nested subtask rows (passed as `children`, rendered below the
 * row inside the wrapper) travel with a dragged parent but can never
 * start a drag themselves, and a drag never opens the peek drawer or
 * toggles selection: the grip stops propagation exactly like the
 * checkbox and row-menu triggers, and dnd-kit's own handlers are
 * still invoked after the stop.
 *
 * C127T0: `sortableId` overrides the dnd-kit id with the stack-scoped
 * id from listHeaderDrop.listSortableId (a row multi-appears across
 * epic sections, so the bare row id would collide in the list-wide
 * context). Defaults to the row id — the C126 shape — when omitted.
 */
export default function SortableIssueRow({
  issue,
  state,
  fields,
  onOpen,
  kbdSelected,
  bulkSelected,
  bulkActive,
  onToggleSelect,
  sortableId,
  children,
}: {
  issue: Issue;
  state: IssueState | undefined;
  fields: DisplayFields;
  onOpen: (issue: Issue) => void;
  kbdSelected?: boolean;
  bulkSelected?: boolean;
  bulkActive?: boolean;
  onToggleSelect?: (checked: boolean) => void;
  /** Stack-scoped dnd-kit id (C127T0); defaults to the row id. */
  sortableId?: string;
  /** Nested subtask rows under this parent (C124) — never draggable. */
  children?: ReactNode;
}) {
  const {
    attributes,
    listeners,
    setNodeRef,
    transform,
    transition,
    isDragging,
  } = useSortable({ id: sortableId ?? issue.id });

  return (
    <div
      ref={setNodeRef}
      style={{
        transform: CSS.Transform.toString(transform),
        transition,
        opacity: isDragging ? 0.35 : 1,
      }}
    >
      <IssueRow
        issue={issue}
        state={state}
        fields={fields}
        onOpen={onOpen}
        kbdSelected={kbdSelected}
        bulkSelected={bulkSelected}
        bulkActive={bulkActive}
        onToggleSelect={onToggleSelect}
        dragHandle={
          <button
            type="button"
            {...attributes}
            {...listeners}
            aria-label={`Drag ${issue.display_id} to reorder`}
            title="Drag to reorder"
            onClick={(e) => e.stopPropagation()}
            onPointerDown={(e) => {
              e.stopPropagation();
              listeners?.onPointerDown?.(e);
            }}
            onKeyDown={(e) => {
              e.stopPropagation();
              listeners?.onKeyDown?.(e);
            }}
            className="inline-flex shrink-0 cursor-grab touch-none items-center justify-center text-ink-muted opacity-0 transition-opacity hover:text-ink group-hover/row:opacity-100 focus-visible:opacity-100 active:cursor-grabbing"
          >
            <GripVertical className="h-3 w-3" aria-hidden />
          </button>
        }
      />
      {children}
    </div>
  );
}
