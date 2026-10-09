# Changelog

All notable changes to glance are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [v0.3.0] - unreleased

"Depth" milestone: the next tier of the Plane parity backlog — modules,
wiki pages, calendar, workspace settings, time tracking — plus the
queued onboarding follow-up (invites endpoint).

### Added

**Admin panel**
- Instance administration (C5T0 backend, C5T1 frontend): user management
  (list, deactivate, role), workspace oversight, instance stats.
  AdminGuard is UX-only — the server is the real gate.

**AI assistance**
- Draft-with-AI and triage suggestions (C5T2 backend, C5T3 frontend):
  `GET /api/v1/ai/status` (config probe, reveals only a boolean),
  draft generation (text inserted for review, never auto-saved),
  triage suggestions with per-row Apply (unmatched labels greyed out,
  never applied). Provider-agnostic; degrades honestly when unconfigured.

**Sub-issues**
- Parent/child issue hierarchy (C5T4 backend, C5T5 frontend):
  `?include_children=1` on issue detail, parent breadcrumb,
  sub-issue list with add/detach, create-with-parent.

**Connections & interop (cycle 8)**
- Issue links UI (C8T0): visual dependency management on the Gantt/link endpoints.
- @mentions in comments (C8T1): autocomplete + notifications.
- GitHub issues importer (C8T2): bring issues over from GitHub.
- Issues export CSV/JSON (C8T3).
- Clone issue (C8T4): duplicate with labels/custom values.
- Project overview page (C8T5): composed single-endpoint dashboard tab.
- Due-date reminders (C8T6): batched scanner, atomic claim, watcher/assignee notify.
- Duplicate detection on create (C8T7): beyond-parity similarity check.

**App shell**
- Plane-style persistent shell wrapping all protected routes (`/login`,
  `/onboarding`, `/s/:token` stay chromeless): 240px sidebar ↔ 48px icon
  rail (persisted), left drawer on mobile, sticky 48px top bar with
  context label.
- Sidebar sections: workspace switcher dropdown, Triage (Home / My Work /
  Notifications with unread badge), Favorites, searchable/collapsible
  project list, footer with account menu (Profile, API tokens, Admin,
  Log out) + theme toggle.
- ProjectNav becomes a sticky horizontal tab bar via portal into the
  shell's `#shell-project-tabs` slot — no page rewrites.
- Notifications slide-over (400px Sheet) sharing the `/notifications`
  query cache; "View all" jumps to the full page.
- Keyboard: `Cmd/Ctrl+B` toggle sidebar (typing-guard aware, single owner),
  `g h` / `g m` / `g n`, `?` cheatsheet, `Esc` closes overlays.
- Per-page global chrome removed (bell, theme toggle, logo top bars);
  page-specific headers untouched.

**Issue templates**
- Reusable issue blueprints (C7T0 backend, C7T1 frontend): migration
  000027, CRUD, `POST .../templates/{id}/apply` (validates stale refs
  → 404). UI: project settings section, template picker in the create
  flow with prefill + clear.

**Custom fields**
- Project-level custom fields — a Plane-paywalled feature, free in
  glance (C7T2 backend, C7T3 frontend): migrations 000029/000030,
  types text|number|date|select|checkbox, type-validated values,
  `?include_custom=1` on issue detail. UI: settings section + issue
  detail editors. `required` is stored-not-enforced (documented).

**Favorites**
- Star issues and projects (C7T4): migration 000028, idempotent
  star/unstar, per-user isolation, sidebar Favorites section.

**Archived issues**
- Dedicated Archived view (C7T5): list, unarchive (PATCH
  `{archived}` — backend gap closed in review), delete.

**Keyboard shortcuts**
- Cheatsheet (`?`), g-chords (`g m` → My work, `g h` → Home),
  app-wide listener, shortcut registry with tests (C7T6).

**OpenAPI**
- Hand-written OpenAPI 3.0 spec for all 177 `/api/v1/` routes
  (C7T7): `docs/openapi.yaml` (JSON-syntax YAML, zero deps),
  served at `GET /api/v1/openapi.json`, route-coverage test keeps
  it honest.

**Workspace invites**
- `POST /api/v1/workspaces/{slug}/invites`: admin-only, adds
  registered users as members by email; per-email statuses
  (invited / already-member / not-registered / invalid). Onboarding
  step 3 now calls it — registered teammates join immediately,
  unknown emails stay as on-device "Pending" with honest copy.

**Modules**
- Full-stack modules (Plane "modules" = epics): migration 000017
  (dates; tables shipped in 000012), CRUD + issue assignment via the
  `module_issues` junction, delete guard (409 unless `?force=true`),
  member-scoped. UI: project nav tab, list/detail pages, progress
  header, assign/remove issues, create/edit modal.

