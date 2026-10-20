package api

// C81T0: dashboards + widgets backend API over HTTP — the full contract:
// default public visibility, private-dashboard invisibility (404, never
// 403), Plane's edit rule (creator or admin on public; creator-only on
// private), honest 400s (malformed UUIDs, bad visibility/type/config),
// guest 403s, grid clamping, and live widget-data recompute (groupings,
// project scoping, ANDed filters). Real test database, no skips.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
)

// dashboardFixture is the shared world for dashboard authZ tests: one
// workspace, an admin (creator), a plain member, a second member, and a
// guest.
type dashboardFixture struct {
	pool          *pgxpool.Pool
	e             *echo.Echo
	wsBase        string
	adminCookie   *http.Cookie
	memberCookie  *http.Cookie
	member2Cookie *http.Cookie
	guestCookie   *http.Cookie
	memberID      string
	memberEmail   string
}

func setupDashboardHTTP(t *testing.T) *dashboardFixture {
	t.Helper()
	pool := newTestPool(t)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterTaxonomyRoutes(e, &IssueHandler{Pool: pool})
	RegisterStateRoutes(e, &StateHandler{Pool: pool})
	RegisterDashboardRoutes(e, &DashboardHandler{Pool: pool})

	fx := &dashboardFixture{pool: pool, e: e}
	adminEmail := uniqueEmail("h-dash-admin")
	memberEmail := uniqueEmail("h-dash-member")
	member2Email := uniqueEmail("h-dash-member2")
	guestEmail := uniqueEmail("h-dash-guest")
	fx.adminCookie = loginTestUser(t, e, pool, adminEmail, "test-agent/1.0", uniqueIP())
	fx.memberCookie = loginTestUser(t, e, pool, memberEmail, "test-agent/1.0", uniqueIP())
	fx.member2Cookie = loginTestUser(t, e, pool, member2Email, "test-agent/1.0", uniqueIP())
	fx.guestCookie = loginTestUser(t, e, pool, guestEmail, "test-agent/1.0", uniqueIP())
	fx.memberEmail = memberEmail

	slug := uniqueSlug("dash")
	createWorkspaceHTTP(t, e, fx.adminCookie, "Dash Corp", slug)
	fx.wsBase = "/api/v1/workspaces/" + slug

	fx.memberID = authedUserID(t, e, fx.memberCookie)
	member2ID := authedUserID(t, e, fx.member2Cookie)
	guestID := authedUserID(t, e, fx.guestCookie)
	for _, add := range []struct {
		id   string
		role int
	}{
		{fx.memberID, 15},
		{member2ID, 15},
		{guestID, 5},
	} {
		rec := postAuthedJSON(t, e, http.MethodPost, fx.wsBase+"/members", fx.adminCookie,
			fmt.Sprintf(`{"user_id":%q,"role":%d}`, add.id, add.role))
		if rec.Code != http.StatusOK {
			t.Fatalf("add member role %d: status = %d (body: %s)", add.role, rec.Code, rec.Body.String())
		}
	}
	return fx
}

// createDashboardHTTP creates a dashboard and returns its id (expects 201).
func createDashboardHTTP(t *testing.T, e *echo.Echo, cookie *http.Cookie, wsBase, body string) string {
	t.Helper()
	rec := postAuthedJSON(t, e, http.MethodPost, wsBase+"/dashboards", cookie, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create dashboard: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var d struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatalf("decode dashboard: %v (body: %s)", err, rec.Body.String())
	}
	if d.ID == "" {
		t.Fatalf("created dashboard has no id (body: %s)", rec.Body.String())
	}
	return d.ID
}

// createWidgetHTTP creates a widget on a dashboard and returns its id (expects 201).
func createWidgetHTTP(t *testing.T, e *echo.Echo, cookie *http.Cookie, dashURL, body string) string {
	t.Helper()
	rec := postAuthedJSON(t, e, http.MethodPost, dashURL+"/widgets", cookie, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create widget: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var w struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &w); err != nil {
		t.Fatalf("decode widget: %v (body: %s)", err, rec.Body.String())
	}
	if w.ID == "" {
		t.Fatalf("created widget has no id (body: %s)", rec.Body.String())
	}
	return w.ID
}

