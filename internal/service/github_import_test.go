package service

// GitHub issues importer tests (C8T2). A stub httptest server emulates
// the GitHub REST v3 API: two paginated issue pages (Link headers), PR
// exclusion, label mapping + creation, comment import, assignee
// best-effort matching, the SSRF host guard, and error mapping.
// All tests run against the real test database — no skips.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// githubStub emulates the GitHub REST API. The happy path serves two
// paginated issue pages (Link headers), per-issue comments, and user
// profiles. seenAuth/seenPaths record what the client sent (used to pin
// the token's transport). Individual handlers can be overridden per test
// for error-mapping cases.
type githubStub struct {
	t *testing.T

	mu        sync.Mutex
	seenAuth  []string
	seenPaths []string

	// octocatEmail is the canned public email for /users/octocat; the
	// test sets it to a real workspace member's email (match case) or
	// leaves it empty (miss case).
	octocatEmail string

	issuesHandler   func(w http.ResponseWriter, r *http.Request)
	commentsHandler func(w http.ResponseWriter, r *http.Request)
	usersHandler    func(w http.ResponseWriter, r *http.Request)
}

func (s *githubStub) record(r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seenAuth = append(s.seenAuth, r.Header.Get("Authorization"))
	s.seenPaths = append(s.seenPaths, r.URL.RequestURI())
}

func (s *githubStub) authOK(t *testing.T) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.seenAuth {
		if a != "Bearer ghp_testsecretTOKEN123" {
			t.Errorf("Authorization header = %q, want %q", a, "Bearer ghp_testsecretTOKEN123")
		}
	}
	for _, p := range s.seenPaths {
		if strings.Contains(p, "ghp_testsecretTOKEN123") {
			t.Errorf("request path leaks token: %q", p)
		}
	}
}

const githubStubIssue1 = `{"number":1,"title":"First bug","body":"the body","state":"open",` +
	`"html_url":"https://github.com/acme/widgets/issues/1",` +
	`"labels":[{"name":"bug","color":"d73a4a"},{"name":"frontend","color":"a2eeef"}],` +
	`"assignees":[{"login":"octocat"},{"login":"ghost"}],` +
	`"milestone":null,"comments":2,` +
	`"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z"}`

const githubStubPR2 = `{"number":2,"title":"A PR","body":null,"state":"open",` +
	`"html_url":"https://github.com/acme/widgets/pull/2",` +
	`"labels":[],"assignees":[],"milestone":null,"comments":0,` +
	`"pull_request":{"url":"https://api.github.com/repos/acme/widgets/pulls/2"},` +
	`"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z"}`

const githubStubIssue3 = `{"number":3,"title":"Old crash","body":"crash body","state":"closed",` +
	`"html_url":"https://github.com/acme/widgets/issues/3",` +
	`"labels":[{"name":"bug","color":"d73a4a"}],` +
	`"assignees":[],` +
	`"milestone":{"title":"v1.0"},"comments":0,` +
	`"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z"}`

const githubStubIssue4 = `{"number":4,"title":"Docs typo","body":"","state":"open",` +
	`"html_url":"https://github.com/acme/widgets/issues/4",` +
	`"labels":[],"assignees":[],"milestone":null,"comments":0,` +
	`"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z"}`

func (s *githubStub) defaultIssues(w http.ResponseWriter, r *http.Request) {
	s.record(r)
	all := []string{githubStubIssue1, githubStubPR2, githubStubIssue3, githubStubIssue4}
	// Honor the state filter like the real API does.
	var filtered []string
	switch r.URL.Query().Get("state") {
	case "open":
		filtered = []string{githubStubIssue1, githubStubPR2, githubStubIssue4}
	case "closed":
		filtered = []string{githubStubIssue3}
	default: // "all" or anything else
		filtered = all
	}
	page := r.URL.Query().Get("page")
	var body string
	if page == "" || page == "1" {
		// Split the filtered list in half to exercise Link pagination in
		// every state-filter mode. The next URL preserves the query
		// string (state filter included) like GitHub's real Link headers.
		half := (len(filtered) + 1) / 2
		body = "[" + strings.Join(filtered[:half], ",") + "]"
		if len(filtered) > half {
			q := r.URL.Query()
			q.Set("page", "2")
			nextURL := &url.URL{Scheme: "http", Host: r.Host, Path: r.URL.Path, RawQuery: q.Encode()}
			lastURL := &url.URL{Scheme: "http", Host: r.Host, Path: r.URL.Path, RawQuery: q.Encode()}
			w.Header().Set("Link", fmt.Sprintf(`<%s>; rel="next", <%s>; rel="last"`, nextURL, lastURL))
		}
	} else {
		half := (len(filtered) + 1) / 2
		body = "[" + strings.Join(filtered[half:], ",") + "]"
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, body)
}

