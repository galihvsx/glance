package service

// Duplicate merge (C25T3): fold a duplicate issue into a surviving issue
// of the same project — POST
// .../issues/{uuid}/merge {"into_uuid": "<uuid>"}.
//
// Everything happens in ONE transaction:
//   - Satellites move from source to target: comments (threads move
//     together — parent_id references stay inside the moved set),
//     attachment metadata rows (the bytes live on disk under the
//     server-generated stored_path, so only the metadata row moves),
//     issue_links (both endpoints rewritten; edges that would become a
//     self-loop or duplicate an existing edge are dropped), reactions,
//     votes and subscribers (exact duplicates on the target are dropped —
//     the user keeps their single target row), and time entries (a source
//     running timer that would collide with the target's running timer is
//     stopped at merge time, never dropped).
//   - The source lands in the project's cancelled-group state (like
//     backlogState, the project's first cancelled state by sequence; a
//     project without one is ErrMergeNoCancelledState).
//   - A 'duplicate' row in issue_relations records the surviving issue —
//     the user-facing relation mechanism (symmetric, like CreateRelation),
//     which doubles as the idempotency key.
//   - Activity: a state_id field entry on the source (uniform with every
//     other state mutation) plus _merged entries on BOTH issues —
//     {"action":"merged_into","target":"ENG-45"} on the source and
//     {"action":"absorbed_duplicate","source":"ENG-44"} on the target.
//   - The source gets a version snapshot and an issue.updated webhook
//     (changed_fields ["state_id"]); both issues get a WS
//     announceIssueUpdated after commit.
//
// Guards (checked in order): empty into_uuid → ErrMergeTargetRequired
// (400); into_uuid == source → ErrMergeSelf (400); unknown target →
// ErrIssueNotFound (404); live target in another project →
// ErrMergeCrossProject (400); target is a sub-issue descendant of the
// source → ErrMergeDescendant (400); member (15)+, else ErrForbidden.
// Idempotency: re-merging a source already merged into the same target
// (duplicate relation present) returns AlreadyMerged=true with no new
// writes. Assignees, labels and cycle/module/release memberships are
// deliberately NOT moved — they stay on the (cancelled) source.

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrMergeSelf is returned when into_uuid names the issue itself.
	// The handler maps it to 400.
	ErrMergeSelf = errors.New("service: cannot merge an issue into itself")
	// ErrMergeCrossProject is returned when into_uuid names a live issue
	// of another project. The handler maps it to 400.
	ErrMergeCrossProject = errors.New("service: cannot merge into an issue of another project")
	// ErrMergeDescendant is returned when into_uuid names a sub-issue
	// descendant of the source. The handler maps it to 400.
	ErrMergeDescendant = errors.New("service: cannot merge into one of the issue's own sub-issues")
	// ErrMergeTargetRequired is returned for a blank into_uuid. The
	// handler maps it to 400.
	ErrMergeTargetRequired = errors.New("service: into_uuid is required")
	// ErrMergeNoCancelledState is returned when the project has no
	// cancelled-group state to close the source into. The handler maps
	// it to 409 — the fix is project settings, not the request.
	ErrMergeNoCancelledState = errors.New("service: project has no cancelled state")
)

// MergeIssueResult is the outcome of MergeIssue. AlreadyMerged is true
// on the idempotent re-merge path (no writes performed); Issue is the
// source issue in its post-merge state and IntoDisplayID the surviving
// issue's display id (ENG-45) for the "View" toast action.
type MergeIssueResult struct {
	Merged        bool   `json:"merged"`
	AlreadyMerged bool   `json:"already_merged"`
	Issue         *Issue `json:"issue"`
	IntoDisplayID string `json:"into_display_id"`
}

