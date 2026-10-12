package service

// Convert issue <-> sub-issue (C24T1): demote a standalone issue into a
// sub-issue of a same-project parent, or promote a sub-issue back to a
// standalone issue (the Plane convert-to-sub-issue / convert-to-issue
// pair).
//
// Demote guards (checked in order):
//   - self-parent is ErrIssueSelfParent (409, shared with SetParent);
//   - the issue must have no live children — ErrConvertHasChildren
//     (400): sub-issues cannot have sub-issues, so the caller detaches
//     or moves them first;
//   - the parent must name a live issue (ErrParentNotFound, 404) in the
//     SAME project (ErrConvertCrossProject, 400) and the assignment must
//     not close a cycle (ErrIssueCyclicParent, 409) — the project and
//     cycle checks reuse validateParentIssue, the contract SetParent uses;
//   - the parent must not itself be a sub-issue (ErrConvertParentIsSubIssue,
//     400): max depth 1, matching the hierarchy rule.
//
// Promote (parentUUID nil/empty) clears the parent; it is a 200 no-op
// when the issue is already standalone. Demote is a no-op when the issue
// already has the requested parent. No-ops return converted=false and
// write nothing (no activity, no version).
//
// Both directions write "_converted" activity entries: demote writes one
// on the issue ("converted to sub-issue of {PARENT_DISPLAY}") and a
// matching one on the parent ("{CHILD_DISPLAY} converted to sub-issue");
// promote writes "promoted to standalone issue" on the issue. The
// parent_id field entry is also written, so the audit trail stays uniform
// with every other parent mutation (SetParent, PATCH parent_id).
//
// Tenancy mirrors SetParent: workspace membership + project resolution
// via resolveSatelliteIssue; mutations need member (15)+. The issue's
// version is snapshotted and an issue.updated webhook fans out with
// changed_fields ["parent_id"].

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrConvertHasChildren is returned when a demote target still has
	// live sub-issues of its own. The handler maps it to 400.
	ErrConvertHasChildren = errors.New("service: issue has sub-issues; detach or move them first")
	// ErrConvertParentIsSubIssue is returned when the requested convert
	// parent is itself a sub-issue (max depth 1). The handler maps it to
	// 400.
	ErrConvertParentIsSubIssue = errors.New("service: parent issue is itself a sub-issue")
	// ErrConvertCrossProject is returned when the requested convert
	// parent is live but belongs to a different project. The handler
	// maps it to 400.
	ErrConvertCrossProject = errors.New("service: parent issue is in another project")
)

// ConvertIssueResult is the outcome of ConvertIssue: whether the state
// changed and the issue in its post-convert state.
type ConvertIssueResult struct {
	Converted bool   `json:"converted"`
	Issue     *Issue `json:"issue"`
}

// ConvertIssue converts an issue between standalone and sub-issue in one
// transaction. parentUUID nil/empty promotes (clears the parent);
// otherwise the issue is demoted under parentUUID. Member (15)+.
func ConvertIssue(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, actorID string, parentUUID *string) (*ConvertIssueResult, error) {
	issueID = normalizeIssueID(issueID)
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return nil, err
	}
	var wantParent string
	if parentUUID != nil {
		wantParent = normalizeIssueID(*parentUUID)
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

	var oldParent *string
	var sequenceID int
	err = tx.QueryRow(ctx,
		`SELECT parent_id::text, sequence_id FROM issues
		 WHERE id = $1::uuid AND project_id = $2::uuid AND deleted_at IS NULL`,
		issueID, projectID).Scan(&oldParent, &sequenceID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrIssueNotFound
		}
		return nil, err
	}
	childDisplay := ident + "-" + strconv.Itoa(sequenceID)

	if wantParent == "" {
		// Promote: already standalone → no-op.
		if oldParent == nil {
			issue, err := fetchConvertIssue(ctx, tx, ident, issueID)
			if err != nil {
				return nil, err
			}
			if err := tx.Commit(ctx); err != nil {
				return nil, err
			}
			return &ConvertIssueResult{Converted: false, Issue: issue}, nil
		}
		return convertCommit(ctx, tx, ident, issueID, actorID, projectID, childDisplay, nil, oldParent, "")
	}

	// Demote.
	if wantParent == issueID {
		return nil, ErrIssueSelfParent
	}
	children, err := hasChildren(ctx, tx, issueID)
	if err != nil {
		return nil, err
	}
	if children {
		return nil, ErrConvertHasChildren
	}
	checked, err := validateParentIssue(ctx, tx, projectID, issueID, wantParent)
	if err != nil {
		if errors.Is(err, ErrIssueCrossProjectParent) {
			return nil, ErrConvertCrossProject
		}
		return nil, err
	}
	var parentParent *string
	var parentSequence int
	if err := tx.QueryRow(ctx,
		`SELECT parent_id::text, sequence_id FROM issues
		 WHERE id = $1::uuid AND deleted_at IS NULL`,
		checked).Scan(&parentParent, &parentSequence); err != nil {
		// validateParentIssue already proved the parent is live; this
		// cannot be ErrNoRows in practice.
		return nil, err
	}
	if parentParent != nil {
		return nil, ErrConvertParentIsSubIssue
	}
	parentDisplay := ident + "-" + strconv.Itoa(parentSequence)

	if strPtrEq(oldParent, &checked) {
		// No-op: already a sub-issue of the requested parent.
		issue, err := fetchConvertIssue(ctx, tx, ident, issueID)
		if err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return &ConvertIssueResult{Converted: false, Issue: issue}, nil
	}

	return convertCommit(ctx, tx, ident, issueID, actorID, projectID, childDisplay, &checked, oldParent, parentDisplay)
}