func (s *githubStub) defaultComments(w http.ResponseWriter, r *http.Request) {
	s.record(r)
	body := `[]`
	if strings.Contains(r.URL.Path, "/issues/1/comments") {
		body = `[
			{"user":{"login":"octocat"},"body":"repro steps here",` +
			`"html_url":"https://github.com/acme/widgets/issues/1#issuecomment-1",` +
			`"created_at":"2026-01-03T10:00:00Z"},
			{"user":{"login":"ghost"},"body":"confirmed",` +
			`"html_url":"https://github.com/acme/widgets/issues/1#issuecomment-2",` +
			`"created_at":"2026-01-04T10:00:00Z"}
		]`
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, body)
}

func (s *githubStub) defaultUsers(w http.ResponseWriter, r *http.Request) {
	s.record(r)
	w.Header().Set("Content-Type", "application/json")
	switch {
	case strings.HasSuffix(r.URL.Path, "/users/octocat"):
		email := "null"
		if s.octocatEmail != "" {
			email = `"` + s.octocatEmail + `"`
		}
		fmt.Fprintf(w, `{"login":"octocat","email":%s}`, email)
	case strings.HasSuffix(r.URL.Path, "/users/ghost"):
		fmt.Fprint(w, `{"login":"ghost","email":null}`)
	default:
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"Not Found"}`)
	}
}

func newGitHubStub(t *testing.T) (*githubStub, *httptest.Server) {
	t.Helper()
	s := &githubStub{t: t}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/acme/widgets/issues", func(w http.ResponseWriter, r *http.Request) {
		if s.issuesHandler != nil {
			s.issuesHandler(w, r)
			return
		}
		s.defaultIssues(w, r)
	})
	mux.HandleFunc("/repos/acme/widgets/issues/1/comments", func(w http.ResponseWriter, r *http.Request) {
		if s.commentsHandler != nil {
			s.commentsHandler(w, r)
			return
		}
		s.defaultComments(w, r)
	})
	mux.HandleFunc("/repos/acme/widgets/issues/3/comments", func(w http.ResponseWriter, r *http.Request) {
		if s.commentsHandler != nil {
			s.commentsHandler(w, r)
			return
		}
		s.defaultComments(w, r)
	})
	mux.HandleFunc("/repos/acme/widgets/issues/4/comments", func(w http.ResponseWriter, r *http.Request) {
		if s.commentsHandler != nil {
			s.commentsHandler(w, r)
			return
		}
		s.defaultComments(w, r)
	})
	mux.HandleFunc("/users/", func(w http.ResponseWriter, r *http.Request) {
		if s.usersHandler != nil {
			s.usersHandler(w, r)
			return
		}
		s.defaultUsers(w, r)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return s, srv
}

// githubImportFixture builds a workspace + project where the actor is a
// member, plus a second member whose email the stub can match.
func githubImportFixture(t *testing.T, pool *pgxpool.Pool) (actorID, memberID, memberEmail, wsSlug, ident string) {
	t.Helper()
	ctx := context.Background()
	actorID = createTestUser(t, pool, uniqueTestEmail("gh-import-actor"))
	memberEmail = uniqueTestEmail("octocat-member")
	memberID = createTestUser(t, pool, memberEmail)
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-gh"), actorID)
	if _, err := pool.Exec(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role)
		 VALUES ($1::uuid, $2::uuid, $3) ON CONFLICT DO NOTHING`,
		ws.ID, memberID, RoleMember); err != nil {
		t.Fatalf("add member: %v", err)
	}
	p := createTestProject(t, pool, ws.Slug, actorID, "Widgets", uniqueTestIdentifier())
	return actorID, memberID, memberEmail, ws.Slug, p.Identifier
}

const githubTestToken = "ghp_testsecretTOKEN123"

func githubTestInput() GitHubImportInput {
	return GitHubImportInput{
		Owner: "acme", Repo: "widgets", Token: githubTestToken, StateFilter: "all",
	}
}

