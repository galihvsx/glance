package service

// Jira Cloud issues importer tests (C9T1). A stub httptest server emulates
// the Jira Cloud REST v3 API: POST /rest/api/3/search/jql with
// nextPageToken pagination, per-issue comments (ADF bodies), the pinned
// <site>.atlassian.net host rule, and error mapping. All tests run against
// the real test database — no skips.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------- ADF fixtures ----------

const jiraADFDoc = `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"the body"}]}]}`

const jiraADFRich = `{"type":"doc","version":1,"content":[` +
	`{"type":"heading","attrs":{"level":2},"content":[{"type":"text","text":"Repro"}]},` +
	`{"type":"bulletList","content":[` +
	`{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"step one"}]}]},` +
	`{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"step two"}]}]}]},` +
	`{"type":"codeBlock","content":[{"type":"text","text":"panic: nil"}]},` +
	`{"type":"paragraph","content":[{"type":"text","text":"by "},{"type":"text","text":"Ada","marks":[{"type":"strong"}]},{"type":"mention","attrs":{"text":"@ada"}}]}` +
	`]}`

// TestJiraADFToText pins the ADF→plain-text extractor used for v3
// description and comment bodies (v3 has no plain-text rendering).
func TestJiraADFToText(t *testing.T) {
	if got := jiraADFToText(json.RawMessage(jiraADFDoc)); got != "the body" {
		t.Errorf("simple doc = %q, want %q", got, "the body")
	}
	got := jiraADFToText(json.RawMessage(jiraADFRich))
	for _, want := range []string{"Repro", "step one", "step two", "panic: nil", "Ada"} {
		if !strings.Contains(got, want) {
			t.Errorf("rich doc missing %q:\n%s", want, got)
		}
	}
	if got := jiraADFToText(nil); got != "" {
		t.Errorf("nil ADF = %q, want empty", got)
	}
	if got := jiraADFToText(json.RawMessage(`"plain string"`)); got != "plain string" {
		t.Errorf("string ADF = %q, want %q", got, "plain string")
	}
}

// ---------- stub server ----------

// jiraIssueJSON builds one v3 search-result issue for the stub.
func jiraIssueJSON(key, summary, statusName, statusCat, priority, assigneeEmail string, labels []string, commentTotal int, descADF string) string {
	fields := map[string]any{
		"summary":   summary,
		"status":    map[string]any{"name": statusName, "statusCategory": map[string]any{"key": statusCat, "name": statusCat}},
		"labels":    labels,
		"assignee":  nil,
		"priority":  nil,
		"comment":   map[string]any{"comments": []any{}, "total": commentTotal, "maxResults": 100, "startAt": 0},
		"created":   "2026-01-01T00:00:00.000+0000",
		"updated":   "2026-01-02T00:00:00.000+0000",
		"issuetype": map[string]any{"name": "Bug"},
	}
	if assigneeEmail != "" {
		fields["assignee"] = map[string]any{"displayName": "Ada Member", "emailAddress": assigneeEmail}
	}
	if priority != "" {
		fields["priority"] = map[string]any{"name": priority}
	}
	if descADF != "" {
		var doc any
		if err := json.Unmarshal([]byte(descADF), &doc); err != nil {
			panic(err)
		}
		fields["description"] = doc
	}
	out, _ := json.Marshal(map[string]any{"key": key, "fields": fields})
	return string(out)
}

// jiraStub emulates Jira Cloud's REST v3 API. searchHandler and
// commentsHandler can be overridden per test for error-mapping cases.
type jiraStub struct {
	mu        sync.Mutex
	seenAuth  []string
	seenHosts []string
	seenJQL   []string

	issues []string

	searchHandler   func(w http.ResponseWriter, r *http.Request)
	commentsHandler func(w http.ResponseWriter, r *http.Request)
}

// jiraStubIssues returns the four canned search-result issues: one per
// page-pair (two pages of two), with a mix of matched/unmatched statuses,
// priorities, assignees, labels, and comment counts.
func jiraStubIssues(memberEmail string) []string {
	return []string{
		jiraIssueJSON("PROJ-1", "First bug", "To Do", "new", "High", memberEmail, []string{"backend"}, 2, jiraADFDoc),
		jiraIssueJSON("PROJ-2", "In flight", "In Progress", "indeterminate", "Medium", "ghost@example.com", []string{}, 0, ""),
		jiraIssueJSON("PROJ-3", "Shipped it", "done", "done", "Lowest", "", []string{"backend", "docs"}, 0, ""),
		jiraIssueJSON("PROJ-4", "Needs eyes", "In Review", "indeterminate", "", "", []string{}, 1, jiraADFRich),
	}
}

