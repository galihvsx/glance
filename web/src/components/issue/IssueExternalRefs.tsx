// Linked pull requests / commits (C62T1): the refs section on the
// issue detail view — the user surface for the backend-only GitHub
// PR/commit linking API (C17T0, POST/DELETE .../issues/{uuid}/refs).
//
// Each row shows the repo, PR number / short commit SHA, a state badge
// (Open/Merged/Closed — the webhook flips merged/closed), and opens the
// URL in a new tab. Refs ride on the issue detail response
// (issue.external_refs), so mutations invalidate the detail query;
// the service also announces issue.updated for cross-tab/collaborator
// freshness.
//
// Authz mirrors the backend: member (15)+ for link/unlink; guests see
// the list read-only. The server enforces all of it; the UI just hides
// what would 403 (same precedent as IssueLinkedPages).

import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  GitCommitHorizontal,
  GitPullRequest,
  Link2,
  Plus,
  X,
} from "lucide-react";
import { api, ApiError } from "../../lib/api";
import { usePageIssueLinkCanWrite } from "../../lib/pageIssueLinks";
import type { ExternalRef } from "../../lib/types";
import { Button } from "../ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../ui/dialog";
import { Input } from "../ui/input";
import { Label } from "../ui/label";
import { NativeSelect, NativeSelectOption } from "../ui/native-select";

/** Compact row label: "acme/repo#42" for PRs, "acme/repo@abc1234" for
 *  commits (7-char short SHA, the git/GitHub convention). */
function refRowLabel(ref: ExternalRef): string {
  return ref.ref_type === "pr"
    ? `${ref.repo}#${ref.ref_id}`
    : `${ref.repo}@${ref.ref_id.slice(0, 7)}`;
}

function stateLabel(state: string): string {
  switch (state) {
    case "merged":
      return "Merged";
    case "closed":
      return "Closed";
    default:
      return "Open";
  }
}

function stateDotClass(state: string): string {
  switch (state) {
    case "merged":
      return "bg-violet-500";
    case "closed":
      return "bg-ink-muted";
    default:
      return "bg-emerald-500";
  }
}

type RefType = "pr" | "commit";

interface LinkDraft {
  repo: string;
  refType: RefType;
  refId: string;
  url: string;
}

const EMPTY_DRAFT: LinkDraft = { repo: "", refType: "pr", refId: "", url: "" };

/** Client-side validation; the backend re-validates everything and its
 *  400 envelope message is shown verbatim for anything subtler. */
function validateDraft(d: LinkDraft): string | null {
  if (!d.repo.trim()) return "Repository is required.";
  if (!d.refId.trim()) return "PR number or commit SHA is required.";
  if (!d.url.trim()) return "URL is required.";
  const lower = d.url.trim().toLowerCase();
  if (!lower.startsWith("https://") && !lower.startsWith("http://"))
    return "URL must start with http(s)://.";
  return null;
}

/** "Link pull request or commit" dialog: repo (owner/name), type,
 *  PR number / commit SHA, URL. POSTs .../refs; 409 → "already
 *  linked", 400 → the backend's invalid-field message. */
