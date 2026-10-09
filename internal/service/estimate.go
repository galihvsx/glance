package service

// Estimate management additions (C6T2): delete a scale (guarded) and add
// points to an existing scale. The settings UI needs both; until cycle 6
// the only estimate endpoints were create-scale and list. All mutations
// are member (15)+.

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrEstimateInUse is returned when deleting a scale whose points are
// referenced by live issues.
var ErrEstimateInUse = errors.New("service: estimate scale is used by issues")

// validateEstimatePointKeys enforces the CreateEstimate point rules on a
// batch: non-empty keys (≤32 chars), no duplicates within the batch, and
// no duplicates against the scale's existing keys.
func validateEstimatePointKeys(points []EstimatePointInput, existing map[string]bool) error {
	seen := map[string]bool{}
	for _, p := range points {
		k := strings.TrimSpace(p.Key)
		if k == "" || len(k) > 32 {
			return ErrInvalidEstimatePoint
		}
		if seen[k] || existing[k] {
			return ErrInvalidEstimatePoint
		}
		seen[k] = true
	}
	return nil
}

// AddEstimatePoints appends points to an existing scale. Member (15)+.
// Duplicate keys (within the batch or against existing points) are
// ErrInvalidEstimatePoint.
func AddEstimatePoints(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, estimateID, actorID string, points []EstimatePointInput) (*Estimate, error) {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return nil, err
	}
	_, projectID, role, err := resolveIssueProject(ctx, pool, wsSlug, ident, actorID)
	if err != nil {
		return nil, err
	}
	if role < RoleMember {
		return nil, ErrForbidden
	}
	if len(points) == 0 {
		return nil, ErrInvalidEstimatePoint
	}
	// The estimate must belong to this project (tenancy: never touch
	// another project's scales).
	var found bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM estimates WHERE id = $1::uuid AND project_id = $2::uuid)`,
		estimateID, projectID).Scan(&found); err != nil {
		if isInvalidUUID(err) {
			return nil, ErrEstimateNotFound
		}
		return nil, err
	}
	if !found {
		return nil, ErrEstimateNotFound
	}
	existing := map[string]bool{}
	rows, err := pool.Query(ctx,
		`SELECT key FROM estimate_points WHERE estimate_id = $1::uuid`, estimateID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			rows.Close()
			return nil, err
		}
		existing[k] = true
	}
	rows.Close()
	if err := validateEstimatePointKeys(points, existing); err != nil {
		return nil, err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	for _, p := range points {
		k := strings.TrimSpace(p.Key)
		if _, err := tx.Exec(ctx,
			`INSERT INTO estimate_points (estimate_id, key, value, description)
			 VALUES ($1::uuid, $2, $3, $4)`,
			estimateID, k, p.Value, p.Description); err != nil {
			if isUniqueViolation(err) {
				return nil, ErrInvalidEstimatePoint
			}
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	// Return the refreshed scale.
	est, err := getEstimate(ctx, pool, projectID, estimateID)
	if err != nil {
		return nil, err
	}
	return est, nil
}

// DeleteEstimate removes a scale and its points. Member (15)+. 409 when
// any live issue references one of the scale's points.
func DeleteEstimate(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, estimateID, actorID string) error {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return err
	}
	_, projectID, role, err := resolveIssueProject(ctx, pool, wsSlug, ident, actorID)
	if err != nil {
		return err
	}
	if role < RoleMember {
		return ErrForbidden
	}
	var found bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM estimates WHERE id = $1::uuid AND project_id = $2::uuid)`,
		estimateID, projectID).Scan(&found); err != nil {
		if isInvalidUUID(err) {
			return ErrEstimateNotFound
		}
		return err
	}
	if !found {
		return ErrEstimateNotFound
	}
	var inUse int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM issues i
		  JOIN estimate_points ep ON ep.id = i.estimate_point_id
		 WHERE ep.estimate_id = $1::uuid AND i.deleted_at IS NULL`,
		estimateID).Scan(&inUse); err != nil {
		return err
	}
	if inUse > 0 {
		return ErrEstimateInUse
	}
	ct, err := pool.Exec(ctx, `DELETE FROM estimates WHERE id = $1::uuid`, estimateID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrEstimateNotFound
	}
	return nil
}

// getEstimate loads one scale with its points (project-scoped).
func getEstimate(ctx context.Context, pool *pgxpool.Pool, projectID, estimateID string) (*Estimate, error) {
	var est Estimate
	if err := pool.QueryRow(ctx,
		`SELECT id::text, project_id::text, name, created_at, updated_at
		   FROM estimates WHERE id = $1::uuid AND project_id = $2::uuid`,
		estimateID, projectID).Scan(&est.ID, &est.ProjectID, &est.Name, &est.CreatedAt, &est.UpdatedAt); err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx,
		`SELECT id::text, key, value, description FROM estimate_points
		  WHERE estimate_id = $1::uuid ORDER BY value, key`, estimateID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	est.Points = []EstimatePoint{}
	for rows.Next() {
		var pt EstimatePoint
		if err := rows.Scan(&pt.ID, &pt.Key, &pt.Value, &pt.Description); err != nil {
			return nil, err
		}
		est.Points = append(est.Points, pt)
	}
	return &est, rows.Err()
}
