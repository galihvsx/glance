// C71T1: Epics list page. Project-scoped; mirrors Initiatives.tsx (its
// workspace-scoped sibling): PageHeader + create dialog, and each card
// fetches its own progress (GET .../epics/:id carries progress.by_group —
// the list endpoint ships none) with a skeleton while it loads.
//
// Archived epics are hidden from the list by the server unless
// ?include_archived=true; the toggle re-issues the list query.

import { useMemo, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Search } from "lucide-react";
import { api, ApiError } from "../lib/api";
import type { Epic } from "../lib/types";
import PageHeader from "../components/shell/PageHeader";
import EpicForm from "../components/epics/EpicForm";
import EpicGroupBreakdown from "../components/epics/EpicGroupBreakdown";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "../components/ui/dialog";
import { Input } from "../components/ui/input";
import { Skeleton } from "../components/ui/skeleton";
import { Switch } from "../components/ui/switch";
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
  const [search, setSearch] = useState("");
  const [showArchived, setShowArchived] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);

  const base = epicBase(slug, identifier);
  const epicsKey = ["epics", slug, identifier, showArchived] as const;

  const epicsQuery = useQuery({
    queryKey: epicsKey,
    queryFn: () =>
      api
        .get<{ epics: Epic[] }>(
          `${base}${showArchived ? "?include_archived=true" : ""}`,
        )
        .then((d) => d.epics),
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

  const epics = useMemo(() => {
    const q = search.trim().toLowerCase();
    const list = epicsQuery.data ?? [];
    if (!q) return list;
    return list.filter((e) => e.name.toLowerCase().includes(q));
  }, [epicsQuery.data, search]);

  return (
    <div className="w-full">
      <PageHeader
        viewName="Epics"
        count={epics.length}
        addButton={{ label: "New epic", onClick: () => setCreateOpen(true) }}
      />
      <div className="mx-auto w-full max-w-[1060px] px-7 py-6">
        <div className="mb-4 flex items-center gap-4">
          <div className="relative w-[260px]">
            <Search className="absolute left-2 top-[8px] h-3.5 w-3.5 text-ink-muted" />
            <Input
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Search epics…"
              aria-label="Search epics"
              className="pl-8"
            />
          </div>
          <label
            htmlFor="epic-show-archived"
            className="flex cursor-pointer items-center gap-2 text-[12px] text-ink-secondary"
          >
            <Switch
              id="epic-show-archived"
              checked={showArchived}
              onCheckedChange={setShowArchived}
            />
            Show archived
          </label>
        </div>

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
          {epicsQuery.isPending ? (
            <div className="space-y-3">
              <Skeleton className="h-[110px] w-full rounded-md" />
              <Skeleton className="h-[110px] w-full rounded-md" />
            </div>
          ) : epics.length === 0 ? (
            <OrgEmpty title="No epics yet">
              {search
                ? "No epics match your search."
                : "Create the first epic to group related work."}
            </OrgEmpty>
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
