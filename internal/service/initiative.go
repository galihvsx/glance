package service

// Initiatives (Plane "initiatives" / programs), C27T0 backend. Initiatives
// are WORKSPACE-scoped containers that group projects — unlike modules
// and cycles, which are project-scoped. Plane paywalls initiatives;
// glance ships them free (Apache-2.0, no gated features).
//
// Schema: 000050 (initiatives + initiative_projects junction). Initiatives
// own no issues: issue membership stays project-scoped, so deleting an
// initiative touches only initiative_projects rows + the initiative row.
//
// Conventions (mirror modules):
//   - Tenancy: every op resolves (workspaceID, role) via
//     resolveInitiativeWorkspace. A bad slug or non-member caller surfaces
//     ErrNotFound ("workspace not found") — the tenancy boundary. A bad
//     initiative id surfaces ErrInitiativeNotFound; a malformed one
//     surfaces ErrInvalidInitiativeID (400, never a ::uuid 500).
//   - Roles: any member (guest 5+) may read; mutations need member (15)+.
//   - PATCH tri-state for nullable fields: nil = untouched, "" = clear
//     (NULL), value = set. Status is a validated vocabulary.
//   - Status transitions: any of active/completed/archived may move to
//     any other (validated, no forbidden transitions).
//   - Member project adds are idempotent (ON CONFLICT DO NOTHING); a
//     project must belong to the same workspace (else ErrProjectNotFound).
//   - Rollup (GET .../:id/rollup): ONE aggregate query — issue counts by
//     state group across all member projects plus a per-project
//     breakdown; completion % = done-group (completed/cancelled) issues
//     over total. Projects with zero issues appear with zero counts.

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
	// ErrInitiativeNotFound is returned when the initiative id matches
	// nothing in the workspace. On this path the workspace is confirmed
	// and the caller is a member.
	ErrInitiativeNotFound = errors.New("service: initiative not found")
	// ErrInitiativeConflict is returned when the initiative name is
	// already taken in the workspace (UNIQUE(workspace_id, name)).
	ErrInitiativeConflict = errors.New("service: initiative name already exists")
	// ErrInvalidInitiative is returned for malformed initiative input: a
	// bad status vocabulary.
	ErrInvalidInitiative = errors.New("service: invalid initiative")
	// ErrInvalidInitiativeID is returned when an initiative id — or a
	// project id passed to a membership endpoint — is not a syntactically
	// valid UUID. The handler maps it to 400 bad_request.
	ErrInvalidInitiativeID = errors.New("service: invalid initiative id")
	// ErrInvalidInitiativeFilter is returned when a ListInitiatives
	// filter param fails validation: unknown status, malformed uuid,
	// unparseable date, unknown sort/order. The handler maps it to 400
	// with the wrapped detail as the message.
	ErrInvalidInitiativeFilter = errors.New("service: invalid initiative list filter")
)

// initiativeStatuses is the status vocabulary enforced by the 000050
// CHECK. Every transition between values is allowed.
var initiativeStatuses = map[string]bool{
	"active":    true,
	"completed": true,
	"archived":  true,
}

