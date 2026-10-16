-- C40T0 up: KR check-in history. An append-only log of metric-KR
-- current_value changes (plus one seed entry per metric KR at
-- creation). previous_value NULL marks the seed entry (there is no
-- previous value at creation). author_id is nullable with ON DELETE
-- SET NULL (page-comments convention): deleting a user keeps their
-- check-ins, attributed to a null author. There is deliberately no
-- update/delete path for this table anywhere in the service layer:
-- a mistaken check-in is corrected by a new check-in. Issue KRs get
-- no rows (their progress is computed from linked issues, never
-- hand-edited). Idempotent: safe to re-run.
CREATE TABLE IF NOT EXISTS key_result_checkins (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    key_result_id  UUID NOT NULL REFERENCES key_results(id) ON DELETE CASCADE,
    author_id      UUID REFERENCES users(id) ON DELETE SET NULL,
    previous_value NUMERIC NULL,
    new_value      NUMERIC NOT NULL,
    note           TEXT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS key_result_checkins_kr_idx
    ON key_result_checkins (key_result_id, created_at DESC);
