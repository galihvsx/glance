package api

// Notification + webhook HTTP endpoint tests (Task 26): the notification
// list/mark-read flow end to end (assign via HTTP → assignee sees the
// notification), prefs get/set, and webhook CRUD with the admin gate.
// Real test database, no skips.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

// TestDigestScheduleTZHTTPEndpoints (C13T1): the tz field round-trips;
// invalid IANA names → 400 with the stored schedule untouched; empty tz
// resets to the server-local default.
func TestDigestScheduleTZHTTPEndpoints(t *testing.T) {
	pool := newTestPool(t)
	e := testNotifyServer(t, pool)

	cookie := loginTestUser(t, e, pool, uniqueEmail("digest-sched-tz"), "ua", "10.9.0.4")

	var sched struct {
		Frequency string `json:"frequency"`
		Hour      int    `json:"hour"`
		TZ        string `json:"tz"`
	}
	decode := func(rec *httptest.ResponseRecorder) {
		t.Helper()
		if err := json.Unmarshal(rec.Body.Bytes(), &sched); err != nil {
			t.Fatalf("decode schedule: %v (body: %s)", err, rec.Body.String())
		}
	}

	// Default: no tz set → tz names the server zone.
	rec := getAuthed(t, e, http.MethodGet, "/api/v1/digest-schedule", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("get schedule: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	decode(rec)
	if sched.TZ == "" {
		t.Fatal("tz empty — GET must report the effective zone, never blank")
	}

	// PUT a timezone round-trips.
	rec = postAuthedJSON(t, e, http.MethodPut, "/api/v1/digest-schedule",
		cookie, `{"frequency":"daily","hour":8,"tz":"Asia/Makassar"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("set schedule tz: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	decode(rec)
	if sched.TZ != "Asia/Makassar" {
		t.Fatalf("put response tz = %q, want Asia/Makassar", sched.TZ)
	}
	rec = getAuthed(t, e, http.MethodGet, "/api/v1/digest-schedule", cookie)
	decode(rec)
	if sched.TZ != "Asia/Makassar" {
		t.Fatalf("get tz = %q, want Asia/Makassar", sched.TZ)
	}

	// Invalid IANA names → 400, stored schedule untouched.
	for _, body := range []string{
		`{"frequency":"daily","hour":8,"tz":"Not/AZone"}`,
		`{"frequency":"daily","hour":8,"tz":"UTC+7"}`,
	} {
		rec = postAuthedJSON(t, e, http.MethodPut, "/api/v1/digest-schedule", cookie, body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("put %s: status = %d, want 400 (body: %s)", body, rec.Code, rec.Body.String())
		}
	}
	rec = getAuthed(t, e, http.MethodGet, "/api/v1/digest-schedule", cookie)
	decode(rec)
	if sched.TZ != "Asia/Makassar" {
		t.Fatalf("tz after rejected puts = %q, want Asia/Makassar (unchanged)", sched.TZ)
	}

	// Empty tz resets to the server-local default.
	rec = postAuthedJSON(t, e, http.MethodPut, "/api/v1/digest-schedule",
		cookie, `{"frequency":"daily","hour":8,"tz":""}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("reset tz: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	decode(rec)
	if sched.TZ == "Asia/Makassar" {
		t.Fatalf("tz after reset = %q, want server-local default", sched.TZ)
	}
}

// TestWebhookDeliveryStatusHTTP (C57T2): list/get/create webhook
// responses include the three delivery fields as present-but-null keys
// for a never-attempted webhook (same null convention as
// AdminUser.name: pointer fields, no omitempty).
func TestWebhookDeliveryStatusHTTP(t *testing.T) {
	pool := newTestPool(t)
	e := testNotifyServer(t, pool)

	cookie := loginTestUser(t, e, pool, uniqueEmail("wh-status"), "ua", "10.9.2.1")
	slug := uniqueSlug("wh-status")
	createWorkspaceHTTP(t, e, cookie, "Webhook Status Co", slug)
	base := "/api/v1/workspaces/" + slug + "/webhooks"

	assertNullDeliveryKeys := func(name string, body []byte) {
		t.Helper()
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		for _, key := range []string{"last_delivery_at", "last_delivery_status", "last_error"} {
			val, ok := m[key]
			if !ok {
				t.Fatalf("%s response lacks key %q: %s", name, key, body)
			}
			if val != nil {
				t.Fatalf("%s response %q = %v, want null (never attempted)", name, key, val)
			}
		}
	}

	// Create → 201 carries the secret AND the null delivery keys.
	rec := postAuthedJSON(t, e, http.MethodPost, base, cookie,
		`{"url":"https://example.com/hook","events":["issue.created"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create webhook: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	assertNullDeliveryKeys("create", rec.Body.Bytes())
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}

	// List → the webhook entry carries the null delivery keys.
	rec = getAuthed(t, e, http.MethodGet, base, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list webhooks: status = %d", rec.Code)
	}
	var list struct {
		Webhooks []json.RawMessage `json:"webhooks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Webhooks) != 1 {
		t.Fatalf("webhooks = %d, want 1", len(list.Webhooks))
	}
	assertNullDeliveryKeys("list", list.Webhooks[0])

	// Get → same three null keys.
	rec = getAuthed(t, e, http.MethodGet, base+"/"+created.ID, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("get webhook: status = %d", rec.Code)
	}
	assertNullDeliveryKeys("get", rec.Body.Bytes())
}

// TestWebhookDeliveriesHTTP (C59T1): GET .../webhooks/:id/deliveries
// (paging, response shape, authz, validation) + POST
// .../deliveries/:deliveryId/redeliver (202 + fresh outbox row,
// 409/404 rules). Real test database, no skips.
func TestWebhookDeliveriesHTTP(t *testing.T) {
	pool := newTestPool(t)
	e := testNotifyServer(t, pool)
	ctx := context.Background()

	cookieA := loginTestUser(t, e, pool, uniqueEmail("wh-del"), "ua", "10.9.3.1")
	cookieB := loginTestUser(t, e, pool, uniqueEmail("wh-del-b"), "ua", "10.9.3.2")
	userBID := authedUserID(t, e, cookieB)

	slug := uniqueSlug("wh-del")
	createWorkspaceHTTP(t, e, cookieA, "Webhook Deliveries Co", slug)
	base := "/api/v1/workspaces/" + slug + "/webhooks"

	// B joins as member: the deliveries endpoints are admin-only → 403.
	rec := postAuthedJSON(t, e, http.MethodPost, "/api/v1/workspaces/"+slug+"/members", cookieA,
		fmt.Sprintf(`{"user_id":%q,"role":15}`, userBID))
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("add member: status = %d", rec.Code)
	}

	// Admin creates the webhook under test.
	rec = postAuthedJSON(t, e, http.MethodPost, base, cookieA,
		`{"url":"https://example.com/hook","events":["issue.created"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create webhook: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var wh struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &wh); err != nil {
		t.Fatalf("decode webhook: %v", err)
	}

	// A second workspace + webhook for the cross-workspace 404 checks:
	// its deliveries must never be visible through the first workspace.
	slug2 := uniqueSlug("wh-del-2")
	createWorkspaceHTTP(t, e, cookieA, "Webhook Deliveries Co 2", slug2)
	rec = postAuthedJSON(t, e, http.MethodPost, "/api/v1/workspaces/"+slug2+"/webhooks", cookieA,
		`{"url":"https://example.com/other","events":["issue.created"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create second webhook: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var wh2 struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &wh2); err != nil {
		t.Fatalf("decode second webhook: %v", err)
	}

	seedDelivery := func(webhookID, event, status string, httpStatus *int, errStr *string, attemptedAt time.Time, data string) int64 {
		t.Helper()
		var id int64
		if err := pool.QueryRow(ctx,
			`INSERT INTO webhook_deliveries
			   (webhook_id, event, data, status, http_status, error, duration_ms, attempted_at)
			 VALUES ($1::uuid, $2, $3::jsonb, $4, $5, $6, 12, $7) RETURNING id`,
			webhookID, event, data, status, httpStatus, errStr, attemptedAt).Scan(&id); err != nil {
			t.Fatalf("seed delivery: %v", err)
		}
		return id
	}
	now := time.Now().UTC()
	http500, http200 := 500, 200
	errBoom := "boom"
	// Seeded oldest-first so ids increase with attempted_at (as the
	// dispatcher produces them: attempted_at ~ now() at insert). The
	// newest-first list is then: failed, delivered, retrying.
	retryingID := seedDelivery(wh.ID, "issue.created", "retrying", nil, &errBoom, now.Add(-2*time.Minute), `{"id":"issue-3"}`)
	deliveredID := seedDelivery(wh.ID, "issue.updated", "delivered", &http200, nil, now.Add(-time.Minute), `{"id":"issue-2"}`)
	failedID := seedDelivery(wh.ID, "issue.created", "failed", &http500, &errBoom, now, `{"id":"issue-1"}`)
	otherFailedID := seedDelivery(wh2.ID, "issue.created", "failed", nil, &errBoom, now, `{"id":"issue-9"}`)

	type deliveryResp struct {
		ID         int64   `json:"id"`
		WebhookID  string  `json:"webhook_id"`
		Event      string  `json:"event"`
		Status     string  `json:"status"`
		HTTPStatus *int    `json:"http_status"`
		Error      *string `json:"error"`
		DurationMs int     `json:"duration_ms"`
		Attempted  string  `json:"attempted_at"`
	}
	type listResp struct {
		Deliveries []deliveryResp `json:"deliveries"`
		NextBefore *int64         `json:"next_before"`
	}
	decodeList := func(rec *httptest.ResponseRecorder) listResp {
		t.Helper()
		var out listResp
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode deliveries list: %v (body: %s)", err, rec.Body.String())
		}
		return out
	}

	deliveriesURL := base + "/" + wh.ID + "/deliveries"

	// Full list: newest first, next_before null on a short page.
	rec = getAuthed(t, e, http.MethodGet, deliveriesURL, cookieA)
	if rec.Code != http.StatusOK {
		t.Fatalf("list deliveries: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	list := decodeList(rec)
	if len(list.Deliveries) != 3 {
		t.Fatalf("deliveries = %d, want 3", len(list.Deliveries))
	}
	if list.NextBefore != nil {
		t.Fatalf("next_before = %d, want null on a short page", *list.NextBefore)
	}
	wantOrder := []int64{failedID, deliveredID, retryingID}
	for i, want := range wantOrder {
		if list.Deliveries[i].ID != want {
			t.Fatalf("deliveries[%d].id = %d, want %d (newest first)", i, list.Deliveries[i].ID, want)
		}
	}
	if list.Deliveries[0].Status != "failed" || list.Deliveries[0].Event != "issue.created" {
		t.Fatalf("first delivery = %+v, want failed issue.created", list.Deliveries[0])
	}
	if list.Deliveries[0].HTTPStatus == nil || *list.Deliveries[0].HTTPStatus != 500 {
		t.Fatalf("first delivery http_status = %v, want 500", list.Deliveries[0].HTTPStatus)
	}
	if list.Deliveries[0].Error == nil || *list.Deliveries[0].Error != "boom" {
		t.Fatalf("first delivery error = %v, want boom", list.Deliveries[0].Error)
	}
	if list.Deliveries[2].HTTPStatus != nil {
		t.Fatalf("retrying delivery http_status = %v, want null (no response received)", list.Deliveries[2].HTTPStatus)
	}

	// The list shape carries NO data and NO outbox_row_id (both are
	// internal / payload-sized, never part of the log view).
	var raw struct {
		Deliveries []map[string]any `json:"deliveries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw list: %v", err)
	}
	for _, d := range raw.Deliveries {
		if _, ok := d["data"]; ok {
			t.Fatalf("delivery list leaks data: %s", rec.Body.String())
		}
		if _, ok := d["outbox_row_id"]; ok {
			t.Fatalf("delivery list leaks outbox_row_id: %s", rec.Body.String())
		}
	}

	// Paging: limit=2 → first two + next_before; before=cursor → rest.
	rec = getAuthed(t, e, http.MethodGet, deliveriesURL+"?limit=2", cookieA)
	if rec.Code != http.StatusOK {
		t.Fatalf("list limit=2: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	page1 := decodeList(rec)
	if len(page1.Deliveries) != 2 || page1.NextBefore == nil || *page1.NextBefore != deliveredID {
		t.Fatalf("page1 = %+v, want 2 rows + next_before %d", page1, deliveredID)
	}
	rec = getAuthed(t, e, http.MethodGet,
		fmt.Sprintf("%s?limit=2&before=%d", deliveriesURL, *page1.NextBefore), cookieA)
	if rec.Code != http.StatusOK {
		t.Fatalf("list page 2: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	page2 := decodeList(rec)
	if len(page2.Deliveries) != 1 || page2.Deliveries[0].ID != retryingID {
		t.Fatalf("page2 = %+v, want the single retrying row", page2)
	}
	if page2.NextBefore != nil {
		t.Fatalf("page2 next_before = %d, want null (exhausted)", *page2.NextBefore)
	}

	// Invalid limit / before → 400.
	for _, q := range []string{"?limit=0", "?limit=101", "?limit=abc", "?limit=-1", "?before=abc", "?before=0", "?before=-5"} {
		rec = getAuthed(t, e, http.MethodGet, deliveriesURL+q, cookieA)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("list %s: status = %d, want 400 (body: %s)", q, rec.Code, rec.Body.String())
		}
	}

	// Non-admin (member) → 403 on both endpoints.
	rec = getAuthed(t, e, http.MethodGet, deliveriesURL, cookieB)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("member list deliveries: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPost, deliveriesURL+"/"+fmt.Sprint(failedID)+"/redeliver", cookieB, `{}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("member redeliver: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}

	// Unknown webhook → 404; a webhook from another workspace → 404
	// (never leak cross-workspace existence).
	rec = getAuthed(t, e, http.MethodGet, base+"/00000000-0000-0000-0000-000000000000/deliveries", cookieA)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown webhook list: status = %d, want 404", rec.Code)
	}
	rec = getAuthed(t, e, http.MethodGet, base+"/"+wh2.ID+"/deliveries", cookieA)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace list: status = %d, want 404", rec.Code)
	}

	// Redeliver of a delivered row → 409 (only failed is redeliverable).
	rec = postAuthedJSON(t, e, http.MethodPost, deliveriesURL+"/"+fmt.Sprint(deliveredID)+"/redeliver", cookieA, `{}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("redeliver delivered: status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}

	// Redeliver of a retrying row → 409 (it retries automatically).
	rec = postAuthedJSON(t, e, http.MethodPost, deliveriesURL+"/"+fmt.Sprint(retryingID)+"/redeliver", cookieA, `{}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("redeliver retrying: status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}

	// Redeliver of a missing delivery → 404; of a delivery under
	// another webhook → 404 (never leak cross-webhook existence).
	rec = postAuthedJSON(t, e, http.MethodPost, deliveriesURL+"/999999999/redeliver", cookieA, `{}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("redeliver missing: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPost, deliveriesURL+"/"+fmt.Sprint(otherFailedID)+"/redeliver", cookieA, `{}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("redeliver cross-webhook: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}

	// Redeliver with a non-numeric delivery id → 400.
	rec = postAuthedJSON(t, e, http.MethodPost, deliveriesURL+"/abc/redeliver", cookieA, `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("redeliver bad id: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// Redeliver the failed row → 202 {ok: true} + a fresh outbox row
	// (pending, attempts 0, same event + data); the original history
	// row is untouched (still failed).
	rec = postAuthedJSON(t, e, http.MethodPost, deliveriesURL+"/"+fmt.Sprint(failedID)+"/redeliver", cookieA, `{}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("redeliver failed: status = %d, want 202 (body: %s)", rec.Code, rec.Body.String())
	}
	var okResp struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &okResp); err != nil || !okResp.OK {
		t.Fatalf("redeliver response = %s, want {ok: true}", rec.Body.String())
	}
	var outbox struct {
		Status   string
		Attempts int
		Payload  []byte
	}
	if err := pool.QueryRow(ctx,
		`SELECT status, attempts, payload FROM outbox
		  WHERE event = 'webhook.deliver' ORDER BY id DESC LIMIT 1`,
	).Scan(&outbox.Status, &outbox.Attempts, &outbox.Payload); err != nil {
		t.Fatalf("read fresh outbox row: %v", err)
	}
	if outbox.Status != "pending" || outbox.Attempts != 0 {
		t.Fatalf("fresh outbox = {%q, attempts %d}, want {pending, 0}", outbox.Status, outbox.Attempts)
	}
	var payload struct {
		WebhookID string         `json:"webhook_id"`
		Event     string         `json:"event"`
		Data      map[string]any `json:"data"`
	}
	if err := json.Unmarshal(outbox.Payload, &payload); err != nil {
		t.Fatalf("decode fresh payload: %v", err)
	}
	if payload.WebhookID != wh.ID || payload.Event != "issue.created" || payload.Data["id"] != "issue-1" {
		t.Fatalf("fresh payload = %+v, want webhook %s / issue.created / {id: issue-1}", payload, wh.ID)
	}
	rec = getAuthed(t, e, http.MethodGet, deliveriesURL, cookieA)
	list = decodeList(rec)
	if len(list.Deliveries) != 3 {
		t.Fatalf("deliveries after redeliver = %d, want 3 (original row untouched, no new history yet)", len(list.Deliveries))
	}
	if list.Deliveries[0].ID != failedID || list.Deliveries[0].Status != "failed" {
		t.Fatalf("first delivery after redeliver = %+v, want the untouched failed row", list.Deliveries[0])
	}

	// Inactive webhook → redelivery is 409 (the dispatcher would skip it).
	rec = postAuthedJSON(t, e, http.MethodPatch, base+"/"+wh.ID, cookieA, `{"active":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("deactivate webhook: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPost, deliveriesURL+"/"+fmt.Sprint(failedID)+"/redeliver", cookieA, `{}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("redeliver inactive webhook: status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
}

// TestWebhookDeliveryDetailHTTP (C60T0): GET
// .../webhooks/:id/deliveries/:deliveryId — 200 {delivery: {...}}
// with the full payload (data), NO outbox_row_id; 400 for a
// non-numeric id; 403 for a member; 404 for missing / cross-webhook
// / cross-workspace / missing-webhook. Real test database, no skips.
func TestWebhookDeliveryDetailHTTP(t *testing.T) {
	pool := newTestPool(t)
	e := testNotifyServer(t, pool)
	ctx := context.Background()

	cookieA := loginTestUser(t, e, pool, uniqueEmail("wh-dd"), "ua", "10.9.7.1")
	cookieB := loginTestUser(t, e, pool, uniqueEmail("wh-dd-b"), "ua", "10.9.7.2")
	userBID := authedUserID(t, e, cookieB)

	slug := uniqueSlug("wh-dd")
	createWorkspaceHTTP(t, e, cookieA, "Webhook Detail Co", slug)
	base := "/api/v1/workspaces/" + slug + "/webhooks"

	// B joins as member: the detail endpoint is admin-only → 403.
	rec := postAuthedJSON(t, e, http.MethodPost, "/api/v1/workspaces/"+slug+"/members", cookieA,
		fmt.Sprintf(`{"user_id":%q,"role":15}`, userBID))
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("add member: status = %d", rec.Code)
	}

	rec = postAuthedJSON(t, e, http.MethodPost, base, cookieA,
		`{"url":"https://example.com/hook","events":["issue.created"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create webhook: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var wh struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &wh); err != nil {
		t.Fatalf("decode webhook: %v", err)
	}

	// A second webhook in the same workspace for the cross-webhook
	// 404: the delivery exists but is not under :id.
	rec = postAuthedJSON(t, e, http.MethodPost, base, cookieA,
		`{"url":"https://example.com/other","events":["issue.created"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create second webhook: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var wh2 struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &wh2); err != nil {
		t.Fatalf("decode second webhook: %v", err)
	}

	// A webhook in a different workspace for the cross-workspace 404.
	slug2 := uniqueSlug("wh-dd-2")
	createWorkspaceHTTP(t, e, cookieA, "Webhook Detail Co 2", slug2)
	rec = postAuthedJSON(t, e, http.MethodPost, "/api/v1/workspaces/"+slug2+"/webhooks", cookieA,
		`{"url":"https://example.com/xws","events":["issue.created"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create xws webhook: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var whX struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &whX); err != nil {
		t.Fatalf("decode xws webhook: %v", err)
	}

	seed := func(webhookID, event, status string, httpStatus *int, errStr *string, attemptedAt time.Time, data string) int64 {
		t.Helper()
		var id int64
		if err := pool.QueryRow(ctx,
			`INSERT INTO webhook_deliveries
			   (webhook_id, event, data, status, http_status, error, duration_ms, attempted_at)
			 VALUES ($1::uuid, $2, $3::jsonb, $4, $5, $6, 12, $7) RETURNING id`,
			webhookID, event, data, status, httpStatus, errStr, attemptedAt).Scan(&id); err != nil {
			t.Fatalf("seed delivery: %v", err)
		}
		return id
	}
	now := time.Now().UTC()
	http500 := 500
	errBoom := "boom"
	failedID := seed(wh.ID, "issue.created", "failed", &http500, &errBoom, now, `{"id":"issue-1","title":"Bug"}`)
	otherFailedID := seed(wh2.ID, "issue.created", "failed", nil, &errBoom, now, `{"id":"issue-9"}`)

	detailURL := base + "/" + wh.ID + "/deliveries/" + fmt.Sprint(failedID)

	// 200: the response carries the full payload, and never
	// outbox_row_id.
	rec = getAuthed(t, e, http.MethodGet, detailURL, cookieA)
	if rec.Code != http.StatusOK {
		t.Fatalf("detail: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode detail body: %v (body: %s)", err, rec.Body.String())
	}
	delivery, ok := body["delivery"].(map[string]any)
	if !ok {
		t.Fatalf("detail body has no delivery object: %v", body)
	}
	wantKeys := []string{"id", "webhook_id", "event", "status", "http_status", "error", "duration_ms", "attempted_at", "data"}
	for _, k := range wantKeys {
		if _, ok := delivery[k]; !ok {
			t.Fatalf("detail.delivery missing key %q: %v", k, delivery)
		}
	}
	if _, ok := delivery["outbox_row_id"]; ok {
		t.Fatalf("detail.delivery carries outbox_row_id, must not: %v", delivery)
	}
	if delivery["event"] != "issue.created" || delivery["status"] != "failed" {
		t.Fatalf("detail = %v, want failed issue.created", delivery)
	}
	data, ok := delivery["data"].(map[string]any)
	if !ok || data["id"] != "issue-1" || data["title"] != "Bug" {
		t.Fatalf("detail.data = %v, want the stored payload", delivery["data"])
	}

	// Non-numeric delivery id → 400.
	rec = getAuthed(t, e, http.MethodGet, base+"/"+wh.ID+"/deliveries/abc", cookieA)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("detail bad id: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// Non-admin (member) → 403.
	rec = getAuthed(t, e, http.MethodGet, detailURL, cookieB)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("member detail: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}

	// Missing delivery → 404.
	rec = getAuthed(t, e, http.MethodGet, base+"/"+wh.ID+"/deliveries/999999999", cookieA)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing detail: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}

	// Delivery under another webhook → 404 (never leak
	// cross-webhook existence).
	rec = getAuthed(t, e, http.MethodGet, base+"/"+wh.ID+"/deliveries/"+fmt.Sprint(otherFailedID), cookieA)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-webhook detail: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}

	// Webhook from another workspace → 404 (never leak
	// cross-workspace existence).
	rec = getAuthed(t, e, http.MethodGet, base+"/"+whX.ID+"/deliveries/"+fmt.Sprint(failedID), cookieA)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace detail: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}

	// Unknown webhook → 404.
	rec = getAuthed(t, e, http.MethodGet, base+"/00000000-0000-0000-0000-000000000000/deliveries/"+fmt.Sprint(failedID), cookieA)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown webhook detail: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
}

// TestWebhookFilterHTTP (C69T0): the filter key is present-but-null in
// create/list/get responses when unset (same null convention as the
// last-delivery fields); create/update accept a filter array; PATCH with
// an explicit null clears it; PATCH without the key leaves it untouched;
// an invalid filter is a 400.
func TestWebhookFilterHTTP(t *testing.T) {
	pool := newTestPool(t)
	e := testNotifyServer(t, pool)

	cookie := loginTestUser(t, e, pool, uniqueEmail("wh-filter"), "ua", "10.9.3.1")
	slug := uniqueSlug("wh-filter")
	createWorkspaceHTTP(t, e, cookie, "Webhook Filter Co", slug)
	base := "/api/v1/workspaces/" + slug + "/webhooks"

	filterOf := func(name string, body []byte) (present bool, raw json.RawMessage) {
		t.Helper()
		var m map[string]json.RawMessage
		if err := json.Unmarshal(body, &m); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		raw, present = m["filter"]
		return present, raw
	}
	assertFilterNull := func(name string, body []byte) {
		t.Helper()
		present, raw := filterOf(name, body)
		if !present {
			t.Fatalf("%s response lacks key %q: %s", name, "filter", body)
		}
		if string(raw) != "null" {
			t.Fatalf("%s response filter = %s, want null", name, raw)
		}
	}

	// Create without a filter → present-but-null.
	rec := postAuthedJSON(t, e, http.MethodPost, base, cookie,
		`{"url":"https://example.com/hook","events":["issue.created"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create webhook: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	assertFilterNull("create", rec.Body.Bytes())
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}

	// Create WITH a filter → the array round-trips.
	rec = postAuthedJSON(t, e, http.MethodPost, base, cookie,
		`{"url":"https://example.com/hook2","events":["issue.created"],`+
			`"filter":[{"field":"priority","op":"in","value":["high","urgent"]}]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create webhook with filter: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	present, raw := filterOf("create-with-filter", rec.Body.Bytes())
	if !present {
		t.Fatalf("create-with-filter response lacks filter key: %s", rec.Body.String())
	}
	var conds []map[string]any
	if err := json.Unmarshal(raw, &conds); err != nil {
		t.Fatalf("decode filter: %v", err)
	}
	if len(conds) != 1 || conds[0]["field"] != "priority" || conds[0]["op"] != "in" {
		t.Fatalf("create-with-filter filter = %s, want the priority condition", raw)
	}
	var created2 struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created2); err != nil {
		t.Fatalf("decode created2: %v", err)
	}

	// Invalid filter → 400.
	rec = postAuthedJSON(t, e, http.MethodPost, base, cookie,
		`{"url":"https://example.com/hook3","events":["issue.created"],`+
			`"filter":[{"field":"priority","op":"eq","value":"critical"}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("create with invalid filter: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// PATCH without the filter key leaves the filter untouched.
	rec = postAuthedJSON(t, e, http.MethodPatch, base+"/"+created2.ID, cookie,
		`{"active":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch without filter: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if present, raw := filterOf("patch-untouched", rec.Body.Bytes()); !present || string(raw) == "null" {
		t.Fatalf("patch-untouched filter = %s, want the priority condition preserved", raw)
	}

	// PATCH with explicit null clears the filter.
	rec = postAuthedJSON(t, e, http.MethodPatch, base+"/"+created2.ID, cookie,
		`{"filter":null}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch clear filter: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	assertFilterNull("patch-clear", rec.Body.Bytes())

	// PATCH with a new array replaces the filter.
	rec = postAuthedJSON(t, e, http.MethodPatch, base+"/"+created2.ID, cookie,
		`{"filter":[{"field":"state_group","op":"eq","value":"started"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch replace filter: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	present, raw = filterOf("patch-replace", rec.Body.Bytes())
	if !present {
		t.Fatalf("patch-replace response lacks filter key: %s", rec.Body.String())
	}
	if err := json.Unmarshal(raw, &conds); err != nil {
		t.Fatalf("decode filter: %v", err)
	}
	if len(conds) != 1 || conds[0]["field"] != "state_group" {
		t.Fatalf("patch-replace filter = %s, want the state_group condition", raw)
	}

	// PATCH with an invalid filter → 400 and the stored filter survives.
	rec = postAuthedJSON(t, e, http.MethodPatch, base+"/"+created2.ID, cookie,
		`{"filter":[{"field":"nope","op":"eq","value":"x"}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("patch invalid filter: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, base+"/"+created2.ID, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("get webhook: status = %d", rec.Code)
	}
	present, raw = filterOf("get", rec.Body.Bytes())
	if !present {
		t.Fatalf("get response lacks filter key: %s", rec.Body.String())
	}
	if err := json.Unmarshal(raw, &conds); err != nil {
		t.Fatalf("decode filter: %v", err)
	}
	if len(conds) != 1 || conds[0]["field"] != "state_group" {
		t.Fatalf("get filter = %s, want the state_group condition (failed patch must not clobber)", raw)
	}

	// List carries the filter on each entry; the unfiltered webhook
	// from the first create reads null.
	rec = getAuthed(t, e, http.MethodGet, base, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list webhooks: status = %d", rec.Code)
	}
	var list struct {
		Webhooks []json.RawMessage `json:"webhooks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Webhooks) != 2 {
		t.Fatalf("webhooks = %d, want 2", len(list.Webhooks))
	}
	for _, entry := range list.Webhooks {
		if present, _ := filterOf("list", entry); !present {
			t.Fatalf("list entry lacks filter key: %s", entry)
		}
	}
}
