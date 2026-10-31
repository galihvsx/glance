package service

// Epics (Plane "epics" / long-running containers), C71T0 backend.
//
// Schema: 000069 (epics + epic_issues + initiative_epics junctions).
// Issue membership uses the epic_issues junction table — like
// module_issues — NOT an issues.epic_id column. The junction keeps
// deletes cheap (ON DELETE CASCADE) and mirrors the modules domain.
//
// Conventions (mirror modules):
//   - Tenancy: every op resolves workspace membership + project via
//     resolveEpicProject. A bad slug or non-member caller surfaces
//     ErrNotFound ("workspace not found"); a bad project identifier
//     surfaces ErrProjectNotFound; a bad epic id surfaces
//     ErrEpicNotFound. Distinct 404s on purpose (Task 11 ruling).
//   - Roles: any member (guest 5+) may read; mutations need member (15)+.
//   - PATCH tri-state for nullable fields: nil = untouched, pointer to
//     "" = clear (NULL), pointer to a value = set. (Description only —
//     epics carry no dates/lead/status vocabulary; lifecycle is archive,
//     per Plane's Archive Epics.)
//   - Delete guard: deleting an epic that still has issues is 409
//     (ErrEpicHasIssues) unless force=true, in which case the junction
//     rows cascade and the issues themselves are untouched.
//   - Archive, not delete-by-default: archived_at set/clear via
//     ArchiveEpic/UnarchiveEpic; archived epics are hidden from the
//     default list (?include_archived=true to show).
//   - Progress: epic detail carries progress = counts of live member
//     issues by state_group + total, computed in ONE aggregate query
//     (same vocabulary as the initiative rollup).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrEpicNotFound is returned when the epic id matches nothing in
	// the project. Deliberately distinct from ErrProjectNotFound: on this
	// path the workspace and project are confirmed and the caller is a
	// member.
	ErrEpicNotFound = errors.New("service: epic not found")
	// ErrEpicConflict is returned when the epic name is already taken
	// in the project (UNIQUE(project_id, name)).
	ErrEpicConflict = errors.New("service: epic name already exists")
	// ErrInvalidEpic is returned for malformed epic input.
	ErrInvalidEpic = errors.New("service: invalid epic")
	// ErrInvalidEpicID is returned when an epic id — or an issue id
	// passed to an epic endpoint — is not a syntactically valid UUID.
	// The handler maps it to 400 bad_request.
	ErrInvalidEpicID = errors.New("service: invalid epic id")
	// ErrEpicHasIssues is returned when deleting an epic that still
	// has issues without force=true. The handler maps it to 409.
	ErrEpicHasIssues = errors.New("service: epic still has issues")
	// ErrInvalidEpicFilter is returned when a ListEpics filter param
	// fails validation: unknown status, malformed uuid, unparseable
	// date, unknown sort/order. The handler maps it to 400 with the
	// wrapped detail as the message.
	ErrInvalidEpicFilter = errors.New("service: invalid epic list filter")
)