// MergeIssue folds the source issue into the target issue (intoUUID) in
// one transaction. See the package doc above for the full semantics.
// Member (15)+.
func MergeIssue(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, actorID, intoUUID string) (*MergeIssueResult, error) {
	issueID = normalizeIssueID(issueID)
	into := normalizeIssueID(intoUUID)
	if into == "" {
		return nil, ErrMergeTargetRequired
	}
	if into == issueID {
		return nil, ErrMergeSelf
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	ident, projectID, role, err := resolveSatelliteIssue(ctx, tx, wsSlug, identifier, issueID, actorID)
	if err != nil {
		return nil, err
	}
	if err := requireSatelliteWriter(role); err != nil {
		return nil, err
	}

	// Lock the source row: a concurrent merge/convert/delete of the same
	// issue serializes here instead of interleaving satellite moves.
	var srcSeq int
	var srcStateID string
	if err := tx.QueryRow(ctx,
		`SELECT sequence_id, state_id::text FROM issues
		 WHERE id = $1::uuid AND project_id = $2::uuid AND deleted_at IS NULL
		 FOR UPDATE`,
		issueID, projectID).Scan(&srcSeq, &srcStateID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrIssueNotFound
		}
		return nil, err
	}
	srcDisplay := ident + "-" + itoa(srcSeq)

	// The target must be a live issue; a live issue elsewhere is a
	// cross-project 400, anything else a 404.
	var tgtSeq int
	var tgtProject string
	if err := tx.QueryRow(ctx,
		`SELECT sequence_id, project_id::text FROM issues
		 WHERE id = $1::uuid AND deleted_at IS NULL`,
		into).Scan(&tgtSeq, &tgtProject); err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return nil, ErrIssueNotFound
		}
		return nil, err
	}
	if tgtProject != projectID {
		return nil, ErrMergeCrossProject
	}
	tgtDisplay := ident + "-" + itoa(tgtSeq)
	// Lock the target too: concurrent merges into the same target
	// serialize instead of racing the dedupe guards.
	if _, err := tx.Exec(ctx, `SELECT 1 FROM issues WHERE id = $1::uuid FOR UPDATE`, into); err != nil {
		return nil, err
	}

	// Merging into one of the source's own sub-issue descendants would
	// orphan the subtree under a cancelled issue — reject it.
	var isDescendant bool
	if err := tx.QueryRow(ctx,
		`WITH RECURSIVE subtree AS (
		   SELECT id FROM issues WHERE id = $1::uuid AND deleted_at IS NULL
		   UNION ALL
		   SELECT i.id FROM issues i JOIN subtree s ON i.parent_id = s.id
		    WHERE i.deleted_at IS NULL
		 )
		 SELECT EXISTS(SELECT 1 FROM subtree WHERE id = $2::uuid)`,
		issueID, into).Scan(&isDescendant); err != nil {
		return nil, err
	}
	if isDescendant {
		return nil, ErrMergeDescendant
	}

	// Idempotency: the duplicate relation is the merge record. A source
	// already merged into THIS target returns success with no new
	// writes.
	var already bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM issue_relations
		  WHERE type = 'duplicate'
		    AND ((issue_id = $1::uuid AND related_issue_id = $2::uuid)
		      OR (issue_id = $2::uuid AND related_issue_id = $1::uuid)))`,
		issueID, into).Scan(&already); err != nil {
		return nil, err
	}
	if already {
		src, err := scanMergeIssue(tx.QueryRow(ctx,
			`SELECT `+issueColumns+` FROM issues WHERE id = $1::uuid`, issueID), ident)
		if err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return &MergeIssueResult{Merged: false, AlreadyMerged: true, Issue: src, IntoDisplayID: tgtDisplay}, nil
	}

	cancelledID, err := cancelledState(ctx, tx, projectID)
	if err != nil {
		return nil, err
	}

	if err := moveMergeSatellites(ctx, tx, issueID, into); err != nil {
		return nil, err
	}

	// Record the surviving issue via the user-facing relation mechanism
	// (symmetric normalization, like CreateRelation).
	fromID, toID := issueID, into
	if toID < fromID {
		fromID, toID = toID, fromID
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO issue_relations (issue_id, related_issue_id, type, created_by)
		 VALUES ($1::uuid, $2::uuid, 'duplicate', $3::uuid)
		 ON CONFLICT DO NOTHING`,
		fromID, toID, actorID); err != nil {
		return nil, err
	}

	// Close the source into the cancelled state.
	src, err := scanMergeIssue(tx.QueryRow(ctx,
		`UPDATE issues SET state_id = $1::uuid, updated_at = now()
		  WHERE id = $2::uuid
		  RETURNING `+issueColumns,
		cancelledID, issueID), ident)
	if err != nil {
		return nil, err
	}

	// Uniform state_id field entry (like every other state mutation).
	if _, err := tx.Exec(ctx,
		`INSERT INTO issue_activities (issue_id, actor_id, field, old_value, new_value)
		 VALUES ($1::uuid, $2::uuid, 'state_id', $3::jsonb, $4::jsonb)`,
		issueID, actorID, toJSONBParam(srcStateID), toJSONBParam(cancelledID)); err != nil {
		return nil, err
	}
	// _merged entries on both sides, carrying the display ids the plan
	// names in the activity copy ("merged into ENG-45 as duplicate" /
	// "absorbed duplicate ENG-44").
	intoVal := mustMarshalJSON(map[string]any{
		"action":    "merged_into",
		"target":    tgtDisplay,
		"target_id": into,
	})
	if _, err := tx.Exec(ctx,
		`INSERT INTO issue_activities (issue_id, actor_id, field, new_value)
		 VALUES ($1::uuid, $2::uuid, '_merged', $3::jsonb)`,
		issueID, actorID, intoVal); err != nil {
		return nil, err
	}
	absorbedVal := mustMarshalJSON(map[string]any{
		"action":    "absorbed_duplicate",
		"source":    srcDisplay,
		"source_id": issueID,
	})
	if _, err := tx.Exec(ctx,
		`INSERT INTO issue_activities (issue_id, actor_id, field, new_value)
		 VALUES ($1::uuid, $2::uuid, '_merged', $3::jsonb)`,
		into, actorID, absorbedVal); err != nil {
		return nil, err
	}

	if err := snapshotVersionTx(ctx, tx, issueID, actorID, src); err != nil {
		return nil, err
	}

	wsID, err := workspaceIDForProjectTx(ctx, tx, projectID)
	if err != nil {
		return nil, err
	}
	if err := enqueueWebhookDeliveryTx(ctx, tx, wsID, EventIssueUpdated, map[string]any{
		"id":             issueID,
		"display_id":     srcDisplay,
		"changed_fields": []string{"state_id"},
		"actor_id":       actorID,
	}); err != nil {
		return nil, err
	}

	// The target row for the post-commit WS announce (its own columns
	// are untouched, but clients need to refresh its satellites).
	tgt, err := scanMergeIssue(tx.QueryRow(ctx,
		`SELECT `+issueColumns+` FROM issues WHERE id = $1::uuid`, into), ident)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	announceIssueUpdated(wsSlug, ident, issueID, src)
	announceIssueUpdated(wsSlug, ident, into, tgt)

	return &MergeIssueResult{Merged: true, Issue: src, IntoDisplayID: tgtDisplay}, nil
}

