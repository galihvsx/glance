package api

// C116T0: parse-level + HTTP tests for the new ?cycles= /
// ?modules= visual list filters. Parse: repeatable AND
// comma-separated via parseRepeatIDList, "none" sentinel,
// malformed → 400 naming the param. HTTP: the project list
// endpoint round-trips the filters, and the export endpoint
// honors them (it shares parseIssueListInput).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestParseIssueListInputCyclesModules(t *testing.T) {
	uuid1 := "11111111-1111-1111-1111-111111111111"
	uuid2 := "22222222-2222-2222-2222-222222222222"

	// Repeatable + comma-separated values merge, whitespace trimmed.
	in, err := parseIssueListInput(url.Values{
		"cycles":  {uuid1 + "," + uuid2, " none "},
		"modules": {uuid1},
	})
	if err != nil {
		t.Fatalf("parse cycles/modules: %v", err)
	}
	if !reflect.DeepEqual(in.FilterCycles, []string{uuid1, uuid2, "none"}) {
		t.Fatalf("FilterCycles = %v, want [%s %s none]", in.FilterCycles, uuid1, uuid2)
	}
	if !reflect.DeepEqual(in.FilterModules, []string{uuid1}) {
		t.Fatalf("FilterModules = %v, want [%s]", in.FilterModules, uuid1)
	}

	// "none" alone parses (the no-cycle / no-module sentinel).
	in, err = parseIssueListInput(url.Values{"cycles": {"none"}, "modules": {"none"}})
	if err != nil {
		t.Fatalf("parse none sentinels: %v", err)
	}
	if !reflect.DeepEqual(in.FilterCycles, []string{"none"}) || !reflect.DeepEqual(in.FilterModules, []string{"none"}) {
		t.Fatalf("sentinels = %v / %v, want [none]", in.FilterCycles, in.FilterModules)
	}

	// Empty segments are dropped; an all-empty param is no filter.
	in, err = parseIssueListInput(url.Values{"cycles": {" ,,"}})
	if err != nil {
		t.Fatalf("parse empty cycles: %v", err)
	}
	if len(in.FilterCycles) != 0 {
		t.Fatalf("FilterCycles = %v, want empty (no filter)", in.FilterCycles)
	}

	// The singular scope params are untouched by the plural
	// filter parse: ?cycle=/?module= still land on Cycle/Module.
	in, err = parseIssueListInput(url.Values{"cycle": {uuid1}, "module": {uuid2}})
	if err != nil {
		t.Fatalf("parse scope params: %v", err)
	}
	if in.Cycle != uuid1 || in.Module != uuid2 {
		t.Fatalf("scope = %q / %q, want %s / %s", in.Cycle, in.Module, uuid1, uuid2)
	}
	if len(in.FilterCycles) != 0 || len(in.FilterModules) != 0 {
		t.Fatalf("scope params leaked into filters: %v / %v", in.FilterCycles, in.FilterModules)
	}

	// Malformed UUIDs are 400-class errors naming the param and
	// the bad value — never silently ignored, never a 500.
	for _, tc := range []struct {
		label string
		param string
		qp    url.Values
	}{
		{"cycles", "cycles", url.Values{"cycles": {"not-a-uuid"}}},
		{"modules", "modules", url.Values{"modules": {"definitely-not-a-uuid"}}},
		{"cycles-mixed", "cycles", url.Values{"cycles": {uuid1 + ",bogus"}}},
	} {
		_, err := parseIssueListInput(tc.qp)
		if err == nil {
			t.Fatalf("parse %s with malformed UUID: no error", tc.label)
		}
		if !strings.Contains(err.Error(), "invalid "+tc.param) {
			t.Fatalf("parse %s malformed UUID: error %q does not name the param", tc.label, err.Error())
		}
	}
}

