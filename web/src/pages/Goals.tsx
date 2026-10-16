// Goals (C35T1): workspace-scoped objectives + key results (OKRs).
// List + create UI; the detail page is GoalDetail.tsx. Guests
// (workspace role < 15) may read; the create affordances are hidden
// for them (the server 403 stays the backstop).
//
// Implements the approved C35 prototype (goals.html): objective cards
// with title, owner, target date, status badge, progress bar, KR count;
// create form with title (required), description, owner, start/target
// date, status (default Active). A duplicate title in the workspace
// surfaces the backend's 409 error. Progress is computed server-side
// (mean of KR progress) — the list endpoint ships it per objective,
// so no per-card rollup fetch is needed (unlike Initiatives).

import { useState, type FormEvent } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../lib/api";
import type {
  Objective,
  ObjectiveStatus,
  Workspace,
  WorkspaceMember,
} from "../lib/types";
import PageHeader from "../components/shell/PageHeader";
import GoalProgressBar from "../components/goals/GoalProgressBar";
import GoalSparkline from "../components/goals/GoalSparkline";
import { objectiveStatusColor } from "../components/goals/okrFormat";
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
import { Textarea } from "../components/ui/textarea";
import { Skeleton } from "../components/ui/skeleton";
import { Alert, AlertDescription } from "../components/ui/alert";
import { OrgCard, OrgEmpty, OrgStatusChip } from "../components/org/OrgViews";

const STATUSES: ObjectiveStatus[] = ["active", "completed", "archived"];

function statusLabel(status: ObjectiveStatus): string {
  return status.charAt(0).toUpperCase() + status.slice(1);
}

function ObjectiveCard({
  objective,
  onOpen,
}: {
  objective: Objective;
  onOpen: () => void;
}) {
  return (
    <OrgCard
      label={`Open objective ${objective.title}`}
      onActivate={onOpen}
    >
      <div className="flex items-center gap-2">
        <span className="truncate text-[13px] font-semibold text-ink">
          {objective.title}
        </span>
        <span className="ml-auto">
          <OrgStatusChip
            color={objectiveStatusColor(objective.status)}
            label={statusLabel(objective.status)}
          />
        </span>
      </div>
      {objective.description && (
        <p className="mt-1 line-clamp-2 text-[11px] text-ink-muted">
          {objective.description}
        </p>
      )}
      <p className="mt-1.5 text-[10px] tabular-nums text-ink-muted">
        Owner: {objective.owner?.name ?? objective.owner?.email ?? "—"}
        {objective.target_date ? ` · Target: ${objective.target_date}` : ""}
        {` · ${objective.key_result_count} key result${objective.key_result_count === 1 ? "" : "s"}`}
      </p>
      <div className="mt-2.5">
        <GoalProgressBar progress={objective.progress} />
      </div>
      {(objective.progress_history?.length ?? 0) > 0 && (
        <div className="mt-2">
          <GoalSparkline points={objective.progress_history ?? []} />
        </div>
      )}
    </OrgCard>
  );
}

