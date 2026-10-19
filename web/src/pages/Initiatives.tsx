// Initiatives (C27T1): workspace-scoped program containers that group
// projects — unlike cycles/modules, which are project-scoped. List +
// create UI; the detail page is C27T2's. Guests (workspace role < 15)
// may read; the create affordances are hidden for them (the server
// 403 stays the backstop).
//
// Progress per card comes from the rollup endpoint
// (GET .../initiatives/:id/rollup) — the list endpoint ships no
// progress, so each card fetches its own rollup with a skeleton while
// it loads.

import { useEffect, useMemo, useState, type FormEvent } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../lib/api";
import {
  initiativeActiveFilterCount,
  toInitiativeApiParams,
  useInitiativeFilters,
} from "../lib/initiativeFilters";
import type {
  Initiative,
  InitiativeRollup,
  Member,
  Project,
  Workspace,
} from "../lib/types";
import PageHeader from "../components/shell/PageHeader";
import InitiativeProgressBar from "../components/initiatives/InitiativeProgressBar";
import InitiativeFilterBar from "../components/initiatives/InitiativeFilterBar";
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
import { Textarea } from "../components/ui/textarea";
import { Skeleton } from "../components/ui/skeleton";
import { Alert, AlertDescription } from "../components/ui/alert";
import { OrgCard, OrgEmpty, OrgStatusChip } from "../components/org/OrgViews";

/** Status chip dot colors: state/accent colors, never hardcoded grays. */
function initiativeStatusColor(status: string): string {
  switch (status) {
    case "active":
      return "#4f8ef7"; // accent blue
    case "completed":
      return "#42c88a"; // accent green
    default:
      return "#777780"; // muted (archived)
  }
}

function InitiativeCard({
  initiative,
  slug,
  onOpen,
}: {
  initiative: Initiative;
  slug: string;
  onOpen: () => void;
}) {
  const rollupQuery = useQuery({
    queryKey: ["initiative-rollup", slug, initiative.id],
    queryFn: () =>
      api.get<InitiativeRollup>(
        `/api/v1/workspaces/${encodeURIComponent(slug)}/initiatives/${initiative.id}/rollup`,
      ),
  });

  return (
    <OrgCard
      label={`Open initiative ${initiative.name}`}
      onActivate={onOpen}
    >
      <div className="flex items-center gap-2">
        <span className="truncate text-[13px] font-semibold text-ink">
          {initiative.name}
        </span>
        <span className="ml-auto">
          <OrgStatusChip
            color={initiativeStatusColor(initiative.status)}
            label={initiative.status}
          />
        </span>
      </div>
      {initiative.description && (
        <p className="mt-1 line-clamp-2 text-[11px] text-ink-muted">
          {initiative.description}
        </p>
      )}
      <p className="mt-1.5 text-[10px] tabular-nums text-ink-muted">
        <span className="font-mono">{initiative.project_count}</span> project
        {initiative.project_count === 1 ? "" : "s"}
      </p>
      <div className="mt-2.5">
        {rollupQuery.isPending ? (
          <Skeleton
            data-testid="initiative-rollup-skeleton"
            className="h-[38px] w-full rounded-md"
          />
        ) : rollupQuery.data ? (
          <InitiativeProgressBar
            completionPct={rollupQuery.data.completion_pct}
            total={rollupQuery.data.total}
            doneCount={rollupQuery.data.done_count}
          />
        ) : null}
      </div>
    </OrgCard>
  );
}

