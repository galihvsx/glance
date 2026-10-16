package service

// Linked-issue OKR broadcast (C44T1): a linked issue's progress-relevant
// mutation fans out objective.updated for its linked objectives, once
// per objective. Only objective.updated events are asserted here —
// the mutations also emit issue.updated / issue.deleted, which the
// filter below ignores.

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// linkedObjectiveEventIDs returns the sorted payload ids of the
// captured objective.updated events.
func linkedObjectiveEventIDs(t *testing.T, fake *capturingBroadcaster) []string {
	t.Helper()
	ids := []string{}
	for _, ev := range fake.snapshot() {
		if ev.eventType != EventObjectiveUpdated {
			continue
		}
		payload, ok := ev.data.(map[string]string)
		if !ok {
			t.Fatalf("objective event payload type = %T, want map[string]string", ev.data)
		}
		ids = append(ids, payload["id"])
	}
	sort.Strings(ids)
	return ids
}

func assertLinkedObjectiveEvents(t *testing.T, fake *capturingBroadcaster, want ...string) {
	t.Helper()
	got := linkedObjectiveEventIDs(t, fake)
	wantSorted := append([]string{}, want...)
	sort.Strings(wantSorted)
	if !reflect.DeepEqual(got, wantSorted) {
		t.Fatalf("objective.updated payload ids = %v, want %v", got, wantSorted)
	}
}

// linkedIssueFixture is one workspace + project + linked issue +
// objective + issue KR, ready for a mutation. The capturing
// broadcaster is installed AFTER setup in each test, so setup
// announces (objective create, KR create, link) are not captured.
type linkedIssueFixture struct {
	ctx       context.Context
	pool      *pgxpool.Pool
	slug      string
	ident     string
	actor     string
	backlogID string
	doneID    string
	obID      string
	krID      string
	iss       *Issue
}

func setupLinkedIssue(t *testing.T) *linkedIssueFixture {
	t.Helper()
	ctx, pool, slug, actor := setupOKRTest(t)
	ident := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Alpha", ident)
	backlogID := backlogStateID(t, pool, slug, ident, actor)
	done, err := CreateState(ctx, pool, slug, ident, actor, StateInput{Name: "Finished", Group: "completed", Color: "green"})
	if err != nil {
		t.Fatalf("CreateState done: %v", err)
	}
	iss, err := CreateIssue(ctx, pool, slug, ident, actor, CreateIssueInput{Name: "linked", StateID: &backlogID})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	ob, err := CreateObjective(ctx, pool, slug, actor, ObjectiveInput{Title: "Ship"})
	if err != nil {
		t.Fatalf("CreateObjective: %v", err)
	}
	kr, err := CreateKeyResult(ctx, pool, slug, actor, ob.ID, KeyResultInput{
		Title: "Ship issues", Kind: "issue", TargetValue: fptr(100),
	})
	if err != nil {
		t.Fatalf("CreateKeyResult: %v", err)
	}
	if err := LinkIssueToKeyResult(ctx, pool, slug, actor, ob.ID, kr.ID, iss.ID); err != nil {
		t.Fatalf("link: %v", err)
	}
	return &linkedIssueFixture{
		ctx: ctx, pool: pool, slug: slug, ident: ident, actor: actor,
		backlogID: backlogID, doneID: done.ID, obID: ob.ID, krID: kr.ID, iss: iss,
	}
}

func TestObjectiveBroadcastLinkedIssueStateChange(t *testing.T) {
	fx := setupLinkedIssue(t)
	fake := installCapturingBroadcaster(t)

	if _, err := UpdateIssue(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, fx.actor,
		IssuePatch{StateID: &fx.doneID}); err != nil {
		t.Fatalf("UpdateIssue state change: %v", err)
	}
	assertLinkedObjectiveEvents(t, fake, fx.obID)
}

func TestObjectiveBroadcastLinkedIssueTitleOnly(t *testing.T) {
	fx := setupLinkedIssue(t)
	fake := installCapturingBroadcaster(t)

	if _, err := UpdateIssue(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, fx.actor,
		IssuePatch{Name: strptr("linked v2")}); err != nil {
		t.Fatalf("UpdateIssue title-only: %v", err)
	}
	assertLinkedObjectiveEvents(t, fake)
}

