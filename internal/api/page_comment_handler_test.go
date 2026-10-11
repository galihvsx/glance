package api

// Page comment HTTP endpoint tests (C18T2): CRUD through the routes,
// body validation (same rule as issue comments), the authz matrix
// (author vs other member vs admin vs viewer vs guest vs non-member),
// author NULLing on user delete, and cascade on page delete. Real test
// database, no skips.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
)

func setupPageCommentHTTP(t *testing.T) (e *echo.Echo, pool *pgxpool.Pool, adminCookie *http.Cookie, pageBase, slug string) {
	t.Helper()
	pool = newTestPool(t)
	migrateTestDB(t, pool)
	e = testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterPageRoutes(e, &IssueHandler{Pool: pool})

	adminCookie = loginTestUser(t, e, pool, uniqueEmail("pc-admin"), "test-agent", uniqueIP())
	slug = uniqueSlug("page-comment")
	createWorkspaceHTTP(t, e, adminCookie, "Page Co", slug)
	ident := uniqueProjectIdentifier("PC")
	createProjectHTTP(t, e, adminCookie, slug, "Engineering", ident)
	pageBase = "/api/v1/workspaces/" + slug + "/projects/" + ident + "/pages"
	return e, pool, adminCookie, pageBase, slug
}

// addWorkspaceMemberWithRole creates a second user, adds them to the
// workspace with the given role, and returns their session cookie.
func addWorkspaceMemberWithRole(t *testing.T, e *echo.Echo, pool *pgxpool.Pool, slug string, prefix string, role int) *http.Cookie {
	t.Helper()
	email := uniqueEmail(prefix)
	cookie := loginTestUser(t, e, pool, email, "test-agent", uniqueIP())
	var wsID, userID string
	if err := pool.QueryRow(t.Context(), `SELECT id::text FROM workspaces WHERE slug = $1`, slug).Scan(&wsID); err != nil {
		t.Fatalf("workspace id: %v", err)
	}
	if err := pool.QueryRow(t.Context(), `SELECT id::text FROM users WHERE email = $1`, email).Scan(&userID); err != nil {
		t.Fatalf("user id: %v", err)
	}
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1::uuid, $2::uuid, $3)`, wsID, userID, role); err != nil {
		t.Fatalf("add member role %d: %v", role, err)
	}
	return cookie
}

const pageCommentDoc = `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Nice page"}]}]}`

type pageCommentJSON struct {
	ID     string `json:"id"`
	PageID string `json:"page_id"`
	Author *struct {
		ID    string  `json:"id"`
		Name  *string `json:"name"`
		Email string  `json:"email"`
	} `json:"author"`
	Body      json.RawMessage `json:"body"`
	CreatedAt string          `json:"created_at"`
}

func postPageComment(t *testing.T, e *echo.Echo, cookie *http.Cookie, commentsBase, body string) *httptest.ResponseRecorder {
	t.Helper()
	return postAuthedJSON(t, e, http.MethodPost, commentsBase, cookie, `{"body":`+body+`}`)
}

func decodePageComment(t *testing.T, rec *httptest.ResponseRecorder) pageCommentJSON {
	t.Helper()
	var c pageCommentJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil {
		t.Fatalf("decode comment: %v (body: %s)", err, rec.Body.String())
	}
	return c
}