**Wiki pages**
- Full-stack pages: migration 000018 (`pages` + `page_revisions`),
  hierarchy (self-FK, cascade), move/reorder with cycle guard,
  revision history + restore (undoable). UI: tree sidebar, page view
  with a hand-rolled XSS-safe markdown renderer (zero new deps),
  textarea editor, move dialog, revisions drawer.

**Calendar**
- Month-grid calendar view per project (nav tab): issues placed by
  due date (start date fallback), click → peek drawer, month
  navigation, "unscheduled" strip. Backend: additive
  `start_after`/`start_before`/`undated` params on the existing list
  endpoint (parameterized, same validation path). Prerequisite for
  cycle-4 gantt.

**Workspace settings**
- `/w/:slug/settings`: rename (name + slug, inline validation),
  members list with role changes, remove member (last-admin 409
  surfaced honestly), delete workspace behind typed-slug
  confirmation. Backend: `PATCH /:slug` gains optional slug change,
  new admin-only `DELETE /:slug` (FK cascades).

**Time tracking**
- Worklogs: migration 000019 (`time_entries`), start/stop/log/list
  endpoints per issue (partial unique index → race-safe one running
  timer per user+issue, 409 on double-start), member-scoped. UI:
  timer widget on the issue detail (live ticking, survives reload via
  server reconciliation, manual log dialog, totals). No realtime
  broadcast by design — worklogs are personal.
- Time reports (C6T8): `GET .../projects/{identifier}/time/summary`
  (`?days=`, `?group_by=day|week|user|issue`; garbage → 400, days
  clamps to 365). Completed entries only — running timers excluded,
  same convention as the per-issue total. UI: "Time report" section
  on the Analytics page (group-by + window selectors, hand-rolled
  SVG bars, ranked horizontals for user/issue).

**API tokens UI**
- `/settings/tokens` (C6T0): create/revoke/list personal tokens,
  show-once plaintext, scope selection. Linked from the profile
  page, closing the nav gap.

**Webhooks UI**
- Workspace settings → Webhooks (C6T1, admin-only): CRUD, event
  selection, active toggle, secret show-on-create; regenerate is
  client-side generate + PATCH (PATCH never returns the secret).

**Project taxonomy settings**
- `/w/:slug/p/:identifier/settings` (C6T2): states CRUD (409 unless
  `?reassign_to=`, last state undeletable), labels, estimate scale
  delete + add-points. Backend added where the UI needed it (states
  were list-only, estimates create+list-only).

**Profile page**
- `/profile` (C6T7): name/email, `PATCH /api/v1/auth/me`, sessions
  list with `current` flag, link to API tokens.

**"My work" view**
- `/w/:slug/my-work` (C6T3): assigned / created / watched tabs via
  `GET /api/v1/workspaces/{slug}/my-issues?filter=`.

**Draft issues**
- "Save as draft" in the create dialog + Drafts tab per project
  (C6T4): resume / publish (`PATCH is_draft=false`) / discard.
  The working-set default now EXCLUDES drafts (no UI could create
  drafts before, so no flow depended on the old behavior); direct
  `?sequence_id=` lookups still resolve them.

**Command palette lookup**
- Display-ID resolution is one query now (C6T5):
  `GET .../issues?sequence_id=N` (400 on garbage) instead of
  page-scanning; project paths are encodeURIComponent'd.

**Gantt chart**
- Full-stack timeline (C4T0 backend, C4T1 frontend): migration
  000020 (`issue_links`, dependency edges, no self-loops, project-scoped);
  `.../issues/{uuid}/links` CRUD + `?include_links=1` bulk contract.
  UI: hand-rolled SVG 3-month timeline, rows by state group, drag-reschedule
  (member-only, day-snapped PATCH), dependency arrows, unscheduled lane.
  Zero new deps, no N+1.

**Analytics**
- Full-stack dashboard (C4T3 backend, C4T4 frontend): `.../analytics/summary`
  (by state/priority/label, overdue, estimates), `/cycle/{id}/burndown`
  (audit-log replay — exact counts; estimate uses current values,
  documented), `/trends?days=N`. UI: donut, burndown line chart,
  trends bars, all hand-rolled SVG.

**Public share links**
- Share issues/pages via URL-safe tokens (migration 000022): member
  creates/manages, `GET /api/v1/public/s/{token}` serves a PII-sanitized
  read-only payload (no UUIDs, emails, assignees, or comments); revoked/
  expired → 404 (never 403). Share modal with expiry + revoke; public
  page reuses the XSS-safe markdown renderer.

**Attachments**
- Upload/download/list/delete with storage abstraction, traversal-hardened
  paths; SVGs are never served inline (forced download) after review
  caught a stored-XSS vector.

**Releases**
- Full-stack milestones (C4T6 backend, C4T7 frontend): migrations 000023
  (`releases`, unique per project) + 000024 (`issues.release_id`,
  ON DELETE SET NULL); CRUD, bulk issue assign/unassign, delete guard
  (409 unless `?reassign=` or `?force=true`). UI: project nav tab,
  release list/detail, progress bar, 409-aware delete dialog.