function LinkRefDialog({
  open,
  onOpenChange,
  refsBase,
  issueKey,
  onError,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  refsBase: string;
  /** The issue-detail query key, invalidated after a successful link. */
  issueKey: unknown[];
  onError: (msg: string | null) => void;
}) {
  const queryClient = useQueryClient();
  const [draft, setDraft] = useState<LinkDraft>(EMPTY_DRAFT);
  const [clientError, setClientError] = useState<string | null>(null);
  const [dialogError, setDialogError] = useState<string | null>(null);

  // Reset the form when the dialog closes (handled here rather than in
  // an effect: open is fully controlled through this handler).
  function handleOpenChange(next: boolean) {
    if (!next) {
      setDraft(EMPTY_DRAFT);
      setClientError(null);
      setDialogError(null);
    }
    onOpenChange(next);
  }

  const linkMutation = useMutation({
    mutationFn: (body: {
      provider: string;
      repo: string;
      ref_type: RefType;
      ref_id: string;
      url: string;
    }) => api.post<ExternalRef>(refsBase, body),
    onSuccess: () => {
      onError(null);
      void queryClient.invalidateQueries({ queryKey: issueKey });
      handleOpenChange(false);
    },
    onError: (e) => {
      // Honest backend messages: 409 = duplicate link (stays open so
      // the user can adjust); 400 = the envelope's invalid-field
      // message verbatim; 403 = viewer (who never sees this dialog).
      setDialogError(
        e instanceof ApiError && e.status === 409
          ? "This pull request or commit is already linked to this issue."
          : e instanceof ApiError
            ? e.message
            : "Failed to link.",
      );
    },
  });

  function submit() {
    const err = validateDraft(draft);
    if (err) {
      setClientError(err);
      return;
    }
    setClientError(null);
    setDialogError(null);
    linkMutation.mutate({
      provider: "github",
      repo: draft.repo.trim(),
      ref_type: draft.refType,
      ref_id: draft.refId.trim(),
      url: draft.url.trim(),
    });
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Link pull request or commit</DialogTitle>
          <DialogDescription>
            Link a GitHub pull request or commit to this issue.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="link-ref-repo">Repository</Label>
            <Input
              id="link-ref-repo"
              value={draft.repo}
              onChange={(e) =>
                setDraft({ ...draft, repo: e.target.value })
              }
              placeholder="owner/name"
              className="h-9"
              autoFocus
              disabled={linkMutation.isPending}
            />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-2">
              <Label htmlFor="link-ref-type">Type</Label>
              <NativeSelect
                id="link-ref-type"
                className="w-full"
                value={draft.refType}
                onChange={(e) =>
                  setDraft({
                    ...draft,
                    refType: e.target.value as RefType,
                  })
                }
                disabled={linkMutation.isPending}
              >
                <NativeSelectOption value="pr">
                  Pull request
                </NativeSelectOption>
                <NativeSelectOption value="commit">Commit</NativeSelectOption>
              </NativeSelect>
            </div>
            <div className="space-y-2">
              <Label htmlFor="link-ref-id">PR number or commit SHA</Label>
              <Input
                id="link-ref-id"
                value={draft.refId}
                onChange={(e) =>
                  setDraft({ ...draft, refId: e.target.value })
                }
                placeholder={draft.refType === "pr" ? "e.g. 42" : "e.g. abc1234…"}
                className="h-9"
                disabled={linkMutation.isPending}
              />
            </div>
          </div>
          <div className="space-y-2">
            <Label htmlFor="link-ref-url">URL</Label>
            <Input
              id="link-ref-url"
              value={draft.url}
              onChange={(e) =>
                setDraft({ ...draft, url: e.target.value })
              }
              placeholder="https://github.com/owner/repo/pull/42"
              className="h-9"
              disabled={linkMutation.isPending}
            />
          </div>
          {(clientError ?? dialogError) && (
            <p role="alert" className="text-sm text-destructive">
              {clientError ?? dialogError}
            </p>
          )}
        </div>
        <DialogFooter>
          <Button
            variant="ghost"
            onClick={() => handleOpenChange(false)}
            disabled={linkMutation.isPending}
          >
            Cancel
          </Button>
          <Button onClick={submit} disabled={linkMutation.isPending}>
            {linkMutation.isPending ? "Linking…" : "Link"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** "Linked pull requests / commits" section for the issue detail page
 *  (and peek drawer): linked refs, link dialog, per-ref remove with
 *  confirm. */
export default function IssueExternalRefs({
  slug,
  identifier,
  uuid,
  refs,
  issueKey,
  onError,
}: {
  slug: string;
  identifier: string;
  uuid: string;
  refs: ExternalRef[];
  /** The issue-detail query key, invalidated after link/unlink. */
  issueKey: unknown[];
  onError: (msg: string | null) => void;
}) {
  const queryClient = useQueryClient();
  const canWrite = usePageIssueLinkCanWrite(slug);
  const base = `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`;
  const refsBase = `${base}/issues/${encodeURIComponent(uuid)}/refs`;
  const [dialogOpen, setDialogOpen] = useState(false);

  const removeMutation = useMutation({
    mutationFn: (refId: string) =>
      api.del(`${refsBase}/${encodeURIComponent(refId)}`),
    onSuccess: () => {
      onError(null);
      void queryClient.invalidateQueries({ queryKey: issueKey });
    },
    // Always refresh after settle: a 404 means the ref was already
    // gone (stale UI) and the refresh removes it — the message below
    // says so.
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: issueKey });
    },
    onError: (e) => {
      const msg =
        e instanceof ApiError ? e.message : "Failed to unlink";
      onError(
        e instanceof ApiError && e.status === 404
          ? `${msg}. The list has been refreshed.`
          : msg,
      );
    },
  });

  function removeRef(ref: ExternalRef) {
    const noun = ref.ref_type === "pr" ? "pull request" : "commit";
    if (!window.confirm(`Unlink ${noun} "${refRowLabel(ref)}" from this issue?`))
      return;
    removeMutation.mutate(ref.id);
  }

  return (
    <section aria-label="Linked pull requests / commits" className="mt-[18px]">
      <h2 className="mb-[9px] flex items-center gap-2 text-[12px] leading-[1.5] font-semibold text-ink">
        <Link2 className="h-3.5 w-3.5 text-ink-muted" aria-hidden />
        Linked pull requests / commits
      </h2>
      <div className="space-y-2">
        {refs.length === 0 ? (
          <p className="text-[10px] text-ink-muted">
            No linked pull requests or commits yet.
          </p>
        ) : (
          <ul className="-mx-2">
            {refs.map((ref) => (
              <li
                key={ref.id}
                className="group flex items-center gap-2 rounded-md px-2 py-1.5 hover:bg-accent"
              >
                {ref.ref_type === "pr" ? (
                  <GitPullRequest
                    className="h-3.5 w-3.5 shrink-0 text-ink-muted"
                    aria-hidden
                  />
                ) : (
                  <GitCommitHorizontal
                    className="h-3.5 w-3.5 shrink-0 text-ink-muted"
                    aria-hidden
                  />
                )}
                <a
                  href={ref.url}
                  target="_blank"
                  rel="noreferrer"
                  className="min-w-0 flex-1 truncate text-sm text-ink-secondary hover:text-ink hover:underline"
                  title={`Open ${refRowLabel(ref)} on GitHub`}
                >
                  {refRowLabel(ref)}
                </a>
                <span
                  className="inline-flex shrink-0 items-center gap-1 rounded-full border border-border-hairline px-1.5 py-px text-[9px] text-ink-secondary"
                  title={`State: ${ref.state}`}
                >
                  <span
                    className={`h-1.5 w-1.5 rounded-full ${stateDotClass(ref.state)}`}
                    aria-hidden
                  />
                  {stateLabel(ref.state)}
                </span>
                {canWrite && (
                  <Button
                    variant="ghost"
                    size="sm"
                    className="h-6 w-6 shrink-0 p-0 opacity-0 group-hover:opacity-100 focus-visible:opacity-100"
                    disabled={removeMutation.isPending}
                    title={`Unlink ${ref.ref_type === "pr" ? "pull request" : "commit"} "${refRowLabel(ref)}"`}
                    aria-label={`Unlink ${ref.ref_type === "pr" ? "pull request" : "commit"} "${refRowLabel(ref)}"`}
                    onClick={() => removeRef(ref)}
                  >
                    <X className="h-3.5 w-3.5" />
                  </Button>
                )}
              </li>
            ))}
          </ul>
        )}
        {canWrite && (
          <button
            type="button"
            onClick={() => setDialogOpen(true)}
            className="mt-1 flex items-center gap-1.5 rounded-[4px] px-1 py-1 text-[10px] text-ink-muted transition-colors hover:bg-wash-hover hover:text-ink"
          >
            <Plus className="h-3 w-3" />
            Link pull request or commit
          </button>
        )}
      </div>
      <LinkRefDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        refsBase={refsBase}
        issueKey={issueKey}
        onError={onError}
      />
    </section>
  );
}
