package api

// C118T0: parse-level + HTTP tests for the new ?created_by=
// visual list filter and the `created_by` PQL field. Parse:
// repeatable AND comma-separated via parseRepeatIDList, NO
// plural alias, malformed → 400 naming the param, "none"
// passes the parser through and is rejected by the service
// (validateListFilterValues) — the label-filter layering.
// HTTP: the project list endpoint round-trips the filter,
// the export endpoint honors it (it shares
// parseIssueListInput), and the PQL field filters project
// lists and /my-issues (name/email/`me` resolution,
// ambiguous-name 400, unknown-name 400, `none` 400, and the
// private-project contrast: a private-only author's name
// still resolves but yields no rows). Resolution semantics
// and the workspace-scope reasoning live in the service
// tests (issue_createdby_filter_c118t0_test.go,
// pql_createdby_c118t0_test.go,
// mywork_pql_createdby_c118t0_test.go); this file pins the
// HTTP envelope.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"testing"

	"glance/internal/service"
)

func TestParseIssueListInputCreatedBy(t *testing.T) {
	uuid1 := "11111111-1111-1111-1111-111111111111"
	uuid2 := "22222222-2222-2222-2222-222222222222"

	// Repeatable + comma-separated values merge, whitespace trimmed.
	in, err := parseIssueListInput(url.Values{
		"created_by": {uuid1 + "," + uuid2, " " + uuid1},
	})
	if err != nil {
		t.Fatalf("parse created_by: %v", err)
	}
	if !reflect.DeepEqual(in.FilterCreatedBy, []string{uuid1, uuid2, uuid1}) {
		t.Fatalf("FilterCreatedBy = %v, want [%s %s %s]", in.FilterCreatedBy, uuid1, uuid2, uuid1)
	}

	// Empty segments are dropped; an all-empty param is no filter.
	in, err = parseIssueListInput(url.Values{"created_by": {" ,,"}})
	if err != nil {
		t.Fatalf("parse empty created_by: %v", err)
	}
	if len(in.FilterCreatedBy) != 0 {
		t.Fatalf("FilterCreatedBy = %v, want empty (no filter)", in.FilterCreatedBy)
	}

	// There is NO plural ?created_bys= alias: it is ignored,
	// never leaked into the filter (do not invent one).
	in, err = parseIssueListInput(url.Values{"created_bys": {uuid1}})
	if err != nil {
		t.Fatalf("parse plural created_bys: %v", err)
	}
	if len(in.FilterCreatedBy) != 0 {
		t.Fatalf("plural created_bys leaked into filter: %v", in.FilterCreatedBy)
	}

	// "none" passes this parser through (the shared
	// passthrough) — the SERVICE rejects it as a 400, proved
	// by the HTTP tests below, not by the parser.
	in, err = parseIssueListInput(url.Values{"created_by": {"none"}})
	if err != nil {
		t.Fatalf("parse none passthrough: %v", err)
	}
	if !reflect.DeepEqual(in.FilterCreatedBy, []string{"none"}) {
		t.Fatalf("FilterCreatedBy = %v, want [none] (service rejects it)", in.FilterCreatedBy)
	}

	// Malformed UUIDs are 400-class errors naming the param and
	// the bad value — never silently ignored, never a 500.
	for _, tc := range []struct {
		label string
		qp    url.Values
	}{
		{"created_by", url.Values{"created_by": {"not-a-uuid"}}},
		{"created_by-mixed", url.Values{"created_by": {uuid1 + ",bogus"}}},
	} {
		_, err := parseIssueListInput(tc.qp)
		if err == nil {
			t.Fatalf("parse %s with malformed UUID: no error", tc.label)
		}
		if !strings.Contains(err.Error(), "invalid created_by") {
			t.Fatalf("parse %s malformed UUID: error %q does not name the param", tc.label, err.Error())
		}
	}
}

