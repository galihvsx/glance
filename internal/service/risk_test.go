package service

// Risk radar (cycle 30 "Risk radar"), C30T1 backend tests: the v1
// composite formula on seeded fixtures, factor ordering and content,
// the zero-open-issues healthy case, tenancy (unknown workspace →
// ErrNotFound, foreign-workspace issues never leak), staleness via
// the activity log with the updated_at fallback, and an N+1 guard
// (query count constant as issue volume grows). Real test database,
// no skips.

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// riskFixture seeds one workspace (admin + one member dev), two
// projects (R1 with a spread of issues, R2 empty), and an estimate
// scale on R1.
type riskFixture struct {
	ctx    context.Context
	pool   *pgxpool.Pool
	wsID   uuid.UUID
	slug   string
	admin  string
	dev    string
	proj1  *Project
	proj2  *Project
	done1  string
	point5 string
}

func seedRiskFixture(t *testing.T, pool *pgxpool.Pool) *riskFixture {
	t.Helper()
	ctx := context.Background()
	fx := &riskFixture{ctx: ctx, pool: pool}
	fx.admin = createTestUser(t, pool, uniqueTestEmail("rk-admin"))
	fx.dev = createTestUser(t, pool, uniqueTestEmail("rk-dev"))
	fx.slug = uniqueTestSlug("rk-ws")
	ws := createTestWorkspace(t, pool, "Risk Co", fx.slug, fx.admin)
	fx.wsID = uuid.MustParse(ws.ID)
	addTestMember(t, pool, fx.slug, fx.admin, fx.dev, RoleMember)

	fx.proj1 = createTestProject(t, pool, fx.slug, fx.admin, "Atlas", uniqueTestIdentifier())
	fx.proj2 = createTestProject(t, pool, fx.slug, fx.admin, "Beacon", uniqueTestIdentifier())
	fx.done1 = stateIDByGroup(t, pool, fx.proj1.ID, "completed")

	// Estimate scale on R1: one 5-point value.
	var estID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO estimates (project_id, name) VALUES ($1::uuid, 'pts') RETURNING id::text`,
		fx.proj1.ID).Scan(&estID); err != nil {
		t.Fatalf("seed estimate: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO estimate_points (estimate_id, key, value) VALUES ($1::uuid, '5', 5) RETURNING id::text`,
		estID).Scan(&fx.point5); err != nil {
		t.Fatalf("seed point: %v", err)
	}
	return fx
}

func riskIssue(t *testing.T, fx *riskFixture, name string, assignee string, priority int, stateID, pointID string, target *time.Time) string {
	t.Helper()
	in := CreateIssueInput{Name: name, Priority: &priority, TargetDate: target}
	if pointID != "" {
		in.EstimatePointID = &pointID
	}
	iss, err := CreateIssue(fx.ctx, fx.pool, fx.slug, fx.proj1.Identifier, fx.admin, in)
	if err != nil {
		t.Fatalf("CreateIssue %s: %v", name, err)
	}
	if assignee != "" {
		if err := AssignAssignee(fx.ctx, fx.pool, fx.slug, fx.proj1.Identifier, iss.ID, assignee, fx.admin); err != nil {
			t.Fatalf("AssignAssignee %s: %v", name, err)
		}
	}
	if stateID != "" {
		if _, err := UpdateIssue(fx.ctx, fx.pool, fx.slug, fx.proj1.Identifier, iss.ID, fx.admin, IssuePatch{StateID: &stateID}); err != nil {
			t.Fatalf("set state %s: %v", name, err)
		}
	}
	return iss.ID
}