export default function Goals() {
  const { slug = "" } = useParams<{ slug: string }>();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [createOpen, setCreateOpen] = useState(false);
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [ownerId, setOwnerId] = useState("");
  const [startDate, setStartDate] = useState("");
  const [targetDate, setTargetDate] = useState("");
  const [status, setStatus] = useState<ObjectiveStatus>("active");
  const [formError, setFormError] = useState<string | null>(null);

  const base = `/api/v1/workspaces/${encodeURIComponent(slug)}/objectives`;
  const objectivesKey = ["objectives", slug] as const;

  // Same workspace role gate as Initiatives — the create affordances
  // are hidden for guests (role < 15); the server 403 stays the
  // backstop.
  const workspaceQuery = useQuery({
    queryKey: ["workspace", slug],
    queryFn: () =>
      api.get<Workspace>(`/api/v1/workspaces/${encodeURIComponent(slug)}`),
  });
  const canCreate = (workspaceQuery.data?.role ?? 0) >= 15;

  const objectivesQuery = useQuery({
    queryKey: objectivesKey,
    queryFn: () =>
      api.get<{ objectives: Objective[] }>(base).then((d) => d.objectives),
  });

  // Owner picker: workspace members, loaded only when the create
  // dialog opens (same enabled-gate pattern as Initiatives' project
  // picker). Owners need not be members server-side, but members are
  // the sane pick list.
  const membersQuery = useQuery({
    queryKey: ["members", slug],
    enabled: createOpen,
    queryFn: () =>
      api
        .get<{ members: WorkspaceMember[] }>(
          `/api/v1/workspaces/${encodeURIComponent(slug)}/members`,
        )
        .then((d) => d.members),
  });

  const createMutation = useMutation({
    mutationFn: (v: {
      title: string;
      description: string;
      ownerId: string;
      startDate: string;
      targetDate: string;
      status: ObjectiveStatus;
    }) =>
      api.post<Objective>(base, {
        title: v.title,
        ...(v.description ? { description: v.description } : {}),
        ...(v.ownerId ? { owner_id: v.ownerId } : {}),
        ...(v.startDate ? { start_date: v.startDate } : {}),
        ...(v.targetDate ? { target_date: v.targetDate } : {}),
        status: v.status,
      }),
    onSuccess: () => {
      setCreateOpen(false);
      setTitle("");
      setDescription("");
      setOwnerId("");
      setStartDate("");
      setTargetDate("");
      setStatus("active");
      setFormError(null);
      void queryClient.invalidateQueries({ queryKey: objectivesKey });
    },
    onError: (e) => {
      setFormError(
        e instanceof ApiError
          ? e.status === 409
            ? "An objective with that title already exists in this workspace."
            : e.message
          : "Failed to create objective",
      );
    },
  });

  function onCreate(e: FormEvent) {
    e.preventDefault();
    setFormError(null);
    createMutation.mutate({
      title: title.trim(),
      description: description.trim(),
      ownerId,
      startDate,
      targetDate,
      status,
    });
  }

  const objectives = objectivesQuery.data ?? [];

  return (
    <div className="w-full">
      <PageHeader
        viewName="Goals"
        count={objectives.length}
        addButton={
          canCreate
            ? { label: "New objective", onClick: () => setCreateOpen(true) }
            : null
        }
      />
      <div className="mx-auto w-full max-w-[1060px] px-7 py-6">
        <Dialog open={createOpen} onOpenChange={setCreateOpen}>
          <DialogContent className="max-h-[80vh] overflow-y-auto">
            <DialogHeader>
              <DialogTitle>New objective</DialogTitle>
              <DialogDescription>
                A workspace goal tracked by measurable key results.
                Progress is computed from the key results — never stored.
              </DialogDescription>
            </DialogHeader>
            <form onSubmit={onCreate} className="space-y-4">
              <div className="space-y-2">
                <Label htmlFor="g-title">Title</Label>
                <Input
                  id="g-title"
                  value={title}
                  onChange={(e) => setTitle(e.target.value)}
                  placeholder="Ship glance v0.4.0 with zero open parity gaps"
                  required
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="g-description">Description</Label>
                <Textarea
                  id="g-description"
                  value={description}
                  onChange={(e) => setDescription(e.target.value)}
                  placeholder="What this objective means (optional)"
                  rows={3}
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="g-owner">Owner</Label>
                <select
                  id="g-owner"
                  value={ownerId}
                  onChange={(e) => setOwnerId(e.target.value)}
                  className="w-full rounded-md border border-input bg-background px-2.5 py-2 text-[13px] text-ink"
                >
                  <option value="">No owner</option>
                  {(membersQuery.data ?? []).map((m) => (
                    <option key={m.id} value={m.id}>
                      {m.name ?? m.email}
                    </option>
                  ))}
                </select>
              </div>
              <div className="grid grid-cols-2 gap-4">
                <div className="space-y-2">
                  <Label htmlFor="g-start">Start date</Label>
                  <Input
                    id="g-start"
                    type="date"
                    value={startDate}
                    onChange={(e) => setStartDate(e.target.value)}
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="g-target">Target date</Label>
                  <Input
                    id="g-target"
                    type="date"
                    value={targetDate}
                    onChange={(e) => setTargetDate(e.target.value)}
                  />
                </div>
              </div>
              <div className="space-y-2">
                <Label htmlFor="g-status">Status</Label>
                <select
                  id="g-status"
                  value={status}
                  onChange={(e) =>
                    setStatus(e.target.value as ObjectiveStatus)
                  }
                  className="w-full rounded-md border border-input bg-background px-2.5 py-2 text-[13px] text-ink"
                >
                  {STATUSES.map((s) => (
                    <option key={s} value={s}>
                      {statusLabel(s)}
                    </option>
                  ))}
                </select>
              </div>
              {formError && (
                <Alert variant="destructive">
                  <AlertDescription>{formError}</AlertDescription>
                </Alert>
              )}
              <DialogFooter>
                <Button
                  type="submit"
                  disabled={createMutation.isPending || !title.trim()}
                >
                  {createMutation.isPending ? "Creating…" : "Create objective"}
                </Button>
              </DialogFooter>
            </form>
          </DialogContent>
        </Dialog>

        {objectivesQuery.isError && (
          <Alert variant="destructive" className="mb-4">
            <AlertDescription>
              {objectivesQuery.error instanceof ApiError
                ? objectivesQuery.error.message
                : "Failed to load goals"}
            </AlertDescription>
          </Alert>
        )}

        <div className="mt-4">
          {objectivesQuery.isPending ? (
            <div className="space-y-3">
              <Skeleton className="h-[110px] w-full rounded-md" />
              <Skeleton className="h-[110px] w-full rounded-md" />
            </div>
          ) : objectives.length === 0 ? (
            <div>
              <OrgEmpty title="No goals yet">
                Create the first objective to track measurable key results
                for this workspace.
              </OrgEmpty>
              {canCreate && (
                <div className="mt-4 flex justify-center">
                  <Button onClick={() => setCreateOpen(true)}>
                    Create objective
                  </Button>
                </div>
              )}
            </div>
          ) : (
            <div className="space-y-3">
              {objectives.map((o) => (
                <ObjectiveCard
                  key={o.id}
                  objective={o}
                  onOpen={() =>
                    navigate(
                      `/w/${encodeURIComponent(slug)}/goals/${o.id}`,
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
