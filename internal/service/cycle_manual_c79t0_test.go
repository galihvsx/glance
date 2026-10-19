package service

// Manual start/end (C79T0): the "Override scheduled cycles" backend
// contract — StartCycle (upcoming → current with the ticker-race
// predicate, overlap re-check, member (15)+ gate, cycle.updated
// announce) and EndCycle (delegates to the exact CompleteCycle ticker
// path, repeat ends are 400, end_date untouched). Real test database,
// no skips.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// manualCycleSetup seeds one project for the manual start/end tests.
func manualCycleSetup(t *testing.T, prefix string) (context.Context, *pgxpool.Pool, string, string, string, *Project) {
	t.Helper()
	ctx := context.Background()
	pool := newTestPool(t)
	actor := createTestUser(t, pool, uniqueTestEmail(prefix))
	slug := uniqueTestSlug(prefix + "-ws")
	createTestWorkspace(t, pool, "Cycle Co", slug, actor)
	ident := uniqueTestIdentifier()
	proj := createTestProject(t, pool, slug, actor, "Eng", ident)
	return ctx, pool, slug, ident, actor, proj
}

// TestStartCycleFlipsUpcomingToCurrent pins the happy path: an upcoming
// cycle starts early, status flips to current, the response carries a
// live snapshot, and exactly one cycle.updated "current" event fans out
// on the project + workspace channels — the same fan-out the ticker's
// activate pass uses.
func TestStartCycleFlipsUpcomingToCurrent(t *testing.T) {
	ctx, pool, slug, ident, actor, _ := manualCycleSetup(t, "ms-start")
	fake := installCapturingBroadcaster(t)
	now := time.Now()

	c, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "Sprint 9", StartDate: now.AddDate(0, 0, 7), EndDate: now.AddDate(0, 0, 21),
	})
	if err != nil {
		t.Fatalf("CreateCycle: %v", err)
	}
	if c.Status != "upcoming" {
		t.Fatalf("seed status = %q, want upcoming", c.Status)
	}

	started, err := StartCycle(ctx, pool, slug, ident, actor, c.ID)
	if err != nil {
		t.Fatalf("StartCycle: %v", err)
	}
	if started.Status != "current" {
		t.Fatalf("status = %q, want current", started.Status)
	}
	// Scheduled dates are never rewritten by a manual start.
	if !started.StartDate.Equal(c.StartDate) || !started.EndDate.Equal(c.EndDate) {
		t.Fatalf("dates changed: %v..%v, want %v..%v",
			started.StartDate, started.EndDate, c.StartDate, c.EndDate)
	}
	for _, g := range snapshotGroups {
		if _, ok := started.ProgressSnapshot[g]; !ok {
			t.Fatalf("snapshot missing group %q", g)
		}
	}

	events := fake.snapshot()
	if len(events) != 1 {
		t.Fatalf("events = %d, want exactly 1 cycle.updated announce", len(events))
	}
	ev := events[0]
	if ev.eventType != EventCycleUpdated {
		t.Fatalf("event type = %q, want %q", ev.eventType, EventCycleUpdated)
	}
	wantChannels := map[string]bool{
		"project:" + slug + ":" + ident: true,
		"workspace:" + slug:             true,
	}
	for _, ch := range ev.channels {
		delete(wantChannels, ch)
	}
	if len(wantChannels) != 0 {
		t.Fatalf("channels = %v, want project + workspace channels", ev.channels)
	}
	payload, ok := ev.data.(map[string]string)
	if !ok {
		t.Fatalf("payload type = %T, want map[string]string", ev.data)
	}
	if payload["id"] != c.ID || payload["status"] != "current" {
		t.Fatalf("payload = %v, want {id %s, status current}", payload, c.ID)
	}
}

