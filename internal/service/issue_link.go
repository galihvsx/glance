package service

// Issue links (C4T0): directed dependency edges between issues — the
// Gantt backend contract.
//
// A link is "issue A (kind) issue B", stored as ONE directed row
// (issue_id → target_issue_id). The default kind is 'blocks'; "blocked
// by" is the same edge addressed from the other side, never a separate
// kind. The vocabulary is intentionally tiny at v0.3.0: blocks,
// relates_to, duplicates.
//
// Tenancy (mirrors satellites): every op resolves workspace membership
// + project via resolveSatelliteIssue. A bad slug or non-member caller
// surfaces ErrNotFound ("workspace not found"); a bad project identifier
// surfaces ErrProjectNotFound; a missing/mismatched issue surfaces
// ErrIssueNotFound. Distinct 404s on purpose (Task 11 ruling).
//
// Roles: any member (guest 5+) may read; mutations need member (15)+.
//
// Honest scope, v0.3.0: we reject direct self-loops and duplicates.
// There is NO cycle detection — A→B→C→A is accepted. Do not claim
// otherwise in docs or UI copy. (The pre-existing issue_relations table
// is the user-facing relation graph with its own type vocabulary;
// issue_links is the lean dependency-edge contract the Gantt consumes.)

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrIssueLinkNotFound is returned when the link id matches nothing
	// in the addressed issue (or the project).
	ErrIssueLinkNotFound = errors.New("service: issue link not found")
	// ErrIssueLinkConflict is returned on a duplicate edge: the unique
	// (issue_id, target_issue_id) is the backstop, so a racing double
	// create serializes into a conflict instead of two rows. The handler
	// maps it to 409.
	ErrIssueLinkConflict = errors.New("service: issue link already exists")
	// ErrIssueLinkSelf is returned when target_issue_id == issue_id.
	// The handler maps it to 409.
	ErrIssueLinkSelf = errors.New("service: issue link cannot target itself")
	// ErrIssueLinkCrossProject is returned when the target issue is live
	// but belongs to a different project. The handler maps it to 409.
	ErrIssueLinkCrossProject = errors.New("service: issue link target is in another project")
	// ErrInvalidIssueLink is returned for a malformed kind, a malformed
	// link id, or a malformed issue id in the batch fetch. The handler
	// maps it to 400 bad_request.
	ErrInvalidIssueLink = errors.New("service: invalid issue link")
	// ErrOpenBlockers is returned when an issue is moved to a
	// completed-group state while it has open 'blocks' inbound edges
	// (issues blocking it that are not themselves completed or archived).
	// The typed *OpenBlockersError carries the blocker display IDs; the
	// handler maps it to 409 with code "open_blockers".
	ErrOpenBlockers = errors.New("service: issue has open blockers")
)

// issueLinkKinds is the strict vocabulary enforced by CreateIssueLink.
// Empty input means the DB default 'blocks'.
var issueLinkKinds = map[string]bool{
	"blocks":     true,
	"relates_to": true,
	"duplicates": true,
}

// IssueLink is one directed dependency edge.
type IssueLink struct {
	ID            string    `json:"id"`
	IssueID       string    `json:"issue_id"`
	TargetIssueID string    `json:"target_issue_id"`
	Kind          string    `json:"kind"`
	CreatedAt     time.Time `json:"created_at"`
	// Direction is derived, never stored: "outgoing" when the addressed
	// issue is the source, "incoming" when it is the target. Set by
	// ListIssueLinks only; ListIssueLinksForIssues leaves it "" because
	// the batch caller holds the whole issue set and derives direction
	// from the endpoints itself.
	Direction string `json:"direction,omitempty"`
}

// normalizeIssueLinkKind validates the kind: trimmed, case-folded, strict
// vocabulary match (C6T5: "BLOCKS" stores as "blocks"). Empty → ""
// (caller lets the DB default 'blocks' apply).
func normalizeIssueLinkKind(kind string) (string, error) {
	k := strings.ToLower(strings.TrimSpace(kind))
	if k == "" {
		return "", nil
	}
	if !issueLinkKinds[k] {
		return "", ErrInvalidIssueLink
	}
	return k, nil
}

// OpenBlockersError is returned when a move into a completed state is
// rejected by the blocker guard (C16T3). Blockers holds the display IDs
// ({IDENTIFIER}-{sequence}) of the open blockers, so the API can name
// them in the 409 details.
type OpenBlockersError struct {
	Blockers []string
}

func (e *OpenBlockersError) Error() string {
	return fmt.Sprintf("service: issue has open blockers: %s",
		strings.Join(e.Blockers, ", "))
}