func TestIssueListHTTPCyclesModules(t *testing.T) {
	pool, e, cookie, base := setupBulkProject(t, "cycmodhttp")
	ctx := context.Background()

	parts := strings.Split(base, "/")
	if len(parts) != 8 {
		t.Fatalf("base = %q, want /api/v1/workspaces/<slug>/projects/<ident>/issues", base)
	}
	slug, ident := parts[4], parts[6]
	var projectID string
	if err := pool.QueryRow(ctx,
		`SELECT p.id::text FROM projects p JOIN workspaces w ON w.id = p.workspace_id
		  WHERE w.slug = $1 AND p.identifier = $2`, slug, ident).Scan(&projectID); err != nil {
		t.Fatalf("project id: %v", err)
	}

	insertCycle := func(name string) string {
		var id string
		if err := pool.QueryRow(ctx,
			`INSERT INTO cycles (project_id, name, start_date, end_date) VALUES ($1::uuid, $2, '2026-01-01', '2026-01-31') RETURNING id::text`,
			projectID, name).Scan(&id); err != nil {
			t.Fatalf("insert cycle %s: %v", name, err)
		}
		return id
	}
	insertModule := func(name string) string {
		var id string
		if err := pool.QueryRow(ctx,
			`INSERT INTO modules (project_id, name) VALUES ($1::uuid, $2) RETURNING id::text`,
			projectID, name).Scan(&id); err != nil {
			t.Fatalf("insert module %s: %v", name, err)
		}
		return id
	}
	c1 := insertCycle("CycleOne")
	c2 := insertCycle("CycleTwo")
	m1 := insertModule("ModuleOne")
	m2 := insertModule("ModuleTwo")

	// a in c1+m1, b in c2+m2, c in c1 only, d in neither.
	aID := createIssueHTTP(t, e, cookie, base, "inC1M1")
	bID := createIssueHTTP(t, e, cookie, base, "inC2M2")
	cID := createIssueHTTP(t, e, cookie, base, "inC1Only")
	createIssueHTTP(t, e, cookie, base, "inNeither")
	for _, q := range []string{
		fmt.Sprintf(`INSERT INTO cycle_issues (cycle_id, issue_id) VALUES ('%s', '%s')`, c1, aID),
		fmt.Sprintf(`INSERT INTO cycle_issues (cycle_id, issue_id) VALUES ('%s', '%s')`, c1, cID),
		fmt.Sprintf(`INSERT INTO cycle_issues (cycle_id, issue_id) VALUES ('%s', '%s')`, c2, bID),
		fmt.Sprintf(`INSERT INTO module_issues (module_id, issue_id) VALUES ('%s', '%s')`, m1, aID),
		fmt.Sprintf(`INSERT INTO module_issues (module_id, issue_id) VALUES ('%s', '%s')`, m2, bID),
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("fixture SQL %q: %v", q, err)
		}
	}

	suffix := "&order_by=-sequence_id&per_page=100"
	want := func(got []string, names ...string) {
		t.Helper()
		sort.Strings(names)
		sorted := append([]string(nil), got...)
		sort.Strings(sorted)
		if strings.Join(sorted, "|") != strings.Join(names, "|") {
			t.Fatalf("got %v, want %v", sorted, names)
		}
	}

	// Per dimension: single UUID, multi-UUID OR, none only,
	// mixed UUIDs + none, comma-separated + repeatable.
	want(pqlListNamesHTTP(t, e, cookie, base, "cycles="+c1+suffix), "inC1M1", "inC1Only")
	want(pqlListNamesHTTP(t, e, cookie, base, "cycles="+c1+","+c2+suffix), "inC1M1", "inC1Only", "inC2M2")
	want(pqlListNamesHTTP(t, e, cookie, base, "cycles="+c1+"&cycles="+c2+suffix), "inC1M1", "inC1Only", "inC2M2")
	want(pqlListNamesHTTP(t, e, cookie, base, "cycles=none"+suffix), "inNeither")
	want(pqlListNamesHTTP(t, e, cookie, base, "cycles="+c1+",none"+suffix), "inC1M1", "inC1Only", "inNeither")
	want(pqlListNamesHTTP(t, e, cookie, base, "modules="+m1+suffix), "inC1M1")
	want(pqlListNamesHTTP(t, e, cookie, base, "modules="+m1+","+m2+suffix), "inC1M1", "inC2M2")
	want(pqlListNamesHTTP(t, e, cookie, base, "modules=none"+suffix), "inC1Only", "inNeither")
	want(pqlListNamesHTTP(t, e, cookie, base, "modules="+m2+",none"+suffix), "inC1Only", "inC2M2", "inNeither")
	// Composition: cycles AND modules.
	want(pqlListNamesHTTP(t, e, cookie, base, "cycles="+c1+"&modules="+m1+suffix), "inC1M1")
	// Scope AND: singular ?cycle= scope + plural filter.
	want(pqlListNamesHTTP(t, e, cookie, base, "cycle="+c1+"&cycles="+c1+suffix), "inC1M1", "inC1Only")
	want(pqlListNamesHTTP(t, e, cookie, base, "cycle="+c1+"&cycles="+c2+suffix))
	want(pqlListNamesHTTP(t, e, cookie, base, "module="+m1+"&modules="+m2+suffix))
	// Empty = no filter.
	want(pqlListNamesHTTP(t, e, cookie, base, suffix[1:]), "inC1M1", "inC1Only", "inC2M2", "inNeither")
	// Malformed values 400 on the list endpoint.
	for _, bad := range []string{"cycles=not-a-uuid", "modules=not-a-uuid"} {
		rec := getAuthed(t, e, http.MethodGet, base+"?"+bad+suffix, cookie)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("malformed %s: status = %d, want 400 (body: %s)", bad, rec.Code, rec.Body.String())
		}
	}
}

