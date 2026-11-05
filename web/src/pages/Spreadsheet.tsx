import { Fragment, useEffect, useMemo, useRef, useState, type FormEvent, type MouseEvent, type ReactNode } from "react";
import { useNavigate, useParams } from "react-router-dom";
import {
  keepPreviousData,
  useInfiniteQuery,
  useMutation,
  useQueries,
  useQuery,
  useQueryClient,
  type InfiniteData,
} from "@tanstack/react-query";
import { ArrowDown, ArrowUp, ArrowUpDown, GripVertical, Search, User } from "lucide-react";
import {
  DndContext,
  PointerSensor,
  closestCorners,
  useDraggable,
  useDroppable,
  useSensor,
  useSensors,
  type DragEndEvent,
  type DragOverEvent,
  type DragStartEvent,
} from "@dnd-kit/core";
import { api, ApiError } from "../lib/api";
import { stateTransitionConflictMessage, useWorkflow } from "../lib/workflow";
import { toast } from "../components/ui/toast";
import {
  planSpreadsheetDrop,
  planSpreadsheetReorder,
  planSpreadsheetReorderToEnd,
  spreadsheetDragGroupKey,
  spreadsheetDragId,
  spreadsheetDragIssueId,
  spreadsheetGroupDroppableId,
  spreadsheetOverGroupKey,
  spreadsheetReorderIndicatorSide,
  spreadsheetRowDropIssueId,
  spreadsheetRowDroppableId,
  SPREADSHEET_GROUP_DROPPABLE_PREFIX,
  SPREADSHEET_ROW_DROPPABLE_PREFIX,
  SPREADSHEET_STATE_UNGROUPED_KEY,
  type SpreadsheetAttachDimension,
} from "../components/issue/spreadsheetDrag";
import { executeCycleMove } from "../components/issue/boardCycle";
import { executeReleaseMove } from "../components/issue/boardRelease";
import type {
  Issue,
  IssueAssignee,
  IssueListResult,
  IssueProjectStructure,
  IssueState,
  Member,
  Project,
  SubtaskRow,
  Workspace,
} from "../lib/types";
import { priorityLabel } from "../lib/types";
import { Button } from "../components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../components/ui/dialog";
import { Input } from "../components/ui/input";
import { Label } from "../components/ui/label";
import { Badge } from "../components/ui/badge";
import { Skeleton } from "../components/ui/skeleton";
import { Alert, AlertDescription } from "../components/ui/alert";
import { Textarea } from "../components/ui/textarea";
import PriorityPicker from "../components/issue/PriorityPicker";
import PriorityBars from "../components/issue/PriorityBars";
import { subtaskPriorityLevel } from "../components/issue/boardSubtasks";
import { splitNestedIssues } from "../components/issue/splitNestedIssues";
import { groupIssuesByAssignee } from "../components/issue/groupByAssignee";
import {
  groupIssuesByCreatedBy,
  type CreatorLookupEntry,
} from "../components/issue/groupByCreatedBy";
import { groupIssuesByCycle } from "../components/issue/groupByCycle";
import { groupIssuesByEpic } from "../components/issue/groupByEpic";
import { groupIssuesByLabel } from "../components/issue/groupByLabel";
import { groupIssuesByModule } from "../components/issue/groupByModule";
import { groupIssuesByRelease } from "../components/issue/groupByRelease";
import { groupIssuesByStateGroup } from "../components/issue/groupByStateGroup";
import {
  buildSubGroupSections,
  effectiveSubGroupBy,
  type SubGroupSection,
} from "../components/issue/subGroup";
import { useSpreadsheetGrouping } from "../components/issue/useSpreadsheetGrouping";
import StatePicker from "../components/issue/StatePicker";
import AssigneePicker from "../components/issue/AssigneePicker";
import {
  CellDateEditor,
  CellPriorityEditor,
  CellStateEditor,
} from "../components/issue/SpreadsheetCellEditors";
import PageHeader, {
  pageHeaderControlClass,
} from "../components/shell/PageHeader";
import SavedViewsMenu from "../components/issue/SavedViewsMenu";
import FilterPanel from "../components/issue/FilterPanel";
import SpreadsheetDisplayPanel from "../components/issue/SpreadsheetDisplayPanel";
import { useSpreadsheetCustomColumns } from "../components/issue/useSpreadsheetCustomColumns";
import { useSpreadsheetSubWorkItems } from "../components/issue/useSpreadsheetSubWorkItems";
import { Avatar, AvatarFallback } from "../components/ui/avatar";
import {
  customFieldKeys,
  fetchCustomFields,
  formatCustomValue,
  resolveVisibleCustomFields,
  type CustomField,
  type CustomValue,
} from "../lib/customFields";
import {
  EMPTY_FILTERS,
  activeFilterCount,
  toApiParams,
  useIssueFilters,
  type IssueFilters,
} from "../lib/filters";
import { pqlHasLimit } from "../lib/pql";
import { useShortcutAction } from "../lib/shortcuts";
import { stateDotClass } from "../lib/stateDot";
import { cn } from "../lib/utils";

const PER_PAGE = 25;

/** Wraps a paragraph of plain text as a minimal TipTap doc. */
function textToTipTapDoc(text: string): unknown {
  const t = text.trim();
  if (!t) return null;
  return {
    type: "doc",
    content: [
      {
        type: "paragraph",
        content: [{ type: "text", text: t }],
      },
    ],
  };
}

function dateOnly(iso?: string | null): string {
  return iso ? iso.slice(0, 10) : "—";
}

function buildIssuePath(
  slug: string,
  identifier: string,
  f: IssueFilters,
  cursor?: string,
  includeSubtasks = false,
): string {
  const p = toApiParams(f);
  p.set("per_page", String(PER_PAGE));
  if (cursor) p.set("cursor", cursor);
  // C21T2: batched project-structure — every returned row carries its
  // cycle/module/release membership (one server-side query), replacing
  // the old per-row ?include_project_structure=1 detail fetches.
  p.set("include_project_structure", "1");
  // C143T1: nested sub-work rows — only when the toggle is on; off
  // means today's flat fetch exactly, no unused data.
  if (includeSubtasks) p.set("include_subtasks", "1");
  return `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}/issues?${p}`;
}

// C104T0: PQL fetch shape for the grid — pql-only filter params plus
// the grid's response-shape flag (?include_project_structure=1, so the
// cycle/module/release columns keep rendering on PQL rows exactly as
// on visual rows). The flag is not a filter: the backend parses it
// independently of pql (issue_handler.go) and attaches membership to
// every returned row regardless of filter mode (service/issue.go), so
// no visual filter can leak in through it.
function buildPqlPath(
  slug: string,
  identifier: string,
  query: string,
  cursor?: string,
  includeSubtasks = false,
): string {
  const p = new URLSearchParams();
  p.set("pql", query.trim());
  p.set("per_page", "100");
  p.set("include_project_structure", "1");
  // C143T1: same toggle-gated flag as the visual fetch. The backend
  // parses it from the shared issueListCommon (issue_handler.go), so
  // the PQL path honors it exactly like the visual path — verified
  // by read, no backend change.
  if (includeSubtasks) p.set("include_subtasks", "1");
  if (cursor) p.set("cursor", cursor);
  return `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}/issues?${p}`;
}

type SortKey =
  | "display_id"
  | "name"
  | "state"
  | "priority"
  | "assignees"
  | "labels"
  | "start_date"
  | "target_date"
  | "created_at"
  | "updated_at"
  | "cycle"
  | "module"
  | "release";

const COLUMNS: { key: SortKey; label: string; className?: string }[] = [
  { key: "display_id", label: "ID", className: "w-28" },
  { key: "name", label: "Title", className: "min-w-64" },
  { key: "state", label: "State", className: "min-w-36" },
  { key: "priority", label: "Priority", className: "w-28" },
  { key: "assignees", label: "Assignees", className: "min-w-40" },
  { key: "labels", label: "Labels", className: "min-w-40" },
  { key: "start_date", label: "Start date", className: "w-32" },
  { key: "target_date", label: "Target date", className: "w-32" },
  { key: "created_at", label: "Created", className: "w-32" },
  { key: "updated_at", label: "Updated", className: "w-32" },
  // C20T2: cycle/module/release membership columns.
  { key: "cycle", label: "Cycle", className: "min-w-36" },
  { key: "module", label: "Module", className: "min-w-40" },
  { key: "release", label: "Release", className: "min-w-32" },
];

/** Raw comparable cell value; "" means "empty" and always sorts last. */
function cellValue(
  issue: Issue,
  key: SortKey,
  stateById: Map<string, IssueState>,
  structure: IssueProjectStructure | undefined,
): string | number {
  switch (key) {
    case "display_id":
      return issue.sequence_id;
    case "name":
      return issue.name.toLowerCase();
    case "state":
      return stateById.get(issue.state_id)?.sequence ?? "";
    case "priority":
      return issue.priority;
    case "assignees":
      return (issue.assignees[0]?.name ?? "").toLowerCase();
    case "labels":
      return (issue.labels[0]?.name ?? "").toLowerCase();
    case "start_date":
      return issue.start_date ?? "";
    case "target_date":
      return issue.target_date ?? "";
    case "created_at":
      return issue.created_at;
    case "updated_at":
      return issue.updated_at;
    // C21T2: membership comes from the batched
    // ?include_project_structure=1 list query (structureByIssue), never
    // from per-row fetches.
    case "cycle":
      return structure?.cycle?.name.toLowerCase() ?? "";
    case "module": {
      const names = (structure?.modules ?? []).map((m) => m.name.toLowerCase());
      return names.length > 0 ? names.join(",") : "";
    }
    case "release":
      return structure?.release?.name.toLowerCase() ?? "";
  }
}

type EditableColumn = "state" | "priority" | "start_date" | "target_date";

/**
 * C148T1: one top-level spreadsheet row as a real component (not an
 * inline render fn) — dnd-kit hooks are per-component-instance, so a
 * row component keeps hook order stable while the row count varies
 * with grouping, filtering, and multi-appearance. The row is both a
 * drag source (its leading handle cell, grouped + canEdit only) and a
 * drop target (the whole row resolves to its primary group — Board
 * over=card precedent). The handle is the ONLY drag source: rows
 * contain click-to-edit cells and text selection, so whole-row drag
 * would break them. When grouped but !canEdit, the leading cell is an
 * empty placeholder (same width) so columns stay aligned with the
 * header row; when flat (groupKey null) there is no leading cell at
 * all and the row is byte-identical to the pre-drag grid. The
 * draggable/droppable hooks run in both modes (dnd-kit degrades to
 * noop default contexts outside a DndContext, and both are disabled
 * when flat), so toggling grouping never changes hook order.
 */
