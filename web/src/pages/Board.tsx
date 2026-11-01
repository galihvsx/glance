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
import { dropSortOrder } from "../lib/sortOrder";
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
  Plus,
  User,
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
  Project,
  Workspace,
} from "../lib/types";
import { priorityLabel } from "../lib/types";
import PageHeader, { pageHeaderControlClass } from "../components/shell/PageHeader";
import QuickAdd from "../components/issue/QuickAdd";
import PeekDrawer from "../components/issue/PeekDrawer";
import BlockerConfirmDialog from "../components/issue/BlockerConfirmDialog";
import { usePeekParam } from "../components/issue/usePeek";
import { Badge } from "../components/ui/badge";
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
 *  and dates render as extra rows only when enabled (and present). */
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
        <span className="font-mono text-[10px] text-ink-muted">
          {issue.display_id}
        </span>
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
          issue has direct subtasks. */}
      <SubtaskProgressBar progress={issue.subtask_progress} variant="compact" />
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
    </>
  );
}

function SortableCard({
  issue,
  state,
  disabled,
  fields,
  onOpen,
  dragId,
}: {
  issue: Issue;
  state: IssueState | undefined;
  disabled: boolean;
  fields: DisplayFields;
  onOpen: (id: string) => void;
  /** dnd-kit sortable id override — epic mode uses compound ids
   *  (epicCardDragId) so multi-appearance drops resolve the right column. */
  dragId?: string;
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

  return (
    <div
      ref={setNodeRef}
      style={{
        transform: CSS.Transform.toString(transform),
        transition,
        opacity: isDragging ? 0.35 : 1,
      }}
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
  );
}

function BoardColumn({
  droppableId,
  title,
  state,
  priority,
  epicId,
  byGroup,
  cardState,
  getCardDragId,
  issues,
  disabled,
  dropDisabled = false,
  onQuickCreate,
  onQuickExpand,
  fields,
  onOpen,
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
  onQuickCreate: (name: string) => Promise<void>;
  onQuickExpand: (name: string) => void;
  fields: DisplayFields;
  onOpen: (id: string) => void;
}) {
  const { setNodeRef, isOver } = useDroppable({
    id: droppableId,
    disabled: dropDisabled,
  });
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
            disabled={disabled}
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
              />
            ))}
          </div>
        </SortableContext>
      )}
      <div className={empty ? "mt-[8px]" : "mt-auto"}>
        <QuickAdd
          onCreate={onQuickCreate}
          onExpand={onQuickExpand}
          disabled={disabled}
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
  const issuesKey = ["board-issues", slug, identifier, filters] as const;

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
  const boardQuery = useQuery({
    queryKey: issuesKey,
    queryFn: async () => {
      const all: Issue[] = [];
      let cursor: string | undefined;
      do {
        const p = toApiParams(filters);
        p.set("order_by", "sort_order");
        p.set("per_page", "100");
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
  const pqlIssuesKey = ["board-issues-pql", slug, identifier, appliedQuery] as const;
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
  // The query feeding the displayed columns. Drag-drop, QuickAdd, and
  // settle-invalidations target this key, so in PQL mode a card whose
  // new state no longer matches the query leaves the board on the
  // settle refetch — the same behavior visual filters already have
  // today, documented here, deliberately NOT special-cased.
  const displayKey =
    pqlMode && appliedQuery.trim() !== "" ? pqlIssuesKey : issuesKey;
  // C72T1: in epic mode the active id is a compound epicCardDragId;
  // epicDragIssueId is identity for plain ids.
  const activeIssue = activeId
    ? (displayedIssues.find((i) => i.id === epicDragIssueId(activeId)) ??
      null)
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
    /** C72T1: column key for epic-mode dnd ids. */
    columnKey?: string;
    /** C72T1: per-state-group member counts for the epic header breakdown. */
    byGroup?: Record<string, number>;
    issues: Issue[];
    /** State/priority stamped onto quick-added issues in this column. */
    newStateId?: string;
    newPriority?: number;
  }

  const groups = useMemo<BoardGroup[]>(() => {
    let gs: BoardGroup[];
    if (groupByEpic) {
      // C72T1: group by LIVE epic membership (see boardEpic.ts for the
      // multi-appearance + archived-exclusion + Ungrouped-last semantics).
      // Progress counts come from the loaded states — no extra fetches
      // (the C70/C71 derived-data discipline).
      gs = buildEpicBoardGroups(
        displayedIssues,
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
    } else if (settings.groupBy === "priority") {
      gs = [4, 3, 2, 1, 0].map((p) => ({
        droppableId: `priority:${p}`,
        title: priorityLabel(p),
        priority: p,
        issues: displayedIssues.filter((i) => i.priority === p),
        newStateId: states[0]?.id,
        newPriority: p,
      }));
    } else {
      gs = states.map((s) => ({
        droppableId: s.id,
        title: s.name,
        state: s,
        issues: columnIssues(displayedIssues, s.id),
        newStateId: s.id,
      }));
    }
    return settings.showEmptyGroups
      ? gs
      : gs.filter((g) => g.issues.length > 0);
  }, [
    groupByEpic,
    settings.groupBy,
    settings.showEmptyGroups,
    states,
    stateById,
    displayedIssues,
  ]);

  // C68T1: per-column drop hint while a drag is active — the set of
  // droppable IDs the project's workflow rules forbid the dragged issue
  // from landing in (state columns only). C72T1: no transition hints for
  // epic columns — epic drops attach without changing state (priority
  // drops re-stamp priority, also exempt).
  // Client hint only; the server enforces on PATCH.
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
   *  create, no attach). The new card appears via query invalidation —
   *  no full reload. */
  async function quickCreate(group: BoardGroup, name: string): Promise<void> {
    const { stateId, attachEpicId } = planEpicQuickAdd(group);
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
    await queryClient.invalidateQueries({ queryKey: issuesKey });
    await queryClient.invalidateQueries({ queryKey: pqlIssuesKey });
  }

  /** Expand a column quick-add into the full create form on the list
   *  page, keeping the typed title and the column's state/priority. */
  function quickExpand(group: BoardGroup, name: string) {
    navigate(`/w/${slug}/p/${identifier}`, {
      state: {
        newIssue: true,
        newIssueName: name,
        newIssueStateId: group.newStateId,
        newIssuePriority: group.newPriority,
      },
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

  function onDragStart(e: DragStartEvent) {
    setBoardError(null);
    setActiveId(String(e.active.id));
  }

  function onDragEnd(e: DragEndEvent) {
    const { active, over } = e;
    setActiveId(null);
    if (!over || !canEdit) return;
    const activeIssueId = String(active.id);
    const overId = String(over.id);

    const all = queryClient.getQueryData<Issue[]>(displayKey) ?? [];
    // C72T1: epicDragIssueId is identity for plain (state/priority mode)
    // ids; in epic mode it unwraps the compound epicCardDragId.
    const dragged = all.find((i) => i.id === epicDragIssueId(activeIssueId));
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
        count={
          groupByEpic
            ? uniqueIssueCount(displayedIssues)
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
                    byGroup={g.byGroup}
                    cardState={
                      groupByEpic
                        ? (issue) => stateById.get(issue.state_id)
                        : undefined
                    }
                    getCardDragId={
                      groupByEpic && columnKey
                        ? (issue) => epicCardDragId(columnKey, issue.id)
                        : undefined
                    }
                    issues={g.issues}
                    disabled={!canEdit}
                    dropDisabled={dropBlocked.has(g.droppableId)}
                    onQuickCreate={(name) => quickCreate(g, name)}
                    onQuickExpand={(name) => quickExpand(g, name)}
                    fields={settings.fields}
                    onOpen={openPeek}
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
