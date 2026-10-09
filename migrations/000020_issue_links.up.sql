-- 000020_issue_links: directed dependency edges between issues (C4T0,
-- the cycle headline: Gantt backend).
--
-- issue_links stores ONE directed edge per (issue_id, target_issue_id):
-- "issue A (kind) issue B", e.g. A blocks B. The default kind is
-- 'blocks'; the service validates the kind vocabulary (blocks,
-- relates_to, duplicates) — the column itself has no CHECK so future
-- kinds are a service change, not a migration.
--
-- The direction matters: "blocked by" is the same edge addressed from
-- the other side (issue_id/target_issue_id swapped), never a separate
-- kind. A UNIQUE(issue_id, target_issue_id) keeps the graph a simple
-- directed graph: no parallel edges, no reverse-duplicates ambiguity.
--
-- Scope note (v0.3.0, honest): the DB rejects only direct self-loops
-- (CHECK issue_id <> target_issue_id) and duplicates. There is NO
-- cycle detection — an A->B->C->A dependency cycle is accepted and
-- would render as-is on the Gantt. Cycle detection is a future task,
-- not claimed here. (issue_relations exists separately as the
-- user-facing relation graph with its own type vocabulary; issue_links
-- is the lean dependency-edge contract the Gantt consumes.)
--
-- Everything is IF NOT EXISTS so the migration is re-runnable.
CREATE TABLE IF NOT EXISTS issue_links (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    issue_id        UUID NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    target_issue_id UUID NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    kind            TEXT NOT NULL DEFAULT 'blocks',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT issue_links_no_self CHECK (issue_id <> target_issue_id),
    CONSTRAINT uq_issue_links UNIQUE (issue_id, target_issue_id)
);
-- Gantt / reverse lookup: "everything blocking issue X". Outgoing
-- edges (WHERE issue_id = ...) are served by the uq_issue_links
-- (issue_id, target_issue_id) btree prefix — no second index needed.
CREATE INDEX IF NOT EXISTS idx_issue_links_target
    ON issue_links(target_issue_id);
