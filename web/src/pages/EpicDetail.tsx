// C71T1: epic detail page. Header (back, name, description, Archive /
// Restore + Edit + Delete), state-group progress card, member issues
// with an add-issues picker, and linked initiatives with a link/unlink
// picker. Mirrors Modules.tsx (detail sections) and InitiativeDetail.tsx
// (progress + dialogs); the backend enforces member (15)+ on writes and
// the UI surfaces its errors.

import { useMemo, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Pencil, Plus, Search, Trash2, X } from "lucide-react";
import { api, ApiError } from "../lib/api";
import { toast } from "../components/ui/toast";
import type {
  Epic,
  Initiative,
  InitiativeEpic,
  Issue,
  IssueState,
} from "../lib/types";
import EpicForm from "../components/epics/EpicForm";
import EpicGroupBreakdown from "../components/epics/EpicGroupBreakdown";
import InitiativeProgressBar from "../components/initiatives/InitiativeProgressBar";
import { doneCount } from "../components/initiatives/stateGroups";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { Card, CardContent } from "../components/ui/card";
import { Checkbox } from "../components/ui/checkbox";
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
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../components/ui/dialog";
import { Input } from "../components/ui/input";
import { Skeleton } from "../components/ui/skeleton";
import { Alert, AlertDescription } from "../components/ui/alert";
import { stateDotClass } from "../lib/stateDot";
import {
  OrgDetailHeader,
  OrgEmpty,
  OrgHeaderButton,
  OrgIssueRow,
  OrgSectionHead,
  OrgStatusChip,
} from "../components/org/OrgViews";

function epicBase(slug: string, identifier: string): string {
  return `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}/epics`;
}

function initiativesBase(slug: string): string {
  return `/api/v1/workspaces/${encodeURIComponent(slug)}/initiatives`;
}

function initiativeStatusColor(status: Initiative["status"]): string {
  switch (status) {
    case "active":
      return "#4f8ef7"; // accent blue
    case "completed":
      return "#42c88a"; // accent green
    default:
      return "#777780"; // muted (archived)
  }
}

