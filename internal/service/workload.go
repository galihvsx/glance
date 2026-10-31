package service

// Workload & capacity (cycle 29 "Workload & capacity"), C29T0 backend.
//
// GetWorkload aggregates, per workspace member, their open assigned
// work (counts, estimate-point sums, overdue, priority buckets), their
// 14-day completion velocity, and an overloaded flag against their
// weekly capacity; plus per-project open unassigned counts.
// SetMemberCapacity sets or clears a member's weekly capacity.
//
// Conventions:
//   - Tenancy: the workspace must exist (else ErrNotFound) and every
//     projectID must belong to it (else ErrProjectNotFound) — a
//     cross-workspace project never leaks, it 404s. The caller-side
//     membership check (any member may read; capacity writes need
//     member 15+) lives in the HTTP layer, like the recurring routes.
//   - Scope is LIVE issues: deleted_at IS NULL AND archived_at IS NULL.
//     "Open" = live and not in a done state group (completed/cancelled),
//     resolved via the ds anti-join so a missing state row behaves
//     like analytics' NOT EXISTS (treated as open), never as a 500.
//   - Estimates come from estimate_points.value via
//     issues.estimate_point_id (NULL = unestimated = 0), the same
//     LEFT JOIN pattern as analytics.go.
//   - Overdue = target_date before today (the schema has no due_date;
//     analytics uses target_date the same way) on an open issue.
//   - Priority buckets follow the import/export word list: 0=none,
//     1=low, 2=medium, 3=high, 4=urgent.
//   - Velocity (completed_14d) is derived from the issue_activities
//     audit log — the day an issue's state_id transitioned INTO a done
//     group — exactly like the analytics trends. Honest limit: an
//     issue created directly in a done state has no transition row
//     and does not count.
//   - No N+1: one member-aggregation query, one velocity query, one
//     unassigned-counts query, regardless of member count.

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrInvalidCapacity is returned by SetMemberCapacity when the capacity
// is outside 0..999. The handler maps it to 400 bad_request.
var ErrInvalidCapacity = errors.New("service: invalid capacity: must be 0..999 or null")

// workloadCapacityMax is the upper bound for weekly_capacity_points,
// mirrored by the CHECK in migration 000052.
const workloadCapacityMax = 999

// workloadVelocityDays is the lookback window for completed_14d.
const workloadVelocityDays = 14

// WorkloadPriority is the per-member open-issue count by priority word.
type WorkloadPriority struct {
	Urgent int64 `json:"urgent"`
	High   int64 `json:"high"`
	Medium int64 `json:"medium"`
	Low    int64 `json:"low"`
	None   int64 `json:"none"`
}

// WorkloadMember is one workspace member's workload row. ProjectIDs are
// the scoped projects holding their open issues (never null).
// WeeklyCapacityPoints is nil when unset — unset members never trip
// the overloaded flag.
type WorkloadMember struct {
	UserID               string           `json:"user_id"`
	DisplayName          string           `json:"display_name"`
	Role                 int              `json:"role"`
	WeeklyCapacityPoints *int             `json:"weekly_capacity_points"`
	OpenCount            int64            `json:"open_count"`
	OpenPoints           int64            `json:"open_points"`
	OverdueCount         int64            `json:"overdue_count"`
	Priority             WorkloadPriority `json:"priority"`
	Completed14d         int64            `json:"completed_14d"`
	Overloaded           bool             `json:"overloaded"`
	ProjectIDs           []string         `json:"project_ids"`
}

// WorkloadUnassigned is the open unassigned-issue count of one project.
type WorkloadUnassigned struct {
	ProjectID         string `json:"project_id"`
	ProjectIdentifier string `json:"project_identifier"`
	OpenCount         int64  `json:"open_count"`
}

// WorkloadReport is the GET workload response body.
type WorkloadReport struct {
	Members     []WorkloadMember     `json:"members"`
	Unassigned  []WorkloadUnassigned `json:"unassigned"`
	GeneratedAt time.Time            `json:"generated_at"`
}

// workloadOpenCond is the WHERE/FILTER fragment for "open": the issue
// row exists and no done-group state row joined (ds is the done-state
// anti-join marker — see the query below).
const workloadOpenCond = `i.id IS NOT NULL AND ds.id IS NULL`