// Initiative is one workspace-scoped program grouping projects.
type Initiative struct {
	ID           string    `json:"id"`
	WorkspaceID  string    `json:"workspace_id"`
	Name         string    `json:"name"`
	Description  *string   `json:"description,omitempty"`
	Status       string    `json:"status"`
	CreatedBy    *string   `json:"created_by,omitempty"`
	ProjectCount int64     `json:"project_count"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// InitiativeInput carries initiative creation fields.
type InitiativeInput struct {
	Name        string
	Description *string
	Status      string // "" = default "active"
}

// InitiativePatch is a partial initiative update: nil fields are
// untouched; a non-nil pointer to "" clears description (NULL).
type InitiativePatch struct {
	Name        *string
	Description *string
	Status      *string
}

// InitiativeProject is a member project of an initiative (list response).
type InitiativeProject struct {
	ProjectID   string `json:"project_id"`
	ProjectName string `json:"project_name"`
	// Labels are the project's workspace project-label chips (C76T0):
	// [{id,name,color}] ordered by name, [] never null — shipped in the
	// same query, no per-project round trip.
	Labels  []ProjectLabel `json:"labels"`
	AddedAt string         `json:"added_at"`
}

// InitiativeProjectRollup is the per-project slice of a rollup.
type InitiativeProjectRollup struct {
	ProjectID     string           `json:"project_id"`
	ProjectName   string           `json:"project_name"`
	Total         int64            `json:"total"`
	DoneCount     int64            `json:"done_count"`
	CompletionPct float64          `json:"completion_pct"`
	ByGroup       map[string]int64 `json:"by_group"`
}

// InitiativeRollup is the cross-project progress aggregate.
type InitiativeRollup struct {
	InitiativeID  string                    `json:"initiative_id"`
	Total         int64                     `json:"total"`
	DoneCount     int64                     `json:"done_count"`
	CompletionPct float64                   `json:"completion_pct"`
	ByGroup       map[string]int64          `json:"by_group"`
	Projects      []InitiativeProjectRollup `json:"projects"`
}

// resolveInitiativeWorkspace resolves (workspaceID, role) for the caller.
// Guests may read; callers pass needMember to enforce member (15)+.
// Non-members (and bad slugs) surface ErrNotFound — the tenancy boundary.
func resolveInitiativeWorkspace(ctx context.Context, q queryRower, wsSlug, actorID string, needMember bool) (wsID string, role int, err error) {
	wsID, role, err = workspaceIDForActor(q.QueryRow(ctx,
		`SELECT w.id::text, m.role
		 FROM workspaces w
		 JOIN workspace_members m ON m.workspace_id = w.id
		 WHERE w.slug = $1 AND m.user_id = $2::uuid`,
		wsSlug, actorID))
	if err != nil {
		return "", 0, err
	}
	if needMember && role < RoleMember {
		return "", 0, ErrForbidden
	}
	return wsID, role, nil
}

// initiativeColumns is the shared SELECT list plus the live member
// project count.
const initiativeColumns = `id::text, workspace_id::text, name, description, status,
	created_by::text, created_at, updated_at`

const initiativeColumnsCounted = initiativeColumns + `,
	(SELECT COUNT(*) FROM initiative_projects ip WHERE ip.initiative_id = initiatives.id)`

func scanInitiativeCounted(row pgx.Row) (Initiative, error) {
	var in Initiative
	err := row.Scan(
		&in.ID, &in.WorkspaceID, &in.Name, &in.Description, &in.Status,
		&in.CreatedBy, &in.CreatedAt, &in.UpdatedAt, &in.ProjectCount)
	if err != nil {
		return Initiative{}, err
	}
	return in, nil
}

// resolveInitiative loads an initiative of the workspace. Malformed ids
// match nothing — 400, not a 500 from the ::uuid cast.
func resolveInitiative(ctx context.Context, q queryRower, wsID, initiativeID string) (Initiative, error) {
	if !isUUIDFormat(initiativeID) {
		return Initiative{}, ErrInvalidInitiativeID
	}
	in, err := scanInitiativeCounted(q.QueryRow(ctx,
		`SELECT `+initiativeColumnsCounted+`
		 FROM initiatives WHERE id = $1::uuid AND workspace_id = $2::uuid`,
		initiativeID, wsID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Initiative{}, ErrInitiativeNotFound
		}
		return Initiative{}, err
	}
	return in, nil
}

// normalizeInitiativeInput validates creation fields, returning the
// cleaned name, status, and description ("" description → NULL).
func normalizeInitiativeInput(in InitiativeInput) (name, status string, desc *string, err error) {
	name = strings.TrimSpace(in.Name)
	if name == "" {
		return "", "", nil, ErrNameRequired
	}
	status = in.Status
	if status == "" {
		status = "active"
	}
	if !initiativeStatuses[status] {
		return "", "", nil, ErrInvalidInitiative
	}
	if in.Description != nil {
		if d := strings.TrimSpace(*in.Description); d != "" {
			desc = &d
		}
	}
	return name, status, desc, nil
}

// CreateInitiative creates an initiative in the workspace. Member (15)+.
func CreateInitiative(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID string, in InitiativeInput) (*Initiative, error) {
	wsID, _, err := resolveInitiativeWorkspace(ctx, pool, wsSlug, actorID, true)
	if err != nil {
		return nil, err
	}
	name, status, desc, err := normalizeInitiativeInput(in)
	if err != nil {
		return nil, err
	}
	init, err := scanInitiativeCounted(pool.QueryRow(ctx,
		`INSERT INTO initiatives (workspace_id, name, description, status, created_by)
		 VALUES ($1::uuid, $2, $3, $4, $5::uuid)
		 RETURNING `+initiativeColumnsCounted,
		wsID, name, desc, status, actorID))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrInitiativeConflict
		}
		return nil, err
	}
	announceInitiativeUpdated(ctx, wsSlug, init.ID)
	return &init, nil
}

// InitiativeListFilter carries the rich filter params for
// ListInitiatives (C73T0 — Plane "initiatives rich filters" parity).
// All user input is parameterized in the query; Sort/Order are
// whitelisted to static SQL fragments, never interpolated.
//
// Status semantics: an explicit Statuses list always wins. Otherwise
// IncludeArchived (the legacy ?include_archived=1|true) means "no
// status exclusion"; the default excludes archived.
type InitiativeListFilter struct {
	IncludeArchived bool
	Statuses        []string
	Q               string
	CreatedBy       string
	CreatedFrom     string // ISO-8601 (RFC3339 or date-only); "" = unbounded
	CreatedTo       string
	ProjectID       string
	Sort            string // name|created_at|updated_at|project_count; "" = created_at
	Order           string // asc|desc; "" = asc for name, desc otherwise
}

// initiativeSortColumns whitelists sort keys to static ORDER BY
// expressions. project_count is a correlated subquery over the
// initiative_projects junction — computed, not interpolated.
var initiativeSortColumns = map[string]string{
	"name":          `name`,
	"created_at":    `created_at`,
	"updated_at":    `updated_at`,
	"project_count": `(SELECT COUNT(*) FROM initiative_projects ip WHERE ip.initiative_id = initiatives.id)`,
}

// parseInitiativeDate accepts ISO-8601: full RFC3339 timestamps and
// bare dates (2006-01-02). "" means unbounded.
func parseInitiativeDate(v string) (*time.Time, error) {
	if v == "" {
		return nil, nil
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return &t, nil
	}
	if t, err := time.Parse("2006-01-02", v); err == nil {
		return &t, nil
	}
	return nil, fmt.Errorf("%w: unparseable date %q (want ISO-8601)", ErrInvalidInitiativeFilter, v)
}

// ListInitiatives returns the workspace's initiatives with rich
// filters (C73T0), live member project counts, and the result never
// nil — an empty match is []. Any role may read.
func ListInitiatives(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID string, filter InitiativeListFilter) ([]Initiative, error) {
	wsID, _, err := resolveInitiativeWorkspace(ctx, pool, wsSlug, actorID, false)
	if err != nil {
		return nil, err
	}

	var conds []string
	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	wsArg := arg(wsID)

	// Status: explicit list wins; otherwise legacy includeArchived maps
	// to "no status exclusion"; default excludes archived.
	if len(filter.Statuses) > 0 {
		for _, s := range filter.Statuses {
			if !initiativeStatuses[s] {
				return nil, fmt.Errorf("%w: unknown status %q (want active|completed|archived)", ErrInvalidInitiativeFilter, s)
			}
		}
		conds = append(conds, `status = ANY(`+arg(filter.Statuses)+`)`)
	} else if !filter.IncludeArchived {
		conds = append(conds, `status <> 'archived'`)
	}

	// Text search: ILIKE on name + description, fully parameterized.
	if q := strings.TrimSpace(filter.Q); q != "" {
		conds = append(conds, `(name ILIKE `+arg("%"+q+"%")+` OR description ILIKE `+arg("%"+q+"%")+`)`)
	}

	// Created-by: malformed uuid is a client error, not a ::uuid 500.
	if filter.CreatedBy != "" {
		if !isUUIDFormat(filter.CreatedBy) {
			return nil, fmt.Errorf("%w: malformed created_by %q", ErrInvalidInitiativeFilter, filter.CreatedBy)
		}
		conds = append(conds, `created_by = `+arg(filter.CreatedBy)+`::uuid`)
	}

	// Created-at range: unparseable dates are client errors.
	if from, err := parseInitiativeDate(filter.CreatedFrom); err != nil {
		return nil, err
	} else if from != nil {
		conds = append(conds, `created_at >= `+arg(*from))
	}
	if to, err := parseInitiativeDate(filter.CreatedTo); err != nil {
		return nil, err
	} else if to != nil {
		conds = append(conds, `created_at <= `+arg(*to))
	}

	// Member-project via the junction; a valid-but-unknown project id
	// simply matches nothing.
	if filter.ProjectID != "" {
		if !isUUIDFormat(filter.ProjectID) {
			return nil, fmt.Errorf("%w: malformed project_id %q", ErrInvalidInitiativeFilter, filter.ProjectID)
		}
		conds = append(conds, `EXISTS (SELECT 1 FROM initiative_projects ip
			WHERE ip.initiative_id = initiatives.id AND ip.project_id = `+arg(filter.ProjectID)+`::uuid)`)
	}

	// Sort/order: whitelisted columns only — unknown values are 400.
	sort := filter.Sort
	if sort == "" {
		sort = "created_at"
	}
	col, ok := initiativeSortColumns[sort]
	if !ok {
		return nil, fmt.Errorf("%w: unknown sort %q (want name|created_at|updated_at|project_count)", ErrInvalidInitiativeFilter, filter.Sort)
	}
	order := strings.ToLower(filter.Order)
	if order == "" {
		order = "desc"
		if sort == "name" {
			order = "asc"
		}
	}
	if order != "asc" && order != "desc" {
		return nil, fmt.Errorf("%w: unknown order %q (want asc|desc)", ErrInvalidInitiativeFilter, filter.Order)
	}

	where := `workspace_id = ` + wsArg + `::uuid`
	if len(conds) > 0 {
		where += " AND " + strings.Join(conds, " AND ")
	}
	// Deterministic tiebreak so pagination-free lists are stable.
	rows, err := pool.Query(ctx,
		`SELECT `+initiativeColumnsCounted+`
		 FROM initiatives WHERE `+where+`
		 ORDER BY `+col+` `+order+`, name, id`,
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Initiative{}
	for rows.Next() {
		in, err := scanInitiativeCounted(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

// GetInitiative returns one initiative with its live member project
// count. Any role may read.
func GetInitiative(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID, initiativeID string) (*Initiative, error) {
	wsID, _, err := resolveInitiativeWorkspace(ctx, pool, wsSlug, actorID, false)
	if err != nil {
		return nil, err
	}
	in, err := resolveInitiative(ctx, pool, wsID, initiativeID)
	if err != nil {
		return nil, err
	}
	return &in, nil
}

// UpdateInitiative applies a partial initiative update. Member (15)+.
// Only non-nil patch fields are written; "" clears description. An empty
// patch is ErrNothingToUpdate.
func UpdateInitiative(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID, initiativeID string, patch InitiativePatch) (*Initiative, error) {
	wsID, _, err := resolveInitiativeWorkspace(ctx, pool, wsSlug, actorID, true)
	if err != nil {
		return nil, err
	}
	if _, err := resolveInitiative(ctx, pool, wsID, initiativeID); err != nil {
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
		if !initiativeStatuses[*patch.Status] {
			return nil, ErrInvalidInitiative
		}
		sets = append(sets, "status = "+arg(*patch.Status))
	}
	if len(sets) == 0 {
		return nil, ErrNothingToUpdate
	}
	sets = append(sets, "updated_at = now()")

	um, err := scanInitiativeCounted(pool.QueryRow(ctx,
		`UPDATE initiatives SET `+strings.Join(sets, ", ")+`
		 WHERE id = `+arg(initiativeID)+`::uuid AND workspace_id = `+arg(wsID)+`::uuid
		 RETURNING `+initiativeColumnsCounted,
		args...))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrInitiativeConflict
		}
		return nil, err
	}
	announceInitiativeUpdated(ctx, wsSlug, um.ID)
	return &um, nil
}

// DeleteInitiative deletes the initiative. The initiative_projects
// junction rows cascade; member projects and all issue data are
// untouched (initiatives own no issues). Member (15)+.
func DeleteInitiative(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID, initiativeID string) error {
	wsID, _, err := resolveInitiativeWorkspace(ctx, pool, wsSlug, actorID, true)
	if err != nil {
		return err
	}
	if _, err := resolveInitiative(ctx, pool, wsID, initiativeID); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx,
		`DELETE FROM initiatives WHERE id = $1::uuid AND workspace_id = $2::uuid`,
		initiativeID, wsID); err != nil {
		return err
	}
	announceInitiativeUpdated(ctx, wsSlug, initiativeID)
	return nil
}

// resolveInitiativeProject validates that projectID names a project of
// the workspace: malformed → ErrInvalidInitiativeID (400), unknown or
// foreign-workspace → ErrProjectNotFound.
func resolveInitiativeProject(ctx context.Context, q queryRower, wsID, projectID string) (string, error) {
	if !isUUIDFormat(projectID) {
		return "", ErrInvalidInitiativeID
	}
	var owner string
	err := q.QueryRow(ctx,
		`SELECT workspace_id::text FROM projects WHERE id = $1::uuid`, projectID).Scan(&owner)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrProjectNotFound
		}
		return "", err
	}
	if owner != wsID {
		return "", ErrProjectNotFound
	}
	return projectID, nil
}

// AddInitiativeProject adds a project to the initiative. Idempotent:
// repeats are no-ops via ON CONFLICT DO NOTHING — never a 409.
// Member (15)+.
func AddInitiativeProject(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID, initiativeID, projectID string) error {
	wsID, _, err := resolveInitiativeWorkspace(ctx, pool, wsSlug, actorID, true)
	if err != nil {
		return err
	}
	if _, err := resolveInitiative(ctx, pool, wsID, initiativeID); err != nil {
		return err
	}
	if _, err := resolveInitiativeProject(ctx, pool, wsID, projectID); err != nil {
		return err
	}
	// C92T0: linking a project to an initiative is a project-scoped
	// write — the effective-access order applies (a private project
	// the caller cannot reach is ErrNotFound) and the member+ gate
	// runs on the effective role.
	if _, effRole, err := effectiveRoleForProjectID(ctx, pool, wsID, projectID, actorID); err != nil {
		return err
	} else if effRole < RoleMember {
		return ErrForbidden
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO initiative_projects (initiative_id, project_id)
		 VALUES ($1::uuid, $2::uuid) ON CONFLICT DO NOTHING`,
		initiativeID, projectID); err != nil {
		return err
	}
	announceInitiativeUpdated(ctx, wsSlug, initiativeID)
	return nil
}

