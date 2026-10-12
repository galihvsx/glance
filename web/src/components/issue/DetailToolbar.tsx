import {
  Bell,
  BellOff,
  Copy,
  CornerDownRight,
  CornerUpLeft,
  ExternalLink,
  FolderInput,
  GitBranch,
  Hash,
  Link2,
  Maximize2,
  Columns2,
  MoreVertical,
  Share2,
  Star,
  StarOff,
  X,
} from "lucide-react";
import type { ReactNode } from "react";
import { buildBranchName } from "../../lib/branchName";
import { copyText } from "../../lib/clipboard";
import type { Issue } from "../../lib/types";
import { toast } from "../ui/toast";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "../ui/dropdown-menu";
import { cn } from "cn";
import VoteControl from "./VoteControl";

function IconBtn({
  title,
  ariaLabel,
  onClick,
  children,
}: {
  title: string;
  ariaLabel?: string;
  onClick?: () => void;
  children: ReactNode;
}) {
  return (
    <button
      type="button"
      title={title}
      aria-label={ariaLabel ?? title}
      onClick={onClick}
      className="flex h-7 w-7 items-center justify-center rounded-[5px] text-ink-muted transition-colors hover:bg-wash-hover hover:text-ink"
    >
      {children}
    </button>
  );
}

/**
 * The 46px issue-detail toolbar (C19T3), spec-exact: bottom border,
 * padding 0 10px, gap 3px. LEFT: close (X), open full screen,
 * toggle layout — 28px icon buttons. RIGHT (margin-left auto, gap 3px):
 * vote control, Subscribe, Copy branch name, Copy issue reference,
 * More.
 */