func (e *OpenBlockersError) Unwrap() error { return ErrOpenBlockers }

// assertNoOpenBlockersTx is the shared blocker guard (C16T3): moving an
// issue into a completed-group state is rejected while open 'blocks'
// inbound edges point at it. An inbound edge (X → issue, kind 'blocks')
// is OPEN when X is a live, unarchived issue of the same project whose
// state's group is NOT 'completed'. A blocker that is completed or
// archived is not open; moves into non-completed states never check.
// Called by updateIssueTx (single PATCH, bulk-update, bulk-set) and by
// automationSetStateTx — the automation failure surfaces on the run row
// (ok:false), never as a silent skip.
func assertNoOpenBlockersTx(ctx context.Context, tx pgx.Tx, projectID, ident, issueID, stateID string) error {
	var group string
	if err := tx.QueryRow(ctx,
		`SELECT "group" FROM states WHERE id = $1::uuid`, stateID).Scan(&group); err != nil {
		return err
	}
	if group != "completed" {
		return nil
	}
	rows, err := tx.Query(ctx,
		`SELECT $3 || '-' || i.sequence_id::text
		 FROM issue_links l
		 JOIN issues i ON i.id = l.issue_id
		 JOIN states s ON s.id = i.state_id
		 WHERE l.target_issue_id = $1::uuid
		   AND l.kind = 'blocks'
		   AND i.project_id = $2::uuid
		   AND i.deleted_at IS NULL
		   AND i.archived_at IS NULL
		   AND s."group" != 'completed'
		 ORDER BY i.sequence_id`,
		issueID, projectID, ident)
	if err != nil {
		return err
	}
	defer rows.Close()
	var blockers []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return err
		}
		blockers = append(blockers, d)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(blockers) > 0 {
		return &OpenBlockersError{Blockers: blockers}
	}
	return nil
}

// scanIssueLink scans a full link row (no direction).
func scanIssueLink(row pgx.Row) (IssueLink, error) {
	var l IssueLink
	err := row.Scan(&l.ID, &l.IssueID, &l.TargetIssueID, &l.Kind, &l.CreatedAt)
	return l, err
}