// TestStartCycleRejectsNonUpcoming pins the 400 map: starting a current
// or completed cycle is ErrCycleNotStartable, never a silent no-op.
func TestStartCycleRejectsNonUpcoming(t *testing.T) {
	ctx, pool, slug, ident, actor, _ := manualCycleSetup(t, "ms-nostart")
	now := time.Now()

	// Already current.
	c, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "Live", StartDate: now.AddDate(0, 0, -1), EndDate: now.AddDate(0, 0, 13),
	})
	if err != nil {
		t.Fatalf("CreateCycle: %v", err)
	}
	if _, err := StartCycle(ctx, pool, slug, ident, actor, c.ID); !errors.Is(err, ErrCycleNotStartable) {
		t.Fatalf("start current: err = %v, want ErrCycleNotStartable", err)
	}

	// Completed.
	ended, err := EndCycle(ctx, pool, slug, ident, actor, c.ID)
	if err != nil {
		t.Fatalf("EndCycle: %v", err)
	}
	if ended.Status != "completed" {
		t.Fatalf("end status = %q, want completed", ended.Status)
	}
	if _, err := StartCycle(ctx, pool, slug, ident, actor, c.ID); !errors.Is(err, ErrCycleNotStartable) {
		t.Fatalf("start completed: err = %v, want ErrCycleNotStartable", err)
	}
}

// TestStartCycleOverlap409NamesCycle pins the C78 invariant for manual
// starts: with parallel_cycles off, starting an upcoming cycle whose
// (unchanged) range overlaps another live cycle is ErrCycleOverlap, and
// the conflicting cycle is named.
func TestStartCycleOverlap409NamesCycle(t *testing.T) {
	ctx, pool, slug, ident, actor, _ := manualCycleSetup(t, "ms-overlap")
	now := time.Now()

	live, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "Sprint 12", StartDate: now.AddDate(0, 0, -7), EndDate: now.AddDate(0, 0, 7),
	})
	if err != nil {
		t.Fatalf("CreateCycle live: %v", err)
	}
	_ = live

	// Overlapping upcoming cycle: born upcoming is impossible while the
	// toggle is off (create would 409), so insert the row directly to
	// isolate the start-time check from the create-time check.
	var upcomingID string
	err = pool.QueryRow(ctx,
		`INSERT INTO cycles (project_id, name, start_date, end_date, status)
		 VALUES ($1::uuid, 'Sprint 13', $2::date, $3::date, 'upcoming')
		 RETURNING id::text`,
		projIDOf(t, pool, slug, ident), now.AddDate(0, 0, 3), now.AddDate(0, 0, 17)).Scan(&upcomingID)
	if err != nil {
		t.Fatalf("insert overlapping upcoming: %v", err)
	}
	if _, err := StartCycle(ctx, pool, slug, ident, actor, upcomingID); !errors.Is(err, ErrCycleOverlap) {
		t.Fatalf("start overlapping: err = %v, want ErrCycleOverlap", err)
	} else {
		var oe *CycleOverlapError
		if !errors.As(err, &oe) {
			t.Fatalf("err type = %T, want *CycleOverlapError", err)
		}
		if oe.Name != "Sprint 12" {
			t.Fatalf("conflict names %q, want Sprint 12", oe.Name)
		}
	}
}

// TestStartCycleParallelOnAllowsOverlap: with the parallel_cycles toggle
// on, a manual start with an overlapping range is allowed (today's
// behavior preserved).
func TestStartCycleParallelOnAllowsOverlap(t *testing.T) {
	ctx, pool, slug, ident, actor, _ := manualCycleSetup(t, "ms-parallel")
	now := time.Now()

	if _, err := UpdateProject(ctx, pool, slug, ident, actor,
		ProjectPatch{ParallelCycles: boolPtr(true)}); err != nil {
		t.Fatalf("enable parallel_cycles: %v", err)
	}
	if _, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "Sprint A", StartDate: now.AddDate(0, 0, -7), EndDate: now.AddDate(0, 0, 7),
	}); err != nil {
		t.Fatalf("CreateCycle A: %v", err)
	}
	b, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "Sprint B", StartDate: now.AddDate(0, 0, 3), EndDate: now.AddDate(0, 0, 17),
	})
	if err != nil {
		t.Fatalf("CreateCycle B: %v", err)
	}
	if b.Status != "upcoming" {
		t.Fatalf("B status = %q, want upcoming", b.Status)
	}
	started, err := StartCycle(ctx, pool, slug, ident, actor, b.ID)
	if err != nil {
		t.Fatalf("StartCycle with parallel on: %v", err)
	}
	if started.Status != "current" {
		t.Fatalf("status = %q, want current", started.Status)
	}
}

