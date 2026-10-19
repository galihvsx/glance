// C74T1: Epics list page. Project-scoped; mirrors Initiatives.tsx (its
// workspace-scoped sibling): PageHeader + create dialog, rich URL-backed
// filter bar (C74T0 server-side params: status multi, q, created_by,
// date range, initiative_id, sort/order), and each card fetches its own
// progress (GET .../epics/:id carries progress.by_group — the list
// endpoint ships none) with a skeleton while it loads.
//
// Guests (workspace role < 15) may read; the create affordances are hidden
// for them (the server 403 stays the backstop).

import { useEffect, useMemo, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../lib/api";
import {
  epicActiveFilterCount,
  toEpicApiParams,
  useEpicFilters,
} from "../lib/epicFilters";
import type { Epic, Initiative, Member, Workspace } from "../lib/types";
import PageHeader from "../components/shell/PageHeader";
import EpicForm from "../components/epics/EpicForm";
import EpicFilterBar from "../components/epics/EpicFilterBar";
import EpicGroupBreakdown from "../components/epics/EpicGroupBreakdown";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "../components/ui/dialog";
import { Button } from "../components/ui/button";
import { Skeleton } from "../components/ui/skeleton";
import { Alert, AlertDescription } from "../components/ui/alert";
import { OrgCard, OrgEmpty, OrgStatusChip } from "../components/org/OrgViews";

function epicBase(slug: string, identifier: string): string {
  return `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}/epics`;
}

function EpicCard({
  epic,
  slug,
  identifier,
  onOpen,
}: {
  epic: Epic;
  slug: string;
  identifier: string;
  onOpen: () => void;
}) {
  const detailQuery = useQuery({
    queryKey: ["epic-detail", slug, identifier, epic.id],
    queryFn: () =>
      api.get<Epic>(`${epicBase(slug, identifier)}/${epic.id}`),
  });
  const archived = !!epic.archived_at;
  const progress = detailQuery.data?.progress ?? null;

  return (
    <OrgCard
      label={`Open epic ${epic.name}`}
      onActivate={onOpen}
      className={archived ? "opacity-55" : undefined}
    >
      <div className="flex items-center gap-2">
        <span className="truncate text-[13px] font-semibold text-ink">
          {epic.name}
        </span>
        {archived && (
          <span className="ml-auto">
            <OrgStatusChip color="#777780" label="Archived" />
          </span>
        )}
      </div>
      {epic.description && (
        <p className="mt-1 line-clamp-2 text-[11px] text-ink-muted">
          {epic.description}
        </p>
      )}
      <p className="mt-1.5 text-[10px] tabular-nums text-ink-muted">
        <span className="font-mono">{epic.issue_count}</span> issue
        {epic.issue_count === 1 ? "" : "s"}
        {archived &&
          epic.archived_at &&
          ` · archived ${epic.archived_at.slice(0, 10)}`}
      </p>
      <div className="mt-2.5">
        {detailQuery.isPending ? (
          <Skeleton
            data-testid="epic-progress-skeleton"
            className="h-[38px] w-full rounded-md"
          />
        ) : progress ? (
          <EpicGroupBreakdown
            byGroup={progress.by_group}
            total={progress.total}
            epicName={epic.name}
          />
        ) : null}
      </div>
    </OrgCard>
  );
}

export default function Epics() {
  const { slug = "", identifier = "" } = useParams<{
    slug: string;
    identifier: string;
  }>();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [createOpen, setCreateOpen] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);

  const base = epicBase(slug, identifier);

  // C74T1: rich filter state lives in the URL search params (shareable
  // links); the list query refetches whenever the serialized API params
  // change. Filtering is server-side — no client-side name filter.
  const [filters, setFilters, clearFilters] = useEpicFilters();
  const filterCount = epicActiveFilterCount(filters);
  const apiParamString = useMemo(
    () => toEpicApiParams(filters).toString(),
    [filters],
  );
  const epicsKey = ["epics", slug, identifier, apiParamString] as const;
  const listUrl = `${base}?${apiParamString}`;

  // Same workspace role gate as the initiatives page — the create
  // affordances are hidden for guests (role < 15); the server 403 stays
  // the backstop. The filter bar stays visible for everyone.
  const workspaceQuery = useQuery({
    queryKey: ["workspace", slug],
    queryFn: () =>
      api.get<Workspace>(`/api/v1/workspaces/${encodeURIComponent(slug)}`),
  });
  const canCreate = (workspaceQuery.data?.role ?? 0) >= 15;

  const epicsQuery = useQuery({
    queryKey: epicsKey,
    queryFn: () =>
      api
        .get<{ epics: Epic[] }>(listUrl)
        .then((d) => d.epics),
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

  // Linked-initiative picker data: workspace initiatives (C73 endpoint).
  const initiativesQuery = useQuery({
    queryKey: ["initiatives", slug],
    queryFn: () =>
      api
        .get<{ initiatives: Initiative[] }>(
          `/api/v1/workspaces/${encodeURIComponent(slug)}/initiatives`,
        )
        .then((d) => d.initiatives),
  });

  const createMutation = useMutation({
    mutationFn: (v: { name: string; description: string }) =>
      api.post<Epic>(base, {
        name: v.name,
        ...(v.description ? { description: v.description } : {}),
      }),
    onSuccess: (created) => {
      setCreateOpen(false);
      setFormError(null);
      // Prefix key: invalidate every filter variant of the list, not just
      // the currently active one.
      void queryClient.invalidateQueries({
        queryKey: ["epics", slug, identifier],
      });
      navigate(
        `/w/${encodeURIComponent(slug)}/p/${encodeURIComponent(identifier)}/epics/${created.id}`,
      );
    },
    onError: (e) =>
      setFormError(
        e instanceof ApiError ? e.message : "Failed to create epic",
      ),
  });

  const epics = epicsQuery.data ?? [];

  return (
    <div className="w-full">
      <PageHeader
        viewName="Epics"
        count={epics.length}
        addButton={
          canCreate
            ? { label: "New epic", onClick: () => setCreateOpen(true) }
            : null
        }
      />
      <div className="mx-auto w-full max-w-[1060px] px-7 py-6">
        {epicsQuery.isError && (
          <Alert variant="destructive" className="mb-4">
            <AlertDescription>
              {epicsQuery.error instanceof ApiError
                ? epicsQuery.error.message
                : "Failed to load epics"}
            </AlertDescription>
          </Alert>
        )}

        <div className="mt-4">
          <EpicFilterBar
            filters={filters}
            onChange={setFilters}
            onClear={clearFilters}
            members={membersQuery.data ?? []}
            initiatives={initiativesQuery.data ?? []}
            searchInput={searchInput}
            onSearchInput={setSearchInput}
          />
          {filterCount > 0 && !epicsQuery.isPending && (
            <p
              className="mt-2 text-xs text-ink-muted"
              aria-live="polite"
              data-testid="epics-result-count"
            >
              {epics.length} result{epics.length === 1 ? "" : "s"}
            </p>
          )}
        </div>

        <div className="mt-4">
          {epicsQuery.isPending ? (
            <div className="space-y-3">
              <Skeleton className="h-[110px] w-full rounded-md" />
              <Skeleton className="h-[110px] w-full rounded-md" />
            </div>
          ) : epics.length === 0 ? (
            filterCount > 0 ? (
              <div>
                <OrgEmpty title="No epics match your filters">
                  Try widening the search or clearing a filter.
                </OrgEmpty>
                <div className="mt-4 flex justify-center">
                  <Button variant="outline" onClick={clearFilters}>
                    Clear filters
                  </Button>
                </div>
              </div>
            ) : (
              <OrgEmpty title="No epics yet">
                Create the first epic to group related work.
              </OrgEmpty>
            )
          ) : (
            <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
              {epics.map((e) => (
                <EpicCard
                  key={e.id}
                  epic={e}
                  slug={slug}
                  identifier={identifier}
                  onOpen={() =>
                    navigate(
                      `/w/${encodeURIComponent(slug)}/p/${encodeURIComponent(identifier)}/epics/${e.id}`,
                    )
                  }
                />
              ))}
            </div>
          )}
        </div>

        <Dialog open={createOpen} onOpenChange={setCreateOpen}>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>New epic</DialogTitle>
              <DialogDescription>
                A long-running container for work that spans cycles — group
                related issues under one epic.
              </DialogDescription>
            </DialogHeader>
            <EpicForm
              key={createOpen ? "open" : "closed"}
              initial={{ name: "", description: "" }}
              pending={createMutation.isPending}
              error={formError}
              submitLabel="Create epic"
              onSubmit={(v) => createMutation.mutate(v)}
            />
          </DialogContent>
        </Dialog>
      </div>
    </div>
  );
}
