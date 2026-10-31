package api

// C84T0: dashboards phase-3 backend API over HTTP — the row-list
// widgets (work_items_table, assigned_to_me): config validation, the
// canonical row shape, sorting, pagination, ANDed filters, the
// assigned_to_me assignee forcing; the drill-down endpoint
// (GET /api/v1/widgets/:id/drilldown): segment→item resolution per
// group-by dimension, metric invariance, honest 400s, and the shared-
// view authZ; CSV export for the new types; the OpenAPI phase-3 doc
// pins. Real test database, no skips.
//
// Note on line/area "time buckets": this codebase computes line/area
// through the same group_by dimensions as every other chart (there is
// no time-bucket code path), so line/area drill down on their group_by
// dim exactly like bar — TestC84_DrilldownLineGroup pins that.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"glance/docs"
)

// getDrilldown fetches the drill-down endpoint with a raw query string.
func getDrilldown(t *testing.T, fx *dashboardFixture, widgetID string, cookie *http.Cookie, query string) (int, map[string]any) {
	t.Helper()
	rec := getAuthed(t, fx.e, http.MethodGet, "/api/v1/widgets/"+widgetID+"/drilldown"+query, cookie)
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode drilldown: %v (body: %s)", err, rec.Body.String())
	}
	return rec.Code, body
}

// getWidgetCSV fetches the CSV export, returning status, headers, and body.
func getWidgetCSV(t *testing.T, fx *dashboardFixture, widgetID string, cookie *http.Cookie) (int, http.Header, string) {
	t.Helper()
	rec := getAuthed(t, fx.e, http.MethodGet, "/api/v1/widgets/"+widgetID+"/export.csv", cookie)
	return rec.Code, rec.Header(), rec.Body.String()
}

// rowListFixture is a widget-data world plus a row-list dashboard URL.
type rowListFixture struct {
	wd      *widgetDataFixture
	dashURL string
}

func setupRowList(t *testing.T) *rowListFixture {
	t.Helper()
	wd := setupWidgetData(t)
	return &rowListFixture{wd: wd, dashURL: wd.fx.wsBase + "/dashboards/" + wd.dashID}
}

// rowTitles extracts the row titles in order.
func rowTitles(t *testing.T, body map[string]any) []string {
	t.Helper()
	rows, ok := body["rows"].([]any)
	if !ok {
		t.Fatalf("no rows in body: %v", body)
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.(map[string]any)["title"].(string))
	}
	return out
}

// drilldownIDs extracts the drill-down item ids as a set.
func drilldownIDs(t *testing.T, body map[string]any) map[string]bool {
	t.Helper()
	items, ok := body["items"].([]any)
	if !ok {
		t.Fatalf("no items in drilldown body: %v", body)
	}
	out := map[string]bool{}
	for _, it := range items {
		out[it.(map[string]any)["id"].(string)] = true
	}
	return out
}

