package service

// CreateComment snapshots the comment_added cascade row (C50T2): a
// comment_added firing that moves the issue row (set_state,
// set_priority, ...) must leave exactly one new version carrying the
// post-cascade row. A comment with no rule, a no-op firing, or a
// firing that touches no issue-row field (add_comment) leaves the
// version count unchanged.

import (
	"context"
	"encoding/json"
	"testing"
)

func TestCommentCascadeSetStateSnapshotsOneVersion(t *testing.T) {
	fx := setupLinkedIssue(t)
	ctx := context.Background()
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.actor, AutomationRuleInput{
		Name:    "comment finisher",
		Trigger: AutomationTrigger{Type: "issue.comment_added"},
		Actions: []AutomationAction{{Type: "set_state", StateID: &fx.doneID}},
	}); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	if got := versionCount(t, fx, fx.iss.ID); got != 1 {
		t.Fatalf("versions before comment = %d, want 1 (create v1)", got)
	}

	doc := json.RawMessage(`{"type":"doc","content":[]}`)
	if _, err := CreateComment(ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, fx.actor, doc, nil); err != nil {
		t.Fatalf("CreateComment: %v", err)
	}

	if got := issueStateID(t, fx, fx.iss.ID); got != fx.doneID {
		t.Fatalf("issue state = %s, want cascade-moved %s", got, fx.doneID)
	}
	// Exactly ONE new version, carrying the post-cascade row.
	if got := versionCount(t, fx, fx.iss.ID); got != 2 {
		t.Fatalf("versions = %d, want 2 (v1 create + v2 cascade)", got)
	}
	if snap := latestVersionSnapshot(t, fx, fx.iss.ID); snap.StateID != fx.doneID {
		t.Fatalf("snapshot StateID = %s, want post-cascade %s", snap.StateID, fx.doneID)
	}
}

func TestCommentCascadeSetPrioritySnapshotsOneVersion(t *testing.T) {
	fx := setupLinkedIssue(t)
	ctx := context.Background()
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.actor, AutomationRuleInput{
		Name:    "comment prioritizer",
		Trigger: AutomationTrigger{Type: "issue.comment_added"},
		Actions: []AutomationAction{{Type: "set_priority", Priority: autoIntPtr(3)}},
	}); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	if got := versionCount(t, fx, fx.iss.ID); got != 1 {
		t.Fatalf("versions before comment = %d, want 1 (create v1)", got)
	}

	doc := json.RawMessage(`{"type":"doc","content":[]}`)
	if _, err := CreateComment(ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, fx.actor, doc, nil); err != nil {
		t.Fatalf("CreateComment: %v", err)
	}

	// Exactly ONE new version, carrying the new priority. State is
	// untouched: the snapshot must reflect the full post-cascade row,
	// not just the moved field.
	if got := versionCount(t, fx, fx.iss.ID); got != 2 {
		t.Fatalf("versions = %d, want 2 (v1 create + v2 cascade)", got)
	}
	snap := latestVersionSnapshot(t, fx, fx.iss.ID)
	if snap.Priority != 3 {
		t.Fatalf("snapshot Priority = %d, want 3", snap.Priority)
	}
	if snap.StateID != fx.backlogID {
		t.Fatalf("snapshot StateID = %s, want unchanged %s", snap.StateID, fx.backlogID)
	}
}

func TestCommentNoRuleSnapshotsNothing(t *testing.T) {
	fx := setupLinkedIssue(t)
	if got := versionCount(t, fx, fx.iss.ID); got != 1 {
		t.Fatalf("versions before comment = %d, want 1 (create v1)", got)
	}

	doc := json.RawMessage(`{"type":"doc","content":[]}`)
	if _, err := CreateComment(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, fx.actor, doc, nil); err != nil {
		t.Fatalf("CreateComment: %v", err)
	}

	if got := versionCount(t, fx, fx.iss.ID); got != 1 {
		t.Fatalf("versions = %d, want 1 (comment-only must not snapshot)", got)
	}
}

func TestCommentNoopFiringSnapshotsNothing(t *testing.T) {
	t.Run("set_state to current state", func(t *testing.T) {
		fx := setupLinkedIssue(t)
		ctx := context.Background()
		if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.actor, AutomationRuleInput{
			Name:    "comment no-op state",
			Trigger: AutomationTrigger{Type: "issue.comment_added"},
			Actions: []AutomationAction{{Type: "set_state", StateID: &fx.backlogID}},
		}); err != nil {
			t.Fatalf("CreateAutomationRule: %v", err)
		}

		doc := json.RawMessage(`{"type":"doc","content":[]}`)
		if _, err := CreateComment(ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, fx.actor, doc, nil); err != nil {
			t.Fatalf("CreateComment: %v", err)
		}

		if got := versionCount(t, fx, fx.iss.ID); got != 1 {
			t.Fatalf("versions = %d, want 1 (no-op firing must not snapshot)", got)
		}
	})
	t.Run("set_priority to current priority", func(t *testing.T) {
		fx := setupLinkedIssue(t)
		ctx := context.Background()
		// The fixture issue's default priority is 0.
		if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.actor, AutomationRuleInput{
			Name:    "comment no-op priority",
			Trigger: AutomationTrigger{Type: "issue.comment_added"},
			Actions: []AutomationAction{{Type: "set_priority", Priority: autoIntPtr(0)}},
		}); err != nil {
			t.Fatalf("CreateAutomationRule: %v", err)
		}

		doc := json.RawMessage(`{"type":"doc","content":[]}`)
		if _, err := CreateComment(ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, fx.actor, doc, nil); err != nil {
			t.Fatalf("CreateComment: %v", err)
		}

		if got := versionCount(t, fx, fx.iss.ID); got != 1 {
			t.Fatalf("versions = %d, want 1 (no-op firing must not snapshot)", got)
		}
	})
}

func TestCommentAddCommentFiringSnapshotsNothing(t *testing.T) {
	fx := setupLinkedIssue(t)
	ctx := context.Background()
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.actor, AutomationRuleInput{
		Name:    "comment noter",
		Trigger: AutomationTrigger{Type: "issue.comment_added"},
		Actions: []AutomationAction{{Type: "add_comment", Body: autoStrPtr("noted!")}},
	}); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	doc := json.RawMessage(`{"type":"doc","content":[]}`)
	if _, err := CreateComment(ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, fx.actor, doc, nil); err != nil {
		t.Fatalf("CreateComment: %v", err)
	}

	// The firing ran (its automation comment landed) but touched no
	// issue-row field, so no version is written.
	comments, err := ListComments(ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, fx.actor)
	if err != nil {
		t.Fatalf("ListComments: %v", err)
	}
	if got := len(comments); got != 2 {
		t.Fatalf("comments = %d, want 2 (user comment + firing comment)", got)
	}
	if got := versionCount(t, fx, fx.iss.ID); got != 1 {
		t.Fatalf("versions = %d, want 1 (row-untouched firing must not snapshot)", got)
	}
}
