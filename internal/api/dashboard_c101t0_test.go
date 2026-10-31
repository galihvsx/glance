package api

// C101T0: dashboards backend over HTTP — the multi-project
// pql_table widget. Rows merge from two projects, ONE global
// ORDER BY wins over per-project order (proved with the projects
// listed B-first, so per-branch concatenation would order
// differently), LIMIT caps the union total across pages, pagination
// walks the merged list, a renamed-name error in one project
// surfaces per-widget naming that project while other widgets still
// load, the CSV export is capped across projects, and a
// single-project widget keeps its C100 behavior (including its
// unannotated error message). Real test database, no skips.
//
// Fixture world (setupRowList / setupWidgetData): project A ("Data
// A") holds a1 (priority 3, Backlog), a2 (priority 1, Backlog), a3
// (priority 3, Todo); project B ("Data B") holds b1 (priority 0)
// and b2 (priority 2). A two-project widget therefore sees 5 rows.

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// pqlMultiWidgetConfig renders a pql_table config for the given
// projects, in the given order, with the given query.
func pqlMultiWidgetConfig(t *testing.T, fx *rowListFixture, query string, projects []string, extra string) string {
	t.Helper()
	quoted := make([]string, 0, len(projects))
	for _, p := range projects {
		quoted = append(quoted, fmt.Sprintf("%q", p))
	}
	if extra != "" {
		extra = "," + extra
	}
	return fmt.Sprintf(`{"query":%q,"project_ids":[%s]%s}`, query, strings.Join(quoted, ","), extra)
}

// rowProjectNames extracts each row's project display name by title.
func rowProjectNames(t *testing.T, body map[string]any) map[string]string {
	t.Helper()
	rows, ok := body["rows"].([]any)
	if !ok {
		t.Fatalf("no rows in body: %v", body)
	}
	out := map[string]string{}
	for _, r := range rows {
		rm := r.(map[string]any)
		proj := rm["project"].(map[string]any)
		out[rm["title"].(string)] = proj["name"].(string)
	}
	return out
}

// widgetErrorMessage extracts the error message from a widget-data
// error body ({error:{message, details}}).
func widgetErrorMessage(t *testing.T, body map[string]any) string {
	t.Helper()
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("no error in body: %v", body)
	}
	msg, ok := errObj["message"].(string)
	if !ok {
		t.Fatalf("no error message in body: %v", body)
	}
	return msg
}

