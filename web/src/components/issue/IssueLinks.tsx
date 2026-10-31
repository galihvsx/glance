import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link2, Plus, X } from "lucide-react";
import { api, ApiError } from "../../lib/api";
import type { Issue, IssueListResult } from "../../lib/types";
import { Button } from "../ui/button";
import { Badge } from "../ui/badge";
import { Input } from "../ui/input";
import { Label } from "../ui/label";
import { Skeleton } from "../ui/skeleton";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../ui/dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "../ui/select";
import { ScrollArea } from "../ui/scroll-area";
import { cn } from "cn";
import { useOpenIssue } from "./SubIssues";
import { parseDisplayId } from "../../lib/displayId";
import {
  LINK_KINDS,
  cycleErrorMessage,
  formatCyclePath,
  groupLinks,
  linksTitle,
  otherIssueId,
  removeLinkMessage,
  useIssueLinkCycles,
  useIssueLinks,
  type IssueLink,
  type IssueLinkKind,
} from "./issueLinks";

const SEARCH_DEBOUNCE_MS = 250;
const SEARCH_PAGE_SIZE = 10;

/** One linked-issue row. The link endpoints only carry ids, so the
 *  other issue is fetched for its display id + title. A 404 (deleted
 *  or raced-away target) renders an honest stale placeholder instead
 *  of a broken row — the link itself stays removable. */
function LinkRow({
  projectBase,
  slug,
  identifier,
  currentId,
  link,
  onOpen,
  onRemove,
  removing,
}: {
  projectBase: string;
  slug: string;
  identifier: string;
  currentId: string;
  link: IssueLink;
  onOpen: (uuid: string) => void;
  onRemove: (linkId: string, title: string) => void;
  removing: boolean;
}) {
  const otherId = otherIssueId(link, currentId);
  const targetQuery = useQuery({
    queryKey: ["issue", slug, identifier, otherId],
    queryFn: () =>
      api.get<Issue>(
        `${projectBase}/issues/${encodeURIComponent(otherId)}`,
      ),
    // A stale ref is a display state, not a retry loop.
    retry: false,
  });

  if (targetQuery.isPending) {
    return (
      <li>
        <Skeleton className="h-8 w-full" />
      </li>
    );
  }

  const stale = targetQuery.isError || !targetQuery.data;
  const target = targetQuery.data;
  const title = target ? target.name : "Deleted or unavailable issue";
  const displayId = target ? target.display_id : null;

  return (
    <li className="group flex items-center gap-2 rounded-md px-2 py-1.5 hover:bg-accent">
      {stale ? (
        <span className="min-w-0 flex-1 truncate text-sm italic text-muted-foreground">
          {title}
        </span>
      ) : (
        <button
          type="button"
          onClick={() => onOpen(otherId)}
          className="flex min-w-0 flex-1 cursor-pointer items-center gap-2 text-left"
          title={`Open ${displayId}`}
        >
          <Badge variant="outline" className="shrink-0 font-mono text-[11px]">
            {displayId}
          </Badge>
          {/* Titles are React-escaped text — no HTML injection. */}
          <span className="min-w-0 flex-1 truncate text-sm">{title}</span>
        </button>
      )}
      <Button
        variant="ghost"
        size="sm"
        className="h-6 w-6 shrink-0 p-0 opacity-0 group-hover:opacity-100 focus-visible:opacity-100"
        disabled={removing}
        title={stale ? "Remove this stale link" : `Remove link to ${displayId}`}
        aria-label={
          stale
            ? "Remove stale link"
            : `Remove link to ${displayId}: ${title}`
        }
        onClick={() => onRemove(link.id, title)}
      >
        <X className="h-3.5 w-3.5" />
      </Button>
    </li>
  );
}

/** "Add link" dialog: debounced issue search (same contract as the
 *  parent picker: ?q=&per_page= on the issue list, current issue
 *  excluded) + link-type select. The created edge is always outgoing
 *  from this issue — "this issue BLOCKS the target", so the select
 *  offers only the three stored kinds, never "is blocked by". */
