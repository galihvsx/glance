package service

// Risk radar (cycle 30 "Risk radar"), C30T1 backend.
//
// GetRiskReport aggregates, per workspace project, a heuristic project
// health risk score (0-100) with a level (healthy/watch/at-risk/
// critical) and the top contributing factors. The frontend dashboard
// (C30T3) builds on this pinned contract.
//
// Conventions:
//   - Tenancy: the workspace must exist (else ErrNotFound) and every
//     query is scoped to its id — cross-workspace rows never leak.
//   - Scope is LIVE issues: deleted_at IS NULL AND archived_at IS NULL.
//     "Open" = live and not in a done state group (completed/cancelled),
//     resolved via the ds anti-join so a missing state row behaves
//     like analytics' NOT EXISTS (treated as open), never as a 500 —
//     the same definition as c29's GetWorkload (mirrored, not shared).
//   - Overdue = target_date before today (the schema has no due_date;
//     analytics/workload use target_date the same way) on an open issue.
//   - Stalled = the latest activity (MAX(created_at) over the
//     issue_activities audit log; issues.updated_at when the issue has
//     no activity rows) is older than riskStaleDays days.
//   - Overload = the c29 GetWorkload `overloaded` flag, reused
//     read-only (never re-implemented): a member with a positive
//     capacity whose open estimate points exceed it. overload_share =
//     overloaded members / all workspace members.
//   - risk_score = round(35*overdue_ratio + 25*stalled_share +
//     20*unassigned_share + 20*overload_share), clamped to 0..100.
//   - No N+1: one per-project aggregation query for the whole
//     workspace plus the (constant, 3-query) GetWorkload call.