// Epic is one project-scoped long-running container for work that spans
// cycles. Progress is populated on GetEpic (detail) only — the list
// carries IssueCount alone so the list stays cheap.
type Epic struct {
	ID          string        `json:"id"`
	ProjectID   string        `json:"project_id"`
	Name        string        `json:"name"`
	Description *string       `json:"description,omitempty"`
	ArchivedAt  *time.Time    `json:"archived_at,omitempty"`
	CreatedBy   *string       `json:"created_by,omitempty"`
	IssueCount  int64         `json:"issue_count"`
	Progress    *EpicProgress `json:"progress,omitempty"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
}

// EpicProgress is the state-group progress Plane tracks on epics:
// counts of live member issues by state_group + total.
type EpicProgress struct {
	Total   int64            `json:"total"`
	ByGroup map[string]int64 `json:"by_group"`
}

// EpicInput carries epic creation fields.
type EpicInput struct {
	Name        string
	Description *string
}

// EpicPatch is a partial epic update: nil fields are untouched; a
// non-nil pointer to "" clears description (NULL).
type EpicPatch struct {
	Name        *string
	Description *string
}

// EpicRef is the per-issue epic reference embedded in issue detail:
// {id, name, archived_at}. Archived epics stay visible here (the UI
// dims them) — membership truth outlives archive.
type EpicRef struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	ArchivedAt *time.Time `json:"archived_at,omitempty"`
}

// InitiativeEpic is an epic linked to an initiative (list response).
type InitiativeEpic struct {
	EpicID      string `json:"epic_id"`
	EpicName    string `json:"epic_name"`
	ProjectID   string `json:"project_id"`
	ProjectName string `json:"project_name"`
	AddedAt     string `json:"added_at"`
}

// resolveEpicProject resolves (projectID, role) for the caller.
// Guests may read; callers pass needMember to enforce member (15)+.
func resolveEpicProject(ctx context.Context, q queryRower, wsSlug, identifier, actorID string, needMember bool) (projectID string, role int, err error) {
	_, projectID, role, err = resolveIssueProject(ctx, q, wsSlug, identifier, actorID)
	if err != nil {
		return "", 0, err
	}
	if needMember && role < RoleMember {
		return "", 0, ErrForbidden
	}
	return projectID, role, nil
}

// epicColumns is the shared SELECT list (no issue count).
const epicColumns = `id::text, project_id::text, name, description,
	archived_at, created_by::text, created_at, updated_at`

// epicColumnsCounted adds the live issue count for list/get responses.
const epicColumnsCounted = epicColumns + `,
	(SELECT COUNT(*) FROM epic_issues ei WHERE ei.epic_id = epics.id)`

func scanEpic(row pgx.Row) (Epic, error) {
	var e Epic
	err := row.Scan(
		&e.ID, &e.ProjectID, &e.Name, &e.Description,
		&e.ArchivedAt, &e.CreatedBy, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		return Epic{}, err
	}
	return e, nil
}

func scanEpicCounted(row pgx.Row) (Epic, error) {
	var e Epic
	err := row.Scan(
		&e.ID, &e.ProjectID, &e.Name, &e.Description,
		&e.ArchivedAt, &e.CreatedBy, &e.CreatedAt, &e.UpdatedAt,
		&e.IssueCount)
	if err != nil {
		return Epic{}, err
	}
	return e, nil
}

func resolveEpic(ctx context.Context, q queryRower, projectID, epicID string) (Epic, error) {
	if !isUUIDFormat(epicID) {
		// Malformed ids match nothing — 400, not a 500 from the ::uuid cast.
		return Epic{}, ErrInvalidEpicID
	}
	e, err := scanEpic(q.QueryRow(ctx,
		`SELECT `+epicColumns+`
		 FROM epics WHERE id = $1::uuid AND project_id = $2::uuid`,
		epicID, projectID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Epic{}, ErrEpicNotFound
		}
		return Epic{}, err
	}
	return e, nil
}

// epicProgress loads the state-group counts for one epic in ONE
// aggregate query: live (non-deleted) member issues grouped by
// states."group", plus the total.
func epicProgress(ctx context.Context, pool *pgxpool.Pool, epicID string) (*EpicProgress, error) {
	rows, err := pool.Query(ctx,
		`SELECT s."group", COUNT(i.id)
		 FROM epic_issues ei
		 JOIN issues i ON i.id = ei.issue_id AND i.deleted_at IS NULL
		 LEFT JOIN states s ON s.id = i.state_id
		 WHERE ei.epic_id = $1::uuid
		 GROUP BY s."group"`, epicID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	p := &EpicProgress{ByGroup: map[string]int64{}}
	for rows.Next() {
		var group *string
		var count int64
		if err := rows.Scan(&group, &count); err != nil {
			return nil, err
		}
		if group == nil {
			// An issue with no state (should not happen — every issue
			// is created in a state) is still a member: count it in the
			// total but in no group.
			p.Total += count
			continue
		}
		p.ByGroup[*group] += count
		p.Total += count
	}
	return p, rows.Err()
}

// CreateEpic creates an epic in the project. Member (15)+.
func CreateEpic(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string, in EpicInput) (*Epic, error) {
	projectID, _, err := resolveEpicProject(ctx, pool, wsSlug, identifier, actorID, true)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, ErrNameRequired
	}
	var desc *string
	if in.Description != nil {
		if d := strings.TrimSpace(*in.Description); d != "" {
			desc = &d
		}
	}
	e, err := scanEpic(pool.QueryRow(ctx,
		`INSERT INTO epics (project_id, name, description, created_by)
		 VALUES ($1::uuid, $2, $3, $4::uuid)
		 RETURNING `+epicColumns,
		projectID, name, desc, actorID))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrEpicConflict
		}
		return nil, err
	}
	announceEpicUpdated(ctx, pool, e.ID, projectID)
	return &e, nil
}

// EpicListFilter carries the rich filter params for ListEpics
// (C74T0 — Plane "epics rich filters" parity, mirroring the C73
// initiative filters). All user input is parameterized in the query;
// Sort/Order are whitelisted to static SQL fragments, never
// interpolated.
//
// Status semantics: an explicit Statuses list always wins. Otherwise
// IncludeArchived (the legacy ?include_archived=1|true) means "no
// status exclusion"; the default excludes archived.
type EpicListFilter struct {
	IncludeArchived bool
	Statuses        []string
	Q               string
	CreatedBy       string
	CreatedFrom     string // ISO-8601 (RFC3339 or date-only); "" = unbounded
	CreatedTo       string
	InitiativeID    string
	Sort            string // name|created_at|updated_at|issue_count; "" = created_at
	Order           string // asc|desc; "" = asc for name, desc otherwise
}

// epicSortColumns whitelists sort keys to static ORDER BY expressions.
// issue_count is a correlated subquery over the epic_issues junction —
// computed, not interpolated.
var epicSortColumns = map[string]string{
	"name":        `name`,
	"created_at":  `created_at`,
	"updated_at":  `updated_at`,
	"issue_count": `(SELECT COUNT(*) FROM epic_issues ei WHERE ei.epic_id = epics.id)`,
}

// parseEpicDate accepts ISO-8601: full RFC3339 timestamps and bare
// dates (2006-01-02). "" means unbounded. A bare date is
// whole-day-inclusive: for a lower bound it means the day's start,
// for an upper bound the day's end.
func parseEpicDate(v string, endOfDay bool) (*time.Time, error) {
	if v == "" {
		return nil, nil
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return &t, nil
	}
	if t, err := time.Parse("2006-01-02", v); err == nil {
		if endOfDay {
			end := t.Add(24*time.Hour - time.Nanosecond)
			return &end, nil
		}
		return &t, nil
	}
	return nil, fmt.Errorf("%w: unparseable date %q (want ISO-8601)", ErrInvalidEpicFilter, v)
}

// ListEpics returns the project's epics with rich filters (C74T0 —
// mirror of the C73 initiative filters) and live issue counts. The
// result is never nil — an empty match is []. Any role may read.
func ListEpics(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string, filter EpicListFilter) ([]Epic, error) {
	projectID, _, err := resolveEpicProject(ctx, pool, wsSlug, identifier, actorID, false)
	if err != nil {
		return nil, err
	}

	var conds []string
	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	projArg := arg(projectID)

	// Status: explicit list wins; otherwise legacy includeArchived maps
	// to "no status exclusion"; default excludes archived. Epics carry
	// no status vocabulary — lifecycle is archive, so the values are
	// "active" (archived_at IS NULL) and "archived" (IS NOT NULL).
	if len(filter.Statuses) > 0 {
		wantActive, wantArchived := false, false
		for _, s := range filter.Statuses {
			switch s {
			case "active":
				wantActive = true
			case "archived":
				wantArchived = true
			default:
				return nil, fmt.Errorf("%w: unknown status %q (want active|archived)", ErrInvalidEpicFilter, s)
			}
		}
		if wantActive && !wantArchived {
			conds = append(conds, `archived_at IS NULL`)
		} else if wantArchived && !wantActive {
			conds = append(conds, `archived_at IS NOT NULL`)
		}
		// Both (or repeats): no exclusion.
	} else if !filter.IncludeArchived {
		conds = append(conds, `archived_at IS NULL`)
	}

	// Text search: ILIKE on name + description, fully parameterized.
	if q := strings.TrimSpace(filter.Q); q != "" {
		conds = append(conds, `(name ILIKE `+arg("%"+q+"%")+` OR description ILIKE `+arg("%"+q+"%")+`)`)
	}

	// Created-by: malformed uuid is a client error, not a ::uuid 500.
	if filter.CreatedBy != "" {
		if !isUUIDFormat(filter.CreatedBy) {
			return nil, fmt.Errorf("%w: malformed created_by %q", ErrInvalidEpicFilter, filter.CreatedBy)
		}
		conds = append(conds, `created_by = `+arg(filter.CreatedBy)+`::uuid`)
	}

	// Created-at range: unparseable dates are client errors.
	if from, err := parseEpicDate(filter.CreatedFrom, false); err != nil {
		return nil, err
	} else if from != nil {
		conds = append(conds, `created_at >= `+arg(*from))
	}
	if to, err := parseEpicDate(filter.CreatedTo, true); err != nil {
		return nil, err
	} else if to != nil {
		conds = append(conds, `created_at <= `+arg(*to))
	}

	// Linked initiative via the initiative_epics junction; a
	// valid-but-unknown initiative id simply matches nothing.
	if filter.InitiativeID != "" {
		if !isUUIDFormat(filter.InitiativeID) {
			return nil, fmt.Errorf("%w: malformed initiative_id %q", ErrInvalidEpicFilter, filter.InitiativeID)
		}
		conds = append(conds, `EXISTS (SELECT 1 FROM initiative_epics ie
			WHERE ie.epic_id = epics.id AND ie.initiative_id = `+arg(filter.InitiativeID)+`::uuid)`)
	}

	// Sort/order: whitelisted columns only — unknown values are 400.
	sort := filter.Sort
	if sort == "" {
		sort = "created_at"
	}
	col, ok := epicSortColumns[sort]
	if !ok {
		return nil, fmt.Errorf("%w: unknown sort %q (want name|created_at|updated_at|issue_count)", ErrInvalidEpicFilter, filter.Sort)
	}
	order := strings.ToLower(filter.Order)
	if order == "" {
		order = "desc"
		if sort == "name" {
			order = "asc"
		}
	}
	if order != "asc" && order != "desc" {
		return nil, fmt.Errorf("%w: unknown order %q (want asc|desc)", ErrInvalidEpicFilter, filter.Order)
	}

	where := `project_id = ` + projArg + `::uuid`
	if len(conds) > 0 {
		where += " AND " + strings.Join(conds, " AND ")
	}
	// Deterministic default sort (created_at desc) with a name,id
	// tiebreak so pagination-free lists are stable.
	rows, err := pool.Query(ctx,
		`SELECT `+epicColumnsCounted+`
		 FROM epics WHERE `+where+`
		 ORDER BY `+col+` `+order+`, name, id`,
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Epic{}
	for rows.Next() {
		e, err := scanEpicCounted(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// GetEpic returns one epic with its live issue count and state-group
// progress. Any role may read.
func GetEpic(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, epicID string) (*Epic, error) {
	projectID, _, err := resolveEpicProject(ctx, pool, wsSlug, identifier, actorID, false)
	if err != nil {
		return nil, err
	}
	if !isUUIDFormat(epicID) {
		return nil, ErrInvalidEpicID
	}
	e, err := scanEpicCounted(pool.QueryRow(ctx,
		`SELECT `+epicColumnsCounted+`
		 FROM epics WHERE id = $1::uuid AND project_id = $2::uuid`,
		epicID, projectID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrEpicNotFound
		}
		return nil, err
	}
	p, err := epicProgress(ctx, pool, epicID)
	if err != nil {
		return nil, err
	}
	e.Progress = p
	return &e, nil
}

// UpdateEpic applies a partial epic update. Member (15)+. Only non-nil
// patch fields are written; "" clears description (NULL). An empty patch
// is ErrNothingToUpdate.
func UpdateEpic(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, epicID string, patch EpicPatch) (*Epic, error) {
	projectID, _, err := resolveEpicProject(ctx, pool, wsSlug, identifier, actorID, true)
	if err != nil {
		return nil, err
	}
	if _, err := resolveEpic(ctx, pool, projectID, epicID); err != nil {
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
	if len(sets) == 0 {
		return nil, ErrNothingToUpdate
	}
	sets = append(sets, "updated_at = now()")

	ue, err := scanEpic(pool.QueryRow(ctx,
		`UPDATE epics SET `+strings.Join(sets, ", ")+`
		 WHERE id = `+arg(epicID)+`::uuid AND project_id = `+arg(projectID)+`::uuid
		 RETURNING `+epicColumns,
		args...))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrEpicConflict
		}
		return nil, err
	}
	// Re-read with the live issue count + progress (the update itself
	// never changes membership, so a second read is exact).
	got, err := GetEpic(ctx, pool, wsSlug, identifier, actorID, ue.ID)
	if err != nil {
		return nil, err
	}
	announceEpicUpdated(ctx, pool, ue.ID, projectID)
	return got, nil
}

// DeleteEpic deletes an epic (membership + initiative-link rows cascade;
// the issues themselves are untouched). Member (15)+. Without force, an
// epic that still has issues is 409 (ErrEpicHasIssues).
func DeleteEpic(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, epicID string, force bool) error {
	projectID, _, err := resolveEpicProject(ctx, pool, wsSlug, identifier, actorID, true)
	if err != nil {
		return err
	}
	if _, err := resolveEpic(ctx, pool, projectID, epicID); err != nil {
		return err
	}
	if !force {
		var n int64
		if err := pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM epic_issues WHERE epic_id = $1::uuid`,
			epicID).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return ErrEpicHasIssues
		}
	}
	_, err = pool.Exec(ctx,
		`DELETE FROM epics WHERE id = $1::uuid AND project_id = $2::uuid`,
		epicID, projectID)
	if err != nil {
		return err
	}
	announceEpicUpdated(ctx, pool, epicID, projectID)
	return nil
}

