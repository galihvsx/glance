package service

// Workflow transition controls (C68T0): per-project state-transition
// allowlist. Plane paywalls this (Business/Enterprise Grid); glance
// ships it free per the fully-free thesis.
//
// Model: ONE allowlist per project — each row permits one directed
// state move (from_state_id → to_state_id). A project with ZERO rule
// rows is unrestricted (fully backwards compatible: existing projects
// behave exactly as today until an admin configures rules).
//
// Enforcement is user-initiated paths only (UpdateIssue,
// BulkUpdateIssues, BulkSetIssues, MoveIssue); system/integrity paths
// keep their existing guarantees and bypass rules — documented at each
// call site: DeleteState reassign (name-remap else backlog fallback),
// RestoreIssueVersion, intake accept, ConvertIssue, recurring issue
// creation, automation set_state firings. Rules govern human workflow
// movement, never system integrity.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// workflowQuerier abstracts *pgxpool.Pool and pgx.Tx for the rule/state
// list queries below (queryRower only offers QueryRow).
type workflowQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

var (
	// ErrStateTransitionNotAllowed is returned when a state move is not
	// a listed (from → to) pair in a project that defines transition
	// rules. The typed *StateTransitionError carries the allowed
	// next-state IDs; the handler maps it to 409 with code
	// "state_transition_not_allowed".
	ErrStateTransitionNotAllowed = errors.New("service: state transition not allowed by project workflow")
	// ErrWorkflowSelfTransition is returned by SetWorkflow when a rule
	// has from_state_id == to_state_id (400-class).
	ErrWorkflowSelfTransition = errors.New("service: workflow transition must move to a different state")
)

// WorkflowTransition is one allowed directed state move.
type WorkflowTransition struct {
	FromStateID string `json:"from_state_id"`
	ToStateID   string `json:"to_state_id"`
}

// Workflow is the project's transition governance: its states plus the
// allowlist. Transitions is always non-nil (empty = unrestricted).
type Workflow struct {
	States      []State              `json:"states"`
	Transitions []WorkflowTransition `json:"transitions"`
}

// StateTransitionError is returned when a move violates the project's
// transition rules. Allowed holds the permitted next-state IDs from
// FromStateID (non-nil, possibly empty), so the API can name them in
// the 409 details.
type StateTransitionError struct {
	FromStateID string
	ToStateID   string
	Allowed     []string
}

func (e *StateTransitionError) Error() string {
	return fmt.Sprintf("service: state transition %s → %s not allowed by project workflow",
		e.FromStateID, e.ToStateID)
}

func (e *StateTransitionError) Unwrap() error { return ErrStateTransitionNotAllowed }

// GetWorkflow returns the project's states (sequence order) plus its
// transition rules. Any workspace member may read (guests included);
// non-members get ErrNotFound via resolveStateScope.
func GetWorkflow(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string) (*Workflow, error) {
	_, projectID, _, err := resolveStateScope(ctx, pool, wsSlug, identifier, actorID)
	if err != nil {
		return nil, err
	}
	states, transitions, err := loadWorkflow(ctx, pool, projectID)
	if err != nil {
		return nil, err
	}
	return &Workflow{States: states, Transitions: transitions}, nil
}

