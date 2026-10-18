package service

// Inbound ref-change broadcast (C63T0): the inbound GitHub PR-webhook path
// fans out issue.updated per affected issue — the same event the manual
// ref create/delete paths already send (C62T1 announceExternalRefChange).
//
//   - opened with a display-ID mention in the PR body → ref row created
//     → exactly one issue.updated for that issue.
//   - closed with merged:true → ref state flipped → exactly one
//     issue.updated per affected issue; the mention rescan on the same
//     issue does not double-broadcast.
//   - no-mention no-op → nothing inserted → nothing announced.
//
// Real test database, no skips.

import (
	"context"
	"testing"
)

func TestGitHubWebhookOpenedAnnouncesIssueUpdated(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	wsSlug, ident, issueID, displayID, secret := webhookTestSetup(t, pool, "ghb1")
	fake := installCapturingBroadcaster(t)

	body := prPayload("opened", 7, "Fix login ("+displayID+")", "Closes "+displayID, "acme/repo", false)
	if err := HandleGitHubWebhook(ctx, pool, "pull_request", signDelivery(secret, body), body); err != nil {
		t.Fatalf("webhook: %v", err)
	}
	if n := countRefs(t, pool, issueID); n != 1 {
		t.Fatalf("refs = %d, want 1", n)
	}
	ev := findSingleIssueUpdated(t, fake)
	assertRefAnnounceShape(t, ev, wsSlug, ident, issueID)
}

func TestGitHubWebhookClosedMergedAnnouncesIssueUpdated(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	wsSlug, ident, issueID, displayID, secret := webhookTestSetup(t, pool, "ghb2")

	opened := prPayload("opened", 7, "Work on "+displayID, "", "acme/repo", false)
	if err := HandleGitHubWebhook(ctx, pool, "pull_request", signDelivery(secret, opened), opened); err != nil {
		t.Fatalf("opened: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	// Close payload mentions nothing: the broadcast must still fire for
	// the (workspace, repo, number) state flip.
	closed := prPayload("closed", 7, "Unrelated title", "no mentions here", "acme/repo", true)
	if err := HandleGitHubWebhook(ctx, pool, "pull_request", signDelivery(secret, closed), closed); err != nil {
		t.Fatalf("closed: %v", err)
	}
	if s := refState(t, pool, issueID, "7"); s != "merged" {
		t.Fatalf("state = %q, want merged", s)
	}
	ev := findSingleIssueUpdated(t, fake)
	assertRefAnnounceShape(t, ev, wsSlug, ident, issueID)
}

func TestGitHubWebhookNoMentionAnnouncesNothing(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	_, _, issueID, _, secret := webhookTestSetup(t, pool, "ghb3")
	fake := installCapturingBroadcaster(t)

	body := prPayload("opened", 7, "Unrelated title", "no mentions here", "acme/repo", false)
	if err := HandleGitHubWebhook(ctx, pool, "pull_request", signDelivery(secret, body), body); err != nil {
		t.Fatalf("webhook: %v", err)
	}
	if n := countRefs(t, pool, issueID); n != 0 {
		t.Fatalf("refs = %d, want 0", n)
	}
	for _, ev := range fake.snapshot() {
		if ev.eventType == EventIssueUpdated {
			t.Fatalf("issue.updated broadcast on no-mention no-op: %+v", ev)
		}
	}
}

func TestGitHubWebhookClosedAnnouncesOncePerIssue(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	wsSlug, ident, issueID, displayID, secret := webhookTestSetup(t, pool, "ghb4")

	// A manual ref exists for the PR (created before the broadcaster is
	// installed so its own announce is not captured).
	var creatorID string
	if err := pool.QueryRow(ctx,
		`SELECT m.user_id::text FROM workspace_members m
		   JOIN workspaces w ON w.id = m.workspace_id
		  WHERE w.slug = $1 AND m.role = 20 LIMIT 1`, wsSlug).Scan(&creatorID); err != nil {
		t.Fatalf("creator: %v", err)
	}
	if _, err := CreateExternalRef(ctx, pool, wsSlug, ident, issueID, creatorID, CreateExternalRefInput{
		Repo: "acme/repo", RefType: "pr", RefID: "9", URL: "https://github.com/acme/repo/pull/9",
	}); err != nil {
		t.Fatalf("manual link: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	// The close delivery both flips the existing ref (closePRRefs) AND
	// re-links via the mention (rescan) — one issue.updated, not two.
	closed := prPayload("closed", 9, "Merge "+displayID, "Closes "+displayID, "acme/repo", true)
	if err := HandleGitHubWebhook(ctx, pool, "pull_request", signDelivery(secret, closed), closed); err != nil {
		t.Fatalf("closed: %v", err)
	}
	if s := refState(t, pool, issueID, "9"); s != "merged" {
		t.Fatalf("state = %q, want merged", s)
	}
	ev := findSingleIssueUpdated(t, fake)
	assertRefAnnounceShape(t, ev, wsSlug, ident, issueID)
}
