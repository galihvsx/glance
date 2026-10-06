-- 000010_satellites: issue satellites (spec §4, Task 18).
--
-- comments are threaded via parent_id (a reply nests under its parent;
-- the service validates the parent belongs to the same issue and is not
-- deleted — a cycle is impossible on create since the new row cannot be
-- its own ancestor, and there is no reparent endpoint). content is JSONB
-- (TipTap doc, same convention as issues.description). Soft delete keeps
-- thread structure readable.
-- comment_reactions / issue_reactions / issue_votes / issue_subscribers
-- are (entity, user) joins; all satellite mutations are idempotent
-- (POST adds, DELETE removes, second POST is a no-op — never a 409).
-- issue_relations stores ONE canonical row per relation; the reverse is
-- derived in the service layer (spec §4). type is CHECK-constrained to
-- the six canonical types; self-relations are rejected.
-- issue_versions is append-only (no UPDATE path anywhere): every issue
-- mutation snapshots the POST-mutation full row as JSONB; restore
-- applies an old snapshot as a NEW version row, never rewriting history.
CREATE TABLE IF NOT EXISTS comments (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    issue_id   UUID NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    parent_id  UUID REFERENCES comments(id) ON DELETE CASCADE,
    actor_id   UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    content    JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ,
    CHECK (parent_id IS NULL OR parent_id != id)
);
CREATE INDEX IF NOT EXISTS comments_issue_idx ON comments (issue_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS comments_parent_idx ON comments (parent_id) WHERE parent_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS comment_reactions (
    comment_id UUID NOT NULL REFERENCES comments(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    emoji      TEXT NOT NULL CHECK (char_length(emoji) BETWEEN 1 AND 32),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (comment_id, user_id, emoji)
);

CREATE TABLE IF NOT EXISTS issue_reactions (
    issue_id   UUID NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    emoji      TEXT NOT NULL CHECK (char_length(emoji) BETWEEN 1 AND 32),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (issue_id, user_id, emoji)
);

CREATE TABLE IF NOT EXISTS issue_votes (
    issue_id   UUID NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (issue_id, user_id)
);

CREATE TABLE IF NOT EXISTS issue_subscribers (
    issue_id   UUID NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (issue_id, user_id)
);

CREATE TABLE IF NOT EXISTS issue_relations (
    issue_id         UUID NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    related_issue_id UUID NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    type             TEXT NOT NULL CHECK (type IN
                         ('blocked_by','relates_to','duplicate',
                          'start_before','finish_before','implemented_by')),
    created_by       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (issue_id, related_issue_id, type),
    CHECK (issue_id != related_issue_id)
);
CREATE INDEX IF NOT EXISTS issue_relations_related_idx ON issue_relations (related_issue_id);

CREATE TABLE IF NOT EXISTS issue_versions (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    issue_id   UUID NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    version_no INT NOT NULL CHECK (version_no >= 1),
    snapshot   JSONB NOT NULL,
    created_by UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (issue_id, version_no)
);
CREATE INDEX IF NOT EXISTS issue_versions_issue_idx ON issue_versions (issue_id, version_no);
