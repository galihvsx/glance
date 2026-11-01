import { useMemo, useState, type FormEvent } from "react";
import { useParams } from "react-router-dom";
import {
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { Plus } from "lucide-react";
import { api, ApiError } from "../lib/api";
import { pqlHasLimit } from "../lib/pql";
import type {
  Cycle,
  CycleBurndown,
  Issue,
  IssueListResult,
  IssueState,
  Workspace,
} from "../lib/types";
import PageHeader from "../components/shell/PageHeader";
import BurndownChart from "../components/cycle/BurndownChart";
import PqlEditor from "../components/issue/PqlEditor";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { Checkbox } from "../components/ui/checkbox";
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "../components/ui/alert-dialog";
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

// C78T1 inline-error pattern: surface ApiError.message, fall back to a
// generic message for anything else.
function errMsg(err: unknown, fallback: string): string {
  return err instanceof ApiError ? err.message : fallback;
}

/**
 * C79T1: manual start/end actions for cycle cards. Upcoming cycles get
 * "Start now", current cycles get "End now" (behind a confirm dialog owned
 * by the page). Members (15)+ only — guests never see these. Completed
 * cycles get neither.
 *
 * The card is an OrgCard (role=button): action clicks/keydowns are stopped
 * so they never also activate the card.
 */
function CycleManualActions({
  cycle,
  canEdit,
  startPending,
  onStart,
  onEndRequest,
  header = false,
}: {
  cycle: Cycle;
  canEdit: boolean;
  startPending: boolean;
  onStart: () => void;
  onEndRequest: () => void;
  header?: boolean;
}) {
  if (!canEdit || (cycle.status !== "upcoming" && cycle.status !== "current")) {
    return null;
  }
  const upcoming = cycle.status === "upcoming";
  const button = header ? (
    upcoming ? (
      <OrgHeaderButton onClick={onStart} disabled={startPending}>
        {startPending ? "Starting…" : "Start now"}
      </OrgHeaderButton>
    ) : (
      <OrgHeaderButton onClick={onEndRequest} destructive>
        End now
      </OrgHeaderButton>
    )
  ) : upcoming ? (
    <Button
      type="button"
      variant="outline"
      size="xs"
      disabled={startPending}
      onClick={onStart}
    >
      {startPending ? "Starting…" : "Start now"}
    </Button>
  ) : (
    <Button type="button" variant="destructive" size="xs" onClick={onEndRequest}>
      End now
    </Button>
  );
  return header ? (
    button
  ) : (
    <div
      className="mt-2.5"
      onClick={(e) => e.stopPropagation()}
      onKeyDown={(e) => e.stopPropagation()}
    >
      {button}
    </div>
  );
}

function CycleCard({
  cycle,
  states,
  onSelect,
  canEdit,
  startPending,
  actionError,
  onStart,
  onEndRequest,
}: {
  cycle: Cycle;
  states: IssueState[];
  onSelect: () => void;
  canEdit: boolean;
  startPending: boolean;
  actionError: string | null;
  onStart: () => void;
  onEndRequest: () => void;
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
      <CycleManualActions
        cycle={cycle}
        canEdit={canEdit}
        startPending={startPending}
        onStart={onStart}
        onEndRequest={onEndRequest}
      />
      {actionError && (
        <Alert variant="destructive" className="mt-2">
          <AlertDescription>{actionError}</AlertDescription>
        </Alert>
      )}
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
  // C79T1: inline per-cycle action error (start/end), keyed by cycle id —
  // the card/dialog stays actionable on failure (C78T1 pattern).
  const [actionError, setActionError] = useState<{
    id: string;
    message: string;
  } | null>(null);
  // C79T1: the cycle awaiting "End now" confirmation.
  const [endTarget, setEndTarget] = useState<Cycle | null>(null);

  const base = `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`;
  const cyclesKey = ["cycles", slug, identifier] as const;

  // C79T1: manual start/end is member (15)+ only — guests are read-only and
  // never see the actions (same role gate as Board.tsx).
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
      // C103T0: the displayed cycle's PQL-filtered list is a separate
      // query key — prefix-invalidate it too (Board pattern), so a
      // committed PQL query re-runs in place after membership
      // changes. Harmless when PQL mode is inactive: that query is
      // disabled and only refetches on next enable.
      void queryClient.invalidateQueries({
        queryKey: ["cycle-issues-pql", slug, identifier, selectedId],
      });
      void queryClient.invalidateQueries({ queryKey: cyclesKey });
    },
    onError: (e) => {
      setFormError(e instanceof ApiError ? e.message : "Failed to add issues");
    },
  });

  // C79T1: manual start — upcoming becomes current. Refresh via the
  // existing cycles query invalidation; a 409 names the conflicting
  // cycle and surfaces inline without closing anything.
  const startMutation = useMutation({
    mutationFn: (id: string) => api.post<Cycle>(`${base}/cycles/${id}/start`),
    onMutate: () => {
      setActionError(null);
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: cyclesKey });
    },
    onError: (e, id) => {
      setActionError({ id, message: errMsg(e, "Failed to start cycle") });
    },
  });

  // C79T1: manual end — completes the cycle early through the exact
  // ticker path (frozen snapshot + rollover per close_in_days). The
  // confirm dialog closes only on success; errors surface inline and
  // stay actionable.
  const endMutation = useMutation({
    mutationFn: (id: string) => api.post<Cycle>(`${base}/cycles/${id}/end`),
    onMutate: () => {
      setActionError(null);
    },
    onSuccess: (_cycle, id) => {
      setEndTarget(null);
      setActionError(null);
      void queryClient.invalidateQueries({ queryKey: cyclesKey });
      void queryClient.invalidateQueries({
        queryKey: ["cycle-issues", slug, identifier, id],
      });
      // C103T0: ending a cycle changes membership via rollover, so
      // the displayed cycle's PQL-filtered list must re-run too
      // (same Board-pattern prefix invalidation as addIssues).
      void queryClient.invalidateQueries({
        queryKey: ["cycle-issues-pql", slug, identifier, id],
      });
      void queryClient.invalidateQueries({
        queryKey: ["cycle-burndown", slug, identifier, id],
      });
    },
    onError: (e, id) => {
      setActionError({ id, message: errMsg(e, "Failed to end cycle") });
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
              key={selected.id}
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
              canEdit={canEdit}
              startPending={
                startMutation.isPending &&
                startMutation.variables === selected.id
              }
              inlineError={
                actionError?.id === selected.id &&
                endTarget?.id !== selected.id
                  ? actionError.message
                  : null
              }
              onStart={() => startMutation.mutate(selected.id)}
              onEndRequest={() => {
                setActionError(null);
                setEndTarget(selected);
              }}
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
                  canEdit={canEdit}
                  startPending={
                    startMutation.isPending && startMutation.variables === c.id
                  }
                  actionError={
                    actionError?.id === c.id && endTarget?.id !== c.id
                      ? actionError.message
                      : null
                  }
                  onStart={() => startMutation.mutate(c.id)}
                  onEndRequest={() => {
                    setActionError(null);
                    setEndTarget(c);
                  }}
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

        {/* C79T1: End-now confirm. A plain Button (not AlertDialogAction) so
            the dialog only closes on success — errors surface inline and the
            user can retry or cancel. */}
        <AlertDialog
          open={endTarget !== null}
          onOpenChange={(o) => {
            if (!o) setEndTarget(null);
          }}
        >
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>End {endTarget?.name} now?</AlertDialogTitle>
              <AlertDialogDescription>
                Completing this cycle early freezes the cycle&apos;s report
                and rolls over incomplete issues per the project&apos;s
                close_in_days rule. This can&apos;t be undone.
              </AlertDialogDescription>
            </AlertDialogHeader>
            {endTarget && actionError?.id === endTarget.id && (
              <Alert variant="destructive">
                <AlertDescription>{actionError.message}</AlertDescription>
              </Alert>
            )}
            <AlertDialogFooter>
              <AlertDialogCancel>Cancel</AlertDialogCancel>
              <Button
                type="button"
                variant="destructive"
                disabled={endMutation.isPending}
                onClick={() => {
                  if (endTarget) endMutation.mutate(endTarget.id);
                }}
              >
                {endMutation.isPending ? "Ending…" : "End cycle"}
              </Button>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
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
  canEdit,
  startPending,
  inlineError,
  onStart,
  onEndRequest,
}: {
  cycle: Cycle;
  states: IssueState[];
  issues: Issue[];
  issuesLoading: boolean;
  onBack: () => void;
  onAdd: () => void;
  slug: string;
  identifier: string;
  canEdit: boolean;
  startPending: boolean;
  inlineError: string | null;
  onStart: () => void;
  onEndRequest: () => void;
}) {
  const burndownQuery = useQuery({
    queryKey: ["cycle-burndown", slug, identifier, cycle.id],
    queryFn: () =>
      api.get<CycleBurndown>(
        `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}/cycles/${cycle.id}/burndown`,
      ),
  });
  // C95T1: PQL mode on the cycle detail list. Reuses PqlEditor verbatim
  // (probe-first, inline 400 + column, cheat-sheet) and the Issues-page
  // pattern: PQL mode changes ONLY the displayed issue list. Progress
  // counts (OrgStatBand / OrgProgressBar below) read
  // cycle.progress_snapshot and the burndown query reads the cycle
  // endpoint — both keep the unfiltered membership list (plan
  // decision 5). The membership fetch in the parent is untouched and
  // still feeds the add-issues dialog's in-cycle set. Default mode
  // (pqlMode false) is byte-for-byte today's behavior: same fetch,
  // same list. No save-as-view here (decision 7) and no visual
  // filter bar (decision 6) — PQL only, transient, no URL persistence.
  const base = `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`;
  const [pqlMode, setPqlMode] = useState(false);
  const [appliedQuery, setAppliedQuery] = useState("");
  const pqlIssuesQuery = useQuery({
    queryKey: ["cycle-issues-pql", slug, identifier, cycle.id, appliedQuery],
    enabled: pqlMode && appliedQuery.trim() !== "",
    queryFn: async () => {
      // C98T1: a LIMIT query is a single page (the server always
      // returns next_cursor null), so skip the cursor loop and
      // fetch once.
      if (pqlHasLimit(appliedQuery)) {
        const p = new URLSearchParams({
          cycle: cycle.id,
          pql: appliedQuery.trim(),
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
          cycle: cycle.id,
          pql: appliedQuery.trim(),
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
  // Probe-first apply (Issues page pattern): fetch one page with
  // cycle + pql and commit only on success. A 400 rejects, so
  // PqlEditor shows it inline and the displayed list is untouched
  // (appliedQuery — and therefore displayedIssues — never changes).
  // Empty query = no filter: stays in PQL mode, shows the full list.
  async function handlePqlApply(query: string): Promise<void> {
    const q = query.trim();
    if (!q) {
      setAppliedQuery("");
      return;
    }
    const p = new URLSearchParams({
      cycle: cycle.id,
      pql: q,
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
    // Leaving PQL mode clears the applied query, so the membership
    // list (already cached) is restored immediately.
    setPqlMode(false);
    setAppliedQuery("");
  }
  // While a committed PQL query is still loading, keep showing the
  // membership list rather than blanking it — the filtered list
  // replaces it as soon as it arrives.
  const displayedIssues =
    pqlMode && appliedQuery.trim() !== ""
      ? (pqlIssuesQuery.data ?? issues)
      : issues;
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
          <>
            {/* C79T1: manual start/end in the detail header (member 15+). */}
            <CycleManualActions
              cycle={cycle}
              header
              canEdit={canEdit}
              startPending={startPending}
              onStart={onStart}
              onEndRequest={onEndRequest}
            />
            <OrgHeaderButton onClick={onAdd}>
              <Plus className="h-3 w-3" aria-hidden />
              Add issues
            </OrgHeaderButton>
          </>
        }
      />

      {inlineError && (
        <Alert variant="destructive" className="mt-4">
          <AlertDescription>{inlineError}</AlertDescription>
        </Alert>
      )}

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
        {/* C95T1: All issues / PQL toggle + editor, above the issue
            list (FilterPanel toggle pattern, detail-page variant:
            there is no visual filter bar here, so the modes are
            "full membership list" vs "PQL query"). */}
        <div
          role="group"
          aria-label="Issue list mode"
          className="mb-2 flex rounded-md border p-0.5"
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
            appliedQuery={appliedQuery}
            onApply={handlePqlApply}
            onClear={() => setAppliedQuery("")}
            showSaveHint={false}
          />
        )}
        <OrgSectionHead
          title="Issues in this cycle"
          count={displayedIssues.length}
        />
        {issuesLoading ? (
          <div className="space-y-2 pt-2">
            <Skeleton className="h-[47px] w-full" />
            <Skeleton className="h-[47px] w-full" />
          </div>
        ) : displayedIssues.length === 0 ? (
          <div className="pt-3">
            {/* A valid query that matches nothing is a real empty
                result, not "no issues assigned" — say so, so the
                empty state never masquerades as membership. */}
            <OrgEmpty
              title={
                pqlMode &&
                appliedQuery.trim() !== "" &&
                pqlIssuesQuery.data !== undefined
                  ? "No issues match this PQL query"
                  : "No issues assigned to this cycle yet"
              }
            />
          </div>
        ) : (
          <div>
            {displayedIssues.map((i) => (
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
