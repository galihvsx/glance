package service

// Comment reaction list + payload tests (C67T0): ListCommentReactions
// groups comment reactions by emoji (count + caller's reacted state),
// returns an empty (non-nil) slice when there are none, and 404s on an
// unknown comment. ListComments embeds reaction groups on every
// returned comment (roots and replies) — always present as [], never
// null — via a single batched query (no N+1).

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestListCommentReactionsGroups(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	wsSlug, ident, issueID, actor := testSatelliteFixture(t, pool, "cmt-react")

	comment, err := CreateComment(ctx, pool, wsSlug, ident, issueID, actor,
		json.RawMessage(`{"type":"doc","content":[]}`), nil)
	if err != nil {
		t.Fatalf("CreateComment: %v", err)
	}

	// Empty list is an empty slice, never nil.
	groups, err := ListCommentReactions(ctx, pool, wsSlug, ident, issueID, comment.ID, actor)
	if err != nil {
		t.Fatalf("ListCommentReactions: %v", err)
	}
	if groups == nil || len(groups) != 0 {
		t.Fatalf("groups = %+v, want non-nil empty", groups)
	}

	// Idempotent add; two users on 👍, one on 🎉.
	if err := AddCommentReaction(ctx, pool, wsSlug, ident, issueID, comment.ID, actor, "👍"); err != nil {
		t.Fatalf("AddCommentReaction: %v", err)
	}
	if err := AddCommentReaction(ctx, pool, wsSlug, ident, issueID, comment.ID, actor, "👍"); err != nil {
		t.Fatalf("double AddCommentReaction: %v", err)
	}
	other := createTestUser(t, pool, uniqueTestEmail("cmt-react-other"))
	addTestMember(t, pool, wsSlug, actor, other, 15) // member, so they may react
	if err := AddCommentReaction(ctx, pool, wsSlug, ident, issueID, comment.ID, other, "👍"); err != nil {
		t.Fatalf("other AddCommentReaction: %v", err)
	}
	if err := AddCommentReaction(ctx, pool, wsSlug, ident, issueID, comment.ID, actor, "🎉"); err != nil {
		t.Fatalf("AddCommentReaction 🎉: %v", err)
	}

	groups, err = ListCommentReactions(ctx, pool, wsSlug, ident, issueID, comment.ID, actor)
	if err != nil {
		t.Fatalf("ListCommentReactions: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("groups = %+v, want 2", groups)
	}
	// Ordered by count desc: 👍 (2) first.
	if groups[0].Emoji != "👍" || groups[0].Count != 2 || !groups[0].Reacted {
		t.Fatalf("groups[0] = %+v, want 👍 count 2 reacted true", groups[0])
	}
	if groups[1].Emoji != "🎉" || groups[1].Count != 1 || !groups[1].Reacted {
		t.Fatalf("groups[1] = %+v, want 🎉 count 1 reacted true", groups[1])
	}

	// From the other user's view: 👍 reacted, 🎉 not.
	groups, err = ListCommentReactions(ctx, pool, wsSlug, ident, issueID, comment.ID, other)
	if err != nil {
		t.Fatalf("ListCommentReactions (other): %v", err)
	}
	if len(groups) != 2 || !groups[0].Reacted || groups[1].Reacted {
		t.Fatalf("other view = %+v, want 👍 reacted / 🎉 not", groups)
	}

	// Remove is idempotent; reacted flips false for the remover.
	if err := RemoveCommentReaction(ctx, pool, wsSlug, ident, issueID, comment.ID, actor, "👍"); err != nil {
		t.Fatalf("RemoveCommentReaction: %v", err)
	}
	if err := RemoveCommentReaction(ctx, pool, wsSlug, ident, issueID, comment.ID, actor, "👍"); err != nil {
		t.Fatalf("double RemoveCommentReaction: %v", err)
	}
	groups, err = ListCommentReactions(ctx, pool, wsSlug, ident, issueID, comment.ID, actor)
	if err != nil {
		t.Fatalf("ListCommentReactions after remove: %v", err)
	}
	byEmoji := map[string]ReactionGroup{}
	for _, g := range groups {
		byEmoji[g.Emoji] = g
	}
	thumbs, ok := byEmoji["👍"]
	if !ok || thumbs.Count != 1 || thumbs.Reacted {
		t.Fatalf("after remove 👍 = %+v, want count 1 reacted false", thumbs)
	}
	party, ok := byEmoji["🎉"]
	if !ok || party.Count != 1 || !party.Reacted {
		t.Fatalf("after remove 🎉 = %+v, want count 1 reacted true", party)
	}

	// Bad emoji rejected on add.
	if err := AddCommentReaction(ctx, pool, wsSlug, ident, issueID, comment.ID, actor, "   "); err != ErrInvalidReaction {
		t.Fatalf("blank emoji err = %v, want ErrInvalidReaction", err)
	}

	// Unknown comment 404s.
	if _, err := ListCommentReactions(ctx, pool, wsSlug, ident, issueID, "00000000-0000-0000-0000-000000000000", actor); err != ErrCommentNotFound {
		t.Fatalf("unknown comment err = %v, want ErrCommentNotFound", err)
	}
}

func TestListCommentsEmbedsReactions(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	wsSlug, ident, issueID, actor := testSatelliteFixture(t, pool, "cmt-react-embed")

	root, err := CreateComment(ctx, pool, wsSlug, ident, issueID, actor,
		json.RawMessage(`{"type":"doc","content":[]}`), nil)
	if err != nil {
		t.Fatalf("CreateComment root: %v", err)
	}
	reply, err := CreateComment(ctx, pool, wsSlug, ident, issueID, actor,
		json.RawMessage(`{"type":"doc","content":[]}`), &root.ID)
	if err != nil {
		t.Fatalf("CreateComment reply: %v", err)
	}
	other := createTestUser(t, pool, uniqueTestEmail("cmt-react-embed-other"))
	addTestMember(t, pool, wsSlug, actor, other, 15)
	if err := AddCommentReaction(ctx, pool, wsSlug, ident, issueID, reply.ID, actor, "👍"); err != nil {
		t.Fatalf("react to reply: %v", err)
	}
	if err := AddCommentReaction(ctx, pool, wsSlug, ident, issueID, reply.ID, other, "👍"); err != nil {
		t.Fatalf("other react to reply: %v", err)
	}
	if err := AddCommentReaction(ctx, pool, wsSlug, ident, issueID, root.ID, other, "🎉"); err != nil {
		t.Fatalf("other react to root: %v", err)
	}

	tree, err := ListComments(ctx, pool, wsSlug, ident, issueID, actor)
	if err != nil {
		t.Fatalf("ListComments: %v", err)
	}
	if len(tree) != 1 {
		t.Fatalf("roots = %d, want 1", len(tree))
	}
	got := tree[0]
	if got.Reactions == nil || len(got.Reactions) != 1 {
		t.Fatalf("root reactions = %+v, want one 🎉 group", got.Reactions)
	}
	if got.Reactions[0].Emoji != "🎉" || got.Reactions[0].Count != 1 || got.Reactions[0].Reacted {
		t.Fatalf("root group = %+v, want 🎉 count 1 reacted false", got.Reactions[0])
	}
	if len(got.Replies) != 1 {
		t.Fatalf("replies = %d, want 1", len(got.Replies))
	}
	rg := got.Replies[0].Reactions
	if rg == nil || len(rg) != 1 {
		t.Fatalf("reply reactions = %+v, want one 👍 group", rg)
	}
	if rg[0].Emoji != "👍" || rg[0].Count != 2 || !rg[0].Reacted {
		t.Fatalf("reply group = %+v, want 👍 count 2 reacted true", rg[0])
	}

	// A comment with no reactions carries an empty (non-nil) slice, and
	// it serializes as "reactions":[] — never null, never absent.
	plain, err := CreateComment(ctx, pool, wsSlug, ident, issueID, actor,
		json.RawMessage(`{"type":"doc","content":[]}`), nil)
	if err != nil {
		t.Fatalf("CreateComment plain: %v", err)
	}
	tree, err = ListComments(ctx, pool, wsSlug, ident, issueID, actor)
	if err != nil {
		t.Fatalf("ListComments: %v", err)
	}
	var plainNode *CommentNode
	for _, n := range tree {
		if n.ID == plain.ID {
			plainNode = n
		}
	}
	if plainNode == nil {
		t.Fatal("plain comment missing from tree")
	}
	if plainNode.Reactions == nil || len(plainNode.Reactions) != 0 {
		t.Fatalf("plain reactions = %+v, want non-nil empty", plainNode.Reactions)
	}
	raw, err := json.Marshal(plainNode)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"reactions":[]`) {
		t.Fatalf("serialized comment missing \"reactions\":[] (body: %s)", raw)
	}
}
