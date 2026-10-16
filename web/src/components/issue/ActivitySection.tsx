import { useMemo, useRef, useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowDownUp,
  Bold,
  Check,
  History as HistoryIcon,
  Italic,
  Link2,
  Plus,
} from "lucide-react";
import { api } from "../../lib/api";
import { textToTipTapDoc, tiptapText } from "../../lib/tiptap";
import {
  applyMention,
  filterMentionMembers,
  findMentionTrigger,
  mentionLabel,
  renderMentionSegments,
  type MentionableMember,
} from "../../lib/mentions";
import type {
  CommentNode,
  HistoryEntry,
  IssueState,
  Member,
} from "../../lib/types";
import { Popover, PopoverContent, PopoverTrigger } from "../ui/popover";
import { Checkbox } from "../ui/checkbox";
import { Label } from "../ui/label";
import { Skeleton } from "../ui/skeleton";
import { cn } from "cn";
import TimeTracker from "./TimeTracker";

export type ActivityTab =
  | "all"
  | "activity"
  | "comments"
  | "updates"
  | "worklogs"
  | "transition"
  | "history";

const TABS: { id: ActivityTab; label: string }[] = [
  { id: "all", label: "All" },
  { id: "activity", label: "Activity" },
  { id: "comments", label: "Comments" },
  { id: "updates", label: "Updates" },
  { id: "worklogs", label: "Worklogs" },
  { id: "transition", label: "Transition" },
  { id: "history", label: "History" },
];

function actorName(a: { name?: string | null; email: string }): string {
  return a.name ?? a.email;
}

/** "Today at 10:24" / "Yesterday at 10:24" / "Oct 10, 10:24". */
function fmtFeedTime(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const now = new Date();
  const day = (x: Date) =>
    `${x.getFullYear()}-${x.getMonth()}-${x.getDate()}`;
  const hm = d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  if (day(d) === day(now)) return `Today at ${hm}`;
  const y = new Date(now);
  y.setDate(y.getDate() - 1);
  if (day(d) === day(y)) return `Yesterday at ${hm}`;
  return (
    d.toLocaleDateString("en-US", { month: "short", day: "numeric" }) + `, ${hm}`
  );
}

function jsonText(v: unknown): string {
  if (typeof v !== "string") return String(v ?? "");
  try {
    const p: unknown = JSON.parse(v);
    return typeof p === "string" ? p : String(p ?? "");
  } catch {
    return v;
  }
}

/** Human-readable event copy. State transitions resolve the state names
 *  ("moved this work item from Backlog to In Progress"); everything else
 *  falls back to the generic changed-X phrasing. */
