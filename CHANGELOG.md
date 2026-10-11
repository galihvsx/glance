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

**Roadmap, importers & notifications (cycle 9)**
- Roadmap project view (C9T0): month columns by `target_date`, Unscheduled lane, 3/6/12-month range, peek drawer; frontend-only.
- Jira Cloud importer (C9T1): `POST .../imports/jira/preview` + `.../imports/jira` (same group as the GitHub importer) — project+site-scoped dedupe, two-phase import (all HTTP before one tx), token body-only (never persisted), SSRF guard (pinned `https://<site>.atlassian.net`, https-only, no redirects, 8MB cap).
- Saved views, server-side (C9T2): migration `000033_issue_views` (per-user, per-project, unique name); `GET/POST /views`, `PATCH/DELETE /views/{id}` — member-scoped, own-views-only; frontend rewired off localStorage with honest error toast.
- Slack incoming-webhook notifications (C9T3): migration `000034_workspace_slack` (single `slack_webhook_url` column on workspaces — no settings table exists to reuse); admin-only PATCH; write-time `https://hooks.slack.com/` hostname allowlist (SSRF control); compact POSTs on issue created / state changed / commented / @mentioned (failures logged, never block); URL never serialized (`slack_configured` boolean instead); workspace-settings "Slack" section with test-message button.
- Custom-field columns in spreadsheet (C9T4): optional per-field columns (default off, per-project localStorage), read-only, typed formatting reused from issue detail.
- Stale-issue nudge (C9T5, beyond parity): daily ticker — issues untouched for 30 days in non-done states get one in-app nudge to assignees + watchers; reuses `issue_reminders` with `kind='stale'` (no migration), respects notification prefs.

**Portability & sharing (cycle 10)**
- Trello Cloud importer (C10T0): `POST .../imports/trello/preview` + `.../imports/trello` (same group as the GitHub/Jira importers) — list→state mapping (unmatched lists create states in the `unstarted` group), Trello palette labels, comments with attribution, assignees best-effort by email, checklists skip-documented, archived cards excluded; SSRF guard (host pinned `https://api.trello.com`, https-only, no redirects); credentials body-only, never persisted.
- Shared saved views (C10T1): migration `000035_issue_views_shared` (`shared` flag on `issue_views`); `GET /views` returns own + project-shared views (annotated with owner); `PATCH {shared}` owner-only (403 on another's shared view, 404 on another's private view — no existence leak); workspace admins (role 20) may delete any shared view (moderation); SavedViewsMenu "Shared" section with owner attribution; default auto-apply scoped to own views only.
- Email digests (C10T2, beyond parity): migration `000036_digest_watermarks` (per-user-per-day idempotency); daily ticker aggregates the last 24h of `issue.assigned`/`mention`/`issue.state_changed` notifications into ONE opt-in `digest.daily` email (default off); watermark claimed only when an email is sent, in the same tx as the outbox row; one Email toggle in notification prefs (no new settings page).
- Full workspace data export (C10T3, beyond parity): admin-only (role 20) `GET /api/v1/workspaces/{slug}/export` streams a single `glance-export/1` JSON archive (chunked, REPEATABLE READ snapshot, memory flat) — workspace, members, projects, states, labels, estimates, custom fields, cycles, modules, pages, issues with nested comments/custom values/assignees/labels/attachment metadata; binaries and secrets (Slack URL, stored paths) never exported; workspace-settings "Data" section with honest copy (no importer yet — future work).

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

**Restore & automate (cycle 11)**
- Workspace archive importer (C11T0): admin-only (role 20)
  `POST /api/v1/workspaces/{slug}/import` (multipart archive JSON, 100MB
  cap) restores `glance-export/1` archives with restore-by-UUID semantics
  (UUID preserved on new rows; same UUID already present → skipped, so
  re-import is idempotent); streaming `json.Decoder` (memory flat), single
  all-or-nothing tx, format pin (`!= glance-export/1` → 400, zero writes);
  members matched by email (never auto-created, unmapped skipped +
  reported); attachments skipped but counted; workspace-settings Data
  section gains an "Import archive" UI with honest copy.