// assertTokenAbsentFromDB fails if the token string appears in any
// imported content table.
func assertTokenAbsentFromDB(t *testing.T, pool *pgxpool.Pool, token string) {
	t.Helper()
	ctx := context.Background()
	queries := map[string]string{
		"issues.description":    `SELECT count(*) FROM issues WHERE description::text LIKE '%' || $1 || '%'`,
		"issues.name":           `SELECT count(*) FROM issues WHERE name LIKE '%' || $1 || '%'`,
		"comments.content":      `SELECT count(*) FROM comments WHERE content::text LIKE '%' || $1 || '%'`,
		"labels":                `SELECT count(*) FROM labels WHERE name LIKE '%' || $1 || '%'`,
		"issue_activities":      `SELECT count(*) FROM issue_activities WHERE new_value::text LIKE '%' || $1 || '%'`,
		"notification payloads": `SELECT count(*) FROM notifications WHERE title LIKE '%' || $1 || '%' OR payload::text LIKE '%' || $1 || '%'`,
	}
	for table, q := range queries {
		var n int
		if err := pool.QueryRow(ctx, q, token).Scan(&n); err != nil {
			t.Fatalf("scan %s: %v", table, err)
		}
		if n != 0 {
			t.Errorf("token found in %s (%d rows) — the PAT must never be persisted", table, n)
		}
	}
}