// TestRiskReportFormula builds a known spread and asserts the exact
// v1 composite math, factor ordering, and content.
func TestRiskReportFormula(t *testing.T) {
	fx := seedRiskFixture(t, newTestPool(t))
	yesterday := time.Now().UTC().AddDate(0, 0, -1)
	tomorrow := time.Now().UTC().AddDate(0, 0, 1)

	started := stateIDByGroup(t, fx.pool, fx.proj1.ID, "started")

	// i1: overdue (target yesterday), assigned dev, 5pts — drives the
	// overdue factor and dev's overload.
	i1 := riskIssue(t, fx, "overdue-1", fx.dev, 3, started, fx.point5, &yesterday)
	// i2: stalled via the updated_at FALLBACK — delete its activity
	// rows, backdate updated_at 10 days.
	i2 := riskIssue(t, fx, "stalled-fallback", fx.dev, 2, started, "", &tomorrow)
	if _, err := fx.pool.Exec(fx.ctx,
		`DELETE FROM issue_activities WHERE issue_id = $1::uuid`, i2); err != nil {
		t.Fatalf("delete activities: %v", err)
	}
	if _, err := fx.pool.Exec(fx.ctx,
		`UPDATE issues SET updated_at = now() - make_interval(days => 10) WHERE id = $1::uuid`, i2); err != nil {
		t.Fatalf("backdate updated_at: %v", err)
	}
	// i3: unassigned, fresh activity, target tomorrow — not stalled.
	_ = riskIssue(t, fx, "unassigned-1", "", 2, started, "", &tomorrow)
	// i4: stalled via stale ACTIVITY — backdate its activity rows but
	// leave updated_at fresh (updated_at must NOT rescue it).
	i4 := riskIssue(t, fx, "stalled-activity", fx.dev, 2, started, "", &tomorrow)
	if _, err := fx.pool.Exec(fx.ctx,
		`UPDATE issue_activities SET created_at = now() - make_interval(days => 10)
		  WHERE issue_id = $1::uuid`, i4); err != nil {
		t.Fatalf("backdate activities: %v", err)
	}
	// i5: completed — excluded from every open ratio.
	_ = riskIssue(t, fx, "done-1", fx.dev, 2, fx.done1, "", &tomorrow)
	// i6: archived — excluded from open entirely.
	archived := riskIssue(t, fx, "archived-1", fx.dev, 2, started, "", &yesterday)
	if _, err := fx.pool.Exec(fx.ctx,
		`UPDATE issues SET archived_at = now() WHERE id = $1::uuid`, archived); err != nil {
		t.Fatalf("archive: %v", err)
	}

	// Overload: dev capacity 1pt; dev's open points = 5 (i1) > 1.
	cap := 1
	if err := SetMemberCapacity(fx.ctx, fx.pool, fx.wsID, uuid.MustParse(fx.dev), &cap); err != nil {
		t.Fatalf("SetMemberCapacity: %v", err)
	}

	rep, err := GetRiskReport(fx.ctx, fx.pool, fx.wsID)
	if err != nil {
		t.Fatalf("GetRiskReport: %v", err)
	}
	if rep.GeneratedAt.IsZero() {
		t.Fatal("generated_at must be set")
	}
	if len(rep.Projects) != 2 {
		t.Fatalf("projects = %d, want 2 (both projects, even empty R2)", len(rep.Projects))
	}

	r1 := rep.Projects[0]
	if r1.Identifier != fx.proj1.Identifier || r1.ProjectID != fx.proj1.ID || r1.Name != "Atlas" {
		t.Fatalf("r1 identity = %+v, want proj1", r1)
	}
	if r1.OpenCount != 4 {
		t.Fatalf("r1 open = %d, want 4 (i1,i2,i3,i4; done+archived excluded)", r1.OpenCount)
	}
	if r1.GeneratedAt.IsZero() {
		t.Fatal("per-project generated_at must be set")
	}

	// open=4: overdue=1/4, stalled=2/4, unassigned=1/4,
	// overload=1/2 (2 members: dev overloaded, admin not).
	// score = round(35*.25 + 25*.5 + 20*.25 + 20*.5) = round(36.25) = 36.
	if r1.RiskScore != 36 {
		t.Fatalf("r1 score = %d, want 36", r1.RiskScore)
	}
	if r1.RiskLevel != RiskLevelWatch {
		t.Fatalf("r1 level = %q, want watch", r1.RiskLevel)
	}
	wantKeys := []string{RiskFactorStalled, RiskFactorOverload, RiskFactorOverdue, RiskFactorUnassigned}
	if len(r1.Factors) != 4 {
		t.Fatalf("r1 factors = %d, want 4 (all contribute)", len(r1.Factors))
	}
	for i, k := range wantKeys {
		if r1.Factors[i].Key != k {
			t.Fatalf("factor[%d].key = %q, want %q (order: %v)", i, r1.Factors[i].Key, k, wantKeys)
		}
	}
	vals := map[string]float64{
		RiskFactorOverdue: 0.25, RiskFactorStalled: 0.5,
		RiskFactorUnassigned: 0.25, RiskFactorOverload: 0.5,
	}
	for _, f := range r1.Factors {
		if math.Abs(f.Value-vals[f.Key]) > 0.001 {
			t.Fatalf("factor %q value = %v, want %v", f.Key, f.Value, vals[f.Key])
		}
		if f.Label == "" || f.Detail == "" {
			t.Fatalf("factor %q must have label and detail", f.Key)
		}
	}
	if r1.Factors[0].Detail != "2 of 4 open issues with no activity in 7 days" {
		t.Fatalf("stalled detail = %q", r1.Factors[0].Detail)
	}
	if r1.Factors[1].Detail != "1 of 2 members overloaded" {
		t.Fatalf("overload detail = %q", r1.Factors[1].Detail)
	}
	_ = i1

	// R2: zero open issues → score 0, healthy, empty factors.
	r2 := rep.Projects[1]
	if r2.Identifier != fx.proj2.Identifier {
		t.Fatalf("r2 = %+v, want proj2", r2)
	}
	if r2.RiskScore != 0 || r2.RiskLevel != RiskLevelHealthy || len(r2.Factors) != 0 || r2.OpenCount != 0 {
		t.Fatalf("r2 must be score 0 / healthy / no factors: %+v", r2)
	}
}

