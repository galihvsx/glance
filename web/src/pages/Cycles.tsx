import { useMemo, useState, type FormEvent } from "react";
import { Link, useParams } from "react-router-dom";
import {
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { Plus } from "lucide-react";
import { api, ApiError } from "../lib/api";
import type {
  Cycle,
  Issue,
  IssueListResult,
  IssueState,
  Project,
} from "../lib/types";
import ProjectNav from "../components/project/ProjectNav";
import { Badge } from "../components/ui/badge";
import { ActionCard } from "../components/ui/action-card";
import { Button } from "../components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "../components/ui/card";
import { Checkbox } from "../components/ui/checkbox";
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
import { Skeleton } from "../components/ui/skeleton";
import { Alert, AlertDescription } from "../components/ui/alert";

/** The six snapshot groups, in display order (backend snapshotGroups). */
const SNAPSHOT_GROUPS = [
  "triage",
  "backlog",
  "unstarted",
  "started",
  "completed",
  "cancelled",
] as const;

function statusVariant(status: string): "default" | "secondary" | "outline" {
  switch (status) {
    case "current":
      return "default";
    case "upcoming":
      return "secondary";
    default:
      return "outline";
  }
}

function dateOnly(iso: string): string {
  return iso.slice(0, 10);
}

/** Stacked progress bar from a progress_snapshot. Group colors come from
 *  the project's states (first state in the group), so no color is
 *  hardcoded. */
function ProgressBar({
  snapshot,
  states,
}: {
  snapshot: Record<string, number>;
  states: IssueState[];
}) {
  const total = SNAPSHOT_GROUPS.reduce((n, g) => n + (snapshot[g] ?? 0), 0);
  if (total === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        No issues in this cycle yet.
      </p>
    );
  }
  return (
    <div>
      <div className="flex h-3 w-full overflow-hidden rounded-full bg-muted">
        {SNAPSHOT_GROUPS.map((g) => {
          const n = snapshot[g] ?? 0;
          if (n === 0) return null;
          const color =
            states.find((s) => s.group === g)?.color ?? "var(--muted)";
          return (
            <div
              key={g}
              className="h-full"
              style={{
                width: `${(n / total) * 100}%`,
                backgroundColor: color,
              }}
              title={`${g}: ${n}`}
            />
          );
        })}
      </div>
      <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1">
        {SNAPSHOT_GROUPS.map((g) => {
          const n = snapshot[g] ?? 0;
          const color =
            states.find((s) => s.group === g)?.color ?? "var(--muted)";
          return (
            <span
              key={g}
              className="flex items-center gap-1.5 text-xs text-muted-foreground"
            >
              <span
                className="h-2 w-2 rounded-full"
                style={{ backgroundColor: color }}
                aria-hidden
              />
              {g} · {n}
            </span>
          );
        })}
      </div>
    </div>
  );
}