export default function DetailToolbar({
  slug,
  identifier,
  uuid,
  issue,
  onClose,
  onOpenFull,
  wide,
  onToggleWide,
  subscribed,
  subscribePending,
  canSubscribe,
  onSubscribeToggle,
  isFavorite,
  onToggleFavorite,
  cloning,
  onClone,
  onMove,
  hasParent,
  hasChildren,
  converting,
  onConvertToSubIssue,
  onConvertToIssue,
  onCopyLink,
  onShare,
}: {
  slug: string;
  identifier: string;
  uuid: string;
  issue: Issue;
  onClose: () => void;
  onOpenFull: () => void;
  /** Layout toggle: wide content vs the centered 860px column. */
  wide: boolean;
  onToggleWide: () => void;
  subscribed: boolean;
  subscribePending: boolean;
  canSubscribe: boolean;
  onSubscribeToggle: () => void;
  isFavorite: boolean;
  onToggleFavorite: () => void;
  cloning: boolean;
  onClone: () => void;
  onMove: () => void;
  /** C24T1: convert menu. hasChildren disables the demote item (the
   *  backend 400s when the issue has sub-issues of its own). */
  hasParent: boolean;
  hasChildren: boolean;
  converting: boolean;
  onConvertToSubIssue: () => void;
  onConvertToIssue: () => void;
  onCopyLink: () => void;
  onShare: () => void;
}) {
  async function doCopy(kind: "branch" | "ref") {
    const value =
      kind === "branch"
        ? buildBranchName(issue.display_id, issue.name)
        : issue.display_id;
    const label = kind === "branch" ? "Branch name" : "Issue reference";
    if (await copyText(value)) {
      toast.add({ title: `${label} copied`, description: value, type: "success" });
    } else {
      toast.add({ title: `Could not copy ${label.toLowerCase()}`, type: "error" });
    }
  }

  return (
    <div className="flex h-[46px] flex-none items-center gap-[3px] border-b border-border px-[10px]">
      <IconBtn title="Close" onClick={onClose}>
        <X className="h-[14px] w-[14px]" strokeWidth={1.8} />
      </IconBtn>
      <IconBtn title="Open full screen" onClick={onOpenFull}>
        <ExternalLink className="h-[14px] w-[14px]" strokeWidth={1.8} />
      </IconBtn>
      <IconBtn
        title={wide ? "Narrow layout" : "Wide layout"}
        ariaLabel="Toggle layout"
        onClick={onToggleWide}
      >
        {wide ? (
          <Maximize2 className="h-[14px] w-[14px]" strokeWidth={1.8} />
        ) : (
          <Columns2 className="h-[14px] w-[14px]" strokeWidth={1.8} />
        )}
      </IconBtn>

      <div className="ml-auto flex items-center gap-[3px]">
        <VoteControl slug={slug} identifier={identifier} uuid={uuid} />
        <button
          type="button"
          onClick={onSubscribeToggle}
          disabled={!canSubscribe || subscribePending}
          title={
            subscribed
              ? "Stop receiving notifications for this issue"
              : "Get notified about updates to this issue"
          }
          className={cn(
            "flex h-[30px] items-center gap-[6px] rounded-[6px] border border-border bg-surface px-[10px] text-[10px] text-ink-secondary transition-colors hover:bg-raised hover:text-ink disabled:opacity-50",
            subscribed && "text-ink",
          )}
        >
          {subscribed ? (
            <BellOff className="h-[13px] w-[13px]" strokeWidth={1.8} />
          ) : (
            <Bell className="h-[13px] w-[13px]" strokeWidth={1.8} />
          )}
          {subscribed ? "Unsubscribe" : "Subscribe"}
        </button>
        <button
          type="button"
          onClick={() => void doCopy("branch")}
          title="Copy branch name"
          className="flex h-[30px] items-center gap-[6px] rounded-[6px] border border-border bg-surface px-[10px] text-[10px] text-ink-secondary transition-colors hover:bg-raised hover:text-ink"
        >
          <GitBranch className="h-[13px] w-[13px]" strokeWidth={1.8} />
          Copy branch name
        </button>
        <IconBtn title="Copy issue reference" onClick={() => void doCopy("ref")}>
          <Hash className="h-[14px] w-[14px]" strokeWidth={1.8} />
        </IconBtn>
        <DropdownMenu>
          <DropdownMenuTrigger
            className="flex h-7 w-7 items-center justify-center rounded-[5px] text-ink-muted transition-colors hover:bg-wash-hover hover:text-ink"
            title="More actions"
            aria-label="More issue actions"
          >
            <MoreVertical className="h-[14px] w-[14px]" strokeWidth={1.8} />
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onClick={onToggleFavorite}>
              {isFavorite ? (
                <StarOff className="h-3.5 w-3.5" />
              ) : (
                <Star className="h-3.5 w-3.5" />
              )}
              {isFavorite ? "Remove from favorites" : "Add to favorites"}
            </DropdownMenuItem>
            <DropdownMenuItem onClick={onCopyLink}>
              <Link2 className="h-3.5 w-3.5" />
              Copy issue URL
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => void doCopy("ref")}>
              <Copy className="h-3.5 w-3.5" />
              Copy issue reference
            </DropdownMenuItem>
            <DropdownMenuItem onClick={onShare}>
              <Share2 className="h-3.5 w-3.5" />
              Share publicly
            </DropdownMenuItem>
            <DropdownMenuItem onClick={onClone} disabled={cloning}>
              <Copy className="h-3.5 w-3.5" />
              {cloning ? "Cloning…" : "Clone issue"}
            </DropdownMenuItem>
            <DropdownMenuItem onClick={onMove}>
              <FolderInput className="h-3.5 w-3.5" />
              Move to project…
            </DropdownMenuItem>
            <DropdownMenuItem
              onClick={onConvertToSubIssue}
              disabled={hasChildren || converting}
              title={
                hasChildren ? "Detach sub-issues first" : "Convert to sub-issue…"
              }
            >
              <CornerDownRight className="h-3.5 w-3.5" />
              Convert to sub-issue…
            </DropdownMenuItem>
            {hasParent && (
              <DropdownMenuItem
                onClick={onConvertToIssue}
                disabled={converting}
              >
                <CornerUpLeft className="h-3.5 w-3.5" />
                Convert to issue
              </DropdownMenuItem>
            )}
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </div>
  );
}
