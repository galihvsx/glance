# Changelog

All notable changes to glance are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

**Global search covers Pages (cycle 66)**
- `GET /api/v1/search` was issues-only; page titles and content were
  invisible to global search. Pages now get a `search` tsvector (migration
  000066, title weighted above content) and the search envelope carries
  `page_results` + `page_next_cursor` alongside the unchanged issue
  `results`/`next_cursor` — same member-only authz, archived-project skip,
  and keyset pagination discipline as issues. The Cmd+K palette renders a
  Pages section (title + project badge) that deep-links into the pages
  tree with the page selected.

**Version-history membership completeness (cycle 65)**
- Label and assignee changes now leave a trace in the issue's version
  history. Previously, attaching/detaching a label or assigning/unassigning
  a member wrote activity rows, fired automations, and enqueued webhooks —
  but wrote **zero version rows** unless an automation cascade happened to
  move the issue. Each real membership change (single attach/detach/assign/
  unassign, and bulk label/assignee sets) now records exactly one version
  carrying the post-mutation row; the old/new sets live in the existing
  `issue_activities` rows, and restore semantics stay row-fields-only.
- The cycle-64 no-op discipline is preserved: idempotent no-ops
  (re-attaching an attached label, detaching an absent one, bulk sets with
  identical membership) snapshot nothing, and a membership change whose
  automation cascade moved the row is never snapshotted twice.

**Version-restore integrity (cycle 64)**
- Restoring a version whose snapshot is identical to the live row is now
  rejected with **409** (`issue already matches that version`) instead of
  minting a duplicate-content version row plus a fake `_restored` marker —
  reachable before via the API (restore of the latest version, or an
  A→B→A stale-but-identical restore). Version history no longer records
  events that never happened.
- Same-class audit of every version-snapshot writer: trash restore and
  merging an already-cancelled source no longer snapshot identical content
  (the delete-time snapshot is the content anchor; the `_restored` activity
  row, merge relation/activities, and webhooks are untouched). MoveIssue,
  BulkSet, `updateIssueTx`, and the version-restore path were verified
  honest and documented in code.

**Inbound GitHub webhook broadcast parity (cycle 63)**
- The inbound PR webhook now fans out realtime `issue.updated` per affected
  issue — the same event the manual link/unlink paths already send (C62T1).
  Opening a PR that mentions an issue, or closing/merging a PR, updates open
  issue-detail pages live instead of sitting stale until refresh.
- Contract match, not a new contract: best-effort announce (post-mutation;
  a nil broadcaster or lookup failure never fails the delivery), no webhook
  enqueue, no state transitions — a PR close/merge still never moves issues
  between states (by design), and `changed_fields` vocabulary is unchanged.
  One event per issue per delivery even when the close path's state flip and
  mention rescan touch the same issue.

**GitHub integration UI (cycle 62)**
- Settings → GitHub (admin-only): webhook-secret set/clear (the secret is
  never echoed back — responses carry only `github_webhook_configured`),
  the inbound webhook URL
  `POST /api/v1/integrations/github/webhook` with copy button, and
  repo-webhook setup instructions. Mirrors the Slack section's
  write-only-secret contract.
- Issue detail: linked pull requests / commits section — repo,
  `#number` / short-SHA, state badge (Open/Merged/Closed), link in new
  tab; link dialog (repo, type, PR number / commit SHA, URL) honoring
  the backend's 409 (already linked) and 400 envelopes; remove with
  confirm. Member (15)+ only (guests read-only); the server enforces
  all of it.
- Realtime: ref create/delete now fan out `issue.updated` (best-effort,
  same posture as other mutations) so collaborators and other tabs
  refetch refs live.
- Known gap (future cycle, product decision): the inbound webhook's
  auto-link path still announces nothing — and a PR close/merge never
  moves issues between states (by design).

**DeleteState webhook fan-out (cycle 61)**
- Deleting a state with reassign now enqueues one in-tx `issue.updated`
  webhook per moved LIVE issue (`changed_fields: ["state_id"]`, same
  payload shape as restoreIssueVersion) — webhook consumers no longer
  silently miss the mass `state_id` change. Soft-deleted issues moved by
  the same delete enqueue nothing (same invariant as the per-issue
  activity rows and version snapshots); an enqueue error rolls back the
  whole delete like every other mutation.

**Webhook delivery payload viewer (cycle 60)**
- Delivery-detail endpoint: `GET
  /api/v1/workspaces/:slug/webhooks/:id/deliveries/:deliveryId` →
  200 `{delivery: {...}}`. The detail shape is the list shape PLUS
  `data` — the stored domain-event payload the dispatcher signed and
  POSTed (domain event only — never the webhook URL or secret, which
  live on the webhook row). It is the ONLY response shape that
  carries `data` and it never carries `outbox_row_id`; the list
  response is unchanged. Admin-only, workspace-scoped; cross-webhook
  delivery ids read as 404 (never leaks cross-webhook existence);
  non-numeric id → 400.
- Settings → webhooks → delivery history: per-row "View payload"
  button on every delivery (all statuses) opening a dialog with the
  payload as pretty-printed `{event, data}` JSON, a copy button
  (same pattern as the secret-reveal flow), and a status/event/
  attempted-at header. Payload fetched lazily on open with
  Skeleton/error states. Additive only — no redesign of the
  webhooks section.
- OpenAPI: `WebhookDeliveryDetail` schema + the detail endpoint,
  pinned in `openapi_test.go` (reflection pin over the embedded
  struct, `outbox_row_id` forbidden).

**Webhook delivery history + manual redelivery (cycle 59)**
- New `webhook_deliveries` table (migration 000065): one row per
  delivery attempt — `webhook_id`, `outbox_row_id` (correlation,
  deliberately NOT a foreign key: outbox rows are pruned after 30d),
  `event`, `data` (domain event data only — never the URL or the
  signing secret, which are re-read from the webhook row at redelivery
  time), `status` (`delivered` | `retrying` | `failed`, the C57T2
  vocabulary), `http_status` (NULL when no HTTP response was
  received), `error`, `duration_ms`, `attempted_at`. Index on
  `(webhook_id, attempted_at DESC, id DESC)`. The dispatcher writes
  the history row alongside each outbox outcome (outbox write first,
  then status, then history); skip paths (deleted/inactive webhook)
  and unattributable rows (poison payload, malformed webhook id)
  write no history. The C57T2 `last_delivery_*` summary on the
  webhook row is unchanged — history is the detail behind it.
- Manual redelivery: `POST
  /api/v1/workspaces/:slug/webhooks/:id/deliveries/:deliveryId/redeliver`
  → 202 `{ok: true}`. Only `failed` entries are redeliverable
  (retrying retries automatically; delivered needs no redelivery —
  enforced in the service with 409, not just in the UI). Redelivery
  enqueues a FRESH outbox row (attempts 0, pending, due now) built
  from the webhook's CURRENT row (current URL + secret — if the
  admin changed the URL since the failure, the redelivery goes to
  the current URL); the original history row stays untouched and
  the redelivery's own attempts create new history rows. 404 when
  the webhook/delivery is unknown or the delivery belongs to
  another webhook (never leaks cross-webhook existence); 409 when
  the webhook is inactive.
- Delivery history API: `GET
  /api/v1/workspaces/:slug/webhooks/:id/deliveries?limit=&before=`
  → `{deliveries, next_before}` — newest first, `limit` default 25
  / max 100 (invalid → 400), `before` is a delivery-id cursor.
  Rows carry no `data` and no `outbox_row_id`. Admin-only,
  workspace-scoped, like all webhook endpoints.
- Settings UI: per-webhook expandable Delivery history panel
  (status badge, event, attempted time, HTTP status / "No
  response", truncated error with full-error tooltip, Load more via
  the `before` cursor, Redeliver button only on failed rows with
  in-flight disable + transient confirmation + list refresh, and a
  "redelivery re-sends to this webhook's current URL" note).
- Retention: `PruneWebhookDeliveries` deletes history rows older
  than 90 days (longer than the outbox's 30, so redelivery outlives
  the queue), batched, in the same daily ticker pass as
  `PruneOutbox`; the prune deletes by `attempted_at` cutoff only,
  never by status. OpenAPI `WebhookDelivery` schema + both
  endpoints, with test pins. Pinned by 17 service tests, handler +
  OpenAPI-pin tests, and 12 new frontend tests.

**Webhook dispatch fairness: per-webhook claim cap (cycle 58)**
- The global dispatcher claim (`claimDue`, unscoped production path)
  now takes at most `webhookPerWebhookCap = 5` rows per webhook per
  pass (`ROW_NUMBER() OVER (PARTITION BY payload->>'webhook_id'
  ORDER BY id)`), filling the 25-row batch across webhooks. Before,
  one webhook's due backlog filled the whole batch and every other
  webhook's rows starved silently behind it (head-of-line blocking).
  Under normal load (≤5 due rows per webhook) the claimed rows and
  order are identical to before; a flooding webhook still drains
  every pass, just at most 5 rows per tick. The scoped (test-only)
  claim stays uncapped. The ranking lives in an `IN`-subquery
  because Postgres rejects `FOR UPDATE` on a query whose `FROM`
  contains a window function; the outer query keeps its exact
  original shape (`ORDER BY id`, `LIMIT`, `FOR UPDATE SKIP LOCKED`
  on outbox rows). No migration, no API/frontend/docs changes.
  Pinned by 6 DB-backed tests (flood starvation, normal load, cap
  boundary, scoped uncapped, oldest-first ordering, full drain).

**Webhook delivery reliability + operability (cycle 57)**
- Dispatcher test-isolation (C57T0): scoped claim path — `run(ctx,
  webhookID)` adds `AND payload->>'webhook_id' = $3` to the claim
  query only when non-empty; production `Run` keeps the exact
  current behavior (unscoped, global claim) and the production
  dispatcher still calls plain `Run`. The regression test pins
  isolation: a scoped run delivers only its own rows while 30
  older due decoy rows for other webhook ids stay pending.
  SSRF + notify dispatcher tests converted to the scoped run.
- Outbox retention (C57T1): `service.PruneOutbox` + daily
  `ticker.OutboxRetentionTicker` prune `done`/`failed` outbox rows
  older than 30 days, batched so one pass never holds a long lock.
  The WHERE clause structurally excludes `pending`/`delivering`
  rows and `processed_at IS NULL` rows. Migration 000063 adds
  the partial index `ON outbox (processed_at) WHERE status IN
  ('done','failed')` (EXPLAIN-verified); down migration drops it;
  up-twice idempotency tested.
- Webhook delivery status (C57T2): migration 000064 adds nullable
  `last_delivery_at`, `last_delivery_status` (CHECK in
  `delivered` / `retrying` / `failed`), and `last_error` to
  `webhooks` (NULL = never attempted). Each dispatcher outcome
  write updates the webhook row in the same step as the outbox
  write: `markDone` → `delivered` + `last_error` NULL,
  `recordFailure` → `retrying` + send error, `markFailed` →
  `failed` + send error. A missing webhook row (deleted between
  lookup and write) or a malformed webhook id is a silent no-op;
  poison / deleted / deactivated / skip paths write no status.
  The status update never touches `updated_at`. API list/get/
  create responses include the three fields present-but-null
  (same null convention as `AdminUser.name`); the settings
  `WebhooksSection` shows a compact indicator per webhook —
  delivered (green, "Delivered <relative time>"), retrying
  (amber), failed (red), never attempted (muted) — with the
  error truncated to 80 chars in the label and the full error on
  the tooltip. OpenAPI `Webhook` schema gains the three fields
  (plus the previously undocumented `workspace_id` /
  `updated_at`), pinned by doc-shape + live-response tests.
  Pinned by 8 service tests (success, retrying, SSRF-failed,
  exhausted retries, invalid-UUID id, deleted webhook,
  deactivated skip, never-attempted), a migration test, an HTTP
  test, and frontend tests.

**Webhook fan-out completeness: every issue mutation enqueues issue.updated (cycle 56)**
- Invariant: exactly one `issue.updated` webhook per user-visible
  mutation per issue, with `changed_fields` = the union of the
  direct change and any automation-cascade change. No-op mutations
  (idempotent re-attach, all-noop firing, no-change patch) enqueue
  nothing. Slack for automation moves stays out (documented scope
  cut — a product decision, not taken here).