func (s *jiraStub) record(r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seenAuth = append(s.seenAuth, r.Header.Get("Authorization"))
	s.seenHosts = append(s.seenHosts, r.Host)
	// Read the body for the JQL assertion, then restore it — the
	// search handler decodes it again.
	raw, _ := io.ReadAll(r.Body)
	r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(raw))
	var body struct {
		JQL string `json:"jql"`
	}
	_ = json.Unmarshal(raw, &body)
	s.seenJQL = append(s.seenJQL, body.JQL)
}

// authOK pins the token transport: HTTP Basic with email:api_token, and
// the raw token must never appear in a URL or in any other header value.
func (s *jiraStub) authOK(t *testing.T, email, token string) {
	t.Helper()
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte(email+":"+token))
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.seenAuth) == 0 {
		t.Fatal("no requests recorded")
	}
	for _, a := range s.seenAuth {
		if a != want {
			t.Errorf("Authorization header = %q, want Basic(email:token)", a)
		}
	}
}

func (s *jiraStub) defaultSearch(w http.ResponseWriter, r *http.Request) {
	s.record(r)
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		JQL           string   `json:"jql"`
		NextPageToken *string  `json:"nextPageToken"`
		MaxResults    int      `json:"maxResults"`
		Fields        []string `json:"fields"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"errorMessages":["bad body"]}`)
		return
	}
	if !strings.Contains(body.JQL, `project = "PROJ"`) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `{"errorMessages":["unexpected JQL: %s"]}`, body.JQL)
		return
	}
	// Two pages of two issues each, token-based like the real API.
	issues := s.issues
	var page []string
	var next *string
	var isLast bool
	if body.NextPageToken == nil {
		page, next, isLast = issues[:2], jiraStrPtr("page2"), false
	} else if *body.NextPageToken == "page2" {
		page, next, isLast = issues[2:], nil, true
	} else {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"errorMessages":["bad page token"]}`)
		return
	}
	resp := map[string]any{"isLast": isLast}
	var raw []json.RawMessage
	for _, is := range page {
		raw = append(raw, json.RawMessage(is))
	}
	resp["issues"] = raw
	if next != nil {
		resp["nextPageToken"] = *next
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func jiraStrPtr(s string) *string { return &s }

func (s *jiraStub) defaultComments(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.seenAuth = append(s.seenAuth, r.Header.Get("Authorization"))
	s.seenHosts = append(s.seenHosts, r.Host)
	s.mu.Unlock()
	key := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/rest/api/3/issue/"), "/comment")
	mkComment := func(id, who, adf, created string) map[string]any {
		var doc any
		_ = json.Unmarshal([]byte(adf), &doc)
		return map[string]any{
			"id":      id,
			"author":  map[string]any{"displayName": who, "emailAddress": who + "@example.com"},
			"body":    doc,
			"created": created,
			"updated": created,
		}
	}
	comments := map[string][]map[string]any{
		"PROJ-1": {
			mkComment("10001", "Ada", jiraADFDoc, "2026-01-03T10:00:00.000+0000"),
			mkComment("10002", "Bob", jiraADFRich, "2026-01-04T10:00:00.000+0000"),
		},
		"PROJ-4": {
			mkComment("10003", "Cara", jiraADFDoc, "2026-01-05T10:00:00.000+0000"),
		},
	}
	all := comments[key]
	startAt := 0
	if v := r.URL.Query().Get("startAt"); v != "" {
		fmt.Sscanf(v, "%d", &startAt)
	}
	// One comment per page to exercise comment pagination.
	pageSize := 1
	var page []map[string]any
	for i := startAt; i < len(all) && i < startAt+pageSize; i++ {
		page = append(page, all[i])
	}
	if page == nil {
		page = []map[string]any{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"comments":   page,
		"total":      len(all),
		"maxResults": pageSize,
		"startAt":    startAt,
	})
}

func newJiraStub(t *testing.T, memberEmail string) (*jiraStub, *httptest.Server) {
	t.Helper()
	s := &jiraStub{issues: jiraStubIssues(memberEmail)}
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/api/3/search/jql", func(w http.ResponseWriter, r *http.Request) {
		if s.searchHandler != nil {
			s.searchHandler(w, r)
			return
		}
		s.defaultSearch(w, r)
	})
	mux.HandleFunc("/rest/api/3/issue/", func(w http.ResponseWriter, r *http.Request) {
		if s.commentsHandler != nil {
			s.commentsHandler(w, r)
			return
		}
		s.defaultComments(w, r)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return s, srv
}

// jiraImportFixture reuses the GitHub fixture shape: actor (member) +
// second member whose email the stub's assignee matches. The project ID
// is returned so assertions can scope to it — the test database is
// never truncated, so unscoped name queries collide with other tests.
func jiraImportFixture(t *testing.T, pool *pgxpool.Pool) (actorID, memberEmail, wsSlug, ident, projectID string) {
	t.Helper()
	ctx := context.Background()
	actorID = createTestUser(t, pool, uniqueTestEmail("jira-import-actor"))
	memberEmail = uniqueTestEmail("jira-member")
	memberID := createTestUser(t, pool, memberEmail)
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-jira"), actorID)
	if _, err := pool.Exec(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role)
		 VALUES ($1::uuid, $2::uuid, $3) ON CONFLICT DO NOTHING`,
		ws.ID, memberID, RoleMember); err != nil {
		t.Fatalf("add member: %v", err)
	}
	p := createTestProject(t, pool, ws.Slug, actorID, "Widgets", uniqueTestIdentifier())
	return actorID, memberEmail, ws.Slug, p.Identifier, p.ID
}

