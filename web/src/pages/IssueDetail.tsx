import { useEffect, useState, type FormEvent } from "react";
import { Link, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { EditorContent, useEditor } from "@tiptap/react";
import StarterKit from "@tiptap/starter-kit";
import { MessageSquare, Pencil } from "lucide-react";
import { api, ApiError } from "../lib/api";
import type {
  CommentNode,
  HistoryEntry,
  Issue,
  IssueState,
  Label as ProjectLabel,
  Member,
} from "../lib/types";
import { tiptapText } from "../lib/tiptap";
import { Button } from "../components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "../components/ui/card";
import { Badge } from "../components/ui/badge";
import { Input } from "../components/ui/input";
import { Label } from "../components/ui/label";
import { Textarea } from "../components/ui/textarea";
import { Skeleton } from "../components/ui/skeleton";
import { Alert, AlertDescription } from "../components/ui/alert";
import { Separator } from "../components/ui/separator";
import StateBadge from "../components/issue/StateBadge";
import StatePicker from "../components/issue/StatePicker";
import PriorityPicker from "../components/issue/PriorityPicker";
import AssigneePicker from "../components/issue/AssigneePicker";
import LabelPicker from "../components/issue/LabelPicker";

/** Wraps plain text as a minimal TipTap doc. */
function textToTipTapDoc(text: string): unknown {
  const t = text.trim();
  if (!t) return null;
  return {
    type: "doc",
    content: t.split(/\n+/).map((line) => ({
      type: "paragraph",
      content: [{ type: "text", text: line }],
    })),
  };
}

function actorName(a: { name?: string | null; email: string }): string {
  return a.name ?? a.email;
}

function fmtTime(iso: string): string {
  const d = new Date(iso);
  return d.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

/** Human label for an activity field. */
function fieldLabel(field: string): string {
  if (field === "_created") return "created the issue";
  if (field === "_deleted") return "deleted the issue";
  return `changed ${field.replace(/_/g, " ")}`;
}

// ---------- Description editor ----------

function DescriptionEditor({
  issue,
  onSave,
  saving,
}: {
  issue: Issue;
  onSave: (doc: unknown | null) => void;
  saving: boolean;
}) {
  const [editing, setEditing] = useState(false);
  const editor = useEditor({
    extensions: [StarterKit],
    content: (issue.description as object) ?? { type: "doc", content: [] },
    editable: editing,
  });

  // Reset content when the issue changes (e.g. after a save) and toggle
  // editability when edit mode changes.
  useEffect(() => {
    if (!editor) return;
    editor.setEditable(editing);
    if (!editing) {
      editor.commands.setContent(
        (issue.description as object) ?? { type: "doc", content: [] },
      );
    }
  }, [editor, editing, issue.description]);

  const empty =
    !issue.description || tiptapText(issue.description) === "";

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-3">
        <CardTitle className="text-base">Description</CardTitle>
        <Button
          variant="ghost"
          size="sm"
          onClick={() => setEditing((e) => !e)}
          className="gap-2"
        >
          <Pencil className="h-3.5 w-3.5" />
          {editing ? "Cancel" : empty ? "Add description" : "Edit"}
        </Button>
      </CardHeader>
      <CardContent>
        {editing ? (
          <div className="space-y-3">
            <div className="min-h-32 rounded-md border p-3 focus-within:ring-1 focus-within:ring-ring">
              <EditorContent editor={editor} />
            </div>
            <div className="flex gap-2">
              <Button
                size="sm"
                disabled={saving}
                onClick={() => {
                  onSave(editor?.getJSON() ?? null);
                  setEditing(false);
                }}
              >
                {saving ? "Saving…" : "Save"}
              </Button>
              <Button
                size="sm"
                variant="ghost"
                className="text-destructive hover:text-destructive"
                onClick={() => {
                  onSave(null);
                  setEditing(false);
                }}
              >
                Remove description
              </Button>
            </div>
          </div>
        ) : empty ? (
          <p className="text-sm text-muted-foreground">
            No description yet.
          </p>
        ) : (
          <div className="prose prose-sm dark:prose-invert max-w-none">
            <EditorContent editor={editor} />
          </div>
        )}
      </CardContent>
    </Card>
  );
}

// ---------- Comments ----------

function CommentItem({
  comment,
  onReply,
}: {
  comment: CommentNode;
  onReply: (parentId: string) => void;
}) {
  return (
    <div className="space-y-2">
      <div className="rounded-md border p-3">
        <div className="mb-1 flex items-center gap-2 text-xs text-muted-foreground">
          <span className="font-medium text-foreground">
            {actorName(comment.actor)}
          </span>
          <span>{fmtTime(comment.created_at)}</span>
          <button
            className="ml-auto hover:underline"
            onClick={() => onReply(comment.id)}
          >
            Reply
          </button>
        </div>
        <p className="whitespace-pre-wrap text-sm">
          {tiptapText(comment.content)}
        </p>
      </div>
      {comment.replies.length > 0 && (
        <div className="space-y-2 border-l-2 pl-4">
          {comment.replies.map((r) => (
            <CommentItem key={r.id} comment={r} onReply={onReply} />
          ))}
        </div>
      )}
    </div>
  );
}

// ---------- Detail page ----------

export default function IssueDetail() {
  const { slug = "", identifier = "", uuid = "" } = useParams<{
    slug: string;
    identifier: string;
    uuid: string;
  }>();
  const queryClient = useQueryClient();
  const base = `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`;
  const issuePath = `${base}/issues/${encodeURIComponent(uuid)}`;

  const [titleEditing, setTitleEditing] = useState(false);
  const [titleDraft, setTitleDraft] = useState("");
  const [commentDraft, setCommentDraft] = useState("");
  const [replyTo, setReplyTo] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const issueKey = ["issue", slug, identifier, uuid];
  const issuesKey = ["issues", slug, identifier];

  const issueQuery = useQuery({
    queryKey: issueKey,
    queryFn: () => api.get<Issue>(issuePath),
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
  const labelsQuery = useQuery({
    queryKey: ["labels", slug, identifier],
    queryFn: () =>
      api.get<{ labels: ProjectLabel[] }>(`${base}/labels`).then((d) => d.labels),
  });
  const commentsQuery = useQuery({
    queryKey: ["comments", slug, identifier, uuid],
    queryFn: () =>
      api
        .get<{ comments: CommentNode[] }>(`${issuePath}/comments`)
        .then((d) => d.comments),
  });
  const historyQuery = useQuery({
    queryKey: ["history", slug, identifier, uuid],
    queryFn: () =>
      api
        .get<{ history: HistoryEntry[] }>(`${issuePath}/history`)
        .then((d) => d.history),
  });

  const issue = issueQuery.data;
  const states = statesQuery.data ?? [];
  const stateById = new Map(states.map((s) => [s.id, s]));

  function invalidateIssue() {
    void queryClient.invalidateQueries({ queryKey: issueKey });
    void queryClient.invalidateQueries({ queryKey: issuesKey });
  }

  // PATCH mutation with optimistic update of the cached issue.
  const patchMutation = useMutation({
    mutationFn: (body: Record<string, unknown>) =>
      api.patch<Issue>(issuePath, body),
    onMutate: async (body) => {
      await queryClient.cancelQueries({ queryKey: issueKey });
      const prev = queryClient.getQueryData<Issue>(issueKey);
      if (prev) {
        queryClient.setQueryData<Issue>(issueKey, {
          ...prev,
          ...(body.name !== undefined ? { name: body.name as string } : {}),
          ...(body.state_id !== undefined
            ? { state_id: body.state_id as string }
            : {}),
          ...(body.priority !== undefined
            ? { priority: body.priority as number }
            : {}),
          ...(body.description !== undefined
            ? { description: body.description as unknown }
            : {}),
        });
      }
      return { prev };
    },
    onError: (e, _body, ctx) => {
      if (ctx?.prev) queryClient.setQueryData(issueKey, ctx.prev);
      setError(
        e instanceof ApiError ? e.message : "Failed to update issue",
      );
    },
    onSettled: () => invalidateIssue(),
  });

  // Assignee/label toggle with optimistic relation update.
  function toggleRelation(
    kind: "assignees" | "labels",
    id: string,
    currently: boolean,
  ) {
    const prev = queryClient.getQueryData<Issue>(issueKey);
    const optimistic = (iss: Issue): Issue => {
      if (kind === "assignees") {
        const member = membersQuery.data?.find((m) => m.id === id);
        const assignees = currently
          ? iss.assignees.filter((a) => a.id !== id)
          : [
              ...iss.assignees,
              { id, name: member?.name ?? member?.email ?? id },
            ];
        return { ...iss, assignees };
      }
      const label = labelsQuery.data?.find((l) => l.id === id);
      const labels = currently
        ? iss.labels.filter((l) => l.id !== id)
        : [
            ...iss.labels,
            {
              id,
              name: label?.name ?? id,
              color: label?.color ?? "#888888",
            },
          ];
      return { ...iss, labels };
    };
    if (prev) queryClient.setQueryData<Issue>(issueKey, optimistic(prev));
    const url =
      kind === "assignees"
        ? `${issuePath}/assignees/${encodeURIComponent(id)}`
        : `${issuePath}/labels/${encodeURIComponent(id)}`;
    const req = currently ? api.del(url) : api.post(url);
    req.then(
      () => invalidateIssue(),
      (e: unknown) => {
        if (prev) queryClient.setQueryData(issueKey, prev);
        setError(
          e instanceof ApiError ? e.message : "Failed to update",
        );
      },
    );
  }

  const commentMutation = useMutation({
    mutationFn: (body: { content: unknown; parent_id?: string | null }) =>
      api.post<CommentNode>(`${issuePath}/comments`, body),
    onSuccess: () => {
      setCommentDraft("");
      setReplyTo(null);
      void queryClient.invalidateQueries({
        queryKey: ["comments", slug, identifier, uuid],
      });
      void queryClient.invalidateQueries({
        queryKey: ["history", slug, identifier, uuid],
      });
    },
    onError: (e) => {
      setError(e instanceof ApiError ? e.message : "Failed to post comment");
    },
  });

  function onCommentSubmit(e: FormEvent) {
    e.preventDefault();
    const doc = textToTipTapDoc(commentDraft);
    if (!doc) return;
    commentMutation.mutate({
      content: doc,
      parent_id: replyTo,
    });
  }

  function onTitleSave() {
    const t = titleDraft.trim();
    if (!t || t === issue?.name) {
      setTitleEditing(false);
      return;
    }
    patchMutation.mutate({ name: t });
    setTitleEditing(false);
  }

  const comments = commentsQuery.data ?? [];
  const history = historyQuery.data ?? [];

  return (
    <div className="mx-auto w-full max-w-5xl p-6">
      <Link
        to={`/w/${slug}/p/${identifier}`}
        className="text-xs text-muted-foreground hover:underline"
      >
        ← Issues
      </Link>

      {error && (
        <Alert variant="destructive" className="mt-4">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {issueQuery.isPending ? (
        <div className="mt-4 space-y-3">
          <Skeleton className="h-10 w-2/3" />
          <Skeleton className="h-40 w-full" />
        </div>
      ) : !issue ? (
        <Alert variant="destructive" className="mt-4">
          <AlertDescription>Issue not found.</AlertDescription>
        </Alert>
      ) : (
        <div className="mt-2">
          <div className="mb-6 flex items-center gap-3">
            <Badge variant="outline" className="font-mono">
              {issue.display_id}
            </Badge>
            {stateById.get(issue.state_id) && (
              <StateBadge state={stateById.get(issue.state_id)!} />
            )}
          </div>

          {titleEditing ? (
            <div className="mb-6 flex gap-2">
              <Input
                value={titleDraft}
                onChange={(e) => setTitleDraft(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") onTitleSave();
                  if (e.key === "Escape") setTitleEditing(false);
                }}
                autoFocus
                className="text-2xl font-semibold"
              />
              <Button onClick={onTitleSave}>Save</Button>
            </div>
          ) : (
            <h1
              className="mb-6 cursor-pointer text-2xl font-semibold tracking-tight hover:underline"
              title="Click to edit"
              onClick={() => {
                setTitleDraft(issue.name);
                setTitleEditing(true);
              }}
            >
              {issue.name}
            </h1>
          )}

          <div className="grid gap-6 md:grid-cols-3">
            <div className="space-y-6 md:col-span-2">
              <DescriptionEditor
                issue={issue}
                saving={patchMutation.isPending}
                onSave={(doc) =>
                  patchMutation.mutate({ description: doc })
                }
              />

              {/* Comments */}
              <Card>
                <CardHeader className="pb-3">
                  <CardTitle className="flex items-center gap-2 text-base">
                    <MessageSquare className="h-4 w-4" />
                    Comments
                    {comments.length > 0 && (
                      <span className="text-sm font-normal text-muted-foreground">
                        {comments.length}
                      </span>
                    )}
                  </CardTitle>
                </CardHeader>
                <CardContent className="space-y-4">
                  {commentsQuery.isPending ? (
                    <div className="space-y-2">
                      <Skeleton className="h-16 w-full" />
                      <Skeleton className="h-16 w-full" />
                    </div>
                  ) : (
                    <div className="space-y-3">
                      {comments.map((c) => (
                        <CommentItem
                          key={c.id}
                          comment={c}
                          onReply={(id) => setReplyTo(id)}
                        />
                      ))}
                    </div>
                  )}
                  <form onSubmit={onCommentSubmit} className="space-y-2">
                    {replyTo && (
                      <div className="flex items-center gap-2 text-xs text-muted-foreground">
                        <span>Replying to a comment</span>
                        <button
                          type="button"
                          className="hover:underline"
                          onClick={() => setReplyTo(null)}
                        >
                          Cancel reply
                        </button>
                      </div>
                    )}
                    <Textarea
                      value={commentDraft}
                      onChange={(e) => setCommentDraft(e.target.value)}
                      placeholder="Write a comment…"
                      rows={3}
                    />
                    <Button
                      type="submit"
                      size="sm"
                      disabled={
                        commentMutation.isPending || !commentDraft.trim()
                      }
                    >
                      {commentMutation.isPending ? "Posting…" : "Comment"}
                    </Button>
                  </form>
                </CardContent>
              </Card>
            </div>

            {/* Sidebar */}
            <div className="space-y-4">
              <Card>
                <CardContent className="space-y-4 pt-6">
                  <div className="space-y-2">
                    <Label>State</Label>
                    <StatePicker
                      states={states}
                      value={issue.state_id}
                      onChange={(stateId) =>
                        patchMutation.mutate({ state_id: stateId })
                      }
                      disabled={patchMutation.isPending}
                    />
                  </div>
                  <div className="space-y-2">
                    <Label>Priority</Label>
                    <PriorityPicker
                      value={issue.priority}
                      onChange={(p) =>
                        patchMutation.mutate({ priority: p })
                      }
                      disabled={patchMutation.isPending}
                    />
                  </div>
                  <Separator />
                  <div className="space-y-2">
                    <Label>Assignees</Label>
                    <div className="flex flex-wrap gap-1.5">
                      {issue.assignees.map((a) => (
                        <Badge key={a.id} variant="secondary">
                          {a.name}
                        </Badge>
                      ))}
                    </div>
                    <AssigneePicker
                      members={membersQuery.data ?? null}
                      assigned={issue.assignees}
                      onToggle={(id, cur) =>
                        toggleRelation("assignees", id, cur)
                      }
                    />
                  </div>
                  <div className="space-y-2">
                    <Label>Labels</Label>
                    <div className="flex flex-wrap gap-1.5">
                      {issue.labels.map((l) => (
                        <Badge
                          key={l.id}
                          variant="secondary"
                          className="gap-1"
                        >
                          <span
                            className="h-2 w-2 rounded-full"
                            style={{ backgroundColor: l.color }}
                            aria-hidden
                          />
                          {l.name}
                        </Badge>
                      ))}
                    </div>
                    <LabelPicker
                      labels={labelsQuery.data ?? null}
                      applied={issue.labels}
                      onToggle={(id, cur) =>
                        toggleRelation("labels", id, cur)
                      }
                    />
                  </div>
                </CardContent>
              </Card>

              {/* History */}
              <Card>
                <CardHeader className="pb-3">
                  <CardTitle className="text-base">Activity</CardTitle>
                </CardHeader>
                <CardContent>
                  {historyQuery.isPending ? (
                    <div className="space-y-2">
                      <Skeleton className="h-8 w-full" />
                      <Skeleton className="h-8 w-full" />
                    </div>
                  ) : history.length === 0 ? (
                    <p className="text-sm text-muted-foreground">
                      No activity yet.
                    </p>
                  ) : (
                    <ul className="space-y-3">
                      {history.map((h) => (
                        <li
                          key={h.id}
                          className="text-sm"
                        >
                          <div className="flex items-baseline gap-1.5">
                            <span className="font-medium">
                              {actorName(h.actor)}
                            </span>
                            <span className="text-muted-foreground">
                              {fieldLabel(h.field)}
                            </span>
                          </div>
                          <div className="text-xs text-muted-foreground">
                            {fmtTime(h.created_at)}
                          </div>
                        </li>
                      ))}
                    </ul>
                  )}
                </CardContent>
              </Card>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