// cancelledState returns the id of the project's first cancelled-group
// state by sequence — the column a merged duplicate closes into.
// CreateProject seeds one ("Cancelled"), so a miss means the project
// admin deleted it: ErrMergeNoCancelledState (409) points at the fix.
func cancelledState(ctx context.Context, q queryRower, projectID string) (string, error) {
	var id string
	err := q.QueryRow(ctx,
		`SELECT id::text FROM states
		 WHERE project_id = $1::uuid AND "group" = 'cancelled'
		 ORDER BY sequence LIMIT 1`, projectID).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrMergeNoCancelledState
		}
		return "", err
	}
	return id, nil
}

// scanMergeIssue scans a full issue row and stamps the display id.
func scanMergeIssue(row pgx.Row, ident string) (*Issue, error) {
	iss, err := scanIssue(row)
	if err != nil {
		return nil, err
	}
	iss.DisplayID = ident + "-" + itoa(iss.SequenceID)
	return iss, nil
}

// moveMergeSatellites re-points every satellite row of the source issue
// at the target issue. Join-table rows that already exist on the target
// (same user+emoji reaction, same vote, same subscriber) are dropped
// rather than moved — the user keeps their single target row. A source
// running timer that would collide with the target's running timer is
// stopped at merge time so no logged work is lost. Links touching the
// source are rewritten endpoint-by-endpoint; edges that would become a
// self-loop or duplicate an existing edge are dropped.
func moveMergeSatellites(ctx context.Context, tx pgx.Tx, sourceID, targetID string) error {
	// Comments (threads move together) and attachment metadata.
	if _, err := tx.Exec(ctx, `UPDATE comments SET issue_id = $1::uuid WHERE issue_id = $2::uuid`, targetID, sourceID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE attachments SET issue_id = $1::uuid WHERE issue_id = $2::uuid`, targetID, sourceID); err != nil {
		return err
	}

	// Deduplicating join-table moves: move what doesn't collide, drop
	// the exact duplicates. The NOT EXISTS subqueries read the
	// pre-statement snapshot, so rows being moved (issue_id = source)
	// never match the target-side probes.
	dedupeMoves := []struct {
		move, drop string
	}{
		{
			`UPDATE issue_reactions SET issue_id = $1::uuid WHERE issue_id = $2::uuid
			  AND NOT EXISTS (SELECT 1 FROM issue_reactions r2
			                  WHERE r2.issue_id = $1::uuid
			                    AND r2.user_id = issue_reactions.user_id
			                    AND r2.emoji = issue_reactions.emoji)`,
			`DELETE FROM issue_reactions WHERE issue_id = $1::uuid`,
		},
		{
			`UPDATE issue_votes SET issue_id = $1::uuid WHERE issue_id = $2::uuid
			  AND NOT EXISTS (SELECT 1 FROM issue_votes v2
			                  WHERE v2.issue_id = $1::uuid
			                    AND v2.user_id = issue_votes.user_id)`,
			`DELETE FROM issue_votes WHERE issue_id = $1::uuid`,
		},
		{
			`UPDATE issue_subscribers SET issue_id = $1::uuid WHERE issue_id = $2::uuid
			  AND NOT EXISTS (SELECT 1 FROM issue_subscribers s2
			                  WHERE s2.issue_id = $1::uuid
			                    AND s2.user_id = issue_subscribers.user_id)`,
			`DELETE FROM issue_subscribers WHERE issue_id = $1::uuid`,
		},
	}
	for _, m := range dedupeMoves {
		if _, err := tx.Exec(ctx, m.move, targetID, sourceID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, m.drop, sourceID); err != nil {
			return err
		}
	}

	// Time entries: stop (never drop) a source running timer that would
	// collide with the target's running timer, then move everything.
	if _, err := tx.Exec(ctx,
		`UPDATE time_entries te SET ended_at = now()
		  WHERE te.issue_id = $1::uuid AND te.ended_at IS NULL
		    AND EXISTS (SELECT 1 FROM time_entries t2
		                WHERE t2.issue_id = $2::uuid
		                  AND t2.user_id = te.user_id
		                  AND t2.ended_at IS NULL)`,
		sourceID, targetID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE time_entries SET issue_id = $1::uuid WHERE issue_id = $2::uuid`, targetID, sourceID); err != nil {
		return err
	}

	// Links: rewrite each edge touching the source onto the target,
	// dropping edges that would self-loop or duplicate an existing one.
	rows, err := tx.Query(ctx,
		`SELECT id::text, issue_id::text, target_issue_id::text, kind
		 FROM issue_links WHERE issue_id = $1::uuid OR target_issue_id = $1::uuid`,
		sourceID)
	if err != nil {
		return err
	}
	type linkEdge struct{ id, src, tgt, kind string }
	var edges []linkEdge
	for rows.Next() {
		var e linkEdge
		if err := rows.Scan(&e.id, &e.src, &e.tgt, &e.kind); err != nil {
			rows.Close()
			return err
		}
		edges = append(edges, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, e := range edges {
		ns, nt := e.src, e.tgt
		if ns == sourceID {
			ns = targetID
		}
		if nt == sourceID {
			nt = targetID
		}
		if ns == nt {
			// Rewriting would self-loop (e.g. an existing
			// source→target edge): drop it — the merge itself is
			// the duplicate record.
			if _, err := tx.Exec(ctx, `DELETE FROM issue_links WHERE id = $1::uuid`, e.id); err != nil {
				return err
			}
			continue
		}
		var dup bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM issue_links
			  WHERE issue_id = $1::uuid AND target_issue_id = $2::uuid AND id <> $3::uuid)`,
			ns, nt, e.id).Scan(&dup); err != nil {
			return err
		}
		if dup {
			if _, err := tx.Exec(ctx, `DELETE FROM issue_links WHERE id = $1::uuid`, e.id); err != nil {
				return err
			}
			continue
		}
		if _, err := tx.Exec(ctx,
			`UPDATE issue_links SET issue_id = $1::uuid, target_issue_id = $2::uuid WHERE id = $3::uuid`,
			ns, nt, e.id); err != nil {
			return err
		}
	}
	return nil
}
