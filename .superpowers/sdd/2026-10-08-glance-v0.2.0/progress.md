# glance v0.2.0 — SDD ledger (cycle 1)

Started 2026-10-08. Autonomous dev loop: audit → Plane compare → plan milestone → implement (subagent-driven dev + adversarial review) → QA gate → push main → report.

> Note: the parity analyst section below was written first; the auditor appends their own sections after it. Do not reorder — append only.

## Plane parity gaps

Source: Plane cloud exploration 2026-10-06 (15 screenshots in `docs/reference/`, REFERENCE.md gap analyses), README vs-Plane table, spec §§10–11, plus verification against glance `web/src` and migrations (2026-10-08). Ranked by user value × implementation cost, cheap high-value first. Sizes: S = hours–1 day, M = 2–5 days, L = 1–3 weeks of focused subagent-driven work. No live browser was available to the analyst; items needing it are marked [NEEDS-LIVE-CHECK].

### Must-have for credibility (hit in the first 5 minutes)

1. **Dark theme toggle** (S) — Plane is dark-first; glance is light-only with no toggle (only unused shadcn `dark:` variants in CSS). Every evaluator's first screenshot comparison is dark. Wire class-based dark mode + a toggle in the top bar / settings.
2. **Inline "+ New work item" quick-add** (S) — Plane has per-state-group inline add rows on list and per-column on kanban; glance always opens the full create form. Fastest issue creation path; core to the "Linear-grade" feel.
3. **Guided onboarding + workspace creation wizard** (M) — Plane: 6-step flow (profile → role → goals → workspace name + editable URL slug + team size → invite teammates). glance drops a new login onto an empty workspaces page. Combine into one wizard: welcome → create workspace (name + slug) → invite.
4. **Home dashboard** (M) — Plane home: greeting + date, Ask-AI widget, Quicklinks, Recents, Stickies, widget grid where every empty state has an inline Add action. glance's `Home.tsx` is an unrouted placeholder. A simple dashboard (your work, recents, quicklinks) makes the app feel alive on first login.
5. **Spreadsheet view** (M) — Plane's view switcher: list / kanban / calendar / spreadsheet / gantt; glance has list + board only. Spreadsheet (full-field table, sorting, horizontal scroll) is the cheapest missing view and the triage workhorse. Spec §11 already lists it.
6. **Display properties panel** (M) — Plane's non-modal dropdown: toggle 13 card fields, Group by / Sub-group by / Order by, "Show sub-work items", "Show empty groups", live apply. glance's board/list layouts are fixed. High perceived-customization win.
7. **Peek view (right-drawer issue detail)** (M) — Plane: clicking a row opens a right drawer (same URL), full-page view only on demand. glance navigates to a full page every time. Drawer keeps list context; big UX-feel upgrade for triage.

### Important