// TestStartCycleNonOverlappingLiveCycleAllowed: toggle off, another live
// cycle with a disjoint range — start proceeds (self-exclusion and
// range-exclusion both exercised).
func TestStartCycleNonOverlappingLiveCycleAllowed(t *testing.T) {
	ctx, pool, slug, ident, actor, _ := manualCycleSetup(t, "ms-disjoint")
	now := time.Now()

	if _, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "Past", StartDate: now.AddDate(0, 0, -60), EndDate: now.AddDate(0, 0, -46),
	}); err != nil {
		t.Fatalf("CreateCycle past: %v", err)
	}
	c, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "Future", StartDate: now.AddDate(0, 0, 30), EndDate: now.AddDate(0, 0, 44),
	})
	if err != nil {
		t.Fatalf("CreateCycle future: %v", err)
	}
	started, err := StartCycle(ctx, pool, slug, ident, actor, c.ID)
	if err != nil {
		t.Fatalf("StartCycle disjoint: %v", err)
	}
	if started.Status != "current" {
		t.Fatalf("status = %q, want current", started.Status)
	}
}

// TestStartCyclePermissionGate mirrors the create/update/delete gate:
// guests (5) are read-only; members (15)+ may start.
func TestStartCyclePermissionGate(t *testing.T) {
	ctx, pool, slug, ident, actor, _ := manualCycleSetup(t, "ms-gate")
	now := time.Now()

	c, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "Sprint 9", StartDate: now.AddDate(0, 0, 7), EndDate: now.AddDate(0, 0, 21),
	})
	if err != nil {
		t.Fatalf("CreateCycle: %v", err)
	}
	guest := createTestUser(t, pool, uniqueTestEmail("ms-gate-guest"))
	addTestMember(t, pool, slug, actor, guest, RoleGuest)
	if _, err := StartCycle(ctx, pool, slug, ident, guest, c.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest start: err = %v, want ErrForbidden", err)
	}
	// Non-member at all: workspace not found, same as the other
	// cycle mutations.
	outsider := createTestUser(t, pool, uniqueTestEmail("ms-gate-outsider"))
	if _, err := StartCycle(ctx, pool, slug, ident, outsider, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("outsider start: err = %v, want ErrNotFound", err)
	}
	// The member can still start afterwards — the guest attempt changed
	// nothing.
	if _, err := StartCycle(ctx, pool, slug, ident, actor, c.ID); err != nil {
		t.Fatalf("member start after guest 403: %v", err)
	}
}

// TestStartCycleNotFound pins the 404/400 maps: unknown id →
// ErrCycleNotFound, malformed id → ErrInvalidCycleID.
func TestStartCycleNotFound(t *testing.T) {
	ctx, pool, slug, ident, actor, _ := manualCycleSetup(t, "ms-404")
	if _, err := StartCycle(ctx, pool, slug, ident, actor,
		"00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrCycleNotFound) {
		t.Fatalf("unknown id: err = %v, want ErrCycleNotFound", err)
	}
	if _, err := StartCycle(ctx, pool, slug, ident, actor,
		"not-a-uuid"); !errors.Is(err, ErrInvalidCycleID) {
		t.Fatalf("malformed id: err = %v, want ErrInvalidCycleID", err)
	}
}

