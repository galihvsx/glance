package service

// DeleteState reassign broadcast (C45T2): deleting a state with
// reassign_to moves every referencing issue; each moved LIVE issue
// announces issue.updated (post-commit snapshot) + objective.updated
// fan-out for its linked objectives. Soft-deleted issues are moved
// but never announced. Zero refs / failed deletes announce nothing.

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// issueUpdatedPayloads returns the payloads of the captured
// issue.updated events, keyed by issue id.
func issueUpdatedPayloads(t *testing.T, fake *capturingBroadcaster) map[string]issueEventData {
	t.Helper()
	got := map[string]issueEventData{}
	for _, ev := range fake.snapshot() {
		if ev.eventType != EventIssueUpdated {
			continue
		}
		payload, ok := ev.data.(issueEventData)
		if !ok {
			t.Fatalf("issue.updated payload type = %T, want issueEventData", ev.data)
		}
		got[payload.ID] = payload
	}
	return got
}

func assertIssueUpdatedIDs(t *testing.T, fake *capturingBroadcaster, want ...string) {
	t.Helper()
	got := []string{}
	for id := range issueUpdatedPayloads(t, fake) {
		got = append(got, id)
	}
	sort.Strings(got)
	wantSorted := append([]string{}, want...)
	sort.Strings(wantSorted)
	if !reflect.DeepEqual(got, wantSorted) {
		t.Fatalf("issue.updated payload ids = %v, want %v", got, wantSorted)
	}
}

