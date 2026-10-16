-- C35T0 down: drop the OKR tables. Idempotent: safe to re-run.
DROP TABLE IF EXISTS key_result_issues;
DROP TABLE IF EXISTS key_results;
DROP TABLE IF EXISTS objectives;
