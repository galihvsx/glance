import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link2, Plus, X } from "lucide-react";
import { api, ApiError } from "../../lib/api";
import type { Issue, IssueListResult } from "../../lib/types";
import { Button } from "../ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "../ui/card";
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
import {
  LINK_KINDS,
  groupLinks,
  linksTitle,
  otherIssueId,
  removeLinkMessage,
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
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  slug: string;
  identifier: string;
  currentId: string;
  projectBase: string;
  onError: (msg: string | null) => void;
}) {
  const [input, setInput] = useState("");
  const [debounced, setDebounced] = useState("");
  const [selected, setSelected] = useState<Issue | null>(null);
  const [kind, setKind] = useState<IssueLinkKind>("blocks");
  const [dialogError, setDialogError] = useState<string | null>(null);
  const { addMutation } = useIssueLinks(slug, identifier, currentId);

  // Reset the dialog state every time it closes.
  useEffect(() => {
    if (!open) {
      setInput("");
      setDebounced("");
      setSelected(null);
      setKind("blocks");
      setDialogError(null);
    }
  }, [open ]);

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
        // project", 404 on a raced-away target, 403 for guests.
        onError: (e) => {
          const msg =
            e instanceof ApiError ? e.message : "Failed to add link";
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

/** "Linked issues" section for the issue detail page (and peek drawer):
 *  edges grouped by type, add dialog, per-link remove with confirm. */
export default function IssueLinks({
  slug,
  identifier,
  uuid,
  projectBase,
  compact = false,
  onError,
}: {
  slug: string;
  identifier: string;
  uuid: string;
  /** Project-scoped API base (without the /issues/{uuid} suffix). */
  projectBase: string;
  compact?: boolean;
  onError: (msg: string | null) => void;
}) {
  const openIssue = useOpenIssue(slug, identifier, compact);
  const [dialogOpen, setDialogOpen] = useState(false);
  const { links, isLoading, isError, refetch, removeMutation } = useIssueLinks(
    slug,
    identifier,
    uuid,
  );

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
    <Card>
      <CardHeader className="pb-3">
        <CardTitle className="flex items-center gap-2 text-base">
          <Link2 className="h-4 w-4" />
          {linksTitle(links.length)}
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-2">
        {isLoading ? (
          <div className="space-y-2">
            <Skeleton className="h-8 w-full" />
            <Skeleton className="h-8 w-full" />
          </div>
        ) : isError ? (
          <div className="flex items-center gap-2">
            <p className="text-sm text-destructive">
              Couldn't load linked issues.
            </p>
            <Button
              variant="ghost"
              size="sm"
              onClick={() => void refetch()}
            >
              Retry
            </Button>
          </div>
        ) : groups.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            No linked issues yet. Link issues that block, relate to, or
            duplicate this one.
          </p>
        ) : (
          groups.map((group) => (
            <div key={group.key} className="pt-1 first:pt-0">
              <h4
                className={cn(
                  "px-2 pb-1 text-xs font-medium text-muted-foreground",
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
        <Button
          variant="ghost"
          size="sm"
          className="gap-1.5 text-muted-foreground"
          onClick={() => setDialogOpen(true)}
        >
          <Plus className="h-3.5 w-3.5" />
          Add link
        </Button>
      </CardContent>
      <AddLinkDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        slug={slug}
        identifier={identifier}
        currentId={uuid}
        projectBase={projectBase}
        onError={onError}
      />
    </Card>
  );
}
