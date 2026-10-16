-- C42T1 down: remove backfilled KR check-in seed rows. The
-- previous_value-IS-NULL + note marker uniquely identifies them:
-- API seeds always have note NULL, and no API path creates a
-- previous-NULL row with a note. Idempotent: safe to re-run.
DELETE FROM key_result_checkins WHERE previous_value IS NULL AND note = 'Backfilled history';
