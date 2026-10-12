import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { EditorContent, useEditor } from "@tiptap/react";
import StarterKit from "@tiptap/starter-kit";
import {
  ArrowRightLeft,
  Link,
  Link2,
  Paperclip,
  Pencil,
  Plus,
} from "lucide-react";
import { api, ApiError } from "../../lib/api";
import { useAuth } from "../../lib/auth";
import type {
  Issue,
  IssueState,
  Label as ProjectLabel,
  Member,
  Subscriber,
} from "../../lib/types";
import { textToTipTapDoc, tiptapText } from "../../lib/tiptap";
import { useProjectRealtime } from "../../lib/realtime";
import { useRecordRecentIssue } from "./recents";
import { useIsStarred, useToggleFavorite } from "../../lib/favorites";
import { copyText } from "../../lib/clipboard";
import { toast } from "../ui/toast";
import { Input } from "../ui/input";
import { Skeleton } from "../ui/skeleton";
import { Alert, AlertDescription } from "../ui/alert";
import ShareModal from "../ShareModal";
import DraftWithAI from "./DraftWithAI";
import TriageSuggestions from "./TriageSuggestions";
import IssueSummary from "./IssueSummary";
import SubIssues from "./SubIssues";
import IssueLinks from "./IssueLinks";
import IssueCustomFields from "./IssueCustomFields";
import Attachments from "./Attachments";
import BlockerConfirmDialog from "./BlockerConfirmDialog";
import { useCloneIssue } from "./useCloneIssue";
import { useMoveIssue } from "./useMoveIssue";
import MoveIssueDialog from "./MoveIssueDialog";
import { useConvertIssue } from "./useConvertIssue";
import ConvertIssueDialog from "./ConvertIssueDialog";
import DetailToolbar from "./DetailToolbar";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "../ui/alert-dialog";
import PropertyStrip from "./PropertyStrip";
import IssueProperties from "./IssueProperties";
import ActivitySection, { type ActivityTab } from "./ActivitySection";
import { useTimeSummary } from "./TimeTracker";
import { cn } from "cn";

/** "Oct 10, 2026". */
function fmtDate(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleDateString("en-US", {
    month: "short",
    day: "numeric",
    year: "numeric",
  });
}

/** "Today at 11:18" / "Yesterday at 11:18" / "Oct 10, 11:18". */
function fmtEdited(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const now = new Date();
  const day = (x: Date) => `${x.getFullYear()}-${x.getMonth()}-${x.getDate()}`;
  const hm = d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  if (day(d) === day(now)) return `Today at ${hm}`;
  const y = new Date(now);
  y.setDate(y.getDate() - 1);
  if (day(d) === day(y)) return `Yesterday at ${hm}`;
  return `${d.toLocaleDateString("en-US", { month: "short", day: "numeric" })}, ${hm}`;
}

// ---------- Description ----------

const DESC_COLLAPSE_CHARS = 600;

