import { useMemo, useState, type FormEvent } from "react";
import { Link, useParams } from "react-router-dom";
import {
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { Pencil, Plus, X } from "lucide-react";
import { api, ApiError } from "../lib/api";
import type {
  Issue,
  IssueState,
  Member,
  Module,
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
import { Textarea } from "../components/ui/textarea";
import {
  NativeSelect,
  NativeSelectOption,
} from "../components/ui/native-select";
import { Alert, AlertDescription } from "../components/ui/alert";

/** State groups in display order (mirrors Cycles.tsx). */
const STATE_GROUPS = [
  "triage",
  "backlog",
  "unstarted",
  "started",
  "completed",
  "cancelled",
] as const;

const MODULE_STATUSES = ["active", "completed", "archived"] as const;

function statusVariant(status: string): "default" | "secondary" | "outline" {
  switch (status) {
    case "active":
      return "default";
    case "completed":
      return "secondary";
    default:
      return "outline";
  }
}

function dateRange(m: Module): string {
  const s = m.start_date?.slice(0, 10);
  const t = m.target_date?.slice(0, 10);
  if (s && t) return `${s} → ${t}`;
  if (s) return `from ${s}`;
  if (t) return `until ${t}`;
  return "No dates set";
}

/** Progress counts from issues + states (same formula as Cycles.tsx). */
function progressCounts(issues: Issue[], states: IssueState[]) {
  const groupOf = new Map(states.map((s) => [s.id, s.group]));
  let open = 0;
  let inProgress = 0;
  let done = 0;
  for (const i of issues) {
    const g = groupOf.get(i.state_id);
    if (g === "started") inProgress++;
    else if (g === "completed") done++;
    else if (g === "triage" || g === "backlog" || g === "unstarted") open++;
  }
  return { open, inProgress, done };
}

interface ModuleFormValues {
  name: string;
  description: string;
  status: string;
  startDate: string;
  targetDate: string;
  leadId: string; // "" = none
}

function emptyForm(): ModuleFormValues {
  return {
    name: "",
    description: "",
    status: "active",
    startDate: "",
    targetDate: "",
    leadId: "",
  };
}

function formFromModule(m: Module): ModuleFormValues {
  return {
    name: m.name,
    description: m.description ?? "",
    status: m.status,
    startDate: m.start_date?.slice(0, 10) ?? "",
    targetDate: m.target_date?.slice(0, 10) ?? "",
    leadId: m.lead_id ?? "",
  };
}

export default function Modules() {
  const { slug = "", identifier = "" } = useParams<{
    slug: string;
    identifier: string;
  }>();
  const queryClient = useQueryClient();
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [formOpen, setFormOpen] = useState(false);
  const [editing, setEditing] = useState<Module | null>(null);
  const [addOpen, setAddOpen] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<Module | null>(null);
  const [formError, setFormError] = useState<string | null>(null);
  const [picked, setPicked] = useState<Set<string>>(new Set());

  const base = `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`;
  const modulesKey = ["modules", slug, identifier] as const;

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
  const modulesQuery = useQuery({
    queryKey: modulesKey,
    queryFn: () =>
      api.get<{ modules: Module[] }>(`${base}/modules`).then((d) => d.modules),
  });

  const selected = useMemo(
    () => (modulesQuery.data ?? []).find((m) => m.id === selectedId) ?? null,
    [modulesQuery.data, selectedId],
  );

  const moduleIssuesQuery = useQuery({
    queryKey: ["module-issues", slug, identifier, selectedId],
    enabled: selectedId !== null,
    queryFn: () =>
      api
        .get<{ issues: Issue[] }>(`${base}/modules/${selectedId}/issues`)
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
    void queryClient.invalidateQueries({ queryKey: modulesKey });
    void queryClient.invalidateQueries({
      queryKey: ["module-issues", slug, identifier, selectedId],
    });
  };

  const errMsg = (e: unknown, fallback: string) =>
    e instanceof ApiError ? e.message : fallback;

  const createMutation = useMutation({
    mutationFn: (v: ModuleFormValues) =>
      api.post<Module>(`${base}/modules`, {
        name: v.name.trim(),
        ...(v.description.trim() ? { description: v.description.trim() } : {}),
        status: v.status,
        ...(v.startDate ? { start_date: v.startDate } : {}),
        ...(v.targetDate ? { target_date: v.targetDate } : {}),
        ...(v.leadId ? { lead_id: v.leadId } : {}),
      }),
    onSuccess: (m) => {
      setFormOpen(false);
      setFormError(null);
      setSelectedId(m.id);
      invalidate();
    },
    onError: (e) => setFormError(errMsg(e, "Failed to create module")),
  });

  const updateMutation = useMutation({
    mutationFn: ({ id, patch }: { id: string; patch: Record<string, string> }) =>
      api.patch<Module>(`${base}/modules/${id}`, patch),
    onSuccess: () => {
      setFormOpen(false);
      setEditing(null);
      setFormError(null);
      invalidate();
    },
    onError: (e) => setFormError(errMsg(e, "Failed to update module")),
  });

  const deleteMutation = useMutation({
    mutationFn: (m: Module) =>
      api.del(
        `${base}/modules/${m.id}${m.issue_count > 0 ? "?force=true" : ""}`,
      ),
    onSuccess: () => {
      setDeleteTarget(null);
      setSelectedId(null);
      invalidate();
    },
    onError: (e) => setFormError(errMsg(e, "Failed to delete module")),
  });

  const addIssuesMutation = useMutation({
    mutationFn: (ids: string[]) =>
      api.post(`${base}/modules/${selectedId}/issues`, { issue_ids: ids }),
    onSuccess: () => {
      setAddOpen(false);
      setPicked(new Set());
      invalidate();
    },
    onError: (e) => setFormError(errMsg(e, "Failed to add issues")),
  });

  const removeIssueMutation = useMutation({
    mutationFn: (issueId: string) =>
      api.del(`${base}/modules/${selectedId}/issues`, {
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

  function openEdit(m: Module) {
    setEditing(m);
    setFormError(null);
    setFormOpen(true);
  }

  function submitForm(v: ModuleFormValues) {
    setFormError(null);
    if (editing) {
      const initial = formFromModule(editing);
      const patch: Record<string, string> = {};
      if (v.name.trim() !== initial.name) patch.name = v.name.trim();
      if (v.description !== initial.description)
        patch.description = v.description; // "" clears (backend tri-state)
      if (v.status !== initial.status) patch.status = v.status;
      if (v.startDate !== initial.startDate) patch.start_date = v.startDate;
      if (v.targetDate !== initial.targetDate) patch.target_date = v.targetDate;
      if (v.leadId !== initial.leadId) patch.lead_id = v.leadId; // "" clears
      updateMutation.mutate({ id: editing.id, patch });
    } else {
      createMutation.mutate(v);
    }
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
  const modules = modulesQuery.data ?? [];
  const members = membersQuery.data ?? [];
  const inModule = useMemo(
    () => new Set((moduleIssuesQuery.data ?? []).map((i) => i.id)),
    [moduleIssuesQuery.data],
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
            <Button onClick={openCreate} className="gap-2">
              <Plus className="h-4 w-4" />
              New module
            </Button>
          </div>
        </div>
      </div>
      <ProjectNav />

      {modulesQuery.isError && (
        <Alert variant="destructive" className="mt-4">
          <AlertDescription>
            {modulesQuery.error instanceof ApiError
              ? modulesQuery.error.message
              : "Failed to load modules"}
          </AlertDescription>
        </Alert>
      )}
      {formError && (
        <Alert variant="destructive" className="mt-4">
          <AlertDescription>{formError}</AlertDescription>
        </Alert>
      )}

      <div className="mt-4">
        {modulesQuery.isPending ? (
          <div className="space-y-3">
            <Skeleton className="h-28 w-full" />
            <Skeleton className="h-28 w-full" />
          </div>
        ) : selected ? (
          <ModuleDetail
            module={selected}
            states={states}
            issues={moduleIssuesQuery.data ?? []}
            issuesLoading={moduleIssuesQuery.isPending}
            members={members}
            onBack={() => setSelectedId(null)}
            onEdit={() => openEdit(selected)}
            onDelete={() => setDeleteTarget(selected)}
            onAdd={() => {
              setPicked(new Set());
              setFormError(null);
              setAddOpen(true);
            }}
            onRemove={(id) => removeIssueMutation.mutate(id)}
            slug={slug}
            identifier={identifier}
          />
        ) : modules.length === 0 ? (
          <Card>
            <CardHeader>
              <CardTitle>No modules yet</CardTitle>
            </CardHeader>
            <CardContent>
              <p className="text-sm text-muted-foreground">
                Group this project's work into modules — epics, components, or
                whatever slices make sense.
              </p>
            </CardContent>
          </Card>
        ) : (
          <div className="space-y-3">
            {modules.map((m) => (
              <ActionCard
                key={m.id}
                role="button"
                label={`Select module ${m.name}`}
                aria-pressed={selectedId === m.id}
                onActivate={() => setSelectedId(m.id)}
              >
                <CardHeader className="space-y-3">
                  <div className="flex items-center gap-2">
                    <CardTitle className="text-base font-medium">
                      {m.name}
                    </CardTitle>
                    <Badge variant={statusVariant(m.status)} className="ml-auto">
                      {m.status}
                    </Badge>
                  </div>
                  <p className="text-xs text-muted-foreground">
                    {dateRange(m)} · {m.issue_count} issue
                    {m.issue_count === 1 ? "" : "s"}
                  </p>
                </CardHeader>
              </ActionCard>
            ))}
          </div>
        )}
      </div>

      <ModuleFormDialog
        key={`${editing?.id ?? "new"}-${formOpen ? "open" : "closed"}`}
        open={formOpen}
        onOpenChange={setFormOpen}
        initial={editing ? formFromModule(editing) : emptyForm()}
        members={members}
        pending={createMutation.isPending || updateMutation.isPending}
        error={formError}
        onSubmit={submitForm}
        title={editing ? "Edit module" : "New module"}
        submitLabel={editing ? "Save changes" : "Create module"}
      />

      {/* Add-issues dialog */}
      <Dialog open={addOpen} onOpenChange={setAddOpen}>
        <DialogContent className="max-h-[80vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>Add issues to {selected?.name}</DialogTitle>
            <DialogDescription>
              Select issues to include in this module. Issues already in the
              module are checked and can't be picked again.
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
                const already = inModule.has(i.id);
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

      {/* Delete confirmation */}
      <Dialog
        open={deleteTarget !== null}
        onOpenChange={(o) => !o && setDeleteTarget(null)}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete module "{deleteTarget?.name}"?</DialogTitle>
            <DialogDescription>
              {deleteTarget && deleteTarget.issue_count > 0 ? (
                <>
                  This module has {deleteTarget.issue_count} issue
                  {deleteTarget.issue_count === 1 ? "" : "s"}. They will be
                  kept but removed from the module.
                </>
              ) : (
                "This can't be undone."
              )}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDeleteTarget(null)}>
              Cancel
            </Button>
            <Button
              variant="destructive"
              disabled={deleteMutation.isPending}
              onClick={() => deleteTarget && deleteMutation.mutate(deleteTarget)}
            >
              {deleteMutation.isPending ? "Deleting…" : "Delete module"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

function ModuleFormDialog({
  open,
  onOpenChange,
  initial,
  members,
  pending,
  error,
  onSubmit,
  title,
  submitLabel,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  initial: ModuleFormValues;
  members: Member[];
  pending: boolean;
  error: string | null;
  onSubmit: (v: ModuleFormValues) => void;
  title: string;
  submitLabel: string;
}) {
  const [values, setValues] = useState<ModuleFormValues>(initial);
  // The parent passes key={editing?.id ?? "new"} so this dialog remounts
  // fresh for each create/edit session — no reset-on-render needed.

  function onForm(e: FormEvent) {
    e.preventDefault();
    onSubmit(values);
  }

  const set = (k: keyof ModuleFormValues) => (v: string) =>
    setValues((prev) => ({ ...prev, [k]: v }));

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>
            A module groups related issues — an epic, a component, a release
            train.
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={onForm} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="m-name">Name</Label>
            <Input
              id="m-name"
              value={values.name}
              onChange={(e) => set("name")(e.target.value)}
              placeholder="Authentication overhaul"
              required
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="m-desc">Description</Label>
            <Textarea
              id="m-desc"
              value={values.description}
              onChange={(e) => set("description")(e.target.value)}
              placeholder="What belongs in this module…"
              rows={3}
            />
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-2">
              <Label htmlFor="m-status">Status</Label>
              <NativeSelect
                id="m-status"
                value={values.status}
                onChange={(e) => set("status")(e.target.value)}
              >
                {MODULE_STATUSES.map((s) => (
                  <NativeSelectOption key={s} value={s}>
                    {s}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </div>
            <div className="space-y-2">
              <Label htmlFor="m-lead">Lead</Label>
              <NativeSelect
                id="m-lead"
                value={values.leadId}
                onChange={(e) => set("leadId")(e.target.value)}
              >
                <NativeSelectOption value="">No lead</NativeSelectOption>
                {members.map((m) => (
                  <NativeSelectOption key={m.id} value={m.id}>
                    {m.name ?? m.email}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </div>
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-2">
              <Label htmlFor="m-start">Start date</Label>
              <Input
                id="m-start"
                type="date"
                value={values.startDate}
                onChange={(e) => set("startDate")(e.target.value)}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="m-target">Target date</Label>
              <Input
                id="m-target"
                type="date"
                value={values.targetDate}
                onChange={(e) => set("targetDate")(e.target.value)}
              />
            </div>
          </div>
          {error && (
            <Alert variant="destructive">
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}
          <DialogFooter>
            <Button type="submit" disabled={pending || !values.name.trim()}>
              {pending ? "Saving…" : submitLabel}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function ModuleDetail({
  module,
  states,
  issues,
  issuesLoading,
  members,
  onBack,
  onEdit,
  onDelete,
  onAdd,
  onRemove,
  slug,
  identifier,
}: {
  module: Module;
  states: IssueState[];
  issues: Issue[];
  issuesLoading: boolean;
  members: Member[];
  onBack: () => void;
  onEdit: () => void;
  onDelete: () => void;
  onAdd: () => void;
  onRemove: (issueId: string) => void;
  slug: string;
  identifier: string;
}) {
  const counts = progressCounts(issues, states);
  const lead = members.find((m) => m.id === module.lead_id);

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
      issues: buckets.get(g) ?? [],
    })).filter((b) => b.issues.length > 0);
  }, [issues, states]);

  return (
    <div>
      <button
        onClick={onBack}
        className="text-xs text-muted-foreground hover:underline"
      >
        ← All modules
      </button>
      <div className="mt-2 flex items-center gap-3">
        <h2 className="text-xl font-semibold tracking-tight">{module.name}</h2>
        <Badge variant={statusVariant(module.status)}>{module.status}</Badge>
        <span className="text-sm text-muted-foreground">
          {dateRange(module)}
        </span>
        <div className="ml-auto flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={onEdit}>
            <Pencil className="mr-1 h-3 w-3" />
            Edit
          </Button>
          <Button variant="outline" size="sm" onClick={onAdd}>
            <Plus className="mr-1 h-3 w-3" />
            Add issues
          </Button>
          <Button variant="outline" size="sm" onClick={onDelete}>
            Delete
          </Button>
        </div>
      </div>
      {module.description && (
        <p className="mt-2 text-sm text-muted-foreground">
          {module.description}
        </p>
      )}
      {lead && (
        <p className="mt-1 text-xs text-muted-foreground">
          Lead: {lead.name ?? lead.email}
        </p>
      )}
      <div className="mt-4 grid grid-cols-3 gap-3">
        <Card>
          <CardContent className="p-4">
            <p className="text-xs text-muted-foreground">Open</p>
            <p className="text-2xl font-semibold tabular-nums">{counts.open}</p>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="p-4">
            <p className="text-xs text-muted-foreground">In progress</p>
            <p className="text-2xl font-semibold tabular-nums">
              {counts.inProgress}
            </p>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="p-4">
            <p className="text-xs text-muted-foreground">Done</p>
            <p className="text-2xl font-semibold tabular-nums">{counts.done}</p>
          </CardContent>
        </Card>
      </div>
      <h3 className="mb-2 mt-6 text-sm font-medium text-muted-foreground">
        Issues in this module ({issues.length})
      </h3>
      {issuesLoading ? (
        <div className="space-y-2">
          <Skeleton className="h-12 w-full" />
          <Skeleton className="h-12 w-full" />
        </div>
      ) : issues.length === 0 ? (
        <Card>
          <CardContent className="p-4 text-sm text-muted-foreground">
            No issues assigned to this module yet.
          </CardContent>
        </Card>
      ) : (
        <div className="space-y-4">
          {grouped.map(({ group, issues: gi }) => (
            <div key={group}>
              <p className="mb-1 text-xs font-medium capitalize text-muted-foreground">
                {group} · {gi.length}
              </p>
              <div className="space-y-2">
                {gi.map((i) => (
                  <div
                    key={i.id}
                    className="group flex items-center gap-3 rounded-md border p-3 text-sm transition-colors hover:bg-accent"
                  >
                    <Link
                      to={`/w/${slug}/p/${identifier}/i/${i.id}`}
                      className="flex min-w-0 flex-1 items-center gap-3"
                    >
                      <Badge
                        variant="outline"
                        className="font-mono text-[11px]"
                      >
                        {i.display_id}
                      </Badge>
                      <span className="truncate font-medium">{i.name}</span>
                    </Link>
                    <button
                      onClick={() => onRemove(i.id)}
                      className="rounded p-1 text-muted-foreground opacity-0 transition-opacity hover:bg-destructive/10 hover:text-destructive group-hover:opacity-100"
                      title={`Remove ${i.display_id} from module`}
                      aria-label={`Remove ${i.display_id} from module`}
                    >
                      <X className="h-4 w-4" />
                    </button>
                  </div>
                ))}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