// ArchiveEpic archives an epic: archived_at is set and the epic drops
// out of the default list. Member (15)+. An activity row is written on
// every member issue so the issue feed stays truthful.
func ArchiveEpic(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, epicID string) (*Epic, error) {
	return setEpicArchived(ctx, pool, wsSlug, identifier, actorID, epicID, true)
}

// UnarchiveEpic restores an archived epic. Member (15)+.
func UnarchiveEpic(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, epicID string) (*Epic, error) {
	return setEpicArchived(ctx, pool, wsSlug, identifier, actorID, epicID, false)
}

func setEpicArchived(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, epicID string, archived bool) (*Epic, error) {
	projectID, _, err := resolveEpicProject(ctx, pool, wsSlug, identifier, actorID, true)
	if err != nil {
		return nil, err
	}
	e, err := resolveEpic(ctx, pool, projectID, epicID)
	if err != nil {
		return nil, err
	}
	var archivedAt any
	if archived {
		archivedAt = time.Now()
	}
	if _, err := pool.Exec(ctx,
		`UPDATE epics SET archived_at = $1, updated_at = now()
		 WHERE id = $2::uuid AND project_id = $3::uuid`,
		archivedAt, epicID, projectID); err != nil {
		return nil, err
	}
	// Activity rows on every member issue (the feed shows the archive).
	memberIDs, err := epicMemberIssueIDs(ctx, pool, epicID)
	if err != nil {
		return nil, err
	}
	field := "epic_archived"
	if !archived {
		field = "epic_unarchived"
	}
	if err := writeEpicActivityRows(ctx, pool, actorID, &e, field, memberIDs); err != nil {
		return nil, err
	}
	got, err := GetEpic(ctx, pool, wsSlug, identifier, actorID, epicID)
	if err != nil {
		return nil, err
	}
	announceEpicUpdated(ctx, pool, epicID, projectID)
	return got, nil
}

