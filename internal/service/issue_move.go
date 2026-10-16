package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrMoveSameProject is returned when the move target is the issue's
	// own project.
	ErrMoveSameProject = errors.New("service: cannot move an issue into its own project")
	// ErrMoveIntakeIssue is returned when the issue is still sitting in
	// the intake inbox (pending/snoozed) — it triages through
	// accept/reject, not cross-project move.
	ErrMoveIntakeIssue = errors.New("service: intake issues cannot be moved between projects")
)

// MoveIssueInput carries the move target: the destination project's
// identifier (case-insensitive, like every other project route).
type MoveIssueInput struct {
	TargetProjectIdentifier string
}

// MoveMembershipsDropped counts the project-scoped memberships a move
// removed.
type MoveMembershipsDropped struct {
	Cycles   int `json:"cycles"`
	Modules  int `json:"modules"`
	Releases int `json:"releases"`
}

// MoveIssueResult is the outcome of MoveIssue. LabelsDropped is always
// non-nil so it serializes as [] rather than null.
type MoveIssueResult struct {
	Issue               *Issue                 `json:"issue"`
	NewDisplayID        string                 `json:"new_display_id"`
	StateRemapped       bool                   `json:"state_remapped"`
	LabelsDropped       []string               `json:"labels_dropped"`
	EstimateDropped     bool                   `json:"estimate_dropped"`
	MembershipsDropped  MoveMembershipsDropped `json:"memberships_dropped"`
	CustomValuesDropped int                    `json:"custom_values_dropped"`
}

