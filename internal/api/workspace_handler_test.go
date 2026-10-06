package api

// Workspace HTTP endpoint tests (Task 11): POST/GET/PATCH /api/v1/workspaces,
// member add/remove with role checks. Slug conflict → 409, guest managing
// members → 403, everything behind RequireAuth. Real test database, no skips.
//
// The test database is shared and never truncated, so every slug inserted
// is run-unique (uniqueSlug); fixed slugs would collide across runs.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
)

// wsTestSeq hands out process-unique numbers for workspace slugs.
var wsTestSeq atomic.Int64

// uniqueSlug returns a run-unique workspace slug.
func uniqueSlug(prefix string) string {
	return fmt.Sprintf("%s-%d-%d", prefix, os.Getpid(), wsTestSeq.Add(1))
}

func testWorkspaceServer(t *testing.T, pool *pgxpool.Pool) *echo.Echo {
	t.Helper()
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	return e
}

// postAuthedJSON issues an authenticated JSON request with the session cookie.
func postAuthedJSON(t *testing.T, e *echo.Echo, method, path string, cookie *http.Cookie, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// authedUserID returns the caller's user id via /me.
func authedUserID(t *testing.T, e *echo.Echo, cookie *http.Cookie) string {
	t.Helper()
	rec := getAuthed(t, e, http.MethodGet, "/api/v1/me", cookie)
	var me struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil {
		t.Fatalf("decode /me: %v", err)
	}
	if me.ID == "" {
		t.Fatal("/me returned empty id")
	}
	return me.ID
}

// createWorkspaceHTTP creates a workspace and returns its slug.
func createWorkspaceHTTP(t *testing.T, e *echo.Echo, cookie *http.Cookie, name, slug string) {
	t.Helper()
	body := fmt.Sprintf(`{"name":%q,"slug":%q}`, name, slug)
	rec := postAuthedJSON(t, e, http.MethodPost, "/api/v1/workspaces", cookie, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create workspace %q: status = %d, want 201 (body: %s)", slug, rec.Code, rec.Body.String())
	}
}

func TestWorkspaceUnauthenticated401(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testWorkspaceServer(t, pool)

	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/workspaces"},
		{http.MethodGet, "/api/v1/workspaces"},
		{http.MethodGet, "/api/v1/workspaces/x"},
		{http.MethodPatch, "/api/v1/workspaces/x"},
		{http.MethodPost, "/api/v1/workspaces/x/members"},
		{http.MethodDelete, "/api/v1/workspaces/x/members/y"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: status = %d, want 401", tc.method, tc.path, rec.Code)
		}
	}
}