import (
	"context"
	"errors"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Risk weights (v1, fixed): overdue carries the most signal, staleness
// next, then unassigned and capacity overload equally. See docs/risk.md.
const (
	riskWeightOverdue    = 35.0
	riskWeightStalled    = 25.0
	riskWeightUnassigned = 20.0
	riskWeightOverload   = 20.0
)

// riskStaleDays is the inactivity window after which an open issue
// counts as stalled (7 days).
const riskStaleDays = 7

// Risk levels on the 0-100 score.
const (
	RiskLevelHealthy  = "healthy"
	RiskLevelWatch    = "watch"
	RiskLevelAtRisk   = "at-risk"
	RiskLevelCritical = "critical"
)

// riskFactorKeys are the stable keys of the contributing factors.
const (
	RiskFactorOverdue    = "overdue"
	RiskFactorStalled    = "stalled"
	RiskFactorUnassigned = "unassigned"
	RiskFactorOverload   = "overload"
)

// RiskFactor is one contributing risk factor: the share of open work
// (or members) it covers plus a human-readable detail string. Factors
// are emitted in descending order of score contribution; a factor
// with zero contribution is omitted.
type RiskFactor struct {
	Key    string  `json:"key"`
	Label  string  `json:"label"`
	Value  float64 `json:"value"`
	Detail string  `json:"detail"`
}

// ProjectRisk is the risk row of one project.
type ProjectRisk struct {
	ProjectID   string       `json:"project_id"`
	Identifier  string       `json:"identifier"`
	Name        string       `json:"name"`
	OpenCount   int64        `json:"open_count"`
	RiskScore   int          `json:"risk_score"`
	RiskLevel   string       `json:"risk_level"`
	Factors     []RiskFactor `json:"factors"`
	GeneratedAt time.Time    `json:"generated_at"`
}

// RiskReport is the GET risk response body.
type RiskReport struct {
	Projects    []ProjectRisk `json:"projects"`
	GeneratedAt time.Time     `json:"generated_at"`
}

// riskOpenCond is the WHERE/FILTER fragment for "open": the issue row
// exists and no done-group state row joined (ds is the done-state
// anti-join marker — see the query below).
const riskOpenCond = `i.id IS NOT NULL AND ds.id IS NULL`

// riskLevelOf maps a 0-100 score to its risk level band.
func riskLevelOf(score int) string {
	switch {
	case score >= 75:
		return RiskLevelCritical
	case score >= 50:
		return RiskLevelAtRisk
	case score >= 25:
		return RiskLevelWatch
	default:
		return RiskLevelHealthy
	}
}

// GetRiskReport returns the project risk report for a workspace:
// every project the actor can effectively reach (ordered by
// identifier) with its risk score, level, and contributing factors.
// Projects with zero open issues score 0 (healthy) with empty
// factors. A missing workspace, or a workspace the actor is not a
// member of, is ErrNotFound. C92T2: the per-project rows are
// filtered by effective project access — a private project the
// actor cannot reach contributes neither its name nor its score,
// exactly like ListProjects vanishing it; the overload share reuses
// the actor-scoped GetWorkload for the same reason.
func GetRiskReport(ctx context.Context, pool *pgxpool.Pool, workspaceID uuid.UUID, actorID string) (*RiskReport, error) {
	wsID := workspaceID.String()

	if err := pool.QueryRow(ctx,
		`SELECT 1 FROM workspace_members m
		  WHERE m.workspace_id = $1::uuid AND m.user_id = $2::uuid`,
		wsID, actorID).Scan(new(int)); err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	stats, err := riskProjectRows(ctx, pool, wsID, actorID)
	if err != nil {
		return nil, err
	}
	// Reuse the c29 overload flags read-only; never re-implement them.
	overloadShare := 0.0
	workload, err := GetWorkload(ctx, pool, workspaceID, actorID, nil)
	if err != nil {
		return nil, err
	}
	if n := len(workload.Members); n > 0 {
		overloaded := 0
		for _, m := range workload.Members {
			if m.Overloaded {
				overloaded++
			}
		}
		overloadShare = float64(overloaded) / float64(n)
	}

	now := time.Now().UTC()
	projects := make([]ProjectRisk, 0, len(stats))
	for _, s := range stats {
		pr := ProjectRisk{
			ProjectID:   s.id,
			Identifier:  s.identifier,
			Name:        s.name,
			OpenCount:   s.open,
			RiskScore:   0,
			RiskLevel:   RiskLevelHealthy,
			Factors:     []RiskFactor{},
			GeneratedAt: now,
		}
		if s.open == 0 {
			// Zero open issues: healthy by definition, no factors.
			projects = append(projects, pr)
			continue
		}
		overdueRatio := float64(s.overdue) / float64(s.open)
		stalledRatio := float64(s.stalled) / float64(s.open)
		unassignedRatio := float64(s.unassigned) / float64(s.open)

		contrib := []struct {
			key, label, detail string
			value, weight      float64
		}{
			{RiskFactorOverdue, "Overdue issues", countDetail(s.overdue, s.open, "open issues overdue"), overdueRatio, riskWeightOverdue},
			{RiskFactorStalled, "Stalled issues", countDetail(s.stalled, s.open, "open issues with no activity in 7 days"), stalledRatio, riskWeightStalled},
			{RiskFactorUnassigned, "Unassigned issues", countDetail(s.unassigned, s.open, "open issues with no assignee"), unassignedRatio, riskWeightUnassigned},
			{RiskFactorOverload, "Overloaded members", memberDetail(workload), overloadShare, riskWeightOverload},
		}
		type scored struct {
			f RiskFactor
			c float64
		}
		var scoredFactors []scored
		score := 0.0
		for _, c := range contrib {
			c := c
			score += c.weight * c.value
			if c.value > 0 {
				scoredFactors = append(scoredFactors, scored{
					f: RiskFactor{
						Key:    c.key,
						Label:  c.label,
						Value:  math.Round(c.value*1000) / 1000,
						Detail: c.detail,
					},
					c: c.weight * c.value,
				})
			}
		}
		score = math.Round(score)
		if score < 0 {
			score = 0
		}
		if score > 100 {
			score = 100
		}
		sort.Slice(scoredFactors, func(a, b int) bool {
			if scoredFactors[a].c == scoredFactors[b].c {
				return scoredFactors[a].f.Key < scoredFactors[b].f.Key
			}
			return scoredFactors[a].c > scoredFactors[b].c
		})
		pr.Factors = []RiskFactor{}
		for _, sf := range scoredFactors {
			pr.Factors = append(pr.Factors, sf.f)
		}
		pr.RiskScore = int(score)
		pr.RiskLevel = riskLevelOf(pr.RiskScore)
		projects = append(projects, pr)
	}

	return &RiskReport{Projects: projects, GeneratedAt: now}, nil
}

// riskProjectStat is one row of the per-project aggregation query.
type riskProjectStat struct {
	id         string
	identifier string
	name       string
	open       int64
	overdue    int64
	stalled    int64
	unassigned int64
}

// riskProjectRows runs the single per-project aggregation query: one
// row per workspace project with open/overdue/stalled/unassigned
// counts. Staleness keys off the issue_activities audit log with
// issues.updated_at as the fallback for activity-less issues. One
// query for the whole workspace — no N+1.
func riskProjectRows(ctx context.Context, pool *pgxpool.Pool, wsID, actorID string) ([]riskProjectStat, error) {
	rows, err := pool.Query(ctx,
		`SELECT p.id::text, p.identifier, p.name,
		        COUNT(i.id) FILTER (WHERE `+riskOpenCond+`) AS open_count,
		        COUNT(i.id) FILTER (WHERE `+riskOpenCond+` AND i.target_date < CURRENT_DATE) AS overdue_count,
		        COUNT(i.id) FILTER (WHERE `+riskOpenCond+`
		                            AND COALESCE(
		                                  (SELECT MAX(a.created_at) FROM issue_activities a WHERE a.issue_id = i.id),
		                                  i.updated_at) < now() - make_interval(days => $3)) AS stalled_count,
		        COUNT(i.id) FILTER (WHERE `+riskOpenCond+`
		                            AND NOT EXISTS (SELECT 1 FROM issue_assignees ia
		                                            WHERE ia.issue_id = i.id)) AS unassigned_count
		   FROM projects p
		   LEFT JOIN issues i ON i.project_id = p.id
		                      AND i.deleted_at IS NULL AND i.archived_at IS NULL
		   LEFT JOIN states ds ON ds.id = i.state_id AND ds."group" = ANY($2)
		  WHERE p.workspace_id = $1::uuid
		    AND `+projectAccessibleSQL("p", "$4::uuid")+`
		  GROUP BY p.id, p.identifier, p.name
		  ORDER BY p.identifier`,
		wsID, doneStateGroups, riskStaleDays, actorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []riskProjectStat{}
	for rows.Next() {
		var s riskProjectStat
		if err := rows.Scan(&s.id, &s.identifier, &s.name, &s.open, &s.overdue, &s.stalled, &s.unassigned); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// countDetail renders "n of m <what>".
func countDetail(n, m int64, what string) string {
	return strconv.FormatInt(n, 10) + " of " + strconv.FormatInt(m, 10) + " " + what
}

// memberDetail renders the overload factor detail from the workload
// report ("k of n members overloaded").
func memberDetail(w *WorkloadReport) string {
	overloaded := 0
	for _, m := range w.Members {
		if m.Overloaded {
			overloaded++
		}
	}
	return strconv.FormatInt(int64(overloaded), 10) + " of " +
		strconv.FormatInt(int64(len(w.Members)), 10) + " members overloaded"
}
