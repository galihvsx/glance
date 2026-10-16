// Project status updates (C34T1): the flat status timeline rendered on
// the project Overview page, plus the latest-status header chip derived
// from the first (newest) list item.
//
// Authz mirrors the backend: the composer shows for member (15)+
// callers (per the overview payload's `role` prop), edit/delete
// buttons for the update's author or a workspace admin (20+). Bodies
// are plain text in v1 — rendered as-is, never through the markdown
// renderer. The server enforces all of it; the UI just hides what
// would 403.

import { useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../../lib/api";
import { useAuth } from "../../lib/auth";
import { relativeTime } from "../../lib/relativeTime";
import type { ProjectUpdate, ProjectUpdateStatus } from "../../lib/types";
import { Alert, AlertDescription } from "../ui/alert";
import { Avatar, AvatarFallback } from "../ui/avatar";
import { Badge } from "../ui/badge";
import { Button } from "../ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "../ui/card";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "../ui/select";
import { Textarea } from "../ui/textarea";

const ROLE_MEMBER = 15;
const ROLE_ADMIN = 20;

const STATUS_META: Record<
  ProjectUpdateStatus,
  { label: string; badgeClassName: string }
> = {
  on_track: {
    label: "On Track",
    badgeClassName:
      "border-transparent bg-emerald-500/10 text-emerald-700 dark:text-emerald-400",
  },
  at_risk: {
    label: "At Risk",
    badgeClassName:
      "border-transparent bg-amber-500/10 text-amber-700 dark:text-amber-400",
  },
  off_track: {
    label: "Off Track",
    badgeClassName: "border-transparent bg-destructive/10 text-destructive",
  },
};

const STATUS_OPTIONS = (
  Object.keys(STATUS_META) as ProjectUpdateStatus[]
).map((status) => ({ status, label: STATUS_META[status].label }));

function actorName(u: ProjectUpdate): string {
  return u.author?.name ?? u.author?.email ?? "Deleted user";
}

function actorInitial(u: ProjectUpdate): string {
  return (actorName(u).trim()[0] ?? "?").toUpperCase();
}

function StatusBadge({ status }: { status: ProjectUpdateStatus }) {
  const meta = STATUS_META[status];
  return <Badge className={meta.badgeClassName}>{meta.label}</Badge>;
}

function UpdateRow({
  update,
  canModerate,
  onEdit,
  onDelete,
  editing,
  editStatus,
  setEditStatus,
  editBody,
  setEditBody,
  onSaveEdit,
  onCancelEdit,
  busy,
}: {
  update: ProjectUpdate;
  canModerate: (u: ProjectUpdate) => boolean;
  onEdit: () => void;
  onDelete: () => void;
  editing: boolean;
  editStatus: ProjectUpdateStatus;
  setEditStatus: (s: ProjectUpdateStatus) => void;
  editBody: string;
  setEditBody: (v: string) => void;
  onSaveEdit: () => void;
  onCancelEdit: () => void;
  busy: boolean;
}) {
  return (
    <div className="border-b border-border-hairline px-4 py-3 last:border-b-0">
      <div className="flex items-start gap-3">
        <Avatar className="h-8 w-8 shrink-0">
          <AvatarFallback className="text-xs font-semibold">
            {actorInitial(update)}
          </AvatarFallback>
        </Avatar>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-medium">{actorName(update)}</span>
            <StatusBadge status={update.status} />
            <span
              className="text-xs text-muted-foreground"
              title={update.created_at}
            >
              {relativeTime(update.created_at)}
            </span>
            {update.updated_at !== update.created_at && (
              <span
                className="text-xs text-muted-foreground"
                title={update.updated_at}
              >
                (edited)
              </span>
            )}
            {canModerate(update) && !editing && (
              <span className="ml-auto flex gap-2 text-xs">
                <button
                  type="button"
                  className="hover:underline"
                  onClick={onEdit}
                >
                  Edit
                </button>
                <button
                  type="button"
                  className="hover:underline"
                  onClick={onDelete}
                >
                  Delete
                </button>
              </span>
            )}
          </div>
          {editing ? (
            <div className="mt-2 space-y-2">
              <Select
                value={editStatus}
                onValueChange={(v) =>
                  setEditStatus(v as ProjectUpdateStatus)
                }
              >
                <SelectTrigger
                  className="w-44"
                  aria-label="Update status"
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {STATUS_OPTIONS.map((o) => (
                    <SelectItem key={o.status} value={o.status}>
                      {o.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <Textarea
                value={editBody}
                onChange={(e) => setEditBody(e.target.value)}
                rows={4}
                aria-label="Edit update message"
                autoFocus
              />
              <div className="flex gap-2">
                <Button
                  size="sm"
                  onClick={onSaveEdit}
                  disabled={busy || !editBody.trim()}
                >
                  Save
                </Button>
                <Button size="sm" variant="ghost" onClick={onCancelEdit}>
                  Cancel
                </Button>
              </div>
            </div>
          ) : (
            // Plain text in v1 — never the markdown renderer.
            <p className="mt-1 whitespace-pre-wrap text-sm">{update.body}</p>
          )}
        </div>
      </div>
    </div>
  );
}

export default function ProjectUpdates({
  slug,
  identifier,
  role,
}: {
  slug: string;
  identifier: string;
  /** The caller's workspace role from the overview payload: composer
   *  at member (15)+, admin (20+) may moderate others' updates. */
  role: number;
}) {
  const { user } = useAuth();
  const queryClient = useQueryClient();
  const base = `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`;
  const updatesKey = ["project-updates", slug, identifier];

  const updatesQuery = useQuery({
    queryKey: updatesKey,
    queryFn: () =>
      api
        .get<{ updates: ProjectUpdate[] }>(`${base}/updates`)
        .then((d) => d.updates),
  });

  const canWrite = role >= ROLE_MEMBER;
  const isAdmin = role >= ROLE_ADMIN;
  const canModerate = (u: ProjectUpdate) =>
    canWrite && (isAdmin || (user != null && u.author?.id === user.id));

  const [status, setStatus] = useState<ProjectUpdateStatus>("on_track");
  const [draft, setDraft] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [editStatus, setEditStatus] = useState<ProjectUpdateStatus>("on_track");
  const [editBody, setEditBody] = useState("");

  const invalidate = () =>
    void queryClient.invalidateQueries({ queryKey: updatesKey });

  const fail = (e: unknown, fallback: string) => {
    setError(e instanceof ApiError ? e.message : fallback);
  };

  const createMutation = useMutation({
    mutationFn: (body: { status: ProjectUpdateStatus; body: string }) =>
      api.post<ProjectUpdate>(`${base}/updates`, body),
    onSuccess: () => {
      setDraft("");
      setStatus("on_track");
      setError(null);
      invalidate();
    },
    onError: (e) => fail(e, "Failed to post update"),
  });

  const updateMutation = useMutation({
    mutationFn: ({
      id,
      status,
      body,
    }: {
      id: string;
      status: ProjectUpdateStatus;
      body: string;
    }) =>
      api.patch<ProjectUpdate>(
        `${base}/updates/${encodeURIComponent(id)}`,
        { status, body },
      ),
    onSuccess: () => {
      setEditingId(null);
      setError(null);
      invalidate();
    },
    onError: (e) => fail(e, "Failed to update update"),
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) =>
      api.del(`${base}/updates/${encodeURIComponent(id)}`),
    onSuccess: () => {
      setError(null);
      invalidate();
    },
    onError: (e) => fail(e, "Failed to delete update"),
  });

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (!draft.trim()) return;
    createMutation.mutate({ status, body: draft });
  }

  function startEdit(u: ProjectUpdate) {
    setEditingId(u.id);
    setEditStatus(u.status);
    setEditBody(u.body);
    setError(null);
  }

  function saveEdit() {
    if (!editingId || !editBody.trim()) return;
    updateMutation.mutate({
      id: editingId,
      status: editStatus,
      body: editBody,
    });
  }

  function askDelete(u: ProjectUpdate) {
    if (!window.confirm("Delete this update?")) return;
    deleteMutation.mutate(u.id);
  }

  const updates = updatesQuery.data ?? [];
  const latest = updates[0];
  const busy = createMutation.isPending || updateMutation.isPending;

  return (
    <Card className="rounded-lg border-border-hairline">
      <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
        <CardTitle className="text-xs font-semibold">Updates</CardTitle>
        {latest ? (
          <span data-testid="project-updates-latest">
            <StatusBadge status={latest.status} />
          </span>
        ) : (
          <Badge variant="secondary" data-testid="project-updates-latest">
            No updates yet
          </Badge>
        )}
      </CardHeader>
      <CardContent className="p-0">
        {error && (
          <Alert variant="destructive" className="mx-4 mb-2 mt-1">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        {updatesQuery.isPending ? (
          <p className="px-4 py-8 text-center text-xs text-muted-foreground">
            Loading updates…
          </p>
        ) : updatesQuery.isError ? (
          <p className="px-4 py-8 text-center text-xs text-destructive">
            Failed to load: {(updatesQuery.error as Error).message}
          </p>
        ) : updates.length === 0 ? (
          <p
            className="px-4 py-8 text-center text-xs text-muted-foreground"
            data-testid="project-updates-empty"
          >
            No updates yet.
            {canWrite && " Post the first status update below."}
          </p>
        ) : (
          updates.map((u) => (
            <UpdateRow
              key={u.id}
              update={u}
              canModerate={canModerate}
              onEdit={() => startEdit(u)}
              onDelete={() => askDelete(u)}
              editing={editingId === u.id}
              editStatus={editStatus}
              setEditStatus={setEditStatus}
              editBody={editBody}
              setEditBody={setEditBody}
              onSaveEdit={saveEdit}
              onCancelEdit={() => setEditingId(null)}
              busy={busy}
            />
          ))
        )}
        {canWrite && (
          <form onSubmit={onSubmit} className="space-y-2 px-4 py-3">
            <Select
              value={status}
              onValueChange={(v) => setStatus(v as ProjectUpdateStatus)}
            >
              <SelectTrigger className="w-44" aria-label="New update status">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {STATUS_OPTIONS.map((o) => (
                  <SelectItem key={o.status} value={o.status}>
                    {o.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Textarea
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              rows={4}
              placeholder="Share a status update…"
              aria-label="New update message"
            />
            <Button
              type="submit"
              size="sm"
              disabled={busy || !draft.trim()}
            >
              {createMutation.isPending ? "Posting…" : "Post update"}
            </Button>
          </form>
        )}
      </CardContent>
    </Card>
  );
}
