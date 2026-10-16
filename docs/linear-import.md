# Importing issues from Linear (C31T0)

One-way, one-shot import of a Linear team's issues into a glance project
via Linear's GraphQL API. Same shape as the GitHub/Jira/Trello importers:
team picker → preview → import.

## Endpoints

All three live under the project import group and require a workspace
member (role 15+). The Linear API key travels in the JSON request body
only — it is sent to `api.linear.app` as a Bearer header and is **never**
stored, never logged, and never echoed in error messages.

```
POST /api/v1/workspaces/:slug/projects/:identifier/imports/linear/teams
  req  { api_key: string }
  →    { teams: [{ id, key, name }] }

POST /api/v1/workspaces/:slug/projects/:identifier/imports/linear/preview
  req  { api_key: string, team_id: string, max?: number }
  →    { issues: [{ identifier, title, state, priority, labels,
                    assignee_email, assignee_matched, due_date, url }][first 10],
         total: number, assignee_misses: number }
  No writes.

POST /api/v1/workspaces/:slug/projects/:identifier/imports/linear
  req  { api_key: string, team_id: string, max?: number }
  →    { created, skipped, failed,
         errors: [{ identifier, message }], assignee_misses: number }
```

`max` caps the total fetched: default 100, hard cap 500. Pagination is
cursor-based (`first: 50`, `after`).

Errors: bad key / Linear 401 → **502 with a generic message** ("linear
authentication failed") — Linear's message is deliberately not surfaced,
so the key can never leak through it. Linear GraphQL `errors[]` → 502.
Team not found → 400. Rate limit → 429 with `Retry-After` (never retried
silently).

## Mapping

| Linear | glance |
|---|---|
| title (fallback: identifier) | name |
| description (Markdown, kept as-is) + footer marker | description |
| `state.type` → group; state name matched case-insensitively, otherwise a new state is created in the mapped group | state |
| priority 0→0, 1→4, 2→3, 3→2, 4→1 | priority |
| label names → labels, creating missing ones (default gray `#6b7280`) | labels |
| assignee email matched case-insensitively against workspace members (misses counted, never fatal) | assignee |
| comments (Markdown, attributed, original `createdAt`) | comments |
| `dueDate` (date-only) | target_date |

State-type → group: `backlog`→`backlog`, `unstarted`→`unstarted`,
`started`→`started`, `completed`→`completed`, `canceled`→`cancelled`
(spelling differs), `triage`→`triage`. Unknown types fall back to the
neutral `unstarted` group.

One deliberate deviation from a literal "match within the target group":
states carry a `(project_id, name)` uniqueness constraint, so a
same-named state in another group cannot be duplicated — it is reused.
The mapped group applies at creation time.

Sub-issues, parent links, cycles, initiatives, triage queues, and
projects are **not** imported (flat issues). Attachments are not
fetched. Linear's own label colors are not carried over — created
labels get the default gray, same as the GitHub/Jira importers.

## Dedupe / idempotency

Every imported issue gets a footer marker in its description:

```
*Imported from [ENG-123](https://linear.app/acme/issue/ENG-123/ship-v2)*
```

Before inserting, the run scans every live issue description in the
project for previously imported Linear identifiers. The parser reads
the identifier out of the marker and requires the closing paren, so
ENG-1 can never match ENG-12. A re-run skips already-imported issues
and reports them as `skipped`. Deleting the footer defeats dedupe
(same as GitHub/Jira). Dedupe is keyed by identifier — the team-key
prefix makes cross-team collisions unlikely; two different Linear
workspaces reusing the same team key could collide, and that case is
accepted rather than solved with a migration.

## Two-phase import

All GraphQL fetches complete before any DB write; the insert phase
runs in one transaction. Per-issue errors (missing title, missing
state) are collected and never abort the batch. Imported comments are
inserted as raw rows — they do NOT fan out comment notifications or
webhook events.

## SSRF safety

Host pinned to `https://api.linear.app` (exact match, https-only, no
redirects), per-request timeout, 8MB response cap. The team id travels
as a GraphQL variable — never interpolated into a URL, query string,
or SQL. The `after` cursor is opaque and echoed back in variables
only; a repeated cursor aborts as a pagination loop.

## Honest v1 limitations

- Import is one-way, one-shot: later Linear edits do not sync;
  re-running imports only NEW issues, it does not update previously
  imported ones.
- Assignee matching is best-effort by email; Linear users without a
  matching glance workspace member import unassigned.
- Deleting the footer marker defeats dedupe.
- Linear Markdown bodies are stored as-is; glance renders them with
  its existing Markdown path — rendering fidelity may differ from
  Linear's.
- Personal API key only in v1 (no Linear OAuth); no two-way sync /
  webhooks back to Linear.