func TestPageCommentCRUD(t *testing.T) {
	e, _, adminCookie, pageBase, _ := setupPageCommentHTTP(t)
	pageID := createPageHTTP(t, e, adminCookie, pageBase, `{"title":"Docs"}`)
	commentsBase := pageBase + "/" + pageID + "/comments"

	// Create → 201, body round-trips, author attributed.
	rec := postPageComment(t, e, adminCookie, commentsBase, pageCommentDoc)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create comment: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	created := decodePageComment(t, rec)
	if created.ID == "" || created.PageID != pageID {
		t.Fatalf("created comment = %+v, want id set and page_id %s", created, pageID)
	}
	if created.Author == nil || created.Author.Email == "" {
		t.Fatalf("created comment has no author: %s", rec.Body.String())
	}
	if strings.TrimSpace(string(created.Body)) != pageCommentDoc {
		// JSONB normalizes key order; compare semantically.
		var want, got any
		if err := json.Unmarshal([]byte(pageCommentDoc), &want); err != nil {
			t.Fatalf("unmarshal want: %v", err)
		}
		if err := json.Unmarshal(created.Body, &got); err != nil {
			t.Fatalf("unmarshal got: %v", err)
		}
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("body = %s, want %s", created.Body, pageCommentDoc)
		}
	}

	// List → 200, one comment.
	rec = getAuthed(t, e, http.MethodGet, commentsBase, adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list comments: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var list struct {
		Comments []pageCommentJSON `json:"comments"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Comments) != 1 || list.Comments[0].ID != created.ID {
		t.Fatalf("list = %+v, want [created]", list.Comments)
	}

	// Patch → 200, body replaced.
	rec = postAuthedJSON(t, e, http.MethodPatch, commentsBase+"/"+created.ID, adminCookie,
		`{"body":{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Edited"}]}]}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch comment: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if got := decodePageComment(t, rec); !strings.Contains(string(got.Body), "Edited") {
		t.Fatalf("patched body = %s, want Edited", got.Body)
	}

	// Delete → 204; second delete → 404.
	rec = postAuthedJSON(t, e, http.MethodDelete, commentsBase+"/"+created.ID, adminCookie, ``)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete comment: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodDelete, commentsBase+"/"+created.ID, adminCookie, ``)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("second delete: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}

	// List is empty again.
	rec = getAuthed(t, e, http.MethodGet, commentsBase, adminCookie)
	var list2 struct {
		Comments []pageCommentJSON `json:"comments"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list2); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list2.Comments) != 0 {
		t.Fatalf("list after delete = %d comments, want 0", len(list2.Comments))
	}
}

func TestPageCommentValidation(t *testing.T) {
	e, _, adminCookie, pageBase, _ := setupPageCommentHTTP(t)
	pageID := createPageHTTP(t, e, adminCookie, pageBase, `{"title":"Docs"}`)
	commentsBase := pageBase + "/" + pageID + "/comments"

	// Empty / null bodies → 400 invalid comment (the same rule as issue
	// comments: only a zero-byte payload or JSON null is rejected).
	for _, tc := range []struct {
		name string
		body string
	}{
		{"empty object", `{}`},
		{"null body", `{"body":null}`},
		{"whitespace raw", `   `},
	} {
		rec := postAuthedJSON(t, e, http.MethodPost, commentsBase, adminCookie, tc.body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400 (body: %s)", tc.name, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"code":"bad_request"`) {
			t.Fatalf("%s: 400 envelope: body=%s", tc.name, rec.Body.String())
		}
	}

	// Parity with issue comments: a JSON empty-string body is accepted
	// (only zero-byte / JSON-null payloads are rejected there too).
	rec := postAuthedJSON(t, e, http.MethodPost, commentsBase, adminCookie, `{"body":""}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("empty-string body: status = %d, want 201 like issue comments (body: %s)", rec.Code, rec.Body.String())
	}

	// Malformed page id → 400; well-formed but missing page → 404.
	rec = getAuthed(t, e, http.MethodGet, pageBase+"/nope/comments", adminCookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad page id: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, pageBase+"/00000000-0000-0000-0000-000000000000/comments", adminCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing page: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}

	// Malformed comment id on PATCH → 400.
	rec = postAuthedJSON(t, e, http.MethodPatch, commentsBase+"/nope", adminCookie, `{"body":`+pageCommentDoc+`}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad comment id: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestPageCommentAuthzMatrix(t *testing.T) {
	e, pool, adminCookie, pageBase, slug := setupPageCommentHTTP(t)
	pageID := createPageHTTP(t, e, adminCookie, pageBase, `{"title":"Docs"}`)
	commentsBase := pageBase + "/" + pageID + "/comments"

	memberCookie := addWorkspaceMemberWithRole(t, e, pool, slug, "pc-member", 15)
	otherCookie := addWorkspaceMemberWithRole(t, e, pool, slug, "pc-other", 15)
	viewerCookie := addWorkspaceMemberWithRole(t, e, pool, slug, "pc-viewer", 10)
	guestCookie := addWorkspaceMemberWithRole(t, e, pool, slug, "pc-guest", 5)
	outsiderCookie := loginTestUser(t, e, pool, uniqueEmail("pc-outsider"), "test-agent", uniqueIP())

	// Member writes → 201.
	rec := postPageComment(t, e, memberCookie, commentsBase, pageCommentDoc)
	if rec.Code != http.StatusCreated {
		t.Fatalf("member create: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	memberComment := decodePageComment(t, rec)

	// Other member: read OK, write OK, but touching someone else's
	// comment is 403.
	rec = getAuthed(t, e, http.MethodGet, commentsBase, otherCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("other member list: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPatch, commentsBase+"/"+memberComment.ID, otherCookie, `{"body":`+pageCommentDoc+`}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("other member patch: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodDelete, commentsBase+"/"+memberComment.ID, otherCookie, ``)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("other member delete: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}

	// Author edits and deletes their own → 200 / 204.
	rec = postAuthedJSON(t, e, http.MethodPatch, commentsBase+"/"+memberComment.ID, memberCookie, `{"body":`+pageCommentDoc+`}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("author patch: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	otherRec := postPageComment(t, e, otherCookie, commentsBase, pageCommentDoc)
	if otherRec.Code != http.StatusCreated {
		t.Fatalf("other member create: status = %d, want 201", otherRec.Code)
	}
	otherComment := decodePageComment(t, otherRec)

	// Admin edits and deletes someone else's comment → 200 / 204.
	rec = postAuthedJSON(t, e, http.MethodPatch, commentsBase+"/"+otherComment.ID, adminCookie, `{"body":`+pageCommentDoc+`}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin patch other's: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodDelete, commentsBase+"/"+otherComment.ID, adminCookie, ``)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("admin delete other's: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}

	// Viewer: read OK, write 403 (create and patch).
	rec = getAuthed(t, e, http.MethodGet, commentsBase, viewerCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("viewer list: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postPageComment(t, e, viewerCookie, commentsBase, pageCommentDoc)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer create: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPatch, commentsBase+"/"+memberComment.ID, viewerCookie, `{"body":`+pageCommentDoc+`}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer patch: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}

	// Guest: read 403 (viewer(10)+ to read), write 403.
	rec = getAuthed(t, e, http.MethodGet, commentsBase, guestCookie)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest list: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postPageComment(t, e, guestCookie, commentsBase, pageCommentDoc)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest create: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}

	// Non-member: 404 workspace-not-found (tenancy boundary).
	rec = getAuthed(t, e, http.MethodGet, commentsBase, outsiderCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("outsider list: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestPageCommentCascadeOnPageDelete(t *testing.T) {
	e, pool, adminCookie, pageBase, _ := setupPageCommentHTTP(t)
	pageID := createPageHTTP(t, e, adminCookie, pageBase, `{"title":"Docs"}`)
	commentsBase := pageBase + "/" + pageID + "/comments"

	rec := postPageComment(t, e, adminCookie, commentsBase, pageCommentDoc)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create comment: status = %d", rec.Code)
	}

	// Deleting the page removes its comments via ON DELETE CASCADE.
	rec = postAuthedJSON(t, e, http.MethodDelete, pageBase+"/"+pageID, adminCookie, ``)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete page: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	var n int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM page_comments WHERE page_id = $1::uuid`, pageID).Scan(&n); err != nil {
		t.Fatalf("count comments: %v", err)
	}
	if n != 0 {
		t.Fatalf("page_comments rows after page delete = %d, want 0", n)
	}
}

func TestPageCommentAuthorNullOnUserDelete(t *testing.T) {
	e, pool, adminCookie, pageBase, slug := setupPageCommentHTTP(t)
	pageID := createPageHTTP(t, e, adminCookie, pageBase, `{"title":"Docs"}`)
	commentsBase := pageBase + "/" + pageID + "/comments"

	memberEmail := uniqueEmail("pc-author-null")
	memberCookie := loginTestUser(t, e, pool, memberEmail, "test-agent", uniqueIP())
	var wsID, userID string
	if err := pool.QueryRow(t.Context(), `SELECT id::text FROM workspaces WHERE slug = $1`, slug).Scan(&wsID); err != nil {
		t.Fatalf("workspace id: %v", err)
	}
	if err := pool.QueryRow(t.Context(), `SELECT id::text FROM users WHERE email = $1`, memberEmail).Scan(&userID); err != nil {
		t.Fatalf("user id: %v", err)
	}
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1::uuid, $2::uuid, 15)`, wsID, userID); err != nil {
		t.Fatalf("add member: %v", err)
	}

	rec := postPageComment(t, e, memberCookie, commentsBase, pageCommentDoc)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create comment: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	// Deleting the user NULLs the author (ON DELETE SET NULL); the
	// comment survives and lists with author: null.
	if _, err := pool.Exec(t.Context(), `DELETE FROM users WHERE id = $1::uuid`, userID); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	rec = getAuthed(t, e, http.MethodGet, commentsBase, adminCookie)
	var list struct {
		Comments []pageCommentJSON `json:"comments"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Comments) != 1 {
		t.Fatalf("list = %d comments, want 1", len(list.Comments))
	}
	if list.Comments[0].Author != nil {
		t.Fatalf("author = %+v, want null after user delete", list.Comments[0].Author)
	}
}