// TestImportGitHubIssuesEndToEnd is the full pipeline: 2-page fetch, PR
// exclusion, label creation with GitHub colors, comment import with
// attribution, assignee match + miss, milestone skip, closed→Done state,
// and rerun dedupe.
func TestImportGitHubIssuesEndToEnd(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, memberID, memberEmail, wsSlug, ident := githubImportFixture(t, pool)

	stub, srv := newGitHubStub(t)
	stub.octocatEmail = memberEmail
	client := newGitHubTestClient(githubTestToken, srv.URL)

	res, err := ImportGitHubIssuesWithClient(ctx, pool, client, wsSlug, ident, actorID, githubTestInput())
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	stub.authOK(t)

	if res.Created != 3 {
		t.Errorf("created = %d, want 3 (issues 1, 3, 4)", res.Created)
	}
	if res.PRsSkipped != 1 {
		t.Errorf("prs_skipped = %d, want 1", res.PRsSkipped)
	}
	if res.Skipped != 0 {
		t.Errorf("skipped = %d, want 0 on first run", res.Skipped)
	}
	if res.MilestonesSkipped != 1 {
		t.Errorf("milestones_skipped = %d, want 1", res.MilestonesSkipped)
	}
	if res.AssigneeMisses != 1 {
		t.Errorf("assignee_misses = %d, want 1 (ghost has a private email)", res.AssigneeMisses)
	}
	if len(res.LabelsCreated) != 2 {
		t.Errorf("labels_created = %v, want [bug frontend]", res.LabelsCreated)
	}
	if res.Failed != 0 || len(res.Errors) != 0 {
		t.Errorf("failed = %d errors = %v, want clean run", res.Failed, res.Errors)
	}

	// Label colors came from GitHub.
	var bugColor string
	if err := pool.QueryRow(ctx,
		`SELECT color FROM labels l JOIN workspaces w ON w.id = l.workspace_id
		  WHERE w.slug = $1 AND l.name = 'bug'`, wsSlug).Scan(&bugColor); err != nil {
		t.Fatalf("read bug label: %v", err)
	}
	if bugColor != "#d73a4a" {
		t.Errorf("bug label color = %q, want #d73a4a (from GitHub)", bugColor)
	}

	// Issue #1: assignee matched, comments imported, marker present.
	var issue1ID, state1, desc1 string
	if err := pool.QueryRow(ctx,
		`SELECT i.id::text, s.name, i.description::text FROM issues i
		  JOIN states s ON s.id = i.state_id
		  JOIN projects p ON p.id = i.project_id
		 WHERE p.identifier = $1 AND i.name = 'First bug' AND i.deleted_at IS NULL`,
		ident).Scan(&issue1ID, &state1, &desc1); err != nil {
		t.Fatalf("read issue 1: %v", err)
	}
	if state1 != "Backlog" {
		t.Errorf("open issue state = %q, want Backlog", state1)
	}
	if !strings.Contains(desc1, "](https://github.com/acme/widgets/issues/1)") {
		t.Errorf("issue 1 description missing dedupe marker: %q", desc1)
	}
	var assignee string
	if err := pool.QueryRow(ctx,
		`SELECT user_id::text FROM issue_assignees WHERE issue_id = $1::uuid`, issue1ID).Scan(&assignee); err != nil {
		t.Fatalf("read assignee: %v", err)
	}
	if assignee != memberID {
		t.Errorf("assignee = %s, want member %s (octocat's public email matched)", assignee, memberID)
	}
	var commentCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM comments WHERE issue_id = $1::uuid AND deleted_at IS NULL`, issue1ID).Scan(&commentCount); err != nil {
		t.Fatalf("count comments: %v", err)
	}
	if commentCount != 2 {
		t.Errorf("comments = %d, want 2", commentCount)
	}
	var cbody string
	var ccreated time.Time
	if err := pool.QueryRow(ctx,
		`SELECT content::text, created_at FROM comments
		  WHERE issue_id = $1::uuid AND deleted_at IS NULL
		  ORDER BY created_at LIMIT 1`, issue1ID).Scan(&cbody, &ccreated); err != nil {
		t.Fatalf("read comment: %v", err)
	}
	if !strings.Contains(cbody, "repro steps here") || !strings.Contains(cbody, "originally posted by @octocat") {
		t.Errorf("comment missing body or attribution: %q", cbody)
	}
	if want := time.Date(2026, 1, 3, 10, 0, 0, 0, time.UTC); !ccreated.Equal(want) {
		t.Errorf("comment created_at = %v, want %v (original timestamp preserved)", ccreated, want)
	}

	// Issue #3 (closed): landed in the completed-group state.
	var state3 string
	if err := pool.QueryRow(ctx,
		`SELECT s.name FROM issues i JOIN states s ON s.id = i.state_id
		  JOIN projects p ON p.id = i.project_id
		 WHERE p.identifier = $1 AND i.name = 'Old crash' AND i.deleted_at IS NULL`,
		ident).Scan(&state3); err != nil {
		t.Fatalf("read issue 3: %v", err)
	}
	if state3 != "Done" {
		t.Errorf("closed issue state = %q, want Done", state3)
	}

	// The token must not be persisted anywhere.
	assertTokenAbsentFromDB(t, pool, githubTestToken)

	// Rerun: everything is deduped.
	res2, err := ImportGitHubIssuesWithClient(ctx, pool, client, wsSlug, ident, actorID, githubTestInput())
	if err != nil {
		t.Fatalf("rerun import: %v", err)
	}
	if res2.Created != 0 {
		t.Errorf("rerun created = %d, want 0", res2.Created)
	}
	if res2.Skipped != 3 {
		t.Errorf("rerun skipped = %d, want 3", res2.Skipped)
	}
	assertTokenAbsentFromDB(t, pool, githubTestToken)
}

// TestPreviewGitHubImport pins the preview shape: totals, first-10 rows,
// would-create labels, assignee matching, comment counts, milestones —
// and that preview writes nothing.
func TestPreviewGitHubImport(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, _, memberEmail, wsSlug, ident := githubImportFixture(t, pool)

	stub, srv := newGitHubStub(t)
	stub.octocatEmail = memberEmail
	client := newGitHubTestClient(githubTestToken, srv.URL)

	prev, err := PreviewGitHubImportWithClient(ctx, pool, client, wsSlug, ident, actorID, githubTestInput())
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	stub.authOK(t)
	if prev.Source != "acme/widgets" {
		t.Errorf("source = %q, want acme/widgets", prev.Source)
	}
	if prev.Total != 3 {
		t.Errorf("total = %d, want 3 (PR excluded)", prev.Total)
	}
	if len(prev.Rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(prev.Rows))
	}
	row1 := prev.Rows[0]
	if row1.Number != 1 || row1.Title != "First bug" || row1.State != "open" {
		t.Errorf("row1 = %+v, want #1 First bug open", row1)
	}
	if len(row1.NewLabels) != 2 {
		t.Errorf("row1 new_labels = %v, want [bug frontend]", row1.NewLabels)
	}
	if len(row1.Assignees) != 2 || !row1.AssigneeMatched {
		t.Errorf("row1 assignees = %v matched = %v, want [octocat ghost] true", row1.Assignees, row1.AssigneeMatched)
	}
	if row1.Comments != 2 {
		t.Errorf("row1 comments = %d, want 2", row1.Comments)
	}
	if row1.AlreadyImported {
		t.Errorf("row1 already_imported = true before any import")
	}
	// Closed issue with a milestone.
	var row3 *GitHubImportPreviewRow
	for i := range prev.Rows {
		if prev.Rows[i].Number == 3 {
			row3 = &prev.Rows[i]
		}
	}
	if row3 == nil {
		t.Fatalf("no preview row for issue #3")
	}
	if row3.Milestone == nil || *row3.Milestone != "v1.0" {
		t.Errorf("row3 milestone = %v, want v1.0", row3.Milestone)
	}

	// Preview wrote nothing (scoped to this test's project — the test
	// database is shared with the rest of the package's tests).
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM issues i JOIN projects p ON p.id = i.project_id
		  WHERE p.identifier = $1`, ident).Scan(&n); err != nil {
		t.Fatalf("count issues: %v", err)
	}
	if n != 0 {
		t.Errorf("preview created %d issues, want 0", n)
	}
	assertTokenAbsentFromDB(t, pool, githubTestToken)
}

