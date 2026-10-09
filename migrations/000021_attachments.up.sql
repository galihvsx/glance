-- 000021_attachments: issue-scoped file attachments (C4T2).
--
-- attachments: one row per uploaded file. The bytes live on the local
-- filesystem under <data-dir>/attachments (see internal/store); the
-- table holds only metadata. stored_path is a server-generated name
-- (uuid + sanitized extension) — never a client-controlled path — so
-- the DB cannot be used to smuggle a traversal into the file layer.
--
-- filename keeps the original client name for DISPLAY only (sanitized
-- at upload); content_type is the sniffed/normalized media type used to
-- decide inline vs. download serving. ON DELETE CASCADE on issue_id
-- removes metadata with the issue; orphaned bytes are cleaned by the
-- delete path (best effort) since a file-store GC is out of scope.
--
-- Everything is IF NOT EXISTS so the migration is re-runnable.
CREATE TABLE IF NOT EXISTS attachments (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    issue_id     UUID NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    filename     TEXT NOT NULL,
    content_type TEXT NOT NULL DEFAULT '',
    size_bytes   BIGINT NOT NULL CHECK (size_bytes >= 0),
    stored_path  TEXT NOT NULL,
    uploaded_by  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_attachments_issue
    ON attachments(issue_id, created_at DESC);
