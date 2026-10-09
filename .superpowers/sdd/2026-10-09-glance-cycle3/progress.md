# Cycle 3 progress — "Depth" (started 2026-10-09)

Ledger for cycle 3. Cycle 2 history lives in `.superpowers/sdd/2026-10-08-glance-v0.2.0/progress.md` (read-only from here on).

## Cycle 3 kicked off — 2026-10-09 ~09:50 +08
- Status at kickoff: cycle 2 merged to main (253f6d2) + pushed to galihvsx/glance; QA gate FULL PASS (go1.27.2; gofmt/vet/tests×9pkgs/govulncheck/tsc/build/vitest 67/smoke green; list p95 892µs, no regression). local main == origin/main == 253f6d2, clean. Cycle-2 session ended without auto-starting cycle 3; PM+architect wrote the plan now.
- Plan: `.superpowers/sdd/2026-10-09-glance-cycle3/cycle3-plan.md` — 9 tasks C3T0–C3T8: invites endpoint, modules backend+frontend, wiki/pages backend+frontend, calendar view, workspace settings UI, time tracking, bug bundle C. Explicitly NOT in cycle 3: gantt (needs calendar — cycle 4), analytics, releases, importers, public share, admin panel, attachments, AI.
- Waves: W1 (T0+T1) → W2 (T2+T3) → W3 (T4+T5) → W4 (T6+T7) → W5 (T8), max 2 parallel implementers, one worktree each (`git worktree add -b <branch> <path> main`).
- Wave-1 readiness: worktrees area clean (cycle-2 worktrees removed); branches namespace `cycle3/c3t<N>-<slug>` free. Ready to dispatch C3T0 + C3T1.
- v0.2.0 GitHub Release still unpublished — galih checkpoint, orthogonal to cycle 3.

## Wave 1 dispatched — 2026-10-09 ~09:55 +08 (by coordinator, right after watchdog resume)
- C3T0 (invites endpoint) + C3T1 (modules backend) in parallel, isolated worktrees, corrected `worktree add -b` form.
- **C3T1 merged** to main (adversarial review pass: member scoping ✓, cross-project assignment blocked ✓, idempotent migration ✓, delete guard ✓). Tests green post-merge. Worktree cleaned.
- **C3T2 merged** (review pass: route/nav/api.del shared changes minimal & backward-compat; Modules.tsx mirrors Cycles patterns; tsc clean post-merge). Worktree cleaned.
- **C3T3 merged** (review pass: project-scoped parent ✓, recursive-CTE cycle guard ✓, tx move/reorder ✓, idempotent migration ✓). Tests green post-merge. Worktree cleaned.
- **C3T4 dispatched** (pages frontend) — dependency on C3T3 satisfied.
- **C3T4 merged** (review pass: hand-rolled markdown renderer XSS-safe — escape-first, href allow-list, rel=noreferrer; zero new deps; 72 vitest green post-merge). Worktree cleaned.
- **C3T5 merged** (review pass: start_after/before + undated params parameterized, reuse parse path; conflict in ProjectNav doc comment resolved keeping both tabs; post-resolve: tsc clean, 83 vitest green, go build/vet clean). Worktree cleaned.
- **C3T6 merged** (review pass: admin-only delete ✓, slug validation + 409 ✓, ErrLastAdmin surfaced ✓). Tests green post-merge. Worktree cleaned.
- **C3T8 dispatched** (bug bundle C) — dependency on C3T6 satisfied.
- **C3T7 merged** (review pass: partial unique index → race-safe 409 ✓, satellite scoping helpers existing ✓, no realtime leak by design ✓). Tests green post-merge, tsc clean. Worktree cleaned.
- **C3T0 merged** (review pass: admin-only ✓, per-email statuses ✓, idempotent ✓, onboarding wiring graceful ✓). Conflicts in workspace test files resolved keeping both sides' tests (C3T6/C3T8 + C3T0). Agent hung post-push (42min idle) — closed, merged manually. Worktree cleaned.
- **All 9 cycle-3 tasks merged.** Running the QA gate.

## Cycle 3 complete — 2026-10-09 ~10:52 +08
- **All 9 tasks merged** (C3T0–C3T8): invites endpoint, modules backend+frontend, wiki backend+frontend, calendar, workspace settings, time tracking, bug bundle C.
- **QA gate: FULL PASS** (go1.27.2): gofmt/vet clean, all 9 Go packages ok, TestRemoveMemberConcurrentLastAdmin 5/5, govulncheck clean, tsc+build clean, 87 vitest passed, smoke test pass (health-check + new routes 401-unauth). Perf: list p95 2.04ms (budget 3.5ms), no regression.
- **Pushed:** main 56c3e85 → c01d619 on galihvsx/glance. CHANGELOG v0.3.0 (unreleased) written.
- v0.2.0 + v0.3.0 GitHub Releases still unpublished — galih checkpoints.
- **Cycle 4 starts automatically** (per plan: gantt — calendar is now the prerequisite).
