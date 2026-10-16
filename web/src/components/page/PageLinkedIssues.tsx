// Linked issues (C36T1): the "Linked issues" section on the page
// detail view, below the page content. List (state badge + display_id +
// title, click → issue detail), "Link issue" picker (search this
// project's live issues via the existing issue list endpoint), unlink
// with confirm, "Create issue from this page" (creates via the existing
// issue-create endpoint, links it, navigates to the new issue).
//
// Authz mirrors the backend: member (15)+ for link/unlink/create;
// viewers see the list read-only. The server enforces all of it; the
// UI just hides what would 403.

import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link2, Plus, X } from "lucide-react";
import { api, ApiError } from "../../lib/api";
import {
  buildCreateFromPageIssueBody,
  filterLinkableIssues,
  pageUrlForPage,
  usePageIssueLinkCanWrite,
} from "../../lib/pageIssueLinks";
import type { Issue, IssueListResult, Page, PageLinkedIssue } from "../../lib/types";
import { cn } from "../../lib/utils";
import { Alert, AlertDescription } from "../ui/alert";
import { Badge } from "../ui/badge";
import { Button } from "../ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "../ui/card";
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
import { ScrollArea } from "../ui/scroll-area";
import { Skeleton } from "../ui/skeleton";
import { toast } from "../ui/toast";

const SEARCH_DEBOUNCE_MS = 250;
const SEARCH_PAGE_SIZE = 10;

/** "Link issue" dialog: debounced server-side issue search (same
 *  contract as IssueLinks' picker: ?q=&per_page= on the issue list),
 *  already-linked / draft / archived issues filtered out. */
