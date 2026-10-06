package service

// Intake inbox + triage actions tests (Task 20): default intake seeded
// with the project (CreateProject hook + migration backfill), issues
// created with intake:true land as pending, and the accept / reject /
// snooze / duplicate triage actions with their state machine,
// idempotency, role gates, and the inbox surfacing rule (snoozed items
// resurface once snoozed_till passes). All tests run against the real
// test database — no skips. Reuses the harness from workspace_test.go
// (newTestPool, migrateTestDB, createTestUser, createTestWorkspace,
// uniqueTestEmail, uniqueTestSlug, createTestProject,
// uniqueTestIdentifier, addTestMember) and issue_test.go
// (backlogStateID, countActivities).

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// intakeFixture builds a workspace + project owned by creator and
// returns the identifiers.
func intakeFixture(t *testing.T, pool *pgxpool.Pool, prefix string) (creator, wsSlug, identifier string) {
	t.Helper()
	creator = createTestUser(t, pool, uniqueTestEmail(prefix))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug(prefix), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())
	return creator, ws.Slug, p.Identifier
}

func createIntakeIssue(t *testing.T, pool *pgxpool.Pool, wsSlug, identifier, actorID, name string) *Issue {
	t.Helper()
	iss, err := CreateIssue(context.Background(), pool, wsSlug, identifier, actorID,
		CreateIssueInput{Name: name, Intake: true})
	if err != nil {
		t.Fatalf("CreateIssue(intake): %v", err)
	}
	return iss
}

// intakeStatusOf reads the raw intake_issues.status for an issue.
func intakeStatusOf(t *testing.T, pool *pgxpool.Pool, issueID string) int16 {
	t.Helper()
	var st int16
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM intake_issues WHERE issue_id = $1::uuid`, issueID).Scan(&st); err != nil {
		t.Fatalf("intakeStatusOf: %v", err)
	}
	return st
}

func TestIntakeAutoCreatedWithProject(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)

	creator, wsSlug, identifier := intakeFixture(t, pool, "intake-auto")

	in, err := GetIntake(context.Background(), pool, wsSlug, identifier, creator)
	if err != nil {
		t.Fatalf("GetIntake: %v", err)
	}
	if !in.IsDefault {
		t.Errorf("IsDefault = false, want true")
	}
	if in.Name == "" {
		t.Errorf("default intake has empty name")
	}
}

func TestCreateIssueWithIntakeTrueLandsPending(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)

	creator, wsSlug, identifier := intakeFixture(t, pool, "intake-land")

	iss := createIntakeIssue(t, pool, wsSlug, identifier, creator, "Triage me")
	if got := intakeStatusOf(t, pool, iss.ID); got != IntakePending {
		t.Errorf("status = %d, want pending (%d)", got, IntakePending)
	}

	// The default path stays untouched: no intake row without the flag.
	plain, err := CreateIssue(context.Background(), pool, wsSlug, identifier, creator,
		CreateIssueInput{Name: "Direct"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM intake_issues WHERE issue_id = $1::uuid`, plain.ID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("intake_issues rows for non-intake issue = %d, want 0", n)
	}
}

func TestAcceptIntakeIssueMovesToBacklog(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	creator, wsSlug, identifier := intakeFixture(t, pool, "intake-accept")

	// Create the issue in a non-backlog state so accept has something to do.
	var startedID string
	states, err := ListStates(ctx, pool, wsSlug, identifier, creator)
	if err != nil {
		t.Fatalf("ListStates: %v", err)
	}
	for _, s := range states {
		if s.Group == "started" {
			startedID = s.ID
		}
	}
	if startedID == "" {
		t.Fatal("no started state seeded")
	}
	iss, err := CreateIssue(ctx, pool, wsSlug, identifier, creator,
		CreateIssueInput{Name: "Triage me", StateID: &startedID, Intake: true})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	got, err := AcceptIntakeIssue(ctx, pool, wsSlug, identifier, iss.ID, creator)
	if err != nil {
		t.Fatalf("AcceptIntakeIssue: %v", err)
	}
	if got.Status != IntakeAccepted {
		t.Errorf("status = %d, want accepted (%d)", got.Status, IntakeAccepted)
	}
	backlog := backlogStateID(t, pool, wsSlug, identifier, creator)
	live, err := GetIssue(ctx, pool, wsSlug, identifier, iss.ID, creator)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if live.StateID != backlog {
		t.Errorf("state = %s, want backlog %s", live.StateID, backlog)
	}
	if n := countActivities(t, pool, iss.ID, "_intake_accepted"); n != 1 {
		t.Errorf("_intake_accepted activity rows = %d, want 1", n)
	}
}

