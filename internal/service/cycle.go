package service

// Cycles (Task 22, spec §4): project-scoped CRUD, bulk issue membership,
// live progress snapshots, and the rollover core the in-process ticker
// (internal/ticker/ticker.go) drives.
//
// Conventions:
//   - Tenancy: every op resolves workspace membership + project via
//     resolveIssueProject. A bad slug or non-member caller surfaces
//     ErrNotFound ("workspace not found"); a bad project identifier
//     surfaces ErrProjectNotFound; a bad cycle id surfaces
//     ErrCycleNotFound. Distinct 404s on purpose (Task 11 ruling).
//   - Roles: any member (guest 5+) may read; mutations need member (15)+.
//   - Status is derived from dates against the caller's clock:
//     start_date > today → upcoming, otherwise current. Creation never
//     yields completed: only the ticker flips a live cycle to completed
//     (via CompleteCycle), so every completion runs the freeze + rollover
//     exactly once. A manual date change recomputes status the same way,
//     which is what keeps the column honest if dates move.
//   - Rollover rule (documented contract, see CompleteCycle): on cycle
//     end the ticker completes the cycle, freezes its progress snapshot,
//     and moves incomplete issues (state group not in
//     completed/cancelled). If a next upcoming/current cycle exists they
//     transfer immediately; otherwise they detach to the project backlog
//     once now >= end_date + close_in_days (NULL = 0 = detach at
//     completion).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrCycleNotFound is returned when the cycle id matches nothing in
	// the project. Deliberately distinct from ErrProjectNotFound: on this
	// path the workspace and project are confirmed and the caller is a
	// member.
	ErrCycleNotFound = errors.New("service: cycle not found")
	// ErrCycleConflict is returned when the cycle name is already taken
	// in the project (UNIQUE(project_id, name)).
	ErrCycleConflict = errors.New("service: cycle name already exists")
	// ErrInvalidCycle is returned for malformed cycle input: empty name,
	// or start_date after end_date.
	ErrInvalidCycle = errors.New("service: invalid cycle")
	// ErrInvalidCloseInDays is returned when close_in_days is negative.
	ErrInvalidCloseInDays = errors.New("service: close_in_days must be >= 0")
	// ErrInvalidCycleID is returned when a cycle id — or an issue id
	// passed to a cycle endpoint — is not a syntactically valid UUID.
	// The handler maps it to 400 bad_request.
	ErrInvalidCycleID = errors.New("service: invalid cycle id")
)

// state groups counted in a progress snapshot. All six groups from the
// states CHECK vocabulary — triage counts as incomplete, and the frozen
// snapshot keeps it so a completed cycle's final report card is whole.
var snapshotGroups = []string{"triage", "backlog", "unstarted", "started", "completed", "cancelled"}

// queryQuerier abstracts *pgxpool.Pool and pgx.Tx for the multi-row reads
// below (the shared queryRower only covers QueryRow).
type queryQuerier interface {
	queryRower
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Cycle is one project timebox.
type Cycle struct {
	ID               string           `json:"id"`
	ProjectID        string           `json:"project_id"`
	Name             string           `json:"name"`
	StartDate        time.Time        `json:"start_date"`
	EndDate          time.Time        `json:"end_date"`
	Status           string           `json:"status"`
	ProgressSnapshot map[string]int64 `json:"progress_snapshot"`
	CreatedAt        time.Time        `json:"created_at"`
	UpdatedAt        time.Time        `json:"updated_at"`
}

// CycleInput carries cycle creation fields.
type CycleInput struct {
	Name      string
	StartDate time.Time
	EndDate   time.Time
}

// CyclePatch is a partial cycle update: nil fields are left untouched.
type CyclePatch struct {
	Name      *string
	StartDate *time.Time
	EndDate   *time.Time
}

// cycleStatusFor derives the birth status from dates against now (date
// granularity: a cycle ending today is still current today). It never
// returns completed: only the ticker flips a live cycle to completed, via
// CompleteCycle, so every completion runs the freeze + rollover exactly
// once. A cycle created with end_date already past is born current and the
// ticker completes it on the next pass (<= 1 minute later).
func cycleStatusFor(start, end, now time.Time) string {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if start.After(today) {
		return "upcoming"
	}
	return "current"
}

// resolveCycleProject resolves (projectID, role) for the caller and then
// the live cycle, all scoped to the project. Guests may read; callers
// pass needMember to enforce member (15)+ for mutations.
func resolveCycleProject(ctx context.Context, q queryRower, wsSlug, identifier, actorID string, needMember bool) (projectID string, role int, err error) {
	// Normalize like every other service entry point (CreateIssue,
	// CreateAutomationRule, ...): identifiers are stored uppercased,
	// and a raw lowercase path param must resolve the same project.
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return "", 0, err
	}
	_, projectID, role, err = resolveIssueProject(ctx, q, wsSlug, ident, actorID)
	if err != nil {
		return "", 0, err
	}
	if needMember && role < RoleMember {
		return "", 0, ErrForbidden
	}
	return projectID, role, nil
}

