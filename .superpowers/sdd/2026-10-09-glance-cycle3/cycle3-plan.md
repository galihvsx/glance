# Cycle 3 plan — "Depth" (2026-10-09)

Standing instruction: **never ask whether to continue** — cycle 3 starts automatically after the cycle-2 report. Cycle 2 is merged to `main` (253f6d2) and pushed to galihvsx/glance; QA gate FULL PASS (go1.27.2: gofmt/vet/tests×9pkgs/govulncheck/tsc/build/vitest 67 tests/smoke all green; perf list p95 892µs, no regression). The v0.2.0 GitHub Release publish is a galih checkpoint and is **orthogonal** to this cycle — it does not block any task below.

Goal: climb the next tier of the parity backlog — modules (full-stack), wiki/pages (full-stack), calendar view, workspace settings UI, time tracking — plus the queued onboarding follow-up (invites endpoint). Cycle 4 takes on gantt (calendar is now the prerequisite).

Explicitly NOT in cycle 3: gantt (needs calendar first), analytics, releases, importers, public share, admin panel, attachments, AI.

Performance budget (non-regression from v0.1.0/v0.2.0): list p95 ≤ 3.5ms, cold start ≤ 0.1s, RSS ≤ 15MB. Any task that moves these numbers must document why or fix it.

Task numbering: C3T0 … C3T8 (9 tasks, same scale as cycle 2). Each task: one git branch `cycle3/c3t<N>-<slug>`, one isolated worktree (`git worktree add -b <branch> <path> main` — never plain `add <path> main` when main is checked out elsewhere), one implementer, one adversarial reviewer, push branch (NOT merge — parent merges), merge to main only when review + task QA pass.

## Tasks

### C3T0 — Workspace invites endpoint (S)
Source: cycle-2 C2T0 follow-up. Onboarding step 3 currently stores invite emails as localStorage "Pending" with honest copy — no backend endpoint exists.
Build: `POST /api/v1/workspaces/{slug}/invites` — member-scoped, accepts email list, looks up users by email, adds as workspace members (default role), returns per-email status (invited / already-member / not-registered). Wire onboarding step 3 to call it (graceful fallback to "Pending" copy for not-registered emails).
Accept: registered user added as member; unknown email → honest pending state, no 500; admin-only; existing onboarding flow unchanged for zero-workspace users.

### C3T1 — Modules backend (M)
Source: backlog important #10 / auditor top-5 #5. Analyst confirmed: no modules migration/routes at all — full-stack, not handlers-only.
Build: migration `000017_modules` (modules table: project-scoped, name, description, status, start/target dates, lead; module_members or issue assignment via `issues.module_id` nullable FK), service + CRUD routes `.../projects/{id}/modules`, add/remove issues to module, delete guard (module with issues → 409 or reassign).
Accept: full CRUD member-scoped; issues assignable; list issues by module; migration up/down clean; Go tests for service layer.

### C3T2 — Modules frontend (M)
Source: backlog important #10. Depends on C3T1 (merge order: T1 first).
Build: "Modules" section in project nav (after Cycles), module list + detail pages (module issues grouped by state, progress header), assign-issue-to-module picker (reuse ParentPicker pattern), module create/edit modal. Reuse existing issue row/card components — no new issue UI.
Accept: create → detail with empty state; assign issues → they show under module; module detail respects display properties where cheap; tsc + build clean.

### C3T3 — Wiki/pages backend (M)
Source: backlog important #11. Same as modules: nothing exists — migration + routes + service.
Build: migration `000018_pages` (pages table: project-scoped, title, content (markdown), parent_id self-FK for hierarchy, position, author; updated_at). CRUD routes `.../projects/{id}/pages`, move (reparent/reorder), simple version history (pages_revisions table, append on update, restore endpoint) — cheap, high value.
Accept: CRUD + hierarchy + reorder member-scoped; revision restore works; Go tests.

### C3T4 — Wiki/pages frontend (M)
Source: backlog important #11. Depends on C3T3 (merge order: T3 first).
Build: "Pages" section in project nav, tree sidebar + page view, markdown editor (textarea-based, lean — no rich-text lib), create/edit/delete/move, revision history drawer. Dark-default theme aware (T3).
Accept: nested pages render; edit → revision recorded; restore works; no new deps.