export default function Initiatives() {
  const { slug = "" } = useParams<{ slug: string }>();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [createOpen, setCreateOpen] = useState(false);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const [formError, setFormError] = useState<string | null>(null);

  const base = `/api/v1/workspaces/${encodeURIComponent(slug)}/initiatives`;

  // C73T1: rich filter state lives in the URL search params (shareable
  // links); the list query refetches whenever the serialized params change.
  const [filters, setFilters, clearFilters] = useInitiativeFilters();
  const filterCount = initiativeActiveFilterCount(filters);
  const apiParamString = useMemo(
    () => toInitiativeApiParams(filters).toString(),
    [filters],
  );
  const initiativesKey = ["initiatives", slug, apiParamString] as const;
  const listUrl = apiParamString ? `${base}?${apiParamString}` : base;

  // C26T0: same workspace role gate as the board/calendar — the create
  // affordances are hidden for guests (role < 15); the server 403 stays
  // the backstop.
  const workspaceQuery = useQuery({
    queryKey: ["workspace", slug],
    queryFn: () =>
      api.get<Workspace>(`/api/v1/workspaces/${encodeURIComponent(slug)}`),
  });
  const canCreate = (workspaceQuery.data?.role ?? 0) >= 15;

  const initiativesQuery = useQuery({
    queryKey: initiativesKey,
    queryFn: () =>
      api.get<{ initiatives: Initiative[] }>(listUrl).then((d) => d.initiatives),
  });

  // Debounced search: the box stays local (300ms) and feeds `q` upstream —
  // seeded from the URL so a pasted link shows its query in the box.
  const [searchInput, setSearchInput] = useState(filters.q);
  useEffect(() => {
    const t = setTimeout(() => {
      const trimmed = searchInput.trim();
      if (trimmed !== filters.q) setFilters({ q: trimmed });
    }, 300);
    return () => clearTimeout(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [searchInput]);
  useEffect(() => {
    setSearchInput(filters.q);
  }, [filters.q]);

  // Member-project picker data: workspace projects (also feeds the create
  // dialog's project picker). Loaded eagerly so the filter bar can use it.
  const projectsQuery = useQuery({
    queryKey: ["projects", slug],
    queryFn: () =>
      api
        .get<{ projects: Project[] }>(
          `/api/v1/workspaces/${encodeURIComponent(slug)}/projects`,
        )
        .then((d) => d.projects),
  });

  // Created-by picker data: workspace members (same endpoint FilterPanel uses).
  const membersQuery = useQuery({
    queryKey: ["members", slug],
    queryFn: () =>
      api
        .get<{ members: Member[] }>(
          `/api/v1/workspaces/${encodeURIComponent(slug)}/members`,
        )
        .then((d) => d.members),
  });

  const createMutation = useMutation({
    mutationFn: async (v: {
      name: string;
      description: string;
      projectIds: string[];
    }) => {
      const created = await api.post<Initiative>(base, {
        name: v.name,
        ...(v.description ? { description: v.description } : {}),
      });
      // Member adds are idempotent server-side (ON CONFLICT DO NOTHING).
      await Promise.all(
        v.projectIds.map((projectId) =>
          api.post(`${base}/${created.id}/projects`, { project_id: projectId }),
        ),
      );
      return created;
    },
    onSuccess: () => {
      setCreateOpen(false);
      setName("");
      setDescription("");
      setPicked(new Set());
      setFormError(null);
      // Prefix key: invalidate every filter variant of the list, not just
      // the currently active one.
      void queryClient.invalidateQueries({ queryKey: ["initiatives", slug] });
    },
    onError: (e) => {
      setFormError(
        e instanceof ApiError ? e.message : "Failed to create initiative",
      );
    },
  });

  function onCreate(e: FormEvent) {
    e.preventDefault();
    setFormError(null);
    createMutation.mutate({
      name: name.trim(),
      description: description.trim(),
      projectIds: [...picked],
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

  const initiatives = initiativesQuery.data ?? [];

  return (
    <div className="w-full">
      <PageHeader
        viewName="Initiatives"
        count={initiatives.length}
        addButton={
          canCreate
            ? { label: "New initiative", onClick: () => setCreateOpen(true) }
            : null
        }
      />
      <div className="mx-auto w-full max-w-[1060px] px-7 py-6">
        <Dialog open={createOpen} onOpenChange={setCreateOpen}>
          <DialogContent className="max-h-[80vh] overflow-y-auto">
            <DialogHeader>
              <DialogTitle>New initiative</DialogTitle>
              <DialogDescription>
                A program spanning projects. Issues stay project-scoped —
                the initiative tracks progress across its member projects.
              </DialogDescription>
            </DialogHeader>
            <form onSubmit={onCreate} className="space-y-4">
              <div className="space-y-2">
                <Label htmlFor="i-name">Name</Label>
                <Input
                  id="i-name"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="Platform modernization"
                  required
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="i-description">Description</Label>
                <Textarea
                  id="i-description"
                  value={description}
                  onChange={(e) => setDescription(e.target.value)}
                  placeholder="What this program is about (optional)"
                  rows={3}
                />
              </div>
              <div className="space-y-2">
                <Label>Member projects</Label>
                {projectsQuery.isPending ? (
                  <div className="space-y-2">
                    <Skeleton className="h-10 w-full" />
                    <Skeleton className="h-10 w-full" />
                  </div>
                ) : (
                  <div className="space-y-1">
                    {(projectsQuery.data ?? []).map((p) => (
                      <label
                        key={p.id}
                        className="flex cursor-pointer items-center gap-3 rounded-md px-2 py-2 text-sm hover:bg-accent"
                      >
                        <Checkbox
                          checked={picked.has(p.id)}
                          onCheckedChange={() => togglePicked(p.id)}
                        />
                        <span className="truncate">{p.name}</span>
                        <span className="ml-auto font-mono text-[10px] text-ink-muted">
                          {p.identifier}
                        </span>
                      </label>
                    ))}
                    {(projectsQuery.data ?? []).length === 0 && (
                      <p className="text-sm text-muted-foreground">
                        No projects in this workspace yet.
                      </p>
                    )}
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
                  {createMutation.isPending ? "Creating…" : "Create initiative"}
                </Button>
              </DialogFooter>
            </form>
          </DialogContent>
        </Dialog>

        {initiativesQuery.isError && (
          <Alert variant="destructive" className="mb-4">
            <AlertDescription>
              {initiativesQuery.error instanceof ApiError
                ? initiativesQuery.error.message
                : "Failed to load initiatives"}
            </AlertDescription>
          </Alert>
        )}

        <div className="mt-4">
          <InitiativeFilterBar
            filters={filters}
            onChange={setFilters}
            onClear={clearFilters}
            members={membersQuery.data ?? []}
            projects={projectsQuery.data ?? []}
            searchInput={searchInput}
            onSearchInput={setSearchInput}
          />
          {filterCount > 0 && !initiativesQuery.isPending && (
            <p
              className="mt-2 text-xs text-ink-muted"
              aria-live="polite"
              data-testid="initiatives-result-count"
            >
              {initiatives.length} result{initiatives.length === 1 ? "" : "s"}
            </p>
          )}
        </div>

        <div className="mt-4">
          {initiativesQuery.isPending ? (
            <div className="space-y-3">
              <Skeleton className="h-[110px] w-full rounded-md" />
              <Skeleton className="h-[110px] w-full rounded-md" />
            </div>
          ) : initiatives.length === 0 ? (
            filterCount > 0 ? (
              <div>
                <OrgEmpty title="No initiatives match your filters">
                  Try widening the search or clearing a filter.
                </OrgEmpty>
                <div className="mt-4 flex justify-center">
                  <Button variant="outline" onClick={clearFilters}>
                    Clear filters
                  </Button>
                </div>
              </div>
            ) : (
              <div>
                <OrgEmpty title="No initiatives yet">
                  Create the first initiative to group projects into a shared
                  program.
                </OrgEmpty>
                {canCreate && (
                  <div className="mt-4 flex justify-center">
                    <Button onClick={() => setCreateOpen(true)}>
                      Create initiative
                    </Button>
                  </div>
                )}
              </div>
            )
          ) : (
            <div className="space-y-3">
              {initiatives.map((i) => (
                <InitiativeCard
                  key={i.id}
                  initiative={i}
                  slug={slug}
                  onOpen={() =>
                    navigate(
                      `/w/${encodeURIComponent(slug)}/initiatives/${i.id}`,
                    )
                  }
                />
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
