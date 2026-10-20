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
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrInvalidActivityLimit is returned when the activity feed `limit`
// query param is not an integer in 1..200. The handler maps it to 400
// bad_request.
var ErrInvalidActivityLimit = errors.New("service: invalid activity limit")

// ErrInvalidActivityCursor is returned when the workspace activity feed
// `before` cursor cannot be decoded. The handler maps it to 400
// bad_request.
var ErrInvalidActivityCursor = errors.New("service: invalid activity cursor")

// ErrInvalidActivityActor is returned when the workspace activity feed
// `actor` filter is present but not a UUID. The handler maps it to 400
// bad_request.
var ErrInvalidActivityActor = errors.New("service: invalid activity actor: not a UUID")

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

// Workspace activity feed (C17T4): the project activity rows aggregated
// workspace-wide, newest first, with cursor pagination and an optional
// field-type filter.
//
// Conventions (mirror the project feed above):
//   - Tenancy: the caller must be a workspace member (15)+. A bad slug or
//     a non-member caller surfaces ErrNotFound; guests (5) surface
//     ErrForbidden. Roles are workspace-wide, so member(15)+ sees every
//     project in the workspace and nothing leaks to outsiders.
//   - Scope is LIVE issues (deleted_at IS NULL), like the project feed.
//   - Single query, no N+1: issue_activities joins issues (workspace
//     scope), projects (identifier + name for the badge), and users
//     (actor display name).
//   - Cursor pagination: `before` is an opaque cursor encoding the
//     (created_at, id) of the page's last row; the next page returns rows
//     strictly before it under the (created_at DESC, id DESC) order.
//     Ties on created_at are broken deterministically by the row id, so
//     pagination never skips or repeats rows.
//   - Type filter: `types` is a list of issue_activities.field values
//     (e.g. "state_id", "priority", "_created"); empty/blank entries are
//     ignored, the rest are deduplicated.
//   - Actor filter (C80T0): `actorFilter` is a member's user UUID,
//     matching issue_activities.actor_id; "" disables the filter.

// WorkspaceActivityEntry is one row of the workspace activity feed: a
// project activity row plus the owning project (identifier for links,
// name for the badge).
type WorkspaceActivityEntry struct {
	ActivityEntry
	ProjectIdentifier string `json:"project_identifier"`
	ProjectName       string `json:"project_name"`
	// activityRowID is the issue_activities row id, used only to build
	// the pagination cursor; it is never serialized.
	activityRowID string
}

// WorkspaceActivityPage is one page of the workspace feed: up to limit
// entries plus the opaque `before` cursor for the next page ("" when the
// feed is exhausted).
type WorkspaceActivityPage struct {
	Entries    []WorkspaceActivityEntry
	NextCursor string
}

// encodeActivityCursor builds the opaque `before` cursor for the next
// page from the last row's (created_at, id).
func encodeActivityCursor(at time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString(
		[]byte(at.UTC().Format(time.RFC3339Nano) + "|" + id))
}

// decodeActivityCursor parses a `before` cursor back into (created_at,
// id). Any malformed input is ErrInvalidActivityCursor (400).
func decodeActivityCursor(cursor string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", ErrInvalidActivityCursor
	}
	ts, id, ok := strings.Cut(string(raw), "|")
	if !ok || id == "" || !isUUIDFormat(id) {
		return time.Time{}, "", ErrInvalidActivityCursor
	}
	at, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return time.Time{}, "", ErrInvalidActivityCursor
	}
	return at, id, nil
}

