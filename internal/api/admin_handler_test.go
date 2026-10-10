package api

// Admin HTTP endpoint tests (C5T0): the /api/v1/admin/* surface behind
// RequireAuth + RequireAdmin. Non-admins get 403 on every route;
// unauthenticated requests get 401. Covers stats shape, user
// promote/demote (self-demote 409), deactivate/reactivate (session
// invalidation + login rejection), workspace overview, and the
// typed-confirmation workspace delete. Real test database, no skips.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"glance/internal/config"
)

func testAdminServer(t *testing.T, pool *pgxpool.Pool) *echo.Echo {
	t.Helper()
	e := echo.New()
	h := &AuthHandler{
		Pool:   pool,
		Config: &config.Config{OTPPepper: "test-pepper-do-not-use-in-prod"},
	}
	RegisterAuthRoutes(e, h)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterAdminRoutes(e, &AdminHandler{Pool: pool})
	return e
}

// loginAdminUser performs a full OTP login and then promotes the user to
// instance admin. AuthenticateSession loads is_admin fresh per request,
// so the returned cookie is admin-capable immediately.
func loginAdminUser(t *testing.T, e *echo.Echo, pool *pgxpool.Pool, email string) *http.Cookie {
	t.Helper()
	cookie := loginTestUser(t, e, pool, email, "test-agent", uniqueIP())
	if _, err := pool.Exec(context.Background(),
		`UPDATE users SET is_admin = true WHERE email = $1`, strings.ToLower(email)); err != nil {
		t.Fatalf("promote test admin: %v", err)
	}
	return cookie
}

func adminPaths() []struct{ method, path string } {
	return []struct{ method, path string }{
		{http.MethodGet, "/api/v1/admin/stats"},
		{http.MethodGet, "/api/v1/admin/users"},
		{http.MethodPatch, "/api/v1/admin/users/00000000-0000-0000-0000-000000000000"},
		{http.MethodPost, "/api/v1/admin/users/00000000-0000-0000-0000-000000000000/deactivate"},
		{http.MethodPost, "/api/v1/admin/users/00000000-0000-0000-0000-000000000000/reactivate"},
		{http.MethodGet, "/api/v1/admin/workspaces"},
		{http.MethodDelete, "/api/v1/admin/workspaces/00000000-0000-0000-0000-000000000000?confirm=x"},
		{http.MethodGet, "/api/v1/admin/audit-log"},
	}
}

func TestAdminUnauthenticated401(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testAdminServer(t, pool)

	for _, tc := range adminPaths() {
		req := httptest.NewRequest(tc.method, tc.path,
			strings.NewReader(`{"is_admin":true}`))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: status = %d, want 401", tc.method, tc.path, rec.Code)
		}
	}
}

func TestAdminNonAdmin403(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testAdminServer(t, pool)

	cookie := loginTestUser(t, e, pool, uniqueEmail("nonadmin"), "test-agent", uniqueIP())
	for _, tc := range adminPaths() {
		rec := postAuthedJSON(t, e, tc.method, tc.path, cookie, `{"is_admin":true}`)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s: status = %d, want 403", tc.method, tc.path, rec.Code)
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Errorf("%s %s: body is not JSON: %v", tc.method, tc.path, err)
		}
	}
}