// cycleColumns is the shared SELECT list; scanCycle handles the nullable
// progress_snapshot (NULL → nil, unmarshalled when present).
const cycleColumns = `id::text, project_id::text, name, start_date, end_date, status,
	progress_snapshot, created_at, updated_at`

func scanCycle(row pgx.Row) (Cycle, error) {
	var c Cycle
	var snap []byte
	err := row.Scan(
		&c.ID, &c.ProjectID, &c.Name, &c.StartDate, &c.EndDate, &c.Status,
		&snap, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return Cycle{}, err
	}
	if snap != nil {
		var m map[string]int64
		if uerr := json.Unmarshal(snap, &m); uerr != nil {
			return Cycle{}, uerr
		}
		c.ProgressSnapshot = m
	}
	return c, nil
}

func resolveCycle(ctx context.Context, q queryRower, projectID, cycleID string) (Cycle, error) {
	if !isUUIDFormat(cycleID) {
		// Malformed ids match nothing — 400, not a 500 from the ::uuid cast.
		return Cycle{}, ErrInvalidCycleID
	}
	c, err := scanCycle(q.QueryRow(ctx,
		`SELECT `+cycleColumns+`
		 FROM cycles WHERE id = $1::uuid AND project_id = $2::uuid`,
		cycleID, projectID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Cycle{}, ErrCycleNotFound
		}
		return Cycle{}, err
	}
	return c, nil
}

// cycleProgressSnapshot counts live (non-deleted) cycle issues by state
// group in ONE query. Every snapshotGroups entry is present — empty groups
// are 0, never missing.
func cycleProgressSnapshot(ctx context.Context, q queryQuerier, cycleID string) (map[string]int64, error) {
	snap := make(map[string]int64, len(snapshotGroups))
	for _, g := range snapshotGroups {
		snap[g] = 0
	}
	rows, err := q.Query(ctx,
		`SELECT s."group", COUNT(i.id)
		 FROM cycle_issues ci
		 JOIN issues i ON i.id = ci.issue_id AND i.deleted_at IS NULL
		 JOIN states s ON s.id = i.state_id
		 WHERE ci.cycle_id = $1::uuid
		 GROUP BY s."group"`, cycleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var group string
		var n int64
		if err := rows.Scan(&group, &n); err != nil {
			return nil, err
		}
		snap[group] = n
	}
	return snap, rows.Err()
}

// CreateCycle creates a cycle in the project. Member (15)+; guests get
// ErrForbidden.
func CreateCycle(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string, in CycleInput) (*Cycle, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, ErrNameRequired
	}
	if in.StartDate.After(in.EndDate) {
		return nil, ErrInvalidCycle
	}
	projectID, _, err := resolveCycleProject(ctx, pool, wsSlug, identifier, actorID, true)
	if err != nil {
		return nil, err
	}
	status := cycleStatusFor(in.StartDate, in.EndDate, time.Now())
	c, err := scanCycle(pool.QueryRow(ctx,
		`INSERT INTO cycles (project_id, name, start_date, end_date, status)
		 VALUES ($1::uuid, $2, $3::date, $4::date, $5)
		 RETURNING `+cycleColumns,
		projectID, name, in.StartDate, in.EndDate, status))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrCycleConflict
		}
		return nil, err
	}
	snap, err := cycleProgressSnapshot(ctx, pool, c.ID)
	if err != nil {
		return nil, err
	}
	c.ProgressSnapshot = snap
	return &c, nil
}