function EventCopy({
  entry,
  states,
}: {
  entry: HistoryEntry;
  states: IssueState[];
}) {
  const byId = useMemo(
    () => new Map(states.map((s) => [s.id, s.name])),
    [states],
  );
  const actor = actorName(entry.actor);
  let body: React.ReactNode;
  if (entry.field === "_created") {
    body = (
      <>
        <Strong>{actor}</Strong> created this work item
      </>
    );
  } else if (entry.field === "_deleted") {
    body = (
      <>
        <Strong>{actor}</Strong> deleted this work item
      </>
    );
  } else if (entry.field === "_moved") {
    // C24T0: cross-project move. new_value is {from, to} display IDs.
    let from = "—";
    let to = "—";
    try {
      const v =
        typeof entry.new_value === "string"
          ? (JSON.parse(entry.new_value) as { from?: unknown; to?: unknown })
          : (entry.new_value as { from?: unknown; to?: unknown } | null);
      if (v) {
        from = String(v.from ?? "—");
        to = String(v.to ?? "—");
      }
    } catch {
      // keep the em-dash fallbacks
    }
    body = (
      <>
        <Strong>{actor}</Strong> moved this work item from <Strong>{from}</Strong>{" "}
        to <Strong>{to}</Strong>
      </>
    );
  } else if (entry.field === "_converted") {
    // C24T1: convert issue <-> sub-issue. new_value is
    // {action:"to_sub_issue", parent:"ENG-12"} on the demoted issue,
    // {action:"child_added", child:"ENG-13"} on the new parent, or
    // {action:"promoted"} on a promoted issue.
    let action = "";
    let ref = "";
    try {
      const v =
        typeof entry.new_value === "string"
          ? (JSON.parse(entry.new_value) as { action?: unknown; parent?: unknown; child?: unknown })
          : (entry.new_value as { action?: unknown; parent?: unknown; child?: unknown } | null);
      if (v) {
        action = String(v.action ?? "");
        ref = String(v.parent ?? v.child ?? "");
      }
    } catch {
      // keep the fallbacks
    }
    if (action === "child_added") {
      body = (
        <>
          <Strong>{actor}</Strong> converted <Strong>{ref || "—"}</Strong>{" "}
          to a sub-issue
        </>
      );
    } else if (action === "promoted") {
      body = (
        <>
          <Strong>{actor}</Strong> promoted this work item to a standalone
          issue
        </>
      );
    } else {
      body = (
        <>
          <Strong>{actor}</Strong> converted this work item to a sub-issue
          of <Strong>{ref || "—"}</Strong>
        </>
      );
    }
  } else if (entry.field === "state_id") {
    const from = byId.get(jsonText(entry.old_value)) ?? "—";
    const to = byId.get(jsonText(entry.new_value)) ?? "—";
    body = (
      <>
        <Strong>{actor}</Strong> moved this work item from{" "}
        <Strong>{from}</Strong> to <Strong>{to}</Strong>
      </>
    );
  } else if (entry.field === "priority") {
    const names = ["None", "Low", "Medium", "High", "Urgent"];
    const n = (v: unknown) => names[Number(jsonText(v))] ?? jsonText(v);
    body = (
      <>
        <Strong>{actor}</Strong> changed priority from{" "}
        <Strong>{n(entry.old_value)}</Strong> to{" "}
        <Strong>{n(entry.new_value)}</Strong>
      </>
    );
  } else {
    const nv = jsonText(entry.new_value);
    // The description's new_value is a TipTap JSON doc — never render it
    // raw ("[object Object]"); the change itself is the story.
    const showValue =
      entry.field !== "description" && entry.new_value != null && nv !== "";
    body = (
      <>
        <Strong>{actor}</Strong> changed{" "}
        {entry.field === "description" ? "the description" : entry.field.replace(/_/g, " ")}
        {showValue && (
          <>
            {" "}
            to <Strong>{nv}</Strong>
          </>
        )}
      </>
    );
  }
  return <p className="text-[10px] leading-[17px] text-ink-secondary">{body}</p>;
}

function Strong({ children }: { children: React.ReactNode }) {
  return <strong className="font-[570] text-ink">{children}</strong>;
}