func jiraTestInput() JiraImportInput {
	return JiraImportInput{
		Site:       "acme",
		Email:      "bot@example.com",
		APIToken:   "ATATT3x" + "DONOTSTORE_z9x8c7v6b5",
		ProjectKey: "PROJ",
		Max:        100,
	}
}

func jiraTestClient(t *testing.T, s *jiraStub, srv *httptest.Server, in JiraImportInput) *jiraClient {
	t.Helper()
	return newJiraTestClient(in.Email, in.APIToken, srv.URL)
}

// ---------- validation ----------

// TestJiraImportInputValidate pins the input contract: subdomain-only
// site (strict DNS-label charset), strict project-key charset (JQL
// injection is impossible through it), and max clamping.
func TestJiraImportInputValidate(t *testing.T) {
	in := jiraTestInput()
	if _, _, _, err := in.validate(); err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}
	bad := []JiraImportInput{
		{Site: "", Email: "a@b.c", APIToken: "t", ProjectKey: "PROJ"},
		{Site: "acme.atlassian.net", Email: "a@b.c", APIToken: "t", ProjectKey: "PROJ"}, // full host, not subdomain
		{Site: "evil.com", Email: "a@b.c", APIToken: "t", ProjectKey: "PROJ"},           // TLD suffix confusion
		{Site: "acme.atlassian.net.evil.com", Email: "a@b.c", APIToken: "t", ProjectKey: "PROJ"},
		{Site: "ac me", Email: "a@b.c", APIToken: "t", ProjectKey: "PROJ"},
		{Site: "acme", Email: "a@b.c", APIToken: "t", ProjectKey: ""},                // missing key
		{Site: "acme", Email: "a@b.c", APIToken: "t", ProjectKey: `PROJ" OR 1=1 --`}, // JQL injection attempt
		{Site: "acme", Email: "a@b.c", APIToken: "t", ProjectKey: "proj key"},        // space in key
		{Site: "acme", Email: "", APIToken: "t", ProjectKey: "PROJ"},                 // missing email
	}
	for i, b := range bad {
		if _, _, _, err := b.validate(); !errors.Is(err, ErrJiraBadInput) {
			t.Errorf("bad input %d: err = %v, want ErrJiraBadInput", i, err)
		}
	}
	// Case is normalized: lowercase site/key are accepted and uppercased
	// for JQL/host construction.
	lower := jiraTestInput()
	lower.Site = "Acme"
	lower.ProjectKey = "proj"
	site, key, _, err := lower.validate()
	if err != nil {
		t.Fatalf("lowercase input rejected: %v", err)
	}
	if site != "acme" || key != "PROJ" {
		t.Errorf("normalized = (%q,%q), want (acme,PROJ)", site, key)
	}
	// Max clamping mirrors the GitHub importer (default 100, abs 1000).
	m := jiraTestInput()
	m.Max = 0
	if _, _, max, err := m.validate(); err != nil || max != 100 {
		t.Errorf("max=0 → (%d,%v), want (100,nil)", max, err)
	}
	m.Max = 5000
	site2, key2, max2, err2 := m.validate()
	if err2 != nil || max2 != 1000 || site2 != "acme" || key2 != "PROJ" {
		t.Errorf("max=5000 → (%q,%q,%d,%v), want (acme,PROJ,1000,nil)", site2, key2, max2, err2)
	}
}

