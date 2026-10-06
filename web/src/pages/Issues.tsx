import { useEffect, useRef, useState, type FormEvent } from "react";
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
  Label as ProjectLabel,
  Member,
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "../components/ui/select";
import { Textarea } from "../components/ui/textarea";
import IssueCard from "../components/issue/IssueCard";
import PriorityPicker from "../components/issue/PriorityPicker";
import StatePicker from "../components/issue/StatePicker";
import ProjectNav from "../components/project/ProjectNav";
import { useShortcutAction, isTypingTarget } from "../lib/shortcuts";

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

interface Filters {
  q: string;
  state: string;
  priority: string;
  assignee: string;
  label: string;
}

const EMPTY_FILTERS: Filters = {
  q: "",
  state: "",
  priority: "",
  assignee: "",
  label: "",
};

function buildIssuePath(
  slug: string,
  identifier: string,
  f: Filters,
  cursor?: string,
): string {
  const p = new URLSearchParams();
  p.set("per_page", String(PER_PAGE));
  if (f.q.trim()) p.set("q", f.q.trim());
  if (f.state) p.set("state", f.state);
  if (f.priority) p.set("priority", f.priority);
  if (f.assignee) p.set("assignee", f.assignee);
  if (f.label) p.set("label", f.label);
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
  const [filters, setFilters] = useState<Filters>(EMPTY_FILTERS);
  const [searchInput, setSearchInput] = useState("");
  const [dialogOpen, setDialogOpen] = useState(false);
  const [selected, setSelected] = useState(0);
  const searchRef = useRef<HTMLInputElement>(null);

  // Opened from the command palette ("Create issue" action).
  useEffect(() => {
    if ((location.state as { newIssue?: boolean } | null)?.newIssue) {
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
  const membersQuery = useQuery({
    queryKey: ["members", slug],
    queryFn: () =>
      api
        .get<{ members: Member[] }>(
          `/api/v1/workspaces/${encodeURIComponent(slug)}/members`,
        )
        .then((d) => d.members),
  });
  const labelsQuery = useQuery({
    queryKey: ["labels", slug, identifier],
    queryFn: () =>
      api
        .get<{ labels: ProjectLabel[] }>(`${base}/labels`)
        .then((d) => d.labels),
  });

  const issuesQuery = useInfiniteQuery({
    queryKey: ["issues", slug, identifier, filters],
    queryFn: ({ pageParam }: { pageParam?: string }) =>
      api.get<IssueListResult>(buildIssuePath(slug, identifier, filters, pageParam)),
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
    setFilters((f) => ({ ...f, q: searchInput }));
  }

  function setFilter<K extends keyof Filters>(k: K, v: Filters[K]) {
    setFilters((f) => ({ ...f, [k]: v }));
  }

  const hasActiveFilters = Object.values(filters).some((v) => v !== "");

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

  const issues = issuesQuery.data?.pages.flatMap((p) => p.results) ?? [];
  const states = statesQuery.data ?? [];
  const selectedId = issues[selected]?.id;

  // j/k move the list selection; Enter opens the selected issue.
  useEffect(() => {
    setSelected(0);
  }, [issuesQuery.data]);
  useShortcutAction("next-item", () =>
    setSelected((s) => Math.min(s + 1, Math.max(issues.length - 1, 0))),
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
        navigate(`/w/${slug}/p/${identifier}/i/${selectedId}`);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [selectedId, navigate, slug, identifier]);

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
          <Button
            variant="outline"
            onClick={() => navigate(`/w/${slug}/p/${identifier}/intake`)}
          >
            Intake inbox
          </Button>
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
        <Select
          value={filters.state || "all"}
          onValueChange={(v) => setFilter("state", v === "all" || v === null ? "" : v)}
        >
          <SelectTrigger className="w-36">
            <SelectValue placeholder="State" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All states</SelectItem>
            {states.map((s) => (
              <SelectItem key={s.id} value={s.id}>
                {s.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Select
          value={filters.priority || "all"}
          onValueChange={(v) => setFilter("priority", v === "all" || v === null ? "" : v)}
        >
          <SelectTrigger className="w-32">
            <SelectValue placeholder="Priority" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All priorities</SelectItem>
            {[0, 1, 2, 3, 4].map((p) => (
              <SelectItem key={p} value={String(p)}>
                {["None", "Low", "Medium", "High", "Urgent"][p]}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Select
          value={filters.assignee || "all"}
          onValueChange={(v) => setFilter("assignee", v === "all" || v === null ? "" : v)}
        >
          <SelectTrigger className="w-36">
            <SelectValue placeholder="Assignee" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">Anyone</SelectItem>
            {(membersQuery.data ?? []).map((m) => (
              <SelectItem key={m.id} value={m.id}>
                {m.name ?? m.email}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Select
          value={filters.label || "all"}
          onValueChange={(v) => setFilter("label", v === "all" || v === null ? "" : v)}
        >
          <SelectTrigger className="w-36">
            <SelectValue placeholder="Label" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All labels</SelectItem>
            {(labelsQuery.data ?? []).map((l) => (
              <SelectItem key={l.id} value={l.id}>
                {l.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        {hasActiveFilters && (
          <Button
            variant="ghost"
            size="sm"
            onClick={() => {
              setFilters(EMPTY_FILTERS);
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
      ) : issues.length === 0 ? (
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
                />
              </div>
            ))}
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