- AdminStats doc (C56T0): the `AdminStats` schema now includes
  `attachment_bytes` (`COALESCE(SUM(size_bytes), 0)` over
  attachments), pinned by `TestOpenAPIDocShapesAdminStats`
  (reflection against `service.AdminStats`).
- Label/assignee attach/detach (C56T1): `AssignLabel`,
  `UnassignLabel`, `AssignAssignee`, `UnassignAssignee` enqueue
  one in-tx `issue.updated` webhook when the mutation really
  changed something (same `RowsAffected() > 0` gate as the WS
  announce). `changed_fields` = the direct entry (`labels` /
  `assignees`) plus the cascade row diff via the shared
  `issueRowChangedFields` helper (all 11 content columns of the
  `Issue` struct). Pinned by 8 tests.
- BulkSet (C56T2): labels-only / assignees-only BulkSet now
  enqueues one webhook per changed issue. When the patch already
  enqueued a row, BulkSet EXTENDS that row's `changed_fields`
  in-tx to the union instead of adding a second row — the
  two-row exception was not needed. The extend match excludes
  rows carrying `labels`/`assignees`, so a concurrent
  `AssignLabel` on the same issue (label paths take no row
  lock) can never have its webhook payload rewritten.
  Pinned by 11 tests including a decoy-row regression test.
- Scheduled firings + PATCH path (C56T3): a scheduled-rule
  firing that changes the row enqueues one in-tx webhook with
  `changed_fields` = the before/after row diff (row-change
  gate, so a `set_priority`-only firing enqueues; WS announce
  stays state-only). `updateIssueTx`'s single enqueue now
  carries the union of the user-patch activities fields and
  the cascade diff, so a `priority_changed → set_state`
  cascade surfaces both `priority` and `state_id`.
  Pinned by 4 tests.

**OpenAPI contract fidelity II: the six leftovers from cycle 54 (cycle 55)**
- Audit: the six drift items cycle 54 deliberately left open are
  now corrected doc-follows-code plus doc-side regression pins.
  Docs-only cycle: no handler, frontend, or migration changes.
- Share-list + subscribers (C55T0): the two share-list endpoints
  document `{links: ShareLink[]}` (handlers always returned
  `{links}`; the doc said `{shares}`). New `Subscriber` schema
  matches `service.Subscriber` field-for-field (`user_id`,
  `name` omitted when unset — `*string` + `omitempty`, never
  null — `email`); the subscribers endpoint now refs it
  instead of `User`. The issue-link list doc already said
  `{links}` (no drift) and is now pinned too. Pinned by
  `TestOpenAPIDocShapesShareSubscriber` (reflection against
  the Go struct).
- Admin-users + author refs (C55T1): GET `/api/v1/admin/users`
  documents the live paged envelope `{items: AdminUser[],
  total, page, per_page}` with the real `page`/`per_page`
  contract (defaults 1/25, out-of-range → 400, never
  clamped). New `AdminUser` schema matches the struct
  field-for-field — all seven keys always present, `name`
  null when unset (no `omitempty`). PATCH
  `/api/v1/admin/users/{id}` takes exactly `{is_admin}`
  (missing → 400), returns `Ok`, documents both 409 cases
  (self-demotion, last-admin demotion), and its summary no
  longer claims role editing. Deactivate/reactivate gained
  their real error responses (deactivate self → 409).
  `ProjectUpdate.author` / `PageComment.author` now ref
  `ActorRef`, keeping `nullable: true` (author FK is
  ON DELETE SET NULL, so a deleted author emits null).
  Pinned by `TestOpenAPIDocShapesAdminAuthor`.
- Issue-list params + `$ref` hoist (C55T2): the list GET
  documents the params the handler actually reads —
  `page` removed (never read), `search` → `q` (full-text
  over name + description), `sort` → `order_by` with the
  real 10-key whitelist (default `-updated_at`, invalid →
  400), `per_page` max 200 → 100 + 400-on-out-of-range,
  plus `label`, `assignee`, `estimate`, `cursor`, `fields`,
  `draft`, `sequence_id`, `cycle`, `undated`, `subscribed`,
  `archived`, `include_links`,
  `include_project_structure`, and the eight date-range
  params with their real operators (`updated_after` is
  strictly `>`, due/start compare date parts). `state` is a
  UUID (no group handling on this path). Trash-mode
  semantics kept (other filters/order ignored; trash
  cursors are trash-order cursors). `ArchiveImportReport`
  is hoisted to a top-level schema (it was nested inside
  `ImportResult.properties` while an endpoint `$ref`'d it
  as top-level — a dangling `$ref`). New
  `TestOpenAPIAllLocalRefsResolve` resolves EVERY local
  `$ref` in the document, plus
  `TestOpenAPIRefResolverCatchesDanglingRef` proving the
  resolver can fail. Pinned by
  `TestOpenAPIDocShapesIssueListParams`.

**OpenAPI contract fidelity: Issue schema drift + restore-mechanism verification (cycle 54)**
- Audit: the shared `Issue` schema in `docs/openapi.yaml` drifted
  from the Go `Issue` struct (missing `project_id` / `created_by` /
  `deleted_at` / `archived_at`; a response `archived` boolean the
  struct never emits; `state_id` marked nullable against a
  NOT NULL column), and the neighbouring schemas carried the same
  class of drift. The spec is now corrected doc-follows-code plus
  live-envelope regression pins. Docs-only cycle: no handler,
  frontend, or migration changes.
- Issue-family schemas (C54T0): `Issue` gains `project_id`,
  `created_by`, `archived_at`, `deleted_at` (trash-list only);
  the response `archived` boolean is deleted (it is request-only
  via `IssuePatch`); `state_id` is non-nullable; `assignees` /
  `labels` now use the real `IssueAssignee` `{id, name}` /
  `IssueLabel` `{id, name, color}` shapes with documented
  presence rules (present on list/detail/trash, absent on
  single-issue write responses and version snapshots);
  `IssuePatch` gains the missing tri-state `release_id`;
  `IssueDetail` children are the real `IssueChild`
  `{uuid, identifier, title, state}` summaries, `custom_values`
  the real `CustomValue` shape (only set values appear), and the
  previously undocumented `project_structure` attachment
  (`IssueProjectStructure` + `NamedRef`) is documented;
  `external_refs` is present only when at least one ref is
  linked (omitempty drops the empty array). Pinned by
  `TestOpenAPIResponseShapesIssueFamily`.
- Restore/undo claims (C54T1): every snapshot/undo/undoable claim
  in the spec verified against its handler. The page-restore
  "snapshotted first" sentence is CORRECT (`insertRevision`
  runs before the `UPDATE` in the same tx) and left untouched —
  the cycle-53 analogy would have corrupted correct docs. The
  one correction: issue-version restore gains a narrow
  qualification — a sort-order rebalance writes no version
  (deliberate maintenance-op design), so after one the latest
  version's `sort_order` predates the live row and restoring it
  reverts `sort_order` too. Pinned by
  `TestIssueVersionRestoreUndoableViaLatestVersion` and
  `TestIssueVersionLatestVersionStaleAfterRebalance`.
- Schema sweep + list envelope (C54T2): `User` loses the phantom
  `role` and gains `avatar_url`, `is_active`, `is_admin`,
  `last_login_at`, `updated_at` (nullable `name`); `Label`
  gains `workspace_id` / timestamps, non-null `color`
  (default `#6b7280`); `State` gains `project_id` /
  timestamps, `group` enum now includes `triage`; `Comment`
  `author: User` → `actor: ActorRef`, gains `issue_id` and
  list-only `replies[]`; `Attachment` gains `issue_id`,
  `uploaded_by: ActorRef`; `SubtaskProgress` presence
  corrected (list/detail/trash/export, not write responses);
  `Page` / `PageRevision` gain `project_id` / `page_id` /
  `author_id` (no `created_by`), non-null `content`, integer
  `position`; `ShareLink` gains `id`, `scope`, `revoked_at`;
  `IssueLink` gains `issue_id`, `created_at`, `direction`,
  `kind` enum; the bogus `Reaction` / `Vote` schemas are
  deleted — reactions return `ReactionGroup`, votes return
  `IssueVotes` `{count, voted}`. The issue-list endpoint
  finally documents the live `{results, next_cursor?, links?}`
  envelope instead of `{issues, total}`. Pinned by
  `TestOpenAPIResponseShapesSweep`.
- Known drift left for a future cycle (recorded in the cycle-54
  ledger): share list endpoints document `{shares}` but return
  `{links}`; subscribers endpoint documents `{subscribers:
  User[]}` but returns `Subscriber[]`; admin-users endpoints
  diverge on shape; `ProjectUpdate.author` /
  `PageComment.author` document `User`, Go uses `*ActorRef`;
  issue-list query params (`page` unread, `cursor` /
  `include_links` / `order_by` / `fields` undocumented).

**Issue version history UI: view, diff, restore (cycle 53)**
- Audit: cycles 49–52 built the version engine + API, but the
  frontend had zero version surface — no lib client, no component,
  no tab; ActivitySection's History tab shows activity rows only.
  This cycle makes the snapshots visible.
- Versions client (C53T0): `web/src/lib/versions.ts` — typed client
  for the fixed contract (`GET .../versions` → `{versions}`,
  oldest first; `GET .../versions/{n}` → bare `IssueVersion`; `POST
  .../versions/{n}/restore` → the post-restore live `Issue`) plus
  `diffSnapshots(prev, curr)` over the 11 scalar fields, skipping
  identity fields, `updated_at`, `subtask_progress`, and
  `deleted_at` (C50 exclusions). `description` compares by canonical
  JSON (key-reorder never false-positives) and reports
  changed/not-changed only — no word diff, no preview. `prev =
  null` (v1) yields the "Created" marker. Null/undefined count as
  the same "absent". `created_by` reuses `ActorRef` (backend name is
  nullable).
