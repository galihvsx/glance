import { useMemo, useState } from "react";
import { Link, useParams } from "react-router-dom";
import {
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import {
  ChevronDown,
  ChevronRight,
  FileText,
  History,
  Pencil,
  Plus,
  Share2,
  Trash2,
} from "lucide-react";
import { api, ApiError } from "../lib/api";
import { renderMarkdown } from "../lib/markdown";
import type { Page, PageRevision, Project } from "../lib/types";
import { cn } from "../lib/utils";
import ProjectNav from "../components/project/ProjectNav";
import ShareModal from "../components/ShareModal";
import ThemeToggle from "../components/ThemeToggle";
import NotificationBell from "../components/notifications/NotificationBell";
import { Button } from "../components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "../components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../components/ui/dialog";
import {
  Drawer,
  DrawerContent,
  DrawerDescription,
  DrawerHeader,
  DrawerTitle,
} from "../components/ui/drawer";
import { Input } from "../components/ui/input";
import { Label } from "../components/ui/label";
import {
  NativeSelect,
  NativeSelectOption,
} from "../components/ui/native-select";
import { Skeleton } from "../components/ui/skeleton";
import { Textarea } from "../components/ui/textarea";
import { Alert, AlertDescription } from "../components/ui/alert";

function timeAgo(iso: string): string {
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) return iso.slice(0, 10);
  const s = Math.max(0, Math.floor((Date.now() - then) / 1000));
  if (s < 60) return "just now";
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  const d = Math.floor(h / 24);
  if (d < 30) return `${d}d ago`;
  return then ? new Date(then).toISOString().slice(0, 10) : iso.slice(0, 10);
}

/* ------------------------------------------------------------------ */
/* Tree helpers                                                        */
/* ------------------------------------------------------------------ */

interface PageNode {
  page: Page;
  children: PageNode[];
  depth: number;
}

/** Flat API list → sorted tree. Orphaned parent_ids become roots. */
export function buildPageTree(pages: Page[]): PageNode[] {
  const byId = new Map<string, PageNode>();
  for (const p of pages) byId.set(p.id, { page: p, children: [], depth: 0 });
  const roots: PageNode[] = [];
  for (const p of pages) {
    const node = byId.get(p.id)!;
    const parentId = p.parent_id ?? null;
    if (parentId && byId.has(parentId)) {
      byId.get(parentId)!.children.push(node);
    } else {
      roots.push(node);
    }
  }
  const byPos = (a: PageNode, b: PageNode) =>
    a.page.position - b.page.position ||
    a.page.title.localeCompare(b.page.title);
  const fix = (nodes: PageNode[], depth: number) => {
    nodes.sort(byPos);
    for (const n of nodes) {
      n.depth = depth;
      fix(n.children, depth + 1);
    }
  };
  fix(roots, 0);
  return roots;
}

/** Ids of the page itself + all descendants (for the move picker). */
export function subtreeIds(root: PageNode): Set<string> {
  const out = new Set<string>();
  const walk = (n: PageNode) => {
    out.add(n.page.id);
    n.children.forEach(walk);
  };
  walk(root);
  return out;
}

function flattenForPicker(nodes: PageNode[]): PageNode[] {
  const out: PageNode[] = [];
  const walk = (n: PageNode) => {
    out.push(n);
    n.children.forEach(walk);
  };
  nodes.forEach(walk);
  return out;
}

/* ------------------------------------------------------------------ */
/* Page                                                                */
/* ------------------------------------------------------------------ */

