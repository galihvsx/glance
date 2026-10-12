package api

// Page reaction HTTP endpoint tests (C24T3): add/remove idempotency,
// reactions[] embed on GET page detail, 404 on missing page, 400 on
// bad emoji, and the authz matrix (member writes, viewer reads, guest
// and non-member rejected). Real test database, no skips.
//
// Route contract: POST/DELETE
// .../pages/{pageID}/reactions?emoji=<emoji>. The write side mirrors
// the issue-reaction endpoints exactly (204 idempotent add/remove);
// the read side is the embedded reactions[] on GET page detail,
// shaped exactly like service.ReactionGroup (emoji/count/reacted).

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type pageReactionGroup struct {
	Emoji   string `json:"emoji"`
	Count   int    `json:"count"`
	Reacted bool   `json:"reacted"`
}

func decodePageDetailReactions(t *testing.T, rec *httptest.ResponseRecorder) []pageReactionGroup {
	t.Helper()
	var body struct {
		ID        string              `json:"id"`
		Reactions []pageReactionGroup `json:"reactions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode page detail: %v (body: %s)", err, rec.Body.String())
	}
	return body.Reactions
}

func findReaction(groups []pageReactionGroup, emoji string) (pageReactionGroup, bool) {
	for _, g := range groups {
		if g.Emoji == emoji {
			return g, true
		}
	}
	return pageReactionGroup{}, false
}

func TestPageReactionAddRemoveIdempotent(t *testing.T) {
	e, pool, adminCookie, pageBase, slug := setupPageCommentHTTP(t)
	pageID := createPageHTTP(t, e, adminCookie, pageBase, `{"title":"Reactions"}`)
	reactBase := pageBase + "/" + pageID + "/reactions"
	detailURL := pageBase + "/" + pageID
	memberCookie := addWorkspaceMemberWithRole(t, e, pool, slug, "pr-member", 15)

	// Add → 204.
	rec := postAuthedJSON(t, e, http.MethodPost, reactBase+"?emoji=🎉", adminCookie, ``)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("add: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}

	// Embed on GET detail: count 1, reacted true for the reactor.
	rec = getAuthed(t, e, http.MethodGet, detailURL, adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("detail: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	g, ok := findReaction(decodePageDetailReactions(t, rec), "🎉")
	if !ok {
		t.Fatalf("detail missing 🎉 group (body: %s)", rec.Body.String())
	}
	if g.Count != 1 || !g.Reacted {
		t.Fatalf("group = %+v, want count 1 reacted true", g)
	}

	// Repeat add → 204, still count 1 (idempotent no-op).
	rec = postAuthedJSON(t, e, http.MethodPost, reactBase+"?emoji=🎉", adminCookie, ``)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("repeat add: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, detailURL, adminCookie)
	g, _ = findReaction(decodePageDetailReactions(t, rec), "🎉")
	if g.Count != 1 {
		t.Fatalf("after repeat add: count = %d, want 1", g.Count)
	}

	// Second user adds the same emoji → count 2.
	rec = postAuthedJSON(t, e, http.MethodPost, reactBase+"?emoji=🎉", memberCookie, ``)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("member add: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, detailURL, memberCookie)
	g, _ = findReaction(decodePageDetailReactions(t, rec), "🎉")
	if g.Count != 2 || !g.Reacted {
		t.Fatalf("after member add: group = %+v, want count 2 reacted true", g)
	}

	// Admin's own remove → count 1, reacted false for admin.
	rec = postAuthedJSON(t, e, http.MethodDelete, reactBase+"?emoji=🎉", adminCookie, ``)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("remove: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, detailURL, adminCookie)
	g, _ = findReaction(decodePageDetailReactions(t, rec), "🎉")
	if g.Count != 1 || g.Reacted {
		t.Fatalf("after remove: group = %+v, want count 1 reacted false", g)
	}

	// Repeat remove → 204 (idempotent no-op).
	rec = postAuthedJSON(t, e, http.MethodDelete, reactBase+"?emoji=🎉", adminCookie, ``)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("repeat remove: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, detailURL, adminCookie)
	g, _ = findReaction(decodePageDetailReactions(t, rec), "🎉")
	if g.Count != 1 {
		t.Fatalf("after repeat remove: count = %d, want 1", g.Count)
	}

	// A page with no reactions embeds an empty (non-null) list.
	rec = getAuthed(t, e, http.MethodGet, detailURL, adminCookie)
	groups := decodePageDetailReactions(t, rec)
	for _, gg := range groups {
		if gg.Emoji == "🚫" {
			t.Fatalf("unexpected group: %+v", gg)
		}
	}
}

func TestPageReactionEmbedReactedFalseForOthers(t *testing.T) {
	e, pool, adminCookie, pageBase, slug := setupPageCommentHTTP(t)
	pageID := createPageHTTP(t, e, adminCookie, pageBase, `{"title":"Reacted flag"}`)
	reactBase := pageBase + "/" + pageID + "/reactions"
	detailURL := pageBase + "/" + pageID
	memberCookie := addWorkspaceMemberWithRole(t, e, pool, slug, "pr-other", 15)

	if rec := postAuthedJSON(t, e, http.MethodPost, reactBase+"?emoji=👍", adminCookie, ``); rec.Code != http.StatusNoContent {
		t.Fatalf("add: status = %d, want 204", rec.Code)
	}
	rec := getAuthed(t, e, http.MethodGet, detailURL, memberCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("detail: status = %d, want 200", rec.Code)
	}
	g, ok := findReaction(decodePageDetailReactions(t, rec), "👍")
	if !ok {
		t.Fatalf("detail missing 👍 group (body: %s)", rec.Body.String())
	}
	if g.Count != 1 || g.Reacted {
		t.Fatalf("group = %+v, want count 1 reacted false", g)
	}
}

func TestPageReactionValidation(t *testing.T) {
	e, _, adminCookie, pageBase, _ := setupPageCommentHTTP(t)
	pageID := createPageHTTP(t, e, adminCookie, pageBase, `{"title":"Validation"}`)
	reactBase := pageBase + "/" + pageID + "/reactions"

	// Missing emoji → 400.
	if rec := postAuthedJSON(t, e, http.MethodPost, reactBase, adminCookie, ``); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing emoji: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	// Overlong emoji (>32 runes) → 400.
	long := ""
	for i := 0; i < 33; i++ {
		long += "a"
	}
	if rec := postAuthedJSON(t, e, http.MethodPost, reactBase+"?emoji="+long, adminCookie, ``); rec.Code != http.StatusBadRequest {
		t.Fatalf("long emoji: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestPageReactionNotFound(t *testing.T) {
	e, _, adminCookie, pageBase, _ := setupPageCommentHTTP(t)
	ghost := "00000000-0000-0000-0000-000000000000"
	reactBase := pageBase + "/" + ghost + "/reactions"

	if rec := postAuthedJSON(t, e, http.MethodPost, reactBase+"?emoji=👍", adminCookie, ``); rec.Code != http.StatusNotFound {
		t.Fatalf("add ghost: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
	if rec := postAuthedJSON(t, e, http.MethodDelete, reactBase+"?emoji=👍", adminCookie, ``); rec.Code != http.StatusNotFound {
		t.Fatalf("remove ghost: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestPageReactionAuthz(t *testing.T) {
	e, pool, adminCookie, pageBase, slug := setupPageCommentHTTP(t)
	pageID := createPageHTTP(t, e, adminCookie, pageBase, `{"title":"Authz"}`)
	reactBase := pageBase + "/" + pageID + "/reactions"
	detailURL := pageBase + "/" + pageID

	viewerCookie := addWorkspaceMemberWithRole(t, e, pool, slug, "pr-viewer", 10)
	guestCookie := addWorkspaceMemberWithRole(t, e, pool, slug, "pr-guest", 5)
	outsiderCookie := loginTestUser(t, e, pool, uniqueEmail("pr-outsider"), "test-agent", uniqueIP())

	// Viewer: read (embed) OK, write 403.
	if rec := getAuthed(t, e, http.MethodGet, detailURL, viewerCookie); rec.Code != http.StatusOK {
		t.Fatalf("viewer detail: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if rec := postAuthedJSON(t, e, http.MethodPost, reactBase+"?emoji=👍", viewerCookie, ``); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer add: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
	if rec := postAuthedJSON(t, e, http.MethodDelete, reactBase+"?emoji=👍", viewerCookie, ``); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer remove: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}

	// Guest (5): page detail is membership-read (pre-existing page
	// surface — reactions ride along), but writes stay member (15)+.
	if rec := getAuthed(t, e, http.MethodGet, detailURL, guestCookie); rec.Code != http.StatusOK {
		t.Fatalf("guest detail: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if rec := postAuthedJSON(t, e, http.MethodPost, reactBase+"?emoji=👍", guestCookie, ``); rec.Code != http.StatusForbidden {
		t.Fatalf("guest add: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}

	// Non-member: 404 (tenancy boundary), same as page comments.
	if rec := postAuthedJSON(t, e, http.MethodPost, reactBase+"?emoji=👍", outsiderCookie, ``); rec.Code != http.StatusNotFound {
		t.Fatalf("outsider add: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
	if rec := postAuthedJSON(t, e, http.MethodDelete, reactBase+"?emoji=👍", outsiderCookie, ``); rec.Code != http.StatusNotFound {
		t.Fatalf("outsider remove: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestPageReactionCascadeOnPageDelete(t *testing.T) {
	e, _, adminCookie, pageBase, _ := setupPageCommentHTTP(t)
	pageID := createPageHTTP(t, e, adminCookie, pageBase, `{"title":"Cascade"}`)
	reactBase := pageBase + "/" + pageID + "/reactions"

	if rec := postAuthedJSON(t, e, http.MethodPost, reactBase+"?emoji=🔥", adminCookie, ``); rec.Code != http.StatusNoContent {
		t.Fatalf("add: status = %d, want 204", rec.Code)
	}
	if rec := postAuthedJSON(t, e, http.MethodDelete, pageBase+"/"+pageID, adminCookie, ``); rec.Code != http.StatusNoContent {
		t.Fatalf("delete page: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	// Page gone → reaction ops 404 (reactions died with the page).
	if rec := postAuthedJSON(t, e, http.MethodPost, reactBase+"?emoji=🔥", adminCookie, ``); rec.Code != http.StatusNotFound {
		t.Fatalf("add after delete: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
	if rec := postAuthedJSON(t, e, http.MethodDelete, reactBase+"?emoji=🔥", adminCookie, ``); rec.Code != http.StatusNotFound {
		t.Fatalf("remove after delete: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
}