export default function EpicDetail() {
  const { slug = "", identifier = "", epicID = "" } = useParams<{
    slug: string;
    identifier: string;
    epicID: string;
  }>();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const base = epicBase(slug, identifier);
  const listPath = `/w/${encodeURIComponent(slug)}/p/${encodeURIComponent(identifier)}/epics`;

  const [editOpen, setEditOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [forceDelete, setForceDelete] = useState(false);
  const [archiveOpen, setArchiveOpen] = useState(false);
  const [addIssuesOpen, setAddIssuesOpen] = useState(false);
  const [linkOpen, setLinkOpen] = useState(false);
  const [issueSearch, setIssueSearch] = useState("");
  const [initiativeSearch, setInitiativeSearch] = useState("");
  const [pickedIssues, setPickedIssues] = useState<Set<string>>(new Set());
  const [pickedInitiative, setPickedInitiative] = useState<string | null>(null);
  const [formError, setFormError] = useState<string | null>(null);

  const epicQuery = useQuery({
    queryKey: ["epic-detail", slug, identifier, epicID],
    queryFn: () => api.get<Epic>(`${base}/${epicID}`),
  });
  const issuesQuery = useQuery({
    queryKey: ["epic-issues", slug, identifier, epicID],
    queryFn: () =>
      api
        .get<{ issues: Issue[] }>(`${base}/${epicID}/issues`)
        .then((d) => d.issues),
  });
  const statesQuery = useQuery({
    queryKey: ["states", slug, identifier],
    queryFn: () =>
      api
        .get<{ states: IssueState[] }>(
          `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}/states`,
        )
        .then((d) => d.states),
  });
  const initiativesQuery = useQuery({
    queryKey: ["initiatives", slug],
    queryFn: () =>
      api
        .get<{ initiatives: Initiative[] }>(initiativesBase(slug))
        .then((d) => d.initiatives),
  });
  // The backend fans out initiative → epics only; the epic → initiatives
  // direction is derived client-side (one query slot, one skeleton).
  const linkedInitiativesQuery = useQuery({
    queryKey: ["epic-linked-initiatives", slug, epicID],
    queryFn: async () => {
      const initiatives = await api
        .get<{ initiatives: Initiative[] }>(initiativesBase(slug))
        .then((d) => d.initiatives);
      const checks = await Promise.all(
        initiatives.map(async (i) => {
          const epics = await api
            .get<{ epics: InitiativeEpic[] }>(
              `${initiativesBase(slug)}/${i.id}/epics`,
            )
            .then((d) => d.epics);
          return { initiative: i, linked: epics.some((e) => e.epic_id === epicID) };
        }),
      );
      return checks.filter((c) => c.linked).map((c) => c.initiative);
    },
  });

  const allIssuesQuery = useQuery({
    queryKey: ["board-issues", slug, identifier],
    enabled: addIssuesOpen,
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
          `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}/issues?${p.toString()}`,
        );
        all.push(...page.results);
        cursor = page.next_cursor;
      } while (cursor);
      return all;
    },
  });

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: ["epic-detail", slug, identifier, epicID] });
    void queryClient.invalidateQueries({ queryKey: ["epic-issues", slug, identifier, epicID] });
    void queryClient.invalidateQueries({ queryKey: ["epics", slug, identifier] });
    void queryClient.invalidateQueries({ queryKey: ["epic-linked-initiatives", slug, epicID] });
  };

  const fail = (e: unknown, what: string) =>
    toast.add({
      title: what,
      description: e instanceof ApiError ? e.message : undefined,
      type: "error",
    });

  const updateMutation = useMutation({
    mutationFn: (patch: { name?: string; description?: string }) =>
      api.patch<Epic>(`${base}/${epicID}`, patch),
    onSuccess: () => {
      setEditOpen(false);
      setFormError(null);
      invalidate();
      toast.add({ title: "Epic updated", type: "success" });
    },
    onError: (e) => setFormError(e instanceof ApiError ? e.message : "Failed to update epic"),
  });

  const deleteMutation = useMutation({
    mutationFn: (force: boolean) =>
      api.del(`${base}/${epicID}${force ? "?force=true" : ""}`),
    onSuccess: () => {
      setDeleteOpen(false);
      setForceDelete(false);
      void queryClient.invalidateQueries({ queryKey: ["epics", slug, identifier] });
      toast.add({ title: "Epic deleted", type: "success" });
      navigate(listPath);
    },
    onError: (e) => {
      // C71T1: the server 409s when the epic still has issues; the
      // confirm dialog then offers "Delete anyway (detach N issues)"
      // which retries with ?force=true.
      if (!forceDelete && e instanceof ApiError && e.status === 409) {
        setForceDelete(true);
        return;
      }
      setDeleteOpen(false);
      setForceDelete(false);
      fail(e, "Couldn't delete the epic");
    },
  });

  const archiveMutation = useMutation({
    mutationFn: () => api.post<Epic>(`${base}/${epicID}/archive`),
    onSuccess: () => {
      setArchiveOpen(false);
      invalidate();
      toast.add({ title: "Epic archived", type: "success" });
    },
    onError: (e) => {
      setArchiveOpen(false);
      fail(e, "Couldn't archive the epic");
    },
  });

  const unarchiveMutation = useMutation({
    mutationFn: () => api.post<Epic>(`${base}/${epicID}/unarchive`),
    onSuccess: () => {
      setArchiveOpen(false);
      invalidate();
      toast.add({ title: "Epic restored", type: "success" });
    },
    onError: (e) => {
      setArchiveOpen(false);
      fail(e, "Couldn't restore the epic");
    },
  });

  const addIssuesMutation = useMutation({
    mutationFn: (ids: string[]) =>
      api.post(`${base}/${epicID}/issues`, { issue_ids: ids }),
    onSuccess: (_, ids) => {
      setAddIssuesOpen(false);
      setPickedIssues(new Set());
      setIssueSearch("");
      invalidate();
      toast.add({
        title:
          ids.length === 1
            ? "Issue added to the epic"
            : `${ids.length} issues added to the epic`,
        type: "success",
      });
    },
    onError: (e) => fail(e, "Couldn't add issues"),
  });

  const removeIssueMutation = useMutation({
    mutationFn: (issueId: string) =>
      api.del(`${base}/${epicID}/issues`, { issue_ids: [issueId] }),
    onSuccess: invalidate,
    onError: (e) => fail(e, "Couldn't remove the issue"),
  });

  const linkMutation = useMutation({
    mutationFn: (initiativeId: string) =>
      api.post(`${initiativesBase(slug)}/${initiativeId}/epics`, {
        epic_id: epicID,
      }),
    onSuccess: () => {
      setLinkOpen(false);
      setPickedInitiative(null);
      setInitiativeSearch("");
      invalidate();
      toast.add({ title: "Initiative linked", type: "success" });
    },
    onError: (e) => fail(e, "Couldn't link the initiative"),
  });

  const unlinkMutation = useMutation({
    mutationFn: (initiativeId: string) =>
      api.del(`${initiativesBase(slug)}/${initiativeId}/epics/${epicID}`),
    onSuccess: invalidate,
    onError: (e) => fail(e, "Couldn't unlink the initiative"),
  });

  const epic = epicQuery.data ?? null;
  const archived = !!epic?.archived_at;
  const byGroup = epic?.progress?.by_group ?? {};
  const total = epic?.progress?.total ?? 0;
  const done = doneCount(byGroup);
  const pct = total > 0 ? (done / total) * 100 : 0;

  const states = statesQuery.data ?? [];
  const stateById = useMemo(
    () => new Map(states.map((s) => [s.id, s])),
    [states],
  );
  const memberIssueIds = useMemo(
    () => new Set((issuesQuery.data ?? []).map((i) => i.id)),
    [issuesQuery.data],
  );
  const filteredIssues = useMemo(() => {
    const q = issueSearch.trim().toLowerCase();
    const all = allIssuesQuery.data ?? [];
    if (!q) return all;
    return all.filter(
      (i) =>
        i.name.toLowerCase().includes(q) ||
        i.display_id.toLowerCase().includes(q),
    );
  }, [allIssuesQuery.data, issueSearch]);

  const linkedInitiatives = linkedInitiativesQuery.data ?? [];
  const linkedIds = useMemo(
    () => new Set(linkedInitiatives.map((i) => i.id)),
    [linkedInitiatives],
  );
  const filteredInitiatives = useMemo(() => {
    const q = initiativeSearch.trim().toLowerCase();
    const all = initiativesQuery.data ?? [];
    if (!q) return all;
    return all.filter((i) => i.name.toLowerCase().includes(q));
  }, [initiativesQuery.data, initiativeSearch]);

  function togglePickedIssue(id: string) {
    setPickedIssues((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  function openDelete() {
    setForceDelete(false);
    setDeleteOpen(true);
  }

  function submitEdit(v: { name: string; description: string }) {
    if (!epic) return;
    const patch: { name?: string; description?: string } = {};
    if (v.name && v.name !== epic.name) patch.name = v.name;
    if (v.description !== (epic.description ?? "")) patch.description = v.description;
    if (Object.keys(patch).length === 0) {
      setEditOpen(false);
      return;
    }
    updateMutation.mutate(patch);
  }

  if (epicQuery.isPending) {
    return (
      <div className="mx-auto w-full max-w-5xl space-y-4 p-4">
        <Skeleton className="h-8 w-64" />
        <Skeleton className="h-24 w-full" />
        <Skeleton className="h-40 w-full" />
      </div>
    );
  }
  if (epicQuery.isError || !epic) {
    return (
      <div className="mx-auto w-full max-w-5xl p-4">
        <OrgEmpty title="Epic not found">
          It may have been deleted, or you may not have access to this project.
        </OrgEmpty>
      </div>
    );
  }

  return (
    <div className="mx-auto w-full max-w-5xl space-y-6 p-4">
      <OrgDetailHeader
        backLabel="Epics"
        onBack={() => navigate(listPath)}
        title={epic.name}
        chip={
          archived ? (
            <OrgStatusChip color="#777780" label="Archived" />
          ) : undefined
        }
        meta={`${epic.issue_count} issue${epic.issue_count === 1 ? "" : "s"}${
          archived && epic.archived_at
            ? ` · archived ${epic.archived_at.slice(0, 10)}`
            : ""
        }`}
        actions={
          <>
            <OrgHeaderButton
              onClick={() => setArchiveOpen(true)}
              title={archived ? "Restore epic" : "Archive epic"}
            >
              {archived ? "Restore" : "Archive"}
            </OrgHeaderButton>
            <OrgHeaderButton onClick={() => setEditOpen(true)} title="Edit epic">
              <Pencil className="h-3 w-3" aria-hidden />
              Edit
            </OrgHeaderButton>
            <OrgHeaderButton
              onClick={openDelete}
              title="Delete epic"
              destructive
            >
              <Trash2 className="h-3 w-3" aria-hidden />
              Delete
            </OrgHeaderButton>
          </>
        }
      />

      {epic.description ? (
        <p className="text-[13px] text-ink-secondary">{epic.description}</p>
      ) : (
        <p className="text-[13px] text-ink-muted italic">No description yet.</p>
      )}

      {/* Progress */}
      <section aria-label="Progress">
        <OrgSectionHead title="Progress" />
        <Card className="mt-3">
          <CardContent className="space-y-3 p-4">
            <InitiativeProgressBar
              completionPct={pct}
              doneCount={done}
              total={total}
            />
            <EpicGroupBreakdown
              byGroup={byGroup}
              total={total}
              epicName={epic.name}
            />
          </CardContent>
        </Card>
      </section>

      {/* Member issues */}
      <section aria-label="Issues">
        <OrgSectionHead
          title="Issues"
          count={issuesQuery.data?.length ?? epic.issue_count}
          actions={
            <OrgHeaderButton
              onClick={() => {
                setPickedIssues(new Set());
                setIssueSearch("");
                setAddIssuesOpen(true);
              }}
              title="Add issues"
            >
              <Plus className="h-3 w-3" aria-hidden />
              Add issues
            </OrgHeaderButton>
          }
        />
        {issuesQuery.isPending ? (
          <div className="mt-3 space-y-2">
            <Skeleton className="h-[47px] w-full" />
            <Skeleton className="h-[47px] w-full" />
          </div>
        ) : issuesQuery.isError ? (
          <div className="mt-3">
            <Alert variant="destructive">
              <AlertDescription>
                {issuesQuery.error instanceof ApiError
                  ? issuesQuery.error.message
                  : "Failed to load issues"}
              </AlertDescription>
            </Alert>
          </div>
        ) : (issuesQuery.data ?? []).length === 0 ? (
          <div className="mt-3">
            <OrgEmpty title="No issues in this epic yet">
              Attach issues to start tracking progress.
            </OrgEmpty>
          </div>
        ) : (
          <div className="mt-1">
            {(issuesQuery.data ?? []).map((i) => (
              <OrgIssueRow
                key={i.id}
                issue={i}
                state={stateById.get(i.state_id)}
                slug={slug}
                identifier={identifier}
                action={{
                  label: `Remove ${i.display_id} from epic`,
                  title: `Remove ${i.display_id} from epic`,
                  onClick: () => removeIssueMutation.mutate(i.id),
                  icon: <X className="h-4 w-4" aria-hidden />,
                }}
              />
            ))}
          </div>
        )}
      </section>

      {/* Linked initiatives */}
      <section aria-label="Linked initiatives">
        <OrgSectionHead
          title="Linked initiatives"
          count={linkedInitiatives.length}
          actions={
            <OrgHeaderButton
              onClick={() => {
                setPickedInitiative(null);
                setInitiativeSearch("");
                setLinkOpen(true);
              }}
              title="Link initiative"
            >
              <Plus className="h-3 w-3" aria-hidden />
              Link initiative
            </OrgHeaderButton>
          }
        />
        {linkedInitiativesQuery.isPending ? (
          <div className="mt-3">
            <Skeleton className="h-[47px] w-full" />
          </div>
        ) : linkedInitiatives.length === 0 ? (
          <div className="mt-3">
            <OrgEmpty title="No linked initiatives">
              Link this epic to a workspace initiative to roll it up.
            </OrgEmpty>
          </div>
        ) : (
          <ul className="mt-3 space-y-2">
            {linkedInitiatives.map((i) => (
              <li key={i.id} data-testid="epic-initiative-row">
                <Card>
                  <CardContent className="flex items-center gap-2 p-3.5">
                    <OrgStatusChip
                      color={initiativeStatusColor(i.status)}
                      label={i.status}
                    />
                    <span className="truncate text-[13px] font-semibold text-ink">
                      {i.name}
                    </span>
                    <span className="text-[10px] tabular-nums text-ink-muted">
                      <span className="font-mono">{i.project_count}</span>{" "}
                      project{i.project_count === 1 ? "" : "s"}
                    </span>
                    <button
                      type="button"
                      aria-label={`Unlink ${i.name}`}
                      title={`Unlink ${i.name}`}
                      onClick={() => unlinkMutation.mutate(i.id)}
                      className="ml-auto rounded p-1 text-ink-muted transition-colors hover:bg-raised hover:text-ink"
                    >
                      <X className="h-3.5 w-3.5" aria-hidden />
                    </button>
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
            <DialogTitle>Edit epic</DialogTitle>
            <DialogDescription>
              Rename the epic or update its description.
            </DialogDescription>
          </DialogHeader>
          <EpicForm
            key={epic.id}
            initial={{ name: epic.name, description: epic.description ?? "" }}
            pending={updateMutation.isPending}
            error={formError}
            submitLabel="Save changes"
            onSubmit={submitEdit}
          />
        </DialogContent>
      </Dialog>

      {/* Add-issues dialog */}
      <Dialog open={addIssuesOpen} onOpenChange={setAddIssuesOpen}>
        <DialogContent className="max-h-[80vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>Add issues to {epic.name}</DialogTitle>
            <DialogDescription>
              Select this project's issues to attach. Attaching is
              idempotent — issues already in the epic are listed, not
              selectable.
            </DialogDescription>
          </DialogHeader>
          <div className="relative">
            <Search className="absolute left-2 top-[8px] h-3.5 w-3.5 text-ink-muted" />
            <Input
              value={issueSearch}
              onChange={(e) => setIssueSearch(e.target.value)}
              placeholder="Search project issues…"
              aria-label="Search project issues"
              className="pl-8"
            />
          </div>
          {allIssuesQuery.isPending ? (
            <div className="space-y-2">
              <Skeleton className="h-10 w-full" />
              <Skeleton className="h-10 w-full" />
              <Skeleton className="h-10 w-full" />
            </div>
          ) : (
            <div className="space-y-1">
              {filteredIssues.map((i) => {
                const already = memberIssueIds.has(i.id);
                return (
                  <label
                    key={i.id}
                    className={`flex cursor-pointer items-center gap-3 rounded-md px-2 py-2 text-sm hover:bg-accent ${
                      already ? "cursor-default opacity-50" : ""
                    }`}
                  >
                    <Checkbox
                      checked={already || pickedIssues.has(i.id)}
                      disabled={already}
                      onCheckedChange={() => togglePickedIssue(i.id)}
                      aria-label={already ? `${i.name} already attached` : `Attach ${i.name}`}
                    />
                    <Badge
                      variant="outline"
                      className="font-mono text-[11px]"
                    >
                      {i.display_id}
                    </Badge>
                    <span
                      className={`state-dot ${stateDotClass(stateById.get(i.state_id)?.group ?? "unstarted")}`}
                      aria-hidden
                    />
                    <span className="truncate">{i.name}</span>
                    {already && (
                      <span className="ml-auto text-[10px] text-ink-muted">
                        Already attached
                      </span>
                    )}
                  </label>
                );
              })}
              {filteredIssues.length === 0 && (
                <p className="text-sm text-muted-foreground">
                  No issues match your search.
                </p>
              )}
            </div>
          )}
          <DialogFooter>
            <Button
              disabled={addIssuesMutation.isPending || pickedIssues.size === 0}
              onClick={() => addIssuesMutation.mutate([...pickedIssues])}
            >
              {addIssuesMutation.isPending
                ? "Adding…"
                : `Add ${pickedIssues.size} issue${pickedIssues.size === 1 ? "" : "s"}`}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Link-initiative dialog */}
      <Dialog open={linkOpen} onOpenChange={setLinkOpen}>
        <DialogContent className="max-h-[80vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>Link initiative</DialogTitle>
            <DialogDescription>
              Link this epic to a workspace initiative. The link is
              bidirectional — the epic also appears under the
              initiative's epics.
            </DialogDescription>
          </DialogHeader>
          <div className="relative">
            <Search className="absolute left-2 top-[8px] h-3.5 w-3.5 text-ink-muted" />
            <Input
              value={initiativeSearch}
              onChange={(e) => setInitiativeSearch(e.target.value)}
              placeholder="Search initiatives…"
              aria-label="Search initiatives"
              className="pl-8"
            />
          </div>
          {initiativesQuery.isPending || linkedInitiativesQuery.isPending ? (
            <div className="space-y-2">
              <Skeleton className="h-10 w-full" />
              <Skeleton className="h-10 w-full" />
            </div>
          ) : (
            <div className="space-y-1">
              {filteredInitiatives.map((i) => {
                const already = linkedIds.has(i.id);
                return (
                  <label
                    key={i.id}
                    className={`flex cursor-pointer items-center gap-3 rounded-md px-2 py-2 text-sm hover:bg-accent ${
                      already ? "cursor-default opacity-50" : ""
                    }`}
                  >
                    <Checkbox
                      checked={already || pickedInitiative === i.id}
                      disabled={already}
                      onCheckedChange={() =>
                        setPickedInitiative((prev) =>
                          prev === i.id ? null : i.id,
                        )
                      }
                      aria-label={already ? `${i.name} linked` : `Link ${i.name}`}
                    />
                    <OrgStatusChip
                      color={initiativeStatusColor(i.status)}
                      label={i.status}
                    />
                    <span className="truncate">{i.name}</span>
                    <span className="ml-auto text-[10px] tabular-nums text-ink-muted">
                      {already ? (
                        "Linked"
                      ) : (
                        <>
                          <span className="font-mono">{i.project_count}</span>{" "}
                          project{i.project_count === 1 ? "" : "s"}
                        </>
                      )}
                    </span>
                  </label>
                );
              })}
              {filteredInitiatives.length === 0 && (
                <p className="text-sm text-muted-foreground">
                  No initiatives match your search.
                </p>
              )}
            </div>
          )}
          <DialogFooter>
            <Button
              disabled={linkMutation.isPending || pickedInitiative === null}
              onClick={() =>
                pickedInitiative && linkMutation.mutate(pickedInitiative)
              }
            >
              {linkMutation.isPending ? "Linking…" : "Link initiative"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Archive / restore confirm */}
      <AlertDialog open={archiveOpen} onOpenChange={setArchiveOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {archived ? `Restore “${epic.name}”?` : `Archive “${epic.name}”?`}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {archived
                ? "The epic returns to the active list. Its issues are untouched."
                : "The epic leaves the active list but stays fully accessible — issues keep their link."}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={() =>
                archived ? unarchiveMutation.mutate() : archiveMutation.mutate()
              }
            >
              {archiveMutation.isPending || unarchiveMutation.isPending
                ? "Working…"
                : archived
                  ? "Restore epic"
                  : "Archive epic"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      {/* Delete confirm (409 → force) */}
      <AlertDialog
        open={deleteOpen}
        onOpenChange={(o) => {
          if (!o) {
            setDeleteOpen(false);
            setForceDelete(false);
          }
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {forceDelete
                ? `Delete anyway (detach ${epic.issue_count} issue${epic.issue_count === 1 ? "" : "s"})?`
                : `Delete “${epic.name}”?`}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {forceDelete
                ? "The epic is deleted and its issues are detached. The issues themselves are kept."
                : epic.issue_count > 0
                  ? `This epic has ${epic.issue_count} issue${epic.issue_count === 1 ? "" : "s"}. They will be kept but detached from the epic.`
                  : "This can't be undone."}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => deleteMutation.mutate(forceDelete)}
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            >
              {deleteMutation.isPending
                ? "Deleting…"
                : forceDelete
                  ? "Delete anyway"
                  : "Delete epic"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
