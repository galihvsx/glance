package api

// C100T0: dashboards backend over HTTP — the pql_table widget type.
// A PQL-driven row list: config validation (blank query, 0/2+ pinned
// projects, filters-key rejection, meaningless-key rejection, bad
// syntax at write time), query ORDER BY (multi-key) winning over the
// config sort, LIMIT as a total cap across pages and in the CSV
// export, pagination, the single-project scope, a renamed-name error
// surfacing per-widget at data time without breaking other widgets,
// and drill-down unavailability. Real test database, no skips.
//
// Fixture world (setupRowList / setupWidgetData): project A holds a1
// (priority 3, Backlog), a2 (priority 1, Backlog), a3 (priority 3,
// Todo); project B holds b1 (priority 0) and b2 (priority 2). A PQL
// widget pinned to project A therefore sees exactly 3 rows.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"glance/docs"
)

// pqlWidget creates a pql_table widget pinned to project A and returns
// its id (expects 201).
func pqlWidget(t *testing.T, fx *rowListFixture, config string) string {
	t.Helper()
	return createWidgetHTTP(t, fx.wd.fx.e, fx.wd.fx.adminCookie, fx.dashURL,
		`{"title":"pql","widget_type":"pql_table","config":`+config+`}`)
}

// pqlWidgetConfig renders a pql_table config for project A.
func pqlWidgetConfig(t *testing.T, fx *rowListFixture, inner string) string {
	t.Helper()
	if inner != "" {
		inner = "," + inner
	}
	return fmt.Sprintf(`{"query":"priority >= 0","project_ids":[%q]%s}`, fx.wd.projAID, inner)
}

