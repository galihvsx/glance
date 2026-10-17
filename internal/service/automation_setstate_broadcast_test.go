package service

// Automation set_state broadcast (C45T1): a set_state action that
// actually moves an issue announces issue.updated (post-commit
// snapshot) + objective.updated for its linked objectives — from
// scheduled firings and from comment-triggered firings. A no-op
// set_state, a failed firing, and any non-set_state action announce
// nothing new. UpdateIssue-driven cascades are NOT re-announced here:
// the outer caller already announces exactly once.

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// countEventsOfType counts the captured events of one type.
func countEventsOfType(t *testing.T, fake *capturingBroadcaster, eventType string) int {
	t.Helper()
	n := 0
	for _, ev := range fake.snapshot() {
		if ev.eventType == eventType {
			n++
		}
	}
	return n
}

// makeIssueOverdue sets the issue's target_date to yesterday so the
// issue.overdue scheduled trigger matches it.
func makeIssueOverdue(t *testing.T, fx *linkedIssueFixture, issueID string) {
	t.Helper()
	if _, err := fx.pool.Exec(fx.ctx,
		`UPDATE issues SET target_date = CURRENT_DATE - 1 WHERE id = $1::uuid`, issueID); err != nil {
		t.Fatalf("make overdue: %v", err)
	}
}

// issueStateID refetches one issue's current state_id.
func issueStateID(t *testing.T, fx *linkedIssueFixture, issueID string) string {
	t.Helper()
	var state string
	if err := fx.pool.QueryRow(fx.ctx,
		`SELECT state_id::text FROM issues WHERE id = $1::uuid`, issueID).Scan(&state); err != nil {
		t.Fatalf("refetch state: %v", err)
	}
	return state
}

// addBlockerTo links a fresh open issue as a blocker of issueID
// ("blocker blocks issueID"), so a move into a completed state is
// rejected by the C16T3 blocker guard.
func addBlockerTo(t *testing.T, fx *linkedIssueFixture, issueID string) {
	t.Helper()
	blocker, err := CreateIssue(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		CreateIssueInput{Name: "blocker", StateID: &fx.backlogID})
	if err != nil {
		t.Fatalf("CreateIssue blocker: %v", err)
	}
	if _, err := CreateIssueLink(fx.ctx, fx.pool, fx.slug, fx.ident, blocker.ID, fx.actor, issueID, "blocks"); err != nil {
		t.Fatalf("CreateIssueLink blocker: %v", err)
	}
}

func TestScheduledSetStateMoveBroadcasts(t *testing.T) {
	fx := setupLinkedIssue(t)
	ctx := context.Background()
	makeIssueOverdue(t, fx, fx.iss.ID)
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.actor, AutomationRuleInput{
		Name:    "overdue finisher",
		Trigger: AutomationTrigger{Type: "issue.overdue"},
		Actions: []AutomationAction{{Type: "set_state", StateID: &fx.doneID}},
	}); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	if err := RunScheduledAutomationPass(ctx, fx.pool, time.Now()); err != nil {
		t.Fatalf("RunScheduledAutomationPass: %v", err)
	}

	if got := issueStateID(t, fx, fx.iss.ID); got != fx.doneID {
		t.Fatalf("issue state = %s, want moved to %s", got, fx.doneID)
	}
	assertIssueUpdatedIDs(t, fake, fx.iss.ID)
	for _, payload := range issueUpdatedPayloads(t, fake) {
		if payload.StateID != fx.doneID {
			t.Fatalf("issue.updated state_id = %s, want post-commit %s", payload.StateID, fx.doneID)
		}
	}
	assertLinkedObjectiveEvents(t, fake, fx.obID)
}

func TestScheduledSetStateNoopBroadcastsNothing(t *testing.T) {
	fx := setupLinkedIssue(t)
	ctx := context.Background()
	makeIssueOverdue(t, fx, fx.iss.ID)
	// Target is the issue's current state: the firing is a no-op.
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.actor, AutomationRuleInput{
		Name:    "overdue no-op",
		Trigger: AutomationTrigger{Type: "issue.overdue"},
		Actions: []AutomationAction{{Type: "set_state", StateID: &fx.backlogID}},
	}); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	if err := RunScheduledAutomationPass(ctx, fx.pool, time.Now()); err != nil {
		t.Fatalf("RunScheduledAutomationPass: %v", err)
	}

	assertZeroEvents(t, fake)
}