// GetWorkload returns the workload report for a workspace. projectIDs
// scopes issue counting to those projects; empty means all workspace
// projects. Members are always all workspace members (ordered by
// display name), even with zero issues. C92T2: the actor's effective
// project access scopes the counting — a non-member is ErrNotFound
// (as before for an unknown workspace), an empty scope counts only
// projects the actor can reach, and an explicitly requested private
// project the actor cannot reach is ErrProjectNotFound (the same
// sentinel a foreign-workspace project already produced, so the
// request never learns whether the id exists). Member rows and
// capacity metadata are workspace-level and are not themselves
// project data; only the issue counting is scoped.
func GetWorkload(ctx context.Context, pool *pgxpool.Pool, workspaceID uuid.UUID, actorID string, projectIDs []uuid.UUID) (*WorkloadReport, error) {
	wsID := workspaceID.String()

	var wsRole int
	if err := pool.QueryRow(ctx,
		`SELECT m.role FROM workspace_members m
		  WHERE m.workspace_id = $1::uuid AND m.user_id = $2::uuid`, wsID, actorID).Scan(&wsRole); err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	_ = wsRole

	scope, err := workloadProjectScope(ctx, pool, wsID, actorID, projectIDs)
	if err != nil {
		return nil, err
	}

	members, err := workloadMemberRows(ctx, pool, wsID, scope)
	if err != nil {
		return nil, err
	}
	velocity, err := workloadVelocity(ctx, pool, wsID, scope)
	if err != nil {
		return nil, err
	}
	for i := range members {
		members[i].Completed14d = velocity[members[i].UserID]
		// Overloaded = capacity set, positive, and open estimate
		// points exceed it. A nil or zero capacity never trips the
		// flag (0 = "no budget tracked", not "over budget").
		if c := members[i].WeeklyCapacityPoints; c != nil && *c > 0 {
			members[i].Overloaded = members[i].OpenPoints > int64(*c)
		}
	}

	unassigned, err := workloadUnassignedRows(ctx, pool, wsID, scope)
	if err != nil {
		return nil, err
	}

	return &WorkloadReport{
		Members:     members,
		Unassigned:  unassigned,
		GeneratedAt: time.Now().UTC(),
	}, nil
}

