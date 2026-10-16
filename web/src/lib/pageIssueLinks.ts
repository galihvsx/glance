// Page↔issue links (C36T1): shared pure helpers + the role hook for the
// two link sections (PageLinkedIssues, IssueLinkedPages). Backend routes
// (C36T0): GET/POST .../pages/{pageID}/issues and
// GET/POST .../issues/{uuid}/pages; duplicate → 409, cross-project →
// 409, unknown → 404. Reads need any member (guest 5+); mutations need
// member (15)+ — the server enforces all of it; the UI just hides what
// would 403, same as PageComments.

import { useQuery } from "@tanstack/react-query";
import { api } from "./api";
import { useAuth } from "./auth";
import { textToTipTapDoc } from "./tiptap";
import type { Issue, Member, Page } from "./types";

/** Minimum workspace role for link/unlink/create mutations (member). */
export const PAGE_ISSUE_LINK_MIN_ROLE = 15;

/**
 * Workspace role gate for the link sections: the caller's role from the
 * members list. `canWrite` is false until the members query resolves, so
 * mutation buttons stay hidden rather than flashing for viewers.
 */
export function usePageIssueLinkCanWrite(slug: string): boolean {
  const { user } = useAuth();
  const membersQuery = useQuery({
    queryKey: ["members", slug],
    queryFn: () =>
      api
        .get<{ members: Member[] }>(
          `/api/v1/workspaces/${encodeURIComponent(slug)}/members`,
        )
        .then((d) => d.members),
  });
  const myRole =
    membersQuery.data?.find((m) => m.id === user?.id)?.role ?? 0;
  return myRole >= PAGE_ISSUE_LINK_MIN_ROLE;
}

/** Plain-text body of the "create issue from this page" description. */
export function buildCreateFromPageDescription(
  pageTitle: string,
  pageUrl: string,
): string {
  return `From page: ${pageTitle}\n${pageUrl}`;
}

/**
 * The exact POST /issues body for "Create issue from this page": title
 * = page title, description = TipTap doc of "From page: <title>" +
 * page URL. The caller links the created issue via POST
 * .../pages/{pageID}/issues and navigates to it.
 */
export function buildCreateFromPageIssueBody(
  pageTitle: string,
  pageUrl: string,
): { name: string; description: unknown } {
  return {
    name: pageTitle,
    description: textToTipTapDoc(
      buildCreateFromPageDescription(pageTitle, pageUrl),
    ),
  };
}

/** Public URL of a project's Pages view (the page-URL half of the
 *  create-from-page description). */
export function pageUrlForPage(
  slug: string,
  identifier: string,
  origin: string,
): string {
  return `${origin}/w/${encodeURIComponent(slug)}/p/${encodeURIComponent(identifier)}/pages`;
}

export interface FlattenedPage {
  page: Page;
  depth: number;
}

/**
 * Flat API page list → tree-flattened picker order (same traversal as
 * Pages.tsx's flattenForPicker: siblings by (position, title), depth
 * first). Orphaned parent_ids become roots; a parent cycle degrades to
 * a single visit per page instead of looping forever.
 */
export function flattenPagesForPicker(pages: Page[]): FlattenedPage[] {
  interface Node {
    page: Page;
    children: Node[];
    depth: number;
  }
  const byId = new Map<string, Node>();
  for (const p of pages) byId.set(p.id, { page: p, children: [], depth: 0 });
  const roots: Node[] = [];
  for (const p of pages) {
    const node = byId.get(p.id)!;
    const parentId = p.parent_id ?? null;
    if (parentId && byId.has(parentId) && parentId !== p.id) {
      byId.get(parentId)!.children.push(node);
    } else {
      roots.push(node);
    }
  }
  const byPos = (a: Node, b: Node) =>
    a.page.position - b.page.position ||
    a.page.title.localeCompare(b.page.title);
  const fix = (nodes: Node[], depth: number) => {
    nodes.sort(byPos);
    for (const n of nodes) {
      n.depth = depth;
      fix(n.children, depth + 1);
    }
  };
  fix(roots, 0);
  const out: FlattenedPage[] = [];
  const seen = new Set<string>();
  const walk = (n: Node) => {
    if (seen.has(n.page.id)) return;
    seen.add(n.page.id);
    out.push({ page: n.page, depth: n.depth });
    n.children.forEach(walk);
  };
  roots.forEach(walk);
  // Any page unreachable from a root (cycle remnants) still shows up.
  for (const p of pages) {
    if (!seen.has(p.id)) out.push({ page: p, depth: 0 });
  }
  return out;
}

/**
 * Picker scoping for the link-issue picker: only this project's live
 * issues — already-linked issues, drafts, and archived issues are not
 * linkable targets. The list endpoint is already project-scoped; this
 * is the client-side half (same contract as IssueLinks' search, which
 * excludes the current issue).
 */
export function filterLinkableIssues(
  issues: Issue[],
  linkedIds: Iterable<string>,
): Issue[] {
  const linked = new Set(linkedIds);
  return issues.filter(
    (i) => !linked.has(i.id) && !i.is_draft && !i.archived_at,
  );
}
