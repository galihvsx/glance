// groupByCreatedBy (C134T1): pure grouping logic for the issue
// list's Group-by-Created-by display mode. Kept framework-free so it
// is cheaply unit-testable; Issues.tsx wires it into the existing
// grouping path. Mirrors groupByRelease.ts (C133T1) shape, with the
// one honest twist the cycle-134 plan names: the creator NAME does
// not ride the row — Issue.created_by is a user id, and the display
// label resolves from a workspace members lookup map passed in as an
// argument (map user id -> {name, email}).
//
// Decided semantics (cycle-134 plan, mirroring groupByRelease.ts):
// - SINGLE-APPEARANCE RULE: an issue appears under exactly ONE
//   creator group — its creator — never under several. created_by
//   is non-nullable and server-set to the actor at create time, so
//   every issue has exactly one creator by construction.
// - Groups are keyed by creator id; the display label is
//   `name ?? email` from the members map. A raw user id is NEVER a
//   group label.
// - Creators with no matching member (e.g. a removed member) collapse
//   into ONE "Unknown" group, ALWAYS last, HIDDEN when empty
//   (callers only get this entry when it has members — same contract
//   as groupByRelease's Ungrouped). There is no per-creator Ungrouped:
//   every issue has a creator, so the only unresolved case is Unknown.
// - Creator groups keep first-appearance order across the input list
//   (Unknown excepted: always last, wherever its first issue appears).
// - Creator groups carry NO color field: members carry no color —
//   same rule as cycles/modules/releases (name + count, no dot).
// - NO move semantics live here: created_by is immutable, so the
//   page sets no drop-target fields on these groups and the headers
//   are not droppable (see Issues.tsx).

import type { Issue } from "../../lib/types";

export interface CreatedByIssueGroup {
  key: string;
  title: string;
  issues: Issue[];
  isUnknown: boolean;
}

/** One workspace member's display identity, as the members endpoint
 *  returns it (Member.id IS the user id Issue.created_by references).
 *  Name is nullable on the wire — label falls back to email. */
export interface CreatorLookupEntry {
  name?: string | null;
  email: string;
}

export const UNKNOWN_CREATOR_KEY = "createdby:unknown";

/** The display label for one creator id: `name ?? email` from the
 *  lookup, or null when the creator has no matching member (the
 *  caller collapses those into the Unknown group). Never returns a
 *  raw user id. */
export function creatorLabel(
  creatorId: string,
  membersById: ReadonlyMap<string, CreatorLookupEntry>,
): string | null {
  const member = membersById.get(creatorId);
  if (!member) return null;
  return member.name ?? member.email;
}

/** Group issue rows by creator. Known-creator groups keep
 *  first-appearance order across the input list; Unknown is last and
 *  hidden when empty. Defensive: a row with a missing/empty
 *  created_by groups as Unknown, never crashes, never labels by id. */
export function groupIssuesByCreatedBy(
  issues: Issue[],
  membersById: ReadonlyMap<string, CreatorLookupEntry>,
): CreatedByIssueGroup[] {
  const byCreator = new Map<string, { title: string; issues: Issue[] }>();
  const unknown: Issue[] = [];
  for (const issue of issues) {
    const creatorId = issue.created_by;
    if (!creatorId) {
      unknown.push(issue);
      continue;
    }
    const label = creatorLabel(creatorId, membersById);
    if (label === null) {
      unknown.push(issue);
      continue;
    }
    // Single-appearance: the issue is appended to exactly ONE group.
    let group = byCreator.get(creatorId);
    if (!group) {
      group = { title: label, issues: [] };
      byCreator.set(creatorId, group);
    }
    group.issues.push(issue);
  }
  const groups: CreatedByIssueGroup[] = [];
  for (const [id, g] of byCreator) {
    groups.push({
      key: id,
      title: g.title,
      issues: g.issues,
      isUnknown: false,
    });
  }
  // Unknown is always last and hidden when empty (callers only get
  // this entry when it has members).
  if (unknown.length > 0) {
    groups.push({
      key: UNKNOWN_CREATOR_KEY,
      title: "Unknown",
      issues: unknown,
      isUnknown: true,
    });
  }
  return groups;
}