// RemoveInitiativeProject removes a project from the initiative.
// Idempotent: removing a non-member is a no-op. Member (15)+.
func RemoveInitiativeProject(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID, initiativeID, projectID string) error {
	wsID, _, err := resolveInitiativeWorkspace(ctx, pool, wsSlug, actorID, true)
	if err != nil {
		return err
	}
	if _, err := resolveInitiative(ctx, pool, wsID, initiativeID); err != nil {
		return err
	}
	if !isUUIDFormat(projectID) {
		// Malformed ids are a client error — 400, not a 500 from the
		// ::uuid cast.
		return ErrInvalidInitiativeID
	}
	// C92T0: same effective-access gate as AddInitiativeProject.
	// A project id that names nothing of this workspace resolves
	// to ErrProjectNotFound here (not the idempotent no-op): the
	// no-op below covers only linked-row absence, never a project
	// the caller cannot prove access to.
	if _, effRole, err := effectiveRoleForProjectID(ctx, pool, wsID, projectID, actorID); err != nil {
		return err
	} else if effRole < RoleMember {
		return ErrForbidden
	}
	if _, err := pool.Exec(ctx,
		`DELETE FROM initiative_projects
		 WHERE initiative_id = $1::uuid AND project_id = $2::uuid`,
		initiativeID, projectID); err != nil {
		return err
	}
	announceInitiativeUpdated(ctx, wsSlug, initiativeID)
	return nil
}

