package service

// Modules (Plane "modules" = project sub-groupings / epics), C3T1.
//
// Schema note: the modules / module_issues / module_members tables shipped
// in 000012 (tables only, no API); 000017 adds start_date/target_date.
// Issue membership uses the module_issues junction table — like
// cycle_issues — NOT an issues.module_id column. The junction keeps
// deletes cheap (ON DELETE CASCADE) and mirrors the cycles domain.
//
// Conventions (mirror cycles):
//   - Tenancy: every op resolves workspace membership + project via
//     resolveIssueProject. A bad slug or non-member caller surfaces
//     ErrNotFound ("workspace not found"); a bad project identifier
//     surfaces ErrProjectNotFound; a bad module id surfaces
//     ErrModuleNotFound. Distinct 404s on purpose (Task 11 ruling).
//   - Roles: any member (guest 5+) may read; mutations need member (15)+.
//   - PATCH tri-state for nullable fields: nil = untouched, pointer to ""
//     = clear (NULL), pointer to a value = set. Dates arrive as *string
//     (YYYY-MM-DD) so "" can mean "clear"; the service parses them —
//     deliberately different from cycles, where dates are NOT NULL and
//     the handler parses.
//   - Delete guard: deleting a module that still has issues is 409
//     (ErrModuleHasIssues) unless force=true, in which case the junction
//     rows cascade and the issues themselves are untouched.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrModuleNotFound is returned when the module id matches nothing in
	// the project. Deliberately distinct from ErrProjectNotFound: on this
	// path the workspace and project are confirmed and the caller is a
	// member.
	ErrModuleNotFound = errors.New("service: module not found")
	// ErrModuleConflict is returned when the module name is already taken
	// in the project (UNIQUE(project_id, name)).
	ErrModuleConflict = errors.New("service: module name already exists")
	// ErrInvalidModule is returned for malformed module input: bad status
	// vocabulary, start_date after target_date, unparseable dates, or a
	// malformed lead id.
	ErrInvalidModule = errors.New("service: invalid module")
	// ErrInvalidModuleID is returned when a module id — or an issue id
	// passed to a module endpoint — is not a syntactically valid UUID.
	// The handler maps it to 400 bad_request.
	ErrInvalidModuleID = errors.New("service: invalid module id")
	// ErrModuleHasIssues is returned when deleting a module that still
	// has issues without force=true. The handler maps it to 409.
	ErrModuleHasIssues = errors.New("service: module still has issues")
	// ErrLeadNotFound is returned when module.lead_id names no user.
	ErrLeadNotFound = errors.New("service: module lead not found")
)

// moduleStatuses is the status vocabulary enforced by the 000012 CHECK.
var moduleStatuses = map[string]bool{
	"active":    true,
	"completed": true,
	"archived":  true,
}