export default function Pages() {
  const { slug = "", identifier = "" } = useParams<{
    slug: string;
    identifier: string;
  }>();
  const queryClient = useQueryClient();
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [formOpen, setFormOpen] = useState(false);
  const [editing, setEditing] = useState<Page | null>(null);
  const [createParentId, setCreateParentId] = useState<string | null>(null);
  const [moveTarget, setMoveTarget] = useState<Page | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<Page | null>(null);
  const [revisionsOpen, setRevisionsOpen] = useState(false);
  const [shareOpen, setShareOpen] = useState(false);
  const [previewRev, setPreviewRev] = useState<PageRevision | null>(null);
  const [formError, setFormError] = useState<string | null>(null);

  const base = `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`;
  const pagesKey = ["pages", slug, identifier] as const;

  const projectQuery = useQuery({
    queryKey: ["project", slug, identifier],
    queryFn: () => api.get<Project>(base),
  });
  const pagesQuery = useQuery({
    queryKey: pagesKey,
    queryFn: () =>
      api.get<{ pages: Page[] }>(`${base}/pages`).then((d) => d.pages),
  });
  const revisionsQuery = useQuery({
    queryKey: ["page-revisions", slug, identifier, selectedId],
    enabled: revisionsOpen && selectedId !== null,
    queryFn: () =>
      api
        .get<{ revisions: PageRevision[] }>(
          `${base}/pages/${selectedId}/revisions`,
        )
        .then((d) => d.revisions),
  });

  const tree = useMemo(
    () => buildPageTree(pagesQuery.data ?? []),
    [pagesQuery.data],
  );
  const pickerPages = useMemo(() => flattenForPicker(tree), [tree]);
  const selected = useMemo(() => {
    const find = (nodes: PageNode[]): Page | null => {
      for (const n of nodes) {
        if (n.page.id === selectedId) return n.page;
        const hit = find(n.children);
        if (hit) return hit;
      }
      return null;
    };
    return find(tree);
  }, [tree, selectedId]);

  // Auto-select the first page on load; auto-expand ancestors of selection.
  const [autoSelected, setAutoSelected] = useState(false);
  if (!autoSelected && tree.length > 0 && selectedId === null) {
    setAutoSelected(true);
    setSelectedId(tree[0].page.id);
  }

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: pagesKey });
    void queryClient.invalidateQueries({
      queryKey: ["page-revisions", slug, identifier, selectedId],
    });
  };

  const errMsg = (e: unknown, fallback: string) =>
    e instanceof ApiError ? e.message : fallback;

  const createMutation = useMutation({
    mutationFn: (v: { title: string; content: string; parent_id: string | null }) =>
      api.post<Page>(`${base}/pages`, {
        title: v.title.trim(),
        content: v.content,
        ...(v.parent_id ? { parent_id: v.parent_id } : {}),
      }),
    onSuccess: (p) => {
      setFormOpen(false);
      setFormError(null);
      setSelectedId(p.id);
      // Expand the new page's parent chain so it becomes visible.
      const chain = new Set<string>();
      let cur: string | null | undefined = p.parent_id;
      const byId = new Map(pickerPages.map((n) => [n.page.id, n]));
      while (cur && byId.has(cur)) {
        chain.add(cur);
        cur = byId.get(cur)!.page.parent_id ?? null;
      }
      setExpanded((prev) => new Set([...prev, ...chain]));
      invalidate();
    },
    onError: (e) => setFormError(errMsg(e, "Failed to create page")),
  });

  const updateMutation = useMutation({
    mutationFn: ({ id, patch }: { id: string; patch: Record<string, string> }) =>
      api.patch<Page>(`${base}/pages/${id}`, patch),
    onSuccess: () => {
      setFormOpen(false);
      setEditing(null);
      setFormError(null);
      invalidate();
    },
    onError: (e) => setFormError(errMsg(e, "Failed to update page")),
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) => api.del(`${base}/pages/${id}`),
    onSuccess: () => {
      setDeleteTarget(null);
      setSelectedId(null);
      setAutoSelected(false);
      invalidate();
    },
    onError: (e) => setFormError(errMsg(e, "Failed to delete page")),
  });

  const moveMutation = useMutation({
    mutationFn: ({
      id,
      parent_id,
      position,
    }: {
      id: string;
      parent_id: string | null;
      position?: number;
    }) => {
      // Key-presence semantics: parent_id is ALWAYS sent explicitly
      // (uuid or null = root); position only when the user typed one.
      const body: Record<string, unknown> = { parent_id };
      if (position !== undefined) body.position = position;
      return api.post<Page>(`${base}/pages/${id}/move`, body);
    },
    onSuccess: () => {
      setMoveTarget(null);
      setFormError(null);
      invalidate();
    },
    onError: (e) => setFormError(errMsg(e, "Failed to move page")),
  });

  const restoreMutation = useMutation({
    mutationFn: (revisionId: string) =>
      api.post<Page>(`${base}/pages/${selectedId}/restore`, {
        revision_id: revisionId,
      }),
    onSuccess: () => {
      setRevisionsOpen(false);
      setPreviewRev(null);
      invalidate();
    },
    onError: (e) => setFormError(errMsg(e, "Failed to restore revision")),
  });

  function openCreate(parentId: string | null) {
    setEditing(null);
    setCreateParentId(parentId);
    setFormError(null);
    setFormOpen(true);
  }

  function openEdit(p: Page) {
    setEditing(p);
    setCreateParentId(null);
    setFormError(null);
    setFormOpen(true);
  }

  function submitForm(title: string, content: string) {
    setFormError(null);
    if (!title.trim()) {
      setFormError("Title is required");
      return;
    }
    if (editing) {
      const patch: Record<string, string> = {};
      if (title.trim() !== editing.title) patch.title = title.trim();
      if (content !== editing.content) patch.content = content;
      if (Object.keys(patch).length === 0) {
        setFormOpen(false);
        setEditing(null);
        return;
      }
      updateMutation.mutate({ id: editing.id, patch });
    } else {
      createMutation.mutate({
        title,
        content,
        parent_id: createParentId,
      });
    }
  }

  function toggleExpanded(id: string) {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  function renderTree(nodes: PageNode[]) {
    return nodes.map((n) => (
      <div key={n.page.id}>
        <div
          className={cn(
            "group flex w-full items-center gap-1 rounded-md px-2 py-1.5 text-sm",
            n.page.id === selectedId
              ? "bg-accent font-medium text-accent-foreground"
              : "text-muted-foreground hover:bg-accent/50 hover:text-foreground",
          )}
          style={{ paddingLeft: `${0.5 + n.depth * 1.1}rem` }}
        >
          {n.children.length > 0 ? (
            <button
              type="button"
              aria-label={expanded.has(n.page.id) ? "Collapse" : "Expand"}
              onClick={() => toggleExpanded(n.page.id)}
              className="rounded p-0.5 hover:bg-accent"
            >
              {expanded.has(n.page.id) ? (
                <ChevronDown className="h-3.5 w-3.5" />
              ) : (
                <ChevronRight className="h-3.5 w-3.5" />
              )}
            </button>
          ) : (
            <span className="w-4 shrink-0" />
          )}
          <button
            type="button"
            onClick={() => setSelectedId(n.page.id)}
            className="flex min-w-0 flex-1 items-center gap-2 text-left"
          >
            <FileText className="h-3.5 w-3.5 shrink-0" />
            <span className="truncate">{n.page.title}</span>
          </button>
          <button
            type="button"
            title="New sub-page"
            onClick={() => openCreate(n.page.id)}
            className="hidden rounded p-1 group-hover:block hover:bg-accent"
          >
            <Plus className="h-3.5 w-3.5" />
          </button>
        </div>
        {expanded.has(n.page.id) && renderTree(n.children)}
      </div>
    ));
  }

  return (
    <div className="mx-auto w-full max-w-6xl p-6">
      <div className="mb-4">
        <Link
          to={`/w/${slug}`}
          className="text-xs text-muted-foreground hover:underline"
        >
          ← Projects
        </Link>
        <div className="mt-1 flex items-start justify-between gap-4">
          <h1 className="text-2xl font-semibold tracking-tight">
            {projectQuery.data ? projectQuery.data.name : <Skeleton className="h-8 w-48" />}
          </h1>
          <div className="flex items-center gap-2">
            <NotificationBell />
            <ThemeToggle />
            <Button onClick={() => openCreate(null)} className="gap-2">
              <Plus className="h-4 w-4" /> New page
            </Button>
          </div>
        </div>
        <div className="mt-3">
          <ProjectNav />
        </div>
      </div>

      {formError && (
        <Alert variant="destructive" className="mb-4">
          <AlertDescription>{formError}</AlertDescription>
        </Alert>
      )}

      <div className="flex gap-4">
        {/* Tree sidebar */}
        <aside className="w-72 shrink-0">
          <Card>
            <CardHeader className="py-3">
              <CardTitle className="text-sm font-medium">Pages</CardTitle>
            </CardHeader>
            <CardContent className="max-h-[60vh] overflow-y-auto px-2 pb-3">
              {pagesQuery.isLoading ? (
                <div className="space-y-2 p-2">
                  <Skeleton className="h-6 w-full" />
                  <Skeleton className="h-6 w-5/6" />
                  <Skeleton className="h-6 w-4/6" />
                </div>
              ) : tree.length === 0 ? (
                <div className="p-4 text-center text-sm text-muted-foreground">
                  No pages yet.
                  <br />
                  <button
                    type="button"
                    onClick={() => openCreate(null)}
                    className="mt-2 text-primary hover:underline"
                  >
                    Create the first one
                  </button>
                </div>
              ) : (
                renderTree(tree)
              )}
            </CardContent>
          </Card>
        </aside>

        {/* Page view */}
        <main className="min-w-0 flex-1">
          {selected ? (
            <Card>
              <CardHeader>
                <div className="flex items-start justify-between gap-4">
                  <div className="min-w-0">
                    <CardTitle className="text-xl">{selected.title}</CardTitle>
                    <p className="mt-1 text-xs text-muted-foreground">
                      Updated {timeAgo(selected.updated_at)}
                    </p>
                  </div>
                  <div className="flex shrink-0 items-center gap-1">
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => openCreate(selected.id)}
                      title="New sub-page"
                    >
                      <Plus className="h-4 w-4" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => openEdit(selected)}
                      title="Edit"
                    >
                      <Pencil className="h-4 w-4" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => {
                        setPreviewRev(null);
                        setRevisionsOpen(true);
                      }}
                      title="Revision history"
                    >
                      <History className="h-4 w-4" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => setMoveTarget(selected)}
                      title="Move"
                    >
                      Move
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => setShareOpen(true)}
                      title="Share publicly"
                    >
                      <Share2 className="h-4 w-4" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => setDeleteTarget(selected)}
                      title="Delete"
                    >
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  </div>
                </div>
              </CardHeader>
              <CardContent>
                {selected.content ? (
                  <div
                    className="wiki-content"
                    dangerouslySetInnerHTML={{
                      __html: renderMarkdown(selected.content),
                    }}
                  />
                ) : (
                  <p className="text-sm text-muted-foreground">
                    This page is empty.{" "}
                    <button
                      type="button"
                      onClick={() => openEdit(selected)}
                      className="text-primary hover:underline"
                    >
                      Add content
                    </button>
                  </p>
                )}
              </CardContent>
            </Card>
          ) : (
            <Card>
              <CardContent className="p-12 text-center text-sm text-muted-foreground">
                {pagesQuery.isLoading ? (
                  <Skeleton className="mx-auto h-6 w-48" />
                ) : (
                  "Select a page from the tree, or create one."
                )}
              </CardContent>
            </Card>
          )}
        </main>
      </div>

      {/* Create / edit dialog */}
      <Dialog
        open={formOpen}
        onOpenChange={(open) => {
          setFormOpen(open);
          if (!open) {
            setEditing(null);
            setCreateParentId(null);
          }
        }}
      >
        <DialogContent className="max-w-2xl">
          <DialogHeader>
            <DialogTitle>
              {editing
                ? `Edit "${editing.title}"`
                : createParentId
                  ? "New sub-page"
                  : "New page"}
            </DialogTitle>
            <DialogDescription>
              {editing
                ? "Saving records a revision of the previous content."
                : "Pages support markdown."}
            </DialogDescription>
          </DialogHeader>
          <PageForm
            key={editing ? `edit-${editing.id}` : `new-${createParentId ?? "root"}`}
            initialTitle={editing?.title ?? ""}
            initialContent={editing?.content ?? ""}
            pending={createMutation.isPending || updateMutation.isPending}
            error={formError}
            submitLabel={editing ? "Save" : "Create"}
            onSubmit={(t, c) => submitForm(t, c)}
            onCancel={() => {
              setFormOpen(false);
              setEditing(null);
              setCreateParentId(null);
            }}
          />
        </DialogContent>
      </Dialog>

      {/* Move dialog */}
      <Dialog
        open={moveTarget !== null}
        onOpenChange={(open) => !open && setMoveTarget(null)}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Move "{moveTarget?.title}"</DialogTitle>
            <DialogDescription>
              Choose a new parent (or root) and an optional position among its
              children.
            </DialogDescription>
          </DialogHeader>
          {moveTarget && (
            <MoveForm
              key={moveTarget.id}
              target={moveTarget}
              tree={tree}
              pending={moveMutation.isPending}
              error={formError}
              onSubmit={(parentId, position) =>
                moveMutation.mutate({
                  id: moveTarget.id,
                  parent_id: parentId,
                  position,
                })
              }
              onCancel={() => setMoveTarget(null)}
            />
          )}
        </DialogContent>
      </Dialog>

      {/* Delete dialog */}
      <Dialog
        open={deleteTarget !== null}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete "{deleteTarget?.title}"?</DialogTitle>
            <DialogDescription>
              This deletes the page and its whole subtree. This cannot be
              undone (restore from revisions is lost with the page).
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDeleteTarget(null)}>
              Cancel
            </Button>
            <Button
              variant="destructive"
              disabled={deleteMutation.isPending}
              onClick={() =>
                deleteTarget && deleteMutation.mutate(deleteTarget.id)
              }
            >
              {deleteMutation.isPending ? "Deleting…" : "Delete"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Revision history drawer */}
      <Drawer
        open={revisionsOpen}
        onOpenChange={(open) => {
          setRevisionsOpen(open);
          if (!open) setPreviewRev(null);
        }}
      >
        <DrawerContent className="max-h-[85vh]">
          <DrawerHeader>
            <DrawerTitle>History — {selected?.title}</DrawerTitle>
            <DrawerDescription>
              Restoring a revision snapshots the current content first, so
              restore is undoable.
            </DrawerDescription>
          </DrawerHeader>
          <div className="grid gap-4 overflow-y-auto px-4 pb-6 md:grid-cols-2">
            <div className="space-y-2">
              {revisionsQuery.isLoading ? (
                <Skeleton className="h-10 w-full" />
              ) : (revisionsQuery.data ?? []).length === 0 ? (
                <p className="text-sm text-muted-foreground">
                  No revisions yet — edit the page to create one.
                </p>
              ) : (
                (revisionsQuery.data ?? []).map((r) => (
                  <button
                    key={r.id}
                    type="button"
                    onClick={() => setPreviewRev(r)}
                    className={cn(
                      "w-full rounded-md border p-3 text-left text-sm",
                      previewRev?.id === r.id
                        ? "border-primary bg-accent"
                        : "hover:bg-accent/50",
                    )}
                  >
                    <div className="font-medium">{r.title}</div>
                    <div className="text-xs text-muted-foreground">
                      {timeAgo(r.created_at)}
                    </div>
                  </button>
                ))
              )}
            </div>
            <div>
              {previewRev ? (
                <Card>
                  <CardHeader className="py-3">
                    <CardTitle className="text-sm">{previewRev.title}</CardTitle>
                  </CardHeader>
                  <CardContent>
                    <div
                      className="wiki-content max-h-[40vh] overflow-y-auto"
                      dangerouslySetInnerHTML={{
                        __html: renderMarkdown(previewRev.content),
                      }}
                    />
                    <div className="mt-4">
                      <Button
                        size="sm"
                        disabled={restoreMutation.isPending}
                        onClick={() => restoreMutation.mutate(previewRev.id)}
                      >
                        {restoreMutation.isPending
                          ? "Restoring…"
                          : "Restore this revision"}
                      </Button>
                    </div>
                  </CardContent>
                </Card>
              ) : (
                <p className="text-sm text-muted-foreground">
                  Select a revision to preview it.
                </p>
              )}
            </div>
          </div>
        </DrawerContent>
      </Drawer>

      {shareOpen && selected && (
        <ShareModal
          resource={`${base}/pages/${encodeURIComponent(selected.id)}/share`}
          onClose={() => setShareOpen(false)}
        />
      )}

      <style>{`
        .wiki-content { font-size: 0.925rem; line-height: 1.7; color: var(--foreground); }
        .wiki-content > *:first-child { margin-top: 0; }
        .wiki-content h1, .wiki-content h2, .wiki-content h3 { font-weight: 600; letter-spacing: -0.01em; margin: 1.25em 0 0.5em; }
        .wiki-content h1 { font-size: 1.5rem; } .wiki-content h2 { font-size: 1.25rem; } .wiki-content h3 { font-size: 1.05rem; }
        .wiki-content p { margin: 0.75em 0; }
        .wiki-content ul, .wiki-content ol { margin: 0.75em 0; padding-left: 1.5rem; }
        .wiki-content ul { list-style: disc; } .wiki-content ol { list-style: decimal; }
        .wiki-content li { margin: 0.25em 0; }
        .wiki-content blockquote { border-left: 3px solid var(--border); padding-left: 0.9rem; margin: 0.75em 0; color: var(--muted-foreground); }
        .wiki-content pre { background: var(--muted); border: 1px solid var(--border); border-radius: 0.5rem; padding: 0.8rem; overflow-x: auto; margin: 0.75em 0; }
        .wiki-content code { font-family: ui-monospace, monospace; font-size: 0.85em; background: var(--muted); border-radius: 0.3rem; padding: 0.1em 0.35em; }
        .wiki-content pre code { background: none; padding: 0; }
        .wiki-content a { color: var(--primary); text-decoration: underline; }
        .wiki-content hr { border: none; border-top: 1px solid var(--border); margin: 1.25em 0; }
      `}</style>
    </div>
  );
}