// ListInitiativeProjects returns the initiative's member projects
// ordered by addition. Any role may read. C92T2: linked projects the
// actor cannot effectively reach (private, no direct row, no
// linked-teamspace seat, not a workspace Admin) are omitted —
// linking a private project to a workspace-visible initiative must
// not disclose its name through this list.
func ListInitiativeProjects(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID, initiativeID string) ([]InitiativeProject, error) {
	wsID, _, err := resolveInitiativeWorkspace(ctx, pool, wsSlug, actorID, false)
	if err != nil {
		return nil, err
	}
	if _, err := resolveInitiative(ctx, pool, wsID, initiativeID); err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx,
		`SELECT p.id::text, p.name, ip.added_at::text, `+projectLabelsAgg("p")+`
		 FROM initiative_projects ip
		 JOIN projects p ON p.id = ip.project_id
		 WHERE ip.initiative_id = $1::uuid
		   AND `+projectAccessibleSQL("p", "$2::uuid")+`
		 ORDER BY ip.added_at`, initiativeID, actorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []InitiativeProject{}
	for rows.Next() {
		var mp InitiativeProject
		var labelsJSON []byte
		if err := rows.Scan(&mp.ProjectID, &mp.ProjectName, &mp.AddedAt, &labelsJSON); err != nil {
			return nil, err
		}
		labels, err := decodeProjectLabels(labelsJSON)
		if err != nil {
			return nil, err
		}
		mp.Labels = labels
		out = append(out, mp)
	}
	return out, rows.Err()
}