// TestGitHubStateFilter exercises the state filter end-to-end: "open"
// must not pull the closed issue.
func TestGitHubStateFilter(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, _, _, wsSlug, ident := githubImportFixture(t, pool)

	_, srv := newGitHubStub(t)
	client := newGitHubTestClient(githubTestToken, srv.URL)

	in := githubTestInput()
	in.StateFilter = "open"
	res, err := ImportGitHubIssuesWithClient(ctx, pool, client, wsSlug, ident, actorID, in)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	// open issues 1 + 4 (PR #2 excluded), closed #3 not fetched.
	if res.Created != 2 {
		t.Errorf("created = %d, want 2 (open only)", res.Created)
	}
	if res.PRsSkipped != 1 {
		t.Errorf("prs_skipped = %d, want 1", res.PRsSkipped)
	}
}

// TestGitHubInputValidation pins the input gate: bad owner/repo shapes
// (including path traversal) and bad state filters are 400-class errors,
// never a GitHub request.
func TestGitHubInputValidation(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, _, _, wsSlug, ident := githubImportFixture(t, pool)

	_, srv := newGitHubStub(t)
	client := newGitHubTestClient(githubTestToken, srv.URL)

	bad := []GitHubImportInput{
		{Owner: "", Repo: "widgets"},
		{Owner: "acme", Repo: ""},
		{Owner: "ac/me", Repo: "widgets"}, // path traversal via slash
		{Owner: "..", Repo: "widgets"},    // dot-dot
		{Owner: "acme", Repo: "wid gets"}, // space
		{Owner: "acme", Repo: "widgets", StateFilter: "bogus"},
		{Owner: strings.Repeat("a", 101), Repo: "widgets"}, // too long
	}
	for i, in := range bad {
		in.Token = githubTestToken
		_, err := PreviewGitHubImportWithClient(ctx, pool, client, wsSlug, ident, actorID, in)
		if !errors.Is(err, ErrGitHubBadInput) {
			t.Errorf("case %d (%+v): err = %v, want ErrGitHubBadInput", i, in, err)
		}
	}

	// Max clamping: negative → default; huge → capped. (Observed via the
	// stub's seen paths: per_page is fixed, pages stop at max.)
	in := githubTestInput()
	in.Max = -5
	if _, _, err := in.validate(); err != nil {
		t.Fatalf("validate negative max: %v", err)
	} else {
		_, max, _ := in.validate()
		if max != githubDefaultMax {
			t.Errorf("max = %d, want default %d", max, githubDefaultMax)
		}
	}
	in.Max = 5000
	if _, max, err := in.validate(); err != nil || max != githubAbsoluteMax {
		t.Errorf("validate huge max: max = %d err = %v, want %d", max, err, githubAbsoluteMax)
	}
}

