package api

// C102T0: dashboards backend over HTTP — the empty-means-all
// pql_table scope. An empty project_ids (absent or []) is valid at
// write time and resolves at data time to every live, accessible
// workspace project (the other row-list types' rule, via
// widgetProjectScope): rows merge from all live projects, archived
// projects are excluded, unreachable (private, unshared) projects
// contribute zero rows rather than an error, a renamed-name error
// in one resolved project surfaces per-widget naming that project
// while other widgets still load, global ORDER BY and LIMIT-as-total
// cap hold over the all-projects union, pagination walks the merged
// list, the CSV export is capped across projects, a pinned
// single-project widget keeps its C101 behavior, an empty-scope
// widget over a one-project workspace matches its pinned twin, and
// a workspace with zero live projects yields an empty widget
// (total 0), not an error. Real test database, no skips.
//
// Fixture world (setupRowList / setupWidgetData): project A ("Data
// A") holds a1 (priority 3, Backlog), a2 (priority 1, Backlog), a3
// (priority 3, Todo); project B ("Data B") holds b1 (priority 0)
// and b2 (priority 2). An empty-scope widget therefore sees 5 rows.

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// archiveProject marks a project archived, so it drops out of the
// resolved widget scope (widgetProjectScope: archived_at IS NULL).
func archiveProject(t *testing.T, fx *rowListFixture, projectID string) {
	t.Helper()
	if _, err := fx.wd.fx.pool.Exec(context.Background(),
		`UPDATE projects SET archived_at = now() WHERE id = $1::uuid`, projectID); err != nil {
		t.Fatalf("archive project: %v", err)
	}
}

// rowCount returns the row count of a widget-data body, tolerating
// the absent-rows shape of an empty page.
func rowCount(body map[string]any) int {
	rows, ok := body["rows"].([]any)
	if !ok {
		return 0
	}
	return len(rows)
}

