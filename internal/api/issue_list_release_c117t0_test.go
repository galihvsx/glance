package api

// C117T0: parse-level + HTTP tests for the new ?releases=
// visual list filter and the `release` PQL field. Parse:
// repeatable AND comma-separated via parseRepeatIDList,
// "none" sentinel, malformed → 400 naming the param. HTTP:
// the project list endpoint round-trips the filter, the
// export endpoint honors it (it shares parseIssueListInput),
// and the PQL field filters project lists and /my-issues
// (workspace multi-match + unreachable-name 400). Resolution
// semantics, set semantics, and the taxonomy-leak rule live
// in the service tests (issue_release_filter_c117t0_test.go,
// pql_release_c117t0_test.go, mywork_pql_release_c117t0_test.go);
// this file pins the HTTP envelope.

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

	"glance/internal/service"
)

func TestParseIssueListInputReleases(t *testing.T) {
	uuid1 := "11111111-1111-1111-1111-111111111111"
	uuid2 := "22222222-2222-2222-2222-222222222222"

	// Repeatable + comma-separated values merge, whitespace trimmed.
	in, err := parseIssueListInput(url.Values{
		"releases": {uuid1 + "," + uuid2, " none "},
	})
	if err != nil {
		t.Fatalf("parse releases: %v", err)
	}
	if !reflect.DeepEqual(in.FilterReleases, []string{uuid1, uuid2, "none"}) {
		t.Fatalf("FilterReleases = %v, want [%s %s none]", in.FilterReleases, uuid1, uuid2)
	}

	// "none" alone parses (the no-release sentinel).
	in, err = parseIssueListInput(url.Values{"releases": {"none"}})
	if err != nil {
		t.Fatalf("parse none sentinel: %v", err)
	}
	if !reflect.DeepEqual(in.FilterReleases, []string{"none"}) {
		t.Fatalf("FilterReleases = %v, want [none]", in.FilterReleases)
	}

	// Empty segments are dropped; an all-empty param is no filter.
	in, err = parseIssueListInput(url.Values{"releases": {" ,,"}})
	if err != nil {
		t.Fatalf("parse empty releases: %v", err)
	}
	if len(in.FilterReleases) != 0 {
		t.Fatalf("FilterReleases = %v, want empty (no filter)", in.FilterReleases)
	}

	// There is NO singular ?release= scope param: it is ignored,
	// never leaked into the filter (do not invent one).
	in, err = parseIssueListInput(url.Values{"release": {uuid1}})
	if err != nil {
		t.Fatalf("parse singular release: %v", err)
	}
	if len(in.FilterReleases) != 0 {
		t.Fatalf("singular release leaked into filter: %v", in.FilterReleases)
	}

	// Malformed UUIDs are 400-class errors naming the param and
	// the bad value — never silently ignored, never a 500.
	for _, tc := range []struct {
		label string
		qp    url.Values
	}{
		{"releases", url.Values{"releases": {"not-a-uuid"}}},
		{"releases-mixed", url.Values{"releases": {uuid1 + ",bogus"}}},
	} {
		_, err := parseIssueListInput(tc.qp)
		if err == nil {
			t.Fatalf("parse %s with malformed UUID: no error", tc.label)
		}
		if !strings.Contains(err.Error(), "invalid releases") {
			t.Fatalf("parse %s malformed UUID: error %q does not name the param", tc.label, err.Error())
		}
	}
}