- Workflow automations v1 (C11T1, beyond parity — Plane paywalls this
  class): migration `000037_automation_rules` (idempotent up/down); per-
  project rules — on issue state change, run actions (assign user, add
  label, post comment); max 25 rules per project; depth-1 loop guard
  (automation-driven changes never re-trigger automations); action failures
  logged, never block the state change; `GET/POST .../projects/{id}/
  automations`, `PATCH/DELETE .../automations/{ruleId}` (read = member(15)+,
  write = member(15)+ — documented: this codebase has no maintainer role);
  project-settings "Automations" section with rule builder and enable/
  disable toggle.
- Digest scheduling granularity (C11T2): per-user digest cadence stored in
  `notification_prefs` with no migration (value-encoded keys
  `digest.frequency:<daily|weekly>` + `digest.hour:<0-23>`, defaults daily
  after 08:00 server-local); the digest ticker now runs hourly and gates
  each opted-in user on due-ness (send-after hour in the server's local
  timezone, daily = no watermark today, weekly = last watermark ≥ 7 days
  old); `GET/PUT /api/v1/digest-schedule` (GET also returns `server_tz`);
  notification prefs gain frequency + hour selects with honest copy
  (per-user time zones not supported yet). Fixed in cycle 12 (C12T0) —
  weekly digests now aggregate the full 7-day window since the last
  successfully-sent digest.
- Trello checklist import (C11T3): closes the C10T0 skip-documented item —
  per-card checklists (name + checked/unchecked items) are appended to the
  imported issue's description as markdown (`## <name>` sections,
  `- [x]/[ ]` items); fetch failure is a per-card error, the batch
  continues; SSRF guard covers the new fetch path.

**Automations & notifications, done right (cycle 12)**
- Weekly digest 7-day window (C12T0): closes the documented C11T2 honesty
  gap — digests now aggregate the window since the last successfully-sent
  digest (`(last_watermark, now]`), capped at 7 days for weekly users; the
  email subject/body honestly states the window ("past 7 days" /
  "past 24 hours"); empty window → no email, no watermark claim; strict
  lower bound prevents double-includes. No migration.
- Automation run history (C12T1, beyond parity — Plane paywalls this
  class): migration `000038_automation_runs` (idempotent up/down, no slot
  waste); one run row per rule firing, written in the same tx as the
  firing (a run-row insert failure is logged, never blocks the state
  change); per-action ok/error results recorded; `GET .../projects/{id}/
  automations/runs` (member(15)+, `rule_id?`/`issue_id?` filters, limit
  default 20 / clamped to 100); rule delete cascades its runs; the
  Automations section gains a "Recent runs" card with per-action
  ok/failed chips and honest loading/error/empty states.
- Automation trigger/action expansion (C12T2, beyond parity): new
  `issue.created` trigger — fires on issue creation inside the same tx
  (no filters in v1; filters on a created trigger are rejected at write
  time, not silently ignored); new `set_priority` (pinned 0–4) and
  `set_state` (state in the same project, write-time validated) actions;
  triggers are event-scoped (a created rule can never fire on a state
  change and vice versa); automation-driven changes never re-trigger
  automations (depth-1 loop guard, tested — no cascade); same-value
  `set_priority`/`set_state` is a no-op (no activity row, and an all-no-op
  firing writes no run row); `set_state` mirrors the manual move's in-app
  fan-out (activity row + watcher notification) but enqueues no
  webhooks/Slack in v1 (documented scope cut); rule builder gains the new
  trigger/action selects with honest copy; openapi schemas updated.

**Honest operations (cycle 13)**
- Automation run retention pruning (C13T0): closes the documented C12T1
  follow-up — `automation_runs` no longer grows unboundedly. Migration
  `000039_automation_run_retention` adds a per-project
  `automation_run_retention_days` column (NULL = 90-day default, 0 = keep
  forever, negative rejected at write); a daily ticker deletes runs older
  than each project's window, never in the firing tx (failures logged,
  never fatal); `GET .../projects/{id}/automations` now also returns the
  effective `retention_days`. No SPA UI for retention in v1 (flagged);
  workspace export/import does not carry the new column (flagged).
