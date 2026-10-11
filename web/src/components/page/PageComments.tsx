// Page comments (C18T2): the flat comment thread rendered under the
// page view, plus the comment-count badge. Bodies are TipTap docs
// (like issue comments); author attribution is null for deleted users.
//
// Authz mirrors the backend: the composer shows for member (15)+
// callers, edit/delete buttons for the comment's author or a workspace
// admin (20+). The server enforces all of it; the UI just hides what
// would 403.

import { useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { MessageSquare } from "lucide-react";
import { api, ApiError } from "../../lib/api";
import { useAuth } from "../../lib/auth";
import { textToTipTapDoc, tiptapText } from "../../lib/tiptap";
import type { Member, PageComment } from "../../lib/types";
import { Alert, AlertDescription } from "../ui/alert";
import { Badge } from "../ui/badge";
import { Button } from "../ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "../ui/card";
import { Textarea } from "../ui/textarea";

const ROLE_MEMBER = 15;
const ROLE_ADMIN = 20;

function actorName(c: PageComment): string {
  return c.author?.name ?? c.author?.email ?? "Deleted user";
}

function timeAgo(iso: string): string {
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) return iso.slice(0, 10);
  const s = Math.max(0, Math.floor((Date.now() - then) / 1000));
  if (s < 60) return "just now";
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  const d = Math.floor(h / 24);
  if (d < 30) return `${d}d ago`;
  return new Date(then).toISOString().slice(0, 10);
}

function CommentRow({
  comment,
  canModerate,
  onEdit,
  onDelete,
  editing,
  editDraft,
  setEditDraft,
  onSaveEdit,
  onCancelEdit,
  busy,
}: {
  comment: PageComment;
  canModerate: (c: PageComment) => boolean;
  onEdit: () => void;
  onDelete: () => void;
  editing: boolean;
  editDraft: string;
  setEditDraft: (v: string) => void;
  onSaveEdit: () => void;
  onCancelEdit: () => void;
  busy: boolean;
}) {
  return (
    <div className="rounded-md border p-3">
      <div className="mb-1 flex items-center gap-2 text-xs text-muted-foreground">
        <span className="font-medium text-foreground">
          {actorName(comment)}
        </span>
        <span title={comment.created_at}>{timeAgo(comment.created_at)}</span>
        {comment.updated_at !== comment.created_at && (
          <span title={comment.updated_at}>(edited)</span>
        )}
        {canModerate(comment) && !editing && (
          <span className="ml-auto flex gap-2">
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
        <div className="space-y-2">
          <Textarea
            value={editDraft}
            onChange={(e) => setEditDraft(e.target.value)}
            rows={3}
            autoFocus
          />
          <div className="flex gap-2">
            <Button size="sm" onClick={onSaveEdit} disabled={busy}>
              Save
            </Button>
            <Button size="sm" variant="ghost" onClick={onCancelEdit}>
              Cancel
            </Button>
          </div>
        </div>
      ) : (
        <p className="whitespace-pre-wrap text-sm">
          {tiptapText(comment.body)}
        </p>
      )}
    </div>
  );
}

export default function PageComments({
  slug,
  identifier,
  pageId,
}: {
  slug: string;
  identifier: string;
  pageId: string;
}) {
  const { user } = useAuth();
  const queryClient = useQueryClient();
  const base = `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}/pages/${encodeURIComponent(pageId)}`;
  const commentsKey = ["page-comments", slug, identifier, pageId];

  const commentsQuery = useQuery({
    queryKey: commentsKey,
    queryFn: () =>
      api
        .get<{ comments: PageComment[] }>(`${base}/comments`)
        .then((d) => d.comments),
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

  const myRole =
    membersQuery.data?.find((m) => m.id === user?.id)?.role ?? 0;
  const canWrite = myRole >= ROLE_MEMBER;
  const isAdmin = myRole >= ROLE_ADMIN;
  const canModerate = (c: PageComment) =>
    canWrite && (isAdmin || (user != null && c.author?.id === user.id));

  const [draft, setDraft] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [editDraft, setEditDraft] = useState("");

  const invalidate = () =>
    void queryClient.invalidateQueries({ queryKey: commentsKey });

  const fail = (e: unknown, fallback: string) => {
    setError(e instanceof ApiError ? e.message : fallback);
  };

  const createMutation = useMutation({
    mutationFn: (body: unknown) =>
      api.post<PageComment>(`${base}/comments`, { body }),
    onSuccess: () => {
      setDraft("");
      setError(null);
      invalidate();
    },
    onError: (e) => fail(e, "Failed to post comment"),
  });

  const updateMutation = useMutation({
    mutationFn: ({ id, body }: { id: string; body: unknown }) =>
      api.patch<PageComment>(
        `${base}/comments/${encodeURIComponent(id)}`,
        { body },
      ),
    onSuccess: () => {
      setEditingId(null);
      setError(null);
      invalidate();
    },
    onError: (e) => fail(e, "Failed to update comment"),
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) =>
      api.del(`${base}/comments/${encodeURIComponent(id)}`),
    onSuccess: () => {
      setError(null);
      invalidate();
    },
    onError: (e) => fail(e, "Failed to delete comment"),
  });

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    const doc = textToTipTapDoc(draft);
    if (!doc) return;
    createMutation.mutate(doc);
  }

  function startEdit(c: PageComment) {
    setEditingId(c.id);
    setEditDraft(tiptapText(c.body));
    setError(null);
  }

  function saveEdit() {
    if (!editingId) return;
    const doc = textToTipTapDoc(editDraft);
    if (!doc) return;
    updateMutation.mutate({ id: editingId, body: doc });
  }

  function askDelete(c: PageComment) {
    if (!window.confirm("Delete this comment?")) return;
    deleteMutation.mutate(c.id);
  }

  const comments = commentsQuery.data ?? [];
  const busy = createMutation.isPending || updateMutation.isPending;

  return (
    <Card>
      <CardHeader className="py-3">
        <CardTitle className="flex items-center gap-2 text-sm font-medium">
          <MessageSquare className="h-4 w-4" />
          Comments
          <Badge variant="secondary" data-testid="page-comment-count">
            {comments.length}
          </Badge>
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        {commentsQuery.isLoading ? (
          <p className="text-sm text-muted-foreground">Loading comments…</p>
        ) : comments.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            No comments yet. Start the discussion below.
          </p>
        ) : (
          <div className="space-y-2">
            {comments.map((c) => (
              <CommentRow
                key={c.id}
                comment={c}
                canModerate={canModerate}
                onEdit={() => startEdit(c)}
                onDelete={() => askDelete(c)}
                editing={editingId === c.id}
                editDraft={editDraft}
                setEditDraft={setEditDraft}
                onSaveEdit={saveEdit}
                onCancelEdit={() => setEditingId(null)}
                busy={busy}
              />
            ))}
          </div>
        )}
        {canWrite && (
          <form onSubmit={onSubmit} className="space-y-2">
            <Textarea
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              rows={3}
              placeholder="Write a comment…"
            />
            <Button
              type="submit"
              size="sm"
              disabled={busy || !draft.trim()}
            >
              {createMutation.isPending ? "Posting…" : "Post comment"}
            </Button>
          </form>
        )}
      </CardContent>
    </Card>
  );
}
