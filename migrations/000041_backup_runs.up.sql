-- 000041_backup_runs: scheduled backup run history (C15T2).
--
-- One row per workspace backup produced by the backup job (scheduled via
-- GLANCE_BACKUP_INTERVAL, or triggered manually via
-- POST /api/v1/admin/backups/run). The archive itself is a gzipped
-- glance-export/1 JSON document on local disk (GLANCE_BACKUP_DIR); this
-- table is the queryable history the admin UI lists, plus the integrity
-- metadata (sha256 over the stored bytes) the verify pass checks.
--
-- A manifest JSON sidecar (<archive>.manifest.json) is written next to
-- each archive so the backup directory stays self-describing even if
-- this table is lost; the row and the sidecar carry the same fields.
--
-- Retention pruning (GLANCE_BACKUP_RETENTION) keeps the newest N runs per
-- workspace: pruned runs keep their row with status='pruned' and
-- file_path NULL (history stays queryable), while the archive + manifest
-- files are deleted from disk. retention=0 keeps everything forever.
--
-- status is a plain TEXT with an operator-facing contract
-- ('ok' | 'failed' | 'pruned'); failed runs still record their error so a
-- broken schedule is visible in the admin UI instead of silent.
CREATE TABLE IF NOT EXISTS backup_runs (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id      UUID REFERENCES workspaces(id) ON DELETE SET NULL,
    workspace_slug    TEXT NOT NULL,
    at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    file_name         TEXT NOT NULL,
    file_path         TEXT,
    byte_size         BIGINT NOT NULL DEFAULT 0,
    sha256            TEXT NOT NULL DEFAULT '',
    format_version    TEXT NOT NULL DEFAULT '',
    migration_version TEXT NOT NULL DEFAULT '',
    status            TEXT NOT NULL DEFAULT 'ok',
    verify_ok         BOOLEAN,
    verify_error      TEXT,
    verified_at       TIMESTAMPTZ,
    triggered_by      TEXT NOT NULL DEFAULT 'schedule'
);

-- Newest-first history reads are the access pattern, optionally scoped
-- to one workspace.
CREATE INDEX IF NOT EXISTS idx_backup_runs_at ON backup_runs (at DESC);
CREATE INDEX IF NOT EXISTS idx_backup_runs_workspace_at ON backup_runs (workspace_slug, at DESC);
