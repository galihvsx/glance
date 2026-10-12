package service

// Page reactions (C24T3): the page analogue of issue reactions (Task
// 18), minus the standalone list endpoint — the read side is the
// embedded reactions[] on GET page detail.
//
// Schema (000049): page_reactions (page_id FK CASCADE, user_id FK
// CASCADE, emoji, PRIMARY KEY (page_id, user_id, emoji)), exactly
// the issue_reactions convention. Groups are reported as
// ReactionGroup (emoji/count/reacted), the same shape issues use.
//
// Conventions (mirror page comments and the issue satellites):
//   - Tenancy: every op resolves workspace membership + project via
//     resolveIssueProject, then the live page via resolvePage. A bad
//     slug or non-member caller surfaces ErrNotFound; a bad project
//     identifier surfaces ErrProjectNotFound; a bad page id surfaces
//     ErrPageNotFound. Distinct 404s on purpose (Task 11 ruling).
//   - Roles: viewer (10)+ may read; mutations need member (15)+,
//     like page comments and the issue satellites.
//   - Emoji: whitespace-trimmed, rejected when empty or longer than
//     32 runes (ErrInvalidReaction), exactly the issue-reaction rule.
//   - Writes are idempotent: a repeat add is a no-op (INSERT ...
//     ON CONFLICT DO NOTHING), a remove of an absent row is a no-op.

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AddPageReaction adds the caller's emoji reaction to a page; a repeat
// is a no-op. Member (15)+.
func AddPageReaction(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, pageID, actorID, emoji string) error {
	if _, _, err := resolvePageCommentProject(ctx, pool, wsSlug, identifier, pageID, actorID, RoleMember); err != nil {
		return err
	}
	emoji = strings.TrimSpace(emoji)
	if emoji == "" || len([]rune(emoji)) > 32 {
		return ErrInvalidReaction
	}
	_, err := pool.Exec(ctx,
		`INSERT INTO page_reactions (page_id, user_id, emoji)
		 VALUES ($1::uuid, $2::uuid, $3) ON CONFLICT DO NOTHING`,
		pageID, actorID, emoji)
	return err
}

// RemovePageReaction removes the caller's emoji reaction from a page;
// absent is a no-op. Member (15)+.
func RemovePageReaction(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, pageID, actorID, emoji string) error {
	if _, _, err := resolvePageCommentProject(ctx, pool, wsSlug, identifier, pageID, actorID, RoleMember); err != nil {
		return err
	}
	_, err := pool.Exec(ctx,
		`DELETE FROM page_reactions
		 WHERE page_id = $1::uuid AND user_id = $2::uuid AND emoji = $3`,
		pageID, actorID, emoji)
	return err
}

// ListPageReactions groups the page's reactions by emoji. Viewer (10)+.
//
// Page detail embeds this (GET .../pages/{pageID} → reactions[]), so
// there is no standalone list endpoint — the read side follows the
// GET-detail shape instead of the issue satellites' GET .../reactions.
func ListPageReactions(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, pageID, actorID string) ([]*ReactionGroup, error) {
	if _, _, err := resolvePageCommentProject(ctx, pool, wsSlug, identifier, pageID, actorID, RoleViewer); err != nil {
		return nil, err
	}
	return listPageReactions(ctx, pool, pageID, actorID)
}

// listPageReactions groups reactions by emoji without re-resolving
// tenancy; for callers that already resolved the page.
// listPageReactions groups reactions by emoji without re-resolving
// tenancy; for callers that already resolved the page.
func listPageReactions(ctx context.Context, pool *pgxpool.Pool, pageID, actorID string) ([]*ReactionGroup, error) {
	rows, err := pool.Query(ctx,
		`SELECT emoji, count(*)::int, bool_or(user_id = $2::uuid)
		 FROM page_reactions
		 WHERE page_id = $1::uuid
		 GROUP BY emoji
		 ORDER BY count(*) DESC, emoji ASC`,
		pageID, actorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*ReactionGroup{}
	for rows.Next() {
		var g ReactionGroup
		if err := rows.Scan(&g.Emoji, &g.Count, &g.Reacted); err != nil {
			return nil, err
		}
		out = append(out, &g)
	}
	return out, rows.Err()
}