// TestDeleteStateReassignBroadcastsMovedLiveIssues: N live referencing
// issues + reassign emits N issue.updated events (with the post-commit
// state_id) + objective fan-out for each linked moved issue; a
// soft-deleted issue moved by the same UPDATE emits nothing but is
// still reassigned.
func TestDeleteStateReassignBroadcastsMovedLiveIssues(t *testing.T) {
	fx := setupLinkedIssue(t)
	ctx, pool := fx.ctx, fx.pool

	// Second live issue in the doomed state, linked to a second
	// objective: its fan-out must fire independently.
	other, err := CreateIssue(ctx, pool, fx.slug, fx.ident, fx.actor,
		CreateIssueInput{Name: "other live", StateID: &fx.backlogID})
	if err != nil {
		t.Fatalf("CreateIssue other: %v", err)
	}
	ob2, err := CreateObjective(ctx, pool, fx.slug, fx.actor, ObjectiveInput{Title: "Grow"})
	if err != nil {
		t.Fatalf("CreateObjective 2: %v", err)
	}
	kr2, err := CreateKeyResult(ctx, pool, fx.slug, fx.actor, ob2.ID, KeyResultInput{
		Title: "Grow issues", Kind: "issue", TargetValue: fptr(50),
	})
	if err != nil {
		t.Fatalf("CreateKeyResult 2: %v", err)
	}
	if err := LinkIssueToKeyResult(ctx, pool, fx.slug, fx.actor, ob2.ID, kr2.ID, other.ID); err != nil {
		t.Fatalf("link other: %v", err)
	}
	// Soft-deleted issue in the doomed state: moved, never announced.
	ghost, err := CreateIssue(ctx, pool, fx.slug, fx.ident, fx.actor,
		CreateIssueInput{Name: "ghost", StateID: &fx.backlogID})
	if err != nil {
		t.Fatalf("CreateIssue ghost: %v", err)
	}

	doomed, err := CreateState(ctx, pool, fx.slug, fx.ident, fx.actor,
		StateInput{Name: "Doomed", Group: "backlog"})
	if err != nil {
		t.Fatalf("CreateState doomed: %v", err)
	}
	// Park all three issues in the doomed state; soft-delete the ghost.
	if _, err := pool.Exec(ctx,
		`UPDATE issues SET state_id = $1::uuid WHERE id = ANY($2::uuid[])`,
		doomed.ID, []string{fx.iss.ID, other.ID, ghost.ID}); err != nil {
		t.Fatalf("move issues: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE issues SET deleted_at = now() WHERE id = $1::uuid`, ghost.ID); err != nil {
		t.Fatalf("soft-delete ghost: %v", err)
	}

	fake := installCapturingBroadcaster(t)
	if err := DeleteState(ctx, pool, fx.slug, fx.ident, doomed.ID, fx.actor, &fx.doneID); err != nil {
		t.Fatalf("DeleteState with reassign: %v", err)
	}

	// Exactly the two live moved issues announce issue.updated, with
	// the post-commit (reassigned) state_id in the payload.
	assertIssueUpdatedIDs(t, fake, fx.iss.ID, other.ID)
	for id, payload := range issueUpdatedPayloads(t, fake) {
		if payload.StateID != fx.doneID {
			t.Fatalf("issue.updated[%s].state_id = %s, want reassigned %s", id, payload.StateID, fx.doneID)
		}
	}
	// Each linked moved issue fans out its own objective.
	assertLinkedObjectiveEvents(t, fake, fx.obID, ob2.ID)

	// The soft-deleted issue was still moved by the bulk UPDATE.
	var ghostState string
	if err := pool.QueryRow(ctx,
		`SELECT state_id::text FROM issues WHERE id = $1::uuid`, ghost.ID).Scan(&ghostState); err != nil {
		t.Fatalf("refetch ghost: %v", err)
	}
	if ghostState != fx.doneID {
		t.Fatalf("soft-deleted issue state = %s, want reassigned %s", ghostState, fx.doneID)
	}
}

// TestDeleteStateZeroRefsBroadcastsNothing: deleting a state no issue
// references emits nothing.
func TestDeleteStateZeroRefsBroadcastsNothing(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	slug, ident, creator := setupStateProject(t, pool)

	empty, err := CreateState(ctx, pool, slug, ident, creator,
		StateInput{Name: "Empty", Group: "backlog"})
	if err != nil {
		t.Fatalf("CreateState: %v", err)
	}
	fake := installCapturingBroadcaster(t)
	if err := DeleteState(ctx, pool, slug, ident, empty.ID, creator, nil); err != nil {
		t.Fatalf("DeleteState zero refs: %v", err)
	}
	assertZeroEvents(t, fake)
}

// TestDeleteStateFailedDeleteBroadcastsNothing: every failure path —
// in-use without reassign (409), invalid reassign target, last state,
// unknown state — announces nothing.
func TestDeleteStateFailedDeleteBroadcastsNothing(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	slug, ident, creator := setupStateProject(t, pool)

	doomed, err := CreateState(ctx, pool, slug, ident, creator,
		StateInput{Name: "Doomed", Group: "backlog"})
	if err != nil {
		t.Fatalf("CreateState: %v", err)
	}
	iss := createTestIssue(t, pool, slug, ident, creator, "doomed issue")
	if _, err := pool.Exec(ctx,
		`UPDATE issues SET state_id = $1::uuid WHERE id = $2::uuid`, doomed.ID, iss.ID); err != nil {
		t.Fatalf("move issue: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	// In-use without reassign → 409, nothing announced.
	if err := DeleteState(ctx, pool, slug, ident, doomed.ID, creator, nil); !errors.Is(err, ErrStateInUse) {
		t.Fatalf("in-use: err = %v, want ErrStateInUse", err)
	}
	// Invalid reassign target → 400-class, nothing announced.
	bogus := "00000000-0000-0000-0000-000000000000"
	if err := DeleteState(ctx, pool, slug, ident, doomed.ID, creator, &bogus); !errors.Is(err, ErrInvalidReassign) {
		t.Fatalf("bogus reassign: err = %v, want ErrInvalidReassign", err)
	}
	// Self reassign → 400-class, nothing announced.
	if err := DeleteState(ctx, pool, slug, ident, doomed.ID, creator, &doomed.ID); !errors.Is(err, ErrInvalidReassign) {
		t.Fatalf("self reassign: err = %v, want ErrInvalidReassign", err)
	}
	// Unknown state → 404-class, nothing announced.
	if err := DeleteState(ctx, pool, slug, ident, bogus, creator, nil); !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("unknown: err = %v, want ErrStateNotFound", err)
	}
	// Last state: delete every other state (zero refs), then the last
	// delete fails → nothing announced for any of it.
	states, err := ListStates(ctx, pool, slug, ident, creator)
	if err != nil {
		t.Fatalf("ListStates: %v", err)
	}
	for _, s := range states {
		if s.ID == doomed.ID {
			continue
		}
		if err := DeleteState(ctx, pool, slug, ident, s.ID, creator, nil); err != nil {
			t.Fatalf("DeleteState %q: %v", s.Name, err)
		}
	}
	if err := DeleteState(ctx, pool, slug, ident, doomed.ID, creator, nil); !errors.Is(err, ErrLastState) {
		t.Fatalf("last state: err = %v, want ErrLastState", err)
	}
	assertZeroEvents(t, fake)
}

// stateIDActivityPairs returns every state_id activity row for an issue
// as (old, new) state-id pairs, in insertion order.
func stateIDActivityPairs(t *testing.T, pool *pgxpool.Pool, issueID string) [][2]string {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT old_value, new_value FROM issue_activities
		  WHERE issue_id = $1::uuid AND field = 'state_id' ORDER BY created_at, id`,
		issueID)
	if err != nil {
		t.Fatalf("read state_id activity: %v", err)
	}
	defer rows.Close()
	var pairs [][2]string
	for rows.Next() {
		var oldRaw, newRaw []byte
		if err := rows.Scan(&oldRaw, &newRaw); err != nil {
			t.Fatalf("scan state_id activity: %v", err)
		}
		var o, n string
		if err := json.Unmarshal(oldRaw, &o); err != nil {
			t.Fatalf("unmarshal old state_id: %v", err)
		}
		if err := json.Unmarshal(newRaw, &n); err != nil {
			t.Fatalf("unmarshal new state_id: %v", err)
		}
		pairs = append(pairs, [2]string{o, n})
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("state_id activity rows: %v", err)
	}
	return pairs
}

// TestDeleteStateReassignWritesStateIDActivity (C47T0): deleting a state
// with reassign_to writes one state_id activity row per moved LIVE issue
// (old = deleted state, new = reassign target) and zero rows for
// soft-deleted issues referencing the deleted state.
func TestDeleteStateReassignWritesStateIDActivity(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	slug, ident, creator := setupStateProject(t, pool)

	doomed, err := CreateState(ctx, pool, slug, ident, creator,
		StateInput{Name: "Doomed", Group: "backlog"})
	if err != nil {
		t.Fatalf("CreateState doomed: %v", err)
	}
	target, err := CreateState(ctx, pool, slug, ident, creator,
		StateInput{Name: "Safe", Group: "completed"})
	if err != nil {
		t.Fatalf("CreateState target: %v", err)
	}
	live1 := createTestIssue(t, pool, slug, ident, creator, "live one")
	live2 := createTestIssue(t, pool, slug, ident, creator, "live two")
	ghost := createTestIssue(t, pool, slug, ident, creator, "ghost")
	// Park all three in the doomed state via direct SQL (no activity
	// rows), then soft-delete the ghost.
	if _, err := pool.Exec(ctx,
		`UPDATE issues SET state_id = $1::uuid WHERE id = ANY($2::uuid[])`,
		doomed.ID, []string{live1.ID, live2.ID, ghost.ID}); err != nil {
		t.Fatalf("park issues: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE issues SET deleted_at = now() WHERE id = $1::uuid`, ghost.ID); err != nil {
		t.Fatalf("soft-delete ghost: %v", err)
	}

	if err := DeleteState(ctx, pool, slug, ident, doomed.ID, creator, &target.ID); err != nil {
		t.Fatalf("DeleteState with reassign: %v", err)
	}
	want := [2]string{doomed.ID, target.ID}
	for _, iss := range []string{live1.ID, live2.ID} {
		pairs := stateIDActivityPairs(t, pool, iss)
		if len(pairs) != 1 || pairs[0] != want {
			t.Fatalf("state_id activity for %s = %v, want [%v]", iss, pairs, want)
		}
	}
	if pairs := stateIDActivityPairs(t, pool, ghost.ID); len(pairs) != 0 {
		t.Fatalf("state_id activity for soft-deleted issue = %v, want none", pairs)
	}
}

// TestDeleteStateNoRefsWritesNoStateIDActivity (C47T0): deleting a state
// no issue references writes no state_id rows.
func TestDeleteStateNoRefsWritesNoStateIDActivity(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	slug, ident, creator := setupStateProject(t, pool)

	iss := createTestIssue(t, pool, slug, ident, creator, "bystander")
	empty, err := CreateState(ctx, pool, slug, ident, creator,
		StateInput{Name: "Empty", Group: "backlog"})
	if err != nil {
		t.Fatalf("CreateState: %v", err)
	}
	if err := DeleteState(ctx, pool, slug, ident, empty.ID, creator, nil); err != nil {
		t.Fatalf("DeleteState zero refs: %v", err)
	}
	if pairs := stateIDActivityPairs(t, pool, iss.ID); len(pairs) != 0 {
		t.Fatalf("state_id activity after zero-ref delete = %v, want none", pairs)
	}
}

// TestDeleteStateFailedDeleteWritesNoStateIDActivity (C47T0): a failed
// DeleteState (in-use without reassign → 409, tx rollback) writes no
// state_id rows and leaves the issue in the doomed state.
func TestDeleteStateFailedDeleteWritesNoStateIDActivity(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	slug, ident, creator := setupStateProject(t, pool)

	doomed, err := CreateState(ctx, pool, slug, ident, creator,
		StateInput{Name: "Doomed", Group: "backlog"})
	if err != nil {
		t.Fatalf("CreateState doomed: %v", err)
	}
	iss := createTestIssue(t, pool, slug, ident, creator, "doomed issue")
	if _, err := pool.Exec(ctx,
		`UPDATE issues SET state_id = $1::uuid WHERE id = $2::uuid`, doomed.ID, iss.ID); err != nil {
		t.Fatalf("park issue: %v", err)
	}
	if err := DeleteState(ctx, pool, slug, ident, doomed.ID, creator, nil); !errors.Is(err, ErrStateInUse) {
		t.Fatalf("in-use without reassign: err = %v, want ErrStateInUse", err)
	}
	if pairs := stateIDActivityPairs(t, pool, iss.ID); len(pairs) != 0 {
		t.Fatalf("state_id activity after failed delete = %v, want none", pairs)
	}
	var stateID string
	if err := pool.QueryRow(ctx,
		`SELECT state_id::text FROM issues WHERE id = $1::uuid`, iss.ID).Scan(&stateID); err != nil {
		t.Fatalf("refetch issue: %v", err)
	}
	if stateID != doomed.ID {
		t.Fatalf("issue state = %s, want still doomed %s", stateID, doomed.ID)
	}
}

// countIssueVersions returns the number of version rows for an issue,
// via direct SQL so it also works for soft-deleted issues (ListVersions
// rejects those: its resolver filters deleted_at IS NULL).
func countIssueVersions(t *testing.T, pool *pgxpool.Pool, issueID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM issue_versions WHERE issue_id = $1::uuid`, issueID).Scan(&n); err != nil {
		t.Fatalf("count versions: %v", err)
	}
	return n
}

// latestVersionSnapshotByID decodes the newest version snapshot of an issue
// plus its author, via direct SQL (works for soft-deleted issues too).
func latestVersionSnapshotByID(t *testing.T, pool *pgxpool.Pool, issueID string) (Issue, string) {
	t.Helper()
	var snapRaw []byte
	var author string
	if err := pool.QueryRow(context.Background(),
		`SELECT snapshot, created_by::text FROM issue_versions
		  WHERE issue_id = $1::uuid ORDER BY version_no DESC LIMIT 1`, issueID).Scan(&snapRaw, &author); err != nil {
		t.Fatalf("latest version: %v", err)
	}
	var snap Issue
	if err := json.Unmarshal(snapRaw, &snap); err != nil {
		t.Fatalf("decode latest snapshot: %v", err)
	}
	return snap, author
}

// TestDeleteStateReassignWritesVersionPerMovedLiveIssue (C51T0):
// deleting a state with reassign_to appends exactly ONE new version per
// moved LIVE issue, carrying the NEW StateID and authored by the deleter;
// a soft-deleted issue moved by the same UPDATE gains NO version.
func TestDeleteStateReassignWritesVersionPerMovedLiveIssue(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	slug, ident, creator := setupStateProject(t, pool)

	doomed, err := CreateState(ctx, pool, slug, ident, creator,
		StateInput{Name: "Doomed", Group: "backlog"})
	if err != nil {
		t.Fatalf("CreateState doomed: %v", err)
	}
	target, err := CreateState(ctx, pool, slug, ident, creator,
		StateInput{Name: "Safe", Group: "completed"})
	if err != nil {
		t.Fatalf("CreateState target: %v", err)
	}
	live1 := createTestIssue(t, pool, slug, ident, creator, "live one")
	live2 := createTestIssue(t, pool, slug, ident, creator, "live two")
	ghost := createTestIssue(t, pool, slug, ident, creator, "ghost")
	// Park all three in the doomed state via direct SQL (no versions),
	// then soft-delete the ghost.
	if _, err := pool.Exec(ctx,
		`UPDATE issues SET state_id = $1::uuid WHERE id = ANY($2::uuid[])`,
		doomed.ID, []string{live1.ID, live2.ID, ghost.ID}); err != nil {
		t.Fatalf("park issues: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE issues SET deleted_at = now() WHERE id = $1::uuid`, ghost.ID); err != nil {
		t.Fatalf("soft-delete ghost: %v", err)
	}
	before := map[string]int{
		live1.ID: countIssueVersions(t, pool, live1.ID),
		live2.ID: countIssueVersions(t, pool, live2.ID),
		ghost.ID: countIssueVersions(t, pool, ghost.ID),
	}

	if err := DeleteState(ctx, pool, slug, ident, doomed.ID, creator, &target.ID); err != nil {
		t.Fatalf("DeleteState with reassign: %v", err)
	}
	for _, iss := range []string{live1.ID, live2.ID} {
		if got := countIssueVersions(t, pool, iss); got != before[iss]+1 {
			t.Fatalf("versions for %s = %d, want baseline %d + 1", iss, got, before[iss])
		}
		snap, author := latestVersionSnapshotByID(t, pool, iss)
		if snap.StateID != target.ID {
			t.Fatalf("latest snapshot StateID for %s = %s, want reassigned %s", iss, snap.StateID, target.ID)
		}
		if author != creator {
			t.Fatalf("latest snapshot author for %s = %s, want deleter %s", iss, author, creator)
		}
	}
	if got := countIssueVersions(t, pool, ghost.ID); got != before[ghost.ID] {
		t.Fatalf("versions for soft-deleted issue = %d, want baseline %d (no version)", got, before[ghost.ID])
	}
}