func TestC101_PQLMergesRowsFromTwoProjects(t *testing.T) {
	fx := setupRowList(t)
	wid := pqlWidget(t, fx, pqlMultiWidgetConfig(t, fx, "priority >= 0", []string{fx.wd.projAID, fx.wd.projBID}, ""))
	code, body := getWidgetData(t, fx.wd.fx, wid, fx.wd.fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("data: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(5) {
		t.Fatalf("total = %v, want 5 (A 3 + B 2)", body["total"])
	}
	if len(rowTitles(t, body)) != 5 {
		t.Fatalf("titles = %v, want all 5 rows", rowTitles(t, body))
	}
	projects := rowProjectNames(t, body)
	if projects["a1"] != "Data A" || projects["b1"] != "Data B" || projects["b2"] != "Data B" {
		t.Fatalf("row projects = %v, want A rows in Data A and B rows in Data B", projects)
	}
}

func TestC101_PQLGlobalOrderByWinsOverPerProjectOrder(t *testing.T) {
	fx := setupRowList(t)
	// Projects listed B-FIRST: per-branch concatenation (B's rows,
	// then A's) under priority DESC would start b2 (p2), b1 (p0).
	// The global order must instead start with A's p3 rows, then
	// b2 (p2), a2 (p1), b1 (p0); created_at ASC breaks the p3 tie
	// (a1 created before a3).
	wid := pqlWidget(t, fx, pqlMultiWidgetConfig(t, fx,
		"priority >= 0 ORDER BY priority DESC, created_at ASC",
		[]string{fx.wd.projBID, fx.wd.projAID}, ""))
	code, body := getWidgetData(t, fx.wd.fx, wid, fx.wd.fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("data: status = %d (body: %v)", code, body)
	}
	if got := rowTitles(t, body); len(got) != 5 || got[0] != "a1" || got[1] != "a3" || got[2] != "b2" || got[3] != "a2" || got[4] != "b1" {
		t.Fatalf("titles = %v, want [a1 a3 b2 a2 b1] (global order, not B-first concatenation)", got)
	}
}

func TestC101_PQLFallbackSortIsGlobal(t *testing.T) {
	fx := setupRowList(t)
	// No query ORDER BY: the config sort (title asc) applies
	// globally over the union — B-first listing must not put b1/b2
	// ahead of a1/a2/a3.
	wid := pqlWidget(t, fx, pqlMultiWidgetConfig(t, fx, "priority >= 0",
		[]string{fx.wd.projBID, fx.wd.projAID}, `"sort_by":"title","sort_dir":"asc"`))
	code, body := getWidgetData(t, fx.wd.fx, wid, fx.wd.fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("data: status = %d (body: %v)", code, body)
	}
	if got := rowTitles(t, body); len(got) != 5 || got[0] != "a1" || got[1] != "a2" || got[2] != "a3" || got[3] != "b1" || got[4] != "b2" {
		t.Fatalf("titles = %v, want [a1 a2 a3 b1 b2] (global title sort)", got)
	}
}

func TestC101_PQLPaginatesMergedList(t *testing.T) {
	fx := setupRowList(t)
	wid := pqlWidget(t, fx, pqlMultiWidgetConfig(t, fx,
		"priority >= 0 ORDER BY priority DESC, created_at ASC",
		[]string{fx.wd.projAID, fx.wd.projBID}, `"max_rows":2`))
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

func TestC101_PQLLimitCapsUnionTotalAcrossPages(t *testing.T) {
	fx := setupRowList(t)
	// LIMIT 3 caps the UNION total (5 rows exist across A+B):
	// pages 1-3 hold one row each (max_rows 1), page 4 is empty,
	// and total reports the capped count (3) on every page.
	wid := pqlWidget(t, fx, pqlMultiWidgetConfig(t, fx, "priority >= 0 LIMIT 3",
		[]string{fx.wd.projAID, fx.wd.projBID}, `"max_rows":1`))
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
		var got int
		if rows, ok := body["rows"].([]any); ok {
			got = len(rows)
		}
		if got != tc.rows {
			t.Fatalf("page %s: rows = %d, want %d", tc.page, got, tc.rows)
		}
	}
}

func TestC101_PQLRenamedNameErrorNamesProject(t *testing.T) {
	fx := setupRowList(t)
	multiID := pqlWidget(t, fx, pqlMultiWidgetConfig(t, fx, "state = Backlog",
		[]string{fx.wd.projAID, fx.wd.projBID}, ""))
	singleID := pqlWidget(t, fx, fmt.Sprintf(`{"query":"state = Backlog","project_ids":[%q]}`, fx.wd.projAID))
	plainID := createWidgetHTTP(t, fx.wd.fx.e, fx.wd.fx.adminCookie, fx.dashURL,
		`{"title":"plain","widget_type":"work_items_table","config":{}}`)
	// All three widgets load before the rename.
	if code, _ := getWidgetData(t, fx.wd.fx, multiID, fx.wd.fx.adminCookie); code != http.StatusOK {
		t.Fatalf("multi data before rename: status = %d, want 200", code)
	}
	// Rename the state the stored query names, in project A only:
	// name resolution now fails for the A branch of the multi widget.
	if _, err := fx.wd.fx.pool.Exec(context.Background(),
		`UPDATE states SET name = 'BacklogRenamed' WHERE id = $1::uuid`, fx.wd.backlogA); err != nil {
		t.Fatalf("rename state: %v", err)
	}
	code, body := getWidgetData(t, fx.wd.fx, multiID, fx.wd.fx.adminCookie)
	if code != http.StatusBadRequest {
		t.Fatalf("multi data after rename: status = %d, want per-widget 400 (body: %v)", code, body)
	}
	msg := widgetErrorMessage(t, body)
	if !strings.Contains(msg, "Data A") {
		t.Fatalf("multi error message = %q, want it to name project Data A", msg)
	}
	if !strings.Contains(msg, "Backlog") {
		t.Fatalf("multi error message = %q, want it to keep the original PQL message", msg)
	}
	// The position detail survives the annotation (the handler maps
	// the typed PQL error, not a plain string).
	errObj := body["error"].(map[string]any)
	if details, ok := errObj["details"].(map[string]any); !ok || details["position"] == nil {
		t.Fatalf("multi error details = %v, want a position", errObj["details"])
	}
	// The single-project widget keeps its C100 error verbatim
	// (byte-identical: no project annotation on a one-project widget).
	code, body = getWidgetData(t, fx.wd.fx, singleID, fx.wd.fx.adminCookie)
	if code != http.StatusBadRequest {
		t.Fatalf("single data after rename: status = %d, want per-widget 400 (body: %v)", code, body)
	}
	if msg := widgetErrorMessage(t, body); strings.Contains(msg, "in project") {
		t.Fatalf("single error message = %q, want the unannotated C100 message", msg)
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

func TestC101_PQLCSVExportCappedAcrossProjects(t *testing.T) {
	fx := setupRowList(t)
	wid := pqlWidget(t, fx, pqlMultiWidgetConfig(t, fx, "priority >= 0 LIMIT 2",
		[]string{fx.wd.projAID, fx.wd.projBID}, ""))
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
	// Without LIMIT the same widget shape exports all 5 merged rows.
	wid2 := pqlWidget(t, fx, pqlMultiWidgetConfig(t, fx, "priority >= 0",
		[]string{fx.wd.projAID, fx.wd.projBID}, ""))
	code, _, body = getWidgetCSV(t, fx.wd.fx, wid2, fx.wd.fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("csv uncapped: status = %d (body: %s)", code, body)
	}
	if len(strings.Split(strings.TrimSpace(body), "\n")) != 6 {
		t.Fatalf("csv uncapped lines = %d, want header + 5 merged rows", len(strings.Split(strings.TrimSpace(body), "\n")))
	}
}

func TestC101_PQLSingleProjectUnchanged(t *testing.T) {
	fx := setupRowList(t)
	// The C100 single-project shape, re-pinned here as the
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
