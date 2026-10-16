package service

// DeleteState reassign broadcast (C45T2): deleting a state with
// reassign_to moves every referencing issue; each moved LIVE issue
// announces issue.updated (post-commit snapshot) + objective.updated
// fan-out for its linked objectives. Soft-deleted issues are moved
// but never announced. Zero refs / failed deletes announce nothing.

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"
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