function SpreadsheetTopLevelRow({
  issue,
  groupKey,
  dragging,
  canEdit,
  state,
  structure,
  visibleCfFields,
  cfValues,
  editing,
  cellStatesFor,
  onOpenCellEditor,
  onCommitCellEdit,
  onCancelEdit,
  members,
  onToggleAssignee,
  onOpen,
}: {
  issue: Issue;
  /** Primary group key, or null in flat mode (no handle, no targets). */
  groupKey: string | null;
  /** True while THIS appearance is the active drag source (C148T2
   *  ghosting: the dragged row dims in place; no DragOverlay — the
   *  dim + the target group's header highlight carry the affordance,
   *  and an overlay would need a second dnd-kit boundary in tests). */
  dragging: boolean;
  canEdit: boolean;
  state: IssueState | undefined;
  structure: IssueProjectStructure | undefined;
  visibleCfFields: CustomField[];
  cfValues: Record<string, CustomValue> | undefined;
  editing: { issueId: string; column: EditableColumn } | null;
  cellStatesFor: (stateId: string) => IssueState[];
  onOpenCellEditor: (
    issueId: string,
    column: EditableColumn,
  ) => (e: MouseEvent) => void;
  onCommitCellEdit: (issueId: string, body: Record<string, unknown>) => void;
  onCancelEdit: () => void;
  members: Member[] | null;
  onToggleAssignee: (
    issue: Issue,
    userId: string,
    currently: boolean,
  ) => void;
  onOpen: (issue: Issue) => void;
}) {
  const grouped = groupKey !== null;
  const canDrag = grouped && canEdit;
  const {
    attributes: dragAttributes,
    listeners: dragListeners,
    setNodeRef: setDragNodeRef,
  } = useDraggable({
    id: canDrag
      ? spreadsheetDragId(groupKey, issue.id)
      : `spreadsheet-drag-disabled:${issue.id}`,
    disabled: !canDrag,
  });
  const { setNodeRef: setDropNodeRef } = useDroppable({
    id: grouped
      ? spreadsheetRowDroppableId(groupKey, issue.id)
      : `spreadsheet-row-drop-disabled:${issue.id}`,
    disabled: !grouped,
  });
  const modules = structure?.modules ?? [];
  const isEditingCell = (column: EditableColumn) =>
    editing?.issueId === issue.id && editing?.column === column;
  return (
    <tr
      ref={setDropNodeRef}
      data-spreadsheet-drag-group={groupKey ?? undefined}
      role="link"
      tabIndex={0}
      aria-label={`Open issue ${issue.display_id || issue.id.slice(0, 8)}: ${issue.name}`}
      onClick={() => onOpen(issue)}
      onKeyDown={(e) => {
        if (e.key === "Enter" && !e.metaKey && !e.ctrlKey) {
          onOpen(issue);
        }
      }}
      className={`h-[47px] cursor-pointer border-b border-border-hairline transition-colors last:border-0 hover:bg-wash-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-focus focus-visible:ring-inset ${dragging ? "opacity-40" : ""}`}
    >
      {grouped && (
        <td className="w-10 px-1 py-2 align-middle">
          {canEdit && (
            <button
              type="button"
              ref={setDragNodeRef}
              {...dragAttributes}
              {...dragListeners}
              aria-label="Drag to move to another group"
              title="Drag to move to another group"
              onClick={(e) => e.stopPropagation()}
              onPointerDown={(e) => {
                e.stopPropagation();
                dragListeners?.onPointerDown?.(e);
              }}
              onKeyDown={(e) => {
                e.stopPropagation();
                dragListeners?.onKeyDown?.(e);
              }}
              className="inline-flex cursor-grab touch-none items-center justify-center text-ink-muted hover:text-ink active:cursor-grabbing"
            >
              <GripVertical className="h-3.5 w-3.5" aria-hidden />
            </button>
          )}
        </td>
      )}
      <td className="sticky left-0 z-10 border-r border-border-hairline bg-surface px-3 py-2 align-middle">
        <span className="whitespace-nowrap font-mono text-[12px] text-ink-secondary">
          {issue.display_id || issue.id.slice(0, 8)}
        </span>
      </td>
      <td className="max-w-64 truncate px-3 py-2 align-middle text-[13px] font-medium text-ink">
        {issue.name}
      </td>
      <td
        className="cursor-cell px-3 py-2 align-middle"
        title="Click to edit state"
        onClick={onOpenCellEditor(issue.id, "state")}
      >
        {isEditingCell("state") ? (
          <CellStateEditor
            states={cellStatesFor(issue.state_id)}
            value={issue.state_id}
            ariaLabel={`Edit state for ${issue.display_id || issue.id.slice(0, 8)}`}
            onCommit={(stateId) =>
              onCommitCellEdit(issue.id, { state_id: stateId })
            }
            onCancel={onCancelEdit}
          />
        ) : state ? (
          <span className="inline-flex items-center gap-1.5">
            <span
              className={`state-dot ${stateDotClass(state.group)}`}
              aria-hidden="true"
            />
            <span className="text-[13px] text-ink-secondary">
              {state.name}
            </span>
          </span>
        ) : (
          <span className="text-[13px] text-ink-muted">—</span>
        )}
      </td>
      <td
        className="cursor-cell px-3 py-2 align-middle"
        title="Click to edit priority"
        onClick={onOpenCellEditor(issue.id, "priority")}
      >
        {isEditingCell("priority") ? (
          <CellPriorityEditor
            value={issue.priority}
            ariaLabel={`Edit priority for ${issue.display_id || issue.id.slice(0, 8)}`}
            onCommit={(priority) =>
              onCommitCellEdit(issue.id, { priority })
            }
            onCancel={onCancelEdit}
          />
        ) : (
          <span className="inline-flex items-center gap-1.5">
            <PriorityBars priority={issue.priority} />
            <span className="text-[13px] text-ink-secondary">
              {priorityLabel(issue.priority)}
            </span>
          </span>
        )}
      </td>
      {/* C20T3: assignees edit inline via the shared
          multi-select popover; each toggle commits
          immediately (POST/DELETE relation endpoints). */}
      <td className="px-3 py-2 align-middle">
        <span
          onClick={(e) => e.stopPropagation()}
          onKeyDown={(e) => e.stopPropagation()}
        >
          <AssigneePicker
            members={members}
            assigned={issue.assignees}
            onToggle={(userId, currently) =>
              onToggleAssignee(issue, userId, currently)
            }
            triggerClassName="h-8 gap-1.5 rounded-[4px] border border-transparent bg-transparent px-1.5 text-[13px] font-normal text-ink-secondary shadow-none hover:border-border hover:bg-wash-hover hover:text-ink"
            triggerContent={
              issue.assignees.length > 0 ? (
                <span className="flex flex-wrap gap-1">
                  {issue.assignees.map((a) => (
                    <Badge
                      key={a.id}
                      variant="outline"
                      className="text-xs font-normal"
                    >
                      {a.name}
                    </Badge>
                  ))}
                </span>
              ) : (
                <span className="text-ink-muted">—</span>
              )
            }
          />
        </span>
      </td>
      <td className="px-3 py-2 align-middle">
        {issue.labels.length > 0 ? (
          <span className="flex flex-wrap gap-1">
            {issue.labels.map((l) => (
              <Badge
                key={l.id}
                variant="secondary"
                className="gap-1 text-xs font-normal"
              >
                <span
                  className="h-2 w-2 rounded-full"
                  style={{ backgroundColor: l.color }}
                  aria-hidden
                />
                {l.name}
              </Badge>
            ))}
          </span>
        ) : (
          <span className="text-[13px] text-ink-muted">—</span>
        )}
      </td>
      <td
        className="cursor-cell whitespace-nowrap px-3 py-2 align-middle"
        title="Click to edit start date"
        onClick={onOpenCellEditor(issue.id, "start_date")}
      >
        {isEditingCell("start_date") ? (
          <CellDateEditor
            value={issue.start_date}
            ariaLabel={`Edit start date for ${issue.display_id || issue.id.slice(0, 8)}`}
            onCommit={(v) =>
              onCommitCellEdit(issue.id, { start_date: v })
            }
            onCancel={onCancelEdit}
          />
        ) : (
          <span className="text-[13px] text-ink-secondary">
            {dateOnly(issue.start_date)}
          </span>
        )}
      </td>
      <td
        className="cursor-cell whitespace-nowrap px-3 py-2 align-middle"
        title="Click to edit target date"
        onClick={onOpenCellEditor(issue.id, "target_date")}
      >
        {isEditingCell("target_date") ? (
          <CellDateEditor
            value={issue.target_date}
            ariaLabel={`Edit target date for ${issue.display_id || issue.id.slice(0, 8)}`}
            onCommit={(v) =>
              onCommitCellEdit(issue.id, { target_date: v })
            }
            onCancel={onCancelEdit}
          />
        ) : (
          <span className="text-[13px] text-ink-secondary">
            {dateOnly(issue.target_date)}
          </span>
        )}
      </td>
      <td className="whitespace-nowrap px-3 py-2 align-middle text-[13px] text-ink-secondary">
        {dateOnly(issue.created_at)}
      </td>
      <td className="whitespace-nowrap px-3 py-2 align-middle text-[13px] text-ink-secondary">
        {dateOnly(issue.updated_at)}
      </td>
      {/* C20T2 columns, C21T2 batched: cycle/module/release
          membership comes from the list rows'
          ?include_project_structure=1 (structureByIssue) —
          resolved server-side in one query, no per-row
          fetches. Read-only; inline editing is C20T3. */}
      <td className="px-3 py-2 align-middle">
        {structure?.cycle ? (
          <Badge
            variant="outline"
            className="text-xs font-normal"
            title={structure.cycle.name}
          >
            {structure.cycle.name}
          </Badge>
        ) : (
          <span className="text-[13px] text-ink-muted">—</span>
        )}
      </td>
      <td className="px-3 py-2 align-middle">
        {modules.length > 0 ? (
          <span className="flex flex-wrap items-center gap-1">
            {modules.map((m, i) => (
              <Fragment key={m.id}>
                {i > 0 && (
                  <span
                    className="text-ink-muted"
                    aria-hidden="true"
                  >
                    ,
                  </span>
                )}
                <Badge
                  variant="outline"
                  className="text-xs font-normal"
                  title={m.name}
                >
                  {m.name}
                </Badge>
              </Fragment>
            ))}
          </span>
        ) : (
          <span className="text-[13px] text-ink-muted">—</span>
        )}
      </td>
      <td className="px-3 py-2 align-middle">
        {structure?.release ? (
          <Badge
            variant="outline"
            className="text-xs font-normal"
            title={structure.release.name}
          >
            {structure.release.name}
          </Badge>
        ) : (
          <span className="text-[13px] text-ink-muted">—</span>
        )}
      </td>
      {/*
        C9T4: custom-field columns are READ-ONLY by design.
        Cells render with the exact same formatting as the
        issue-detail custom-fields section
        (lib/customFields formatCustomValue); editing stays
        in the issue detail / peek drawer, where the
        per-type validation and write paths already live.
        Unset values render as "—".
      */}
      {visibleCfFields.map((f) => (
        <td
          key={`cf-${f.id}`}
          className="whitespace-nowrap px-3 py-2 align-middle text-[13px] text-ink-secondary"
        >
          {formatCustomValue(
            f,
            cfValues?.[f.id],
          )}
        </td>
      ))}
    </tr>
  );
}

/**
 * C148T1: a primary group header as a dnd-kit drop target. The
 * droppable registration lives here (a component, so the hook is
 * per-header-instance); the highlight comes from the page's
 * drag-over state, not this header's own isOver — one mechanism
 * covers both header-overs and row-overs (a row under the pointer
 * highlights its group's header, T2's over-row polish included).
 * Sub-group headers are NOT this component: they stay plain rows
 * with no droppable, preserving C147 rule 7.
 */
function SpreadsheetGroupHeaderRow({
  groupKey,
  title,
  count,
  colSpan,
  highlighted,
}: {
  groupKey: string;
  title: string;
  count: number;
  colSpan: number;
  highlighted: boolean;
}) {
  const { setNodeRef } = useDroppable({
    id: spreadsheetGroupDroppableId(groupKey),
  });
  return (
    <tr
      ref={setNodeRef}
      data-spreadsheet-group-header={groupKey}
      className={`border-b border-border-hairline ${highlighted ? "bg-accent-blue/10" : "bg-wash"}`}
    >
      <td
        colSpan={colSpan}
        className="px-3 py-2 align-middle text-[13px] font-medium text-ink"
      >
        {title}{" "}
        <span className="font-normal text-ink-muted">({count})</span>
      </td>
    </tr>
  );
}