**CSV importer**
- `POST .../imports` (multipart CSV + column mapping) with preview endpoint
  and template download. Two-phase: validate all rows (per-row errors,
  never silent), then single-transaction insert. Resolves states/priorities/
  labels/assignees by name; unknown values → per-row errors.

### Fixed

- `ws.smoke.test.ts` now cleans up its scratch rows (zero live rows
  post-run; soft-delete audit trail preserved).
- Last-admin removal/demotion race: `SELECT … FOR UPDATE` on
  membership rows serializes concurrent removals (20× race test
  green, incl. `-race`).
- Snoozed intake inbox excluded archived issues (the pending inbox
  already did); both intake orders gained an `ii.id` tiebreak (C6T5).
- `CompleteCycle` only completes `current`/`upcoming` cycles; a
  second call is a nil no-op (ticker-race safe) (C6T5).
- `UpdateWebhook` with an empty secret regenerates instead of
  storing "" (which would silently disable HMAC signing) (C6T5).
- Issue-link kinds are case-folded on create ("BLOCKS" stores as
  "blocks") (C6T5).
- API token scopes dedupe on create (C6T5).
- Webhook outbox `markFailed` now stamps `processed_at` like the
  `done` path, so dead rows are distinguishable from retrying rows
  (C6T5).
- `internal/api` test `TestMain` truncates `rate_limits` once at
  package startup — retires the manual-TRUNCATE-before-every-run
  workaround for the environmental 429 flake (C6T6).

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

**Onboarding & home (cycle 2)**
- Guided onboarding wizard (`/onboarding`): welcome → create workspace
  (inline slug validation) → invite teammates (optional); zero-workspace
  users are funneled through it, existing users see no change. Email
  invites are not sent yet — the list is kept on-device with honest copy
  until a backend invites endpoint exists.
- Home dashboard (`/`): greeting + date, "Your work" (assigned issues
  across projects), recents (last 5 opened issues, localStorage), quick
  links; every section has inline empty-state actions. Workspace list
  moved to `/w`.

**Create flow & detail (cycle 2)**
- Denser create flow: parent issue picker (creates sub-issue relation),
  "Create more" toggle (keeps the dialog open), success toast with
  display_id + Copy link / View actions.
- Issue-detail extras: Subscribe/Unsubscribe (existing backend),
  Copy-link button, unified Activity timeline (comments + history,
  relative timestamps). Lands in both the full page and the peek drawer.

**Cycles (cycle 2)**
- Cycle burndown: progress header (open/in-progress/done) + hand-rolled
  SVG burndown with ideal line, via new read-only
  `GET .../cycles/{id}/burndown` (reconstructed from the audit log;
  member-scoped).

**Filters & views (cycle 2)**
- Richer filters: state, multi priority/label/assignee (incl.
  "unassigned"), estimate, created/updated/due ranges, subscribed —
  all backend-filtered, all URL-serializable (round-trips through the
  URL), shared by list/board/spreadsheet.
- Saved views: per-project named views (filter + display preset),
  apply/rename/delete/default, persisted in localStorage.

**Production**
- `--health-check` flag: the binary probes its own `/health`
  (distroless has no shell/curl); `HEALTHCHECK` added to the Dockerfile.
- Graceful shutdown: SIGINT/SIGTERM cancels dispatchers + tickers and
  drains in-flight requests (10s timeout).
- `GET .../cycles/{id}/burndown`: read-only aggregate endpoint
  (member-scoped).

### Fixed

- Parallel `go test ./...` migration race: `Migrate()` now runs in one
  transaction guarded by `pg_advisory_xact_lock` (transaction-scoped,
  auto-released); new `TestMigrateConcurrent` regression test.
- Snooze-expiry ticker (spec §3): intake issues whose `snoozed_till`
  passes flip back to pending automatically (1-min ticker, idempotent,
  realtime fan-out so inboxes refresh).
- `archived=` filter on list/board/inbox: archived issues are now
  excluded by default, visible with the "Show archived" toggle.
- `per_page` out of range (1–100) is now rejected with 400 on all list
  endpoints (was: silently clamped); service-layer clamp stays as
  defense-in-depth.

### Security

- `users.is_active` is now enforced: deactivated users' sessions are
  rejected and neither login path mints new ones (`ErrUserDeactivated`
  → 401).
- OTP_PEPPER fail-fast in production: `APP_ENV=production` ignores the
  `ALLOW_INSECURE_OTP_PEPPER` hatch — empty pepper refuses to boot.
- Comment POST is now covered by `Idempotency-Key` (same key → one
  comment).
- Webhook dispatcher no longer follows HTTP redirects (SSRF-adjacent).
- `X-Forwarded-For` is honored only from `TRUSTED_PROXY_CIDRS`
  (default: trust none — direct TCP peer is the client IP).

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