// TestGitHubMemberGate pins the role gate: non-members get ErrNotFound
// (tenancy boundary), guests get ErrForbidden — same as the CSV importer.
func TestGitHubMemberGate(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, _, _, wsSlug, ident := githubImportFixture(t, pool)

	_, srv := newGitHubStub(t)
	client := newGitHubTestClient(githubTestToken, srv.URL)

	outsider := createTestUser(t, pool, uniqueTestEmail("gh-outsider"))
	if _, err := PreviewGitHubImportWithClient(ctx, pool, client, wsSlug, ident, outsider, githubTestInput()); !errors.Is(err, ErrNotFound) {
		t.Errorf("outsider preview err = %v, want ErrNotFound", err)
	}

	guest := createTestUser(t, pool, uniqueTestEmail("gh-guest"))
	if _, err := pool.Exec(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role)
		 SELECT w.id, $1::uuid, $2 FROM workspaces w WHERE w.slug = $3
		 ON CONFLICT DO NOTHING`, guest, RoleGuest, wsSlug); err != nil {
		t.Fatalf("add guest: %v", err)
	}
	_ = actorID
	if _, err := ImportGitHubIssuesWithClient(ctx, pool, client, wsSlug, ident, guest, githubTestInput()); !errors.Is(err, ErrForbidden) {
		t.Errorf("guest import err = %v, want ErrForbidden", err)
	}
}

// TestGitHubSSRFGuard pins the fixed-host rule: the production client
// only ever builds api.github.com URLs; anything else is rejected; and a
// hostile Link header cannot redirect the token elsewhere.
func TestGitHubSSRFGuard(t *testing.T) {
	// Production client builds the fixed host.
	c := newGitHubClient("tok")
	raw, err := c.urlFor("/repos/o/r/issues?state=open")
	if err != nil {
		t.Fatalf("urlFor: %v", err)
	}
	if raw != "https://api.github.com/repos/o/r/issues?state=open" {
		t.Errorf("urlFor = %q, want the fixed api.github.com URL", raw)
	}

	// A non-api.github.com base with enforcement on is rejected.
	hostile := &githubClient{http: newGitHubHTTPClient(), baseURL: "https://evil.example", enforceHost: true}
	if _, err := hostile.urlFor("/repos/o/r/issues"); !errors.Is(err, errGitHubSSRF) {
		t.Errorf("hostile urlFor err = %v, want errGitHubSSRF", err)
	}
	// Plain-HTTP api.github.com is also rejected (scheme must be https).
	plainHTTP := &githubClient{http: newGitHubHTTPClient(), baseURL: "http://api.github.com", enforceHost: true}
	if _, err := plainHTTP.urlFor("/x"); !errors.Is(err, errGitHubSSRF) {
		t.Errorf("http urlFor err = %v, want errGitHubSSRF", err)
	}

	// A Link header pointing off-host aborts the fetch before any
	// follow-up request carries the token there.
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, _, _, wsSlug, ident := githubImportFixture(t, pool)

	stub, srv := newGitHubStub(t)
	stub.issuesHandler = func(w http.ResponseWriter, r *http.Request) {
		stub.record(r)
		w.Header().Set("Link", `<https://evil.example/next>; rel="next"`)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[]`)
	}
	client := newGitHubTestClient(githubTestToken, srv.URL)
	_, err = ImportGitHubIssuesWithClient(ctx, pool, client, wsSlug, ident, actorID, githubTestInput())
	if !errors.Is(err, errGitHubSSRF) {
		t.Errorf("evil Link header: err = %v, want errGitHubSSRF", err)
	}
	stub.mu.Lock()
	n := len(stub.seenPaths)
	stub.mu.Unlock()
	if n != 1 {
		t.Errorf("evil Link header: %d requests made, want 1 (never follow off-host)", n)
	}
}