- Versions tab (C53T1): a "Versions" tab in issue detail
  (`ActivitySection`, prototype-as-spec in the cycle dir) rendering
  `VersionHistory` — newest-first list, `vN` + author + relative
  time + changed-field chips (v1 "Created", zero-change versions
  "No changes"), expandable field diff table (human labels;
  `state_id` → state name via already-loaded states; `priority` →
  label; `parent_id` → display id if already loaded, else
  truncated UUID, never a per-row fetch; `description` → "changed"
  with no preview; dates plain; booleans Yes/No) plus the
  one-line note that labels, assignees, and custom values are not
  part of version snapshots. Restore per row with an `AlertDialog`
  confirm ("applies the old snapshot as a new version; history is
  never rewritten"), visible only to writers (role >= 15, hidden
  until the role resolves) and never on the latest version —
  restoring the latest is not an error but writes a
  duplicate-content version + `_restored` marker (verified in
  `RestoreIssueVersion`'s no-diff branch), so the UI does not offer
  it. On success: prompt invalidation of versions + issue + issues
  + board-issues + history (no optimistic writes). 403 →
  permission message; 404 → message + list refetch.
- OpenAPI correctness (C53T2): bulk-delete doc fixed from
  `{deleted: integer}` to the shipped `{results: [{id, ok,
  error?}]}` envelope plus per-item semantics and 400/403/404
  responses. Versions entries corrected ("description versions"
  → "issue versions", full-snapshot wording) with the missing
  error responses (list 404, get 400, restore 400/403) and the
  `IssueVersion` schema rewritten field-for-field against the Go
  struct (`version_no, snapshot: Issue, created_by: {id, name
  nullable, email}, created_at`). A coordinator-caught fix-loop
  corrected the restore description's undoability mechanism: the
  pre-restore state is already the latest version (no extra
  pre-restore snapshot is written). New
  `TestOpenAPIResponseShapesFourEndpoints` pins the live envelopes
  for these four endpoints — the regression net the route-coverage
  test does not provide. Recorded for a future cycle (not fixed
  here): the shared `Issue` schema itself drifts from the Go struct
  (missing `project_id`/`created_by`/`deleted_at`, extra
  `archived` boolean, `state_id` nullability) — needs a dedicated
  cycle since it touches nearly all issue endpoints.
- No backend changes to the versions engine, no migration.

**Trash: restore deleted issues (cycle 52)**
- Audit: delete was a one-way door — soft-deleted issues had no
  list/restore path (`RestoreIssueVersion` requires `deleted_at IS
  NULL`; no `deleted_at IS NOT NULL` reads anywhere in
  `internal/service/`). An accidental or bulk delete was
  unrecoverable through the app. This cycle makes Trash
  restore-only: there is still no hard/permanent delete anywhere.
- Trash API core (C52T0): `GET .../issues?deleted=1` returns
  deleted-only rows (`deleted_at DESC`, same pagination envelope;
  deliberately deleted-only, unlike `?archived=1` which only
  *includes* archived rows) and `POST .../issues/:uuid/restore`
  un-deletes one issue. The restore trail mirrors delete:
  `_restored` activity row + version snapshot + webhook
  `issue.updated` (changed_fields `["deleted_at"]`, plus
  `"parent_id"` when the parent was cleared) + WS `issue.updated`
  (post-commit re-read) + objective fan-out (restored issues
  re-enter KR progress). State re-validated (deleted state rejects
  the restore); a deleted/missing parent is cleared in-tx with a
  `parent_id` activity row, a live parent is kept. A live issue
  restores as 404 ("not in trash"), guests get 403. No migration.
  Note: `_restored` is shared with `RestoreIssueVersion` — payloads
  distinguish (`{"restored_at": ...}` trash vs `{"version_no": n}`
  version).
- Bulk restore (C52T1): `POST .../issues/bulk-restore` mirrors
  `BulkDeleteIssues` (one tx, per-item savepoints, per-item
  failures, 100-item cap, Idempotency-Key) — each restored item
  gets the full C52T0 trail. Edges verified in code + tests:
  archived-project restore follows the same rule as every other
  mutation (no block); delete→restore→delete→restore round-trips
  with no clobbered rows; concurrent restore/delete serializes on
  the row lock; a deleted project restores as 404 (hard delete +
  FK cascades); children detached by the delete stay detached; a
  `[child, parent]` batch clears the child's link deterministically.
  Bulk WS announce uses the in-tx rows (BulkUpdateIssues
  precedent), not the single-restore post-commit re-read.
- Trash page (C52T2): `/w/:slug/p/:identifier/trash` (Trash tab
  after Archived) mirroring `Archived.tsx` — plain-text titles
  (a still-deleted row's detail route 404s), `Deleted` badge +
  `deleted_at` relative time, per-row Restore with no confirm
  dialog, bulk checkboxes + Select-all + "Restore selected"
  (per-item failures stay selected for retry), client-side search
  filter, empty state. "Deleted by" is not shown (no `deleted_by`
  field — same documented limitation as Archived's "Archived by").
  Restore invalidates trash + issues/board/drafts/calendar query
  caches.

**Issue-history trails for the remaining direct-row mutations (cycle 51)**
- Audit: every `UPDATE issues` / `INSERT INTO issues` site in
  `internal/service/` was checked against the triple trail (version
  snapshot + activity rows + `issue.updated` WS + webhook). The three
  remaining gaps are closed below. Deliberately untouched (documented
  no-trail decisions): project archive/unarchive, RebalanceSortOrder,
  imports (versions = post-import edit history). No migration, no
  frontend changes.
- DeleteState reassign versions (C51T0): deleting a state with
  reassignTo now appends one version snapshot per moved LIVE issue
  (new StateID, authored by the deleter, same tx). Activity rows
  (C47T0) and announce (C45T2) already existed; soft-deleted issues
  get no version, matching their no-activity/no-announce rule.
- DeleteIssue child detach trail (C51T1): deleting a parent now
  leaves a trace on each detached live child — a `parent_id`
  activity row (old = deleted parent, new = NULL, same shape as
  Convert's row) + a version snapshot of the post-detach row +
  webhook `issue.updated` (changed_fields `["parent_id"]`,
  SetParent/Convert parity) + WS `issue.updated` after commit —
  across all four `deleteIssueTx` call sites (DeleteIssue,
  BulkDeleteIssues, RejectIntakeIssue, DuplicateIntakeIssue). No
  objective fan-out (parent_id does not affect KR progress).
- Release bulk ops trail (C51T2): Assign/UnassignReleaseIssues and
  the DeleteRelease reassign / force branches now write one
  `release_id` activity row (old → new, PATCH-path shape) + enqueue
  one webhook `issue.updated` (changed_fields `["release_id"]`) +
  announce WS `issue.updated` per actually-changed issue — the WS
  announce invalidates open issue detail, which `release.updated`
  does not. All writes run in one tx; the change-check SELECTs are
  `FOR UPDATE` so concurrent release/PATCH/move ops serialize.
  Idempotent no-ops and 409 deletes write/announce nothing. No
  version snapshot: `release_id` is not a field of the `Issue`
  struct. `announceReleaseUpdated` unchanged.
- Stale carried candidates from the cycle-50 close note re-checked
  against code and rejected: BulkSet cascade version (fixed C50T0),
  RestoreVersion `issue.updated` (fixed C45T0), state-deletion
  `issue.updated` (fixed C45T2), automation-triggered `issue.updated`
  (covered C44–C46).

**Version coverage for the remaining automation-cascade paths (cycle 50)**
- Shared rule: after an operation fires automations, the final in-tx
  row is snapshotted as a version iff it differs from the last
  snapshot in that operation (content comparison excluding
  UpdatedAt/DisplayID/SubtaskProgress) — never a duplicate-content
  version. The four cycle-50 tasks each shipped their own copy of
  the comparison; a follow-up consolidation keeps a single shared
  `issueRowContentEqual` beside `snapshotVersionTx`.
- BulkSet post-cascade snapshot (C50T0): a BulkSet label/assignee
  cascade after `updateIssueTx` now lands in that item's version —
  closes the known residual carried from cycle 49.
- Label/assignee endpoints (C50T1): Assign/Unassign label/assignee
  now snapshot the cascade row when an attached rule moves the row.
- CreateComment cascade (C50T2): a `comment_added` firing that moves
  the row (e.g. `set_state` / `set_priority`) now snapshots the
  cascade row; comment-only (no firing / no-op firing) writes no
  version.
- Scheduled rule firings (C50T3): a scheduled firing now snapshots
  the row change it caused, with the rule author as actor; failed /
  no-op firings write no version.
- No migration, no frontend changes.

**Version-history integrity (cycle 49)**
- CreateIssue v1 post-automation snapshot (C49T0): the v1 version
  snapshot now moves to AFTER the created-rules block, snapshotting a
  fresh in-tx re-read of the row (same tx, still under the row lock).
  A created-rule `set_state` / `set_priority` move used to leave v1
  holding the pre-rules INSERT row, so restoring v1 silently reverted
  the automation move; restoring v1 is now a no-op diff. C48T1's
  post-commit re-read for announce/return, webhook/Slack payloads,
  and failure paths are untouched.
- updateIssueTx post-automation snapshot + return (C49T1): the version
  snapshot is now taken AFTER all automation blocks (state rules +
  field-level rules), snapshotting a fresh in-tx re-read, and
  `updateIssueTx` returns that fresh row — one version per user
  action. `UpdateIssue` announce + return, `BulkUpdateIssues`
  announce, and the `RestoreIssueVersion` has-fields branch now all
  carry the post-automation row. Webhook `changed_fields` stays the
  user-patch fields; notification/Slack text stays on the user-patch
  state. Known residual (BulkSet untouched by design): a BulkSet
  label/assignee cascade after `updateIssueTx` still isn't in that
  item's version — follow-up candidate.
- MoveIssue version snapshots (C49T2): a move now writes one version
  snapshot per moved row (root + every moved subtree member), in the
  same tx, from the UPDATE ... RETURNING row — zero extra queries.
  Moves previously wrote no version at all. No-op and failed moves
  write nothing; no broadcast / return-value changes.

**Realtime payload correctness (cycle 48)**
- CreateIssue fresh snapshot (C48T1): `CreateIssue` now re-reads the row
  post-commit and uses the fresh row for BOTH the `issue.created`
  broadcast and the returned value, so a created-rule `set_state` /
  `set_priority` move no longer leaves subscribers + the 201 body with
  a stale `StateID` until refetch. Best-effort: on lookup failure it
  falls back to the pre-rules snapshot, never failing the create.
  `DisplayID` is re-derived from the fresh `SequenceID`.
- OKR transitions loader dedup (C48T0): the transitions query now uses
  `SELECT DISTINCT` (extracted into `loadIssueTransitions`), collapsing
  the per-KR copies the `key_result_issues` join used to fan out for
  issues linked to N KRs. `ia.id` stays in the select list so genuinely
  distinct rows (same issue/old/new/timestamp, different id) are kept
  apart; ordering (issue, created_at, id) is unchanged. Replay output
  is identical — only the waste is gone.

**Issue-only objectives get a progress history (cycle 47)**
- `state_id` activity write gaps closed (C47T0): `MoveIssue` now writes
  one `state_id` activity row per moved issue (old = source state, new =
  remapped state) in the same tx, alongside the untouched `_moved` row;
  `DeleteState` reassign writes one `state_id` row per moved LIVE issue
  (old = deleted state, new = reassign target) in the same tx, using the
  existing `movedLiveIDs` collection — soft-deleted issues get no row.
  No broadcast, notification, or return-value changes. Tests: 5 new
  (move remap row incl. backlog fallback, failed move silent, reassign
  rows per live issue + zero for the soft-deleted ghost, zero-ref and
  409 paths silent).
- Link time + issue-KR replay (C47T1): migration 000062 adds
  `created_at` to `key_result_issues`, backfilling existing rows with
  `GREATEST(kr.created_at, issue.created_at)` — documented as a lower
  bound, not the true link time; new links get the true time from
  `DEFAULT now()`. `fillObjectiveProgressHistory` now replays issue-KR
  state transitions in three extra batched queries (no N+1): per-issue
  state-at-time reconstruction (initial state = earliest row's
  `old_value`, else current `state_id`; pre-link transitions shape the
  link-time state without emitting a point), contribution only from
  `max(link, KR, issue created_at)`, done iff the reconstructed state
  maps to a done group (a state with no surviving row maps to NOT done —
  conservative, never over-claims), event timestamps = union of check-ins
  + link starts + post-link transitions with same-timestamp collapse,
  last-60 cap kept. Metric-only histories are byte-identical (legacy
  builder kept as a wrapper; pinned by tests). Unlink removes the row,
  so an unlinked issue vanishes from history — consistent with the
  existing metric-KR orphan defense and current-progress computation.
  Tests: 9 new replay tests + migration down/up round-trip test.
- GoalDetail objective chart (C47T2): a new "Objective progress over
  time" section renders an objective-level chart fed ONLY by
  `objective.progress_history` (no new fetch), reusing the C42/C43
  visual language + guards (accent-blue line, zero time range →
  index-spread, zero value range → midline, single point → dot, empty →
  section hidden, not an empty box). The per-KR check-in history
  sections are unchanged; no per-KR chart is duplicated for issue KRs.
  This is the only chart that shows anything for issue-only objectives.
  The Goals list sparkline needs no change — it renders automatically
  once history is non-empty. Tests: 9 new (6 component + 3 GoalDetail
  integration incl. zero check-ins fetches).
- Known residual (not a defect): the transitions loader joins
  `key_result_issues` without selecting the KR, so an issue linked to N
  KRs yields each transition N times — re-application is idempotent and
  events dedup, so results are identical; a future cleanup may add
  DISTINCT. Pre-T0 move/reassign transitions have no activity rows
  (the transactions are gone) — replay over surviving rows is the
  documented semantics, same orphan-defense principle as metric KRs.

**Close the remaining issue.updated fan-out gaps (cycle 46)**
- BulkSet cascade (C46T0): `BulkSetIssues` captures a before-image
  `state_id` under the row lock, then after `replaceIssueLabelsTx` /
  `replaceIssueAssigneesTx` re-reads the final in-tx row — that row is
  the announced snapshot (post-replace labels/assignees/state, exactly
  what commits) and its `state_id` vs the before-image is the fan-out
  detector (C45T1 before/after pattern; a net-zero round trip — direct
  move + a rule moving it back — reports false). A cascade `set_state`
  move now fans out `objective.updated`; the announced `issue.updated`
  payload is no longer stale. Announce loop still after `tx.Commit`;
  one `issue.updated` per item, one `objective.updated` per linked
  objective across the batch. Tests: 4 new tests (labels-changed
  cascade, assigned cascade, labels-only no cascade, direct + cascade
  fans out once).
- Standalone label/assignee paths (C46T1): `AssignLabel`,
  `UnassignLabel`, `AssignAssignee`, `UnassignAssignee` in
  `internal/service/label.go` now announce via a shared
  `announceLabelChange` helper — after commit only, on a real change
  (`RowsAffected() > 0`); post-commit snapshot; before/after
  `state_id` comparison fans out `objective.updated` when an in-tx
  automation rule's `set_state` actually moved the issue. Exactly one
  `issue.updated` per call; no-ops (idempotent re-attach, unassign
  absent, malformed id) and failed commits announce nothing.
  Recurring (`CreateIssue` + attach) and intake BulkTriage accept
  callers inherit the broadcast, as intended. Tests: 12 new tests
  (four paths, cascade set_state per path, no-op cascade, idempotent
  re-attach, failed mutation, recurring created→updated sequence).
- With this cycle, every direct issue mutation path broadcasts
  `issue.updated` after commit only, and every state-changing path
  (direct or automation-cascade) fans out `objective.updated` to
  linked objectives. Honest limit carried from cycle 44: issue-only
  objectives still have no sparkline (`issue_activities` replay write
  coverage unaudited).

**Close the issue.updated emission gap (cycle 45)**
- RestoreIssueVersion broadcast (C45T0): captures the `stateChanged`
  flag from `updateIssueTx` instead of discarding it; after commit (and
  only when `patch.hasFields()`) mirrors `UpdateIssue` exactly —
  `announceIssueUpdated` unconditionally +
  `announceObjectivesForIssue` iff `stateChanged`. No-diff restores
  announce nothing; failed restores return pre-commit. Tests:
  `TestRestoreBroadcast*` 5/5.
- Automation set_state broadcast (C45T1): scheduled firings
  (`fireScheduledRule`) + comment-triggered firings (`CreateComment`)
  announce `issue.updated` + objective fan-out after commit when a
  `set_state` action actually moved the issue — detected by comparing
  `state_id` before vs after the firing tx (only `set_state` touches
  `state_id`, so before≠after means exactly a real move). The announce
  sits strictly after `tx.Commit` succeeds; rolled-back / failed
  commits announce nothing; post-commit snapshot lookup failures are
  best-effort skips, never fatal. `runAutomationRuleActionsTx`
  signature untouched; depth-1 loop guard untouched; `set_state`
  ONLY — other automation actions stay unannounced. Verify-don't-touch
  confirmed in code: UpdateIssue / BulkUpdate / BulkSet outer
  announces + `updateIssueTx`'s final in-tx `state_id` re-read cover
  every in-tx firing, CreateIssue created-rules are covered by its
  post-commit `announceIssueCreated` — no duplicate announces added.
  Tests: 10 new tests (scheduled move / no-op / non-set_state /
  blocker-rejected, comment move / no-op / no-rule / non-set_state /
  blocker-rejected, UpdateIssue cascade announces exactly once).
- DeleteState reassign broadcast (C45T2): the reassign UPDATE uses
  `RETURNING id::text, deleted_at IS NULL`; after commit, per moved
  live issue, post-commit snapshot + `announceIssueUpdated` +
  `announceObjectivesForIssue`. Zero refs / 409 / failed commit
  announce nothing; soft-deleted issues moved by the bulk UPDATE are
  never announced. Tests: `TestDeleteState*` + `TestStateMutations*`
  5/5.
- Known follow-ups (verified in code, out of scope — candidates for a
  later cycle): BulkSet label/assign cascade — `stateChanged` is
  computed by `updateIssueTx` BEFORE `replaceIssueLabelsTx` /
  `replaceIssueAssigneesTx` run their `labels_changed` / `assigned`
  rules, so a cascade `set_state` move there gets `issue.updated`
  (stale pre-move snapshot) but NO `objective.updated`; standalone
  `label.go` Assign/UnassignLabel/Assignee paths run automation rules
  but announce no `issue.updated` at all (zero `announceIssueUpdated`
  in `internal/api/`). Also observed: CreateIssue announces
  `issue.created` with the pre-created-rules snapshot (stale `StateID`
  after a created-rule `set_state` move) — benign per the
  clients-refetch broadcast contract.

## [v0.3.0] - 2026-10-17

"Depth" milestone: the next tier of the Plane parity backlog — modules,
wiki pages, calendar, workspace settings, time tracking — plus the
queued onboarding follow-up (invites endpoint).

This release also includes everything planned for v0.2.0 ("Credibility"
milestone), which never shipped separately — see the v0.2.0 content
subsection at the end of this entry.

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

**Initiatives (cycle 27)**
- Workspace-scoped program containers that group projects (Plane
  "initiatives", which Plane paywalls — glance ships them free). Backend
  (C27T0): migration 000050 (`initiatives` + `initiative_projects`
  junction; `created_by` is SET NULL so user deletion never cascades).
  `POST/GET /api/v1/workspaces/{slug}/initiatives` (archived excluded by
  default, `?include_archived=1` to include), `GET/PATCH/DELETE
  .../{initiativeID}`, member-project endpoints (`POST` + `DELETE`
  `.../{initiativeID}/projects`, idempotent), and `GET
  .../{initiativeID}/rollup` — one aggregate query returning issue
  counts by state group across all member projects, a per-project
  breakdown, and completion % (done-group issues / total; zero-issue
  projects included with zero counts). Auth required; reads are
  member-scoped (guest 5+), writes need member (15)+; cross-workspace
  access is 404, not 403. Deletes cascade only membership rows —
  initiatives own no issues, so issue data is never touched.
- List + create UI (C27T1): `/w/:slug/initiatives` page with progress
  cards (rollup per card, skeleton loading), sidebar entry in the
  Workspace section with active-count badge, create dialog with
  member-project picker. Guests read; members create.
- Detail UI (C27T2): `/w/:slug/initiatives/:id` with aggregate rollup
  (stacked state-group segments), per-project breakdown bars,
  member-project add/remove, status changer, delete-with-confirm.
  Guests read-only.

**Recurring work (cycle 28)**
- Scheduled issue creation on a daily/weekly/monthly cadence (standups,
  weekly reports, sprint rituals) — the first deliberate beyond-Plane
  capability (Plane has no native recurring issues; glance ships it
  free). Backend (C28T0): migration 000051 (`issue_schedules`, workspace
  + project tenanted, read any member / mutate member+), schedule CRUD
  service with pure next-occurrence math (daily/weekly/monthly ×
  interval, weekday sets, day-of-month clamp incl. leap, ends_on
  inclusive, hour in UTC).
- Ticker execution (C28T1): 1-min ticker claiming due schedules
  `FOR UPDATE SKIP LOCKED`; each occurrence created through the normal
  `CreateIssue` path so automations, webhooks, Slack and assignee
  notifications apply exactly as for manual issues; activity entry
  "Created by schedule '<name>'"; stale label/state/assignee refs
  deactivate the schedule instead of failing forever; per-occurrence
  atomicity (row lock + fresh re-read + advance), no double-run.
- HTTP surface (C28T2): schedule CRUD + `POST .../schedules/:id/run-now`
  (calling user as actor, must be workspace member; stale refs → 400,
  no-op → 409), list with next run, `docs/openapi.yaml` updates,
  `docs/recurring-work.md`.
- Schedules UI (C28T3): project sub-nav "Schedules" page — rules table
  (name, cadence summary, next run, active toggle, run-now,
  edit/delete), builder dialog reusing existing pickers, honest empty
  state. Guests read; members edit.
- (Recurring work) Honest v1 limitations: hour is UTC (no per-workspace
  timezone yet), template vars are `{{date}}` / `{{datetime}}` only,
  occurrences are ordinary issues once created (no skip-next, no series
  editing), and a schedule without `ends_on` runs until paused/deleted
  (surfaced in UI copy).

**Milestones — Releases surface completion (cycle 38)**
- Release progress snapshot (C38T0): `Release` gains
  `progress_snapshot` — live count of the release's non-deleted issues
  by state group (triage, backlog, unstarted, started, completed,
  cancelled — same six groups as cycle snapshots, every group present
  even when zero), computed in ONE aggregate query on List/Get (Create
  returns the exact all-zero snapshot; Update attaches the live
  snapshot, which also fixes Update's `issue_count`, previously always
  0). No migration, no stored column. Overview gains `releases`: the
  project's upcoming (planned) releases — id/name/status/date/
  issue_count/snapshot, ordered by release_date nulls-last then name,
  capped at 5, `[]` when none. openapi (incl. the import description,
  which no longer says milestones are skipped).
- Milestones UI (C38T1): `ReleaseCard` shows a progress bar + folded
  counts (`OrgProgressBar` + `OrgProgressCounts`, shared
  `snapshotProgressCounts` fold: triage+backlog+unstarted → open,
  started → in progress, completed+cancelled → done) from the backend
  snapshot — the card never fetches the release's issues; an empty
  snapshot renders an all-open bar via the opt-in `emptyBar` prop
  (Cycles callers untouched). Overview gains a Milestones section
  (upcoming releases with progress + date; rows deep-link to the
  release detail via `?release=<id>`, header links to Releases; the
  empty state keeps the section visible). GitHub import result shows a
  "Releases created" Stat. Prototype: `prototype/milestones.html`.
- GitHub import maps milestones → releases (C38T2): each distinct
  milestone title becomes a Release (name = title, release_date =
  milestone `due_on` or NULL, status `planned` if open / `released`
  if closed); each imported issue is assigned to its milestone's
  release; duplicate titles map to ONE release; a pre-existing release
  is reused untouched. `milestones_skipped` now counts only
  unmappable milestones (empty title); result gains
  `releases_created`. Import copy + docs updated. Known non-blocking
  observation: an unknown `?release=` id fires one wasted issues fetch
  whose error is never rendered — the list still renders.

**Milestone parity completion (cycle 39)**
- Jira + Plane import milestone mapping (C39T0): Jira `fixVersions`
  map to releases — each distinct version name becomes a Release
  (name = version name, release_date = version `releaseDate` when
  present and parseable, status `released` when the version is released
  or archived, else `planned`); each imported issue is assigned to its
  version's release; duplicate names map to ONE release; a pre-existing
  release is reused untouched; issues with multiple fixVersions take
  the first (glance issues carry a single `release_id`); only
  empty-name versions count in `versions_skipped`. Result gains
  `releases_created` + `versions_skipped`; the Jira search fetch now
  requests `fixVersions`. Plane export carries NO milestone data (no
  per-issue milestone link, no milestones collection — verified
  against the parser fields, both fixtures, and Plane's export docs),
  so the Plane import creates no releases — documented as a no-op,
  nothing invented. openapi updated. Jira preview rows do not yet
  show the fixVersion (follow-up if wanted).
- Detail sub-issue progress + wasted-fetch fix (C39T1): the issue
  detail Sub-issues section renders the existing `SubtaskProgressBar`
  (detail variant — full bar + done/total) under the header, above the
  child list, fed by `issue.subtask_progress` (already on the detail
  payload); renders nothing when there are no sub-issues, so the empty
  state is unchanged. `Releases.tsx` gates the detail issues query on
  the release actually existing (`selected !== null`), so an unknown
  `?release=` id fires no fetch — closing the cycle 38 observation.
  Prototype: `prototype/subissues.html`.

**OKR check-in history (cycle 40)**
- Check-in history backend (C40T0): migration
  `000060_key_result_checkins` (append-only by convention;
  `key_result_id` CASCADE, `author_id` SET NULL, `previous_value`
  nullable, `new_value`, `note` max 1000 chars, `created_at`; index on
  `(key_result_id, created_at DESC)`). Metric KRs seed one entry at
  creation (`previous_value` NULL, `new_value` = the inserted
  `current_value` — 0 when omitted, not `start_value`); every
  `current_value` change on a metric KR records an entry (old → new,
  optional `checkin_note`, author) in the same transaction as the
  UPDATE. No entry on no-op updates, title/unit/target-only changes,
  or issue KRs. New endpoint `GET .../key-results/:krID/check-ins`
  (member 15+, newest first, capped at 100, `[]` for issue KRs;
  author `{id, display_name}`, null when the user was deleted).
  openapi updated. `workspace_import` does not seed (restore path —
  imported KRs show empty history until their first post-import value
  change). KR comments remain excluded; no progress-over-time chart.
- Check-in UI (C40T1): the GoalDetail metric-KR editor gains an
  optional "Note (optional)" input, sent as `checkin_note` (omitted
  entirely when empty); each metric KR shows a "Check-in history"
  timeline (newest first, `previous → new` with signed delta, note,
  author + time; the seed entry renders "started at {value}", never as
  a delta; "Deleted user" for a null author, matching the existing
  convention). History mounts for members only (the endpoint is member
  15+) and never fetches for issue KRs; loading / error / empty
  states covered. Prototype: `prototype/checkins.html`.

**Realtime completion + Jira preview fixVersion (cycle 41)**
- Realtime handlers (C41T0): the frontend `handleRealtimeEvent` now
  covers the six `*.updated` events the backend already broadcast but
  the client dropped — `module.updated` (modules + module-issues),
  `page.updated` (pages + page-revisions + per-page detail),
  `release.updated` (releases + release-issues + release-notes),
  `template.updated`, `custom_field.updated` (custom-fields + the
  single subject issue's detail — per-issue value writes announce with
  the issue id and emit no `issue.*` event, so without this a second
  tab's open issue stayed stale; field-CRUD events carry a field id,
  which matches no issue key), `initiative.updated` (initiatives list
  + detail + rollup). `useProjectRealtime` also subscribes to the
  workspace channel — `initiative.updated` arrives ONLY there
  (initiatives are workspace-scoped). Double-delivery (project +
  workspace) is benign: the handler's only effect is idempotent
  invalidation. `resyncProject` invalidates the same key families on
  reconnect. Tests: realtime.test.ts 9 → 18.
- Jira preview Milestone column (C41T1): `JiraImportPreviewRow`
  gains `milestone` (the issue's FIRST fixVersion, trimmed; omitted
  when none or empty-name-first — reuses the shared `mapJiraIssue`
  `versionName` computation, import path untouched). The Jira preview
  table renders the same `{milestone} (→ release)` badge as the
  GitHub table, `—` when absent. The preview no longer hides the
  release assignment the import is about to perform.
- Notification realtime (C41T2): `notification.created` invalidates
  the `["notifications"]` prefix (covers list/unread/prefs/schedule +
  context). `useProjectRealtime` takes an optional `userId` and, when
  set, subscribes to the caller's own `user:{id}` channel —
  `notification.created` arrives ONLY there, and the hub authorizes a
  subscriber to its OWN user channel only (foreign ids → forbidden,
  verified in `authorizeChannel`; no hub/auth change). The id comes
  from the existing `/me` session plumbing (`useAuth()` at the
  ProjectScope / IssueDetailContent call sites); omitted while auth
  is loading. Notifications stay out of `resyncProject` (the bell
  refetches on open). Tests: realtime.test.ts 18 → 21, incl. a
  resync-exclusion guard.

**OKR history completion (cycle 42)**
- Progress-over-time chart (C42T0): GoalDetail's KR check-in history
  gains a pure-SVG chart above the timeline, from the SAME check-ins
  query (no new fetch — a test asserts exactly 1 check-ins GET).
  Points are every check-in's `new_value` chronological (seed first);
  dashed labelled reference lines for `start_value` / `target_value`;
  Y domain = min/max ∪ {start, target}; first/last date axis labels.
  Guards, all tested: zero time range (backfilled/imported rows can
  share one timestamp) → index-spread, never divide by zero; zero
  value range → flat midline; descending KRs handled by the domain;
  single point → dot + reference lines, no line; empty → no chart
  (the timeline's "No check-ins yet." stands). `<title>` +
  `aria-label` describe the trend. Timeline rows/order/copy
  unchanged. Tests: GoalDetail.test.tsx 23 → 31.
- Backfill KR history seeds (C42T1): migration `000061` inserts one
  seed row per metric KR with zero check-ins (pre-cycle-40 KRs, whose
  timeline showed "No check-ins yet"): previous NULL, new =
  `current_value`, note `'Backfilled history'`, author NULL (the
  creator is not recorded on the KR row — nothing to recover),
  `created_at = kr.created_at`. Idempotent (`NOT EXISTS` guard —
  precedent 000011); down migration deletes exactly the
  previous-NULL + note-marker rows. Test re-executes the statement
  (000011 pattern): one seed, second run adds nothing, issue KRs get
  nothing, KRs with existing check-ins untouched.
- Workspace import seeds check-ins (C42T2): export carries each
  metric KR's FULL check-in history (`check_ins`, oldest-first, no
  cap-100 — the API list cap does not apply to the archive; values as
  `json.Number`; author as id + email). Import restores them via the
  established `insertWithFallback` + dedup discipline; author
  resolves by email, null when unmapped — never fabricated;
  non-numeric values skip with a note; hand-crafted check-ins on
  issue KRs are refused. Restoring history never changes the KR's
  `current_value`; retries complete missing check-ins without
  duplicates; old archives without `check_ins` still import (the
  backfill covers those KRs later). Known minor interaction: a
  re-import after the backfill ran can leave two seed rows (the
  backfill seed + the archived seed, different UUIDs) — an extra
  "started at" row, no data loss.

**Realtime completeness (cycle 44)**
- Global realtime (C44T0): new `GlobalScope` layout mounted inside
  `ProtectedRoute` above `/onboarding` + `AppShell`, so every
  authenticated route — including the slug-less `/`, `/notifications`,
  `/profile`, `/settings/tokens`, `/w` — subscribes the `user:{id}`
  channel (the ONLY channel carrying `notification.created`) via new
  `useGlobalRealtime`, which routes through the SAME
  `handleRealtimeEvent` notification.created case — no second handler.
  GlobalScope owns the socket lifecycle + a global reconnect callback
  (registered through the stable `handleReconnect` dispatcher; order
  project → workspace → global) that resyncs only the bare
  `["notifications"]` prefix, and owns the single `<ConnectionDot />`
  (moved out of `WorkspaceScope`, so `/w/:slug/*` routes still show
  exactly one dot). Mounting in `App.tsx` rather than `AppShell` was
  required to cover `/onboarding`, which sits outside `AppShell`.
  Server side verified: the user channel carries ONLY
  `notification.created`, so the global hook's empty-slug ctx is safe.
  Tests: GlobalScope layout tests (exactly one dot at all three nesting
  depths, user-only subscription, live invalidation, unmount cleanup)
  + realtime tests (global hook, resyncNotifications, 3-way dispatcher
  composition, stale-cleanup guard).
- Backend objective.updated on linked-issue progress change (C44T1):
  new best-effort `announceObjectivesForIssue(s)` helper (one
  `SELECT DISTINCT o.id` via `key_result_issues → key_results →
  objectives`, workspace-scoped by slug; lookup failure skips
  silently; deliberately NO `deleted_at` filter so delete paths still
  fan out after commit) hooked into every issue-mutation path that
  can change KR progress, after commit only: UpdateIssue (gated on
  `state_id` actually changing — `updateIssueTx` gains a third return
  computed by re-reading `state_id` AFTER the automation blocks, so
  a field-level rule cascade is caught and a net-zero cascade reports
  false), BulkUpdateIssues + BulkSetIssues (one batch announce,
  DISTINCT per objective), DeleteIssue + BulkDeleteIssues, MergeIssue
  source (gated on the source's state actually changing), MoveIssue
  (move always rewrites `state_id` — remap by state name else the
  target's backlog fallback, and same-project moves are rejected —
  so the hook matches UpdateIssue's id-based gate granularity).
  Title-only / same-state edits, unlinked issues, and failed
  mutations announce nothing; `issue.updated` emission untouched.
  Explicitly skipped with reasons: RestoreVersion, scheduled +
  comment-triggered automation state changes, and state deletion
  (all emit no `issue.updated` today — adding `objective.updated`
  alone would diverge the streams; follow-up with that gap),
  ConvertIssue (parent_id only, never state/deletes), intake accept
  (pre-triage, no links yet). Tests: 17
  `TestObjectiveBroadcastLinkedIssue*` with a capturing Broadcaster
  (state change, title-only, same-state, unlinked, delete, bulk
  delete/update/set, merge ×3, move, two objectives, two KRs one
  objective, failed mutation, automation cascade, nil broadcaster).
  Review note: the first full-suite re-run showed 240 FAILs, all
  "database glance_test_service does not exist" — root-caused as
  two overlapping `go test` runs sharing one per-package test DB
  (an orphaned run's teardown DROP hit the live run); clean single
  re-run green. Never run two `go test` invocations on the same
  package concurrently.
- Residual (still open): issue-only objectives render no sparkline —
  the trajectory would need replaying `issue_activities`
  (field-level state history), but activity-write coverage across
  bulk/automation paths is unaudited; that audit is a later cycle's
  job.

**Realtime everywhere + Goals list sparkline (cycle 43)**
- Workspace-level realtime mount (C43T0): new `WorkspaceScope`
  layout wraps every `/w/:slug/*` route (workspace pages AND the
  project routes nested under it) — it subscribes the
  `workspace:{slug}` + `user:{id}` channels, owns the socket
  lifecycle + reconnect resync for workspace keys, and renders the
  single `<ConnectionDot />` (moved up from ProjectScope, so
  project pages show exactly one dot). New `resyncWorkspace`
  unconditionally invalidates 16 workspace-level key families on
  reconnect (initiatives/initiative/initiative-rollup,
  objectives/objective/kr-checkins, bare `["notifications"]`,
  workload, risk, mywork, projects, workspace, members, webhooks,
  project-templates, workspace-audit-log — each verified against
  its owning page), never project keys. New `objective.updated`
  handler case (objectives list + objective detail + kr-checkins
  prefix). Review fix (coordinator): the WS client keeps only the
  last-registered `onReconnect`, and React runs child passive
  effects before parent ones — per-scope handlers raced by mount
  order, so on a fresh project-page load the workspace-only handler
  overwrote the composed one and `resyncProject` silently never
  ran. Both scopes now register the SAME stable `handleReconnect`
  dispatcher, which reads the currently-registered resync callbacks
  at reconnect time; registrations return identity-guarded
  unregister cleanups. Tests: realtime.test.ts + WorkspaceScope
  layout tests (single dot, resync key sets, double-mount safety,
  dispatcher incl. stale-cleanup guard). Known bound: `/notifications`
  and `/` carry no workspace slug, so they sit outside
  WorkspaceScope — the Notifications page still only gets
  `notification.created` inside a project scope (follow-up).
- Backend OKR broadcast (C43T1): `EventObjectiveUpdated`
  (`objective.updated`, `workspace:{slug}` channel only, id-only
  payload — the `initiative.updated` precedent) announced after
  successful mutation from all eight OKR mutation paths:
  Create/Update/DeleteObjective, Create/Update/DeleteKeyResult,
  Link/UnlinkIssueToKeyResult (link/unlink change issue-KR
  progress, so they announce too — caught in review). No announce
  from import paths (bulk import would spam the channel) and none
  on failed mutations. Tests with a fake Broadcaster assert the
  exact type/channel/payload per call site. Residual: a linked
  issue's own state change fires `issue.updated`, not
  `objective.updated` — another tab's Goals list refreshes on OKR
  mutations, not on linked-issue progress drift.
- Goals list progress sparkline (C43T2): the list response carries
  an additive `progress_history` per objective (one point per
  distinct metric-KR check-in timestamp, capped at the last 60) —
  a batched 2-query loader (no N+1), replaying check-in rows
  chronologically with the same progress formula as the current
  bar (metric via `metricKRProgress`, issue KRs at their current
  progress, KRs contribute only from creation onward, same-timestamp
  rows collapse to the post-event state, orphan rows skipped).
  Each Goals card renders a pure-SVG `GoalSparkline` under the
  progress bar (C42 chart guards: index-spread, midline, dot,
  empty → null) fed ONLY by the list response — no per-goal
  fetches. Issue-only objectives have empty history and render no
  sparkline (honest: no trajectory data); the sparkline's last
  point can predate the current bar when only issue KRs were added
  after the last metric check-in.

**Workspace audit log (cycle 37, Plane parity)**
- Workspace audit log backend (C37T0): migration
  `000058_workspace_audit_log` (workspace-scoped, append-only by
  convention; actor_email snapshot survives account deletion) + service
  (`RecordWorkspaceAuditTx` same-tx / pool best-effort variants,
  `ListWorkspaceAuditLog`) + emitters wired into the existing write
  paths for 14 control-plane actions — `member.invited`,
  `member.added`, `member.role_changed` (only on a real change),
  `member.removed`, `project.created`, `project.archived`,
  `project.restored`, `project.deleted`, `workspace.settings_updated`
  (changed-field names only), `integration.webhook_created` /
  `webhook_deleted` (host only — never the full URL),
  `integration.github_configured`, `integration.slack_configured` /
  `slack_disconnected` (boolean meta only — never the secret/URL).
  Routes: `GET /api/v1/workspaces/{slug}/audit-log` (filters: action,
  actor_id, actor email substring, entity_type, since/until) +
  `GET .../audit-log/export?format=csv` (10,000-row cap, `X-Truncated`
  header, `audit-log-<slug>-<date>.csv`). Admin-only reads (role 20);
  non-member → 404, member non-admin → 403. openapi.
- Workspace audit log UI (C37T1): Settings → Audit log section
  (admin-only; non-admins never see it or fetch): newest-first table
  (When / Actor / Action / Entity / IP), draft/applied filters
  (Category narrows the Action dropdown — the backend has no category
  param — Actor email, Entity type, Since/Until as RFC 3339 UTC),
  pagination, row-click detail panel (pretty-printed meta JSON, IP,
  entity, actor), Export CSV button (same applied filters),
  loading/error/empty states. Null IP renders as "—". 16 new vitest.
- Tamper-evidence hardening (C37T2): migration `000059` adds a
  `BEFORE UPDATE OR DELETE` trigger on `workspace_audit_log` that
  rejects direct DML (depth-1) while allowing the table's own FK
  lifecycle (workspace DELETE cascades its rows, user DELETE NULLs
  `actor_id` only). Archive export/import explicitly excludes the audit
  log (documented + tested — history is not portable; importing audit
  rows would fabricate witnessed history). Emitter meta secret review:
  pass, no leaks. 5 tamper/archive tests.
- Honest v1 limitations: no `token_created` / `token_revoked` events —
  API tokens are user-scoped (no workspace attribution; fanning one
  token event out to every workspace would fabricate history); token
  lifecycle belongs in a future global auth log, same as global
  sign-in/out events. IP is NULL for all v1 rows (no service function
  carries the request IP; column + field exist for future plumbing).
  No retention purge / auto-expiry (append-only, full stop — retention
  is a future decision). No real-time broadcast for audit entries.
  The trigger guards the application path, not DDL/TRUNCATE (a role
  with DDL privilege can drop the trigger).

**Pages ↔ Work items (cycle 36, Plane parity)**
- Page↔issue links backend (C36T0): migration `000057_page_issue_links`
  (`page_issue_links` with UNIQUE(page_id, issue_id) duplicate backstop,
  both FKs ON DELETE CASCADE, creator FK SET NULL) + 6 routes:
  `GET/POST .../pages/{pageID}/issues`,
  `DELETE .../pages/{pageID}/issues/{issueID}`,
  `GET/POST .../issues/{uuid}/pages`,
  `DELETE .../issues/{uuid}/pages/{pageID}` (guest 5+ reads, member 15+
  mutations; duplicate → 409, cross-project → 409, unknown → 404;
  archived and draft issues are linkable; soft-deleted issues hidden).
  openapi. Service + HTTP tests.
- Page↔issue links UI (C36T1): page-detail "Linked issues" section
  (state badge + display_id + title, click → issue detail; Link-issue
  dialog with debounced project-scoped search, already-linked / draft /
  archived filtered out; unlink with confirm; "Create issue from this
  page" creates via the existing issue-create endpoint — title = page
  title, description = TipTap doc of "From page: <title>" + page URL —
  then links and navigates to the new issue) + issue-detail "Linked
  pages" section in the project-structure area (title + relative updated
  time, click → Pages view with that page selected via location state;
  Link-page dialog with tree-flattened picker, client-side title filter;
  unlink with confirm). Member 15+ for mutations, viewers read-only
  (server enforces); loading / error / empty states. 20 new vitest.
- Workspace archive round-trip (C36T2): exporter includes
  `page_issue_links` per project (page id + title, issue display_id,
  creator id + email; streamed after issues; links to soft-deleted
  issues excluded); importer restores them (page by id-then-title,
  issue by project identifier + display_id; unresolvable side → skip +
  note; cross-project pair → skip; unmapped creator → null + note;
  re-import idempotent via ON CONFLICT DO NOTHING → "already linked"
  skip). 4 archive tests (round-trip, unresolvable side, title
  fallback, cross-project skipped).
- Cycle 36 closes the last verified Plane-parity gap in the
  Pages↔Work-items area: Plane documents linking Pages to Work Items
  and creating Work Items from Page content (Pro/Business-tier in
  Plane); glance had zero page↔issue linkage. The audit also closed the
  Milestones gap without new work — Milestone already maps to Releases
  (issue-detail picker exists, Releases detail computes progress).
- Honest v1 limitations: no live issue cards embedded *inside* page
  content (links live in the Linked-issues section, not the Markdown
  body); no @-mentioning issues inside page Markdown; same-project
  links only; no link comments / annotations; no notifications on
  link/unlink.

**OKRs — Objectives + Key Results (cycle 35, beyond Plane)**
- OKR backend (C35T0): migration `000056_okrs` (`objectives` with
  UNIQUE(workspace_id, title), status CHECK active/completed/archived,
  owner FK SET NULL; `key_results` with kind CHECK metric/issue,
  CHECK(target_value <> start_value); `key_result_issues` composite PK)
  + `GET/POST .../objectives`, `GET/PATCH/DELETE
  .../objectives/{id}`, `POST .../objectives/{id}/key-results`,
  `PATCH/DELETE .../objectives/{id}/key-results/{krID}`,
  `POST/DELETE .../objectives/{id}/key-results/{krID}/issues[/{issueID}]`
  (guest 5+ reads, member 15+ mutations; PATCH tri-state; duplicate
  title → 409; kind immutable; target==start → 400; metric-only fields
  on an issue KR → 400; link idempotent; cross-workspace link → 404).
  Progress is COMPUTED, never stored: metric KRs
  clamp((current−start)/(target−start), 0, 1) (descending targets
  work); issue KRs = done-group linked issues ÷ linked issues (0 with
  no links); objective = mean of KR progress. openapi. Service + HTTP
  tests.
- OKR UI (C35T1): Goals list + detail pages implementing
  `prototype/goals.html` — sidebar "Goals" entry under Initiatives
  (non-archived-count badge), routes `/w/:slug/goals` + `/:id`,
  objective cards (title, owner, target date, status badge, progress
  bar, KR count), create dialog (owner picker, dates, status; 409
  surfaced inline), detail page (header + status changer + edit/delete,
  KR list with per-KR bars, metric inline current-value update, issue-KR
  project-first link picker + unlink confirm, add-KR form with
  zero-span client guard, edit form with no kind control), guest
  read-only gating (server 403 backstop), loading / error / empty
  states. 31 new vitest.
- Workspace archive round-trip (C35T2): exporter includes `objectives`
  per workspace (nested KRs + KR-issue links keyed by project
  identifier + issue display_id — immune to the UUID clone remap,
  streamed last so links resolve against imported issues); importer
  restores them (owner by email; unmapped owner → null + note; metric
  KRs fully restored; issue KRs restore resolvable links, unresolvable
  → KR imports link-less + note; zero-span KR skipped + note; title
  conflict with a different objective → objective + KRs skipped, never
  merged). 5 archive tests (round-trip, unmapped owner, unresolvable
  link, zero-span skipped, title conflict).
- Cycle 35 is a deliberate beyond-Plane cycle (parity gaps exhausted;
  c34 closed the last verified one): Plane has no OKR feature — its
  Initiatives are containers and Milestones are date alignment, neither
  carries measurable targets.
- Honest v1 limitations: no KR check-in history / comments; no
  confidence (RAG) scores; flat objectives (no sub-objectives, no
  objective↔initiative alignment links); KRs carry no target dates
  (only the objective does); no OKR notifications; no quarter presets
  (free start/target dates); no CSV export (workspace archive only);
  no AI-suggested KRs.

**Project status updates (cycle 34)**
- Project updates backend (C34T0): migration `000055_project_updates`
  (`project_id` CASCADE, `author_id` SET NULL, `status` CHECK
  on_track/at_risk/off_track, plain-text `body`, index on
  `(project_id, created_at DESC)`) + `GET/POST .../updates`,
  `PATCH/DELETE .../updates/{updateID}` (viewer 10+ reads, member 15+
  mutations, author-or-admin edit/delete; invalid status / empty body →
  400, unknown update → 404; PATCH tri-state, no-key PATCH → 400).
  openapi. 7 service tests + 3 HTTP tests.
- Project updates UI (C34T1): Updates section on Overview — latest-status
  header chip (derived from the first list item; "No updates yet" empty
  state), composer (status select + textarea, member 15+ only; viewers
  read-only), timeline newest first (status badge, author, relative
  time, `(edited)` marker, body as React-escaped plain text — never the
  Markdown renderer), inline Edit / confirm Delete (author or admin).
  11 new vitest.
- Workspace archive round-trip (C34T2): exporter includes
  `project_updates` per project (id, project_id, author_id,
  author_email, status, body, timestamps); importer restores them
  (author by email; unmapped author → skipped with a skip note naming
  the email, never a fabricated author; null author imports as null;
  unknown status / empty body skipped). 3 archive tests (round-trip,
  unmapped author, invalid rows).
- Cycle 34 closes the last verified Plane-parity gap (Plane's project
  updates: On Track / At Risk / Off Track + message, chronological
  timeline on Overview).
- Honest v1 limitations: no settings toggle (Overview always shows the
  section — Plane gates behind a toggle); no update notifications or
  digest inclusion; no reactions or threading on updates; bodies are
  plain text (no Markdown rendering); latest status surfaces on
  Overview only, not the Projects list.

**Clear the deferred list (cycle 33)**
- Page export (C33T0): page-detail Export .md + Copy Markdown — the same
  document for both (front-matter `title` + `updated_at`, YAML-escaped,
  + stored Markdown content), slugified ASCII filename (`page.md` fallback),
  success/failure toasts. Frontend-only, no backend change. 13 new vitest.
- Release notes (C33T1): `GET .../releases/{releaseID}/notes` → 200
  `{release, markdown, counts}`; read gate = the release-issues gate (any
  member may read; unknown/cross-workspace → 404). Markdown groups live
  issues by state group (Completed / In progress / Backlog; cancelled and
  unknown groups land in Backlog), `- <DISPLAY_ID> <title>` lines, empty
  sections omitted, zero issues → `_No issues in this release._`, no
  timestamps (stable, diffable). `counts.unstarted` counts the Backlog
  section, so completed + started + unstarted == total. UI: release-detail
  "Release notes" button → dialog rendering the backend Markdown verbatim
  + Copy + Download `.md` (same ASCII slug rule as page export). openapi.
  7 service tests + 1 HTTP test + 10 vitest. No migration, read-only.
- Recurring double-write verification (C33T2): NO CHANGE — the c29 latent
  note is closed as refuted/fixed: the bug was real and already fixed by
  aa685c6 (bool-pattern helper), verified still fixed at HEAD by a
  caller-by-caller audit (single caller each, all return nil on false) +
  the passing `TestRecurringScheduleGuestRunNowWritesOnce` regression test
  + single-write probes on the non-member / bad-slug / malformed-JSON paths.
- Cycle 33 closes the deferred list outright: the c25 list had four items
  (calendar drag-reschedule shipped in c26; page export + release notes
  ship here), and the c29 latent-bug note is settled. Cycle 34 starts from
  a clean beyond-Plane slate.
- Honest v1 limitations: page export is the stored Markdown as-is (no
  HTML/PDF rendering), single page only; release notes are a point-in-time
  snapshot, not a stored artifact (re-generate after issues change).

**Comment resolve threads (cycle 32)**
- Resolve backend (C32T0): migration `000054_comment_resolve`
  (`resolved_at timestamptz`, `resolved_by uuid REFERENCES users(id) ON
  DELETE SET NULL`) + `POST .../comments/:commentID/resolve` /
  `.../reopen` → 200 Comment (roots only — reply → 400; any member 15+
  may resolve/reopen any thread, NOT author-only; idempotent no-op
  re-resolve/re-reopen writes no duplicate activity entry) +
  `GET .../comments?resolved=all|open|resolved` (default all, invalid
  → 400; filter applies to thread roots, subtrees intact) +
  `_comment_resolved` / `_comment_reopened` activity entries
  (`{"comment_id": "<uuid>"}`). Workspace archive export/import
  round-trips resolution (roots only; unmapped resolver degrades to
  unresolved with a skip note, never drops the comment). openapi.yaml.
- Resolve UI (C32T1): ActivitySection comment tree — Resolve/Reopen
  button on thread roots only (never on replies), resolved badge
  ("Resolved by <name> · <time>"), resolved threads collapsed by
  default with Show/Hide thread (header stays visible so Reopen is one
  click away), Open / Resolved / All segmented filter wired to
  `?resolved=`, filter-aware thread count ("N open threads" under
  All/Open, "N resolved threads" under Resolved). 5 new vitest.
- Honest v1 limitations: resolution is per-thread-root, not per-reply;
  a reply posted to a resolved thread does NOT auto-reopen it
  (reopening is explicit, GitHub-style); no "unresolved threads block
  issue close" rule in v1; page comments not covered (issue comments
  only); importers keep imported comments unresolved.
- Cycle 32 closes the most substantive item on the c25/c26/c27 deferred
  list (page export + release notes stay low value; calendar
  drag-reschedule stays medium value / Plane's own calendar is basic).

**Linear importer (cycle 31)**
- Linear backend (C31T0): GraphQL client (`POST
  https://api.linear.app/graphql`, Bearer key, pinned host, https-only,
  no redirects, 8MB cap, cursor pagination) + `POST
  .../imports/linear/teams` (team picker data), `.../linear/preview`
  (first 10 mapped + total + assignee misses, no writes),
  `.../linear` (import → created/skipped/failed/errors). Mapping:
  `state.type` → glance group (`canceled` → `cancelled`,
  unknown → `unstarted`; unmatched names create states in the mapped
  group), priority inversion (Linear 1=urgent … 4=low), labels created
  default gray, assignees best-effort by email (misses counted, never
  fatal), comments attributed with original timestamps (Markdown
  kept), `dueDate` → `target_date`, flat issues (no sub-issues/parents/
  cycles). Dedupe via footer marker `*Imported from [ENG-123](url)*`
  (closing paren + `https://linear.app/` URL required — a planted
  marker with a non-Linear URL counts for nothing). Token body-only,
  never persisted/logged/echoed; bad key → 502 generic; two-phase
  (all HTTP before one tx). Max hard cap 500. `docs/linear-import.md`,
  openapi.yaml.
- Linear UI (C31T1): ImportSection Linear card — password key input
  (cleared after every run, never persisted), Load-teams picker
  (KEY — Name), preview table (identifier link, title, state, priority,
  labels, assignee match/miss, due date), import confirm + result card
  (created/skipped/failed/assignee misses + per-issue errors), honest
  copy ("Sent to api.linear.app only, never stored"). 20 new vitest
  (12 lib + 8 component). Max input clamped to the backend cap 500.
- Honest v1 limitations: one-way, one-shot import (re-runs import only
  NEW issues, never update previously imported ones); assignee matching
  best-effort by email; deleting the footer marker defeats dedupe;
  Linear Markdown stored as-is (rendering fidelity may differ).
- Cycle 31 closes the last named deferred importer gap (deferred in
  c25/c26/c27); galih runs the loop's sprint board on Linear, so this
  is the dogfood path for migrating Linear-tracked work into glance.

**Risk radar (cycle 30)**
- Risk backend (C30T1): `GetRiskReport` — documented weighted composite
  `risk_score = round(35×overdue_ratio + 25×stalled_share +
  20×unassigned_share + 20×overload_share)`, each ratio in [0,1], score
  0–100; levels healthy 0–24 / watch 25–49 / at-risk 50–74 / critical
  75–100. Stalled = open issue with latest activity older than 7 days
  (activity log, `updated_at` fallback); overload share reuses c29
  `GetWorkload`. `GET /workspaces/:slug/risk` (member 5+), openapi.yaml,
  `docs/risk.md`. No migration needed; batched queries, no N+1.
- Risk UI (C30T3): `/w/:slug/risk` page implementing the galih-approved
  C30T2 prototype — summary strip (counts per level), per-project risk
  cards (score, level badge, score bar, factor rows with ratio + mini
  bar), drill-down links into Issues on the established filter schema
  (overdue → `due_before=yesterday`, stalled → `updated_before=7d`,
  unassigned → `assignee=none`, overload → Workload), loading / empty /
  error states. Sidebar "Risk" entry in the Workspace section next to
  Workload. 28 new vitest.
- Latent fix (C30T0): `requireScheduleMember` double-write — helper now
  uses the bool-pattern (writes + returns false on failure; caller
  returns), all 3 call sites updated, regression test asserts a single
  response write on the 403 path.
- Honest v1 limitations: risk is a heuristic composite, not a
  prediction; weights fixed v1; stalled detection keys off the activity
  log with `updated_at` fallback (bulk no-op updates reset staleness);
  no historical risk series in v1; overload share depends on capacity
  being set (c29).
- Cycle 30 is the third deliberate beyond-Plane capability (Plane's
  analytics show descriptive charts but no synthesized project health
  signal); shipped free.

**Workload & capacity (cycle 29)**
- Workload backend (C29T0): migration 000052
  (`workspace_members.weekly_capacity_points`, 0..999 CHECK), `GetWorkload`
  aggregation (per-member open counts/points, overdue, priority buckets,
  14d velocity, overloaded flag, unassigned per project — 3 queries, no
  N+1), `GET /workspaces/:slug/workload` (`project=` repeatable filter),
  `PUT /workspaces/:slug/members/:id/capacity` (member 15+), openapi.yaml.
- Workload UI (C29T1): `/w/:slug/workload` page — per-member
  load-vs-capacity bars (green → amber ≥80% → red overloaded, neutral
  when unset), overdue/urgent flags, estimate-point load, 14d velocity,
  project filter chips, drill-down to assignee-filtered issues,
  unassigned-per-project triage section; sidebar "Workload" entry in the
  Workspace section. 30 vitest.
- Capacity alerts (C29T2): migration 000053 (`capacity_alerts`, PK =
  member+day dedupe key), daily pass on the existing reminder ticker —
  single batched cross-workspace scan, conditional claim
  `ON CONFLICT DO NOTHING` per member, `capacity.overloaded`
  notification to the overloaded member (once/day dedupe, prefs-aware);
  weekly digests gain a "Capacity overloads" section; CapacityEditor
  popover for `weekly_capacity_points` (member 15+); `docs/workload.md`.
- Honest v1 limitations: capacity is estimate points, not hours;
  overload notification fires once per member per day; velocity is a
  simple 14d completed count, not cycle-normalized; per-day dedupe is
  calendar-day based (a fresh day = a fresh alert).
- Cycle 29 is the second deliberate beyond-Plane capability (Plane's
  analytics have per-assignee stats but no weekly capacity planning or
  overload signal); shipped free.

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

**Command & clarity (cycle 18)**
- Project icons (C18T4): `projects.icon TEXT NULL` (migration 000046,
  validated server-side: single emoji grapheme, no ZWJ sequences, empty
  = NULL); `POST/PATCH /projects` accept optional `icon`
  (`ErrInvalidIcon` → 400); emoji picker in project settings + new-project
  dialog; sidebar project rows show the icon (fallback: initials).
- Mermaid diagrams (C18T5): ```mermaid fenced blocks in markdown surfaces
  (pages, revision preview, public share, project overview) render
  client-side via a lazily-imported mermaid chunk
  (securityLevel:"strict", idempotent hydration); render failures or
  no-JS fall back to the plain code block — never a blank hole. Entry
  chunk growth +1.0 kB. Beyond parity (Plane #9786 is an open feature
  request).
- Global command palette (C18T0): Cmd+K mounted at the app-shell level —
  debounced `GET /api/v1/search` across all projects the caller can see
  (archived projects excluded), grouped by project with icon +
  identifier; `ENG-123` display-ID fast path now jumps across projects;
  workspace-scoped recents; commands: create issue (project picker), go
  to Home/My Work/Inbox/Analytics, toggle sidebar, shortcuts cheatsheet.
- Required custom fields (C18T1): closes the documented stored-not-enforced
  gap — issue create/update enforce every project-level `required` field
  (`ErrCustomFieldRequired` → 400 listing missing fields; empty/null =
  missing, checkbox false = present; drafts exempt; partial updates enforce
  touched-and-previously-empty fields); required markers + inline server
  errors in the issue detail/create editors.
- Page comments (C18T2): `page_comments` (migration 000047 — page FK
  CASCADE, author FK SET NULL, TipTap JSONB body); `GET/POST
  .../pages/{id}/comments`, `PATCH/DELETE .../comments/{commentId}`
  (viewer(10)+ read, member(15)+ write, author-or-admin edit/delete);
  comment thread on the page view with count badge.
- Project archive/delete (C18T3): `projects.archived_at` (migration 000048);
  `POST /projects/{identifier}/archive|unarchive` (admin(20)) — archive
  hides the project from sidebar, list, and global search, and archives
  all its live issues in the same tx; `DELETE` (admin(20)) — hard delete
  with verified FK cascade, requires `?confirm=<identifier>`, refuses
  projects with non-archived issues unless `?force=true`; Danger zone
  section in project settings with type-to-confirm.

**Developer gravity (cycle 17)**
- GitHub two-way link (C17T0): issues ↔ PRs/commits — `issue_external_refs`
  (migration 000043, unique `(issue_id, provider, ref_type, ref_id)`);
  manual `POST/DELETE .../issues/:uuid/refs` (member+); repo-level
  webhook `POST /api/v1/integrations/github/webhook` (HMAC-SHA256
  signature verify) auto-links PRs whose title/body mentions `GLC-123`
  and syncs open/merged/closed state on close; `github_webhook_secret`
  set via `PATCH /workspaces/{slug}` (write-time validated, never
  serialized); `external_refs[]` embedded on issue detail; idempotent
  re-delivery.
- Project templates (C17T1): `project_templates` (migration 000044,
  workspace-scoped, payload JSONB strictly validated on write);
  CRUD `/api/v1/workspaces/{slug}/templates` (admin writes, member+
  reads); `POST /projects` accepts `template_id` — states/labels/modules/
  estimate scale seeded in the same tx (unknown id → 400, no leaked
  rows; existing same-named workspace labels reused). Template picker in
  the New project dialog + management section in workspace settings.
- Sub-issue progress rollup (C17T2): issue detail gains computed
  `subtask_progress {total, done}` (done = completed-group state, direct
  children only, single query); progress bar + "3/8" in the IssueDetail
  header, compact bars on list/board cards when total > 0.
- Dev quick actions (C17T3): issue detail header — "Copy branch name"
  (`glance/glc-123-short-slug`, slugified, 40 chars), "Copy issue URL",
  "Copy ID" with toast confirm; keyboard `c b` / `c u` / `c i`.
- Workspace activity feed (C17T4): `GET /api/v1/workspaces/{slug}/activity`
  — workspace-wide activity scoped to projects the caller can see
  (member+), cursor pagination, type filter; "Recent activity" section on
  Home under assigned issues with per-row project badges.
- Viewer role (C17T5): `RoleViewer = 10` (read-only). Enforcement rides
  the existing gates — every write path already checks
  `role >= RoleMember (15)` while reads need only membership, so no
  enforcement logic changed; migration 000045 widens the
  `workspace_members` role CHECK to `(5, 10, 15, 20)`. Role select
  (admin/member/viewer) in workspace member management; invite still
  defaults to member. Tests pin viewer blocked on issue create/update/
  delete + comment and reading everything a member can.

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

### Changed

**Cycle 19 — UI polish: Plane layout parity (UI-first, prototype-approved)**
- App shell: 52px icon rail + 280px sidebar + 44px top bar; sidebar
  collapses leaving only the rail; new `Rail.tsx`, old shadcn sidebar
  deleted; Geist → Inter.
- Shared 49px `PageHeader` (breadcrumb + view segments + actions) on all
  17 project pages.
- Board: 325px columns / 28px gap, card anatomy (ID above title + chip
  metadata row), state dots, amber priority bars (`PriorityBars.tsx`).
- Issue detail: right-docked slide-over peek panel
  (`min(1050px, 100% − 112px)`), 46px toolbar + votes, property strip,
  Properties sections (Details + Project structure), tabs, composer.
- List: dense 47px rows (`IssueRow.tsx`), 4-col grid.
- Spreadsheet: 13px density, state dots, amber bars.
- Calendar/Gantt/Roadmap: token-density pass.
- Cycles/Modules/Releases/Intake: status chips, stat bands, 47px queue
  rows, compact triage buttons.
- Personal views (My Work/Home/Inbox/Drafts/Archived/Projects/
  Workspaces): stat bands, dense rows, token empty states.
- Admin views (Analytics/Overview/Activity/Settings/Profile/Admin/
  ApiTokens/Login/Onboarding): hairline cards, 12px/600 headers, dark
  input ramp; WorkspaceSettings moved to shared PageHeader.
- Backend micro-gaps filled: `?include_project_structure=1` on issue
  detail (backward-compatible), tri-state `release_id` PATCH.

**Cycle 20 — Data views, finished**
- Fixed the genuinely time-of-day-flaky `TestScheduledDueSoonCustomWindow`
  (C20T0): `matchScheduledIssues` used a strict `<` upper bound, missing
  targets landing on the same calendar date as `now + window`; now
  inclusive bound, proven deterministic with a time-of-day sweep test.
- Calendar: week view alongside month (C20T1) — Month/Week segmented
  switcher, 7 day-columns (Mon–Sun), all-day issue chips, today highlight,
  prev/next-week navigation + Today jump.
- Spreadsheet: Cycle / Module / Release columns (C20T2) — 13px density
  chips, sortable, empty cells sort last; membership wired from existing
  queries, no new backend surface.
- Spreadsheet: inline cell editing (C20T3) — state/priority dropdowns,
  start/target date pickers, assignees multi-select popover; commits via
  existing `PATCH .../issues/{uuid}` + assignee relation endpoints
  (zero new backend surface); optimistic update with error rollback;
  Esc cancels, Enter/blur commits; row navigation suppressed while editing.

**Cycle 25 — Triage & issue intelligence**
- AI issue summary (C25T0, better-than-Plane): `POST /api/v1/ai/summarize`
  `{workspace_slug, project_identifier, issue_uuid}` → `{summary}` —
  TL;DR from title + TipTap description + last 20 comments + last 20
  activity rows (What / Why / Current status / Open questions, ≤200
  words), input capped at 12k chars. Follows the ai_handler contract
  (503 `ai_not_configured`, 502 provider failure, member 15+ tenancy —
  guests get 404, provider never called for outsiders). UI: "Summarize"
  button below the description (detail + peek) with inline card +
  regenerate, per-issue cache, honest disabled state when unconfigured.
- AI sub-issue breakdown (C25T1, better-than-Plane): `POST
  /api/v1/ai/subissues` `{workspace_slug, project_identifier,
  issue_uuid, max?}` → `{suggestions: [{title, description?}]}` —
  defensive JSON parsing (code-fence strip, empty-title drop, clamp
  1..10, malformed → honest 502). UI: "Break down" button → dialog
  with checkboxes (all checked, titles editable) → creates selected via
  the existing sub-issue endpoint; partial-success-aware toast.
- Intake bulk triage (C25T2): `POST .../intake/bulk-triage`
  `{intake_ids, action: "accept"|"reject", assignee_id?}` → per-item
  `{accepted, rejected, skipped, errors}` (buckets partition input
  order) — reuses per-item triage logic, one tx per item, non-leaking
  error messages, assignee validated up-front (accept only). UI:
  checkboxes + bulk bar on the Intake page (Accept/Reject/Assign…).
- Duplicate merge (C25T3): `POST .../issues/{uuid}/merge`
  `{into_uuid}` — one transaction moves comments (threads intact),
  attachment metadata, links (rewritten, self-loops/dupes dropped),
  reactions/votes/subscribers (target dupes dropped), time entries
  (colliding running timer stopped, never dropped). Source closes into
  the project's first cancelled state; a `'duplicate'` row in
  `issue_relations` records the survivor (doubles as idempotency key —
  re-merge → `already_merged`); `_merged` activity both sides, version
  snapshot, webhook + WS announces. Guards: self/cross-project/
  descendant → 400, guest → 403, no cancelled state → 409. UI: "Mark as
  duplicate…" in the … menu with similar-issues picker → toast with
  "View ENG-45". Completes the duplicate-suggestions feature.

**Cycle 26 — Calendar scheduling (better-than-Plane)**
- Drag-reschedule on month + week views (C26T0/C26T1): dnd-kit foundation
  (`ScheduleDnd`, `DraggableIssueChip`, `DroppableDayCell`,
  `DroppableWeekColumn`) mirroring the board's drag language
  (PointerSensor + KeyboardSensor, distance-6 activation so click-to-peek
  still works, drop wash highlight, DragOverlay ghost, aria-live
  announcements). Move semantics are duration-preserving — the drop delta
  shifts whichever of `start_date`/`target_date` are set, so the server's
  start≤target invariant can never break; same-day drops are no-ops.
  PATCHes the existing `.../issues/{uuid}` endpoint with optimistic
  react-query updates, rollback + error toast on failure. Member (15+)
  only; guests get static chips.
- Unscheduled strip scheduling (C26T2): the "Unscheduled (N)" strip is now
  a drop zone (always rendered for members, live count, empty-state hint)
  — drag dated chips onto it to unschedule (PATCH clears the placing date
  via the tri-state `null`), drag strip chips onto any day to schedule
  (sets `target_date`). Zero new backend surface.
- Day-cell quick-add (C26T3): hover/`+` affordance on month/week day cells
  (members only) opens the existing create-issue dialog on the issues page
  via the board's `location.state` prefill, with the day as the due date —
  and the create dialog now has Start/Due date fields (Plane parity gap
  closed: dates are settable at create). Created issues invalidate the
  calendar caches so the chip appears on its day.

**Cycle 24 — Workflow depth**
- Move issue between projects (C24T0): `POST .../issues/{uuid}/move`
  `{target_project_identifier}` — one transaction, member (15+) on both
  projects. The issue keeps its UUID (comments, attachments, links,
  reactions, votes, time entries move untouched); fresh display IDs from
  the target project's sequence; whole sub-issue subtree moves
  recursively. State matched by name (case-insensitive) else target
  backlog (`state_remapped`); labels re-linked by name (workspace-scoped);
  estimate matched by value else nulled. Cycle/module/release memberships
  and custom values are project-scoped — dropped and reported. 400 on
  same-project or intake-inbox issues. UI: "Move to project…" in the issue
  … menu with remap preview dialog (landing state, drops) + bulk-bar move
  with partial-success report.
- Convert issue ↔ sub-issue (C24T1, Plane parity): `POST
  .../issues/{uuid}/convert` `{parent_uuid}` — demote/promote with the
  existing cycle-guard, children-block (400), max depth 1, same-project
  only; idempotent no-ops; `_converted` activity entries both sides.
  UI: "Convert to sub-issue…" / "Convert to issue" in the … menu
  (disabled with "Detach sub-issues first" tooltip when it has children).
- Bulk organize (C24T2): bulk bar gains "Add to cycle…" / "Add to module…"
  → existing idempotent membership endpoints; UI-only, no backend.
- Page reactions (C24T3): `POST`/`DELETE .../pages/{pageID}/reactions`,
  embedded `reactions[]` on GET page detail, optimistic toggle UI —
  completes glance's reactions feature (issues + comments already had it).
- Test hygiene (coordinator fixup): `TestGetTimeSummary` time-of-day flake
  fixed (test clock pinned to noon UTC) and documented in FLAKES.md.

**Cycle 23 — Test isolation**
- Per-package test databases (C23T0): new `internal/testdb` — each
  DB-touching test package gets a fresh, private database
  (`glance_test_<pkg>`, drop+create+migrate in TestMain, best-effort
  teardown), killing the whole shared-DB flake class (cross-package row
  interference) and cross-run row accumulation (the suite no longer
  self-slows). Identifier-validated DDL, panics on setup failure
  (no silent skips). Piloted on `internal/mail`.
- Rollout (C23T1+C23T2): api, auth, service, store, ticker, perf all
  migrated; redundant `migrateTestDB` helpers + 438 call sites deleted;
  api's `TRUNCATE rate_limits` dead code removed. The two FLAKES.md
  `suspected` rows (`TestGetWorkspaceActivityAggregates`,
  `TestActivityHTTPLimit`/`TestActivityHTTPFeed`) promoted to FIXED —
  root cause was shared-DB interference, removed by isolation.
  Test files only; zero production-code changes.

**Cycle 22 — Hardening, proven**
- `TestDispatcherSingleFlight` root-caused + fixed (C22T0): a test bug,
  not a prod race — the shared outbox table plus `claimDue`'s 25-oldest
  ordering meant a parallel-suite pass claimed foreign rows while the
  test asserted on its own row (the `started` signal fired on a Send,
  not on *which* row). Assertions now pin the guard contract with
  explicit signals: recorded send count, second `Run` in a goroutine
  with a 3s skip timeout (the old `elapsed > 5s` check could never fire —
  the run would hang, not return slowly), `defer`-closed release so an
  early `t.Fatal` can't wedge the pass. `go vet`/`-race`/5× full-suite
  green; negative path verified (guard disabled → fails in ~3.0s).
- Slowest-test sweep (C22T1): measured the suite (1020+ tests) and found
  zero shippable real-time waits — no `time.Sleep` in test or production
  code in scope; the slowest tests are real work over accumulated
  shared-DB rows. Honest no-op: nothing changed. Noted for a future
  cycle: the suite self-slows as `glance_test` accumulates rows
  (test isolation — per-package DBs or fixture cleanup — is the real fix).
- Flake watch codified (C22T2): `qa/flake-sweep.sh` runs the suite N
  times with caching off, reports per-run wall times + failing tests,
  exits non-zero on any failure (anchored to repo root); `docs/FLAKES.md`
  is the durable register — `TestFindSimilarIssuesPerf` FIXED (C21),
  `TestDispatcherSingleFlight` FIXED (C22), two new `suspected` rows
  (`TestGetWorkspaceActivityAggregates` timestamp ordering under load,
  `TestActivityHTTPLimit`/`TestActivityHTTPFeed` intermittent 401s);
  standing rule: no cycle closes with an undocumented known flake.

**Cycle 21 — Verification, durable**
- Durable visual QA harness (C21T0): the cycle-19 screenshot harness
  (`qa/visual.sh` + Playwright scripts) promoted into the repo —
  `qa/README.md` docs, `qa/package.json`/`package-lock.json` pinned,
  secrets scrubbed to `GLANCE_QA_DB_*` env vars, `qa/shots/` gitignored;
  end-to-end proven from repo root.
- `TestFindSimilarIssuesPerf` root-caused + fixed (C21T1): the planner
  flattened the `similarity()` call into the `ORDER BY ... LIMIT` level,
  evaluating trigram ops 3×/row instead of 2; a `MATERIALIZED` CTE fences
  it to one eval per row, narrow `(id, sim)` ranking + PK fetch of the
  final 5 — ~27% faster, ~3.4× headroom under the 50ms budget. No
  migration, no API change.
- Batched project structure on issue lists (C21T2): `?include_project_structure=1`
  now supported on the issue list endpoint — cycle/module/release
  membership resolved in ONE server-side query per list call (same shape
  as the detail attachment; off by default, byte-identical responses).
  The spreadsheet drops its per-row detail-fetch N+1; columns render
  identically.

### v0.2.0 content (never shipped separately — included in v0.3.0)

"Credibility" milestone: the highest value-per-cost Plane parity gaps,
plus reliability and production-readiness hardening.

#### Added

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

#### Fixed

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

#### Security

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
