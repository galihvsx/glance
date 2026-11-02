// PQL frontend helpers (C91T1). The deterministic Plane Query Language
// shipped by the C91T0 backend as `?pql=` on the project issue-list
// endpoint. This module is the single place where the frontend knows
// the language: the field/operator cheat-sheet, the exclusivity rule
// for PQL requests, and the server 400 message + position formatting.
// Field names, aliases, and functions below mirror the T0 contract
// (internal/service/pql.go + the OpenAPI `pql` parameter doc) —
// never extend this table from guesswork; a field the backend does
// not know is a server 400, not a client feature.
import { ApiError } from "./api";

/** One cheat-sheet row: a copyable example and what it matches. */
export interface PqlCheatEntry {
  example: string;
  meaning: string;
}

/**
 * The documented field table (C91T0). Name fields accept =/!=/in;
 * priority and dates accept all comparison operators plus in.
 * The project field (C114T0) accepts =/!=/in like a name field but
 * is workspace-scoped only: on a project-scoped list the server
 * rejects it with a 400, since the list is already one project.
 * Keywords (AND/OR/NOT/IN), field names, priority names, and
 * function names are case-insensitive. Quote names containing spaces.
 * A trailing ORDER BY clause (C98T0, multi-key C99T0) sorts by one or
 * more comma-separated keys from updated_at, created_at, sequence_id,
 * sort_order, or priority, each with an optional ASC/DESC direction
 * (default ASC per key); it wins over the order_by param, and an
 * unknown or duplicate sort field, a trailing comma, or a missing
 * field after a comma is a 400. The order_by param itself accepts
 * the same idea as a comma-separated list (C99T1, e.g.
 * order_by=priority,-updated_at), with the same 400 rules.
 * A trailing LIMIT clause (C98T1, after ORDER BY or standalone)
 * caps the page to at most min(per_page, n) rows with next_cursor
 * always null (single page); a cursor combined with LIMIT, a second
 * LIMIT, LIMIT before ORDER BY, or an out-of-range/non-integer LIMIT
 * is a 400.
 */
export const PQL_CHEAT_SHEET: PqlCheatEntry[] = [
  { example: "state = Todo", meaning: "State by name (project-scoped)" },
  {
    example: 'state in (Todo, "In Progress")',
    meaning: "State is one of the listed names",
  },
  {
    example: "priority = High",
    meaning: "Priority by name: none, low, medium, high, urgent",
  },
  {
    example: "priority >= 3",
    meaning: "Priority by number 0–4 (also <, >, <=, !=, <>)",
  },
  {
    example: "assignee = me",
    meaning: "Assigned to the current user (also a member name, email, or none)",
  },
  {
    example: 'assignee = "Jane Doe"',
    meaning: "Assigned to the named member (workspace-scoped)",
  },
  { example: "hasNoAssignee()", meaning: "Issues with no assignees" },
  { example: "hasAssignee()", meaning: "Issues with at least one assignee" },
  {
    example: "label = bug",
    meaning: "Label by name (workspace-scoped)",
  },
  {
    example: "label in (bug, frontend)",
    meaning: "Label is one of the listed names",
  },
  {
    example: "project = ABC",
    meaning: "Project by key or name (workspace lists like MyWork only)",
  },
  {
    example: "epic = Platform",
    meaning: "Epic by name (project-scoped; also none for issues in no epic)",
  },
  {
    example: "epic in (Platform, none)",
    meaning: "Epic is one of the listed names, or in no epic",
  },
  {
    example: "estimate = S",
    meaning:
      "Estimate-point key within the project's estimate system (also none for unestimated issues)",
  },
  {
    example: "estimate in (S, M)",
    meaning: "Estimate point is one of the listed keys",
  },
  {
    example: "parent = ENG-123",
    meaning:
      "Parent issue by key {IDENTIFIER}-{sequence_id} (project-scoped; also none for top-level issues)",
  },
  {
    example: "parent in (ENG-123, none)",
    meaning: "Parent is the listed issue, or the issue is top-level",
  },
  { example: "hasParent()", meaning: "Issues with a parent" },
  {
    example: "hasChildren()",
    meaning: "Issues with at least one live child (archived, deleted, and draft children do not count)",
  },
  {
    example: "due > 2026-11-01",
    meaning: "Target date (aliases: due_date, target_date)",
  },
  {
    example: "start <= 2026-11-01",
    meaning: "Start date (alias: start_date)",
  },
  {
    example: "created >= 2026-10-01",
    meaning: "Created date (alias: created_at; YYYY-MM-DD or RFC3339)",
  },
  {
    example: "updated < 2026-11-01",
    meaning: "Updated date (alias: updated_at; YYYY-MM-DD or RFC3339)",
  },
  {
    example: "NOT hasNoAssignee()",
    meaning: "Combine with AND, OR, NOT, and parentheses",
  },
  {
    example: "priority >= 3 ORDER BY priority DESC",
    meaning:
      "Sort by one key (updated_at, created_at, sequence_id, sort_order, or priority); wins over the order_by param",
  },
  {
    example: "state = Todo ORDER BY created_at",
    meaning: "Direction defaults to ASC when omitted (also ORDER BY updated_at DESC)",
  },
  {
    example: "priority >= 3 ORDER BY priority DESC, updated_at DESC",
    meaning:
      "Sort by several keys: the second key decides when the first ties (same keys, any direction each)",
  },
  {
    example: "state = Todo ORDER BY priority DESC, sequence_id",
    meaning:
      "The same list as an order_by param is order_by=-priority,sequence_id (the param uses - for DESC, no ASC/DESC words)",
  },
  {
    example: "state = Todo LIMIT 10",
    meaning:
      "At most 10 rows, single page (next_cursor is always null; a cursor cannot be combined with LIMIT)",
  },
  {
    example: "priority >= 3 ORDER BY priority DESC LIMIT 10",
    meaning:
      "LIMIT composes after ORDER BY; without ORDER BY it uses the default order (1–100, no clamping)",
  },
];

