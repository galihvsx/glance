package service

// Sub-issue parent management (C5T4): set / clear an issue's parent and
// list an issue's children.
//
// The parent relation is issues.parent_id (migration 000007); migration
// 000026 adds the self-parent CHECK backstop and the parent_id index.
// SetParent is the write contract with full validation:
//   - the parent must be a live issue in the SAME project (409 on
//     cross-project, 404 when the parent id matches nothing live);
//   - self-parent is 409 (the CHECK is the backstop);
//   - the ancestor walk is bounded (100 hops): if issueID is encountered
//     among the parent's ancestors the assignment is rejected with 409
//     cyclic_parent.
// Clearing (DELETE .../parent, or empty parent id) is always allowed.
//
// Tenancy mirrors the satellite resolvers: every op resolves workspace
// membership + project via resolveSatelliteIssue. A bad slug or
// non-member caller surfaces ErrNotFound ("workspace not found"); a bad
// project identifier surfaces ErrProjectNotFound; a missing issue
// surfaces ErrIssueNotFound. Reads need any member (guest 5+);
// mutations need member (15)+, enforced via requireSatelliteWriter.
//
// Deleting a parent leaves its children as top-level issues — the FK is
// ON DELETE SET NULL, so no service code is involved.

import (
	"context"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrIssueSelfParent is returned when an issue is set as its own
	// parent. The handler maps it to 409.
	ErrIssueSelfParent = errors.New("service: issue cannot be its own parent")
	// ErrIssueCrossProjectParent is returned when the parent issue is
	// live but belongs to a different project. The handler maps it to
	// 409.
	ErrIssueCrossProjectParent = errors.New("service: parent issue is in another project")
	// ErrIssueCyclicParent is returned when the assignment would close a
	// parent cycle (the issue appears among the parent's ancestors). The
	// handler maps it to 409.
	ErrIssueCyclicParent = errors.New("service: parent assignment would create a cycle")
)

// maxParentHops bounds the ancestor walk in checkParentCycle. 100 is far
// beyond any real hierarchy depth and keeps a pathological chain from
// turning into an unbounded N+1 scan.
const maxParentHops = 100

// IssueChild is one child summary for ?include_children=1 on the issue
// detail endpoint: identifier is the display id (e.g. ENG-12), title is
// the issue name, state is the state NAME (human-facing; the child does
// not need the full state row).
type IssueChild struct {
	UUID       string `json:"uuid"`
	Identifier string `json:"identifier"`
	Title      string `json:"title"`
	State      string `json:"state"`
}

// validateParentIssue checks that parentID may become issueID's parent
// without violating the sub-issue contract. parentID is already
// normalized (non-empty, lowercase, trimmed) and differs from issueID.
// It returns the normalized parent id on success.
//
// Checks, in order: the parent names a live issue (else ErrParentNotFound
// 404), it sits in the same project (else ErrIssueCrossProjectParent
// 409), and none of its ancestors is issueID (else ErrIssueCyclicParent
// 409). queryRower keeps the helper usable from transactions.
func validateParentIssue(ctx context.Context, q queryRower, projectID, issueID, parentID string) (string, error) {
	var proj string
	err := q.QueryRow(ctx,
		`SELECT project_id::text FROM issues
		 WHERE id = $1::uuid AND deleted_at IS NULL`,
		parentID).Scan(&proj)
	if err != nil {
		if isInvalidUUID(err) || errors.Is(err, pgx.ErrNoRows) {
			return "", ErrParentNotFound
		}
		return "", err
	}
	if proj != projectID {
		return "", ErrIssueCrossProjectParent
	}
	if err := checkParentCycle(ctx, q, issueID, parentID); err != nil {
		return "", err
	}
	return parentID, nil
}

// checkParentCycle walks parentID's ancestors (bounded by maxParentHops)
// and returns ErrIssueCyclicParent if issueID appears among them —
// making issueID a child of parentID would close a cycle. deleted_at is
// ignored on purpose: cycle detection is about the parent_id graph, not
// row visibility.
func checkParentCycle(ctx context.Context, q queryRower, issueID, parentID string) error {
	ancestor := parentID
	for i := 0; i < maxParentHops && ancestor != ""; i++ {
		var next *string
		if err := q.QueryRow(ctx,
			`SELECT parent_id::text FROM issues WHERE id = $1::uuid`,
			ancestor).Scan(&next); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				break
			}
			return err
		}
		if next == nil {
			// Reached the root of the chain — no cycle.
			break
		}
		if *next == issueID {
			return ErrIssueCyclicParent
		}
		ancestor = *next
	}
	return nil
}

