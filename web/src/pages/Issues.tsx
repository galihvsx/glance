import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { Link, useLocation, useNavigate, useParams } from "react-router-dom";
import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { Plus, Search } from "lucide-react";
import { api, ApiError } from "../lib/api";
import type {
  Issue,
  IssueListResult,
  IssueState,
  Project,
} from "../lib/types";
import { Button } from "../components/ui/button";
import { Card, CardDescription, CardHeader, CardTitle } from "../components/ui/card";
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
import { Kbd } from "../components/ui/kbd";
import { Skeleton } from "../components/ui/skeleton";
import { Alert, AlertDescription } from "../components/ui/alert";
import { Textarea } from "../components/ui/textarea";
import IssueCard from "../components/issue/IssueCard";
import PeekDrawer from "../components/issue/PeekDrawer";
import { usePeekParam } from "../components/issue/usePeek";
import PriorityPicker from "../components/issue/PriorityPicker";
import ParentPicker from "../components/issue/ParentPicker";
import QuickAdd from "../components/issue/QuickAdd";
import ThemeToggle from "../components/ThemeToggle";
import StatePicker from "../components/issue/StatePicker";
import ProjectNav from "../components/project/ProjectNav";
import DisplayPanel from "../components/issue/DisplayPanel";
import { useDisplaySettings } from "../components/issue/useDisplaySettings";
import FilterPanel from "../components/issue/FilterPanel";
import {
  activeFilterCount,
  toApiParams,
  useIssueFilters,
  type IssueFilters,
} from "../lib/filters";
import { Checkbox } from "../components/ui/checkbox";
import { toast } from "../components/ui/toast";
import { priorityLabel } from "../lib/types";
import { useShortcutAction, isTypingTarget } from "../lib/shortcuts";

const PER_PAGE = 25;

/** Best-effort clipboard copy; silently ignored where unavailable. */
function copyToClipboard(text: string) {
  void (async () => {
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      // Clipboard API unavailable (e.g. insecure context) — ignore.
    }
  })();
}

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

function buildIssuePath(
  slug: string,
  identifier: string,
  f: IssueFilters,
  orderBy: string,
  cursor?: string,
): string {
  const p = toApiParams(f);
  p.set("per_page", String(PER_PAGE));
  p.set("order_by", orderBy);
  if (cursor) p.set("cursor", cursor);
  return `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}/issues?${p}`;
}

