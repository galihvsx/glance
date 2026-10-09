package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CloneIssue creates a new issue in the SAME project as the source issue.
//
// The row itself goes through CreateIssue, so the clone gets the exact
// same validation and normalization as a hand-made issue (name required,
// priority range, date range, state/parent/estimate-point project
// scoping, atomic gapless sequence increment, _created activity row,
// version-1 snapshot, issue.created webhook, WS announce). Only afterwards
// are the source's label links and custom field values duplicated in a
// second transaction as raw row copies.
//
// Copied: name, description, priority, state (the clone lands in the same
// column, not backlog), parent (only when set — the clone becomes a
// sibling of the source; the source's own sub-issues are NOT copied),
// estimate point, start/target dates, is_draft (a draft clones into a
// draft), labels, custom field values.
//
// NOT copied: comments, watchers/subscribers, assignees, activity rows,
// attachments (files are not duplicated), intake-inbox membership.
// An archived source clones into a live issue (archival is a lifecycle
// state, not content); soft-deleted sources are ErrIssueNotFound like
// any other missing issue.
//
// The clone gets a fresh sequence_id from the project's issue_sequences
// counter and created_by = the cloning user, so it is fully attributable
// as a new issue.
//
// Notifications: CreateIssue fires no in-app notifications on create
// (matrix, Task 26 — assignees are notified when assigned, watchers on
// the events that follow), and the label/custom-value copy deliberately
// bypasses the assign/set endpoints so no per-row notifications or WS
// announcements fire for the duplicated data. The issue.created webhook
// fires once, because the clone IS a create.
//
// Role gate mirrors CreateIssue: member (15)+; guests get ErrForbidden,
// non-members ErrNotFound. Project scoping is enforced by resolving the
// source issue inside the requested project, so a clone can never pull
// data across projects or workspaces.
func CloneIssue(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, actorID string) (*Issue, error) {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return nil, err
	}
	_, projectID, role, err := resolveIssueProject(ctx, pool, wsSlug, ident, actorID)
	if err != nil {
		return nil, err
	}
	if role < RoleMember {
		return nil, ErrForbidden
	}

	// Project-scoped source read: deleted issues and issues of any other
	// project/workspace resolve to ErrIssueNotFound, never leak.
	src, err := scanIssue(pool.QueryRow(ctx,
		`SELECT `+issueColumns+` FROM issues
		  WHERE id = $1::uuid AND project_id = $2::uuid AND deleted_at IS NULL`,
		issueID, projectID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return nil, ErrIssueNotFound
		}
		return nil, err
	}

	priority := src.Priority
	stateID := src.StateID
	clone, err := CreateIssue(ctx, pool, wsSlug, ident, actorID, CreateIssueInput{
		Name:            src.Name,
		Description:     src.Description,
		Priority:        &priority,
		StateID:         &stateID,
		ParentID:        src.ParentID,
		StartDate:       src.StartDate,
		TargetDate:      src.TargetDate,
		EstimatePointID: src.EstimatePointID,
		IsDraft:         src.IsDraft,
	})
	if err != nil {
		return nil, err
	}

	// Labels are workspace-scoped and custom fields project-scoped; both
	// the source and the clone live in the same project/workspace by
	// construction, so every copied row references a definition that is
	// valid here. Raw INSERT ... SELECT keeps the copy in one statement
	// per table instead of N validated endpoint calls.
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx,
		`INSERT INTO issue_labels (issue_id, label_id)
		  SELECT $1::uuid, label_id FROM issue_labels WHERE issue_id = $2::uuid
		 ON CONFLICT DO NOTHING`,
		clone.ID, src.ID); err != nil {
		return nil, fmt.Errorf("service: clone issue labels: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO issue_custom_values
		     (issue_id, field_id, value_text, value_number, value_date, value_bool)
		  SELECT $1::uuid, field_id, value_text, value_number, value_date, value_bool
		    FROM issue_custom_values WHERE issue_id = $2::uuid
		 ON CONFLICT DO NOTHING`,
		clone.ID, src.ID); err != nil {
		return nil, fmt.Errorf("service: clone issue custom values: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return clone, nil
}
