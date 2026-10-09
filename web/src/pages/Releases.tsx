import { useMemo, useState, type FormEvent } from "react";
import { Link, useParams } from "react-router-dom";
import {
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { Pencil, Plus, Rocket, Trash2, X } from "lucide-react";
import { api, ApiError } from "../lib/api";
import type {
  Issue,
  IssueState,
  Project,
  Release,
} from "../lib/types";
import ProjectNav from "../components/project/ProjectNav";
import ThemeToggle from "../components/ThemeToggle";
import NotificationBell from "../components/notifications/NotificationBell";
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
import { Textarea } from "../components/ui/textarea";
import {
  NativeSelect,
  NativeSelectOption,
} from "../components/ui/native-select";
import { Alert, AlertDescription } from "../components/ui/alert";

/** State groups in display order (mirrors Modules.tsx). */
const STATE_GROUPS = [
  "triage",
  "backlog",
  "unstarted",
  "started",
  "completed",
  "cancelled",
] as const;

const RELEASE_STATUSES = ["planned", "released"] as const;

function statusVariant(status: string): "default" | "secondary" | "outline" {
  switch (status) {
    case "released":
      return "default";
    case "planned":
      return "secondary";
    default:
      return "outline";
  }
}

function fmtDate(d?: string | null): string {
  if (!d) return "No release date";
  return d.slice(0, 10);
}

/** Progress counts from issues + states (same formula as Modules.tsx). */
export function progressCounts(issues: Issue[], states: IssueState[]) {
  const groupOf = new Map(states.map((s) => [s.id, s.group]));
  let open = 0;
  let inProgress = 0;
  let done = 0;
  for (const i of issues) {
    const g = groupOf.get(i.state_id);
    if (g === "started") inProgress++;
    else if (g === "completed" || g === "cancelled") done++;
    else if (g === "triage" || g === "backlog" || g === "unstarted") open++;
  }
  return { open, inProgress, done };
}

/** Simple done-% bar; colors use semantic theme classes (dark-theme aware). */
export function DoneBar({ done, total }: { done: number; total: number }) {
  const pct = total === 0 ? 0 : Math.round((done / total) * 100);
  return (
    <div className="flex items-center gap-2">
      <div
        className="h-2 w-full overflow-hidden rounded-full bg-muted"
        role="progressbar"
        aria-valuenow={pct}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-label={`${pct}% done`}
      >
        <div
          className="h-full rounded-full bg-primary transition-all"
          style={{ width: `${pct}%` }}
        />
      </div>
      <span className="shrink-0 text-xs text-muted-foreground">{pct}%</span>
    </div>
  );
}

interface ReleaseFormValues {
  name: string;
  description: string;
  status: string;
  releaseDate: string; // YYYY-MM-DD or ""
}

function emptyForm(): ReleaseFormValues {
  return { name: "", description: "", status: "planned", releaseDate: "" };
}

function formFromRelease(r: Release): ReleaseFormValues {
  return {
    name: r.name,
    description: r.description ?? "",
    status: r.status,
    releaseDate: r.release_date?.slice(0, 10) ?? "",
  };
}

export default function Releases() {
  const { slug = "", identifier = "" } = useParams<{
    slug: string;
    identifier: string;
  }>();
  const queryClient = useQueryClient();
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [formOpen, setFormOpen] = useState(false);
  const [editing, setEditing] = useState<Release | null>(null);
  const [addOpen, setAddOpen] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<Release | null>(null);
  /** When the plain delete hits 409, the dialog escalates to reassign/force. */
  const [deleteConflict, setDeleteConflict] = useState(false);
  const [reassignTo, setReassignTo] = useState("");
  const [formError, setFormError] = useState<string | null>(null);
  const [picked, setPicked] = useState<Set<string>>(new Set());

  const base = `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`;
  const releasesKey = ["releases", slug, identifier] as const;

  const projectQuery = useQuery({
    queryKey: ["project", slug, identifier],
    queryFn: () => api.get<Project>(base),
  });
  const statesQuery = useQuery({
    queryKey: ["states", slug, identifier],
    queryFn: () =>
      api.get<{ states: IssueState[] }>(`${base}/states`).then((d) => d.states),
  });
  const releasesQuery = useQuery({
    queryKey: releasesKey,
    queryFn: () =>
      api
        .get<{ releases: Release[] }>(`${base}/releases`)
        .then((d) => d.releases),
  });

  const selected = useMemo(
    () => (releasesQuery.data ?? []).find((r) => r.id === selectedId) ?? null,
    [releasesQuery.data, selectedId],
  );

  const releaseIssuesQuery = useQuery({
    queryKey: ["release-issues", slug, identifier, selectedId],
    enabled: selectedId !== null,
    queryFn: () =>
      api
        .get<{ issues: Issue[] }>(`${base}/releases/${selectedId}/issues`)
        .then((d) => d.issues),
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
        const page = await api.get<{ results: Issue[]; next_cursor?: string }>(
          `${base}/issues?${p.toString()}`,
        );
        all.push(...page.results);
        cursor = page.next_cursor;
      } while (cursor);
      return all;
    },
  });

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: releasesKey });
    void queryClient.invalidateQueries({
      queryKey: ["release-issues", slug, identifier, selectedId],
    });
  };

  const errMsg = (e: unknown, fallback: string) =>
    e instanceof ApiError ? e.message : fallback;

  const createMutation = useMutation({
    mutationFn: (v: ReleaseFormValues) =>
      api.post<Release>(`${base}/releases`, {
        name: v.name.trim(),
        ...(v.description.trim() ? { description: v.description.trim() } : {}),
        status: v.status,
        ...(v.releaseDate ? { release_date: v.releaseDate } : {}),
      }),
    onSuccess: (r) => {
      setFormOpen(false);
      setFormError(null);
      setSelectedId(r.id);
      invalidate();
    },
    onError: (e) => setFormError(errMsg(e, "Failed to create release")),
  });

  const updateMutation = useMutation({
    mutationFn: ({ id, patch }: { id: string; patch: Record<string, string> }) =>
      api.patch<Release>(`${base}/releases/${id}`, patch),
    onSuccess: () => {
      setFormOpen(false);
      setEditing(null);
      setFormError(null);
      invalidate();
    },
    onError: (e) => setFormError(errMsg(e, "Failed to update release")),
  });

  /** markReleased is a thin PATCH — the full edit path lives in the form. */
  const markReleasedMutation = useMutation({
    mutationFn: (r: Release) =>
      api.patch<Release>(`${base}/releases/${r.id}`, { status: "released" }),
    onSuccess: invalidate,
    onError: (e) => setFormError(errMsg(e, "Failed to mark release")),
  });

  const doDelete = (r: Release, query: string) =>
    api.del(`${base}/releases/${r.id}${query}`);

  const deleteMutation = useMutation({
    mutationFn: (r: Release) => doDelete(r, ""),
    onSuccess: () => {
      setDeleteTarget(null);
      setDeleteConflict(false);
      setReassignTo("");
      setSelectedId(null);
      invalidate();
    },
    onError: (e) => {
      if (e instanceof ApiError && e.status === 409) {
        // Release still has issues — escalate the dialog to reassign/force.
        setDeleteConflict(true);
        setFormError(null);
      } else {
        setFormError(errMsg(e, "Failed to delete release"));
      }
    },
  });

  const deleteWithOptionMutation = useMutation({
    mutationFn: (r: Release) =>
      doDelete(r, reassignTo ? `?reassign=${reassignTo}` : "?force=true"),
    onSuccess: () => {
      setDeleteTarget(null);
      setDeleteConflict(false);
      setReassignTo("");
      setSelectedId(null);
      invalidate();
    },
    onError: (e) => setFormError(errMsg(e, "Failed to delete release")),
  });

  const addIssuesMutation = useMutation({
    mutationFn: (ids: string[]) =>
      api.post(`${base}/releases/${selectedId}/issues`, { issue_ids: ids }),
    onSuccess: () => {
      setAddOpen(false);
      setPicked(new Set());
      invalidate();
    },
    onError: (e) => setFormError(errMsg(e, "Failed to add issues")),
  });

  const removeIssueMutation = useMutation({
    mutationFn: (issueId: string) =>
      api.del(`${base}/releases/${selectedId}/issues`, {
        issue_ids: [issueId],
      }),
    onSuccess: invalidate,
    onError: (e) => setFormError(errMsg(e, "Failed to remove issue")),
  });

  function openCreate() {
    setEditing(null);
    setFormError(null);
    setFormOpen(true);
  }

  function openEdit(r: Release) {
    setEditing(r);
    setFormError(null);
    setFormOpen(true);
  }

  function submitForm(v: ReleaseFormValues) {
    setFormError(null);
    if (editing) {
      const initial = formFromRelease(editing);
      const patch: Record<string, string> = {};
      if (v.name.trim() !== initial.name) patch.name = v.name.trim();
      if (v.description !== initial.description)
        patch.description = v.description; // "" clears (backend tri-state)
      if (v.status !== initial.status) patch.status = v.status;
      if (v.releaseDate !== initial.releaseDate)
        patch.release_date = v.releaseDate; // "" clears
      if (Object.keys(patch).length === 0) {
        setFormError("No changes to save.");
        return;
      }
      updateMutation.mutate({ id: editing.id, patch });
    } else {
      createMutation.mutate(v);
    }
  }

  function openDelete(r: Release) {
    setDeleteConflict(false);
    setReassignTo("");
    setFormError(null);
    setDeleteTarget(r);
  }

  function togglePicked(id: string) {
    setPicked((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  const states = statesQuery.data ?? [];
  const releases = releasesQuery.data ?? [];
  const inRelease = useMemo(
    () => new Set((releaseIssuesQuery.data ?? []).map((i) => i.id)),
    [releaseIssuesQuery.data],
  );
  const reassignCandidates = useMemo(
    () => releases.filter((r) => r.id !== deleteTarget?.id),
    [releases, deleteTarget],
  );

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
          <div className="flex items-center gap-2">
            <NotificationBell />
            <ThemeToggle />
            <Button onClick={openCreate} className="gap-2">
              <Plus className="h-4 w-4" />
              New release
            </Button>
          </div>
        </div>
      </div>
      <ProjectNav />

      {releasesQuery.isError && (
        <Alert variant="destructive" className="mt-4">
          <AlertDescription>
            {releasesQuery.error instanceof ApiError
              ? releasesQuery.error.message
              : "Failed to load releases"}
          </AlertDescription>
        </Alert>
      )}
      {formError && (
        <Alert variant="destructive" className="mt-4">
          <AlertDescription>{formError}</AlertDescription>
        </Alert>
      )}

      <div className="mt-4">
        {releasesQuery.isPending ? (
          <div className="space-y-3">
            <Skeleton className="h-28 w-full" />
            <Skeleton className="h-28 w-full" />
          </div>
        ) : selected ? (
          <ReleaseDetail
            release={selected}
            states={states}
            issues={releaseIssuesQuery.data ?? []}
            issuesLoading={releaseIssuesQuery.isPending}
            onBack={() => setSelectedId(null)}
            onEdit={() => openEdit(selected)}
            onDelete={() => openDelete(selected)}
            onAdd={() => {
              setPicked(new Set());
              setFormError(null);
              setAddOpen(true);
            }}
            onRemove={(id) => removeIssueMutation.mutate(id)}
            onMarkReleased={() => markReleasedMutation.mutate(selected)}
            markReleasedPending={markReleasedMutation.isPending}
            slug={slug}
            identifier={identifier}
          />
        ) : releases.length === 0 ? (
          <Card>
            <CardHeader>
              <CardTitle>No releases yet</CardTitle>
            </CardHeader>
            <CardContent>
              <p className="text-sm text-muted-foreground">
                Plan your project's milestones — mark what ships in each
                release, then track progress as issues close.
              </p>
            </CardContent>
          </Card>
        ) : (
          <div className="space-y-3">
            {releases.map((r) => (
              <ActionCard
                key={r.id}
                role="button"
                label={`Select release ${r.name}`}
                aria-pressed={selectedId === r.id}
                onActivate={() => setSelectedId(r.id)}
              >
                <CardHeader className="space-y-3">
                  <div className="flex items-center gap-2">
                    <CardTitle className="text-base font-medium">
                      {r.name}
                    </CardTitle>
                    <Badge variant={statusVariant(r.status)} className="ml-auto">
                      {r.status}
                    </Badge>
                  </div>
                  <p className="text-xs text-muted-foreground">
                    {fmtDate(r.release_date)} · {r.issue_count} issue
                    {r.issue_count === 1 ? "" : "s"}
                  </p>
                </CardHeader>
              </ActionCard>
            ))}
          </div>
        )}
      </div>

      <ReleaseFormDialog
        key={`${editing?.id ?? "new"}-${formOpen ? "open" : "closed"}`}
        open={formOpen}
        onOpenChange={setFormOpen}
        initial={editing ? formFromRelease(editing) : emptyForm()}
        pending={createMutation.isPending || updateMutation.isPending}
        error={formError}
        onSubmit={submitForm}
        title={editing ? "Edit release" : "New release"}
        submitLabel={editing ? "Save changes" : "Create release"}
      />

      {/* Add-issues dialog */}
      <Dialog open={addOpen} onOpenChange={setAddOpen}>
        <DialogContent className="max-h-[80vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>Add issues to {selected?.name}</DialogTitle>
            <DialogDescription>
              Select issues to include in this release. Issues already in the
              release are checked and can't be picked again.
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
                const already = inRelease.has(i.id);
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

      {/* Delete confirmation — escalates on 409 to reassign / force-detach */}
      <Dialog
        open={deleteTarget !== null}
        onOpenChange={(o) => {
          if (!o) {
            setDeleteTarget(null);
            setDeleteConflict(false);
            setReassignTo("");
          }
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete release "{deleteTarget?.name}"?</DialogTitle>
            <DialogDescription>
              {deleteConflict ? (
                <>
                  This release still has {deleteTarget?.issue_count} issue
                  {deleteTarget?.issue_count === 1 ? "" : "s"}. Move them to
                  another release, or detach them (they'll be kept, just
                  unassigned).
                </>
              ) : deleteTarget && deleteTarget.issue_count > 0 ? (
                <>
                  This release has {deleteTarget.issue_count} issue
                  {deleteTarget.issue_count === 1 ? "" : "s"}.
                </>
              ) : (
                "This can't be undone."
              )}
            </DialogDescription>
          </DialogHeader>
          {deleteConflict && (
            <div className="space-y-2">
              <Label htmlFor="r-reassign">Move issues to another release</Label>
              <NativeSelect
                id="r-reassign"
                value={reassignTo}
                onChange={(e) => setReassignTo(e.target.value)}
              >
                <NativeSelectOption value="">
                  — or detach them (force) —
                </NativeSelectOption>
                {reassignCandidates.map((r) => (
                  <NativeSelectOption key={r.id} value={r.id}>
                    {r.name}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </div>
          )}
          {formError && (
            <Alert variant="destructive">
              <AlertDescription>{formError}</AlertDescription>
            </Alert>
          )}
          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => {
                setDeleteTarget(null);
                setDeleteConflict(false);
                setReassignTo("");
              }}
            >
              Cancel
            </Button>
            {deleteConflict ? (
              <Button
                variant="destructive"
                disabled={deleteWithOptionMutation.isPending}
                onClick={() =>
                  deleteTarget &&
                  deleteWithOptionMutation.mutate(deleteTarget)
                }
              >
                {deleteWithOptionMutation.isPending
                  ? "Deleting…"
                  : reassignTo
                    ? "Move issues & delete"
                    : "Detach issues & delete"}
              </Button>
            ) : (
              <Button
                variant="destructive"
                disabled={deleteMutation.isPending}
                onClick={() => deleteTarget && deleteMutation.mutate(deleteTarget)}
              >
                {deleteMutation.isPending ? "Deleting…" : "Delete release"}
              </Button>
            )}
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

function ReleaseDetail({
  release,
  states,
  issues,
  issuesLoading,
  onBack,
  onEdit,
  onDelete,
  onAdd,
  onRemove,
  onMarkReleased,
  markReleasedPending,
  slug,
  identifier,
}: {
  release: Release;
  states: IssueState[];
  issues: Issue[];
  issuesLoading: boolean;
  onBack: () => void;
  onEdit: () => void;
  onDelete: () => void;
  onAdd: () => void;
  onRemove: (issueId: string) => void;
  onMarkReleased: () => void;
  markReleasedPending: boolean;
  slug: string;
  identifier: string;
}) {
  const counts = progressCounts(issues, states);
  const total = counts.open + counts.inProgress + counts.done;

  const grouped = useMemo(() => {
    const groupOf = new Map(states.map((s) => [s.id, s.group]));
    const buckets = new Map<string, Issue[]>();
    for (const i of issues) {
      const g = groupOf.get(i.state_id) ?? "unstarted";
      const arr = buckets.get(g) ?? [];
      arr.push(i);
      buckets.set(g, arr);
    }
    return STATE_GROUPS.map((g) => ({
      group: g,
      label: g.charAt(0).toUpperCase() + g.slice(1),
      issues: buckets.get(g) ?? [],
    })).filter((b) => b.issues.length > 0);
  }, [issues, states]);

  return (
    <div className="space-y-4">
      <Button variant="outline" size="sm" onClick={onBack}>
        ← All releases
      </Button>
      <Card>
        <CardHeader className="space-y-3">
          <div className="flex items-center gap-2">
            <CardTitle className="text-lg">{release.name}</CardTitle>
            <Badge variant={statusVariant(release.status)} className="ml-auto">
              {release.status}
            </Badge>
          </div>
          {release.description && (
            <p className="text-sm text-muted-foreground">
              {release.description}
            </p>
          )}
          <p className="text-xs text-muted-foreground">
            {fmtDate(release.release_date)} · {release.issue_count} issue
            {release.issue_count === 1 ? "" : "s"}
          </p>
          {total > 0 && <DoneBar done={counts.done} total={total} />}
          <div className="flex flex-wrap items-center gap-2 pt-1">
            {release.status === "planned" && (
              <Button
                size="sm"
                onClick={onMarkReleased}
                disabled={markReleasedPending}
                className="gap-2"
              >
                <Rocket className="h-4 w-4" />
                {markReleasedPending ? "Marking…" : "Mark released"}
              </Button>
            )}
            <Button size="sm" variant="outline" onClick={onEdit} className="gap-2">
              <Pencil className="h-4 w-4" />
              Edit
            </Button>
            <Button
              size="sm"
              variant="outline"
              onClick={onDelete}
              className="gap-2 text-destructive"
            >
              <Trash2 className="h-4 w-4" />
              Delete
            </Button>
          </div>
        </CardHeader>
      </Card>

      <div className="flex items-center justify-between">
        <h2 className="text-sm font-medium">Issues in this release</h2>
        <Button size="sm" variant="outline" onClick={onAdd} className="gap-2">
          <Plus className="h-4 w-4" />
          Add issues
        </Button>
      </div>

      {issuesLoading ? (
        <div className="space-y-2">
          <Skeleton className="h-16 w-full" />
          <Skeleton className="h-16 w-full" />
        </div>
      ) : issues.length === 0 ? (
        <Card>
          <CardContent className="pt-6">
            <p className="text-sm text-muted-foreground">
              No issues in this release yet — add some to start tracking
              progress.
            </p>
          </CardContent>
        </Card>
      ) : (
        <div className="space-y-4">
          {grouped.map((b) => (
            <div key={b.group}>
              <h3 className="mb-2 text-xs font-medium uppercase tracking-wide text-muted-foreground">
                {b.label} · {b.issues.length}
              </h3>
              <div className="space-y-2">
                {b.issues.map((i) => (
                  <Card key={i.id}>
                    <CardContent className="flex items-center gap-3 py-3">
                      <Badge
                        variant="outline"
                        className="shrink-0 font-mono text-[11px]"
                      >
                        {i.display_id}
                      </Badge>
                      <Link
                        to={`/w/${slug}/p/${identifier}/i/${i.id}`}
                        className="truncate text-sm hover:underline"
                      >
                        {i.name}
                      </Link>
                      <Button
                        size="sm"
                        variant="ghost"
                        className="ml-auto shrink-0"
                        onClick={() => onRemove(i.id)}
                        aria-label={`Remove ${i.display_id} from release`}
                      >
                        <X className="h-4 w-4" />
                      </Button>
                    </CardContent>
                  </Card>
                ))}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function ReleaseFormDialog({
  open,
  onOpenChange,
  initial,
  pending,
  error,
  onSubmit,
  title,
  submitLabel,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  initial: ReleaseFormValues;
  pending: boolean;
  error: string | null;
  onSubmit: (v: ReleaseFormValues) => void;
  title: string;
  submitLabel: string;
}) {
  const [values, setValues] = useState<ReleaseFormValues>(initial);
  // The parent passes key={editing?.id ?? "new"} so this dialog remounts
  // fresh for each create/edit session — no reset-on-render needed.

  function onForm(e: FormEvent) {
    e.preventDefault();
    onSubmit(values);
  }

  const set = (k: keyof ReleaseFormValues) => (v: string) =>
    setValues((prev) => ({ ...prev, [k]: v }));

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>
            A release is a project milestone — the set of issues that ship
            together.
          </DialogDescription>
        </DialogHeader>
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        <form onSubmit={onForm} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="r-name">Name</Label>
            <Input
              id="r-name"
              value={values.name}
              onChange={(e) => set("name")(e.target.value)}
              placeholder="v2.4.0"
              required
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="r-desc">Description</Label>
            <Textarea
              id="r-desc"
              value={values.description}
              onChange={(e) => set("description")(e.target.value)}
              placeholder="What's shipping in this release…"
              rows={3}
            />
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-2">
              <Label htmlFor="r-status">Status</Label>
              <NativeSelect
                id="r-status"
                value={values.status}
                onChange={(e) => set("status")(e.target.value)}
              >
                {RELEASE_STATUSES.map((s) => (
                  <NativeSelectOption key={s} value={s}>
                    {s}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </div>
            <div className="space-y-2">
              <Label htmlFor="r-date">Release date</Label>
              <Input
                id="r-date"
                type="date"
                value={values.releaseDate}
                onChange={(e) => set("releaseDate")(e.target.value)}
              />
            </div>
          </div>
          <DialogFooter>
            <Button type="submit" disabled={pending}>
              {pending ? "Saving…" : submitLabel}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
