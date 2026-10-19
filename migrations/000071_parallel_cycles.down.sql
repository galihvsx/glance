-- 000071_parallel_cycles down: drop the per-project parallel-cycles toggle
-- column (manual rollback only; the migrator never applies down files).

ALTER TABLE projects DROP COLUMN IF EXISTS parallel_cycles;