func TestAdminMeExposesIsAdmin(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testAdminServer(t, pool)

	adminCookie := loginAdminUser(t, e, pool, uniqueEmail("me-admin"))
	plainCookie := loginTestUser(t, e, pool, uniqueEmail("me-plain"), "test-agent", uniqueIP())

	for _, tc := range []struct {
		cookie *http.Cookie
		want   bool
	}{
		{adminCookie, true},
		{plainCookie, false},
	} {
		rec := getAuthed(t, e, http.MethodGet, "/api/v1/me", tc.cookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("/me: status = %d, want 200", rec.Code)
		}
		var me struct {
			IsAdmin *bool `json:"is_admin"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil {
			t.Fatalf("decode /me: %v", err)
		}
		if me.IsAdmin == nil || *me.IsAdmin != tc.want {
			t.Errorf("/me is_admin = %v, want %v", me.IsAdmin, tc.want)
		}
	}
}

func TestAdminStats200(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testAdminServer(t, pool)

	cookie := loginAdminUser(t, e, pool, uniqueEmail("stats-admin"))
	rec := getAuthed(t, e, http.MethodGet, "/api/v1/admin/stats", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var stats map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &stats); err != nil {
		t.Fatalf("decode stats: %v", err)
	}
	for _, key := range []string{"users", "workspaces", "projects", "issues", "attachment_bytes"} {
		v, ok := stats[key]
		if !ok {
			t.Errorf("stats missing key %q", key)
			continue
		}
		if _, ok := v.(float64); !ok {
			t.Errorf("stats[%q] = %v (%T), want number", key, v, v)
		}
	}
}

func TestAdminListUsersEnvelope(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testAdminServer(t, pool)

	cookie := loginAdminUser(t, e, pool, uniqueEmail("list-admin"))
	rec := getAuthed(t, e, http.MethodGet, "/api/v1/admin/users?per_page=2&page=1", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var env struct {
		Items []struct {
			ID             string `json:"id"`
			Email          string `json:"email"`
			IsAdmin        bool   `json:"is_admin"`
			IsActive       bool   `json:"is_active"`
			WorkspaceCount int64  `json:"workspace_count"`
			CreatedAt      string `json:"created_at"`
		} `json:"items"`
		Total   int64 `json:"total"`
		Page    int   `json:"page"`
		PerPage int   `json:"per_page"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if len(env.Items) != 2 {
		t.Errorf("len(items) = %d, want 2", len(env.Items))
	}
	if env.Total < 1 || env.Page != 1 || env.PerPage != 2 {
		t.Errorf("envelope = total:%d page:%d per_page:%d, want total>=1 page:1 per_page:2",
			env.Total, env.Page, env.PerPage)
	}
	for _, u := range env.Items {
		if u.ID == "" || u.Email == "" || u.CreatedAt == "" {
			t.Errorf("user item missing fields: %+v", u)
		}
	}

	// per_page contract: out of range is 400, never clamped.
	for _, q := range []string{"per_page=0", "per_page=101", "per_page=abc", "page=0"} {
		rec := getAuthed(t, e, http.MethodGet, "/api/v1/admin/users?"+q, cookie)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("GET /users?%s: status = %d, want 400", q, rec.Code)
		}
	}
}

func TestAdminPatchUserSelfDemote409(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testAdminServer(t, pool)

	email := uniqueEmail("selfdemote-admin")
	cookie := loginAdminUser(t, e, pool, email)
	selfID := authedUserID(t, e, cookie)

	// Self-demotion → 409.
	rec := postAuthedJSON(t, e, http.MethodPatch, "/api/v1/admin/users/"+selfID, cookie,
		`{"is_admin":false}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("self-demote: status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}

	// Promoting another user works; they show up as admin in the listing.
	victimEmail := uniqueEmail("promote-victim")
	victimCookie := loginTestUser(t, e, pool, victimEmail, "test-agent", uniqueIP())
	victimID := authedUserID(t, e, victimCookie)
	rec = postAuthedJSON(t, e, http.MethodPatch, "/api/v1/admin/users/"+victimID, cookie,
		`{"is_admin":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("promote: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, "/api/v1/me", victimCookie)
	var me struct {
		IsAdmin bool `json:"is_admin"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil {
		t.Fatalf("decode /me: %v", err)
	}
	if !me.IsAdmin {
		t.Error("promoted user: /me is_admin = false, want true")
	}

	// Missing is_admin field → 400 (explicitness required).
	rec = postAuthedJSON(t, e, http.MethodPatch, "/api/v1/admin/users/"+victimID, cookie, `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing is_admin: status = %d, want 400", rec.Code)
	}
	// Malformed UUID → 400.
	rec = postAuthedJSON(t, e, http.MethodPatch, "/api/v1/admin/users/not-a-uuid", cookie,
		`{"is_admin":true}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("malformed uuid: status = %d, want 400", rec.Code)
	}
	// Unknown UUID → 404.
	rec = postAuthedJSON(t, e, http.MethodPatch,
		"/api/v1/admin/users/00000000-0000-0000-0000-000000000000", cookie, `{"is_admin":true}`)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown user: status = %d, want 404", rec.Code)
	}
}

func TestAdminDeactivateReactivateFlow(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testAdminServer(t, pool)

	adminCookie := loginAdminUser(t, e, pool, uniqueEmail("deact-admin"))
	victimEmail := uniqueEmail("deact-victim")
	victimCookie := loginTestUser(t, e, pool, victimEmail, "test-agent", uniqueIP())
	victimID := authedUserID(t, e, victimCookie)

	// Deactivate → 200.
	rec := postAuthedJSON(t, e, http.MethodPost,
		"/api/v1/admin/users/"+victimID+"/deactivate", adminCookie, `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("deactivate: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	// The victim's session is dead immediately.
	if rec := getAuthed(t, e, http.MethodGet, "/api/v1/me", victimCookie); rec.Code != http.StatusUnauthorized {
		t.Errorf("victim /me after deactivate: status = %d, want 401", rec.Code)
	}
	// The victim cannot log back in: OTP verify rejects deactivated accounts
	// (covered at the auth layer by TestDeactivatedUserCannotVerifyOTP).

	// Reactivate → 200; a fresh login works again.
	rec = postAuthedJSON(t, e, http.MethodPost,
		"/api/v1/admin/users/"+victimID+"/reactivate", adminCookie, `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("reactivate: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	freshCookie := loginTestUser(t, e, pool, victimEmail, "test-agent", uniqueIP())
	if rec := getAuthed(t, e, http.MethodGet, "/api/v1/me", freshCookie); rec.Code != http.StatusOK {
		t.Errorf("victim /me after reactivate + fresh login: status = %d, want 200", rec.Code)
	}

	// Self-deactivation is refused.
	adminID := authedUserID(t, e, adminCookie)
	rec = postAuthedJSON(t, e, http.MethodPost,
		"/api/v1/admin/users/"+adminID+"/deactivate", adminCookie, `{}`)
	if rec.Code != http.StatusConflict {
		t.Errorf("self-deactivate: status = %d, want 409", rec.Code)
	}

	// Deactivating an unknown user → 404.
	rec = postAuthedJSON(t, e, http.MethodPost,
		"/api/v1/admin/users/00000000-0000-0000-0000-000000000000/deactivate", adminCookie, `{}`)
	if rec.Code != http.StatusNotFound {
		t.Errorf("deactivate unknown: status = %d, want 404", rec.Code)
	}
}

func TestAdminDeleteWorkspaceConfirm(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testAdminServer(t, pool)

	adminCookie := loginAdminUser(t, e, pool, uniqueEmail("delws-admin"))
	slug := uniqueSlug("adm-delws")
	createWorkspaceHTTP(t, e, adminCookie, "Typed Confirm WS", slug)

	wsPath := "/api/v1/admin/workspaces/" + slug

	// No confirm param → 400.
	rec := postAuthedJSON(t, e, http.MethodDelete, wsPath, adminCookie, ``)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("no confirm: status = %d, want 400", rec.Code)
	}
	// Wrong name → 409, workspace survives.
	rec = postAuthedJSON(t, e, http.MethodDelete, wsPath+"?confirm=Wrong+Name", adminCookie, ``)
	if rec.Code != http.StatusConflict {
		t.Errorf("wrong confirm: status = %d, want 409", rec.Code)
	}
	if rec := getAuthed(t, e, http.MethodGet, "/api/v1/workspaces/"+slug, adminCookie); rec.Code != http.StatusOK {
		t.Errorf("workspace should survive wrong confirm: GET status = %d, want 200", rec.Code)
	}
	// Unknown workspace → 404.
	rec = postAuthedJSON(t, e, http.MethodDelete,
		"/api/v1/admin/workspaces/"+uniqueSlug("nope")+"?confirm=nope", adminCookie, ``)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown workspace: status = %d, want 404", rec.Code)
	}
	// Exact name → 204, workspace gone.
	rec = postAuthedJSON(t, e, http.MethodDelete, wsPath+"?confirm=Typed+Confirm+WS", adminCookie, ``)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("correct confirm: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	if rec := getAuthed(t, e, http.MethodGet, "/api/v1/workspaces/"+slug, adminCookie); rec.Code != http.StatusNotFound {
		t.Errorf("workspace should be gone: GET status = %d, want 404", rec.Code)
	}
}

func TestAdminListWorkspacesEnvelope(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testAdminServer(t, pool)

	adminCookie := loginAdminUser(t, e, pool, uniqueEmail("wslist-admin"))
	slug := uniqueSlug("adm-wslist")
	createWorkspaceHTTP(t, e, adminCookie, "Overview WS", slug)

	rec := getAuthed(t, e, http.MethodGet, "/api/v1/admin/workspaces?per_page=5", adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var env struct {
		Items []struct {
			ID           string `json:"id"`
			Slug         string `json:"slug"`
			Name         string `json:"name"`
			MemberCount  int64  `json:"member_count"`
			ProjectCount int64  `json:"project_count"`
			IssueCount   int64  `json:"issue_count"`
			CreatedAt    string `json:"created_at"`
		} `json:"items"`
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Total < 1 {
		t.Errorf("total = %d, want >= 1", env.Total)
	}
	// The seeded workspace may sit past page one in the shared DB; page
	// through (bounded, 100/page) to find it deterministically.
	found := false
	for page := 1; page <= int(env.Total/100)+2 && !found; page++ {
		rec := getAuthed(t, e, http.MethodGet,
			fmt.Sprintf("/api/v1/admin/workspaces?per_page=100&page=%d", page), adminCookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("page %d: status = %d", page, rec.Code)
		}
		var penv struct {
			Items []struct {
				Slug        string `json:"slug"`
				Name        string `json:"name"`
				MemberCount int64  `json:"member_count"`
				CreatedAt   string `json:"created_at"`
			} `json:"items"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &penv); err != nil {
			t.Fatalf("decode page %d: %v", page, err)
		}
		if len(penv.Items) == 0 {
			break
		}
		for _, w := range penv.Items {
			if w.Slug == slug {
				found = true
				if w.Name != "Overview WS" || w.MemberCount < 1 || w.CreatedAt == "" {
					t.Errorf("workspace row wrong: %+v", w)
				}
			}
		}
	}
	if !found {
		t.Errorf("seeded workspace %q missing from listing", slug)
	}
}

// TestAdminAuditLogEnvelope (C15T1): GET /api/v1/admin/audit-log returns
// the page envelope; admin mutations performed over HTTP show up as
// rows, and the action/actor_id/entity_type filters plus validation
// behave like the other admin list endpoints.
func TestAdminAuditLogEnvelope(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testAdminServer(t, pool)

	adminCookie := loginAdminUser(t, e, pool, uniqueEmail("audit-h-admin"))
	victimCookie := loginTestUser(t, e, pool, uniqueEmail("audit-h-victim"), "test-agent", uniqueIP())
	victimID := authedUserID(t, e, victimCookie)
	adminID := authedUserID(t, e, adminCookie)

	// Deactivate the victim over HTTP — the handler path records the row.
	rec := postAuthedJSON(t, e, http.MethodPost,
		"/api/v1/admin/users/"+victimID+"/deactivate", adminCookie, `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("deactivate: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	var env struct {
		Items []struct {
			ID         string  `json:"id"`
			At         string  `json:"at"`
			ActorID    *string `json:"actor_id"`
			ActorEmail *string `json:"actor_email"`
			Action     string  `json:"action"`
			EntityType string  `json:"entity_type"`
			EntityID   string  `json:"entity_id"`
			IP         *string `json:"ip"`
		} `json:"items"`
		Total   int64 `json:"total"`
		Page    int   `json:"page"`
		PerPage int   `json:"per_page"`
	}
	decode := func(path string, wantCode int) {
		t.Helper()
		rec := getAuthed(t, e, http.MethodGet, path, adminCookie)
		if rec.Code != wantCode {
			t.Fatalf("GET %s: status = %d, want %d (body: %s)", path, rec.Code, wantCode, rec.Body.String())
		}
		if wantCode != http.StatusOK {
			return
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
	}

	// Filter to exactly this actor + action: one row.
	decode("/api/v1/admin/audit-log?action=user.deactivated&actor_id="+adminID+"&entity_type=user", http.StatusOK)
	if env.Total != 1 || len(env.Items) != 1 {
		t.Fatalf("filtered audit log: total=%d items=%d, want 1/1", env.Total, len(env.Items))
	}
	row := env.Items[0]
	if row.Action != "user.deactivated" || row.EntityType != "user" || row.EntityID != victimID {
		t.Errorf("row = %+v, want the deactivate mutation", row)
	}
	if row.ActorID == nil || *row.ActorID != adminID {
		t.Errorf("actor_id = %v, want %s", row.ActorID, adminID)
	}
	if row.ActorEmail == nil || *row.ActorEmail == "" {
		t.Error("actor_email not resolved")
	}
	if row.IP == nil || *row.IP == "" {
		t.Error("ip not recorded")
	}
	if row.At == "" {
		t.Error("at missing")
	}
	if env.Page != 1 || env.PerPage != 25 {
		t.Errorf("envelope page=%d per_page=%d, want 1/25", env.Page, env.PerPage)
	}

	// A non-matching action filter: empty items, total 0.
	decode("/api/v1/admin/audit-log?action=user.deactivated&actor_id="+victimID, http.StatusOK)
	if env.Total != 0 || len(env.Items) != 0 {
		t.Errorf("non-matching filter: total=%d items=%d, want 0/0", env.Total, len(env.Items))
	}

	// Malformed actor_id → 400, not a 500 from the ::uuid cast.
	decode("/api/v1/admin/audit-log?actor_id=not-a-uuid", http.StatusBadRequest)

	// Bad page → 400 like every other list endpoint.
	decode("/api/v1/admin/audit-log?page=0", http.StatusBadRequest)
}