export default function Issues() {
  const { slug = "", identifier = "" } = useParams<{
    slug: string;
    identifier: string;
  }>();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const location = useLocation();
  const [filters, setFilters, clearFilters] = useIssueFilters();
  // Seed from the URL so a pasted link shows its query in the box.
  const [searchInput, setSearchInput] = useState(filters.q);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [selected, setSelected] = useState(0);
  const searchRef = useRef<HTMLInputElement>(null);
  const { settings, update, updateFields, reset } = useDisplaySettings(
    slug,
    identifier,
  );
  const { peekUuid, openPeek, closePeek } = usePeekParam();

  // Opened from the command palette ("Create issue" action) or from the
  // board's inline quick-add expand button (prefills title + state, and
  // priority when the board is grouped by priority).
  useEffect(() => {
    const st = location.state as {
      newIssue?: boolean;
      newIssueName?: string;
      newIssueStateId?: string;
      newIssuePriority?: number;
    } | null;
    if (st?.newIssue) {
      if (typeof st.newIssueName === "string") setName(st.newIssueName);
      if (typeof st.newIssueStateId === "string")
        setNewStateId(st.newIssueStateId);
      if (typeof st.newIssuePriority === "number")
        setNewPriority(st.newIssuePriority);
      setDialogOpen(true);
      navigate(location.pathname, { replace: true });
    }
    // Only on mount — the replace clears state so this can't loop.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useShortcutAction("new-issue", () => setDialogOpen(true));
  useShortcutAction("focus-search", () => searchRef.current?.focus());
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [newPriority, setNewPriority] = useState(0);
  const [newStateId, setNewStateId] = useState("");
  const [parentIssue, setParentIssue] = useState<Issue | null>(null);
  const [createMore, setCreateMore] = useState(false);
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

  const issuesQuery = useInfiniteQuery({
    queryKey: ["issues", slug, identifier, filters, settings.orderBy],
    queryFn: ({ pageParam }: { pageParam?: string }) =>
      api.get<IssueListResult>(
        buildIssuePath(slug, identifier, filters, settings.orderBy, pageParam),
      ),
    getNextPageParam: (last) => last.next_cursor || undefined,
    initialPageParam: undefined as string | undefined,
  });

  const stateById = new Map((statesQuery.data ?? []).map((s) => [s.id, s]));

  const createMutation = useMutation({
    mutationFn: (body: {
      name: string;
      description: unknown;
      priority: number;
      state_id?: string;
      parent_id?: string;
    }) => api.post<Issue>(`${base}/issues`, body),
    onSuccess: (issue) => {
      resetCreateForm();
      // "Create more" keeps the dialog open for rapid entry; otherwise
      // the dialog closes and the toast is the confirmation.
      if (!createMore) setDialogOpen(false);
      void queryClient.invalidateQueries({
        queryKey: ["issues", slug, identifier],
      });
      showCreatedToast(issue);
    },
    onError: (e) => {
      setFormError(
        e instanceof ApiError ? e.message : "Failed to create issue",
      );
    },
  });

  /** Clear the create form (after a successful create). */
  function resetCreateForm() {
    setName("");
    setDescription("");
    setNewPriority(0);
    setNewStateId("");
    setParentIssue(null);
    setFormError(null);
  }

  /** Success toast for a created issue: display_id + Copy link / View. */
  function showCreatedToast(issue: Issue) {
    const url = `${window.location.origin}/w/${slug}/p/${identifier}/i/${issue.id}`;
    toast.add({
      title: `${issue.display_id} created`,
      type: "success",
      description: (
        <span className="flex flex-col gap-2">
          <span className="truncate">{issue.name}</span>
          <span className="flex gap-2">
            <button
              type="button"
              className="rounded-md border px-2 py-1 text-xs font-medium hover:bg-accent"
              onClick={() => copyToClipboard(url)}
            >
              Copy link
            </button>
            <button
              type="button"
              className="rounded-md border px-2 py-1 text-xs font-medium hover:bg-accent"
              onClick={() => {
                setDialogOpen(false);
                openPeek(issue.id);
              }}
            >
              View
            </button>
          </span>
        </span>
      ),
    });
  }

  function onSearchSubmit(e: FormEvent) {
    e.preventDefault();
    setFilters({ q: searchInput });
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
      ...(parentIssue ? { parent_id: parentIssue.id } : {}),
    });
  }

  /** Inline quick-add: title only. The new row appears via query
   *  invalidation — no full reload. */
  async function quickCreate(
    stateId: string,
    name: string,
    priority?: number,
  ): Promise<void> {
    await api.post<Issue>(`${base}/issues`, {
      name,
      ...(stateId ? { state_id: stateId } : {}),
      ...(priority !== undefined ? { priority } : {}),
    });
    await queryClient.invalidateQueries({
      queryKey: ["issues", slug, identifier],
    });
  }

  /** Expand an inline quick-add into the full create form, keeping the
   *  typed title and the group's state/priority. */
  function expandQuickAdd(stateId: string, name: string, priority?: number) {
    setName(name);
    setNewStateId(stateId);
    if (priority !== undefined) setNewPriority(priority);
    setFormError(null);
    setDialogOpen(true);
  }

  const issues = issuesQuery.data?.pages.flatMap((p) => p.results) ?? [];
  const states = statesQuery.data ?? [];
  const sortedStates = useMemo(
    () => [...states].sort((a, b) => a.sequence - b.sequence),
    [states],
  );
  // Plane-style grouping, driven by the Display panel. State grouping is
  // skipped when filtering to a single state (or before states load);
  // priority grouping is skipped when filtering to a single priority.
  const groupByState =
    settings.groupBy === "state" && sortedStates.length > 0 && !filters.state;
  const groupByPriority =
    settings.groupBy === "priority" && filters.priorities.length !== 1;
  const groupingActive = groupByState || groupByPriority;

  interface IssueGroup {
    key: string;
    title: string;
    color?: string;
    issues: Issue[];
    stateId?: string;
    priority?: number;
  }

  const groups = useMemo<IssueGroup[]>(() => {
    let gs: IssueGroup[] = [];
    if (groupByState) {
      gs = sortedStates.map((s) => ({
        key: s.id,
        title: s.name,
        color: s.color,
        issues: issues.filter((i) => i.state_id === s.id),
        stateId: s.id,
      }));
    } else if (groupByPriority) {
      gs = [4, 3, 2, 1, 0].map((p) => ({
        key: `priority:${p}`,
        title: priorityLabel(p),
        issues: issues.filter((i) => i.priority === p),
        stateId: sortedStates[0]?.id,
        priority: p,
      }));
    }
    return settings.showEmptyGroups
      ? gs
      : gs.filter((g) => g.issues.length > 0);
  }, [
    groupByState,
    groupByPriority,
    sortedStates,
    issues,
    settings.showEmptyGroups,
  ]);
  const orderedIssues = groupingActive
    ? groups.flatMap((g) => g.issues)
    : issues;
  const indexById = useMemo(
    () => new Map(orderedIssues.map((it, i) => [it.id, i] as const)),
    [orderedIssues],
  );
  const selectedId = orderedIssues[selected]?.id;

  // j/k move the list selection; Enter opens the selected issue in the
  // peek drawer (keeping list context), not the full page.
  useEffect(() => {
    setSelected(0);
  }, [issuesQuery.data]);
  useShortcutAction("next-item", () =>
    setSelected((s) => Math.min(s + 1, Math.max(orderedIssues.length - 1, 0))),
  );
  useShortcutAction("prev-item", () =>
    setSelected((s) => Math.max(s - 1, 0)),
  );
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (
        e.key === "Enter" &&
        !e.metaKey &&
        !e.ctrlKey &&
        !e.altKey &&
        !isTypingTarget(e.target) &&
        selectedId
      ) {
        openPeek(selectedId);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedId, openPeek]);

  return (
    <div className="mx-auto w-full max-w-4xl p-6">
      <div className="mb-6 flex items-start justify-between gap-4">
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
          <p className="text-sm text-muted-foreground">
            Issues for this project.{" "}
            <span className="hidden sm:inline">
              Press <Kbd className="mx-0.5">/</Kbd> to search,{" "}
              <Kbd className="mx-0.5">j</Kbd>/<Kbd className="mx-0.5">k</Kbd> to
              move, <Kbd className="mx-0.5">c</Kbd> for new,{" "}
              <Kbd className="mx-0.5">⌘K</Kbd> for commands.
            </span>
          </p>
        </div>
        <div className="flex gap-2">
          <ThemeToggle />
          <Button
            variant="outline"
            onClick={() => navigate(`/w/${slug}/p/${identifier}/intake`)}
          >
            Intake inbox
          </Button>
          <Dialog
            open={dialogOpen}
            onOpenChange={(open) => {
              setDialogOpen(open);
              // A picked parent must not leak into the next create.
              if (!open) setParentIssue(null);
            }}
          >
          <Button onClick={() => setDialogOpen(true)} className="gap-2">
            <Plus className="h-4 w-4" />
            New issue
          </Button>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>New issue</DialogTitle>
              <DialogDescription>
                It gets the next sequence number, e.g. {identifier}-1.
              </DialogDescription>
            </DialogHeader>
            <form onSubmit={onCreate} className="space-y-4">
              <div className="space-y-2">
                <Label htmlFor="i-name">Title</Label>
                <Input
                  id="i-name"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="Fix the login redirect"
                  required
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="i-desc">Description</Label>
                <Textarea
                  id="i-desc"
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
                {states.length > 0 && (
                  <div className="space-y-2">
                    <Label>State</Label>
                    <StatePicker
                      states={states}
                      value={newStateId}
                      onChange={setNewStateId}
                    />
                  </div>
                )}
              </div>
              <div className="space-y-2">
                <Label>Parent</Label>
                <ParentPicker
                  slug={slug}
                  identifier={identifier}
                  value={parentIssue}
                  onChange={setParentIssue}
                  disabled={createMutation.isPending}
                />
              </div>
              {formError && (
                <Alert variant="destructive">
                  <AlertDescription>{formError}</AlertDescription>
                </Alert>
              )}
              <DialogFooter className="items-center sm:justify-between">
                <Label className="flex cursor-pointer items-center gap-2 text-sm font-normal text-muted-foreground">
                  <Checkbox
                    checked={createMore}
                    onCheckedChange={(v) => setCreateMore(v === true)}
                  />
                  Create more
                </Label>
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
        </div>
      </div>

      {/* Filter toolbar */}
      <div className="mt-4">
        <ProjectNav />
      </div>
      <div className="mb-4 mt-4 flex flex-wrap items-center gap-2">
        <form onSubmit={onSearchSubmit} className="relative">
          <Search className="absolute left-2.5 top-2.5 h-4 w-4 text-muted-foreground" />
          <Input
            ref={searchRef}
            value={searchInput}
            onChange={(e) => setSearchInput(e.target.value)}
            placeholder="Search issues…  ( / )"
            className="w-56 pl-8"
          />
        </form>
        <FilterPanel
          slug={slug}
          identifier={identifier}
          filters={filters}
          onChange={setFilters}
          onClear={() => {
            clearFilters();
            setSearchInput("");
          }}
        />
        <DisplayPanel
          settings={settings}
          onUpdate={update}
          onUpdateFields={updateFields}
          onReset={reset}
          view="list"
        />
        {hasActiveFilters && (
          <Button
            variant="ghost"
            size="sm"
            onClick={() => {
              clearFilters();
              setSearchInput("");
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

      {issuesQuery.isPending ? (
        <div className="space-y-3">
          <Skeleton className="h-24 w-full" />
          <Skeleton className="h-24 w-full" />
          <Skeleton className="h-24 w-full" />
        </div>
      ) : issues.length === 0 && !groupingActive ? (
        <Card>
          <CardHeader>
            <CardTitle>
              {hasActiveFilters ? "No issues match" : "No issues yet"}
            </CardTitle>
            <CardDescription>
              {hasActiveFilters
                ? "Try clearing the filters."
                : "Create the first issue in this project."}
            </CardDescription>
          </CardHeader>
        </Card>
      ) : (
        <>
          {groupingActive ? (
            <div className="space-y-6">
              {groups.map((g) => (
                <section key={g.key} aria-label={g.title}>
                  <div className="mb-2 flex items-center gap-2">
                    {g.color && (
                      <span
                        className="h-2.5 w-2.5 rounded-full"
                        style={{ backgroundColor: g.color }}
                        aria-hidden
                      />
                    )}
                    <h2 className="text-sm font-medium">{g.title}</h2>
                    <span className="text-xs text-muted-foreground">
                      {g.issues.length}
                    </span>
                  </div>
                  <div className="space-y-3">
                    {g.issues.map((issue) => (
                      <div
                        key={issue.id}
                        className={
                          indexById.get(issue.id) === selected
                            ? "rounded-lg ring-2 ring-ring"
                            : undefined
                        }
                      >
                        <IssueCard
                          issue={issue}
                          state={stateById.get(issue.state_id)}
                          fields={settings.fields}
                          onOpen={(it) => openPeek(it.id)}
                        />
                      </div>
                    ))}
                  </div>
                  <QuickAdd
                    className="mt-1"
                    onCreate={(name) =>
                      quickCreate(g.stateId ?? "", name, g.priority)
                    }
                    onExpand={(name) =>
                      expandQuickAdd(g.stateId ?? "", name, g.priority)
                    }
                  />
                </section>
              ))}
            </div>
          ) : (
            <div className="space-y-3">
              {issues.map((issue, i) => (
                <div
                  key={issue.id}
                  className={
                    i === selected ? "rounded-lg ring-2 ring-ring" : undefined
                  }
                >
                  <IssueCard
                    issue={issue}
                    state={stateById.get(issue.state_id)}
                    fields={settings.fields}
                    onOpen={(it) => openPeek(it.id)}
                  />
                </div>
              ))}
              {filters.state && (
                <QuickAdd
                  className="mt-1"
                  onCreate={(name) => quickCreate(filters.state, name)}
                  onExpand={(name) => expandQuickAdd(filters.state, name)}
                />
              )}
            </div>
          )}
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