- Digest per-user timezone (C13T1): closes the documented C11T2 follow-up
  — the digest hour is now evaluated in the user's own IANA timezone
  (`digest.tz` in `notification_prefs`, no migration; absent = exact
  pre-C13T1 server-local behavior); hour gate, claim window, and watermark
  day are all user-local, so no double-send or starvation across tz
  midnight (DST handled by `time.LoadLocation`); invalid TZ → 400 at
  write, malformed rows ignored on read; `GET/PUT /digest-schedule` carry
  `tz`, and the Notifications page gains a timezone input (shortlist +
  free IANA, server-400 rollback).
- Plane-native import research (C13T2, no code — research-first): studied
  Plane's real OSS export (`POST /api/workspaces/<slug>/export-issues/`,
  JSON provider is the only lossless format); delivered the format spec,
  a Plane→glance mapping (biggest gaps: descriptions are never exported,
  people are name-strings only, relations/attachments/custom properties
  lost), a faithful export fixture, and a cycle-14 importer build plan in
  the cycle dir. Note: upstream Plane is now AGPL-3.0, and its JSON
  export is a bare array with no version envelope — the importer must
  validate by shape.

**Plane-native import (cycle 14)**
- Plane JSON-export importer (C14T1–C14T6): bring a Plane project over
  from **Settings → Exports → JSON** in Plane. Accepts the raw `.json`
  or the `.zip` Plane downloads it in (exactly one `.json` per zip);
  the export is a bare JSON array with no version envelope, so rows are
  validated by shape — wrongly typed or identifier-less rows fail
  per-row (keyed by Plane `identifier`), never the whole file. One
  import run = one Plane project → one glance project; multi-project
  files are rejected. 256 MiB input cap; no migration.
- Two-phase like the other importers: **analyze** parses the file and
  reports everything unresolved — people (Plane exports name-strings
  only, no emails/IDs) map explicitly to workspace members, never
  guessed; states/labels/cycles/modules matched case-insensitively,
  unmatched ones created (in the `backlog` group, per the import design) — then **execute** imports in a single transaction.
- Honest-loss policy: the import report carries a per-issue `gaps` list
  documenting what didn't survive. Upstream truth first: **Plane never
  exports issue descriptions**, so those are gone before glance ever
  sees the file; issue-to-issue relations are dropped (glance has no
  relations model); attachment *files* never come over (counts only);
  unset estimates stay unset; comment timestamps are naive in the
  export and are read as UTC. Dangling parents (parent identifier not
  in the file) import as root issues with a warning in their gaps.
- User guide at `docs/importing-from-plane.md`, including the
  triage/draft/archived caveat: Plane exports the whole project, so
  clean up triage, drafts, and archived issues in Plane before
  exporting — otherwise they come across as-is and you'll map
  throwaway states in the analyze step.
- HTTP surface (C14T4): `POST
  /api/v1/workspaces/{slug}/projects/{identifier}/imports/plane-import/analyze`
  (multipart `file`, member+) and `.../execute` (multipart `file` +
  JSON `resolutions` + optional `options`, member(15)+), both documented
  in `docs/openapi.yaml` (new `PlaneImportAnalysis`/`PlaneImportReport`/
  `PlaneImportRowError`/`PlaneImportGap` schemas).
- SPA UI (C14T5): the Import tab in project settings gains a Plane panel —
  upload → analyze → inline mapping UI (people→members, states→existing
  or create-in-backlog, labels/cycles/modules→existing or create, plus a
  `strict_states` option) → execute with confirm → report card with
  per-issue errors and gaps. The honest-loss list is shown up front,
  before upload.