// TestRiskLevelBands exercises the score→level mapping, including the
// band edges.
func TestRiskLevelBands(t *testing.T) {
	for score, want := range map[int]string{
		0: RiskLevelHealthy, 24: RiskLevelHealthy,
		25: RiskLevelWatch, 49: RiskLevelWatch,
		50: RiskLevelAtRisk, 74: RiskLevelAtRisk,
		75: RiskLevelCritical, 100: RiskLevelCritical,
	} {
		if got := riskLevelOf(score); got != want {
			t.Fatalf("riskLevelOf(%d) = %q, want %q", score, got, want)
		}
	}
}

// TestRiskHealthyProject: a project whose issues are all fresh,
// assigned and on target produces a low healthy score (not exactly
// zero when overload share is nonzero — here no capacities are set,
// so everything is zero).
func TestRiskHealthyProject(t *testing.T) {
	fx := seedRiskFixture(t, newTestPool(t))
	tomorrow := time.Now().UTC().AddDate(0, 0, 1)
	started := stateIDByGroup(t, fx.pool, fx.proj1.ID, "started")
	riskIssue(t, fx, "fresh-1", fx.dev, 2, started, "", &tomorrow)
	riskIssue(t, fx, "fresh-2", fx.admin, 2, started, "", &tomorrow)

	rep, err := GetRiskReport(fx.ctx, fx.pool, fx.wsID)
	if err != nil {
		t.Fatalf("GetRiskReport: %v", err)
	}
	r1 := rep.Projects[0]
	if r1.RiskScore != 0 {
		t.Fatalf("healthy project score = %d, want 0", r1.RiskScore)
	}
	if r1.RiskLevel != RiskLevelHealthy {
		t.Fatalf("healthy project level = %q", r1.RiskLevel)
	}
	if len(r1.Factors) != 0 {
		t.Fatalf("healthy project must have no factors, got %+v", r1.Factors)
	}
}

// TestRiskTenancy: unknown workspace → ErrNotFound; issues in a
// foreign workspace never leak into the report.
func TestRiskTenancy(t *testing.T) {
	fx := seedRiskFixture(t, newTestPool(t))

	if _, err := GetRiskReport(fx.ctx, fx.pool, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown workspace: err = %v, want ErrNotFound", err)
	}

	other := createTestUser(t, fx.pool, uniqueTestEmail("rk-other"))
	otherSlug := uniqueTestSlug("rk-other")
	createTestWorkspace(t, fx.pool, "Other Co", otherSlug, other)
	otherProj := createTestProject(t, fx.pool, otherSlug, other, "Sneaky", uniqueTestIdentifier())
	started := stateIDByGroup(t, fx.pool, otherProj.ID, "started")
	yesterday := time.Now().UTC().AddDate(0, 0, -1)
	urgent := 4
	in := CreateIssueInput{Name: "sneaky-overdue", Priority: &urgent, TargetDate: &yesterday}
	iss, err := CreateIssue(fx.ctx, fx.pool, otherSlug, otherProj.Identifier, other, in)
	if err != nil {
		t.Fatalf("seed foreign issue: %v", err)
	}
	if _, err := UpdateIssue(fx.ctx, fx.pool, otherSlug, otherProj.Identifier, iss.ID, other, IssuePatch{StateID: &started}); err != nil {
		t.Fatalf("seed foreign state: %v", err)
	}

	rep, err := GetRiskReport(fx.ctx, fx.pool, fx.wsID)
	if err != nil {
		t.Fatalf("GetRiskReport: %v", err)
	}
	for _, p := range rep.Projects {
		if p.Identifier == otherProj.Identifier {
			t.Fatal("foreign-workspace project leaked into the report")
		}
	}
	if len(rep.Projects) != 2 {
		t.Fatalf("projects = %d, want 2 (no foreign rows)", len(rep.Projects))
	}
}

// TestRiskNoNPlusOne: the report cost is constant in the number of
// projects and issues — 1 existence check + 1 per-project aggregation
// + GetWorkload's constant queries.
func TestRiskNoNPlusOne(t *testing.T) {
	pool, counter := tracedTestPool(t)
	fx := seedRiskFixture(t, pool)
	tomorrow := time.Now().UTC().AddDate(0, 0, 1)
	started := stateIDByGroup(t, pool, fx.proj1.ID, "started")
	for i := 0; i < 30; i++ {
		riskIssue(t, fx, "bulk", fx.dev, 2, started, "", &tomorrow)
	}
	// Extra projects also must not grow the query count.
	for i := 0; i < 5; i++ {
		createTestProject(t, pool, fx.slug, fx.admin, "bulk", uniqueTestIdentifier())
	}

	counter.n.Store(0)
	rep, err := GetRiskReport(context.Background(), pool, fx.wsID)
	if err != nil {
		t.Fatalf("GetRiskReport: %v", err)
	}
	n := counter.n.Load()
	// 1 (workspace exists) + 1 (project aggregation) + 5 (GetWorkload:
	// exists, project scope, members, velocity, unassigned) = 7.
	if n != 7 {
		t.Fatalf("queries = %d, want exactly 7 (batched aggregation, no N+1)", n)
	}
	if len(rep.Projects) != 7 {
		t.Fatalf("projects = %d, want 7", len(rep.Projects))
	}
}
