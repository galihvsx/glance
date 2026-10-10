package api

// Notification + webhook HTTP endpoint tests (Task 26): the notification
// list/mark-read flow end to end (assign via HTTP → assignee sees the
// notification), prefs get/set, and webhook CRUD with the admin gate.
// Real test database, no skips.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
)

func testNotifyServer(t *testing.T, pool *pgxpool.Pool) *echo.Echo {
	t.Helper()
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterTaxonomyRoutes(e, &IssueHandler{Pool: pool})
	RegisterNotifyRoutes(e, &NotifyHandler{Pool: pool})
	return e
}

func TestNotifyHTTPEndpoints(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testNotifyServer(t, pool)

	cookieA := loginTestUser(t, e, pool, uniqueEmail("notify-a"), "ua", "10.9.0.1")
	userAID := authedUserID(t, e, cookieA)
	cookieB := loginTestUser(t, e, pool, uniqueEmail("notify-b"), "ua", "10.9.0.2")
	userBID := authedUserID(t, e, cookieB)
	_ = userAID

	slug := uniqueSlug("notify-http")
	createWorkspaceHTTP(t, e, cookieA, "Notify Co", slug)
	ident := uniqueProjectIdentifier("NH")
	createProjectHTTP(t, e, cookieA, slug, "Engineering", ident)
	base := "/api/v1/workspaces/" + slug + "/projects/" + ident

	// B joins as member (creator A is admin).
	rec := postAuthedJSON(t, e, http.MethodPost, "/api/v1/workspaces/"+slug+"/members", cookieA,
		fmt.Sprintf(`{"user_id":%q,"role":15}`, userBID))
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("add member: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	// A creates an issue, then assigns B via HTTP.
	rec = postAuthedJSON(t, e, http.MethodPost, base+"/issues", cookieA, `{"name":"Notify me"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create issue: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	rec = postAuthedJSON(t, e, http.MethodPost,
		base+"/issues/"+created.ID+"/assignees/"+userBID, cookieA, `{}`)
	if rec.Code != http.StatusOK && rec.Code != http.StatusNoContent {
		t.Fatalf("assign: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	// B lists notifications → 1 unread, type issue.assigned.
	rec = getAuthed(t, e, http.MethodGet, "/api/v1/notifications", cookieB)
	if rec.Code != http.StatusOK {
		t.Fatalf("list notifications: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var list struct {
		Notifications []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"notifications"`
		UnreadCount int `json:"unread_count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Notifications) != 1 || list.UnreadCount != 1 {
		t.Fatalf("list = %+v, want 1 notification / unread 1", list)
	}
	if list.Notifications[0].Type != "issue.assigned" {
		t.Fatalf("type = %q, want issue.assigned", list.Notifications[0].Type)
	}

	// B marks it read → unread drops to 0.
	rec = postAuthedJSON(t, e, http.MethodPost,
		"/api/v1/notifications/"+list.Notifications[0].ID+"/read", cookieB, `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("mark read: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, "/api/v1/notifications?unread_only=true", cookieB)
	var unread struct {
		Notifications []any `json:"notifications"`
		UnreadCount   int   `json:"unread_count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &unread); err != nil {
		t.Fatalf("decode unread: %v", err)
	}
	if len(unread.Notifications) != 0 || unread.UnreadCount != 0 {
		t.Fatalf("after read: %+v, want empty", unread)
	}

	// A (the actor) has no notifications.
	rec = getAuthed(t, e, http.MethodGet, "/api/v1/notifications", cookieA)
	var listA struct {
		Notifications []any `json:"notifications"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listA); err != nil {
		t.Fatalf("decode A list: %v", err)
	}
	if len(listA.Notifications) != 0 {
		t.Fatalf("actor notifications = %d, want 0", len(listA.Notifications))
	}

	// Prefs: GET lists all known events with defaults; PUT sets one;
	// unknown event → 400 envelope.
	rec = getAuthed(t, e, http.MethodGet, "/api/v1/notification-prefs", cookieB)
	var prefs struct {
		Prefs []struct {
			Event string `json:"event"`
			InApp bool   `json:"in_app"`
			Email bool   `json:"email"`
		} `json:"prefs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &prefs); err != nil {
		t.Fatalf("decode prefs: %v", err)
	}
	if len(prefs.Prefs) == 0 {
		t.Fatal("prefs list empty")
	}
	rec = postAuthedJSON(t, e, http.MethodPut, "/api/v1/notification-prefs/issue.assigned",
		cookieB, `{"in_app":true,"email":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("set pref: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPut, "/api/v1/notification-prefs/bogus.event",
		cookieB, `{"in_app":true,"email":false}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bogus pref: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestWebhookHTTPCRUD(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testNotifyServer(t, pool)

	cookieA := loginTestUser(t, e, pool, uniqueEmail("wh-a"), "ua", "10.9.1.1")
	cookieB := loginTestUser(t, e, pool, uniqueEmail("wh-b"), "ua", "10.9.1.2")
	userBID := authedUserID(t, e, cookieB)

	slug := uniqueSlug("wh-http")
	createWorkspaceHTTP(t, e, cookieA, "Webhook Co", slug)
	base := "/api/v1/workspaces/" + slug + "/webhooks"

	// B joins as member: webhook CRUD is admin-only → 403.
	rec := postAuthedJSON(t, e, http.MethodPost, "/api/v1/workspaces/"+slug+"/members", cookieA,
		fmt.Sprintf(`{"user_id":%q,"role":15}`, userBID))
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("add member: status = %d", rec.Code)
	}
	rec = postAuthedJSON(t, e, http.MethodPost, base, cookieB, `{"url":"https://example.com/h"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("member create webhook: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}

	// Admin creates → 201 with generated secret.
	rec = postAuthedJSON(t, e, http.MethodPost, base, cookieA,
		`{"url":"https://example.com/hook","events":["issue.created"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create webhook: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var wh struct {
		ID     string   `json:"id"`
		URL    string   `json:"url"`
		Secret string   `json:"secret"`
		Events []string `json:"events"`
		Active bool     `json:"active"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &wh); err != nil {
		t.Fatalf("decode webhook: %v", err)
	}
	if wh.Secret == "" || !wh.Active {
		t.Fatalf("webhook = %+v, want secret + active", wh)
	}

	// Bad URL → 400 envelope.
	rec = postAuthedJSON(t, e, http.MethodPost, base, cookieA, `{"url":"not-a-url"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad url: status = %d, want 400", rec.Code)
	}

	// List → 1 (no secret in the wire shape); get → 200 (no secret);
	// patch active=false → 200 (no secret); delete → 204;
	// get after delete → 404.
	assertNoSecretKey := func(name string, body []byte) {
		t.Helper()
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		if _, ok := m["secret"]; ok {
			t.Fatalf("%s response leaks secret: %s", name, body)
		}
	}
	rec = getAuthed(t, e, http.MethodGet, base, cookieA)
	var list struct {
		Webhooks []map[string]any `json:"webhooks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Webhooks) != 1 {
		t.Fatalf("webhooks = %d, want 1", len(list.Webhooks))
	}
	if _, ok := list.Webhooks[0]["secret"]; ok {
		t.Fatalf("list response leaks secret: %s", rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, base+"/"+wh.ID, cookieA)
	if rec.Code != http.StatusOK {
		t.Fatalf("get webhook: status = %d", rec.Code)
	}
	assertNoSecretKey("get", rec.Body.Bytes())
	rec = postAuthedJSON(t, e, http.MethodPatch, base+"/"+wh.ID, cookieA, `{"active":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch webhook: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	assertNoSecretKey("patch", rec.Body.Bytes())
	rec = postAuthedJSON(t, e, http.MethodDelete, base+"/"+wh.ID, cookieA, ``)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete webhook: status = %d, want 204", rec.Code)
	}
	rec = getAuthed(t, e, http.MethodGet, base+"/"+wh.ID, cookieA)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete: status = %d, want 404", rec.Code)
	}

	// Unauthenticated → 401.
	req := httptest.NewRequest(http.MethodGet, base, nil)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: status = %d, want 401", rec.Code)
	}
}

// TestDigestScheduleHTTPEndpoints (C11T2): GET/PUT /api/v1/digest-schedule
// round-trips the caller's digest cadence; invalid frequency/hour → 400;
// unauthenticated → 401. Real test database, no skips.
func TestDigestScheduleHTTPEndpoints(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testNotifyServer(t, pool)

	cookie := loginTestUser(t, e, pool, uniqueEmail("digest-sched"), "ua", "10.9.0.3")

	var sched struct {
		Frequency string `json:"frequency"`
		Hour      int    `json:"hour"`
		ServerTZ  string `json:"server_tz"`
	}
	decode := func(rec *httptest.ResponseRecorder) {
		t.Helper()
		if err := json.Unmarshal(rec.Body.Bytes(), &sched); err != nil {
			t.Fatalf("decode schedule: %v (body: %s)", err, rec.Body.String())
		}
	}

	// Defaults: daily at 08:00, server_tz named.
	rec := getAuthed(t, e, http.MethodGet, "/api/v1/digest-schedule", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("get schedule: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	decode(rec)
	if sched.Frequency != "daily" || sched.Hour != 8 {
		t.Fatalf("default schedule = %+v, want {daily 8}", sched)
	}
	if sched.ServerTZ == "" {
		t.Fatal("server_tz empty — the UI needs it for the honest TZ caveat")
	}

	// PUT weekly/14 round-trips through GET.
	rec = postAuthedJSON(t, e, http.MethodPut, "/api/v1/digest-schedule",
		cookie, `{"frequency":"weekly","hour":14}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("set schedule: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, "/api/v1/digest-schedule", cookie)
	decode(rec)
	if sched.Frequency != "weekly" || sched.Hour != 14 {
		t.Fatalf("schedule = %+v, want {weekly 14}", sched)
	}

	// Invalid inputs → 400, stored schedule untouched.
	for _, body := range []string{
		`{"frequency":"monthly","hour":8}`,
		`{"frequency":"daily","hour":24}`,
		`{"frequency":"daily","hour":-1}`,
	} {
		rec = postAuthedJSON(t, e, http.MethodPut, "/api/v1/digest-schedule", cookie, body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("put %s: status = %d, want 400 (body: %s)", body, rec.Code, rec.Body.String())
		}
	}
	rec = getAuthed(t, e, http.MethodGet, "/api/v1/digest-schedule", cookie)
	decode(rec)
	if sched.Frequency != "weekly" || sched.Hour != 14 {
		t.Fatalf("schedule after rejected puts = %+v, want {weekly 14} (unchanged)", sched)
	}

	// Unauthenticated → 401.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/digest-schedule", nil)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: status = %d, want 401", rec.Code)
	}
}