**Automation 2.0 — rules that see everything (cycle 16)**
- Event triggers (C16T0): rules now fire on the mutations that used to
  bypass them — `issue.assigned` / `issue.unassigned`,
  `issue.labels_changed` (optional `label_ids` filter), `issue.priority_changed`
  (optional `from/to_priorities` filters, 0-4), `issue.due_date_changed`,
  `issue.estimate_changed`, and `issue.comment_added`. Events are emitted
  in the service write paths; rules never fire for rolled-back writes,
  and filters that don't belong to a trigger type are rejected rather
  than silently ignored. No migration (trigger JSONB).
- Scheduled triggers (C16T0→C16T1): time-based rules evaluated by a new
  ticker — `issue.due_soon` (optional `window_hours`, default 48),
  `issue.overdue`, `issue.stale` (optional `stale_days`, default 7), and
  `cycle.ending_soon` (72h, fixed). Cadence from
  `GLANCE_AUTOMATION_SCHEDULE_INTERVAL` (empty = disabled, the default);
  invalid or sub-minute values fail boot loudly. Dedupe via
  `automation_runs`: one scheduled fire per (rule, issue) per calendar
  day. Scheduled firings run each (rule, issue) in its own tx; the rule's
  author is the actor for honest attribution. No migration (reuses
  `automation_rules` / `automation_runs`).
- New actions (C16T2): `remove_label`, `unassign` (clears every assignee),
  `set_estimate` (estimate-point UUID, validated against the project's
  scales — honest 400 on mismatch), `set_due_date` (absolute
  `YYYY-MM-DD` or relative `+Nd` days from the firing time),
  `move_to_cycle` / `move_to_module` (in-project validated, issue leaves
  its other cycles/modules), `add_watcher` (workspace member → issue
  subscriber). All idempotent (already-there = no-op, no run row). A
  failed action is recorded `ok:false` on the run row and now **aborts
  the rule's remaining actions** (a half-applied rule is worse than a
  stopped one); the firing event still commits.
- Blocker guard on completion (C16T3): moving an issue into a
  completed-group state while it has open `blocks` blockers (blocking
  issues that are neither completed nor archived) is rejected with 409
  code `open_blockers` — `details.blockers` names the blocker display
  IDs; `ignore_blockers=true` bypasses. Enforced in the shared
  state-transition path, so it covers single PATCH, bulk-update
  (per-item failure), atomic bulk-set (aborts the batch), and the
  automation `set_state` action (failure recorded `ok:false` on the run
  row, never a silent skip). Issue detail + board drag show a confirm
  dialog with "Complete anyway". No migration (reads `issue_links`).
- Automation UI 2.0 (C16T4, frontend-only): the Automations builder now
  exposes all 13 trigger types (grouped "on an event" / "on a schedule")
  with their filters — label multi-select, priority from/to, due-soon
  window hours, stale days — and all 12 actions with their inputs
  (cycle/module selects, estimate points grouped by scale, due-date
  absolute/relative toggle). Incomplete inputs surface honest inline
  errors instead of silent drops; rule descriptions render the new
  shapes. Deliberate omission: no user filter on assigned/unassigned —
  the backend rejects all filters there, and offering one would 400.

**Finish the gaps (cycle 15)**
- Gantt dependency editing (C15T0, frontend-only): create/delete issue
  links (`blocks`/`blocked-by`) directly on the Gantt timeline —
  link-mode toggle or drag-from-bar-handle to draw a dependency edge,
  click an edge to delete (confirm); optimistic updates with toast
  rollback; keyboard-accessible link editor in the issue detail panel.
  Reuses the cycle-8 issue-link API; no backend changes.
- Admin audit log (C15T1): append-only `audit_log` table (migration
  `000038_admin_audit_log`: at, actor, action, entity, workspace, ip,
  meta jsonb) fed by a service hook on all admin mutations (user
  deactivate/reactivate/role change, workspace delete, …); no
  update/delete API. `GET /api/v1/admin/audit-log` (admin-only,
  paginated, filter by action/actor/entity); "Audit log" tab in
  `Admin.tsx` with filters and relative timestamps. Refused mutations
  (self-demote/deactivate, unknown user, confirm mismatch) return
  before any audit write.