export default function Cycles() {
  const { slug = "", identifier = "" } = useParams<{
    slug: string;
    identifier: string;
  }>();
  const queryClient = useQueryClient();
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [newOpen, setNewOpen] = useState(false);
  const [addOpen, setAddOpen] = useState(false);
  const [name, setName] = useState("");
  const [startDate, setStartDate] = useState("");
  const [endDate, setEndDate] = useState("");
  const [formError, setFormError] = useState<string | null>(null);
  const [picked, setPicked] = useState<Set<string>>(new Set());

  const base = `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`;
  const cyclesKey = ["cycles", slug, identifier] as const;

  const projectQuery = useQuery({
    queryKey: ["project", slug, identifier],
    queryFn: () => api.get<Project>(base),
  });
  const statesQuery = useQuery({
    queryKey: ["states", slug, identifier],
    queryFn: () =>
      api.get<{ states: IssueState[] }>(`${base}/states`).then((d) => d.states),
  });
  const cyclesQuery = useQuery({
    queryKey: cyclesKey,
    queryFn: () =>
      api.get<{ cycles: Cycle[] }>(`${base}/cycles`).then((d) => d.cycles),
  });

  const selected = useMemo(
    () => (cyclesQuery.data ?? []).find((c) => c.id === selectedId) ?? null,
    [cyclesQuery.data, selectedId],
  );

  const cycleIssuesQuery = useQuery({
    queryKey: ["cycle-issues", slug, identifier, selectedId],
    enabled: selectedId !== null,
    queryFn: async () => {
      const all: Issue[] = [];
      let cursor: string | undefined;
      do {
        const p = new URLSearchParams({
          cycle: selectedId ?? "",
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

  const allIssuesQuery = useQuery({
    queryKey: ["board-issues", slug, identifier],
    enabled: addOpen,
    queryFn: async () => {
      const all: Issue[] = [];
      let cursor: string | undefined;
      do {
        const p = new URLSearchParams({
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

  const createMutation = useMutation({
    mutationFn: (body: { name: string; start_date: string; end_date: string }) =>
      api.post<Cycle>(`${base}/cycles`, body),
    onSuccess: (cycle) => {
      setNewOpen(false);
      setName("");
      setStartDate("");
      setEndDate("");
      setFormError(null);
      setSelectedId(cycle.id);
      void queryClient.invalidateQueries({ queryKey: cyclesKey });
    },
    onError: (e) => {
      setFormError(e instanceof ApiError ? e.message : "Failed to create cycle");
    },
  });

  const addIssuesMutation = useMutation({
    mutationFn: (ids: string[]) =>
      api.post(`${base}/cycles/${selectedId}/issues`, { issue_ids: ids }),
    onSuccess: () => {
      setAddOpen(false);
      setPicked(new Set());
      void queryClient.invalidateQueries({
        queryKey: ["cycle-issues", slug, identifier, selectedId],
      });
      void queryClient.invalidateQueries({ queryKey: cyclesKey });
    },
    onError: (e) => {
      setFormError(e instanceof ApiError ? e.message : "Failed to add issues");
    },
  });

  function onCreate(e: FormEvent) {
    e.preventDefault();
    setFormError(null);
    createMutation.mutate({
      name: name.trim(),
      start_date: startDate,
      end_date: endDate,
    });
  }

  function togglePicked(id: string) {
    setPicked((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  const inCycle = useMemo(
    () => new Set((cycleIssuesQuery.data ?? []).map((i) => i.id)),
    [cycleIssuesQuery.data],
  );
  const states = statesQuery.data ?? [];
  const cycles = cyclesQuery.data ?? [];

  return (
    <div className="mx-auto w-full max-w-4xl p-6">
      <div className="mb-4">
        <Link
          to={`/w/${slug}`}
          className="text-xs text-muted-foreground hover:underline"
        >
          ← Projects
        </Link>
        <div className="mt-1 flex items-start justify-between gap-4">
          <h1 className="flex items-center gap-3 text-2xl font-semibold tracking-tight">
            {projectQuery.data ? (
              <>
                <Badge>{projectQuery.data.identifier}</Badge>
                {projectQuery.data.name}
              </>
            ) : (
              <Skeleton className="h-8 w-48" />
            )}
          </h1>
          <Dialog open={newOpen} onOpenChange={setNewOpen}>
            <Button onClick={() => setNewOpen(true)} className="gap-2">
              <Plus className="h-4 w-4" />
              New cycle
            </Button>
            <DialogContent>
              <DialogHeader>
                <DialogTitle>New cycle</DialogTitle>
                <DialogDescription>
                  A time-boxed iteration. Issues added to it roll over to
                  the next cycle when it completes.
                </DialogDescription>
              </DialogHeader>
              <form onSubmit={onCreate} className="space-y-4">
                <div className="space-y-2">
                  <Label htmlFor="c-name">Name</Label>
                  <Input
                    id="c-name"
                    value={name}
                    onChange={(e) => setName(e.target.value)}
                    placeholder="Sprint 12"
                    required
                  />
                </div>
                <div className="flex gap-4">
                  <div className="space-y-2">
                    <Label htmlFor="c-start">Start date</Label>
                    <Input
                      id="c-start"
                      type="date"
                      value={startDate}
                      onChange={(e) => setStartDate(e.target.value)}
                      required
                    />
                  </div>
                  <div className="space-y-2">
                    <Label htmlFor="c-end">End date</Label>
                    <Input
                      id="c-end"
                      type="date"
                      value={endDate}
                      onChange={(e) => setEndDate(e.target.value)}
                      required
                    />
                  </div>
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
                    {createMutation.isPending ? "Creating…" : "Create cycle"}
                  </Button>
                </DialogFooter>
              </form>
            </DialogContent>
          </Dialog>
        </div>
      </div>
      <ProjectNav />

      {cyclesQuery.isError && (
        <Alert variant="destructive" className="mt-4">
          <AlertDescription>
            {cyclesQuery.error instanceof ApiError
              ? cyclesQuery.error.message
              : "Failed to load cycles"}
          </AlertDescription>
        </Alert>
      )}

      <div className="mt-4">
        {cyclesQuery.isPending ? (
          <div className="space-y-3">
            <Skeleton className="h-28 w-full" />
            <Skeleton className="h-28 w-full" />
          </div>
        ) : selected ? (
          <CycleDetail
            cycle={selected}
            states={states}
            issues={cycleIssuesQuery.data ?? []}
            issuesLoading={cycleIssuesQuery.isPending}
            onBack={() => setSelectedId(null)}
            onAdd={() => {
              setPicked(new Set());
              setFormError(null);
              setAddOpen(true);
            }}
            slug={slug}
            identifier={identifier}
          />
        ) : cycles.length === 0 ? (
          <Card>
            <CardHeader>
              <CardTitle>No cycles yet</CardTitle>
            </CardHeader>
            <CardContent>
              <p className="text-sm text-muted-foreground">
                Create the first cycle to start time-boxing work.
              </p>
            </CardContent>
          </Card>
        ) : (
          <div className="space-y-3">
            {cycles.map((c) => (
              <ActionCard
                key={c.id}
                role="button"
                label={`Select cycle ${c.name}`}
                aria-pressed={selectedId === c.id}
                onActivate={() => setSelectedId(c.id)}
              >
                <CardHeader className="space-y-3">
                  <div className="flex items-center gap-2">
                    <CardTitle className="text-base font-medium">
                      {c.name}
                    </CardTitle>
                    <Badge
                      variant={statusVariant(c.status)}
                      className="ml-auto"
                    >
                      {c.status}
                    </Badge>
                  </div>
                  <p className="text-xs text-muted-foreground">
                    {dateOnly(c.start_date)} → {dateOnly(c.end_date)}
                  </p>
                  <ProgressBar
                    snapshot={c.progress_snapshot ?? {}}
                    states={states}
                  />
                </CardHeader>
              </ActionCard>
            ))}
          </div>
        )}
      </div>

      {/* Add-issues dialog */}
      <Dialog open={addOpen} onOpenChange={setAddOpen}>
        <DialogContent className="max-h-[80vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>Add issues to {selected?.name}</DialogTitle>
            <DialogDescription>
              Select issues to include in this cycle. Issues already in the
              cycle are checked and can't be picked again.
            </DialogDescription>
          </DialogHeader>
          {formError && (
            <Alert variant="destructive">
              <AlertDescription>{formError}</AlertDescription>
            </Alert>
          )}
          {allIssuesQuery.isPending ? (
            <div className="space-y-2">
              <Skeleton className="h-10 w-full" />
              <Skeleton className="h-10 w-full" />
              <Skeleton className="h-10 w-full" />
            </div>
          ) : (
            <div className="space-y-1">
              {(allIssuesQuery.data ?? []).map((i) => {
                const already = inCycle.has(i.id);
                return (
                  <label
                    key={i.id}
                    className={`flex cursor-pointer items-center gap-3 rounded-md px-2 py-2 text-sm hover:bg-accent ${
                      already ? "opacity-50" : ""
                    }`}
                  >
                    <Checkbox
                      checked={already || picked.has(i.id)}
                      disabled={already}
                      onCheckedChange={() => togglePicked(i.id)}
                    />
                    <Badge variant="outline" className="font-mono text-[11px]">
                      {i.display_id}
                    </Badge>
                    <span className="truncate">{i.name}</span>
                  </label>
                );
              })}
              {(allIssuesQuery.data ?? []).length === 0 && (
                <p className="text-sm text-muted-foreground">
                  No issues in this project yet.
                </p>
              )}
            </div>
          )}
          <DialogFooter>
            <Button
              disabled={addIssuesMutation.isPending || picked.size === 0}
              onClick={() => addIssuesMutation.mutate([...picked])}
            >
              {addIssuesMutation.isPending
                ? "Adding…"
                : `Add ${picked.size} issue${picked.size === 1 ? "" : "s"}`}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

function CycleDetail({
  cycle,
  states,
  issues,
  issuesLoading,
  onBack,
  onAdd,
  slug,
  identifier,
}: {
  cycle: Cycle;
  states: IssueState[];
  issues: Issue[];
  issuesLoading: boolean;
  onBack: () => void;
  onAdd: () => void;
  slug: string;
  identifier: string;
}) {
  return (
    <div>
      <button
        onClick={onBack}
        className="text-xs text-muted-foreground hover:underline"
      >
        ← All cycles
      </button>
      <div className="mt-2 flex items-center gap-3">
        <h2 className="text-xl font-semibold tracking-tight">{cycle.name}</h2>
        <Badge variant={statusVariant(cycle.status)}>{cycle.status}</Badge>
        <span className="text-sm text-muted-foreground">
          {dateOnly(cycle.start_date)} → {dateOnly(cycle.end_date)}
        </span>
        <Button variant="outline" size="sm" className="ml-auto" onClick={onAdd}>
          <Plus className="mr-1 h-4 w-4" />
          Add issues
        </Button>
      </div>
      <div className="mt-4">
        <ProgressBar snapshot={cycle.progress_snapshot ?? {}} states={states} />
      </div>
      <h3 className="mb-2 mt-6 text-sm font-medium text-muted-foreground">
        Issues in this cycle ({issues.length})
      </h3>
      {issuesLoading ? (
        <div className="space-y-2">
          <Skeleton className="h-12 w-full" />
          <Skeleton className="h-12 w-full" />
        </div>
      ) : issues.length === 0 ? (
        <Card>
          <CardContent className="p-4 text-sm text-muted-foreground">
            No issues assigned to this cycle yet.
          </CardContent>
        </Card>
      ) : (
        <div className="space-y-2">
          {issues.map((i) => (
            <Link
              key={i.id}
              to={`/w/${slug}/p/${identifier}/i/${i.id}`}
              className="flex items-center gap-3 rounded-md border p-3 text-sm transition-colors hover:bg-accent"
            >
              <Badge variant="outline" className="font-mono text-[11px]">
                {i.display_id}
              </Badge>
              <span className="truncate font-medium">{i.name}</span>
            </Link>
          ))}
        </div>
      )}
    </div>
  );
}