// TestEndCycleFromCurrentFreezesAndRollsOver pins that manual end runs
// the EXACT ticker path: the progress snapshot freezes with the live
// counts, incomplete issues transfer to the next queued cycle, done
// issues stay attached, end_date is kept, and cycle.updated "completed"
// fans out.
func TestEndCycleFromCurrentFreezesAndRollsOver(t *testing.T) {
	ctx, pool, slug, ident, actor, proj := manualCycleSetup(t, "ms-endroll")
	fake := installCapturingBroadcaster(t)
	now := time.Now()

	cur, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "Sprint 1", StartDate: now.AddDate(0, 0, -7), EndDate: now.AddDate(0, 0, 7),
	})
	if err != nil {
		t.Fatalf("CreateCycle current: %v", err)
	}
	next, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "Sprint 2", StartDate: now.AddDate(0, 0, 8), EndDate: now.AddDate(0, 0, 22),
	})
	if err != nil {
		t.Fatalf("CreateCycle next: %v", err)
	}

	// Issues: one started (rolls over), one completed (stays).
	open := createTestIssue(t, pool, slug, ident, actor, "open-work")
	done := createTestIssue(t, pool, slug, ident, actor, "done-work")
	openState := stateIDByGroup(t, pool, proj.ID, "started")
	doneState := stateIDByGroup(t, pool, proj.ID, "completed")
	if _, err := UpdateIssue(ctx, pool, slug, ident, open.ID, actor,
		IssuePatch{StateID: &openState}); err != nil {
		t.Fatalf("set open state: %v", err)
	}
	if _, err := UpdateIssue(ctx, pool, slug, ident, done.ID, actor,
		IssuePatch{StateID: &doneState}); err != nil {
		t.Fatalf("set done state: %v", err)
	}
	if err := AddCycleIssues(ctx, pool, slug, ident, actor, cur.ID,
		[]string{open.ID, done.ID}); err != nil {
		t.Fatalf("AddCycleIssues: %v", err)
	}

	ended, err := EndCycle(ctx, pool, slug, ident, actor, cur.ID)
	if err != nil {
		t.Fatalf("EndCycle: %v", err)
	}
	if ended.Status != "completed" {
		t.Fatalf("status = %q, want completed", ended.Status)
	}
	// end_date is NOT rewritten — the report keeps scheduled dates.
	if !ended.EndDate.Equal(cur.EndDate) {
		t.Fatalf("end_date = %v, want the scheduled %v", ended.EndDate, cur.EndDate)
	}
	// Frozen snapshot: the live counts at end time.
	if ended.ProgressSnapshot["started"] != 1 || ended.ProgressSnapshot["completed"] != 1 {
		t.Fatalf("frozen snapshot = %+v, want started=1 completed=1", ended.ProgressSnapshot)
	}
	// Rollover: the incomplete issue transferred to the next cycle.
	if !memberInCycle(t, pool, next.ID, open.ID) {
		t.Fatal("incomplete issue did not transfer to the next cycle")
	}
	if memberInCycle(t, pool, cur.ID, open.ID) {
		t.Fatal("incomplete issue still attached to the completed cycle")
	}
	// The done issue stays attached to the completed cycle's report.
	if !memberInCycle(t, pool, cur.ID, done.ID) {
		t.Fatal("completed issue detached from the completed cycle")
	}

	// Setup (UpdateIssue state moves) also broadcasts issue.updated, so
	// filter for the cycle fan-out.
	var cycleEvents []capturedEvent
	for _, ev := range fake.snapshot() {
		if ev.eventType == EventCycleUpdated {
			cycleEvents = append(cycleEvents, ev)
		}
	}
	if len(cycleEvents) != 1 {
		t.Fatalf("cycle.updated events = %d, want exactly 1", len(cycleEvents))
	}
	ev := cycleEvents[0]
	if ev.eventType != EventCycleUpdated {
		t.Fatalf("event type = %q, want %q", ev.eventType, EventCycleUpdated)
	}
	payload, ok := ev.data.(map[string]string)
	if !ok || payload["id"] != cur.ID || payload["status"] != "completed" {
		t.Fatalf("payload = %v, want {id %s, status completed}", ev.data, cur.ID)
	}
}