func TestObjectiveBroadcastLinkedIssueSameState(t *testing.T) {
	fx := setupLinkedIssue(t)
	fake := installCapturingBroadcaster(t)

	// Patch names the state the issue is already in: no change, no event.
	if _, err := UpdateIssue(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, fx.actor,
		IssuePatch{StateID: &fx.backlogID}); err != nil {
		t.Fatalf("UpdateIssue same state: %v", err)
	}
	assertLinkedObjectiveEvents(t, fake)
}

func TestObjectiveBroadcastLinkedIssueUnlinked(t *testing.T) {
	fx := setupLinkedIssue(t)
	other, err := CreateIssue(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		CreateIssueInput{Name: "unlinked", StateID: &fx.backlogID})
	if err != nil {
		t.Fatalf("CreateIssue unlinked: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	if _, err := UpdateIssue(fx.ctx, fx.pool, fx.slug, fx.ident, other.ID, fx.actor,
		IssuePatch{StateID: &fx.doneID}); err != nil {
		t.Fatalf("UpdateIssue unlinked state change: %v", err)
	}
	assertLinkedObjectiveEvents(t, fake)
}

func TestObjectiveBroadcastLinkedIssueDelete(t *testing.T) {
	fx := setupLinkedIssue(t)
	fake := installCapturingBroadcaster(t)

	if err := DeleteIssue(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, fx.actor); err != nil {
		t.Fatalf("DeleteIssue: %v", err)
	}
	assertLinkedObjectiveEvents(t, fake, fx.obID)
}

func TestObjectiveBroadcastLinkedIssueBulkDelete(t *testing.T) {
	fx := setupLinkedIssue(t)
	second, err := CreateIssue(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		CreateIssueInput{Name: "linked 2", StateID: &fx.backlogID})
	if err != nil {
		t.Fatalf("CreateIssue second: %v", err)
	}
	if err := LinkIssueToKeyResult(fx.ctx, fx.pool, fx.slug, fx.actor, fx.obID, fx.krID, second.ID); err != nil {
		t.Fatalf("link second: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	results, err := BulkDeleteIssues(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor, []string{fx.iss.ID, second.ID})
	if err != nil {
		t.Fatalf("BulkDeleteIssues: %v", err)
	}
	for _, r := range results {
		if !r.OK {
			t.Fatalf("bulk delete item %s not OK: %v", r.ID, r.Error)
		}
	}
	// Two deleted issues, one shared objective: exactly one event.
	assertLinkedObjectiveEvents(t, fake, fx.obID)
}

func TestObjectiveBroadcastLinkedIssueBulkUpdate(t *testing.T) {
	fx := setupLinkedIssue(t)
	second, err := CreateIssue(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		CreateIssueInput{Name: "linked 2", StateID: &fx.backlogID})
	if err != nil {
		t.Fatalf("CreateIssue second: %v", err)
	}
	if err := LinkIssueToKeyResult(fx.ctx, fx.pool, fx.slug, fx.actor, fx.obID, fx.krID, second.ID); err != nil {
		t.Fatalf("link second: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	if _, err := BulkUpdateIssues(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		[]string{fx.iss.ID, second.ID}, IssuePatch{StateID: &fx.doneID}); err != nil {
		t.Fatalf("BulkUpdateIssues state change: %v", err)
	}
	// Two state-changed issues, one shared objective: exactly one event.
	assertLinkedObjectiveEvents(t, fake, fx.obID)

	// Bulk title-only edit: no further event.
	if _, err := BulkUpdateIssues(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		[]string{fx.iss.ID, second.ID}, IssuePatch{Name: strptr("renamed")}); err != nil {
		t.Fatalf("BulkUpdateIssues title-only: %v", err)
	}
	assertLinkedObjectiveEvents(t, fake, fx.obID)
}

func TestObjectiveBroadcastLinkedIssueBulkSet(t *testing.T) {
	fx := setupLinkedIssue(t)
	fake := installCapturingBroadcaster(t)

	if _, _, err := BulkSetIssues(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		[]string{fx.iss.ID}, BulkIssueSet{StateID: &fx.doneID}); err != nil {
		t.Fatalf("BulkSetIssues state change: %v", err)
	}
	assertLinkedObjectiveEvents(t, fake, fx.obID)

	// Priority-only set: no further event.
	if _, _, err := BulkSetIssues(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		[]string{fx.iss.ID}, BulkIssueSet{Priority: autoIntPtr(2)}); err != nil {
		t.Fatalf("BulkSetIssues priority-only: %v", err)
	}
	assertLinkedObjectiveEvents(t, fake, fx.obID)
}

func TestObjectiveBroadcastLinkedIssueMerge(t *testing.T) {
	fx := setupLinkedIssue(t)
	target, err := CreateIssue(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		CreateIssueInput{Name: "survivor", StateID: &fx.backlogID})
	if err != nil {
		t.Fatalf("CreateIssue target: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	res, err := MergeIssue(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, fx.actor, target.ID)
	if err != nil {
		t.Fatalf("MergeIssue: %v", err)
	}
	if !res.Merged {
		t.Fatalf("Merged = false, want true")
	}
	// Source closed into cancelled: its linked objective fans out once.
	assertLinkedObjectiveEvents(t, fake, fx.obID)
}

func TestObjectiveBroadcastLinkedIssueMergeUnlinked(t *testing.T) {
	fx := setupLinkedIssue(t)
	source, err := CreateIssue(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		CreateIssueInput{Name: "unlinked source", StateID: &fx.backlogID})
	if err != nil {
		t.Fatalf("CreateIssue source: %v", err)
	}
	target, err := CreateIssue(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		CreateIssueInput{Name: "survivor", StateID: &fx.backlogID})
	if err != nil {
		t.Fatalf("CreateIssue target: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	if _, err := MergeIssue(fx.ctx, fx.pool, fx.slug, fx.ident, source.ID, fx.actor, target.ID); err != nil {
		t.Fatalf("MergeIssue unlinked: %v", err)
	}
	assertLinkedObjectiveEvents(t, fake)
}

func TestObjectiveBroadcastLinkedIssueMergeAlreadyCancelled(t *testing.T) {
	fx := setupLinkedIssue(t)
	states, err := ListStates(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor)
	if err != nil {
		t.Fatalf("ListStates: %v", err)
	}
	var cancelledID string
	for _, s := range states {
		if s.Group == "cancelled" {
			cancelledID = s.ID
		}
	}
	if cancelledID == "" {
		t.Fatal("no cancelled state seeded")
	}
	// Park the linked source in the cancelled state first: its KR
	// progress already reflects that.
	if _, err := UpdateIssue(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, fx.actor,
		IssuePatch{StateID: &cancelledID}); err != nil {
		t.Fatalf("UpdateIssue to cancelled: %v", err)
	}
	target, err := CreateIssue(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		CreateIssueInput{Name: "survivor", StateID: &fx.backlogID})
	if err != nil {
		t.Fatalf("CreateIssue target: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	if _, err := MergeIssue(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, fx.actor, target.ID); err != nil {
		t.Fatalf("MergeIssue already-cancelled: %v", err)
	}
	// Merge moved no state: no event.
	assertLinkedObjectiveEvents(t, fake)
}

func TestObjectiveBroadcastLinkedIssueMove(t *testing.T) {
	fx := setupLinkedIssue(t)
	dstIdent := uniqueTestIdentifier()
	createTestProject(t, fx.pool, fx.slug, fx.actor, "Beta", dstIdent)
	// Put the linked issue in the done state: the target project has
	// no "Finished" state, so the move falls back to its backlog
	// state — a group change, hence a progress change.
	if _, err := UpdateIssue(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, fx.actor,
		IssuePatch{StateID: &fx.doneID}); err != nil {
		t.Fatalf("UpdateIssue to done: %v", err)
	}
	unlinked, err := CreateIssue(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		CreateIssueInput{Name: "unlinked", StateID: &fx.backlogID})
	if err != nil {
		t.Fatalf("CreateIssue unlinked: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	if _, err := MoveIssue(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, fx.actor,
		MoveIssueInput{TargetProjectIdentifier: dstIdent}); err != nil {
		t.Fatalf("MoveIssue linked: %v", err)
	}
	assertLinkedObjectiveEvents(t, fake, fx.obID)

	if _, err := MoveIssue(fx.ctx, fx.pool, fx.slug, fx.ident, unlinked.ID, fx.actor,
		MoveIssueInput{TargetProjectIdentifier: dstIdent}); err != nil {
		t.Fatalf("MoveIssue unlinked: %v", err)
	}
	assertLinkedObjectiveEvents(t, fake, fx.obID)
}

func TestObjectiveBroadcastLinkedIssueTwoObjectives(t *testing.T) {
	fx := setupLinkedIssue(t)
	ob2, err := CreateObjective(fx.ctx, fx.pool, fx.slug, fx.actor, ObjectiveInput{Title: "Grow"})
	if err != nil {
		t.Fatalf("CreateObjective 2: %v", err)
	}
	kr2, err := CreateKeyResult(fx.ctx, fx.pool, fx.slug, fx.actor, ob2.ID, KeyResultInput{
		Title: "Grow issues", Kind: "issue", TargetValue: fptr(50),
	})
	if err != nil {
		t.Fatalf("CreateKeyResult 2: %v", err)
	}
	if err := LinkIssueToKeyResult(fx.ctx, fx.pool, fx.slug, fx.actor, ob2.ID, kr2.ID, fx.iss.ID); err != nil {
		t.Fatalf("link to objective 2: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	if _, err := UpdateIssue(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, fx.actor,
		IssuePatch{StateID: &fx.doneID}); err != nil {
		t.Fatalf("UpdateIssue state change: %v", err)
	}
	// One event per objective, no duplicates.
	assertLinkedObjectiveEvents(t, fake, fx.obID, ob2.ID)
}

func TestObjectiveBroadcastLinkedIssueSameObjectiveTwoKRs(t *testing.T) {
	fx := setupLinkedIssue(t)
	kr2, err := CreateKeyResult(fx.ctx, fx.pool, fx.slug, fx.actor, fx.obID, KeyResultInput{
		Title: "Ship issues 2", Kind: "issue", TargetValue: fptr(50),
	})
	if err != nil {
		t.Fatalf("CreateKeyResult 2: %v", err)
	}
	if err := LinkIssueToKeyResult(fx.ctx, fx.pool, fx.slug, fx.actor, fx.obID, kr2.ID, fx.iss.ID); err != nil {
		t.Fatalf("link to second KR: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	if _, err := UpdateIssue(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, fx.actor,
		IssuePatch{StateID: &fx.doneID}); err != nil {
		t.Fatalf("UpdateIssue state change: %v", err)
	}
	// Same issue, two KRs, one objective: DISTINCT collapses to one event.
	assertLinkedObjectiveEvents(t, fake, fx.obID)
}

func TestObjectiveBroadcastLinkedIssueFailedMutation(t *testing.T) {
	fx := setupLinkedIssue(t)
	fake := installCapturingBroadcaster(t)

	// Unknown state: validation fails before any mutation.
	if _, err := UpdateIssue(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, fx.actor,
		IssuePatch{StateID: strptr("00000000-0000-0000-0000-000000000000")}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("bad state err = %v, want ErrInvalidState", err)
	}
	// Missing issue: nothing mutated.
	if _, err := UpdateIssue(fx.ctx, fx.pool, fx.slug, fx.ident, "00000000-0000-0000-0000-000000000000", fx.actor,
		IssuePatch{StateID: &fx.doneID}); !errors.Is(err, ErrIssueNotFound) {
		t.Fatalf("missing issue err = %v, want ErrIssueNotFound", err)
	}
	if err := DeleteIssue(fx.ctx, fx.pool, fx.slug, fx.ident, "00000000-0000-0000-0000-000000000000", fx.actor); !errors.Is(err, ErrIssueNotFound) {
		t.Fatalf("missing delete err = %v, want ErrIssueNotFound", err)
	}
	assertLinkedObjectiveEvents(t, fake)
}

func TestObjectiveBroadcastLinkedIssueAutomationCascade(t *testing.T) {
	fx := setupLinkedIssue(t)
	// A priority_changed rule that moves the issue to Done: the PATCH
	// itself touches only priority, so only the post-automation
	// re-read in updateIssueTx can see the state change.
	in := eventRuleInput("priority bot",
		AutomationTrigger{Type: "issue.priority_changed", ToPriorities: []int{3}},
		[]AutomationAction{{Type: "set_state", StateID: &fx.doneID}})
	if _, err := CreateAutomationRule(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor, in); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	if _, err := UpdateIssue(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, fx.actor,
		IssuePatch{Priority: autoIntPtr(3)}); err != nil {
		t.Fatalf("UpdateIssue priority-only: %v", err)
	}
	assertLinkedObjectiveEvents(t, fake, fx.obID)
}

func TestObjectiveBroadcastLinkedIssueNilBroadcasterSilent(t *testing.T) {
	fx := setupLinkedIssue(t)
	prev := Realtime
	Realtime = nil
	t.Cleanup(func() { Realtime = prev })

	// No panic, no error: announce is a silent no-op with no
	// broadcaster wired.
	if _, err := UpdateIssue(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, fx.actor,
		IssuePatch{StateID: &fx.doneID}); err != nil {
		t.Fatalf("UpdateIssue state change: %v", err)
	}
	if err := DeleteIssue(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, fx.actor); err != nil {
		t.Fatalf("DeleteIssue: %v", err)
	}
}
