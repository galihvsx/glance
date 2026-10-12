package service

// Capacity-overload alerts (C29T2) tests: overload detection from the
// batched scan, once-per-day dedupe (same-day re-run silent, next-day
// re-fire), the overload-rule boundary (strict >, nil/zero capacity
// never alerts), done-state/archived/deleted exclusion, and
// cross-workspace tenancy (alerts never leak across workspaces).
// Real test database, no skips.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// alertFixture is the workload fixture plus a fixed clock for the
// capacity pass.
type alertFixture struct {
	*workloadFixture
	now time.Time
}

func seedAlertFixture(t *testing.T) *alertFixture {
	t.Helper()
	return &alertFixture{
		workloadFixture: seedWorkloadFixture(t),
		now:             time.Date(2026, 10, 13, 6, 0, 0, 0, time.UTC),
	}
}

// setCap sets one member's capacity, failing the test on error.
func setCap(t *testing.T, ctx context.Context, pool *pgxpool.Pool, wsID, userID uuid.UUID, capacity *int) {
	t.Helper()
	if err := SetMemberCapacity(ctx, pool, wsID, userID, capacity); err != nil {
		t.Fatalf("SetMemberCapacity: %v", err)
	}
}

// overloadAlerts counts capacity.overloaded notifications for a user.
func overloadAlerts(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM notifications WHERE user_id = $1::uuid AND type = $2`,
		userID, NotifyCapacityOverloaded).Scan(&n); err != nil {
		t.Fatalf("count alerts: %v", err)
	}
	return n
}

// alertRow reads the newest capacity.overloaded notification title +
// payload fields for a user.
func alertRow(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID string) (title string, openPoints, capacity int64, wsID string) {
	t.Helper()
	if err := pool.QueryRow(ctx,
		`SELECT title,
		        (payload->>'open_points')::bigint,
		        (payload->>'weekly_capacity_points')::bigint,
		        payload->>'workspace_id'
		   FROM notifications
		  WHERE user_id = $1::uuid AND type = $2
		  ORDER BY created_at DESC LIMIT 1`,
		userID, NotifyCapacityOverloaded).Scan(&title, &openPoints, &capacity, &wsID); err != nil {
		t.Fatalf("read alert row: %v", err)
	}
	return title, openPoints, capacity, wsID
}

func TestRunCapacityAlertsOverloadAndDedupe(t *testing.T) {
	fx := seedAlertFixture(t)

	// dev1 has 7 open points (i1=5, i2=2; unestimated i3/j1 count 0;
	// archived i8 and deleted i9 excluded). Capacity 5 → overloaded.
	five := 5
	setCap(t, fx.ctx, fx.pool, fx.wsID, uuid.MustParse(fx.dev1), &five)
	// dev2 has no open points (i4/i5 are done) and no capacity: never
	// alerted.
	// admin has no issues and no capacity: never alerted.

	if err := RunCapacityAlerts(fx.ctx, fx.pool, fx.now); err != nil {
		t.Fatalf("RunCapacityAlerts: %v", err)
	}
	if n := overloadAlerts(t, fx.ctx, fx.pool, fx.dev1); n != 1 {
		t.Fatalf("dev1 alerts = %d, want 1", n)
	}
	if n := overloadAlerts(t, fx.ctx, fx.pool, fx.dev2); n != 0 {
		t.Fatalf("dev2 alerts = %d, want 0 (no open points, no capacity)", n)
	}
	if n := overloadAlerts(t, fx.ctx, fx.pool, fx.admin); n != 0 {
		t.Fatalf("admin alerts = %d, want 0 (no capacity)", n)
	}

	title, openPoints, capacity, wsID := alertRow(t, fx.ctx, fx.pool, fx.dev1)
	if openPoints != 7 || capacity != 5 {
		t.Fatalf("alert payload open_points=%d capacity=%d, want 7/5", openPoints, capacity)
	}
	if wsID != fx.wsID.String() {
		t.Fatalf("alert workspace_id = %s, want %s", wsID, fx.wsID)
	}
	if title == "" {
		t.Fatalf("alert title empty")
	}

	// Same-day re-run: dedupe claim converges silently — no second row.
	if err := RunCapacityAlerts(fx.ctx, fx.pool, fx.now.Add(2*time.Hour)); err != nil {
		t.Fatalf("RunCapacityAlerts (same day): %v", err)
	}
	if n := overloadAlerts(t, fx.ctx, fx.pool, fx.dev1); n != 1 {
		t.Fatalf("dev1 alerts after same-day re-run = %d, want 1 (deduped)", n)
	}

	// Next day: the member is still overloaded → re-fires.
	if err := RunCapacityAlerts(fx.ctx, fx.pool, fx.now.AddDate(0, 0, 1)); err != nil {
		t.Fatalf("RunCapacityAlerts (next day): %v", err)
	}
	if n := overloadAlerts(t, fx.ctx, fx.pool, fx.dev1); n != 2 {
		t.Fatalf("dev1 alerts after next-day run = %d, want 2", n)
	}
}

// TestRunCapacityAlertsBoundaries: the overload rule is strict —
// open_points == capacity is not overloaded; a zero capacity is "no
// budget tracked" and never trips.
func TestRunCapacityAlertsBoundaries(t *testing.T) {
	fx := seedAlertFixture(t)

	seven := 7
	setCap(t, fx.ctx, fx.pool, fx.wsID, uuid.MustParse(fx.dev1), &seven)
	if err := RunCapacityAlerts(fx.ctx, fx.pool, fx.now); err != nil {
		t.Fatalf("RunCapacityAlerts: %v", err)
	}
	if n := overloadAlerts(t, fx.ctx, fx.pool, fx.dev1); n != 0 {
		t.Fatalf("dev1 alerts with capacity == open_points = %d, want 0 (strict >)", n)
	}

	zero := 0
	setCap(t, fx.ctx, fx.pool, fx.wsID, uuid.MustParse(fx.dev1), &zero)
	if err := RunCapacityAlerts(fx.ctx, fx.pool, fx.now); err != nil {
		t.Fatalf("RunCapacityAlerts: %v", err)
	}
	if n := overloadAlerts(t, fx.ctx, fx.pool, fx.dev1); n != 0 {
		t.Fatalf("dev1 alerts with zero capacity = %d, want 0 (zero = untracked)", n)
	}

	// Nil (unset) capacity also never alerts.
	if err := SetMemberCapacity(fx.ctx, fx.pool, fx.wsID, uuid.MustParse(fx.dev1), nil); err != nil {
		t.Fatalf("clear capacity: %v", err)
	}
	if err := RunCapacityAlerts(fx.ctx, fx.pool, fx.now); err != nil {
		t.Fatalf("RunCapacityAlerts: %v", err)
	}
	if n := overloadAlerts(t, fx.ctx, fx.pool, fx.dev1); n != 0 {
		t.Fatalf("dev1 alerts with nil capacity = %d, want 0", n)
	}
}

// TestRunCapacityAlertsResolvedOverload: moving every open issue to a
// done state clears the overload — no alert fires.
func TestRunCapacityAlertsResolvedOverload(t *testing.T) {
	fx := seedAlertFixture(t)
	five := 5
	setCap(t, fx.ctx, fx.pool, fx.wsID, uuid.MustParse(fx.dev1), &five)

	// Complete all of dev1's open issues (P1 started → done).
	if _, err := fx.pool.Exec(fx.ctx,
		`UPDATE issues i SET state_id = $1::uuid
		  FROM issue_assignees ia
		 WHERE ia.issue_id = i.id AND ia.user_id = $2::uuid
		   AND i.deleted_at IS NULL AND i.archived_at IS NULL`,
		fx.done1, fx.dev1); err != nil {
		t.Fatalf("complete dev1 issues: %v", err)
	}

	if err := RunCapacityAlerts(fx.ctx, fx.pool, fx.now); err != nil {
		t.Fatalf("RunCapacityAlerts: %v", err)
	}
	if n := overloadAlerts(t, fx.ctx, fx.pool, fx.dev1); n != 0 {
		t.Fatalf("dev1 alerts after resolving = %d, want 0", n)
	}
}

// TestRunCapacityAlertsTenancy: overload alerts are per workspace — a
// second workspace's overloaded member is alerted with that workspace's
// id in the payload, and membership alone never crosses the boundary.
func TestRunCapacityAlertsTenancy(t *testing.T) {
	fx := seedAlertFixture(t)
	five := 5
	setCap(t, fx.ctx, fx.pool, fx.wsID, uuid.MustParse(fx.dev1), &five)

	// Second workspace, one member, one 5pt issue in a started state,
	// capacity 1 → overloaded there too.
	dev3 := createTestUser(t, fx.pool, uniqueTestEmail("ca-dev3"))
	slug2 := uniqueTestSlug("ca-ws2")
	createTestWorkspace(t, fx.pool, "Capacity Two", slug2, dev3)
	ws2 := uuid.MustParse(workspaceIDBySlug(t, fx.pool, slug2))
	proj := createTestProject(t, fx.pool, slug2, dev3, "P3", uniqueTestIdentifier())
	started := stateIDByGroup(t, fx.pool, proj.ID, "started")

	var estID string
	if err := fx.pool.QueryRow(fx.ctx,
		`INSERT INTO estimates (project_id, name) VALUES ($1::uuid, 'pts') RETURNING id::text`,
		proj.ID).Scan(&estID); err != nil {
		t.Fatalf("seed estimate: %v", err)
	}
	var pointID string
	if err := fx.pool.QueryRow(fx.ctx,
		`INSERT INTO estimate_points (estimate_id, key, value) VALUES ($1::uuid, '5', 5) RETURNING id::text`,
		estID).Scan(&pointID); err != nil {
		t.Fatalf("seed point: %v", err)
	}
	iss, err := CreateIssue(fx.ctx, fx.pool, slug2, proj.Identifier, dev3,
		CreateIssueInput{Name: "heavy", EstimatePointID: &pointID})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := AssignAssignee(fx.ctx, fx.pool, slug2, proj.Identifier, iss.ID, dev3, dev3); err != nil {
		t.Fatalf("AssignAssignee: %v", err)
	}
	if _, err := UpdateIssue(fx.ctx, fx.pool, slug2, proj.Identifier, iss.ID, dev3,
		IssuePatch{StateID: &started}); err != nil {
		t.Fatalf("start issue: %v", err)
	}
	one := 1
	setCap(t, fx.ctx, fx.pool, ws2, uuid.MustParse(dev3), &one)

	if err := RunCapacityAlerts(fx.ctx, fx.pool, fx.now); err != nil {
		t.Fatalf("RunCapacityAlerts: %v", err)
	}

	if n := overloadAlerts(t, fx.ctx, fx.pool, fx.dev1); n != 1 {
		t.Fatalf("dev1 alerts = %d, want 1", n)
	}
	if n := overloadAlerts(t, fx.ctx, fx.pool, dev3); n != 1 {
		t.Fatalf("dev3 alerts = %d, want 1", n)
	}
	_, _, _, wsID1 := alertRow(t, fx.ctx, fx.pool, fx.dev1)
	if wsID1 != fx.wsID.String() {
		t.Fatalf("dev1 alert workspace = %s, want %s (no cross-workspace leak)", wsID1, fx.wsID)
	}
	_, _, _, wsID2 := alertRow(t, fx.ctx, fx.pool, dev3)
	if wsID2 != ws2.String() {
		t.Fatalf("dev3 alert workspace = %s, want %s", wsID2, ws2)
	}
	// dev2 (no overload, first workspace only) stays silent.
	if n := overloadAlerts(t, fx.ctx, fx.pool, fx.dev2); n != 0 {
		t.Fatalf("dev2 alerts = %d, want 0", n)
	}
}

// workspaceIDBySlug resolves a workspace slug to its id string.
func workspaceIDBySlug(t *testing.T, pool *pgxpool.Pool, slug string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`SELECT id::text FROM workspaces WHERE slug = $1`, slug).Scan(&id); err != nil {
		t.Fatalf("workspace id by slug: %v", err)
	}
	return id
}
