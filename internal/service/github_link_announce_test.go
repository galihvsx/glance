package service

// External-ref realtime announce (C62T1): ref create/delete fan out
// issue.updated on the issue / project / workspace channels so open
// issue-detail pages refetch their refs (cross-tab and collaborator
// freshness). The frontend also invalidates directly on mutation
// success; the announce is the shared-freshness backstop. Real test
// database, no skips.

import (
	"context"
	"testing"
)

// findSingleIssueUpdated returns the one captured issue.updated event,
// failing the test unless exactly one was broadcast.
func findSingleIssueUpdated(t *testing.T, fake *capturingBroadcaster) capturedEvent {
	t.Helper()
	var found []capturedEvent
	for _, ev := range fake.snapshot() {
		if ev.eventType == EventIssueUpdated {
			found = append(found, ev)
		}
	}
	if len(found) != 1 {
		t.Fatalf("issue.updated events = %d, want 1", len(found))
	}
	return found[0]
}

// assertRefAnnounceShape checks the channel set and payload of a ref
// mutation's issue.updated event.
func assertRefAnnounceShape(t *testing.T, ev capturedEvent, wsSlug, ident, issueID string) {
	t.Helper()
	wantChannels := map[string]bool{
		"issue:" + issueID:                true,
		"project:" + wsSlug + ":" + ident: true,
		"workspace:" + wsSlug:             true,
	}
	if len(ev.channels) != len(wantChannels) {
		t.Fatalf("announce channels = %v, want %v", ev.channels, wantChannels)
	}
	for _, ch := range ev.channels {
		if !wantChannels[ch] {
			t.Fatalf("announce channels = %v, want %v", ev.channels, wantChannels)
		}
	}
	payload, ok := ev.data.(issueEventData)
	if !ok {
		t.Fatalf("announce payload type = %T, want issueEventData", ev.data)
	}
	if payload.ID != issueID {
		t.Fatalf("announce payload ID = %q, want %q", payload.ID, issueID)
	}
}

func TestCreateExternalRefAnnouncesIssueUpdated(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	wsSlug, ident, issueID, adminID, _ := linkTestSetup(t, pool, "ghann")
	fake := installCapturingBroadcaster(t)

	if _, err := CreateExternalRef(ctx, pool, wsSlug, ident, issueID, adminID, CreateExternalRefInput{
		Repo:    "acme/repo",
		RefType: "pr",
		RefID:   "9",
		URL:     "https://github.com/acme/repo/pull/9",
	}); err != nil {
		t.Fatalf("CreateExternalRef: %v", err)
	}
	ev := findSingleIssueUpdated(t, fake)
	assertRefAnnounceShape(t, ev, wsSlug, ident, issueID)
}

func TestDeleteExternalRefAnnouncesIssueUpdated(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	wsSlug, ident, issueID, adminID, _ := linkTestSetup(t, pool, "ghdelann")
	ref, err := CreateExternalRef(ctx, pool, wsSlug, ident, issueID, adminID, CreateExternalRefInput{
		Repo:    "acme/repo",
		RefType: "commit",
		RefID:   "abc1234",
		URL:     "https://github.com/acme/repo/commit/abc1234",
	})
	if err != nil {
		t.Fatalf("CreateExternalRef: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	if err := DeleteExternalRef(ctx, pool, wsSlug, ident, issueID, adminID, ref.ID); err != nil {
		t.Fatalf("DeleteExternalRef: %v", err)
	}
	ev := findSingleIssueUpdated(t, fake)
	assertRefAnnounceShape(t, ev, wsSlug, ident, issueID)
}