/**
 * C148T1: DndContext scope for the grouped grid only. DndContext
 * renders screen-reader nodes as siblings of its children, so it
 * cannot sit inside <tbody> (a div inside tbody is invalid HTML and
 * browsers hoist it out of the table) — it wraps the grid container
 * instead, which still scopes drag sources + targets to grouped
 * mode exactly (flat mode renders the bare grid, no provider).
 */
function SpreadsheetDragScope({
  grouped,
  sensors,
  onDragStart,
  onDragOver,
  onDragEnd,
  onDragCancel,
  children,
}: {
  grouped: boolean;
  sensors: ReturnType<typeof useSensors>;
  onDragStart: (e: DragStartEvent) => void;
  onDragOver: (e: DragOverEvent) => void;
  onDragEnd: (e: DragEndEvent) => void;
  onDragCancel: () => void;
  children: ReactNode;
}) {
  if (!grouped) return <>{children}</>;
  return (
    <DndContext
      sensors={sensors}
      collisionDetection={closestCorners}
      onDragStart={onDragStart}
      onDragOver={onDragOver}
      onDragEnd={onDragEnd}
      onDragCancel={onDragCancel}
    >
      {children}
    </DndContext>
  );
}

export default function Spreadsheet() {
  const { slug = "", identifier = "" } = useParams<{
    slug: string;
    identifier: string;
  }>();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [filters, setFilters, clearFilters] = useIssueFilters();
  // C104T0: PQL mode, following the List (C91) pattern verbatim. The
  // applied query lives in the URL (?pql=) via the shared filter
  // state; the mode flag itself is page state. Entering PQL mode
  // parks the visual selection in memory (pql stripped) and exiting
  // restores it; the parked set is in-memory only — a reload keeps
  // the applied query but not the parked selection.
  const [pqlMode, setPqlMode] = useState(filters.pql?.trim() ? true : false);
  const parkedFiltersRef = useRef<IssueFilters | null>(null);
  // Backstop for mode sync: a non-blank ?pql= in the URL (pasted link,
  // back/forward) always means PQL mode. Mode is NEVER auto-cleared
  // here — leaving PQL mode happens only via an explicit toggle to
  // Filters or applying a visual saved view, because PQL mode with an
  // empty (cleared) query is a real state: no filter.
  useEffect(() => {
    if (filters.pql?.trim()) setPqlMode(true);
  }, [filters.pql]);
  // Seed from the URL so a pasted link shows its query in the box.
  const [searchInput, setSearchInput] = useState(filters.q);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [sortKey, setSortKey] = useState<SortKey | null>(null);
  const [sortDir, setSortDir] = useState<"asc" | "desc">("asc");
  const searchRef = useRef<HTMLInputElement>(null);

  useShortcutAction("new-issue", () => setDialogOpen(true));
  useShortcutAction("focus-search", () => searchRef.current?.focus());
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [newPriority, setNewPriority] = useState(0);
  const [newStateId, setNewStateId] = useState("");
  const [formError, setFormError] = useState<string | null>(null);

  const base = `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`;

  const projectQuery = useQuery({
    queryKey: ["project", slug, identifier],
    queryFn: () => api.get<Project>(base),
  });
  // C148T1: workspace role gates drag sources (Board precedent:
  // role >= 15 = member can edit; guests get no handles and drops
  // are refused in onDragEnd). Same query key + endpoint as Board.
  const workspaceQuery = useQuery({
    queryKey: ["workspace", slug],
    queryFn: () =>
      api.get<Workspace>(`/api/v1/workspaces/${encodeURIComponent(slug)}`),
  });
  const canEdit = (workspaceQuery.data?.role ?? 0) >= 15;
  const statesQuery = useQuery({
    queryKey: ["states", slug, identifier],
    queryFn: () =>
      api.get<{ states: IssueState[] }>(`${base}/states`).then((d) => d.states),
  });

  // C9T4: custom-field columns. Definitions come from the project endpoint;
  // visibility is per-project localStorage, default OFF.
  const { visible: cfVisible, toggle: toggleCfColumn, reset: resetCfColumns } =
    useSpreadsheetCustomColumns(slug, identifier);
  // C143T1: "Show sub-work items" — own per-project localStorage key
  // (useSpreadsheetSubWorkItems), default ON, never shared with the
  // list/board useDisplaySettings object.
  const {
    showSubWorkItems,
    toggle: toggleShowSubWorkItems,
    reset: resetShowSubWorkItems,
  } = useSpreadsheetSubWorkItems(slug, identifier);
  // C147T1/T2: spreadsheet grouping — its OWN per-project
  // localStorage key (useSpreadsheetGrouping), never
  // useDisplaySettings, so grouping here never flips list/board
  // rendering. T3 wires the setters into SpreadsheetDisplayPanel,
  // so a panel selection persists and re-renders the grid
  // immediately (no reload, no refetch).
  const { groupBy, subGroupBy, setGroupBy, setSubGroupBy, reset: resetGrouping } =
    useSpreadsheetGrouping(slug, identifier);
  const customFieldsQuery = useQuery({
    queryKey: customFieldKeys(slug, identifier).fields,
    queryFn: () => fetchCustomFields(slug, identifier),
    staleTime: 60_000,
  });
  const visibleCfFields = useMemo(
    () => resolveVisibleCustomFields(customFieldsQuery.data ?? [], cfVisible),
    [customFieldsQuery.data, cfVisible],
  );

  const issuesQuery = useInfiniteQuery({
    // C143T1: the toggle is part of the key — flipping it refetches
    // with / without include_subtasks=1 instead of reusing stale rows
    // (board/list refetch-on-toggle precedent).
    queryKey: ["issues", slug, identifier, filters, showSubWorkItems],
    queryFn: ({ pageParam }: { pageParam?: string }) =>
      api.get<IssueListResult>(
        buildIssuePath(slug, identifier, filters, pageParam, showSubWorkItems),
      ),
    getNextPageParam: (last) => last.next_cursor || undefined,
    initialPageParam: undefined as string | undefined,
    // C104T0: keep the previous pages across filter-key changes, so
    // committing a PQL query never blanks the grid while the PQL query
    // is still loading — the previous visual rows stay on screen until
    // the PQL result replaces them (Board/C95 pattern). Same effect
    // on visual filter changes: old rows until the new page arrives.
    placeholderData: keepPreviousData,
  });

  // C104T0: committed PQL query — Board's pqlIssuesQuery shape,
  // adapted to the grid's row list (per_page 100 cursor loop, pql-only
  // filter params; a LIMIT query is a single page since the server
  // always returns next_cursor null for it).
  const appliedQuery = filters.pql?.trim() ?? "";
  const pqlActive = pqlMode && appliedQuery !== "";
  const pqlIssuesKey = [
    "spreadsheet-issues-pql",
    slug,
    identifier,
    appliedQuery,
    showSubWorkItems,
  ] as const;
  const pqlIssuesQuery = useQuery({
    queryKey: pqlIssuesKey,
    enabled: pqlActive,
    queryFn: async () => {
      if (pqlHasLimit(appliedQuery)) {
        const page = await api.get<IssueListResult>(
          buildPqlPath(slug, identifier, appliedQuery, undefined, showSubWorkItems),
        );
        return page.results;
      }
      const all: Issue[] = [];
      let cursor: string | undefined;
      do {
        const page = await api.get<IssueListResult>(
          buildPqlPath(slug, identifier, appliedQuery, cursor, showSubWorkItems),
        );
        all.push(...page.results);
        cursor = page.next_cursor;
      } while (cursor);
      return all;
    },
  });

  // C104T1: displayKey-style settle targeting (Board pattern): while
  // a query is committed, mutations re-run that committed query so a
  // row whose new values no longer match leaves the grid on refetch
  // instead of going stale (the C104T0 carry-over — optimistic cell
  // edits patch only the visual cache, which is not displayed while
  // a query is committed). Outside PQL mode the invalidation is
  // byte-for-byte the pre-existing visual prefix invalidation.
  function invalidateDisplayedIssues() {
    if (pqlActive) {
      void queryClient.invalidateQueries({ queryKey: pqlIssuesKey });
    } else {
      void queryClient.invalidateQueries({
        queryKey: ["issues", slug, identifier],
      });
    }
  }

  // C104T0: PQL mode handlers (List pattern verbatim — see the
  // mode-restore note at the pqlMode state above).
  function handlePqlModeChange(mode: "visual" | "pql") {
    if (mode === "pql") {
      if (!pqlMode) {
        // Park the visual selection (pql stripped — the parked set is
        // visual-only) and clear the URL so no request can mix modes.
        parkedFiltersRef.current = { ...filters, pql: "" };
        setFilters({ ...EMPTY_FILTERS, pql: "" });
        setSearchInput("");
      }
      setPqlMode(true);
      return;
    }
    const parked = parkedFiltersRef.current;
    parkedFiltersRef.current = null;
    setPqlMode(false);
    if (parked) {
      setFilters({ ...parked, pql: "" });
      setSearchInput(parked.q);
    } else {
      setFilters({ pql: "" });
    }
  }

  /**
   * Probe-first PQL apply (List pattern): fetch one page with the
   * pql-only params; commit to the URL only when the server accepts
   * the query. A 400 rejects, so the grid never changes and the
   * editor shows the server's message + column inline. Empty query =
   * no filter (commits "" and stays in PQL mode).
   */
  async function handlePqlApply(query: string): Promise<void> {
    const q = query.trim();
    if (!q) {
      setFilters({ pql: "" });
      return;
    }
    await api.get<IssueListResult>(
      buildPqlPath(slug, identifier, q, undefined, showSubWorkItems),
    );
    setFilters({ pql: q });
  }

  function handlePqlClear() {
    setFilters({ pql: "" });
  }

  /**
   * Saved-view application (List pattern): the menu already wrote the
   * view's filters to the URL; this only settles the mode. A PQL view
   * enters PQL mode; a visual view leaves it. Either way the parked
   * visual selection is dropped — the view, not the pre-view
   * selection, is now the state.
   */
  function handleSavedViewApplied(pqlQuery: string | null) {
    parkedFiltersRef.current = null;
    setPqlMode(pqlQuery !== null);
  }

  const stateById = useMemo(
    () => new Map((statesQuery.data ?? []).map((s) => [s.id, s] as const)),
    [statesQuery.data],
  );
  // C68T1: workflow transition controls — client hint only. When the
  // project restricts moves, the state cell editor only offers the
  // row's current state plus its permitted next states.
  const workflow = useWorkflow(slug, identifier).data ?? null;
  // C148T1: cross-group drag wiring. PointerSensor with Board's
  // distance-6 activation constraint, so plain clicks (row open,
  // cell edit, text selection) never start a drag. dragOverGroupKey
  // is the single highlight source: the header of the group under
  // the pointer highlights whether the pointer is over the header
  // itself or over a row of that group.
  const dragSensors = useSensors(
    useSensor(PointerSensor, {
      activationConstraint: { distance: 6 },
    }),
  );
  const [dragOverGroupKey, setDragOverGroupKey] = useState<string | null>(
    null,
  );
  // C152T2: the top-level row id under the pointer, or null over a
  // header / outside any row target — the second half of the
  // insertion-indicator input (the first half is
  // activeSpreadsheetDragId). Row droppables exist only on
  // top-level rows, so this can never be a nested child id.
  const [dragOverRowIssueId, setDragOverRowIssueId] = useState<string | null>(
    null,
  );
  // C148T2: active spreadsheet drag id (compound id of the dragged
  // appearance, or null) — the single dim source: the dragged row
  // renders opacity-40 while its drag is active, cleared on end and
  // on cancel. A multi-appearance issue dims only the dragged
  // appearance, never its siblings.
  const [activeSpreadsheetDragId, setActiveSpreadsheetDragId] = useState<
    string | null
  >(null);
  const stateNamesById = useMemo(
    () => new Map((statesQuery.data ?? []).map((s) => [s.id, s.name] as const)),
    [statesQuery.data],
  );
  const cellStatesFor = (stateId: string) => {
    const all = statesQuery.data ?? [];
    if (!workflow || !workflow.transitions.length) return all;
    const allowed = new Set(
      workflow.transitions
        .filter((t) => t.from_state_id === stateId)
        .map((t) => t.to_state_id),
    );
    return all.filter((s) => s.id === stateId || allowed.has(s.id));
  };

  const createMutation = useMutation({
    mutationFn: (body: {
      name: string;
      description: unknown;
      priority: number;
      state_id?: string;
    }) => api.post<Issue>(`${base}/issues`, body),
    onSuccess: () => {
      setDialogOpen(false);
      setName("");
      setDescription("");
      setNewPriority(0);
      setNewStateId("");
      setFormError(null);
      invalidateDisplayedIssues();
    },
    onError: (e) => {
      setFormError(
        e instanceof ApiError ? e.message : "Failed to create issue",
      );
    },
  });

  // C20T3: inline cell editing. Members feed the assignees multi-select;
  // the same query key the detail page uses, 60s stale.
  const membersQuery = useQuery({
    queryKey: ["members", slug],
    queryFn: () =>
      api
        .get<{ members: Member[] }>(
          `/api/v1/workspaces/${encodeURIComponent(slug)}/members`,
        )
        .then((d) => d.members),
    staleTime: 60_000,
  });

  const [editing, setEditing] = useState<{
    issueId: string;
    column: EditableColumn;
  } | null>(null);
  const [editError, setEditError] = useState<string | null>(null);

  const issuesKey = ["issues", slug, identifier, filters, showSubWorkItems] as const;
  type IssuesCache = InfiniteData<IssueListResult>;

  /** Applies a PATCH body to one cached issue row (optimistic). */
  function applyIssuePatch(issue: Issue, body: Record<string, unknown>): Issue {
    return {
      ...issue,
      ...(body.state_id !== undefined
        ? { state_id: body.state_id as string }
        : {}),
      ...(body.priority !== undefined
        ? { priority: body.priority as number }
        : {}),
      // C152T1: the reorder PATCH stamps sort_order optimistically
      // (cell edits never send it, so their behavior is unchanged);
      // the state-group sort_order sort below re-renders the group
      // in the new order immediately, before the settle refetch.
      ...(body.sort_order !== undefined
        ? { sort_order: body.sort_order as number }
        : {}),
      ...(body.start_date !== undefined
        ? { start_date: body.start_date as string | null }
        : {}),
      ...(body.target_date !== undefined
        ? { target_date: body.target_date as string | null }
        : {}),
    };
  }

  function patchCachedIssue(
    data: IssuesCache | undefined,
    id: string,
    f: (issue: Issue) => Issue,
  ): IssuesCache | undefined {
    if (!data) return data;
    return {
      ...data,
      pages: data.pages.map((p) => ({
        ...p,
        results: p.results.map((issue) =>
          issue.id === id ? f(issue) : issue,
        ),
      })),
    };
  }

  // C20T3: commits via the existing PATCH .../issues/{uuid} endpoint —
  // the same contract the detail page's patchMutation uses (state_id,
  // priority, start_date, target_date all accepted; verified in
  // IssueDetailContent). No new backend surface.
  const patchMutation = useMutation({
    mutationFn: ({ id, body }: { id: string; body: Record<string, unknown> }) =>
      api.patch<Issue>(`${base}/issues/${encodeURIComponent(id)}`, body),
    onMutate: async ({ id, body }) => {
      await queryClient.cancelQueries({ queryKey: issuesKey });
      const prev = queryClient.getQueryData<IssuesCache>(issuesKey);
      queryClient.setQueryData<IssuesCache>(issuesKey, (data) =>
        patchCachedIssue(data, id, (issue) => applyIssuePatch(issue, body)),
      );
      return { prev };
    },
    onError: (e, _vars, ctx) => {
      if (ctx?.prev) queryClient.setQueryData(issuesKey, ctx.prev);
      // C68T1: workflow transition controls — the cell edit was denied by
      // the project's workflow rules; name the permitted next states.
      const conflict = stateTransitionConflictMessage(
        e,
        new Map((statesQuery.data ?? []).map((s) => [s.id, s.name] as const)),
      );
      if (conflict) {
        toast.add({ title: "Move blocked", description: conflict, type: "error" });
      } else {
        setEditError(e instanceof ApiError ? e.message : "Failed to update issue");
      }
    },
    onSettled: () => {
      invalidateDisplayedIssues();
    },
  });

  /** C152T1: Board's rebalanceMutation, spreadsheet-shaped — POST
   *  .../issues/rebalance {state_id} re-spaces one state's column
   *  to even 1024-steps. Used only by the collapsed-gap retry in
   *  applySpreadsheetReorder, never on its own. */
  const rebalanceMutation = useMutation({
    mutationFn: (stateId: string) =>
      api.post<{ rebalanced: number }>(`${base}/issues/rebalance`, {
        state_id: stateId,
      }),
  });

  /** C148T2: Board's attach-mutation shape, one mutation for all four
   *  attach dimensions (epic/assignees/labels/modules) — the endpoint
   *  is the only per-dimension difference, exactly as Board's four
   *  attach mutations differ only in mutationFn. Optimistic append
   *  mirrors Board's attachTo* optimistic setQueryData (epic appends
   *  {id, name, archived_at: null}; assignee {id, name}; label
   *  {id, name, color}; module appends to project_structure.modules);
   *  rollback on error restores the snapshot; settle invalidates via
   *  invalidateDisplayedIssues, so PQL-mode moves re-run the committed
   *  query (rule 9, same settle path as patchMutation — no separate
   *  PQL move path, no change to shared cell-edit behavior). Error
   *  text mirrors Board's per-dimension onError (ApiError message,
   *  else Board's fallback). */
  const attachMutation = useMutation({
    mutationFn: ({
      dimension,
      issueId,
      memberId,
    }: {
      dimension: SpreadsheetAttachDimension;
      issueId: string;
      memberId: string;
      title: string;
      color?: string;
    }) => {
      switch (dimension) {
        case "epic":
          return api.post(
            `${base}/epics/${encodeURIComponent(memberId)}/issues`,
            { issue_ids: [issueId] },
          );
        case "assignees":
          return api.post(
            `${base}/issues/${encodeURIComponent(issueId)}/assignees/${encodeURIComponent(memberId)}`,
          );
        case "labels":
          return api.post(
            `${base}/issues/${encodeURIComponent(issueId)}/labels/${encodeURIComponent(memberId)}`,
          );
        case "modules":
          return api.post(
            `${base}/modules/${encodeURIComponent(memberId)}/issues`,
            { issue_ids: [issueId] },
          );
      }
    },
    onMutate: async (vars) => {
      await queryClient.cancelQueries({ queryKey: issuesKey });
      const prev = queryClient.getQueryData<IssuesCache>(issuesKey);
      queryClient.setQueryData<IssuesCache>(issuesKey, (data) =>
        patchCachedIssue(data, vars.issueId, (issue) => {
          switch (vars.dimension) {
            case "epic":
              return {
                ...issue,
                epics: [
                  ...(issue.epics ?? []),
                  {
                    id: vars.memberId,
                    name: vars.title,
                    archived_at: null,
                  },
                ],
              };
            case "assignees":
              return {
                ...issue,
                assignees: [
                  ...issue.assignees,
                  { id: vars.memberId, name: vars.title },
                ],
              };
            case "labels":
              return {
                ...issue,
                labels: [
                  ...issue.labels,
                  {
                    id: vars.memberId,
                    name: vars.title,
                    color: vars.color ?? "",
                  },
                ],
              };
            case "modules":
              return {
                ...issue,
                project_structure: {
                  ...(issue.project_structure ?? { modules: [] }),
                  modules: [
                    ...(issue.project_structure?.modules ?? []),
                    { id: vars.memberId, name: vars.title },
                  ],
                },
              };
          }
        }),
      );
      return { prev };
    },
    onError: (e, vars, ctx) => {
      if (ctx?.prev) queryClient.setQueryData(issuesKey, ctx.prev);
      const fallback = {
        epic: "Failed to attach issue to epic",
        assignees: "Failed to assign issue",
        labels: "Failed to label issue",
        modules: "Failed to attach issue to module",
      }[vars.dimension];
      setEditError(e instanceof ApiError ? e.message : fallback);
    },
    onSettled: () => {
      invalidateDisplayedIssues();
    },
  });

  /** C148T2: Board's moveToCycle, spreadsheet-shaped — optimistic
   *  cycle stamp, then Board's executeCycleMove pipeline (add target
   *  first, then remove source; add failure aborts before any
   *  remove and rolls back; remove failure after a successful add
   *  surfaces honestly as dual state and keeps the optimistic stamp
   *  until the invalidate below refetches server state, exactly as
   *  Board does). Settle always invalidates (PQL key included via
   *  invalidateDisplayedIssues). */
  async function moveSpreadsheetToCycle(
    issueId: string,
    cycleId: string,
    cycleTitle: string,
    sourceCycleId: string | null,
  ) {
    await queryClient.cancelQueries({ queryKey: issuesKey });
    const prev = queryClient.getQueryData<IssuesCache>(issuesKey);
    queryClient.setQueryData<IssuesCache>(issuesKey, (data) =>
      patchCachedIssue(data, issueId, (issue) => ({
        ...issue,
        project_structure: {
          ...(issue.project_structure ?? { modules: [] }),
          cycle: { id: cycleId, name: cycleTitle },
        },
      })),
    );
    const result = await executeCycleMove(
      {
        addToCycle: (targetCycleId, id) =>
          api.post(
            `${base}/cycles/${encodeURIComponent(targetCycleId)}/issues`,
            { issue_ids: [id] },
          ),
        removeFromCycle: (sourceId, id) =>
          api.del(
            `${base}/cycles/${encodeURIComponent(sourceId)}/issues`,
            { issue_ids: [id] },
          ),
      },
      { kind: "move", cycleId, sourceCycleId },
      issueId,
    );
    if (!result.ok) {
      const detail =
        result.error instanceof ApiError ? result.error.message : null;
      if (result.stage === "add") {
        if (prev) queryClient.setQueryData(issuesKey, prev);
        setEditError(detail ?? "Failed to move issue to cycle");
      } else {
        setEditError(
          detail
            ? `${detail} (the issue is now in both cycles)`
            : "Issue was added to the new cycle but could not be removed from the old cycle",
        );
      }
    }
    invalidateDisplayedIssues();
  }

  /** C148T2: Board's moveToRelease, spreadsheet-shaped — optimistic
   *  release stamp, then Board's executeReleaseMove single assign
   *  (no DELETE, ever). Assign failure rolls back and surfaces
   *  Board's message; settle always invalidates. */
  async function moveSpreadsheetToRelease(
    issueId: string,
    releaseId: string,
    releaseTitle: string,
  ) {
    await queryClient.cancelQueries({ queryKey: issuesKey });
    const prev = queryClient.getQueryData<IssuesCache>(issuesKey);
    queryClient.setQueryData<IssuesCache>(issuesKey, (data) =>
      patchCachedIssue(data, issueId, (issue) => ({
        ...issue,
        project_structure: {
          ...(issue.project_structure ?? { modules: [] }),
          release: { id: releaseId, name: releaseTitle },
        },
      })),
    );
    const result = await executeReleaseMove(
      {
        assignToRelease: (targetReleaseId, id) =>
          api.post(
            `${base}/releases/${encodeURIComponent(targetReleaseId)}/issues`,
            { issue_ids: [id] },
          ),
      },
      { kind: "move", releaseId },
      issueId,
    );
    if (!result.ok) {
      if (prev) queryClient.setQueryData(issuesKey, prev);
      const detail =
        result.error instanceof ApiError ? result.error.message : null;
      setEditError(detail ?? "Failed to move issue to release");
    }
    invalidateDisplayedIssues();
  }

  /** Assignee add/remove — relation endpoints, same as the detail page. */
  function toggleAssignee(issue: Issue, userId: string, currently: boolean) {
    const member = membersQuery.data?.find((m) => m.id === userId);
    const apply = (iss: Issue): Issue => ({
      ...iss,
      assignees: currently
        ? iss.assignees.filter((a) => a.id !== userId)
        : [
            ...iss.assignees,
            {
              id: userId,
              name: member?.name ?? member?.email ?? userId,
            } satisfies IssueAssignee,
          ],
    });
    const prev = queryClient.getQueryData<IssuesCache>(issuesKey);
    queryClient.setQueryData<IssuesCache>(issuesKey, (data) =>
      patchCachedIssue(data, issue.id, apply),
    );
    const url = `${base}/issues/${encodeURIComponent(issue.id)}/assignees/${encodeURIComponent(userId)}`;
    const req = currently ? api.del(url) : api.post(url);
    req.then(
      () => {
        invalidateDisplayedIssues();
      },
      (e: unknown) => {
        if (prev) queryClient.setQueryData(issuesKey, prev);
        setEditError(
          e instanceof ApiError ? e.message : "Failed to update assignees",
        );
      },
    );
  }

  function commitCellEdit(issueId: string, body: Record<string, unknown>) {
    setEditing(null);
    setEditError(null);
    patchMutation.mutate({ id: issueId, body });
  }

  /** Opens an inline editor instead of navigating to the issue. */
  function openCellEditor(issueId: string, column: EditableColumn) {
    return (e: MouseEvent) => {
      e.stopPropagation();
      setEditing({ issueId, column });
    };
  }

  function onSearchSubmit(e: FormEvent) {
    e.preventDefault();
    setFilters({ q: searchInput });
    // A new filter set is a new result set — clear any column sort.
    setSortKey(null);
  }

  const hasActiveFilters = activeFilterCount(filters) > 0;

  async function onCreate(e: FormEvent) {
    e.preventDefault();
    setFormError(null);
    createMutation.mutate({
      name: name.trim(),
      description: textToTipTapDoc(description) ?? undefined,
      priority: newPriority,
      ...(newStateId ? { state_id: newStateId } : {}),
    });
  }

  /** Click cycles asc → desc → none. Empty cells always sort last. */
  function toggleSort(key: SortKey) {
    if (sortKey !== key) {
      setSortKey(key);
      setSortDir("asc");
    } else if (sortDir === "asc") {
      setSortDir("desc");
    } else {
      setSortKey(null);
    }
  }

  const issues = useMemo(
    () => issuesQuery.data?.pages.flatMap((p) => p.results) ?? [],
    [issuesQuery.data],
  );

  // C104T0: the displayed rows — the visual grid by default, the PQL
  // result while a query is committed. While a committed query is
  // still loading, keep showing the visual rows rather than blanking
  // the grid (Board/C95 pattern); the filtered list replaces them on
  // arrival. The header count, custom-value fetches, membership map,
  // and client-side sort all derive from this list.
  const displayedIssues = useMemo(
    () => (pqlActive ? (pqlIssuesQuery.data ?? issues) : issues),
    [pqlActive, pqlIssuesQuery.data, issues],
  );

  // C143T1: split the displayed rows into visible top-level rows +
  // nested rows (shared splitNestedIssues — never duplicated here).
  // Toggle off (or a fetch without the flag) returns the flat list
  // untouched; orphans and grandchildren stay flat rows — see
  // splitNestedIssues. Counts, the empty state, and the sort all
  // derive from visibleIssues, so a nested child never appears twice
  // and never counts.
  const { visibleIssues, nestedByParent } = useMemo(
    () => splitNestedIssues(displayedIssues, showSubWorkItems),
    [displayedIssues, showSubWorkItems],
  );

  // C9T4: per-issue custom values. The list endpoint deliberately never
  // carries custom_values (C7T2 kept the list query untouched for perf, and
  // this task is frontend-only with zero backend changes), so while at
  // least one custom-field column is enabled each visible issue is fetched
  // once via ?include_custom=1. Opt-in cost only — nothing fires while all
  // columns are off (the default).
  const cfValueQueries = useQueries({
    queries:
      visibleCfFields.length === 0
        ? []
        : visibleIssues.map((issue) => ({
            queryKey: ["issue-custom-values", slug, identifier, issue.id],
            queryFn: () =>
              api
                .get<Issue>(
                  `${base}/issues/${encodeURIComponent(issue.id)}?include_custom=1`,
                )
                .then((d) => d.custom_values ?? {}),
            staleTime: 60_000,
          })),
  });
  const cfValuesByIssue = useMemo(
    () =>
      new Map(
        visibleIssues.map(
          (issue, i) =>
            [
              issue.id,
              (cfValueQueries[i]?.data ?? {}) as Record<string, CustomValue>,
            ] as const,
        ),
      ),
    [visibleIssues, cfValueQueries],
  );

  // C21T2: cycle/module/release membership rides on the list rows —
  // the list query passes ?include_project_structure=1 and the server
  // resolves it batched in one query, so the old per-row useQueries N+1
  // (one ?include_project_structure=1 detail fetch per visible issue) is
  // gone. Rows without membership carry { modules: [] } with
  // cycle/release omitted, exactly like the detail shape.
  const structureByIssue = useMemo(
    () =>
      new Map(
        visibleIssues.map(
          (issue) => [issue.id, issue.project_structure] as const,
        ),
      ),
    [visibleIssues],
  );
  // C143T1: the column sort reorders TOP-LEVEL rows only; nested rows
  // travel with their parent in stack order (they are rendered from
  // nestedByParent after the sort, never sorted themselves).
  const sortedIssues = useMemo(() => {
    if (!sortKey) return visibleIssues;
    const dir = sortDir === "asc" ? 1 : -1;
    return [...visibleIssues].sort((a, b) => {
      const va = cellValue(
        a,
        sortKey,
        stateById,
        structureByIssue.get(a.id),
      );
      const vb = cellValue(
        b,
        sortKey,
        stateById,
        structureByIssue.get(b.id),
      );
      const aEmpty = va === "";
      const bEmpty = vb === "";
      if (aEmpty && bEmpty) return 0;
      if (aEmpty) return 1;
      if (bEmpty) return -1;
      if (typeof va === "number" && typeof vb === "number")
        return (va - vb) * dir;
      return String(va).localeCompare(String(vb)) * dir;
    });
  }, [visibleIssues, sortKey, sortDir, stateById, structureByIssue]);

  // C147T1: group sections over sortedIssues — the already
  // column-sorted top-level list, so a column sort re-sorts rows
  // WITHIN groups and never re-sorts the groups themselves (binding
  // rule 4). Grouping partitions TOP-LEVEL rows only: a nested
  // child travels with its parent (rendered from nestedByParent
  // under the parent's row) and is never counted separately
  // (rule 5). Helpers are the Board-proven groupBy* functions with
  // the same lookup arguments Board passes; group order,
  // Unknown/Ungrouped-last, empty-hidden, and multi-appearance
  // semantics are inherited verbatim (rule 3). Null = today's flat
  // path (groupBy "none", or createdBy while members are still
  // resolving — Board's no-Unknown-flash gate, with the flat grid
  // as the fallback instead of empty columns, so no row ever
  // vanishes while waiting).
  const stateGroupById = useMemo(
    () => new Map((statesQuery.data ?? []).map((s) => [s.id, s.group] as const)),
    [statesQuery.data],
  );
  const membersById = useMemo(
    () =>
      new Map<string, CreatorLookupEntry>(
        (membersQuery.data ?? []).map((m) => [
          m.id,
          { name: m.name, email: m.email },
        ]),
      ),
    [membersQuery.data],
  );
  const groupingMembersReady =
    membersQuery.data !== undefined || membersQuery.isError;
  interface SpreadsheetGroupSection {
    key: string;
    title: string;
    issues: Issue[];
  }
  const groupSections = useMemo<SpreadsheetGroupSection[] | null>(() => {
    if (groupBy === "none") return null;
    if (groupBy === "createdBy" && !groupingMembersReady) return null;
    switch (groupBy) {
      case "state": {
        // Board's inline ordering: states in sequence order. Board
        // has no column for an unresolvable state_id; the
        // spreadsheet must never drop a row, so those collapse
        // into one "Ungrouped" group, last, hidden when empty
        // (the subGroup.ts state-section precedent).
        const ordered = [...(statesQuery.data ?? [])].sort(
          (a, b) => a.sequence - b.sequence,
        );
        const sections: SpreadsheetGroupSection[] = [];
        for (const s of ordered) {
          const rows = sortedIssues.filter((i) => i.state_id === s.id);
          // C152T1: the visual fetch carries no order_by (the
          // backend default is -updated_at), so without a column
          // sort the state-group display order would NOT be
          // sort_order order — yet a state group maps 1:1 to a
          // Board column, whose order IS sort_order ASC. Sort the
          // group's rows by sort_order ASC here, visual-filter
          // mode only: PQL ordering is query-owned, and a column
          // sort owns the order while active. The Ungrouped
          // section below keeps fetch order — it has no Board
          // column, and no reorder ever targets it.
          if (sortKey === null && !pqlActive) {
            rows.sort((a, b) => a.sort_order - b.sort_order);
          }
          if (rows.length > 0) {
            sections.push({ key: s.id, title: s.name, issues: rows });
          }
        }
        const ungrouped = sortedIssues.filter(
          (i) => !stateById.has(i.state_id),
        );
        if (ungrouped.length > 0) {
          sections.push({
            key: "state:ungrouped",
            title: "Ungrouped",
            issues: ungrouped,
          });
        }
        return sections;
      }
      case "priority": {
        // Board's inline ordering: 4,3,2,1,0 with priorityLabel
        // titles. A priority outside 0–4 (defensive only — the
        // backend vocabulary is 0–4) gets its own trailing group
        // via priorityLabel's `P${p}` fallback rather than
        // dropping the row.
        const sections: SpreadsheetGroupSection[] = [];
        for (const p of [4, 3, 2, 1, 0]) {
          const rows = sortedIssues.filter((i) => i.priority === p);
          if (rows.length > 0) {
            sections.push({
              key: `priority:${p}`,
              title: priorityLabel(p),
              issues: rows,
            });
          }
        }
        const seen = new Set([4, 3, 2, 1, 0]);
        for (const issue of sortedIssues) {
          if (seen.has(issue.priority)) continue;
          seen.add(issue.priority);
          sections.push({
            key: `priority:${issue.priority}`,
            title: priorityLabel(issue.priority),
            issues: sortedIssues.filter(
              (i) => i.priority === issue.priority,
            ),
          });
        }
        return sections;
      }
      case "epic":
        return groupIssuesByEpic(
          sortedIssues,
          (i) => stateById.get(i.state_id)?.group,
        ).map((g) => ({ key: g.key, title: g.title, issues: g.issues }));
      case "assignees":
        return groupIssuesByAssignee(sortedIssues).map((g) => ({
          key: g.key,
          title: g.title,
          issues: g.issues,
        }));
      case "labels":
        return groupIssuesByLabel(sortedIssues).map((g) => ({
          key: g.key,
          title: g.title,
          issues: g.issues,
        }));
      case "modules":
        return groupIssuesByModule(sortedIssues).map((g) => ({
          key: g.key,
          title: g.title,
          issues: g.issues,
        }));
      case "cycles":
        return groupIssuesByCycle(sortedIssues).map((g) => ({
          key: g.key,
          title: g.title,
          issues: g.issues,
        }));
      case "releases":
        return groupIssuesByRelease(sortedIssues).map((g) => ({
          key: g.key,
          title: g.title,
          issues: g.issues,
        }));
      case "createdBy":
        return groupIssuesByCreatedBy(sortedIssues, membersById).map((g) => ({
          key: g.key,
          title: g.title,
          issues: g.issues,
        }));
      case "stateGroups":
        return groupIssuesByStateGroup(sortedIssues, stateGroupById).map(
          (g) => ({ key: g.key, title: g.title, issues: g.issues }),
        );
      default:
        return null;
    }
  }, [
    groupBy,
    groupingMembersReady,
    sortedIssues,
    statesQuery.data,
    stateById,
    stateGroupById,
    membersById,
    sortKey,
    pqlActive,
  ]);

  // C147T2: sub-group sections inside each primary group, via the
  // shared subGroup.ts sections API with the List call pattern
  // (states + stateById + membersById + stateGroupById — exactly
  // the lookups each sub-dimension needs). The same-dimension rule
  // lives ONLY in effectiveSubGroupBy: stored subGroupBy equal to
  // groupBy renders as "none", and a "none" primary group is flat.
  // Null = T1's rendering runs untouched (effective sub-group
  // "none", no primary groups, or createdBy sub-group while members
  // are still resolving — the T1 no-Unknown-flash gate, applied to
  // the sub-dimension too). Sections are VISUAL ONLY: target fields
  // are ignored — the spreadsheet has no move pipeline to wire,
  // exactly as Board does for epic/assignee/label sub-sections.
  // Empty sections are dropped (state/priority/stateGroups
  // synthesize empties; the spreadsheet has no showEmptyGroups,
  // and T1 omits zero-row primary groups the same way).
  const effectiveSubGroup = effectiveSubGroupBy({ groupBy, subGroupBy });
  const subSectionsByGroup = useMemo<Map<string, SubGroupSection[]> | null>(
    () => {
      if (groupSections === null) return null;
      if (effectiveSubGroup === "none") return null;
      if (effectiveSubGroup === "createdBy" && !groupingMembersReady) {
        return null;
      }
      const m = new Map<string, SubGroupSection[]>();
      for (const group of groupSections) {
        m.set(
          group.key,
          buildSubGroupSections(group.issues, effectiveSubGroup, {
            states: statesQuery.data ?? [],
            stateById,
            membersById,
            stateGroupById,
          }).filter((s) => s.issues.length > 0),
        );
      }
      return m;
    },
    [
      groupSections,
      effectiveSubGroup,
      groupingMembersReady,
      statesQuery.data,
      stateById,
      membersById,
      stateGroupById,
    ],
  );

  /**
   * C148T1: cross-group drop handling (Board onDragEnd semantics, per
   * dimension, via planSpreadsheetDrop). Resolves the source group
   * from the compound drag id and the target group from the header
   * id or the over-row's group; sub-group headers carry no droppable,
   * so a drop over one yields over === null and returns here. Moves
   * run through the existing patchMutation shape (optimistic
   * setQueryData on issuesKey, rollback on error, invalidate on
   * settle — the PQL key via invalidateDisplayedIssues, rule 9), so
   * a move the server rejects never sticks in the grid. Membership
   * moves run through the same shape: attachMutation for
   * epic/assignees/labels/modules (Board's attach endpoints), and
   * Board's executeCycleMove / executeReleaseMove pipelines for
   * cycles/releases — all settling through invalidateDisplayedIssues,
   * so PQL-mode moves re-run the committed query exactly as
   * patchMutation moves do. No-ops (same-group, createdBy,
   * stateGroups, Ungrouped targets, already-member) PATCH nothing
   * and surface no error, by design.
   */
  function handleSpreadsheetDragStart(e: DragStartEvent) {
    setActiveSpreadsheetDragId(String(e.active.id));
  }

  function handleSpreadsheetDragOver(e: DragOverEvent) {
    const overId = e.over ? String(e.over.id) : null;
    setDragOverGroupKey(overId ? spreadsheetOverGroupKey(overId) : null);
    setDragOverRowIssueId(overId ? spreadsheetRowDropIssueId(overId) : null);
  }

  /**
   * C152T2: the insertion indicator — group key, hovered row id,
   * and side ("above" | "below") — or null when no line renders.
   * The eligibility gate is the T1 drop gate verbatim (state
   * grouping, no column sort, no sub-group, visual-filter mode, a
   * real state group, canEdit, and the hovered row in the source's
   * own group); the side itself comes from
   * spreadsheetReorderIndicatorSide, indices alone, so the line
   * always agrees with the T1 planner. Null covers every
   * non-reorder hover: cross-group, non-state grouping, column
   * sort active, sub-group, PQL mode, nested rows (no row
   * droppable exists for them), and the source row itself. The
   * header highlight is suppressed exactly when this is non-null
   * (the line REPLACES the C148 over-row highlight for
   * reorder-eligible hovers; cross-group hovers keep it).
   */
  const reorderIndicator = useMemo(() => {
    if (activeSpreadsheetDragId === null || dragOverRowIssueId === null) {
      return null;
    }
    if (
      groupBy !== "state" ||
      sortKey !== null ||
      effectiveSubGroup !== "none" ||
      pqlActive ||
      !canEdit ||
      groupSections === null
    ) {
      return null;
    }
    const sourceGroupKey = spreadsheetDragGroupKey(activeSpreadsheetDragId);
    if (
      sourceGroupKey === null ||
      sourceGroupKey === SPREADSHEET_STATE_UNGROUPED_KEY ||
      dragOverGroupKey !== sourceGroupKey
    ) {
      return null;
    }
    const group = groupSections.find((g) => g.key === sourceGroupKey);
    if (!group) return null;
    const ordered = [...group.issues].sort(
      (a, b) => a.sort_order - b.sort_order,
    );
    const side = spreadsheetReorderIndicatorSide({
      rows: ordered,
      sourceId: spreadsheetDragIssueId(activeSpreadsheetDragId),
      hoveredId: dragOverRowIssueId,
    });
    if (side === null) return null;
    return { groupKey: sourceGroupKey, issueId: dragOverRowIssueId, side };
  }, [
    activeSpreadsheetDragId,
    dragOverRowIssueId,
    dragOverGroupKey,
    groupBy,
    sortKey,
    effectiveSubGroup,
    pqlActive,
    canEdit,
    groupSections,
  ]);

  /**
   * C152T1: within-state-group reorder (Board's applyDrop pipeline,
   * spreadsheet-shaped). The planner input is the group's
   * sort_order-ordered top-level rows; a null midpoint means the
   * gap collapsed, so the column is rebalanced, refetched, and the
   * drop retried once — never a silent second write without a
   * refetch. The PATCH reuses patchMutation (optimistic
   * sort_order stamp, rollback on error, invalidate on settle),
   * exactly like a C148 state move. Only ever called from the
   * reorder-eligible branches of handleSpreadsheetDragEnd.
   * C152T2: a null targetRowId is the group-header drop — move to
   * the END of the group's sort_order sequence via
   * planSpreadsheetReorderToEnd, same rebalance-retry path.
   */
  async function applySpreadsheetReorder(
    issueId: string,
    stateId: string,
    groupRows: Issue[],
    targetRowId: string | null,
  ) {
    const ordered = [...groupRows].sort(
      (a, b) => a.sort_order - b.sort_order,
    );
    const planFor = (rows: Issue[]) =>
      targetRowId === null
        ? planSpreadsheetReorderToEnd({ rows, sourceId: issueId, stateId })
        : planSpreadsheetReorder({
            rows,
            sourceId: issueId,
            targetRowId,
            stateId,
          });
    const plan0 = planFor(ordered);
    if (plan0.kind === "noop") return;
    if (plan0.sort_order !== null) {
      setEditError(null);
      patchMutation.mutate({
        id: issueId,
        body: { sort_order: plan0.sort_order },
      });
      return;
    }
    // Gap collapsed: rebalance, refetch, retry once (Board verbatim).
    try {
      await rebalanceMutation.mutateAsync(stateId);
    } catch (e) {
      setEditError(
        e instanceof ApiError ? e.message : "Failed to rebalance column",
      );
      return;
    }
    invalidateDisplayedIssues();
    const refetched = await issuesQuery.refetch();
    const fresh = refetched.data?.pages.flatMap((p) => p.results) ?? [];
    const freshGroup = splitNestedIssues(fresh, showSubWorkItems)
      .visibleIssues.filter((i) => i.state_id === stateId)
      .sort((a, b) => a.sort_order - b.sort_order);
    const plan1 = planFor(freshGroup);
    if (plan1.kind === "noop" || plan1.sort_order === null) {
      setEditError("Couldn't place the row even after rebalancing.");
      return;
    }
    setEditError(null);
    patchMutation.mutate({
      id: issueId,
      body: { sort_order: plan1.sort_order },
    });
  }

  function handleSpreadsheetDragEnd(e: DragEndEvent) {
    const { active, over } = e;
    setDragOverGroupKey(null);
    setDragOverRowIssueId(null);
    setActiveSpreadsheetDragId(null);
    if (!over || !canEdit) return;
    if (groupSections === null) return;
    const sourceGroupKey = spreadsheetDragGroupKey(String(active.id));
    const targetGroupKey = spreadsheetOverGroupKey(String(over.id));
    if (sourceGroupKey === null || targetGroupKey === null) return;
    const issueId = spreadsheetDragIssueId(String(active.id));
    const issue = sortedIssues.find((i) => i.id === issueId);
    if (!issue) return;
    // C152T1: same-group row drop in the ONE reorder-eligible
    // configuration — grouped by state, no column sort, no
    // sub-group, visual-filter mode, a real state group (never the
    // Ungrouped section, which has no Board column), and a row
    // droppable in the source's own group. Reorder availability is
    // a DROP-time rule: the handle stays enabled exactly as today
    // (grouped && canEdit), and every other configuration falls
    // through to the C148 path below unchanged (same-group
    // non-state drops, column-sort-active drops, sub-grouped
    // drops, PQL-mode drops, nested-row drops all remain no-ops;
    // cross-group drops are untouched).
    const overId = String(over.id);
    if (overId.startsWith(SPREADSHEET_ROW_DROPPABLE_PREFIX)) {
      const rowGroupKey = spreadsheetOverGroupKey(overId);
      const targetRowId = spreadsheetRowDropIssueId(overId);
      if (
        rowGroupKey === sourceGroupKey &&
        targetRowId !== null &&
        groupBy === "state" &&
        sortKey === null &&
        effectiveSubGroup === "none" &&
        !pqlActive &&
        sourceGroupKey !== SPREADSHEET_STATE_UNGROUPED_KEY
      ) {
        const group = groupSections.find((g) => g.key === sourceGroupKey);
        if (group) {
          void applySpreadsheetReorder(
            issue.id,
            sourceGroupKey,
            group.issues,
            targetRowId,
          );
          return;
        }
      }
    }
    // C152T2: same-group drop onto the state group's header — the
    // C148 no-op — moves the row to the END of that group's
    // sort_order sequence (destIndex = group length, same
    // rebalance-retry path as the row drop). The eligibility gate
    // is the T1 gate verbatim; any failure falls through to the
    // C148 path below unchanged, and a header of a DIFFERENT
    // group keeps its C148 cross-group meaning (state move, not
    // reorder) via planSpreadsheetDrop.
    if (overId.startsWith(SPREADSHEET_GROUP_DROPPABLE_PREFIX)) {
      if (
        targetGroupKey === sourceGroupKey &&
        groupBy === "state" &&
        sortKey === null &&
        effectiveSubGroup === "none" &&
        !pqlActive &&
        sourceGroupKey !== SPREADSHEET_STATE_UNGROUPED_KEY
      ) {
        const group = groupSections.find((g) => g.key === sourceGroupKey);
        if (group) {
          void applySpreadsheetReorder(
            issue.id,
            sourceGroupKey,
            group.issues,
            null,
          );
          return;
        }
      }
    }
    const plan = planSpreadsheetDrop({
      groupBy,
      sourceGroupKey,
      targetGroupKey,
      issue,
      workflow,
      stateNames: stateNamesById,
    });
    if (plan.kind === "noop") return;
    if (plan.kind === "blocked") {
      // Board's error path for a denied move (its patchMutation
      // onError toast, same wording as stateTransitionConflictMessage):
      // no PATCH was issued, and the toast names the permitted states.
      const permitted =
        plan.permittedNames.length > 0
          ? ` Permitted next states: ${plan.permittedNames.join(", ")}`
          : "";
      toast.add({
        title: "Move blocked",
        description: `State transition not allowed by the project's workflow.${permitted}`,
        type: "error",
      });
      return;
    }
    setEditError(null);
    if (plan.kind === "patch") {
      patchMutation.mutate({ id: issue.id, body: plan.body });
      return;
    }
    // Target group title feeds the optimistic membership append
    // (Board's attachTo* title argument); the group must exist —
    // it rendered the drop target — so the fallback is defensive.
    const targetGroup = groupSections.find(
      (g) => g.key === targetGroupKey,
    );
    if (plan.kind === "attach") {
      // Board passes the column's label color for labels; the
      // spreadsheet's groups carry no color, so it is read off a
      // sibling row already carrying that label (a target group is
      // never empty — groups derive from loaded rows).
      const color =
        plan.dimension === "labels"
          ? (targetGroup?.issues
              .flatMap((i) => i.labels ?? [])
              .find((l) => l.id === plan.memberId)?.color ?? "")
          : undefined;
      attachMutation.mutate({
        dimension: plan.dimension,
        issueId: issue.id,
        memberId: plan.memberId,
        title: targetGroup?.title ?? plan.memberId,
        color,
      });
      return;
    }
    if (plan.kind === "move-cycle") {
      void moveSpreadsheetToCycle(
        issue.id,
        plan.cycleId,
        targetGroup?.title ?? plan.cycleId,
        plan.sourceCycleId,
      );
      return;
    }
    void moveSpreadsheetToRelease(
      issue.id,
      plan.releaseId,
      targetGroup?.title ?? plan.releaseId,
    );
  }

  function openIssue(issue: Issue) {
    navigate(`/w/${slug}/p/${identifier}/i/${issue.id}`);
  }

  /** C143T1: nested rows navigate by id — a SubtaskRow is not an Issue. */
  function openIssueById(id: string) {
    navigate(`/w/${slug}/p/${identifier}/i/${id}`);
  }

  /**
   * C143T1: one nested sub-work row under its parent row — the grid
   * mirror of the list's NestedListSubtaskRow. A SubtaskRow carries
   * NO labels, dates, cycle/module/release membership, or custom
   * values, so those columns render "—" (the grid's empty rendering),
   * never a fetched value and never a per-row fetch. NOT
   * inline-editable (no state/priority/date editors — they take a
   * full Issue) and NOT bulk-selectable (no checkbox; the grid has
   * no bulk bar at all). Rows come from nestedByParent, so they
   * render in sort_order ASC under their parent regardless of the
   * active column sort.
   */
  function renderNestedRow(child: SubtaskRow, grouped = false) {
    const childState = child.state_id
      ? stateById.get(child.state_id)
      : undefined;
    const level = subtaskPriorityLevel(child.priority);
    const dash = <span className="text-[13px] text-ink-muted">—</span>;
    return (
      <tr
        key={child.id}
        data-nested-subtask={child.id}
        role="link"
        tabIndex={0}
        aria-label={`Open issue ${child.display_id}: ${child.title}`}
        onClick={() => openIssueById(child.id)}
        onKeyDown={(e) => {
          if (e.key === "Enter" && !e.metaKey && !e.ctrlKey) {
            openIssueById(child.id);
          }
        }}
        className="h-[47px] cursor-pointer border-b border-border-hairline transition-colors last:border-0 hover:bg-wash-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-focus focus-visible:ring-inset"
      >
        {/* C148T1: grouped rows carry a leading handle column, so a
            nested row (never a drag source, rule 3) needs the same
            empty leading cell or its columns misalign with its
            parent's. Flat mode renders no placeholder. */}
        {grouped && <td className="w-10 px-1 py-2 align-middle" />}
        <td className="sticky left-0 z-10 border-r border-border-hairline bg-surface px-3 py-2 align-middle">
          <span className="whitespace-nowrap font-mono text-[12px] text-ink-secondary">
            {child.display_id}
          </span>
        </td>
        <td className="max-w-64 truncate py-2 pl-10 pr-3 align-middle text-[13px] font-medium text-ink">
          {child.title}
        </td>
        <td className="px-3 py-2 align-middle">
          {childState ? (
            <span className="inline-flex items-center gap-1.5">
              <span
                className={`state-dot ${stateDotClass(childState.group)}`}
                aria-hidden="true"
              />
              <span className="text-[13px] text-ink-secondary">
                {childState.name}
              </span>
            </span>
          ) : (
            dash
          )}
        </td>
        <td className="px-3 py-2 align-middle">
          {level !== null ? (
            <span
              className="inline-flex items-center gap-1.5"
              aria-label={`Priority: ${priorityLabel(level)}`}
            >
              <PriorityBars priority={level} />
              <span className="text-[13px] text-ink-secondary">
                {priorityLabel(level)}
              </span>
            </span>
          ) : (
            dash
          )}
        </td>
        <td className="px-3 py-2 align-middle">
          {child.assignee_ids.length > 0 ? (
            <span
              className="flex flex-wrap -space-x-1"
              aria-label={`${child.assignee_ids.length} assignee${child.assignee_ids.length === 1 ? "" : "s"}`}
            >
              {child.assignee_ids.map((id) => (
                <Avatar key={id} size="sm" aria-hidden="true">
                  <AvatarFallback>
                    <User className="h-3 w-3" aria-hidden />
                  </AvatarFallback>
                </Avatar>
              ))}
            </span>
          ) : (
            dash
          )}
        </td>
        {/* Labels, dates, Created, Updated, Cycle, Module, Release:
            a SubtaskRow carries none of these — always "—". */}
        <td className="px-3 py-2 align-middle">{dash}</td>
        <td className="whitespace-nowrap px-3 py-2 align-middle">{dash}</td>
        <td className="whitespace-nowrap px-3 py-2 align-middle">{dash}</td>
        <td className="whitespace-nowrap px-3 py-2 align-middle">{dash}</td>
        <td className="whitespace-nowrap px-3 py-2 align-middle">{dash}</td>
        <td className="px-3 py-2 align-middle">{dash}</td>
        <td className="px-3 py-2 align-middle">{dash}</td>
        <td className="px-3 py-2 align-middle">{dash}</td>
        {visibleCfFields.map((f) => (
          <td
            key={`cf-${f.id}`}
            className="whitespace-nowrap px-3 py-2 align-middle text-[13px] text-ink-secondary"
          >
            {dash}
          </td>
        ))}
      </tr>
    );
  }

  /**
   * C147T1: one top-level row + its nested children from
   * nestedByParent, unchanged from the pre-grouping grid. Used
   * by both the flat path and every group section, so a grouped
   * row is the same editable row (same click-to-open, same cell
   * editors) as the flat row — grouping only partitions rows.
   */
  /**
   * C152T2: one grouped top-level row + the insertion line, when
   * this row is the reorder-eligible hover target. The line is a
   * hairline at the row boundary — the existing row-border token
   * (border-border-hairline), no new color, no new component
   * family — rendered as its own <tr> so it never conflicts with
   * the row's own hairline border: ABOVE means the line sits
   * before the row's fragment, BELOW after it (after any nested
   * children, since the drop boundary is in the top-level
   * sequence). The sub-group branch never calls this: the
   * indicator gate already excludes sub-grouped drags.
   */
  function renderGroupedRow(issue: Issue, groupKey: string) {
    const side =
      reorderIndicator !== null &&
      reorderIndicator.groupKey === groupKey &&
      reorderIndicator.issueId === issue.id
        ? reorderIndicator.side
        : null;
    if (side === null) return renderTopLevelRow(issue, groupKey);
    const line = (
      <tr
        key={`reorder-indicator-${issue.id}`}
        data-spreadsheet-reorder-indicator={issue.id}
        data-spreadsheet-reorder-side={side}
        aria-hidden="true"
      >
        <td
          colSpan={COLUMNS.length + visibleCfFields.length + 1}
          className="p-0"
        >
          <div className="border-t border-border-hairline" />
        </td>
      </tr>
    );
    return (
      <Fragment key={issue.id}>
        {side === "above" ? line : null}
        {renderTopLevelRow(issue, groupKey)}
        {side === "below" ? line : null}
      </Fragment>
    );
  }

  /**
   * C148T1: one top-level row + its nested children from
   * nestedByParent, unchanged from the pre-grouping grid — the row
   * itself moved to SpreadsheetTopLevelRow (module level) so its
   * dnd-kit hooks are per-instance. groupKey is the row's PRIMARY
   * group (a row under a sub-group header still resolves to its
   * primary group); null = flat mode, no handle, no targets.
   */
  function renderTopLevelRow(issue: Issue, groupKey: string | null) {
    const state = stateById.get(issue.state_id);
    const structure = structureByIssue.get(issue.id);
    return (
      <Fragment key={issue.id}>
        <SpreadsheetTopLevelRow
          issue={issue}
          groupKey={groupKey}
          dragging={
            groupKey !== null &&
            activeSpreadsheetDragId ===
              spreadsheetDragId(groupKey, issue.id)
          }
          canEdit={canEdit}
          state={state}
          structure={structure}
          visibleCfFields={visibleCfFields}
          cfValues={cfValuesByIssue.get(issue.id)}
          editing={editing}
          cellStatesFor={cellStatesFor}
          onOpenCellEditor={openCellEditor}
          onCommitCellEdit={commitCellEdit}
          onCancelEdit={() => setEditing(null)}
          members={membersQuery.data ?? null}
          onToggleAssignee={toggleAssignee}
          onOpen={openIssue}
        />
        {/* C143T1: nested rows, in stack order, directly
            under their parent — in visual mode, PQL
            mode, and under a column sort alike. */}
        {(nestedByParent.get(issue.id) ?? []).map((child) =>
          renderNestedRow(child, groupKey !== null),
        )}
      </Fragment>
    );
  }

  return (
    <div className="w-full">
      {/* C19T4: shared page header replaces the per-page h1 + ProjectNav. */}
      <PageHeader
        viewName="Table"
        count={visibleIssues.length}
        showViews
        filterPanel={
          <FilterPanel
            slug={slug}
            identifier={identifier}
            filters={filters}
            onChange={(patch) => {
              setFilters(patch);
              setSortKey(null);
            }}
            onClear={() => {
              clearFilters();
              setSearchInput("");
              setSortKey(null);
            }}
            triggerClassName={pageHeaderControlClass}
            pqlMode={pqlMode}
            pqlAppliedQuery={appliedQuery}
            onPqlModeChange={handlePqlModeChange}
            onPqlApply={handlePqlApply}
            onPqlClear={handlePqlClear}
            // The spreadsheet's Views menu saves visual filters only
            // (no PQL save path here — Board/C96T1 precedent), so the
            // editor must not promise a Views save for the query.
            pqlShowSaveHint={false}
          />
        }
        displayPanel={
          <SpreadsheetDisplayPanel
            fields={customFieldsQuery.data ?? []}
            isLoading={customFieldsQuery.isPending}
            isError={customFieldsQuery.isError}
            onRetry={() => void customFieldsQuery.refetch()}
            visible={cfVisible}
            onToggle={toggleCfColumn}
            onReset={resetCfColumns}
            showSubWorkItems={showSubWorkItems}
            onToggleShowSubWorkItems={toggleShowSubWorkItems}
            onResetShowSubWorkItems={resetShowSubWorkItems}
            groupBy={groupBy}
            subGroupBy={subGroupBy}
            onGroupByChange={setGroupBy}
            onSubGroupByChange={setSubGroupBy}
            onResetGrouping={resetGrouping}
            triggerClassName={pageHeaderControlClass}
          />
        }
        extraControls={
          <SavedViewsMenu
            key={`${slug}:${identifier}`}
            projectId={projectQuery.data?.id ?? ""}
            filters={filters}
            display={null}
            onApplyDisplay={null}
            // C105T1: List (C91) PQL save contract — saving in PQL mode
            // stores only the query ({...EMPTY_FILTERS, pql}) instead
            // of the verbatim filters, and the save hint says so. The
            // apply path is unchanged (C104 already settles the mode).
            pqlMode={pqlMode}
            pqlQuery={appliedQuery}
            onViewApplied={handleSavedViewApplied}
            triggerClassName={pageHeaderControlClass}
          />
        }
        addButton={{ onClick: () => setDialogOpen(true) }}
      />
      <div className="mx-auto w-full max-w-7xl p-6">
        <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>New issue</DialogTitle>
              <DialogDescription>
                It gets the next sequence number, e.g. {identifier}-1.
              </DialogDescription>
            </DialogHeader>
            <form onSubmit={onCreate} className="space-y-4">
              <div className="space-y-2">
                <Label htmlFor="s-name">Title</Label>
                <Input
                  id="s-name"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="Fix the login redirect"
                  required
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="s-desc">Description</Label>
                <Textarea
                  id="s-desc"
                  value={description}
                  onChange={(e) => setDescription(e.target.value)}
                  placeholder="What needs to happen…"
                  rows={4}
                />
              </div>
              <div className="flex gap-4">
                <div className="space-y-2">
                  <Label>Priority</Label>
                  <PriorityPicker
                    value={newPriority}
                    onChange={setNewPriority}
                  />
                </div>
                {(statesQuery.data ?? []).length > 0 && (
                  <div className="space-y-2">
                    <Label>State</Label>
                    <StatePicker
                      states={statesQuery.data ?? []}
                      value={newStateId}
                      onChange={setNewStateId}
                    />
                  </div>
                )}
              </div>
              {formError && (
                <Alert variant="destructive">
                  <AlertDescription>{formError}</AlertDescription>
                </Alert>
              )}
              <DialogFooter>
                <Button
                  type="submit"
                  disabled={createMutation.isPending || !name.trim()}
                >
                  {createMutation.isPending ? "Creating…" : "Create issue"}
                </Button>
              </DialogFooter>
            </form>
            </DialogContent>
        </Dialog>

        {/* Filter toolbar — same query params as the list view */}
        <div className="mb-4 flex flex-wrap items-center gap-2">
          <form onSubmit={onSearchSubmit} className="relative">
            <Search className="absolute left-2.5 top-2.5 h-4 w-4 text-ink-muted" />
            <Input
              ref={searchRef}
              value={searchInput}
              onChange={(e) => setSearchInput(e.target.value)}
              placeholder={
                pqlMode
                  ? "Search is replaced by PQL mode"
                  : "Search issues…  ( / )"
              }
              disabled={pqlMode}
              title={
                pqlMode
                  ? "Free-text search is a visual filter — switch back to Filters to search"
                  : undefined
              }
              className="w-56 bg-surface pl-8 text-[13px]"
            />
          </form>
          {hasActiveFilters && (
            <Button
              variant="ghost"
              size="sm"
              className="text-ink-secondary hover:bg-wash-hover hover:text-ink"
              onClick={() => {
                // Clearing means clearing: the parked visual selection
                // is dropped too, so toggling back cannot resurrect it.
                parkedFiltersRef.current = null;
                clearFilters();
                setSearchInput("");
                setSortKey(null);
              }}
            >
              Clear
            </Button>
          )}
        </div>

        {issuesQuery.isError && (
          <Alert variant="destructive" className="mb-4">
            <AlertDescription>
              {issuesQuery.error instanceof ApiError
                ? issuesQuery.error.message
                : "Failed to load issues"}
            </AlertDescription>
          </Alert>
        )}

        {/* C20T3: inline-edit failures roll back optimistically and surface
            here; dismiss on the next edit or via the close button. */}
        {editError && (
          <Alert variant="destructive" className="mb-4">
            <AlertDescription className="flex items-center justify-between gap-2">
              <span>{editError}</span>
              <button
                type="button"
                className="shrink-0 text-[12px] underline underline-offset-2"
                onClick={() => setEditError(null)}
              >
                Dismiss
              </button>
            </AlertDescription>
          </Alert>
        )}

        {issuesQuery.isPending ? (
          <div className="space-y-3">
            <Skeleton className="h-10 w-full" />
            <Skeleton className="h-64 w-full" />
          </div>
        ) : sortedIssues.length === 0 ? (
          <div className="rounded-lg border border-border bg-surface px-6 py-12 text-center">
            <p className="text-[13px] font-medium text-ink">
              {hasActiveFilters ? "No issues match" : "No issues yet"}
            </p>
            <p className="mt-1 text-[12px] text-ink-muted">
              {hasActiveFilters
                ? "Try clearing the filters."
                : "Create the first issue in this project."}
            </p>
          </div>
        ) : (
          <>
            <SpreadsheetDragScope
              grouped={groupSections !== null}
              sensors={dragSensors}
              onDragStart={handleSpreadsheetDragStart}
              onDragOver={handleSpreadsheetDragOver}
              onDragEnd={handleSpreadsheetDragEnd}
              onDragCancel={() => {
                setDragOverGroupKey(null);
                setDragOverRowIssueId(null);
                setActiveSpreadsheetDragId(null);
              }}
            >
            <div className="overflow-x-auto rounded-lg border border-border bg-surface">
              <table className="w-full min-w-[1200px] border-collapse text-[13px] text-ink">
                <thead className="sticky top-0 z-20">
                  <tr className="border-b border-border-hairline">
                    {/* C148T1: leading handle column header, grouped
                        only — matches the handle <td> on every
                        top-level row. Flat mode renders no such
                        column. */}
                    {groupSections !== null && (
                      <th
                        aria-label="Drag to move"
                        className="w-10 bg-surface px-1 py-2 text-left align-middle"
                      />
                    )}
                    {COLUMNS.map((col, ci) => {
                      const active = sortKey === col.key;
                      return (
                        <th
                          key={col.key}
                          className={cn(
                            "bg-surface px-3 py-2 text-left align-middle",
                            col.className,
                            ci === 0 &&
                              "sticky left-0 z-30 border-r border-border-hairline",
                          )}
                        >
                          <button
                            type="button"
                            onClick={() => toggleSort(col.key)}
                            className={cn(
                              "inline-flex items-center gap-1.5 whitespace-nowrap text-[13px] font-medium",
                              active
                                ? "text-accent-blue"
                                : "text-ink-muted hover:text-ink-secondary",
                            )}
                            aria-label={`Sort by ${col.label}`}
                            aria-sort={
                              active
                                ? sortDir === "asc"
                                  ? "ascending"
                                  : "descending"
                                : "none"
                            }
                          >
                            {col.label}
                            {active ? (
                              sortDir === "asc" ? (
                                <ArrowUp
                                  className="h-3.5 w-3.5 text-accent-blue"
                                  aria-hidden
                                />
                              ) : (
                                <ArrowDown
                                  className="h-3.5 w-3.5 text-accent-blue"
                                  aria-hidden
                                />
                              )
                            ) : (
                              <ArrowUpDown
                                className="h-3.5 w-3.5 opacity-40"
                                aria-hidden
                              />
                            )}
                          </button>
                        </th>
                      );
                    })}
                    {/* C9T4: optional custom-field columns. Plain headers — no
                        client-side sort on these columns (kept minimal; the
                        standard columns above keep their sorting). */}
                    {visibleCfFields.map((f) => (
                      <th
                        key={`cf-${f.id}`}
                        className="min-w-36 bg-surface px-3 py-2 text-left align-middle"
                      >
                        <span
                          className="inline-flex items-center gap-1.5 whitespace-nowrap text-[13px] font-medium text-ink-muted"
                          title={`Custom field — read-only in the spreadsheet`}
                        >
                          {f.name}
                        </span>
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {groupSections === null
                    ? sortedIssues.map((issue) =>
                        renderTopLevelRow(issue, null),
                      )
                    : groupSections.map((group) => (
                        <Fragment key={group.key}>
                          {/* C148T1: primary group headers are drop
                              targets now (C147's visual-only rule
                              survives only for SUB-group headers
                              below). Still never clickable to open
                              an issue, never bulk-selectable, never
                              counted by the empty state (the empty
                              branch above gates on
                              sortedIssues.length, not on headers).
                              Title + TOP-LEVEL count only. Colspan
                              grows by 1 for the handle column. */}
                          <SpreadsheetGroupHeaderRow
                            groupKey={group.key}
                            title={group.title}
                            count={group.issues.length}
                            colSpan={
                              COLUMNS.length + visibleCfFields.length + 1
                            }
                            highlighted={
                              dragOverGroupKey === group.key &&
                              reorderIndicator === null
                            }
                          />
                          {subSectionsByGroup === null
                            ? group.issues.map((issue) =>
                                renderGroupedRow(issue, group.key),
                              )
                            : (
                                subSectionsByGroup.get(group.key) ?? []
                              ).map((sub) => (
                                <Fragment key={sub.key}>
                                  {/* C147T2: visual-only sub-group
                                      header — indented under the
                                      primary header, same treatment
                                      as primary headers: never a
                                      drop target, never clickable,
                                      never bulk-selectable, never
                                      counted by the empty state.
                                      Title + TOP-LEVEL count only;
                                      target fields ignored. Colspan
                                      grows by 1 for the handle
                                      column, matching the rows. */}
                                  <tr
                                    data-spreadsheet-sub-group-header={`${group.key}:${sub.key}`}
                                    className="border-b border-border-hairline bg-wash"
                                  >
                                    <td
                                      colSpan={
                                        COLUMNS.length +
                                        visibleCfFields.length +
                                        1
                                      }
                                      className="py-2 pl-8 pr-3 align-middle text-[13px] font-medium text-ink"
                                    >
                                      {sub.title}{" "}
                                      <span className="font-normal text-ink-muted">
                                        ({sub.issues.length})
                                      </span>
                                    </td>
                                  </tr>
                                  {sub.issues.map((issue) =>
                                    renderTopLevelRow(issue, group.key),
                                  )}
                                </Fragment>
                              ))}
                        </Fragment>
                      ))}
                </tbody>
              </table>
            </div>
            </SpreadsheetDragScope>
            {/* C104T0: no Load more in PQL mode — the PQL result is
                already the full cursor loop, not a first page. */}
            {!pqlActive && issuesQuery.hasNextPage && (
              <div className="mt-4 flex justify-center">
                <Button
                  variant="outline"
                  onClick={() => void issuesQuery.fetchNextPage()}
                  disabled={issuesQuery.isFetchingNextPage}
                >
                  {issuesQuery.isFetchingNextPage ? "Loading…" : "Load more"}
                </Button>
              </div>
            )}
          </>
        )}
      </div>
    </div>
  );
}