func TestScheduledNonSetStateActionBroadcastsNothing(t *testing.T) {
	fx := setupLinkedIssue(t)
	ctx := context.Background()
	makeIssueOverdue(t, fx, fx.iss.ID)
	// set_state ONLY: an add_comment firing runs but announces nothing.
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.actor, AutomationRuleInput{
		Name:    "overdue nudger",
		Trigger: AutomationTrigger{Type: "issue.overdue"},
		Actions: []AutomationAction{{Type: "add_comment", Body: autoStrPtr("overdue nudge")}},
	}); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	if err := RunScheduledAutomationPass(ctx, fx.pool, time.Now()); err != nil {
		t.Fatalf("RunScheduledAutomationPass: %v", err)
	}

	if got := commentCount(t, automationFixture{pool: fx.pool}, fx.iss.ID); got != 1 {
		t.Fatalf("comments = %d, want 1 (the firing must still run)", got)
	}
	assertZeroEvents(t, fake)
}

func TestScheduledSetStateBlockedFiringBroadcastsNothing(t *testing.T) {
	fx := setupLinkedIssue(t)
	ctx := context.Background()
	makeIssueOverdue(t, fx, fx.iss.ID)
	addBlockerTo(t, fx, fx.iss.ID)
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.actor, AutomationRuleInput{
		Name:    "overdue finisher",
		Trigger: AutomationTrigger{Type: "issue.overdue"},
		Actions: []AutomationAction{{Type: "set_state", StateID: &fx.doneID}},
	}); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	if err := RunScheduledAutomationPass(ctx, fx.pool, time.Now()); err != nil {
		t.Fatalf("RunScheduledAutomationPass: %v", err)
	}

	// The blocker guard rejected the move: state unchanged, nothing announced.
	if got := issueStateID(t, fx, fx.iss.ID); got != fx.backlogID {
		t.Fatalf("issue state = %s, want unmoved %s", got, fx.backlogID)
	}
	assertZeroEvents(t, fake)
}

func TestCommentSetStateMoveBroadcasts(t *testing.T) {
	fx := setupLinkedIssue(t)
	ctx := context.Background()
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.actor, AutomationRuleInput{
		Name:    "comment finisher",
		Trigger: AutomationTrigger{Type: "issue.comment_added"},
		Actions: []AutomationAction{{Type: "set_state", StateID: &fx.doneID}},
	}); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	doc := json.RawMessage(`{"type":"doc","content":[]}`)
	if _, err := CreateComment(ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, fx.actor, doc, nil); err != nil {
		t.Fatalf("CreateComment: %v", err)
	}

	if got := issueStateID(t, fx, fx.iss.ID); got != fx.doneID {
		t.Fatalf("issue state = %s, want moved to %s", got, fx.doneID)
	}
	if n := countEventsOfType(t, fake, EventCommentCreated); n != 1 {
		t.Fatalf("comment.created = %d, want 1", n)
	}
	assertIssueUpdatedIDs(t, fake, fx.iss.ID)
	for _, payload := range issueUpdatedPayloads(t, fake) {
		if payload.StateID != fx.doneID {
			t.Fatalf("issue.updated state_id = %s, want post-commit %s", payload.StateID, fx.doneID)
		}
	}
	assertLinkedObjectiveEvents(t, fake, fx.obID)
}

func TestCommentSetStateNoopBroadcastsOnlyCommentCreated(t *testing.T) {
	fx := setupLinkedIssue(t)
	ctx := context.Background()
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.actor, AutomationRuleInput{
		Name:    "comment no-op",
		Trigger: AutomationTrigger{Type: "issue.comment_added"},
		Actions: []AutomationAction{{Type: "set_state", StateID: &fx.backlogID}},
	}); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	doc := json.RawMessage(`{"type":"doc","content":[]}`)
	if _, err := CreateComment(ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, fx.actor, doc, nil); err != nil {
		t.Fatalf("CreateComment: %v", err)
	}

	if n := countEventsOfType(t, fake, EventCommentCreated); n != 1 {
		t.Fatalf("comment.created = %d, want 1", n)
	}
	if n := countEventsOfType(t, fake, EventIssueUpdated); n != 0 {
		t.Fatalf("issue.updated = %d, want 0 (no-op set_state)", n)
	}
	assertLinkedObjectiveEvents(t, fake)
}

