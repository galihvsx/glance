import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../../lib/api";

/** Issue links (C8T0): directed dependency edges between issues, backed by
 *  GET/POST .../issues/{uuid}/links and DELETE .../links/{linkID}.
 *
 *  The backend stores ONE directed row per edge with a tiny kind
 *  vocabulary ("blocks", "relates_to", "duplicates" — the service
 *  enforces this; the column has no CHECK so the vocabulary may grow).
 *  "Is blocked by" is the SAME edge addressed from the other side,
 *  never a separate kind: the list endpoint returns every edge touching
 *  the issue with a derived direction ("outgoing" = this issue is the
 *  source, "incoming" = this issue is the target). This module derives
 *  the display groups from that pair. */

export interface IssueLink {
  id: string;
  issue_id: string;
  target_issue_id: string;
  kind: string;
  created_at: string;
  direction?: "outgoing" | "incoming";
}

/** The backend's link-kind vocabulary (service.issueLinkKinds). Adding a
 *  kind here without a backend change produces a 400 — keep in sync. */
export const LINK_KINDS = [
  { value: "blocks", label: "Blocks" },
  { value: "relates_to", label: "Relates to" },
  { value: "duplicates", label: "Duplicates" },
] as const;
export type IssueLinkKind = (typeof LINK_KINDS)[number]["value"];

/** Display group keys. The directed kinds split into two groups each
 *  (outgoing/incoming); "relates_to" is shown as one group. Unknown
 *  kinds (future backend vocabulary) fall through as their raw kind. */
export type IssueLinkGroupKey =
  | "blocks"
  | "blocked_by"
  | "relates_to"
  | "duplicates"
  | "duplicated_by";

export const LINK_GROUP_LABELS: Record<IssueLinkGroupKey, string> = {
  blocks: "Blocks",
  blocked_by: "Is blocked by",
  relates_to: "Relates to",
  duplicates: "Duplicates",
  duplicated_by: "Duplicated by",
};

/** Canonical group order on the page: outgoing first, mirrors last. */
export const LINK_GROUP_ORDER: IssueLinkGroupKey[] = [
  "blocks",
  "blocked_by",
  "relates_to",
  "duplicates",
  "duplicated_by",
];

export interface LinkGroup {
  key: string;
  label: string;
  links: IssueLink[];
}

function humanizeKind(kind: string): string {
  return kind
    .split("_")
    .filter(Boolean)
    .map((w) => w.charAt(0).toUpperCase() + w.slice(1))
    .join(" ");
}

/** The issue on the other end of the edge. Derived from the endpoint
 *  ids (not `direction`) so it stays correct even if direction is
 *  absent (e.g. the batch endpoint leaves it empty). */
export function otherIssueId(link: IssueLink, currentId: string): string {
  return link.issue_id === currentId ? link.target_issue_id : link.issue_id;
}

/** Display group for one link from the current issue's perspective. */
export function linkGroupKey(link: IssueLink, currentId: string): string {
  const outgoing = link.issue_id === currentId;
  if (link.kind === "blocks") return outgoing ? "blocks" : "blocked_by";
  if (link.kind === "duplicates")
    return outgoing ? "duplicates" : "duplicated_by";
  // "relates_to" is symmetric on screen; anything else is a future kind
  // passed through raw so nothing renders blank.
  return link.kind;
}

/** Human label for a group key; unknown kinds are humanized raw. */
export function linkGroupLabel(key: string): string {
  return (
    LINK_GROUP_LABELS[key as IssueLinkGroupKey] ?? humanizeKind(key) ?? key
  );
}

/** Links grouped for display, in LINK_GROUP_ORDER; unknown-kind groups
 *  trail in alphabetical order. */
export function groupLinks(
  links: IssueLink[],
  currentId: string,
): LinkGroup[] {
  const byKey = new Map<string, IssueLink[]>();
  for (const link of links) {
    const key = linkGroupKey(link, currentId);
    const bucket = byKey.get(key);
    if (bucket) bucket.push(link);
    else byKey.set(key, [link]);
  }
  const known = LINK_GROUP_ORDER.filter((k) => byKey.has(k));
  const extra = [...byKey.keys()]
    .filter((k) => !(LINK_GROUP_ORDER as string[]).includes(k))
    .sort();
  return [...known, ...extra].map((key) => ({
    key,
    label: linkGroupLabel(key),
    links: byKey.get(key)!,
  }));
}

export function linksTitle(count: number): string {
  return count === 0 ? "Linked issues" : `Linked issues (${count})`;
}

export function removeLinkMessage(title: string): string {
  return `Remove the link to "${title}"? The other issue is not affected.`;
}

/** Base path for an issue's link endpoints. */
export function issueLinksPath(
  slug: string,
  identifier: string,
  uuid: string,
): string {
  return `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}/issues/${encodeURIComponent(uuid)}/links`;
}

/** Issue-link data + mutations for the detail page. Errors are left to
 *  the caller (honest backend messages), but remove always refreshes
 *  the list — a 404 means the link was already gone, which is exactly
 *  what a refresh shows. */
export function useIssueLinks(
  slug: string,
  identifier: string,
  uuid: string,
) {
  const queryClient = useQueryClient();
  const linksPath = issueLinksPath(slug, identifier, uuid);
  const key = ["issue-links", slug, identifier, uuid];

  const listQuery = useQuery({
    queryKey: key,
    queryFn: () =>
      api
        .get<{ links: IssueLink[] }>(linksPath)
        .then((d) => d.links),
  });

  function invalidate() {
    void queryClient.invalidateQueries({ queryKey: key });
  }

  const addMutation = useMutation({
    mutationFn: (input: { targetIssueId: string; kind: IssueLinkKind }) =>
      api.post<IssueLink>(linksPath, {
        target_issue_id: input.targetIssueId,
        kind: input.kind,
      }),
    onSettled: () => invalidate(),
  });

  const removeMutation = useMutation({
    mutationFn: (linkId: string) =>
      api.del(`${linksPath}/${encodeURIComponent(linkId)}`),
    onSettled: () => invalidate(),
  });

  return {
    links: listQuery.data ?? [],
    isLoading: listQuery.isPending,
    isError: listQuery.isError,
    refetch: listQuery.refetch,
    addMutation,
    removeMutation,
  };
}