// GetInitiativeRollup aggregates live (non-deleted) issue counts by state
// group across ALL member projects in ONE query, plus a per-project
// breakdown. Completion % = done-group (completed/cancelled) issues over
// total; projects with zero issues appear with zero counts. Any role may
// read.
func GetInitiativeRollup(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID, initiativeID string) (*InitiativeRollup, error) {
	wsID, _, err := resolveInitiativeWorkspace(ctx, pool, wsSlug, actorID, false)
	if err != nil {
		return nil, err
	}
	if _, err := resolveInitiative(ctx, pool, wsID, initiativeID); err != nil {
		return nil, err
	}
	done := map[string]bool{}
	for _, g := range doneStateGroups {
		done[g] = true
	}
	// C92T2: same filter as ListInitiativeProjects — the rollup's
	// per-project breakdown and its totals must not count issues of
	// linked private projects the actor cannot reach.
	rows, err := pool.Query(ctx,
		`SELECT p.id::text, p.name, s."group", COUNT(i.id)
		 FROM initiative_projects ip
		 JOIN projects p ON p.id = ip.project_id
		 LEFT JOIN issues i ON i.project_id = p.id AND i.deleted_at IS NULL
		 LEFT JOIN states s ON s.id = i.state_id
		 WHERE ip.initiative_id = $1::uuid
		   AND `+projectAccessibleSQL("p", "$2::uuid")+`
		 GROUP BY p.id, p.name, s."group"
		 ORDER BY p.name, s."group"`, initiativeID, actorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	roll := &InitiativeRollup{
		InitiativeID: initiativeID,
		ByGroup:      map[string]int64{},
		Projects:     []InitiativeProjectRollup{},
	}
	byProject := map[string]int{} // project id → index into roll.Projects
	for rows.Next() {
		var projectID, projectName string
		var group *string
		var count int64
		if err := rows.Scan(&projectID, &projectName, &group, &count); err != nil {
			return nil, err
		}
		idx, ok := byProject[projectID]
		if !ok {
			idx = len(roll.Projects)
			roll.Projects = append(roll.Projects, InitiativeProjectRollup{
				ProjectID:   projectID,
				ProjectName: projectName,
				ByGroup:     map[string]int64{},
			})
			byProject[projectID] = idx
		}
		pr := &roll.Projects[idx]
		if group == nil {
			// LEFT JOIN row for a project with zero issues — the
			// breakdown entry already exists; nothing to count.
			continue
		}
		pr.ByGroup[*group] += count
		roll.ByGroup[*group] += count
		pr.Total += count
		roll.Total += count
		if done[*group] {
			pr.DoneCount += count
			roll.DoneCount += count
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	roll.CompletionPct = pctOf(roll.DoneCount, roll.Total)
	for i := range roll.Projects {
		roll.Projects[i].CompletionPct = pctOf(roll.Projects[i].DoneCount, roll.Projects[i].Total)
	}
	return roll, nil
}

// pctOf returns done/total as a percentage; 0 when total is 0.
func pctOf(done, total int64) float64 {
	if total == 0 {
		return 0
	}
	return float64(done) * 100 / float64(total)
}

// announceInitiativeUpdated broadcasts initiative.updated on the
// workspace channel. Best-effort: nil broadcaster in tests → silent.
// Initiatives are workspace-scoped, so the channel is the workspace one.
func announceInitiativeUpdated(ctx context.Context, wsSlug, initiativeID string) {
	announce(
		[]string{workspaceChannel(wsSlug)},
		EventInitiativeUpdated,
		map[string]string{"id": initiativeID},
	)
}