// SetWorkflow replaces the project's transition rules wholesale
// (delete + insert in one tx). Member (15)+, mirroring the states API
// auth convention. Validation: every endpoint must be a state of this
// project (else ErrInvalidState), no from==to self-loops (else
// ErrWorkflowSelfTransition); duplicate pairs are deduped.
func SetWorkflow(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string, rules []WorkflowTransition) (*Workflow, error) {
	_, projectID, role, err := resolveStateScope(ctx, pool, wsSlug, identifier, actorID)
	if err != nil {
		return nil, err
	}
	if role < RoleMember {
		return nil, ErrForbidden
	}

	// Canonicalize + validate against the project's state set (one query).
	stateIDs := map[string]bool{}
	rows, err := pool.Query(ctx, `SELECT id::text FROM states WHERE project_id = $1::uuid`, projectID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		stateIDs[strings.ToLower(id)] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	seen := map[[2]string]bool{}
	uniq := make([]WorkflowTransition, 0, len(rules))
	for _, r := range rules {
		from := strings.ToLower(strings.TrimSpace(r.FromStateID))
		to := strings.ToLower(strings.TrimSpace(r.ToStateID))
		if !stateIDs[from] || !stateIDs[to] {
			return nil, ErrInvalidState
		}
		if from == to {
			return nil, ErrWorkflowSelfTransition
		}
		key := [2]string{from, to}
		if seen[key] {
			continue
		}
		seen[key] = true
		uniq = append(uniq, WorkflowTransition{FromStateID: from, ToStateID: to})
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx,
		`DELETE FROM project_state_transitions WHERE project_id = $1::uuid`, projectID); err != nil {
		return nil, err
	}
	for _, r := range uniq {
		if _, err := tx.Exec(ctx,
			`INSERT INTO project_state_transitions (from_state_id, to_state_id, project_id)
			 VALUES ($1::uuid, $2::uuid, $3::uuid)`,
			r.FromStateID, r.ToStateID, projectID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	states, transitions, err := loadWorkflow(ctx, pool, projectID)
	if err != nil {
		return nil, err
	}
	return &Workflow{States: states, Transitions: transitions}, nil
}

// loadWorkflow reads the states (sequence order) and the rule set of a
// project. Both slices are non-nil so the API serializes [] not null.
func loadWorkflow(ctx context.Context, q workflowQuerier, projectID string) ([]State, []WorkflowTransition, error) {
	states := []State{}
	rows, err := q.Query(ctx,
		`SELECT s.id::text, s.project_id::text, s.name, s."group", s.color,
		        s.sequence, s.created_at, s.updated_at
		 FROM states s
		 WHERE s.project_id = $1::uuid
		 ORDER BY s.sequence`, projectID)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var s State
		if err := rows.Scan(&s.ID, &s.ProjectID, &s.Name, &s.Group, &s.Color,
			&s.Sequence, &s.CreatedAt, &s.UpdatedAt); err != nil {
			rows.Close()
			return nil, nil, err
		}
		states = append(states, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	transitions := []WorkflowTransition{}
	trows, err := q.Query(ctx,
		`SELECT from_state_id::text, to_state_id::text
		 FROM project_state_transitions
		 WHERE project_id = $1::uuid
		 ORDER BY from_state_id, to_state_id`, projectID)
	if err != nil {
		return nil, nil, err
	}
	for trows.Next() {
		var t WorkflowTransition
		if err := trows.Scan(&t.FromStateID, &t.ToStateID); err != nil {
			trows.Close()
			return nil, nil, err
		}
		transitions = append(transitions, t)
	}
	trows.Close()
	if err := trows.Err(); err != nil {
		return nil, nil, err
	}
	return states, transitions, nil
}

// checkTransitionTx enforces the project's transition rules for one
// (from → to) move. A same-state "move" is not a transition. Zero rule
// rows for the project = unrestricted (backwards compatible). When
// rules exist and (from → to) is not listed, returns
// *StateTransitionError (unwrapping to ErrStateTransitionNotAllowed)
// carrying the allowed next-state IDs from `from`.
//
// One query per check: the project's whole rule set is small (bounded
// by states²) and moves already load the issue row — YAGNI over a cache.
func checkTransitionTx(ctx context.Context, q workflowQuerier, projectID, fromStateID, toStateID string) error {
	from := strings.ToLower(strings.TrimSpace(fromStateID))
	to := strings.ToLower(strings.TrimSpace(toStateID))
	if from == to {
		return nil
	}
	rows, err := q.Query(ctx,
		`SELECT from_state_id::text, to_state_id::text
		 FROM project_state_transitions
		 WHERE project_id = $1::uuid`, projectID)
	if err != nil {
		return err
	}
	allowed := []string{}
	ruleCount := 0
	for rows.Next() {
		var f, t string
		if err := rows.Scan(&f, &t); err != nil {
			rows.Close()
			return err
		}
		ruleCount++
		if strings.ToLower(f) == from {
			allowed = append(allowed, strings.ToLower(t))
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if ruleCount == 0 {
		return nil
	}
	for _, a := range allowed {
		if a == to {
			return nil
		}
	}
	return &StateTransitionError{FromStateID: from, ToStateID: to, Allowed: allowed}
}