// TestEndCycleFromUpcoming pins that ending before the start is legal:
// status completes, the close_in_days rule applies unchanged (no next
// cycle queued here and end_date is in the future, so incomplete issues
// stay attached until the grace passes).
func TestEndCycleFromUpcoming(t *testing.T) {
	ctx, pool, slug, ident, actor, proj := manualCycleSetup(t, "ms-endup")
	now := time.Now()

	c, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "Sprint 9", StartDate: now.AddDate(0, 0, 7), EndDate: now.AddDate(0, 0, 21),
	})
	if err != nil {
		t.Fatalf("CreateCycle: %v", err)
	}
	iss := createTestIssue(t, pool, slug, ident, actor, "early-work")
	openState := stateIDByGroup(t, pool, proj.ID, "started")
	if _, err := UpdateIssue(ctx, pool, slug, ident, iss.ID, actor,
		IssuePatch{StateID: &openState}); err != nil {
		t.Fatalf("set state: %v", err)
	}
	if err := AddCycleIssues(ctx, pool, slug, ident, actor, c.ID, []string{iss.ID}); err != nil {
		t.Fatalf("AddCycleIssues: %v", err)
	}

	ended, err := EndCycle(ctx, pool, slug, ident, actor, c.ID)
	if err != nil {
		t.Fatalf("EndCycle upcoming: %v", err)
	}
	if ended.Status != "completed" {
		t.Fatalf("status = %q, want completed", ended.Status)
	}
	if !ended.EndDate.Equal(c.EndDate) {
		t.Fatalf("end_date = %v, want the scheduled %v", ended.EndDate, c.EndDate)
	}
	// No next cycle queued and the scheduled end is in the future:
	// close_in_days (0 default) grace has not passed, so the incomplete
	// issue stays attached — SweepCompletedCycles moves it later. This
	// is the existing rule, unchanged by manual end.
	if !memberInCycle(t, pool, c.ID, iss.ID) {
		t.Fatal("incomplete issue detached early; the grace rule changed")
	}
}

// TestEndCycleAlreadyCompleted pins the 400 map: a repeat end is
// ErrCycleAlreadyCompleted and announces nothing further.
func TestEndCycleAlreadyCompleted(t *testing.T) {
	ctx, pool, slug, ident, actor, _ := manualCycleSetup(t, "ms-dblend")
	fake := installCapturingBroadcaster(t)
	now := time.Now()

	c, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "Sprint 1", StartDate: now.AddDate(0, 0, -7), EndDate: now.AddDate(0, 0, 7),
	})
	if err != nil {
		t.Fatalf("CreateCycle: %v", err)
	}
	if _, err := EndCycle(ctx, pool, slug, ident, actor, c.ID); err != nil {
		t.Fatalf("first EndCycle: %v", err)
	}
	if _, err := EndCycle(ctx, pool, slug, ident, actor, c.ID); !errors.Is(err, ErrCycleAlreadyCompleted) {
		t.Fatalf("repeat EndCycle: err = %v, want ErrCycleAlreadyCompleted", err)
	}
	if n := len(fake.snapshot()); n != 1 {
		t.Fatalf("events = %d, want exactly 1 (the repeat must announce nothing)", n)
	}
}

// TestEndCyclePermissionGate: guests (5) read-only on end too.
func TestEndCyclePermissionGate(t *testing.T) {
	ctx, pool, slug, ident, actor, _ := manualCycleSetup(t, "ms-endgate")
	now := time.Now()

	c, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "Sprint 1", StartDate: now.AddDate(0, 0, -7), EndDate: now.AddDate(0, 0, 7),
	})
	if err != nil {
		t.Fatalf("CreateCycle: %v", err)
	}
	guest := createTestUser(t, pool, uniqueTestEmail("ms-endgate-guest"))
	addTestMember(t, pool, slug, actor, guest, RoleGuest)
	if _, err := EndCycle(ctx, pool, slug, ident, guest, c.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest end: err = %v, want ErrForbidden", err)
	}
	if _, err := EndCycle(ctx, pool, slug, ident, actor,
		"00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrCycleNotFound) {
		t.Fatalf("unknown id: err = %v, want ErrCycleNotFound", err)
	}
	// Still completable by the member afterwards.
	if _, err := EndCycle(ctx, pool, slug, ident, actor, c.ID); err != nil {
		t.Fatalf("member end after guest 403: %v", err)
	}
}

// projIDOf resolves the project id for the direct-insert overlap test
// (the row is inserted by hand to isolate the start-time overlap check
// from the create-time one).
func projIDOf(t *testing.T, pool *pgxpool.Pool, slug, ident string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(),
		`SELECT p.id::text FROM projects p JOIN workspaces w ON w.id = p.workspace_id
		 WHERE w.slug = $1 AND p.identifier = $2`, slug, ident).Scan(&id)
	if err != nil {
		t.Fatalf("projIDOf: %v", err)
	}
	return id
}
