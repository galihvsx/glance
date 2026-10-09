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

func TestListMembersHTTP(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testWorkspaceServer(t, pool)

	adminCookie := loginTestUser(t, e, pool, uniqueEmail("h-ws-lm-admin"), "test-agent/1.0", uniqueIP())
	guestCookie := loginTestUser(t, e, pool, uniqueEmail("h-ws-lm-guest"), "test-agent/1.0", uniqueIP())
	outsiderCookie := loginTestUser(t, e, pool, uniqueEmail("h-ws-lm-out"), "test-agent/1.0", uniqueIP())
	slug := uniqueSlug("corp-members")
	membersPath := "/api/v1/workspaces/" + slug + "/members"

	createWorkspaceHTTP(t, e, adminCookie, "Corp", slug)
	guestID := authedUserID(t, e, guestCookie)
	rec := postAuthedJSON(t, e, http.MethodPost, membersPath, adminCookie,
		fmt.Sprintf(`{"user_id":%q,"role":5}`, guestID))
	if rec.Code != http.StatusOK {
		t.Fatalf("admin add guest: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	// Admin sees both members, wrapped shape {"members": [...]}.
	grec := getAuthed(t, e, http.MethodGet, membersPath, adminCookie)
	if grec.Code != http.StatusOK {
		t.Fatalf("list members: status = %d, want 200 (body: %s)", grec.Code, grec.Body.String())
	}
	var body struct {
		Members []struct {
			ID    string `json:"id"`
			Email string `json:"email"`
			Role  int    `json:"role"`
		} `json:"members"`
	}
	if err := json.Unmarshal(grec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode members: %v", err)
	}
	if len(body.Members) != 2 {
		t.Fatalf("members = %d, want 2", len(body.Members))
	}
	roles := map[string]int{}
	for _, m := range body.Members {
		roles[m.ID] = m.Role
		if m.Email == "" {
			t.Fatalf("member %s has empty email", m.ID)
		}
	}
	if roles[guestID] != 5 {
		t.Fatalf("guest role = %d, want 5", roles[guestID])
	}

	// Guest (member of workspace) may read too.
	grec = getAuthed(t, e, http.MethodGet, membersPath, guestCookie)
	if grec.Code != http.StatusOK {
		t.Fatalf("guest list members: status = %d, want 200", grec.Code)
	}

	// Outsider gets 404 (tenancy: never reveal the workspace exists).
	orec := getAuthed(t, e, http.MethodGet, membersPath, outsiderCookie)
	if orec.Code != http.StatusNotFound {
		t.Fatalf("outsider list members: status = %d, want 404", orec.Code)
	}
}

func TestWorkspacePatchSlugAndDeleteHTTP(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testWorkspaceServer(t, pool)

	cookie := loginTestUser(t, e, pool, uniqueEmail("h-ws-slugdel"), "test-agent/1.0", uniqueIP())
	slug := uniqueSlug("slug-http")
	createWorkspaceHTTP(t, e, cookie, "Sluggy", slug)

	// PATCH with a new slug: persists and returns the new slug.
	newSlug := uniqueSlug("slug-http-new")
	prec := postAuthedJSON(t, e, http.MethodPatch, "/api/v1/workspaces/"+slug, cookie,
		`{"name":"Sluggy","slug":"`+newSlug+`"}`)
	if prec.Code != http.StatusOK {
		t.Fatalf("patch slug: status = %d, want 200 (body: %s)", prec.Code, prec.Body.String())
	}
	var patched struct {
		Slug string `json:"slug"`
	}
	if err := json.Unmarshal(prec.Body.Bytes(), &patched); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	if patched.Slug != newSlug {
		t.Fatalf("slug = %q, want %q", patched.Slug, newSlug)
	}

	// Old slug is gone; new slug resolves.
	gone := getAuthed(t, e, http.MethodGet, "/api/v1/workspaces/"+slug, cookie)
	if gone.Code != http.StatusNotFound {
		t.Fatalf("old slug: status = %d, want 404", gone.Code)
	}

	// Invalid slug → 400.
	bad := postAuthedJSON(t, e, http.MethodPatch, "/api/v1/workspaces/"+newSlug, cookie,
		`{"name":"Sluggy","slug":"Bad_Slug!!"}`)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("bad slug: status = %d, want 400", bad.Code)
	}

	// DELETE → 204; workspace gone afterwards.
	drec := postAuthedJSON(t, e, http.MethodDelete, "/api/v1/workspaces/"+newSlug, cookie, ``)
	if drec.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d, want 204 (body: %s)", drec.Code, drec.Body.String())
	}
	after := getAuthed(t, e, http.MethodGet, "/api/v1/workspaces/"+newSlug, cookie)
	if after.Code != http.StatusNotFound {
		t.Fatalf("after delete: status = %d, want 404", after.Code)
	}
}
func TestInviteMembersEndpoint(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testWorkspaceServer(t, pool)

	adminEmail := uniqueEmail("h-ws-inv-admin")
	adminCookie := loginTestUser(t, e, pool, adminEmail, "test-agent/1.0", uniqueIP())
	targetEmail := uniqueEmail("h-ws-inv-target")
	loginTestUser(t, e, pool, targetEmail, "test-agent/1.0", uniqueIP())
	guestCookie := loginTestUser(t, e, pool, uniqueEmail("h-ws-inv-guest"), "test-agent/1.0", uniqueIP())

	slug := uniqueSlug("inv-http")
	createWorkspaceHTTP(t, e, adminCookie, "Inv", slug)
	invitesPath := "/api/v1/workspaces/" + slug + "/invites"
	ghost := uniqueEmail("h-ws-inv-ghost")

	rec := postAuthedJSON(t, e, http.MethodPost, invitesPath, adminCookie,
		fmt.Sprintf(`{"emails":[%q,%q],"role":15}`, targetEmail, ghost))
	if rec.Code != http.StatusOK {
		t.Fatalf("invite: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		Results []struct {
			Email  string `json:"email"`
			Status string `json:"status"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("results = %d, want 2", len(resp.Results))
	}
	byEmail := map[string]string{}
	for _, r := range resp.Results {
		byEmail[r.Email] = r.Status
	}
	if byEmail[targetEmail] != "invited" {
		t.Fatalf("registered: status = %q, want invited", byEmail[targetEmail])
	}
	if byEmail[ghost] != "not-registered" {
		t.Fatalf("unknown: status = %q, want not-registered", byEmail[ghost])
	}

	// Re-invite the same user → already-member, still 200.
	rec = postAuthedJSON(t, e, http.MethodPost, invitesPath, adminCookie,
		fmt.Sprintf(`{"emails":[%q]}`, targetEmail))
	if rec.Code != http.StatusOK {
		t.Fatalf("re-invite: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var resp2 struct {
		Results []struct {
			Email  string `json:"email"`
			Status string `json:"status"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp2.Results) != 1 || resp2.Results[0].Status != "already-member" {
		t.Fatalf("re-invite: results = %+v, want one already-member", resp2.Results)
	}

	// Non-admin member → 403. First add the guest as a plain member.
	guestID := authedUserID(t, e, guestCookie)
	rec = postAuthedJSON(t, e, http.MethodPost, "/api/v1/workspaces/"+slug+"/members", adminCookie,
		fmt.Sprintf(`{"user_id":%q,"role":15}`, guestID))
	if rec.Code != http.StatusOK {
		t.Fatalf("add guest member: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPost, invitesPath, guestCookie,
		fmt.Sprintf(`{"emails":[%q]}`, ghost))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("member invite: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}

	// Invalid role → 400.
	rec = postAuthedJSON(t, e, http.MethodPost, invitesPath, adminCookie,
		fmt.Sprintf(`{"emails":[%q],"role":99}`, ghost))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad role: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// Unknown workspace → 404, no existence hint.
	rec = postAuthedJSON(t, e, http.MethodPost, "/api/v1/workspaces/"+uniqueSlug("inv-nosuch")+"/invites", adminCookie,
		fmt.Sprintf(`{"emails":[%q]}`, ghost))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("bad slug: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
}