/** Renders comment text with @-mention chips. */
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
            className="rounded bg-accent-blue/15 px-1 py-0.5 font-medium text-accent-blue"
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
  onToggleResolve,
  resolvingId,
  expanded = true,
  onToggleExpand,
  members,
  depth = 0,
}: {
  comment: CommentNode;
  onReply: (parentId: string) => void;
  /** Root-only resolve/reopen (C32). Undefined for reply subtrees. */
  onToggleResolve?: (comment: CommentNode) => void;
  resolvingId?: string | null;
  /** Resolved roots start collapsed; open threads are always expanded. */
  expanded?: boolean;
  onToggleExpand?: (id: string) => void;
  members: MentionableMember[];
  depth?: number;
}) {
  const initial = (actorName(comment.actor)[0] ?? "?").toUpperCase();
  const resolved = comment.resolved_at != null;
  // The header (author, badge, actions) always stays visible; only the
  // body + replies collapse, so Reopen stays one click away.
  const showBody = !resolved || expanded;
  const resolving = resolvingId === comment.id;
  return (
    <div className="grid grid-cols-[27px_1fr] gap-[9px] py-[17px]">
      <span
        className="flex h-[25px] w-[25px] items-center justify-center rounded-full border border-border bg-raised text-[10px] font-semibold text-ink-secondary"
        aria-hidden
      >
        {initial}
      </span>
      <div className="min-w-0">
        <div className="flex items-center gap-2">
          <span className="text-[10px] font-semibold text-ink">
            {actorName(comment.actor)}
          </span>
          <span className="text-[9px] text-ink-muted">
            {fmtFeedTime(comment.created_at)}
          </span>
          {resolved && comment.resolved_by && (
            <span className="rounded-full border border-border bg-raised px-1.5 py-px text-[9px] text-ink-secondary">
              Resolved by {actorName(comment.resolved_by)} ·{" "}
              {fmtFeedTime(comment.resolved_at as string)}
            </span>
          )}
          {depth === 0 && (
            <>
              {resolved && (
                <button
                  type="button"
                  onClick={() => onToggleExpand?.(comment.id)}
                  className="text-[9px] text-ink-muted hover:text-ink hover:underline"
                >
                  {expanded ? "Hide thread" : "Show thread"}
                </button>
              )}
              <button
                type="button"
                disabled={resolving}
                onClick={() => onToggleResolve?.(comment)}
                className="ml-auto text-[9px] text-ink-muted hover:text-ink hover:underline disabled:opacity-50"
              >
                {resolving
                  ? resolved
                    ? "Reopening…"
                    : "Resolving…"
                  : resolved
                    ? "Reopen"
                    : "Resolve"}
              </button>
              <button
                type="button"
                onClick={() => onReply(comment.id)}
                className="text-[9px] text-ink-muted hover:text-ink hover:underline"
              >
                Reply
              </button>
            </>
          )}
        </div>
        {showBody && (
          <>
            <p className="mt-1 text-[10px] leading-[17px] whitespace-pre-wrap text-ink-secondary">
              <CommentBody text={tiptapText(comment.content)} members={members} />
            </p>
            {comment.replies.length > 0 && (
              <div className="mt-1 border-l border-border-hairline pl-4">
                {comment.replies.map((r) => (
                  <CommentItem
                    key={r.id}
                    comment={r}
                    onReply={onReply}
                    onToggleResolve={onToggleResolve}
                    resolvingId={resolvingId}
                    expanded={expanded}
                    onToggleExpand={onToggleExpand}
                    members={members}
                    depth={depth + 1}
                  />
                ))}
              </div>
            )}
          </>
        )}
      </div>
    </div>
  );
}

function EventItem({ entry, states }: { entry: HistoryEntry; states: IssueState[] }) {
  return (
    <div className="grid grid-cols-[27px_1fr] gap-[9px] py-[17px]">
      <span
        className="flex h-[25px] w-[25px] items-center justify-center rounded-full border border-border bg-raised"
        aria-hidden
      >
        <HistoryIcon className="h-3 w-3 text-ink-muted" strokeWidth={1.8} />
      </span>
      <div className="min-w-0">
        <EventCopy entry={entry} states={states} />
        <p className="mt-0.5 text-[9px] text-ink-muted">{fmtFeedTime(entry.created_at)}</p>
      </div>
    </div>
  );
}

const actionBtn =
  "flex h-[25px] items-center gap-1 rounded-[6px] border border-border bg-surface px-[7px] text-[10px] text-ink-secondary transition-colors hover:bg-raised hover:text-ink";

/** Comment resolution filter (C32): applies to thread roots; a passing
 *  root carries its whole reply subtree. */
type ResolvedFilter = "all" | "open" | "resolved";

const RESOLVED_FILTERS: { id: ResolvedFilter; label: string }[] = [
  { id: "open", label: "Open" },
  { id: "resolved", label: "Resolved" },
  { id: "all", label: "All" },
];

/**
 * The activity section (C19T3): tab bar (All / Activity / Comments /
 * Updates / Worklogs / Transition / History) with Log work / Filters /
 * Sort actions, the feed, and the comment composer (Cmd/Ctrl+Enter to
 * submit, @-mentions, format buttons).
 */
