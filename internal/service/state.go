package service

// States management (C6T2): create / rename / delete project states.
//
// Until cycle 6 the only states endpoint was GET (list); the settings UI
// needs full management. All mutations are member (15)+; guests read.
// Delete is guarded: a state referenced by live issues is 409
// (issues.state_id has no ON DELETE action — the DB would 500), and the
// project's last state cannot be deleted (a project without states is
// unusable — see defaultStates).

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrInvalidStateGroup = errors.New("service: invalid state group")
	ErrStateConflict     = errors.New("service: state name already exists")
	ErrStateNotFound     = errors.New("service: state not found")
	ErrStateInUse        = errors.New("service: state is used by issues")
	ErrLastState         = errors.New("service: project must keep at least one state")
	ErrInvalidReassign   = errors.New("service: invalid reassign_to state")
)

// validStateGroups mirrors the CHECK constraint on states."group"
// (migration 000006).
var validStateGroups = map[string]bool{
	"triage":    true,
	"backlog":   true,
	"unstarted": true,
	"started":   true,
	"completed": true,
	"cancelled": true,
}

// StateInput carries state creation fields. Sequence nil → appended after
// the current max (seeded spacing is 10000; see defaultStates).
type StateInput struct {
	Name     string
	Group    string
	Color    string
	Sequence *int
}

// StatePatch carries state update fields; nil = untouched.
type StatePatch struct {
	Name     *string
	Group    *string
	Color    *string
	Sequence *int
}

// resolveStateScope resolves the project for (wsSlug, identifier) and the
// actor's workspace role. Non-members get ErrNotFound (tenancy boundary,
// same as resolveIssueProject).
func resolveStateScope(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string) (wsID, projectID string, role int, err error) {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return "", "", 0, err
	}
	return resolveIssueProject(ctx, pool, wsSlug, ident, actorID)
}

// CreateState adds a state to the project. Member (15)+.
func CreateState(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string, in StateInput) (*State, error) {
	_, projectID, role, err := resolveStateScope(ctx, pool, wsSlug, identifier, actorID)
	if err != nil {
		return nil, err
	}
	if role < RoleMember {
		return nil, ErrForbidden
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 120 {
		return nil, ErrNameRequired
	}
	group := strings.ToLower(strings.TrimSpace(in.Group))
	if !validStateGroups[group] {
		return nil, ErrInvalidStateGroup
	}
	seq := 0
	if in.Sequence != nil {
		seq = *in.Sequence
	} else {
		if err := pool.QueryRow(ctx,
			`SELECT COALESCE(MAX(sequence),0)+10000 FROM states WHERE project_id = $1::uuid`,
			projectID).Scan(&seq); err != nil {
			return nil, err
		}
	}
	var s State
	err = pool.QueryRow(ctx,
		`INSERT INTO states (project_id, name, "group", color, sequence)
		 VALUES ($1::uuid, $2, $3, $4, $5)
		 RETURNING id::text, project_id::text, name, "group", color, sequence, created_at, updated_at`,
		projectID, name, group, in.Color, seq).Scan(
		&s.ID, &s.ProjectID, &s.Name, &s.Group, &s.Color, &s.Sequence, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrStateConflict
		}
		return nil, err
	}
	return &s, nil
}

