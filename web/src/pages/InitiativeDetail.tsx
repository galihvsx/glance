// C27T2: initiative detail page. Header (name, status badge + changer,
// description, edit, delete), aggregate progress rollup, per-project
// breakdown, and member-project management. Guests get a read-only view;
// all writes are member (15)+ gated, matching the backend contract.
import { useMemo, useState, type FormEvent } from "react";
import { useNavigate, useParams } from "react-router-dom";
import {
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { Pencil, Plus, Trash2, X } from "lucide-react";
import { api, ApiError } from "../lib/api";
import { toast } from "../components/ui/toast";
import type { Project, Workspace } from "../lib/types";
import InitiativeProgressBar from "../components/initiatives/InitiativeProgressBar";
import {
  GROUP_COLORS,
  GROUP_ORDER,
  groupLabel,
} from "../components/initiatives/stateGroups";
import type {
  Initiative,
  InitiativeProject,
  InitiativeProjectRollup,
  InitiativeRollup,
  InitiativeStatus,
} from "../components/initiatives/types";
import ProjectLabelChips from "../components/project/ProjectLabelChips";
import {
  OrgDetailHeader,
  OrgEmpty,
  OrgHeaderButton,
  OrgSectionHead,
  OrgStatusChip,
} from "../components/org/OrgViews";
import { Card, CardContent } from "../components/ui/card";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "../components/ui/alert-dialog";
import { Button } from "../components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "../components/ui/dropdown-menu";
import { Input } from "../components/ui/input";
import { Label } from "../components/ui/label";
import { Skeleton } from "../components/ui/skeleton";
import { Textarea } from "../components/ui/textarea";

const STATUSES: InitiativeStatus[] = ["active", "completed", "archived"];

function statusColor(status: InitiativeStatus): string {
  switch (status) {
    case "active":
      return "#4f8ef7"; // accent blue
    case "completed":
      return "#42c88a"; // accent green
    case "archived":
      return "#777780"; // muted
  }
}

/** Stacked state-group segments + legend chips (labeled, with counts). */
function GroupBreakdown({
  byGroup,
  emptyLabel,
}: {
  byGroup: Record<string, number>;
  emptyLabel: string;
}) {
  const total = Object.values(byGroup).reduce((n, c) => n + (c ?? 0), 0);
  if (total === 0) {
    return <p className="text-[11px] text-ink-muted">{emptyLabel}</p>;
  }
  return (
    <div>
      <div
        className="flex h-[6px] w-full overflow-hidden rounded-full bg-raised"
        role="progressbar"
        aria-valuemin={0}
        aria-valuemax={total}
        aria-valuenow={byGroup.completed ?? 0}
        aria-label="Issues by state group"
      >
        {GROUP_ORDER.map((g) => {
          const n = byGroup[g] ?? 0;
          if (n === 0) return null;
          return (
            <div
              key={g}
              className="h-full"
              style={{
                width: `${(n / total) * 100}%`,
                backgroundColor: GROUP_COLORS[g] ?? "#777780",
              }}
              title={`${groupLabel(g)}: ${n}`}
            />
          );
        })}
      </div>
      <div className="mt-1.5 flex flex-wrap gap-x-3 gap-y-1">
        {GROUP_ORDER.map((g) => {
          const n = byGroup[g] ?? 0;
          if (n === 0) return null;
          return (
            <span
              key={g}
              className="inline-flex items-center gap-1 text-[11px] text-ink-secondary tabular-nums"
            >
              <span
                className="size-2 rounded-full"
                style={{ backgroundColor: GROUP_COLORS[g] ?? "#777780" }}
                aria-hidden
              />
              {groupLabel(g)}
              <span className="text-ink-muted">{n}</span>
            </span>
          );
        })}
      </div>
    </div>
  );
}

function friendlyError(err: unknown, fallback: string): string {
  if (err instanceof ApiError) {
    if (err.status === 403)
      return "You need member access or higher to do that.";
    if (err.status === 409)
      return "An initiative with that name already exists.";
    if (err.body) return err.body;
  }
  return fallback;
}

export default function InitiativeDetail() {
  const { slug = "", id = "" } = useParams();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const encSlug = encodeURIComponent(slug);
  const base = `/api/v1/workspaces/${encSlug}/initiatives/${encodeURIComponent(id)}`;

  const [editOpen, setEditOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [addOpen, setAddOpen] = useState(false);
  const [removeTarget, setRemoveTarget] =
    useState<InitiativeProjectRollup | null>(null);

  const workspaceQuery = useQuery({
    queryKey: ["workspace", slug],
    queryFn: () =>
      api.get<Workspace>(`/api/v1/workspaces/${encSlug}`),
  });
  const canEdit = (workspaceQuery.data?.role ?? 0) >= 15;

  const initiativeQuery = useQuery({
    queryKey: ["initiative", slug, id],
    queryFn: () => api.get<Initiative>(base),
  });
  const rollupQuery = useQuery({
    queryKey: ["initiative-rollup", slug, id],
    queryFn: () => api.get<InitiativeRollup>(`${base}/rollup`),
  });
  // C76T0: member projects ship their workspace project-label chips
  // in-query (the rollup's per-project slice does not carry them).
  const memberProjectsQuery = useQuery({
    queryKey: ["initiative-projects", slug, id],
    queryFn: () =>
      api
        .get<{ projects: InitiativeProject[] }>(`${base}/projects`)
        .then((d) => d.projects),
  });
  const projectsQuery = useQuery({
    queryKey: ["projects", slug],
    queryFn: () =>
      api.get<{ projects: Project[] }>(`/api/v1/workspaces/${encSlug}/projects`),
    enabled: canEdit,
  });

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: ["initiative", slug, id] });
    void queryClient.invalidateQueries({
      queryKey: ["initiative-rollup", slug, id],
    });
    void queryClient.invalidateQueries({
      queryKey: ["initiative-projects", slug, id],
    });
    void queryClient.invalidateQueries({ queryKey: ["initiatives", slug] });
  };

  const updateMutation = useMutation({
    mutationFn: (patch: {
      name?: string;
      description?: string;
      status?: InitiativeStatus;
    }) => api.patch<Initiative>(base, patch),
    onSuccess: () => {
      invalidate();
      setEditOpen(false);
      toast.add({ title: "Initiative updated", type: "success" });
    },
    onError: (err) =>
      toast.add({
        title: "Couldn't update the initiative",
        description: friendlyError(err, "Please try again."),
        type: "error",
      }),
  });

  const deleteMutation = useMutation({
    mutationFn: () => api.del(base),
    onSuccess: () => {
      toast.add({ title: "Initiative deleted", type: "success" });
      void queryClient.invalidateQueries({
        queryKey: ["initiatives", slug],
      });
      navigate(`/w/${slug}/initiatives`);
    },
    onError: (err) =>
      toast.add({
        title: "Couldn't delete the initiative",
        description: friendlyError(err, "Please try again."),
        type: "error",
      }),
  });

  const addProjectsMutation = useMutation({
    mutationFn: async (projectIds: string[]) => {
      // Backend adds one project per call; it is idempotent, so repeat
      // adds are no-ops.
      for (const projectId of projectIds) {
        await api.post(`${base}/projects`, { project_id: projectId });
      }
    },
    onSuccess: (_, projectIds) => {
      invalidate();
      setAddOpen(false);
      toast.add({
        title:
          projectIds.length === 1
            ? "Project added to the initiative"
            : `${projectIds.length} projects added to the initiative`,
        type: "success",
      });
    },
    onError: (err) =>
      toast.add({
        title: "Couldn't add projects",
        description: friendlyError(err, "Please try again."),
        type: "error",
      }),
  });

  const removeProjectMutation = useMutation({
    mutationFn: (projectId: string) =>
      api.del(`${base}/projects/${encodeURIComponent(projectId)}`),
    onSuccess: () => {
      invalidate();
      setRemoveTarget(null);
      toast.add({ title: "Project removed from the initiative", type: "success" });
    },
    onError: (err) =>
      toast.add({
        title: "Couldn't remove the project",
        description: friendlyError(err, "Please try again."),
        type: "error",
      }),
  });

  const initiative = initiativeQuery.data;
  const rollup = rollupQuery.data;
  const memberIds = useMemo(
    () => new Set((rollup?.projects ?? []).map((p) => p.project_id)),
    [rollup],
  );
  // C76T1: project id → label chips for the member-project rows below.
  const memberLabels = useMemo(() => {
    const m = new Map<string, InitiativeProject["labels"]>();
    for (const p of memberProjectsQuery.data ?? []) {
      m.set(p.project_id, p.labels ?? []);
    }
    return m;
  }, [memberProjectsQuery.data]);
  const addableProjects = useMemo(
    () =>
      (projectsQuery.data?.projects ?? [])
        .filter((p) => !p.archived_at && !memberIds.has(p.id))
        .sort((a, b) => a.name.localeCompare(b.name)),
    [projectsQuery.data, memberIds],
  );

  if (initiativeQuery.status === "pending") {
    return (
      <div className="mx-auto w-full max-w-5xl space-y-4 p-4">
        <Skeleton className="h-8 w-64" />
        <Skeleton className="h-24 w-full" />
        <Skeleton className="h-40 w-full" />
      </div>
    );
  }
  if (initiativeQuery.status === "error" || !initiative) {
    return (
      <div className="mx-auto w-full max-w-5xl p-4">
        <OrgEmpty title="Initiative not found">
          It may have been deleted, or you may not have access to this
          workspace.
        </OrgEmpty>
      </div>
    );
  }

  return (
    <div className="mx-auto w-full max-w-5xl space-y-6 p-4">
      <OrgDetailHeader
        backLabel="Initiatives"
        onBack={() => navigate(`/w/${slug}/initiatives`)}
        title={initiative.name}
        chip={
          canEdit ? (
            <DropdownMenu>
              <DropdownMenuTrigger
                className="rounded-full transition-opacity hover:opacity-80"
                aria-label="Change status"
              >
                <OrgStatusChip
                  color={statusColor(initiative.status)}
                  label={initiative.status}
                />
              </DropdownMenuTrigger>
              <DropdownMenuContent align="start">
                {STATUSES.map((s) => (
                  <DropdownMenuItem
                    key={s}
                    disabled={s === initiative.status || updateMutation.isPending}
                    onClick={() => updateMutation.mutate({ status: s })}
                  >
                    <span
                      className="size-2 rounded-full"
                      style={{ backgroundColor: statusColor(s) }}
                      aria-hidden
                    />
                    {groupLabel(s)}
                  </DropdownMenuItem>
                ))}
              </DropdownMenuContent>
            </DropdownMenu>
          ) : (
            <OrgStatusChip
              color={statusColor(initiative.status)}
              label={initiative.status}
            />
          )
        }
        meta={`${initiative.project_count} project${initiative.project_count === 1 ? "" : "s"}`}
        actions={
          canEdit ? (
            <>
              <OrgHeaderButton
                onClick={() => setEditOpen(true)}
                title="Edit initiative"
              >
                <Pencil className="h-3 w-3" aria-hidden />
                Edit initiative
              </OrgHeaderButton>
              <OrgHeaderButton
                onClick={() => setDeleteOpen(true)}
                title="Delete initiative"
                destructive
              >
                <Trash2 className="h-3 w-3" aria-hidden />
                Delete initiative
              </OrgHeaderButton>
            </>
          ) : undefined
        }
      />

      {initiative.description ? (
        <p className="text-[13px] text-ink-secondary">{initiative.description}</p>
      ) : (
        <p className="text-[13px] text-ink-muted italic">
          No description yet.
        </p>
      )}

      {!canEdit && (
        <p className="text-[11px] text-ink-muted">
          You have guest access to this workspace — this initiative is
          read-only.
        </p>
      )}

      {/* Aggregate progress */}
      <section aria-label="Overall progress">
        <OrgSectionHead title="Progress" />
        <Card className="mt-3">
          <CardContent className="p-4">
            {rollupQuery.status === "pending" ? (
              <Skeleton className="h-16 w-full" />
            ) : rollupQuery.status === "error" || !rollup ? (
              <OrgEmpty title="Couldn't load progress" />
            ) : (
              <div className="space-y-3">
                <InitiativeProgressBar
                  completionPct={rollup.completion_pct}
                  doneCount={rollup.done_count}
                  total={rollup.total}
                />
                <GroupBreakdown
                  byGroup={rollup.by_group}
                  emptyLabel="No issues across member projects yet."
                />
              </div>
            )}
          </CardContent>
        </Card>
      </section>

      {/* Per-project breakdown + management */}
      <section aria-label="Projects">
        <OrgSectionHead
          title="Projects"
          count={rollup?.projects.length ?? initiative.project_count}
          actions={
            canEdit ? (
              <OrgHeaderButton
                onClick={() => setAddOpen(true)}
                title="Add projects"
              >
                <Plus className="h-3 w-3" aria-hidden />
                Add projects
              </OrgHeaderButton>
            ) : undefined
          }
        />
        {rollupQuery.status === "pending" ? (
          <Skeleton className="mt-3 h-32 w-full" />
        ) : rollupQuery.status === "error" || !rollup ? (
          <OrgEmpty title="Couldn't load projects" />
        ) : rollup.projects.length === 0 ? (
          <OrgEmpty title="No projects in this initiative yet">
            {canEdit
              ? "Add projects to start tracking progress across them."
              : "A member can add projects to this initiative."}
          </OrgEmpty>
        ) : (
          <ul className="mt-3 space-y-2">
            {rollup.projects.map((p) => (
              <li key={p.project_id} data-testid="initiative-project-row">
                <Card>
                  <CardContent className="p-3.5">
                    <div className="flex items-center gap-2">
                      <h3 className="text-[13px] font-semibold text-ink">
                        {p.project_name}
                      </h3>
                      {/* C76T1: member-project label chips (small, after
                          the project name). */}
                      <ProjectLabelChips
                        labels={memberLabels.get(p.project_id) ?? []}
                        max={3}
                      />
                      {canEdit && (
                        <button
                          type="button"
                          aria-label={`Remove ${p.project_name} from initiative`}
                          title={`Remove ${p.project_name}`}
                          onClick={() => setRemoveTarget(p)}
                          className="ml-auto rounded p-1 text-ink-muted transition-colors hover:bg-raised hover:text-ink"
                        >
                          <X className="h-3.5 w-3.5" aria-hidden />
                        </button>
                      )}
                    </div>
                    <div className="mt-2">
                      {p.total === 0 ? (
                        <p className="text-[11px] text-ink-muted">
                          <span className="font-semibold text-ink tabular-nums">
                            0%
                          </span>
                          <span aria-hidden> · </span>
                          <span>0 issues</span>
                        </p>
                      ) : (
                        <InitiativeProgressBar
                          completionPct={p.completion_pct}
                          doneCount={p.done_count}
                          total={p.total}
                        />
                      )}
                    </div>
                  </CardContent>
                </Card>
              </li>
            ))}
          </ul>
        )}
      </section>

      {/* Edit dialog */}
      <Dialog open={editOpen} onOpenChange={setEditOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Edit initiative</DialogTitle>
            <DialogDescription>
              Rename the initiative or update its description.
            </DialogDescription>
          </DialogHeader>
          <EditInitiativeForm
            key={initiative.id}
            initiative={initiative}
            pending={updateMutation.isPending}
            onSubmit={(patch) => updateMutation.mutate(patch)}
            onCancel={() => setEditOpen(false)}
          />
        </DialogContent>
      </Dialog>

      {/* Add projects dialog */}
      <Dialog open={addOpen} onOpenChange={setAddOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Add projects</DialogTitle>
            <DialogDescription>
              Select workspace projects to track under this initiative. Adding
              is idempotent — projects already in the initiative are listed
              above, not here.
            </DialogDescription>
          </DialogHeader>
          <AddProjectsForm
            addable={addableProjects}
            loading={projectsQuery.status === "pending"}
            pending={addProjectsMutation.isPending}
            onSubmit={(ids) => addProjectsMutation.mutate(ids)}
            onCancel={() => setAddOpen(false)}
          />
        </DialogContent>
      </Dialog>

      {/* Delete confirm */}
      <AlertDialog open={deleteOpen} onOpenChange={setDeleteOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              Delete “{initiative.name}”?
            </AlertDialogTitle>
            <AlertDialogDescription>
              The initiative and its project memberships are removed. The
              projects and their issues are untouched.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => deleteMutation.mutate()}
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            >
              {deleteMutation.isPending ? "Deleting…" : "Delete"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      {/* Remove project confirm */}
      <AlertDialog
        open={removeTarget !== null}
        onOpenChange={(open) => {
          if (!open) setRemoveTarget(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              Remove {removeTarget ? `“${removeTarget.project_name}”` : "project"}?
            </AlertDialogTitle>
            <AlertDialogDescription>
              The project leaves this initiative. Its issues are untouched.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={() =>
                removeTarget &&
                removeProjectMutation.mutate(removeTarget.project_id)
              }
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            >
              {removeProjectMutation.isPending ? "Removing…" : "Remove project"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

function EditInitiativeForm({
  initiative,
  pending,
  onSubmit,
  onCancel,
}: {
  initiative: Initiative;
  pending: boolean;
  onSubmit: (patch: { name?: string; description?: string }) => void;
  onCancel: () => void;
}) {
  const [name, setName] = useState(initiative.name);
  const [description, setDescription] = useState(initiative.description ?? "");

  const submit = (e: FormEvent) => {
    e.preventDefault();
    const patch: { name?: string; description?: string } = {};
    const trimmed = name.trim();
    if (trimmed && trimmed !== initiative.name) patch.name = trimmed;
    if (description !== (initiative.description ?? ""))
      patch.description = description;
    onSubmit(patch);
  };

  return (
    <form onSubmit={submit} className="space-y-4">
      <div className="space-y-1.5">
        <Label htmlFor="initiative-name">Name</Label>
        <Input
          id="initiative-name"
          value={name}
          onChange={(e) => setName(e.target.value)}
          maxLength={255}
        />
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="initiative-description">Description</Label>
        <Textarea
          id="initiative-description"
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          rows={3}
          placeholder="What is this initiative about?"
        />
      </div>
      <DialogFooter>
        <Button type="button" variant="outline" onClick={onCancel}>
          Cancel
        </Button>
        <Button type="submit" disabled={pending || name.trim().length === 0}>
          {pending ? "Saving…" : "Save changes"}
        </Button>
      </DialogFooter>
    </form>
  );
}

function AddProjectsForm({
  addable,
  loading,
  pending,
  onSubmit,
  onCancel,
}: {
  addable: Project[];
  loading: boolean;
  pending: boolean;
  onSubmit: (projectIds: string[]) => void;
  onCancel: () => void;
}) {
  const [selected, setSelected] = useState<string[]>([]);

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (selected.length > 0) onSubmit(selected);
  };

  if (loading) return <Skeleton className="h-32 w-full" />;
  if (addable.length === 0) {
    return (
      <div className="space-y-4">
        <p className="text-[13px] text-ink-muted">
          Every workspace project is already in this initiative.
        </p>
        <DialogFooter>
          <Button type="button" variant="outline" onClick={onCancel}>
            Close
          </Button>
        </DialogFooter>
      </div>
    );
  }

  return (
    <form onSubmit={submit} className="space-y-4">
      <div className="space-y-1.5">
        <Label htmlFor="add-projects-select">Projects to add</Label>
        {/*
          C27T2: no shared multi-select component existed in the codebase at
          implementation time (C27T1's branch had not merged), so this follows
          the existing accessible pattern from AutomationPickers: a native
          multi-select. Swap to T1's component here when it lands.
        */}
        <select
          id="add-projects-select"
          multiple
          size={Math.min(8, Math.max(3, addable.length))}
          className="w-full rounded-md border border-input bg-background px-2.5 py-2 text-[13px] text-ink"
          value={selected}
          onChange={(e) =>
            setSelected(
              Array.from(e.target.selectedOptions).map((o) => o.value),
            )
          }
        >
          {addable.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name} ({p.identifier})
            </option>
          ))}
        </select>
        <p className="text-xs text-muted-foreground">
          Hold Ctrl/Cmd to select several projects.
        </p>
      </div>
      <DialogFooter>
        <Button type="button" variant="outline" onClick={onCancel}>
          Cancel
        </Button>
        <Button type="submit" disabled={pending || selected.length === 0}>
          {pending ? "Adding…" : "Add"}
        </Button>
      </DialogFooter>
    </form>
  );
}