func TestIssueListHTTPCreatedBy(t *testing.T) {
	pool, e, cookie, base := setupBulkProject(t, "cbhttp")
	ctx := context.Background()

	parts := strings.Split(base, "/")
	if len(parts) != 8 {
		t.Fatalf("base = %q, want /api/v1/workspaces/<slug>/projects/<ident>/issues", base)
	}
	slug, ident := parts[4], parts[6]
	var mainID, wsID string
	if err := pool.QueryRow(ctx,
		`SELECT w.id::text FROM workspaces w WHERE w.slug = $1`, slug).Scan(&wsID); err != nil {
		t.Fatalf("workspace id: %v", err)
	}

	// Bob: workspace member ("Bob Builder") who authors two issues.
	bobEmail := uniqueEmail("cb-http-bob")
	bobCookie := loginTestUser(t, e, pool, bobEmail, "test-agent", uniqueIP())
	_ = bobCookie
	var bobID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users WHERE email = $1`, bobEmail).Scan(&bobID); err != nil {
		t.Fatalf("bob id: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1::uuid, $2::uuid, 15)`,
		wsID, bobID); err != nil {
		t.Fatalf("bob membership: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET name = 'Bob Builder' WHERE id = $1::uuid`, bobID); err != nil {
		t.Fatalf("name Bob: %v", err)
	}
	// The main setup user is the other author; resolve its id.
	if err := pool.QueryRow(ctx,
		`SELECT m.user_id::text FROM workspace_members m WHERE m.workspace_id = $1::uuid AND m.role = 20 LIMIT 1`, wsID).Scan(&mainID); err != nil {
		t.Fatalf("main id: %v", err)
	}

	// a, c by main (via HTTP); b, d by bob (via service — the
	// HTTP create path always authors as the caller).
	createIssueHTTP(t, e, cookie, base, "byMainA")
	createIssueHTTP(t, e, cookie, base, "byMainC")
	for _, name := range []string{"byBobB", "byBobD"} {
		if _, err := service.CreateIssue(ctx, pool, slug, ident, bobID, service.CreateIssueInput{Name: name}); err != nil {
			t.Fatalf("CreateIssue %q as bob: %v", name, err)
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

	want(pqlListNamesHTTP(t, e, cookie, base, "created_by="+mainID+suffix), "byMainA", "byMainC")
	want(pqlListNamesHTTP(t, e, cookie, base, "created_by="+bobID+suffix), "byBobB", "byBobD")
	want(pqlListNamesHTTP(t, e, cookie, base, "created_by="+mainID+","+bobID+suffix), "byMainA", "byMainC", "byBobB", "byBobD")
	want(pqlListNamesHTTP(t, e, cookie, base, "created_by="+mainID+"&created_by="+bobID+suffix), "byMainA", "byMainC", "byBobB", "byBobD")
	// Empty = no filter.
	want(pqlListNamesHTTP(t, e, cookie, base, suffix[1:]), "byMainA", "byMainC", "byBobB", "byBobD")
	// Malformed values 400 on the list endpoint.
	rec := getAuthed(t, e, http.MethodGet, base+"?created_by=not-a-uuid"+suffix, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed created_by: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	// "none" 400s on the list endpoint (service validation —
	// there are no authorless issues).
	rec = getAuthed(t, e, http.MethodGet, base+"?created_by=none"+suffix, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("created_by=none: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestIssueExportHTTPCreatedBy(t *testing.T) {
	pool, e, cookie, base, _, _ := setupExportProject(t, "exp-cb")
	ctx := context.Background()

	parts := strings.Split(base, "/")
	slug2, ident2 := parts[4], parts[6]
	var wsID string
	if err := pool.QueryRow(ctx,
		`SELECT w.id::text FROM workspaces w WHERE w.slug = $1`, slug2).Scan(&wsID); err != nil {
		t.Fatalf("workspace id: %v", err)
	}
	bobEmail := uniqueEmail("cb-exp-bob")
	loginTestUser(t, e, pool, bobEmail, "test-agent", uniqueIP())
	var bobID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users WHERE email = $1`, bobEmail).Scan(&bobID); err != nil {
		t.Fatalf("bob id: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1::uuid, $2::uuid, 15)`,
		wsID, bobID); err != nil {
		t.Fatalf("bob membership: %v", err)
	}

	createIssueHTTP(t, e, cookie, base, "ExportByMain")
	if _, err := service.CreateIssue(ctx, pool, slug2, ident2, bobID, service.CreateIssueInput{Name: "ExportByBob"}); err != nil {
		t.Fatalf("CreateIssue as bob: %v", err)
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
	// filter applies there for free — proved, not assumed.
	if got := exportNames("created_by=" + bobID); strings.Join(got, "|") != "ExportByBob" {
		t.Fatalf("export created_by filter: got %v, want [ExportByBob]", got)
	}
	if got := exportNames(""); strings.Join(got, "|") != "ExportByBob|ExportByMain" {
		t.Fatalf("export unfiltered: got %v, want both issues", got)
	}
	// Malformed filter values 400 on the export endpoint too.
	rec := getExport(t, e, base+"/export?format=csv&created_by=not-a-uuid", cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("export malformed created_by: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	// "none" 400s on the export endpoint too.
	rec = getExport(t, e, base+"/export?format=csv&created_by=none", cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("export created_by=none: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	// JSON format honors the filter as well (spot check).
	rec = getExport(t, e, base+"/export?format=json&created_by="+bobID, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("export json created_by: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var rows []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode export json: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "ExportByBob" {
		t.Fatalf("export json created_by: got %v, want [ExportByBob]", rows)
	}
}

func TestCreatedByPQLHTTP(t *testing.T) {
	pool := newTestPool(t)
	e := testServer(t, pool)
	RegisterAuthRoutes(e, testAuthHandler(pool))
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterMyWorkRoutes(e, &MyWorkHandler{Pool: pool})

	aliceEmail := uniqueEmail("cb-pql-alice")
	aliceCookie := loginTestUser(t, e, pool, aliceEmail, "test-agent", uniqueIP())
	slug := uniqueSlug("cb-pql")
	createWorkspaceHTTP(t, e, aliceCookie, "Acme", slug)
	ident := uniqueProjectIdentifier("CB")
	createProjectHTTP(t, e, aliceCookie, slug, "Engineering", ident)
	base := "/api/v1/workspaces/" + slug + "/projects/" + ident
	myBase := "/api/v1/workspaces/" + slug + "/my-issues"

	bobEmail := uniqueEmail("cb-pql-bob")
	bobCookie := loginTestUser(t, e, pool, bobEmail, "test-agent", uniqueIP())
	ctx := context.Background()
	var aliceID, bobID, wsID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users WHERE email = $1`, aliceEmail).Scan(&aliceID); err != nil {
		t.Fatalf("alice id: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users WHERE email = $1`, bobEmail).Scan(&bobID); err != nil {
		t.Fatalf("bob id: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM workspaces WHERE slug = $1`, slug).Scan(&wsID); err != nil {
		t.Fatalf("workspace id: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1::uuid, $2::uuid, 15)`,
		wsID, bobID); err != nil {
		t.Fatalf("bob membership: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET name = 'Alice Example' WHERE id = $1::uuid`, aliceID); err != nil {
		t.Fatalf("name Alice: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET name = 'Bob Builder' WHERE id = $1::uuid`, bobID); err != nil {
		t.Fatalf("name Bob: %v", err)
	}
	// Twins sharing one display name, for the ambiguity 400.
	for _, prefix := range []string{"cb-pql-twin1", "cb-pql-twin2"} {
		twinEmail := uniqueEmail(prefix)
		loginTestUser(t, e, pool, twinEmail, "test-agent", uniqueIP())
		var twinID string
		if err := pool.QueryRow(ctx, `SELECT id::text FROM users WHERE email = $1`, twinEmail).Scan(&twinID); err != nil {
			t.Fatalf("twin id: %v", err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1::uuid, $2::uuid, 15)`,
			wsID, twinID); err != nil {
			t.Fatalf("twin membership: %v", err)
		}
		if _, err := pool.Exec(ctx, `UPDATE users SET name = 'Sam Twin' WHERE id = $1::uuid`, twinID); err != nil {
			t.Fatalf("name Sam Twin: %v", err)
		}
	}

	create := func(cookie *http.Cookie, projectBase, name string) string {
		t.Helper()
		rec := postAuthedJSON(t, e, http.MethodPost, projectBase+"/issues", cookie,
			`{"name":"`+name+`"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %q: status = %d (body: %s)", name, rec.Code, rec.Body.String())
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
		if _, err := pool.Exec(ctx,
			`INSERT INTO issue_assignees (issue_id, user_id) VALUES ($1::uuid, $2::uuid)`,
			issueID, userID); err != nil {
			t.Fatalf("assign: %v", err)
		}
	}
	fetchProjectNames := func(path string, cookie *http.Cookie) []string {
		t.Helper()
		rec := getAuthed(t, e, http.MethodGet, path, cookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: status = %d, want 200 (body: %s)", path, rec.Code, rec.Body.String())
		}
		var body struct {
			Results []struct {
				Name string `json:"name"`
			} `json:"results"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		names := make([]string, 0, len(body.Results))
		for _, it := range body.Results {
			names = append(names, it.Name)
		}
		return names
	}
	fetchMyNames := func(path string, cookie *http.Cookie) []string {
		t.Helper()
		rec := getAuthed(t, e, http.MethodGet, path, cookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: status = %d, want 200 (body: %s)", path, rec.Code, rec.Body.String())
		}
		var body struct {
			Issues []struct {
				Name string `json:"name"`
			} `json:"issues"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		names := make([]string, 0, len(body.Issues))
		for _, it := range body.Issues {
			names = append(names, it.Name)
		}
		return names
	}
	wantExactly := func(got []string, want ...string) {
		t.Helper()
		sort.Strings(got)
		sort.Strings(want)
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Fatalf("got %v, want exactly %v", got, want)
		}
	}
	wantPQL400 := func(path string, cookie *http.Cookie, substr string) {
		t.Helper()
		rec := getAuthed(t, e, http.MethodGet, path, cookie)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("GET %s: status = %d, want 400 (body: %s)", path, rec.Code, rec.Body.String())
		}
		msg, pos, hasPos := pqlErrorBody(t, rec.Body.String())
		if !strings.Contains(msg, substr) {
			t.Fatalf("GET %s: error message = %q, want substring %q", path, msg, substr)
		}
		if !hasPos || pos <= 0 {
			t.Fatalf("GET %s: error position = %v (hasPos=%v), want a positive column", path, pos, hasPos)
		}
	}

	// Project scope: alpha + gamma by alice, beta by bob.
	alpha := create(aliceCookie, base, "alpha")
	beta := create(bobCookie, base, "beta")
	gamma := create(aliceCookie, base, "gamma")
	for _, id := range []string{alpha, beta, gamma} {
		assign(id, bobID)
	}

	wantExactly(fetchProjectNames(base+"/issues?pql="+url.QueryEscape(`created_by = me`), aliceCookie), "alpha", "gamma")
	wantExactly(fetchProjectNames(base+"/issues?pql="+url.QueryEscape(`created_by = me`), bobCookie), "beta")
	wantExactly(fetchProjectNames(base+"/issues?pql="+url.QueryEscape(`created_by = "Alice Example"`), aliceCookie), "alpha", "gamma")
	wantExactly(fetchProjectNames(base+"/issues?pql="+url.QueryEscape(`created_by = "alice example"`), aliceCookie), "alpha", "gamma")
	wantExactly(fetchProjectNames(base+"/issues?pql="+url.QueryEscape(`created_by = `+bobEmail), aliceCookie), "beta")
	wantExactly(fetchProjectNames(base+"/issues?pql="+url.QueryEscape(`created_by != me`), aliceCookie), "beta")
	wantExactly(fetchProjectNames(base+"/issues?pql="+url.QueryEscape(`created_by in (me, "Bob Builder")`), aliceCookie), "alpha", "beta", "gamma")
	// Unknown name, ambiguous name, and `none` all 400 with
	// message + position on the project list.
	wantPQL400(base+"/issues?pql="+url.QueryEscape(`created_by = NoSuchAuthor`), aliceCookie, `unknown created_by "NoSuchAuthor"`)
	wantPQL400(base+"/issues?pql="+url.QueryEscape(`created_by = "Sam Twin"`), aliceCookie, `ambiguous created_by "Sam Twin"`)
	wantPQL400(base+"/issues?pql="+url.QueryEscape(`created_by = none`), aliceCookie, `every issue has an author`)

	// Workspace scope (/my-issues, assigned filter): the same
	// member resolution applies — bob sees alice's issues by
	// her name and his own by `me`.
	wantExactly(fetchMyNames(myBase+"?filter=assigned&pql="+url.QueryEscape(`created_by = "Alice Example"`), bobCookie), "alpha", "gamma")
	wantExactly(fetchMyNames(myBase+"?filter=assigned&pql="+url.QueryEscape(`created_by = me`), bobCookie), "beta")
	wantPQL400(myBase+"?filter=assigned&pql="+url.QueryEscape(`created_by = NoSuchAuthor`), bobCookie, `unknown created_by "NoSuchAuthor"`)
	wantPQL400(myBase+"?filter=assigned&pql="+url.QueryEscape(`created_by = none`), bobCookie, `every issue has an author`)

	// Private-project contrast (C118, unlike C117's release
	// rule): a private-only author's name still resolves, but
	// yields no rows for Bob, who cannot reach that project.
	identP := uniqueProjectIdentifier("CB")
	createProjectHTTP(t, e, aliceCookie, slug, "Private", identP)
	if _, err := pool.Exec(ctx,
		`UPDATE projects SET visibility = 'private' WHERE workspace_id = $1::uuid AND identifier = $2`, wsID, identP); err != nil {
		t.Fatalf("make private: %v", err)
	}
	privEmail := uniqueEmail("cb-pql-priv")
	privCookie := loginTestUser(t, e, pool, privEmail, "test-agent", uniqueIP())
	var privID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users WHERE email = $1`, privEmail).Scan(&privID); err != nil {
		t.Fatalf("priv id: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1::uuid, $2::uuid, 15)`,
		wsID, privID); err != nil {
		t.Fatalf("priv membership: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET name = 'Priv Author' WHERE id = $1::uuid`, privID); err != nil {
		t.Fatalf("name Priv Author: %v", err)
	}
	if _, err := service.AddProjectMember(ctx, pool, slug, identP, aliceID, privID, service.RoleMember); err != nil {
		t.Fatalf("AddProjectMember priv: %v", err)
	}
	hidden := create(privCookie, "/api/v1/workspaces/"+slug+"/projects/"+identP, "hidden")
	assign(hidden, bobID)
	// Resolves (200), but no rows: confinement, not a 400.
	wantExactly(fetchMyNames(myBase+"?filter=assigned&pql="+url.QueryEscape(`created_by = "Priv Author"`), bobCookie))
	// And the unfiltered assigned list yields no private row.
	for _, n := range fetchMyNames(myBase+"?filter=assigned", bobCookie) {
		if n == "hidden" {
			t.Fatal("row leak: private-project issue visible in unfiltered my-issues")
		}
	}
}
