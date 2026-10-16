import { useEffect, useMemo, useState, type FormEvent } from "react";
import { useParams, useSearchParams } from "react-router-dom";
import {
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { Pencil, Plus, Rocket, Trash2, X } from "lucide-react";
import { api, ApiError } from "../lib/api";
import { copyText } from "../lib/clipboard";
import type {
  Issue,
  IssueState,
  Release,
  ReleaseNotes,
} from "../lib/types";
import PageHeader from "../components/shell/PageHeader";
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
import { Textarea } from "../components/ui/textarea";
import {
  NativeSelect,
  NativeSelectOption,
} from "../components/ui/native-select";
import { Alert, AlertDescription } from "../components/ui/alert";
import { stateDotClass } from "../lib/stateDot";
import {
  OrgCard,
  OrgDetailHeader,
  OrgDoneBar,
  OrgEmpty,
  OrgHeaderButton,
  OrgHeaderPrimaryButton,
  OrgIssueRow,
  OrgProgressBar,
  OrgProgressCounts,
  OrgSectionHead,
  OrgStatBand,
  OrgStatusChip,
  snapshotProgressCounts,
} from "../components/org/OrgViews";

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

/** Status chip dot colors: state/accent colors. */
function statusColor(status: string): string {
  switch (status) {
    case "released":
      return "#42c88a"; // accent green
    case "planned":
      return "#e7ad42"; // accent amber
    default:
      return "#777780";
  }
}

function fmtDate(d?: string | null): string {
  if (!d) return "No release date";
  return d.slice(0, 10);
}

/**
 * Filename slug — the SAME ASCII rule as C33T0's pageExportFilename
 * (keep in sync): lowercase, runs of non-`a-z0-9` collapse to one `-`,
 * leading/trailing `-` trimmed. Non-ASCII letters become `-` too, so the
 * result is always a safe ASCII filename.
 */
export function slugifyExportName(name: string): string {
  return name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
}

/** Download filename for a release's notes: `<slug>-release-notes.md`. */
export function releaseNotesFilename(name: string): string {
  const slug = slugifyExportName(name);
  return slug ? `${slug}-release-notes.md` : "release-notes.md";
}

/** Trigger a browser download of a text file (Blob + anchor click). */
export function downloadTextFile(filename: string, text: string): void {
  const blob = new Blob([text], { type: "text/markdown;charset=utf-8" });
  const objectUrl = URL.createObjectURL(blob);
  try {
    const a = document.createElement("a");
    a.href = objectUrl;
    a.download = filename;
    document.body.appendChild(a);
    a.click();
    a.remove();
  } finally {
    URL.revokeObjectURL(objectUrl);
  }
}

/**
 * Release-notes dialog (C33T1): renders the backend Markdown verbatim
 * in a scrollable monospace block. Copy and Download both use the exact
 * same `markdown` string the dialog shows — no re-formatting.
 */
export function ReleaseNotesDialog({
  open,
  onOpenChange,
  releaseName,
  markdown,
  loading,
  error,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  releaseName: string;
  markdown: string;
  loading: boolean;
  error: unknown;
}) {
  const [copied, setCopied] = useState(false);
  const [copyFailed, setCopyFailed] = useState(false);
  useEffect(() => {
    if (open) {
      setCopied(false);
      setCopyFailed(false);
    }
  }, [open]);

  async function handleCopy() {
    if (!markdown) return;
    if (await copyText(markdown)) {
      setCopied(true);
      setCopyFailed(false);
    } else {
      setCopyFailed(true);
    }
  }

  function handleDownload() {
    if (!markdown) return;
    downloadTextFile(releaseNotesFilename(releaseName), markdown);
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[85vh] sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Release notes — {releaseName}</DialogTitle>
          <DialogDescription>
            Generated from this release&apos;s issues, grouped by state.
            Re-generate after issues change — notes are a snapshot, not a
            stored artifact.
          </DialogDescription>
        </DialogHeader>
        {loading ? (
          <div className="space-y-2">
            <Skeleton className="h-4 w-full" />
            <Skeleton className="h-4 w-full" />
            <Skeleton className="h-4 w-2/3" />
          </div>
        ) : error ? (
          <Alert variant="destructive">
            <AlertDescription>
              {error instanceof ApiError
                ? error.message
                : "Failed to load release notes"}
            </AlertDescription>
          </Alert>
        ) : (
          <pre className="max-h-[50vh] overflow-auto rounded-md border border-border bg-surface p-3 font-mono text-xs leading-relaxed text-ink">
            {markdown}
          </pre>
        )}
        <DialogFooter>
          {copyFailed && (
            <span className="mr-auto self-center text-xs text-accent-red">
              Copy failed — try Download instead.
            </span>
          )}
          <Button
            variant="outline"
            disabled={loading || !markdown}
            onClick={() => void handleCopy()}
          >
            {copied ? "Copied" : "Copy"}
          </Button>
          <Button disabled={loading || !markdown} onClick={handleDownload}>
            Download .md
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
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

/**
 * Release list card (C38T1): progress bar + folded counts from the
 * backend `progress_snapshot` — the card never fetches the release's
 * issues to compute progress. An empty snapshot (all six groups zero)
 * renders an all-open bar, not a missing bar.
 */
export function ReleaseCard({
  release,
  states,
  onSelect,
}: {
  release: Release;
  states: IssueState[];
  onSelect: () => void;
}) {
  const counts = snapshotProgressCounts(release.progress_snapshot ?? {});
  return (
    <OrgCard label={`Open release ${release.name}`} onActivate={onSelect}>
      <div className="flex items-center gap-2">
        <span className="truncate text-[13px] font-semibold text-ink">
          {release.name}
        </span>
        <span className="ml-auto">
          <OrgStatusChip
            color={statusColor(release.status)}
            label={release.status}
          />
        </span>
      </div>
      <p className="mt-1.5 text-[10px] tabular-nums text-ink-muted">
        {fmtDate(release.release_date)}
        <span className="mx-1.5" aria-hidden>
          ·
        </span>
        <span className="font-mono">{release.issue_count}</span> issue
        {release.issue_count === 1 ? "" : "s"}
      </p>
      <div className="mt-2.5">
        <OrgProgressBar
          snapshot={release.progress_snapshot ?? {}}
          states={states}
          emptyBar
        />
        <OrgProgressCounts {...counts} />
      </div>
    </OrgCard>
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
  // Overview Milestones rows deep-link here with ?release=<id> (C38T1).
  // An unknown id simply matches nothing and shows the list.
  const [searchParams] = useSearchParams();
  const [selectedId, setSelectedId] = useState<string | null>(() =>
    searchParams.get("release"),
  );
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
    <div className="w-full">
      {/* C19T4: shared page header replaces the per-page h1 + ProjectNav. */}
      <PageHeader
        viewName="Releases"
        count={releases.length}
        addButton={{ label: "New release", onClick: openCreate }}
      />
      <div className="mx-auto w-full max-w-[1060px] px-7 py-6">
        {releasesQuery.isError && (
          <Alert variant="destructive" className="mb-4">
            <AlertDescription>
              {releasesQuery.error instanceof ApiError
                ? releasesQuery.error.message
                : "Failed to load releases"}
            </AlertDescription>
          </Alert>
        )}
        {formError && (
          <Alert variant="destructive" className="mb-4">
            <AlertDescription>{formError}</AlertDescription>
          </Alert>
        )}

        <div className="mt-4">
          {releasesQuery.isPending ? (
            <div className="space-y-3">
              <Skeleton className="h-[68px] w-full rounded-md" />
              <Skeleton className="h-[68px] w-full rounded-md" />
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
            <OrgEmpty title="No releases yet">
              Plan your project's milestones — mark what ships in each
              release, then track progress as issues close.
            </OrgEmpty>
          ) : (
            <div className="space-y-3">
              {releases.map((r) => (
                <ReleaseCard
                  key={r.id}
                  release={r}
                  states={states}
                  onSelect={() => setSelectedId(r.id)}
                />
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
  const stateById = useMemo(
    () => new Map(states.map((s) => [s.id, s])),
    [states],
  );

  const [notesOpen, setNotesOpen] = useState(false);
  const base = `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`;
  const notesQuery = useQuery({
    queryKey: ["release-notes", slug, identifier, release.id],
    enabled: notesOpen,
    queryFn: () =>
      api.get<ReleaseNotes>(`${base}/releases/${release.id}/notes`),
  });

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
    <div>
      <OrgDetailHeader
        backLabel="All releases"
        onBack={onBack}
        title={release.name}
        chip={
          <OrgStatusChip
            color={statusColor(release.status)}
            label={release.status}
          />
        }
        meta={`${fmtDate(release.release_date)} · ${release.issue_count} issue${release.issue_count === 1 ? "" : "s"}`}
        actions={
          <>
            {release.status === "planned" && (
              <OrgHeaderPrimaryButton
                onClick={onMarkReleased}
                disabled={markReleasedPending}
              >
                <Rocket className="h-3 w-3" aria-hidden />
                {markReleasedPending ? "Marking…" : "Mark released"}
              </OrgHeaderPrimaryButton>
            )}
            <OrgHeaderButton onClick={onEdit}>
              <Pencil className="h-3 w-3" aria-hidden />
              Edit
            </OrgHeaderButton>
            <OrgHeaderButton
              onClick={() => setNotesOpen(true)}
              disabled={issuesLoading}
              title={
                issuesLoading
                  ? "Issues are still loading"
                  : "Generate release notes"
              }
            >
              Release notes
            </OrgHeaderButton>
            <OrgHeaderButton onClick={onDelete} destructive>
              <Trash2 className="h-3 w-3" aria-hidden />
              Delete
            </OrgHeaderButton>
          </>
        }
      />

      {release.description && (
        <p className="mt-3 text-[12px] leading-relaxed text-ink-secondary">
          {release.description}
        </p>
      )}

      <div className="mt-4">
        <OrgStatBand
          stats={[
            { label: "Open", value: counts.open, note: "triaged, backlog, unstarted" },
            { label: "In progress", value: counts.inProgress, note: "started" },
            { label: "Done", value: counts.done, note: "completed, cancelled" },
          ]}
        />
      </div>

      {total > 0 && (
        <div className="mt-4">
          <OrgDoneBar done={counts.done} total={total} />
        </div>
      )}

      <div className="mt-6">
        <OrgSectionHead
          title="Issues in this release"
          count={issues.length}
          actions={
            <OrgHeaderButton onClick={onAdd}>
              <Plus className="h-3 w-3" aria-hidden />
              Add issues
            </OrgHeaderButton>
          }
        />
        {issuesLoading ? (
          <div className="space-y-2 pt-2">
            <Skeleton className="h-[47px] w-full" />
            <Skeleton className="h-[47px] w-full" />
          </div>
        ) : issues.length === 0 ? (
          <div className="pt-3">
            <OrgEmpty title="No issues in this release yet">
              Add some to start tracking progress.
            </OrgEmpty>
          </div>
        ) : (
          <div>
            {grouped.map(({ group, label, issues: gi }) => (
              <div key={group} className={group !== grouped[0].group ? "mt-4" : undefined}>
                <OrgSectionHead
                  title={label}
                  count={gi.length}
                  prefix={
                    <span
                      className={`state-dot ${stateDotClass(group)}`}
                      aria-hidden
                    />
                  }
                />
                <div>
                  {gi.map((i) => (
                    <OrgIssueRow
                      key={i.id}
                      issue={i}
                      state={stateById.get(i.state_id)}
                      slug={slug}
                      identifier={identifier}
                      action={{
                        label: `Remove ${i.display_id} from release`,
                        title: `Remove ${i.display_id} from release`,
                        onClick: () => onRemove(i.id),
                        icon: <X className="h-4 w-4" aria-hidden />,
                      }}
                    />
                  ))}
                </div>
              </div>
            ))}
          </div>
        )}
      </div>

      <ReleaseNotesDialog
        open={notesOpen}
        onOpenChange={setNotesOpen}
        releaseName={release.name}
        markdown={notesQuery.data?.markdown ?? ""}
        loading={notesQuery.isPending}
        error={notesQuery.error}
      />
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