function LinkIssueDialog({
  open,
  onOpenChange,
  slug,
  identifier,
  pageId,
  linkedIds,
  projectBase,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  slug: string;
  identifier: string;
  pageId: string;
  linkedIds: Set<string>;
  projectBase: string;
}) {
  const queryClient = useQueryClient();
  const [input, setInput] = useState("");
  const [debounced, setDebounced] = useState("");
  const [selected, setSelected] = useState<Issue | null>(null);
  const [dialogError, setDialogError] = useState<string | null>(null);

  useEffect(() => {
    if (!open) {
      setInput("");
      setDebounced("");
      setSelected(null);
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
    queryKey: ["link-issue-search", slug, identifier, debounced],
    queryFn: () => {
      const p = new URLSearchParams();
      p.set("per_page", String(SEARCH_PAGE_SIZE));
      if (debounced) p.set("q", debounced);
      return api.get<IssueListResult>(`${projectBase}/issues?${p}`);
    },
    enabled: open,
    staleTime: 30_000,
  });

  const linkMutation = useMutation({
    mutationFn: (issueId: string) =>
      api.post(`${projectBase}/pages/${encodeURIComponent(pageId)}/issues`, {
        issue_id: issueId,
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["page-linked-issues", slug, identifier, pageId],
      });
      onOpenChange(false);
    },
    onError: (e) => {
      // Honest backend messages: "page-issue link already exists",
      // "issue is in another project", 404 on a raced-away issue,
      // 403 for viewers (who never see this dialog).
      setDialogError(
        e instanceof ApiError ? e.message : "Failed to link issue",
      );
    },
  });

  const results = filterLinkableIssues(
    searchQuery.data?.results ?? [],
    linkedIds,
  );

  function submit() {
    if (!selected || linkMutation.isPending) return;
    linkMutation.mutate(selected.id);
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Link issue</DialogTitle>
          <DialogDescription>
            Link an issue from this project to this page.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="link-issue-search">Issue</Label>
            <Input
              id="link-issue-search"
              value={input}
              onChange={(e) => setInput(e.target.value)}
              placeholder="Search issues by ID or title…"
              className="h-9"
              autoFocus
              disabled={linkMutation.isPending}
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
                Couldn&apos;t load issues. Try again.
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
            disabled={linkMutation.isPending}
          >
            Cancel
          </Button>
          <Button
            onClick={submit}
            disabled={!selected || linkMutation.isPending}
          >
            {linkMutation.isPending ? "Linking…" : "Link issue"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export default function PageLinkedIssues({
  slug,
  identifier,
  page,
}: {
  slug: string;
  identifier: string;
  page: Page;
}) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const canWrite = usePageIssueLinkCanWrite(slug);
  const base = `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`;
  const linksKey = ["page-linked-issues", slug, identifier, page.id] as const;
  const [dialogOpen, setDialogOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const linksQuery = useQuery({
    queryKey: linksKey,
    queryFn: () =>
      api
        .get<{ issues: PageLinkedIssue[] }>(
          `${base}/pages/${encodeURIComponent(page.id)}/issues`,
        )
        .then((d) => d.issues),
  });

  const invalidate = () =>
    void queryClient.invalidateQueries({ queryKey: linksKey });

  const unlinkMutation = useMutation({
    mutationFn: (issueId: string) =>
      api.del(
        `${base}/pages/${encodeURIComponent(page.id)}/issues/${encodeURIComponent(issueId)}`,
      ),
    onSuccess: () => {
      setError(null);
      invalidate();
    },
    onError: (e) => {
      setError(e instanceof ApiError ? e.message : "Failed to unlink issue");
    },
  });

  const createMutation = useMutation({
    mutationFn: async () => {
      const pageUrl = pageUrlForPage(
        slug,
        identifier,
        window.location.origin,
      );
      const created = await api.post<Issue>(
        `${base}/issues`,
        buildCreateFromPageIssueBody(page.title, pageUrl),
      );
      // The issue exists now: a link failure must not lose it — warn
      // via toast and still navigate to the new issue, where the link
      // can be retried from the issue's Linked pages section.
      let linkError: string | null = null;
      try {
        await api.post(
          `${base}/pages/${encodeURIComponent(page.id)}/issues`,
          { issue_id: created.id },
        );
      } catch (e) {
        linkError =
          e instanceof ApiError ? e.message : "Failed to link issue";
      }
      return { created, linkError };
    },
    onSuccess: ({ created, linkError }) => {
      setError(null);
      if (linkError) {
        toast.add({
          title: `Issue created but not linked: ${linkError}`,
          type: "error",
        });
      }
      invalidate();
      navigate(`/w/${slug}/p/${identifier}/i/${created.id}`);
    },
    onError: (e) => {
      setError(
        e instanceof ApiError ? e.message : "Failed to create issue",
      );
    },
  });

  function askUnlink(linked: PageLinkedIssue) {
    if (
      !window.confirm(
        `Unlink ${linked.display_id} from this page?`,
      )
    )
      return;
    unlinkMutation.mutate(linked.id);
  }

  const linked = linksQuery.data ?? [];
  const linkedIds = new Set(linked.map((l) => l.id));

  return (
    <Card>
      <CardHeader className="py-3">
        <CardTitle className="flex items-center gap-2 text-sm font-medium">
          <Link2 className="h-4 w-4" />
          Linked issues
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        {linksQuery.isLoading ? (
          <div className="space-y-2">
            <Skeleton className="h-8 w-full" />
            <Skeleton className="h-8 w-full" />
          </div>
        ) : linksQuery.isError ? (
          <div className="flex items-center gap-2">
            <p className="text-sm text-muted-foreground">
              Couldn&apos;t load linked issues.
            </p>
            <button
              type="button"
              className="text-sm text-primary hover:underline"
              onClick={() => void linksQuery.refetch()}
            >
              Retry
            </button>
          </div>
        ) : linked.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            No linked issues yet.
          </p>
        ) : (
          <ul className="space-y-1">
            {linked.map((l) => (
              <li
                key={l.id}
                className="group flex items-center gap-2 rounded-md px-2 py-1.5 hover:bg-accent"
              >
                <button
                  type="button"
                  onClick={() =>
                    navigate(`/w/${slug}/p/${identifier}/i/${l.id}`)
                  }
                  className="flex min-w-0 flex-1 cursor-pointer items-center gap-2 text-left"
                  title={`Open ${l.display_id}`}
                >
                  {/* Plain badge: the list shape carries the state name
                      but no color — no StateBadge without a full state. */}
                  <Badge
                    variant="secondary"
                    className="shrink-0 text-[11px]"
                  >
                    {l.state}
                  </Badge>
                  <span className="shrink-0 font-mono text-[13px] text-muted-foreground">
                    {l.display_id}
                  </span>
                  <span className="min-w-0 flex-1 truncate text-sm">
                    {l.title}
                  </span>
                </button>
                {canWrite && (
                  <Button
                    variant="ghost"
                    size="sm"
                    className={cn(
                      "h-6 shrink-0 px-2 opacity-0 group-hover:opacity-100 focus-visible:opacity-100",
                    )}
                    disabled={unlinkMutation.isPending}
                    title={`Unlink ${l.display_id}`}
                    aria-label={`Unlink ${l.display_id}: ${l.title}`}
                    onClick={() => askUnlink(l)}
                  >
                    Unlink
                  </Button>
                )}
              </li>
            ))}
          </ul>
        )}
        {canWrite && (
          <div className="flex flex-wrap items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              onClick={() => setDialogOpen(true)}
            >
              <Link2 className="h-3.5 w-3.5" />
              Link issue
            </Button>
            <Button
              variant="ghost"
              size="sm"
              disabled={createMutation.isPending}
              onClick={() => createMutation.mutate()}
            >
              <Plus className="h-3.5 w-3.5" />
              {createMutation.isPending
                ? "Creating…"
                : "Create issue from this page"}
            </Button>
          </div>
        )}
      </CardContent>
      <LinkIssueDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        slug={slug}
        identifier={identifier}
        pageId={page.id}
        linkedIds={linkedIds}
        projectBase={base}
      />
    </Card>
  );
}