function AddLinkDialog({
  open,
  onOpenChange,
  slug,
  identifier,
  currentId,
  projectBase,
  onError,
  initialKind = "blocks",
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  slug: string;
  identifier: string;
  currentId: string;
  projectBase: string;
  onError: (msg: string | null) => void;
  /** C19T3: preselected kind when opened from the action toolbar. */
  initialKind?: IssueLinkKind;
}) {
  const [input, setInput] = useState("");
  const [debounced, setDebounced] = useState("");
  const [selected, setSelected] = useState<Issue | null>(null);
  const [kind, setKind] = useState<IssueLinkKind>(initialKind);
  const [dialogError, setDialogError] = useState<string | null>(null);
  const { addMutation } = useIssueLinks(slug, identifier, currentId);

  // Reset the dialog state every time it opens/closes (kind follows the
  // latest initialKind).
  useEffect(() => {
    if (open) {
      setKind(initialKind);
    } else {
      setInput("");
      setDebounced("");
      setSelected(null);
      setKind(initialKind);
      setDialogError(null);
    }
  }, [open, initialKind]);

  useEffect(() => {
    const t = setTimeout(
      () => setDebounced(input.trim()),
      SEARCH_DEBOUNCE_MS,
    );
    return () => clearTimeout(t);
  }, [input]);

  const searchQuery = useQuery({
    queryKey: ["link-target-search", slug, identifier, debounced],
    queryFn: () => {
      const p = new URLSearchParams();
      p.set("per_page", String(SEARCH_PAGE_SIZE));
      if (debounced) p.set("q", debounced);
      return api.get<IssueListResult>(`${projectBase}/issues?${p}`);
    },
    // Only search while the dialog is open — no wasted requests.
    enabled: open,
    staleTime: 30_000,
  });

  const results = (searchQuery.data?.results ?? []).filter(
    (i) => i.id !== currentId,
  );

  function submit() {
    if (!selected || addMutation.isPending) return;
    addMutation.mutate(
      { targetIssueId: selected.id, kind },
      {
        onSuccess: () => {
          onError(null);
          onOpenChange(false);
        },
        // Honest backend messages: "issue link already exists",
        // "issue cannot link to itself", "target issue is in another
        // project", 404 on a raced-away target, 403 for guests. A 409
        // with code issue_link_cycle renders the cycle path verbatim
        // (C94T2) through the same dialog error — no second channel.
        onError: (e) => {
          const msg =
            cycleErrorMessage(e) ??
            (e instanceof ApiError ? e.message : "Failed to add link");
          setDialogError(msg);
        },
      },
    );
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Link an issue</DialogTitle>
          <DialogDescription>
            Create a dependency edge from this issue to another issue in
            the same project.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="link-target-search">Target issue</Label>
            <Input
              id="link-target-search"
              value={input}
              onChange={(e) => setInput(e.target.value)}
              placeholder="Search issues…"
              className="h-9"
              autoFocus
              disabled={addMutation.isPending}
            />
            {selected ? (
              <div className="flex items-center gap-2 rounded-md border px-2 py-1.5">
                <Badge
                  variant="outline"
                  className="shrink-0 font-mono text-[11px]"
                >
                  {selected.display_id}
                </Badge>
                <span className="min-w-0 flex-1 truncate text-sm">
                  {selected.name}
                </span>
                <Button
                  variant="ghost"
                  size="sm"
                  className="h-6 w-6 p-0"
                  aria-label="Clear selected issue"
                  onClick={() => setSelected(null)}
                >
                  <X className="h-3.5 w-3.5" />
                </Button>
              </div>
            ) : searchQuery.isLoading ? (
              <div className="space-y-2">
                <Skeleton className="h-8 w-full" />
                <Skeleton className="h-8 w-full" />
              </div>
            ) : searchQuery.isError ? (
              <p className="text-sm text-destructive">
                Couldn't load issues. Try again.
              </p>
            ) : (
              <ScrollArea className="max-h-56 rounded-md border">
                {results.map((issue) => (
                  <button
                    key={issue.id}
                    type="button"
                    className="flex w-full items-center gap-2 px-2 py-1.5 text-left text-sm hover:bg-accent"
                    onClick={() => setSelected(issue)}
                  >
                    <Badge
                      variant="outline"
                      className="shrink-0 font-mono text-[11px]"
                    >
                      {issue.display_id}
                    </Badge>
                    <span className="min-w-0 flex-1 truncate">
                      {issue.name}
                    </span>
                  </button>
                ))}
                {results.length === 0 && (
                  <p className="p-2 text-sm text-muted-foreground">
                    {debounced ? "No issues match." : "No issues in this project."}
                  </p>
                )}
              </ScrollArea>
            )}
          </div>
          <div className="space-y-2">
            <Label htmlFor="link-kind">Link type</Label>
            <Select
              value={kind}
              onValueChange={(v) => setKind(v as IssueLinkKind)}
              disabled={addMutation.isPending}
            >
              <SelectTrigger id="link-kind" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {LINK_KINDS.map((k) => (
                  <SelectItem key={k.value} value={k.value}>
                    {k.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <p className="text-xs text-muted-foreground">
              This issue {LINK_KINDS.find((k) => k.value === kind)?.label.toLowerCase()}{" "}
              the selected issue.
            </p>
          </div>
          {dialogError && (
            <p role="alert" className="text-sm text-destructive">
              {dialogError}
            </p>
          )}
        </div>
        <DialogFooter>
          <Button
            variant="ghost"
            onClick={() => onOpenChange(false)}
            disabled={addMutation.isPending}
          >
            Cancel
          </Button>
          <Button onClick={submit} disabled={!selected || addMutation.isPending}>
            {addMutation.isPending ? "Adding…" : "Add link"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** One member of a reported cycle. The cycles endpoint carries display
 *  IDs only, so the member resolves to a UUID through the same
 *  ?sequence_id= lookup the command palette uses for display-ID jumps.
 *  Until (or unless) it resolves, the display ID still renders as plain
 *  text — the notice stays readable even when resolution fails. */
function CycleIssueLink({
  projectBase,
  slug,
  identifier,
  displayId,
  onOpen,
}: {
  projectBase: string;
  slug: string;
  identifier: string;
  displayId: string;
  onOpen: (uuid: string) => void;
}) {
  const parsed = parseDisplayId(displayId);
  const resolveQuery = useQuery({
    queryKey: ["issue-link-cycle-resolve", slug, identifier, displayId],
    queryFn: () =>
      api.get<IssueListResult>(
        `${projectBase}/issues?sequence_id=${parsed!.sequence}&per_page=1`,
      ),
    enabled: parsed !== null,
    retry: false,
  });

  const issue = resolveQuery.data?.results[0];
  const resolved =
    parsed !== null &&
    issue !== undefined &&
    issue.display_id.toUpperCase() === displayId.toUpperCase();
  if (!resolved) {
    return <span className="font-mono">{displayId}</span>;
  }
  return (
    <button
      type="button"
      onClick={() => onOpen(issue.id)}
      className="font-mono underline decoration-dotted underline-offset-2 hover:text-ink"
      title={`Open ${displayId}`}
    >
      {displayId}
    </button>
  );
}

/** Pre-existing `blocks` cycles in this project (C94T1 report, C94T2
 *  notice). Read-only: the notice deletes nothing — breaking a cycle
 *  stays a manual per-link delete below. A clean project (or a failed
 *  report) renders nothing: no notice, no new chrome. */
function CyclesNotice({
  slug,
  identifier,
  projectBase,
  compact,
}: {
  slug: string;
  identifier: string;
  projectBase: string;
  compact: boolean;
}) {
  const openIssue = useOpenIssue(slug, identifier, compact);
  const { cycles, isLoading, isError } = useIssueLinkCycles(slug, identifier);

  if (isLoading || isError || cycles.length === 0) return null;

  return (
    <div
      role="note"
      aria-label="Dependency cycles in this project"
      className="mb-2 rounded-md border border-amber-500/40 bg-amber-500/10 px-2 py-1.5"
    >
      <p className="text-[11px] text-ink-secondary">
        This project has dependency cycles: every issue in a cycle is
        blocked, directly or indirectly, by another issue in it, so none
        of them can complete. Remove one link in a cycle to break it,
        using the per-link delete below.
      </p>
      <ul className="mt-1 space-y-0.5">
        {cycles.map((cycle, i) => (
          <li
            key={i}
            aria-label={formatCyclePath(cycle)}
            className="text-[11px] text-ink"
          >
            {cycle.map((displayId, j) => (
              <span key={`${displayId}-${j}`}>
                {j > 0 && (
                  <span aria-hidden="true" className="text-ink-muted">
                    {" → "}
                  </span>
                )}
                <CycleIssueLink
                  projectBase={projectBase}
                  slug={slug}
                  identifier={identifier}
                  displayId={displayId}
                  onOpen={openIssue}
                />
              </span>
            ))}
          </li>
        ))}
      </ul>
    </div>
  );
}

/** "Linked issues" section for the issue detail page (and peek drawer):
 *  edges grouped by type, add dialog, per-link remove with confirm. */
export default function IssueLinks({
  slug,
  identifier,
  uuid,
  projectBase,
  compact = false,
  onError,
  /** C19T3: increment to open the add-link dialog from the detail action
   *  toolbar. */
  openSignal,
  /** Preselects the link kind when opened via openSignal. */
  openKind,
}: {
  slug: string;
  identifier: string;
  uuid: string;
  /** Project-scoped API base (without the /issues/{uuid} suffix). */
  projectBase: string;
  compact?: boolean;
  onError: (msg: string | null) => void;
  openSignal?: number;
  openKind?: IssueLinkKind;
}) {
  const openIssue = useOpenIssue(slug, identifier, compact);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [dialogKind, setDialogKind] = useState<IssueLinkKind>("blocks");
  const { links, isLoading, isError, refetch, removeMutation } = useIssueLinks(
    slug,
    identifier,
    uuid,
  );

  // C19T3: external "Add dependency / Add relation" trigger.
  useEffect(() => {
    if (openSignal) {
      if (openKind) setDialogKind(openKind);
      setDialogOpen(true);
    }
  }, [openSignal, openKind]);

  const groups = groupLinks(links, uuid);

  function removeLink(linkId: string, title: string) {
    if (!window.confirm(removeLinkMessage(title))) return;
    removeMutation.mutate(linkId, {
      onSuccess: () => onError(null),
      // A 404 here means the link was already gone (stale UI) — the
      // onSettled refresh already removed it; say so honestly.
      onError: (e) => {
        const msg =
          e instanceof ApiError ? e.message : "Failed to remove link";
        onError(
          e instanceof ApiError && e.status === 404
            ? `${msg}. The list has been refreshed.`
            : msg,
        );
      },
    });
  }

  return (
    <section aria-label="Linked issues" className="mt-[18px]">
      <h2 className="mb-[9px] flex items-center gap-2 text-[12px] leading-[1.5] font-semibold text-ink">
        <Link2 className="h-3.5 w-3.5 text-ink-muted" aria-hidden />
        {linksTitle(links.length)}
      </h2>
      <CyclesNotice
        slug={slug}
        identifier={identifier}
        projectBase={projectBase}
        compact={compact}
      />
      <div className="space-y-2">
        {isLoading ? (
          <div className="space-y-2">
            <Skeleton className="h-8 w-full" />
            <Skeleton className="h-8 w-full" />
          </div>
        ) : isError ? (
          <div className="flex items-center gap-2">
            <p className="text-[10px] text-accent-red">
              Couldn't load linked issues.
            </p>
            <button
              type="button"
              className="rounded-[4px] px-2 py-1 text-[10px] text-ink-secondary hover:bg-wash-hover hover:text-ink"
              onClick={() => void refetch()}
            >
              Retry
            </button>
          </div>
        ) : groups.length === 0 ? (
          <p className="text-[10px] text-ink-muted">
            No linked issues yet. Link issues that block, relate to, or
            duplicate this one.
          </p>
        ) : (
          groups.map((group) => (
            <div key={group.key} className="pt-1 first:pt-0">
              <h4
                className={cn(
                  "px-2 pb-1 text-[10px] font-medium text-ink-muted",
                )}
              >
                {group.label}
              </h4>
              <ul className="-mx-2">
                {group.links.map((link) => (
                  <LinkRow
                    key={link.id}
                    projectBase={projectBase}
                    slug={slug}
                    identifier={identifier}
                    currentId={uuid}
                    link={link}
                    onOpen={openIssue}
                    onRemove={removeLink}
                    removing={removeMutation.isPending}
                  />
                ))}
              </ul>
            </div>
          ))
        )}
        <button
          type="button"
          onClick={() => setDialogOpen(true)}
          className="mt-1 flex items-center gap-1.5 rounded-[4px] px-1 py-1 text-[10px] text-ink-muted transition-colors hover:bg-wash-hover hover:text-ink"
        >
          <Plus className="h-3 w-3" />
          Add link
        </button>
      </div>
      <AddLinkDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        slug={slug}
        identifier={identifier}
        currentId={uuid}
        projectBase={projectBase}
        onError={onError}
        initialKind={dialogKind}
      />
    </section>
  );
}
