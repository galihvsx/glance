package service

// Workload & capacity (cycle 29 "Workload & capacity"), C29T0 backend
// tests: aggregation math (open counts/points/overdue/priority
// buckets/velocity), capacity set/unset validation bounds, tenancy
// (unknown workspace → ErrNotFound, foreign project →
// ErrProjectNotFound, no cross-workspace leaks), overload flag
// semantics, and idempotent capacity writes. Real test database, no
// skips.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// workloadFixture seeds one workspace (admin + two member devs), two
// projects, an estimate scale on P1, and a spread of issues covering
// every aggregation dimension.
type workloadFixture struct {
	ctx       context.Context
	pool      *pgxpool.Pool
	wsID      uuid.UUID
	slug      string
	admin     string
	dev1      string
	dev2      string
	proj1     *Project
	proj2     *Project
	started1  string
	done1     string
	started2  string
	done2     string
	point2    string
	point5    string
	yesterday time.Time
	tomorrow  time.Time
}

func seedWorkloadFixture(t *testing.T) *workloadFixture {
	t.Helper()
	ctx := context.Background()
	pool := newTestPool(t)
	fx := &workloadFixture{ctx: ctx, pool: pool}
	fx.admin = createTestUser(t, pool, uniqueTestEmail("wl-admin"))
	fx.dev1 = createTestUser(t, pool, uniqueTestEmail("wl-dev1"))
	fx.dev2 = createTestUser(t, pool, uniqueTestEmail("wl-dev2"))
	fx.slug = uniqueTestSlug("wl-ws")
	ws := createTestWorkspace(t, pool, "Workload Co", fx.slug, fx.admin)
	fx.wsID = uuid.MustParse(ws.ID)
	addTestMember(t, pool, fx.slug, fx.admin, fx.dev1, RoleMember)
	addTestMember(t, pool, fx.slug, fx.admin, fx.dev2, RoleMember)
	// Give dev1 a display name so COALESCE(name, email) is exercised.
	if _, err := pool.Exec(ctx, `UPDATE users SET name = 'Dev One' WHERE id = $1::uuid`, fx.dev1); err != nil {
		t.Fatalf("set name: %v", err)
	}

	fx.proj1 = createTestProject(t, pool, fx.slug, fx.admin, "P1", uniqueTestIdentifier())
	fx.proj2 = createTestProject(t, pool, fx.slug, fx.admin, "P2", uniqueTestIdentifier())
	fx.started1 = stateIDByGroup(t, pool, fx.proj1.ID, "started")
	fx.done1 = stateIDByGroup(t, pool, fx.proj1.ID, "completed")
	fx.started2 = stateIDByGroup(t, pool, fx.proj2.ID, "started")
	fx.done2 = stateIDByGroup(t, pool, fx.proj2.ID, "completed")

	// Estimate scale on P1: S=2, M=5.
	var estID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO estimates (project_id, name) VALUES ($1::uuid, 'pts') RETURNING id::text`,
		fx.proj1.ID).Scan(&estID); err != nil {
		t.Fatalf("seed estimate: %v", err)
	}
	for _, pv := range [][2]int{{2, 2}, {5, 5}} {
		var pid string
		if err := pool.QueryRow(ctx,
			`INSERT INTO estimate_points (estimate_id, key, value) VALUES ($1::uuid, $2, $3) RETURNING id::text`,
			estID, string(rune('0'+pv[0])), pv[1]).Scan(&pid); err != nil {
			t.Fatalf("seed point: %v", err)
		}
		if pv[1] == 2 {
			fx.point2 = pid
		} else {
			fx.point5 = pid
		}
	}

	fx.yesterday = time.Now().UTC().AddDate(0, 0, -1)
	fx.tomorrow = time.Now().UTC().AddDate(0, 0, 1)

	// mkIssue creates an issue in a project, optionally assigning it,
	// moving it to a state, and setting an estimate point.
	mkIssue := func(proj *Project, ident, name string, assignee string, priority int, stateID, pointID string, target *time.Time) string {
		in := CreateIssueInput{Name: name, Priority: &priority, TargetDate: target}
		if pointID != "" {
			in.EstimatePointID = &pointID
		}
		iss, err := CreateIssue(ctx, pool, fx.slug, ident, fx.admin, in)
		if err != nil {
			t.Fatalf("CreateIssue %s: %v", name, err)
		}
		if assignee != "" {
			if err := AssignAssignee(ctx, pool, fx.slug, ident, iss.ID, assignee, fx.admin); err != nil {
				t.Fatalf("AssignAssignee %s: %v", name, err)
			}
		}
		if stateID != "" {
			if _, err := UpdateIssue(ctx, pool, fx.slug, ident, iss.ID, fx.admin, IssuePatch{StateID: &stateID}); err != nil {
				t.Fatalf("set state %s: %v", name, err)
			}
		}
		return iss.ID
	}
	p1, p2 := fx.proj1.Identifier, fx.proj2.Identifier

	// P1 issues.
	mkIssue(fx.proj1, p1, "i1", fx.dev1, 4, fx.started1, fx.point5, &fx.yesterday) // urgent, 5pts, overdue
	mkIssue(fx.proj1, p1, "i2", fx.dev1, 3, fx.started1, fx.point2, &fx.tomorrow)  // high, 2pts
	mkIssue(fx.proj1, p1, "i3", fx.dev1, 0, fx.started1, "", nil)                  // none, unestimated
	i4 := mkIssue(fx.proj1, p1, "i4", fx.dev2, 1, fx.done1, fx.point5, &fx.yesterday)
	i5 := mkIssue(fx.proj1, p1, "i5", fx.dev2, 2, fx.done1, "", nil)
	mkIssue(fx.proj1, p1, "i6", "", 2, fx.started1, "", nil) // unassigned open
	mkIssue(fx.proj1, p1, "i7", "", 1, fx.done1, "", nil)    // unassigned but done
	i8 := mkIssue(fx.proj1, p1, "i8", fx.dev1, 2, fx.started1, "", nil)
	i9 := mkIssue(fx.proj1, p1, "i9", fx.dev1, 2, fx.started1, "", nil)
	// i5's completion is 20 days old: outside the velocity window.
	if _, err := pool.Exec(ctx,
		`UPDATE issue_activities SET created_at = now() - interval '20 days'
		  WHERE issue_id = $1::uuid AND field = 'state_id'`, i5); err != nil {
		t.Fatalf("backdate i5: %v", err)
	}
	_ = i4
	// Archived and deleted issues never count as open.
	if _, err := pool.Exec(ctx, `UPDATE issues SET archived_at = now() WHERE id = $1::uuid`, i8); err != nil {
		t.Fatalf("archive i8: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE issues SET deleted_at = now() WHERE id = $1::uuid`, i9); err != nil {
		t.Fatalf("delete i9: %v", err)
	}

	// P2 issues.
	mkIssue(fx.proj2, p2, "j1", fx.dev1, 1, fx.started2, "", nil) // low, unestimated
	mkIssue(fx.proj2, p2, "j2", "", 2, fx.started2, "", nil)      // unassigned open
	mkIssue(fx.proj2, p2, "j3", fx.dev2, 3, fx.done2, "", nil)    // done 0 days ago → velocity

	return fx
}

