package api

// Comment reaction list HTTP endpoint tests (C67T0): GET
// .../issues/{uuid}/comments/{commentID}/reactions returns the emoji
// groups (count + caller's reacted state), an empty reactions array when
// there are none, 404 on an unknown comment, 400 on a malformed
// comment id. The list endpoint also embeds reactions[] on every
// comment — always an array, never null. Real test database, no skips.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

type commentReactionGroup struct {
	Emoji   string `json:"emoji"`
	Count   int    `json:"count"`
	Reacted bool   `json:"reacted"`
}

func decodeCommentReactionList(t *testing.T, body []byte) []commentReactionGroup {
	t.Helper()
	var env struct {
		Reactions []commentReactionGroup `json:"reactions"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode reaction list: %v (body: %s)", err, string(body))
	}
	return env.Reactions
}

func TestCommentReactionListEndpoint(t *testing.T) {
	pool := newTestPool(t)
	e := testSatelliteServer(t, pool)

	cookie := loginTestUser(t, e, pool, uniqueEmail("cmt-react-http"), "test-agent", uniqueIP())
	slug := uniqueSlug("cmt-react-http")
	createWorkspaceHTTP(t, e, cookie, "React Co", slug)
	ident := uniqueProjectIdentifier("CR")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	base := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/issues"

	issueID := createIssueHTTP(t, e, cookie, base, "Reaction host")
	commentsBase := base + "/" + issueID + "/comments"

	// Create a comment.
	rec := postAuthedJSON(t, e, http.MethodPost, commentsBase, cookie,
		`{"content":{"type":"doc","content":[]}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create comment: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode comment: %v", err)
	}
	// Bare single-comment responses carry reactions:[] — always
	// present, never null — and no other new keys.
	var createdKeys map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &createdKeys); err != nil {
		t.Fatalf("decode comment keys: %v", err)
	}
	wantKeys := []string{"id", "issue_id", "content", "actor", "created_at", "updated_at", "resolved_at", "resolved_by", "reactions"}
	if len(createdKeys) != len(wantKeys) {
		t.Fatalf("create response has %d keys, want %d", len(createdKeys), len(wantKeys))
	}
	for _, k := range wantKeys {
		if _, ok := createdKeys[k]; !ok {
			t.Fatalf("create response missing key %q", k)
		}
	}
	if string(createdKeys["reactions"]) != "[]" {
		t.Fatalf("create reactions = %s, want []", string(createdKeys["reactions"]))
	}
	reactBase := commentsBase + "/" + created.ID + "/reactions"

	// No reactions yet: empty array, not null.
	rec = getAuthed(t, e, http.MethodGet, reactBase, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"reactions":[]`) {
		t.Fatalf("empty list body = %s, want {\"reactions\":[]}", rec.Body.String())
	}

	// Add via the existing POST endpoint, then list.
	rec = postAuthedJSON(t, e, http.MethodPost, reactBase, cookie, `{"emoji":"👍"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("add: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, reactBase, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	groups := decodeCommentReactionList(t, rec.Body.Bytes())
	if len(groups) != 1 || groups[0].Emoji != "👍" || groups[0].Count != 1 || !groups[0].Reacted {
		t.Fatalf("groups = %+v, want one 👍 count 1 reacted true", groups)
	}

	// The comment list embeds the same group.
	rec = getAuthed(t, e, http.MethodGet, commentsBase, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list comments: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var tree struct {
		Comments []map[string]json.RawMessage `json:"comments"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &tree); err != nil {
		t.Fatalf("decode comment list: %v", err)
	}
	if len(tree.Comments) != 1 {
		t.Fatalf("comments = %d, want 1", len(tree.Comments))
	}
	raw, ok := tree.Comments[0]["reactions"]
	if !ok {
		t.Fatalf("comment missing reactions key (body: %s)", rec.Body.String())
	}
	embedded := decodeCommentReactionList(t, append([]byte(`{"reactions":`), append(raw, '}')...))
	if len(embedded) != 1 || embedded[0].Emoji != "👍" || embedded[0].Count != 1 || !embedded[0].Reacted {
		t.Fatalf("embedded = %+v, want one 👍 count 1 reacted true", embedded)
	}

	// Remove via DELETE, then list is empty again.
	rec = postAuthedJSON(t, e, http.MethodDelete, reactBase+"?emoji=👍", cookie, ``)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("remove: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, reactBase, cookie)
	if !strings.Contains(rec.Body.String(), `"reactions":[]`) {
		t.Fatalf("after remove body = %s, want {\"reactions\":[]}", rec.Body.String())
	}

	// Unknown comment → 404.
	rec = getAuthed(t, e, http.MethodGet, commentsBase+"/00000000-0000-0000-0000-000000000000/reactions", cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown comment: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}

	// Malformed comment id → 400.
	rec = getAuthed(t, e, http.MethodGet, commentsBase+"/not-a-uuid/reactions", cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed comment id: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// Bad emoji on POST → 400 (existing add path, pinned here as the
	// write-side partner of the new list route).
	rec = postAuthedJSON(t, e, http.MethodPost, reactBase, cookie, `{"emoji":""}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("blank emoji: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

// TestIssueReactionTrioEndpoint (C67T0 audit pin): the issue-level
// reaction trio is complete and correct over HTTP — GET lists groups
// (empty array when none), POST validates the emoji (400 on blank),
// add/remove are idempotent, and unknown issues 404.
func TestIssueReactionTrioEndpoint(t *testing.T) {
	pool := newTestPool(t)
	e := testSatelliteServer(t, pool)

	cookie := loginTestUser(t, e, pool, uniqueEmail("issue-react-http"), "test-agent", uniqueIP())
	slug := uniqueSlug("issue-react-http")
	createWorkspaceHTTP(t, e, cookie, "React Co", slug)
	ident := uniqueProjectIdentifier("IR")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	base := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/issues"

	issueID := createIssueHTTP(t, e, cookie, base, "Issue reaction host")
	reactBase := base + "/" + issueID + "/reactions"

	// Empty list: {"reactions":[]}.
	rec := getAuthed(t, e, http.MethodGet, reactBase, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"reactions":[]`) {
		t.Fatalf("empty list body = %s, want {\"reactions\":[]}", rec.Body.String())
	}

	// Blank emoji → 400.
	rec = postAuthedJSON(t, e, http.MethodPost, reactBase, cookie, `{"emoji":""}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("blank emoji: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// Add → 204; list shows the group.
	rec = postAuthedJSON(t, e, http.MethodPost, reactBase, cookie, `{"emoji":"🚀"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("add: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPost, reactBase, cookie, `{"emoji":"🚀"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("repeat add: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, reactBase, cookie)
	groups := decodeCommentReactionList(t, rec.Body.Bytes())
	if len(groups) != 1 || groups[0].Emoji != "🚀" || groups[0].Count != 1 || !groups[0].Reacted {
		t.Fatalf("groups = %+v, want one 🚀 count 1 reacted true", groups)
	}

	// Remove → 204; repeat remove → 204; list empty again.
	rec = postAuthedJSON(t, e, http.MethodDelete, reactBase+"?emoji=🚀", cookie, ``)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("remove: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodDelete, reactBase+"?emoji=🚀", cookie, ``)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("repeat remove: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, reactBase, cookie)
	if !strings.Contains(rec.Body.String(), `"reactions":[]`) {
		t.Fatalf("after remove body = %s, want {\"reactions\":[]}", rec.Body.String())
	}

	// Unknown issue → 404.
	rec = getAuthed(t, e, http.MethodGet, base+"/00000000-0000-0000-0000-000000000000/reactions", cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown issue: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
}