// UpdateState patches a state. Member (15)+. Unknown fields untouched;
// empty patch is ErrNothingToUpdate.
func UpdateState(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, stateID, actorID string, p StatePatch) (*State, error) {
	_, projectID, role, err := resolveStateScope(ctx, pool, wsSlug, identifier, actorID)
	if err != nil {
		return nil, err
	}
	if role < RoleMember {
		return nil, ErrForbidden
	}
	set := []string{}
	args := []any{projectID, stateID}
	if p.Name != nil {
		name := strings.TrimSpace(*p.Name)
		if name == "" || len(name) > 120 {
			return nil, ErrNameRequired
		}
		args = append(args, name)
		set = append(set, `name = $`+strconv.Itoa(len(args)))
	}
	if p.Group != nil {
		group := strings.ToLower(strings.TrimSpace(*p.Group))
		if !validStateGroups[group] {
			return nil, ErrInvalidStateGroup
		}
		args = append(args, group)
		set = append(set, `"group" = $`+strconv.Itoa(len(args)))
	}
	if p.Color != nil {
		args = append(args, *p.Color)
		set = append(set, `color = $`+strconv.Itoa(len(args)))
	}
	if p.Sequence != nil {
		args = append(args, *p.Sequence)
		set = append(set, `sequence = $`+strconv.Itoa(len(args)))
	}
	if len(set) == 0 {
		return nil, ErrNothingToUpdate
	}
	var s State
	err = pool.QueryRow(ctx,
		`UPDATE states SET `+strings.Join(set, ", ")+`, updated_at = now()
		 WHERE id = $2::uuid AND project_id = $1::uuid
		 RETURNING id::text, project_id::text, name, "group", color, sequence, created_at, updated_at`,
		args...).Scan(
		&s.ID, &s.ProjectID, &s.Name, &s.Group, &s.Color, &s.Sequence, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		if isInvalidUUID(err) {
			return nil, ErrStateNotFound
		}
		if isUniqueViolation(err) {
			return nil, ErrStateConflict
		}
		// pgx.ErrNoRows → unknown state in this project (tenancy: never
		// reveal other projects' states).
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrStateNotFound
		}
		return nil, err
	}
	return &s, nil
}

// DeleteState removes a state. Member (15)+.
//
// issues.state_id has no ON DELETE action, so every referencing issue —
// live or soft-deleted — must be moved first: pass reassignTo (another
// state in the same project) to move them in the same transaction.
// Without reassignTo and with any referencing issues → 409 ErrStateInUse.
// Deleting the project's last state is always 409 ErrLastState.
func DeleteState(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, stateID, actorID string, reassignTo *string) error {
	_, projectID, role, err := resolveStateScope(ctx, pool, wsSlug, identifier, actorID)
	if err != nil {
		return err
	}
	if role < RoleMember {
		return ErrForbidden
	}
	var exists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM states WHERE id = $1::uuid AND project_id = $2::uuid)`,
		stateID, projectID).Scan(&exists); err != nil {
		if isInvalidUUID(err) {
			return ErrStateNotFound
		}
		return err
	}
	if !exists {
		return ErrStateNotFound
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Lock the project's state rows: two concurrent deletes of the last
	// two states must serialize (same race class as the C3T8 last-admin
	// fix — SELECT ... FOR UPDATE on the membership rows).
	rows, err := tx.Query(ctx,
		`SELECT id FROM states WHERE project_id = $1::uuid FOR UPDATE`, projectID)
	if err != nil {
		return err
	}
	total := 0
	for rows.Next() {
		total++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if total <= 1 {
		return ErrLastState
	}
	var refs int
	if err := tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM issues WHERE state_id = $1::uuid`, stateID).Scan(&refs); err != nil {
		return err
	}
	if refs > 0 {
		if reassignTo == nil || *reassignTo == "" {
			return ErrStateInUse
		}
		var targetOK bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM states
			 WHERE id = $1::uuid AND project_id = $2::uuid AND id <> $3::uuid)`,
			*reassignTo, projectID, stateID).Scan(&targetOK); err != nil {
			if isInvalidUUID(err) {
				return ErrInvalidReassign
			}
			return err
		}
		if !targetOK {
			return ErrInvalidReassign
		}
		if _, err := tx.Exec(ctx,
			`UPDATE issues SET state_id = $1::uuid, updated_at = now() WHERE state_id = $2::uuid`,
			*reassignTo, stateID); err != nil {
			return err
		}
	}
	ct, err := tx.Exec(ctx, `DELETE FROM states WHERE id = $1::uuid`, stateID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrStateNotFound
	}
	return tx.Commit(ctx)
}
