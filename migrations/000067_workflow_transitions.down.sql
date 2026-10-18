-- C68T0 down: drop the workflow transition rules table.
-- Idempotent: safe to re-run. Dropping the table loses the configured
-- rules (they are admin configuration, not issue data); a rollback
-- pairs with the C68T0 code (the enforcement check reads this table,
-- so dropping it without rolling the code back would fail state moves).
DROP TABLE IF EXISTS project_state_transitions;
