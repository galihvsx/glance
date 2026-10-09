-- 000018_pages: project wiki/documentation pages (C3T3).
--
-- pages: project-scoped doc tree. parent_id is a self-FK with
-- ON DELETE CASCADE: deleting a parent deletes its whole subtree, the
-- same cascade semantics as project deletion (documented choice, C3T3).
-- position orders siblings; list queries ORDER BY position, id.
--
-- page_revisions: content snapshots appended by the service on every
-- title/content update (pre-update state) and on restore; ON DELETE
-- CASCADE with the page.
--
-- Everything is IF NOT EXISTS so the migration is re-runnable.
CREATE TABLE IF NOT EXISTS pages (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id  UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    parent_id   UUID REFERENCES pages(id) ON DELETE CASCADE,
    title       TEXT NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
    content     TEXT NOT NULL DEFAULT '',
    position    INT NOT NULL DEFAULT 0,
    author_id   UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_pages_project_parent
    ON pages(project_id, parent_id);

CREATE TABLE IF NOT EXISTS page_revisions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    page_id     UUID NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
    title       TEXT NOT NULL,
    content     TEXT NOT NULL,
    author_id   UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_page_revisions_page
    ON page_revisions(page_id, created_at DESC);
