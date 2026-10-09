package service

// Analytics (C4T3): summary / cycle burndown / trends aggregates.
// Tests seed exact data and assert exact counts — not just 200s.

import (
	"context"
	"testing"
	"time"
)

func TestAnalyticsSummary(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	actor := createTestUser(t, pool, uniqueTestEmail("analytics"))
	slug := uniqueTestSlug("analytics-ws")
	createTestWorkspace(t, pool, "Analytics Co", slug, actor)
	ident := uniqueTestIdentifier()
	proj := createTestProject(t, pool, slug, actor, "Eng", ident)

	doneState := stateIDByGroup(t, pool, proj.ID, "completed")
	startedState := stateIDByGroup(t, pool, proj.ID, "started")

	// Estimate scale: S=2, M=5.
	var estID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO estimates (project_id, name) VALUES ($1::uuid, 'pts') RETURNING id::text`,
		proj.ID).Scan(&estID); err != nil {
		t.Fatalf("seed estimate: %v", err)
	}
	pointID := map[int]string{}
	for _, pv := range [][2]int{{2, 2}, {5, 5}} {
		var pid string
		if err := pool.QueryRow(ctx,
			`INSERT INTO estimate_points (estimate_id, key, value) VALUES ($1::uuid, $2, $3) RETURNING id::text`,
			estID, string(rune('0'+pv[0])), pv[1]).Scan(&pid); err != nil {
			t.Fatalf("seed point: %v", err)
		}
		pointID[pv[1]] = pid
	}

	lbl := createTestLabel(t, pool, slug, ident, actor, "backend")

	mkIssue := func(name string, priority int, stateID, pointID string, targetDate *time.Time) string {
		in := CreateIssueInput{Name: name, Priority: &priority, TargetDate: targetDate}
		if pointID != "" {
			in.EstimatePointID = &pointID
		}
		iss, err := CreateIssue(ctx, pool, slug, ident, actor, in)
		if err != nil {
			t.Fatalf("CreateIssue %s: %v", name, err)
		}
		if stateID != "" {
			if _, err := UpdateIssue(ctx, pool, slug, ident, iss.ID, actor, IssuePatch{StateID: &stateID}); err != nil {
				t.Fatalf("set state %s: %v", name, err)
			}
		}
		return iss.ID
	}

	yesterday := time.Now().UTC().AddDate(0, 0, -1)
	tomorrow := time.Now().UTC().AddDate(0, 0, 1)

	// i1: started, priority 1, estimate 2, overdue (target yesterday), label backend.
	i1 := mkIssue("i1", 1, startedState, pointID[2], &yesterday)
	if err := AssignLabel(ctx, pool, slug, ident, i1, lbl.ID, actor); err != nil {
		t.Fatalf("AssignLabel: %v", err)
	}
	// i2: done, priority 3, estimate 5, target tomorrow (not overdue).
	mkIssue("i2", 3, doneState, pointID[5], &tomorrow)
	// i3: started, priority 1, no estimate, no target date.
	mkIssue("i3", 1, startedState, "", nil)
	// i4: done, priority 0, no estimate, overdue target but done → not overdue.
	mkIssue("i4", 0, doneState, "", &yesterday)

	s, err := GetAnalyticsSummary(ctx, pool, slug, ident, actor)
	if err != nil {
		t.Fatalf("GetAnalyticsSummary: %v", err)
	}

	if got := s.ByState[startedState]; got != 2 {
		t.Fatalf("by_state[started] = %d, want 2", got)
	}
	if got := s.ByState[doneState]; got != 2 {
		t.Fatalf("by_state[done] = %d, want 2", got)
	}
	if got := s.ByPriority["1"]; got != 2 {
		t.Fatalf("by_priority[1] = %d, want 2", got)
	}
	if got := s.ByPriority["3"]; got != 1 {
		t.Fatalf("by_priority[3] = %d, want 1", got)
	}
	if got := s.ByPriority["0"]; got != 1 {
		t.Fatalf("by_priority[0] = %d, want 1", got)
	}
	if got := s.ByLabel[lbl.ID]; got != 1 {
		t.Fatalf("by_label = %d, want 1", got)
	}
	if s.OverdueCount != 1 { // only i1: i4 is done
		t.Fatalf("overdue = %d, want 1", s.OverdueCount)
	}
	if s.EstimateTotal != 7 { // 2 + 5
		t.Fatalf("estimate_total = %d, want 7", s.EstimateTotal)
	}
	if s.EstimateDone != 5 { // i2's 5
		t.Fatalf("estimate_done = %d, want 5", s.EstimateDone)
	}
}

func TestAnalyticsSummaryAccess(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	actor := createTestUser(t, pool, uniqueTestEmail("analytics-acc"))
	slug := uniqueTestSlug("analytics-acc-ws")
	createTestWorkspace(t, pool, "Analytics Acc", slug, actor)
	ident := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Eng", ident)

	// Outsider (never a member) → 404, not a leak.
	outsider := createTestUser(t, pool, uniqueTestEmail("analytics-out"))
	if _, err := GetAnalyticsSummary(ctx, pool, slug, ident, outsider); !isErrNotFound(err) {
		t.Fatalf("outsider summary: err = %v, want ErrNotFound", err)
	}
	// Guest member may read.
	guest := createTestUser(t, pool, uniqueTestEmail("analytics-guest"))
	addTestMember(t, pool, slug, actor, guest, RoleGuest)
	if _, err := GetAnalyticsSummary(ctx, pool, slug, ident, guest); err != nil {
		t.Fatalf("guest summary: %v", err)
	}
}

func isErrNotFound(err error) bool {
	return err != nil && (isErr(err, ErrNotFound) || isErr(err, ErrProjectNotFound))
}

func TestAnalyticsCycleBurndown(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	actor := createTestUser(t, pool, uniqueTestEmail("analytics-bd"))
	slug := uniqueTestSlug("analytics-bd-ws")
	createTestWorkspace(t, pool, "Analytics BD", slug, actor)
	ident := uniqueTestIdentifier()
	proj := createTestProject(t, pool, slug, actor, "Eng", ident)

	now := time.Now().UTC()
	c, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "S1", StartDate: now.AddDate(0, 0, -2), EndDate: now.AddDate(0, 0, 1),
	})
	if err != nil {
		t.Fatalf("CreateCycle: %v", err)
	}
	doneState := stateIDByGroup(t, pool, proj.ID, "completed")

	// Estimate scale: only one point, value 3.
	var estID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO estimates (project_id, name) VALUES ($1::uuid, 'pts') RETURNING id::text`,
		proj.ID).Scan(&estID); err != nil {
		t.Fatalf("seed estimate: %v", err)
	}
	var pid3 string
	if err := pool.QueryRow(ctx,
		`INSERT INTO estimate_points (estimate_id, key, value) VALUES ($1::uuid, 'M', 3) RETURNING id::text`,
		estID).Scan(&pid3); err != nil {
		t.Fatalf("seed point: %v", err)
	}

	mk := func(name, pointID string) string {
		in := CreateIssueInput{Name: name}
		if pointID != "" {
			in.EstimatePointID = &pointID
		}
		iss, err := CreateIssue(ctx, pool, slug, ident, actor, in)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		if err := AddCycleIssues(ctx, pool, slug, ident, actor, c.ID, []string{iss.ID}); err != nil {
			t.Fatalf("AddCycleIssues: %v", err)
		}
		return iss.ID
	}
	a := mk("a", pid3) // estimate 3
	b := mk("b", "")   // unestimated
	_ = b

	// Backdate membership to cycle start so both count from day 0.
	cycleStart := now.AddDate(0, 0, -2)
	for _, id := range []string{a, b} {
		if _, err := pool.Exec(ctx,
			`UPDATE cycle_issues SET created_at = $1 WHERE cycle_id = $2::uuid AND issue_id = $3::uuid`,
			cycleStart, c.ID, id); err != nil {
			t.Fatalf("backdate membership: %v", err)
		}
	}

	// A completed 1 day ago.
	if _, err := UpdateIssue(ctx, pool, slug, ident, a, actor, IssuePatch{StateID: &doneState}); err != nil {
		t.Fatalf("complete A: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE issue_activities SET created_at = $1 WHERE issue_id = $2::uuid AND field = 'state_id'`,
		now.AddDate(0, 0, -1), a); err != nil {
		t.Fatalf("backdate A activity: %v", err)
	}

	bd, err := GetAnalyticsCycleBurndown(ctx, pool, slug, ident, actor, c.ID)
	if err != nil {
		t.Fatalf("GetAnalyticsCycleBurndown: %v", err)
	}
	if bd.TotalScope != 2 {
		t.Fatalf("total_scope = %d, want 2", bd.TotalScope)
	}
	if len(bd.Days) != 4 { // T-2 .. T+1
		t.Fatalf("days = %d, want 4", len(bd.Days))
	}
	deref := func(p *int) int {
		if p == nil {
			return -1
		}
		return *p
	}
	deref64 := func(p *int64) int64 {
		if p == nil {
			return -1
		}
		return *p
	}
	// T-2: both open → 2 remaining, estimate 3 (only A has one).
	if got := deref(bd.Days[0].Remaining); got != 2 {
		t.Fatalf("day0 remaining = %d, want 2", got)
	}
	if got := deref64(bd.Days[0].RemainingEstimate); got != 3 {
		t.Fatalf("day0 remaining_estimate = %d, want 3", got)
	}
	// T-1: A done → 1 remaining, estimate 0.
	if got := deref(bd.Days[1].Remaining); got != 1 {
		t.Fatalf("day1 remaining = %d, want 1", got)
	}
	if got := deref64(bd.Days[1].RemainingEstimate); got != 0 {
		t.Fatalf("day1 remaining_estimate = %d, want 0", got)
	}
	// T (today): A done → 1 remaining, estimate 0.
	if got := deref(bd.Days[2].Remaining); got != 1 {
		t.Fatalf("day2 remaining = %d, want 1", got)
	}
	if got := deref64(bd.Days[2].RemainingEstimate); got != 0 {
		t.Fatalf("day2 remaining_estimate = %d, want 0", got)
	}
	// T+1: future → nils.
	if bd.Days[3].Remaining != nil || bd.Days[3].RemainingEstimate != nil {
		t.Fatalf("day3 should have nil actuals (future)")
	}

	// Unknown cycle → 404; malformed → 400.
	if _, err := GetAnalyticsCycleBurndown(ctx, pool, slug, ident, actor, "00000000-0000-0000-0000-000000000000"); !isErr(err, ErrCycleNotFound) {
		t.Fatalf("unknown cycle: err = %v, want ErrCycleNotFound", err)
	}
	if _, err := GetAnalyticsCycleBurndown(ctx, pool, slug, ident, actor, "nope"); !isErr(err, ErrInvalidCycleID) {
		t.Fatalf("malformed cycle: err = %v, want ErrInvalidCycleID", err)
	}
}