/* ------------------------------------------------------------------ */
/* Forms                                                               */
/* ------------------------------------------------------------------ */

function PageForm({
  initialTitle,
  initialContent,
  pending,
  error,
  submitLabel,
  onSubmit,
  onCancel,
}: {
  initialTitle: string;
  initialContent: string;
  pending: boolean;
  error: string | null;
  submitLabel: string;
  onSubmit: (title: string, content: string) => void;
  onCancel: () => void;
}) {
  const [title, setTitle] = useState(initialTitle);
  const [content, setContent] = useState(initialContent);
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        onSubmit(title, content);
      }}
      className="space-y-4"
    >
      <div className="space-y-2">
        <Label htmlFor="page-title">Title</Label>
        <Input
          id="page-title"
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder="Page title"
          autoFocus
        />
      </div>
      <div className="space-y-2">
        <Label htmlFor="page-content">Content (markdown)</Label>
        <Textarea
          id="page-content"
          value={content}
          onChange={(e) => setContent(e.target.value)}
          placeholder={"# Heading\n\nWrite in **markdown**…"}
          rows={12}
          className="font-mono text-sm"
        />
      </div>
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      <DialogFooter>
        <Button type="button" variant="outline" onClick={onCancel}>
          Cancel
        </Button>
        <Button type="submit" disabled={pending}>
          {pending ? "Saving…" : submitLabel}
        </Button>
      </DialogFooter>
    </form>
  );
}