// TestDeleteStateZeroRefsWritesNoVersions (C51T0): deleting a state no
// issue references writes no versions anywhere.
func TestDeleteStateZeroRefsWritesNoVersions(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	slug, ident, creator := setupStateProject(t, pool)

	bystander := createTestIssue(t, pool, slug, ident, creator, "bystander")
	before := countIssueVersions(t, pool, bystander.ID)
	empty, err := CreateState(ctx, pool, slug, ident, creator,
		StateInput{Name: "Empty", Group: "backlog"})
	if err != nil {
		t.Fatalf("CreateState: %v", err)
	}
	if err := DeleteState(ctx, pool, slug, ident, empty.ID, creator, nil); err != nil {
		t.Fatalf("DeleteState zero refs: %v", err)
	}
	if got := countIssueVersions(t, pool, bystander.ID); got != before {
		t.Fatalf("versions after zero-ref delete = %d, want baseline %d", got, before)
	}
}

// TestDeleteStateFailedDeleteWritesNoVersions (C51T0): a failed
// DeleteState — in-use without reassign (409), invalid reassign target,
// last state — writes no versions and leaves the issue in the doomed
// state (tx rollback).
func TestDeleteStateFailedDeleteWritesNoVersions(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	slug, ident, creator := setupStateProject(t, pool)

	doomed, err := CreateState(ctx, pool, slug, ident, creator,
		StateInput{Name: "Doomed", Group: "backlog"})
	if err != nil {
		t.Fatalf("CreateState doomed: %v", err)
	}
	iss := createTestIssue(t, pool, slug, ident, creator, "doomed issue")
	if _, err := pool.Exec(ctx,
		`UPDATE issues SET state_id = $1::uuid WHERE id = $2::uuid`, doomed.ID, iss.ID); err != nil {
		t.Fatalf("park issue: %v", err)
	}
	before := countIssueVersions(t, pool, iss.ID)

	if err := DeleteState(ctx, pool, slug, ident, doomed.ID, creator, nil); !errors.Is(err, ErrStateInUse) {
		t.Fatalf("in-use without reassign: err = %v, want ErrStateInUse", err)
	}
	bogus := "00000000-0000-0000-0000-000000000000"
	if err := DeleteState(ctx, pool, slug, ident, doomed.ID, creator, &bogus); !errors.Is(err, ErrInvalidReassign) {
		t.Fatalf("bogus reassign: err = %v, want ErrInvalidReassign", err)
	}
	if got := countIssueVersions(t, pool, iss.ID); got != before {
		t.Fatalf("versions after failed deletes = %d, want baseline %d", got, before)
	}
	var stateID string
	if err := pool.QueryRow(ctx,
		`SELECT state_id::text FROM issues WHERE id = $1::uuid`, iss.ID).Scan(&stateID); err != nil {
		t.Fatalf("refetch issue: %v", err)
	}
	if stateID != doomed.ID {
		t.Fatalf("issue state = %s, want still doomed %s", stateID, doomed.ID)
	}
}