// hasChildren reports whether issueID has any live sub-issues.
func hasChildren(ctx context.Context, q queryRower, issueID string) (bool, error) {
	var n int
	if err := q.QueryRow(ctx,
		`SELECT count(*) FROM issues WHERE parent_id = $1::uuid AND deleted_at IS NULL`,
		issueID).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// fetchConvertIssue re-reads the full issue row for the no-op response
// shape (the issue with its display id, no mutation applied).
func fetchConvertIssue(ctx context.Context, q queryRower, ident, issueID string) (*Issue, error) {
	issue, err := scanIssue(q.QueryRow(ctx,
		`SELECT `+issueColumns+` FROM issues WHERE id = $1::uuid AND deleted_at IS NULL`,
		issueID))
	if err != nil {
		return nil, err
	}
	issue.DisplayID = ident + "-" + strconv.Itoa(issue.SequenceID)
	return issue, nil
}

// convertCommit applies the parent_id change (pid nil = promote), writes
// the parent_id field activity plus the "_converted" entries, snapshots
// an issue version, fans out the issue.updated webhook, and returns the
// converted result. parentDisplay is the new parent's display id on
// demote ("" on promote).
func convertCommit(ctx context.Context, tx pgx.Tx, ident, issueID, actorID, projectID, childDisplay string, pid, oldParent *string, parentDisplay string) (*ConvertIssueResult, error) {
	updated, err := scanIssue(tx.QueryRow(ctx,
		`UPDATE issues SET parent_id = $2::uuid, updated_at = now()
		 WHERE id = $1::uuid AND project_id = $3::uuid AND deleted_at IS NULL
		 RETURNING `+issueColumns,
		issueID, pid, projectID))
	if err != nil {
		return nil, err
	}
	updated.DisplayID = ident + "-" + strconv.Itoa(updated.SequenceID)

	if _, err := tx.Exec(ctx,
		`INSERT INTO issue_activities (issue_id, actor_id, field, old_value, new_value)
		 VALUES ($1::uuid, $2::uuid, 'parent_id', $3::jsonb, $4::jsonb)`,
		updated.ID, actorID, toJSONBParam(derefPtr(oldParent)), toJSONBParam(derefPtr(pid))); err != nil {
		return nil, err
	}

	var convertedVal string
	if pid != nil {
		convertedVal = mustMarshalJSON(map[string]any{
			"action": "to_sub_issue",
			"parent": parentDisplay,
		})
	} else {
		convertedVal = mustMarshalJSON(map[string]any{"action": "promoted"})
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO issue_activities (issue_id, actor_id, field, new_value)
		 VALUES ($1::uuid, $2::uuid, '_converted', $3::jsonb)`,
		updated.ID, actorID, convertedVal); err != nil {
		return nil, err
	}

	if pid != nil {
		childVal := mustMarshalJSON(map[string]any{
			"action": "child_added",
			"child":  childDisplay,
		})
		if _, err := tx.Exec(ctx,
			`INSERT INTO issue_activities (issue_id, actor_id, field, new_value)
			 VALUES ($1::uuid, $2::uuid, '_converted', $3::jsonb)`,
			*pid, actorID, childVal); err != nil {
			return nil, err
		}
	}

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
	return &ConvertIssueResult{Converted: true, Issue: updated}, nil
}

// mustMarshalJSON renders a small activity payload; it never fails for
// the string maps used here.
func mustMarshalJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
