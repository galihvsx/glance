package api

// C83T0: dashboards phase-2 backend API over HTTP — the new widget
// types (config validation, gauge math, pagination), intake source
// threading, the sharing authZ matrix, templates, and CSV export.
// Real test database, no skips.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
)

// postWidgetExpect creates a widget and returns the status code + body
// (for the 400-case tests).
func postWidgetExpect(t *testing.T, e *echo.Echo, cookie *http.Cookie, dashURL, body string) *httptest.ResponseRecorder {
	t.Helper()
	return postAuthedJSON(t, e, http.MethodPost, dashURL+"/widgets", cookie, body)
}

// getWidgetDataPage fetches widget data with an explicit page param.
func getWidgetDataPage(t *testing.T, fx *dashboardFixture, widgetID string, cookie *http.Cookie, page string) (int, map[string]any) {
	t.Helper()
	path := "/api/v1/widgets/" + widgetID + "/data"
	if page != "" {
		path += "?page=" + page
	}
	rec := getAuthed(t, fx.e, http.MethodGet, path, cookie)
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode widget data: %v (body: %s)", err, rec.Body.String())
	}
	return rec.Code, body
}

// gaugeGroups indexes gauge groups by label.
func gaugeGroups(t *testing.T, body map[string]any) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	groups, ok := body["groups"].([]any)
	if !ok {
		t.Fatalf("no groups in body: %v", body)
	}
	for _, g := range groups {
		gm := g.(map[string]any)
		out[gm["label"].(string)] = gm
	}
	return out
}