// ---------- SSRF guard ----------

// TestJiraSSRFGuard pins the fixed-host rule: the production client only
// ever builds https://<site>.atlassian.net URLs; a tampered base, plain
// HTTP, or a non-atlassian.net host is rejected; Server/DC-style freeform
// hosts are impossible by construction (the host is derived from the
// validated site, never from user input).
func TestJiraSSRFGuard(t *testing.T) {
	// Production client builds the pinned host.
	c, err := newJiraClient("bot@example.com", "tok", "acme")
	if err != nil {
		t.Fatalf("newJiraClient: %v", err)
	}
	raw, err := c.urlFor("/rest/api/3/search/jql")
	if err != nil {
		t.Fatalf("urlFor: %v", err)
	}
	if raw != "https://acme.atlassian.net/rest/api/3/search/jql" {
		t.Errorf("urlFor = %q, want the pinned atlassian.net URL", raw)
	}

	// A non-atlassian.net base with enforcement on is rejected.
	hostile := &jiraClient{http: newJiraHTTPClient(), baseURL: "https://evil.example", enforceHost: true, expectedHost: "acme.atlassian.net"}
	if _, err := hostile.urlFor("/rest/api/3/search/jql"); !errors.Is(err, errJiraSSRF) {
		t.Errorf("hostile urlFor err = %v, want errJiraSSRF", err)
	}
	// A lookalike subdomain (different tenant) is also rejected: the
	// guard pins the EXACT host, not the atlassian.net suffix.
	lookalike := &jiraClient{http: newJiraHTTPClient(), baseURL: "https://evil.atlassian.net", enforceHost: true, expectedHost: "acme.atlassian.net"}
	if _, err := lookalike.urlFor("/rest/api/3/search/jql"); !errors.Is(err, errJiraSSRF) {
		t.Errorf("lookalike urlFor err = %v, want errJiraSSRF", err)
	}
	// Plain HTTP is rejected (scheme must be https).
	plainHTTP := &jiraClient{http: newJiraHTTPClient(), baseURL: "http://acme.atlassian.net", enforceHost: true, expectedHost: "acme.atlassian.net"}
	if _, err := plainHTTP.urlFor("/x"); !errors.Is(err, errJiraSSRF) {
		t.Errorf("http urlFor err = %v, want errJiraSSRF", err)
	}

	// A hostile nextPageToken cannot steer the follow-up request
	// off-host: tokens are opaque cursors, never URLs.
	stub, srv := newJiraStub(t, "nobody@example.com")
	stub.searchHandler = func(w http.ResponseWriter, r *http.Request) {
		stub.record(r)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"issues":[],"nextPageToken":"https://evil.example/steal","isLast":false}`)
	}
	client := newJiraTestClient("bot@example.com", "tok", srv.URL)
	in := jiraTestInput()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, _, wsSlug, ident, _ := jiraImportFixture(t, pool)
	if _, err := ImportJiraIssuesWithClient(ctx, pool, client, wsSlug, ident, actorID, in); err == nil {
		t.Fatal("expected an error for a token-shaped nextPageToken loop")
	} else {
		// The run must not have followed the token anywhere: only the
		// search endpoint was hit.
		stub.mu.Lock()
		defer stub.mu.Unlock()
		for _, h := range stub.seenHosts {
			if strings.Contains(h, "evil") {
				t.Errorf("request reached hostile host %q", h)
			}
		}
	}
}

// ---------- preview ----------

// TestPreviewJiraImport exercises the preview: two pages fetched via
// nextPageToken, status→state matching (case-insensitive), unmatched
// status flagged as new, labels/new-labels, and comment counts without
// fetching comment bodies.
func TestPreviewJiraImport(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, memberEmail, wsSlug, ident, projectID := jiraImportFixture(t, pool)

	stub, srv := newJiraStub(t, memberEmail)
	in := jiraTestInput()
	client := jiraTestClient(t, stub, srv, in)

	prev, err := PreviewJiraImportWithClient(ctx, pool, client, wsSlug, ident, actorID, in)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if prev.Source != "acme/PROJ" {
		t.Errorf("source = %q, want acme/PROJ", prev.Source)
	}
	if prev.Total != 4 {
		t.Errorf("total = %d, want 4 (both pages)", prev.Total)
	}
	if len(prev.Rows) != 4 {
		t.Fatalf("rows = %d, want 4", len(prev.Rows))
	}
	byKey := map[string]JiraImportPreviewRow{}
	for _, r := range prev.Rows {
		byKey[r.Key] = r
	}
	// "To Do" matches the seeded "Todo" state... by NAME it does NOT
	// ("to do" != "todo"): it must be flagged as a new state.
	if r := byKey["PROJ-1"]; !r.StateIsNew {
		t.Errorf("PROJ-1: state_is_new = false, want true (\"To Do\" != \"Todo\")")
	}
	// "In Progress" matches the seeded state case-insensitively.
	if r := byKey["PROJ-2"]; r.StateIsNew || r.State != "In Progress" {
		t.Errorf("PROJ-2: state = %q is_new = %v, want In Progress/false", r.State, r.StateIsNew)
	}
	// "done" (lowercase) matches the seeded "Done" state.
	if r := byKey["PROJ-3"]; r.StateIsNew || r.State != "Done" {
		t.Errorf("PROJ-3: state = %q is_new = %v, want Done/false", r.State, r.StateIsNew)
	}
	// Unmatched "In Review" is flagged new.
	if r := byKey["PROJ-4"]; !r.StateIsNew || r.State != "In Review" {
		t.Errorf("PROJ-4: state = %q is_new = %v, want In Review/true", r.State, r.StateIsNew)
	}
	// Labels: "backend" is new to the workspace.
	if r := byKey["PROJ-1"]; len(r.Labels) != 1 || len(r.NewLabels) != 1 || r.NewLabels[0] != "backend" {
		t.Errorf("PROJ-1 labels = %v new = %v, want [backend]/[backend]", r.Labels, r.NewLabels)
	}
	// Assignee matched for PROJ-1 (member email), missed for PROJ-2.
	if r := byKey["PROJ-1"]; !r.AssigneeMatched {
		t.Errorf("PROJ-1: assignee_matched = false, want true")
	}
	if r := byKey["PROJ-2"]; r.AssigneeMatched {
		t.Errorf("PROJ-2: assignee_matched = true, want false")
	}
	// Comments are counted, not fetched, at preview time.
	if r := byKey["PROJ-1"]; r.Comments != 2 {
		t.Errorf("PROJ-1 comments = %d, want 2", r.Comments)
	}
	// Preview writes nothing: no issues, no states, no labels — scoped to
	// this test's project (the DB is never truncated).
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM issues WHERE project_id = $1::uuid`, projectID).Scan(&n); err != nil || n != 0 {
		t.Errorf("preview wrote %d issues", n)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM states WHERE project_id = $1::uuid AND name IN ('To Do','In Review')`, projectID).Scan(&n); err != nil || n != 0 {
		t.Errorf("preview created %d states", n)
	}

	stub.authOK(t, in.Email, in.APIToken)
}

// ---------- end-to-end import ----------

// TestImportJiraIssuesEndToEnd is the full pipeline: token pagination,
// ADF description extraction, comment import (paginated) with
// attribution, label creation, priority mapping, assignee match + miss,
// unmatched-status state creation in the unstarted group, and rerun
// dedupe via the footer marker.
func TestImportJiraIssuesEndToEnd(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, memberEmail, wsSlug, ident, projectID := jiraImportFixture(t, pool)

	stub, srv := newJiraStub(t, memberEmail)
	in := jiraTestInput()
	client := jiraTestClient(t, stub, srv, in)

	res, err := ImportJiraIssuesWithClient(ctx, pool, client, wsSlug, ident, actorID, in)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Source != "acme/PROJ" {
		t.Errorf("source = %q, want acme/PROJ", res.Source)
	}
	if res.Created != 4 {
		t.Errorf("created = %d, want 4", res.Created)
	}
	if res.Skipped != 0 {
		t.Errorf("skipped = %d, want 0", res.Skipped)
	}
	if len(res.LabelsCreated) != 2 { // backend, docs
		t.Errorf("labels_created = %v, want 2", res.LabelsCreated)
	}
	if len(res.StatesCreated) != 2 { // "To Do", "In Review"
		t.Errorf("states_created = %v, want 2 (To Do, In Review)", res.StatesCreated)
	}
	if res.AssigneeMisses != 1 { // ghost@example.com
		t.Errorf("assignee_misses = %d, want 1", res.AssigneeMisses)
	}

	// New states land in the unstarted group (documented: glance has no
	// "Default" group; unstarted is the neutral tracked group).
	var group string
	if err := pool.QueryRow(ctx,
		`SELECT "group" FROM states WHERE project_id = $1::uuid AND name = 'In Review'`, projectID).Scan(&group); err != nil {
		t.Fatalf("In Review state: %v", err)
	} else if group != "unstarted" {
		t.Errorf("In Review group = %q, want unstarted", group)
	}

	// PROJ-1: description is ADF-extracted text + footer marker with the
	// browse link; priority High → 3; assignee matched the member.
	var desc, stateName string
	var priority int
	var assigneeID *string
	if err := pool.QueryRow(ctx, `
		SELECT i.description::text, s.name, i.priority, a.user_id::text
		FROM issues i JOIN states s ON s.id = i.state_id
		LEFT JOIN issue_assignees a ON a.issue_id = i.id
		WHERE i.project_id = $1::uuid AND i.name = 'First bug'`, projectID).Scan(&desc, &stateName, &priority, &assigneeID); err != nil {
		t.Fatalf("PROJ-1 row: %v", err)
	}
	if !strings.Contains(desc, "the body") {
		t.Errorf("PROJ-1 description missing ADF text: %q", desc)
	}
	if !strings.Contains(desc, "*Imported from [PROJ-1](https://acme.atlassian.net/browse/PROJ-1)*") {
		t.Errorf("PROJ-1 missing footer marker: %q", desc)
	}
	if priority != 3 {
		t.Errorf("PROJ-1 priority = %d, want 3 (High)", priority)
	}
	if assigneeID == nil {
		t.Error("PROJ-1: assignee not matched to the member")
	}
	// Lowest → 1 (low): glance has no "lowest".
	if err := pool.QueryRow(ctx, `SELECT priority FROM issues WHERE project_id = $1::uuid AND name = 'Shipped it'`, projectID).Scan(&priority); err != nil {
		t.Fatalf("PROJ-3 priority: %v", err)
	} else if priority != 1 {
		t.Errorf("PROJ-3 priority = %d, want 1 (Lowest→low)", priority)
	}

	// Comments: PROJ-1 has 2 (paginated 1-per-page), PROJ-4 has 1, all
	// attributed with original timestamps.
	var comments int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM comments c JOIN issues i ON i.id = c.issue_id WHERE i.project_id = $1::uuid AND i.name = 'First bug'`, projectID).Scan(&comments); err != nil {
		t.Fatalf("comment count: %v", err)
	} else if comments != 2 {
		t.Errorf("PROJ-1 comments = %d, want 2", comments)
	}
	var content string
	var created string
	if err := pool.QueryRow(ctx, `
		SELECT c.content::text, c.created_at::text FROM comments c
		JOIN issues i ON i.id = c.issue_id
		WHERE i.project_id = $1::uuid AND i.name = 'First bug' ORDER BY c.created_at LIMIT 1`, projectID).Scan(&content, &created); err != nil {
		t.Fatalf("comment row: %v", err)
	}
	if !strings.Contains(content, "originally posted by Ada") {
		t.Errorf("comment missing attribution: %q", content)
	}
	if !strings.Contains(created, "2026-01-03") {
		t.Errorf("comment created_at = %q, want the original 2026-01-03", created)
	}

	// Rerun: everything is skipped via the footer marker (dedupe), no
	// duplicate states or labels are created.
	res2, err := ImportJiraIssuesWithClient(ctx, pool, client, wsSlug, ident, actorID, in)
	if err != nil {
		t.Fatalf("rerun: %v", err)
	}
	if res2.Created != 0 || res2.Skipped != 4 {
		t.Errorf("rerun: created=%d skipped=%d, want 0/4", res2.Created, res2.Skipped)
	}
	if len(res2.StatesCreated) != 0 || len(res2.LabelsCreated) != 0 {
		t.Errorf("rerun created states=%v labels=%v, want none", res2.StatesCreated, res2.LabelsCreated)
	}

	stub.authOK(t, in.Email, in.APIToken)
}