function MoveForm({
  target,
  tree,
  pending,
  error,
  onSubmit,
  onCancel,
}: {
  target: Page;
  tree: PageNode[];
  pending: boolean;
  error: string | null;
  onSubmit: (parentId: string | null, position?: number) => void;
  onCancel: () => void;
}) {
  // Exclude the target's own subtree: the backend rejects cycles, but
  // offering them in the picker would be a confusing dead end.
  const excluded = useMemo(() => {
    const find = (nodes: PageNode[]): PageNode | null => {
      for (const n of nodes) {
        if (n.page.id === target.id) return n;
        const hit = find(n.children);
        if (hit) return hit;
      }
      return null;
    };
    const node = find(tree);
    return node ? subtreeIds(node) : new Set<string>([target.id]);
  }, [tree, target.id]);

  const options = useMemo(
    () => flattenForPicker(tree).filter((n) => !excluded.has(n.page.id)),
    [tree, excluded],
  );

  const [parentId, setParentId] = useState<string>(target.parent_id ?? "");
  const [positionRaw, setPositionRaw] = useState("");
  const unchanged =
    parentId === (target.parent_id ?? "") && positionRaw.trim() === "";

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        const position =
          positionRaw.trim() === "" ? undefined : Number(positionRaw);
        if (position !== undefined && (!Number.isInteger(position) || position < 0)) {
          return;
        }
        onSubmit(parentId === "" ? null : parentId, position);
      }}
      className="space-y-4"
    >
      <div className="space-y-2">
        <Label htmlFor="move-parent">Parent</Label>
        <NativeSelect
          id="move-parent"
          value={parentId}
          onChange={(e) => setParentId(e.target.value)}
        >
          <NativeSelectOption value="">(Root — no parent)</NativeSelectOption>
          {options.map((n) => (
            <NativeSelectOption key={n.page.id} value={n.page.id}>
              {"\u00a0\u00a0".repeat(n.depth)}
              {n.page.title}
            </NativeSelectOption>
          ))}
        </NativeSelect>
      </div>
      <div className="space-y-2">
        <Label htmlFor="move-position">
          Position <span className="text-muted-foreground">(optional)</span>
        </Label>
        <Input
          id="move-position"
          inputMode="numeric"
          value={positionRaw}
          onChange={(e) => setPositionRaw(e.target.value)}
          placeholder="Leave empty to append at the end"
        />
      </div>
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      <DialogFooter>
        <Button type="button" variant="outline" onClick={onCancel}>
          Cancel
        </Button>
        <Button type="submit" disabled={pending || unchanged}>
          {pending ? "Moving…" : "Move"}
        </Button>
      </DialogFooter>
    </form>
  );
}
