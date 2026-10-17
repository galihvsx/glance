package service

// CreateIssue post-created-rules fresh snapshot (C48T1): a created-rule
// that mutates the row (set_state / set_priority) runs in the same tx
// as the INSERT, so the pre-rules INSERT-time row is stale. CreateIssue
// must re-read the row post-commit and use the fresh row for BOTH the
// issue.created broadcast payload AND the returned Issue (the 201
// body). Lookup failure falls back to the pre-rules row, never failing
// the create. Failed mutations announce nothing.

import (
	"context"
	"errors"
	"testing"
)

// issueCreatedPayloads returns the payloads of the captured
// issue.created events, keyed by issue id.
func issueCreatedPayloads(t *testing.T, fake *capturingBroadcaster) map[string]issueEventData {
	t.Helper()
	got := map[string]issueEventData{}
	for _, ev := range fake.snapshot() {
		if ev.eventType != EventIssueCreated {
			continue
		}
		payload, ok := ev.data.(issueEventData)
		if !ok {
			t.Fatalf("issue.created payload type = %T, want issueEventData", ev.data)
		}
		got[payload.ID] = payload
	}
	return got
}

func TestCreateIssueCreatedRuleSetStateFreshSnapshot(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, createdRuleInput("auto triage", []AutomationAction{
		{Type: "set_state", StateID: autoStrPtr(fx.started)},
	})); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	// No explicit state: the issue lands in backlog, the created-rule
	// moves it to started before commit.
	iss, err := CreateIssue(ctx, fx.pool, fx.slug, fx.ident, fx.admin, CreateIssueInput{Name: "fresh snapshot"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if iss.StateID != fx.started {
		t.Fatalf("returned StateID = %s, want post-rules %s", iss.StateID, fx.started)
	}
	if iss.DisplayID == "" {
		t.Fatalf("returned DisplayID empty, want derived ident-sequence")
	}

	if n := countEventsOfType(t, fake, EventIssueCreated); n != 1 {
		t.Fatalf("issue.created = %d, want exactly 1", n)
	}
	payload, ok := issueCreatedPayloads(t, fake)[iss.ID]
	if !ok {
		t.Fatalf("no issue.created for %s: %+v", iss.ID, issueCreatedPayloads(t, fake))
	}
	if payload.StateID != fx.started {
		t.Fatalf("issue.created state_id = %s, want post-rules %s", payload.StateID, fx.started)
	}
	if payload.DisplayID != iss.DisplayID || payload.Name != iss.Name {
		t.Fatalf("issue.created payload = %+v, want display %q name %q", payload, iss.DisplayID, iss.Name)
	}
}

func TestCreateIssueNoRulePayloadMatchesReturn(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()
	fake := installCapturingBroadcaster(t)

	iss, err := CreateIssue(ctx, fx.pool, fx.slug, fx.ident, fx.admin, CreateIssueInput{Name: "plain create"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if iss.StateID != fx.backlog {
		t.Fatalf("returned StateID = %s, want backlog %s", iss.StateID, fx.backlog)
	}

	if n := countEventsOfType(t, fake, EventIssueCreated); n != 1 {
		t.Fatalf("issue.created = %d, want exactly 1", n)
	}
	payload, ok := issueCreatedPayloads(t, fake)[iss.ID]
	if !ok {
		t.Fatalf("no issue.created for %s", iss.ID)
	}
	if payload.StateID != iss.StateID || payload.DisplayID != iss.DisplayID || payload.Name != iss.Name {
		t.Fatalf("issue.created payload = %+v, want state %q display %q name %q",
			payload, iss.StateID, iss.DisplayID, iss.Name)
	}
}

func TestCreateIssueCreatedRuleSetPriorityFreshReturn(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, createdRuleInput("urgent triage", []AutomationAction{
		{Type: "set_priority", Priority: autoIntPtr(4)},
	})); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	// Default priority 0; the created-rule raises it to 4.
	iss, err := CreateIssue(ctx, fx.pool, fx.slug, fx.ident, fx.admin, CreateIssueInput{Name: "priority fresh"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if iss.Priority != 4 {
		t.Fatalf("returned Priority = %d, want post-rules 4", iss.Priority)
	}
}

func TestCreateIssueFailedCreateAnnouncesNothing(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()
	fake := installCapturingBroadcaster(t)

	if _, err := CreateIssue(ctx, fx.pool, fx.slug, fx.ident, fx.admin, CreateIssueInput{Name: "  "}); !errors.Is(err, ErrNameRequired) {
		t.Fatalf("empty name err = %v, want ErrNameRequired", err)
	}
	if _, err := CreateIssue(ctx, fx.pool, fx.slug, fx.ident, fx.admin,
		CreateIssueInput{Name: "bad state", StateID: autoStrPtr("00000000-0000-0000-0000-000000000000")}); err == nil {
		t.Fatalf("unknown state err = nil, want an error")
	}
	assertZeroEvents(t, fake)
}
