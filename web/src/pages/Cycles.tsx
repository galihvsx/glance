import { useMemo, useState, type FormEvent } from "react";
import { useParams } from "react-router-dom";
import {
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { Plus } from "lucide-react";
import { api, ApiError } from "../lib/api";
import type {
  Cycle,
  CycleBurndown,
  Issue,
  IssueListResult,
  IssueState,
} from "../lib/types";
import PageHeader from "../components/shell/PageHeader";
import BurndownChart from "../components/cycle/BurndownChart";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
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
import {
  OrgCard,
  OrgDetailHeader,
  OrgEmpty,
  OrgHeaderButton,
  OrgIssueRow,
  OrgProgressBar,
  OrgSectionHead,
  OrgStatBand,
  OrgStatusChip,
} from "../components/org/OrgViews";

/** Status chip dot colors: state/accent colors, never hardcoded grays. */
function statusColor(status: string): string {
  switch (status) {
    case "current":
      return "#4f8ef7"; // accent blue
    case "completed":
      return "#42c88a"; // accent green
    default:
      return "#777780"; // muted (upcoming / archived)
  }
}

function dateOnly(iso: string): string {
  return iso.slice(0, 10);
}

function CycleCard({
  cycle,
  states,
  onSelect,
}: {
  cycle: Cycle;
  states: IssueState[];
  onSelect: () => void;
}) {
  const issueCount = Object.values(cycle.progress_snapshot ?? {}).reduce(
    (n, c) => n + c,
    0,
  );
  return (
    <OrgCard label={`Open cycle ${cycle.name}`} onActivate={onSelect}>
      <div className="flex items-center gap-2">
        <span className="truncate text-[13px] font-semibold text-ink">
          {cycle.name}
        </span>
        <span className="ml-auto">
          <OrgStatusChip color={statusColor(cycle.status)} label={cycle.status} />
        </span>
      </div>
      <p className="mt-1.5 text-[10px] tabular-nums text-ink-muted">
        {dateOnly(cycle.start_date)} → {dateOnly(cycle.end_date)}
        <span className="mx-1.5" aria-hidden>
          ·
        </span>
        <span className="font-mono">{issueCount}</span> issue
        {issueCount === 1 ? "" : "s"}
      </p>
      <div className="mt-2.5">
        <OrgProgressBar
          snapshot={cycle.progress_snapshot ?? {}}
          states={states}
          emptyLabel="No issues in this cycle yet."
        />
      </div>
    </OrgCard>
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
    <div className="w-full">
      {/* C19T4: shared page header replaces the per-page h1 + ProjectNav. */}
      <PageHeader
        viewName="Cycles"
        count={cycles.length}
        addButton={{ label: "New cycle", onClick: () => setNewOpen(true) }}
      />
      <div className="mx-auto w-full max-w-[1060px] px-7 py-6">
        <Dialog open={newOpen} onOpenChange={setNewOpen}>
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

        {cyclesQuery.isError && (
          <Alert variant="destructive" className="mb-4">
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
              <Skeleton className="h-[86px] w-full rounded-md" />
              <Skeleton className="h-[86px] w-full rounded-md" />
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
            <OrgEmpty title="No cycles yet">
              Create the first cycle to start time-boxing work.
            </OrgEmpty>
          ) : (
            <div className="space-y-3">
              {cycles.map((c) => (
                <CycleCard
                  key={c.id}
                  cycle={c}
                  states={states}
                  onSelect={() => setSelectedId(c.id)}
                />
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
  const burndownQuery = useQuery({
    queryKey: ["cycle-burndown", slug, identifier, cycle.id],
    queryFn: () =>
      api.get<CycleBurndown>(
        `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}/cycles/${cycle.id}/burndown`,
      ),
  });
  const snapshot = cycle.progress_snapshot ?? {};
  const openCount =
    (snapshot.triage ?? 0) + (snapshot.backlog ?? 0) + (snapshot.unstarted ?? 0);
  const inProgressCount = snapshot.started ?? 0;
  const doneCount = snapshot.completed ?? 0;
  const stateById = useMemo(
    () => new Map(states.map((s) => [s.id, s])),
    [states],
  );

  return (
    <div>
      <OrgDetailHeader
        backLabel="All cycles"
        onBack={onBack}
        title={cycle.name}
        chip={
          <OrgStatusChip color={statusColor(cycle.status)} label={cycle.status} />
        }
        meta={`${dateOnly(cycle.start_date)} → ${dateOnly(cycle.end_date)}`}
        actions={
          <OrgHeaderButton onClick={onAdd}>
            <Plus className="h-3 w-3" aria-hidden />
            Add issues
          </OrgHeaderButton>
        }
      />

      <div className="mt-4">
        <OrgStatBand
          stats={[
            { label: "Open", value: openCount, note: "triaged, backlog, unstarted" },
            { label: "In progress", value: inProgressCount, note: "started" },
            { label: "Done", value: doneCount, note: "completed" },
          ]}
        />
      </div>

      <div className="mt-4">
        <OrgProgressBar
          snapshot={cycle.progress_snapshot ?? {}}
          states={states}
          legend
          emptyLabel="No issues in this cycle yet."
        />
      </div>

      <div className="mt-4">
        {burndownQuery.status === "pending" ? (
          <Skeleton className="h-64 w-full rounded-md" />
        ) : burndownQuery.status === "error" ? (
          <OrgEmpty title="Couldn't load the burndown chart" />
        ) : burndownQuery.data.total_scope === 0 ? (
          <OrgEmpty title="No burndown yet">
            No issues in this cycle yet — the burndown chart appears once
            issues are added.
          </OrgEmpty>
        ) : !burndownQuery.data.days.some((d) => d.remaining != null) ? (
          <OrgEmpty title="No burndown yet">
            This cycle hasn't started yet — the burndown chart appears once
            it's underway.
          </OrgEmpty>
        ) : (
          <BurndownChart data={burndownQuery.data} />
        )}
      </div>

      <div className="mt-6">
        <OrgSectionHead title="Issues in this cycle" count={issues.length} />
        {issuesLoading ? (
          <div className="space-y-2 pt-2">
            <Skeleton className="h-[47px] w-full" />
            <Skeleton className="h-[47px] w-full" />
          </div>
        ) : issues.length === 0 ? (
          <div className="pt-3">
            <OrgEmpty title="No issues assigned to this cycle yet" />
          </div>
        ) : (
          <div>
            {issues.map((i) => (
              <OrgIssueRow
                key={i.id}
                issue={i}
                state={stateById.get(i.state_id)}
                slug={slug}
                identifier={identifier}
              />
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