func TestAcceptIntakeIssueIdempotent(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	creator, wsSlug, identifier := intakeFixture(t, pool, "intake-accept2")
	iss := createIntakeIssue(t, pool, wsSlug, identifier, creator, "Triage me")

	if _, err := AcceptIntakeIssue(ctx, pool, wsSlug, identifier, iss.ID, creator); err != nil {
		t.Fatalf("first accept: %v", err)
	}
	// Re-accepting an already-accepted intake issue is a no-op success.
	got, err := AcceptIntakeIssue(ctx, pool, wsSlug, identifier, iss.ID, creator)
	if err != nil {
		t.Fatalf("second accept: %v", err)
	}
	if got.Status != IntakeAccepted {
		t.Errorf("status = %d, want accepted", got.Status)
	}
	if n := countActivities(t, pool, iss.ID, "_intake_accepted"); n != 1 {
		t.Errorf("_intake_accepted rows after re-accept = %d, want 1", n)
	}
}

func TestRejectIntakeIssueSoftDeletes(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	creator, wsSlug, identifier := intakeFixture(t, pool, "intake-reject")
	iss := createIntakeIssue(t, pool, wsSlug, identifier, creator, "Spam")

	got, err := RejectIntakeIssue(ctx, pool, wsSlug, identifier, iss.ID, creator)
	if err != nil {
		t.Fatalf("RejectIntakeIssue: %v", err)
	}
	if got.Status != IntakeRejected {
		t.Errorf("status = %d, want rejected (%d)", got.Status, IntakeRejected)
	}
	// The issue itself is soft-deleted: reads report not-found.
	if _, err := GetIssue(ctx, pool, wsSlug, identifier, iss.ID, creator); !errors.Is(err, ErrIssueNotFound) {
		t.Errorf("GetIssue after reject = %v, want ErrIssueNotFound", err)
	}
	if n := countActivities(t, pool, iss.ID, "_intake_rejected"); n != 1 {
		t.Errorf("_intake_rejected rows = %d, want 1", n)
	}
}

func TestSnoozeIntakeIssue(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	creator, wsSlug, identifier := intakeFixture(t, pool, "intake-snooze")
	iss := createIntakeIssue(t, pool, wsSlug, identifier, creator, "Later")

	till := time.Now().Add(48 * time.Hour).UTC()
	got, err := SnoozeIntakeIssue(ctx, pool, wsSlug, identifier, iss.ID, creator, till)
	if err != nil {
		t.Fatalf("SnoozeIntakeIssue: %v", err)
	}
	if got.Status != IntakeSnoozed {
		t.Errorf("status = %d, want snoozed (%d)", got.Status, IntakeSnoozed)
	}
	if got.SnoozedTill == nil || got.SnoozedTill.Before(time.Now()) {
		t.Errorf("snoozed_till not set to the future: %v", got.SnoozedTill)
	}
	if n := countActivities(t, pool, iss.ID, "_intake_snoozed"); n != 1 {
		t.Errorf("_intake_snoozed rows = %d, want 1", n)
	}

	// A past snoozed_till is rejected.
	if _, err := SnoozeIntakeIssue(ctx, pool, wsSlug, identifier, iss.ID, creator,
		time.Now().Add(-time.Hour)); !errors.Is(err, ErrSnoozeDatePast) {
		t.Errorf("snooze in the past = %v, want ErrSnoozeDatePast", err)
	}
}