// ListCycles returns the project's cycles ordered by start_date. Any role
// may read.
func ListCycles(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string) ([]Cycle, error) {
	projectID, _, err := resolveCycleProject(ctx, pool, wsSlug, identifier, actorID, false)
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx,
		`SELECT `+cycleColumns+`
		 FROM cycles WHERE project_id = $1::uuid
		 ORDER BY start_date, created_at`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Cycle{}
	for rows.Next() {
		c, err := scanCycle(rows)
		if err != nil {
			return nil, err
		}
		if err := fillSnapshot(ctx, pool, &c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetCycle returns one cycle with its live progress snapshot. Any role may
// read.
func GetCycle(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, cycleID string) (*Cycle, error) {
	projectID, _, err := resolveCycleProject(ctx, pool, wsSlug, identifier, actorID, false)
	if err != nil {
		return nil, err
	}
	c, err := resolveCycle(ctx, pool, projectID, cycleID)
	if err != nil {
		return nil, err
	}
	if err := fillSnapshot(ctx, pool, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// fillSnapshot sets c.ProgressSnapshot: completed cycles report the frozen
// snapshot the ticker wrote at completion (the cycle's final report
// card); live cycles compute it in one query. A completed cycle with no
// frozen snapshot (predates the column) falls back to the live count.
func fillSnapshot(ctx context.Context, pool *pgxpool.Pool, c *Cycle) error {
	if c.Status == "completed" && c.ProgressSnapshot != nil {
		return nil
	}
	snap, err := cycleProgressSnapshot(ctx, pool, c.ID)
	if err != nil {
		return err
	}
	c.ProgressSnapshot = snap
	return nil
}

// UpdateCycle applies a partial cycle update. Member (15)+. Only non-nil
// patch fields are written; a date change recomputes status the same way
// creation does. An empty patch is ErrNothingToUpdate.
func UpdateCycle(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, cycleID string, patch CyclePatch) (*Cycle, error) {
	projectID, _, err := resolveCycleProject(ctx, pool, wsSlug, identifier, actorID, true)
	if err != nil {
		return nil, err
	}
	c, err := resolveCycle(ctx, pool, projectID, cycleID)
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
		c.Name = name
	}
	start, end := c.StartDate, c.EndDate
	if patch.StartDate != nil {
		start = *patch.StartDate
		sets = append(sets, "start_date = "+arg(start)+"::date")
	}
	if patch.EndDate != nil {
		end = *patch.EndDate
		sets = append(sets, "end_date = "+arg(end)+"::date")
	}
	if start.After(end) {
		return nil, ErrInvalidCycle
	}
	if len(sets) == 0 {
		return nil, ErrNothingToUpdate
	}
	sets = append(sets, "status = "+arg(cycleStatusFor(start, end, time.Now())))
	sets = append(sets, "updated_at = now()")

	c, err = scanCycle(pool.QueryRow(ctx,
		`UPDATE cycles SET `+strings.Join(sets, ", ")+`
		 WHERE id = `+arg(cycleID)+`::uuid AND project_id = `+arg(projectID)+`::uuid
		 RETURNING `+cycleColumns,
		args...))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrCycleConflict
		}
		return nil, err
	}
	snap, err := cycleProgressSnapshot(ctx, pool, c.ID)
	if err != nil {
		return nil, err
	}
	c.ProgressSnapshot = snap
	announceCycleUpdated(ctx, pool, c.ID, projectID, c.Status)
	return &c, nil
}

// DeleteCycle deletes a cycle (membership rows cascade). Member (15)+.
func DeleteCycle(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, cycleID string) error {
	projectID, _, err := resolveCycleProject(ctx, pool, wsSlug, identifier, actorID, true)
	if err != nil {
		return err
	}
	if _, err := resolveCycle(ctx, pool, projectID, cycleID); err != nil {
		return err
	}
	_, err = pool.Exec(ctx,
		`DELETE FROM cycles WHERE id = $1::uuid AND project_id = $2::uuid`,
		cycleID, projectID)
	return err
}

// AddCycleIssues bulk-adds issues to a cycle. Every issue must be a live
// (non-deleted) issue of the same project, otherwise ErrIssueNotFound.
// The add is idempotent: repeats are no-ops via ON CONFLICT DO NOTHING —
// never a 409. Member (15)+; an empty id list is ErrBulkEmptyIDs.
func AddCycleIssues(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, cycleID string, issueIDs []string) error {
	if len(issueIDs) == 0 {
		return ErrBulkEmptyIDs
	}
	projectID, _, err := resolveCycleProject(ctx, pool, wsSlug, identifier, actorID, true)
	if err != nil {
		return err
	}
	if _, err := resolveCycle(ctx, pool, projectID, cycleID); err != nil {
		return err
	}
	// Validate ownership + liveness for every id first: a partial add
	// that silently drops foreign issues would be worse than a 404.
	for _, id := range issueIDs {
		if !isUUIDFormat(id) {
			// Malformed ids are a client error — 400, not a 500 from
			// the ::uuid cast.
			return ErrInvalidCycleID
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
		`INSERT INTO cycle_issues (cycle_id, issue_id)
		 SELECT $1::uuid, unnest($2::uuid[])
		 ON CONFLICT DO NOTHING`, cycleID, issueIDs)
	return err
}

// incompleteCycleIssues returns the ids of live cycle issues whose state
// group is neither completed nor cancelled — the ones rollover moves.
func incompleteCycleIssues(ctx context.Context, q queryQuerier, cycleID string) ([]string, error) {
	rows, err := q.Query(ctx,
		`SELECT i.id::text
		 FROM cycle_issues ci
		 JOIN issues i ON i.id = ci.issue_id AND i.deleted_at IS NULL
		 JOIN states s ON s.id = i.state_id
		 WHERE ci.cycle_id = $1::uuid
		   AND s."group" NOT IN ('completed', 'cancelled')`, cycleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// CompleteCycle is the ticker's rollover core for one ended cycle, run in
// a single tx:
//
//  1. Freeze: status → completed, progress_snapshot ← the live counts.
//  2. moveIncompleteIssuesTx: transfer to the next cycle when one is
//     queued, else detach to the backlog once the close_in_days grace
//     passes.
func CompleteCycle(ctx context.Context, pool *pgxpool.Pool, cycleID, projectID string, now time.Time) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	snap, err := cycleProgressSnapshot(ctx, tx, cycleID)
	if err != nil {
		return err
	}
	snapJSON, err := marshalSnapshot(snap)
	if err != nil {
		return err
	}
	var endDate time.Time
	err = tx.QueryRow(ctx,
		`UPDATE cycles SET status = 'completed', progress_snapshot = $2::jsonb, updated_at = now()
		 WHERE id = $1::uuid AND status IN ('current', 'upcoming')
		 RETURNING end_date`, cycleID, snapJSON).Scan(&endDate)
	if err == pgx.ErrNoRows {
		// Already completed (or never completable): the ticker may
		// race itself — completing twice must be a no-op, not a
		// second snapshot + second issue transfer.
		_ = tx.Rollback(ctx)
		return nil
	}
	if err != nil {
		return err
	}
	if err := moveIncompleteIssuesTx(ctx, tx, cycleID, projectID, endDate, now); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	announceCycleUpdated(ctx, pool, cycleID, projectID, "completed")
	return nil
}

// moveIncompleteIssuesTx moves a cycle's incomplete issues (state group not
// in completed/cancelled) inside the caller's tx:
//
//  1. If a next cycle exists (upcoming/current, start_date >= the old
//     end_date, earliest first) they transfer immediately.
//  2. Otherwise they stay attached to the completed cycle until
//     now >= end_date + close_in_days (NULL = 0), then detach to the
//     backlog — deleted from cycle_issues; the issues themselves are
//     untouched.
func moveIncompleteIssuesTx(ctx context.Context, tx pgx.Tx, cycleID, projectID string, endDate time.Time, now time.Time) error {
	incomplete, err := incompleteCycleIssues(ctx, tx, cycleID)
	if err != nil {
		return err
	}
	if len(incomplete) == 0 {
		return nil
	}
	var nextID string
	err = tx.QueryRow(ctx,
		`SELECT id::text FROM cycles
		 WHERE project_id = $1::uuid AND id <> $2::uuid
		   AND status IN ('upcoming', 'current')
		   AND start_date >= $3::date
		 ORDER BY start_date, created_at
		 LIMIT 1`, projectID, cycleID, endDate).Scan(&nextID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if err == nil {
		// Transfer: move the membership rows to the next cycle.
		if _, err := tx.Exec(ctx,
			`DELETE FROM cycle_issues
			 WHERE cycle_id = $1::uuid AND issue_id = ANY($2::uuid[])`,
			cycleID, incomplete); err != nil {
			return err
		}
		_, err = tx.Exec(ctx,
			`INSERT INTO cycle_issues (cycle_id, issue_id)
			 SELECT $1::uuid, unnest($2::uuid[])
			 ON CONFLICT DO NOTHING`, nextID, incomplete)
		return err
	}
	// No next cycle: detach once the close_in_days grace passes.
	var grace *int
	if err := tx.QueryRow(ctx,
		`SELECT close_in_days FROM projects WHERE id = $1::uuid`,
		projectID).Scan(&grace); err != nil {
		return err
	}
	graceDays := 0
	if grace != nil {
		graceDays = *grace
	}
	deadline := time.Date(endDate.Year(), endDate.Month(), endDate.Day(),
		0, 0, 0, 0, time.UTC).AddDate(0, 0, graceDays)
	if !now.Before(deadline) {
		_, err := tx.Exec(ctx,
			`DELETE FROM cycle_issues
			 WHERE cycle_id = $1::uuid AND issue_id = ANY($2::uuid[])`,
			cycleID, incomplete)
		return err
	}
	return nil
}

// SweepCompletedCycles handles the deferred half of rollover: incomplete
// issues still attached to completed cycles. That happens when the
// close_in_days grace had not passed at completion time, or when a next
// cycle was created after the completion. Each cycle moves in its own tx
// so one bad row cannot block the rest.
func SweepCompletedCycles(ctx context.Context, pool *pgxpool.Pool, now time.Time) error {
	rows, err := pool.Query(ctx,
		`SELECT c.id::text, c.project_id::text, c.end_date
		 FROM cycles c
		 WHERE c.status = 'completed'
		   AND EXISTS (
		         SELECT 1 FROM cycle_issues ci
		         JOIN issues i ON i.id = ci.issue_id AND i.deleted_at IS NULL
		         JOIN states s ON s.id = i.state_id
		         WHERE ci.cycle_id = c.id
		           AND s."group" NOT IN ('completed', 'cancelled'))`)
	if err != nil {
		return err
	}
	type target struct {
		id, projectID string
		endDate       time.Time
	}
	var targets []target
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.id, &t.projectID, &t.endDate); err != nil {
			rows.Close()
			return err
		}
		targets = append(targets, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, t := range targets {
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		merr := moveIncompleteIssuesTx(ctx, tx, t.id, t.projectID, t.endDate, now)
		if merr != nil {
			tx.Rollback(ctx)
			return merr
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

// marshalSnapshot encodes the frozen snapshot for the JSONB column.
func marshalSnapshot(snap map[string]int64) (string, error) {
	var b strings.Builder
	b.WriteByte('{')
	first := true
	for _, g := range snapshotGroups {
		if !first {
			b.WriteByte(',')
		}
		first = false
		fmt.Fprintf(&b, "%q:%d", g, snap[g])
	}
	b.WriteByte('}')
	return b.String(), nil
}

// BurndownDay is one day of a cycle burndown chart. Remaining is nil for
// days with no actual data yet (future days of an active cycle); Ideal is
// always present — the straight line from total scope to zero.
type BurndownDay struct {
	Date      string  `json:"date"` // YYYY-MM-DD
	Remaining *int    `json:"remaining"`
	Ideal     float64 `json:"ideal"`
}

// CycleBurndown is the GET .../cycles/{id}/burndown response.
type CycleBurndown struct {
	CycleID    string        `json:"cycle_id"`
	StartDate  string        `json:"start_date"` // YYYY-MM-DD
	EndDate    string        `json:"end_date"`
	Status     string        `json:"status"`
	TotalScope int           `json:"total_scope"`
	Days       []BurndownDay `json:"days"`
}

// doneStateGroups are the state groups that stop counting as remaining
// scope on a burndown. Cancelled work is gone work, same as completed.
var doneStateGroups = []string{"completed", "cancelled"}

// GetCycleBurndown builds a true burndown from the issue_activities audit
// log: for each day of the cycle, remaining = cycle issues whose state at
// end of day was not in a done group. State is reconstructed per issue
// from its state_id transitions (initial state = the first transition's
// old_value, or the current state when the issue never moved).
//
// Design notes (deliberate):
//   - One aggregate endpoint, not client-side: the per-issue history
//     endpoint exists, but fetching it for every issue in the cycle is
//     N+1 round trips. This is three indexed queries total.
//   - Scope uses current cycle membership; an issue added mid-cycle
//     counts from its cycle_issues.created_at day (scope growth is
//     visible). Issues removed from the cycle leave no audit row, so a
//     removal shrinks every day retroactively — documented limitation.
//   - Day boundaries are UTC midnights; activity timestamps are bucketed
//     by (created_at AT TIME ZONE 'UTC')::date.
//   - Ideal is the straight line total_scope → 0 over the cycle's days,
//     rounded to one decimal.
func GetCycleBurndown(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, cycleID string) (*CycleBurndown, error) {
	projectID, _, err := resolveCycleProject(ctx, pool, wsSlug, identifier, actorID, false)
	if err != nil {
		return nil, err
	}
	c, err := resolveCycle(ctx, pool, projectID, cycleID)
	if err != nil {
		return nil, err
	}

	done := map[string]bool{}
	rows, err := pool.Query(ctx,
		`SELECT id::text FROM states WHERE project_id = $1::uuid AND "group" = ANY($2)`,
		projectID, doneStateGroups)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		done[id] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	type issueRow struct {
		id      string
		stateID string
		added   string // YYYY-MM-DD (UTC)
	}
	var issues []issueRow
	rows, err = pool.Query(ctx,
		`SELECT i.id::text, i.state_id::text, (ci.created_at AT TIME ZONE 'UTC')::date::text
		 FROM cycle_issues ci
		 JOIN issues i ON i.id = ci.issue_id AND i.deleted_at IS NULL
		 WHERE ci.cycle_id = $1::uuid`,
		cycleID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0)
	for rows.Next() {
		var r issueRow
		if err := rows.Scan(&r.id, &r.stateID, &r.added); err != nil {
			rows.Close()
			return nil, err
		}
		issues = append(issues, r)
		ids = append(ids, r.id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// All state_id transitions for these issues, oldest first. old_value /
	// new_value are JSON strings holding the state UUIDs.
	type transition struct {
		day      string // YYYY-MM-DD (UTC)
		oldState string
		newState string
	}
	transByIssue := map[string][]transition{}
	if len(ids) > 0 {
		rows, err = pool.Query(ctx,
			`SELECT issue_id::text, (created_at AT TIME ZONE 'UTC')::date::text,
			        old_value #>> '{}', new_value #>> '{}'
			 FROM issue_activities
			 WHERE issue_id::text = ANY($1) AND field = 'state_id'
			 ORDER BY created_at ASC`,
			ids)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var issueID string
			var tr transition
			if err := rows.Scan(&issueID, &tr.day, &tr.oldState, &tr.newState); err != nil {
				rows.Close()
				return nil, err
			}
			transByIssue[issueID] = append(transByIssue[issueID], tr)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	startDay := c.StartDate.Format("2006-01-02")
	endDay := c.EndDate.Format("2006-01-02")
	nDays := int(c.EndDate.Sub(c.StartDate).Hours()/24) + 1
	if nDays < 1 {
		nDays = 1
	}
	today := time.Now().UTC().Format("2006-01-02")
	total := len(issues)

	days := make([]BurndownDay, 0, nDays)
	for i := 0; i < nDays; i++ {
		day := c.StartDate.AddDate(0, 0, i).Format("2006-01-02")
		bd := BurndownDay{Date: day}
		// Ideal: straight line total → 0 across the cycle's days.
		if nDays == 1 {
			bd.Ideal = float64(total)
		} else {
			bd.Ideal = math.Round(float64(total)*float64(nDays-1-i)/float64(nDays-1)*10) / 10
		}
		// No actuals for future days.
		if day <= today {
			remaining := 0
			for _, is := range issues {
				if is.added > day {
					continue // not in scope yet on this day
				}
				state := is.stateID
				if trs := transByIssue[is.id]; len(trs) > 0 {
					state = trs[0].oldState
				}
				for _, tr := range transByIssue[is.id] {
					if tr.day > day {
						break
					}
					state = tr.newState
				}
				if !done[state] {
					remaining++
				}
			}
			bd.Remaining = &remaining
		}
		days = append(days, bd)
	}

	return &CycleBurndown{
		CycleID:    c.ID,
		StartDate:  startDay,
		EndDate:    endDay,
		Status:     c.Status,
		TotalScope: total,
		Days:       days,
	}, nil
}
