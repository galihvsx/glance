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
 * Keywords (AND/OR/NOT/IN), field names, priority names, and
 * function names are case-insensitive. Quote names containing spaces.
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
];

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