func TestDuplicateIntakeIssue(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	creator, wsSlug, identifier := intakeFixture(t, pool, "intake-dupe")
	iss := createIntakeIssue(t, pool, wsSlug, identifier, creator, "Dupe report")
	target, err := CreateIssue(ctx, pool, wsSlug, identifier, creator,
		CreateIssueInput{Name: "Original"})
	if err != nil {
		t.Fatalf("CreateIssue target: %v", err)
	}

	got, err := DuplicateIntakeIssue(ctx, pool, wsSlug, identifier, iss.ID, creator, target.ID)
	if err != nil {
		t.Fatalf("DuplicateIntakeIssue: %v", err)
	}
	if got.Status != IntakeDuplicate {
		t.Errorf("status = %d, want duplicate (%d)", got.Status, IntakeDuplicate)
	}
	if got.DuplicateToID == nil || *got.DuplicateToID != target.ID {
		t.Errorf("duplicate_to_id = %v, want %s", got.DuplicateToID, target.ID)
	}
	// The dupe itself is soft-deleted; the target survives.
	if _, err := GetIssue(ctx, pool, wsSlug, identifier, iss.ID, creator); !errors.Is(err, ErrIssueNotFound) {
		t.Errorf("GetIssue after duplicate = %v, want ErrIssueNotFound", err)
	}
	if _, err := GetIssue(ctx, pool, wsSlug, identifier, target.ID, creator); err != nil {
		t.Errorf("target GetIssue = %v, want nil", err)
	}
	if n := countActivities(t, pool, iss.ID, "_intake_duplicate"); n != 1 {
		t.Errorf("_intake_duplicate rows = %d, want 1", n)
	}

	// A target in another project is rejected.
	otherCreator, otherWS, otherIdent := intakeFixture(t, pool, "intake-dupe2")
	otherTarget, err := CreateIssue(ctx, pool, otherWS, otherIdent, otherCreator,
		CreateIssueInput{Name: "Far away"})
	if err != nil {
		t.Fatalf("CreateIssue other: %v", err)
	}
	iss2 := createIntakeIssue(t, pool, wsSlug, identifier, creator, "Another dupe")
	if _, err := DuplicateIntakeIssue(ctx, pool, wsSlug, identifier, iss2.ID, creator, otherTarget.ID); !errors.Is(err, ErrDuplicateTargetInvalid) {
		t.Errorf("cross-project duplicate = %v, want ErrDuplicateTargetInvalid", err)
	}
}

func TestIntakeInboxSurfacing(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	creator, wsSlug, identifier := intakeFixture(t, pool, "intake-inbox")

	pending := createIntakeIssue(t, pool, wsSlug, identifier, creator, "Pending one")
	futureSnoozed := createIntakeIssue(t, pool, wsSlug, identifier, creator, "Snoozed future")
	if _, err := SnoozeIntakeIssue(ctx, pool, wsSlug, identifier, futureSnoozed.ID, creator,
		time.Now().Add(72*time.Hour)); err != nil {
		t.Fatalf("snooze future: %v", err)
	}
	expiredSnoozed := createIntakeIssue(t, pool, wsSlug, identifier, creator, "Snoozed expired")
	// Write an already-expired snooze directly: the service rejects past
	// dates, so the row is seeded by hand to model "time has passed".
	if _, err := pool.Exec(ctx,
		`UPDATE intake_issues SET status = $1, snoozed_till = now() - interval '1 hour'
		 WHERE issue_id = $2::uuid`, IntakeSnoozed, expiredSnoozed.ID); err != nil {
		t.Fatalf("seed expired snooze: %v", err)
	}
	accepted := createIntakeIssue(t, pool, wsSlug, identifier, creator, "Accepted one")
	if _, err := AcceptIntakeIssue(ctx, pool, wsSlug, identifier, accepted.ID, creator); err != nil {
		t.Fatalf("accept: %v", err)
	}

	items, err := ListIntakeIssues(ctx, pool, wsSlug, identifier, creator, 50)
	if err != nil {
		t.Fatalf("ListIntakeIssues: %v", err)
	}
	byID := map[string]*IntakeIssue{}
	for i := range items {
		byID[items[i].IssueID] = &items[i]
	}
	if _, ok := byID[pending.ID]; !ok {
		t.Errorf("pending issue missing from inbox")
	}
	// The expired snooze resurfaces — presented with effective status pending.
	resurfaced, ok := byID[expiredSnoozed.ID]
	if !ok {
		t.Errorf("expired-snooze issue missing from inbox")
	} else if resurfaced.EffectiveStatus() != IntakePending {
		t.Errorf("resurfaced effective status = %d, want pending", resurfaced.EffectiveStatus())
	}
	if _, ok := byID[futureSnoozed.ID]; ok {
		t.Errorf("future-snoozed issue present in inbox, want excluded")
	}
	if _, ok := byID[accepted.ID]; ok {
		t.Errorf("accepted issue present in inbox, want excluded")
	}
}

