// Linked pages (C36T1): the "Linked pages" section on the issue
// detail view, in the project-structure area (rendered right after
// IssueProperties). List (page title + relative updated time, click →
// Pages view with that page selected), "Link page" picker (this
// project's pages, tree-flattened like the existing page picker),
// unlink with confirm.
//
// Authz mirrors the backend: member (15)+ for link/unlink; viewers
// see the list read-only. The server enforces all of it; the UI just
// hides what would 403.

import { useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { FileText, Link2, Plus, X } from "lucide-react";
import { api, ApiError } from "../../lib/api";
import {
  flattenPagesForPicker,
  usePageIssueLinkCanWrite,
} from "../../lib/pageIssueLinks";
import { relativeTime } from "../../lib/relativeTime";
import type { Page, PageLinkedPage } from "../../lib/types";
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
import { ScrollArea } from "../ui/scroll-area";
import { Skeleton } from "../ui/skeleton";

/** "Link page" dialog: this project's pages, tree-flattened, filtered
 *  client-side by title. Already-linked pages are excluded. */
function LinkPageDialog({
  open,
  onOpenChange,
  slug,
  identifier,
  uuid,
  linkedIds,
  projectBase,
  onError,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  slug: string;
  identifier: string;
  uuid: string;
  linkedIds: Set<string>;
  projectBase: string;
  onError: (msg: string | null) => void;
}) {
  const queryClient = useQueryClient();
  const [input, setInput] = useState("");
  const [selected, setSelected] = useState<Page | null>(null);
  const [dialogError, setDialogError] = useState<string | null>(null);

  useEffect(() => {
    if (!open) {
      setInput("");
      setSelected(null);
      setDialogError(null);
    }
  }, [open]);

  const pagesQuery = useQuery({
    queryKey: ["pages", slug, identifier],
    queryFn: () =>
      api.get<{ pages: Page[] }>(`${projectBase}/pages`).then((d) => d.pages),
    enabled: open,
    staleTime: 30_000,
  });

  const linkMutation = useMutation({
    mutationFn: (pageId: string) =>
      api.post(
        `${projectBase}/issues/${encodeURIComponent(uuid)}/pages`,
        { page_id: pageId },
      ),
    onSuccess: () => {
      onError(null);
      void queryClient.invalidateQueries({
        queryKey: ["issue-linked-pages", slug, identifier, uuid],
      });
      onOpenChange(false);
    },
    onError: (e) => {
      // Honest backend messages: "page-issue link already exists",
      // "issue is in another project", 404 on a raced-away page,
      // 403 for viewers (who never see this dialog).
      setDialogError(
        e instanceof ApiError ? e.message : "Failed to link page",
      );
    },
  });

  const flat = useMemo(
    () => flattenPagesForPicker(pagesQuery.data ?? []),
    [pagesQuery.data],
  );
  const q = input.trim().toLowerCase();
  const results = flat.filter(
    (f) => !linkedIds.has(f.page.id) && (!q || f.page.title.toLowerCase().includes(q)),
  );

  function submit() {
    if (!selected || linkMutation.isPending) return;
    linkMutation.mutate(selected.id);
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Link page</DialogTitle>
          <DialogDescription>
            Link a page from this project to this issue.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="link-page-search">Page</Label>
            <Input
              id="link-page-search"
              value={input}
              onChange={(e) => setInput(e.target.value)}
              placeholder="Search pages…"
              className="h-9"
              autoFocus
              disabled={linkMutation.isPending}
            />
            {selected ? (
              <div className="flex items-center gap-2 rounded-md border px-2 py-1.5">
                <FileText className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                <span className="min-w-0 flex-1 truncate text-sm">
                  {selected.title}
                </span>
                <Button
                  variant="ghost"
                  size="sm"
                  className="h-6 w-6 p-0"
                  aria-label="Clear selected page"
                  onClick={() => setSelected(null)}
                >
                  <X className="h-3.5 w-3.5" />
                </Button>
              </div>
            ) : pagesQuery.isLoading ? (
              <div className="space-y-2">
                <Skeleton className="h-8 w-full" />
                <Skeleton className="h-8 w-full" />
              </div>
            ) : pagesQuery.isError ? (
              <p className="text-sm text-destructive">
                Couldn&apos;t load pages. Try again.
              </p>
            ) : (
              <ScrollArea className="max-h-56 rounded-md border">
                {results.map(({ page, depth }) => (
                  <button
                    key={page.id}
                    type="button"
                    className="flex w-full items-center gap-2 px-2 py-1.5 text-left text-sm hover:bg-accent"
                    onClick={() => setSelected(page)}
                  >
                    <span className="min-w-0 flex-1 truncate">
                      {"\u00a0\u00a0".repeat(depth)}
                      {page.title}
                    </span>
                  </button>
                ))}
                {results.length === 0 && (
                  <p className="p-2 text-sm text-muted-foreground">
                    {q ? "No pages match." : "No pages in this project."}
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
            {linkMutation.isPending ? "Linking…" : "Link page"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** "Linked pages" section for the issue detail page (and peek drawer):
 *  linked pages, add dialog, per-page remove with confirm. */
export default function IssueLinkedPages({
  slug,
  identifier,
  uuid,
  onError,
}: {
  slug: string;
  identifier: string;
  uuid: string;
  onError: (msg: string | null) => void;
}) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const canWrite = usePageIssueLinkCanWrite(slug);
  const base = `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`;
  const linksKey = ["issue-linked-pages", slug, identifier, uuid] as const;
  const [dialogOpen, setDialogOpen] = useState(false);

  const linksQuery = useQuery({
    queryKey: linksKey,
    queryFn: () =>
      api
        .get<{ pages: PageLinkedPage[] }>(
          `${base}/issues/${encodeURIComponent(uuid)}/pages`,
        )
        .then((d) => d.pages),
  });

  const removeMutation = useMutation({
    mutationFn: (pageId: string) =>
      api.del(
        `${base}/issues/${encodeURIComponent(uuid)}/pages/${encodeURIComponent(pageId)}`,
      ),
    onSuccess: () => {
      onError(null);
      void queryClient.invalidateQueries({ queryKey: linksKey });
    },
    onError: (e) => {
      // A 404 here means the link was already gone (stale UI) — the
      // onSettled refresh already removed it; say so honestly.
      const msg =
        e instanceof ApiError ? e.message : "Failed to remove link";
      onError(
        e instanceof ApiError && e.status === 404
          ? `${msg}. The list has been refreshed.`
          : msg,
      );
    },
  });

  function removeLink(page: PageLinkedPage) {
    if (!window.confirm(`Unlink page "${page.title}" from this issue?`))
      return;
    removeMutation.mutate(page.id);
  }

  const linked = linksQuery.data ?? [];
  const linkedIds = new Set(linked.map((p) => p.id));

  return (
    <section aria-label="Linked pages" className="mt-[18px]">
      <h2 className="mb-[9px] flex items-center gap-2 text-[12px] leading-[1.5] font-semibold text-ink">
        <Link2 className="h-3.5 w-3.5 text-ink-muted" aria-hidden />
        Linked pages
      </h2>
      <div className="space-y-2">
        {linksQuery.isLoading ? (
          <div className="space-y-2">
            <Skeleton className="h-8 w-full" />
            <Skeleton className="h-8 w-full" />
          </div>
        ) : linksQuery.isError ? (
          <div className="flex items-center gap-2">
            <p className="text-[10px] text-accent-red">
              Couldn&apos;t load linked pages.
            </p>
            <button
              type="button"
              className="rounded-[4px] px-2 py-1 text-[10px] text-ink-secondary hover:bg-wash-hover hover:text-ink"
              onClick={() => void linksQuery.refetch()}
            >
              Retry
            </button>
          </div>
        ) : linked.length === 0 ? (
          <p className="text-[10px] text-ink-muted">No linked pages yet.</p>
        ) : (
          <ul className="-mx-2">
            {linked.map((p) => (
              <li
                key={p.id}
                className="group flex items-center gap-2 rounded-md px-2 py-1.5 hover:bg-accent"
              >
                <button
                  type="button"
                  onClick={() =>
                    navigate(`/w/${slug}/p/${identifier}/pages`, {
                      state: { pageId: p.id },
                    })
                  }
                  className="flex min-w-0 flex-1 cursor-pointer items-center gap-2 text-left"
                  title={`Open page "${p.title}"`}
                >
                  <FileText className="h-3.5 w-3.5 shrink-0 text-ink-muted" />
                  <span className="min-w-0 flex-1 truncate text-sm">
                    {p.title}
                  </span>
                  <span
                    className="shrink-0 text-[10px] text-ink-muted"
                    title={p.updated_at}
                  >
                    updated {relativeTime(p.updated_at)}
                  </span>
                </button>
                {canWrite && (
                  <Button
                    variant="ghost"
                    size="sm"
                    className="h-6 w-6 shrink-0 p-0 opacity-0 group-hover:opacity-100 focus-visible:opacity-100"
                    disabled={removeMutation.isPending}
                    title={`Unlink "${p.title}"`}
                    aria-label={`Unlink page "${p.title}"`}
                    onClick={() => removeLink(p)}
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
            Link page
          </button>
        )}
      </div>
      <LinkPageDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        slug={slug}
        identifier={identifier}
        uuid={uuid}
        linkedIds={linkedIds}
        projectBase={base}
        onError={onError}
      />
    </section>
  );
}