function DescriptionSection({
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
  const [expanded, setExpanded] = useState(false);
  const [aiGenerated, setAiGenerated] = useState(false);
  const [aiError, setAiError] = useState<string | null>(null);
  const editor = useEditor({
    extensions: [StarterKit],
    content: (issue.description as object) ?? { type: "doc", content: [] },
    editable: editing,
    onUpdate: () => setAiGenerated(false),
  });

  useEffect(() => {
    if (!editor) return;
    editor.setEditable(editing);
    if (!editing) {
      editor.commands.setContent(
        (issue.description as object) ?? { type: "doc", content: [] },
      );
    }
  }, [editor, editing, issue.description]);

  const text = issue.description ? tiptapText(issue.description) : "";
  const empty = text === "";
  const long = text.length > DESC_COLLAPSE_CHARS;

  return (
    <div className="py-[22px] pb-[13px]">
      {editing ? (
        <div className="space-y-3">
          <div className="min-h-32 rounded-md border border-border p-3 focus-within:border-accent-blue">
            <EditorContent editor={editor} />
          </div>
          {aiGenerated && (
            <p className="text-[10px] text-ink-muted">
              AI-generated — review before saving.
            </p>
          )}
          {aiError && (
            <p role="alert" className="text-[10px] text-accent-red">
              {aiError}
            </p>
          )}
          <div className="flex gap-2">
            <button
              type="button"
              disabled={saving}
              onClick={() => {
                onSave(editor?.getJSON() ?? null);
                setEditing(false);
                setAiGenerated(false);
              }}
              className="flex h-[26px] items-center rounded-[5px] bg-accent-blue px-3 text-[11px] font-medium text-white hover:bg-accent-blue-hover disabled:opacity-50"
            >
              {saving ? "Saving…" : "Save"}
            </button>
            <DraftWithAI
              slug={slug}
              identifier={identifier}
              title={issue.name}
              context={text}
              onDraft={(d) => {
                editor?.commands.setContent(textToTipTapDoc(d) as object);
                setAiGenerated(true);
                setAiError(null);
              }}
              onError={setAiError}
              disabled={saving}
            />
            <button
              type="button"
              onClick={() => {
                onSave(null);
                setEditing(false);
                setAiGenerated(false);
                setAiError(null);
              }}
              className="flex h-[26px] items-center rounded-[5px] px-2 text-[11px] text-accent-red hover:bg-wash-hover"
            >
              Remove description
            </button>
          </div>
        </div>
      ) : empty ? (
        <button
          type="button"
          onClick={() => setEditing(true)}
          className="flex items-center gap-1.5 rounded-[4px] px-1 py-1 text-[11px] text-ink-muted transition-colors hover:bg-wash-hover hover:text-ink"
        >
          <Pencil className="h-3 w-3" />
          Add description
        </button>
      ) : (
        <>
          <div
            onClick={() => setEditing(true)}
            title="Click to edit"
            className={cn(
              "cursor-text text-[13px] leading-[22px] text-[#d2d2d6] [&_.tiptap]:outline-none",
              "[&_p]:mb-3 [&_p]:max-w-[72ch] [&_ul]:mb-3 [&_ol]:mb-3",
              "[&_code]:rounded-[4px] [&_code]:border [&_code]:border-border [&_code]:bg-raised [&_code]:px-1 [&_code]:font-mono [&_code]:text-[11px]",
              "[&_pre]:rounded-[6px] [&_pre]:border [&_pre]:border-border [&_pre]:bg-raised [&_pre]:p-3 [&_pre]:font-mono [&_pre]:text-[11px]",
              "[&_h1]:text-[15px] [&_h1]:font-semibold [&_h2]:text-[14px] [&_h2]:font-semibold [&_h3]:text-[13px] [&_h3]:font-semibold",
              !expanded && long && "max-h-[220px] overflow-hidden",
            )}
          >
            <EditorContent editor={editor} />
          </div>
          <div className="mt-2 flex items-center gap-[10px]">
            {long && (
              <button
                type="button"
                onClick={() => setExpanded((e) => !e)}
                className="rounded-[4px] px-1 py-0.5 text-[10px] text-ink-secondary transition-colors hover:bg-wash-hover hover:text-ink"
              >
                {expanded ? "Show less" : "Show all"}
              </button>
            )}
            <span className="text-[9px] text-ink-muted">
              Last edited {fmtEdited(issue.updated_at)}
            </span>
          </div>
        </>
      )}
    </div>
  );
}

// ---------- Detail content ----------

/**
 * The issue detail body, shared by the full page (IssueDetail) and the
 * peek drawer (PeekDrawer). Restructured per the cycle-19 prototype
 * (Screen 3): 46px toolbar, ID chip, 25px title + char counter, inline
 * property strip, description, action toolbar, Properties (Details +
 * Project structure), meta rows, activity tabs, composer.
 *
 * `onClose` wires the toolbar's close button (peek: close the drawer;
 * full page: navigate back). `onOpenFull` wires "open full screen"
 * (peek only — omitted on the full page, hiding the button).
 * `fillHeight` makes the body a flex column with its own scroll area
 * (peek panel); the full page lets the window scroll instead.
 */
export default function IssueDetailContent({
  slug,
  identifier,
  uuid,
  compact = false,
  onClose,
  onOpenFull,
  fillHeight = false,
}: {
  slug: string;
  identifier: string;
  uuid: string;
  compact?: boolean;
  onClose?: () => void;
  onOpenFull?: () => void;
  fillHeight?: boolean;
}) {
  const queryClient = useQueryClient();
  const { user } = useAuth();
  const base = `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`;
  const issuePath = `${base}/issues/${encodeURIComponent(uuid)}`;
  const subscribersPath = `${issuePath}/subscribers`;
  const subscribersKey = ["subscribers", slug, identifier, uuid];

  const [wide, setWide] = useState(false);
  const [activityTab, setActivityTab] = useState<ActivityTab>("all");
  const [titleEditing, setTitleEditing] = useState(false);
  const [titleDraft, setTitleDraft] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [blockerConfirm, setBlockerConfirm] = useState<{
    stateId: string;
    blockers: string[];
  } | null>(null);
  const [shareOpen, setShareOpen] = useState(false);
  // C19T3: action-toolbar signals for the sub-sections below.
  const [addSubSignal, setAddSubSignal] = useState(0);
  const [linkSignal, setLinkSignal] = useState(0);
  const [linkKind, setLinkKind] = useState<"blocks" | "relates_to">("blocks");
  const [pickSignal, setPickSignal] = useState(0);

  const {
    cloneIssue,
    cloning,
    error: cloneError,
  } = useCloneIssue(slug, identifier);
  const {
    moveIssue,
    moving: movePending,
    error: moveError,
  } = useMoveIssue(slug, identifier);
  const [moveOpen, setMoveOpen] = useState(false);
  // C24T1: convert issue <-> sub-issue.
  const {
    convertIssue,
    converting: convertPending,
    error: convertError,
  } = useConvertIssue(slug, identifier);
  const [convertOpen, setConvertOpen] = useState(false);
  const [promoteConfirm, setPromoteConfirm] = useState(false);
  const navigate = useNavigate();
  const isFavorite = useIsStarred("issue", uuid);
  const toggleFavorite = useToggleFavorite();

  const issueKey = ["issue", slug, identifier, uuid];
  const issuesKey = ["issues", slug, identifier];

  useProjectRealtime({ slug, identifier, issueUuid: uuid });

  const issueQuery = useQuery({
    queryKey: issueKey,
    // C5T5: ?include_children=1 attaches child summaries; C7T3:
    // ?include_custom=1 attaches custom values; C19T3:
    // ?include_project_structure=1 attaches cycle/module/release
    // membership (detail only — the list query is untouched).
    queryFn: () =>
      api.get<Issue>(
        `${issuePath}?include_children=1&include_custom=1&include_project_structure=1`,
      ),
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
  const subscribersQuery = useQuery({
    queryKey: subscribersKey,
    queryFn: () =>
      api
        .get<{ subscribers: Subscriber[] }>(subscribersPath)
        .then((d) => d.subscribers),
  });
  // Parent issue (for the Properties > Details > Parent cell).
  const parentQuery = useQuery({
    queryKey: ["issue", slug, identifier, issueQuery.data?.parent_id ?? "none"],
    queryFn: () =>
      api.get<Issue>(
        `${base}/issues/${encodeURIComponent(issueQuery.data!.parent_id!)}`,
      ),
    enabled: !!issueQuery.data?.parent_id,
    retry: false,
  });

  const issue = issueQuery.data;
  const states = statesQuery.data ?? [];
  const { totalSeconds: trackedSeconds } = useTimeSummary(slug, identifier, uuid);

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
          ...(body.start_date !== undefined
            ? { start_date: body.start_date as string | null }
            : {}),
          ...(body.target_date !== undefined
            ? { target_date: body.target_date as string | null }
            : {}),
          ...(body.release_id !== undefined
            ? {
                project_structure: {
                  ...(prev.project_structure ?? { modules: [] }),
                  release:
                    body.release_id === null
                      ? null
                      : prev.project_structure?.release?.id === body.release_id
                        ? prev.project_structure.release
                        : { id: body.release_id as string, name: "…" },
                },
              }
            : {}),
          ...(body.parent_id !== undefined
            ? { parent_id: body.parent_id as string | null }
            : {}),
        });
      }
      return { prev };
    },
    onError: (e, body, ctx) => {
      if (ctx?.prev) queryClient.setQueryData(issueKey, ctx.prev);
      // C16T3: the blocker guard rejected the move — show the confirm
      // dialog instead of the error banner.
      if (e instanceof ApiError && e.code === "open_blockers") {
        const details = e.details as { blockers?: string[] } | undefined;
        const blockers = Array.isArray(details?.blockers)
          ? details.blockers.filter((b): b is string => typeof b === "string")
          : [];
        const stateId = body.state_id;
        if (typeof stateId === "string") {
          setBlockerConfirm({ stateId, blockers });
          return;
        }
      }
      setError(e instanceof ApiError ? e.message : "Failed to update issue");
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
        setError(e instanceof ApiError ? e.message : "Failed to update");
      },
    );
  }

  const subscribers = subscribersQuery.data ?? [];
  const subscribed =
    user !== null && subscribers.some((s) => s.user_id === user.id);

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
      toast.add({ title: "Issue URL copied", description: url, type: "success" });
    };
    void copyText(url).then((ok) => {
      if (ok) done();
      else setError("Could not copy link to clipboard");
    });
  }

  // C24T0: move the issue to another project. The dialog's preview is
  // computed before confirm; here we run the move, toast the new
  // display ID with a View action, and navigate to the moved issue.
  async function confirmMove(targetIdentifier: string) {
    const fromDisplay = issue?.display_id ?? uuid;
    try {
      const res = await moveIssue(uuid, targetIdentifier);
      setMoveOpen(false);
      const dest = `/w/${encodeURIComponent(slug)}/p/${encodeURIComponent(targetIdentifier)}/i/${res.issue.id}`;
      toast.add({
        title: `Moved to ${res.new_display_id}`,
        description: `${fromDisplay} → ${res.new_display_id}`,
        type: "success",
        actionProps: {
          children: "View",
          onClick: () => navigate(dest),
        },
      });
      void queryClient.invalidateQueries({ queryKey: ["issues", slug, identifier] });
      navigate(dest);
    } catch {
      // moveError (from the hook) is shown inside the dialog; keep it
      // open so the user can retry or pick another project.
    }
  }

  // C24T1: convert the issue to a sub-issue of parentUuid, or back to a
  // standalone issue when parentUuid is null. Toasts the outcome and
  // refetches; the dialog's own error alert keeps failures visible for
  // retry, and the promote confirm stays open on failure.
  async function confirmConvert(parentUuid: string | null, parentDisplay?: string) {
    const fromDisplay = issue?.display_id ?? uuid;
    try {
      const res = await convertIssue(uuid, parentUuid);
      setConvertOpen(false);
      setPromoteConfirm(false);
      if (!res.converted) {
        toast.add({
          title: parentUuid
            ? `${fromDisplay} is already a sub-issue of ${parentDisplay ?? ""}`
            : `${fromDisplay} is already a standalone issue`,
          type: "success",
        });
      } else {
        toast.add({
          title: parentUuid
            ? `Converted to sub-issue of ${parentDisplay ?? res.issue.parent_id ?? ""}`
            : "Converted to issue",
          description: parentUuid
            ? `${fromDisplay} is now a sub-issue`
            : `${fromDisplay} is now a standalone issue`,
          type: "success",
        });
      }
      invalidateIssue();
    } catch (e) {
      // Demote failures surface in the dialog's error alert; keep it
      // open for retry. Promote has no error slot, so toast it and
      // close the confirm.
      if (parentUuid === null) {
        setPromoteConfirm(false);
        toast.add({
          title: "Could not convert to issue",
          description: e instanceof Error ? e.message : "Conversion failed",
          type: "error",
        });
      }
      // convertError (from the hook) is shown inside the convert dialog;
      // keep it open so the user can retry or pick another parent.
    }
  }

  function onCopyRef() {
    if (!issue) return;
    void copyText(issue.display_id).then((ok) => {
      if (ok)
        toast.add({
          title: "Issue reference copied",
          description: issue.display_id,
          type: "success",
        });
      else setError("Could not copy reference to clipboard");
    });
  }

  function onTitleSave() {
    const t = titleDraft.trim().slice(0, 255);
    if (!t || t === issue?.name) {
      setTitleEditing(false);
      return;
    }
    patchMutation.mutate({ name: t });
    setTitleEditing(false);
  }

  const members = membersQuery.data ?? [];
  const createdByName =
    members.find((m) => m.id === issue?.created_by)?.name ??
    members.find((m) => m.id === issue?.created_by)?.email ??
    issue?.created_by;

  const actionBtn =
    "flex h-7 items-center gap-[5px] rounded-[4px] px-2 text-[9px] text-ink-muted transition-colors hover:bg-wash-hover hover:text-ink";

  return (
    <div className={cn("flex w-full flex-col", fillHeight && "h-full min-h-0")}>
      {onClose && issue && (
        <DetailToolbar
          slug={slug}
          identifier={identifier}
          uuid={uuid}
          issue={issue}
          onClose={onClose}
          onOpenFull={onOpenFull ?? onClose}
          wide={wide}
          onToggleWide={() => setWide((w) => !w)}
          subscribed={subscribed}
          subscribePending={subscribeMutation.isPending}
          canSubscribe={user !== null}
          onSubscribeToggle={onSubscribeToggle}
          isFavorite={isFavorite}
          onToggleFavorite={() =>
            toggleFavorite.mutate({ type: "issue", id: uuid, starred: isFavorite })
          }
          cloning={cloning}
          onClone={() => void cloneIssue(uuid)}
          onMove={() => setMoveOpen(true)}
          hasParent={!!issue?.parent_id}
          hasChildren={(issue?.children?.length ?? 0) > 0}
          converting={convertPending}
          onConvertToSubIssue={() => setConvertOpen(true)}
          onConvertToIssue={() => setPromoteConfirm(true)}
          onCopyLink={onCopyLink}
          onShare={() => setShareOpen(true)}
        />
      )}

      <div className={cn(fillHeight && "min-h-0 flex-1 overflow-y-auto")}>
        <div
          className={cn(
            "px-[42px] pt-[27px] pb-[70px]",
            !wide && "mx-auto max-w-[860px]",
          )}
        >
          {cloneError && (
            <Alert variant="destructive" className="mb-4">
              <AlertDescription>{cloneError}</AlertDescription>
            </Alert>
          )}
          {error && (
            <Alert variant="destructive" className="mb-4">
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}

          {issueQuery.isPending ? (
            <div className="space-y-3">
              <Skeleton className="h-10 w-2/3" />
              <Skeleton className="h-40 w-full" />
            </div>
          ) : !issue ? (
            <Alert variant="destructive">
              <AlertDescription>Issue not found.</AlertDescription>
            </Alert>
          ) : (
            <>
              {/* ID chip */}
              <button
                type="button"
                onClick={onCopyRef}
                title="Copy issue reference"
                className="flex h-[25px] items-center rounded-[5px] border border-border bg-surface px-[7px] font-mono text-[10px] text-ink-secondary transition-colors hover:text-ink"
              >
                {issue.display_id}
              </button>

              {/* Title block */}
              <div className="relative mt-3">
                {titleEditing ? (
                  <div className="flex gap-2 pr-[62px]">
                    <Input
                      value={titleDraft}
                      maxLength={255}
                      onChange={(e) => setTitleDraft(e.target.value)}
                      onKeyDown={(e) => {
                        if (e.key === "Enter") onTitleSave();
                        if (e.key === "Escape") {
                          setTitleEditing(false);
                          e.stopPropagation();
                        }
                      }}
                      autoFocus
                      className="h-auto border-border bg-transparent py-0 text-[25px] leading-[34px] font-semibold tracking-[-0.025em]"
                    />
                    <button
                      type="button"
                      onClick={onTitleSave}
                      className="flex h-[26px] shrink-0 items-center rounded-[5px] bg-accent-blue px-3 text-[11px] font-medium text-white hover:bg-accent-blue-hover"
                    >
                      Save
                    </button>
                  </div>
                ) : (
                  <h1
                    className="cursor-text pr-[62px] text-[25px] leading-[34px] font-[660] tracking-[-0.025em] text-ink"
                    title="Click to edit"
                    onClick={() => {
                      setTitleDraft(issue.name);
                      setTitleEditing(true);
                    }}
                  >
                    {issue.name}
                  </h1>
                )}
                <span className="absolute right-0 bottom-1 font-mono text-[9px] text-ink-muted">
                  {issue.name.length}/255
                </span>
              </div>

              {/* Inline property strip */}
              <PropertyStrip
                issue={issue}
                states={states}
                members={membersQuery.data ?? null}
                onPatch={(b) => patchMutation.mutate(b)}
                onToggleAssignee={(id, cur) => toggleRelation("assignees", id, cur)}
                disabled={patchMutation.isPending}
              />

              {/* Description */}
              <DescriptionSection
                issue={issue}
                slug={slug}
                identifier={identifier}
                saving={patchMutation.isPending}
                onSave={(doc) => patchMutation.mutate({ description: doc })}
              />

              {/* AI summary (C25T0) — just below the description block */}
              <IssueSummary slug={slug} identifier={identifier} issue={issue} />

              {/* Action toolbar */}
              <div className="flex h-10 w-max max-w-full items-center gap-[3px] overflow-x-auto rounded-[7px] border border-border bg-surface px-[5px]">
                <button
                  type="button"
                  className={actionBtn}
                  onClick={() => setAddSubSignal((n) => n + 1)}
                >
                  <Plus className="h-[13px] w-[13px]" strokeWidth={1.8} />
                  Add sub-work item
                </button>
                <button
                  type="button"
                  className={actionBtn}
                  onClick={() => {
                    setLinkKind("blocks");
                    setLinkSignal((n) => n + 1);
                  }}
                >
                  <ArrowRightLeft className="h-[13px] w-[13px]" strokeWidth={1.8} />
                  Add dependency
                </button>
                <button
                  type="button"
                  className={actionBtn}
                  onClick={() => {
                    setLinkKind("relates_to");
                    setLinkSignal((n) => n + 1);
                  }}
                >
                  <Link2 className="h-[13px] w-[13px]" strokeWidth={1.8} />
                  Add relation
                </button>
                <button
                  type="button"
                  className={actionBtn}
                  onClick={() => setLinkSignal((n) => n + 1)}
                >
                  <Link className="h-[13px] w-[13px]" strokeWidth={1.8} />
                  Add link
                </button>
                <button
                  type="button"
                  className={actionBtn}
                  onClick={() => setPickSignal((n) => n + 1)}
                >
                  <Paperclip className="h-[13px] w-[13px]" strokeWidth={1.8} />
                  Attach
                </button>
              </div>

              {/* Sub-work items */}
              <SubIssues
                slug={slug}
                identifier={identifier}
                issue={issue}
                issueKey={issueKey}
                compact={compact}
                onError={setError}
                addSignal={addSubSignal}
              />

              {/* Dependencies / relations */}
              <IssueLinks
                slug={slug}
                identifier={identifier}
                uuid={uuid}
                projectBase={base}
                compact={compact}
                onError={setError}
                openSignal={linkSignal}
                openKind={linkKind}
              />

              {/* Attachments */}
              <Attachments
                slug={slug}
                identifier={identifier}
                uuid={uuid}
                pickSignal={pickSignal}
              />

              {/* AI triage suggestions */}
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

              {/* Custom fields */}
              <IssueCustomFields
                slug={slug}
                identifier={identifier}
                issue={issue}
                issueKey={issueKey}
              />

              {/* Properties */}
              <IssueProperties
                slug={slug}
                identifier={identifier}
                issue={issue}
                parentIssue={issue.parent_id ? (parentQuery.data ?? null) : null}
                labels={labelsQuery.data ?? []}
                structure={issue.project_structure}
                issueKey={issueKey}
                onPatch={(b) => patchMutation.mutate(b)}
                onToggleLabel={(id, cur) => toggleRelation("labels", id, cur)}
                trackedSeconds={trackedSeconds}
                onOpenWorklogs={() => setActivityTab("worklogs")}
                onError={setError}
                disabled={patchMutation.isPending}
              />

              {/* Meta rows */}
              <div className="mt-[18px] grid grid-cols-3 gap-3 border-y border-border-hairline py-[10px]">
                <div>
                  <span className="block text-[9px] text-ink-muted">Created by</span>
                  <span className="mt-[3px] block text-[10px] font-medium text-ink-secondary">
                    {createdByName}
                  </span>
                </div>
                <div>
                  <span className="block text-[9px] text-ink-muted">Created on</span>
                  <span className="mt-[3px] block text-[10px] font-medium text-ink-secondary">
                    {fmtDate(issue.created_at)}
                  </span>
                </div>
                <div>
                  <span className="block text-[9px] text-ink-muted">Updated on</span>
                  <span className="mt-[3px] block text-[10px] font-medium text-ink-secondary">
                    {fmtDate(issue.updated_at)}
                  </span>
                </div>
              </div>

              {/* Activity */}
              <ActivitySection
                slug={slug}
                identifier={identifier}
                uuid={uuid}
                userId={user?.id ?? null}
                states={states}
                activeTab={activityTab}
                onTabChange={setActivityTab}
                onError={setError}
              />
            </>
          )}
        </div>
      </div>

      {shareOpen && (
        <ShareModal
          resource={`/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}/issues/${encodeURIComponent(uuid)}/share`}
          onClose={() => setShareOpen(false)}
        />
      )}
      {issue && (
        <MoveIssueDialog
          open={moveOpen}
          onOpenChange={setMoveOpen}
          slug={slug}
          sourceIdentifier={identifier}
          issue={issue}
          sourceStates={states}
          onConfirm={(t) => void confirmMove(t)}
          confirming={movePending}
          error={moveError}
        />
      )}
      {issue && (
        <ConvertIssueDialog
          open={convertOpen}
          onOpenChange={setConvertOpen}
          slug={slug}
          identifier={identifier}
          issue={issue}
          onConfirm={(parent) => void confirmConvert(parent.id, parent.display_id)}
          confirming={convertPending}
          error={convertError}
        />
      )}
      {promoteConfirm && issue && (
        <AlertDialog
          open={promoteConfirm}
          onOpenChange={(o) => {
            if (!o) setPromoteConfirm(false);
          }}
        >
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>Convert to issue?</AlertDialogTitle>
              <AlertDialogDescription>
                <span className="font-mono font-medium text-foreground">
                  {issue.display_id}
                </span>{" "}
                becomes a standalone issue.
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel disabled={convertPending}>
                Cancel
              </AlertDialogCancel>
              <AlertDialogAction
                disabled={convertPending}
                onClick={() => void confirmConvert(null)}
              >
                {convertPending ? "Converting…" : "Convert to issue"}
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      )}
      {blockerConfirm && (
        <BlockerConfirmDialog
          open={blockerConfirm !== null}
          blockers={blockerConfirm.blockers}
          pending={patchMutation.isPending}
          onCancel={() => setBlockerConfirm(null)}
          onConfirm={() => {
            const bc = blockerConfirm;
            setBlockerConfirm(null);
            patchMutation.mutate({
              state_id: bc.stateId,
              ignore_blockers: true,
            });
          }}
        />
      )}
    </div>
  );
}
