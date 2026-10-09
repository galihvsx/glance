import { useMemo, useRef, useState, type FormEvent } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { ArrowDown, ArrowUp, ArrowUpDown, Plus, Search } from "lucide-react";
import { api, ApiError } from "../lib/api";
import type {
  Issue,
  IssueListResult,
  IssueState,
  Project,
} from "../lib/types";
import { priorityLabel } from "../lib/types";
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
import PriorityPicker from "../components/issue/PriorityPicker";
import StatePicker from "../components/issue/StatePicker";
import StateBadge from "../components/issue/StateBadge";
import ThemeToggle from "../components/ThemeToggle";
import ProjectNav from "../components/project/ProjectNav";
import FilterPanel from "../components/issue/FilterPanel";
import {
  activeFilterCount,
  toApiParams,
  useIssueFilters,
  type IssueFilters,
} from "../lib/filters";
import { useShortcutAction } from "../lib/shortcuts";
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
  | "updated_at";

const COLUMNS: { key: SortKey; label: string; className?: string }[] = [
  { key: "display_id", label: "ID", className: "w-24" },
  { key: "name", label: "Title", className: "min-w-64" },
  { key: "state", label: "State", className: "min-w-36" },
  { key: "priority", label: "Priority", className: "w-28" },
  { key: "assignees", label: "Assignees", className: "min-w-40" },
  { key: "labels", label: "Labels", className: "min-w-40" },
  { key: "start_date", label: "Start date", className: "w-32" },
  { key: "target_date", label: "Target date", className: "w-32" },
  { key: "created_at", label: "Created", className: "w-32" },
  { key: "updated_at", label: "Updated", className: "w-32" },
];

/** Raw comparable cell value; "" means "empty" and always sorts last. */
function cellValue(
  issue: Issue,
  key: SortKey,
  stateById: Map<string, IssueState>,
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
  const sortedIssues = useMemo(() => {
    if (!sortKey) return issues;
    const dir = sortDir === "asc" ? 1 : -1;
    return [...issues].sort((a, b) => {
      const va = cellValue(a, sortKey, stateById);
      const vb = cellValue(b, sortKey, stateById);
      const aEmpty = va === "";
      const bEmpty = vb === "";
      if (aEmpty && bEmpty) return 0;
      if (aEmpty) return 1;
      if (bEmpty) return -1;
      if (typeof va === "number" && typeof vb === "number")
        return (va - vb) * dir;
      return String(va).localeCompare(String(vb)) * dir;
    });
  }, [issues, sortKey, sortDir, stateById]);

  function openIssue(issue: Issue) {
    navigate(`/w/${slug}/p/${identifier}/i/${issue.id}`);
  }

  return (
    <div className="mx-auto w-full max-w-7xl p-6">
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
            Spreadsheet view of issues. Click a column header to sort.{" "}
            <span className="hidden sm:inline">
              Press <Kbd className="mx-0.5">/</Kbd> to search,{" "}
              <Kbd className="mx-0.5">c</Kbd> for new.
            </span>
          </p>
        </div>
        <div className="flex gap-2">
          <ThemeToggle />
          <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
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
        </div>
      </div>

      {/* View switcher */}
      <div className="mt-4">
        <ProjectNav />
      </div>

      {/* Filter toolbar — same query params as the list view */}
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
          onChange={(patch) => {
            setFilters(patch);
            setSortKey(null);
          }}
          onClear={() => {
            clearFilters();
            setSearchInput("");
            setSortKey(null);
          }}
        />
        {hasActiveFilters && (
          <Button
            variant="ghost"
            size="sm"
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

      {issuesQuery.isPending ? (
        <div className="space-y-3">
          <Skeleton className="h-10 w-full" />
          <Skeleton className="h-64 w-full" />
        </div>
      ) : sortedIssues.length === 0 ? (
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
          <div className="overflow-x-auto rounded-lg border">
            <table className="w-full min-w-[1200px] border-collapse text-sm">
              <thead className="sticky top-0 z-20">
                <tr className="border-b bg-muted/80 backdrop-blur">
                  {COLUMNS.map((col, ci) => {
                    const active = sortKey === col.key;
                    return (
                      <th
                        key={col.key}
                        className={cn(
                          "px-3 py-2.5 text-left align-middle",
                          col.className,
                          ci === 0 &&
                            "sticky left-0 z-30 border-r bg-muted shadow-[1px_0_0_0_var(--border)]",
                        )}
                      >
                        <button
                          type="button"
                          onClick={() => toggleSort(col.key)}
                          className={cn(
                            "inline-flex items-center gap-1.5 font-medium hover:text-foreground",
                            active
                              ? "text-foreground"
                              : "text-muted-foreground",
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
                              <ArrowUp className="h-3.5 w-3.5" aria-hidden />
                            ) : (
                              <ArrowDown className="h-3.5 w-3.5" aria-hidden />
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
                </tr>
              </thead>
              <tbody>
                {sortedIssues.map((issue) => {
                  const state = stateById.get(issue.state_id);
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
                      className="cursor-pointer border-b transition-colors last:border-0 hover:bg-muted/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset"
                    >
                      <td className="sticky left-0 z-10 border-r bg-background px-3 py-2.5 align-middle">
                        <Badge variant="outline" className="font-mono">
                          {issue.display_id || issue.id.slice(0, 8)}
                        </Badge>
                      </td>
                      <td className="max-w-64 truncate px-3 py-2.5 align-middle font-medium">
                        {issue.name}
                      </td>
                      <td className="px-3 py-2.5 align-middle">
                        {state ? (
                          <StateBadge state={state} />
                        ) : (
                          <span className="text-muted-foreground">—</span>
                        )}
                      </td>
                      <td className="px-3 py-2.5 align-middle">
                        {priorityLabel(issue.priority)}
                      </td>
                      <td className="px-3 py-2.5 align-middle">
                        {issue.assignees.length > 0 ? (
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
                          <span className="text-muted-foreground">—</span>
                        )}
                      </td>
                      <td className="px-3 py-2.5 align-middle">
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
                          <span className="text-muted-foreground">—</span>
                        )}
                      </td>
                      <td className="whitespace-nowrap px-3 py-2.5 align-middle text-muted-foreground">
                        {dateOnly(issue.start_date)}
                      </td>
                      <td className="whitespace-nowrap px-3 py-2.5 align-middle text-muted-foreground">
                        {dateOnly(issue.target_date)}
                      </td>
                      <td className="whitespace-nowrap px-3 py-2.5 align-middle text-muted-foreground">
                        {dateOnly(issue.created_at)}
                      </td>
                      <td className="whitespace-nowrap px-3 py-2.5 align-middle text-muted-foreground">
                        {dateOnly(issue.updated_at)}
                      </td>
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
  );
}
