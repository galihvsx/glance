package service

// C50T3: a scheduled-rule firing that changes the issue row must record
// exactly one version snapshot (in-tx, actor = rule author); a firing
// whose actions are all no-ops must record none. The firing swallows
// errors (logs, never returns), so these tests observe the committed
// version rows, not return values.

import (
	"context"
	"testing"
	"time"
)

func TestScheduledFiringSetStateSnapshotsVersion(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, AutomationRuleInput{
		Name:    "overdue escalator",
		Trigger: AutomationTrigger{Type: "issue.overdue"},
		Actions: []AutomationAction{{Type: "set_state", StateID: autoStrPtr(fx.started)}},
	}); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	iss := createTargetIssue(t, fx, "overdue", timePtr(-24*time.Hour))
	if iss.StateID != fx.backlog {
		t.Fatalf("created StateID = %s, want backlog %s", iss.StateID, fx.backlog)
	}
	if n := countVersions(t, fx.pool, iss.ID); n != 1 {
		t.Fatalf("versions before firing = %d, want 1 (v1 from create)", n)
	}

	if err := RunScheduledAutomationPass(ctx, fx.pool, time.Now()); err != nil {
		t.Fatalf("RunScheduledAutomationPass: %v", err)
	}

	vs, err := ListVersions(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(vs) != 2 || vs[1].VersionNo != 2 {
		t.Fatalf("versions after firing = %d, want exactly [v1 v2]", len(vs))
	}
	if got := decodeVersionSnapshot(t, vs[1]); got.StateID != fx.started {
		t.Fatalf("v2 snapshot StateID = %s, want post-firing %s", got.StateID, fx.started)
	}
	if vs[1].CreatedBy.ID != fx.admin {
		t.Fatalf("v2 created_by = %s, want rule author %s", vs[1].CreatedBy.ID, fx.admin)
	}
}

func TestScheduledFiringSetPrioritySnapshotsVersion(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, AutomationRuleInput{
		Name:    "overdue urgency",
		Trigger: AutomationTrigger{Type: "issue.overdue"},
		Actions: []AutomationAction{{Type: "set_priority", Priority: autoIntPtr(3)}},
	}); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	iss := createTargetIssue(t, fx, "overdue", timePtr(-24*time.Hour))
	if iss.Priority != 0 {
		t.Fatalf("created Priority = %d, want default 0", iss.Priority)
	}
	if n := countVersions(t, fx.pool, iss.ID); n != 1 {
		t.Fatalf("versions before firing = %d, want 1 (v1 from create)", n)
	}

	if err := RunScheduledAutomationPass(ctx, fx.pool, time.Now()); err != nil {
		t.Fatalf("RunScheduledAutomationPass: %v", err)
	}

	vs, err := ListVersions(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(vs) != 2 || vs[1].VersionNo != 2 {
		t.Fatalf("versions after firing = %d, want exactly [v1 v2]", len(vs))
	}
	snap := decodeVersionSnapshot(t, vs[1])
	if snap.Priority != 3 {
		t.Fatalf("v2 snapshot Priority = %d, want post-firing 3", snap.Priority)
	}
	if snap.StateID != fx.backlog {
		t.Fatalf("v2 snapshot StateID = %s, want unchanged backlog %s", snap.StateID, fx.backlog)
	}
	if vs[1].CreatedBy.ID != fx.admin {
		t.Fatalf("v2 created_by = %s, want rule author %s", vs[1].CreatedBy.ID, fx.admin)
	}
}

func TestScheduledFiringNoOpSnapshotsNothing(t *testing.T) {
	t.Run("set_state to current state", func(t *testing.T) {
		fx := setupAutomationFixture(t)
		ctx := context.Background()

		if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, AutomationRuleInput{
			Name:    "overdue no-op state",
			Trigger: AutomationTrigger{Type: "issue.overdue"},
			Actions: []AutomationAction{{Type: "set_state", StateID: autoStrPtr(fx.backlog)}},
		}); err != nil {
			t.Fatalf("CreateAutomationRule: %v", err)
		}

		iss := createTargetIssue(t, fx, "overdue", timePtr(-24*time.Hour))
		if n := countVersions(t, fx.pool, iss.ID); n != 1 {
			t.Fatalf("versions before firing = %d, want 1 (v1 from create)", n)
		}

		if err := RunScheduledAutomationPass(ctx, fx.pool, time.Now()); err != nil {
			t.Fatalf("RunScheduledAutomationPass: %v", err)
		}
		if n := countVersions(t, fx.pool, iss.ID); n != 1 {
			t.Fatalf("versions after no-op firing = %d, want 1 (no new version)", n)
		}
	})

	t.Run("set_priority to current priority", func(t *testing.T) {
		fx := setupAutomationFixture(t)
		ctx := context.Background()

		if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, AutomationRuleInput{
			Name:    "overdue no-op priority",
			Trigger: AutomationTrigger{Type: "issue.overdue"},
			Actions: []AutomationAction{{Type: "set_priority", Priority: autoIntPtr(0)}},
		}); err != nil {
			t.Fatalf("CreateAutomationRule: %v", err)
		}

		iss := createTargetIssue(t, fx, "overdue", timePtr(-24*time.Hour))
		if n := countVersions(t, fx.pool, iss.ID); n != 1 {
			t.Fatalf("versions before firing = %d, want 1 (v1 from create)", n)
		}

		if err := RunScheduledAutomationPass(ctx, fx.pool, time.Now()); err != nil {
			t.Fatalf("RunScheduledAutomationPass: %v", err)
		}
		if n := countVersions(t, fx.pool, iss.ID); n != 1 {
			t.Fatalf("versions after no-op firing = %d, want 1 (no new version)", n)
		}
	})
}
