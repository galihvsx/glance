import { useEffect, useRef, useState, type FormEvent, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { EditorContent, useEditor } from "@tiptap/react";
import StarterKit from "@tiptap/starter-kit";
import {
  Bell,
  BellOff,
  Check,
  History as HistoryIcon,
  Link2,
  Pencil,
  Share2,
} from "lucide-react";
import { api, ApiError } from "../../lib/api";
import { useAuth } from "../../lib/auth";
import type {
  CommentNode,
  HistoryEntry,
  Issue,
  IssueState,
  Label as ProjectLabel,
  Member,
  Subscriber,
} from "../../lib/types";
import { tiptapText } from "../../lib/tiptap";
import {
  applyMention,
  filterMentionMembers,
  findMentionTrigger,
  mentionLabel,
  renderMentionSegments,
  type MentionableMember,
} from "../../lib/mentions";
import { useProjectRealtime } from "../../lib/realtime";
import { useRecordRecentIssue } from "./recents";
import { Button } from "../ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "../ui/card";
import { Badge } from "../ui/badge";
import { Input } from "../ui/input";
import { Label } from "../ui/label";
import { Textarea } from "../ui/textarea";
import { Skeleton } from "../ui/skeleton";
import { Alert, AlertDescription } from "../ui/alert";
import { Separator } from "../ui/separator";
import StateBadge from "./StateBadge";
import StatePicker from "./StatePicker";
import PriorityPicker from "./PriorityPicker";
import AssigneePicker from "./AssigneePicker";
import ShareModal from "../ShareModal";
import LabelPicker from "./LabelPicker";
import TimeTracker from "./TimeTracker";
import Attachments from "./Attachments";
import FavoriteStar from "../favorites/FavoriteStar";
import DraftWithAI from "./DraftWithAI";
import TriageSuggestions from "./TriageSuggestions";
import SubIssues, { ParentBreadcrumb } from "./SubIssues";
import IssueLinks from "./IssueLinks";
import IssueCustomFields from "./IssueCustomFields";

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

/** Human-friendly relative timestamp ("just now", "5m ago", "2h ago",
 *  "3d ago"); falls back to the absolute fmtTime for events older than
 *  30 days. */
function timeAgo(iso: string): string {
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) return fmtTime(iso);
  const s = Math.max(0, Math.floor((Date.now() - then) / 1000));
  if (s < 60) return "just now";
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  const d = Math.floor(h / 24);
  if (d < 30) return `${d}d ago`;
  return fmtTime(iso);
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
  slug,
  identifier,
  onSave,
  saving,
}: {
  issue: Issue;
  slug: string;
  identifier: string;
  onSave: (doc: unknown | null) => void;
  saving: boolean;
}) {
  const [editing, setEditing] = useState(false);
  // C5T3: honest "AI-generated" flag — set on a Draft-with-AI insert,
  // cleared when the user edits, saves, or cancels.
  const [aiGenerated, setAiGenerated] = useState(false);
  const [aiError, setAiError] = useState<string | null>(null);
  const editor = useEditor({
    extensions: [StarterKit],
    content: (issue.description as object) ?? { type: "doc", content: [] },
    editable: editing,
    onUpdate: () => setAiGenerated(false),
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
            {aiGenerated && (
              <p className="text-xs text-muted-foreground">
                AI-generated — review before saving.
              </p>
            )}
            {aiError && (
              <p role="alert" className="text-xs text-destructive">
                {aiError}
              </p>
            )}
            <div className="flex gap-2">
              <Button
                size="sm"
                disabled={saving}
                onClick={() => {
                  onSave(editor?.getJSON() ?? null);
                  setEditing(false);
                  setAiGenerated(false);
                }}
              >
                {saving ? "Saving…" : "Save"}
              </Button>
              <DraftWithAI
                slug={slug}
                identifier={identifier}
                title={issue.name}
                context={tiptapText(issue.description)}
                onDraft={(d) => {
                  editor?.commands.setContent(
                    textToTipTapDoc(d) as object,
                  );
                  setAiGenerated(true);
                  setAiError(null);
                }}
                onError={setAiError}
                disabled={saving}
              />
              <Button
                size="sm"
                variant="ghost"
                className="text-destructive hover:text-destructive"
                onClick={() => {
                  onSave(null);
                  setEditing(false);
                  setAiGenerated(false);
                  setAiError(null);
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

/** Renders comment text with @-mention chips (C8T1). */
function CommentBody({
  text,
  members,
}: {
  text: string;
  members: MentionableMember[];
}) {
  const segments = renderMentionSegments(text, members);
  return (
    <>
      {segments.map((s, i) =>
        s.kind === "mention" ? (
          <span
            key={i}
            className="rounded bg-primary/15 px-1 py-0.5 font-medium text-primary"
          >
            @{s.label}
          </span>
        ) : (
          <span key={i}>{s.text}</span>
        ),
      )}
    </>
  );
}

function CommentItem({
  comment,
  onReply,
  members,
}: {
  comment: CommentNode;
  onReply: (parentId: string) => void;
  members: MentionableMember[];
}) {
  return (
    <div className="space-y-2">
      <div className="rounded-md border p-3">
        <div className="mb-1 flex items-center gap-2 text-xs text-muted-foreground">
          <span className="font-medium text-foreground">
            {actorName(comment.actor)}
          </span>
          <span title={fmtTime(comment.created_at)}>
            {timeAgo(comment.created_at)}
          </span>
          <button
            className="ml-auto hover:underline"
            onClick={() => onReply(comment.id)}
          >
            Reply
          </button>
        </div>
        <p className="whitespace-pre-wrap text-sm">
          <CommentBody text={tiptapText(comment.content)} members={members} />
        </p>
      </div>
      {comment.replies.length > 0 && (
        <div className="space-y-2 border-l-2 pl-4">
          {comment.replies.map((r) => (
            <CommentItem
              key={r.id}
              comment={r}
              onReply={onReply}
              members={members}
            />
          ))}
        </div>
      )}
    </div>
  );
}

// ---------- Reusable detail content ----------

/**
 * The issue detail body, shared by the full page (IssueDetail) and the
 * peek drawer (PeekDrawer). Takes explicit ids instead of useParams so it
 * can render anywhere inside the project scope.
 *
 * `compact` stacks the sidebar below the main column (drawer width).
 * `headerActions` renders at the right of the display-id/state row.
 */
export default function IssueDetailContent({
  slug,
  identifier,
  uuid,
  compact = false,
  headerActions,
}: {
  slug: string;
  identifier: string;
  uuid: string;
  compact?: boolean;
  headerActions?: ReactNode;
}) {
  const queryClient = useQueryClient();
  const { user } = useAuth();
  const base = `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`;
  const issuePath = `${base}/issues/${encodeURIComponent(uuid)}`;
  const subscribersPath = `${issuePath}/subscribers`;
  const subscribersKey = ["subscribers", slug, identifier, uuid];

  const [titleEditing, setTitleEditing] = useState(false);
  const [titleDraft, setTitleDraft] = useState("");
  const [commentDraft, setCommentDraft] = useState("");
  const [replyTo, setReplyTo] = useState<string | null>(null);
  // @-mention autocomplete state (C8T1).
  const commentRef = useRef<HTMLTextAreaElement>(null);
  const [mentionTrigger, setMentionTrigger] = useState<{
    start: number;
    query: string;
  } | null>(null);
  const [mentionActive, setMentionActive] = useState(0);
  const [error, setError] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const [shareOpen, setShareOpen] = useState(false);

  const issueKey = ["issue", slug, identifier, uuid];
  const issuesKey = ["issues", slug, identifier];

  // Realtime: issue channel for this issue (comments, concurrent edits) on
  // top of the project channel from ProjectScope. Resync-on-reconnect is
  // owned by ProjectScope.
  useProjectRealtime({ slug, identifier, issueUuid: uuid });

  const issueQuery = useQuery({
    queryKey: issueKey,
    // C5T5: ?include_children=1 attaches the child summaries for the
    // Sub-issues section; C7T3: ?include_custom=1 attaches custom_values
    // for the Custom fields section (detail only — the list query is
    // untouched).
    queryFn: () =>
      api.get<Issue>(`${issuePath}?include_children=1&include_custom=1`),
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
  const subscribersQuery = useQuery({
    queryKey: subscribersKey,
    queryFn: () =>
      api
        .get<{ subscribers: Subscriber[] }>(subscribersPath)
        .then((d) => d.subscribers),
  });

  const issue = issueQuery.data;
  const states = statesQuery.data ?? [];
  const stateById = new Map(states.map((s) => [s.id, s]));

  // Recents (localStorage): every opened issue — full page or peek drawer —
  // lands here, feeding the dashboard's "Recents" section.
  useRecordRecentIssue(issue, slug, identifier);

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

  const subscribers = subscribersQuery.data ?? [];
  const subscribed =
    user !== null && subscribers.some((s) => s.user_id === user.id);

  // Subscribe/unsubscribe the current user. Backend is idempotent
  // (POST/DELETE .../subscribers → 204); optimistic so the button flips
  // instantly, rolled back on error.
  const subscribeMutation = useMutation({
    mutationFn: (next: boolean) =>
      next ? api.post<void>(subscribersPath) : api.del<void>(subscribersPath),
    onMutate: async (next) => {
      await queryClient.cancelQueries({ queryKey: subscribersKey });
      const prev =
        queryClient.getQueryData<Subscriber[]>(subscribersKey) ?? [];
      if (user) {
        queryClient.setQueryData<Subscriber[]>(
          subscribersKey,
          next
            ? [...prev, { user_id: user.id, name: user.name, email: user.email }]
            : prev.filter((s) => s.user_id !== user.id),
        );
      }
      return { prev };
    },
    onError: (e, _next, ctx) => {
      if (ctx) queryClient.setQueryData(subscribersKey, ctx.prev);
      setError(
        e instanceof ApiError ? e.message : "Failed to update subscription",
      );
    },
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: subscribersKey });
    },
  });

  function onSubscribeToggle() {
    if (!user || subscribeMutation.isPending) return;
    subscribeMutation.mutate(!subscribed);
  }

  // Copy the issue's canonical full-page URL.
  function onCopyLink() {
    const url = `${window.location.origin}/w/${slug}/p/${identifier}/i/${uuid}`;
    const done = () => {
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1600);
    };
    if (navigator.clipboard?.writeText) {
      void navigator.clipboard
        .writeText(url)
        .then(done)
        .catch(() => setError("Could not copy link to clipboard"));
    } else {
      // Non-secure context fallback.
      const ta = document.createElement("textarea");
      ta.value = url;
      ta.style.position = "fixed";
      ta.style.opacity = "0";
      document.body.appendChild(ta);
      ta.select();
      try {
        if (document.execCommand("copy")) done();
        else setError("Could not copy link to clipboard");
      } catch {
        setError("Could not copy link to clipboard");
      }
      document.body.removeChild(ta);
    }
  }

  function onCommentSubmit(e: FormEvent) {
    e.preventDefault();
    setMentionTrigger(null);
    const doc = textToTipTapDoc(commentDraft);
    if (!doc) return;
    commentMutation.mutate({
      content: doc,
      parent_id: replyTo,
    });
  }

  /** @-mention autocomplete (C8T1): tracks the @-token before the caret. */
  // The actor is excluded: self-mentions never notify (backend skips them).
  const mentionMembers = (membersQuery.data ?? []).filter(
    (m) => m.id !== user?.id,
  );
  const mentionList = mentionTrigger
    ? filterMentionMembers(mentionMembers, mentionTrigger.query)
    : [];

  function syncMentionTrigger(value: string, caret: number) {
    setMentionTrigger(findMentionTrigger(value, caret));
    setMentionActive(0);
  }

  function onCommentChange(e: React.ChangeEvent<HTMLTextAreaElement>) {
    const v = e.target.value;
    setCommentDraft(v);
    syncMentionTrigger(v, e.target.selectionStart ?? v.length);
  }

  function onCommentSelect(e: React.SyntheticEvent<HTMLTextAreaElement>) {
    const el = e.currentTarget;
    syncMentionTrigger(el.value, el.selectionStart ?? el.value.length);
  }

  function pickMention(m: MentionableMember) {
    if (!mentionTrigger) return;
    const el = commentRef.current;
    const caret = el?.selectionStart ?? commentDraft.length;
    const applied = applyMention(
      commentDraft,
      mentionTrigger.start,
      caret,
      m,
    );
    setCommentDraft(applied.text);
    setMentionTrigger(null);
    requestAnimationFrame(() => {
      el?.focus();
      el?.setSelectionRange(applied.caret, applied.caret);
    });
  }

  function onComposerKeyDown(e: React.KeyboardEvent<HTMLTextAreaElement>) {
    if (!mentionTrigger || mentionList.length === 0) return;
    if (e.key === "Escape") {
      setMentionTrigger(null);
      return;
    }
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setMentionActive((a) => (a + 1) % mentionList.length);
      return;
    }
    if (e.key === "ArrowUp") {
      e.preventDefault();
      setMentionActive(
        (a) => (a - 1 + mentionList.length) % mentionList.length,
      );
      return;
    }
    if (e.key === "Enter" || e.key === "Tab") {
      e.preventDefault();
      pickMention(mentionList[mentionActive] ?? mentionList[0]);
    }
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

  /** Unified activity timeline: top-level comments + history (system)
   *  events, interleaved newest-first. Replies stay nested under their
   *  parent comment. Relations aren't rendered on the detail page (no
   *  relations read here), so the timeline covers exactly what the page
   *  already fetches — nothing invented. */
  type TimelineItem =
    | { kind: "comment"; at: string; comment: CommentNode }
    | { kind: "event"; at: string; entry: HistoryEntry };
  const timeline: TimelineItem[] = [
    ...comments.map(
      (comment): TimelineItem => ({
        kind: "comment",
        at: comment.created_at,
        comment,
      }),
    ),
    ...history.map(
      (entry): TimelineItem => ({ kind: "event", at: entry.created_at, entry }),
    ),
  ].sort((a, b) => (a.at < b.at ? 1 : a.at > b.at ? -1 : 0));

  return (
    <div className="w-full">
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
          {/* C5T5: parent breadcrumb — "child of GLA-123". */}
          {issue.parent_id && (
            <ParentBreadcrumb
              slug={slug}
              identifier={identifier}
              parentId={issue.parent_id}
              compact={compact}
            />
          )}
          <div className="mb-6 flex items-center gap-3">
            <Badge variant="outline" className="font-mono">
              {issue.display_id}
            </Badge>
            {stateById.get(issue.state_id) && (
              <StateBadge state={stateById.get(issue.state_id)!} />
            )}
            <span className="ml-auto flex items-center gap-1">
              {/* C7T4: star toggle for this issue. */}
              <FavoriteStar type="issue" id={uuid} />
              <Button
                variant="ghost"
                size="sm"
                className="gap-1.5"
                onClick={onSubscribeToggle}
                disabled={!user || subscribeMutation.isPending}
                title={
                  subscribed
                    ? "Stop receiving notifications for this issue"
                    : "Get notified about updates to this issue"
                }
              >
                {subscribed ? (
                  <BellOff className="h-3.5 w-3.5" />
                ) : (
                  <Bell className="h-3.5 w-3.5" />
                )}
                {subscribed ? "Unsubscribe" : "Subscribe"}
              </Button>
              <Button
                variant="ghost"
                size="sm"
                className="gap-1.5"
                onClick={onCopyLink}
                title="Copy link to this issue"
              >
                {copied ? (
                  <Check className="h-3.5 w-3.5" />
                ) : (
                  <Link2 className="h-3.5 w-3.5" />
                )}
                {copied ? "Copied" : "Copy link"}
              </Button>
              <Button
                variant="ghost"
                size="sm"
                className="gap-1.5"
                onClick={() => setShareOpen(true)}
                title="Share publicly"
              >
                <Share2 className="h-3.5 w-3.5" />
                Share
              </Button>
              {headerActions}
            </span>
          </div>

          {titleEditing ? (
            <div className="mb-6 flex gap-2">
              <Input
                value={titleDraft}
                onChange={(e) => setTitleDraft(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") onTitleSave();
                  if (e.key === "Escape") {
                    setTitleEditing(false);
                    // Don't let the drawer's Escape-to-close fire when
                    // just cancelling the title edit.
                    e.stopPropagation();
                  }
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

          <div className={compact ? "grid gap-6" : "grid gap-6 md:grid-cols-3"}>
            <div
              className={compact ? "space-y-6" : "space-y-6 md:col-span-2"}
            >
              <DescriptionEditor
                issue={issue}
                slug={slug}
                identifier={identifier}
                saving={patchMutation.isPending}
                onSave={(doc) =>
                  patchMutation.mutate({ description: doc })
                }
              />

              {/* C5T5: sub-issues — child list, add, detach. */}
              <SubIssues
                slug={slug}
                identifier={identifier}
                issue={issue}
                issueKey={issueKey}
                compact={compact}
                onError={setError}
              />

              {/* C8T0: issue links — directed dependency edges, grouped by
                  type. Shares the detail/peek layout like SubIssues. */}
              <IssueLinks
                slug={slug}
                identifier={identifier}
                uuid={uuid}
                projectBase={base}
                compact={compact}
                onError={setError}
              />

              {/* Activity: comments + system events, one timeline */}
              <Card>
                <CardHeader className="pb-3">
                  <CardTitle className="flex items-center gap-2 text-base">
                    <HistoryIcon className="h-4 w-4" />
                    Activity
                    {timeline.length > 0 && (
                      <span className="text-sm font-normal text-muted-foreground">
                        {timeline.length}
                      </span>
                    )}
                  </CardTitle>
                </CardHeader>
                <CardContent className="space-y-4">
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
                    <div className="relative">
                      <Textarea
                        ref={commentRef}
                        value={commentDraft}
                        onChange={onCommentChange}
                        onSelect={onCommentSelect}
                        onKeyDown={onComposerKeyDown}
                        placeholder="Write a comment… (type @ to mention)"
                        rows={3}
                      />
                      {mentionTrigger && mentionList.length > 0 && (
                        <div
                          className="absolute top-full left-0 z-10 mt-1 max-h-56 w-64 overflow-auto rounded-md border bg-popover shadow-md"
                          role="listbox"
                          aria-label="Mention a member"
                        >
                          {mentionList.map((m, i) => (
                            <button
                              key={m.id}
                              type="button"
                              role="option"
                              aria-selected={i === mentionActive}
                              className={`flex w-full items-center gap-2 px-3 py-2 text-left text-sm ${
                                i === mentionActive
                                  ? "bg-accent text-accent-foreground"
                                  : ""
                              }`}
                              onMouseDown={(e) => {
                                e.preventDefault();
                                pickMention(m);
                              }}
                              onMouseEnter={() => setMentionActive(i)}
                            >
                              <span className="font-medium">
                                {mentionLabel(m)}
                              </span>
                              <span className="truncate text-xs text-muted-foreground">
                                {m.email}
                              </span>
                            </button>
                          ))}
                        </div>
                      )}
                    </div>
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
                  {commentsQuery.isPending || historyQuery.isPending ? (
                    <div className="space-y-2">
                      <Skeleton className="h-16 w-full" />
                      <Skeleton className="h-16 w-full" />
                    </div>
                  ) : timeline.length === 0 ? (
                    <p className="text-sm text-muted-foreground">
                      No activity yet.
                    </p>
                  ) : (
                    <ul className="space-y-3">
                      {timeline.map((item) =>
                        item.kind === "comment" ? (
                          <li key={item.comment.id}>
                            <CommentItem
                              comment={item.comment}
                              onReply={(id) => setReplyTo(id)}
                              members={mentionMembers}
                            />
                          </li>
                        ) : (
                          <li
                            key={item.entry.id}
                            className="flex items-start gap-2 text-sm"
                          >
                            <HistoryIcon
                              className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground"
                              aria-hidden
                            />
                            <div>
                              <span className="font-medium">
                                {actorName(item.entry.actor)}
                              </span>{" "}
                              <span className="text-muted-foreground">
                                {fieldLabel(item.entry.field)}
                              </span>
                              <div
                                className="text-xs text-muted-foreground"
                                title={fmtTime(item.entry.created_at)}
                              >
                                {timeAgo(item.entry.created_at)}
                              </div>
                            </div>
                          </li>
                        ),
                      )}
                    </ul>
                  )}
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
              {/* C7T3: custom fields — typed per-issue values, inline edit. */}
              <IssueCustomFields
                slug={slug}
                identifier={identifier}
                issue={issue}
                issueKey={issueKey}
              />
              {/* C5T3: AI triage suggestions — one-click apply per row. */}
              <TriageSuggestions
                slug={slug}
                identifier={identifier}
                issue={issue}
                states={states}
                labels={labelsQuery.data ?? []}
                onPatch={(p) => patchMutation.mutate(p)}
                onAddLabel={(id) => toggleRelation("labels", id, false)}
                disabled={patchMutation.isPending}
              />
              <TimeTracker slug={slug} identifier={identifier} uuid={uuid} />
              <Attachments slug={slug} identifier={identifier} uuid={uuid} />
            </div>
          </div>
        </div>
      )}
      {shareOpen && (
        <ShareModal
          resource={`/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}/issues/${encodeURIComponent(uuid)}/share`}
          onClose={() => setShareOpen(false)}
        />
      )}
    </div>
  );
}