func decodeDashboardList(t *testing.T, rec *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	var body struct {
		Dashboards []map[string]any `json:"dashboards"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode dashboard list: %v (body: %s)", err, rec.Body.String())
	}
	return body.Dashboards
}

func TestDashboardCreateDefaultsPublic(t *testing.T) {
	fx := setupDashboardHTTP(t)

	id := createDashboardHTTP(t, fx.e, fx.adminCookie, fx.wsBase, `{"name":"Sprint overview"}`)
	rec := getAuthed(t, fx.e, http.MethodGet, fx.wsBase+"/dashboards/"+id, fx.adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET dashboard: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var d map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if d["visibility"] != "public" {
		t.Fatalf("visibility = %v, want public (default)", d["visibility"])
	}
	if d["name"] != "Sprint overview" {
		t.Fatalf("name = %v", d["name"])
	}
	if d["created_by_name"] == nil || d["created_by_name"] == "" {
		t.Fatalf("created_by_name missing (body: %s)", rec.Body.String())
	}
	if _, ok := d["widgets"]; !ok {
		t.Fatalf("detail must carry widgets key (body: %s)", rec.Body.String())
	}
}

func TestDashboardCreateBadVisibility(t *testing.T) {
	fx := setupDashboardHTTP(t)

	rec := postAuthedJSON(t, fx.e, http.MethodPost, fx.wsBase+"/dashboards", fx.adminCookie,
		`{"name":"x","visibility":"secret"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad visibility: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"bad_request"`) {
		t.Fatalf("400 envelope: body=%s", rec.Body.String())
	}

	rec = postAuthedJSON(t, fx.e, http.MethodPost, fx.wsBase+"/dashboards", fx.adminCookie,
		`{"name":""}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty name: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestDashboardGuestForbidden(t *testing.T) {
	fx := setupDashboardHTTP(t)

	rec := getAuthed(t, fx.e, http.MethodGet, fx.wsBase+"/dashboards", fx.guestCookie)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest list: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, fx.e, http.MethodPost, fx.wsBase+"/dashboards", fx.guestCookie, `{"name":"g"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest create: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestDashboardListHidesOthersPrivate(t *testing.T) {
	fx := setupDashboardHTTP(t)

	pubID := createDashboardHTTP(t, fx.e, fx.adminCookie, fx.wsBase, `{"name":"Public board"}`)
	privID := createDashboardHTTP(t, fx.e, fx.memberCookie, fx.wsBase, `{"name":"Member secrets","visibility":"private"}`)

	// The member sees both.
	rec := getAuthed(t, fx.e, http.MethodGet, fx.wsBase+"/dashboards", fx.memberCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("member list: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	seen := map[string]bool{}
	for _, d := range decodeDashboardList(t, rec) {
		seen[d["id"].(string)] = true
	}
	if !seen[pubID] || !seen[privID] {
		t.Fatalf("member sees %v, want both %s and %s", seen, pubID, privID)
	}

	// The admin sees the public one but NOT the member's private one.
	rec = getAuthed(t, fx.e, http.MethodGet, fx.wsBase+"/dashboards", fx.adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin list: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	seen = map[string]bool{}
	for _, d := range decodeDashboardList(t, rec) {
		seen[d["id"].(string)] = true
		if d["visibility"] == "private" {
			t.Fatalf("admin list leaks private dashboard %s", d["id"])
		}
	}
	if !seen[pubID] {
		t.Fatalf("admin list missing public dashboard")
	}
	if seen[privID] {
		t.Fatalf("admin list leaks member's private dashboard")
	}
}

func TestDashboardGetPrivateByNonCreator404(t *testing.T) {
	fx := setupDashboardHTTP(t)

	privID := createDashboardHTTP(t, fx.e, fx.memberCookie, fx.wsBase, `{"name":"s3cr3t","visibility":"private"}`)

	// Admin (not creator) gets 404 — not 403, so existence doesn't leak.
	rec := getAuthed(t, fx.e, http.MethodGet, fx.wsBase+"/dashboards/"+privID, fx.adminCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("private GET by admin: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
	// The creator reads it fine.
	rec = getAuthed(t, fx.e, http.MethodGet, fx.wsBase+"/dashboards/"+privID, fx.memberCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("private GET by creator: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	// Unknown dashboard id is also 404.
	rec = getAuthed(t, fx.e, http.MethodGet, fx.wsBase+"/dashboards/00000000-0000-0000-0000-000000000000", fx.adminCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown dashboard: status = %d, want 404", rec.Code)
	}
}

func TestDashboardMalformedUUID400(t *testing.T) {
	fx := setupDashboardHTTP(t)

	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodGet, fx.wsBase + "/dashboards/not-a-uuid"},
		{http.MethodGet, fx.wsBase + "/dashboards/not-a-uuid/widgets"},
		// NOTE: GET /api/v1/widgets/not-a-uuid and GET .../layout have no
		// routes at all (Echo 404s before any handler runs); the widget
		// routes that do exist 400 on malformed UUIDs.
		{http.MethodGet, "/api/v1/widgets/not-a-uuid/data"},
		{http.MethodPatch, "/api/v1/widgets/not-a-uuid/layout"},
	} {
		var rec *httptest.ResponseRecorder
		if tc.method == http.MethodGet {
			rec = getAuthed(t, fx.e, tc.method, tc.path, fx.adminCookie)
		} else {
			rec = postAuthedJSON(t, fx.e, tc.method, tc.path, fx.adminCookie, `{"grid_x":1}`)
		}
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s %s: status = %d, want 400", tc.method, tc.path, rec.Code)
		}
	}
	rec := postAuthedJSON(t, fx.e, http.MethodPatch, "/api/v1/widgets/not-a-uuid", fx.adminCookie, `{"title":"x"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PATCH widget malformed: status = %d, want 400", rec.Code)
	}
	rec = postAuthedJSON(t, fx.e, http.MethodPatch, fx.wsBase+"/dashboards/zzz", fx.adminCookie, `{"name":"x"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PATCH malformed: status = %d, want 400", rec.Code)
	}
}

func TestDashboardPatchEditRule(t *testing.T) {
	fx := setupDashboardHTTP(t)

	pubID := createDashboardHTTP(t, fx.e, fx.memberCookie, fx.wsBase, `{"name":"Team board"}`)
	privID := createDashboardHTTP(t, fx.e, fx.memberCookie, fx.wsBase, `{"name":"Mine","visibility":"private"}`)

	patch := func(cookie *http.Cookie, id, body string) int {
		rec := postAuthedJSON(t, fx.e, http.MethodPatch, fx.wsBase+"/dashboards/"+id, cookie, body)
		return rec.Code
	}

	// Creator edits their own public dashboard: 200.
	if code := patch(fx.memberCookie, pubID, `{"name":"Team board v2"}`); code != http.StatusOK {
		t.Fatalf("creator patch public: status = %d, want 200", code)
	}
	// Admin (non-creator) edits another's public dashboard: 200 (Plane's rule).
	if code := patch(fx.adminCookie, pubID, `{"description":"owned by all"}`); code != http.StatusOK {
		t.Fatalf("admin patch public: status = %d, want 200", code)
	}
	// Plain member (non-creator, non-admin) on public dashboard: 403.
	if code := patch(fx.member2Cookie, pubID, `{"name":"hijack"}`); code != http.StatusForbidden {
		t.Fatalf("member patch others public: status = %d, want 403", code)
	}
	// Admin (non-creator) on private dashboard: 404 — private is creator-only.
	if code := patch(fx.adminCookie, privID, `{"name":"hijack"}`); code != http.StatusNotFound {
		t.Fatalf("admin patch private: status = %d, want 404", code)
	}
	// Creator edits their own private dashboard: 200.
	if code := patch(fx.memberCookie, privID, `{"visibility":"public"}`); code != http.StatusOK {
		t.Fatalf("creator patch private: status = %d, want 200", code)
	}
	// Bad visibility on patch: 400. Empty patch: 400 (nothing to update).
	if code := patch(fx.memberCookie, pubID, `{"visibility":"everyone"}`); code != http.StatusBadRequest {
		t.Fatalf("bad visibility patch: status = %d, want 400", code)
	}
	if code := patch(fx.memberCookie, pubID, `{}`); code != http.StatusBadRequest {
		t.Fatalf("empty patch: status = %d, want 400", code)
	}
}

func TestDashboardDeleteCascadesWidgets(t *testing.T) {
	fx := setupDashboardHTTP(t)

	id := createDashboardHTTP(t, fx.e, fx.adminCookie, fx.wsBase, `{"name":"doomed"}`)
	dashURL := fx.wsBase + "/dashboards/" + id
	wid := createWidgetHTTP(t, fx.e, fx.adminCookie, dashURL,
		`{"title":"By state","widget_type":"donut","config":{"group_by":"state"}}`)

	rec := getAuthed(t, fx.e, http.MethodDelete, dashURL, fx.adminCookie)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE dashboard: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	// The dashboard is gone…
	rec = getAuthed(t, fx.e, http.MethodGet, dashURL, fx.adminCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET after delete: status = %d, want 404", rec.Code)
	}
	// …and its widget with it (cascade).
	rec = getAuthed(t, fx.e, http.MethodGet, "/api/v1/widgets/"+wid+"/data", fx.adminCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("widget data after dashboard delete: status = %d, want 404", rec.Code)
	}
}

func TestWidgetCreateValidation(t *testing.T) {
	fx := setupDashboardHTTP(t)

	id := createDashboardHTTP(t, fx.e, fx.adminCookie, fx.wsBase, `{"name":"w"}`)
	dashURL := fx.wsBase + "/dashboards/" + id
	post := func(body string) *httptest.ResponseRecorder {
		return postAuthedJSON(t, fx.e, http.MethodPost, dashURL+"/widgets", fx.adminCookie, body)
	}

	badBodies := map[string]string{
		"bad type":       `{"title":"t","widget_type":"radar","config":{"group_by":"state"}}`,
		"missing type":   `{"title":"t","config":{"group_by":"state"}}`,
		"bad group_by":   `{"title":"t","widget_type":"bar","config":{"group_by":"sprint"}}`,
		"missing group":  `{"title":"t","widget_type":"bar","config":{}}`,
		"bad metric":     `{"title":"t","widget_type":"bar","config":{"group_by":"state","metric":"sum"}}`,
		"unknown key":    `{"title":"t","widget_type":"bar","config":{"group_by":"state","bogus":1}}`,
		"unknown filter": `{"title":"t","widget_type":"bar","config":{"group_by":"state","filters":{"teams":[]}}}`,
		"bad prio":       `{"title":"t","widget_type":"bar","config":{"group_by":"state","filters":{"priorities":[9]}}}`,
		"bad uuid":       `{"title":"t","widget_type":"bar","config":{"group_by":"state","filters":{"states":["nope"]}}}`,
		"bad projects":   `{"title":"t","widget_type":"bar","config":{"group_by":"state","project_ids":["nope"]}}`,
		"empty title":    `{"title":"  ","widget_type":"bar","config":{"group_by":"state"}}`,
	}
	for name, body := range badBodies {
		if rec := post(body); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400 (body: %s)", name, rec.Code, rec.Body.String())
		}
	}

	// number widgets don't need group_by; a stale group_by is ignored.
	wid := createWidgetHTTP(t, fx.e, fx.adminCookie, dashURL,
		`{"title":"Total","widget_type":"number","config":{"group_by":"state"}}`)

	// Grid clamping: w/h clamp to 1..12, x/y to non-negative.
	rec := post(`{"title":"clamped","widget_type":"bar","config":{"group_by":"state"},"grid_x":-5,"grid_y":-1,"grid_w":99,"grid_h":0}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("clamped create: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var w struct {
		GridX int `json:"grid_x"`
		GridY int `json:"grid_y"`
		GridW int `json:"grid_w"`
		GridH int `json:"grid_h"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &w); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if w.GridX != 0 || w.GridY != 0 || w.GridW != 12 || w.GridH != 1 {
		t.Fatalf("clamped grid = (%d,%d,%d,%d), want (0,0,12,1)", w.GridX, w.GridY, w.GridW, w.GridH)
	}

	// The number widget reads back with its config intact.
	rec = getAuthed(t, fx.e, http.MethodGet, dashURL, fx.adminCookie)
	var d struct {
		Widgets []struct {
			ID         string `json:"id"`
			WidgetType string `json:"widget_type"`
		} `json:"widgets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	found := false
	for _, wd := range d.Widgets {
		if wd.ID == wid && wd.WidgetType == "number" {
			found = true
		}
	}
	if !found {
		t.Fatalf("detail widgets missing the number widget (body: %s)", rec.Body.String())
	}
}

func TestWidgetLayoutPatch(t *testing.T) {
	fx := setupDashboardHTTP(t)

	id := createDashboardHTTP(t, fx.e, fx.adminCookie, fx.wsBase, `{"name":"canvas"}`)
	wid := createWidgetHTTP(t, fx.e, fx.adminCookie, fx.wsBase+"/dashboards/"+id,
		`{"title":"movable","widget_type":"bar","config":{"group_by":"state"}}`)
	layoutURL := "/api/v1/widgets/" + wid + "/layout"

	// Happy path: drag/resize commit without resending config.
	rec := postAuthedJSON(t, fx.e, http.MethodPatch, layoutURL, fx.adminCookie,
		`{"grid_x":3,"grid_y":2,"grid_w":6,"grid_h":5}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("layout patch: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var w struct {
		GridX int    `json:"grid_x"`
		GridY int    `json:"grid_y"`
		GridW int    `json:"grid_w"`
		GridH int    `json:"grid_h"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &w); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if w.GridX != 3 || w.GridY != 2 || w.GridW != 6 || w.GridH != 5 {
		t.Fatalf("grid = (%d,%d,%d,%d), want (3,2,6,5)", w.GridX, w.GridY, w.GridW, w.GridH)
	}
	if w.Title != "movable" {
		t.Fatalf("layout patch must not touch other fields (title=%q)", w.Title)
	}

	// Unknown keys are rejected…
	rec = postAuthedJSON(t, fx.e, http.MethodPatch, layoutURL, fx.adminCookie, `{"grid_x":1,"title":"nope"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("layout unknown key: status = %d, want 400", rec.Code)
	}
	// …non-integers are rejected…
	rec = postAuthedJSON(t, fx.e, http.MethodPatch, layoutURL, fx.adminCookie, `{"grid_w":2.5}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("layout float: status = %d, want 400", rec.Code)
	}
	// …an empty body is a 400 (nothing to update)…
	rec = postAuthedJSON(t, fx.e, http.MethodPatch, layoutURL, fx.adminCookie, `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("layout empty: status = %d, want 400", rec.Code)
	}
	// …and values still clamp.
	rec = postAuthedJSON(t, fx.e, http.MethodPatch, layoutURL, fx.adminCookie, `{"grid_w":50,"grid_x":-2}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("layout clamp: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &w); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if w.GridW != 12 || w.GridX != 0 {
		t.Fatalf("clamped = (%d,%d), want w=12 x=0", w.GridW, w.GridX)
	}
}

func TestWidgetAuthZFollowsDashboard(t *testing.T) {
	fx := setupDashboardHTTP(t)

	pubID := createDashboardHTTP(t, fx.e, fx.memberCookie, fx.wsBase, `{"name":"public canvas"}`)
	privID := createDashboardHTTP(t, fx.e, fx.memberCookie, fx.wsBase, `{"name":"private canvas","visibility":"private"}`)
	pubURL := fx.wsBase + "/dashboards/" + pubID
	privURL := fx.wsBase + "/dashboards/" + privID
	privWidget := createWidgetHTTP(t, fx.e, fx.memberCookie, privURL,
		`{"title":"secret chart","widget_type":"pie","config":{"group_by":"priority"}}`)

	// Plain member (non-creator) on another's PUBLIC dashboard: 403.
	rec := postAuthedJSON(t, fx.e, http.MethodPost, pubURL+"/widgets", fx.member2Cookie,
		`{"title":"t","widget_type":"bar","config":{"group_by":"state"}}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("member widget-create on others public: status = %d, want 403", rec.Code)
	}
	// …but the admin may.
	adminWidget := createWidgetHTTP(t, fx.e, fx.adminCookie, pubURL,
		`{"title":"admin chart","widget_type":"bar","config":{"group_by":"state"}}`)

	// Non-creator on a PRIVATE dashboard: 404 everywhere (never leak).
	rec = postAuthedJSON(t, fx.e, http.MethodPost, privURL+"/widgets", fx.adminCookie,
		`{"title":"t","widget_type":"bar","config":{"group_by":"state"}}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("admin widget-create on private: status = %d, want 404", rec.Code)
	}
	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodPatch, "/api/v1/widgets/" + privWidget},
		{http.MethodDelete, "/api/v1/widgets/" + privWidget},
		{http.MethodPatch, "/api/v1/widgets/" + privWidget + "/layout"},
		{http.MethodGet, "/api/v1/widgets/" + privWidget + "/data"},
	} {
		var rec *httptest.ResponseRecorder
		if tc.method == http.MethodGet || tc.method == http.MethodDelete {
			rec = getAuthed(t, fx.e, tc.method, tc.path, fx.adminCookie)
		} else {
			rec = postAuthedJSON(t, fx.e, tc.method, tc.path, fx.adminCookie, `{"title":"x"}`)
		}
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s %s: status = %d, want 404", tc.method, tc.path, rec.Code)
		}
	}

	// The creator's own widget ops work; deleting the admin's public
	// widget as admin works too.
	rec = postAuthedJSON(t, fx.e, http.MethodPatch, "/api/v1/widgets/"+privWidget, fx.memberCookie, `{"title":"renamed"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("creator widget patch: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, fx.e, http.MethodDelete, "/api/v1/widgets/"+adminWidget, fx.adminCookie)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("admin widget delete: status = %d, want 204", rec.Code)
	}
	rec = getAuthed(t, fx.e, http.MethodGet, "/api/v1/widgets/"+adminWidget+"/data", fx.adminCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("deleted widget data: status = %d, want 404", rec.Code)
	}
}

// widgetDataFixture builds two projects with issues spread across states
// and priorities, and returns the pieces the data tests need.
type widgetDataFixture struct {
	fx       *dashboardFixture
	wsBase   string
	dashID   string
	identA   string
	identB   string
	projAID  string
	projBID  string
	backlogA string
	todoA    string
}

func setupWidgetData(t *testing.T) *widgetDataFixture {
	t.Helper()
	fx := setupDashboardHTTP(t)
	wd := &widgetDataFixture{fx: fx, wsBase: fx.wsBase}

	identA := uniqueProjectIdentifier("WDA")
	identB := uniqueProjectIdentifier("WDB")
	wd.identA, wd.identB = identA, identB
	createProjectHTTP(t, fx.e, fx.adminCookie, strings.TrimPrefix(fx.wsBase, "/api/v1/workspaces/"), "Data A", identA)
	createProjectHTTP(t, fx.e, fx.adminCookie, strings.TrimPrefix(fx.wsBase, "/api/v1/workspaces/"), "Data B", identB)
	projABase := fx.wsBase + "/projects/" + identA
	projBBase := fx.wsBase + "/projects/" + identB

	// Project UUIDs come from the project list.
	rec := getAuthed(t, fx.e, http.MethodGet, fx.wsBase+"/projects", fx.adminCookie)
	var plist struct {
		Projects []struct {
			ID         string `json:"id"`
			Identifier string `json:"identifier"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &plist); err != nil {
		t.Fatalf("decode projects: %v", err)
	}
	for _, p := range plist.Projects {
		switch p.Identifier {
		case identA:
			wd.projAID = p.ID
		case identB:
			wd.projBID = p.ID
		}
	}
	if wd.projAID == "" || wd.projBID == "" {
		t.Fatalf("project ids not resolved (body: %s)", rec.Body.String())
	}

	// State ids of project A (Backlog + Todo from the seeded defaults).
	rec = getAuthed(t, fx.e, http.MethodGet, projABase+"/states", fx.adminCookie)
	var slist struct {
		States []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"states"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &slist); err != nil {
		t.Fatalf("decode states: %v", err)
	}
	for _, s := range slist.States {
		switch s.Name {
		case "Backlog":
			wd.backlogA = s.ID
		case "Todo":
			wd.todoA = s.ID
		}
	}
	if wd.backlogA == "" || wd.todoA == "" {
		t.Fatalf("seeded states not found (body: %s)", rec.Body.String())
	}

	mkIssue := func(base, name string, body string) string {
		t.Helper()
		rec := postAuthedJSON(t, fx.e, http.MethodPost, base+"/issues", fx.adminCookie,
			fmt.Sprintf(`{"name":%q%s}`, name, body))
		if rec.Code != http.StatusCreated {
			t.Fatalf("create issue %q: status = %d (body: %s)", name, rec.Code, rec.Body.String())
		}
		var iss struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &iss); err != nil {
			t.Fatalf("decode issue: %v", err)
		}
		return iss.ID
	}
	// Project A: two in Backlog (p3, p1), one in Todo (p3).
	mkIssue(projABase, "a1", fmt.Sprintf(`,"priority":3,"state_id":%q`, wd.backlogA))
	mkIssue(projABase, "a2", fmt.Sprintf(`,"priority":1,"state_id":%q`, wd.backlogA))
	mkIssue(projABase, "a3", fmt.Sprintf(`,"priority":3,"state_id":%q`, wd.todoA))
	// Project B: two in Backlog (p0, p2).
	mkIssue(projBBase, "b1", `,"priority":0`)
	mkIssue(projBBase, "b2", `,"priority":2`)

	wd.dashID = createDashboardHTTP(t, fx.e, fx.adminCookie, fx.wsBase, `{"name":"data board"}`)
	return wd
}

func getWidgetData(t *testing.T, fx *dashboardFixture, widgetID string, cookie *http.Cookie) (int, map[string]any) {
	t.Helper()
	rec := getAuthed(t, fx.e, http.MethodGet, "/api/v1/widgets/"+widgetID+"/data", cookie)
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode widget data: %v (body: %s)", err, rec.Body.String())
	}
	return rec.Code, body
}

func groupValues(body map[string]any) map[string]float64 {
	out := map[string]float64{}
	for _, g := range body["groups"].([]any) {
		gm := g.(map[string]any)
		out[gm["label"].(string)] = gm["value"].(float64)
	}
	return out
}

func TestWidgetDataByState(t *testing.T) {
	wd := setupWidgetData(t)
	fx := wd.fx
	dashURL := fx.wsBase + "/dashboards/" + wd.dashID

	wid := createWidgetHTTP(t, fx.e, fx.adminCookie, dashURL,
		`{"title":"By state","widget_type":"bar","config":{"group_by":"state"}}`)
	code, body := getWidgetData(t, fx, wid, fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("widget data: status = %d (body: %v)", code, body)
	}
	vals := groupValues(body)
	// A: Backlog×2, Todo×1; B: Backlog×2 → Backlog 4, Todo 1.
	if vals["Backlog"] != 4 || vals["Todo"] != 1 {
		t.Fatalf("state groups = %v, want Backlog=4 Todo=1", vals)
	}
	if len(vals) != 2 {
		t.Fatalf("state groups = %v, want exactly 2 groups", vals)
	}
}

func TestWidgetDataProjectScoping(t *testing.T) {
	wd := setupWidgetData(t)
	fx := wd.fx
	dashURL := fx.wsBase + "/dashboards/" + wd.dashID

	// project_ids=[A] excludes project B's issues.
	wid := createWidgetHTTP(t, fx.e, fx.adminCookie, dashURL,
		fmt.Sprintf(`{"title":"A only","widget_type":"donut","config":{"group_by":"state","project_ids":[%q]}}`, wd.projAID))
	code, body := getWidgetData(t, fx, wid, fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("widget data: status = %d (body: %v)", code, body)
	}
	vals := groupValues(body)
	if vals["Backlog"] != 2 || vals["Todo"] != 1 {
		t.Fatalf("scoped state groups = %v, want Backlog=2 Todo=1", vals)
	}

	// number widget with the same scope counts project A only.
	wid = createWidgetHTTP(t, fx.e, fx.adminCookie, dashURL,
		fmt.Sprintf(`{"title":"A count","widget_type":"number","config":{"project_ids":[%q]}}`, wd.projAID))
	code, body = getWidgetData(t, fx, wid, fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("number data: status = %d (body: %v)", code, body)
	}
	if body["value"] != float64(3) {
		t.Fatalf("number value = %v, want 3", body["value"])
	}
	// …and unscoped counts everything.
	wid = createWidgetHTTP(t, fx.e, fx.adminCookie, dashURL,
		`{"title":"All count","widget_type":"number","config":{}}`)
	code, body = getWidgetData(t, fx, wid, fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("number data: status = %d (body: %v)", code, body)
	}
	if body["value"] != float64(5) {
		t.Fatalf("number value = %v, want 5", body["value"])
	}
}

func TestWidgetDataFiltersAND(t *testing.T) {
	wd := setupWidgetData(t)
	fx := wd.fx
	dashURL := fx.wsBase + "/dashboards/" + wd.dashID

	// priorities=[3] AND states=[Backlog of A]: a1 only (b1 is p0, a3 is Todo).
	wid := createWidgetHTTP(t, fx.e, fx.adminCookie, dashURL,
		fmt.Sprintf(`{"title":"filtered","widget_type":"number","config":{"filters":{"priorities":[3],"states":[%q]}}}`, wd.backlogA))
	code, body := getWidgetData(t, fx, wid, fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("widget data: status = %d (body: %v)", code, body)
	}
	if body["value"] != float64(1) {
		t.Fatalf("ANDed filter value = %v, want 1", body["value"])
	}

	// priorities=[3] alone: a1, a3 → 2.
	wid = createWidgetHTTP(t, fx.e, fx.adminCookie, dashURL,
		`{"title":"p3","widget_type":"number","config":{"filters":{"priorities":[3]}}}`)
	code, body = getWidgetData(t, fx, wid, fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("widget data: status = %d (body: %v)", code, body)
	}
	if body["value"] != float64(2) {
		t.Fatalf("priority filter value = %v, want 2", body["value"])
	}
}

func TestWidgetDataByPriorityLabels(t *testing.T) {
	wd := setupWidgetData(t)
	fx := wd.fx
	dashURL := fx.wsBase + "/dashboards/" + wd.dashID

	wid := createWidgetHTTP(t, fx.e, fx.adminCookie, dashURL,
		`{"title":"By priority","widget_type":"pie","config":{"group_by":"priority"}}`)
	code, body := getWidgetData(t, fx, wid, fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("widget data: status = %d (body: %v)", code, body)
	}
	vals := groupValues(body)
	// p3: a1,a3 → High 2; p1: a2 → Low 1; p0: b1 → None 1; p2: b2 → Medium 1.
	want := map[string]float64{"High": 2, "Low": 1, "None": 1, "Medium": 1}
	for label, n := range want {
		if vals[label] != n {
			t.Fatalf("priority groups = %v, want %v", vals, want)
		}
	}
}

func TestWidgetDataByAssignee(t *testing.T) {
	wd := setupWidgetData(t)
	fx := wd.fx
	dashURL := fx.wsBase + "/dashboards/" + wd.dashID

	// Assign one issue to the member via the idempotent assignee endpoint.
	issues := listIssueIDs(t, fx, wd.identA)
	if len(issues) == 0 {
		t.Fatal("no issues in project A")
	}
	rec := postAuthedJSON(t, fx.e, http.MethodPost,
		fmt.Sprintf("%s/projects/%s/issues/%s/assignees/%s", wd.wsBase, wd.identA, issues[0], fx.memberID),
		fx.adminCookie, ``)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("assign: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}

	wid := createWidgetHTTP(t, fx.e, fx.adminCookie, dashURL,
		`{"title":"By assignee","widget_type":"bar","config":{"group_by":"assignee"}}`)
	code, body := getWidgetData(t, fx, wid, fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("widget data: status = %d (body: %v)", code, body)
	}
	vals := groupValues(body)
	// Assignee display name falls back to email (no name set).
	if vals[fx.memberEmail] != 1 {
		t.Fatalf("assignee groups = %v, want %q=1", vals, fx.memberEmail)
	}
	// The other four issues land in the Unassigned bucket.
	if vals["Unassigned"] != 4 {
		t.Fatalf("assignee groups = %v, want Unassigned=4", vals)
	}
}

func TestWidgetDataByLabel(t *testing.T) {
	wd := setupWidgetData(t)
	fx := wd.fx
	projABase := wd.wsBase + "/projects/" + wd.identA
	dashURL := fx.wsBase + "/dashboards/" + wd.dashID

	// Create a label and attach it to one issue.
	rec := postAuthedJSON(t, fx.e, http.MethodPost, projABase+"/labels", fx.adminCookie,
		`{"name":"bug","color":"#ef4444"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create label: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var label struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &label); err != nil {
		t.Fatalf("decode label: %v", err)
	}
	issues := listIssueIDs(t, fx, wd.identA)
	rec = postAuthedJSON(t, fx.e, http.MethodPost,
		fmt.Sprintf("%s/issues/%s/labels/%s", projABase, issues[0], label.ID), fx.adminCookie, ``)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("attach label: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	wid := createWidgetHTTP(t, fx.e, fx.adminCookie, dashURL,
		`{"title":"By label","widget_type":"donut","config":{"group_by":"label"}}`)
	code, body := getWidgetData(t, fx, wid, fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("widget data: status = %d (body: %v)", code, body)
	}
	vals := groupValues(body)
	if vals["bug"] != 1 {
		t.Fatalf("label groups = %v, want bug=1", vals)
	}
	if vals["No label"] != 4 {
		t.Fatalf("label groups = %v, want No label=4", vals)
	}

	// Label filter ANDs: only the labeled issue counts.
	wid = createWidgetHTTP(t, fx.e, fx.adminCookie, dashURL,
		fmt.Sprintf(`{"title":"bug only","widget_type":"number","config":{"filters":{"labels":[%q]}}}`, label.ID))
	code, body = getWidgetData(t, fx, wid, fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("widget data: status = %d (body: %v)", code, body)
	}
	if body["value"] != float64(1) {
		t.Fatalf("label filter value = %v, want 1", body["value"])
	}
}

func TestWidgetDataPrivateDashboard404(t *testing.T) {
	wd := setupWidgetData(t)
	fx := wd.fx

	privID := createDashboardHTTP(t, fx.e, fx.memberCookie, fx.wsBase, `{"name":"hidden","visibility":"private"}`)
	wid := createWidgetHTTP(t, fx.e, fx.memberCookie, fx.wsBase+"/dashboards/"+privID,
		`{"title":"x","widget_type":"number","config":{}}`)

	code, _ := getWidgetData(t, fx, wid, fx.adminCookie)
	if code != http.StatusNotFound {
		t.Fatalf("private widget data by admin: status = %d, want 404", code)
	}
	code, body := getWidgetData(t, fx, wid, fx.memberCookie)
	if code != http.StatusOK {
		t.Fatalf("private widget data by creator: status = %d (body: %v)", code, body)
	}
	if body["value"] != float64(5) {
		t.Fatalf("creator sees value = %v, want 5", body["value"])
	}
}

// listIssueIDs returns the ids of a project's issues (per_page=100).
func listIssueIDs(t *testing.T, fx *dashboardFixture, identifier string) []string {
	t.Helper()
	rec := getAuthed(t, fx.e, http.MethodGet, fx.wsBase+"/projects/"+identifier+"/issues?per_page=100", fx.adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list issues: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Results []struct {
			ID string `json:"id"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode issues: %v", err)
	}
	var ids []string
	for _, iss := range body.Results {
		ids = append(ids, iss.ID)
	}
	return ids
}
