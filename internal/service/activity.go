package service

// Project activity feed (C5T7): read-only aggregation of the
// issue_activities field-level audit log (migration 000007) into a
// project-wide feed, newest first.
//
// Conventions (mirror analytics / cycles):
//   - Tenancy: the project is resolved via resolveCycleProject with
//     needMember=true — any workspace member (15)+ may read; guests
//     (5) surface ErrForbidden. A bad slug, identifier, or non-member
//     caller surfaces ErrNotFound (shared 404).
//   - Scope is LIVE issues (deleted_at IS NULL), matching the list
//     endpoint's default; activity on soft-deleted issues is not part
//     of the feed. Archived issues keep their rows (archival is not a
//     deletion — the feed is a change log, not a board).
//   - Single query, no N+1: issue_activities joins issues (project
//     scope), users (actor display name), and projects (the display
//     identifier for "ENG-123").
//
// Index note: the query joins issues on project_id under the partial
// issues_project_idx (project_id, WHERE deleted_at IS NULL — the query's
// predicate matches it exactly) and drives into issue_activities via
// issue_activities_issue_idx (issue_id, created_at). No new index and no
// migration: the two existing indexes serve the join path, and the feed
// is a LIMIT-capped read, not a hot path like the issue list.

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrInvalidActivityLimit is returned when the activity feed `limit`
// query param is not an integer in 1..200. The handler maps it to 400
// bad_request.
var ErrInvalidActivityLimit = errors.New("service: invalid activity limit")

// ActivityMaxLimit caps the feed page size; ActivityDefaultLimit is the
// page size when the caller passes no limit.
const (
	ActivityDefaultLimit = 50
	ActivityMaxLimit     = 200
)

// ActivityEntry is one row of the project activity feed: a single
// field-level audit entry. Old/New carry the raw JSONB values as text
// (NULL when the field was unset on that side of the change); the
// frontend renders them into human-readable form.
type ActivityEntry struct {
	At              time.Time `json:"at"`
	Actor           string    `json:"actor"`
	IssueUUID       string    `json:"issue_uuid"`
	IssueIdentifier string    `json:"issue_identifier"`
	Field           string    `json:"field"`
	Old             *string   `json:"old"`
	New             *string   `json:"new"`
}

// GetProjectActivity returns the newest-first activity feed for a
// project: the issue_activities audit rows of its live issues, capped
// at limit rows. Member (15)+; guests ErrForbidden.
func GetProjectActivity(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string, limit int) ([]ActivityEntry, error) {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return nil, err
	}
	if limit < 1 || limit > ActivityMaxLimit {
		return nil, ErrInvalidActivityLimit
	}
	projectID, _, err := resolveCycleProject(ctx, pool, wsSlug, ident, actorID, true)
	if err != nil {
		return nil, err
	}

	rows, err := pool.Query(ctx,
		`SELECT a.created_at,
		        COALESCE(NULLIF(u.name, ''), u.email),
		        i.id::text,
		        p.identifier || '-' || i.sequence_id::text,
		        a.field, a.old_value::text, a.new_value::text
		   FROM issue_activities a
		   JOIN issues i ON i.id = a.issue_id
		   JOIN users u ON u.id = a.actor_id
		   JOIN projects p ON p.id = i.project_id
		  WHERE i.project_id = $1::uuid AND i.deleted_at IS NULL
		  ORDER BY a.created_at DESC, a.id DESC
		  LIMIT $2`,
		projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := []ActivityEntry{}
	for rows.Next() {
		var e ActivityEntry
		if err := rows.Scan(&e.At, &e.Actor, &e.IssueUUID, &e.IssueIdentifier, &e.Field, &e.Old, &e.New); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}
