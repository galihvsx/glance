import { useEffect, useMemo, useState, type ReactNode } from "react";
import { useNavigate, useParams } from "react-router-dom";
import {
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import {
  DndContext,
  DragOverlay,
  KeyboardSensor,
  PointerSensor,
  closestCorners,
  useDroppable,
  useSensor,
  useSensors,
  type DragEndEvent,
  type DragStartEvent,
} from "@dnd-kit/core";
import {
  SortableContext,
  sortableKeyboardCoordinates,
  useSortable,
  verticalListSortingStrategy,
} from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { api, ApiError } from "../lib/api";
import { pqlHasLimit } from "../lib/pql";
import {
  dropSortOrder,
  midpoint,
  stackDropSortOrder,
} from "../lib/sortOrder";
import { stateDotClass } from "../lib/stateDot";
import {
  isTransitionAllowed,
  stateTransitionConflictMessage,
  useWorkflow,
} from "../lib/workflow";
import { toast } from "../components/ui/toast";
import {
  Ban,
  Calendar,
  Copy,
  GitBranch,
  Layers,
  Link2,
  Maximize2,
  MoreHorizontal,
  Paperclip,
  Plus,
  User,
  Waypoints,
} from "lucide-react";
import DisplayPanel from "../components/issue/DisplayPanel";
import PriorityBars from "../components/issue/PriorityBars";
import SubtaskProgressBar from "../components/issue/SubtaskProgressBar";
import EpicGroupBreakdown from "../components/epics/EpicGroupBreakdown";
import {
  buildEpicBoardGroups,
  epicCardDragId,
  epicDragIssueId,
  epicDropColumnKey,
  planEpicDrop,
  planEpicQuickAdd,
  uniqueIssueCount,
} from "../components/issue/boardEpic";
import {
  assigneeCardDragId,
  assigneeDragColumnKey,
  assigneeDragIssueId,
  assigneeDropColumnKey,
  buildAssigneeBoardGroups,
  planAssigneeDrop,
  planAssigneeQuickAdd,
  planBoardQuickExpand,
} from "../components/issue/boardAssignee";
import {
  buildLabelBoardGroups,
  labelCardDragId,
  labelDragColumnKey,
  labelDragIssueId,
  labelDropColumnKey,
  planBoardLabelQuickExpand,
  planLabelDrop,
  planLabelQuickAdd,
} from "../components/issue/boardLabel";
import {
  buildModuleBoardGroups,
  moduleCardDragId,
  moduleDragColumnKey,
  moduleDragIssueId,
  moduleDropColumnKey,
  planBoardModuleQuickExpand,
  planModuleDrop,
  planModuleQuickAdd,
} from "../components/issue/boardModule";
import {
  CYCLE_UNGROUPED_COLUMN_KEY,
  buildCycleBoardGroups,
  cycleCardDragId,
  cycleDragColumnKey,
  cycleDragIssueId,
  cycleDropColumnKey,
  executeCycleMove,
  planBoardCycleQuickExpand,
  planCycleDrop,
  planCycleQuickAdd,
} from "../components/issue/boardCycle";
import {
  buildReleaseBoardGroups,
  releaseCardDragId,
  releaseDragColumnKey,
  releaseDragIssueId,
  releaseDropColumnKey,
  executeReleaseMove,
  planBoardReleaseQuickExpand,
  planReleaseDrop,
  planReleaseQuickAdd,
} from "../components/issue/boardRelease";
import {
  buildCreatedByBoardGroups,
  createdByCardDragId,
  createdByDragColumnKey,
  createdByDragIssueId,
  createdByDropColumnKey,
  planCreatedByDrop,
} from "../components/issue/boardCreatedBy";
import type { CreatorLookupEntry } from "../components/issue/groupByCreatedBy";
import {
  buildStateGroupBoardGroups,
  planStateGroupDrop,
  stateGroupCardDragId,
  stateGroupDragColumnKey,
  stateGroupDragIssueId,
  stateGroupDropColumnKey,
} from "../components/issue/boardStateGroup";
import SavedViewsMenu from "../components/issue/SavedViewsMenu";
import FilterPanel from "../components/issue/FilterPanel";
import PqlEditor from "../components/issue/PqlEditor";
import { useCloneIssue } from "../components/issue/useCloneIssue";
import { toApiParams, useIssueFilters } from "../lib/filters";
import {
  formatIssueDateRange,
  useDisplaySettings,
  type DisplayFields,
} from "../components/issue/useDisplaySettings";
import type {
  Issue,
  IssueListResult,
  IssueState,
  Member,
  Project,
  SubtaskRow,
  Workspace,
} from "../lib/types";
import { priorityLabel } from "../lib/types";
import PageHeader, { pageHeaderControlClass } from "../components/shell/PageHeader";
import QuickAdd from "../components/issue/QuickAdd";
import PeekDrawer from "../components/issue/PeekDrawer";
import BlockerConfirmDialog from "../components/issue/BlockerConfirmDialog";
import { usePeekParam } from "../components/issue/usePeek";
import { Badge } from "../components/ui/badge";
import { Avatar, AvatarFallback } from "../components/ui/avatar";
import { subtaskPriorityLevel } from "../components/issue/boardSubtasks";
import { splitNestedIssues } from "../components/issue/splitNestedIssues";
import {
  buildSubGroupSections,
  effectiveSubGroupBy,
  type SubGroupSection,
} from "../components/issue/subGroup";
import type { SubGroupBy } from "../components/issue/useDisplaySettings";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "../components/ui/dropdown-menu";
import { Skeleton } from "../components/ui/skeleton";
import { Alert, AlertDescription } from "../components/ui/alert";

/** Issues in one column, in board order (the API returns sort_order ASC). */
function columnIssues(all: Issue[], stateId: string): Issue[] {
  return all.filter((i) => i.state_id === stateId);
}

/** C123T0: sortable id prefix for cards inside an epic sub-group
 *  section: `subgroup-card:<sectionKey>:<issueId>`. Epic sub-sections
 *  are multi-appearance (one issue renders in several sections of one
 *  column), so plain issue ids would collide as sortable ids — the
 *  compound id keeps each copy draggable. C136T2: assignee sub-sections
 *  take the same compound id on the same branch (same multi-appearance
 *  shape). C137T2: label sub-sections take it too (same shape). The
 *  drop pipeline unwraps it
 *  (subgroupDragIssueId) before resolving the target, so drag semantics
 *  stay primary-dimension-only: sub-group sections are visual only,
 *  never drop targets that re-stamp the sub-dimension. Issue ids are
 *  UUIDs (no colons), so the LAST colon always separates the issue id. */
const SUBGROUP_CARD_PREFIX = "subgroup-card:";
/** Issue id behind a sub-group sortable id. Identity for plain and
 *  epic-compound ids, so it is safe to call on every active/over id. */
function subgroupDragIssueId(id: string): string {
  if (!id.startsWith(SUBGROUP_CARD_PREFIX)) return id;
  const rest = id.slice(SUBGROUP_CARD_PREFIX.length);
  const sep = rest.lastIndexOf(":");
  return sep === -1 ? id : rest.slice(sep + 1);
}

/** Shared chip / mini-control / tool-button classes for the board
 *  (prototype Screen 2: 22px chips, border, radius 4px, bg #111113,
 *  9px secondary, padding 0 6px, gap 5px). */
const boardChipClass =
  "inline-flex h-[22px] items-center gap-[5px] rounded-[4px] border border-border bg-[#111113] px-[6px] text-[9px] text-ink-secondary";
const boardMiniClass =
  "inline-flex h-[22px] w-[22px] flex-none items-center justify-center rounded-[4px] border border-border bg-[#111113] text-ink-muted transition-colors hover:bg-raised hover:text-ink";
const boardToolClass =
  "inline-flex h-6 w-6 items-center justify-center rounded-[4px] text-ink-muted transition-colors hover:bg-wash-hover hover:text-ink disabled:cursor-not-allowed disabled:opacity-50";

/** "…" menu on a board card: copy the issue link, clone the issue.
 *  Clicks and pointer-downs are stopped so the card neither opens the
 *  peek drawer nor starts a drag. */
function CardMoreMenu({ issue }: { issue: Issue }) {
  const { slug = "", identifier = "" } = useParams<{
    slug: string;
    identifier: string;
  }>();
  const { cloneIssue, cloning } = useCloneIssue(slug, identifier);

  function copyLink() {
    const url = `${window.location.origin}/w/${encodeURIComponent(slug)}/p/${encodeURIComponent(identifier)}/i/${issue.id}`;
    try {
      void navigator.clipboard?.writeText(url);
    } catch {
      /* clipboard unavailable (insecure context) — stay silent */
    }
  }

  return (
    <span
      className="ml-auto flex items-center"
      onClick={(e) => e.stopPropagation()}
      onPointerDown={(e) => e.stopPropagation()}
      onKeyDown={(e) => e.stopPropagation()}
    >
      <DropdownMenu>
        <DropdownMenuTrigger
          className="inline-flex h-[20px] w-[22px] items-center justify-center rounded-[4px] text-ink-muted transition-colors hover:bg-wash-hover hover:text-ink"
          title="Card actions"
          aria-label={`Actions for ${issue.display_id}`}
        >
          <MoreHorizontal className="h-[13px] w-[13px]" aria-hidden />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuItem onClick={copyLink}>
            <Link2 className="h-3.5 w-3.5" aria-hidden />
            Copy link
          </DropdownMenuItem>
          <DropdownMenuItem
            onClick={() => void cloneIssue(issue.id)}
            disabled={cloning}
          >
            <Copy className="h-3.5 w-3.5" aria-hidden />
            {cloning ? "Cloning…" : "Clone issue"}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </span>
  );
}

/** The card face shared by the sortable card and the drag overlay:
 *  ID row → title → meta chip row (prototype Screen 2). The Display
 *  panel still gates the priority chip and the assignee control; labels
 *  and dates render as extra rows only when enabled (and present).
 *  C121T1: the same gate applies to the ID text, the subtask progress
 *  bar, the module/cycle/release chips (from project_structure,
 *  guarded when undefined), and the attachment/link/dependency count
 *  rows (only when the count is > 0). */
function BoardCardBody({
  issue,
  state,
  fields,
  menu,
}: {
  issue: Issue;
  state: IssueState | undefined;
  fields: DisplayFields;
  /** Card actions menu; null for the drag-overlay ghost. */
  menu: ReactNode;
}) {
  const dateRange = formatIssueDateRange(issue.start_date, issue.target_date);
  return (
    <>
      <div className="flex items-center">
        {fields.id && (
          <span className="font-mono text-[10px] text-ink-muted">
            {issue.display_id}
          </span>
        )}
        {menu}
      </div>
      <p className="mb-[10px] mt-[5px] text-[13px] font-[570] leading-[18px] tracking-[-0.008em] text-ink">
        {issue.name}
      </p>
      <div className="flex flex-wrap gap-[4px]">
        {state && (
          <span className={boardChipClass}>
            <span
              className={`state-dot ${stateDotClass(state.group)}`}
              aria-hidden="true"
            />
            {state.name}
          </span>
        )}
        {fields.priority && (
          <span className={boardChipClass}>
            <PriorityBars priority={issue.priority} />
            {priorityLabel(issue.priority)}
          </span>
        )}
        {fields.assignees && (
          <button
            type="button"
            className={boardMiniClass}
            title="Assignees"
            aria-label={`Assignees for ${issue.display_id}`}
            onClick={(e) => e.stopPropagation()}
          >
            <User className="h-3 w-3" aria-hidden />
          </button>
        )}
        <button
          type="button"
          className={boardMiniClass}
          title="Releases"
          aria-label={`Releases for ${issue.display_id}`}
          onClick={(e) => e.stopPropagation()}
        >
          <GitBranch className="h-3 w-3" aria-hidden />
        </button>
      </div>
      {/* C17T2: compact subtask progress — renders only when the
          issue has direct subtasks. C121T1: gated by the Sub-items
          display toggle. */}
      {fields.subitems && (
        <SubtaskProgressBar progress={issue.subtask_progress} variant="compact" />
      )}
      {fields.labels && issue.labels.length > 0 && (
        <div className="mt-2 flex flex-wrap gap-1">
          {issue.labels.slice(0, 3).map((l) => (
            <Badge
              key={l.id}
              variant="secondary"
              className="gap-1 text-[11px] font-normal"
            >
              <span
                className="h-2 w-2 rounded-full"
                style={{ backgroundColor: l.color }}
                aria-hidden
              />
              {l.name}
            </Badge>
          ))}
          {issue.labels.length > 3 && (
            <Badge variant="secondary" className="text-[11px] font-normal">
              +{issue.labels.length - 3}
            </Badge>
          )}
        </div>
      )}
      {fields.dates && dateRange && (
        <div className="mt-2 flex items-center gap-1 text-[11px] text-ink-muted">
          <Calendar className="h-3 w-3" aria-hidden />
          <span>{dateRange}</span>
        </div>
      )}
      {/* C121T1: project-structure chips — module chips like labels
          (first 3 + overflow), cycle + release as single chips.
          project_structure is undefined on fetches without
          include_project_structure=1: guard it and render nothing,
          never a per-row detail fetch. */}
      {((fields.modules && (issue.project_structure?.modules.length ?? 0) > 0) ||
        (fields.cycle && issue.project_structure?.cycle) ||
        (fields.release && issue.project_structure?.release)) && (
        <div className="mt-2 flex flex-wrap gap-1">
          {fields.modules &&
            (issue.project_structure?.modules ?? [])
              .slice(0, 3)
              .map((m) => (
                <Badge
                  key={m.id}
                  variant="secondary"
                  className="text-[11px] font-normal"
                  aria-label={`Module: ${m.name}`}
                >
                  {m.name}
                </Badge>
              ))}
          {fields.modules &&
            (issue.project_structure?.modules.length ?? 0) > 3 && (
              <Badge variant="secondary" className="text-[11px] font-normal">
                +{(issue.project_structure?.modules.length ?? 0) - 3}
              </Badge>
            )}
          {fields.cycle && issue.project_structure?.cycle && (
            <Badge
              variant="secondary"
              className="text-[11px] font-normal"
              aria-label={`Cycle: ${issue.project_structure.cycle.name}`}
            >
              {issue.project_structure.cycle.name}
            </Badge>
          )}
          {fields.release && issue.project_structure?.release && (
            <Badge
              variant="secondary"
              className="text-[11px] font-normal"
              aria-label={`Release: ${issue.project_structure.release.name}`}
            >
              {issue.project_structure.release.name}
            </Badge>
          )}
        </div>
      )}
      {/* C121T1: icon + count rows in the dates-row shape, rendered
          only when the count is > 0 (undefined = not computed on
          this fetch: render nothing, never a silent zero). */}
      {fields.attachments && (issue.attachment_count ?? 0) > 0 && (
        <div
          className="mt-2 flex items-center gap-1 text-[11px] text-ink-muted"
          aria-label={`${issue.attachment_count} attachment${issue.attachment_count === 1 ? "" : "s"}`}
        >
          <Paperclip className="h-3 w-3" aria-hidden />
          <span>{issue.attachment_count}</span>
        </div>
      )}
      {fields.links && (issue.link_count ?? 0) > 0 && (
        <div
          className="mt-2 flex items-center gap-1 text-[11px] text-ink-muted"
          aria-label={`${issue.link_count} link${issue.link_count === 1 ? "" : "s"}`}
        >
          <Link2 className="h-3 w-3" aria-hidden />
          <span>{issue.link_count}</span>
        </div>
      )}
      {fields.dependencies && (issue.relation_count ?? 0) > 0 && (
        <div
          className="mt-2 flex items-center gap-1 text-[11px] text-ink-muted"
          aria-label={`${issue.relation_count} ${issue.relation_count === 1 ? "dependency" : "dependencies"}`}
        >
          <Waypoints className="h-3 w-3" aria-hidden />
          <span>{issue.relation_count}</span>
        </div>
      )}
    </>
  );
}

/** C122T1: one nested sub-work row under a parent card — existing
 *  primitives only (state indicator, title link, assignee avatar,
 *  priority icon), indented under the parent card. No drag handle:
 *  nested rows are not draggable this cycle. The row renders in the
 *  PARENT's column even when the child's own state differs; the state
 *  indicator shows the child's real state. Assignee avatars are
 *  generic glyphs: the SubtaskRow contract carries ids only, no names,
 *  so no initials are fabricated. */
function NestedSubtaskRow({
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

function SortableCard({
  issue,
  state,
  disabled,
  fields,
  onOpen,
  dragId,
  nested,
  stateById,
}: {
  issue: Issue;
  state: IssueState | undefined;
  disabled: boolean;
  fields: DisplayFields;
  onOpen: (id: string) => void;
  /** dnd-kit sortable id override — epic mode uses compound ids
   *  (epicCardDragId) so multi-appearance drops resolve the right column. */
  dragId?: string;
  /** C122T1: direct-subtask rows nested under this card (empty when
   *  the toggle is off or the fetch carried no subtasks). */
  nested?: SubtaskRow[];
  /** C122T1: state lookup for nested rows' real-state indicators. */
  stateById?: Map<string, IssueState>;
}) {
  const {
    attributes,
    listeners,
    setNodeRef,
    transform,
    transition,
    isDragging,
  } = useSortable({ id: dragId ?? issue.id, disabled });

  // Card click / Enter opens the peek drawer (keeps board context),
  // not the full detail page.
  function openIssue() {
    onOpen(issue.id);
  }

  // C122T1: the sortable node is the wrapper so a dragged parent card
  // carries its nested rows with it; the drag listeners live on the
  // card face only, so nested rows never start a drag.
  return (
    <div
      ref={setNodeRef}
      style={{
        transform: CSS.Transform.toString(transform),
        transition,
        opacity: isDragging ? 0.35 : 1,
      }}
    >
      <div
        {...attributes}
        {...listeners}
        // Compose with dnd-kit's own onKeyDown (keyboard-drag): our handler
        // runs second and only acts on Enter outside an active drag.
        onKeyDown={(e) => {
          listeners?.onKeyDown?.(e);
          if (e.key === "Enter" && !e.defaultPrevented && !isDragging) {
            openIssue();
          }
        }}
        className="cursor-pointer touch-none rounded-[6px] border border-border bg-surface p-[10px] transition-[border-color,background-color] duration-150 hover:border-[#3f3f46] hover:bg-[#17171a] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
        onClick={openIssue}
        data-board-card={issue.id}
      >
        <BoardCardBody
          issue={issue}
          state={state}
          fields={fields}
          menu={<CardMoreMenu issue={issue} />}
        />
      </div>
      {(nested ?? []).length > 0 && (
        <div className="ml-4 mt-[7px] flex flex-col gap-[2px] border-l-2 border-border pl-3">
          {(nested ?? []).map((row) => (
            <NestedSubtaskRow
              key={row.id}
              row={row}
              state={stateById?.get(row.state_id ?? "")}
              onOpen={onOpen}
            />
          ))}
        </div>
      )}
    </div>
  );
}

function BoardColumn({
  droppableId,
  title,
  state,
  priority,
  epicId,
  assigneeId,
  labelId,
  labelColor,
  moduleId,
  cycleId,
  releaseId,
  creatorId,
  stateGroup,
  byGroup,
  cardState,
  getCardDragId,
  issues,
  disabled,
  dropDisabled = false,
  disableQuickAdd = false,
  onQuickCreate,
  onQuickExpand,
  fields,
  onOpen,
  nestedByParent,
  stateById,
  subSections,
  subGroup,
}: {
  droppableId: string;
  title: string;
  /** State for state-grouped columns (drives the header state-dot). */
  state?: IssueState;
  /** Priority for priority-grouped columns (header shows amber bars). */
  priority?: number;
  /** C72T1: epic-mode columns — live epic id (null = Ungrouped). When
   *  set, the header shows the epic glyph + EpicGroupBreakdown. */
  epicId?: string | null;
  /** C129T1: assignee-mode columns — member id (null = Ungrouped).
   *  When set, the header shows the assignee glyph (Ungrouped = Ban),
   *  mirroring the epic header glyph rule — no new visual language. */
  assigneeId?: string | null;
  /** C130T1: label-mode columns — label id (null = Ungrouped).
   *  When set, the header shows the label's color dot (the same
   *  color-dot primitive the sub-group section headers already use;
   *  Ungrouped = Ban) — no new visual language. */
  labelId?: string | null;
  /** C130T1: label-mode columns — the label's color from the row. */
  labelColor?: string;
  /** C131T1: module-mode columns — module id (null = Ungrouped).
   *  When set, the header shows NO color dot: modules have no color
   *  (NamedRef carries id + name only) — name + count only.
   *  Ungrouped = Ban, mirroring the label/assignee header rule. */
  moduleId?: string | null;
  /** C132T2: cycle-mode columns — cycle id (null = Ungrouped).
   *  When set, the header shows NO color dot: cycles have no color
   *  (NamedRef carries id + name only) — name + count only.
   *  Ungrouped = Ban, mirroring the module header rule. */
  cycleId?: string | null;
  /** C133T2: release-mode columns — release id (null = Ungrouped).
   *  When set, the header shows NO color dot: releases have no color
   *  (NamedRef carries id + name only) — name + count only.
   *  Ungrouped = Ban, mirroring the cycle header rule. */
  releaseId?: string | null;
  /** C134T2: created-by-mode columns — creator user id (null =
   *  Unknown). When set, the header shows NO color dot: members have
   *  no color — name + count only. Unknown keeps the Ban glyph,
   *  mirroring the release header rule. */
  creatorId?: string | null;
  /** C135T2: state-group-mode columns — group slug (null =
   *  Unknown). When set, the header shows NO color dot: groups have
   *  no color — name + count only, NO breakdown bar. Unknown keeps
   *  the Ban glyph, mirroring the creator header rule. */
  stateGroup?: string | null;
  /** C72T1: per-state-group member counts for the epic header breakdown. */
  byGroup?: Record<string, number>;
  /** C72T1: per-card state badge — epic columns no longer imply state, so
   *  each card resolves its own. */
  cardState?: (issue: Issue) => IssueState | undefined;
  /** C72T1: dnd-kit sortable id per card — epic mode uses compound ids
   *  (epicCardDragId) so multi-appearance drops resolve the right column. */
  getCardDragId?: (issue: Issue) => string;
  issues: Issue[];
  disabled: boolean;
  /** C68T1: true when a drag is active and the project's workflow rules
   *  forbid dropping the dragged issue into this state column. The
   *  droppable is unregistered (client hint only — the server enforces)
   *  and the column dims while dragging. */
  dropDisabled?: boolean;
  /** C134T2: true under created-by grouping — per-column quick-add
   *  is DISABLED there (created_by is server-set to the actor; the
   *  create form has no creator picker). This disables ONLY the
   *  header "+" tool and the QuickAdd row: it deliberately does NOT
   *  touch `disabled`, which also gates card dragging — within-column
   *  reorder drags must stay enabled under created-by grouping. */
  disableQuickAdd?: boolean;
  onQuickCreate: (name: string) => Promise<void>;
  onQuickExpand: (name: string) => void;
  fields: DisplayFields;
  onOpen: (id: string) => void;
  /** C122T1: parent id → nested subtask rows for this column's cards. */
  nestedByParent?: Map<string, SubtaskRow[]>;
  /** C122T1: state lookup for nested rows' real-state indicators. */
  stateById?: Map<string, IssueState>;
  /** C123T0: sub-group sections for this column's cards (already
   *  show-empty-filtered by the caller). Undefined = today's flat
   *  column layout exactly. */
  subSections?: SubGroupSection[];
  /** C123T0: effective sub-group dimension (drives the epic
   *  multi-appearance drag ids below). */
  subGroup?: SubGroupBy;
}) {
  const { setNodeRef, isOver } = useDroppable({
    id: droppableId,
    disabled: dropDisabled,
  });
  /** C123T0: sortable id per card. Epic sub-sections need the compound
   *  id (one issue, several section copies); every other mode keeps
   *  today's id (epic-column compound ids included). C136T2: assignee
   *  sub-sections are multi-appearance the same way, so they take the
   *  compound id on the same branch — no second unwrapper is needed,
   *  subgroupDragIssueId strips the prefix generically (last colon
   *  separates the issue id, which also covers the Ungrouped section
   *  key "assignee:ungrouped"). C137T2: label sub-sections are
   *  multi-appearance the same way, so they join the same branch —
   *  the generic last-colon strip also covers the Ungrouped section
   *  key "label:ungrouped". C140T3: module sub-sections are
 *  multi-appearance the same way, so they join the same branch —
 *  the generic last-colon strip also covers the Ungrouped section
 *  key "module:ungrouped". */
  const cardDragId = (issue: Issue, sectionKey?: string) =>
    (subGroup === "epic" ||
      subGroup === "assignees" ||
      subGroup === "labels" ||
      subGroup === "modules") &&
    sectionKey !== undefined
      ? `${SUBGROUP_CARD_PREFIX}${sectionKey}:${issue.id}`
      : (getCardDragId?.(issue) ?? issue.id);
  // Header expand tool: widen this column for a closer look.
  const [expanded, setExpanded] = useState(false);
  // Header "+" tool: opens this column's inline quick-add.
  const [addSignal, setAddSignal] = useState(0);
  const empty = issues.length === 0;

  return (
    <div
      ref={setNodeRef}
      className={`flex min-h-[420px] shrink-0 flex-col rounded-[7px] border bg-[#0f0f11] px-[8px] pb-[8px] transition-[width,border-color,background-color] duration-150 ${
        expanded ? "w-[520px]" : "w-[325px]"
      } ${isOver ? "border-accent-blue/70" : "border-border-hairline"} ${
        dropDisabled ? "opacity-40" : ""
      }`}
      aria-disabled={dropDisabled || undefined}
    >
      <div className="flex h-[42px] flex-none items-center gap-[7px] px-[4px]">
        {epicId !== undefined ? (
          epicId === null ? (
            <Ban className="h-[13px] w-[13px] text-ink-muted" aria-hidden />
          ) : (
            <Layers
              className="h-[13px] w-[13px] text-accent-purple"
              aria-hidden
            />
          )
        ) : assigneeId !== undefined ? (
          assigneeId === null ? (
            <Ban className="h-[13px] w-[13px] text-ink-muted" aria-hidden />
          ) : (
            <User className="h-[13px] w-[13px] text-ink-muted" aria-hidden />
          )
        ) : labelId !== undefined ? (
          labelId === null ? (
            <Ban className="h-[13px] w-[13px] text-ink-muted" aria-hidden />
          ) : (
            <span
              className="h-2 w-2 rounded-full"
              style={{ backgroundColor: labelColor }}
              aria-hidden="true"
            />
          )
        ) : moduleId !== undefined ? (
          // C131T1: module columns render NO color dot (modules have
          // none); Ungrouped keeps the Ban glyph, member columns
          // render name + count only.
          moduleId === null ? (
            <Ban className="h-[13px] w-[13px] text-ink-muted" aria-hidden />
          ) : null
        ) : cycleId !== undefined ? (
          // C132T2: cycle columns render NO color dot (cycles have
          // none) — same rule as modules; Ungrouped keeps the Ban
          // glyph, member columns render name + count only.
          cycleId === null ? (
            <Ban className="h-[13px] w-[13px] text-ink-muted" aria-hidden />
          ) : null
        ) : releaseId !== undefined ? (
          // C133T2: release columns render NO color dot (releases
          // have none) — same rule as cycles; Ungrouped keeps the Ban
          // glyph, member columns render name + count only.
          releaseId === null ? (
            <Ban className="h-[13px] w-[13px] text-ink-muted" aria-hidden />
          ) : null
        ) : creatorId !== undefined ? (
          // C134T2: creator columns render NO color dot (members have
          // none) — same rule as releases; Unknown keeps the Ban
          // glyph, creator columns render name + count only.
          creatorId === null ? (
            <Ban className="h-[13px] w-[13px] text-ink-muted" aria-hidden />
          ) : null
        ) : stateGroup !== undefined ? (
          // C135T2: state-group columns render NO color dot (groups
          // have none) — same rule as creators; Unknown keeps the Ban
          // glyph, group columns render name + count only.
          stateGroup === null ? (
            <Ban className="h-[13px] w-[13px] text-ink-muted" aria-hidden />
          ) : null
        ) : state ? (
          <span
            className={`state-dot ${stateDotClass(state.group)}`}
            aria-hidden="true"
          />
        ) : priority !== undefined ? (
          <PriorityBars priority={priority} />
        ) : null}
        <span className="text-[11px] font-semibold text-ink">{title}</span>
        <span className="font-mono text-[10px] text-ink-muted">
          {issues.length}
        </span>
        <div className="ml-auto flex items-center">
          <button
            type="button"
            className={boardToolClass}
            title={expanded ? "Collapse column" : "Expand column"}
            aria-label={`${expanded ? "Collapse" : "Expand"} ${title} column`}
            aria-pressed={expanded}
            onClick={() => setExpanded((v) => !v)}
          >
            <Maximize2 className="h-[13px] w-[13px]" aria-hidden />
          </button>
          <button
            type="button"
            className={boardToolClass}
            title="Add work item"
            aria-label={`Add work item to ${title}`}
            disabled={disabled || disableQuickAdd}
            onClick={() => setAddSignal((n) => n + 1)}
          >
            <Plus className="h-[15px] w-[15px]" aria-hidden />
          </button>
        </div>
      </div>
      {/* C72T1: epic column header progress — the shared EpicGroupBreakdown
          (5 segments, no triage, done = completed + cancelled),
          byte-identical reuse from the C71 epic surfaces. */}
      {epicId !== undefined && (
        <div className="px-[4px] pb-[8px]">
          <EpicGroupBreakdown
            byGroup={byGroup ?? {}}
            total={issues.length}
            epicName={title}
          />
        </div>
      )}
      {empty ? (
        // Empty column: header + full-width new-item row only. The spacer
        // pins the footer to the column bottom (min-height 420px).
        <div className="min-h-[20px] flex-1" aria-hidden="true" />
      ) : subSections ? (
        // C123T0: sub-group sections inside the column — one
        // SortableContext for the whole column, so dragging a card
        // across sections of one column still resolves by the primary
        // column exactly as today (sections are visual only, not drop
        // targets). C122 nested rows render under their parent card
        // inside whichever section holds the parent.
        <SortableContext
          items={subSections.flatMap((s) =>
            s.issues.map((i) => cardDragId(i, s.key)),
          )}
          strategy={verticalListSortingStrategy}
        >
          <div className="flex flex-col gap-[10px]">
            {subSections.map((section) => (
              <div key={section.key} data-sub-group-section={section.key}>
                <div className="flex items-center gap-[6px] px-[4px] pb-[4px]">
                  {section.color && (
                    <span
                      className="h-2 w-2 rounded-full"
                      style={{ backgroundColor: section.color }}
                      aria-hidden="true"
                    />
                  )}
                  <span className="text-[11px] font-semibold text-ink">
                    {section.title}
                  </span>
                  <span className="font-mono text-[10px] text-ink-muted">
                    {section.issues.length}
                  </span>
                </div>
                <div className="flex flex-col gap-[7px]">
                  {section.issues.map((issue) => (
                    <SortableCard
                      key={`${section.key}:${issue.id}`}
                      issue={issue}
                      state={cardState ? cardState(issue) : state}
                      disabled={disabled}
                      fields={fields}
                      onOpen={onOpen}
                      dragId={cardDragId(issue, section.key)}
                      nested={nestedByParent?.get(issue.id) ?? []}
                      stateById={stateById}
                    />
                  ))}
                </div>
              </div>
            ))}
          </div>
        </SortableContext>
      ) : (
        <SortableContext
          items={issues.map((i) => i.id)}
          strategy={verticalListSortingStrategy}
        >
          <div className="flex flex-col gap-[7px]">
            {issues.map((issue) => (
              <SortableCard
                key={issue.id}
                issue={issue}
                state={cardState ? cardState(issue) : state}
                disabled={disabled}
                fields={fields}
                onOpen={onOpen}
                dragId={getCardDragId?.(issue)}
                nested={nestedByParent?.get(issue.id) ?? []}
                stateById={stateById}
              />
            ))}
          </div>
        </SortableContext>
      )}
      <div className={empty ? "mt-[8px]" : "mt-auto"}>
        <QuickAdd
          onCreate={onQuickCreate}
          onExpand={onQuickExpand}
          disabled={disabled || disableQuickAdd}
          startSignal={addSignal}
        />
      </div>
    </div>
  );
}

export default function Board() {
  const { slug = "", identifier = "" } = useParams<{
    slug: string;
    identifier: string;
  }>();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [activeId, setActiveId] = useState<string | null>(null);
  const [boardError, setBoardError] = useState<string | null>(null);
  // C16T3: pending open-blockers confirm on a board drag into a completed
  // column — the drop plus the blocker display IDs from the 409 details.
  const [blockerConfirm, setBlockerConfirm] = useState<{
    id: string;
    stateId: string;
    sortOrder: number;
    blockers: string[];
  } | null>(null);
  const { settings, update, updateFields, reset } = useDisplaySettings(
    slug,
    identifier,
  );
  const { peekUuid, openPeek, closePeek } = usePeekParam();
  const [filters, setFilters, clearFilters] = useIssueFilters();

  // C96T0: PQL mode on the board — mirrors the C95 detail-page pattern
  // (toggle + PqlEditor reused verbatim, probe-first, display-only
  // filtering). Default mode (pqlMode false) is byte-for-byte today's
  // behavior: the boardQuery below is untouched. In PQL mode the
  // displayed list comes from the PQL query instead; grouping and the
  // header count follow the displayed list because they derive from
  // displayedIssues. C105T0 wires the List (C91) saved-view contract
  // onto SavedViewsMenu below: saving in PQL mode stores only the
  // query, applying a view settles the mode. Board never parks the
  // visual filters (unlike List/Spreadsheet): entering PQL mode leaves
  // them untouched and exiting restores the visual board automatically.
  // The mode itself is local state (appliedQuery), not the URL — the
  // URL ?pql= is only a backstop entry point (pasted links, view
  // applies), never the source of truth.
  const [pqlMode, setPqlMode] = useState(false);
  const [appliedQuery, setAppliedQuery] = useState("");
  // A query must never leak across projects: reset synchronously
  // during render when slug/identifier change. An effect would be too
  // late — the PQL query for the new project (keyed by slug +
  // identifier + the stale query) would already have fetched once
  // against the new project. This is React's documented render-phase
  // state-update pattern: the mismatched render is discarded before
  // commit, so no PQL fetch for the new project ever fires.
  const [projectKey, setProjectKey] = useState(`${slug}:${identifier}`);
  if (projectKey !== `${slug}:${identifier}`) {
    setProjectKey(`${slug}:${identifier}`);
    setPqlMode(false);
    setAppliedQuery("");
  }

  const base = `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`;
  // Filters are part of the key: changing them refetches the board.
  // C122T1: the toggle is part of the key too — flipping it refetches
  // with / without include_subtasks=1 instead of reusing stale rows.
  const issuesKey = [
    "board-issues",
    slug,
    identifier,
    filters,
    settings.showSubWorkItems,
  ] as const;

  const projectQuery = useQuery({
    queryKey: ["project", slug, identifier],
    queryFn: () => api.get<Project>(base),
  });
  const workspaceQuery = useQuery({
    queryKey: ["workspace", slug],
    queryFn: () =>
      api.get<Workspace>(`/api/v1/workspaces/${encodeURIComponent(slug)}`),
  });
  const statesQuery = useQuery({
    queryKey: ["states", slug, identifier],
    queryFn: () =>
      api.get<{ states: IssueState[] }>(`${base}/states`).then((d) => d.states),
  });
  // C134T2: created-by grouping needs the workspace members to resolve
  // creator ids to names (label = name ?? email). The board fetched NO
  // members before this; this query uses the SAME React Query key
  // ["members", slug] and the SAME endpoint
  // GET /api/v1/workspaces/:slug/members as T1's list-page query, so
  // the cache is shared with the list page (navigating list ↔ board
  // under created-by grouping fires no second members request).
  // Gated on the grouping (not ungated): the board otherwise loads
  // exactly as today — no extra request on non-created-by boards.
  // Member.id IS the user id Issue.created_by references (verified
  // from service.ListMembers: SELECT u.id::text ... JOIN users u ON
  // u.id = m.user_id), so the lookup below keys directly on it.
  const membersQuery = useQuery({
    queryKey: ["members", slug],
    queryFn: () =>
      api
        .get<{ members: Member[] }>(
          `/api/v1/workspaces/${encodeURIComponent(slug)}/members`,
        )
        .then((d) => d.members),
    enabled: settings.groupBy === "createdBy",
  });
  const boardQuery = useQuery({
    queryKey: issuesKey,
    queryFn: async () => {
      const all: Issue[] = [];
      let cursor: string | undefined;
      do {
        const p = toApiParams(filters);
        p.set("order_by", "sort_order");
        p.set("per_page", "100");
        // C121T1: batched project-structure chips on board cards (C21T2).
        p.set("include_project_structure", "1");
        // C122T1: nested sub-work rows — only when the toggle is on;
        // off means today's flat fetch exactly, no unused data.
        if (settings.showSubWorkItems) p.set("include_subtasks", "1");
        if (cursor) p.set("cursor", cursor);
        const page = await api.get<IssueListResult>(
          `${base}/issues?${p.toString()}`,
        );
        all.push(...page.results);
        cursor = page.next_cursor;
      } while (cursor);
      return all;
    },
  });

  // Committed PQL query: same fetch shape as the visual board
  // (order_by sort_order, per_page 100, cursor loop) but pql-only
  // params — visual filters never combine with a PQL query.
  // C98T1: a LIMIT query is a single page (the server always returns
  // next_cursor null), so skip the cursor loop and fetch once.
  const pqlIssuesKey = [
    "board-issues-pql",
    slug,
    identifier,
    appliedQuery,
    settings.showSubWorkItems,
  ] as const;
  const pqlIssuesQuery = useQuery({
    queryKey: pqlIssuesKey,
    enabled: pqlMode && appliedQuery.trim() !== "",
    queryFn: async () => {
      if (pqlHasLimit(appliedQuery)) {
        const p = new URLSearchParams({
          pql: appliedQuery.trim(),
          order_by: "sort_order",
          per_page: "100",
        });
        // C121T1: batched project-structure chips on board cards (C21T2).
        p.set("include_project_structure", "1");
        // C122T1: same toggle-gated flag as the visual fetch.
        if (settings.showSubWorkItems) p.set("include_subtasks", "1");
        const page = await api.get<IssueListResult>(
          `${base}/issues?${p.toString()}`,
        );
        return page.results;
      }
      const all: Issue[] = [];
      let cursor: string | undefined;
      do {
        const p = new URLSearchParams({
          pql: appliedQuery.trim(),
          order_by: "sort_order",
          per_page: "100",
        });
        // C121T1: batched project-structure chips on board cards (C21T2).
        p.set("include_project_structure", "1");
        // C122T1: same toggle-gated flag as the visual fetch.
        if (settings.showSubWorkItems) p.set("include_subtasks", "1");
        if (cursor) p.set("cursor", cursor);
        const page = await api.get<IssueListResult>(
          `${base}/issues?${p.toString()}`,
        );
        all.push(...page.results);
        cursor = page.next_cursor;
      } while (cursor);
      return all;
    },
  });
  // Probe-first apply (C95 pattern): fetch one page with the query and
  // commit only on success. A 400 rejects, so PqlEditor shows it
  // inline and the board is untouched (appliedQuery never changes).
  // Empty query = no filter: stays in PQL mode, shows the full board.
  async function handlePqlApply(query: string): Promise<void> {
    const q = query.trim();
    if (!q) {
      setAppliedQuery("");
      return;
    }
    const p = new URLSearchParams({
      pql: q,
      order_by: "sort_order",
      per_page: "100",
    });
    // C121T1: keep the probe's param shape identical to the board fetch.
    p.set("include_project_structure", "1");
    // C122T1: same toggle-gated flag as the board fetch.
    if (settings.showSubWorkItems) p.set("include_subtasks", "1");
    await api.get<IssueListResult>(`${base}/issues?${p.toString()}`);
    setAppliedQuery(q);
  }
  function handlePqlModeChange(mode: "all" | "pql") {
    if (mode === "pql") {
      setPqlMode(true);
      return;
    }
    // Leaving PQL mode clears the applied query, so the visual-filter
    // board (already cached) is restored immediately.
    setPqlMode(false);
    setAppliedQuery("");
    // C105T0 invariant: exiting PQL mode must leave no non-blank
    // ?pql= in the URL, or the backstop below re-enters immediately.
    // Patch-merge: other params are untouched. In the normal local-PQL
    // flow the URL carries no pql, so this is a no-op.
    setFilters({ pql: "" });
  }

  // C105T0: saved-view application (List C91 pattern, adapted to
  // Board's local PQL state). The menu already wrote the view's
  // filters to the URL; this only settles the mode + committed query.
  // A PQL view enters PQL mode with that query; a visual view leaves
  // PQL mode and clears the query, so the visual board returns. This
  // also covers the menu's default-view auto-apply, which calls
  // onViewApplied too. Visual filters are never parked or cleared:
  // Board never parks, so exiting restores the visual board as-is.
  function handleSavedViewApplied(pqlQuery: string | null) {
    if (pqlQuery !== null) {
      setPqlMode(true);
      setAppliedQuery(pqlQuery);
      return;
    }
    setPqlMode(false);
    setAppliedQuery("");
  }

  // C105T0 backstop (List/Spreadsheet pattern, adapted to Board's
  // local state): a non-blank ?pql= in the URL while not in PQL mode
  // means PQL mode — covers pasted ?pql= links. Idempotent with
  // handleSavedViewApplied: the menu sets the URL then calls the
  // handler, so by the time this effect runs the mode is already
  // settled and the guard below is a no-op. Mode is never auto-cleared
  // here — leaving PQL mode happens only via the toggle above or a
  // visual view apply, both of which clear ?pql= from the URL first.
  // Deps are [filters.pql] ONLY, exactly like List/Spreadsheet: the
  // guard reads the pqlMode from the render in which the URL last
  // changed. Depending on pqlMode too would re-run this effect on
  // every mode toggle while the URL clear is still in flight (React
  // Router navigations commit separately from the toggle's urgent
  // state), re-entering PQL mode with the stale query on exit — the
  // very fight the invariant above exists to prevent.
  useEffect(() => {
    if (!pqlMode && filters.pql?.trim()) {
      setPqlMode(true);
      setAppliedQuery(filters.pql.trim());
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [filters.pql]);

  const canEdit = (workspaceQuery.data?.role ?? 0) >= 15;

  const states = useMemo(
    () =>
      [...(statesQuery.data ?? [])].sort((a, b) => a.sequence - b.sequence),
    [statesQuery.data],
  );
  const stateIds = useMemo(() => new Set(states.map((s) => s.id)), [states]);
  const stateById = useMemo(
    () => new Map(states.map((s) => [s.id, s] as const)),
    [states],
  );
  const stateNames = useMemo(
    () => new Map(states.map((s) => [s.id, s.name] as const)),
    [states],
  );
  // C68T1: workflow transition controls — client-side move hints. The
  // server enforces; this only disables disallowed drop targets and
  // names the permitted next states on a 409.
  const workflowQuery = useWorkflow(slug, identifier);
  const workflow = workflowQuery.data ?? null;
  // Stabilized so downstream memos (column grouping) don't re-evaluate
  // every render on a fresh [] identity while the query is pending.
  const issues = useMemo(() => boardQuery.data ?? [], [boardQuery.data]);
  // C96T0: the displayed list — the visual board by default, the PQL
  // result while a PQL query is committed. While a committed query is
  // still loading, keep showing the visual board rather than blanking
  // it (C95 pattern); the filtered list replaces it on arrival.
  const displayedIssues =
    pqlMode && appliedQuery.trim() !== ""
      ? (pqlIssuesQuery.data ?? issues)
      : issues;
  // C122T1: split the displayed list into visible cards + nested rows.
  // Toggle off (or a fetch without the flag) returns the flat list
  // untouched; orphans and grandchildren stay flat cards — see
  // splitNestedIssues (C124T0 shared helper, same semantics).
  // Grouping, counts and drag state all derive from the split list,
  // so a nested child never appears twice.
  const { visibleIssues: boardIssues, nestedByParent } = useMemo(
    () => splitNestedIssues(displayedIssues, settings.showSubWorkItems),
    [displayedIssues, settings.showSubWorkItems],
  );
  // The query feeding the displayed columns. Drag-drop, QuickAdd, and
  // settle-invalidations target this key, so in PQL mode a card whose
  // new state no longer matches the query leaves the board on the
  // settle refetch — the same behavior visual filters already have
  // today, documented here, deliberately NOT special-cased.
  const displayKey =
    pqlMode && appliedQuery.trim() !== "" ? pqlIssuesKey : issuesKey;
  // C72T1: in epic mode the active id is a compound epicCardDragId;
  // epicDragIssueId is identity for plain ids. C129T1: same rule for
  // assignee-mode compound ids (each unwrapper is identity for
  // non-matching ids, so chaining them is safe in every mode).
  const activeIssue = activeId
    ? (boardIssues.find(
        (i) =>
          i.id ===
          stateGroupDragIssueId(
            createdByDragIssueId(
              releaseDragIssueId(
                cycleDragIssueId(
                  moduleDragIssueId(
                    labelDragIssueId(
                      assigneeDragIssueId(
                        epicDragIssueId(subgroupDragIssueId(activeId)),
                      ),
                    ),
                  ),
                ),
              ),
            ),
          ),
      ) ?? null)
    : null;

  const patchMutation = useMutation({
    mutationFn: (body: {
      id: string;
      state_id: string;
      sort_order: number;
      ignore_blockers?: boolean;
    }) =>
      api.patch<Issue>(`${base}/issues/${body.id}`, {
        state_id: body.state_id,
        sort_order: body.sort_order,
        ...(body.ignore_blockers ? { ignore_blockers: true } : {}),
      }),
    onError: (e, body) => {
      // C16T3: the blocker guard rejected the drop — offer "complete
      // anyway" instead of just the error banner.
      if (e instanceof ApiError && e.code === "open_blockers") {
        const details = e.details as { blockers?: string[] } | undefined;
        const blockers = Array.isArray(details?.blockers)
          ? details.blockers.filter((b): b is string => typeof b === "string")
          : [];
        setBlockerConfirm({
          id: body.id,
          stateId: body.state_id,
          sortOrder: body.sort_order,
          blockers,
        });
      } else {
        // C68T1: workflow transition controls — the move was denied by the
        // project's workflow rules; name the permitted next states.
        const conflict = stateTransitionConflictMessage(e, stateNames);
        if (conflict) {
          toast.add({ title: "Move blocked", description: conflict, type: "error" });
        } else {
          setBoardError(
            e instanceof ApiError ? e.message : "Failed to move issue",
          );
        }
      }
      void queryClient.invalidateQueries({ queryKey: issuesKey });
      void queryClient.invalidateQueries({ queryKey: pqlIssuesKey });
    },
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: issuesKey });
      void queryClient.invalidateQueries({ queryKey: pqlIssuesKey });
    },
  });

  const rebalanceMutation = useMutation({
    mutationFn: (stateId: string) =>
      api.post<{ rebalanced: number }>(`${base}/issues/rebalance`, {
        state_id: stateId,
      }),
  });

  /** Priority-mode drops: the column is a priority, so the drop changes
   *  the issue's priority (no fine sort-order positioning). */
  const priorityMutation = useMutation({
    mutationFn: (body: { id: string; priority: number }) =>
      api.patch<Issue>(`${base}/issues/${body.id}`, {
        priority: body.priority,
      }),
    onError: (e) => {
      setBoardError(
        e instanceof ApiError ? e.message : "Failed to change priority",
      );
      void queryClient.invalidateQueries({ queryKey: issuesKey });
      void queryClient.invalidateQueries({ queryKey: pqlIssuesKey });
    },
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: issuesKey });
      void queryClient.invalidateQueries({ queryKey: pqlIssuesKey });
    },
  });

  /** C72T1: epic-column drops attach the issue to the target epic
   *  (the C71 attach endpoint — idempotent, multi-epic aware, never
   *  detaches other epics). */
  const epicAttachMutation = useMutation({
    mutationFn: ({ issueId, epicId }: { issueId: string; epicId: string }) =>
      api.post(`${base}/epics/${encodeURIComponent(epicId)}/issues`, {
        issue_ids: [issueId],
      }),
    onError: (e) => {
      setBoardError(
        e instanceof ApiError ? e.message : "Failed to attach issue to epic",
      );
      void queryClient.invalidateQueries({ queryKey: issuesKey });
      void queryClient.invalidateQueries({ queryKey: pqlIssuesKey });
    },
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: issuesKey });
      void queryClient.invalidateQueries({ queryKey: pqlIssuesKey });
    },
  });

  /** C129T1: assignee-column drops attach the issue to the target
   *  member (the assignee membership endpoint — idempotent,
   *  multi-assignee aware, never detaches other assignees). */
  const assigneeAttachMutation = useMutation({
    mutationFn: ({
      issueId,
      memberId,
    }: {
      issueId: string;
      memberId: string;
    }) =>
      api.post(
        `${base}/issues/${issueId}/assignees/${encodeURIComponent(memberId)}`,
      ),
    onError: (e) => {
      setBoardError(
        e instanceof ApiError ? e.message : "Failed to assign issue",
      );
      void queryClient.invalidateQueries({ queryKey: issuesKey });
      void queryClient.invalidateQueries({ queryKey: pqlIssuesKey });
    },
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: issuesKey });
      void queryClient.invalidateQueries({ queryKey: pqlIssuesKey });
    },
  });

  /** Optimistic attach: the card appears in the target assignee column
   *  immediately; the refetch on settle corrects any drift. */
  function attachToAssignee(
    issueId: string,
    memberId: string,
    memberName: string,
  ) {
    setBoardError(null);
    queryClient.setQueryData<Issue[]>(displayKey, (old) =>
      old?.map((i) =>
        i.id === issueId
          ? {
              ...i,
              assignees: [
                ...(i.assignees ?? []),
                { id: memberId, name: memberName },
              ],
            }
          : i,
      ),
    );
    assigneeAttachMutation.mutate({ issueId, memberId });
  }

  /** C130T1: label-column drops attach the issue to the target
   *  label (the label membership endpoint — idempotent,
   *  multi-label aware, never detaches other labels). */
  const labelAttachMutation = useMutation({
    mutationFn: ({
      issueId,
      labelId,
    }: {
      issueId: string;
      labelId: string;
    }) =>
      api.post(
        `${base}/issues/${issueId}/labels/${encodeURIComponent(labelId)}`,
      ),
    onError: (e) => {
      setBoardError(
        e instanceof ApiError ? e.message : "Failed to label issue",
      );
      void queryClient.invalidateQueries({ queryKey: issuesKey });
      void queryClient.invalidateQueries({ queryKey: pqlIssuesKey });
    },
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: issuesKey });
      void queryClient.invalidateQueries({ queryKey: pqlIssuesKey });
    },
  });

  /** Optimistic attach: the card appears in the target label column
   *  immediately; the refetch on settle corrects any drift. */
  function attachToLabel(
    issueId: string,
    labelId: string,
    labelName: string,
    labelColor?: string,
  ) {
    setBoardError(null);
    queryClient.setQueryData<Issue[]>(displayKey, (old) =>
      old?.map((i) =>
        i.id === issueId
          ? {
              ...i,
              labels: [
                ...(i.labels ?? []),
                { id: labelId, name: labelName, color: labelColor ?? "" },
              ],
            }
          : i,
      ),
    );
    labelAttachMutation.mutate({ issueId, labelId });
  }

  /** C131T1: module-column drops attach the issue to the target
   *  module (the module membership endpoint — idempotent,
   *  multi-module aware, never detaches other modules). */
  const moduleAttachMutation = useMutation({
    mutationFn: ({
      issueId,
      moduleId,
    }: {
      issueId: string;
      moduleId: string;
    }) =>
      api.post(
        `${base}/modules/${encodeURIComponent(moduleId)}/issues`,
        { issue_ids: [issueId] },
      ),
    onError: (e) => {
      setBoardError(
        e instanceof ApiError ? e.message : "Failed to attach issue to module",
      );
      void queryClient.invalidateQueries({ queryKey: issuesKey });
      void queryClient.invalidateQueries({ queryKey: pqlIssuesKey });
    },
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: issuesKey });
      void queryClient.invalidateQueries({ queryKey: pqlIssuesKey });
    },
  });

  /** Optimistic attach: the card appears in the target module column
   *  immediately; the refetch on settle corrects any drift. */
  function attachToModule(
    issueId: string,
    moduleId: string,
    moduleName: string,
  ) {
    setBoardError(null);
    queryClient.setQueryData<Issue[]>(displayKey, (old) =>
      old?.map((i) =>
        i.id === issueId
          ? {
              ...i,
              project_structure: {
                ...(i.project_structure ?? { modules: [] }),
                modules: [
                  ...(i.project_structure?.modules ?? []),
                  { id: moduleId, name: moduleName },
                ],
              },
            }
          : i,
      ),
    );
    moduleAttachMutation.mutate({ issueId, moduleId });
  }

  /** C132T2: cycle-column drops MOVE the issue to the target cycle
   *  (the deliberate divergence from the attach-only precedent — the
   *  single-value display would misdisplay an attach-only card under
   *  the wrong cycle). Optimistic move: the card appears in the target
   *  cycle column immediately; the invalidation below restores server
   *  state in every outcome. Add failure aborts before remove (the
   *  issue is untouched); remove failure after a successful add
   *  surfaces honestly — the issue is now in both cycles, and the
   *  refetch shows whichever the server resolves as displayed. Never
   *  changes state, so no C68 guard applies. */
  async function moveToCycle(
    issueId: string,
    cycleId: string,
    cycleName: string,
    sourceCycleId: string | null,
  ) {
    setBoardError(null);
    queryClient.setQueryData<Issue[]>(displayKey, (old) =>
      old?.map((i) =>
        i.id === issueId
          ? {
              ...i,
              project_structure: {
                ...(i.project_structure ?? { modules: [] }),
                cycle: { id: cycleId, name: cycleName },
              },
            }
          : i,
      ),
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
        result.error instanceof ApiError
          ? result.error.message
          : null;
      setBoardError(
        result.stage === "add"
          ? (detail ?? "Failed to move issue to cycle")
          : (detail
              ? `${detail} (the issue is now in both cycles)`
              : "Issue was added to the new cycle but could not be removed from the old cycle"),
      );
    }
    await queryClient.invalidateQueries({ queryKey: issuesKey });
    await queryClient.invalidateQueries({ queryKey: pqlIssuesKey });
  }

  /** C133T2: release-column drops MOVE the issue to the target release
   *  via a SINGLE assign (the deliberate divergence from the cycle
   *  add-then-remove pipeline — assign overwrites `release_id`, so no
   *  DELETE of the source is issued, ever). Optimistic move: the card
   *  appears in the target release column immediately; the
   *  invalidation below restores server state in every outcome.
   *  Assign failure means nothing changed — it surfaces honestly,
   *  never as a dual-state case (there is none: one column, one
   *  write). Never changes state, so no C68 guard applies. */
  async function moveToRelease(
    issueId: string,
    releaseId: string,
    releaseName: string,
  ) {
    setBoardError(null);
    queryClient.setQueryData<Issue[]>(displayKey, (old) =>
      old?.map((i) =>
        i.id === issueId
          ? {
              ...i,
              project_structure: {
                ...(i.project_structure ?? { modules: [] }),
                release: { id: releaseId, name: releaseName },
              },
            }
          : i,
      ),
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
      const detail =
        result.error instanceof ApiError ? result.error.message : null;
      setBoardError(detail ?? "Failed to move issue to release");
    }
    await queryClient.invalidateQueries({ queryKey: issuesKey });
    await queryClient.invalidateQueries({ queryKey: pqlIssuesKey });
  }

  /** Optimistic attach: the card appears in the target epic column
   *  immediately; the refetch on settle corrects any drift. */
  function attachToEpic(issueId: string, epicId: string, epicName: string) {
    setBoardError(null);
    queryClient.setQueryData<Issue[]>(displayKey, (old) =>
      old?.map((i) =>
        i.id === issueId
          ? {
              ...i,
              epics: [
                ...(i.epics ?? []),
                { id: epicId, name: epicName, archived_at: null },
              ],
            }
          : i,
      ),
    );
    epicAttachMutation.mutate({ issueId, epicId });
  }

  // C72T1: epic columns render one column per live epic (Option A);
  // cards keep their own state badge because the column no longer
  // implies state.
  const groupByEpic = settings.groupBy === "epic";
  // C129T1: assignee columns render one column per member (same rule
  // as epic columns — the column no longer implies state).
  const groupByAssignees = settings.groupBy === "assignees";
  // C130T1: label columns render one column per label (same rule
  // as assignee columns — the column no longer implies state).
  const groupByLabels = settings.groupBy === "labels";
  // C131T1: module columns render one column per module (same rule
  // as label columns — the column no longer implies state).
  const groupByModules = settings.groupBy === "modules";
  // C132T2: cycle columns render one column per cycle (same rule
  // as module columns — the column no longer implies state).
  const groupByCycles = settings.groupBy === "cycles";
  // C133T2: release columns render one column per release (same rule
  // as cycle columns — the column no longer implies state).
  const groupByReleases = settings.groupBy === "releases";
  // C134T2: creator columns render one column per creator (same rule
  // as release columns — the column no longer implies state).
  const groupByCreatedBy = settings.groupBy === "createdBy";
  // C135T2: state-group columns render one column per state group
  // (same rule as creator columns — the column no longer implies a
  // single state; it holds every state in the group).
  const groupByStateGroups = settings.groupBy === "stateGroups";
  // C135T2: state-by-id lookup (state id -> group string) for the
  // state-group builder. States load with the page, so unlike
  // created-by there is no pending-lookup flash to gate on.
  const stateGroupById = useMemo(
    () => new Map(states.map((s) => [s.id, s.group] as const)),
    [states],
  );
  // C134T2: members lookup for creator labels (map user id ->
  // {name, email}), mirroring T1's list-page membersById. A raw user
  // id is NEVER a column label: the builder collapses unresolved
  // creators into Unknown, and the render gates on resolution below.
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
  // Resolved = members data arrived, OR the fetch failed (then the
  // builder runs with an empty map and unresolved creators honestly
  // collapse into Unknown — an error state, not a first-paint flash).
  // While pending, the groups branch below returns NO columns, so
  // first paint never flashes every column as "Unknown" and never
  // renders a raw creator id as a label (same contract as T1's list).
  const groupingMembersReady =
    membersQuery.data !== undefined || membersQuery.isError;

  /** One board column: a state (default), a priority bucket, or a live
   *  epic, depending on the Display panel's Group by setting. */
  interface BoardGroup {
    droppableId: string;
    title: string;
    /** State for state-grouped columns (header dot + card state chips). */
    state?: IssueState;
    /** Priority for priority-grouped columns (header amber bars). */
    priority?: number;
    /** C72T1: epic-mode columns — live epic id (null = Ungrouped). */
    epicId?: string | null;
    /** C129T1: assignee-mode columns — member id (null = Ungrouped). */
    assigneeId?: string | null;
    /** C130T1: label-mode columns — label id (null = Ungrouped). */
    labelId?: string | null;
    /** C130T1: label-mode columns — the label's color from the row. */
    labelColor?: string;
    /** C131T1: module-mode columns — module id (null = Ungrouped).
     *  No color: modules have none. */
    moduleId?: string | null;
    /** C132T2: cycle-mode columns — cycle id (null = Ungrouped).
     *  No color: cycles have none, same rule as modules. */
    cycleId?: string | null;
    /** C133T2: release-mode columns — release id (null = Ungrouped).
     *  No color: releases have none, same rule as cycles. */
    releaseId?: string | null;
    /** C134T2: created-by-mode columns — creator user id (null =
     *  Unknown). No color: members have none, same rule as releases. */
    creatorId?: string | null;
    /** C135T2: state-group-mode columns — group slug (null =
     *  Unknown). No color: groups have none, same rule as creators. */
    stateGroup?: string | null;
    /** C72T1: column key for epic-mode dnd ids. C129T1: same field
     *  carries the assignee column key (member id / "ungrouped").
     *  C130T1: same field carries the label column key
     *  (label id / "ungrouped"). C131T1: same field carries the
     *  module column key (module id / "ungrouped"). C132T2: same
     *  field carries the cycle column key (cycle id / "ungrouped").
     *  C133T2: same field carries the release column key
     *  (release id / "ungrouped"). C134T2: same field carries the
     *  creator column key (creator user id / "unknown"). C135T2:
     *  same field carries the state-group column key (group slug /
     *  "unknown"). */
    columnKey?: string;
    /** C72T1: per-state-group member counts for the epic header breakdown. */
    byGroup?: Record<string, number>;
    issues: Issue[];
    /** State/priority stamped onto quick-added issues in this column. */
    newStateId?: string;
    newPriority?: number;
    /** C123T0: sub-group sections for this column (undefined = flat). */
    subSections?: SubGroupSection[];
  }

  // C123T0: effective sub-group dimension (never equals groupBy,
  // "none" when groupBy is "none") — shared helper, same rule the
  // list view (T1) consumes.
  const effectiveSubGroup = effectiveSubGroupBy(settings);

  const groups = useMemo<BoardGroup[]>(() => {
    let gs: BoardGroup[];
    if (groupByEpic) {
      // C72T1: group by LIVE epic membership (see boardEpic.ts for the
      // multi-appearance + archived-exclusion + Ungrouped-last semantics).
      // Progress counts come from the loaded states — no extra fetches
      // (the C70/C71 derived-data discipline).
      gs = buildEpicBoardGroups(
        boardIssues,
        (i) => stateById.get(i.state_id)?.group,
        states[0]?.id,
      ).map((g) => ({
        droppableId: g.droppableId,
        title: g.title,
        columnKey: g.columnKey,
        epicId: g.epicId,
        byGroup: g.byGroup,
        issues: g.issues,
        newStateId: g.newStateId,
      }));
    } else if (groupByAssignees) {
      // C129T1: group by assignee membership (see boardAssignee.ts for
      // the multi-appearance + Ungrouped-last semantics, built on
      // T0's groupIssuesByAssignee). Quick-add stamps the first sorted
      // state, mirroring the epic columns. "Show empty groups"
      // synthesizes no member columns: the builder only emits columns
      // for members on loaded issues, and the empty-group filter below
      // drops nothing here because no built column is ever empty.
      gs = buildAssigneeBoardGroups(boardIssues, states[0]?.id).map((g) => ({
        droppableId: g.droppableId,
        title: g.title,
        columnKey: g.columnKey,
        assigneeId: g.assigneeId,
        issues: g.issues,
        newStateId: g.newStateId,
      }));
    } else if (groupByLabels) {
      // C130T1: group by label membership (see boardLabel.ts for
      // the multi-appearance + Ungrouped-last semantics, built on
      // T0's groupIssuesByLabel). Quick-add stamps the first sorted
      // state, mirroring the assignee columns. "Show empty groups"
      // synthesizes no label columns: the builder only emits columns
      // for labels on loaded issues, and the empty-group filter below
      // drops nothing here because no built column is ever empty.
      gs = buildLabelBoardGroups(boardIssues, states[0]?.id).map((g) => ({
        droppableId: g.droppableId,
        title: g.title,
        columnKey: g.columnKey,
        labelId: g.labelId,
        labelColor: g.color,
        issues: g.issues,
        newStateId: g.newStateId,
      }));
    } else if (groupByModules) {
      // C131T1: group by module membership (see boardModule.ts for
      // the multi-appearance + Ungrouped-last semantics, built on
      // T0's groupIssuesByModule). Quick-add stamps the first sorted
      // state, mirroring the label columns. "Show empty groups"
      // synthesizes no module columns: the builder only emits columns
      // for modules on loaded issues, and the empty-group filter below
      // drops nothing here because no built column is ever empty.
      // Module headers render name + count with NO color dot —
      // modules have no color (see BoardColumn's moduleId branch).
      gs = buildModuleBoardGroups(boardIssues, states[0]?.id).map((g) => ({
        droppableId: g.droppableId,
        title: g.title,
        columnKey: g.columnKey,
        moduleId: g.moduleId,
        issues: g.issues,
        newStateId: g.newStateId,
      }));
    } else if (groupByCycles) {
      // C132T2: group by displayed cycle (see boardCycle.ts for the
      // single-appearance + Ungrouped-last semantics, built on T1's
      // groupIssuesByCycle). Quick-add stamps the first sorted
      // state, mirroring the module columns. "Show empty groups"
      // synthesizes no cycle columns: the builder only emits columns
      // for cycles on loaded issues, and the empty-group filter below
      // drops nothing here because no built column is ever empty.
      // Cycle headers render name + count with NO color dot —
      // cycles have no color (see BoardColumn's cycleId branch).
      // Sub-group sections compose via the C123 machinery below
      // exactly as under module columns.
      gs = buildCycleBoardGroups(boardIssues, states[0]?.id).map((g) => ({
        droppableId: g.droppableId,
        title: g.title,
        columnKey: g.columnKey,
        cycleId: g.cycleId,
        issues: g.issues,
        newStateId: g.newStateId,
      }));
    } else if (groupByReleases) {
      // C133T2: group by displayed release (see boardRelease.ts for
      // the single-appearance + Ungrouped-last semantics, built on
      // T1's groupIssuesByRelease). Quick-add stamps the first sorted
      // state, mirroring the cycle columns. "Show empty groups"
      // synthesizes no release columns: the builder only emits columns
      // for releases on loaded issues, and the empty-group filter below
      // drops nothing here because no built column is ever empty.
      // Release headers render name + count with NO color dot —
      // releases have no color (see BoardColumn's releaseId branch).
      // Sub-group sections compose via the C123 machinery below
      // exactly as under cycle columns.
      gs = buildReleaseBoardGroups(boardIssues, states[0]?.id).map((g) => ({
        droppableId: g.droppableId,
        title: g.title,
        columnKey: g.columnKey,
        releaseId: g.releaseId,
        issues: g.issues,
        newStateId: g.newStateId,
      }));
    } else if (groupByCreatedBy) {
      // C134T2: group by creator (see boardCreatedBy.ts for the
      // single-appearance + Unknown-last/hidden-when-empty semantics,
      // built on T1's groupIssuesByCreatedBy). Quick-add is DISABLED
      // on these columns (rendered via disableQuickAdd below — the
      // newStateId stamp is carried for shape uniformity only and is
      // never consumed under created-by grouping). "Show empty
      // groups" synthesizes no creator columns: the builder only
      // emits columns for creators on loaded issues, and the
      // empty-group filter below drops nothing here because no built
      // column is ever empty. Creator headers render name + count
      // with NO color dot — members have no color (see BoardColumn's
      // creatorId branch). Sub-group sections compose via the C123
      // machinery below exactly as under release columns. While the
      // members query is pending, NO columns render at all (not
      // Unknown columns): groupingMembersReady gates here so first
      // paint never flashes every column as "Unknown" and never
      // renders a raw creator id as a label. Columns derive from
      // visible rows only (C72T0 live-membership rule, same as every
      // dimension) — filters narrow boardIssues before this branch.
      gs = groupingMembersReady
        ? buildCreatedByBoardGroups(
            boardIssues,
            membersById,
            states[0]?.id,
          ).map((g) => ({
            droppableId: g.droppableId,
            title: g.title,
            columnKey: g.columnKey,
            creatorId: g.creatorId,
            issues: g.issues,
            newStateId: g.newStateId,
          }))
        : [];
    } else if (groupByStateGroups) {
      // C135T2: group by state group (see boardStateGroup.ts for
      // the canonical-six-order + Unknown-last/hidden-when-empty
      // semantics, built on T1's groupIssuesByStateGroup).
      // Per-column quick-add IS enabled: newStateId is the
      // first-by-sequence state in the group (states is
      // sequence-sorted), so the new card STAYS in the column; a
      // column with no states at all gets newStateId undefined and
      // renders its quick-add disabled via disableQuickAdd below.
      // "Show empty groups" synthesizes all six canonical groups
      // via the builder's includeEmptyGroups option (closed set);
      // Unknown renders only when it has rows, in both modes, and
      // the empty-group filter below drops nothing here beyond
      // what the builder already hides when show-empty is off.
      // State-group headers render name + count with NO color dot
      // and NO breakdown bar (see BoardColumn's stateGroup branch).
      // Sub-group sections compose via the C123 machinery below
      // exactly as under creator columns. Columns derive from
      // visible rows only (C72T0 live-membership rule, same as
      // every dimension) — filters narrow boardIssues before this
      // branch. States load with the page, so no resolution gate.
      gs = buildStateGroupBoardGroups(
        boardIssues,
        stateGroupById,
        states,
        { includeEmptyGroups: settings.showEmptyGroups },
      ).map((g) => ({
        droppableId: g.droppableId,
        title: g.title,
        columnKey: g.columnKey,
        stateGroup: g.stateGroup,
        issues: g.issues,
        newStateId: g.newStateId,
      }));
    } else if (settings.groupBy === "priority") {
      gs = [4, 3, 2, 1, 0].map((p) => ({
        droppableId: `priority:${p}`,
        title: priorityLabel(p),
        priority: p,
        issues: boardIssues.filter((i) => i.priority === p),
        newStateId: states[0]?.id,
        newPriority: p,
      }));
    } else {
      gs = states.map((s) => ({
        droppableId: s.id,
        title: s.name,
        state: s,
        issues: columnIssues(boardIssues, s.id),
        newStateId: s.id,
      }));
    }
    // C123T0: sub-group sections per column (show-empty filtering
    // here at the call site, not in the helper). When the effective
    // sub-group is "none", columns stay exactly as today.
    const withSub =
      effectiveSubGroup === "none"
        ? gs
        : gs.map((g) => {
            const sections = buildSubGroupSections(
              g.issues,
              effectiveSubGroup,
              { states, stateById, membersById, stateGroupById },
            );
            return {
              ...g,
              subSections: settings.showEmptyGroups
                ? sections
                : sections.filter((s) => s.issues.length > 0),
            };
          });
    return settings.showEmptyGroups
      ? withSub
      : withSub.filter((g) => g.issues.length > 0);
  }, [
    groupByEpic,
    groupByAssignees,
    groupByLabels,
    groupByModules,
    groupByCycles,
    groupByReleases,
    groupByCreatedBy,
    groupByStateGroups,
    groupingMembersReady,
    membersById,
    stateGroupById,
    settings.groupBy,
    settings.showEmptyGroups,
    states,
    stateById,
    boardIssues,
    effectiveSubGroup,
  ]);

  // C68T1: per-column drop hint while a drag is active — the set of
  // droppable IDs the project's workflow rules forbid the dragged issue
  // from landing in (state columns only). C72T1: no transition hints for
  // epic columns — epic drops attach without changing state (priority
  // drops re-stamp priority, also exempt). C129T1: same exemption for
  // assignee columns — assignee drops attach without changing state.
  // C130T1: same exemption for label columns — label drops attach
  // without changing state. C131T1: same exemption for module
  // columns — module drops attach without changing state. C132T2:
  // same exemption for cycle columns — cycle drops MOVE without
  // changing state. C133T2: same exemption for release columns —
  // release drops MOVE (single assign) without changing state.
  // C134T2: same exemption for creator columns — creator drops are a
  // deliberate no-op and never change state. C135T2: same exemption
  // for state-group columns — state-group drops are a deliberate
  // no-op and never change state. VERIFIED (not assumed):
  // the guard below reads `settings.groupBy !== "state"` — the hint
  // set is empty in every non-state grouping, so no code change is
  // needed here for created-by either. Creator columns are NOT state
  // columns, so no workflow transition guard applies on this path.
  const dropBlocked = useMemo(() => {
    const blocked = new Set<string>();
    if (!workflow || !activeIssue || settings.groupBy !== "state") {
      return blocked;
    }
    for (const g of groups) {
      if (
        g.state &&
        !isTransitionAllowed(workflow, activeIssue.state_id, g.state.id)
      ) {
        blocked.add(g.droppableId);
      }
    }
    return blocked;
  }, [workflow, activeIssue, settings.groupBy, groups]);

  /** Inline quick-add: title only; the column stamps state (and priority
   *  in priority-grouped mode). C72T1: an epic column's quick-add also
   *  attaches the new issue to the column's epic (Ungrouped = plain
   *  create, no attach). C129T1: an assignee column's quick-add also
   *  attaches the new issue to the column's member (same rule).
   *  C130T1: a label column's quick-add also attaches the new issue
   *  to the column's label (same rule). C131T1: a module column's
   *  quick-add also attaches the new issue to the column's module
   *  (same rule). C132T2: a cycle column's quick-add also attaches
   *  the new issue to the column's cycle (same rule). C133T2: a
   *  release column's quick-add also assigns the new issue to the
   *  column's release (same rule). C135T2: a state-group column's
   *  quick-add needs no attach — its newStateId stamp (first-by-
   *  sequence state in the group) keeps the new card in the column;
   *  a stateless column never reaches this function (its QuickAdd
   *  renders disabled via disableQuickAdd). The
   *  new card appears via query invalidation — no full reload. */
  async function quickCreate(group: BoardGroup, name: string): Promise<void> {
    // C134T2: created-by columns never reach this function — their
    // QuickAdd is rendered disabled (disableQuickAdd) — but the
    // bypass below keeps the unreachable path harmless too: no epic
    // plan runs, and every attach below resolves to null under
    // created-by grouping, so no creator attach is ever attempted.
    const { stateId, attachEpicId } =
      groupByAssignees ||
      groupByLabels ||
      groupByModules ||
      groupByCycles ||
      groupByReleases ||
      groupByCreatedBy ||
      groupByStateGroups
        ? { stateId: group.newStateId, attachEpicId: null as string | null }
        : planEpicQuickAdd(group);
    const { attachAssigneeId } = groupByAssignees
      ? planAssigneeQuickAdd(group)
      : { attachAssigneeId: null as string | null };
    const { attachLabelId } = groupByLabels
      ? planLabelQuickAdd(group)
      : { attachLabelId: null as string | null };
    const { attachModuleId } = groupByModules
      ? planModuleQuickAdd(group)
      : { attachModuleId: null as string | null };
    const { attachCycleId } = groupByCycles
      ? planCycleQuickAdd(group)
      : { attachCycleId: null as string | null };
    const { attachReleaseId } = groupByReleases
      ? planReleaseQuickAdd(group)
      : { attachReleaseId: null as string | null };
    const created = await api.post<Issue>(`${base}/issues`, {
      name,
      ...(stateId ? { state_id: stateId } : {}),
      ...(group.newPriority !== undefined
        ? { priority: group.newPriority }
        : {}),
    });
    if (attachEpicId) {
      try {
        await api.post(
          `${base}/epics/${encodeURIComponent(attachEpicId)}/issues`,
          { issue_ids: [created.id] },
        );
      } catch (e) {
        // The issue exists; only the attach failed — surface it and let
        // the invalidation below show the card in Ungrouped.
        setBoardError(
          e instanceof ApiError ? e.message : "Failed to attach issue to epic",
        );
      }
    }
    if (attachAssigneeId) {
      try {
        await api.post(
          `${base}/issues/${created.id}/assignees/${encodeURIComponent(attachAssigneeId)}`,
        );
      } catch (e) {
        // Same rule as the epic path: the issue exists, only the
        // attach failed — surface it and let the invalidation below
        // show the card in Ungrouped.
        setBoardError(
          e instanceof ApiError ? e.message : "Failed to assign issue",
        );
      }
    }
    if (attachLabelId) {
      try {
        await api.post(
          `${base}/issues/${created.id}/labels/${encodeURIComponent(attachLabelId)}`,
        );
      } catch (e) {
        // Same rule as the assignee path: the issue exists, only the
        // attach failed — surface it and let the invalidation below
        // show the card in Ungrouped.
        setBoardError(
          e instanceof ApiError ? e.message : "Failed to label issue",
        );
      }
    }
    if (attachModuleId) {
      try {
        await api.post(
          `${base}/modules/${encodeURIComponent(attachModuleId)}/issues`,
          { issue_ids: [created.id] },
        );
      } catch (e) {
        // Same rule as the label path: the issue exists, only the
        // attach failed — surface it and let the invalidation below
        // show the card in Ungrouped.
        setBoardError(
          e instanceof ApiError
            ? e.message
            : "Failed to attach issue to module",
        );
      }
    }
    if (attachCycleId) {
      try {
        await api.post(
          `${base}/cycles/${encodeURIComponent(attachCycleId)}/issues`,
          { issue_ids: [created.id] },
        );
      } catch (e) {
        // Same rule as the module path: the issue exists, only the
        // attach failed — surface it and let the invalidation below
        // show the card in Ungrouped.
        setBoardError(
          e instanceof ApiError
            ? e.message
            : "Failed to attach issue to cycle",
        );
      }
    }
    if (attachReleaseId) {
      try {
        await api.post(
          `${base}/releases/${encodeURIComponent(attachReleaseId)}/issues`,
          { issue_ids: [created.id] },
        );
      } catch (e) {
        // Same rule as the cycle path: the issue exists, only the
        // assign failed — surface it and let the invalidation below
        // show the card in Ungrouped.
        setBoardError(
          e instanceof ApiError
            ? e.message
            : "Failed to attach issue to release",
        );
      }
    }
    await queryClient.invalidateQueries({ queryKey: issuesKey });
    await queryClient.invalidateQueries({ queryKey: pqlIssuesKey });
  }

  /** Expand a column quick-add into the full create form on the list
   *  page, keeping the typed title and the column's state/priority.
   *  C129T1: an assignee column also carries its member id, which the
   *  list page attaches after create (its dialog has no assignee
   *  field — see Issues.tsx's pendingAssigneeRef); Ungrouped columns
   *  carry title + state only. C130T1: a label column also carries
   *  its label id, which the list page attaches after create the same
   *  way (its dialog has no label field either — see Issues.tsx's
   *  pendingLabelRef, C130T0); Ungrouped label columns carry title +
   *  state only. C131T1: a module column also carries its module id,
   *  which the list page attaches after create the same way (its
   *  dialog has no module field either — see Issues.tsx's
   *  pendingModuleRef, C131T0); Ungrouped module columns carry title +
   *  state only. C132T2: a cycle column also carries its cycle id
   *  the same way (its dialog has no cycle field either — see
   *  Issues.tsx's pendingCycleRef, C132T1: T1 built the
   *  newIssueCycleId bridge for this, so NO list-page edits are
   *  needed in T2); Ungrouped cycle columns carry title + state
   *  only. C133T2: a release column also carries its release id
   *  the same way (its dialog has no release field either — see
   *  Issues.tsx's pendingReleaseRef, C133T1: T1 built the
   *  newIssueReleaseId bridge for this, so NO list-page edits are
   *  needed in T2); Ungrouped release columns carry title + state
   *  only. C134T2: a creator column's expand carries NOTHING for
   *  created-by — there is no creator field in the bridge (the
   *  default expand below carries title + state only, no creator
   *  id), and the column's QuickAdd is disabled so the expand is
   *  unreachable from a creator column anyway; the global add stays
   *  available. C135T2: a state-group column's expand carries
   *  NOTHING for the group either — the default expand below
   *  carries title + the column's state stamp (first-by-sequence
   *  state in the group), which keeps the created card in the
   *  column; a stateless column's QuickAdd is disabled so its
   *  expand is unreachable anyway. */
  function quickExpand(group: BoardGroup, name: string) {
    navigate(`/w/${slug}/p/${identifier}`, {
      state: groupByReleases
        ? planBoardReleaseQuickExpand(group, name)
        : groupByCycles
          ? planBoardCycleQuickExpand(group, name)
          : groupByModules
            ? planBoardModuleQuickExpand(group, name)
            : groupByLabels
              ? planBoardLabelQuickExpand(group, name)
              : planBoardQuickExpand(group, name),
    });
  }

  function writeDrop(
    activeId: string,
    destStateId: string,
    sortOrder: number,
    ignoreBlockers = false,
  ) {
    setBoardError(null);
    queryClient.setQueryData<Issue[]>(displayKey, (old) =>
      old?.map((i) =>
        i.id === activeId
          ? { ...i, state_id: destStateId, sort_order: sortOrder }
          : i,
      ),
    );
    patchMutation.mutate({
      id: activeId,
      state_id: destStateId,
      sort_order: sortOrder,
      ...(ignoreBlockers ? { ignore_blockers: true } : {}),
    });
  }

  /** Full drop pipeline: dropSortOrder → null means the gap collapsed,
   *  so rebalance the column, refetch, and retry the drop once. */
  async function applyDrop(
    activeId: string,
    sourceStateId: string,
    destStateId: string,
    destIndex: number,
  ) {
    const fresh0 = queryClient.getQueryData<Issue[]>(displayKey) ?? [];
    const so0 = dropSortOrder(
      fresh0,
      activeId,
      sourceStateId,
      destStateId,
      destIndex,
    );
    if (so0 !== null) {
      writeDrop(activeId, destStateId, so0);
      return;
    }
    // Gap collapsed: rebalance, refetch, retry once.
    try {
      await rebalanceMutation.mutateAsync(destStateId);
    } catch (e) {
      setBoardError(
        e instanceof ApiError ? e.message : "Failed to rebalance column",
      );
      return;
    }
    await queryClient.invalidateQueries({ queryKey: issuesKey });
    await queryClient.invalidateQueries({ queryKey: pqlIssuesKey });
    // Retry against the displayed list: the PQL query in PQL mode,
    // the visual board query otherwise.
    const refetched =
      pqlMode && appliedQuery.trim() !== ""
        ? await pqlIssuesQuery.refetch()
        : await boardQuery.refetch();
    const fresh = refetched.data ?? [];
    const so1 = dropSortOrder(
      fresh,
      activeId,
      sourceStateId,
      destStateId,
      destIndex,
    );
    if (so1 === null) {
      setBoardError("Couldn't place the card even after rebalancing.");
      return;
    }
    writeDrop(activeId, destStateId, so1);
  }

  /** C129T1: within-column reorder for assignee columns — sort_order
   *  only, never a reassignment. The column's visible sequence is the
   *  stack (C126 list-stack math, reused verbatim): the drop position
   *  is computed against the column's own cards, not the dragged
   *  issue's state column, because an assignee column mixes states.
   *  Gap collapse follows the board's rebalance → refetch → retry-once
   *  pipeline, then an honest error (never a loop). `overIssueId`
   *  null = dropped on the column itself (append at the end). */
  async function applyAssigneeReorder(
    issueId: string,
    stateId: string,
    columnKey: string,
    overIssueId: string | null,
  ) {
    const col = groups.find((g) => g.columnKey === columnKey);
    if (!col) return;
    const write = (sortOrder: number) => writeDrop(issueId, stateId, sortOrder);
    if (overIssueId === null) {
      const without = col.issues.filter((i) => i.id !== issueId);
      if (without.length === 0) return;
      if (col.issues[col.issues.length - 1]?.id === issueId) return;
      const so = midpoint(without[without.length - 1].sort_order, null);
      if (so === null) return;
      write(so);
      return;
    }
    const drop = stackDropSortOrder(col.issues, issueId, overIssueId);
    if (!drop.moved) return;
    if (drop.sortOrder !== null) {
      write(drop.sortOrder);
      return;
    }
    // Gap collapsed: rebalance the issue's state column, refetch, and
    // retry once against the fresh column sequence.
    try {
      await rebalanceMutation.mutateAsync(stateId);
    } catch (e) {
      setBoardError(
        e instanceof ApiError ? e.message : "Failed to rebalance column",
      );
      return;
    }
    await queryClient.invalidateQueries({ queryKey: issuesKey });
    await queryClient.invalidateQueries({ queryKey: pqlIssuesKey });
    const refetched =
      pqlMode && appliedQuery.trim() !== ""
        ? await pqlIssuesQuery.refetch()
        : await boardQuery.refetch();
    const fresh = refetched.data ?? [];
    const freshVisible = splitNestedIssues(
      fresh,
      settings.showSubWorkItems,
    ).visibleIssues;
    const freshCol = buildAssigneeBoardGroups(
      freshVisible,
      states[0]?.id,
    ).find((g) => g.columnKey === columnKey);
    if (!freshCol) {
      setBoardError("Couldn't place the card even after rebalancing.");
      return;
    }
    const retry = stackDropSortOrder(freshCol.issues, issueId, overIssueId);
    if (!retry.moved || retry.sortOrder === null) {
      setBoardError("Couldn't place the card even after rebalancing.");
      return;
    }
    write(retry.sortOrder);
  }

  /** C130T1: within-column reorder for label columns — sort_order
   *  only, never a re-label. Mirrors applyAssigneeReorder verbatim
   *  with the label column builder: the column's visible sequence is
   *  the stack, because a label column mixes states. Gap collapse
   *  follows the same rebalance → refetch → retry-once pipeline, then
   *  an honest error (never a loop). `overIssueId` null = dropped on
   *  the column itself (append at the end). */
  async function applyLabelReorder(
    issueId: string,
    stateId: string,
    columnKey: string,
    overIssueId: string | null,
  ) {
    const col = groups.find((g) => g.columnKey === columnKey);
    if (!col) return;
    const write = (sortOrder: number) => writeDrop(issueId, stateId, sortOrder);
    if (overIssueId === null) {
      const without = col.issues.filter((i) => i.id !== issueId);
      if (without.length === 0) return;
      if (col.issues[col.issues.length - 1]?.id === issueId) return;
      const so = midpoint(without[without.length - 1].sort_order, null);
      if (so === null) return;
      write(so);
      return;
    }
    const drop = stackDropSortOrder(col.issues, issueId, overIssueId);
    if (!drop.moved) return;
    if (drop.sortOrder !== null) {
      write(drop.sortOrder);
      return;
    }
    // Gap collapsed: rebalance the issue's state column, refetch, and
    // retry once against the fresh column sequence.
    try {
      await rebalanceMutation.mutateAsync(stateId);
    } catch (e) {
      setBoardError(
        e instanceof ApiError ? e.message : "Failed to rebalance column",
      );
      return;
    }
    await queryClient.invalidateQueries({ queryKey: issuesKey });
    await queryClient.invalidateQueries({ queryKey: pqlIssuesKey });
    const refetched =
      pqlMode && appliedQuery.trim() !== ""
        ? await pqlIssuesQuery.refetch()
        : await boardQuery.refetch();
    const fresh = refetched.data ?? [];
    const freshVisible = splitNestedIssues(
      fresh,
      settings.showSubWorkItems,
    ).visibleIssues;
    const freshCol = buildLabelBoardGroups(
      freshVisible,
      states[0]?.id,
    ).find((g) => g.columnKey === columnKey);
    if (!freshCol) {
      setBoardError("Couldn't place the card even after rebalancing.");
      return;
    }
    const retry = stackDropSortOrder(freshCol.issues, issueId, overIssueId);
    if (!retry.moved || retry.sortOrder === null) {
      setBoardError("Couldn't place the card even after rebalancing.");
      return;
    }
    write(retry.sortOrder);
  }

  /** C131T1: within-column reorder for module columns — sort_order
   *  only, never a re-module. Mirrors applyLabelReorder verbatim
   *  with the module column builder: the column's visible sequence is
   *  the stack, because a module column mixes states. Gap collapse
   *  follows the same rebalance → refetch → retry-once pipeline, then
   *  an honest error (never a loop). `overIssueId` null = dropped on
   *  the column itself (append at the end). */
  async function applyModuleReorder(
    issueId: string,
    stateId: string,
    columnKey: string,
    overIssueId: string | null,
  ) {
    const col = groups.find((g) => g.columnKey === columnKey);
    if (!col) return;
    const write = (sortOrder: number) => writeDrop(issueId, stateId, sortOrder);
    if (overIssueId === null) {
      const without = col.issues.filter((i) => i.id !== issueId);
      if (without.length === 0) return;
      if (col.issues[col.issues.length - 1]?.id === issueId) return;
      const so = midpoint(without[without.length - 1].sort_order, null);
      if (so === null) return;
      write(so);
      return;
    }
    const drop = stackDropSortOrder(col.issues, issueId, overIssueId);
    if (!drop.moved) return;
    if (drop.sortOrder !== null) {
      write(drop.sortOrder);
      return;
    }
    // Gap collapsed: rebalance the issue's state column, refetch, and
    // retry once against the fresh column sequence.
    try {
      await rebalanceMutation.mutateAsync(stateId);
    } catch (e) {
      setBoardError(
        e instanceof ApiError ? e.message : "Failed to rebalance column",
      );
      return;
    }
    await queryClient.invalidateQueries({ queryKey: issuesKey });
    await queryClient.invalidateQueries({ queryKey: pqlIssuesKey });
    const refetched =
      pqlMode && appliedQuery.trim() !== ""
        ? await pqlIssuesQuery.refetch()
        : await boardQuery.refetch();
    const fresh = refetched.data ?? [];
    const freshVisible = splitNestedIssues(
      fresh,
      settings.showSubWorkItems,
    ).visibleIssues;
    const freshCol = buildModuleBoardGroups(
      freshVisible,
      states[0]?.id,
    ).find((g) => g.columnKey === columnKey);
    if (!freshCol) {
      setBoardError("Couldn't place the card even after rebalancing.");
      return;
    }
    const retry = stackDropSortOrder(freshCol.issues, issueId, overIssueId);
    if (!retry.moved || retry.sortOrder === null) {
      setBoardError("Couldn't place the card even after rebalancing.");
      return;
    }
    write(retry.sortOrder);
  }

  /** C132T2: within-column reorder for cycle columns — sort_order
   *  only, never a re-cycle. Mirrors applyModuleReorder verbatim
   *  with the cycle column builder: the column's visible sequence is
   *  the stack, because a cycle column mixes states. Gap collapse
   *  follows the same rebalance → refetch → retry-once pipeline, then
   *  an honest error (never a loop). `overIssueId` null = dropped on
   *  the column itself (append at the end). */
  async function applyCycleReorder(
    issueId: string,
    stateId: string,
    columnKey: string,
    overIssueId: string | null,
  ) {
    const col = groups.find((g) => g.columnKey === columnKey);
    if (!col) return;
    const write = (sortOrder: number) => writeDrop(issueId, stateId, sortOrder);
    if (overIssueId === null) {
      const without = col.issues.filter((i) => i.id !== issueId);
      if (without.length === 0) return;
      if (col.issues[col.issues.length - 1]?.id === issueId) return;
      const so = midpoint(without[without.length - 1].sort_order, null);
      if (so === null) return;
      write(so);
      return;
    }
    const drop = stackDropSortOrder(col.issues, issueId, overIssueId);
    if (!drop.moved) return;
    if (drop.sortOrder !== null) {
      write(drop.sortOrder);
      return;
    }
    // Gap collapsed: rebalance the issue's state column, refetch, and
    // retry once against the fresh column sequence.
    try {
      await rebalanceMutation.mutateAsync(stateId);
    } catch (e) {
      setBoardError(
        e instanceof ApiError ? e.message : "Failed to rebalance column",
      );
      return;
    }
    await queryClient.invalidateQueries({ queryKey: issuesKey });
    await queryClient.invalidateQueries({ queryKey: pqlIssuesKey });
    const refetched =
      pqlMode && appliedQuery.trim() !== ""
        ? await pqlIssuesQuery.refetch()
        : await boardQuery.refetch();
    const fresh = refetched.data ?? [];
    const freshVisible = splitNestedIssues(
      fresh,
      settings.showSubWorkItems,
    ).visibleIssues;
    const freshCol = buildCycleBoardGroups(
      freshVisible,
      states[0]?.id,
    ).find((g) => g.columnKey === columnKey);
    if (!freshCol) {
      setBoardError("Couldn't place the card even after rebalancing.");
      return;
    }
    const retry = stackDropSortOrder(freshCol.issues, issueId, overIssueId);
    if (!retry.moved || retry.sortOrder === null) {
      setBoardError("Couldn't place the card even after rebalancing.");
      return;
    }
    write(retry.sortOrder);
  }

  /** C133T2: within-column reorder for release columns — sort_order
   *  only, never a re-release. Mirrors applyCycleReorder verbatim
   *  with the release column builder: the column's visible sequence is
   *  the stack, because a release column mixes states. Gap collapse
   *  follows the same rebalance → refetch → retry-once pipeline, then
   *  an honest error (never a loop). `overIssueId` null = dropped on
   *  the column itself (append at the end). */
  async function applyReleaseReorder(
    issueId: string,
    stateId: string,
    columnKey: string,
    overIssueId: string | null,
  ) {
    const col = groups.find((g) => g.columnKey === columnKey);
    if (!col) return;
    const write = (sortOrder: number) => writeDrop(issueId, stateId, sortOrder);
    if (overIssueId === null) {
      const without = col.issues.filter((i) => i.id !== issueId);
      if (without.length === 0) return;
      if (col.issues[col.issues.length - 1]?.id === issueId) return;
      const so = midpoint(without[without.length - 1].sort_order, null);
      if (so === null) return;
      write(so);
      return;
    }
    const drop = stackDropSortOrder(col.issues, issueId, overIssueId);
    if (!drop.moved) return;
    if (drop.sortOrder !== null) {
      write(drop.sortOrder);
      return;
    }
    // Gap collapsed: rebalance the issue's state column, refetch, and
    // retry once against the fresh column sequence.
    try {
      await rebalanceMutation.mutateAsync(stateId);
    } catch (e) {
      setBoardError(
        e instanceof ApiError ? e.message : "Failed to rebalance column",
      );
      return;
    }
    await queryClient.invalidateQueries({ queryKey: issuesKey });
    await queryClient.invalidateQueries({ queryKey: pqlIssuesKey });
    const refetched =
      pqlMode && appliedQuery.trim() !== ""
        ? await pqlIssuesQuery.refetch()
        : await boardQuery.refetch();
    const fresh = refetched.data ?? [];
    const freshVisible = splitNestedIssues(
      fresh,
      settings.showSubWorkItems,
    ).visibleIssues;
    const freshCol = buildReleaseBoardGroups(
      freshVisible,
      states[0]?.id,
    ).find((g) => g.columnKey === columnKey);
    if (!freshCol) {
      setBoardError("Couldn't place the card even after rebalancing.");
      return;
    }
    const retry = stackDropSortOrder(freshCol.issues, issueId, overIssueId);
    if (!retry.moved || retry.sortOrder === null) {
      setBoardError("Couldn't place the card even after rebalancing.");
      return;
    }
    write(retry.sortOrder);
  }

  /** C134T2: within-column reorder for creator columns — sort_order
   *  only, never a re-creator (created_by is immutable). Mirrors
   *  applyReleaseReorder verbatim with the creator column builder:
   *  the column's visible sequence is the stack, because a creator
   *  column mixes states. Gap collapse follows the same rebalance →
   *  refetch → retry-once pipeline, then an honest error (never a
   *  loop). `overIssueId` null = dropped on the column itself (append
   *  at the end). The retry rebuilds with the members lookup so the
   *  fresh column resolves labels the same way the groups memo does. */
  async function applyCreatedByReorder(
    issueId: string,
    stateId: string,
    columnKey: string,
    overIssueId: string | null,
  ) {
    const col = groups.find((g) => g.columnKey === columnKey);
    if (!col) return;
    const write = (sortOrder: number) => writeDrop(issueId, stateId, sortOrder);
    if (overIssueId === null) {
      const without = col.issues.filter((i) => i.id !== issueId);
      if (without.length === 0) return;
      if (col.issues[col.issues.length - 1]?.id === issueId) return;
      const so = midpoint(without[without.length - 1].sort_order, null);
      if (so === null) return;
      write(so);
      return;
    }
    const drop = stackDropSortOrder(col.issues, issueId, overIssueId);
    if (!drop.moved) return;
    if (drop.sortOrder !== null) {
      write(drop.sortOrder);
      return;
    }
    // Gap collapsed: rebalance the issue's state column, refetch, and
    // retry once against the fresh column sequence.
    try {
      await rebalanceMutation.mutateAsync(stateId);
    } catch (e) {
      setBoardError(
        e instanceof ApiError ? e.message : "Failed to rebalance column",
      );
      return;
    }
    await queryClient.invalidateQueries({ queryKey: issuesKey });
    await queryClient.invalidateQueries({ queryKey: pqlIssuesKey });
    const refetched =
      pqlMode && appliedQuery.trim() !== ""
        ? await pqlIssuesQuery.refetch()
        : await boardQuery.refetch();
    const fresh = refetched.data ?? [];
    const freshVisible = splitNestedIssues(
      fresh,
      settings.showSubWorkItems,
    ).visibleIssues;
    const freshCol = buildCreatedByBoardGroups(
      freshVisible,
      membersById,
      states[0]?.id,
    ).find((g) => g.columnKey === columnKey);
    if (!freshCol) {
      setBoardError("Couldn't place the card even after rebalancing.");
      return;
    }
    const retry = stackDropSortOrder(freshCol.issues, issueId, overIssueId);
    if (!retry.moved || retry.sortOrder === null) {
      setBoardError("Couldn't place the card even after rebalancing.");
      return;
    }
    write(retry.sortOrder);
  }

  /** C135T2: within-column reorder for state-group columns —
   *  sort_order only, never a re-group (a group holds several states,
   *  so a reorder never picks a target state). Mirrors
   *  applyCreatedByReorder verbatim with the state-group column
   *  builder: the column's visible sequence is the stack, because a
   *  state-group column mixes states. Gap collapse follows the same
   *  rebalance → refetch → retry-once pipeline, then an honest error
   *  (never a loop). `overIssueId` null = dropped on the column
   *  itself (append at the end). The retry rebuilds with the same
   *  state-group lookup and sequence-sorted states the groups memo
   *  uses, so the fresh column resolves the same way. */
  async function applyStateGroupReorder(
    issueId: string,
    stateId: string,
    columnKey: string,
    overIssueId: string | null,
  ) {
    const col = groups.find((g) => g.columnKey === columnKey);
    if (!col) return;
    const write = (sortOrder: number) => writeDrop(issueId, stateId, sortOrder);
    if (overIssueId === null) {
      const without = col.issues.filter((i) => i.id !== issueId);
      if (without.length === 0) return;
      if (col.issues[col.issues.length - 1]?.id === issueId) return;
      const so = midpoint(without[without.length - 1].sort_order, null);
      if (so === null) return;
      write(so);
      return;
    }
    const drop = stackDropSortOrder(col.issues, issueId, overIssueId);
    if (!drop.moved) return;
    if (drop.sortOrder !== null) {
      write(drop.sortOrder);
      return;
    }
    // Gap collapsed: rebalance the issue's state column, refetch, and
    // retry once against the fresh column sequence.
    try {
      await rebalanceMutation.mutateAsync(stateId);
    } catch (e) {
      setBoardError(
        e instanceof ApiError ? e.message : "Failed to rebalance column",
      );
      return;
    }
    await queryClient.invalidateQueries({ queryKey: issuesKey });
    await queryClient.invalidateQueries({ queryKey: pqlIssuesKey });
    const refetched =
      pqlMode && appliedQuery.trim() !== ""
        ? await pqlIssuesQuery.refetch()
        : await boardQuery.refetch();
    const fresh = refetched.data ?? [];
    const freshVisible = splitNestedIssues(
      fresh,
      settings.showSubWorkItems,
    ).visibleIssues;
    const freshCol = buildStateGroupBoardGroups(
      freshVisible,
      stateGroupById,
      states,
    ).find((g) => g.columnKey === columnKey);
    if (!freshCol) {
      setBoardError("Couldn't place the card even after rebalancing.");
      return;
    }
    const retry = stackDropSortOrder(freshCol.issues, issueId, overIssueId);
    if (!retry.moved || retry.sortOrder === null) {
      setBoardError("Couldn't place the card even after rebalancing.");
      return;
    }
    write(retry.sortOrder);
  }

  function onDragStart(e: DragStartEvent) {
    setBoardError(null);
    setActiveId(String(e.active.id));
  }

  function onDragEnd(e: DragEndEvent) {
    const { active, over } = e;
    setActiveId(null);
    if (!over || !canEdit) return;
    // C123T0: unwrap sub-group compound ids before resolving — the
    // drop still resolves by primary column exactly as today.
    const activeIssueId = subgroupDragIssueId(String(active.id));
    const overId = subgroupDragIssueId(String(over.id));

    const all = queryClient.getQueryData<Issue[]>(displayKey) ?? [];
    // C72T1: epicDragIssueId is identity for plain (state/priority mode)
    // ids; in epic mode it unwraps the compound epicCardDragId.
    // C129T1: same for assignee compound ids (each unwrapper is
    // identity for non-matching ids).
    const dragged = all.find(
      (i) =>
        i.id ===
        stateGroupDragIssueId(
          createdByDragIssueId(
            releaseDragIssueId(
              cycleDragIssueId(
                moduleDragIssueId(
                  labelDragIssueId(
                    assigneeDragIssueId(epicDragIssueId(activeIssueId)),
                  ),
                ),
              ),
            ),
          ),
        ),
    );
    if (!dragged) return;

    if (groupByEpic) {
      // C72T1: epic columns — dropping attaches the issue to the target
      // epic (idempotent, multi-epic aware; Ungrouped drops are a
      // deliberate no-op). Epic drops never change state, so the C68
      // transition guard does not apply. The over id may be the column
      // droppable or a card inside it (compound id).
      const columnKey = epicDropColumnKey(overId);
      if (columnKey === null) return;
      const plan = planEpicDrop(columnKey, dragged);
      if (plan.kind === "noop") return;
      const targetTitle =
        groups.find((g) => g.columnKey === columnKey)?.title ?? plan.epicId;
      attachToEpic(dragged.id, plan.epicId, targetTitle);
      return;
    }

    if (groupByAssignees) {
      // C129T1: assignee columns — cross-column drops ATTACH ONLY
      // (idempotent, multi-assignee aware; Ungrouped drops are a
      // deliberate no-op). Assignee drops never change state, so the
      // C68 transition guard does not apply. A drag whose source
      // column IS the target column is a within-column reorder
      // (sort_order only — a reorder never reassigns assignees),
      // resolved from the dragged card's compound id. The over id may
      // be the column droppable or a card inside it (compound id);
      // sub-group-section cards unwrap to plain issue ids and resolve
      // no column, so those drops stay visual-only, as epic drops do
      // under sub-grouping.
      const columnKey = assigneeDropColumnKey(overId);
      if (columnKey === null) return;
      const sourceKey = assigneeDragColumnKey(activeIssueId);
      if (sourceKey !== null && sourceKey === columnKey) {
        if (overId === `assignee:${columnKey}`) {
          void applyAssigneeReorder(
            dragged.id,
            dragged.state_id,
            columnKey,
            null,
          );
        } else {
          void applyAssigneeReorder(
            dragged.id,
            dragged.state_id,
            columnKey,
            assigneeDragIssueId(overId),
          );
        }
        return;
      }
      const plan = planAssigneeDrop(columnKey, dragged);
      if (plan.kind === "noop") return;
      const targetTitle =
        groups.find((g) => g.columnKey === columnKey)?.title ?? plan.memberId;
      attachToAssignee(dragged.id, plan.memberId, targetTitle);
      return;
    }

    if (groupByLabels) {
      // C130T1: label columns — cross-column drops ATTACH ONLY
      // (idempotent, multi-label aware; Ungrouped drops are a
      // deliberate no-op). Label drops never change state, so the
      // C68 transition guard does not apply. A drag whose source
      // column IS the target column is a within-column reorder
      // (sort_order only — a reorder never re-labels), resolved
      // from the dragged card's compound id. The over id may be the
      // column droppable or a card inside it (compound id);
      // sub-group-section cards unwrap to plain issue ids and resolve
      // no column, so those drops stay visual-only, as assignee drops
      // do under sub-grouping.
      const columnKey = labelDropColumnKey(overId);
      if (columnKey === null) return;
      const sourceKey = labelDragColumnKey(activeIssueId);
      if (sourceKey !== null && sourceKey === columnKey) {
        if (overId === `label:${columnKey}`) {
          void applyLabelReorder(
            dragged.id,
            dragged.state_id,
            columnKey,
            null,
          );
        } else {
          void applyLabelReorder(
            dragged.id,
            dragged.state_id,
            columnKey,
            labelDragIssueId(overId),
          );
        }
        return;
      }
      const plan = planLabelDrop(columnKey, dragged);
      if (plan.kind === "noop") return;
      const target =
        groups.find((g) => g.columnKey === columnKey);
      attachToLabel(
        dragged.id,
        plan.labelId,
        target?.title ?? plan.labelId,
        target?.labelColor,
      );
      return;
    }

    if (groupByModules) {
      // C131T1: module columns — cross-column drops ATTACH ONLY
      // (idempotent, multi-module aware; Ungrouped drops are a
      // deliberate no-op). Module drops never change state, so the
      // C68 transition guard does not apply. A drag whose source
      // column IS the target column is a within-column reorder
      // (sort_order only — a reorder never re-modules), resolved
      // from the dragged card's compound id. The over id may be the
      // column droppable or a card inside it (compound id);
      // sub-group-section cards unwrap to plain issue ids and resolve
      // no column, so those drops stay visual-only, as label drops
      // do under sub-grouping.
      const columnKey = moduleDropColumnKey(overId);
      if (columnKey === null) return;
      const sourceKey = moduleDragColumnKey(activeIssueId);
      if (sourceKey !== null && sourceKey === columnKey) {
        if (overId === `module:${columnKey}`) {
          void applyModuleReorder(
            dragged.id,
            dragged.state_id,
            columnKey,
            null,
          );
        } else {
          void applyModuleReorder(
            dragged.id,
            dragged.state_id,
            columnKey,
            moduleDragIssueId(overId),
          );
        }
        return;
      }
      const plan = planModuleDrop(columnKey, dragged);
      if (plan.kind === "noop") return;
      const target = groups.find((g) => g.columnKey === columnKey);
      attachToModule(
        dragged.id,
        plan.moduleId,
        target?.title ?? plan.moduleId,
      );
      return;
    }

    if (groupByCycles) {
      // C132T2: cycle columns — cross-column drops MOVE (add target
      // first, then remove source; the deliberate divergence from the
      // attach-only precedent — single-value display would misdisplay
      // an attach-only card under the wrong cycle). Ungrouped drops
      // are a deliberate no-op. Cycle drops never change state, so
      // the C68 transition guard does not apply (verified
      // state-grouping-only, see dropBlocked). A drag whose source
      // column IS the target column is a within-column reorder
      // (sort_order only — a reorder never re-cycles), resolved
      // from the dragged card's compound id BEFORE consulting the
      // drop plan (C131T1 trap: same-column drags reorder, never
      // no-op as already-member). The over id may be the column
      // droppable or a card inside it (compound id);
      // sub-group-section cards unwrap to plain issue ids and resolve
      // no column, so those drops stay visual-only, as module drops
      // do under sub-grouping.
      const columnKey = cycleDropColumnKey(overId);
      if (columnKey === null) return;
      const sourceKey = cycleDragColumnKey(activeIssueId);
      if (sourceKey !== null && sourceKey === columnKey) {
        if (overId === `cycle:${columnKey}`) {
          void applyCycleReorder(
            dragged.id,
            dragged.state_id,
            columnKey,
            null,
          );
        } else {
          void applyCycleReorder(
            dragged.id,
            dragged.state_id,
            columnKey,
            cycleDragIssueId(overId),
          );
        }
        return;
      }
      const plan = planCycleDrop(columnKey, dragged);
      if (plan.kind === "noop") return;
      const target = groups.find((g) => g.columnKey === columnKey);
      // Source is the dragged card's column (the contract's rule);
      // the sub-group-section case carries no compound source, so it
      // falls back to the plan's row-derived source. An Ungrouped
      // card column is not a real cycle, so it falls back the same
      // way (the plan names a null source there: add-only move).
      const sourceCycleId =
        sourceKey !== null && sourceKey !== CYCLE_UNGROUPED_COLUMN_KEY
          ? sourceKey
          : plan.sourceCycleId;
      void moveToCycle(
        dragged.id,
        plan.cycleId,
        target?.title ?? plan.cycleId,
        sourceCycleId,
      );
      return;
    }

    if (groupByReleases) {
      // C133T2: release columns — cross-column drops MOVE via a
      // SINGLE assign (assign overwrites release_id; no DELETE of
      // the source is issued, ever — the deliberate divergence from
      // the cycle add-then-remove pipeline). Ungrouped drops are a
      // deliberate no-op. Release drops never change state, so the
      // C68 transition guard does not apply (verified
      // state-grouping-only, see dropBlocked). A drag whose source
      // column IS the target column is a within-column reorder
      // (sort_order only — a reorder never re-releases), resolved
      // from the dragged card's compound id BEFORE consulting the
      // drop plan (C131T1 trap: same-column drags reorder, never
      // no-op as already-member). The over id may be the column
      // droppable or a card inside it (compound id);
      // sub-group-section cards unwrap to plain issue ids and resolve
      // no column, so those drops stay visual-only, as cycle drops
      // do under sub-grouping.
      const columnKey = releaseDropColumnKey(overId);
      if (columnKey === null) return;
      const sourceKey = releaseDragColumnKey(activeIssueId);
      if (sourceKey !== null && sourceKey === columnKey) {
        if (overId === `release:${columnKey}`) {
          void applyReleaseReorder(
            dragged.id,
            dragged.state_id,
            columnKey,
            null,
          );
        } else {
          void applyReleaseReorder(
            dragged.id,
            dragged.state_id,
            columnKey,
            releaseDragIssueId(overId),
          );
        }
        return;
      }
      const plan = planReleaseDrop(columnKey, dragged);
      if (plan.kind === "noop") return;
      const target = groups.find((g) => g.columnKey === columnKey);
      void moveToRelease(
        dragged.id,
        plan.releaseId,
        target?.title ?? plan.releaseId,
      );
      return;
    }

    if (groupByCreatedBy) {
      // C134T2: creator columns — cross-column drops are a DELIBERATE
      // NO-OP (created_by is immutable; no endpoint reassigns a
      // creator, so no state change, no membership call, and no sort
      // PATCH that implies a move is ever issued — the plan is always
      // noop and this branch returns without touching anything).
      // Creator drops never change state, so the C68 transition guard
      // does not apply (verified state-grouping-only, see
      // dropBlocked). A drag whose source column IS the target column
      // is a within-column reorder (sort_order only — a reorder never
      // re-creates), resolved from the dragged card's compound id
      // BEFORE consulting the drop plan (C131T1 trap: same-column
      // drags reorder, never no-op as already-member). The over id
      // may be the column droppable or a card inside it (compound
      // id); sub-group-section cards unwrap to plain issue ids and
      // resolve no column, so those drops stay visual-only, as
      // release drops do under sub-grouping.
      const columnKey = createdByDropColumnKey(overId);
      if (columnKey === null) return;
      const sourceKey = createdByDragColumnKey(activeIssueId);
      if (sourceKey !== null && sourceKey === columnKey) {
        if (overId === `createdby:${columnKey}`) {
          void applyCreatedByReorder(
            dragged.id,
            dragged.state_id,
            columnKey,
            null,
          );
        } else {
          void applyCreatedByReorder(
            dragged.id,
            dragged.state_id,
            columnKey,
            createdByDragIssueId(overId),
          );
        }
        return;
      }
      // The plan is always noop (see planCreatedByDrop): this branch
      // changes nothing — no state change, no membership call, no
      // sort PATCH — and returns without falling through to the
      // state/priority drop handling below.
      planCreatedByDrop(columnKey, dragged);
      return;
    }

    if (groupByStateGroups) {
      // C135T2: state-group columns — cross-column drops are a
      // DELIBERATE NO-OP (a group holds several states, so there is
      // no single target state to move to — picking one would
      // silently change the issue's workflow state; no state change,
      // no membership call, and no sort PATCH that implies a move is
      // ever issued — the plan is always noop and this branch
      // returns without touching anything). State-group drops never
      // change state, so the C68 transition guard does not apply
      // (verified state-grouping-only, see dropBlocked). A drag
      // whose source column IS the target column is a within-column
      // reorder (sort_order only — a reorder never re-groups),
      // resolved from the dragged card's compound id BEFORE
      // consulting the drop plan (C131T1 trap: same-column drags
      // reorder, never no-op as already-member). The over id may be
      // the column droppable or a card inside it (compound id);
      // sub-group-section cards unwrap to plain issue ids and
      // resolve no column, so those drops stay visual-only, as
      // creator drops do under sub-grouping.
      const columnKey = stateGroupDropColumnKey(overId);
      if (columnKey === null) return;
      const sourceKey = stateGroupDragColumnKey(activeIssueId);
      if (sourceKey !== null && sourceKey === columnKey) {
        if (overId === `stategroup:${columnKey}`) {
          void applyStateGroupReorder(
            dragged.id,
            dragged.state_id,
            columnKey,
            null,
          );
        } else {
          void applyStateGroupReorder(
            dragged.id,
            dragged.state_id,
            columnKey,
            stateGroupDragIssueId(overId),
          );
        }
        return;
      }
      // The plan is always noop (see planStateGroupDrop): this branch
      // changes nothing — no state change, no membership call, no
      // sort PATCH — and returns without falling through to the
      // state/priority drop handling below.
      planStateGroupDrop(columnKey, dragged, stateGroupById);
      return;
    }

    if (settings.groupBy === "priority") {
      // Priority columns: dropping changes the issue's priority.
      const m = /^priority:([0-4])$/.exec(overId);
      let destPriority: number | null = m ? Number(m[1]) : null;
      if (destPriority === null) {
        const overIssue = all.find((i) => i.id === overId);
        if (overIssue) destPriority = overIssue.priority;
      }
      if (destPriority === null || destPriority === dragged.priority) return;
      setBoardError(null);
      const p = destPriority;
      queryClient.setQueryData<Issue[]>(displayKey, (old) =>
        old?.map((i) => (i.id === activeIssueId ? { ...i, priority: p } : i)),
      );
      priorityMutation.mutate({ id: activeIssueId, priority: p });
      return;
    }

    let destStateId: string;
    let destIndex: number;
    if (stateIds.has(overId)) {
      // Dropped on the column itself (possibly empty): append at the end.
      destStateId = overId;
      destIndex = columnIssues(all, destStateId).length;
    } else {
      const overIssue = all.find((i) => i.id === overId);
      if (!overIssue) return;
      destStateId = overIssue.state_id;
      destIndex = columnIssues(all, destStateId).findIndex(
        (i) => i.id === overId,
      );
    }

    if (destStateId === dragged.state_id) {
      const col = columnIssues(all, destStateId);
      const oldIndex = col.findIndex((i) => i.id === activeIssueId);
      if (oldIndex === destIndex) return; // no-op drop
    }

    void applyDrop(activeIssueId, dragged.state_id, destStateId, destIndex);
  }

  const pointerSensor = useSensor(PointerSensor, {
    activationConstraint: { distance: 6 },
  });
  // KeyboardSensor enables keyboard dragging: focus a card, Space lifts
  // it, arrow keys move it between/within columns, Space drops, Esc
  // cancels (dnd-kit default announcements narrate the moves).
  const keyboardSensor = useSensor(KeyboardSensor, {
    coordinateGetter: sortableKeyboardCoordinates,
  });
  const allSensors = useSensors(pointerSensor, keyboardSensor);

  const loading = projectQuery.isPending || statesQuery.isPending || boardQuery.isPending;
  const loadError =
    projectQuery.error ?? statesQuery.error ?? boardQuery.error;

  return (
    <div className="flex w-full flex-col">
      {/* C19T4: shared page header replaces the per-page h1 + ProjectNav. */}
      <PageHeader
        viewName="Board"
        // C72T1: under multi-appearance the column memberships exceed the
        // issue count — the header shows UNIQUE issues, not memberships.
        // C129T1: same rule for assignee columns. C130T1: same rule
        // for label columns. C131T1: same rule for module columns.
        // C132T2: same rule for cycle columns (trivially true under
        // single-appearance, but asserted — never show membership
        // counts as issue counts). C133T2: same rule for release
        // columns (trivially true under single-appearance). C134T2:
        // same rule for creator columns (trivially true under
        // single-appearance). C135T2: same rule for state-group
        // columns (trivially true under single-appearance).
        count={
          groupByEpic ||
          groupByAssignees ||
          groupByLabels ||
          groupByModules ||
          groupByCycles ||
          groupByReleases ||
          groupByCreatedBy ||
          groupByStateGroups
            ? uniqueIssueCount(boardIssues)
            : groups.reduce((n, g) => n + g.issues.length, 0)
        }
        showViews
        filterPanel={
          <FilterPanel
            slug={slug}
            identifier={identifier}
            filters={filters}
            onChange={setFilters}
            onClear={clearFilters}
            triggerClassName={pageHeaderControlClass}
          />
        }
        displayPanel={
          <DisplayPanel
            settings={settings}
            onUpdate={update}
            onUpdateFields={updateFields}
            onReset={reset}
            view="board"
            triggerClassName={pageHeaderControlClass}
          />
        }
        extraControls={
          <SavedViewsMenu
            key={`${slug}:${identifier}`}
            projectId={projectQuery.data?.id ?? ""}
            filters={filters}
            display={settings}
            onApplyDisplay={update}
            triggerClassName={pageHeaderControlClass}
            pqlMode={pqlMode}
            pqlQuery={appliedQuery}
            onViewApplied={handleSavedViewApplied}
          />
        }
      />

      {/* C96T0: All issues / PQL toggle + editor above the columns
          (below the header), mirroring the C95 detail-page placement.
          Drag-drop + QuickAdd stay enabled in PQL mode; see the
          displayKey note above for the refetch semantics. */}
      <div className="px-[18px] pt-[14px]">
        <div
          role="group"
          aria-label="Board mode"
          className="flex w-[280px] rounded-md border p-0.5"
        >
          <button
            type="button"
            aria-pressed={!pqlMode}
            onClick={() => handlePqlModeChange("all")}
            className={`h-7 flex-1 rounded-sm text-xs font-medium ${
              !pqlMode ? "bg-primary text-primary-foreground" : ""
            }`}
          >
            All issues
          </button>
          <button
            type="button"
            aria-pressed={pqlMode}
            onClick={() => handlePqlModeChange("pql")}
            className={`h-7 flex-1 rounded-sm text-xs font-medium ${
              pqlMode ? "bg-primary text-primary-foreground" : ""
            }`}
          >
            PQL
          </button>
        </div>
        {pqlMode && (
          <PqlEditor
            key={`${slug}:${identifier}`}
            appliedQuery={appliedQuery}
            onApply={handlePqlApply}
            onClear={() => setAppliedQuery("")}
            showSaveHint={false}
          />
        )}
      </div>

      {(boardError || loadError) && (
        <div className="px-[18px] pt-[14px]">
          <Alert variant="destructive">
            <AlertDescription>
              {boardError ??
                (loadError instanceof ApiError
                  ? loadError.message
                  : "Failed to load board")}
            </AlertDescription>
          </Alert>
        </div>
      )}
      {!canEdit && !loading && (
        <div className="px-[18px] pt-[14px]">
          <Alert>
            <AlertDescription>
              You have guest access to this workspace — cards are read-only.
            </AlertDescription>
          </Alert>
        </div>
      )}

      <div className="min-h-0 flex-1 overflow-x-auto">
        {loading ? (
          <div className="flex gap-[28px] px-[18px] pb-[18px] pt-[14px]">
            {[0, 1, 2, 3, 4].map((i) => (
              <Skeleton
                key={i}
                className="h-[420px] w-[325px] shrink-0 rounded-[7px]"
              />
            ))}
          </div>
        ) : (
          <DndContext
            sensors={allSensors}
            collisionDetection={closestCorners}
            onDragStart={onDragStart}
            onDragEnd={onDragEnd}
          >
            <div className="flex min-h-full items-stretch gap-[28px] px-[18px] pb-[18px] pt-[14px]">
              {groups.map((g) => {
                const columnKey = g.columnKey;
                return (
                  <BoardColumn
                    key={g.droppableId}
                    droppableId={g.droppableId}
                    title={g.title}
                    state={g.state}
                    priority={g.priority}
                    epicId={g.epicId}
                    assigneeId={g.assigneeId}
                    labelId={g.labelId}
                    labelColor={g.labelColor}
                    moduleId={g.moduleId}
                    cycleId={g.cycleId}
                    releaseId={g.releaseId}
                    creatorId={g.creatorId}
                    stateGroup={g.stateGroup}
                    byGroup={g.byGroup}
                    cardState={
                      groupByEpic ||
                      groupByAssignees ||
                      groupByLabels ||
                      groupByModules ||
                      groupByCycles ||
                      groupByReleases ||
                      groupByCreatedBy ||
                      groupByStateGroups
                        ? (issue) => stateById.get(issue.state_id)
                        : undefined
                    }
                    getCardDragId={
                      groupByEpic && columnKey
                        ? (issue) => epicCardDragId(columnKey, issue.id)
                        : groupByAssignees && columnKey
                          ? (issue) => assigneeCardDragId(columnKey, issue.id)
                          : groupByLabels && columnKey
                            ? (issue) => labelCardDragId(columnKey, issue.id)
                            : groupByModules && columnKey
                              ? (issue) =>
                                  moduleCardDragId(columnKey, issue.id)
                              : groupByCycles && columnKey
                                ? (issue) =>
                                    cycleCardDragId(columnKey, issue.id)
                                : groupByReleases && columnKey
                                  ? (issue) =>
                                      releaseCardDragId(columnKey, issue.id)
                                  : groupByCreatedBy && columnKey
                                    ? (issue) =>
                                        createdByCardDragId(
                                          columnKey,
                                          issue.id,
                                        )
                                    : groupByStateGroups && columnKey
                                      ? (issue) =>
                                          stateGroupCardDragId(
                                            columnKey,
                                            issue.id,
                                          )
                                      : undefined
                    }
                    issues={g.issues}
                    disabled={!canEdit}
                    // C134T2: per-column quick-add DISABLED under
                    // created-by grouping (created_by is server-set;
                    // the form has no creator picker). Cards stay
                    // draggable — only the "+" tool and QuickAdd row
                    // are disabled, so within-column reorder still
                    // works. C135T2: per-column quick-add ENABLED
                    // under state-group grouping, EXCEPT columns with
                    // no states at all (no state to default to — the
                    // Unknown column, or a synthesized empty group
                    // whose group has no state): those render
                    // disabled via the same dedicated prop, so their
                    // cards stay draggable and within-column reorder
                    // still works.
                    disableQuickAdd={
                      groupByCreatedBy ||
                      (groupByStateGroups && !g.newStateId)
                    }
                    dropDisabled={dropBlocked.has(g.droppableId)}
                    onQuickCreate={(name) => quickCreate(g, name)}
                    onQuickExpand={(name) => quickExpand(g, name)}
                    fields={settings.fields}
                    onOpen={openPeek}
                    nestedByParent={nestedByParent}
                    stateById={stateById}
                    subSections={g.subSections}
                    subGroup={effectiveSubGroup}
                  />
                );
              })}
            </div>
            <DragOverlay>
              {activeIssue ? (
                <div className="w-[325px] rotate-2 rounded-[6px] border border-border bg-surface p-[10px] shadow-lg">
                  <BoardCardBody
                    issue={activeIssue}
                    state={stateById.get(activeIssue.state_id)}
                    fields={settings.fields}
                    menu={null}
                  />
                </div>
              ) : null}
            </DragOverlay>
          </DndContext>
        )}
      </div>

      {peekUuid && (
        <PeekDrawer
          slug={slug}
          identifier={identifier}
          uuid={peekUuid}
          onClose={closePeek}
        />
      )}

      {/* C16T3: open-blockers confirm — retry the drop with
          ignore_blockers=true. */}
      {blockerConfirm && (
        <BlockerConfirmDialog
          open={blockerConfirm !== null}
          blockers={blockerConfirm.blockers}
          pending={patchMutation.isPending}
          onCancel={() => setBlockerConfirm(null)}
          onConfirm={() => {
            const bc = blockerConfirm;
            setBlockerConfirm(null);
            writeDrop(bc.id, bc.stateId, bc.sortOrder, true);
          }}
        />
      )}
    </div>
  );
}