- Scheduled backups (C15T2): `GLANCE_BACKUP_INTERVAL` (empty = disabled,
  default), `GLANCE_BACKUP_DIR`, `GLANCE_BACKUP_RETENTION` config; a
  ticker backs up every workspace on schedule into gzipped
  glance-export/1 archives (same streaming implementation as the
  interactive export — no format drift), each with a manifest sidecar
  (slug, at, byte size, sha256, migration version) and a `backup_runs`
  row (migration `000041_backup_runs`); verify = sha256 recompute +
  streaming header decode (format pin + slug match); failed runs are
  recorded, never silent; retention keeps the newest N per workspace
  (rows stay as `pruned`, files deleted). `GET/POST
  /api/v1/admin/backups` (admin group, 207 on partial failure); "Backups"
  tab in `Admin.tsx` with schedule/retention display, run-now, and a
  filterable history table. Restore path = gunzip + the existing
  archive importer.
- iCal subscription feeds (C15T3): `GET
  /api/v1/workspaces/{slug}/projects/{identifier}/calendar.ics` and
  `.../cycles/{cycleID}/calendar.ics` — `text/calendar` feeds of dated
  issues (RFC 5545 escaping, CRLF folding, UID/DTSTAMP/SUMMARY/DUE/
  DESCRIPTION with issue URL), authenticated by opaque per-user
  `glcal_…` feed tokens (migration `000042_calendar_feed_tokens`;
  crypto/rand 32B, SHA-256 hash stored only, single live token per user,
  per-token rate limit, revoked/missing → generic 401 with no
  enumeration). Token management (`GET/POST/DELETE
  /api/v1/me/calendar-token`) sits behind session auth — a feed token
  can never mint or revoke tokens. "Copy iCal URL" in the Calendar view
  + regenerate in Profile settings.
- PWA installability (C15T4, frontend-only): `manifest.webmanifest`
  (name/short_name `glance`, standalone, dark theme color, 192/512 PNG
  icons incl. maskable, generated from the existing logo artwork) +
  minimal service worker (cache-first app shell with precache +
  runtime cache for hashed `/assets/*`, network-first for `/api/*`,
  offline navigation fallback; no sync, no push), registered in
  `main.tsx` and guarded out of dev mode; subtle install affordance in
  the shell footer (beforeinstallprompt hook, dismiss persists in
  localStorage); manifest link, theme-color meta, and apple-touch-icon
  in `index.html`.

### Fixed

- `TestExecutePlaneImportRelationsDropped` asserted a global
  `issue_relations` row count, which flakes whenever another test (the
  api package's issue-link tests share the test DB) leaves its own rows.
  The assertion is now scoped to the test project via
  `JOIN issues … WHERE i.project_id = $1`.

- Slack webhook clear path (C9T3 follow-up): PATCH
  `{"slack_webhook_url":null}` never cleared the column — encoding/json
  unmarshals JSON null into a nil `*json.RawMessage`, making explicit
  null indistinguishable from an absent key. The body now uses
  `service.PatchField[string]` (tri-state: omitted = untouched,
  null = clear, string = set); pinned by `TestSlackWebhookURLHTTP`.

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
No longer deferred — these shipped after the original v1.0 list was
written: `pages`, `issue_views`, `attachments`, `favorites`, module UI,
gantt, calendar, roadmaps, analytics, spreadsheet, public boards,
admin panel.

True remaining gaps:
- **SSO/SAML** — deliberately deferred; email OTP + Google/GitHub OAuth
  covers auth. Enterprise SSO is a post-parity consideration, not a v1
  gap.
- **Data portability beyond CSV/JSON** — issue export exists as
  CSV/JSON; admin-only workspace-wide JSON archive export ships
  (C10T3); the archive importer (including Plane-compatible imports)
  remains future work.
- **Beyond-parity stance** — the loop's target is not just zero gap with
  Plane but a leaner, better, fully free (Apache-2.0) tracker. Any new
  capability is judged against Plane parity first, then on its own
  merit.