// MoveIssue moves an issue (plus its whole sub-issue subtree) from one
// project to another project of the same workspace (C24T0).
//
// Auth: member (15)+ on BOTH the source and the target project; guests
// get ErrForbidden, non-members ErrNotFound (the tenancy boundary).
//
// Everything happens in ONE transaction. Semantics:
//   - The issue keeps its UUID, so every satellite keyed by issue UUID
//     (comments, attachments metadata, links, relations, reactions,
//     votes, time entries, assignees, subscribers, activities, versions,
//     external refs, reminders) moves with it untouched.
//   - Each moved issue gets a fresh display ID from the target project's
//     issue_sequences (the same atomic gapless increment CreateIssue
//     uses), and a `_moved` activity entry "moved from {OLD} to {NEW}".
//   - State: matched by name case-insensitively in the target project;
//     otherwise the target's default backlog state, with StateRemapped.
//   - Labels are workspace-scoped, so they are re-linked by name in the
//     target workspace; names with no match are dropped and reported.
//   - Estimate: matched by value in the target project's estimate
//     scale(s); otherwise nulled, with EstimateDropped when the issue
//     had one.
//   - Sub-issues: the whole subtree moves recursively; parent_id links
//     survive because UUIDs are preserved. Children are sequenced after
//     their parent (depth order, then created_at).
//   - Project-scoped data is dropped and reported: cycle/module/release
//     memberships (MembershipsDropped) and custom field values
//     (CustomValuesDropped).
//   - Issues still sitting in the intake inbox (pending/snoozed) cannot
//     be moved: ErrMoveIntakeIssue. Moving into the same project is
//     ErrMoveSameProject.
//
// Webhooks: one issue.updated fan-out per moved issue (in-tx), and one
// WS announce per moved issue after commit. No in-app notifications —
// the _moved activity entry is the record.
func MoveIssue(ctx context.Context, pool *pgxpool.Pool, wsSlug, sourceIdentifier, issueID, actorID string, in MoveIssueInput) (*MoveIssueResult, error) {
	srcIdent, err := normalizeIdentifier(sourceIdentifier)
	if err != nil {
		return nil, err
	}
	dstIdent, err := normalizeIdentifier(in.TargetProjectIdentifier)
	if err != nil {
		return nil, err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	_, srcProjectID, srcRole, err := resolveIssueProject(ctx, tx, wsSlug, srcIdent, actorID)
	if err != nil {
		return nil, err
	}
	if srcRole < RoleMember {
		return nil, ErrForbidden
	}
	wsID, dstProjectID, dstRole, err := resolveIssueProject(ctx, tx, wsSlug, dstIdent, actorID)
	if err != nil {
		return nil, err
	}
	if dstRole < RoleMember {
		return nil, ErrForbidden
	}
	if dstProjectID == srcProjectID {
		return nil, ErrMoveSameProject
	}

	// Lock the root issue: a concurrent move/delete of the same issue
	// serializes here instead of interleaving sequence assignments.
	var rootExists bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM issues
		  WHERE id = $1::uuid AND project_id = $2::uuid AND deleted_at IS NULL
		  FOR UPDATE)`,
		issueID, srcProjectID).Scan(&rootExists); err != nil {
		if isInvalidUUID(err) {
			return nil, ErrIssueNotFound
		}
		return nil, err
	}
	if !rootExists {
		return nil, ErrIssueNotFound
	}

	// Intake inbox items triage through accept/reject, not cross-project
	// move: pending or snoozed rows block the move.
	var intakeN int
	if err := tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM intake_issues
		  WHERE issue_id = $1::uuid AND status IN ($2, $3)`,
		issueID, IntakePending, IntakeSnoozed).Scan(&intakeN); err != nil {
		return nil, err
	}
	if intakeN > 0 {
		return nil, ErrMoveIntakeIssue
	}

	// Whole live subtree, root first (depth, then created_at — stable
	// sequence assignment: parent always precedes its children).
	type moveNode struct {
		id              string
		stateID         string
		estimatePointID *string
		sequenceID      int
	}
	rows, err := tx.Query(ctx,
		`WITH RECURSIVE subtree AS (
		   SELECT i.id, 0 AS depth FROM issues i
		    WHERE i.id = $1::uuid AND i.project_id = $2::uuid AND i.deleted_at IS NULL
		   UNION ALL
		   SELECT i.id, s.depth + 1 FROM issues i
		     JOIN subtree s ON i.parent_id = s.id
		    WHERE i.project_id = $2::uuid AND i.deleted_at IS NULL
		 )
		 SELECT i.id::text, i.state_id::text, i.estimate_point_id::text,
		        i.sequence_id, MIN(s.depth) AS depth
		   FROM subtree s JOIN issues i ON i.id = s.id
		  GROUP BY i.id, i.state_id, i.estimate_point_id, i.sequence_id, i.created_at
		  ORDER BY depth, i.created_at`,
		issueID, srcProjectID)
	if err != nil {
		return nil, err
	}
	var nodes []moveNode
	for rows.Next() {
		var n moveNode
		var depth int
		if err := rows.Scan(&n.id, &n.stateID, &n.estimatePointID, &n.sequenceID, &depth); err != nil {
			rows.Close()
			return nil, err
		}
		nodes = append(nodes, n)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return nil, ErrIssueNotFound
	}
	nodeIDs := make([]string, len(nodes))
	for i, n := range nodes {
		nodeIDs[i] = n.id
	}

	// queryKV scans a two-column (key, value) result into a map.
	queryKV := func(sql string, args ...any) (map[string]string, error) {
		m := map[string]string{}
		rows, err := tx.Query(ctx, sql, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var k, v string
			if err := rows.Scan(&k, &v); err != nil {
				return nil, err
			}
			m[k] = v
		}
		return m, rows.Err()
	}

	// State remap: source state name → target state id (case-insensitive),
	// else the target's default backlog state.
	srcStateNames, err := queryKV(
		`SELECT id::text, name FROM states WHERE project_id = $1::uuid`, srcProjectID)
	if err != nil {
		return nil, err
	}
	dstStateRows, err := queryKV(
		`SELECT id::text, name FROM states WHERE project_id = $1::uuid`, dstProjectID)
	if err != nil {
		return nil, err
	}
	dstStates := map[string]string{}
	for id, name := range dstStateRows {
		if _, ok := dstStates[strings.ToLower(name)]; !ok {
			dstStates[strings.ToLower(name)] = id
		}
	}
	dstBacklog, err := backlogState(ctx, tx, dstProjectID)
	if err != nil {
		return nil, err
	}
	stateRemapped := false
	newStateIDs := make([]string, len(nodes))
	for i, n := range nodes {
		name := srcStateNames[n.stateID]
		if id, ok := dstStates[strings.ToLower(name)]; ok {
			newStateIDs[i] = id
		} else {
			newStateIDs[i] = dstBacklog
			stateRemapped = true
		}
	}

	// Estimate remap: source point value → target point id by value.
	srcPointRows, err := queryKV(
		`SELECT ep.id::text, ep.value::text FROM estimate_points ep
		   JOIN issues i ON i.estimate_point_id = ep.id
		  WHERE i.id = ANY($1::uuid[])`, nodeIDs)
	if err != nil {
		return nil, err
	}
	srcPointValues := map[string]int{}
	for id, vs := range srcPointRows {
		v, convErr := strconv.Atoi(vs)
		if convErr != nil {
			return nil, convErr
		}
		srcPointValues[id] = v
	}
	dstPointRows, err := queryKV(
		`SELECT ep.id::text, ep.value::text FROM estimate_points ep
		   JOIN estimates e ON e.id = ep.estimate_id
		  WHERE e.project_id = $1::uuid`, dstProjectID)
	if err != nil {
		return nil, err
	}
	dstPointByValue := map[int]string{}
	for id, vs := range dstPointRows {
		v, convErr := strconv.Atoi(vs)
		if convErr != nil {
			return nil, convErr
		}
		if _, ok := dstPointByValue[v]; !ok {
			dstPointByValue[v] = id
		}
	}
	estimateDropped := false
	newEstimateIDs := make([]*string, len(nodes))
	for i, n := range nodes {
		if n.estimatePointID == nil {
			continue
		}
		v, ok := srcPointValues[*n.estimatePointID]
		if !ok {
			// Dangling point (deleted scale mid-flight): drop it.
			estimateDropped = true
			continue
		}
		if id, ok := dstPointByValue[v]; ok {
			newEstimateIDs[i] = &id
		} else {
			estimateDropped = true
		}
	}

	// Labels are workspace-scoped: match by name in the same workspace.
	// (Both projects share the workspace, so this only ever drops when a
	// label row vanished; the link rows themselves move untouched with the
	// issue UUID.) One query over the whole subtree instead of N.
	wsLabelRows, err := queryKV(
		`SELECT name, name FROM labels WHERE workspace_id = $1::uuid`, wsID)
	if err != nil {
		return nil, err
	}
	labelsDropped := []string{}
	rows, err = tx.Query(ctx,
		`SELECT DISTINCT l.name FROM issue_labels il
		   JOIN labels l ON l.id = il.label_id
		  WHERE il.issue_id = ANY($1::uuid[])`, nodeIDs)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return nil, err
		}
		if _, ok := wsLabelRows[name]; !ok {
			labelsDropped = append(labelsDropped, name)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Project-scoped membership + custom values: count, then drop.
	var dropped MoveMembershipsDropped
	if err := tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM cycle_issues WHERE issue_id = ANY($1::uuid[])`, nodeIDs).Scan(&dropped.Cycles); err != nil {
		return nil, err
	}
	if err := tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM module_issues WHERE issue_id = ANY($1::uuid[])`, nodeIDs).Scan(&dropped.Modules); err != nil {
		return nil, err
	}
	if err := tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM issues WHERE id = ANY($1::uuid[]) AND release_id IS NOT NULL`, nodeIDs).Scan(&dropped.Releases); err != nil {
		return nil, err
	}
	var customDropped int
	if err := tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM issue_custom_values WHERE issue_id = ANY($1::uuid[])`, nodeIDs).Scan(&customDropped); err != nil {
		return nil, err
	}

	// Move each issue: fresh target sequence, remapped state/estimate,
	// release cleared, memberships + custom values dropped, _moved
	// activity. The issue row is re-read with RETURNING for the announce
	// fan-out after commit.
	moved := make([]*Issue, len(nodes))
	for i, n := range nodes {
		var seq int
		if err := tx.QueryRow(ctx,
			`INSERT INTO issue_sequences (project_id, last_value) VALUES ($1::uuid, 1)
			 ON CONFLICT (project_id) DO UPDATE SET last_value = issue_sequences.last_value + 1
			 RETURNING last_value`,
			dstProjectID).Scan(&seq); err != nil {
			return nil, fmt.Errorf("service: increment issue sequence: %w", err)
		}
		oldDisplay := srcIdent + "-" + strconv.Itoa(n.sequenceID)
		newDisplay := dstIdent + "-" + strconv.Itoa(seq)

		iss, err := scanIssue(tx.QueryRow(ctx,
			`UPDATE issues SET project_id = $1::uuid, sequence_id = $2,
			        state_id = $3::uuid, estimate_point_id = $4::uuid,
			        release_id = NULL, updated_at = now()
			  WHERE id = $5::uuid
			  RETURNING `+issueColumns,
			dstProjectID, seq, newStateIDs[i], newEstimateIDs[i], n.id))
		if err != nil {
			return nil, err
		}
		iss.DisplayID = newDisplay
		moved[i] = iss

		if _, err := tx.Exec(ctx, `DELETE FROM cycle_issues WHERE issue_id = $1::uuid`, n.id); err != nil {
			return nil, fmt.Errorf("service: move issue drop cycle membership: %w", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM module_issues WHERE issue_id = $1::uuid`, n.id); err != nil {
			return nil, fmt.Errorf("service: move issue drop module membership: %w", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM issue_custom_values WHERE issue_id = $1::uuid`, n.id); err != nil {
			return nil, fmt.Errorf("service: move issue drop custom values: %w", err)
		}

		oldVal, _ := json.Marshal(map[string]any{"from": oldDisplay, "to": newDisplay})
		if _, err := tx.Exec(ctx,
			`INSERT INTO issue_activities (issue_id, actor_id, field, new_value)
			 VALUES ($1::uuid, $2::uuid, '_moved', $3::jsonb)`,
			n.id, actorID, string(oldVal)); err != nil {
			return nil, fmt.Errorf("service: move issue activity: %w", err)
		}

		if err := enqueueWebhookDeliveryTx(ctx, tx, wsID, EventIssueUpdated, map[string]any{
			"id":             n.id,
			"display_id":     newDisplay,
			"changed_fields": []string{"project_id", "state_id", "estimate_point_id", "release_id"},
			"actor_id":       actorID,
		}); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	for _, iss := range moved {
		announceIssueUpdated(wsSlug, dstIdent, iss.ID, iss)
	}
	// C44T1: a move always rewrites state_id — remap by state name,
	// else the target's backlog fallback — and the remap can change
	// the state GROUP, so KR progress can change. Fan out for every
	// moved issue's linked objectives (DISTINCT per objective).
	movedIDs := make([]string, 0, len(moved))
	for _, iss := range moved {
		movedIDs = append(movedIDs, iss.ID)
	}
	announceObjectivesForIssues(ctx, pool, wsSlug, movedIDs)

	return &MoveIssueResult{
		Issue:               moved[0],
		NewDisplayID:        moved[0].DisplayID,
		StateRemapped:       stateRemapped,
		LabelsDropped:       labelsDropped,
		EstimateDropped:     estimateDropped,
		MembershipsDropped:  dropped,
		CustomValuesDropped: customDropped,
	}, nil
}