func TestC102_PQLEmptyScopeWriteAccepted(t *testing.T) {
	fx := setupRowList(t)
	// Empty project_ids at create time: 201, not the C101 400.
	wid := pqlWidget(t, fx, `{"query":"priority >= 0","project_ids":[]}`)
	code, body := getWidgetData(t, fx.wd.fx, wid, fx.wd.fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("empty-scope data: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(5) {
		t.Fatalf("empty-scope total = %v, want 5 (A 3 + B 2)", body["total"])
	}
	// Missing project_ids at create time: 201 as well.
	wid2 := pqlWidget(t, fx, `{"query":"priority >= 0"}`)
	code, body = getWidgetData(t, fx.wd.fx, wid2, fx.wd.fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("missing-scope data: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(5) {
		t.Fatalf("missing-scope total = %v, want 5", body["total"])
	}
	// Updating a pinned widget to the empty scope: 200, and the
	// widget then reads all live projects.
	rec := postAuthedJSON(t, fx.wd.fx.e, http.MethodPatch, "/api/v1/widgets/"+wid2, fx.wd.fx.adminCookie,
		`{"config":{"query":"priority >= 3","project_ids":[]}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("update to empty scope: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	code, body = getWidgetData(t, fx.wd.fx, wid2, fx.wd.fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("data after empty-scope update: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(2) {
		t.Fatalf("total after empty-scope update = %v, want 2 (a1, a3 at priority >= 3)", body["total"])
	}
}

func TestC102_PQLEmptyScopeMergesAllLiveProjects(t *testing.T) {
	fx := setupRowList(t)
	wid := pqlWidget(t, fx, `{"query":"priority >= 0","project_ids":[],"sort_by":"title","sort_dir":"asc"}`)
	code, body := getWidgetData(t, fx.wd.fx, wid, fx.wd.fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("data: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(5) {
		t.Fatalf("total = %v, want 5 (A 3 + B 2)", body["total"])
	}
	if got := rowTitles(t, body); len(got) != 5 || got[0] != "a1" || got[1] != "a2" || got[2] != "a3" || got[3] != "b1" || got[4] != "b2" {
		t.Fatalf("titles = %v, want [a1 a2 a3 b1 b2]", got)
	}
	projects := rowProjectNames(t, body)
	if projects["a1"] != "Data A" || projects["b1"] != "Data B" || projects["b2"] != "Data B" {
		t.Fatalf("row projects = %v, want A rows in Data A and B rows in Data B", projects)
	}
}

func TestC102_PQLEmptyScopeExcludesArchived(t *testing.T) {
	fx := setupRowList(t)
	archiveProject(t, fx, fx.wd.projBID)
	wid := pqlWidget(t, fx, `{"query":"priority >= 0","project_ids":[],"sort_by":"title","sort_dir":"asc"}`)
	code, body := getWidgetData(t, fx.wd.fx, wid, fx.wd.fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("data: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(3) {
		t.Fatalf("total = %v, want 3 (project A only — B archived)", body["total"])
	}
	if got := rowTitles(t, body); len(got) != 3 || got[0] != "a1" || got[1] != "a2" || got[2] != "a3" {
		t.Fatalf("titles = %v, want [a1 a2 a3]", got)
	}
}

func TestC102_PQLEmptyScopeUnreachableContributesZero(t *testing.T) {
	fx := setupRowList(t)
	wsSlug := strings.TrimPrefix(fx.wd.fx.wsBase, "/api/v1/workspaces/")
	identC := uniqueProjectIdentifier("WDC")
	createProjectHTTP(t, fx.wd.fx.e, fx.wd.fx.adminCookie, wsSlug, "Hidden C", identC)
	projCBase := fx.wd.fx.wsBase + "/projects/" + identC
	rec := postAuthedJSON(t, fx.wd.fx.e, http.MethodPost, projCBase+"/issues", fx.wd.fx.adminCookie,
		`{"name":"c1","priority":4}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create issue c1: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	wid := pqlWidget(t, fx, `{"query":"priority >= 0","project_ids":[],"sort_by":"title","sort_dir":"asc"}`)
	// While C is public, its issue merges in: 6 rows.
	code, body := getWidgetData(t, fx.wd.fx, wid, fx.wd.fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("data with public C: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(6) {
		t.Fatalf("total with public C = %v, want 6 (A 3 + B 2 + C 1)", body["total"])
	}
	// Flip C to private: the workspace member (role 15, not a C
	// project member) can no longer reach it — its rows drop out
	// silently, not as an error.
	rec = postAuthedJSON(t, fx.wd.fx.e, http.MethodPatch, projCBase, fx.wd.fx.adminCookie,
		`{"visibility":"private"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("privatize C: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	code, body = getWidgetData(t, fx.wd.fx, wid, fx.wd.fx.memberCookie)
	if code != http.StatusOK {
		t.Fatalf("member data with private C: status = %d, want 200 (body: %v)", code, body)
	}
	if body["total"] != float64(5) {
		t.Fatalf("member total with private C = %v, want 5 (C contributes zero rows)", body["total"])
	}
	if got := rowTitles(t, body); len(got) != 5 || got[4] != "b2" {
		t.Fatalf("member titles = %v, want [a1 a2 a3 b1 b2] without c1", got)
	}
	for _, title := range rowTitles(t, body) {
		if title == "c1" {
			t.Fatalf("member titles include c1 from unreachable project C")
		}
	}
}

func TestC102_PQLEmptyScopeRenamedNameErrorNamesProject(t *testing.T) {
	fx := setupRowList(t)
	wid := pqlWidget(t, fx, `{"query":"state = Backlog","project_ids":[]}`)
	plainID := createWidgetHTTP(t, fx.wd.fx.e, fx.wd.fx.adminCookie, fx.dashURL,
		`{"title":"plain","widget_type":"work_items_table","config":{}}`)
	if code, _ := getWidgetData(t, fx.wd.fx, wid, fx.wd.fx.adminCookie); code != http.StatusOK {
		t.Fatalf("empty-scope data before rename: status = %d, want 200", code)
	}
	// Rename the state the stored query names, in project A only:
	// name resolution now fails for the A branch of the union.
	if _, err := fx.wd.fx.pool.Exec(context.Background(),
		`UPDATE states SET name = 'BacklogRenamed' WHERE id = $1::uuid`, fx.wd.backlogA); err != nil {
		t.Fatalf("rename state: %v", err)
	}
	code, body := getWidgetData(t, fx.wd.fx, wid, fx.wd.fx.adminCookie)
	if code != http.StatusBadRequest {
		t.Fatalf("empty-scope data after rename: status = %d, want per-widget 400 (body: %v)", code, body)
	}
	msg := widgetErrorMessage(t, body)
	if !strings.Contains(msg, "Data A") {
		t.Fatalf("empty-scope error message = %q, want it to name project Data A", msg)
	}
	if !strings.Contains(msg, "Backlog") {
		t.Fatalf("empty-scope error message = %q, want it to keep the original PQL message", msg)
	}
	errObj := body["error"].(map[string]any)
	if details, ok := errObj["details"].(map[string]any); !ok || details["position"] == nil {
		t.Fatalf("empty-scope error details = %v, want a position", errObj["details"])
	}
	// The sibling widget and the dashboard itself still load.
	if code, body := getWidgetData(t, fx.wd.fx, plainID, fx.wd.fx.adminCookie); code != http.StatusOK {
		t.Fatalf("plain widget after rename: status = %d, want 200 (body: %v)", code, body)
	}
	rec := getAuthed(t, fx.wd.fx.e, http.MethodGet, fx.dashURL, fx.wd.fx.adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("dashboard after rename: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestC102_PQLEmptyScopeGlobalOrderByWins(t *testing.T) {
	fx := setupRowList(t)
	// No project list at all: the global order must still win —
	// per-branch concatenation would order differently.
	wid := pqlWidget(t, fx, `{"query":"priority >= 0 ORDER BY priority DESC, created_at ASC","project_ids":[]}`)
	code, body := getWidgetData(t, fx.wd.fx, wid, fx.wd.fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("data: status = %d (body: %v)", code, body)
	}
	if got := rowTitles(t, body); len(got) != 5 || got[0] != "a1" || got[1] != "a3" || got[2] != "b2" || got[3] != "a2" || got[4] != "b1" {
		t.Fatalf("titles = %v, want [a1 a3 b2 a2 b1] (global order over the all-projects union)", got)
	}
}

func TestC102_PQLEmptyScopeLimitCapsUnionTotal(t *testing.T) {
	fx := setupRowList(t)
	wid := pqlWidget(t, fx, `{"query":"priority >= 0 LIMIT 3","project_ids":[],"max_rows":1}`)
	for _, tc := range []struct {
		page string
		rows int
	}{
		{"1", 1}, {"2", 1}, {"3", 1}, {"4", 0},
	} {
		code, body := getWidgetDataPage(t, fx.wd.fx, wid, fx.wd.fx.adminCookie, tc.page)
		if code != http.StatusOK {
			t.Fatalf("page %s: status = %d (body: %v)", tc.page, code, body)
		}
		if body["total"] != float64(3) {
			t.Fatalf("page %s: total = %v, want capped 3", tc.page, body["total"])
		}
		if got := rowCount(body); got != tc.rows {
			t.Fatalf("page %s: rows = %d, want %d", tc.page, got, tc.rows)
		}
	}
}

func TestC102_PQLEmptyScopePaginatesMergedList(t *testing.T) {
	fx := setupRowList(t)
	wid := pqlWidget(t, fx, `{"query":"priority >= 0 ORDER BY priority DESC, created_at ASC","project_ids":[],"max_rows":2}`)
	want := [][]string{{"a1", "a3"}, {"b2", "a2"}, {"b1"}}
	for i, w := range want {
		code, body := getWidgetDataPage(t, fx.wd.fx, wid, fx.wd.fx.adminCookie, fmt.Sprint(i+1))
		if code != http.StatusOK {
			t.Fatalf("page %d: status = %d (body: %v)", i+1, code, body)
		}
		if body["total"] != float64(5) {
			t.Fatalf("page %d: total = %v, want 5", i+1, body["total"])
		}
		if got := rowTitles(t, body); len(got) != len(w) || (len(w) > 0 && got[0] != w[0]) || (len(w) > 1 && got[1] != w[1]) {
			t.Fatalf("page %d: titles = %v, want %v", i+1, got, w)
		}
	}
}

func TestC102_PQLEmptyScopeCSVExportCapped(t *testing.T) {
	fx := setupRowList(t)
	wid := pqlWidget(t, fx, `{"query":"priority >= 0 LIMIT 2","project_ids":[]}`)
	code, _, body := getWidgetCSV(t, fx.wd.fx, wid, fx.wd.fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("csv: status = %d (body: %s)", code, body)
	}
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) != 3 {
		t.Fatalf("csv lines = %d, want header + 2 capped rows across projects (body: %s)", len(lines), body)
	}
	if lines[0] != "id,title,state,priority,assignee,labels,project" {
		t.Fatalf("csv header = %q, want the default seven columns in order", lines[0])
	}
	wid2 := pqlWidget(t, fx, `{"query":"priority >= 0","project_ids":[]}`)
	code, _, body = getWidgetCSV(t, fx.wd.fx, wid2, fx.wd.fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("csv uncapped: status = %d (body: %s)", code, body)
	}
	if len(strings.Split(strings.TrimSpace(body), "\n")) != 6 {
		t.Fatalf("csv uncapped lines = %d, want header + 5 merged rows", len(strings.Split(strings.TrimSpace(body), "\n")))
	}
}

func TestC102_PQLPinnedSingleUnchanged(t *testing.T) {
	fx := setupRowList(t)
	// The C101 single-project shape, re-pinned here as the
	// byte-identical regression: 3 rows from A only, config sort
	// honored, B's rows never appear.
	wid := pqlWidget(t, fx, fmt.Sprintf(`{"query":"priority >= 0","project_ids":[%q],"sort_by":"title","sort_dir":"asc"}`, fx.wd.projAID))
	code, body := getWidgetData(t, fx.wd.fx, wid, fx.wd.fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("data: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(3) {
		t.Fatalf("total = %v, want 3 (project A only, not 5)", body["total"])
	}
	if got := rowTitles(t, body); len(got) != 3 || got[0] != "a1" || got[1] != "a2" || got[2] != "a3" {
		t.Fatalf("titles = %v, want [a1 a2 a3]", got)
	}
}

func TestC102_PQLEmptyScopeOverOneProjectMatchesPinned(t *testing.T) {
	fx := setupRowList(t)
	// One live project left: the empty-scope widget must behave
	// identically to the pinned twin (same resolved scope of 1 →
	// the same single-project path).
	archiveProject(t, fx, fx.wd.projBID)
	emptyID := pqlWidget(t, fx, `{"query":"priority >= 0","project_ids":[],"sort_by":"title","sort_dir":"asc"}`)
	pinnedID := pqlWidget(t, fx, fmt.Sprintf(`{"query":"priority >= 0","project_ids":[%q],"sort_by":"title","sort_dir":"asc"}`, fx.wd.projAID))
	code, emptyBody := getWidgetData(t, fx.wd.fx, emptyID, fx.wd.fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("empty-scope data: status = %d (body: %v)", code, emptyBody)
	}
	code, pinnedBody := getWidgetData(t, fx.wd.fx, pinnedID, fx.wd.fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("pinned data: status = %d (body: %v)", code, pinnedBody)
	}
	if emptyBody["total"] != pinnedBody["total"] {
		t.Fatalf("empty total = %v, pinned total = %v, want identical", emptyBody["total"], pinnedBody["total"])
	}
	emptyTitles, pinnedTitles := rowTitles(t, emptyBody), rowTitles(t, pinnedBody)
	if len(emptyTitles) != len(pinnedTitles) {
		t.Fatalf("empty titles = %v, pinned titles = %v, want identical", emptyTitles, pinnedTitles)
	}
	for i := range emptyTitles {
		if emptyTitles[i] != pinnedTitles[i] {
			t.Fatalf("empty titles = %v, pinned titles = %v, want identical", emptyTitles, pinnedTitles)
		}
	}
}

func TestC102_PQLEmptyScopeOverZeroLiveProjects(t *testing.T) {
	fx := setupRowList(t)
	archiveProject(t, fx, fx.wd.projAID)
	archiveProject(t, fx, fx.wd.projBID)
	wid := pqlWidget(t, fx, `{"query":"priority >= 0","project_ids":[]}`)
	code, body := getWidgetData(t, fx.wd.fx, wid, fx.wd.fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("data: status = %d, want 200 (body: %v)", code, body)
	}
	if body["total"] != float64(0) {
		t.Fatalf("total = %v, want 0 (empty widget, not an error)", body["total"])
	}
	if got := rowCount(body); got != 0 {
		t.Fatalf("rows = %d, want 0", got)
	}
}