func TestTriageRequiresMember(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	creator, wsSlug, identifier := intakeFixture(t, pool, "intake-roles")
	guest := createTestUser(t, pool, uniqueTestEmail("intake-guest"))
	addTestMember(t, pool, wsSlug, creator, guest, RoleGuest)
	iss := createIntakeIssue(t, pool, wsSlug, identifier, creator, "Triage me")

	// Guests may read the inbox but not triage.
	if _, err := ListIntakeIssues(ctx, pool, wsSlug, identifier, guest, 50); err != nil {
		t.Errorf("guest ListIntakeIssues = %v, want nil", err)
	}
	if _, err := AcceptIntakeIssue(ctx, pool, wsSlug, identifier, iss.ID, guest); !errors.Is(err, ErrForbidden) {
		t.Errorf("guest accept = %v, want ErrForbidden", err)
	}
	if _, err := RejectIntakeIssue(ctx, pool, wsSlug, identifier, iss.ID, guest); !errors.Is(err, ErrForbidden) {
		t.Errorf("guest reject = %v, want ErrForbidden", err)
	}
}

func TestTriageConflictOnTerminal(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	creator, wsSlug, identifier := intakeFixture(t, pool, "intake-term")
	iss := createIntakeIssue(t, pool, wsSlug, identifier, creator, "Spam")

	if _, err := RejectIntakeIssue(ctx, pool, wsSlug, identifier, iss.ID, creator); err != nil {
		t.Fatalf("reject: %v", err)
	}
	// A different action on a terminal intake row is a conflict, not a
	// silent re-triage.
	if _, err := AcceptIntakeIssue(ctx, pool, wsSlug, identifier, iss.ID, creator); !errors.Is(err, ErrIntakeAlreadyTriaged) {
		t.Errorf("accept after reject = %v, want ErrIntakeAlreadyTriaged", err)
	}
	// Re-rejecting is a no-op success.
	if _, err := RejectIntakeIssue(ctx, pool, wsSlug, identifier, iss.ID, creator); err != nil {
		t.Errorf("second reject = %v, want nil", err)
	}
}

func TestTriageUnknownIssue(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	creator, wsSlug, identifier := intakeFixture(t, pool, "intake-404")

	if _, err := AcceptIntakeIssue(ctx, pool, wsSlug, identifier, "00000000-0000-0000-0000-000000000000", creator); !errors.Is(err, ErrIntakeNotFound) {
		t.Errorf("accept unknown = %v, want ErrIntakeNotFound", err)
	}
	// An issue that exists but never entered intake is not triageable.
	plain, err := CreateIssue(ctx, pool, wsSlug, identifier, creator, CreateIssueInput{Name: "Direct"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if _, err := AcceptIntakeIssue(ctx, pool, wsSlug, identifier, plain.ID, creator); !errors.Is(err, ErrIntakeNotFound) {
		t.Errorf("accept non-intake issue = %v, want ErrIntakeNotFound", err)
	}
}

func TestIntakeBackfillForExistingProjects(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	creator, wsSlug, identifier := intakeFixture(t, pool, "intake-backfill")

	// Simulate a project that predates the intake migration: remove its
	// seeded intake, then re-run the backfill statement.
	in, err := GetIntake(ctx, pool, wsSlug, identifier, creator)
	if err != nil {
		t.Fatalf("GetIntake: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM intake WHERE id = $1::uuid`, in.ID); err != nil {
		t.Fatalf("delete intake: %v", err)
	}
	if _, err := pool.Exec(ctx, intakeBackfillSQL); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if _, err := GetIntake(ctx, pool, wsSlug, identifier, creator); err != nil {
		t.Errorf("GetIntake after backfill = %v, want nil", err)
	}
}