func findMember(t *testing.T, members []WorkloadMember, userID string) WorkloadMember {
	t.Helper()
	for _, m := range members {
		if m.UserID == userID {
			return m
		}
	}
	t.Fatalf("member %s missing from report", userID)
	return WorkloadMember{}
}

func TestGetWorkloadAggregation(t *testing.T) {
	fx := seedWorkloadFixture(t)

	rep, err := GetWorkload(fx.ctx, fx.pool, fx.wsID, fx.admin, nil)
	if err != nil {
		t.Fatalf("GetWorkload: %v", err)
	}
	if len(rep.Members) != 3 {
		t.Fatalf("members = %d, want 3 (admin + 2 devs)", len(rep.Members))
	}
	if rep.GeneratedAt.IsZero() {
		t.Fatalf("generated_at missing")
	}

	dev1 := findMember(t, rep.Members, fx.dev1)
	if dev1.DisplayName != "Dev One" {
		t.Fatalf("dev1 display_name = %q, want name-over-email", dev1.DisplayName)
	}
	if dev1.Role != RoleMember {
		t.Fatalf("dev1 role = %d, want %d", dev1.Role, RoleMember)
	}
	if dev1.WeeklyCapacityPoints != nil {
		t.Fatalf("dev1 capacity = %v, want nil (unset)", dev1.WeeklyCapacityPoints)
	}
	// i1+i2+i3 (P1) + j1 (P2). i8 archived, i9 deleted.
	if dev1.OpenCount != 4 {
		t.Fatalf("dev1 open_count = %d, want 4", dev1.OpenCount)
	}
	if dev1.OpenPoints != 7 { // 5 + 2
		t.Fatalf("dev1 open_points = %d, want 7", dev1.OpenPoints)
	}
	if dev1.OverdueCount != 1 { // i1 only
		t.Fatalf("dev1 overdue_count = %d, want 1", dev1.OverdueCount)
	}
	p := dev1.Priority
	if p.Urgent != 1 || p.High != 1 || p.Medium != 0 || p.Low != 1 || p.None != 1 {
		t.Fatalf("dev1 priority = %+v, want {1 1 0 1 1}", p)
	}
	if dev1.Completed14d != 0 {
		t.Fatalf("dev1 completed_14d = %d, want 0", dev1.Completed14d)
	}
	if dev1.Overloaded {
		t.Fatalf("dev1 overloaded with nil capacity")
	}
	if len(dev1.ProjectIDs) != 2 {
		t.Fatalf("dev1 project_ids = %v, want 2 projects", dev1.ProjectIDs)
	}

	dev2 := findMember(t, rep.Members, fx.dev2)
	if dev2.OpenCount != 0 || dev2.OpenPoints != 0 || dev2.OverdueCount != 0 {
		t.Fatalf("dev2 open = %+v, want zeros", dev2)
	}
	// i4 (today) + j3 (today) count; i5 is 20 days old.
	if dev2.Completed14d != 2 {
		t.Fatalf("dev2 completed_14d = %d, want 2", dev2.Completed14d)
	}
	if dev2.Overloaded {
		t.Fatalf("dev2 overloaded with nil capacity")
	}

	admin := findMember(t, rep.Members, fx.admin)
	if admin.OpenCount != 0 || admin.Completed14d != 0 {
		t.Fatalf("admin = %+v, want zeros", admin)
	}
	if admin.DisplayName == "" {
		t.Fatalf("admin display_name empty")
	}

	if len(rep.Unassigned) != 2 {
		t.Fatalf("unassigned rows = %d, want 2", len(rep.Unassigned))
	}
	byIdent := map[string]int64{}
	for _, u := range rep.Unassigned {
		byIdent[u.ProjectIdentifier] = u.OpenCount
		if u.ProjectID == "" {
			t.Fatalf("unassigned row missing project_id")
		}
	}
	// i6 only (i7 is done); j2 only.
	if byIdent[fx.proj1.Identifier] != 1 || byIdent[fx.proj2.Identifier] != 1 {
		t.Fatalf("unassigned = %v, want 1 per project", byIdent)
	}
}

