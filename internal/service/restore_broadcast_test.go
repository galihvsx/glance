package service

// RestoreIssueVersion broadcast (C45T0): a restore with a real diff
// mirrors UpdateIssue exactly — issue.updated unconditionally,
// objective.updated iff state_id actually changed. A failed restore
// (a no-diff restore has been one since C64T0: it is rejected with
// ErrVersionRestoreNoOp before writing anything) announces nothing.
// Only issue.updated / objective.updated events are asserted here.

import (
	"errors"
	"reflect"
	"sort"
	"testing"
)

// issueUpdatedEventIDs returns the sorted payload ids of the
// captured issue.updated events.
func issueUpdatedEventIDs(t *testing.T, fake *capturingBroadcaster) []string {
	t.Helper()
	ids := []string{}
	for _, ev := range fake.snapshot() {
		if ev.eventType != EventIssueUpdated {
			continue
		}
		payload, ok := ev.data.(issueEventData)
		if !ok {
			t.Fatalf("issue event payload type = %T, want issueEventData", ev.data)
		}
		ids = append(ids, payload.ID)
	}
	sort.Strings(ids)
	return ids
}

func assertIssueUpdatedEvents(t *testing.T, fake *capturingBroadcaster, want ...string) {
	t.Helper()
	got := issueUpdatedEventIDs(t, fake)
	wantSorted := append([]string{}, want...)
	sort.Strings(wantSorted)
	if !reflect.DeepEqual(got, wantSorted) {
		t.Fatalf("issue.updated payload ids = %v, want %v", got, wantSorted)
	}
}

func TestRestoreBroadcastStateChange(t *testing.T) {
	fx := setupLinkedIssue(t)

	// v1: {linked, backlog}. Move to done + rename: v2.
	moved := "moved"
	if _, err := UpdateIssue(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, fx.actor,
		IssuePatch{StateID: &fx.doneID, Name: &moved}); err != nil {
		t.Fatalf("UpdateIssue to done: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	restored, err := RestoreIssueVersion(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, fx.actor, 1)
	if err != nil {
		t.Fatalf("RestoreIssueVersion v1: %v", err)
	}
	if restored.StateID != fx.backlogID {
		t.Fatalf("restored StateID = %q, want backlog %q", restored.StateID, fx.backlogID)
	}
	assertIssueUpdatedEvents(t, fake, fx.iss.ID)
	assertLinkedObjectiveEvents(t, fake, fx.obID)
}

func TestRestoreBroadcastTitleOnly(t *testing.T) {
	fx := setupLinkedIssue(t)

	// v1: {linked, backlog}. Rename only: v2, state unchanged.
	renamed := "renamed"
	if _, err := UpdateIssue(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, fx.actor,
		IssuePatch{Name: &renamed}); err != nil {
		t.Fatalf("UpdateIssue rename: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	restored, err := RestoreIssueVersion(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, fx.actor, 1)
	if err != nil {
		t.Fatalf("RestoreIssueVersion v1: %v", err)
	}
	if restored.Name != "linked" {
		t.Fatalf("restored Name = %q, want %q", restored.Name, "linked")
	}
	if restored.StateID != fx.backlogID {
		t.Fatalf("restored StateID = %q, want backlog %q", restored.StateID, fx.backlogID)
	}
	assertIssueUpdatedEvents(t, fake, fx.iss.ID)
	assertLinkedObjectiveEvents(t, fake)
}

func TestRestoreBroadcastNoDiff(t *testing.T) {
	fx := setupLinkedIssue(t)
	fake := installCapturingBroadcaster(t)

	// v1 is the current state: restoring it diffs nothing. C64T0: the
	// no-diff restore is rejected before writing anything, so nothing
	// is announced.
	if _, err := RestoreIssueVersion(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, fx.actor, 1); !errors.Is(err, ErrVersionRestoreNoOp) {
		t.Fatalf("no-diff restore: err = %v, want ErrVersionRestoreNoOp", err)
	}
	assertIssueUpdatedEvents(t, fake)
	assertLinkedObjectiveEvents(t, fake)
}

func TestRestoreBroadcastFailedBadVersion(t *testing.T) {
	fx := setupLinkedIssue(t)
	fake := installCapturingBroadcaster(t)

	if _, err := RestoreIssueVersion(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, fx.actor, 99); !errors.Is(err, ErrVersionNotFound) {
		t.Fatalf("restore v99 err = %v, want ErrVersionNotFound", err)
	}
	assertIssueUpdatedEvents(t, fake)
	assertLinkedObjectiveEvents(t, fake)
}

func TestRestoreBroadcastFailedResurrectedState(t *testing.T) {
	fx := setupLinkedIssue(t)

	// Move the issue to done (v2), then delete the now-unreferenced
	// backlog state. Restoring v1 would resurrect the deleted state.
	if _, err := UpdateIssue(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, fx.actor,
		IssuePatch{StateID: &fx.doneID}); err != nil {
		t.Fatalf("UpdateIssue to done: %v", err)
	}
	if err := DeleteState(fx.ctx, fx.pool, fx.slug, fx.ident, fx.backlogID, fx.actor, nil); err != nil {
		t.Fatalf("DeleteState backlog: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	if _, err := RestoreIssueVersion(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, fx.actor, 1); err == nil {
		t.Fatal("restore resurrecting a deleted state accepted, want an error")
	}
	assertIssueUpdatedEvents(t, fake)
	assertLinkedObjectiveEvents(t, fake)
}