func TestCommentNoRuleBroadcastsOnlyCommentCreated(t *testing.T) {
	fx := setupLinkedIssue(t)
	fake := installCapturingBroadcaster(t)

	doc := json.RawMessage(`{"type":"doc","content":[]}`)
	if _, err := CreateComment(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, fx.actor, doc, nil); err != nil {
		t.Fatalf("CreateComment: %v", err)
	}

	if n := countEventsOfType(t, fake, EventCommentCreated); n != 1 {
		t.Fatalf("comment.created = %d, want 1", n)
	}
	if n := countEventsOfType(t, fake, EventIssueUpdated); n != 0 {
		t.Fatalf("issue.updated = %d, want 0 (no rule fired)", n)
	}
	assertLinkedObjectiveEvents(t, fake)
}

func TestCommentNonSetStateActionBroadcastsOnlyCommentCreated(t *testing.T) {
	fx := setupLinkedIssue(t)
	ctx := context.Background()
	// set_state ONLY: an add_comment firing runs but announces no issue event.
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.actor, AutomationRuleInput{
		Name:    "comment noter",
		Trigger: AutomationTrigger{Type: "issue.comment_added"},
		Actions: []AutomationAction{{Type: "add_comment", Body: autoStrPtr("noted!")}},
	}); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	doc := json.RawMessage(`{"type":"doc","content":[]}`)
	if _, err := CreateComment(ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, fx.actor, doc, nil); err != nil {
		t.Fatalf("CreateComment: %v", err)
	}

	// Exactly one comment.created: the user's comment. The automation's
	// own comment bypasses CreateComment and announces nothing.
	if n := countEventsOfType(t, fake, EventCommentCreated); n != 1 {
		t.Fatalf("comment.created = %d, want 1", n)
	}
	if n := countEventsOfType(t, fake, EventIssueUpdated); n != 0 {
		t.Fatalf("issue.updated = %d, want 0 (add_comment is not set_state)", n)
	}
	assertLinkedObjectiveEvents(t, fake)
}

func TestCommentSetStateBlockedFiringBroadcastsOnlyCommentCreated(t *testing.T) {
	fx := setupLinkedIssue(t)
	ctx := context.Background()
	addBlockerTo(t, fx, fx.iss.ID)
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.actor, AutomationRuleInput{
		Name:    "comment finisher",
		Trigger: AutomationTrigger{Type: "issue.comment_added"},
		Actions: []AutomationAction{{Type: "set_state", StateID: &fx.doneID}},
	}); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	doc := json.RawMessage(`{"type":"doc","content":[]}`)
	if _, err := CreateComment(ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, fx.actor, doc, nil); err != nil {
		t.Fatalf("CreateComment: %v", err)
	}

	// The comment posts; the blocker guard rejects the move.
	if got := issueStateID(t, fx, fx.iss.ID); got != fx.backlogID {
		t.Fatalf("issue state = %s, want unmoved %s", got, fx.backlogID)
	}
	if n := countEventsOfType(t, fake, EventCommentCreated); n != 1 {
		t.Fatalf("comment.created = %d, want 1", n)
	}
	if n := countEventsOfType(t, fake, EventIssueUpdated); n != 0 {
		t.Fatalf("issue.updated = %d, want 0 (failed firing)", n)
	}
	assertLinkedObjectiveEvents(t, fake)
}

func TestUpdateIssueAutomationCascadeAnnouncesOnce(t *testing.T) {
	fx := setupLinkedIssue(t)
	ctx := context.Background()
	started, err := CreateState(ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		StateInput{Name: "Started", Group: "started"})
	if err != nil {
		t.Fatalf("CreateState started: %v", err)
	}
	// A state_changed rule whose set_state moves the issue one step
	// further: the outer UpdateIssue announce must fire exactly once,
	// not once per cascade step.
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.actor, AutomationRuleInput{
		Name:    "started finisher",
		Trigger: AutomationTrigger{Type: "issue.state_changed", ToStates: []string{started.ID}},
		Actions: []AutomationAction{{Type: "set_state", StateID: &fx.doneID}},
	}); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	if _, err := UpdateIssue(ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, fx.actor,
		IssuePatch{StateID: &started.ID}); err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}

	if got := issueStateID(t, fx, fx.iss.ID); got != fx.doneID {
		t.Fatalf("issue state = %s, want cascade-moved to %s", got, fx.doneID)
	}
	if n := countEventsOfType(t, fake, EventIssueUpdated); n != 1 {
		t.Fatalf("issue.updated = %d, want exactly 1 (outer caller only)", n)
	}
	assertLinkedObjectiveEvents(t, fake, fx.obID)
}