func TestGetWorkloadProjectFilter(t *testing.T) {
	fx := seedWorkloadFixture(t)
	p1 := uuid.MustParse(fx.proj1.ID)

	rep, err := GetWorkload(fx.ctx, fx.pool, fx.wsID, fx.admin, []uuid.UUID{p1, p1}) // dupes deduped
	if err != nil {
		t.Fatalf("GetWorkload: %v", err)
	}
	dev1 := findMember(t, rep.Members, fx.dev1)
	if dev1.OpenCount != 3 { // i1+i2+i3; j1 (P2) filtered out
		t.Fatalf("dev1 open_count = %d, want 3", dev1.OpenCount)
	}
	if len(dev1.ProjectIDs) != 1 || dev1.ProjectIDs[0] != fx.proj1.ID {
		t.Fatalf("dev1 project_ids = %v, want [P1]", dev1.ProjectIDs)
	}
	if len(rep.Unassigned) != 1 || rep.Unassigned[0].ProjectIdentifier != fx.proj1.Identifier {
		t.Fatalf("unassigned = %+v, want P1 only", rep.Unassigned)
	}
	// dev2's velocity is P1-scoped too: i4 only (j3 is P2).
	dev2 := findMember(t, rep.Members, fx.dev2)
	if dev2.Completed14d != 1 {
		t.Fatalf("dev2 completed_14d = %d, want 1 (P1-scoped)", dev2.Completed14d)
	}
}

