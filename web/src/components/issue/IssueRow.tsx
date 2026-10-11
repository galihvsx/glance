import { useParams } from "react-router-dom";
import { Copy, MoreVertical } from "lucide-react";
import type { Issue, IssueState } from "../../lib/types";
import { priorityLabel } from "../../lib/types";
import { Checkbox } from "../ui/checkbox";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "../ui/dropdown-menu";
import { stateDotClass } from "../../lib/stateDot";
import PriorityBars from "./PriorityBars";
import { useCloneIssue } from "./useCloneIssue";

/** One dense row of the issue list, per the Cycle 19 Plane-layout
 *  prototype (SHELL CONTENT §5): min-height 47px, grid
 *  `76px | minmax(0,1fr) | 110px | 80px`, gap 10px, centered, bottom
 *  hairline, hover wash. Col 1: display_id mono 10px muted. Col 2: title
 *  12px ink, truncated. Col 3: 8px state-dot + 10px state label.
 *  Col 4: priority bars + 10px label.
 *
 *  A 28px selection gutter sits left of the grid (checkbox reveals on
 *  hover / focus / bulk-select, never shifting the grid). Clicking the
 *  row opens the issue — via `onOpen` when given (peek drawer),
 *  otherwise the detail page. `kbdSelected` is the j/k keyboard cursor
 *  (inset blue ring); `bulkSelected` paints the active wash.
 *  `fields` (Display panel) toggles the state/priority columns; labels,
 *  assignees and dates have no column on the dense row and are ignored
 *  here (they still render on board cards). */
export default function IssueRow({
  issue,
  state,
  onOpen,
  fields,
  kbdSelected,
  bulkSelected,
  bulkActive,
  onToggleSelect,
  slug: slugProp,
  identifier: identifierProp,
}: {
  issue: Issue;
  state: IssueState | undefined;
  onOpen?: (issue: Issue) => void;
  fields: { state: boolean; priority: boolean };
  kbdSelected?: boolean;
  bulkSelected?: boolean;
  bulkActive?: boolean;
  onToggleSelect?: (checked: boolean) => void;
  slug?: string;
  identifier?: string;
}) {
  const params = useParams<{ slug: string; identifier: string }>();
  const slug = slugProp ?? params.slug;
  const identifier = identifierProp ?? params.identifier;
  // C8T4: row-menu clone — POST .../issues/{uuid}/clone. The trigger lives
  // inside the row, so clicks are stopped exactly like the checkbox below.
  const { cloneIssue, cloning, error: cloneError } = useCloneIssue(
    slug ?? "",
    identifier ?? "",
  );

  const open = () => {
    if (onOpen) onOpen(issue);
  };
  const checkboxVisible = bulkSelected || bulkActive;

  return (
    <div
      role="link"
      tabIndex={0}
      aria-label={`Open issue ${issue.display_id}: ${issue.name}`}
      title={issue.name}
      onClick={open}
      onKeyDown={(e) => {
        if ((e.key === "Enter" || e.key === " ") && !e.metaKey && !e.ctrlKey && !e.altKey) {
          e.preventDefault();
          open();
        }
      }}
      className={`group/row relative flex cursor-pointer border-b border-border-hairline transition-colors hover:bg-wash-hover focus-visible:outline-2 focus-visible:outline-offset-[-2px] focus-visible:outline-[#4f8ef7] ${
        bulkSelected ? "bg-wash-active hover:bg-wash-active" : ""
      } ${kbdSelected ? "ring-1 ring-inset ring-[#4f8ef7]" : ""}`}
    >
      {/* Selection gutter — fixed 28px, never shifts the grid. */}
      {onToggleSelect && (
        <span
          className="flex w-7 flex-none items-center justify-center"
          onClick={(e) => e.stopPropagation()}
          onKeyDown={(e) => e.stopPropagation()}
        >
          <Checkbox
            checked={bulkSelected ?? false}
            onCheckedChange={(v) => onToggleSelect(v === true)}
            aria-label={`Select issue ${issue.display_id}`}
            tabIndex={-1}
            className={`transition-opacity ${
              checkboxVisible
                ? "opacity-100"
                : "opacity-0 group-hover/row:opacity-100 focus-visible:opacity-100"
            }`}
          />
        </span>
      )}
      <div className="grid min-h-[47px] flex-1 grid-cols-[76px_minmax(0,1fr)_110px_80px] items-center gap-[10px] py-2 pr-8">
        <span className="truncate font-mono text-[10px] text-ink-muted">
          {issue.display_id}
        </span>
        <span className="truncate text-[12px] text-ink">{issue.name}</span>
        <span className="flex min-w-0 items-center gap-[6px]">
          {state && fields.state ? (
            <>
              <span
                className={`state-dot ${stateDotClass(state.group)}`}
                aria-hidden
              />
              <span className="truncate text-[10px] text-ink-secondary">
                {state.name}
              </span>
            </>
          ) : null}
        </span>
        <span className="flex min-w-0 items-center gap-[5px]">
          {fields.priority ? (
            <>
              <PriorityBars priority={issue.priority} />
              <span className="truncate text-[10px] text-ink-secondary">
                {priorityLabel(issue.priority)}
              </span>
            </>
          ) : null}
        </span>
      </div>
      {/* Hover-reveal row menu (clone). */}
      <span
        className="absolute right-1 top-1/2 -translate-y-1/2 opacity-0 transition-opacity group-hover/row:opacity-100 focus-within:opacity-100"
        onClick={(e) => e.stopPropagation()}
        onKeyDown={(e) => e.stopPropagation()}
      >
        <DropdownMenu>
          <DropdownMenuTrigger
            className="inline-flex h-6 w-6 items-center justify-center rounded-[4px] text-ink-muted transition-colors hover:bg-wash-hover hover:text-ink focus-visible:opacity-100"
            title="Issue actions"
            aria-label={`Actions for issue ${issue.display_id}`}
            tabIndex={-1}
          >
            <MoreVertical className="h-3.5 w-3.5" />
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem
              onClick={() => void cloneIssue(issue.id)}
              disabled={cloning}
            >
              <Copy className="h-3.5 w-3.5" />
              {cloning ? "Cloning…" : "Clone issue"}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </span>
      {cloneError && (
        <span
          role="alert"
          className="absolute bottom-1 left-8 text-[10px] text-destructive"
        >
          {cloneError}
        </span>
      )}
    </div>
  );
}