### C3T5 — Calendar view (M)
Source: backlog important #12. Prerequisite for cycle-4 gantt.
Build: month-grid calendar view per project (new tab in ProjectNav alongside List/Board/Spreadsheet): issues placed by due date (start date if present), click → peek drawer, month navigation, "unscheduled" strip for issues with no dates. Backend: derive from existing list API with date filters if cheap; add dedicated range endpoint only if N+1 appears.
Accept: issues land on correct days; click opens peek; empty month shows honest empty state; no perf regression on list endpoint.

### C3T6 — Workspace settings UI (M)
Source: backlog important #13.
Build: `/w/:slug/settings` page: rename workspace (name + slug with inline validation, reuse onboarding slug logic), members list with roles, change role, remove member (respect existing last-admin guard), delete workspace with typed confirmation. Reuses existing workspaces/members endpoints — verify delete exists, add only if missing.
Accept: rename validates + persists; last member-admin cannot be removed; delete requires confirmation and cascades cleanly; no new migrations expected.

### C3T7 — Time tracking (M)
Source: backlog important #14 (worklogs, deferred from cycle 1).
Build: migration `000019_time_entries` (issue-scoped: user_id, started_at, ended_at nullable, note), endpoints: start/stop/log/list per issue + total per issue, member-scoped. Frontend: timer widget on issue detail (start/stop, manual log entry, running timer persisted client-side), total time display. Keep lean: no billing, no reports page.
Accept: start → stop records entry; manual log works; total aggregates correctly; running timer survives reload; Go + vitest tests.

### C3T8 — Bug bundle C: ws-smoke residue + last-admin race (S)
Source: auditor backlog #3 (`ws.smoke.test.ts` leaves scratch rows in glance_test — test pollution), #7 (last-admin removal race — concurrent removals can zero out admins; `SELECT … FOR UPDATE`). Depends on C3T6 (merge order: T6 first — both touch member removal).
Build: smoke test cleans up its rows (defer delete / transactional fixture); member-removal path takes row lock (`SELECT … FOR UPDATE` on workspace membership) so concurrent last-admin removals serialize.
Accept: smoke test leaves zero rows (verify by querying glance_test post-run); concurrent-removal race test (20×) keeps ≥1 admin; no behavior change for single removal.

## Dependency / merge order
C3T1 → C3T2 (strict). C3T3 → C3T4 (strict). C3T6 → C3T8 (strict — member removal overlap). Others independent; merge each when its review+QA pass, no big-bang.

## Waves (max 2 parallel implementers per wave, one worktree each)
- **Wave 1:** C3T0 (invites) + C3T1 (modules backend) — disjoint: workspaces API vs new modules domain.
- **Wave 2:** C3T2 (modules frontend) + C3T3 (pages backend) — disjoint: frontend modules vs new pages domain.
- **Wave 3:** C3T4 (pages frontend) + C3T5 (calendar) — both frontend-additive, disjoint new files; calendar touches ProjectNav only.
- **Wave 4:** C3T6 (workspace settings) + C3T7 (time tracking) — disjoint: settings page vs detail timer widget + new domain.
- **Wave 5:** C3T8 (bug bundle) — alone, after T6 merge.

## QA gate (same as cycle 2, non-negotiable)
gofmt clean, `go vet` clean, `go test ./...` all green (plus 20× flake-run on any new concurrency test — T8's race test included), `tsc --noEmit` + `npm run build` clean, `vitest` green, smoke test on demo seed, govulncheck clean, perf numbers re-measured and recorded (list p95 ≤ 3.5ms, RSS ≤ 15MB). Then push main, update CHANGELOG, prepare v0.3.0 draft release notes (NEVER publish — publishing is galih's checkpoint), report.

## Architecture / process notes
- TDD mandatory, real PG via TEST_DATABASE_URL (per-command only), no silent skips.
- Branch per task: `cycle3/c3t<N>-<slug>`; implementers PUSH the branch, never merge — parent reviews diff adversarially then merges to main.
- One worktree per implementer. `git worktree add -b <branch> <path> main` (never `add <path> main` while main is checked out elsewhere — cycle-2 lesson).
- Dark-default theme: all new UI must be theme-aware (cycle-1 T3).
- Lean constraints unchanged: single Go binary, Postgres-only, Apache-2.0, everything free (no paywalled features). No new npm/Go deps without justification in the task review.
- Markdown editor for pages: textarea-based, no rich-text library (lean thesis).

## Loop state carried forward
- Cycle-3 ledger: `.superpowers/sdd/2026-10-09-glance-cycle3/progress.md` (this cycle's sections go there; the v0.2.0 progress.md stays as history).
- Checkpoints that need galih: publishing the v0.3.0 GitHub Release, breaking changes, real trade-offs. Everything else: decide and keep cycling.