func TestAnalyticsTrends(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	actor := createTestUser(t, pool, uniqueTestEmail("analytics-tr"))
	slug := uniqueTestSlug("analytics-tr-ws")
	createTestWorkspace(t, pool, "Analytics TR", slug, actor)
	ident := uniqueTestIdentifier()
	proj := createTestProject(t, pool, slug, actor, "Eng", ident)
	doneState := stateIDByGroup(t, pool, proj.ID, "completed")

	now := time.Now().UTC()
	// i1 created 3 days ago, closed 1 day ago.
	i1 := createTestIssue(t, pool, slug, ident, actor, "tr1")
	if _, err := pool.Exec(ctx, `UPDATE issues SET created_at = $1 WHERE id = $2::uuid`,
		now.AddDate(0, 0, -3), i1.ID); err != nil {
		t.Fatalf("backdate i1: %v", err)
	}
	if _, err := UpdateIssue(ctx, pool, slug, ident, i1.ID, actor, IssuePatch{StateID: &doneState}); err != nil {
		t.Fatalf("close i1: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE issue_activities SET created_at = $1 WHERE issue_id = $2::uuid AND field = 'state_id'`,
		now.AddDate(0, 0, -1), i1.ID); err != nil {
		t.Fatalf("backdate i1 close: %v", err)
	}
	// i2 created 1 day ago, still open.
	i2 := createTestIssue(t, pool, slug, ident, actor, "tr2")
	if _, err := pool.Exec(ctx, `UPDATE issues SET created_at = $1 WHERE id = $2::uuid`,
		now.AddDate(0, 0, -1), i2.ID); err != nil {
		t.Fatalf("backdate i2: %v", err)
	}

	// Note: the _created audit row for i1/i2 also exists but field !=
	// 'state_id', so it must not count as a close.
	days, err := GetAnalyticsTrends(ctx, pool, slug, ident, actor, 5)
	if err != nil {
		t.Fatalf("GetAnalyticsTrends: %v", err)
	}
	if len(days) != 5 {
		t.Fatalf("len(days) = %d, want 5", len(days))
	}
	byDate := map[string]AnalyticsTrendDay{}
	for _, d := range days {
		byDate[d.Date] = d
	}
	d3 := now.AddDate(0, 0, -3).Format("2006-01-02")
	d1 := now.AddDate(0, 0, -1).Format("2006-01-02")
	d0 := now.Format("2006-01-02")
	if got := byDate[d3].Created; got != 1 {
		t.Fatalf("d-3 created = %d, want 1", got)
	}
	if got := byDate[d1].Created; got != 1 {
		t.Fatalf("d-1 created = %d, want 1", got)
	}
	if got := byDate[d1].Closed; got != 1 {
		t.Fatalf("d-1 closed = %d, want 1", got)
	}
	if got := byDate[d0].Created; got != 0 {
		t.Fatalf("today created = %d, want 0", got)
	}
	if got := byDate[d0].Closed; got != 0 {
		t.Fatalf("today closed = %d, want 0", got)
	}
}
