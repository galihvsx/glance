package service

// Estimate management additions (C6T2): delete a scale (guarded against
// in-use points) and add points to an existing scale (duplicate keys
// rejected). Real test database, no skips.

import (
	"context"
	"errors"
	"testing"
)

// TestDeleteEstimate: unused scale deletes; in-use scale 409s; unknown 404s.
func TestDeleteEstimate(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	slug, ident, creator := setupStateProject(t, pool)

	est, err := CreateEstimate(ctx, pool, slug, ident, creator, EstimateInput{
		Name:   "Fibonacci",
		Points: []EstimatePointInput{{Key: "1", Value: 1}, {Key: "2", Value: 2}},
	})
	if err != nil {
		t.Fatalf("CreateEstimate: %v", err)
	}
	// Point an issue at one of the scale's points.
	iss := createTestIssue(t, pool, slug, ident, creator, "estimated issue")
	if _, err := pool.Exec(ctx,
		`UPDATE issues SET estimate_point_id = $1::uuid WHERE id = $2::uuid`,
		est.Points[0].ID, iss.ID); err != nil {
		t.Fatalf("point issue: %v", err)
	}
	if err := DeleteEstimate(ctx, pool, slug, ident, est.ID, creator); !errors.Is(err, ErrEstimateInUse) {
		t.Fatalf("delete in-use scale: err = %v, want ErrEstimateInUse", err)
	}
	// Soft-delete the issue → deletable.
	if _, err := pool.Exec(ctx,
		`UPDATE issues SET deleted_at = now() WHERE id = $1::uuid`, iss.ID); err != nil {
		t.Fatalf("soft-delete issue: %v", err)
	}
	if err := DeleteEstimate(ctx, pool, slug, ident, est.ID, creator); err != nil {
		t.Fatalf("delete unused scale: %v", err)
	}
	if err := DeleteEstimate(ctx, pool, slug, ident, est.ID, creator); !errors.Is(err, ErrEstimateNotFound) {
		t.Fatalf("delete missing: err = %v, want ErrEstimateNotFound", err)
	}
	// Malformed UUID → 404-class, not 500.
	if err := DeleteEstimate(ctx, pool, slug, ident, "bogus", creator); !errors.Is(err, ErrEstimateNotFound) {
		t.Fatalf("delete bogus id: err = %v, want ErrEstimateNotFound", err)
	}
}

// TestAddEstimatePoints: appends points; duplicates rejected.
func TestAddEstimatePoints(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	slug, ident, creator := setupStateProject(t, pool)

	est, err := CreateEstimate(ctx, pool, slug, ident, creator, EstimateInput{
		Name:   "T-shirt",
		Points: []EstimatePointInput{{Key: "S", Value: 1}},
	})
	if err != nil {
		t.Fatalf("CreateEstimate: %v", err)
	}
	updated, err := AddEstimatePoints(ctx, pool, slug, ident, est.ID, creator,
		[]EstimatePointInput{{Key: "M", Value: 2}, {Key: "L", Value: 3, Description: strPtr2("large")}})
	if err != nil {
		t.Fatalf("AddEstimatePoints: %v", err)
	}
	if len(updated.Points) != 3 {
		t.Fatalf("points = %d, want 3", len(updated.Points))
	}
	// Duplicate against existing → invalid.
	if _, err := AddEstimatePoints(ctx, pool, slug, ident, est.ID, creator,
		[]EstimatePointInput{{Key: "M", Value: 5}}); !errors.Is(err, ErrInvalidEstimatePoint) {
		t.Fatalf("dup vs existing: err = %v, want ErrInvalidEstimatePoint", err)
	}
	// Duplicate within the batch → invalid.
	if _, err := AddEstimatePoints(ctx, pool, slug, ident, est.ID, creator,
		[]EstimatePointInput{{Key: "XL", Value: 5}, {Key: "XL", Value: 8}}); !errors.Is(err, ErrInvalidEstimatePoint) {
		t.Fatalf("dup in batch: err = %v, want ErrInvalidEstimatePoint", err)
	}
	// Empty batch → invalid.
	if _, err := AddEstimatePoints(ctx, pool, slug, ident, est.ID, creator,
		nil); !errors.Is(err, ErrInvalidEstimatePoint) {
		t.Fatalf("empty batch: err = %v, want ErrInvalidEstimatePoint", err)
	}
	// Unknown scale → 404-class.
	if _, err := AddEstimatePoints(ctx, pool, slug, ident, "00000000-0000-0000-0000-000000000000", creator,
		[]EstimatePointInput{{Key: "XXL", Value: 13}}); !errors.Is(err, ErrEstimateNotFound) {
		t.Fatalf("unknown scale: err = %v, want ErrEstimateNotFound", err)
	}
}

// TestEstimateMutationsForbiddenForGuests: guests cannot mutate scales.
func TestEstimateMutationsForbiddenForGuests(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	slug, ident, creator := setupStateProject(t, pool)

	est, err := CreateEstimate(ctx, pool, slug, ident, creator, EstimateInput{
		Name:   "Story",
		Points: []EstimatePointInput{{Key: "1", Value: 1}},
	})
	if err != nil {
		t.Fatalf("CreateEstimate: %v", err)
	}
	guest := createTestUser(t, pool, uniqueTestEmail("est-guest"))
	addTestMember(t, pool, slug, creator, guest, RoleGuest)

	if _, err := AddEstimatePoints(ctx, pool, slug, ident, est.ID, guest,
		[]EstimatePointInput{{Key: "2", Value: 2}}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest add points: err = %v, want ErrForbidden", err)
	}
	if err := DeleteEstimate(ctx, pool, slug, ident, est.ID, guest); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest delete scale: err = %v, want ErrForbidden", err)
	}
}