func TestIssueListHTTPReleases(t *testing.T) {
	pool, e, cookie, base := setupBulkProject(t, "relhttp")
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

	insertRelease := func(name string) string {
		var id string
		if err := pool.QueryRow(ctx,
			`INSERT INTO releases (project_id, name) VALUES ($1::uuid, $2) RETURNING id::text`,
			projectID, name).Scan(&id); err != nil {
			t.Fatalf("insert release %s: %v", name, err)
		}
		return id
	}
	r1 := insertRelease("ReleaseOne")
	r2 := insertRelease("ReleaseTwo")

	// a in r1, b in r2, c in neither, d in r1.
	aID := createIssueHTTP(t, e, cookie, base, "inR1")
	bID := createIssueHTTP(t, e, cookie, base, "inR2")
	createIssueHTTP(t, e, cookie, base, "inNeither")
	dID := createIssueHTTP(t, e, cookie, base, "inR1Too")
	for _, q := range []string{
		fmt.Sprintf(`UPDATE issues SET release_id = '%s' WHERE id = '%s'`, r1, aID),
		fmt.Sprintf(`UPDATE issues SET release_id = '%s' WHERE id = '%s'`, r2, bID),
		fmt.Sprintf(`UPDATE issues SET release_id = '%s' WHERE id = '%s'`, r1, dID),
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

	want(pqlListNamesHTTP(t, e, cookie, base, "releases="+r1+suffix), "inR1", "inR1Too")
	want(pqlListNamesHTTP(t, e, cookie, base, "releases="+r1+","+r2+suffix), "inR1", "inR1Too", "inR2")
	want(pqlListNamesHTTP(t, e, cookie, base, "releases="+r1+"&releases="+r2+suffix), "inR1", "inR1Too", "inR2")
	want(pqlListNamesHTTP(t, e, cookie, base, "releases=none"+suffix), "inNeither")
	want(pqlListNamesHTTP(t, e, cookie, base, "releases="+r1+",none"+suffix), "inR1", "inR1Too", "inNeither")
	// Empty = no filter.
	want(pqlListNamesHTTP(t, e, cookie, base, suffix[1:]), "inR1", "inR1Too", "inR2", "inNeither")
	// Malformed values 400 on the list endpoint.
	rec := getAuthed(t, e, http.MethodGet, base+"?releases=not-a-uuid"+suffix, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed releases: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestIssueExportHTTPReleases(t *testing.T) {
	pool, e, cookie, base, _, _ := setupExportProject(t, "exp-rel")
	ctx := context.Background()

	parts := strings.Split(base, "/")
	slug2, ident2 := parts[4], parts[6]
	var projectID string
	if err := pool.QueryRow(ctx,
		`SELECT p.id::text FROM projects p JOIN workspaces w ON w.id = p.workspace_id
		  WHERE w.slug = $1 AND p.identifier = $2`, slug2, ident2).Scan(&projectID); err != nil {
		t.Fatalf("project id: %v", err)
	}
	var r1 string
	if err := pool.QueryRow(ctx,
		`INSERT INTO releases (project_id, name) VALUES ($1::uuid, 'ExportRelease') RETURNING id::text`,
		projectID).Scan(&r1); err != nil {
		t.Fatalf("insert release: %v", err)
	}

	inID := createIssueHTTP(t, e, cookie, base, "ExportInRelease")
	createIssueHTTP(t, e, cookie, base, "ExportOutRelease")
	if _, err := pool.Exec(ctx,
		`UPDATE issues SET release_id = $1::uuid WHERE id = $2::uuid`, r1, inID); err != nil {
		t.Fatalf("attach release: %v", err)
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
	if got := exportNames("releases=" + r1); strings.Join(got, "|") != "ExportInRelease" {
		t.Fatalf("export releases filter: got %v, want [ExportInRelease]", got)
	}
	if got := exportNames("releases=none"); strings.Join(got, "|") != "ExportOutRelease" {
		t.Fatalf("export releases=none: got %v, want [ExportOutRelease]", got)
	}
	if got := exportNames(""); strings.Join(got, "|") != "ExportInRelease|ExportOutRelease" {
		t.Fatalf("export unfiltered: got %v, want both issues", got)
	}
	// Malformed filter values 400 on the export endpoint too.
	rec := getExport(t, e, base+"/export?format=csv&releases=not-a-uuid", cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("export malformed releases: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	// JSON format honors the filter as well (spot check).
	rec = getExport(t, e, base+"/export?format=json&releases="+r1, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("export json releases: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var rows []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode export json: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "ExportInRelease" {
		t.Fatalf("export json releases: got %v, want [ExportInRelease]", rows)
	}
}

func TestReleasePQLHTTP(t *testing.T) {
	pool := newTestPool(t)
	e := testServer(t, pool)
	RegisterAuthRoutes(e, testAuthHandler(pool))
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterMyWorkRoutes(e, &MyWorkHandler{Pool: pool})

	aliceEmail := uniqueEmail("rel-pql-alice")
	aliceCookie := loginTestUser(t, e, pool, aliceEmail, "test-agent", uniqueIP())
	slug := uniqueSlug("rel-pql")
	createWorkspaceHTTP(t, e, aliceCookie, "Acme", slug)
	ident := uniqueProjectIdentifier("RL")
	createProjectHTTP(t, e, aliceCookie, slug, "Engineering", ident)
	base := "/api/v1/workspaces/" + slug + "/projects/" + ident
	myBase := "/api/v1/workspaces/" + slug + "/my-issues"

	bobEmail := uniqueEmail("rel-pql-bob")
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
	assign := func(issueID string) {
		t.Helper()
		if _, err := pool.Exec(ctx,
			`INSERT INTO issue_assignees (issue_id, user_id) VALUES ($1::uuid, $2::uuid)`,
			issueID, bobID); err != nil {
			t.Fatalf("assign: %v", err)
		}
	}
	fetchProjectNames := func(path string) []string {
		t.Helper()
		rec := getAuthed(t, e, http.MethodGet, path, aliceCookie)
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
	fetchMyNames := func(path string) []string {
		t.Helper()
		rec := getAuthed(t, e, http.MethodGet, path, bobCookie)
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

	// Project scope: v1.0 / v2.0 releases, alpha in v1.0,
	// beta in v2.0, gamma in neither.
	rel1, err := service.CreateRelease(ctx, pool, slug, ident, aliceID, service.ReleaseInput{Name: "v1.0"})
	if err != nil {
		t.Fatalf("CreateRelease v1.0: %v", err)
	}
	rel2, err := service.CreateRelease(ctx, pool, slug, ident, aliceID, service.ReleaseInput{Name: "v2.0"})
	if err != nil {
		t.Fatalf("CreateRelease v2.0: %v", err)
	}
	alpha := create(aliceCookie, base, "alpha")
	beta := create(aliceCookie, base, "beta")
	gamma := create(aliceCookie, base, "gamma")
	if err := service.AssignReleaseIssues(ctx, pool, slug, ident, aliceID, rel1.ID, []string{alpha}); err != nil {
		t.Fatalf("AssignReleaseIssues v1.0: %v", err)
	}
	if err := service.AssignReleaseIssues(ctx, pool, slug, ident, aliceID, rel2.ID, []string{beta}); err != nil {
		t.Fatalf("AssignReleaseIssues v2.0: %v", err)
	}
	for _, id := range []string{alpha, beta, gamma} {
		assign(id)
	}

	wantExactly(fetchProjectNames(base+"/issues?pql="+url.QueryEscape(`release = "v1.0"`)), "alpha")
	wantExactly(fetchProjectNames(base+"/issues?pql="+url.QueryEscape(`release = none`)), "gamma")
	wantExactly(fetchProjectNames(base+"/issues?pql="+url.QueryEscape(`release != "v1.0"`)), "beta", "gamma")
	wantExactly(fetchProjectNames(base+"/issues?pql="+url.QueryEscape(`release in ("v1.0", none)`)), "alpha", "gamma")
	// Unknown name 400s with message + position on the project list.
	rec := getAuthed(t, e, http.MethodGet, base+"/issues?pql="+url.QueryEscape(`release = NoSuchRelease`), aliceCookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown release: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	msg, pos, hasPos := pqlErrorBody(t, rec.Body.String())
	if !strings.Contains(msg, `unknown release "NoSuchRelease"`) {
		t.Fatalf("error message = %q, want unknown-release text", msg)
	}
	if !hasPos || pos <= 0 {
		t.Fatalf("error position = %v (hasPos=%v), want a positive column", pos, hasPos)
	}

	// Workspace scope, multi-match: a second project with a
	// release of the SAME name ("Shared Release"); /my-issues
	// matches rows in BOTH projects.
	identB := uniqueProjectIdentifier("RL")
	createProjectHTTP(t, e, aliceCookie, slug, "Backend", identB)
	baseB := "/api/v1/workspaces/" + slug + "/projects/" + identB
	sharedA, err := service.CreateRelease(ctx, pool, slug, ident, aliceID, service.ReleaseInput{Name: "Shared Release"})
	if err != nil {
		t.Fatalf("CreateRelease Shared A: %v", err)
	}
	sharedB, err := service.CreateRelease(ctx, pool, slug, identB, aliceID, service.ReleaseInput{Name: "Shared Release"})
	if err != nil {
		t.Fatalf("CreateRelease Shared B: %v", err)
	}
	a1 := create(aliceCookie, base, "sharedA")
	b1 := create(aliceCookie, baseB, "sharedB")
	n0 := create(aliceCookie, base, "sharedNone")
	if err := service.AssignReleaseIssues(ctx, pool, slug, ident, aliceID, sharedA.ID, []string{a1}); err != nil {
		t.Fatalf("AssignReleaseIssues Shared A: %v", err)
	}
	if err := service.AssignReleaseIssues(ctx, pool, slug, identB, aliceID, sharedB.ID, []string{b1}); err != nil {
		t.Fatalf("AssignReleaseIssues Shared B: %v", err)
	}
	for _, id := range []string{a1, b1, n0} {
		assign(id)
	}
	wantExactly(fetchMyNames(myBase+"?filter=assigned&pql="+url.QueryEscape(`release = "Shared Release"`)), "sharedA", "sharedB")
	wantExactly(fetchMyNames(myBase+"?filter=assigned&pql="+url.QueryEscape(`release = none`)), "gamma", "sharedNone")

	// Unreachable name: a release existing ONLY in a private
	// project Bob cannot reach 400s as unknown, never leaks.
	identP := uniqueProjectIdentifier("RL")
	createProjectHTTP(t, e, aliceCookie, slug, "Private", identP)
	if _, err := pool.Exec(ctx,
		`UPDATE projects SET visibility = 'private' WHERE workspace_id = $1::uuid AND identifier = $2`, wsID, identP); err != nil {
		t.Fatalf("make private: %v", err)
	}
	shadow, err := service.CreateRelease(ctx, pool, slug, identP, aliceID, service.ReleaseInput{Name: "ShadowRelease"})
	if err != nil {
		t.Fatalf("CreateRelease Shadow: %v", err)
	}
	hidden := create(aliceCookie, "/api/v1/workspaces/"+slug+"/projects/"+identP, "hidden")
	assign(hidden)
	if err := service.AssignReleaseIssues(ctx, pool, slug, identP, aliceID, shadow.ID, []string{hidden}); err != nil {
		t.Fatalf("AssignReleaseIssues Shadow: %v", err)
	}
	rec = getAuthed(t, e, http.MethodGet, myBase+"?filter=assigned&pql="+url.QueryEscape(`release = ShadowRelease`), bobCookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unreachable release: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	msg, pos, hasPos = pqlErrorBody(t, rec.Body.String())
	if !strings.Contains(msg, `unknown release "ShadowRelease"`) {
		t.Fatalf("error message = %q, want unknown-release text", msg)
	}
	if !hasPos || pos <= 0 {
		t.Fatalf("error position = %v (hasPos=%v), want a positive column", pos, hasPos)
	}
	// And the unfiltered assigned list yields no private row.
	for _, n := range fetchMyNames(myBase + "?filter=assigned") {
		if n == "hidden" {
			t.Fatal("row leak: private-project issue visible in unfiltered my-issues")
		}
	}
}
