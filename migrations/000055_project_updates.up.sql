-- C34T0 up: project status updates. One flat timeline per project:
-- each update carries a health indicator (on_track / at_risk /
-- off_track) plus a plain-text message, newest first on Overview.
-- author_id is nullable with ON DELETE SET NULL (plan spec): deleting
-- a user keeps their updates, attributed to a null author. Deleting a
-- project deletes its updates via ON DELETE CASCADE. Body is plain
-- text (NOT markdown, NOT TipTap JSONB) in v1, validated in the
-- service like page-comment bodies: whitespace-trimmed, non-empty.
-- Idempotent: safe to re-run.
CREATE TABLE IF NOT EXISTS project_updates (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    author_id  UUID REFERENCES users(id) ON DELETE SET NULL,
    status     TEXT NOT NULL CHECK (status IN ('on_track', 'at_risk', 'off_track')),
    body       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS project_updates_project_created_idx
    ON project_updates (project_id, created_at DESC);