// Module is one project sub-grouping (epic).
type Module struct {
	ID          string     `json:"id"`
	ProjectID   string     `json:"project_id"`
	Name        string     `json:"name"`
	Description *string    `json:"description,omitempty"`
	Status      string     `json:"status"`
	LeadID      *string    `json:"lead_id,omitempty"`
	StartDate   *time.Time `json:"start_date,omitempty"`
	TargetDate  *time.Time `json:"target_date,omitempty"`
	IssueCount  int64      `json:"issue_count"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// ModuleInput carries module creation fields. Dates are YYYY-MM-DD
// strings; nil means NULL.
type ModuleInput struct {
	Name        string
	Description *string
	Status      string // "" = default "active"
	LeadID      *string
	StartDate   *string
	TargetDate  *string
}

// ModulePatch is a partial module update: nil fields are untouched; a
// non-nil pointer to "" clears a nullable field (NULL); otherwise the
// value is set.
type ModulePatch struct {
	Name        *string
	Description *string
	Status      *string
	LeadID      *string
	StartDate   *string
	TargetDate  *string
}

// resolveModuleProject resolves (projectID, role) for the caller.
// Guests may read; callers pass needMember to enforce member (15)+.
func resolveModuleProject(ctx context.Context, q queryRower, wsSlug, identifier, actorID string, needMember bool) (projectID string, role int, err error) {
	_, projectID, role, err = resolveIssueProject(ctx, q, wsSlug, identifier, actorID)
	if err != nil {
		return "", 0, err
	}
	if needMember && role < RoleMember {
		return "", 0, ErrForbidden
	}
	return projectID, role, nil
}

// moduleColumns is the shared SELECT list (no issue count).
const moduleColumns = `id::text, project_id::text, name, description, status,
	lead_id::text, start_date, target_date, created_at, updated_at`

// moduleColumnsCounted adds the live issue count for list/get responses.
const moduleColumnsCounted = moduleColumns + `,
	(SELECT COUNT(*) FROM module_issues mi WHERE mi.module_id = modules.id)`

func scanModule(row pgx.Row) (Module, error) {
	var m Module
	err := row.Scan(
		&m.ID, &m.ProjectID, &m.Name, &m.Description, &m.Status,
		&m.LeadID, &m.StartDate, &m.TargetDate, &m.CreatedAt, &m.UpdatedAt)
	if err != nil {
		return Module{}, err
	}
	return m, nil
}

func scanModuleCounted(row pgx.Row) (Module, error) {
	var mod Module
	err := row.Scan(
		&mod.ID, &mod.ProjectID, &mod.Name, &mod.Description, &mod.Status,
		&mod.LeadID, &mod.StartDate, &mod.TargetDate, &mod.CreatedAt, &mod.UpdatedAt,
		&mod.IssueCount)
	if err != nil {
		return Module{}, err
	}
	return mod, nil
}

func resolveModule(ctx context.Context, q queryRower, projectID, moduleID string) (Module, error) {
	if !isUUIDFormat(moduleID) {
		// Malformed ids match nothing — 400, not a 500 from the ::uuid cast.
		return Module{}, ErrInvalidModuleID
	}
	m, err := scanModule(q.QueryRow(ctx,
		`SELECT `+moduleColumns+`
		 FROM modules WHERE id = $1::uuid AND project_id = $2::uuid`,
		moduleID, projectID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Module{}, ErrModuleNotFound
		}
		return Module{}, err
	}
	return m, nil
}

// parseModuleDate parses a YYYY-MM-DD date or returns nil for "" (clear).
func parseModuleDate(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil, ErrInvalidModule
	}
	return &t, nil
}

// validateModuleLead ensures a non-empty lead id names an existing user.
func validateModuleLead(ctx context.Context, q queryRower, leadID *string) error {
	if leadID == nil || *leadID == "" {
		return nil
	}
	if !isUUIDFormat(*leadID) {
		return ErrInvalidModule
	}
	var exists bool
	if err := q.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM users WHERE id = $1::uuid)`,
		*leadID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrLeadNotFound
	}
	return nil
}

// normalizeModuleInput validates creation fields, returning the cleaned
// name, status, dates, and description ("" description → NULL).
func normalizeModuleInput(ctx context.Context, q queryRower, in ModuleInput) (name, status string, desc *string, start, target *time.Time, err error) {
	name = strings.TrimSpace(in.Name)
	if name == "" {
		return "", "", nil, nil, nil, ErrNameRequired
	}
	status = in.Status
	if status == "" {
		status = "active"
	}
	if !moduleStatuses[status] {
		return "", "", nil, nil, nil, ErrInvalidModule
	}
	if in.Description != nil {
		if d := strings.TrimSpace(*in.Description); d != "" {
			desc = &d
		}
	}
	if start, err = parseModuleDate(orEmpty(in.StartDate)); err != nil {
		return "", "", nil, nil, nil, err
	}
	if target, err = parseModuleDate(orEmpty(in.TargetDate)); err != nil {
		return "", "", nil, nil, nil, err
	}
	if start != nil && target != nil && start.After(*target) {
		return "", "", nil, nil, nil, ErrInvalidModule
	}
	if err := validateModuleLead(ctx, q, in.LeadID); err != nil {
		return "", "", nil, nil, nil, err
	}
	return name, status, desc, start, target, nil
}

func orEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// nullLeadID converts the tri-state lead: nil → untouched (handled by
// caller), "" → NULL, value → the uuid.
func nullLeadID(leadID *string) any {
	if leadID == nil || *leadID == "" {
		return nil
	}
	return *leadID
}

// CreateModule creates a module in the project. Member (15)+.
func CreateModule(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string, in ModuleInput) (*Module, error) {
	projectID, _, err := resolveModuleProject(ctx, pool, wsSlug, identifier, actorID, true)
	if err != nil {
		return nil, err
	}
	name, status, desc, start, target, err := normalizeModuleInput(ctx, pool, in)
	if err != nil {
		return nil, err
	}
	m, err := scanModule(pool.QueryRow(ctx,
		`INSERT INTO modules (project_id, name, description, status, lead_id, start_date, target_date)
		 VALUES ($1::uuid, $2, $3, $4, $5::uuid, $6::date, $7::date)
		 RETURNING `+moduleColumns,
		projectID, name, desc, status, nullLeadID(in.LeadID), start, target))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrModuleConflict
		}
		return nil, err
	}
	announceModuleUpdated(ctx, pool, m.ID, projectID)
	return &m, nil
}

// ListModules returns the project's modules with live issue counts,
// ordered by creation. Any role may read.
func ListModules(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string) ([]Module, error) {
	projectID, _, err := resolveModuleProject(ctx, pool, wsSlug, identifier, actorID, false)
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx,
		`SELECT `+moduleColumnsCounted+`
		 FROM modules WHERE project_id = $1::uuid
		 ORDER BY created_at`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Module{}
	for rows.Next() {
		m, err := scanModuleCounted(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetModule returns one module with its live issue count. Any role may read.
func GetModule(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, moduleID string) (*Module, error) {
	projectID, _, err := resolveModuleProject(ctx, pool, wsSlug, identifier, actorID, false)
	if err != nil {
		return nil, err
	}
	if !isUUIDFormat(moduleID) {
		return nil, ErrInvalidModuleID
	}
	m, err := scanModuleCounted(pool.QueryRow(ctx,
		`SELECT `+moduleColumnsCounted+`
		 FROM modules WHERE id = $1::uuid AND project_id = $2::uuid`,
		moduleID, projectID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrModuleNotFound
		}
		return nil, err
	}
	return &m, nil
}

// UpdateModule applies a partial module update. Member (15)+. Only
// non-nil patch fields are written; "" clears a nullable field. An empty
// patch is ErrNothingToUpdate.
func UpdateModule(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, moduleID string, patch ModulePatch) (*Module, error) {
	projectID, _, err := resolveModuleProject(ctx, pool, wsSlug, identifier, actorID, true)
	if err != nil {
		return nil, err
	}
	m, err := resolveModule(ctx, pool, projectID, moduleID)
	if err != nil {
		return nil, err
	}

	var sets []string
	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if patch.Name != nil {
		name := strings.TrimSpace(*patch.Name)
		if name == "" {
			return nil, ErrNameRequired
		}
		sets = append(sets, "name = "+arg(name))
	}
	if patch.Description != nil {
		var desc *string
		if d := strings.TrimSpace(*patch.Description); d != "" {
			desc = &d
		}
		sets = append(sets, "description = "+arg(desc))
	}
	if patch.Status != nil {
		if !moduleStatuses[*patch.Status] {
			return nil, ErrInvalidModule
		}
		sets = append(sets, "status = "+arg(*patch.Status))
	}
	if patch.LeadID != nil {
		if err := validateModuleLead(ctx, pool, patch.LeadID); err != nil {
			return nil, err
		}
		sets = append(sets, "lead_id = "+arg(nullLeadID(patch.LeadID))+"::uuid")
	}
	start, target := m.StartDate, m.TargetDate
	if patch.StartDate != nil {
		start, err = parseModuleDate(*patch.StartDate)
		if err != nil {
			return nil, err
		}
		sets = append(sets, "start_date = "+arg(start)+"::date")
	}
	if patch.TargetDate != nil {
		target, err = parseModuleDate(*patch.TargetDate)
		if err != nil {
			return nil, err
		}
		sets = append(sets, "target_date = "+arg(target)+"::date")
	}
	if start != nil && target != nil && start.After(*target) {
		return nil, ErrInvalidModule
	}
	if len(sets) == 0 {
		return nil, ErrNothingToUpdate
	}
	sets = append(sets, "updated_at = now()")

	um, err := scanModule(pool.QueryRow(ctx,
		`UPDATE modules SET `+strings.Join(sets, ", ")+`
		 WHERE id = `+arg(moduleID)+`::uuid AND project_id = `+arg(projectID)+`::uuid
		 RETURNING `+moduleColumns,
		args...))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrModuleConflict
		}
		return nil, err
	}
	// Re-read with the live issue count (the update itself never changes
	// membership, so a second read is exact).
	got, err := GetModule(ctx, pool, wsSlug, identifier, actorID, um.ID)
	if err != nil {
		return nil, err
	}
	announceModuleUpdated(ctx, pool, um.ID, projectID)
	return got, nil
}

// DeleteModule deletes a module (membership rows cascade; the issues
// themselves are untouched). Member (15)+. Without force, a module that
// still has issues is 409 (ErrModuleHasIssues).
func DeleteModule(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, moduleID string, force bool) error {
	projectID, _, err := resolveModuleProject(ctx, pool, wsSlug, identifier, actorID, true)
	if err != nil {
		return err
	}
	if _, err := resolveModule(ctx, pool, projectID, moduleID); err != nil {
		return err
	}
	if !force {
		var n int64
		if err := pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM module_issues WHERE module_id = $1::uuid`,
			moduleID).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return ErrModuleHasIssues
		}
	}
	_, err = pool.Exec(ctx,
		`DELETE FROM modules WHERE id = $1::uuid AND project_id = $2::uuid`,
		moduleID, projectID)
	if err != nil {
		return err
	}
	announceModuleUpdated(ctx, pool, moduleID, projectID)
	return nil
}

// AddModuleIssues bulk-adds issues to a module. Every issue must be a live
// (non-deleted) issue of the same project, otherwise ErrIssueNotFound.
// The add is idempotent: repeats are no-ops via ON CONFLICT DO NOTHING —
// never a 409. Member (15)+; an empty id list is ErrBulkEmptyIDs.
func AddModuleIssues(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, moduleID string, issueIDs []string) error {
	if len(issueIDs) == 0 {
		return ErrBulkEmptyIDs
	}
	projectID, _, err := resolveModuleProject(ctx, pool, wsSlug, identifier, actorID, true)
	if err != nil {
		return err
	}
	if _, err := resolveModule(ctx, pool, projectID, moduleID); err != nil {
		return err
	}
	// Validate ownership + liveness for every id first: a partial add
	// that silently drops foreign issues would be worse than a 404.
	for _, id := range issueIDs {
		if !isUUIDFormat(id) {
			// Malformed ids are a client error — 400, not a 500 from
			// the ::uuid cast.
			return ErrInvalidModuleID
		}
		var owner string
		err := pool.QueryRow(ctx,
			`SELECT project_id::text FROM issues
			 WHERE id = $1::uuid AND deleted_at IS NULL`, id).Scan(&owner)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrIssueNotFound
			}
			return err
		}
		if owner != projectID {
			return ErrIssueNotFound
		}
	}
	_, err = pool.Exec(ctx,
		`INSERT INTO module_issues (module_id, issue_id)
		 SELECT $1::uuid, unnest($2::uuid[])
		 ON CONFLICT DO NOTHING`, moduleID, issueIDs)
	if err != nil {
		return err
	}
	announceModuleUpdated(ctx, pool, moduleID, projectID)
	return nil
}

// RemoveModuleIssues bulk-removes issues from a module. Idempotent:
// removing a non-member is a no-op. Member (15)+; an empty id list is
// ErrBulkEmptyIDs. Malformed ids are 400 (the ::uuid[] cast would 500).
func RemoveModuleIssues(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, moduleID string, issueIDs []string) error {
	if len(issueIDs) == 0 {
		return ErrBulkEmptyIDs
	}
	projectID, _, err := resolveModuleProject(ctx, pool, wsSlug, identifier, actorID, true)
	if err != nil {
		return err
	}
	if _, err := resolveModule(ctx, pool, projectID, moduleID); err != nil {
		return err
	}
	for _, id := range issueIDs {
		if !isUUIDFormat(id) {
			return ErrInvalidModuleID
		}
	}
	_, err = pool.Exec(ctx,
		`DELETE FROM module_issues
		 WHERE module_id = $1::uuid AND issue_id = ANY($2::uuid[])`,
		moduleID, issueIDs)
	if err != nil {
		return err
	}
	announceModuleUpdated(ctx, pool, moduleID, projectID)
	return nil
}

// prefixColumns qualifies every column in a comma-separated list with the
// given table alias. The shared issueColumns const carries no alias,
// which is ambiguous once module_issues is joined.
func prefixColumns(alias, cols string) string {
	parts := strings.Split(cols, ",")
	for i, p := range parts {
		parts[i] = alias + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}

// scanModuleIssueRow scans one module-issue row: the full issue column
// list plus aggregated assignees/labels (mirrors getIssueRow).
func scanModuleIssueRow(row pgx.Row, ident string) (*IssueListItem, error) {
	var item IssueListItem
	var description []byte
	var assigneesJSON, labelsJSON []byte
	err := row.Scan(
		&item.ID, &item.ProjectID, &item.SequenceID, &item.Name,
		&description,
		&item.Priority, &item.StateID, &item.ParentID, &item.SortOrder,
		&item.StartDate, &item.TargetDate, &item.EstimatePointID,
		&item.IsDraft, &item.ArchivedAt,
		&item.CreatedBy, &item.CreatedAt, &item.UpdatedAt,
		&assigneesJSON, &labelsJSON,
	)
	if err != nil {
		return nil, err
	}
	if description != nil {
		item.Description = json.RawMessage(description)
	}
	if err := unmarshalRelations(assigneesJSON, labelsJSON, &item); err != nil {
		return nil, err
	}
	item.DisplayID = ident + "-" + strconv.Itoa(item.SequenceID)
	return &item, nil
}

// ListModuleIssues returns the module's live issues as full list items
// (assignees + labels aggregated, one query). Any role may read.
func ListModuleIssues(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, moduleID string) ([]IssueListItem, error) {
	projectID, _, err := resolveModuleProject(ctx, pool, wsSlug, identifier, actorID, false)
	if err != nil {
		return nil, err
	}
	if _, err := resolveModule(ctx, pool, projectID, moduleID); err != nil {
		return nil, err
	}
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx,
		`SELECT `+prefixColumns("i.", issueColumns)+`, `+assigneesAgg+`, `+labelsAgg+`
		 FROM module_issues mi
		 JOIN issues i ON i.id = mi.issue_id AND i.deleted_at IS NULL
		 WHERE mi.module_id = $1::uuid
		 ORDER BY i.created_at`, moduleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IssueListItem{}
	for rows.Next() {
		item, err := scanModuleIssueRow(rows, ident)
		if err != nil {
			return nil, err
		}
		out = append(out, *item)
	}
	return out, rows.Err()
}