func TestGetWorkloadTenancy(t *testing.T) {
	fx := seedWorkloadFixture(t)

	// Unknown workspace → ErrNotFound.
	if _, err := GetWorkload(fx.ctx, fx.pool, uuid.New(), fx.admin, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown workspace err = %v, want ErrNotFound", err)
	}

	// A project of another workspace → ErrProjectNotFound, never a leak.
	otherAdmin := createTestUser(t, fx.pool, uniqueTestEmail("wl-other"))
	otherSlug := uniqueTestSlug("wl-other-ws")
	createTestWorkspace(t, fx.pool, "Other Co", otherSlug, otherAdmin)
	otherProj := createTestProject(t, fx.pool, otherSlug, otherAdmin, "Other", uniqueTestIdentifier())
	otherPID := uuid.MustParse(otherProj.ID)
	if _, err := GetWorkload(fx.ctx, fx.pool, fx.wsID, fx.admin, []uuid.UUID{otherPID}); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("foreign project err = %v, want ErrProjectNotFound", err)
	}

	// The other workspace's report contains none of this workspace's members.
	rep, err := GetWorkload(fx.ctx, fx.pool, uuid.MustParse(otherProj.WorkspaceID), otherAdmin, nil)
	if err != nil {
		t.Fatalf("other GetWorkload: %v", err)
	}
	for _, m := range rep.Members {
		if m.UserID == fx.dev1 || m.UserID == fx.dev2 {
			t.Fatalf("cross-workspace member leaked: %s", m.UserID)
		}
	}
	if len(rep.Members) != 1 || rep.Members[0].UserID != otherAdmin {
		t.Fatalf("other members = %+v, want only otherAdmin", rep.Members)
	}
}