func TestIssueExportHTTPCyclesModules(t *testing.T) {
	pool, e, cookie, base, slug, ident := setupExportProject(t, "exp-cycmod")
	_ = slug
	_ = ident
	ctx := context.Background()

	parts := strings.Split(base, "/")
	slug2, ident2 := parts[4], parts[6]
	var projectID string
	if err := pool.QueryRow(ctx,
		`SELECT p.id::text FROM projects p JOIN workspaces w ON w.id = p.workspace_id
		  WHERE w.slug = $1 AND p.identifier = $2`, slug2, ident2).Scan(&projectID); err != nil {
		t.Fatalf("project id: %v", err)
	}
	var c1 string
	if err := pool.QueryRow(ctx,
		`INSERT INTO cycles (project_id, name, start_date, end_date) VALUES ($1::uuid, 'ExportCycle', '2026-01-01', '2026-01-31') RETURNING id::text`,
		projectID).Scan(&c1); err != nil {
		t.Fatalf("insert cycle: %v", err)
	}
	var m1 string
	if err := pool.QueryRow(ctx,
		`INSERT INTO modules (project_id, name) VALUES ($1::uuid, 'ExportModule') RETURNING id::text`,
		projectID).Scan(&m1); err != nil {
		t.Fatalf("insert module: %v", err)
	}

	inID := createIssueHTTP(t, e, cookie, base, "ExportInCycle")
	outID := createIssueHTTP(t, e, cookie, base, "ExportOutCycle")
	_ = outID
	if _, err := pool.Exec(ctx,
		`INSERT INTO cycle_issues (cycle_id, issue_id) VALUES ($1::uuid, $2::uuid)`, c1, inID); err != nil {
		t.Fatalf("attach cycle: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO module_issues (module_id, issue_id) VALUES ($1::uuid, $2::uuid)`, m1, inID); err != nil {
		t.Fatalf("attach module: %v", err)
	}

	exportNames := func(query string) []string {
		t.Helper()
		rec := getExport(t, e, base+"/export?format=csv&"+query, cookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("export %s: status = %d (body: %s)", query, rec.Code, rec.Body.String())
		}
		recs := parseCSVBody(t, rec.Body.String())
		if len(recs) < 1 || recs[0][1] != "name" {
			t.Fatalf("export header = %v, want name in column 1", recs)
		}
		names := make([]string, 0, len(recs)-1)
		for _, r := range recs[1:] {
			names = append(names, r[1])
		}
		sort.Strings(names)
		return names
	}
	// The export endpoint shares parseIssueListInput, so the new
	// filters apply there for free — proved, not assumed.
	if got := exportNames("cycles=" + c1); strings.Join(got, "|") != "ExportInCycle" {
		t.Fatalf("export cycles filter: got %v, want [ExportInCycle]", got)
	}
	if got := exportNames("modules=" + m1); strings.Join(got, "|") != "ExportInCycle" {
		t.Fatalf("export modules filter: got %v, want [ExportInCycle]", got)
	}
	if got := exportNames("cycles=none"); strings.Join(got, "|") != "ExportOutCycle" {
		t.Fatalf("export cycles=none: got %v, want [ExportOutCycle]", got)
	}
	if got := exportNames(""); strings.Join(got, "|") != "ExportInCycle|ExportOutCycle" {
		t.Fatalf("export unfiltered: got %v, want both issues", got)
	}
	// Malformed filter values 400 on the export endpoint too.
	rec := getExport(t, e, base+"/export?format=csv&cycles=not-a-uuid", cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("export malformed cycles: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	// JSON format honors the filter as well (spot check).
	rec = getExport(t, e, base+"/export?format=json&modules="+m1, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("export json modules: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var rows []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode export json: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "ExportInCycle" {
		t.Fatalf("export json modules: got %v, want [ExportInCycle]", rows)
	}
}
