import { useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
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
import { dropSortOrder } from "../lib/sortOrder";
import { Calendar } from "lucide-react";
import DisplayPanel from "../components/issue/DisplayPanel";
import SavedViewsMenu from "../components/issue/SavedViewsMenu";
import FilterPanel from "../components/issue/FilterPanel";
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
import ProjectNav from "../components/project/ProjectNav";
import QuickAdd from "../components/issue/QuickAdd";
import PeekDrawer from "../components/issue/PeekDrawer";
import { usePeekParam } from "../components/issue/usePeek";
import { Badge } from "../components/ui/badge";
import { Card, CardContent } from "../components/ui/card";
import { Skeleton } from "../components/ui/skeleton";
import { Alert, AlertDescription } from "../components/ui/alert";

/** Issues in one column, in board order (the API returns sort_order ASC). */
function columnIssues(all: Issue[], stateId: string): Issue[] {
  return all.filter((i) => i.state_id === stateId);
}

function SortableCard({
  issue,
  disabled,
  fields,
  onOpen,
}: {
  issue: Issue;
  disabled: boolean;
  fields: DisplayFields;
  onOpen: (id: string) => void;
}) {
  const {
    attributes,
    listeners,
    setNodeRef,
    transform,
    transition,
    isDragging,
  } = useSortable({ id: issue.id, disabled });

  // Card click / Enter opens the peek drawer (keeps board context),
  // not the full detail page.
  function openIssue() {
    onOpen(issue.id);
  }

  const dateRange = formatIssueDateRange(issue.start_date, issue.target_date);

  return (
    <Card
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
      className="cursor-grab touch-none active:cursor-grabbing focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
      onClick={openIssue}
    >
      <CardContent className="space-y-1.5 p-3">
        <div className="flex items-center gap-2">
          <Badge variant="outline" className="font-mono text-[11px]">
            {issue.display_id}
          </Badge>
          {fields.priority && (
            <span className="ml-auto text-[11px] text-muted-foreground">
              {priorityLabel(issue.priority)}
            </span>
          )}
        </div>
        <p className="text-sm font-medium leading-snug">{issue.name}</p>
        {fields.labels && issue.labels.length > 0 && (
          <div className="flex flex-wrap gap-1">
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
          <div className="flex items-center gap-1 text-[11px] text-muted-foreground">
            <Calendar className="h-3 w-3" aria-hidden />
            <span>{dateRange}</span>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function BoardColumn({
  droppableId,
  title,
  color,
  issues,
  disabled,
  onQuickCreate,
  onQuickExpand,
  fields,
  onOpen,
}: {
  droppableId: string;
  title: string;
  color?: string;
  issues: Issue[];
  disabled: boolean;
  onQuickCreate: (name: string) => Promise<void>;
  onQuickExpand: (name: string) => void;
  fields: DisplayFields;
  onOpen: (id: string) => void;
}) {
  const { setNodeRef, isOver } = useDroppable({ id: droppableId });
  return (
    <div
      ref={setNodeRef}
      className={`flex w-72 shrink-0 flex-col rounded-lg border bg-muted/40 p-2 transition-colors ${
        isOver ? "border-primary/60 bg-muted/70" : ""
      }`}
    >
      <div className="flex items-center gap-2 px-1 pb-2 pt-1">
        {color && (
          <span
            className="h-2.5 w-2.5 rounded-full"
            style={{ backgroundColor: color }}
            aria-hidden
          />
        )}
        <span className="text-sm font-medium">{title}</span>
        <Badge variant="secondary" className="ml-auto text-[11px]">
          {issues.length}
        </Badge>
      </div>
      <SortableContext
        items={issues.map((i) => i.id)}
        strategy={verticalListSortingStrategy}
      >
        <div className="flex min-h-24 flex-col gap-2">
          {issues.map((issue) => (
            <SortableCard
              key={issue.id}
              issue={issue}
              disabled={disabled}
              fields={fields}
              onOpen={onOpen}
            />
          ))}
          {issues.length === 0 && (
            <p className="rounded-md border border-dashed p-4 text-center text-xs text-muted-foreground">
              Drop issues here
            </p>
          )}
        </div>
      </SortableContext>
      <div className="mt-1">
        <QuickAdd
          onCreate={onQuickCreate}
          onExpand={onQuickExpand}
          disabled={disabled}
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
  const { settings, update, updateFields, reset } = useDisplaySettings(
    slug,
    identifier,
  );
  const { peekUuid, openPeek, closePeek } = usePeekParam();
  const [filters, setFilters, clearFilters] = useIssueFilters();

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

  const canEdit = (workspaceQuery.data?.role ?? 0) >= 15;

  const states = useMemo(
    () =>
      [...(statesQuery.data ?? [])].sort((a, b) => a.sequence - b.sequence),
    [statesQuery.data],
  );
  const stateIds = useMemo(() => new Set(states.map((s) => s.id)), [states]);
  // Stabilized so downstream memos (column grouping) don't re-evaluate
  // every render on a fresh [] identity while the query is pending.
  const issues = useMemo(() => boardQuery.data ?? [], [boardQuery.data]);
  const activeIssue = activeId
    ? (issues.find((i) => i.id === activeId) ?? null)
    : null;

  const patchMutation = useMutation({
    mutationFn: (body: { id: string; state_id: string; sort_order: number }) =>
      api.patch<Issue>(`${base}/issues/${body.id}`, {
        state_id: body.state_id,
        sort_order: body.sort_order,
      }),
    onError: (e) => {
      setBoardError(
        e instanceof ApiError ? e.message : "Failed to move issue",
      );
      void queryClient.invalidateQueries({ queryKey: issuesKey });
    },
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: issuesKey });
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
    },
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: issuesKey });
    },
  });

  /** One board column: either a state (default) or a priority bucket,
   *  depending on the Display panel's Group by setting. */
  interface BoardGroup {
    droppableId: string;
    title: string;
    color?: string;
    issues: Issue[];
    /** State/priority stamped onto quick-added issues in this column. */
    newStateId?: string;
    newPriority?: number;
  }

  const groups = useMemo<BoardGroup[]>(() => {
    let gs: BoardGroup[];
    if (settings.groupBy === "priority") {
      gs = [4, 3, 2, 1, 0].map((p) => ({
        droppableId: `priority:${p}`,
        title: priorityLabel(p),
        issues: issues.filter((i) => i.priority === p),
        newStateId: states[0]?.id,
        newPriority: p,
      }));
    } else {
      gs = states.map((s) => ({
        droppableId: s.id,
        title: s.name,
        color: s.color,
        issues: columnIssues(issues, s.id),
        newStateId: s.id,
      }));
    }
    return settings.showEmptyGroups
      ? gs
      : gs.filter((g) => g.issues.length > 0);
  }, [settings.groupBy, settings.showEmptyGroups, states, issues]);

  /** Inline quick-add: title only; the column stamps state (and priority
   *  in priority-grouped mode). The new card appears via query
   *  invalidation — no full reload. */
  async function quickCreate(group: BoardGroup, name: string): Promise<void> {
    await api.post<Issue>(`${base}/issues`, {
      name,
      ...(group.newStateId ? { state_id: group.newStateId } : {}),
      ...(group.newPriority !== undefined
        ? { priority: group.newPriority }
        : {}),
    });
    await queryClient.invalidateQueries({ queryKey: issuesKey });
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

  function writeDrop(activeId: string, destStateId: string, sortOrder: number) {
    setBoardError(null);
    queryClient.setQueryData<Issue[]>(issuesKey, (old) =>
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
    const fresh0 = queryClient.getQueryData<Issue[]>(issuesKey) ?? [];
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
    const refetched = await boardQuery.refetch();
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

    const all = queryClient.getQueryData<Issue[]>(issuesKey) ?? [];
    const dragged = all.find((i) => i.id === activeIssueId);
    if (!dragged) return;

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
      queryClient.setQueryData<Issue[]>(issuesKey, (old) =>
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
    <div className="mx-auto flex h-screen w-full max-w-7xl flex-col p-6">
      <div className="mb-4 flex items-start justify-between gap-4">
        <div>
        <Link
          to={`/w/${slug}`}
          className="text-xs text-muted-foreground hover:underline"
        >
          ← Projects
        </Link>
        <h1 className="mt-1 flex items-center gap-3 text-2xl font-semibold tracking-tight">
          {projectQuery.data ? (
            <>
              <Badge>{projectQuery.data.identifier}</Badge>
              {projectQuery.data.name}
            </>
          ) : (
            <Skeleton className="h-8 w-48" />
          )}
        </h1>
        </div>
        <div className="flex items-center gap-2">
          <FilterPanel
            slug={slug}
            identifier={identifier}
            filters={filters}
            onChange={setFilters}
            onClear={clearFilters}
          />
          <DisplayPanel
            settings={settings}
            onUpdate={update}
            onUpdateFields={updateFields}
            onReset={reset}
            view="board"
          />
        </div>
      </div>
      <ProjectNav
        trailing={
          <SavedViewsMenu
            key={`${slug}:${identifier}`}
            projectId={projectQuery.data?.id ?? ""}
            filters={filters}
            display={settings}
            onApplyDisplay={update}
          />
        }
      />

      {(boardError || loadError) && (
        <Alert variant="destructive" className="mt-4">
          <AlertDescription>
            {boardError ??
              (loadError instanceof ApiError
                ? loadError.message
                : "Failed to load board")}
          </AlertDescription>
        </Alert>
      )}
      {!canEdit && !loading && (
        <Alert className="mt-4">
          <AlertDescription>
            You have guest access to this workspace — cards are read-only.
          </AlertDescription>
        </Alert>
      )}

      <div className="mt-4 min-h-0 flex-1 overflow-x-auto pb-4">
        {loading ? (
          <div className="flex gap-3">
            {[0, 1, 2, 3].map((i) => (
              <Skeleton key={i} className="h-96 w-72 shrink-0" />
            ))}
          </div>
        ) : (
          <DndContext
            sensors={allSensors}
            collisionDetection={closestCorners}
            onDragStart={onDragStart}
            onDragEnd={onDragEnd}
          >
            <div className="flex h-full items-start gap-3">
              {groups.map((g) => (
                <BoardColumn
                  key={g.droppableId}
                  droppableId={g.droppableId}
                  title={g.title}
                  color={g.color}
                  issues={g.issues}
                  disabled={!canEdit}
                  onQuickCreate={(name) => quickCreate(g, name)}
                  onQuickExpand={(name) => quickExpand(g, name)}
                  fields={settings.fields}
                  onOpen={openPeek}
                />
              ))}
            </div>
            <DragOverlay>
              {activeIssue ? (
                <Card className="w-72 rotate-2 shadow-lg">
                  <CardContent className="space-y-1.5 p-3">
                    <Badge variant="outline" className="font-mono text-[11px]">
                      {activeIssue.display_id}
                    </Badge>
                    <p className="text-sm font-medium leading-snug">
                      {activeIssue.name}
                    </p>
                  </CardContent>
                </Card>
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
    </div>
  );
}
