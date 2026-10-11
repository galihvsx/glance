import { Fragment, useMemo, useRef, useState, type FormEvent, type MouseEvent } from "react";
import { useNavigate, useParams } from "react-router-dom";
import {
  useInfiniteQuery,
  useMutation,
  useQueries,
  useQuery,
  useQueryClient,
  type InfiniteData,
} from "@tanstack/react-query";
import { ArrowDown, ArrowUp, ArrowUpDown, Search } from "lucide-react";
import { api, ApiError } from "../lib/api";
import type {
  Issue,
  IssueAssignee,
  IssueListResult,
  IssueProjectStructure,
  IssueState,
  Member,
  Project,
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
import {
  customFieldKeys,
  fetchCustomFields,
  formatCustomValue,
  resolveVisibleCustomFields,
  type CustomValue,
} from "../lib/customFields";
import {
  activeFilterCount,
  toApiParams,
  useIssueFilters,
  type IssueFilters,
} from "../lib/filters";
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
): string {
  const p = toApiParams(f);
  p.set("per_page", String(PER_PAGE));
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
    // C20T2: membership comes from the per-row
    // ?include_project_structure=1 fetch (structureByIssue), never from
    // the list rows.
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

export default function Spreadsheet() {
  const { slug = "", identifier = "" } = useParams<{
    slug: string;
    identifier: string;
  }>();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [filters, setFilters, clearFilters] = useIssueFilters();
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
  const statesQuery = useQuery({
    queryKey: ["states", slug, identifier],
    queryFn: () =>
      api.get<{ states: IssueState[] }>(`${base}/states`).then((d) => d.states),
  });

  // C9T4: custom-field columns. Definitions come from the project endpoint;
  // visibility is per-project localStorage, default OFF.
  const { visible: cfVisible, toggle: toggleCfColumn, reset: resetCfColumns } =
    useSpreadsheetCustomColumns(slug, identifier);
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
    queryKey: ["issues", slug, identifier, filters],
    queryFn: ({ pageParam }: { pageParam?: string }) =>
      api.get<IssueListResult>(buildIssuePath(slug, identifier, filters, pageParam)),
    getNextPageParam: (last) => last.next_cursor || undefined,
    initialPageParam: undefined as string | undefined,
  });

  const stateById = useMemo(
    () => new Map((statesQuery.data ?? []).map((s) => [s.id, s] as const)),
    [statesQuery.data],
  );

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
      void queryClient.invalidateQueries({
        queryKey: ["issues", slug, identifier],
      });
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

  type EditableColumn = "state" | "priority" | "start_date" | "target_date";
  const [editing, setEditing] = useState<{
    issueId: string;
    column: EditableColumn;
  } | null>(null);
  const [editError, setEditError] = useState<string | null>(null);

  const issuesKey = ["issues", slug, identifier, filters] as const;
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
      setEditError(e instanceof ApiError ? e.message : "Failed to update issue");
    },
    onSettled: () => {
      void queryClient.invalidateQueries({
        queryKey: ["issues", slug, identifier],
      });
    },
  });

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
        void queryClient.invalidateQueries({
          queryKey: ["issues", slug, identifier],
        });
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

  function isEditing(issueId: string, column: EditableColumn) {
    return editing?.issueId === issueId && editing?.column === column;
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
        : issues.map((issue) => ({
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
        issues.map(
          (issue, i) =>
            [
              issue.id,
              (cfValueQueries[i]?.data ?? {}) as Record<string, CustomValue>,
            ] as const,
        ),
      ),
    [issues, cfValueQueries],
  );

  // C20T2: per-row cycle/module/release membership. The list endpoint
  // deliberately never carries project_structure (C19T3 attached it to the
  // issue detail endpoint only, keeping the list query untouched for perf),
  // so each visible issue is fetched once via ?include_project_structure=1
  // — the same per-row pattern C9T4 uses for custom values, and zero new
  // backend surface. Cache key is distinct from the detail page's
  // ["issue", ...] key (different data shape) with a 60s stale time.
  const structureQueries = useQueries({
    queries: issues.map((issue) => ({
      queryKey: ["issue-project-structure", slug, identifier, issue.id],
      queryFn: () =>
        api
          .get<Issue>(
            `${base}/issues/${encodeURIComponent(issue.id)}?include_project_structure=1`,
          )
          .then((d) => d.project_structure ?? { modules: [] }),
      staleTime: 60_000,
    })),
  });
  const structureByIssue = useMemo(
    () =>
      new Map(
        issues.map(
          (issue, i) => [issue.id, structureQueries[i]?.data] as const,
        ),
      ),
    [issues, structureQueries],
  );
  const sortedIssues = useMemo(() => {
    if (!sortKey) return issues;
    const dir = sortDir === "asc" ? 1 : -1;
    return [...issues].sort((a, b) => {
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
  }, [issues, sortKey, sortDir, stateById, structureByIssue]);

  function openIssue(issue: Issue) {
    navigate(`/w/${slug}/p/${identifier}/i/${issue.id}`);
  }

  return (
    <div className="w-full">
      {/* C19T4: shared page header replaces the per-page h1 + ProjectNav. */}
      <PageHeader
        viewName="Table"
        count={issues.length}
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
              placeholder="Search issues…  ( / )"
              className="w-56 bg-surface pl-8 text-[13px]"
            />
          </form>
          {hasActiveFilters && (
            <Button
              variant="ghost"
              size="sm"
              className="text-ink-secondary hover:bg-wash-hover hover:text-ink"
              onClick={() => {
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
            <div className="overflow-x-auto rounded-lg border border-border bg-surface">
              <table className="w-full min-w-[1200px] border-collapse text-[13px] text-ink">
                <thead className="sticky top-0 z-20">
                  <tr className="border-b border-border-hairline">
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
                  {sortedIssues.map((issue) => {
                    const state = stateById.get(issue.state_id);
                    const structure = structureByIssue.get(issue.id);
                    const modules = structure?.modules ?? [];
                    return (
                      <tr
                        key={issue.id}
                        role="link"
                        tabIndex={0}
                        aria-label={`Open issue ${issue.display_id || issue.id.slice(0, 8)}: ${issue.name}`}
                        onClick={() => openIssue(issue)}
                        onKeyDown={(e) => {
                          if (e.key === "Enter" && !e.metaKey && !e.ctrlKey) {
                            openIssue(issue);
                          }
                        }}
                        className="h-[47px] cursor-pointer border-b border-border-hairline transition-colors last:border-0 hover:bg-wash-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-focus focus-visible:ring-inset"
                      >
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
                          onClick={openCellEditor(issue.id, "state")}
                        >
                          {isEditing(issue.id, "state") ? (
                            <CellStateEditor
                              states={statesQuery.data ?? []}
                              value={issue.state_id}
                              ariaLabel={`Edit state for ${issue.display_id || issue.id.slice(0, 8)}`}
                              onCommit={(stateId) =>
                                commitCellEdit(issue.id, { state_id: stateId })
                              }
                              onCancel={() => setEditing(null)}
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
                          onClick={openCellEditor(issue.id, "priority")}
                        >
                          {isEditing(issue.id, "priority") ? (
                            <CellPriorityEditor
                              value={issue.priority}
                              ariaLabel={`Edit priority for ${issue.display_id || issue.id.slice(0, 8)}`}
                              onCommit={(priority) =>
                                commitCellEdit(issue.id, { priority })
                              }
                              onCancel={() => setEditing(null)}
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
                              members={membersQuery.data ?? null}
                              assigned={issue.assignees}
                              onToggle={(userId, currently) =>
                                toggleAssignee(issue, userId, currently)
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
                          onClick={openCellEditor(issue.id, "start_date")}
                        >
                          {isEditing(issue.id, "start_date") ? (
                            <CellDateEditor
                              value={issue.start_date}
                              ariaLabel={`Edit start date for ${issue.display_id || issue.id.slice(0, 8)}`}
                              onCommit={(v) =>
                                commitCellEdit(issue.id, { start_date: v })
                              }
                              onCancel={() => setEditing(null)}
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
                          onClick={openCellEditor(issue.id, "target_date")}
                        >
                          {isEditing(issue.id, "target_date") ? (
                            <CellDateEditor
                              value={issue.target_date}
                              ariaLabel={`Edit target date for ${issue.display_id || issue.id.slice(0, 8)}`}
                              onCommit={(v) =>
                                commitCellEdit(issue.id, { target_date: v })
                              }
                              onCancel={() => setEditing(null)}
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
                        {/* C20T2: cycle/module/release membership columns.
                            Membership is wired via per-row
                            ?include_project_structure=1 (structureByIssue)
                            — no new backend surface. Read-only; inline
                            editing is C20T3. */}
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
                              cfValuesByIssue.get(issue.id)?.[f.id],
                            )}
                          </td>
                        ))}
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
            {issuesQuery.hasNextPage && (
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