func TestCreateWorkspaceSlugConflict409(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testWorkspaceServer(t, pool)
	cookie := loginTestUser(t, e, pool, uniqueEmail("h-ws"), "test-agent/1.0", uniqueIP())
	slug := uniqueSlug("acme-http")

	body := fmt.Sprintf(`{"name":"Acme","slug":%q}`, slug)
	rec := postAuthedJSON(t, e, http.MethodPost, "/api/v1/workspaces", cookie, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}

	// Same slug again → 409, not 500.
	rec = postAuthedJSON(t, e, http.MethodPost, "/api/v1/workspaces", cookie, body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate slug: status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get(echo.HeaderContentType); !strings.Contains(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want JSON", ct)
	}
}

func TestCreateWorkspaceInvalidSlug400(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testWorkspaceServer(t, pool)
	cookie := loginTestUser(t, e, pool, uniqueEmail("h-ws-bad"), "test-agent/1.0", uniqueIP())

	rec := postAuthedJSON(t, e, http.MethodPost, "/api/v1/workspaces", cookie, `{"name":"Bad","slug":"NOT-lower"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid slug: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestGuestCannotAddMember403(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testWorkspaceServer(t, pool)

	adminCookie := loginTestUser(t, e, pool, uniqueEmail("h-ws-admin"), "test-agent/1.0", uniqueIP())
	guestCookie := loginTestUser(t, e, pool, uniqueEmail("h-ws-guest"), "test-agent/1.0", uniqueIP())
	victimCookie := loginTestUser(t, e, pool, uniqueEmail("h-ws-victim"), "test-agent/1.0", uniqueIP())
	slug := uniqueSlug("corp-http")
	membersPath := "/api/v1/workspaces/" + slug + "/members"

	createWorkspaceHTTP(t, e, adminCookie, "Corp", slug)

	// Admin adds the guest.
	guestID := authedUserID(t, e, guestCookie)
	rec := postAuthedJSON(t, e, http.MethodPost, membersPath, adminCookie,
		fmt.Sprintf(`{"user_id":%q,"role":5}`, guestID))
	if rec.Code != http.StatusOK {
		t.Fatalf("admin add guest: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	// Guest tries to add the victim → 403.
	victimID := authedUserID(t, e, victimCookie)
	rec = postAuthedJSON(t, e, http.MethodPost, membersPath, guestCookie,
		fmt.Sprintf(`{"user_id":%q,"role":15}`, victimID))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest add member: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}

	// Victim must not have become a member: GET is 404 for non-members.
	vget := getAuthed(t, e, http.MethodGet, "/api/v1/workspaces/"+slug, victimCookie)
	if vget.Code != http.StatusNotFound {
		t.Fatalf("victim get workspace: status = %d, want 404 (body: %s)", vget.Code, vget.Body.String())
	}
}

func TestWorkspaceListAndPatch(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testWorkspaceServer(t, pool)

	cookie := loginTestUser(t, e, pool, uniqueEmail("h-ws-lp"), "test-agent/1.0", uniqueIP())
	slug := uniqueSlug("old-http")
	createWorkspaceHTTP(t, e, cookie, "Old", slug)

	// List shows the new workspace with the caller's admin role.
	lrec := getAuthed(t, e, http.MethodGet, "/api/v1/workspaces", cookie)
	if lrec.Code != http.StatusOK {
		t.Fatalf("list: status = %d, want 200", lrec.Code)
	}
	var list struct {
		Workspaces []struct {
			Slug string `json:"slug"`
			Role int    `json:"role"`
		} `json:"workspaces"`
	}
	if err := json.Unmarshal(lrec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Workspaces) != 1 || list.Workspaces[0].Slug != slug || list.Workspaces[0].Role != 20 {
		t.Fatalf("list = %+v, want [%s role=20]", list.Workspaces, slug)
	}

	// PATCH rename.
	prec := postAuthedJSON(t, e, http.MethodPatch, "/api/v1/workspaces/"+slug, cookie, `{"name":"New"}`)
	if prec.Code != http.StatusOK {
		t.Fatalf("patch: status = %d, want 200 (body: %s)", prec.Code, prec.Body.String())
	}
	var patched struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(prec.Body.Bytes(), &patched); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	if patched.Name != "New" {
		t.Fatalf("name = %q, want %q", patched.Name, "New")
	}
}

func TestAdminCannotRemoveLastAdminHTTP(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testWorkspaceServer(t, pool)

	cookie := loginTestUser(t, e, pool, uniqueEmail("h-ws-lock"), "test-agent/1.0", uniqueIP())
	slug := uniqueSlug("lock-http")
	createWorkspaceHTTP(t, e, cookie, "Lock", slug)

	meID := authedUserID(t, e, cookie)
	drec := postAuthedJSON(t, e, http.MethodDelete, "/api/v1/workspaces/"+slug+"/members/"+meID, cookie, "")
	if drec.Code != http.StatusConflict {
		t.Fatalf("remove sole admin: status = %d, want 409 (body: %s)", drec.Code, drec.Body.String())
	}

	// Workspace must be untouched: the admin still lists it.
	lrec := getAuthed(t, e, http.MethodGet, "/api/v1/workspaces", cookie)
	var list struct {
		Workspaces []struct {
			Slug string `json:"slug"`
		} `json:"workspaces"`
	}
	if err := json.Unmarshal(lrec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Workspaces) != 1 {
		t.Fatalf("workspaces after blocked removal = %d, want 1", len(list.Workspaces))
	}
}

// randomAbsentUUID returns a UUID guaranteed to match no user row.
func randomAbsentUUID(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `SELECT gen_random_uuid()::text`).Scan(&id); err != nil {
		t.Fatalf("gen_random_uuid: %v", err)
	}
	return id
}

// assertErrorMessage pins the spec §5 envelope {"error":{"code":…,"message":…}}
// of an error response: both the machine-readable code and the human
// message must be exact.
func assertErrorMessage(t *testing.T, body []byte, wantCode, wantMessage string) {
	t.Helper()
	var decoded struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode error body %q: %v", body, err)
	}
	if decoded.Error.Code != wantCode {
		t.Fatalf("error code = %q, want %q", decoded.Error.Code, wantCode)
	}
	if decoded.Error.Message != wantMessage {
		t.Fatalf("error message = %q, want %q", decoded.Error.Message, wantMessage)
	}
}

func TestUpsertMemberUnknownUser404(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testWorkspaceServer(t, pool)
	cookie := loginTestUser(t, e, pool, uniqueEmail("h-ws-nouser"), "test-agent/1.0", uniqueIP())
	slug := uniqueSlug("nouser-http")
	createWorkspaceHTTP(t, e, cookie, "NoUser", slug)

	// Admin typoing a user_id: 404 "user not found" — NOT "workspace not
	// found" (the workspace exists and the caller is a confirmed admin).
	unknown := randomAbsentUUID(t, pool)
	rec := postAuthedJSON(t, e, http.MethodPost, "/api/v1/workspaces/"+slug+"/members", cookie,
		fmt.Sprintf(`{"user_id":%q,"role":15}`, unknown))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("upsert unknown user: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
	assertErrorMessage(t, rec.Body.Bytes(), "user_not_found", "user not found")
}

func TestRemoveNonMember404(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testWorkspaceServer(t, pool)
	adminCookie := loginTestUser(t, e, pool, uniqueEmail("h-ws-nomem"), "test-agent/1.0", uniqueIP())
	outsiderCookie := loginTestUser(t, e, pool, uniqueEmail("h-ws-nomem-out"), "test-agent/1.0", uniqueIP())
	slug := uniqueSlug("nomem-http")
	createWorkspaceHTTP(t, e, adminCookie, "NoMember", slug)

	// Existing user who is not a member → 404 "member not found".
	outsiderID := authedUserID(t, e, outsiderCookie)
	rec := postAuthedJSON(t, e, http.MethodDelete,
		"/api/v1/workspaces/"+slug+"/members/"+outsiderID, adminCookie, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("remove non-member: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
	assertErrorMessage(t, rec.Body.Bytes(), "member_not_found", "member not found")

	// Nonexistent user entirely → same "member not found".
	rec = postAuthedJSON(t, e, http.MethodDelete,
		"/api/v1/workspaces/"+slug+"/members/"+randomAbsentUUID(t, pool), adminCookie, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("remove unknown user: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
	assertErrorMessage(t, rec.Body.Bytes(), "member_not_found", "member not found")
}

func TestWorkspaceNotFoundMessageUnchanged(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testWorkspaceServer(t, pool)
	cookie := loginTestUser(t, e, pool, uniqueEmail("h-ws-nfmsg"), "test-agent/1.0", uniqueIP())

	// The actor-resolution path (bad slug) still reports "workspace not
	// found" — the new sentinels must not have disturbed it.
	rec := getAuthed(t, e, http.MethodGet, "/api/v1/workspaces/no-such-workspace-xyz", cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing workspace: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
	assertErrorMessage(t, rec.Body.Bytes(), "not_found", "workspace not found")
}