/**
 * Whether the applied PQL query carries a LIMIT clause (C98T1).
 * LIMIT is always the trailing clause, so the check is "LIMIT
 * <integer>" at the end of the query, with quoted literals blanked
 * first so a value like "LIMIT 10" never matches (and a bare
 * limit-like value without a number, e.g. label = limit, does not
 * either). The PQL modes use this — the one place that knows the
 * language — to skip their cursor loop and fetch a single page
 * instead; the server remains authoritative (an invalid LIMIT still
 * 400s on the first fetch, and next_cursor null stops any loop).
 */
export function pqlHasLimit(query: string): boolean {
  const unquoted = query.replace(/'(?:[^']|'')*'|"(?:[^"]|"")*"/g, '""');
  return /\bLIMIT\s+\d+$/i.test(unquoted.trim());
}

/** The applied PQL query carried by IssueFilters, normalized: "" when
 *  the filters carry no query (old views predate PQL and have none). */
export function pqlOf(filters: { pql?: string } | null | undefined): string {
  return filters?.pql?.trim() ?? "";
}

export interface PqlRequestOptions {
  orderBy: string;
  perPage: number;
  cursor?: string;
}

/**
 * PQL request params (exclusivity rule, C91T0): a PQL request sends
 * ONLY the `pql` predicate plus the list endpoint's pagination /
 * ordering params. Visual filter params must never leak into it —
 * the backend would ignore them, but the frontend must not send a
 * request that claims two filter modes at once.
 */
export function buildPqlRequestParams(
  query: string,
  opts: PqlRequestOptions,
): URLSearchParams {
  const p = new URLSearchParams();
  p.set("pql", query.trim());
  p.set("per_page", String(opts.perPage));
  p.set("order_by", opts.orderBy);
  if (opts.cursor) p.set("cursor", opts.cursor);
  return p;
}

/** Extract the 1-based error column from a server error envelope's
 *  details, when the backend attached one (C91T0: details.position). */
export function pqlErrorPosition(error: unknown): number | null {
  if (error instanceof ApiError) {
    const details = error.details as { position?: unknown } | undefined;
    if (
      details &&
      typeof details === "object" &&
      typeof details.position === "number" &&
      Number.isFinite(details.position) &&
      details.position > 0
    ) {
      return details.position;
    }
  }
  return null;
}

/**
 * Format a failed PQL apply for inline display in the editor: the
 * server's 400 message verbatim, plus its column when the server
 * named one and the message does not already carry it. The message
 * is never paraphrased — the column is what lets the user find the
 * offending token. Non-API failures fall back to the Error message.
 */
export function formatPqlError(error: unknown): string {
  if (error instanceof ApiError) {
    const message = error.message || `Request failed (${error.status})`;
    const pos = pqlErrorPosition(error);
    if (pos !== null && !message.includes("at column")) {
      return `${message} (at column ${pos})`;
    }
    return message;
  }
  if (error instanceof Error && error.message) return error.message;
  return "Failed to apply the PQL query";
}