export default function ActivitySection({
  slug,
  identifier,
  uuid,
  userId,
  states,
  activeTab,
  onTabChange,
  onError,
}: {
  slug: string;
  identifier: string;
  uuid: string;
  userId: string | null;
  states: IssueState[];
  activeTab: ActivityTab;
  onTabChange: (t: ActivityTab) => void;
  onError: (msg: string | null) => void;
}) {
  const queryClient = useQueryClient();
  const base = `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`;
  const issuePath = `${base}/issues/${encodeURIComponent(uuid)}`;

  const [sortDesc, setSortDesc] = useState(true);
  const [showComments, setShowComments] = useState(true);
  const [showEvents, setShowEvents] = useState(true);
  const [filtersOpen, setFiltersOpen] = useState(false);
  const [logSignal, setLogSignal] = useState(0);
  // C32: resolution filter + per-thread expand state. Resolved threads
  // start collapsed; open threads are always expanded.
  const [resolvedFilter, setResolvedFilter] = useState<ResolvedFilter>("all");
  const [expandedThreads, setExpandedThreads] = useState<Set<string>>(new Set());

  // Composer state.
  const [commentDraft, setCommentDraft] = useState("");
  const [replyTo, setReplyTo] = useState<string | null>(null);
  const [justAdded, setJustAdded] = useState(false);
  const commentRef = useRef<HTMLTextAreaElement>(null);
  const [mentionTrigger, setMentionTrigger] = useState<{
    start: number;
    query: string;
  } | null>(null);
  const [mentionActive, setMentionActive] = useState(0);

  const commentsQuery = useQuery({
    queryKey: ["comments", slug, identifier, uuid, resolvedFilter],
    queryFn: () =>
      api
        .get<{ comments: CommentNode[] }>(
          `${issuePath}/comments?resolved=${resolvedFilter}`,
        )
        .then((d) => d.comments),
  });
  const historyQuery = useQuery({
    queryKey: ["history", slug, identifier, uuid],
    queryFn: () =>
      api.get<{ history: HistoryEntry[] }>(`${issuePath}/history`).then((d) => d.history),
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

  const comments = commentsQuery.data ?? [];
  const history = historyQuery.data ?? [];
  const mentionMembers = (membersQuery.data ?? []).filter((m) => m.id !== userId);

  const commentMutation = useMutation({
    mutationFn: (body: { content: unknown; parent_id?: string | null }) =>
      api.post<CommentNode>(`${issuePath}/comments`, body),
    onSuccess: () => {
      setCommentDraft("");
      setReplyTo(null);
      setMentionTrigger(null);
      setJustAdded(true);
      window.setTimeout(() => setJustAdded(false), 1800);
      void queryClient.invalidateQueries({ queryKey: ["comments", slug, identifier, uuid] });
      void queryClient.invalidateQueries({ queryKey: ["history", slug, identifier, uuid] });
    },
    onError: (e) => {
      onError(e instanceof Error ? e.message : "Failed to post comment");
    },
  });

  // C32: resolve/reopen a thread root. Any workspace member may resolve
  // or reopen any thread (NOT author-only); replies are never resolvable
  // (the button only renders on roots). The refetch picks up the new
  // resolved state + the _comment_resolved/_comment_reopened activity
  // entry the backend writes.
  const resolveMutation = useMutation({
    mutationFn: ({ id, action }: { id: string; action: "resolve" | "reopen" }) =>
      api.post<CommentNode>(
        `${issuePath}/comments/${encodeURIComponent(id)}/${action}`,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["comments", slug, identifier, uuid] });
      void queryClient.invalidateQueries({ queryKey: ["history", slug, identifier, uuid] });
    },
    onError: (e) => {
      onError(e instanceof Error ? e.message : "Failed to update comment");
    },
  });

  function toggleResolve(comment: CommentNode) {
    if (resolveMutation.isPending) return;
    resolveMutation.mutate({
      id: comment.id,
      action: comment.resolved_at ? "reopen" : "resolve",
    });
  }

  function toggleExpand(id: string) {
    setExpandedThreads((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  type FeedItem =
    | { kind: "comment"; at: string; comment: CommentNode }
    | { kind: "event"; at: string; entry: HistoryEntry };

  const feed: FeedItem[] = useMemo(() => {
    let items: FeedItem[] = [];
    const cs: FeedItem[] = showComments
      ? comments.map((comment) => ({ kind: "comment" as const, at: comment.created_at, comment }))
      : [];
    const ev = (list: HistoryEntry[]): FeedItem[] =>
      showEvents ? list.map((entry) => ({ kind: "event" as const, at: entry.created_at, entry })) : [];
    switch (activeTab) {
      case "all":
        items = [...cs, ...ev(history)];
        break;
      case "activity":
        items = ev(history.filter((e) => e.field !== "_created"));
        break;
      case "comments":
        items = cs;
        break;
      case "updates":
        items = ev(history.filter((e) => e.field !== "_created" && e.field !== "_deleted" && e.field !== "state_id"));
        break;
      case "transition":
        items = ev(history.filter((e) => e.field === "state_id"));
        break;
      case "history":
        items = ev(history);
        break;
      case "worklogs":
        items = [];
        break;
    }
    items.sort((a, b) => {
      if (a.at === b.at) return 0;
      return sortDesc ? (a.at < b.at ? 1 : -1) : a.at < b.at ? -1 : 1;
    });
    return items;
  }, [activeTab, comments, history, showComments, showEvents, sortDesc]);

  function submitComment() {
    const doc = textToTipTapDoc(commentDraft);
    if (!doc || commentMutation.isPending) return;
    commentMutation.mutate({ content: doc, parent_id: replyTo });
  }

  function onCommentSubmit(e: FormEvent) {
    e.preventDefault();
    submitComment();
  }

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
    const applied = applyMention(commentDraft, mentionTrigger.start, caret, m);
    setCommentDraft(applied.text);
    setMentionTrigger(null);
    requestAnimationFrame(() => {
      el?.focus();
      el?.setSelectionRange(applied.caret, applied.caret);
    });
  }
  function wrapSelection(before: string, after: string) {
    const el = commentRef.current;
    if (!el) return;
    const s = el.selectionStart ?? 0;
    const e = el.selectionEnd ?? 0;
    const sel = commentDraft.slice(s, e) || "text";
    const next = commentDraft.slice(0, s) + before + sel + after + commentDraft.slice(e);
    setCommentDraft(next);
    requestAnimationFrame(() => {
      el.focus();
      el.setSelectionRange(s + before.length, s + before.length + sel.length);
    });
  }
  function onComposerKeyDown(e: React.KeyboardEvent<HTMLTextAreaElement>) {
    // Cmd/Ctrl+Enter submits — the prototype's headline behavior.
    if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
      e.preventDefault();
      submitComment();
      return;
    }
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
      setMentionActive((a) => (a - 1 + mentionList.length) % mentionList.length);
      return;
    }
    if (e.key === "Enter" || e.key === "Tab") {
      e.preventDefault();
      pickMention(mentionList[mentionActive] ?? mentionList[0]);
    }
  }

  const loading = commentsQuery.isPending || historyQuery.isPending;
  const commentCount = comments.length;
  // Open roots in the current view (the fetched, filter-applied list).
  // The count label is filter-aware: under the Resolved filter the list
  // holds only resolved roots, so "0 open threads" would misreport the
  // issue's open total — show the resolved count instead.
  const openThreadCount = comments.filter((c) => !c.resolved_at).length;
  const countLabel =
    resolvedFilter === "resolved"
      ? `${comments.length} resolved thread${comments.length === 1 ? "" : "s"}`
      : `${openThreadCount} open thread${openThreadCount === 1 ? "" : "s"}`;
  const showResolvedFilter = activeTab === "all" || activeTab === "comments";

  return (
    <section aria-label="Activity" className="mt-[27px]">
      <div className="flex items-end border-b border-border">
        <div className="flex" role="tablist" aria-label="Activity views">
          {TABS.map((t) => (
            <button
              key={t.id}
              type="button"
              role="tab"
              aria-selected={activeTab === t.id}
              onClick={() => onTabChange(t.id)}
              className={cn(
                "h-[34px] border-b-2 border-transparent px-2 text-[9px] text-ink-muted transition-colors hover:text-ink-secondary",
                activeTab === t.id && "border-ink text-ink",
              )}
            >
              {t.label}
              {t.id === "comments" && commentCount > 0 && (
                <span className="ml-1 font-mono text-[9px] text-ink-muted">{commentCount}</span>
              )}
            </button>
          ))}
        </div>
        <div className="ml-auto flex items-center gap-[3px] pb-[5px]">
          <button type="button" className={actionBtn} onClick={() => setLogSignal((n) => n + 1)}>
            Log work
          </button>
          <Popover open={filtersOpen} onOpenChange={setFiltersOpen}>
            <PopoverTrigger className={actionBtn}>Filters</PopoverTrigger>
            <PopoverContent className="w-48 p-2" align="end">
              <Label className="flex cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 text-xs hover:bg-wash-hover">
                <Checkbox
                  checked={showComments}
                  onCheckedChange={(v) => setShowComments(v === true)}
                />
                Comments
              </Label>
              <Label className="flex cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 text-xs hover:bg-wash-hover">
                <Checkbox checked={showEvents} onCheckedChange={(v) => setShowEvents(v === true)} />
                Activity events
              </Label>
            </PopoverContent>
          </Popover>
          <button
            type="button"
            className={actionBtn}
            title={sortDesc ? "Newest first" : "Oldest first"}
            onClick={() => setSortDesc((d) => !d)}
          >
            <ArrowDownUp className="h-3 w-3" />
            Sort
          </button>
        </div>
      </div>

      {activeTab === "worklogs" ? (
        <div className="pt-4">
          <TimeTracker slug={slug} identifier={identifier} uuid={uuid} logSignal={logSignal} />
        </div>
      ) : (
        <>
          {showResolvedFilter && (
            <div className="flex items-center gap-2 pt-3">
              <div
                role="group"
                aria-label="Filter comments by resolution"
                className="flex rounded-[6px] border border-border bg-surface p-[2px]"
              >
                {RESOLVED_FILTERS.map((f) => (
                  <button
                    key={f.id}
                    type="button"
                    aria-pressed={resolvedFilter === f.id}
                    onClick={() => setResolvedFilter(f.id)}
                    className={cn(
                      "rounded-[4px] px-2 py-1 text-[10px] transition-colors",
                      resolvedFilter === f.id
                        ? "bg-raised font-medium text-ink"
                        : "text-ink-muted hover:text-ink-secondary",
                    )}
                  >
                    {f.label}
                  </button>
                ))}
              </div>
              <span className="text-[10px] text-ink-muted">{countLabel}</span>
            </div>
          )}
          {loading ? (
            <div className="space-y-2 py-4">
              <Skeleton className="h-12 w-full" />
              <Skeleton className="h-12 w-full" />
            </div>
          ) : feed.length === 0 ? (
            <p className="py-6 text-[10px] text-ink-muted">No activity yet.</p>
          ) : (
            <div className="divide-y divide-border-hairline">
              {feed.map((item) =>
                item.kind === "comment" ? (
                  <CommentItem
                    key={item.comment.id}
                    comment={item.comment}
                    onReply={(id) => {
                      setReplyTo(id);
                      commentRef.current?.focus();
                    }}
                    onToggleResolve={toggleResolve}
                    resolvingId={
                      resolveMutation.isPending
                        ? (resolveMutation.variables?.id ?? null)
                        : null
                    }
                    expanded={
                      !item.comment.resolved_at ||
                      expandedThreads.has(item.comment.id)
                    }
                    onToggleExpand={toggleExpand}
                    members={mentionMembers}
                  />
                ) : (
                  <EventItem key={item.entry.id} entry={item.entry} states={states} />
                ),
              )}
            </div>
          )}

          {/* Comment composer */}
          <form onSubmit={onCommentSubmit} className="mt-2">
            {replyTo && (
              <div className="mb-2 flex items-center gap-2 text-[10px] text-ink-muted">
                <span>Replying to a comment</span>
                <button
                  type="button"
                  className="hover:text-ink hover:underline"
                  onClick={() => setReplyTo(null)}
                >
                  Cancel reply
                </button>
              </div>
            )}
            <div
              className="overflow-hidden rounded-[7px] border border-border bg-surface"
              onClick={() => {
                if (!commentDraft) commentRef.current?.focus();
              }}
            >
              <div className="relative">
                <textarea
                  ref={commentRef}
                  value={commentDraft}
                  onChange={onCommentChange}
                  onSelect={onCommentSelect}
                  onKeyDown={onComposerKeyDown}
                  placeholder="Add comment"
                  aria-label="Add comment"
                  className="min-h-[72px] w-full resize-y bg-transparent px-[11px] py-[10px] text-[11px] leading-relaxed text-ink outline-none placeholder:text-ink-muted"
                />
                {mentionTrigger && mentionList.length > 0 && (
                  <div
                    className="absolute top-full left-0 z-10 mt-1 max-h-56 w-64 overflow-auto rounded-md border border-border bg-raised shadow-lg"
                    role="listbox"
                    aria-label="Mention a member"
                  >
                    {mentionList.map((m, i) => (
                      <button
                        key={m.id}
                        type="button"
                        role="option"
                        aria-selected={i === mentionActive}
                        className={cn(
                          "flex w-full items-center gap-2 px-3 py-2 text-left text-sm",
                          i === mentionActive ? "bg-wash-active text-ink" : "text-ink-secondary",
                        )}
                        onMouseDown={(e) => {
                          e.preventDefault();
                          pickMention(m);
                        }}
                        onMouseEnter={() => setMentionActive(i)}
                      >
                        <span className="font-medium">{mentionLabel(m)}</span>
                        <span className="truncate text-xs text-ink-muted">{m.email}</span>
                      </button>
                    ))}
                  </div>
                )}
              </div>
              <div className="flex h-[36px] items-center gap-[3px] border-t border-border-hairline px-[6px]">
                <button
                  type="button"
                  title="Bold"
                  aria-label="Bold"
                  onClick={() => wrapSelection("**", "**")}
                  className="flex h-[25px] w-[25px] items-center justify-center rounded-[4px] text-ink-muted transition-colors hover:bg-wash-hover hover:text-ink"
                >
                  <Bold className="h-3 w-3" strokeWidth={2.2} />
                </button>
                <button
                  type="button"
                  title="Italic"
                  aria-label="Italic"
                  onClick={() => wrapSelection("_", "_")}
                  className="flex h-[25px] w-[25px] items-center justify-center rounded-[4px] text-ink-muted transition-colors hover:bg-wash-hover hover:text-ink"
                >
                  <Italic className="h-3 w-3" strokeWidth={2.2} />
                </button>
                <button
                  type="button"
                  title="Insert link"
                  aria-label="Insert link"
                  onClick={() => wrapSelection("[", "](https://)")}
                  className="flex h-[25px] w-[25px] items-center justify-center rounded-[4px] text-ink-muted transition-colors hover:bg-wash-hover hover:text-ink"
                >
                  <Link2 className="h-3 w-3" strokeWidth={2} />
                </button>
                <button
                  type="button"
                  title="Mention someone"
                  aria-label="Mention someone"
                  onClick={() => {
                    const el = commentRef.current;
                    const caret = el?.selectionStart ?? commentDraft.length;
                    const next = commentDraft.slice(0, caret) + "@" + commentDraft.slice(caret);
                    setCommentDraft(next);
                    requestAnimationFrame(() => {
                      el?.focus();
                      el?.setSelectionRange(caret + 1, caret + 1);
                      syncMentionTrigger(next, caret + 1);
                    });
                  }}
                  className="flex h-[25px] w-[25px] items-center justify-center rounded-[4px] text-ink-muted transition-colors hover:bg-wash-hover hover:text-ink"
                >
                  <Plus className="h-3 w-3" strokeWidth={2.2} />
                </button>
                <button
                  type="submit"
                  disabled={commentMutation.isPending || !commentDraft.trim() || justAdded}
                  className={cn(
                    "ml-auto flex h-[26px] items-center gap-1 rounded-[5px] bg-accent-blue px-3 text-[11px] font-medium text-white transition-colors hover:bg-accent-blue-hover disabled:cursor-not-allowed disabled:opacity-50",
                    justAdded && "bg-accent-green hover:bg-accent-green",
                  )}
                >
                  {justAdded && <Check className="h-3 w-3" strokeWidth={2.5} />}
                  {commentMutation.isPending ? "Posting…" : justAdded ? "Added" : "Comment"}
                </button>
              </div>
            </div>
          </form>
        </>
      )}
    </section>
  );
}