// cleanActivityTypes trims, drops blanks, and deduplicates the requested
// type filter values.
func cleanActivityTypes(types []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, t := range types {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// GetWorkspaceActivity returns the newest-first activity feed across all
// projects of the workspace the caller is a member (15)+ of. `before` is
// an opaque cursor for the next page ("" for the first); `types`
// optionally filters to issue_activities.field values; `actorFilter`
// optionally filters to rows written by one member (their user UUID —
// "" disables the filter). At most limit entries are returned;
// NextCursor is "" when the feed is exhausted. A well-formed actorFilter
// that matches no member of the workspace is ErrMemberNotFound (404);
// a malformed one is ErrInvalidActivityActor (400).
func GetWorkspaceActivity(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID string, limit int, before string, types []string, actorFilter string) (WorkspaceActivityPage, error) {
	if limit < 1 || limit > ActivityMaxLimit {
		return WorkspaceActivityPage{}, ErrInvalidActivityLimit
	}
	// C80T0: reject a malformed actor filter before any DB touch.
	if actorFilter != "" && !isUUIDFormat(actorFilter) {
		return WorkspaceActivityPage{}, ErrInvalidActivityActor
	}
	var beforeAt time.Time
	var beforeID string
	if before != "" {
		var err error
		beforeAt, beforeID, err = decodeActivityCursor(before)
		if err != nil {
			return WorkspaceActivityPage{}, err
		}
	}
	cleanTypes := cleanActivityTypes(types)

	wsID, role, err := workspaceIDForActor(pool.QueryRow(ctx,
		`SELECT w.id::text, m.role
		   FROM workspaces w
		   JOIN workspace_members m ON m.workspace_id = w.id
		  WHERE w.slug = $1 AND m.user_id = $2::uuid`,
		wsSlug, actorID))
	if err != nil {
		return WorkspaceActivityPage{}, err
	}
	if role < RoleMember {
		return WorkspaceActivityPage{}, ErrForbidden
	}
	// C80T0: resolve the actor filter to a real member of THIS workspace.
	// The caller's own role is checked above, so guests still get 403 and
	// outsiders 404 — the filter never widens the feed's authZ. A UUID
	// that matches no member (or a member of another workspace) is 404
	// via ErrMemberNotFound, the same sentinel the members endpoints use.
	if actorFilter != "" {
		var isMember bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM workspace_members
			          WHERE workspace_id = $1::uuid AND user_id = $2::uuid)`,
			wsID, actorFilter).Scan(&isMember); err != nil {
			return WorkspaceActivityPage{}, err
		}
		if !isMember {
			return WorkspaceActivityPage{}, ErrMemberNotFound
		}
	}

	var sb strings.Builder
	args := []any{wsID}
	sb.WriteString(`SELECT a.created_at,
	        COALESCE(NULLIF(u.name, ''), u.email),
	        i.id::text,
	        p.identifier || '-' || i.sequence_id::text,
	        a.field, a.old_value::text, a.new_value::text,
	        p.identifier, p.name, a.id::text
	   FROM issue_activities a
	   JOIN issues i ON i.id = a.issue_id
	   JOIN projects p ON p.id = i.project_id
	   JOIN users u ON u.id = a.actor_id
	  WHERE p.workspace_id = $1::uuid AND i.deleted_at IS NULL`)
	if len(cleanTypes) > 0 {
		args = append(args, cleanTypes)
		fmt.Fprintf(&sb, " AND a.field = ANY($%d::text[])", len(args))
	}
	if actorFilter != "" {
		args = append(args, actorFilter)
		fmt.Fprintf(&sb, " AND a.actor_id = $%d::uuid", len(args))
	}
	if before != "" {
		args = append(args, beforeAt, beforeID)
		fmt.Fprintf(&sb, " AND (a.created_at, a.id) < ($%d::timestamptz, $%d::uuid)", len(args)-1, len(args))
	}
	args = append(args, limit+1)
	fmt.Fprintf(&sb, " ORDER BY a.created_at DESC, a.id DESC LIMIT $%d", len(args))

	rows, err := pool.Query(ctx, sb.String(), args...)
	if err != nil {
		return WorkspaceActivityPage{}, err
	}
	defer rows.Close()

	entries := []WorkspaceActivityEntry{}
	for rows.Next() {
		var e WorkspaceActivityEntry
		var rowID string
		if err := rows.Scan(&e.At, &e.Actor, &e.IssueUUID, &e.IssueIdentifier,
			&e.Field, &e.Old, &e.New, &e.ProjectIdentifier, &e.ProjectName, &rowID); err != nil {
			return WorkspaceActivityPage{}, err
		}
		e.activityRowID = rowID
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return WorkspaceActivityPage{}, err
	}

	page := WorkspaceActivityPage{Entries: entries}
	if len(entries) > limit {
		last := entries[limit-1]
		page.NextCursor = encodeActivityCursor(last.At, last.activityRowID)
		page.Entries = entries[:limit]
	}
	return page, nil
}