8. **Saved views** (M) — Plane nav has Views: named saved filter/group/display presets per project. glance has "issue views (saved filters)" on the README roadmap, not built. Natural follow-on to #6.
9. **Richer filters** (M) — Plane's filter panel: ~19 dimensions plus PQL code mode (AI filters optional — skip; conflicts with lean thesis). glance has basic list filters. Needed before saved views are useful.
10. **Modules (epics)** (L, full-stack) — Plane's planning hierarchy: project → module → issues, with module list + detail + progress. glance has zero modules backend (no migration, no routes) and no UI. Spec §11 lists modules UI post-v1; this is the real thing.
11. **Wiki / Pages block editor** (L) — Plane: block editor (text, lists, quotes, code, tables), Collections (General/Shared/Private/Archives), Publish, starter templates. glance has nothing. README roadmap #1. Largest single credibility item after views.
12. **Workspace settings UI** (M) — Plane settings: General (name, slug, timezone, logo), members, Imports/Exports, Worklogs, Features toggles, Developers/Webhooks. glance has **no settings UI at all** (only APIs). Minimum: general + members + webhooks UI (webhooks API exists, no UI).
13. **Calendar view** (M) — date-based planning on start/due dates; spec §11 roadmap. Cheaper than gantt, visible in the view switcher.
14. **Time tracking / worklogs** (M) — Plane issue detail shows "Tracked time" + a Worklogs tab. glance has none. Pairs with the settings Worklogs section (#12).
15. **Create-flow density** (S) — Plane's create modal: all property chips + parent picker + "Create more" toggle + save toast (Copy link / View). glance lacks "create more" and needs its form compared field-by-field [NEEDS-LIVE-CHECK for exact modal parity].
16. **Cycle burndown chart** (S) — Plane's active-cycle view has progress header + burndown; glance cycles have progress tracking + rollover but no charts. Small, high-signal for the "sprints" story.
17. **Issue-detail extras** (S) — Plane: Subscribe, upvote/downvote, copy-link, full activity tabs. glance has comments/relations/history. Subscribe + activity polish are cheap.

### Nice-to-have

18. **Gantt / timeline view** (L) — spec explicitly calls it "custom build, large work item". Defer until calendar + spreadsheet land.
19. **Analytics** (M/L) — Plane's Analytics button on views; glance none. Needs event/aggregation story first.
20. **Releases** (M) — Plane tracks releases per project; glance has no concept. [NEEDS-LIVE-CHECK: batch 3 (releases page) was never explored.]
21. **Importers — GitHub / Jira** (M/L) — README roadmap; classic adoption unlock, but only after core parity.
22. **Public share boards** (M) — README roadmap; needs token-scoped public routes (auth design required).
23. **Admin panel** (M) — README roadmap; instance-level user/workspace management.
24. **Attachments** (M) — README roadmap; needs object storage story (S3-compatible or local disk decision).
25. **Inbox view polish** (S) — glance has in-app notifications; Plane's inbox/notifications view wasn't explored [NEEDS-LIVE-CHECK] — compare before building.
26. **AI assistant equivalent** (L) — Plane leans on Plane AI (ask widget, AI filters, page summaries). Deliberately deprioritized: conflicts with the lean/self-host thesis; revisit only on real demand.

### Explicitly out of scope for parity (keep the lean thesis)

- SSO/SAML enterprise auth, audit logs, advanced governance — enterprise tier concerns, not the rebuild goal.
- Multi-region / horizontal scaling (spec §11.4) — scaling story, not parity.

---

## Cycle 1 audit findings (auditor, 2026-10-08 ~10:35–11:00 WITA)

Audit only — no code changes, no pushes. Sources: spec `spec/2026-10-06-glance-design-spec.md`,
v1 ledger `.superpowers/sdd/2026-10-06-glance-v1/progress.md` (R1–R11), live repo @ `main` (582446d).

### Environment notes
- VM was wiped again (PG16 cluster gone). Current: **PostgreSQL 18.6**, roles `omon`/`postgres` (QA stack).
  Recreated per runbook: role `glance` (superuser, password `glance`), database `glance_test`.
- Go 1.27.1, `GOTOOLCHAIN=local`, `GOCACHE`/`GOTMPDIR` under workspace. No Docker (unchanged).

### Git state
- Working tree **clean**, `main` in sync with `origin/main` (galihvsx/glance). No drift, no uncommitted work.
- HEAD = `582446d` "docs: annotate spec §5 unbuilt endpoints as planned, NOT in v0.1.0" (2026-10-08).
  → The v1 "spec §5 overclaim" pending decision is now resolved **in the spec itself**.

### Test results
- `go test -p 1 -count=1 ./...` with REAL `glance_test` DB: **ALL GREEN**
  (api, auth, config, mail, perf, realtime, service, store, ticker).
- **Zero skipped tests** (no `--- SKIP` in verbose output).
- `go vet ./...`: clean. `gofmt -l`: clean.
- ⚠️ **Default `go test ./...` (parallel packages) FAILS**: 3 tests in `internal/auth`
  (`TestOAuthCallbackLinksAccount`, `TestOAuthUnverifiedEmailRejected`, `TestOAuthUnconfiguredProvider`)
  fail at `migrateTestDB` with `duplicate key value violates unique constraint "pg_type_typname_nsp_index"`
  during migrations 000008/000010/000013. Root cause: **concurrent `Migrate` with no advisory lock**
  (known R3 future-note, now reproduced). Two packages both see a migration as pending and both run it;
  `CREATE TABLE` creates a composite pg_type rowtype, and the loser's `IF NOT EXISTS` guard doesn't
  protect against a concurrent creator → pg_type collision. Same class as the v1-day "unreproduced FAIL"
  (Task 9 review flake note). Not a product bug — test-infra race — but it red-flakes CI.

### Spec compliance sweep (§5)
Present and working: OTP request/verify, OAuth login/callback (google+github), logout, /me,
ws/ticket, workspaces CRUD + members, projects CRUD + states, issues CRUD + bulk-update/bulk-delete,
issue detail, comments/reactions, relations, history, versions+restore, labels, assignees, estimates,
votes/subscribers, cycles CRUD + cycle issues, intake + accept/reject/snooze/duplicate,
notifications + prefs, webhooks, API tokens, cursor pagination, `?updated_after=` delta sync,
`?fields=description` sparse fieldsets, `?state=&assignee=&label=&priority=&cycle=&q=` filtering,
`{error:{code,message,details}}` envelope (R10 retrofit), `Idempotency-Key` (T17), `/ws` with
workspace-qualified `project:{slug}:{identifier}` channels (R11).
- **Known gaps — confirmed absent, NOT regressions** (spec now annotated):
  `GET /api/v1/work-items/{display-id}`, `GET /api/v1/search?q=`.
- **§5 lists but unbuilt** (tables exist, no handlers; §10 defers them post-v1): `.../modules`,
  `.../pages`, `.../views` — feed the backlog below, not spec violations.

### Security pass (quick)
- ✅ OTP budget-squatting fix verified in place (`internal/auth/otp.go:293` — `verify:email:`
  consumed only after a live code row is locked; `verify:ip:` pre-check intact; comments document
  the invariant).
- ✅ OAuth uid-first lookup (T8).
- ✅ WS channel authz (`internal/api/ws_handler.go:161` — `user:{id}` self-only;
  workspace/project/issue channels require membership; R11 scheme).
- ✅ SSRF guard on webhook delivery (`internal/service/webhook.go:634`+).
- ✅ HTTP project↔workspace binding (`resolveIssueProject`, `internal/service/issue.go:183` —
  project must belong to the slugged workspace AND actor must be a member; no cross-workspace IDOR).
- ⚠️ **Latent:** `AuthenticateSession` (`internal/auth/session.go:99-127`) selects `users.is_active`
  but never enforces it — a deactivated user keeps valid sessions. No deactivation feature exists yet,
  so not exploitable today; known T9 deferred minor, still open.
- Note: `c.RealIP()` trusts `X-Forwarded-For` (T6 minor) — bounds the T28 `(email,IP)` rate-limit
  re-keying; per-IP accounting spoofable when directly exposed. Victim availability still holds.

## Candidate v0.2.0 backlog — bugs & tech debt (auditor)

Each with 1-line why. (Parity/feature items live in the analyst's section above.)

**Bugs**
1. Parallel `go test ./...` migration race → flaky red (R3). Why: CI reliability; add advisory lock or serialize migrate in tests.
2. `is_active` not enforced on session auth (`internal/auth/session.go`). Why: latent privilege hole once deactivation ships.
3. `ws.smoke.test.ts` leaves scratch rows in `glance_test` (T25 minor). Why: test pollution across runs.
4. `per_page > 100` silently clamped while 400 says "want 1-100" (T15 minor). Why: clamp-vs-reject undocumented; pick one.

**Missing spec items (backend)**
5. `GET /api/v1/work-items/{display-id}`. Why: command palette + deep links need display-ID lookup (T25 noted the 500-issue scan cap).
6. `GET /api/v1/search?q=` global search. Why: core discoverability; generated tsvector column already exists, only the endpoint is missing.

**Tech debt / hardening**
7. Last-admin removal race (T11 minor) — concurrent removals can zero out admins. Why: workspace lockout risk; fix with `SELECT … FOR UPDATE`.
8. Snooze-expiry ticker missing (T20 minor) — spec §3 requires background ticker; currently read-time resurfacing only. Why: spec deviation.
9. Empty `OTP_PEPPER` only warns (R7). Why: silent weak-hash deploy footgun; fail fast in production.
10. XFF trusted for rate limits (T6 minor). Why: weakens T28 re-keying; trust proxy headers only from configured CIDRs.
11. Board/list/inbox show archived issues — no `archived=` filter (T15/T23 deferred). Why: archived clutter in every list view.
12. Comment POST not covered by `Idempotency-Key` (T18 minor). Why: retried comment POST double-posts; inconsistent with issue mutations.
13. Webhook dispatcher follows redirects (T28 minor). Why: SSRF-adjacent; webhooks shouldn't follow redirects.
14. Dispatcher/ticker use `context.Background()`, never cancelled (T10 minor). Why: unclean shutdown; wire to server shutdown ctx.
15. Dead code: unwired `Home.tsx`, dead `web/public/icons.svg`, stock Vite `web/README.md` (T3/T13/T19). Why: public-repo tree hygiene.
16. `ListSnoozedIntakeIssues` has no Go test (T21 follow-up). Why: only curl-verified; partitioning logic unpinned.
17. Docker/compose never validated on a real Docker host (R5). Why: documented deploy path is unverified.

## Auditor's top-5 recommendation for cycle 1 implementation
1. Parallel-test migration race (bug, CI reliability).
2. `GET /api/v1/work-items/{display-id}` (spec gap, unblocks palette/deep links).
3. `GET /api/v1/search?q=` (spec gap, FTS infra already in place).
4. `is_active` session enforcement (latent auth hole).
5. Modules endpoints (largest parity gap; complements analyst item #10 — note: analyst found no
   modules migration/routes at all, so this is full-stack: migration + routes + service, not just handlers).

## v0.2.0 plan — "Credibility" (PM + architect, 2026-10-08)

Goal: close the highest value×cost gaps so a first-time evaluator sees Plane parity in the first 5 minutes. Continuous loop: cycle N+1 starts automatically after cycle N's report; never ask to continue.

### Scope (cycle 1)
**Wave 1 — backend + quick frontend wins (parallel):**
- T1: Fix parallel-test migration race (PG advisory lock around Migrate in test helper). Why: CI reliability.
- T2: Spec-gap endpoints: `GET /api/v1/work-items/{display-id}` (ENG-123 style, project-scoped sequence; membership-checked, 404 otherwise) + `GET /api/v1/search?q=` (global tsvector search, workspace-membership scoped, cursor pagination). Why: closes spec §5 gaps; unblocks palette/deep links.
- T3: Dark theme toggle (class-based, topbar toggle, localStorage-persisted, **default dark** — Plane-first impression, matches HiFi slices).
- T4: Inline "+ New work item" quick-add (per-group list rows + per-column kanban; Enter creates, Esc cancels; full form still reachable).
- (Jarvis direct): `is_active` session enforcement — 5-line fix.

**Wave 2 — views (after wave 1 merges):**
- T5: Spreadsheet view (full-field table, sorting, horizontal scroll).
- T6: Peek view (right-drawer issue detail, same URL, list context preserved).
- T7: Display properties panel (toggle card fields, group/sub-group/order by, live apply).

**Deferred to cycle 2+:** onboarding wizard, home dashboard, saved views, richer filters, modules (full-stack L), wiki/pages (L), workspace settings UI, calendar view, worklogs; tech-debt items 3,4,7–17 from auditor backlog.

### Architecture decisions
- Display-ID: `{PROJECT_IDENTIFIER}-{SEQ}`, sequence per project; backfill existing issues in migration.
- Dark default: class strategy on `<html>`, `.dark` CSS vars already present; respect persisted choice > default dark.
- TDD mandatory, real PG, no silent skips. Branch per task: `cycle1/t<n>-<slug>`; push branch; Jarvis reviews diff before merge to main.

### Completed (2026-10-08)
- **T0: is_active enforcement** (Jarvis direct, merged to main as d2953fd): `AuthenticateSession` now rejects deactivated users as `ErrSessionInvalid` (indistinguishable); OTP + OAuth login refuse to mint sessions (`ErrUserDeactivated` → 401 "account is deactivated"). TDD: `internal/auth/isactive_test.go` (2 tests). vet/gofmt clean, auth+api packages green.
- **T4: inline quick-add** (merged to main): new `QuickAdd.tsx` (idle button → inline input; Enter creates, Esc cancels, expand → full form); list view groups by state with per-group QuickAdd; kanban columns get per-column QuickAdd (guest-disabled). Review: API contract `{name, state_id}` verified; query invalidation via prefix match; no scope conflicts. lint/build/tests pass.
- **T3: dark theme** (merged to main as 805ced8): class-based dark mode, `use-theme` hook + `ThemeToggle` in page headers, pre-React bootstrap in index.html (no FOUC, default dark), `color-scheme` adaptations. T3 self-isolated to /tmp worktree after the shared-tree collision; parent integrated via ~/workspace/t3-dark-theme-deliverable/ (11 files) + manual toggle hunks onto post-T4 Issues/Board. Build passes.
- **T4: inline quick-add** (merged to main as 8e07168): reviewed clean (API contract, query-key prefix invalidation, scope check on duplicate useNavigate).
- **Process incident (2026-10-08)**: 4 parallel implementers shared one working tree → staged files landed on main, one merge failed. Recovery: git freeze on T1/T2/T3 ("no git commands, parent handles git"), T3 self-extracted to isolated worktree, parent did patch-level integration. Lesson recorded in ~/AGENTS.md: one worktree per implementer going forward.
- **T1: migration race fix** (merged to main as 4bfdb2d): `Migrate()` now runs in a single transaction guarded by `pg_advisory_xact_lock` (FNV-1a key of "glance/schema_migrations"); helpers take a `dbtx` interface; exported signature unchanged. New `TestMigrateConcurrent` regression test (8 goroutines, scratch DB, refuses to skip). Review: clean; no CONCURRENTLY in migrations so single-tx is safe. Known pre-existing flake noted by T1: `TestDispatcherSingleFlight` (internal/mail) ~1/7 parallel runs — separate test-robustness task for a later cycle.
- **T2: spec-gap endpoints** (merged to main): `GET /api/v1/work-items/{display-id}` (last-dash parse, identifier normalization, membership-scoped, ambiguous → single 404, no enumeration) + `GET /api/v1/search?q=` (tsvector, ts_rank, workspace-scoped, keyset cursor). Pure refactor of `getIssueRow` out of `GetIssue`. Review: clean; no migration needed (sequence_id/tsvector pre-existed). vet/gofmt/tests green.
- **Wave 1 complete**: T0 (is_active) + T1 (migration race) + T2 (spec-gap endpoints) + T3 (dark theme) + T4 (inline quick-add) all merged to main.
- **T5: spreadsheet view** (merged to main): new `Spreadsheet.tsx` (10 columns, sortable headers, sticky header+ID col, horizontal scroll), route + "Spreadsheet" tab in ProjectNav. Reuses list API + query key; row click → detail. Review: additive only, build/lint pass.
- **T5: spreadsheet view** (merged to main as 7e091de): new `Spreadsheet.tsx` + route + nav tab. Additive only.
- **T6: peek drawer** (merged to main as b9e013a): extracted `IssueDetailContent.tsx` (reusable), new `PeekDrawer.tsx` (shadcn Sheet) + `usePeek.ts` (`?peek=<uuid>` query param, `replace:true`); row/card/Enter opens drawer, full-page route intact, focus trap + Esc. Auto-merged clean vs T5 (no overlapping files). Build passes; QuickAdd + peek coexist verified in Issues.tsx.

## Production-ready track (started 2026-10-08, in parallel with wave 2)
- **Dockerfile/HEALTHCHECK** (merged to main as d78c060): `--health-check` flag on the binary (probes 127.0.0.1:$PORT/health, exit 0 only on 200 + `"status":"ok"` body — a bare 200 is distrusted after finding the omon QA API squatting :8080 in dev); runs before config load so it needs no OTP_PEPPER/DB. Dockerfile `HEALTHCHECK CMD ["/glance", "--health-check"]`. Verified live: exit 1 with nothing listening, exit 0 against a booted server. compose verified by reading: fail-fast OTP_PEPPER, postgres healthcheck dependency, sane defaults. `docker build` itself still needs a Docker-capable host (blocked, unchanged).
- Remaining for the track: CHANGELOG.md, version bump to v0.2.0, final QA gate, push, release draft.
- **T7: display properties panel** (merged to main as d6309e9): `useDisplaySettings` hook (localStorage per project) + `DisplayPanel` component (field toggles, group-by, order-by, hide-empty). Conflicts vs T5/T6 in IssueCard/Board/Issues resolved by keeping BOTH prop families (onOpen for peek + fields for display); tsc + vite build clean.

## Cycle 1 complete — 2026-10-08 ~11:10 WITA
- **QA gate: FULL PASS.** gofmt clean (85 files), go vet clean, `go test ./...` all 9 packages ok (no flakes this run), govulncheck clean after x/text bump v0.40.0→v0.41.0 (GO-2026-6629, non-exploitable, one-line fix), tsc + vite build clean, smoke test (--health-check exit 1 empty / 0 live) pass. Security re-check pass: is_active enforcement (session+OTP+OAuth), T2 endpoints membership-scoped, healthcheck localhost-only.
- **Pushed:** main 582446d → 668a757 on galihvsx/glance.
- Release v0.2.0 GitHub Release NOT published — checkpoint, waiting for galih's tap.
- **Cycle 2 starts automatically** (next tier of the 26-gap backlog).

## Cycle 2 kicked off — 2026-10-09 ~08:08 +08
- Status when galih asked: cycle 1 done + pushed + QA-passed (2026-10-08); cycle 2 had NOT auto-started (no live session to continue it — loop needs a running session). Kicked off manually on his "udah sampe mana" ping.
- Wave 1 dispatched: C2T0 onboarding wizard + C2T1 home dashboard (parallel, isolated worktrees, branches cycle2/c2t0-onboarding + cycle2/c2t1-dashboard). Cleaned up cycle-1 worktrees (t5/t6/t7).
- v0.2.0 GitHub Release still unpublished — galih checkpoint, orthogonal to cycle 2.
- **C2T0: onboarding wizard** (merged to main as c0b948f): `Onboarding.tsx` 3-step wizard (welcome → create workspace → invite), `useWorkspaces` shared hook, ProtectedRoute funnels zero-workspace users to `/onboarding` (fail-open on error), `Home.tsx` placeholder deleted. Invite step stores emails as localStorage "Pending" with honest copy (no invite-by-email endpoint exists). tsc + build clean post-merge.
- **Follow-up (later cycle):** backend `POST /api/v1/workspaces/{slug}/invites` (email → user lookup + invite) to make onboarding step 3 real.
- **Env change 2026-10-09:** VM wiped again overnight; PG is now **16.15** (was 18). Role `glance` + `glance_test` recreated per runbook. Smoke-test residue in glance_test: user `c2t0-new@example.com` + workspace `acme-inc` (harmless).
- **C2T1: home dashboard** (merged to main as 392af03): `Home.tsx` resurrected as dashboard (greeting+date, Your work aggregation max 10 projects, recents via `recents.ts` localStorage store recorded on every issue open incl. peek drawer, quick links, empty-state actions). `/` → dashboard, `/w` → workspace list (Projects back-link updated). Merge conflict vs C2T0 (Home.tsx delete/modify + App.tsx routes) resolved keeping both. tsc + build + 45 tests pass.
- **Cycle 2 wave 1 complete** (C2T0 + C2T1). Dispatching wave 2: C2T2, C2T3, C2T4.
- **Wave-2 setup notes (2026-10-09):** C2T3's initial `git worktree add` failed transiently (retry succeeded — worktree healthy at 392af03). C2T4's worktree landed on stale main 668a757 (pre-C2T0/C2T1); messaged the agent to rebase onto 392af03 while its tree is still clean.
- **C2T3: issue-detail extras** (merged to main as 9f5870b): subscribe/unsubscribe (existing backend .../subscribers endpoints, optimistic + rollback), copy-link button (clipboard fallback), unified Activity timeline (comments + history interleaved newest-first, relative timestamps). Auto-merged clean (only IssueDetailContent.tsx + types.ts). tsc + build clean post-merge.
- **C2T2: denser create flow** (merged to main as 41b902e): `ParentPicker.tsx` (searchable popover over project issues, sets parent_id), create dialog gains Parent field + "Create more" checkbox (keeps dialog open, resets form), success toast with display_id + Copy link / View (opens peek drawer). `<Toaster />` mounted in App.tsx (infra existed, was never mounted). Auto-merged clean. tsc + build clean post-merge.
- **C2T4: cycle burndown** (merged to main as a05a870): new backend `GET .../cycles/{cycleID}/burndown` (read-only, member-scoped via resolveCycleProject; reconstructed from issue_activities audit log; 3 Go tests), hand-rolled SVG `BurndownChart.tsx` (theme-aware, no chart lib), `CycleDetail` header cards (open/in-progress/done) + burndown section with empty states. Justification for server-side: N+1 via per-issue /history. Merged clean (types.ts auto-merged vs C2T3's Subscriber). 3 new Go tests + tsc + build green post-merge.
- **Cycle 2 wave 2 complete** (C2T2 + C2T3 + C2T4). Dispatching wave 3: C2T5 (strictly before C2T6).
- **C2T5: richer filters** (merged to main as 01e03f9): backend `ListIssuesInput` extended (multi priority/label/assignee incl. `none` sentinel, estimate, created/updated/due ranges, subscribed) — all parameterized SQL, malformed → 400; frontend `filters.ts` (URL-serializable schema documented in module header — the C2T6 contract) + shared `FilterPanel`; Issues/Board/Spreadsheet migrated (~150 lines dup removed per page). Merged clean. service+api tests, tsc, build green post-merge.
- **C2T6: saved views** (merged to main as 834b09e): `useSavedViews.ts` (localStorage per project, CRUD + default, sanitize on load, 50 cap) + `SavedViewsMenu.tsx` (apply/rename/delete/default, inline validation) mounted in ProjectNav trailing slot on Issues/Board/Spreadsheet. Storage = localStorage (no backend prefs store exists; documented). Merged clean. tsc + build + 67 tests green post-merge.
- **Cycle 2 wave 3 complete** (C2T5 → C2T6). Dispatching wave 4: C2T7 + C2T8.
- **C2T7: bug bundle A** (merged to main as fd17620): snooze-expiry ticker (`internal/ticker/snooze.go`, 1-min interval, idempotent UPDATE + intake.updated fan-out, wired in main.go), `archived=` filter (backend ListIssuesInput.Archived + intake inbox, default excludes; frontend FilterPanel "Show archived" checkbox + URL round-trip), tests (TestListSnoozedIntakeIssues, TestListIssuesArchivedFilter, TestSnoozeTickerExpiresRows — all 20× green). Intended behavior change: list + inbox now exclude archived by default. ticker+service tests, tsc green post-merge.
- **Worktree lesson (2026-10-09):** `git worktree add <path> main` FAILS when main is checked out in another worktree ("already used"). Correct: `git worktree add --detach <path> main && git checkout -b <branch>` or `git worktree add -b <branch> <path> main`. This was the transient failure C2T3/C2T4 hit.
- **C2T8: bug bundle B** (merged to main as be26a1d): OTP_PEPPER prod fail-fast (`APP_ENV=production` ignores the ALLOW_INSECURE hatch), comment POST idempotency (withIdempotency + test), webhook no-redirect (both HTTP clients + tests), XFF trust only from TRUSTED_PROXY_CIDRS (default trust none), graceful shutdown (signal.NotifyContext + http.Server.Shutdown; snooze ticker from C2T7 rides the same ctx), per_page REJECT (400) consistently across list endpoints (service clamp stays as defense-in-depth). Merged clean. Build green.
- **Toolchain:** go1.27.1 → go1.27.2 (GO-2026-6617 stdlib HPACK race); govulncheck clean after.
- **Cycle 2 wave 4 complete** (C2T7 + C2T8). Running the cycle-2 QA gate.

## Cycle 2 complete — 2026-10-09 ~09:00 +08
- **All 9 tasks merged** (C2T0–C2T8): onboarding wizard, home dashboard, denser create flow, detail extras, burndown, richer filters, saved views, bug bundle A, bug bundle B.
- **QA gate: FULL PASS** (go1.27.2): gofmt clean, vet clean, all 9 Go packages ok, govulncheck clean, tsc+build clean, 67 vitest passed, smoke test pass (health-check + burndown route 401-unauth). Perf: list p95 892µs (budget 3.5ms), no regression.
- **Security re-check:** burndown endpoint member-scoped, filter SQL parameterized, XFF trust explicit, idempotency wrapper sound.
- **Pushed:** main 668a757 → 253f6d2 on galihvsx/glance.
- v0.2.0 GitHub Release still unpublished — galih checkpoint.
- **Cycle 3 starts automatically** (per plan: modules (L), wiki/pages (L), calendar (M), workspace settings UI (M), time tracking (M)).

## Cycle 3 kicked off — 2026-10-09 ~09:50 +08
- Cycle-2 session ended without auto-starting cycle 3; PM+architect wrote the plan now. Plan: `.superpowers/sdd/2026-10-09-glance-cycle3/cycle3-plan.md`; this cycle's ledger: `.superpowers/sdd/2026-10-09-glance-cycle3/progress.md`.
- 9 tasks C3T0–C3T8: invites endpoint, modules backend+frontend, wiki/pages backend+frontend, calendar view, workspace settings UI, time tracking, bug bundle C. NOT in cycle 3: gantt (cycle 4), analytics, releases, importers, public share, admin panel, attachments, AI.
- v0.2.0 GitHub Release still unpublished — galih checkpoint, orthogonal to cycle 3.