// epicMemberIssueIDs returns the live (non-deleted) member issue ids.
func epicMemberIssueIDs(ctx context.Context, pool *pgxpool.Pool, epicID string) ([]string, error) {
	rows, err := pool.Query(ctx,
		`SELECT ei.issue_id::text FROM epic_issues ei
		 JOIN issues i ON i.id = ei.issue_id AND i.deleted_at IS NULL
		 WHERE ei.epic_id = $1::uuid`, epicID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// writeEpicActivityRows writes one activity row per affected issue so the
// issue Transitions/Activity feed stays truthful about epic membership
// changes. new_value carries the epic ref as JSON.
func writeEpicActivityRows(ctx context.Context, pool *pgxpool.Pool, actorID string, e *Epic, field string, issueIDs []string) error {
	if len(issueIDs) == 0 {
		return nil
	}
	newVal, err := json.Marshal(map[string]any{"epic_id": e.ID, "epic_name": e.Name})
	if err != nil {
		return err
	}
	for _, id := range issueIDs {
		if _, err := pool.Exec(ctx,
			`INSERT INTO issue_activities (issue_id, actor_id, field, new_value)
			 VALUES ($1::uuid, $2::uuid, $3, $4::jsonb)`,
			id, actorID, field, string(newVal)); err != nil {
			return err
		}
	}
	return nil
}

// AddEpicIssues bulk-adds issues to an epic. Every issue must be a live
// (non-deleted) issue of the same project, otherwise ErrIssueNotFound.
// The add is idempotent: repeats are no-ops via ON CONFLICT DO NOTHING —
// never a 409, and no duplicate activity rows. Member (15)+; an empty id
// list is ErrBulkEmptyIDs.
func AddEpicIssues(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, epicID string, issueIDs []string) error {
	if len(issueIDs) == 0 {
		return ErrBulkEmptyIDs
	}
	projectID, _, err := resolveEpicProject(ctx, pool, wsSlug, identifier, actorID, true)
	if err != nil {
		return err
	}
	e, err := resolveEpic(ctx, pool, projectID, epicID)
	if err != nil {
		return err
	}
	// Validate ownership + liveness for every id first: a partial add
	// that silently drops foreign issues would be worse than a 404.
	for _, id := range issueIDs {
		if !isUUIDFormat(id) {
			// Malformed ids are a client error — 400, not a 500 from
			// the ::uuid cast.
			return ErrInvalidEpicID
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
	// RETURNING gives exactly the newly-added ids — only those get
	// activity rows, so re-adds write no duplicates.
	rows, err := pool.Query(ctx,
		`INSERT INTO epic_issues (epic_id, issue_id)
		 SELECT $1::uuid, unnest($2::uuid[])
		 ON CONFLICT DO NOTHING
		 RETURNING issue_id::text`, epicID, issueIDs)
	if err != nil {
		return err
	}
	var added []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		added = append(added, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if err := writeEpicActivityRows(ctx, pool, actorID, &e, "epic_added", added); err != nil {
		return err
	}
	announceEpicUpdated(ctx, pool, epicID, projectID)
	return nil
}

// RemoveEpicIssues bulk-removes issues from an epic. Idempotent:
// removing a non-member is a no-op (and writes no activity row).
// Member (15)+; an empty id list is ErrBulkEmptyIDs. Malformed ids are
// 400 (the ::uuid[] cast would 500).
func RemoveEpicIssues(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, epicID string, issueIDs []string) error {
	if len(issueIDs) == 0 {
		return ErrBulkEmptyIDs
	}
	projectID, _, err := resolveEpicProject(ctx, pool, wsSlug, identifier, actorID, true)
	if err != nil {
		return err
	}
	e, err := resolveEpic(ctx, pool, projectID, epicID)
	if err != nil {
		return err
	}
	for _, id := range issueIDs {
		if !isUUIDFormat(id) {
			return ErrInvalidEpicID
		}
	}
	rows, err := pool.Query(ctx,
		`DELETE FROM epic_issues
		 WHERE epic_id = $1::uuid AND issue_id = ANY($2::uuid[])
		 RETURNING issue_id::text`, epicID, issueIDs)
	if err != nil {
		return err
	}
	var removed []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		removed = append(removed, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if err := writeEpicActivityRows(ctx, pool, actorID, &e, "epic_removed", removed); err != nil {
		return err
	}
	announceEpicUpdated(ctx, pool, epicID, projectID)
	return nil
}

// ListEpicIssues returns the epic's live issues as full list items
// (assignees + labels aggregated, one query). Any role may read.
func ListEpicIssues(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, epicID string) ([]IssueListItem, error) {
	projectID, _, err := resolveEpicProject(ctx, pool, wsSlug, identifier, actorID, false)
	if err != nil {
		return nil, err
	}
	if _, err := resolveEpic(ctx, pool, projectID, epicID); err != nil {
		return nil, err
	}
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx,
		`SELECT `+prefixColumns("i.", issueColumns)+`, `+assigneesAgg+`, `+labelsAgg+`
		 FROM epic_issues ei
		 JOIN issues i ON i.id = ei.issue_id AND i.deleted_at IS NULL
		 WHERE ei.epic_id = $1::uuid
		 ORDER BY i.created_at`, epicID)
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

// resolveEpicWorkspace resolves (workspaceID, epic projectID) for a
// workspace-scoped initiative↔epic op: the epic must belong to a project
// of the workspace, otherwise ErrProjectNotFound.
func resolveEpicWorkspace(ctx context.Context, q queryRower, wsSlug, actorID string, needMember bool, epicID string) (wsID, projectID string, err error) {
	wsID, _, err = resolveInitiativeWorkspace(ctx, q, wsSlug, actorID, needMember)
	if err != nil {
		return "", "", err
	}
	if !isUUIDFormat(epicID) {
		return "", "", ErrInvalidEpicID
	}
	err = q.QueryRow(ctx,
		`SELECT e.project_id::text FROM epics e
		 JOIN projects p ON p.id = e.project_id
		 WHERE e.id = $1::uuid AND p.workspace_id = $2::uuid`,
		epicID, wsID).Scan(&projectID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// No such epic, or an epic of another workspace — either
			// way it names nothing linkable here. Distinguish like the
			// module domain does: existence elsewhere is
			// ErrProjectNotFound only when it belongs to a foreign
			// workspace; a truly unknown id is ErrEpicNotFound.
			var exists bool
			if qe := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM epics WHERE id = $1::uuid)`, epicID).Scan(&exists); qe == nil && !exists {
				return "", "", ErrEpicNotFound
			}
			return "", "", ErrProjectNotFound
		}
		return "", "", err
	}
	// C92T0: the epic names a project, so linking it to an initiative
	// is a project-scoped act — the effective-access order applies
	// (a private project the caller cannot reach is ErrNotFound, and
	// needMember gates on the effective role).
	_, effRole, err := effectiveRoleForProjectID(ctx, q, wsID, projectID, actorID)
	if err != nil {
		return "", "", err
	}
	if needMember && effRole < RoleMember {
		return "", "", ErrForbidden
	}
	return wsID, projectID, nil
}

// AddInitiativeEpic links an epic to an initiative. The epic must belong
// to a project of the initiative's workspace. Idempotent: repeats are
// no-ops via ON CONFLICT DO NOTHING — never a 409. Member (15)+.
func AddInitiativeEpic(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID, initiativeID, epicID string) error {
	wsID, projectID, err := resolveEpicWorkspace(ctx, pool, wsSlug, actorID, true, epicID)
	if err != nil {
		return err
	}
	if _, err := resolveInitiative(ctx, pool, wsID, initiativeID); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO initiative_epics (initiative_id, epic_id)
		 VALUES ($1::uuid, $2::uuid) ON CONFLICT DO NOTHING`,
		initiativeID, epicID); err != nil {
		return err
	}
	announceEpicUpdated(ctx, pool, epicID, projectID)
	announceInitiativeUpdated(ctx, wsSlug, initiativeID)
	return nil
}

// RemoveInitiativeEpic unlinks an epic from an initiative. Idempotent:
// removing a non-link is a no-op. Member (15)+.
func RemoveInitiativeEpic(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID, initiativeID, epicID string) error {
	wsID, projectID, err := resolveEpicWorkspace(ctx, pool, wsSlug, actorID, true, epicID)
	if err != nil {
		return err
	}
	if _, err := resolveInitiative(ctx, pool, wsID, initiativeID); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx,
		`DELETE FROM initiative_epics
		 WHERE initiative_id = $1::uuid AND epic_id = $2::uuid`,
		initiativeID, epicID); err != nil {
		return err
	}
	announceEpicUpdated(ctx, pool, epicID, projectID)
	announceInitiativeUpdated(ctx, wsSlug, initiativeID)
	return nil
}

// ListInitiativeEpics returns the initiative's linked epics (with their
// projects) ordered by addition. Any role may read.
func ListInitiativeEpics(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID, initiativeID string) ([]InitiativeEpic, error) {
	wsID, _, err := resolveInitiativeWorkspace(ctx, pool, wsSlug, actorID, false)
	if err != nil {
		return nil, err
	}
	if _, err := resolveInitiative(ctx, pool, wsID, initiativeID); err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx,
		`SELECT e.id::text, e.name, e.project_id::text, p.name, ie.added_at::text
		 FROM initiative_epics ie
		 JOIN epics e ON e.id = ie.epic_id
		 JOIN projects p ON p.id = e.project_id
		 WHERE ie.initiative_id = $1::uuid
		 ORDER BY ie.added_at`, initiativeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []InitiativeEpic{}
	for rows.Next() {
		var ie InitiativeEpic
		if err := rows.Scan(&ie.EpicID, &ie.EpicName, &ie.ProjectID, &ie.ProjectName, &ie.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, ie)
	}
	return out, rows.Err()
}