// ListIssueLinks returns every edge touching the issue: outgoing edges
// stored from this issue plus incoming edges stored from the other
// side, each labeled with a derived direction. Any member (guest 5+)
// may read.
func ListIssueLinks(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, actorID string) ([]IssueLink, error) {
	issueID = normalizeIssueID(issueID)
	_, projectID, _, err := resolveSatelliteIssue(ctx, pool, wsSlug, identifier, issueID, actorID)
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx,
		`SELECT l.id::text, l.issue_id::text, l.target_issue_id::text, l.kind, l.created_at
		 FROM issue_links l
		 JOIN issues o ON o.id = CASE WHEN l.issue_id = $1::uuid
		                              THEN l.target_issue_id ELSE l.issue_id END
		 WHERE (l.issue_id = $1::uuid OR l.target_issue_id = $1::uuid)
		   AND o.deleted_at IS NULL AND o.project_id = $2::uuid
		 ORDER BY l.created_at ASC`,
		issueID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IssueLink{}
	for rows.Next() {
		l, err := scanIssueLink(rows)
		if err != nil {
			return nil, err
		}
		if l.IssueID == issueID {
			l.Direction = "outgoing"
		} else {
			l.Direction = "incoming"
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// CreateIssueLink links issueID → targetID with a kind (default
// 'blocks'). The target must be a live issue in the same project —
// verified for BOTH issues in ONE query (id + project_id fetched for
// the pair, then checked in Go). Member (15)+.
func CreateIssueLink(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, actorID, targetID, kind string) (*IssueLink, error) {
	issueID = normalizeIssueID(issueID)
	_, projectID, role, err := resolveSatelliteIssue(ctx, pool, wsSlug, identifier, issueID, actorID)
	if err != nil {
		return nil, err
	}
	if err := requireSatelliteWriter(role); err != nil {
		return nil, err
	}
	target := normalizeIssueID(targetID)
	if target == "" || target == issueID {
		return nil, ErrIssueLinkSelf
	}
	k, err := normalizeIssueLinkKind(kind)
	if err != nil {
		return nil, err
	}

	// Verify the target in a single query: fetch (id, project_id) for the
	// pair. The source issue was just resolved, so it must come back;
	// the target must come back AND carry the same project_id.
	rows, err := pool.Query(ctx,
		`SELECT id::text, project_id::text FROM issues
		 WHERE id = ANY($1::uuid[]) AND deleted_at IS NULL`,
		[]string{issueID, target})
	if err != nil {
		if isInvalidUUID(err) {
			return nil, ErrIssueNotFound
		}
		return nil, err
	}
	var targetProject string
	foundTarget := false
	for rows.Next() {
		var id, proj string
		if err := rows.Scan(&id, &proj); err != nil {
			rows.Close()
			return nil, err
		}
		if id == target {
			foundTarget = true
			targetProject = proj
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		if isInvalidUUID(err) {
			return nil, ErrIssueNotFound
		}
		return nil, err
	}
	rows.Close()
	if !foundTarget {
		// No live issue by that id anywhere: 404, like GetIssue.
		return nil, ErrIssueNotFound
	}
	if targetProject != projectID {
		return nil, ErrIssueLinkCrossProject
	}

	// Insert; the unique (issue_id, target_issue_id) is the race backstop.
	cols := `id::text, issue_id::text, target_issue_id::text, kind, created_at`
	l, err := scanIssueLink(pool.QueryRow(ctx,
		`INSERT INTO issue_links (issue_id, target_issue_id, kind)
		 VALUES ($1::uuid, $2::uuid, COALESCE(NULLIF($3, ''), 'blocks'))
		 RETURNING `+cols,
		issueID, target, k))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrIssueLinkConflict
		}
		return nil, err
	}
	l.Direction = "outgoing"
	return &l, nil
}

// DeleteIssueLink removes the addressed link. The link is scoped to the
// addressed issue: the row must touch issueID (either endpoint) AND its
// source issue must live in the caller's project. A link id that belongs
// to another issue or project is ErrIssueLinkNotFound from this route,
// never a cross-tenant delete. Member (15)+.
func DeleteIssueLink(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, actorID, linkID string) error {
	issueID = normalizeIssueID(issueID)
	_, projectID, role, err := resolveSatelliteIssue(ctx, pool, wsSlug, identifier, issueID, actorID)
	if err != nil {
		return err
	}
	if err := requireSatelliteWriter(role); err != nil {
		return err
	}
	if !isUUIDFormat(linkID) {
		// Malformed ids match nothing — 400, not a 500 from ::uuid.
		return ErrInvalidIssueLink
	}
	tag, err := pool.Exec(ctx,
		`DELETE FROM issue_links l
		 USING issues i
		 WHERE l.id = $1::uuid
		   AND l.issue_id = i.id
		   AND i.project_id = $2::uuid
		   AND i.deleted_at IS NULL
		   AND (l.issue_id = $3::uuid OR l.target_issue_id = $3::uuid)`,
		linkID, projectID, issueID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrIssueLinkNotFound
	}
	return nil
}

// ListIssueLinksForIssues fetches every edge touching ANY of the given
// issue ids in ONE query — the Gantt consumption contract. The caller
// passes the ids it just listed (e.g. the ?start_after/?start_before
// window); both directions are included, so the client can draw edges
// between visible issues without N+1 lookups.
//
// Project-scoped (both endpoints must be live issues in the caller's
// project) and dead-endpoint filtered: an edge whose other end is
// soft-deleted or in another project never appears. Any member
// (guest 5+) may read.
//
// Edges are returned in canonical stored form (issue_id →
// target_issue_id); Direction is left "" — the batch caller derives it
// from the endpoints.
func ListIssueLinksForIssues(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string, issueIDs []string) ([]IssueLink, error) {
	projectID, _, err := resolveModuleProject(ctx, pool, wsSlug, identifier, actorID, false)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(issueIDs))
	for _, id := range issueIDs {
		id = normalizeIssueID(id)
		if id == "" {
			continue
		}
		if !isUUIDFormat(id) {
			return nil, ErrInvalidIssueLink
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return []IssueLink{}, nil
	}
	rows, err := pool.Query(ctx,
		`SELECT l.id::text, l.issue_id::text, l.target_issue_id::text, l.kind, l.created_at
		 FROM issue_links l
		 JOIN issues src ON src.id = l.issue_id AND src.deleted_at IS NULL
		 JOIN issues tgt ON tgt.id = l.target_issue_id AND tgt.deleted_at IS NULL
		 WHERE src.project_id = $1::uuid AND tgt.project_id = $1::uuid
		   AND (l.issue_id = ANY($2::uuid[]) OR l.target_issue_id = ANY($2::uuid[]))
		 ORDER BY l.created_at ASC`,
		projectID, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IssueLink{}
	for rows.Next() {
		l, err := scanIssueLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
