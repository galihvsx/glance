# Changelog

All notable changes to glance are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [v0.2.0] - unreleased

"Credibility" milestone: the highest value-per-cost Plane parity gaps,
plus reliability and production-readiness hardening.

### Added

**Views**
- Spreadsheet view: full-field sortable table (10 columns, sticky header
  + ID column, horizontal scroll), wired into the project view switcher.
- Peek view: clicking a row/card opens the issue detail in a right
  drawer (`?peek=<uuid>`, route unchanged) instead of full-page
  navigation; list context (selection, scroll, filters) is preserved.
  Full-page detail remains available via "Open full page".
- Inline "+ New work item" quick-add: per-state-group rows on the list
  view and per-column rows on the kanban board; Enter creates, Esc
  cancels, expand icon hands the typed title to the full create form.
- Display properties panel: field toggles (state/priority/labels/
  assignees/dates), group-by (state/priority/none), order-by,
  hide-empty-groups — persisted per project in localStorage and applied
  live to the list and board views.

**Spec gaps closed**
- `GET /api/v1/work-items/{display-id}`: global `{IDENTIFIER}-{SEQ}`
  lookup (e.g. `ENG-123`), same shape as GET-by-UUID. Unknown,
  malformed, ambiguous, and non-member lookups share one 404 — no
  enumeration oracle.
- `GET /api/v1/search?q=`: global full-text search (tsvector,
  ts_rank-ordered), scoped to the caller's workspaces, cursor
  pagination.

**Theming**
- Dark theme toggle (class-based, persisted, defaults to dark), in every
  page header; no-flash bootstrap in `index.html`.

**Production**
- `--health-check` flag: the binary probes its own `/health`
  (distroless has no shell/curl); `HEALTHCHECK` added to the Dockerfile.

### Fixed

- Parallel `go test ./...` migration race: `Migrate()` now runs in one
  transaction guarded by `pg_advisory_xact_lock` (transaction-scoped,
  auto-released); new `TestMigrateConcurrent` regression test.

### Security

- `users.is_active` is now enforced: deactivated users' sessions are
  rejected and neither login path mints new ones (`ErrUserDeactivated`
  → 401).

## [v0.1.0] - 2026-10-06

First release. glance is a lean, self-hosted issue tracker (Plane-like):
a single Go binary serving the API and an embedded React SPA,
Postgres-only, passwordless auth, Apache-2.0.

### Added

**Scaffolding**
- Single-binary Go monolith (Echo v5) with the React SPA embedded via
  `go:embed`; forward-only SQL migrations run automatically at boot.
- `Dockerfile` (multi-stage: Node SPA build → Go build → distroless
  runtime) and `docker-compose.yml` (app + Postgres 16).

**Auth (passwordless)**
- Email OTP login: 6-digit codes, HMAC-peppered hashes, 5 attempts per
  code, per-email/per-IP rate limits. `OTP_PEPPER` is required at boot —
  the server refuses to start without it (fail closed).
- Google and GitHub OAuth login (PKCE/state, account linking).
- Cookie sessions with rotation and idle-timeout refresh.

**Workspaces & projects**
- Workspaces with slugs as immutable addressing keys; projects with
  identifiers unique per workspace; role-based membership
  (owner/admin/member/guest).
- `GET /workspaces/{slug}/members` for member management (assignee
  picker).

**Issues**
- Issue CRUD with atomic per-project sequences, sub-issues, priorities,
  estimates, dates.
- List with filters, cursor pagination, and delta sync; full-text search.
- Labels (hierarchical, cycle-safe) and assignees with real
  `assignee=`/`label=` filters.
- Bulk operations with `Idempotency-Key` support (byte-identical replay).
- Comments, issue relations, version history with restore, and an
  activity feed.
- Web: issue list, detail view, labels/assignees pickers.

**Intake triage**
- Inbox for un-triaged issues: accept to backlog, reject/duplicate
  (soft-delete), snooze with a snoozed-issues view.

**Cycles & board**
- Cycles with progress snapshots and configurable rollover of incomplete
  issues on completion.
- Kanban board with drag-and-drop (dnd-kit, incl. keyboard dragging),
  midpoint ordering, and rebalance.

**Realtime**
- In-process WebSocket hub broadcasting issue/intake/cycle changes.
  Project channels are workspace-qualified —
  `project:{slug}:{identifier}` (slugs are the immutable addressing key;
  identifiers are unique per workspace only).
- Web realtime client, Cmd+K command palette, keyboard shortcuts.

**Notifications & webhooks**
- In-app notifications with a transactional outbox dispatcher.
- Outgoing webhooks: HMAC-signed deliveries, exponential-backoff retry,
  delivery-time SSRF guard (private/loopback/link-local targets rejected
  after delivery-time DNS resolution, dial pinned to the resolved IP).

**API tokens**
- Personal API tokens (`gl_` prefix, SHA-256 stored hashes, shown once),
  read/write scopes, 600 req/min rate limit, cookie-only management
  endpoints.

**Performance**
- Verified v1 budgets on a 2 vCPU VM (see `docs/perf.md`): issue list
  p95 3.54ms (< 100ms), detail p95 0.42ms (< 300ms), search p95 3.19ms
  (< 200ms), cold start 0.06–0.14s (< 2s), idle RSS 14.9MB (< 256MB).
- `internal/perf` budget tests fail the suite on regression; composite
  index on the hot list path (`000016_perf_indexes`).

**Hardening**
- Keyboard + a11y audit: all board/list/intake actions keyboard
  reachable (focusable action cards, keyboard drag on the board, visible
  focus rings).
- OTP verify/request budgets immune to budget-squatting login DoS
  (verify budget consumed only against a live code row; request budget
  keyed per email+IP).
- Malformed UUID path parameters return 400 `bad_request` (no 500s from
  `::uuid` casts).

### Changed
- WebSocket project channel addressing is `project:{slug}:{identifier}`
  (workspace-qualified) — identifiers are unique per workspace only.

### Notes
- Docker: `docker compose up` requires `OTP_PEPPER` in the environment
  (the compose file fails fast with instructions if it is unset).
  The Dockerfile/compose files are line-reviewed but have not been
  executed here — no Docker daemon is available in this build
  environment; run the first real `docker build` / `docker compose up`
  on a Docker-capable host before publishing images.

### Deferred (post-v1)
Intentionally absent from v1; each ships with its own forward-only
migration — no big-bang schema: `pages`, `issue_views`, `attachments`,
`favorites`, module UI, gantt, calendar, roadmaps, analytics,
spreadsheet, public boards, admin panel.