func TestDashboardPhase2_SmartCounterBadGroupBy(t *testing.T) {
	fx := setupDashboardHTTP(t)
	dashID := createDashboardHTTP(t, fx.e, fx.adminCookie, fx.wsBase, `{"name":"cfg"}`)
	rec := postWidgetExpect(t, fx.e, fx.adminCookie, fx.wsBase+"/dashboards/"+dashID,
		`{"title":"c","widget_type":"smart_counter","config":{"group_by":"sprint"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad group_by: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestDashboardPhase2_SmartCounterBadMetric(t *testing.T) {
	fx := setupDashboardHTTP(t)
	dashID := createDashboardHTTP(t, fx.e, fx.adminCookie, fx.wsBase, `{"name":"cfg"}`)
	rec := postWidgetExpect(t, fx.e, fx.adminCookie, fx.wsBase+"/dashboards/"+dashID,
		`{"title":"c","widget_type":"smart_counter","config":{"group_by":"state","metric":"avg"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad metric: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestDashboardPhase2_SmartGaugeBadColor(t *testing.T) {
	fx := setupDashboardHTTP(t)
	dashID := createDashboardHTTP(t, fx.e, fx.adminCookie, fx.wsBase, `{"name":"cfg"}`)
	rec := postWidgetExpect(t, fx.e, fx.adminCookie, fx.wsBase+"/dashboards/"+dashID,
		`{"title":"g","widget_type":"smart_gauge","config":{"group_by":"state","gauge_color":"red"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad gauge_color: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestDashboardPhase2_BarRejectsMaxRows(t *testing.T) {
	fx := setupDashboardHTTP(t)
	dashID := createDashboardHTTP(t, fx.e, fx.adminCookie, fx.wsBase, `{"name":"cfg"}`)
	// max_rows is meaningless for phase-1 chart types: honest 400.
	rec := postWidgetExpect(t, fx.e, fx.adminCookie, fx.wsBase+"/dashboards/"+dashID,
		`{"title":"b","widget_type":"bar","config":{"group_by":"state","max_rows":5}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("max_rows on bar: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestDashboardPhase2_Table2DIgnoresMetric(t *testing.T) {
	fx := setupDashboardHTTP(t)
	slug := strings.TrimPrefix(fx.wsBase, "/api/v1/workspaces/")
	ident := uniqueProjectIdentifier("T2")
	createProjectHTTP(t, fx.e, fx.adminCookie, slug, "Table Proj", ident)
	projBase := fx.wsBase + "/projects/" + ident
	rec := postAuthedJSON(t, fx.e, http.MethodPost, projBase+"/issues", fx.adminCookie, `{"name":"t-issue"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create issue: status = %d", rec.Code)
	}
	dashID := createDashboardHTTP(t, fx.e, fx.adminCookie, fx.wsBase, `{"name":"cfg"}`)
	// table_2d's metric is fixed to count: accepted and ignored.
	wid := createWidgetHTTP(t, fx.e, fx.adminCookie, fx.wsBase+"/dashboards/"+dashID,
		`{"title":"t","widget_type":"table_2d","config":{"group_by":"priority","metric":"estimate_points"}}`)
	code, body := getWidgetData(t, fx, wid, fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("data: status = %d (body: %v)", code, body)
	}
	groups, ok := body["groups"].([]any)
	if !ok || len(groups) == 0 {
		t.Fatalf("no groups in table_2d data: %v", body)
	}
	if body["page"].(float64) != 1 || body["total_groups"].(float64) != float64(len(groups)) {
		t.Fatalf("pagination envelope wrong: %v", body)
	}
}

func TestDashboardPhase2_IntakeBreakdownIgnoresWidgetFilters(t *testing.T) {
	fx := setupDashboardHTTP(t)
	dashID := createDashboardHTTP(t, fx.e, fx.adminCookie, fx.wsBase, `{"name":"cfg"}`)
	wid := createWidgetHTTP(t, fx.e, fx.adminCookie, fx.wsBase+"/dashboards/"+dashID,
		`{"title":"ib","widget_type":"intake_breakdown","config":{"view":"table","filters":{"priorities":[4]}}}`)
	code, body := getWidgetData(t, fx, wid, fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("data: status = %d (body: %v)", code, body)
	}
	intake, ok := body["intake"].(map[string]any)
	if !ok {
		t.Fatalf("no intake in body: %v", body)
	}
	for _, k := range []string{"in_app_volume", "email_volume", "form_volume", "total_accepted", "total_rejected", "avg_decide_seconds"} {
		if _, ok := intake[k]; !ok {
			t.Fatalf("intake missing %q: %v", k, intake)
		}
	}
	if intake["email_volume"].(float64) != 0 {
		t.Fatalf("email_volume = %v, want 0 (no email ingestion)", intake["email_volume"])
	}
}

// gaugeFixture is a project with issues in Backlog and Done (completed
// group) states for the smart_gauge math tests.
type gaugeFixture struct {
	fx       *dashboardFixture
	projBase string
	backlog  string
	done     string
	dashID   string
}

func setupGauge(t *testing.T) *gaugeFixture {
	t.Helper()
	fx := setupDashboardHTTP(t)
	g := &gaugeFixture{fx: fx}
	slug := strings.TrimPrefix(fx.wsBase, "/api/v1/workspaces/")
	ident := uniqueProjectIdentifier("GZ")
	createProjectHTTP(t, fx.e, fx.adminCookie, slug, "Gauge Proj", ident)
	g.projBase = fx.wsBase + "/projects/" + ident

	rec := getAuthed(t, fx.e, http.MethodGet, g.projBase+"/states", fx.adminCookie)
	var slist struct {
		States []struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Group string `json:"group"`
		} `json:"states"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &slist); err != nil {
		t.Fatalf("decode states: %v", err)
	}
	for _, s := range slist.States {
		switch s.Name {
		case "Backlog":
			g.backlog = s.ID
		case "Done":
			g.done = s.ID
			if s.Group != "completed" {
				t.Fatalf("Done group = %q, want completed", s.Group)
			}
		}
	}
	if g.backlog == "" || g.done == "" {
		t.Fatalf("seeded states missing (body: %s)", rec.Body.String())
	}
	g.dashID = createDashboardHTTP(t, fx.e, fx.adminCookie, fx.wsBase, `{"name":"gauge board"}`)
	return g
}

func (g *gaugeFixture) mkIssue(t *testing.T, name, stateID, extra string) string {
	t.Helper()
	body := fmt.Sprintf(`{"name":%q,"state_id":%q%s}`, name, stateID, extra)
	rec := postAuthedJSON(t, g.fx.e, http.MethodPost, g.projBase+"/issues", g.fx.adminCookie, body)
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

func TestDashboardPhase2_SmartGaugeCountCompletion(t *testing.T) {
	g := setupGauge(t)
	// 2 completed (Done), 1 backlog.
	g.mkIssue(t, "d1", g.done, "")
	g.mkIssue(t, "d2", g.done, "")
	g.mkIssue(t, "b1", g.backlog, "")

	wid := createWidgetHTTP(t, g.fx.e, g.fx.adminCookie, g.fx.wsBase+"/dashboards/"+g.dashID,
		`{"title":"gauge","widget_type":"smart_gauge","config":{"group_by":"state_group","metric":"count"}}`)
	code, body := getWidgetData(t, g.fx, wid, g.fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("data: status = %d (body: %v)", code, body)
	}
	groups := gaugeGroups(t, body)
	done := groups["Completed"]
	if done["value"].(float64) != 2 || done["completed"].(float64) != 2 || done["percentage"].(float64) != 100 {
		t.Fatalf("Completed group = %v, want value=2 completed=2 pct=100", done)
	}
	backlog := groups["Backlog"]
	if backlog["value"].(float64) != 1 || backlog["completed"].(float64) != 0 || backlog["percentage"].(float64) != 0 {
		t.Fatalf("Backlog group = %v, want value=1 completed=0 pct=0", backlog)
	}
	if body["page"].(float64) != 1 || body["max_rows"].(float64) != 10 {
		t.Fatalf("pagination envelope wrong: %v", body)
	}
	if body["total_groups"].(float64) != float64(len(groups)) {
		t.Fatalf("total_groups = %v, want %d", body["total_groups"], len(groups))
	}
}

// createEstimateScale creates a Fib-like scale over HTTP and returns
// point key → point id.
func createEstimateScale(t *testing.T, g *gaugeFixture) map[string]string {
	t.Helper()
	rec := postAuthedJSON(t, g.fx.e, http.MethodPost, g.projBase+"/estimates", g.fx.adminCookie,
		`{"name":"Fib","points":[{"key":"3","value":3},{"key":"5","value":5}]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create estimate: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var est struct {
		Points []struct {
			ID    string `json:"id"`
			Key   string `json:"key"`
			Value int    `json:"value"`
		} `json:"points"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &est); err != nil {
		t.Fatalf("decode estimate: %v", err)
	}
	out := map[string]string{}
	for _, p := range est.Points {
		out[p.Key] = p.ID
	}
	return out
}

func TestDashboardPhase2_SmartGaugeEstimatePoints(t *testing.T) {
	g := setupGauge(t)
	points := createEstimateScale(t, g)
	// Done: 5pt + 3pt = 8 completed of 8+3=11 total → 72.72…%.
	g.mkIssue(t, "d1", g.done, fmt.Sprintf(`,"estimate_point_id":%q`, points["5"]))
	g.mkIssue(t, "d2", g.done, fmt.Sprintf(`,"estimate_point_id":%q`, points["3"]))
	g.mkIssue(t, "b1", g.backlog, fmt.Sprintf(`,"estimate_point_id":%q`, points["3"]))

	wid := createWidgetHTTP(t, g.fx.e, g.fx.adminCookie, g.fx.wsBase+"/dashboards/"+g.dashID,
		`{"title":"gauge","widget_type":"smart_gauge","config":{"group_by":"state_group","metric":"estimate_points"}}`)
	code, body := getWidgetData(t, g.fx, wid, g.fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("data: status = %d (body: %v)", code, body)
	}
	groups := gaugeGroups(t, body)
	done := groups["Completed"]
	if done["value"].(float64) != 8 || done["completed"].(float64) != 8 {
		t.Fatalf("Completed = %v, want value=8 completed=8", done)
	}
	if pct := done["percentage"].(float64); pct != 100 {
		t.Fatalf("Completed pct = %v, want 100", pct)
	}
	backlog := groups["Backlog"]
	if backlog["value"].(float64) != 3 || backlog["completed"].(float64) != 0 {
		t.Fatalf("Backlog = %v, want value=3 completed=0", backlog)
	}
	if pct := backlog["percentage"].(float64); pct != 0 {
		t.Fatalf("Backlog pct = %v, want 0", pct)
	}
}

func TestDashboardPhase2_SmartGaugeZeroEstimates(t *testing.T) {
	g := setupGauge(t)
	// Points metric, but nothing is estimated: percentage must be 0,
	// not NaN — Plane parity.
	g.mkIssue(t, "d1", g.done, "")
	g.mkIssue(t, "b1", g.backlog, "")

	wid := createWidgetHTTP(t, g.fx.e, g.fx.adminCookie, g.fx.wsBase+"/dashboards/"+g.dashID,
		`{"title":"gauge","widget_type":"smart_gauge","config":{"group_by":"state_group","metric":"estimate_points"}}`)
	code, body := getWidgetData(t, g.fx, wid, g.fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("data: status = %d (body: %v)", code, body)
	}
	groups := gaugeGroups(t, body)
	for label, grp := range groups {
		if grp["value"].(float64) != 0 {
			t.Fatalf("%s value = %v, want 0", label, grp["value"])
		}
		if pct := grp["percentage"].(float64); pct != 0 {
			t.Fatalf("%s pct = %v, want 0", label, pct)
		}
	}
}

func TestDashboardPhase2_SmartCounterPagination(t *testing.T) {
	fx := setupDashboardHTTP(t)
	slug := strings.TrimPrefix(fx.wsBase, "/api/v1/workspaces/")
	ident := uniqueProjectIdentifier("PG")
	createProjectHTTP(t, fx.e, fx.adminCookie, slug, "Page Proj", ident)
	projBase := fx.wsBase + "/projects/" + ident
	memberID := fx.memberID
	member2ID := authedUserID(t, fx.e, fx.member2Cookie)

	mkIssue := func(name string) string {
		t.Helper()
		rec := postAuthedJSON(t, fx.e, http.MethodPost, projBase+"/issues", fx.adminCookie,
			fmt.Sprintf(`{"name":%q}`, name))
		if rec.Code != http.StatusCreated {
			t.Fatalf("create issue: status = %d (body: %s)", rec.Code, rec.Body.String())
		}
		var iss struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &iss); err != nil {
			t.Fatalf("decode issue: %v", err)
		}
		return iss.ID
	}
	assign := func(issueID, userID string) {
		t.Helper()
		rec := postAuthedJSON(t, fx.e, http.MethodPost,
			projBase+"/issues/"+issueID+"/assignees/"+userID, fx.adminCookie, "")
		if rec.Code != http.StatusOK && rec.Code != http.StatusCreated && rec.Code != http.StatusNoContent {
			t.Fatalf("assign: status = %d (body: %s)", rec.Code, rec.Body.String())
		}
	}
	// 3 groups: member (2 issues), member2 (1), Unassigned (1).
	m1, m2 := mkIssue("m1"), mkIssue("m2")
	m3 := mkIssue("m3")
	mkIssue("u1")
	assign(m1, memberID)
	assign(m2, memberID)
	assign(m3, member2ID)

	dashID := createDashboardHTTP(t, fx.e, fx.adminCookie, fx.wsBase, `{"name":"page board"}`)
	wid := createWidgetHTTP(t, fx.e, fx.adminCookie, fx.wsBase+"/dashboards/"+dashID,
		`{"title":"counter","widget_type":"smart_counter","config":{"group_by":"assignee","max_rows":2}}`)

	// Page 1: 2 of 3 groups, ordered by count desc.
	code, body := getWidgetDataPage(t, fx, wid, fx.adminCookie, "1")
	if code != http.StatusOK {
		t.Fatalf("page 1: status = %d (body: %v)", code, body)
	}
	if n := len(body["groups"].([]any)); n != 2 {
		t.Fatalf("page 1 groups = %d, want 2", n)
	}
	if body["total_groups"].(float64) != 3 || body["max_rows"].(float64) != 2 || body["page"].(float64) != 1 {
		t.Fatalf("page 1 envelope wrong: %v", body)
	}
	// Page 2: the last group.
	code, body = getWidgetDataPage(t, fx, wid, fx.adminCookie, "2")
	if code != http.StatusOK {
		t.Fatalf("page 2: status = %d (body: %v)", code, body)
	}
	groups := body["groups"].([]any)
	if len(groups) != 1 {
		t.Fatalf("page 2 groups = %d, want 1", len(groups))
	}
	if body["page"].(float64) != 2 {
		t.Fatalf("page = %v, want 2", body["page"])
	}
	// Page 3: past the end → empty groups (key absent via omitempty),
	// still 200.
	code, body = getWidgetDataPage(t, fx, wid, fx.adminCookie, "3")
	if code != http.StatusOK {
		t.Fatalf("page 3: status = %d", code)
	}
	if g3, ok := body["groups"]; ok {
		if n := len(g3.([]any)); n != 0 {
			t.Fatalf("page 3 groups = %d, want 0", n)
		}
	}
	// Bad page → 400.
	rec := getAuthed(t, fx.e, http.MethodGet, "/api/v1/widgets/"+wid+"/data?page=0", fx.adminCookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("page=0: status = %d, want 400", rec.Code)
	}
	rec = getAuthed(t, fx.e, http.MethodGet, "/api/v1/widgets/"+wid+"/data?page=nope", fx.adminCookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("page=nope: status = %d, want 400", rec.Code)
	}
}

func TestDashboardPhase2_Table2DMatchesBarCounts(t *testing.T) {
	// The review contract: table_2d numbers must match the equivalent
	// bar chart for the same grouping.
	g := setupGauge(t)
	g.mkIssue(t, "d1", g.done, "")
	g.mkIssue(t, "d2", g.done, "")
	g.mkIssue(t, "b1", g.backlog, "")

	barID := createWidgetHTTP(t, g.fx.e, g.fx.adminCookie, g.fx.wsBase+"/dashboards/"+g.dashID,
		`{"title":"bar","widget_type":"bar","config":{"group_by":"state"}}`)
	tabID := createWidgetHTTP(t, g.fx.e, g.fx.adminCookie, g.fx.wsBase+"/dashboards/"+g.dashID,
		`{"title":"tab","widget_type":"table_2d","config":{"group_by":"state"}}`)
	_, barBody := getWidgetData(t, g.fx, barID, g.fx.adminCookie)
	_, tabBody := getWidgetData(t, g.fx, tabID, g.fx.adminCookie)
	barVals, tabVals := groupValues(barBody), map[string]float64{}
	for _, g2 := range tabBody["groups"].([]any) {
		gm := g2.(map[string]any)
		tabVals[gm["label"].(string)] = gm["value"].(float64)
	}
	if len(barVals) != len(tabVals) {
		t.Fatalf("group count differs: bar=%v table=%v", barVals, tabVals)
	}
	for k, v := range barVals {
		if tabVals[k] != v {
			t.Fatalf("group %q: bar=%v table_2d=%v", k, v, tabVals[k])
		}
	}
}

// intakeSourceOf reads the intake_issues.source for an issue id.
func intakeSourceOf(t *testing.T, fx *dashboardFixture, issueID string) string {
	t.Helper()
	var source string
	err := fx.pool.QueryRow(context.Background(),
		`SELECT source FROM intake_issues WHERE issue_id = $1::uuid`, issueID).Scan(&source)
	if err != nil {
		t.Fatalf("read intake source: %v", err)
	}
	return source
}

func TestDashboardPhase2_IntakeSourceForm(t *testing.T) {
	pool := newTestPool(t)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterIntakeRoutes(e, &IssueHandler{Pool: pool})
	RegisterIntakeFormPublicRoutes(e, &IssueHandler{Pool: pool})
	cookie := loginTestUser(t, e, pool, uniqueEmail("c83src"), "test-agent/1.0", uniqueIP())
	formsBase, _ := setupIntakeFormProject(t, e, cookie, "c83src")
	created := createIntakeFormHTTP(t, e, cookie, formsBase,
		`{"name":"Bug report","fields":`+titleOnlyFields+`}`)

	rec := postPublicJSON(t, e, "/api/v1/forms/"+created.ShareToken+"/submit",
		`{"values":{"title":"from the public form"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("submit: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var submitted struct {
		IssueID string `json:"issue_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &submitted); err != nil {
		t.Fatalf("decode submit: %v", err)
	}
	fx := &dashboardFixture{pool: pool, e: e}
	if got := intakeSourceOf(t, fx, submitted.IssueID); got != "form" {
		t.Fatalf("source = %q, want form", got)
	}
}

func TestDashboardPhase2_IntakeSourceInApp(t *testing.T) {
	fx := setupDashboardHTTP(t)
	slug := strings.TrimPrefix(fx.wsBase, "/api/v1/workspaces/")
	ident := uniqueProjectIdentifier("IA")
	createProjectHTTP(t, fx.e, fx.adminCookie, slug, "Intake Proj", ident)
	projBase := fx.wsBase + "/projects/" + ident

	rec := postAuthedJSON(t, fx.e, http.MethodPost, projBase+"/issues", fx.adminCookie,
		`{"name":"in-app intake","intake":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create intake issue: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var iss struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &iss); err != nil {
		t.Fatalf("decode issue: %v", err)
	}
	if got := intakeSourceOf(t, fx, iss.ID); got != "in_app" {
		t.Fatalf("source = %q, want in_app", got)
	}
}

// putShares performs the PUT shares call and decodes the share list.
func putShares(t *testing.T, e *echo.Echo, cookie *http.Cookie, dashURL, body string) (int, []map[string]any) {
	t.Helper()
	rec := postAuthedJSON(t, e, http.MethodPut, dashURL+"/shares", cookie, body)
	var decoded struct {
		Shares []map[string]any `json:"shares"`
	}
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("decode shares: %v (body: %s)", err, rec.Body.String())
		}
	}
	return rec.Code, decoded.Shares
}

func shareDashFixture(t *testing.T) (*dashboardFixture, string, string) {
	t.Helper()
	fx := setupDashboardHTTP(t)
	dashID := createDashboardHTTP(t, fx.e, fx.adminCookie, fx.wsBase,
		`{"name":"shared board","visibility":"private"}`)
	wid := createWidgetHTTP(t, fx.e, fx.adminCookie, fx.wsBase+"/dashboards/"+dashID,
		`{"title":"w","widget_type":"bar","config":{"group_by":"state"}}`)
	return fx, fx.wsBase + "/dashboards/" + dashID, wid
}

func TestDashboardPhase2_ShareViewCannotEdit(t *testing.T) {
	fx, dashURL, wid := shareDashFixture(t)
	// Before the share: private dashboard is 404 for the member.
	rec := getAuthed(t, fx.e, http.MethodGet, dashURL, fx.memberCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("pre-share GET: status = %d, want 404", rec.Code)
	}
	code, _ := putShares(t, fx.e, fx.adminCookie, dashURL,
		fmt.Sprintf(`{"shares":[{"member_id":%q,"access":"view"}]}`, fx.memberID))
	if code != http.StatusOK {
		t.Fatalf("PUT shares: status = %d", code)
	}
	// View access: reads work…
	if rec := getAuthed(t, fx.e, http.MethodGet, dashURL, fx.memberCookie); rec.Code != http.StatusOK {
		t.Fatalf("shared GET dashboard: status = %d, want 200", rec.Code)
	}
	if code, _ := getWidgetData(t, fx, wid, fx.memberCookie); code != http.StatusOK {
		t.Fatalf("shared GET widget data: status = %d, want 200", code)
	}
	if rec := getAuthed(t, fx.e, http.MethodGet, "/api/v1/widgets/"+wid+"/export.csv", fx.memberCookie); rec.Code != http.StatusOK {
		t.Fatalf("shared GET export.csv: status = %d, want 200", rec.Code)
	}
	// …but every write is 403.
	if rec := postAuthedJSON(t, fx.e, http.MethodPost, dashURL+"/widgets", fx.memberCookie,
		`{"title":"x","widget_type":"bar","config":{"group_by":"state"}}`); rec.Code != http.StatusForbidden {
		t.Fatalf("view-share create widget: status = %d, want 403", rec.Code)
	}
	if rec := postAuthedJSON(t, fx.e, http.MethodPatch, "/api/v1/widgets/"+wid, fx.memberCookie,
		`{"title":"renamed"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("view-share patch widget: status = %d, want 403", rec.Code)
	}
	if rec := postAuthedJSON(t, fx.e, http.MethodPatch, dashURL, fx.memberCookie,
		`{"name":"renamed"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("view-share patch dashboard: status = %d, want 403", rec.Code)
	}
	if code, _ := putShares(t, fx.e, fx.memberCookie, dashURL, `{"shares":[]}`); code != http.StatusForbidden {
		t.Fatalf("view-share PUT shares: status = %d, want 403", code)
	}
}

func TestDashboardPhase2_ShareEditAccessMatrix(t *testing.T) {
	fx, dashURL, _ := shareDashFixture(t)
	member2ID := authedUserID(t, fx.e, fx.member2Cookie)
	code, _ := putShares(t, fx.e, fx.adminCookie, dashURL,
		fmt.Sprintf(`{"shares":[{"member_id":%q,"access":"edit"}]}`, member2ID))
	if code != http.StatusOK {
		t.Fatalf("PUT shares: status = %d", code)
	}
	// Edit access: widget CRUD + layout + dashboard PATCH (name) work…
	wid := createWidgetHTTP(t, fx.e, fx.member2Cookie, dashURL,
		`{"title":"m2w","widget_type":"bar","config":{"group_by":"state"}}`)
	if rec := postAuthedJSON(t, fx.e, http.MethodPatch, dashURL, fx.member2Cookie,
		`{"name":"renamed by editor"}`); rec.Code != http.StatusOK {
		t.Fatalf("edit-share patch name: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if rec := postAuthedJSON(t, fx.e, http.MethodPatch, "/api/v1/widgets/"+wid+"/layout", fx.member2Cookie,
		`{"grid_x":1}`); rec.Code != http.StatusOK {
		t.Fatalf("edit-share layout: status = %d, want 200", rec.Code)
	}
	// …but owner-only operations are 403: delete, visibility change,
	// share management.
	if rec := postAuthedJSON(t, fx.e, http.MethodDelete, dashURL, fx.member2Cookie, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("edit-share delete dashboard: status = %d, want 403", rec.Code)
	}
	if rec := postAuthedJSON(t, fx.e, http.MethodPatch, dashURL, fx.member2Cookie,
		`{"visibility":"public"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("edit-share visibility change: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
	if code, _ := putShares(t, fx.e, fx.member2Cookie, dashURL, `{"shares":[]}`); code != http.StatusForbidden {
		t.Fatalf("edit-share PUT shares: status = %d, want 403", code)
	}
	if rec := getAuthed(t, fx.e, http.MethodGet, dashURL+"/shares", fx.member2Cookie); rec.Code != http.StatusForbidden {
		t.Fatalf("edit-share GET shares: status = %d, want 403", rec.Code)
	}
	// Promote member2 to workspace admin (still NOT the dashboard
	// creator): owner-only ops stay 403 — Plane's owner-only rule, not
	// the phase-1 admin-edit rule.
	if rec := postAuthedJSON(t, fx.e, http.MethodPost, fx.wsBase+"/members", fx.adminCookie,
		fmt.Sprintf(`{"user_id":%q,"role":20}`, member2ID)); rec.Code != http.StatusOK {
		t.Fatalf("promote member2: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if rec := postAuthedJSON(t, fx.e, http.MethodDelete, dashURL, fx.member2Cookie, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("non-creator admin delete dashboard: status = %d, want 403", rec.Code)
	}
	if rec := postAuthedJSON(t, fx.e, http.MethodPatch, dashURL, fx.member2Cookie,
		`{"visibility":"public"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("non-creator admin visibility change: status = %d, want 403", rec.Code)
	}
	// The owner can still delete.
	if rec := postAuthedJSON(t, fx.e, http.MethodDelete, dashURL, fx.adminCookie, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("owner delete dashboard: status = %d, want 204", rec.Code)
	}
}

func TestDashboardPhase2_ShareGuestRejected(t *testing.T) {
	fx, dashURL, _ := shareDashFixture(t)
	guestID := authedUserID(t, fx.e, fx.guestCookie)
	code, _ := putShares(t, fx.e, fx.adminCookie, dashURL,
		fmt.Sprintf(`{"shares":[{"member_id":%q,"access":"view"}]}`, guestID))
	if code != http.StatusBadRequest {
		t.Fatalf("guest share: status = %d, want 400", code)
	}
	// The creator cannot share with themselves either.
	adminID := authedUserID(t, fx.e, fx.adminCookie)
	code, _ = putShares(t, fx.e, fx.adminCookie, dashURL,
		fmt.Sprintf(`{"shares":[{"member_id":%q,"access":"view"}]}`, adminID))
	if code != http.StatusBadRequest {
		t.Fatalf("self share: status = %d, want 400", code)
	}
	// A non-member cannot be added.
	code, _ = putShares(t, fx.e, fx.adminCookie, dashURL,
		`{"shares":[{"member_id":"00000000-0000-0000-0000-000000000099","access":"view"}]}`)
	if code != http.StatusBadRequest {
		t.Fatalf("non-member share: status = %d, want 400", code)
	}
	// A bad access value is 400.
	code, _ = putShares(t, fx.e, fx.adminCookie, dashURL,
		fmt.Sprintf(`{"shares":[{"member_id":%q,"access":"admin"}]}`, fx.memberID))
	if code != http.StatusBadRequest {
		t.Fatalf("bad access share: status = %d, want 400", code)
	}
}

func TestDashboardPhase2_PrivateDashboardSharedVisible(t *testing.T) {
	fx, dashURL, _ := shareDashFixture(t)
	code, _ := putShares(t, fx.e, fx.adminCookie, dashURL,
		fmt.Sprintf(`{"shares":[{"member_id":%q,"access":"view"}]}`, fx.memberID))
	if code != http.StatusOK {
		t.Fatalf("PUT shares: status = %d", code)
	}
	rec := getAuthed(t, fx.e, http.MethodGet, dashURL, fx.memberCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("shared GET private dashboard: status = %d, want 200", rec.Code)
	}
	var d struct {
		SharedWithMe bool   `json:"shared_with_me"`
		MyAccess     string `json:"my_access"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatalf("decode dashboard: %v", err)
	}
	if !d.SharedWithMe || d.MyAccess != "view" {
		t.Fatalf("shared_with_me=%v my_access=%q, want true/view", d.SharedWithMe, d.MyAccess)
	}
	// A member WITHOUT a share still gets 404 (no leak).
	member2ID := authedUserID(t, fx.e, fx.member2Cookie)
	_ = member2ID
	if rec := getAuthed(t, fx.e, http.MethodGet, dashURL, fx.member2Cookie); rec.Code != http.StatusNotFound {
		t.Fatalf("unshared GET private dashboard: status = %d, want 404", rec.Code)
	}
}

func TestDashboardPhase2_SharePutReplaces(t *testing.T) {
	fx, dashURL, _ := shareDashFixture(t)
	member2ID := authedUserID(t, fx.e, fx.member2Cookie)
	code, _ := putShares(t, fx.e, fx.adminCookie, dashURL,
		fmt.Sprintf(`{"shares":[{"member_id":%q,"access":"view"}]}`, fx.memberID))
	if code != http.StatusOK {
		t.Fatalf("PUT shares 1: status = %d", code)
	}
	// Replace: member out, member2 in with edit.
	code, shares := putShares(t, fx.e, fx.adminCookie, dashURL,
		fmt.Sprintf(`{"shares":[{"member_id":%q,"access":"edit"}]}`, member2ID))
	if code != http.StatusOK {
		t.Fatalf("PUT shares 2: status = %d", code)
	}
	if len(shares) != 1 || shares[0]["member_id"] != member2ID || shares[0]["access"] != "edit" {
		t.Fatalf("replaced shares = %v", shares)
	}
	if rec := getAuthed(t, fx.e, http.MethodGet, dashURL, fx.memberCookie); rec.Code != http.StatusNotFound {
		t.Fatalf("removed member GET: status = %d, want 404", rec.Code)
	}
	if rec := getAuthed(t, fx.e, http.MethodGet, dashURL, fx.member2Cookie); rec.Code != http.StatusOK {
		t.Fatalf("new member GET: status = %d, want 200", rec.Code)
	}
	// Empty list clears every share.
	code, shares = putShares(t, fx.e, fx.adminCookie, dashURL, `{"shares":[]}`)
	if code != http.StatusOK || len(shares) != 0 {
		t.Fatalf("clear shares: status = %d shares = %v", code, shares)
	}
	if rec := getAuthed(t, fx.e, http.MethodGet, dashURL, fx.member2Cookie); rec.Code != http.StatusNotFound {
		t.Fatalf("cleared member GET: status = %d, want 404", rec.Code)
	}
}

func TestDashboardPhase2_ListSharingFlags(t *testing.T) {
	fx := setupDashboardHTTP(t)
	pubID := createDashboardHTTP(t, fx.e, fx.adminCookie, fx.wsBase, `{"name":"public board"}`)
	privID := createDashboardHTTP(t, fx.e, fx.adminCookie, fx.wsBase, `{"name":"private board","visibility":"private"}`)
	privURL := fx.wsBase + "/dashboards/" + privID
	code, _ := putShares(t, fx.e, fx.adminCookie, privURL,
		fmt.Sprintf(`{"shares":[{"member_id":%q,"access":"edit"}]}`, fx.memberID))
	if code != http.StatusOK {
		t.Fatalf("PUT shares: status = %d", code)
	}

	// Member's list: the public board (view, not shared-with-me) and
	// the shared private board.
	rec := getAuthed(t, fx.e, http.MethodGet, fx.wsBase+"/dashboards", fx.memberCookie)
	var mlist struct {
		Dashboards []struct {
			ID           string `json:"id"`
			SharedWithMe bool   `json:"shared_with_me"`
			MyAccess     string `json:"my_access"`
			Shared       bool   `json:"shared"`
		} `json:"dashboards"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &mlist); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	byID := map[string]struct {
		SharedWithMe bool
		MyAccess     string
		Shared       bool
	}{}
	for _, d := range mlist.Dashboards {
		byID[d.ID] = struct {
			SharedWithMe bool
			MyAccess     string
			Shared       bool
		}{d.SharedWithMe, d.MyAccess, d.Shared}
	}
	pub, ok := byID[pubID]
	if !ok || pub.SharedWithMe || pub.MyAccess != "view" {
		t.Fatalf("member public row = %+v, want view/no-share", pub)
	}
	priv, ok := byID[privID]
	if !ok || !priv.SharedWithMe || priv.MyAccess != "edit" {
		t.Fatalf("member private row = %+v, want shared_with_me/edit", priv)
	}

	// Owner's list: my_access=owner everywhere; shared=true on the
	// dashboard shared out.
	rec = getAuthed(t, fx.e, http.MethodGet, fx.wsBase+"/dashboards", fx.adminCookie)
	var olist struct {
		Dashboards []struct {
			ID           string `json:"id"`
			SharedWithMe bool   `json:"shared_with_me"`
			MyAccess     string `json:"my_access"`
			Shared       bool   `json:"shared"`
		} `json:"dashboards"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &olist); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	obyID := map[string]struct {
		MyAccess string
		Shared   bool
	}{}
	for _, d := range olist.Dashboards {
		obyID[d.ID] = struct {
			MyAccess string
			Shared   bool
		}{d.MyAccess, d.Shared}
	}
	if o := obyID[pubID]; o.MyAccess != "owner" || o.Shared {
		t.Fatalf("owner public row = %+v, want owner/unshared", o)
	}
	if o := obyID[privID]; o.MyAccess != "owner" || !o.Shared {
		t.Fatalf("owner private row = %+v, want owner/shared", o)
	}
}

func TestDashboardPhase2_TemplateCatalog(t *testing.T) {
	fx := setupDashboardHTTP(t)
	rec := getAuthed(t, fx.e, http.MethodGet, fx.wsBase+"/dashboard-templates", fx.adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("templates: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var catalog struct {
		Templates []struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Description string `json:"description"`
			Widgets     []struct {
				Title      string         `json:"title"`
				WidgetType string         `json:"widget_type"`
				Config     map[string]any `json:"config"`
				GridX      int            `json:"grid_x"`
				GridY      int            `json:"grid_y"`
				GridW      int            `json:"grid_w"`
				GridH      int            `json:"grid_h"`
			} `json:"widgets"`
		} `json:"templates"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &catalog); err != nil {
		t.Fatalf("decode catalog: %v", err)
	}
	if len(catalog.Templates) < 3 {
		t.Fatalf("catalog has %d templates, want >= 3", len(catalog.Templates))
	}
	for _, tpl := range catalog.Templates {
		if tpl.ID == "" || tpl.Name == "" || tpl.Description == "" || len(tpl.Widgets) == 0 {
			t.Fatalf("template shape wrong: %+v", tpl)
		}
	}
	// Guests are 403.
	if rec := getAuthed(t, fx.e, http.MethodGet, fx.wsBase+"/dashboard-templates", fx.guestCookie); rec.Code != http.StatusForbidden {
		t.Fatalf("guest templates: status = %d, want 403", rec.Code)
	}
}

func TestDashboardPhase2_CreateFromTemplate(t *testing.T) {
	fx := setupDashboardHTTP(t)
	rec := postAuthedJSON(t, fx.e, http.MethodPost, fx.wsBase+"/dashboards", fx.adminCookie,
		`{"template":"sprint-health"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create from template: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var d struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Visibility string `json:"visibility"`
		CreatedBy  string `json:"created_by"`
		Widgets    []struct {
			Title      string `json:"title"`
			WidgetType string `json:"widget_type"`
		} `json:"widgets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatalf("decode dashboard: %v", err)
	}
	if d.Name != "Sprint health" {
		t.Fatalf("name = %q, want template default", d.Name)
	}
	if d.Visibility != "public" {
		t.Fatalf("visibility = %q, want public", d.Visibility)
	}
	adminID := authedUserID(t, fx.e, fx.adminCookie)
	if d.CreatedBy != adminID {
		t.Fatalf("created_by = %q, want caller", d.CreatedBy)
	}
	if len(d.Widgets) != 2 {
		t.Fatalf("widgets = %d, want 2 (atomic materialization)", len(d.Widgets))
	}
	types := map[string]bool{}
	for _, w := range d.Widgets {
		types[w.WidgetType] = true
	}
	if !types["smart_gauge"] || !types["smart_counter"] {
		t.Fatalf("widget types = %v, want smart_gauge + smart_counter", types)
	}
	// Explicit name overrides the template default.
	rec = postAuthedJSON(t, fx.e, http.MethodPost, fx.wsBase+"/dashboards", fx.adminCookie,
		`{"template":"triage-performance","name":"My triage"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create with name: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var d2 struct {
		Name    string `json:"name"`
		Widgets []any  `json:"widgets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &d2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if d2.Name != "My triage" || len(d2.Widgets) != 2 {
		t.Fatalf("name=%q widgets=%d", d2.Name, len(d2.Widgets))
	}
}

func TestDashboardPhase2_CreateFromTemplateBadID(t *testing.T) {
	fx := setupDashboardHTTP(t)
	rec := postAuthedJSON(t, fx.e, http.MethodPost, fx.wsBase+"/dashboards", fx.adminCookie,
		`{"template":"nope"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad template: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestDashboardPhase2_TemplateMutuallyExclusive(t *testing.T) {
	fx := setupDashboardHTTP(t)
	for _, body := range []string{
		`{"template":"sprint-health","visibility":"private"}`,
		`{"template":"sprint-health","description":"x"}`,
	} {
		rec := postAuthedJSON(t, fx.e, http.MethodPost, fx.wsBase+"/dashboards", fx.adminCookie, body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400", body, rec.Code)
		}
	}
}

func TestDashboardPhase2_CSVExport(t *testing.T) {
	fx := setupDashboardHTTP(t)
	slug := strings.TrimPrefix(fx.wsBase, "/api/v1/workspaces/")
	ident := uniqueProjectIdentifier("CE")
	createProjectHTTP(t, fx.e, fx.adminCookie, slug, "CSV Proj", ident)
	projBase := fx.wsBase + "/projects/" + ident

	// A label with CSV-hostile characters to prove quoting.
	rec := postAuthedJSON(t, fx.e, http.MethodPost, projBase+"/labels", fx.adminCookie,
		`{"name":"Q3, \"final\"","color":"#ff0000"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create label: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var label struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &label); err != nil {
		t.Fatalf("decode label: %v", err)
	}
	mkIssue := func(name string) string {
		t.Helper()
		rec := postAuthedJSON(t, fx.e, http.MethodPost, projBase+"/issues", fx.adminCookie,
			fmt.Sprintf(`{"name":%q}`, name))
		if rec.Code != http.StatusCreated {
			t.Fatalf("create issue: status = %d", rec.Code)
		}
		var iss struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &iss); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return iss.ID
	}
	i1, i2 := mkIssue("one"), mkIssue("two")
	i3 := mkIssue("three") // unlabeled → the "No label" bucket
	_ = i3
	for _, id := range []string{i1, i2} {
		rec := postAuthedJSON(t, fx.e, http.MethodPost,
			projBase+"/issues/"+id+"/labels/"+label.ID, fx.adminCookie, "")
		if rec.Code != http.StatusOK && rec.Code != http.StatusCreated && rec.Code != http.StatusNoContent {
			t.Fatalf("assign label: status = %d (body: %s)", rec.Code, rec.Body.String())
		}
	}

	dashID := createDashboardHTTP(t, fx.e, fx.adminCookie, fx.wsBase, `{"name":"Export Board"}`)
	// max_rows=1 but the export must carry the FULL dataset.
	wid := createWidgetHTTP(t, fx.e, fx.adminCookie, fx.wsBase+"/dashboards/"+dashID,
		`{"title":"Label, count","widget_type":"smart_counter","config":{"group_by":"label","max_rows":1}}`)

	rec = getAuthed(t, fx.e, http.MethodGet, "/api/v1/widgets/"+wid+"/export.csv", fx.adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("export: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Fatalf("content-type = %q, want text/csv", ct)
	}
	cd := rec.Header().Get("Content-Disposition")
	if !strings.HasPrefix(cd, "attachment;") || !strings.Contains(cd, ".csv") {
		t.Fatalf("content-disposition = %q", cd)
	}
	if !strings.Contains(cd, "export-board-label-count.csv") {
		t.Fatalf("filename not sanitized: %q", cd)
	}
	body := rec.Body.String()
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if len(lines) != 3 { // header + 2 label groups (Q3 label + "No label")
		t.Fatalf("csv lines = %d, want 3 (full dataset, not the page):\n%s", len(lines), body)
	}
	if lines[0] != "key,label,value" {
		t.Fatalf("header = %q", lines[0])
	}
	if !strings.Contains(body, `"Q3, ""final"""`) {
		t.Fatalf("label not CSV-quoted:\n%s", body)
	}
	if rec.Header().Get("X-Result-Truncated") != "" {
		t.Fatalf("unexpected truncation header")
	}
}

func TestDashboardPhase2_CSVIntakeBreakdown(t *testing.T) {
	fx := setupDashboardHTTP(t)
	dashID := createDashboardHTTP(t, fx.e, fx.adminCookie, fx.wsBase, `{"name":"csv intake"}`)
	wid := createWidgetHTTP(t, fx.e, fx.adminCookie, fx.wsBase+"/dashboards/"+dashID,
		`{"title":"breakdown","widget_type":"intake_breakdown","config":{}}`)
	rec := getAuthed(t, fx.e, http.MethodGet, "/api/v1/widgets/"+wid+"/export.csv", fx.adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("export: status = %d", rec.Code)
	}
	body := rec.Body.String()
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if len(lines) != 7 { // header + 6 headline numbers
		t.Fatalf("csv lines = %d, want 7:\n%s", len(lines), body)
	}
	if lines[0] != "metric,value" {
		t.Fatalf("header = %q", lines[0])
	}
	for _, metric := range []string{"in_app_volume", "email_volume", "form_volume", "total_accepted", "total_rejected", "avg_decide_seconds"} {
		if !strings.Contains(body, metric+",") {
			t.Fatalf("csv missing metric %q:\n%s", metric, body)
		}
	}
}

func TestDashboardPhase2_CSVRequiresRead(t *testing.T) {
	fx, _, wid := shareDashFixture(t)
	// Private dashboard, no share: 404 (never a leak).
	if rec := getAuthed(t, fx.e, http.MethodGet, "/api/v1/widgets/"+wid+"/export.csv", fx.memberCookie); rec.Code != http.StatusNotFound {
		t.Fatalf("unshared export: status = %d, want 404", rec.Code)
	}
	// Guest: 403.
	if rec := getAuthed(t, fx.e, http.MethodGet, "/api/v1/widgets/"+wid+"/export.csv", fx.guestCookie); rec.Code != http.StatusForbidden {
		t.Fatalf("guest export: status = %d, want 403", rec.Code)
	}
}
