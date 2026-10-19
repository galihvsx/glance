-- 000071_parallel_cycles: per-project parallel-cycles toggle (C78T0).
--
-- Plane's Cycles docs — "Parallel cycles" (Enterprise Grid): by default,
-- only one cycle can be active at a time and cycles cannot have overlapping
-- dates; with parallel cycles enabled, multiple cycles may run
-- simultaneously with overlapping date ranges (e.g. a two-week sprint
-- alongside a longer release cycle).
--
-- glance's service layer enforces the OFF semantics (CreateCycle /
-- UpdateCycle reject date ranges overlapping another live (upcoming /
-- current) cycle of the same project); ON keeps today's behavior (no
-- restriction). Grandfathering is deliberate: flipping the toggle OFF
-- with pre-existing overlapping cycles does NOT retroactively alter
-- them — only new creates/updates are blocked.
--
-- Idempotent: safe to re-run (IF NOT EXISTS).

ALTER TABLE projects
    ADD COLUMN IF NOT EXISTS parallel_cycles BOOLEAN NOT NULL DEFAULT false;
