import type { Issue, IssueChild } from "../../lib/types";

/** Nil-safe child list: the detail payload omits `children` entirely
 *  unless it was fetched with ?include_children=1. */
export function childRows(children: Issue["children"]): IssueChild[] {
  return children ?? [];
}

/** Section header label: plain "Sub-issues" when empty, counted otherwise. */
export function subIssuesTitle(children: IssueChild[]): string {
  return children.length === 0
    ? "Sub-issues"
    : `Sub-issues (${children.length})`;
}

/** Confirm copy for detaching a child from its parent. */
export function removeChildMessage(title: string): string {
  return `Remove "${title}" from its parent? It will become a top-level issue.`;
}