// SetParent sets (or clears, when parentID is "") issueID's parent.
// parentID must be a live issue in the same project; the ancestor walk
// rejects cycles. Member (15)+.
//
// Like the PATCH parent_id path, the mutation writes a parent_id activity
// row, snapshots an issue version, bumps updated_at, and fans out to
// webhooks as issue.updated — so the audit trail is uniform no matter
// which route sets the parent. Returns the updated parent_id (nil when
// cleared).
func SetParent(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, actorID, parentID string) (*string, error) {
	issueID = normalizeIssueID(issueID)
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return nil, err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	_, projectID, role, err := resolveSatelliteIssue(ctx, tx, wsSlug, ident, issueID, actorID)
	if err != nil {
		return nil, err
	}
	if err := requireSatelliteWriter(role); err != nil {
		return nil, err
	}

	var pid *string
	if parent := normalizeIssueID(parentID); parent != "" {
		if parent == issueID {
			return nil, ErrIssueSelfParent
		}
		checked, err := validateParentIssue(ctx, tx, projectID, issueID, parent)
		if err != nil {
			return nil, err
		}
		pid = &checked
	}

	var oldParent *string
	err = tx.QueryRow(ctx,
		`SELECT parent_id::text FROM issues
		 WHERE id = $1::uuid AND project_id = $2::uuid AND deleted_at IS NULL`,
		issueID, projectID).Scan(&oldParent)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrIssueNotFound
		}
		return nil, err
	}
	if strPtrEq(oldParent, pid) {
		// No-op: already in the desired state — no activity, no version.
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return oldParent, nil
	}

	var updated *Issue
	updated, err = scanIssue(tx.QueryRow(ctx,
		`UPDATE issues SET parent_id = $2::uuid, updated_at = now()
		 WHERE id = $1::uuid AND project_id = $3::uuid AND deleted_at IS NULL
		 RETURNING `+issueColumns,
		issueID, pid, projectID))
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO issue_activities (issue_id, actor_id, field, old_value, new_value)
		 VALUES ($1::uuid, $2::uuid, 'parent_id', $3::jsonb, $4::jsonb)`,
		updated.ID, actorID, toJSONBParam(derefPtr(oldParent)), toJSONBParam(derefPtr(pid))); err != nil {
		return nil, err
	}
	updated.DisplayID = ident + "-" + strconv.Itoa(updated.SequenceID)
	if err := snapshotVersionTx(ctx, tx, updated.ID, actorID, updated); err != nil {
		return nil, err
	}
	wsID, err := workspaceIDForProjectTx(ctx, tx, projectID)
	if err != nil {
		return nil, err
	}
	if err := enqueueWebhookDeliveryTx(ctx, tx, wsID, EventIssueUpdated, map[string]any{
		"id":             updated.ID,
		"display_id":     updated.DisplayID,
		"changed_fields": []string{"parent_id"},
		"actor_id":       actorID,
	}); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return updated.ParentID, nil
}

// ListIssueChildren returns the live children of the addressed issue as
// child summaries, ordered by sequence_id (stable, matches the project's
// natural ordering). Any member (guest 5+) may read.
func ListIssueChildren(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, actorID string) ([]IssueChild, error) {
	issueID = normalizeIssueID(issueID)
	_, projectID, _, err := resolveSatelliteIssue(ctx, pool, wsSlug, identifier, issueID, actorID)
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx,
		`SELECT i.id::text, p.identifier || '-' || i.sequence_id, i.name, s.name
		 FROM issues i
		 JOIN projects p ON p.id = i.project_id
		 JOIN states s ON s.id = i.state_id
		 WHERE i.parent_id = $1::uuid
		   AND i.project_id = $2::uuid
		   AND i.deleted_at IS NULL
		 ORDER BY i.sequence_id ASC`,
		issueID, projectID)
	if err != nil {
		if isInvalidUUID(err) {
			return nil, ErrIssueNotFound
		}
		return nil, err
	}
	defer rows.Close()
	out := []IssueChild{}
	for rows.Next() {
		var c IssueChild
		if err := rows.Scan(&c.UUID, &c.Identifier, &c.Title, &c.State); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