func TestSetMemberCapacity(t *testing.T) {
	fx := seedWorkloadFixture(t)

	cap := 8
	if err := SetMemberCapacity(fx.ctx, fx.pool, fx.wsID, uuid.MustParse(fx.dev1), &cap); err != nil {
		t.Fatalf("set: %v", err)
	}
	rep, err := GetWorkload(fx.ctx, fx.pool, fx.wsID, fx.admin, nil)
	if err != nil {
		t.Fatalf("GetWorkload: %v", err)
	}
	dev1 := findMember(t, rep.Members, fx.dev1)
	if dev1.WeeklyCapacityPoints == nil || *dev1.WeeklyCapacityPoints != 8 {
		t.Fatalf("capacity = %v, want 8", dev1.WeeklyCapacityPoints)
	}
	if dev1.Overloaded { // 7 open points < 8
		t.Fatalf("overloaded at 7/8")
	}

	// Tighten below the open points: overloaded trips.
	tight := 6
	if err := SetMemberCapacity(fx.ctx, fx.pool, fx.wsID, uuid.MustParse(fx.dev1), &tight); err != nil {
		t.Fatalf("tighten: %v", err)
	}
	rep, err = GetWorkload(fx.ctx, fx.pool, fx.wsID, fx.admin, nil)
	if err != nil {
		t.Fatalf("GetWorkload: %v", err)
	}
	if dev1 = findMember(t, rep.Members, fx.dev1); !dev1.Overloaded {
		t.Fatalf("not overloaded at 7/6")
	}

	// Zero capacity never trips the flag (0 = untracked, not over budget).
	zero := 0
	if err := SetMemberCapacity(fx.ctx, fx.pool, fx.wsID, uuid.MustParse(fx.dev1), &zero); err != nil {
		t.Fatalf("zero: %v", err)
	}
	rep, err = GetWorkload(fx.ctx, fx.pool, fx.wsID, fx.admin, nil)
	if err != nil {
		t.Fatalf("GetWorkload: %v", err)
	}
	if dev1 = findMember(t, rep.Members, fx.dev1); dev1.Overloaded {
		t.Fatalf("overloaded at capacity 0")
	}

	// Idempotent: same value twice is fine.
	if err := SetMemberCapacity(fx.ctx, fx.pool, fx.wsID, uuid.MustParse(fx.dev1), &zero); err != nil {
		t.Fatalf("idempotent re-set: %v", err)
	}

	// Nil clears back to NULL.
	if err := SetMemberCapacity(fx.ctx, fx.pool, fx.wsID, uuid.MustParse(fx.dev1), nil); err != nil {
		t.Fatalf("clear: %v", err)
	}
	rep, err = GetWorkload(fx.ctx, fx.pool, fx.wsID, fx.admin, nil)
	if err != nil {
		t.Fatalf("GetWorkload: %v", err)
	}
	if dev1 = findMember(t, rep.Members, fx.dev1); dev1.WeeklyCapacityPoints != nil {
		t.Fatalf("capacity = %v, want nil after clear", dev1.WeeklyCapacityPoints)
	}
}

func TestSetMemberCapacityValidation(t *testing.T) {
	fx := seedWorkloadFixture(t)
	dev1 := uuid.MustParse(fx.dev1)

	for _, bad := range []int{-1, 1000, 1 << 30} {
		v := bad
		if err := SetMemberCapacity(fx.ctx, fx.pool, fx.wsID, dev1, &v); !errors.Is(err, ErrInvalidCapacity) {
			t.Fatalf("capacity %d err = %v, want ErrInvalidCapacity", bad, err)
		}
	}
	// Bounds are inclusive.
	for _, ok := range []int{0, 999} {
		v := ok
		if err := SetMemberCapacity(fx.ctx, fx.pool, fx.wsID, dev1, &v); err != nil {
			t.Fatalf("capacity %d: %v", ok, err)
		}
	}

	// Non-member user (and unknown workspace) → ErrMemberNotFound.
	stranger := createTestUser(t, fx.pool, uniqueTestEmail("wl-stranger"))
	v := 10
	if err := SetMemberCapacity(fx.ctx, fx.pool, fx.wsID, uuid.MustParse(stranger), &v); !errors.Is(err, ErrMemberNotFound) {
		t.Fatalf("stranger err = %v, want ErrMemberNotFound", err)
	}
	if err := SetMemberCapacity(fx.ctx, fx.pool, uuid.New(), dev1, &v); !errors.Is(err, ErrMemberNotFound) {
		t.Fatalf("unknown workspace err = %v, want ErrMemberNotFound", err)
	}

	// A member of another workspace is not a member here either.
	otherAdmin := createTestUser(t, fx.pool, uniqueTestEmail("wl-cap-other"))
	createTestWorkspace(t, fx.pool, "Cap Other", uniqueTestSlug("wl-cap-other"), otherAdmin)
	if err := SetMemberCapacity(fx.ctx, fx.pool, fx.wsID, uuid.MustParse(otherAdmin), &v); !errors.Is(err, ErrMemberNotFound) {
		t.Fatalf("cross-workspace err = %v, want ErrMemberNotFound", err)
	}
}