// TestGitHubErrorMapping pins honest upstream errors: 404 surfaces
// GitHub's message, 401 is unauthorized, rate-limit exhaustion becomes a
// typed 429 error, and the token never appears in any error string.
func TestGitHubErrorMapping(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, _, _, wsSlug, ident := githubImportFixture(t, pool)

	const errToken = "SECRET_ERR_TOKEN_999"
	cases := []struct {
		name    string
		handler func(s *githubStub) func(http.ResponseWriter, *http.Request)
		check   func(t *testing.T, err error)
	}{
		{
			name: "repo not found surfaces github message",
			handler: func(s *githubStub) func(http.ResponseWriter, *http.Request) {
				return func(w http.ResponseWriter, r *http.Request) {
					s.record(r)
					w.WriteHeader(http.StatusNotFound)
					fmt.Fprint(w, `{"message":"Not Found"}`)
				}
			},
			check: func(t *testing.T, err error) {
				t.Helper()
				if !errors.Is(err, ErrGitHubRepoNotFound) {
					t.Errorf("err = %v, want ErrGitHubRepoNotFound", err)
				}
				if !strings.Contains(err.Error(), "Not Found") {
					t.Errorf("err = %q, want GitHub's message surfaced", err.Error())
				}
			},
		},
		{
			name: "bad token is unauthorized",
			handler: func(s *githubStub) func(http.ResponseWriter, *http.Request) {
				return func(w http.ResponseWriter, r *http.Request) {
					s.record(r)
					w.WriteHeader(http.StatusUnauthorized)
					fmt.Fprint(w, `{"message":"Bad credentials"}`)
				}
			},
			check: func(t *testing.T, err error) {
				t.Helper()
				if !errors.Is(err, ErrGitHubUnauthorized) {
					t.Errorf("err = %v, want ErrGitHubUnauthorized", err)
				}
			},
		},
		{
			name: "rate limit exhaustion is typed with retry-after",
			handler: func(s *githubStub) func(http.ResponseWriter, *http.Request) {
				return func(w http.ResponseWriter, r *http.Request) {
					s.record(r)
					w.Header().Set("X-RateLimit-Remaining", "0")
					w.Header().Set("X-RateLimit-Reset", fmt.Sprint(time.Now().Unix()+120))
					w.WriteHeader(http.StatusForbidden)
					fmt.Fprint(w, `{"message":"API rate limit exceeded"}`)
				}
			},
			check: func(t *testing.T, err error) {
				t.Helper()
				var rl *GitHubRateLimitError
				if !errors.As(err, &rl) {
					t.Fatalf("err = %v, want *GitHubRateLimitError", err)
				}
				if rl.RetryAfter <= 0 || rl.RetryAfter > 180 {
					t.Errorf("retry-after = %d, want ~120s from X-RateLimit-Reset", rl.RetryAfter)
				}
			},
		},
		{
			name: "upstream 500 is a 502-class error",
			handler: func(s *githubStub) func(http.ResponseWriter, *http.Request) {
				return func(w http.ResponseWriter, r *http.Request) {
					s.record(r)
					w.WriteHeader(http.StatusInternalServerError)
					fmt.Fprint(w, `{"message":"boom"}`)
				}
			},
			check: func(t *testing.T, err error) {
				t.Helper()
				if !errors.Is(err, ErrGitHubUpstream) {
					t.Errorf("err = %v, want ErrGitHubUpstream", err)
				}
				if !strings.Contains(err.Error(), "boom") {
					t.Errorf("err = %q, want upstream message surfaced", err.Error())
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub, srv := newGitHubStub(t)
			stub.issuesHandler = tc.handler(stub)
			client := newGitHubTestClient(errToken, srv.URL)
			in := githubTestInput()
			in.Token = errToken
			_, err := ImportGitHubIssuesWithClient(ctx, pool, client, wsSlug, ident, actorID, in)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			tc.check(t, err)
			if strings.Contains(err.Error(), errToken) {
				t.Errorf("error string leaks the token: %q", err.Error())
			}
		})
	}
}

// TestGitHubTokenNeverPersisted is the dedicated token-hygiene pin: after
// a full import AND after failed runs, the PAT appears nowhere in the
// database and in no error string.
func TestGitHubTokenNeverPersisted(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, _, memberEmail, wsSlug, ident := githubImportFixture(t, pool)

	const token = "ghp_DONOTSTORE_" + "z9x8c7v6b5"
	stub, srv := newGitHubStub(t)
	stub.octocatEmail = memberEmail
	client := newGitHubTestClient(token, srv.URL)

	in := githubTestInput()
	in.Token = token
	if _, err := ImportGitHubIssuesWithClient(ctx, pool, client, wsSlug, ident, actorID, in); err != nil {
		t.Fatalf("import: %v", err)
	}
	assertTokenAbsentFromDB(t, pool, token)

	// Failed run (401): the error must not echo the token either.
	stub.issuesHandler = func(w http.ResponseWriter, r *http.Request) {
		stub.record(r)
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"message":"Bad credentials"}`)
	}
	_, err := ImportGitHubIssuesWithClient(ctx, pool, client, wsSlug, ident, actorID, in)
	if err == nil {
		t.Fatalf("expected 401 error")
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("401 error leaks the token: %q", err.Error())
	}
	assertTokenAbsentFromDB(t, pool, token)
}

// TestGitHubImportRespectsContextCancellation pins that the fetch
// pipeline honors context cancellation (the production wrappers add a
// run-level deadline on top of the per-request timeout).
func TestGitHubImportRespectsContextCancellation(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	actorID, _, _, wsSlug, ident := githubImportFixture(t, pool)

	_, srv := newGitHubStub(t)
	client := newGitHubTestClient(githubTestToken, srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already dead before the first request
	_, err := ImportGitHubIssuesWithClient(ctx, pool, client, wsSlug, ident, actorID, githubTestInput())
	if err == nil {
		t.Fatalf("expected context-cancellation error, got nil")
	}
	// The cancellation can surface at the DB lookup (context.Canceled) or
	// at the first GitHub request (wrapped ErrGitHubUpstream) — either
	// way the run must abort, never proceed.
	if !errors.Is(err, context.Canceled) && !errors.Is(err, ErrGitHubUpstream) {
		t.Errorf("err = %v, want context.Canceled or ErrGitHubUpstream", err)
	}
}

// TestParseImportedGitHubNumbers pins the dedupe needle: the closing
// paren disambiguates #12 from #123, and non-marker text never matches.
func TestParseImportedGitHubNumbers(t *testing.T) {
	descs := []string{
		`"body text\n\n---\n*Imported from [acme/widgets#12](https://github.com/acme/widgets/issues/12)*"`,
		`"another\n\n---\n*Imported from [acme/widgets#123](https://github.com/acme/widgets/issues/123)*"`,
		`"mentions https://github.com/acme/widgets/issues/12 in prose (no paren-marker)"`,
		`"other repo [o/r#7](https://github.com/other/repo/issues/7)"`,
	}
	got := parseImportedGitHubNumbers(descs, "acme", "widgets")
	if !got[12] || !got[123] {
		t.Errorf("got = %v, want 12 and 123", got)
	}
	if got[7] {
		t.Errorf("got[7] = true, want false (different owner/repo)")
	}
	if len(got) != 2 {
		t.Errorf("got = %v, want exactly {12, 123}", got)
	}
}

// TestGitHubLabelColor pins the color sanitizer: valid GitHub hex passes
// through, anything else falls back to the default gray.
func TestGitHubLabelColor(t *testing.T) {
	if got := githubLabelColor("d73a4a"); got != "#d73a4a" {
		t.Errorf("githubLabelColor(d73a4a) = %q", got)
	}
	// Whitespace is trimmed before the hex check, so this is valid too.
	if got := githubLabelColor("d73a4a "); got != "#d73a4a" {
		t.Errorf("githubLabelColor(\"d73a4a \") = %q", got)
	}
	for _, bad := range []string{"", "red", "d73a4", "d73a4ag", "#d73a4a"} {
		if got := githubLabelColor(bad); got != "#6b7280" {
			t.Errorf("githubLabelColor(%q) = %q, want #6b7280", bad, got)
		}
	}
}

// TestGitHubPreviewRowCap: more than 10 issues → preview shows 10 but
// reports the real total. (Covers the preview/display split.)
func TestGitHubPreviewRowCap(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, _, _, wsSlug, ident := githubImportFixture(t, pool)

	stub, srv := newGitHubStub(t)
	stub.issuesHandler = func(w http.ResponseWriter, r *http.Request) {
		stub.record(r)
		var items []string
		for i := 1; i <= 15; i++ {
			items = append(items, fmt.Sprintf(
				`{"number":%d,"title":"Issue %d","body":null,"state":"open",`+
					`"html_url":"https://github.com/acme/widgets/issues/%d",`+
					`"labels":[],"assignees":[],"milestone":null,"comments":0,`+
					`"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`,
				i, i, i))
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, "[%s]", strings.Join(items, ","))
	}
	client := newGitHubTestClient(githubTestToken, srv.URL)
	in := githubTestInput()
	in.StateFilter = "open"

	prev, err := PreviewGitHubImportWithClient(ctx, pool, client, wsSlug, ident, actorID, in)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if prev.Total != 15 {
		t.Errorf("total = %d, want 15", prev.Total)
	}
	if len(prev.Rows) != 10 {
		t.Errorf("rows = %d, want 10 (display cap)", len(prev.Rows))
	}
}
