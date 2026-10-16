-- 000057_page_issue_links: links between wiki pages and issues (cycle 36,
-- C36T0 "Pages <-> Work items"). One row per (page, issue) pair: the link
-- is a pointer, not a copy — no sync of any kind. Deleting a page or an
-- issue cascades its links; deleting a user NULLs the link creator.
-- The UNIQUE (page_id, issue_id) is the duplicate backstop: a racing
-- double create serializes into a conflict (409), never two rows.
-- Archived and draft issues are linkable — no exclusion on either.
-- Idempotent: safe to re-run.
CREATE TABLE IF NOT EXISTS page_issue_links (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    page_id    UUID NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
    issue_id   UUID NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (page_id, issue_id)
);
CREATE INDEX IF NOT EXISTS page_issue_links_issue_idx
    ON page_issue_links (issue_id);