func TestC100_PQLWriteValidation(t *testing.T) {
	fx := setupRowList(t)
	dashURL := fx.dashURL
	admin := fx.wd.fx.adminCookie
	cases := []struct {
		name string
		body string
	}{
		{"missing query", `{"title":"t","widget_type":"pql_table","config":{"project_ids":["` + fx.wd.projAID + `"]}}`},
		{"blank query", `{"title":"t","widget_type":"pql_table","config":{"query":"  ","project_ids":["` + fx.wd.projAID + `"]}}`},
		{"no projects", `{"title":"t","widget_type":"pql_table","config":{"query":"priority >= 0","project_ids":[]}}`},
		{"missing projects", `{"title":"t","widget_type":"pql_table","config":{"query":"priority >= 0"}}`},
		{"two projects", `{"title":"t","widget_type":"pql_table","config":{"query":"priority >= 0","project_ids":["` + fx.wd.projAID + `","` + fx.wd.projBID + `"]}}`},
		{"filters key", `{"title":"t","widget_type":"pql_table","config":{"query":"priority >= 0","project_ids":["` + fx.wd.projAID + `"],"filters":{"states":["` + fx.wd.backlogA + `"]}}}`},
		{"visualization key", `{"title":"t","widget_type":"pql_table","config":{"query":"priority >= 0","project_ids":["` + fx.wd.projAID + `"],"visualization":"table"}}`},
		{"bad syntax", `{"title":"t","widget_type":"pql_table","config":{"query":"priority >=","project_ids":["` + fx.wd.projAID + `"]}}`},
		{"unknown field", `{"title":"t","widget_type":"pql_table","config":{"query":"sprint = 3","project_ids":["` + fx.wd.projAID + `"]}}`},
		{"query on bar", `{"title":"t","widget_type":"bar","config":{"group_by":"state","query":"priority >= 0"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := postWidgetExpect(t, fx.wd.fx.e, admin, dashURL, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s: status = %d, want 400 (body: %s)", tc.name, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestC100_PQLWriteErrorCarriesPosition(t *testing.T) {
	fx := setupRowList(t)
	rec := postWidgetExpect(t, fx.wd.fx.e, fx.wd.fx.adminCookie, fx.dashURL,
		`{"title":"t","widget_type":"pql_table","config":{"query":"priority >=","project_ids":["`+fx.wd.projAID+`"]}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad syntax: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Error struct {
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v (body: %s)", err, rec.Body.String())
	}
	pos, ok := body.Error.Details["position"].(float64)
	if !ok || pos <= 0 {
		t.Fatalf("error details = %v, want a position > 0", body.Error.Details)
	}
}

func TestC100_PQLUpdateValidation(t *testing.T) {
	fx := setupRowList(t)
	wid := pqlWidget(t, fx, pqlWidgetConfig(t, fx, ""))
	// A type flip to pql_table with two pinned projects is 400.
	rec := postAuthedJSON(t, fx.wd.fx.e, http.MethodPatch, "/api/v1/widgets/"+wid, fx.wd.fx.adminCookie,
		`{"config":{"query":"priority >= 0","project_ids":["`+fx.wd.projAID+`","`+fx.wd.projBID+`"]}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("update to 2 projects: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	// A valid query swap succeeds and takes effect.
	rec = postAuthedJSON(t, fx.wd.fx.e, http.MethodPatch, "/api/v1/widgets/"+wid, fx.wd.fx.adminCookie,
		`{"config":{"query":"priority >= 3","project_ids":["`+fx.wd.projAID+`"]}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid update: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	code, body := getWidgetData(t, fx.wd.fx, wid, fx.wd.fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("data after update: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(2) {
		t.Fatalf("total = %v, want 2 (a1, a3 at priority >= 3)", body["total"])
	}
}

func TestC100_PQLOrderByMultiKeyWinsOverConfigSort(t *testing.T) {
	fx := setupRowList(t)
	// Query ORDER BY (multi-key) beats the config sort (title asc):
	// priority DESC first (a1/a3 at p3 before a2 at p1), then
	// created_at ASC breaks the p3 tie (a1 created before a3).
	wid := pqlWidget(t, fx, fmt.Sprintf(`{"query":"priority >= 0 ORDER BY priority DESC, created_at ASC","project_ids":[%q],"sort_by":"title","sort_dir":"asc"}`, fx.wd.projAID))
	code, body := getWidgetData(t, fx.wd.fx, wid, fx.wd.fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("data: status = %d (body: %v)", code, body)
	}
	if got := rowTitles(t, body); len(got) != 3 || got[0] != "a1" || got[1] != "a3" || got[2] != "a2" {
		t.Fatalf("titles = %v, want [a1 a3 a2]", got)
	}
	if body["total"] != float64(3) {
		t.Fatalf("total = %v, want 3 (project A only)", body["total"])
	}
}

func TestC100_PQLFallbackSort(t *testing.T) {
	fx := setupRowList(t)
	// No query ORDER BY: the config sort applies (title asc).
	wid := pqlWidget(t, fx, fmt.Sprintf(`{"query":"priority >= 0","project_ids":[%q],"sort_by":"title","sort_dir":"asc"}`, fx.wd.projAID))
	code, body := getWidgetData(t, fx.wd.fx, wid, fx.wd.fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("data: status = %d (body: %v)", code, body)
	}
	if got := rowTitles(t, body); len(got) != 3 || got[0] != "a1" || got[1] != "a2" || got[2] != "a3" {
		t.Fatalf("titles = %v, want [a1 a2 a3]", got)
	}
}

func TestC100_PQLSingleProjectScope(t *testing.T) {
	fx := setupRowList(t)
	// Unfiltered query, pinned to A: project B's rows never appear.
	wid := pqlWidget(t, fx, pqlWidgetConfig(t, fx, ""))
	code, body := getWidgetData(t, fx.wd.fx, wid, fx.wd.fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("data: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(3) {
		t.Fatalf("total = %v, want 3 (project A only, not 5)", body["total"])
	}
	for _, title := range rowTitles(t, body) {
		if title == "b1" || title == "b2" {
			t.Fatalf("titles = %v, project B leaked into a project-A widget", rowTitles(t, body))
		}
	}
}

func TestC100_PQLPaginates(t *testing.T) {
	fx := setupRowList(t)
	wid := pqlWidget(t, fx, pqlWidgetConfig(t, fx, `"max_rows":2`))
	code, body := getWidgetDataPage(t, fx.wd.fx, wid, fx.wd.fx.adminCookie, "1")
	if code != http.StatusOK {
		t.Fatalf("page 1: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(3) || body["page"] != float64(1) || body["max_rows"] != float64(2) {
		t.Fatalf("page 1 envelope = %v, want total 3 page 1 max_rows 2", body)
	}
	if len(rowTitles(t, body)) != 2 {
		t.Fatalf("page 1 rows = %v, want 2", rowTitles(t, body))
	}
	code, body = getWidgetDataPage(t, fx.wd.fx, wid, fx.wd.fx.adminCookie, "2")
	if code != http.StatusOK {
		t.Fatalf("page 2: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(3) || len(rowTitles(t, body)) != 1 {
		t.Fatalf("page 2 = %v, want total 3 with 1 row", body)
	}
}

func TestC100_PQLLimitTotalCapAcrossPages(t *testing.T) {
	fx := setupRowList(t)
	// LIMIT 2 caps the TOTAL: page 1 and 2 hold one row each (max_rows
	// 1), page 3 is empty, and total reports the capped count (2) on
	// every page — never the uncapped 3.
	wid := pqlWidget(t, fx, fmt.Sprintf(`{"query":"priority >= 0 LIMIT 2","project_ids":[%q],"max_rows":1}`, fx.wd.projAID))
	for _, tc := range []struct {
		page string
		rows int
	}{
		{"1", 1}, {"2", 1}, {"3", 0},
	} {
		code, body := getWidgetDataPage(t, fx.wd.fx, wid, fx.wd.fx.adminCookie, tc.page)
		if code != http.StatusOK {
			t.Fatalf("page %s: status = %d (body: %v)", tc.page, code, body)
		}
		if body["total"] != float64(2) {
			t.Fatalf("page %s: total = %v, want capped 2", tc.page, body["total"])
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

func TestC100_PQLStateFilter(t *testing.T) {
	fx := setupRowList(t)
	// A name-resolved predicate narrows to the Backlog rows of A.
	wid := pqlWidget(t, fx, fmt.Sprintf(`{"query":"state = Backlog","project_ids":[%q]}`, fx.wd.projAID))
	code, body := getWidgetData(t, fx.wd.fx, wid, fx.wd.fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("data: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(2) {
		t.Fatalf("total = %v, want 2 (a1, a2)", body["total"])
	}
	for _, title := range rowTitles(t, body) {
		if title == "a3" {
			t.Fatalf("titles = %v, Todo row a3 must not match state = Backlog", rowTitles(t, body))
		}
	}
}

func TestC100_PQLRenamedNamePerWidgetError(t *testing.T) {
	fx := setupRowList(t)
	pqlID := pqlWidget(t, fx, fmt.Sprintf(`{"query":"state = Backlog","project_ids":[%q]}`, fx.wd.projAID))
	plainID := createWidgetHTTP(t, fx.wd.fx.e, fx.wd.fx.adminCookie, fx.dashURL,
		`{"title":"plain","widget_type":"work_items_table","config":{}}`)
	// Both widgets load before the rename.
	if code, _ := getWidgetData(t, fx.wd.fx, pqlID, fx.wd.fx.adminCookie); code != http.StatusOK {
		t.Fatalf("pql data before rename: status = %d, want 200", code)
	}
	// Rename the state the stored query names: name resolution now
	// fails at compute time.
	if _, err := fx.wd.fx.pool.Exec(context.Background(),
		`UPDATE states SET name = 'BacklogRenamed' WHERE id = $1::uuid`, fx.wd.backlogA); err != nil {
		t.Fatalf("rename state: %v", err)
	}
	code, body := getWidgetData(t, fx.wd.fx, pqlID, fx.wd.fx.adminCookie)
	if code != http.StatusBadRequest {
		t.Fatalf("pql data after rename: status = %d, want per-widget 400 (body: %v)", code, body)
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

func TestC100_PQLCSVExportCapped(t *testing.T) {
	fx := setupRowList(t)
	wid := pqlWidget(t, fx, fmt.Sprintf(`{"query":"priority >= 0 LIMIT 2","project_ids":[%q]}`, fx.wd.projAID))
	code, _, body := getWidgetCSV(t, fx.wd.fx, wid, fx.wd.fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("csv: status = %d (body: %s)", code, body)
	}
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) != 3 {
		t.Fatalf("csv lines = %d, want header + 2 capped rows (body: %s)", len(lines), body)
	}
	if lines[0] != "id,title,state,priority,assignee,labels,project" {
		t.Fatalf("csv header = %q, want the default seven columns in order", lines[0])
	}
	// Without LIMIT the same widget shape exports all 3 rows.
	wid2 := pqlWidget(t, fx, pqlWidgetConfig(t, fx, ""))
	code, _, body = getWidgetCSV(t, fx.wd.fx, wid2, fx.wd.fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("csv uncapped: status = %d (body: %s)", code, body)
	}
	if len(strings.Split(strings.TrimSpace(body), "\n")) != 4 {
		t.Fatalf("csv uncapped lines = %d, want header + 3 rows", len(strings.Split(strings.TrimSpace(body), "\n")))
	}
}

func TestC100_PQLDrilldownUnavailable(t *testing.T) {
	fx := setupRowList(t)
	wid := pqlWidget(t, fx, pqlWidgetConfig(t, fx, ""))
	code, _ := getDrilldown(t, fx.wd.fx, wid, fx.wd.fx.adminCookie, "?group=Backlog")
	if code != http.StatusBadRequest {
		t.Fatalf("drilldown: status = %d, want 400 (rows already ARE the list)", code)
	}
}

func TestOpenAPIDocShapesPQLWidget(t *testing.T) {
	var spec struct {
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(docs.OpenAPI, &spec); err != nil {
		t.Fatalf("embedded openapi spec is not valid JSON: %v", err)
	}
	raw, ok := spec.Components.Schemas["WidgetConfig"]
	if !ok {
		t.Fatal("spec has no WidgetConfig schema")
	}
	var cfg struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("decode WidgetConfig: %v", err)
	}
	if _, ok := cfg.Properties["query"]; !ok {
		t.Fatal("WidgetConfig has no query property for pql_table")
	}
	// The widget_type enums carry the new type.
	if !strings.Contains(string(docs.OpenAPI), `"pql_table"`) {
		t.Fatal("spec never mentions pql_table")
	}
}