// workloadProjectScope resolves the project filter to in-workspace
// project id strings. Empty input means every project of the
// workspace; any id outside the workspace is ErrProjectNotFound.
// C92T2: both branches apply effective access (see GetWorkload) —
// the empty branch lists only reachable projects, and an explicitly
// requested unreachable project is ErrProjectNotFound, never a
// filtered-out silent scope (an explicit id is a probe; a silent
// drop would let the caller distinguish "no issues" from "no
// access" only by... nothing — but the pre-existing convention for
// out-of-scope ids is the 404, and the sweep keeps it).
func workloadProjectScope(ctx context.Context, pool *pgxpool.Pool, wsID, actorID string, projectIDs []uuid.UUID) ([]string, error) {
	if len(projectIDs) == 0 {
		rows, err := pool.Query(ctx,
			`SELECT id::text FROM projects p WHERE p.workspace_id = $1::uuid
			   AND `+projectAccessibleSQL("p", "$2::uuid")+`
			 ORDER BY p.identifier`, wsID, actorID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		ids := []string{}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return nil, err
			}
			ids = append(ids, id)
		}
		return ids, rows.Err()
	}
	seen := map[string]bool{}
	want := make([]string, 0, len(projectIDs))
	for _, id := range projectIDs {
		s := id.String()
		if !seen[s] {
			seen[s] = true
			want = append(want, s)
		}
	}
	rows, err := pool.Query(ctx,
		`SELECT id::text FROM projects p
		  WHERE p.workspace_id = $1::uuid AND p.id = ANY($2::uuid[])
		    AND `+projectAccessibleSQL("p", "$3::uuid"),
		wsID, want, actorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	found := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		found[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, id := range want {
		if !found[id] {
			return nil, ErrProjectNotFound
		}
	}
	return want, nil
}

// workloadMemberRows runs the single member-aggregation query: one row
// per workspace member with open counts, open estimate-point sums,
// overdue counts, priority buckets, and the projects holding their
// open issues. Members with no assigned issues still appear (zeros).
func workloadMemberRows(ctx context.Context, pool *pgxpool.Pool, wsID string, scope []string) ([]WorkloadMember, error) {
	rows, err := pool.Query(ctx,
		`SELECT m.user_id::text,
		        COALESCE(u.name, u.email::text),
		        m.role,
		        m.weekly_capacity_points,
		        COUNT(i.id) FILTER (WHERE `+workloadOpenCond+`) AS open_count,
		        COALESCE(SUM(ep.value) FILTER (WHERE `+workloadOpenCond+`), 0) AS open_points,
		        COUNT(i.id) FILTER (WHERE `+workloadOpenCond+` AND i.target_date < CURRENT_DATE) AS overdue_count,
		        COUNT(i.id) FILTER (WHERE `+workloadOpenCond+` AND i.priority = 4) AS p_urgent,
		        COUNT(i.id) FILTER (WHERE `+workloadOpenCond+` AND i.priority = 3) AS p_high,
		        COUNT(i.id) FILTER (WHERE `+workloadOpenCond+` AND i.priority = 2) AS p_medium,
		        COUNT(i.id) FILTER (WHERE `+workloadOpenCond+` AND i.priority = 1) AS p_low,
		        COUNT(i.id) FILTER (WHERE `+workloadOpenCond+` AND i.priority = 0) AS p_none,
		        COALESCE(array_agg(DISTINCT i.project_id::text) FILTER (WHERE `+workloadOpenCond+`), '{}') AS project_ids
		   FROM workspace_members m
		   JOIN users u ON u.id = m.user_id
		   LEFT JOIN issue_assignees ia ON ia.user_id = m.user_id
		   LEFT JOIN issues i ON i.id = ia.issue_id
		                  AND i.deleted_at IS NULL AND i.archived_at IS NULL
		                  AND i.project_id = ANY($2::uuid[])
		   LEFT JOIN estimate_points ep ON ep.id = i.estimate_point_id
		   LEFT JOIN states ds ON ds.id = i.state_id AND ds."group" = ANY($3)
		  WHERE m.workspace_id = $1::uuid
		  GROUP BY m.user_id, u.name, u.email, m.role, m.weekly_capacity_points
		  ORDER BY COALESCE(u.name, u.email::text)`,
		wsID, scope, doneStateGroups)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	members := []WorkloadMember{}
	for rows.Next() {
		var m WorkloadMember
		if err := rows.Scan(
			&m.UserID, &m.DisplayName, &m.Role, &m.WeeklyCapacityPoints,
			&m.OpenCount, &m.OpenPoints, &m.OverdueCount,
			&m.Priority.Urgent, &m.Priority.High, &m.Priority.Medium,
			&m.Priority.Low, &m.Priority.None,
			&m.ProjectIDs,
		); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

// workloadVelocity counts, per workspace member, the distinct issues
// currently assigned to them whose state transitioned INTO a done
// group within the last workloadVelocityDays days. One query for the
// whole member set — no N+1.
func workloadVelocity(ctx context.Context, pool *pgxpool.Pool, wsID string, scope []string) (map[string]int64, error) {
	rows, err := pool.Query(ctx,
		`SELECT ia.user_id::text, COUNT(DISTINCT a.issue_id)
		   FROM issue_activities a
		   JOIN issues i ON i.id = a.issue_id
		                  AND i.deleted_at IS NULL AND i.archived_at IS NULL
		                  AND i.project_id = ANY($2::uuid[])
		   JOIN issue_assignees ia ON ia.issue_id = a.issue_id
		   JOIN workspace_members m ON m.workspace_id = $1::uuid AND m.user_id = ia.user_id
		  WHERE a.field = 'state_id'
		    AND a.created_at >= now() - make_interval(days => $4)
		    AND EXISTS (SELECT 1 FROM states s
		                WHERE s.id::text = (a.new_value #>> '{}')
		                  AND s.project_id = ANY($2::uuid[])
		                  AND s."group" = ANY($3))
		  GROUP BY ia.user_id`,
		wsID, scope, doneStateGroups, workloadVelocityDays)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var userID string
		var n int64
		if err := rows.Scan(&userID, &n); err != nil {
			return nil, err
		}
		out[userID] = n
	}
	return out, rows.Err()
}

// workloadUnassignedRows counts open issues with no assignee per scoped
// project. One query for the whole workspace — no N+1.
func workloadUnassignedRows(ctx context.Context, pool *pgxpool.Pool, wsID string, scope []string) ([]WorkloadUnassigned, error) {
	rows, err := pool.Query(ctx,
		`SELECT p.id::text, p.identifier,
		        COUNT(i.id) FILTER (WHERE `+workloadOpenCond+`
		                            AND NOT EXISTS (SELECT 1 FROM issue_assignees ia
		                                            WHERE ia.issue_id = i.id)) AS open_count
		   FROM projects p
		   LEFT JOIN issues i ON i.project_id = p.id
		                      AND i.deleted_at IS NULL AND i.archived_at IS NULL
		   LEFT JOIN states ds ON ds.id = i.state_id AND ds."group" = ANY($2)
		  WHERE p.workspace_id = $1::uuid AND p.id = ANY($3::uuid[])
		  GROUP BY p.id, p.identifier
		  ORDER BY p.identifier`,
		wsID, doneStateGroups, scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WorkloadUnassigned{}
	for rows.Next() {
		var u WorkloadUnassigned
		if err := rows.Scan(&u.ProjectID, &u.ProjectIdentifier, &u.OpenCount); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// SetMemberCapacity sets a member's weekly capacity (story points); a
// nil capacity clears it back to NULL. Non-nil values must be 0..999
// (ErrInvalidCapacity otherwise). A non-member user is
// ErrMemberNotFound — cross-workspace ids never leak. Idempotent:
// re-setting the same value is a no-op success.
func SetMemberCapacity(ctx context.Context, pool *pgxpool.Pool, workspaceID, userID uuid.UUID, capacity *int) error {
	if capacity != nil && (*capacity < 0 || *capacity > workloadCapacityMax) {
		return ErrInvalidCapacity
	}
	tag, err := pool.Exec(ctx,
		`UPDATE workspace_members
		    SET weekly_capacity_points = $3
		  WHERE workspace_id = $1::uuid AND user_id = $2::uuid`,
		workspaceID.String(), userID.String(), capacity)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrMemberNotFound
	}
	return nil
}