// TestJiraTokenNeverPersisted pins the token handling: Basic auth on the
// wire, never in the database, never in error messages.
func TestJiraTokenNeverPersisted(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, memberEmail, wsSlug, ident, _ := jiraImportFixture(t, pool)

	const token = "ATATT3x" + "DONOTSTORE_z9x8c7v6b5"
	stub, srv := newJiraStub(t, memberEmail)
	client := newJiraTestClient("bot@example.com", token, srv.URL)

	in := jiraTestInput()
	in.APIToken = token
	if _, err := ImportJiraIssuesWithClient(ctx, pool, client, wsSlug, ident, actorID, in); err != nil {
		t.Fatalf("import: %v", err)
	}
	assertTokenAbsentFromDB(t, pool, token)
	// The email is fine to persist (it identifies the assignee); the
	// token must not appear anywhere.
	stub.authOK(t, in.Email, token)

	// Failed run (401): the error must not echo the token either.
	stub.searchHandler = func(w http.ResponseWriter, r *http.Request) {
		stub.record(r)
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"errorMessages":["You do not have permission"],"errors":{}}`)
	}
	_, err := ImportJiraIssuesWithClient(ctx, pool, client, wsSlug, ident, actorID, in)
	if err == nil {
		t.Fatal("expected 401 error")
	}
	if !errors.Is(err, ErrJiraUnauthorized) {
		t.Errorf("err = %v, want ErrJiraUnauthorized", err)
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("401 error leaks the token: %q", err.Error())
	}
	assertTokenAbsentFromDB(t, pool, token)
}

// TestJiraImportErrorMapping pins upstream error mapping: 404 on a bad
// project key, 429 with Retry-After, and 5xx as a generic upstream
// error.
func TestJiraImportErrorMapping(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, _, wsSlug, ident, _ := jiraImportFixture(t, pool)
	in := jiraTestInput()

	cases := []struct {
		name    string
		status  int
		body    string
		headers map[string]string
		// wantRateLimit pins the typed rate-limit error (mirrors the
		// GitHub importer: 429 is never retried silently, the handler
		// surfaces Retry-After). wantErr pins the sentinel otherwise.
		wantRateLimit bool
		wantErr       error
	}{
		{"not found", 404, `{"errorMessages":["The issue key 'XX-1' is invalid."]}`, nil, false, ErrJiraNotFound},
		{"rate limited", 429, `{"errorMessages":["Rate limit exceeded"]}`, map[string]string{"Retry-After": "30"}, true, nil},
		{"server error", 500, `{"errorMessages":["boom"]}`, nil, false, ErrJiraUpstream},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub, srv := newJiraStub(t, "nobody@example.com")
			stub.searchHandler = func(w http.ResponseWriter, r *http.Request) {
				for k, v := range tc.headers {
					w.Header().Set(k, v)
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}
			client := newJiraTestClient(in.Email, in.APIToken, srv.URL)
			_, err := PreviewJiraImportWithClient(ctx, pool, client, wsSlug, ident, actorID, in)
			if tc.wantRateLimit {
				var rl *JiraRateLimitError
				if !errors.As(err, &rl) {
					t.Errorf("err = %v, want *JiraRateLimitError", err)
				} else if rl.RetryAfter != 30 {
					t.Errorf("retry_after = %d, want 30", rl.RetryAfter)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

var _ = pgxpool.Pool{}