func TestC84_RowListBadColumn(t *testing.T) {
	fx := setupRowList(t)
	rec := postWidgetExpect(t, fx.wd.fx.e, fx.wd.fx.adminCookie, fx.dashURL,
		`{"title":"t","widget_type":"work_items_table","config":{"columns":["title","bogus"]}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad column: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestC84_RowListBadSortBy(t *testing.T) {
	fx := setupRowList(t)
	// sort_by must sit inside the configured columns.
	rec := postWidgetExpect(t, fx.wd.fx.e, fx.wd.fx.adminCookie, fx.dashURL,
		`{"title":"t","widget_type":"work_items_table","config":{"columns":["title"],"sort_by":"priority"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("sort_by outside columns: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	// Unknown sort_by is 400 even with default columns.
	rec = postWidgetExpect(t, fx.wd.fx.e, fx.wd.fx.adminCookie, fx.dashURL,
		`{"title":"t","widget_type":"work_items_table","config":{"sort_by":"sprint"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad sort_by: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestC84_RowListBadSortDir(t *testing.T) {
	fx := setupRowList(t)
	rec := postWidgetExpect(t, fx.wd.fx.e, fx.wd.fx.adminCookie, fx.dashURL,
		`{"title":"t","widget_type":"work_items_table","config":{"sort_dir":"sideways"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad sort_dir: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestC84_RowListGroupByIgnored(t *testing.T) {
	fx := setupRowList(t)
	// group_by is meaningless for a row list: accepted and ignored
	// (a stale config survives a type flip without a 400).
	wid := createWidgetHTTP(t, fx.wd.fx.e, fx.wd.fx.adminCookie, fx.dashURL,
		`{"title":"t","widget_type":"work_items_table","config":{"group_by":"state"}}`)
	code, body := getWidgetData(t, fx.wd.fx, wid, fx.wd.fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("data: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(5) {
		t.Fatalf("total = %v, want 5", body["total"])
	}
}

func TestC84_RowListMetricIgnored(t *testing.T) {
	fx := setupRowList(t)
	// The metric is fixed to item rows: accepted and ignored.
	wid := createWidgetHTTP(t, fx.wd.fx.e, fx.wd.fx.adminCookie, fx.dashURL,
		`{"title":"t","widget_type":"assigned_to_me","config":{"metric":"estimate_points"}}`)
	code, body := getWidgetData(t, fx.wd.fx, wid, fx.wd.fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("data: status = %d (body: %v)", code, body)
	}
	// The admin has no assigned issues: total 0 (rows absent on an
	// empty page, like the phase-2 groups convention).
	if body["total"] != float64(0) {
		t.Fatalf("total = %v, want 0", body["total"])
	}
}

func TestC84_RowListRejectsAlienKeys(t *testing.T) {
	fx := setupRowList(t)
	// visualization belongs to smart_counter only.
	rec := postWidgetExpect(t, fx.wd.fx.e, fx.wd.fx.adminCookie, fx.dashURL,
		`{"title":"t","widget_type":"work_items_table","config":{"visualization":"table"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("visualization on work_items_table: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	// columns belongs to the row-list types only.
	rec = postWidgetExpect(t, fx.wd.fx.e, fx.wd.fx.adminCookie, fx.dashURL,
		`{"title":"b","widget_type":"bar","config":{"group_by":"state","columns":["title"]}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("columns on bar: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestC84_RowListRowsShape(t *testing.T) {
	fx := setupRowList(t)
	wid := createWidgetHTTP(t, fx.wd.fx.e, fx.wd.fx.adminCookie, fx.dashURL,
		`{"title":"t","widget_type":"work_items_table","config":{}}`)
	code, body := getWidgetData(t, fx.wd.fx, wid, fx.wd.fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("data: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(5) {
		t.Fatalf("total = %v, want 5", body["total"])
	}
	if body["page"] != float64(1) || body["max_rows"] != float64(10) {
		t.Fatalf("envelope = %v, want page 1 max_rows 10", body)
	}
	rows := body["rows"].([]any)
	if len(rows) != 5 {
		t.Fatalf("rows = %d, want 5", len(rows))
	}
	first := rows[0].(map[string]any)
	for _, k := range []string{"id", "title", "state", "state_group", "priority", "assignee", "labels", "project"} {
		if _, ok := first[k]; !ok {
			t.Fatalf("row missing key %q (keys: %v)", k, first)
		}
	}
	// Unassigned → assignee null, labels [].
	if first["assignee"] != nil {
		t.Fatalf("assignee = %v, want null", first["assignee"])
	}
	if ls := first["labels"].([]any); len(ls) != 0 {
		t.Fatalf("labels = %v, want []", ls)
	}
	proj := first["project"].(map[string]any)
	if proj["id"] == "" || proj["name"] == "" {
		t.Fatalf("project = %v, want {id, name}", proj)
	}
	// state is the human state name; state_group the slug.
	if first["state"] != "Backlog" && first["state"] != "Todo" {
		t.Fatalf("state = %v, want a state name", first["state"])
	}
	if first["state_group"] != "backlog" && first["state_group"] != "unstarted" {
		t.Fatalf("state_group = %v, want a state-group slug", first["state_group"])
	}
}

func TestC84_RowListSortAscDesc(t *testing.T) {
	fx := setupRowList(t)
	wid := createWidgetHTTP(t, fx.wd.fx.e, fx.wd.fx.adminCookie, fx.dashURL,
		`{"title":"t","widget_type":"work_items_table","config":{"sort_by":"priority","sort_dir":"asc","max_rows":100}}`)
	code, body := getWidgetData(t, fx.wd.fx, wid, fx.wd.fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("data: status = %d (body: %v)", code, body)
	}
	// b1(p0), a2(p1), b2(p2), then the two p3 issues in id order.
	titles := rowTitles(t, body)
	if len(titles) != 5 || titles[0] != "b1" || titles[1] != "a2" || titles[2] != "b2" {
		t.Fatalf("asc titles = %v, want [b1 a2 b2 ...]", titles)
	}
	lastTwo := map[string]bool{titles[3]: true, titles[4]: true}
	if !lastTwo["a1"] || !lastTwo["a3"] {
		t.Fatalf("asc titles = %v, want the two p3 issues last", titles)
	}
	wid = createWidgetHTTP(t, fx.wd.fx.e, fx.wd.fx.adminCookie, fx.dashURL,
		`{"title":"t2","widget_type":"work_items_table","config":{"sort_by":"priority","sort_dir":"desc","max_rows":100}}`)
	code, body = getWidgetData(t, fx.wd.fx, wid, fx.wd.fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("data: status = %d (body: %v)", code, body)
	}
	titles = rowTitles(t, body)
	firstTwo := map[string]bool{titles[0]: true, titles[1]: true}
	if !firstTwo["a1"] || !firstTwo["a3"] || titles[2] != "b2" || titles[3] != "a2" || titles[4] != "b1" {
		t.Fatalf("desc titles = %v, want the two p3 issues first then b2,a2,b1", titles)
	}
}

func TestC84_RowListPagination(t *testing.T) {
	fx := setupRowList(t)
	wid := createWidgetHTTP(t, fx.wd.fx.e, fx.wd.fx.adminCookie, fx.dashURL,
		`{"title":"t","widget_type":"work_items_table","config":{"sort_by":"title","sort_dir":"asc","max_rows":2}}`)
	code, body := getWidgetDataPage(t, fx.wd.fx, wid, fx.wd.fx.adminCookie, "2")
	if code != http.StatusOK {
		t.Fatalf("data page 2: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(5) || body["max_rows"] != float64(2) || body["page"] != float64(2) {
		t.Fatalf("envelope = %v, want total 5 max_rows 2 page 2", body)
	}
	// title asc: a1,a2 | a3,b1 | b2 — page 2 is a3,b1.
	if got, want := rowTitles(t, body), []string{"a3", "b1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("page 2 titles = %v, want %v", got, want)
	}
	code, body = getWidgetDataPage(t, fx.wd.fx, wid, fx.wd.fx.adminCookie, "3")
	if code != http.StatusOK {
		t.Fatalf("data page 3: status = %d (body: %v)", code, body)
	}
	if got, want := rowTitles(t, body), []string{"b2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("page 3 titles = %v, want %v", got, want)
	}
	// Bad page is a 400.
	if code, _ := getWidgetDataPage(t, fx.wd.fx, wid, fx.wd.fx.adminCookie, "0"); code != http.StatusBadRequest {
		t.Fatalf("page 0: status = %d, want 400", code)
	}
}

func TestC84_RowListFiltersAND(t *testing.T) {
	fx := setupRowList(t)
	wd := fx.wd
	// priorities=[3] AND states=[Todo of A]: a3 only.
	wid := createWidgetHTTP(t, wd.fx.e, wd.fx.adminCookie, fx.dashURL,
		fmt.Sprintf(`{"title":"t","widget_type":"work_items_table","config":{"sort_by":"title","sort_dir":"asc","filters":{"priorities":[3],"states":[%q]}}}`, wd.todoA))
	code, body := getWidgetData(t, wd.fx, wid, wd.fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("data: status = %d (body: %v)", code, body)
	}
	if got, want := rowTitles(t, body), []string{"a3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ANDed filter titles = %v, want %v", got, want)
	}
	// project_ids=[A] alone: a1,a2,a3.
	wid = createWidgetHTTP(t, wd.fx.e, wd.fx.adminCookie, fx.dashURL,
		fmt.Sprintf(`{"title":"t2","widget_type":"work_items_table","config":{"sort_by":"title","sort_dir":"asc","project_ids":[%q]}}`, wd.projAID))
	code, body = getWidgetData(t, wd.fx, wid, wd.fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("data: status = %d (body: %v)", code, body)
	}
	if got, want := rowTitles(t, body), []string{"a1", "a2", "a3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("project-scoped titles = %v, want %v", got, want)
	}
	// The full row shape survives a columns subset (columns only
	// decide rendering/CSV, not the payload).
	wid = createWidgetHTTP(t, wd.fx.e, wd.fx.adminCookie, fx.dashURL,
		`{"title":"t3","widget_type":"work_items_table","config":{"columns":["title","priority"],"sort_by":"title"}}`)
	code, body = getWidgetData(t, wd.fx, wid, wd.fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("data: status = %d (body: %v)", code, body)
	}
	first := body["rows"].([]any)[0].(map[string]any)
	for _, k := range []string{"id", "title", "state", "priority", "assignee", "labels", "project"} {
		if _, ok := first[k]; !ok {
			t.Fatalf("row missing key %q with columns subset (keys: %v)", k, first)
		}
	}
}

func TestC84_AssignedToMeOnlyCaller(t *testing.T) {
	fx := setupRowList(t)
	wd := fx.wd
	issues := listIssueIDs(t, wd.fx, wd.identA)
	if len(issues) == 0 {
		t.Fatal("no issues in project A")
	}
	// Assign one issue to the member, one to the admin.
	adminID := authedUserID(t, wd.fx.e, wd.fx.adminCookie)
	for _, asg := range []struct {
		issue, user string
		cookie      *http.Cookie
	}{
		{issues[0], wd.fx.memberID, wd.fx.adminCookie},
		{issues[1], adminID, wd.fx.adminCookie},
	} {
		rec := postAuthedJSON(t, wd.fx.e, http.MethodPost,
			fmt.Sprintf("%s/projects/%s/issues/%s/assignees/%s", wd.wsBase, wd.identA, asg.issue, asg.user),
			asg.cookie, ``)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("assign: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
		}
	}
	wid := createWidgetHTTP(t, wd.fx.e, wd.fx.adminCookie, fx.dashURL,
		`{"title":"mine","widget_type":"assigned_to_me","config":{"sort_by":"title","sort_dir":"asc"}}`)
	// The member sees only their own issue.
	code, body := getWidgetData(t, wd.fx, wid, wd.fx.memberCookie)
	if code != http.StatusOK {
		t.Fatalf("member data: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(1) {
		t.Fatalf("member total = %v, want 1", body["total"])
	}
	rows := body["rows"].([]any)
	got := rows[0].(map[string]any)
	if got["assignee"] == nil || got["assignee"].(map[string]any)["id"] != wd.fx.memberID {
		t.Fatalf("row assignee = %v, want the member", got["assignee"])
	}
	// The admin sees only THEIR issue — the member's is excluded.
	code, body = getWidgetData(t, wd.fx, wid, wd.fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("admin data: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(1) {
		t.Fatalf("admin total = %v, want 1", body["total"])
	}
	adminRows := body["rows"].([]any)
	adminRow := adminRows[0].(map[string]any)
	if adminRow["id"] == got["id"] {
		t.Fatal("admin sees the member's issue — assignee forcing is broken")
	}
	if adminRow["assignee"] == nil || adminRow["assignee"].(map[string]any)["id"] != adminID {
		t.Fatalf("admin row assignee = %v, want the admin", adminRow["assignee"])
	}
}

func TestC84_AssignedToMeOverridesConflictingFilter(t *testing.T) {
	fx := setupRowList(t)
	wd := fx.wd
	issues := listIssueIDs(t, wd.fx, wd.identA)
	adminID := authedUserID(t, wd.fx.e, wd.fx.adminCookie)
	rec := postAuthedJSON(t, wd.fx.e, http.MethodPost,
		fmt.Sprintf("%s/projects/%s/issues/%s/assignees/%s", wd.wsBase, wd.identA, issues[0], wd.fx.memberID),
		wd.fx.adminCookie, ``)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("assign: status = %d, want 204", rec.Code)
	}
	// A widget filter naming the ADMIN is overridden: the caller's
	// (member's) items still come back.
	wid := createWidgetHTTP(t, wd.fx.e, wd.fx.adminCookie, fx.dashURL,
		fmt.Sprintf(`{"title":"mine","widget_type":"assigned_to_me","config":{"filters":{"assignees":[%q]}}}`, adminID))
	code, body := getWidgetData(t, wd.fx, wid, wd.fx.memberCookie)
	if code != http.StatusOK {
		t.Fatalf("data: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(1) {
		t.Fatalf("total = %v, want 1 (the conflicting assignee filter must be overridden)", body["total"])
	}
	row := body["rows"].([]any)[0].(map[string]any)
	if row["assignee"].(map[string]any)["id"] != wd.fx.memberID {
		t.Fatalf("row assignee = %v, want the member", row["assignee"])
	}
}

func TestC84_DrilldownBarSegment(t *testing.T) {
	fx := setupRowList(t)
	wd := fx.wd
	wid := createWidgetHTTP(t, wd.fx.e, wd.fx.adminCookie, fx.dashURL,
		`{"title":"By state","widget_type":"bar","config":{"group_by":"state"}}`)
	code, body := getDrilldown(t, wd.fx, wid, wd.fx.adminCookie, "?group=Backlog")
	if code != http.StatusOK {
		t.Fatalf("drilldown: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(4) || body["group_label"] != "Backlog" || body["group_by"] != "state" {
		t.Fatalf("envelope = %v, want total 4 group_label Backlog group_by state", body)
	}
	if body["page"] != float64(1) || body["per_page"] != float64(10) {
		t.Fatalf("envelope = %v, want page 1 per_page 10", body)
	}
	// The segment's exact items: a1,a2 (A) + b1,b2 (B), same-named
	// states merge across projects.
	ids := drilldownIDs(t, body)
	if len(ids) != 4 {
		t.Fatalf("drilldown items = %d, want 4", len(ids))
	}
	items := body["items"].([]any)
	for _, it := range items {
		row := it.(map[string]any)
		if row["state"] != "Backlog" {
			t.Fatalf("drilldown row state = %v, want Backlog", row["state"])
		}
		for _, k := range []string{"id", "title", "priority", "assignee", "labels", "project"} {
			if _, ok := row[k]; !ok {
				t.Fatalf("drilldown row missing key %q", k)
			}
		}
	}
}

func TestC84_DrilldownDonutSlice(t *testing.T) {
	fx := setupRowList(t)
	wd := fx.wd
	wid := createWidgetHTTP(t, wd.fx.e, wd.fx.adminCookie, fx.dashURL,
		`{"title":"By priority","widget_type":"donut","config":{"group_by":"priority"}}`)
	code, body := getDrilldown(t, wd.fx, wid, wd.fx.adminCookie, "?group=High")
	if code != http.StatusOK {
		t.Fatalf("drilldown: status = %d (body: %v)", code, body)
	}
	// High = priority 3: a1, a3.
	if body["total"] != float64(2) {
		t.Fatalf("total = %v, want 2", body["total"])
	}
	titles := map[string]bool{}
	for _, it := range body["items"].([]any) {
		titles[it.(map[string]any)["title"].(string)] = true
	}
	if !titles["a1"] || !titles["a3"] || len(titles) != 2 {
		t.Fatalf("drilldown titles = %v, want a1+a3", titles)
	}
}

func TestC84_DrilldownSmartCounterMetricInvariant(t *testing.T) {
	fx := setupRowList(t)
	wd := fx.wd
	mk := func(metric string) string {
		return createWidgetHTTP(t, wd.fx.e, wd.fx.adminCookie, fx.dashURL,
			fmt.Sprintf(`{"title":"sc","widget_type":"smart_counter","config":{"group_by":"state","metric":%q}}`, metric))
	}
	_, countBody := getDrilldown(t, wd.fx, mk("count"), wd.fx.adminCookie, "?group=Backlog")
	_, pointsBody := getDrilldown(t, wd.fx, mk("estimate_points"), wd.fx.adminCookie, "?group=Backlog")
	// The metric changes the displayed group value, NOT the item set.
	if !reflect.DeepEqual(drilldownIDs(t, countBody), drilldownIDs(t, pointsBody)) {
		t.Fatal("smart_counter drill-down item sets differ between count and estimate_points")
	}
	if countBody["total"] != float64(4) {
		t.Fatalf("total = %v, want 4", countBody["total"])
	}
}

func TestC84_DrilldownLineGroup(t *testing.T) {
	fx := setupRowList(t)
	wd := fx.wd
	// line/area have no time buckets in this codebase — they group by
	// the configured dim exactly like bar.
	wid := createWidgetHTTP(t, wd.fx.e, wd.fx.adminCookie, fx.dashURL,
		`{"title":"Trend","widget_type":"line","config":{"group_by":"priority"}}`)
	code, body := getDrilldown(t, wd.fx, wid, wd.fx.adminCookie, "?group=High")
	if code != http.StatusOK {
		t.Fatalf("drilldown: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(2) || body["group_by"] != "priority" {
		t.Fatalf("envelope = %v, want total 2 group_by priority", body)
	}
}

func TestC84_DrilldownStateGroupLabel(t *testing.T) {
	fx := setupRowList(t)
	wd := fx.wd
	wid := createWidgetHTTP(t, wd.fx.e, wd.fx.adminCookie, fx.dashURL,
		`{"title":"By state group","widget_type":"pie","config":{"group_by":"state_group"}}`)
	// The human LABEL ("Backlog") resolves through the group's Key
	// (the "backlog" slug) to the filter — label and key both work.
	for _, g := range []string{"Backlog", "backlog"} {
		code, body := getDrilldown(t, wd.fx, wid, wd.fx.adminCookie, "?group="+g)
		if code != http.StatusOK {
			t.Fatalf("drilldown group=%s: status = %d (body: %v)", g, code, body)
		}
		if body["total"] != float64(4) || body["group_label"] != "Backlog" {
			t.Fatalf("group=%s envelope = %v, want total 4 group_label Backlog", g, body)
		}
	}
}

func TestC84_DrilldownUnassignedBucket(t *testing.T) {
	fx := setupRowList(t)
	wd := fx.wd
	issues := listIssueIDs(t, wd.fx, wd.identA)
	rec := postAuthedJSON(t, wd.fx.e, http.MethodPost,
		fmt.Sprintf("%s/projects/%s/issues/%s/assignees/%s", wd.wsBase, wd.identA, issues[0], wd.fx.memberID),
		wd.fx.adminCookie, ``)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("assign: status = %d, want 204", rec.Code)
	}
	wid := createWidgetHTTP(t, wd.fx.e, wd.fx.adminCookie, fx.dashURL,
		`{"title":"By assignee","widget_type":"bar","config":{"group_by":"assignee"}}`)
	// Both the synthetic key ("unassigned") and the label
	// ("Unassigned") drill to the same 4 unassigned issues.
	for _, g := range []string{"Unassigned", "unassigned"} {
		code, body := getDrilldown(t, wd.fx, wid, wd.fx.adminCookie, "?group="+g)
		if code != http.StatusOK {
			t.Fatalf("drilldown group=%s: status = %d (body: %v)", g, code, body)
		}
		if body["total"] != float64(4) {
			t.Fatalf("group=%s total = %v, want 4", g, body["total"])
		}
		for _, it := range body["items"].([]any) {
			if it.(map[string]any)["assignee"] != nil {
				t.Fatalf("group=%s: unassigned bucket returned an assigned row", g)
			}
		}
	}
	// The member's display name (email fallback) drills to their issue.
	code, body := getDrilldown(t, wd.fx, wid, wd.fx.adminCookie, "?group="+wd.fx.memberEmail)
	if code != http.StatusOK {
		t.Fatalf("drilldown member: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(1) {
		t.Fatalf("member total = %v, want 1", body["total"])
	}
}

func TestC84_DrilldownPagination(t *testing.T) {
	fx := setupRowList(t)
	wd := fx.wd
	wid := createWidgetHTTP(t, wd.fx.e, wd.fx.adminCookie, fx.dashURL,
		`{"title":"sc","widget_type":"smart_counter","config":{"group_by":"state","max_rows":2}}`)
	code, body := getDrilldown(t, wd.fx, wid, wd.fx.adminCookie, "?group=Backlog&page=2")
	if code != http.StatusOK {
		t.Fatalf("drilldown: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(4) || body["per_page"] != float64(2) || body["page"] != float64(2) {
		t.Fatalf("envelope = %v, want total 4 per_page 2 page 2", body)
	}
	if len(body["items"].([]any)) != 2 {
		t.Fatalf("page 2 items = %d, want 2", len(body["items"].([]any)))
	}
}

func TestC84_DrilldownHonest400s(t *testing.T) {
	fx := setupRowList(t)
	wd := fx.wd
	// number: no groups → 400.
	num := createWidgetHTTP(t, wd.fx.e, wd.fx.adminCookie, fx.dashURL,
		`{"title":"n","widget_type":"number","config":{}}`)
	if code, _ := getDrilldown(t, wd.fx, num, wd.fx.adminCookie, "?group=Backlog"); code != http.StatusBadRequest {
		t.Fatalf("number drilldown: status = %d, want 400", code)
	}
	// intake_breakdown: no groups → 400.
	ib := createWidgetHTTP(t, wd.fx.e, wd.fx.adminCookie, fx.dashURL,
		`{"title":"ib","widget_type":"intake_breakdown","config":{}}`)
	if code, _ := getDrilldown(t, wd.fx, ib, wd.fx.adminCookie, "?group=x"); code != http.StatusBadRequest {
		t.Fatalf("intake drilldown: status = %d, want 400", code)
	}
	// work_items_table: no groups → 400.
	tbl := createWidgetHTTP(t, wd.fx.e, wd.fx.adminCookie, fx.dashURL,
		`{"title":"t","widget_type":"work_items_table","config":{}}`)
	if code, _ := getDrilldown(t, wd.fx, tbl, wd.fx.adminCookie, "?group=Backlog"); code != http.StatusBadRequest {
		t.Fatalf("work_items_table drilldown: status = %d, want 400", code)
	}
	bar := createWidgetHTTP(t, wd.fx.e, wd.fx.adminCookie, fx.dashURL,
		`{"title":"b","widget_type":"bar","config":{"group_by":"state"}}`)
	// Missing group → 400.
	if code, _ := getDrilldown(t, wd.fx, bar, wd.fx.adminCookie, ""); code != http.StatusBadRequest {
		t.Fatalf("missing group: status = %d, want 400", code)
	}
	// Unresolvable group key → 400, never a guess.
	if code, body := getDrilldown(t, wd.fx, bar, wd.fx.adminCookie, "?group=Nope"); code != http.StatusBadRequest {
		t.Fatalf("bad group: status = %d, want 400 (body: %v)", code, body)
	}
	// Bad page → 400.
	if code, _ := getDrilldown(t, wd.fx, bar, wd.fx.adminCookie, "?group=Backlog&page=0"); code != http.StatusBadRequest {
		t.Fatalf("bad page: status = %d, want 400", code)
	}
	// Malformed widget id → 400.
	rec := getAuthed(t, wd.fx.e, http.MethodGet, "/api/v1/widgets/not-a-uuid/drilldown?group=Backlog", wd.fx.adminCookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed widget id: status = %d, want 400", rec.Code)
	}
}

func TestC84_DrilldownAuthZ(t *testing.T) {
	fx := setupRowList(t)
	wd := fx.wd
	privID := createDashboardHTTP(t, wd.fx.e, wd.fx.adminCookie, wd.fx.wsBase,
		`{"name":"hidden","visibility":"private"}`)
	wid := createWidgetHTTP(t, wd.fx.e, wd.fx.adminCookie, wd.fx.wsBase+"/dashboards/"+privID,
		`{"title":"b","widget_type":"bar","config":{"group_by":"state"}}`)
	dashURL := wd.fx.wsBase + "/dashboards/" + privID
	// Private + unshared: the member gets 404 (no existence leak).
	if code, _ := getDrilldown(t, wd.fx, wid, wd.fx.memberCookie, "?group=Backlog"); code != http.StatusNotFound {
		t.Fatalf("unshared drilldown: status = %d, want 404", code)
	}
	// View-shared: the member CAN drill (read affordance).
	code, _ := putShares(t, wd.fx.e, wd.fx.adminCookie, dashURL,
		fmt.Sprintf(`{"shares":[{"member_id":%q,"access":"view"}]}`, wd.fx.memberID))
	if code != http.StatusOK {
		t.Fatalf("PUT shares: status = %d", code)
	}
	if code, body := getDrilldown(t, wd.fx, wid, wd.fx.memberCookie, "?group=Backlog"); code != http.StatusOK {
		t.Fatalf("shared drilldown: status = %d, want 200 (body: %v)", code, body)
	}
	// Guest: 403 like every other dashboard read.
	if code, _ := getDrilldown(t, wd.fx, wid, wd.fx.guestCookie, "?group=Backlog"); code != http.StatusForbidden {
		t.Fatalf("guest drilldown: status = %d, want 403", code)
	}
}

func TestC84_CSVRowList(t *testing.T) {
	fx := setupRowList(t)
	wd := fx.wd
	wid := createWidgetHTTP(t, wd.fx.e, wd.fx.adminCookie, fx.dashURL,
		`{"title":"Tasks","widget_type":"work_items_table","config":{"columns":["title","priority","project"],"sort_by":"title","sort_dir":"asc"}}`)
	code, header, body := getWidgetCSV(t, wd.fx, wid, wd.fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("csv: status = %d (body: %s)", code, body)
	}
	if ct := header.Get("Content-Type"); ct != "text/csv" {
		t.Fatalf("Content-Type = %q, want text/csv", ct)
	}
	if cd := header.Get("Content-Disposition"); !strings.Contains(cd, "attachment") || !strings.Contains(cd, ".csv") {
		t.Fatalf("Content-Disposition = %q, want attachment csv", cd)
	}
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) != 6 {
		t.Fatalf("csv lines = %d, want 6 (header + 5 rows — the full dataset)", len(lines))
	}
	if lines[0] != "title,priority,project" {
		t.Fatalf("csv header = %q, want the configured columns in order", lines[0])
	}
	if !strings.HasPrefix(lines[1], "a1,3,") {
		t.Fatalf("csv first row = %q, want a1 first (title asc)", lines[1])
	}
}

func TestC84_CSVAssignedToMe(t *testing.T) {
	fx := setupRowList(t)
	wd := fx.wd
	issues := listIssueIDs(t, wd.fx, wd.identA)
	rec := postAuthedJSON(t, wd.fx.e, http.MethodPost,
		fmt.Sprintf("%s/projects/%s/issues/%s/assignees/%s", wd.wsBase, wd.identA, issues[0], wd.fx.memberID),
		wd.fx.adminCookie, ``)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("assign: status = %d, want 204", rec.Code)
	}
	wid := createWidgetHTTP(t, wd.fx.e, wd.fx.adminCookie, fx.dashURL,
		`{"title":"Mine","widget_type":"assigned_to_me","config":{}}`)
	// Default columns = all seven, in the documented order.
	code, _, body := getWidgetCSV(t, wd.fx, wid, wd.fx.memberCookie)
	if code != http.StatusOK {
		t.Fatalf("csv: status = %d (body: %s)", code, body)
	}
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) != 2 {
		t.Fatalf("csv lines = %d, want 2 (header + the member's single row)", len(lines))
	}
	if lines[0] != "id,title,state,priority,assignee,labels,project" {
		t.Fatalf("csv header = %q, want the default seven columns", lines[0])
	}
}

func TestC84_CSVRowListRequiresRead(t *testing.T) {
	fx := setupRowList(t)
	wd := fx.wd
	privID := createDashboardHTTP(t, wd.fx.e, wd.fx.adminCookie, wd.fx.wsBase,
		`{"name":"hidden","visibility":"private"}`)
	wid := createWidgetHTTP(t, wd.fx.e, wd.fx.adminCookie, wd.fx.wsBase+"/dashboards/"+privID,
		`{"title":"t","widget_type":"work_items_table","config":{}}`)
	if code, _, _ := getWidgetCSV(t, wd.fx, wid, wd.fx.memberCookie); code != http.StatusNotFound {
		t.Fatalf("unshared csv: status = %d, want 404", code)
	}
}

// TestOpenAPIDocShapesDashboardPhase3 pins the C84T0 dashboards
// phase-3 API contract in docs/openapi.yaml: the drill-down route, the
// row-list config keys, the WidgetRowItem / WidgetDrilldown schemas,
// and the widened widget_type enums. If the implementation drifts from
// the pinned contract, this test fails first.
func TestOpenAPIDocShapesDashboardPhase3(t *testing.T) {
	var spec struct {
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(docs.OpenAPI, &spec); err != nil {
		t.Fatalf("embedded openapi spec is not valid JSON: %v", err)
	}
	decode := func(name string, v any) {
		t.Helper()
		raw, ok := spec.Components.Schemas[name]
		if !ok {
			t.Fatalf("spec has no %s schema", name)
		}
		if err := json.Unmarshal(raw, v); err != nil {
			t.Fatalf("decode %s schema: %v", name, err)
		}
	}

	// The drill-down route is documented with its required group param.
	ops, ok := spec.Paths["/api/v1/widgets/{widget_id}/drilldown"]
	if !ok {
		t.Fatal("spec missing drilldown path")
	}
	var drillOp struct {
		Summary    string `json:"summary"`
		Parameters []struct {
			Name     string `json:"name"`
			In       string `json:"in"`
			Required bool   `json:"required"`
		} `json:"parameters"`
		Responses map[string]json.RawMessage `json:"responses"`
	}
	if err := json.Unmarshal(ops["get"], &drillOp); err != nil {
		t.Fatalf("decode drilldown op: %v", err)
	}
	if drillOp.Summary == "" {
		t.Fatal("drilldown op missing summary")
	}
	var groupParam, pageParam *struct {
		Name     string `json:"name"`
		In       string `json:"in"`
		Required bool   `json:"required"`
	}
	for i := range drillOp.Parameters {
		p := &drillOp.Parameters[i]
		switch p.Name {
		case "group":
			groupParam = p
		case "page":
			pageParam = p
		}
	}
	if groupParam == nil || !groupParam.Required || groupParam.In != "query" {
		t.Fatalf("drilldown group param = %+v, want required query param", groupParam)
	}
	if pageParam == nil || pageParam.Required || pageParam.In != "query" {
		t.Fatalf("drilldown page param = %+v, want optional query param", pageParam)
	}
	for _, code := range []string{"200", "400", "403", "404"} {
		if _, ok := drillOp.Responses[code]; !ok {
			t.Fatalf("drilldown op missing %s response", code)
		}
	}
	var drillResp struct {
		Content map[string]struct {
			Schema struct {
				Ref string `json:"$ref"`
			} `json:"schema"`
		} `json:"content"`
	}
	if err := json.Unmarshal(drillOp.Responses["200"], &drillResp); err != nil {
		t.Fatalf("decode drilldown 200: %v", err)
	}
	if drillResp.Content["application/json"].Schema.Ref != "#/components/schemas/WidgetDrilldown" {
		t.Fatalf("drilldown 200 schema ref = %q, want WidgetDrilldown",
			drillResp.Content["application/json"].Schema.Ref)
	}

	// WidgetRowItem pins the canonical row shape.
	var row struct {
		Props map[string]json.RawMessage `json:"properties"`
	}
	decode("WidgetRowItem", &row)
	for _, prop := range []string{"id", "title", "state", "state_group", "priority", "assignee", "labels", "project"} {
		if _, ok := row.Props[prop]; !ok {
			t.Fatalf("WidgetRowItem missing %s property", prop)
		}
	}

	// WidgetDrilldown pins the drill-down envelope.
	var dd struct {
		Props map[string]json.RawMessage `json:"properties"`
	}
	decode("WidgetDrilldown", &dd)
	for _, prop := range []string{"items", "page", "per_page", "total", "group_label", "group_by"} {
		if _, ok := dd.Props[prop]; !ok {
			t.Fatalf("WidgetDrilldown missing %s property", prop)
		}
	}

	// WidgetData carries the row-list fields.
	var data struct {
		Props map[string]json.RawMessage `json:"properties"`
	}
	decode("WidgetData", &data)
	for _, prop := range []string{"rows", "total"} {
		if _, ok := data.Props[prop]; !ok {
			t.Fatalf("WidgetData missing %s property", prop)
		}
	}

	// WidgetConfig carries the row-list keys; columns/sort enums pin
	// the write-time validation contract (columns is an array — its
	// enum lives under items).
	var config struct {
		Props map[string]struct {
			Enum  []string `json:"enum"`
			Items struct {
				Enum []string `json:"enum"`
			} `json:"items"`
		} `json:"properties"`
	}
	decode("WidgetConfig", &config)
	wantCols := []string{"id", "title", "state", "priority", "assignee", "labels", "project"}
	if got := config.Props["columns"].Items.Enum; !reflect.DeepEqual(got, wantCols) {
		t.Fatalf("WidgetConfig.columns items enum = %v, want %v", got, wantCols)
	}
	if got := config.Props["sort_by"].Enum; !reflect.DeepEqual(got, wantCols) {
		t.Fatalf("WidgetConfig.sort_by enum = %v, want %v", got, wantCols)
	}
	if got := config.Props["sort_dir"].Enum; !reflect.DeepEqual(got, []string{"asc", "desc"}) {
		t.Fatalf("WidgetConfig.sort_dir enum = %v, want [asc desc]", got)
	}

	// Every widget_type enum in the spec carries the thirteen types
	// (C100T0 adds pql_table).
	var widget struct {
		Props map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	decode("DashboardWidget", &widget)
	wantTypes := []string{"bar", "line", "area", "donut", "pie", "number",
		"smart_counter", "smart_gauge", "table_2d", "intake_breakdown",
		"work_items_table", "assigned_to_me", "pql_table"}
	if got := widget.Props["widget_type"].Enum; !reflect.DeepEqual(got, wantTypes) {
		t.Fatalf("DashboardWidget.widget_type enum = %v, want %v", got, wantTypes)
	}
	// The local-ref test covers dangling refs; assert the new refs
	// exist here too.
	for _, name := range []string{"WidgetRowItem", "WidgetDrilldown"} {
		if _, ok := spec.Components.Schemas[name]; !ok {
			t.Fatalf("spec has no %s schema", name)
		}
	}

	// The export.csv op still answers text/csv and now documents the
	// row-list columns.
	var exportOp struct {
		Description string `json:"description"`
		Responses   map[string]struct {
			Content map[string]json.RawMessage `json:"content"`
		} `json:"responses"`
	}
	if err := json.Unmarshal(spec.Paths["/api/v1/widgets/{widget_id}/export.csv"]["get"], &exportOp); err != nil {
		t.Fatalf("decode export op: %v", err)
	}
	if _, ok := exportOp.Responses["200"].Content["text/csv"]; !ok {
		t.Fatal("export.csv 200 response missing text/csv content")
	}
	if !strings.Contains(exportOp.Description, "work_items_table") {
		t.Fatal("export.csv description does not document the row-list columns")
	}
}
