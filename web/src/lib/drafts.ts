// drafts: Drafts view client (C6T4).
//
// Contract: drafts are issues with is_draft=true.
//   GET .../issues?draft=true&per_page=100 -> {results: Issue[]}
//   PATCH .../issues/:uuid {is_draft:false}  -> publish
//   DELETE .../issues/:uuid                  -> discard
// The working-set default excludes drafts (backend C6T4 decision).

import { api } from "./api";
import type { Issue, IssueListResult } from "./types";

function base(slug: string, identifier: string): string {
  return `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}/issues`;
}

export function fetchDrafts(slug: string, identifier: string): Promise<Issue[]> {
  return api
    .get<IssueListResult>(`${base(slug, identifier)}?draft=true&per_page=100`)
    .then((r) => r.results ?? []);
}

export function publishDraft(
  slug: string,
  identifier: string,
  id: string,
): Promise<Issue> {
  return api.patch<Issue>(`${base(slug, identifier)}/${id}`, {
    is_draft: false,
  });
}

export function discardDraft(
  slug: string,
  identifier: string,
  id: string,
): Promise<void> {
  return api.del<void>(`${base(slug, identifier)}/${id}`);
}

export function draftsKey(slug: string, identifier: string) {
  return ["drafts", slug, identifier] as const;
}
