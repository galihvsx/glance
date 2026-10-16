-- C40T0 down: drop the KR check-in history table. Idempotent: safe to re-run.
DROP TABLE IF EXISTS key_result_checkins;
