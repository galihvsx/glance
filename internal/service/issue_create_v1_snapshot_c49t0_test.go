package service

// CreateIssue v1 snapshot after created-rules (C49T0): the v1
// snapshotVersionTx must run AFTER runAutomationCreatedRulesTx,
// snapshotting a fresh in-tx re-read of the row. A created-rule
// set_state / set_priority move must therefore be visible in v1,
// and restoring v1 must not revert the automation move.

import (
	"context"
	"encoding/json"
	"testing"
)

func decodeVersionSnapshot(t *testing.T, v *IssueVersion) Issue {
	t.Helper()
	var iss Issue
	if err := json.Unmarshal(v.Snapshot, &iss); err != nil {
		t.Fatalf("decode version %d snapshot: %v", v.VersionNo, err)
	}
	return iss
}

func TestCreateIssueV1SnapshotCarriesPostRulesState(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, createdRuleInput("auto triage", []AutomationAction{
		{Type: "set_state", StateID: autoStrPtr(fx.started)},
	})); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	// No explicit state: the issue lands in backlog, the created-rule
	// moves it to started before commit.
	iss, err := CreateIssue(ctx, fx.pool, fx.slug, fx.ident, fx.admin, CreateIssueInput{Name: "v1 post-rules state"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if iss.StateID != fx.started {
		t.Fatalf("returned StateID = %s, want post-rules %s", iss.StateID, fx.started)
	}

	vs, err := ListVersions(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(vs) != 1 || vs[0].VersionNo != 1 {
		t.Fatalf("versions after create = %d, want exactly [v1]", len(vs))
	}
	if got := decodeVersionSnapshot(t, vs[0]); got.StateID != fx.started {
		t.Fatalf("v1 snapshot StateID = %s, want post-rules %s (v1 must not hold the pre-rules row)", got.StateID, fx.started)
	}
}

func TestCreateIssueV1SnapshotNoRuleMatchesCreate(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()
	priority := 2

	iss, err := CreateIssue(ctx, fx.pool, fx.slug, fx.ident, fx.admin, CreateIssueInput{Name: "v1 plain create", Priority: &priority})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	vs, err := ListVersions(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(vs) != 1 || vs[0].VersionNo != 1 {
		t.Fatalf("versions after create = %d, want exactly [v1]", len(vs))
	}
	snap := decodeVersionSnapshot(t, vs[0])
	if snap.StateID != fx.backlog {
		t.Fatalf("v1 snapshot StateID = %s, want backlog %s", snap.StateID, fx.backlog)
	}
	if snap.Priority != priority {
		t.Fatalf("v1 snapshot Priority = %d, want %d", snap.Priority, priority)
	}
	if snap.Name != iss.Name || snap.SequenceID != iss.SequenceID || snap.DisplayID != iss.DisplayID {
		t.Fatalf("v1 snapshot = %+v, want name %q sequence %d display %q", snap, iss.Name, iss.SequenceID, iss.DisplayID)
	}
}

func TestCreateIssueV1SnapshotCarriesPostRulesPriority(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, createdRuleInput("urgent triage", []AutomationAction{
		{Type: "set_priority", Priority: autoIntPtr(4)},
	})); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	// Default priority 0; the created-rule raises it to 4.
	iss, err := CreateIssue(ctx, fx.pool, fx.slug, fx.ident, fx.admin, CreateIssueInput{Name: "v1 post-rules priority"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if iss.Priority != 4 {
		t.Fatalf("returned Priority = %d, want post-rules 4", iss.Priority)
	}

	vs, err := ListVersions(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(vs) != 1 || vs[0].VersionNo != 1 {
		t.Fatalf("versions after create = %d, want exactly [v1]", len(vs))
	}
	if got := decodeVersionSnapshot(t, vs[0]); got.Priority != 4 {
		t.Fatalf("v1 snapshot Priority = %d, want post-rules 4 (v1 must not hold the pre-rules row)", got.Priority)
	}
}

func TestRestoreV1AfterCreatedRuleMoveIsNoOp(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, createdRuleInput("auto triage", []AutomationAction{
		{Type: "set_state", StateID: autoStrPtr(fx.started)},
	})); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	iss, err := CreateIssue(ctx, fx.pool, fx.slug, fx.ident, fx.admin, CreateIssueInput{Name: "v1 restore no-op"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if iss.StateID != fx.started {
		t.Fatalf("created StateID = %s, want post-rules %s", iss.StateID, fx.started)
	}

	// v1 already holds the post-rules row, so restoring it diffs to
	// nothing: the live row must stay in the post-rules state, and
	// the restore still records the marker + a new version per the
	// existing no-diff branch.
	restored, err := RestoreIssueVersion(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin, 1)
	if err != nil {
		t.Fatalf("RestoreIssueVersion v1: %v", err)
	}
	if restored.StateID != fx.started {
		t.Fatalf("restored StateID = %s, want post-rules %s (restore must not revert the automation move)", restored.StateID, fx.started)
	}

	vs, err := ListVersions(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(vs) != 2 || vs[1].VersionNo != 2 {
		t.Fatalf("versions after restore = %d, want [v1 v2] (restore marker version)", len(vs))
	}
	if got := decodeVersionSnapshot(t, vs[1]); got.StateID != fx.started {
		t.Fatalf("v2 snapshot StateID = %s, want post-rules %s", got.StateID, fx.started)
	}
}
