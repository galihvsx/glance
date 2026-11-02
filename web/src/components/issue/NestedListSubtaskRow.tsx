import { User } from "lucide-react";
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
 *  renders one row only. */
export default function NestedListSubtaskRow({
  row,
  state,
  onOpen,
}: {
  row: SubtaskRow;
  state: IssueState | undefined;
  onOpen: (id: string) => void;
}) {
  const level = subtaskPriorityLevel(row.priority);
  return (
    <div
      data-nested-subtask={row.id}
      className="flex items-center gap-2 py-[3px]"
    >
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
