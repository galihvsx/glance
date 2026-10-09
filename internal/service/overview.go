package service

// Project overview (C8T5): the single-round-trip payload behind
// GET /api/v1/workspaces/{slug}/projects/{identifier}/overview.
//
// A frontend composition of the existing building blocks costs six
// round-trips (project, workspace role, analytics summary, states,
// activity, cycles). This endpoint composes them server-side into one
// aggregate — the only new backend surface this task adds. All
// constituent pieces are the same tested service calls the individual
// endpoints use; nothing here loops per-row (no N+1).

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// OverviewActivityLimit is the activity depth the overview pulls: the
// page shows the newest entries inline and derives top contributors
// from the full window. Capped by GetProjectActivity's own maximum.
const OverviewActivityLimit = 100

// ProjectSummary is the overview's trimmed project view: identity plus
// the editable markdown description. The full Project row stays one
// GET away on the existing project endpoint.
type ProjectSummary struct {
	ID          string `json:"id"`
	Identifier  string `json:"identifier"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// OverviewCycle is the live-cycle progress card: the project's current
// cycle (or its most recently started one when none is current) with
// completed/total issue counts.
type OverviewCycle struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Completed int64  `json:"completed"`
	Total     int64  `json:"total"`
}

// ProjectOverview is the response of GET .../projects/{identifier}/overview.
type ProjectOverview struct {
	Project  *ProjectSummary   `json:"project"`
	Role     int               `json:"role"`
	Summary  *AnalyticsSummary `json:"summary"`
	States   []State           `json:"states"`
	Activity []ActivityEntry   `json:"activity"`
	Cycle    *OverviewCycle    `json:"cycle"`
	// OpenByPriority is open-issue counts by priority bucket. The
	// analytics summary's by_priority covers all live issues (no
	// state×priority split), so the overview computes the open-only
	// split itself in one aggregate query.
	OpenByPriority map[string]int64 `json:"open_by_priority"`
}

// GetProjectOverview composes the overview payload. Member (15)+ — the
// page shows the activity feed, which is member-scoped like its own
// endpoint; guests get ErrForbidden, outsiders ErrNotFound.
func GetProjectOverview(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string) (*ProjectOverview, error) {
	projectID, role, err := resolveCycleProject(ctx, pool, wsSlug, identifier, actorID, true)
	if err != nil {
		return nil, err
	}

	p, err := GetProject(ctx, pool, wsSlug, identifier, actorID)
	if err != nil {
		return nil, err
	}
	summary, err := GetAnalyticsSummary(ctx, pool, wsSlug, identifier, actorID)
	if err != nil {
		return nil, err
	}
	states, err := ListStates(ctx, pool, wsSlug, identifier, actorID)
	if err != nil {
		return nil, err
	}
	activity, err := GetProjectActivity(ctx, pool, wsSlug, identifier, actorID, OverviewActivityLimit)
	if err != nil {
		return nil, err
	}
	cycle, err := currentOverviewCycle(ctx, pool, projectID)
	if err != nil {
		return nil, err
	}
	openByPriority, err := openIssuePriorityCounts(ctx, pool, projectID)
	if err != nil {
		return nil, err
	}

	return &ProjectOverview{
		Project: &ProjectSummary{
			ID:          p.ID,
			Identifier:  p.Identifier,
			Name:        p.Name,
			Description: p.Description,
		},
		Role:           role,
		Summary:        summary,
		States:         states,
		Activity:       activity,
		Cycle:          cycle,
		OpenByPriority: openByPriority,
	}, nil
}

// openIssuePriorityCounts counts live issues by priority bucket,
// excluding issues in a done state group (completed/cancelled). One
// indexed aggregate — the analytics summary cannot produce this split.
func openIssuePriorityCounts(ctx context.Context, pool *pgxpool.Pool, projectID string) (map[string]int64, error) {
	m := map[string]int64{}
	rows, err := pool.Query(ctx,
		`SELECT i.priority::text, COUNT(*)
		   FROM issues i
		   JOIN states s ON s.id = i.state_id
		  WHERE i.project_id = $1::uuid
		    AND i.deleted_at IS NULL
		    AND i.archived_at IS NULL
		    AND s."group" NOT IN ('completed', 'cancelled')
		  GROUP BY i.priority`,
		projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var n int64
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		m[k] = n
	}
	return m, rows.Err()
}

// currentOverviewCycle returns the project's current cycle with its
// completed/total issue counts, or the most recently started cycle
// when none is current, or (nil, nil) when the project has no cycles.
// One aggregate query — no per-cycle fan-out.
func currentOverviewCycle(ctx context.Context, pool *pgxpool.Pool, projectID string) (*OverviewCycle, error) {
	var cyc OverviewCycle
	err := pool.QueryRow(ctx,
		`WITH pick AS (
		   SELECT id, name, status
		     FROM cycles
		    WHERE project_id = $1::uuid
		    ORDER BY CASE WHEN status = 'current' THEN 0 ELSE 1 END,
		             start_date DESC
		    LIMIT 1
		 )
		 SELECT p.id::text, p.name, p.status,
		        COUNT(i.id) FILTER (WHERE s."group" = 'completed'),
		        COUNT(i.id)
		   FROM pick p
		   LEFT JOIN cycle_issues ci ON ci.cycle_id = p.id
		   LEFT JOIN issues i ON i.id = ci.issue_id AND i.deleted_at IS NULL
		   LEFT JOIN states s ON s.id = i.state_id
		  GROUP BY p.id, p.name, p.status`,
		projectID).Scan(&cyc.ID, &cyc.Name, &cyc.Status, &cyc.Completed, &cyc.Total)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &cyc, nil
}
