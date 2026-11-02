package service

// Persisted @-mention index (C120T0): the issue_mentions table
// (migration 000082) answers the Mentions list filter dimension
// without re-parsing comment bodies per query. One row per (issue,
// user, comment) — the per-comment grain is what makes write-path
// maintenance trivial: a comment edit or delete touches only that
// comment's rows, never a cross-comment recount.
//
// Resolution reuses the EXISTING notification rules exactly
// (resolveCommentMentions in mention.go: longest-prefix names,
// ambiguous skipped, self-mention skipped, non-member skipped),
// so the index and the notifications can never disagree about who
// a comment mentions. Import paths deliberately do NOT populate
// the index — they follow the notification precedent ("must not
// fire mention parsing"); an imported comment matches the filter
// only after it is edited through the normal write path.

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// syncIssueMentionsTx rewrites one comment's index rows in the
// caller's tx: delete-then-insert keeps an edit that REMOVES a
// mention correct, and a delete-then-insert of the same set is a
// no-op. Callers MUST run it in the same tx as the comment write —
// never a second tx that can diverge from the comment. An empty
// userID list still deletes, so clearing every mention works.
func syncIssueMentionsTx(ctx context.Context, tx pgx.Tx, issueID, commentID string, userIDs []string) error {
	if _, err := tx.Exec(ctx,
		`DELETE FROM issue_mentions WHERE comment_id = $1::uuid`,
		commentID); err != nil {
		return err
	}
	for _, userID := range userIDs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO issue_mentions (issue_id, user_id, comment_id)
			 VALUES ($1::uuid, $2::uuid, $3::uuid)
			 ON CONFLICT DO NOTHING`,
			issueID, userID, commentID); err != nil {
			return err
		}
	}
	return nil
}

// deleteIssueMentionsTx removes one comment's index rows in the
// caller's tx (the DeleteComment path, where there is no resolved
// set to re-insert).
func deleteIssueMentionsTx(ctx context.Context, tx pgx.Tx, commentID string) error {
	_, err := tx.Exec(ctx,
		`DELETE FROM issue_mentions WHERE comment_id = $1::uuid`,
		commentID)
	return err
}

// BackfillIssueMentions is the one-time sweep over pre-existing
// comments, which predate the index. It runs in a SINGLE tx: every
// non-deleted comment is resolved via the existing
// resolveCommentMentions per comment workspace and its rows are
// inserted with ON CONFLICT DO NOTHING, so re-running it is a
// no-op. The fast path is inside the same tx: a non-empty table
// implies a completed sweep (a crash mid-sweep leaves zero rows,
// because the whole sweep rolls back — so the next boot re-runs
// it); a genuinely mention-less DB re-sweeps each boot and finds
// nothing, which is cheap.
func BackfillIssueMentions(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var exists bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM issue_mentions)`).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}

	rows, err := tx.Query(ctx,
		`SELECT c.id::text, c.issue_id::text, c.actor_id::text,
		        p.workspace_id::text, c.content::text
		   FROM comments c
		   JOIN issues i ON i.id = c.issue_id
		   JOIN projects p ON p.id = i.project_id
		  WHERE c.deleted_at IS NULL
		  ORDER BY c.created_at, c.id`)
	if err != nil {
		return err
	}
	type commentRow struct {
		commentID string
		issueID   string
		actorID   string
		wsID      string
		content   json.RawMessage
	}
	var comments []commentRow
	for rows.Next() {
		var r commentRow
		var content string
		if err := rows.Scan(&r.commentID, &r.issueID, &r.actorID, &r.wsID, &content); err != nil {
			rows.Close()
			return err
		}
		r.content = json.RawMessage(content)
		comments = append(comments, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, c := range comments {
		mentioned, err := resolveCommentMentions(ctx, tx, c.wsID, c.actorID, c.content)
		if err != nil {
			return err
		}
		if err := syncIssueMentionsTx(ctx, tx, c.issueID, c.commentID, mentioned); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
