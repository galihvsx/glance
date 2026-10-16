package service

// Linear importer tests (C31T0). A stub httptest server emulates the
// Linear GraphQL API: POST /graphql serving teams + team issues
// (after-cursor pagination). All tests run against the real test
// database — no skips.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------- stub server ----------

// linearStub emulates the Linear GraphQL API. The handler can be
// overridden per test for error-mapping cases.
type linearStub struct {
	mu sync.Mutex
	// seenAuth records the Authorization header of every request so
	// tests can pin that the key travels as a Bearer header.
	seenAuth  []string
	seenHosts []string

	override func(w http.ResponseWriter, r *http.Request) bool
}

func (s *linearStub) record(r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seenAuth = append(s.seenAuth, r.Header.Get("Authorization"))
	s.seenHosts = append(s.seenHosts, r.Host)
}

// authOK pins the credential transport: the API key must travel as a
// Bearer Authorization header, never in the URL, the GraphQL query, or
// the variables.
func (s *linearStub) authOK(t *testing.T, apiKey string) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.seenAuth) == 0 {
		t.Fatal("no requests recorded")
	}
	for _, h := range s.seenAuth {
		if h != "Bearer "+apiKey {
			t.Errorf("Authorization = %q, want Bearer <key>", h)
		}
	}
}

// linearIssueJSON builds one canned issue payload for the stub.
func linearIssueJSON(identifier, title, desc, stateName, stateType string, priority int, labels []string, assigneeEmail, assigneeName, dueDate, url string, comments []map[string]any) map[string]any {
	labelNodes := []map[string]any{}
	for _, l := range labels {
		labelNodes = append(labelNodes, map[string]any{"name": l})
	}
	var due any
	if dueDate != "" {
		due = dueDate
	}
	var assignee any
	if assigneeEmail != "" || assigneeName != "" {
		assignee = map[string]any{"email": assigneeEmail, "name": assigneeName}
	}
	if comments == nil {
		comments = []map[string]any{}
	}
	return map[string]any{
		"id":          "id-" + identifier,
		"identifier":  identifier,
		"title":       title,
		"description": desc,
		"state":       map[string]any{"name": stateName, "type": stateType},
		"priority":    priority,
		"labels":      map[string]any{"nodes": labelNodes},
		"assignee":    assignee,
		"comments":    map[string]any{"nodes": comments},
		"createdAt":   "2026-01-01T10:00:00.000Z",
		"updatedAt":   "2026-01-02T10:00:00.000Z",
		"dueDate":     due,
		"url":         url,
	}
}

func linearCommentJSON(body, name, email, createdAt string) map[string]any {
	return map[string]any{
		"body":      body,
		"user":      map[string]any{"name": name, "email": email},
		"createdAt": createdAt,
	}
}

// linearStubIssues returns the canned issues: 4 issues served in two
// pages of 2 via the after cursor. ENG-12 (not ENG-1) is the dedupe
// needle's trap: a prefix match would confuse the two.
func linearStubIssues(memberEmail string) []map[string]any {
	return []map[string]any{
		linearIssueJSON("ENG-1", "Ship v2", "**bold** markdown", "Todo", "unstarted", 2,
			[]string{"bug"}, memberEmail, "Ada Member", "2026-02-01",
			"https://linear.app/acme/issue/ENG-1/ship-v2",
			[]map[string]any{linearCommentJSON("first comment", "Ada", memberEmail, "2026-01-03T10:00:00.000Z")}),
		linearIssueJSON("ENG-2", "Refactor", "", "In Progress", "started", 4,
			nil, "ghost@example.com", "Ghost", "",
			"https://linear.app/acme/issue/ENG-2/refactor", nil),
		linearIssueJSON("ENG-12", "Docs", "", "Done", "completed", 0,
			[]string{"feature"}, "", "", "2026-03-01",
			"https://linear.app/acme/issue/ENG-12/docs", nil),
		linearIssueJSON("ENG-13", "Kill switch", "", "Canceled", "canceled", 3,
			nil, "", "", "",
			"https://linear.app/acme/issue/ENG-13/kill-switch", nil),
	}
}

// newLinearStub builds the stub server with two teams (ENG, DES) and
// the canned ENG issues. A "team-missing" team id yields a null team
// (the "team not found" path).
func newLinearStub(t *testing.T, memberEmail string) (*linearStub, *httptest.Server) {
	t.Helper()
	s := &linearStub{}
	issues := linearStubIssues(memberEmail)
	mux := http.NewServeMux()
	mux.HandleFunc("/graphql", func(w http.ResponseWriter, r *http.Request) {
		if s.override != nil && s.override(w, r) {
			return
		}
		s.record(r)
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"errors":[{"message":"bad request"}]}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(req.Query, "teams") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"teams": map[string]any{"nodes": []map[string]any{
						{"id": "team-1", "key": "ENG", "name": "Engineering"},
						{"id": "team-2", "key": "DES", "name": "Design"},
					}},
				},
			})
			return
		}
		teamID, _ := req.Variables["teamId"].(string)
		if teamID == "team-missing" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"team": nil},
			})
			return
		}
		after, _ := req.Variables["after"].(string)
		var page []map[string]any
		var hasNext bool
		var cursor string
		switch after {
		case "":
			page, hasNext, cursor = issues[:2], true, "c1"
		case "c1":
			page, hasNext, cursor = issues[2:], false, ""
		default:
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `{"errors":[{"message":"bad after cursor %s"}]}`, after)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"team": map[string]any{
					"id": teamID,
					"issues": map[string]any{
						"nodes":    page,
						"pageInfo": map[string]any{"hasNextPage": hasNext, "endCursor": cursor},
					},
				},
			},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return s, srv
}

// linearImportFixture mirrors the GitHub/Jira/Trello fixture shape:
// actor (member) + second member whose email the stub's assignee
// matches.
func linearImportFixture(t *testing.T, pool *pgxpool.Pool) (actorID, memberEmail, wsSlug, ident, projectID string) {
	t.Helper()
	ctx := context.Background()
	actorID = createTestUser(t, pool, uniqueTestEmail("linear-import-actor"))
	memberEmail = uniqueTestEmail("linear-member")
	memberID := createTestUser(t, pool, memberEmail)
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-linear"), actorID)
	if _, err := pool.Exec(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role)
		 VALUES ($1::uuid, $2::uuid, $3) ON CONFLICT DO NOTHING`,
		ws.ID, memberID, RoleMember); err != nil {
		t.Fatalf("add member: %v", err)
	}
	p := createTestProject(t, pool, ws.Slug, actorID, "Widgets", uniqueTestIdentifier())
	return actorID, memberEmail, ws.Slug, p.Identifier, p.ID
}

func linearTestInput() LinearImportInput {
	return LinearImportInput{
		APIKey: "lin_api_testkey",
		TeamID: "team-1",
		Max:    100,
	}
}

func linearTestClient(t *testing.T, s *linearStub, srv *httptest.Server, in LinearImportInput) *linearClient {
	t.Helper()
	return newLinearTestClient(in.APIKey, srv.URL)
}

// ---------- validation ----------

// TestLinearImportInputValidate pins the input contract: api_key and
// team_id are required; max is clamped (default 100, hard cap 500 —
// the C31T0 task pins 500, tighter than Jira/Trello's 1000).
func TestLinearImportInputValidate(t *testing.T) {
	in := linearTestInput()
	if _, _, err := in.validate(); err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}
	bad := []LinearImportInput{
		{APIKey: "", TeamID: "team-1"},    // missing api key
		{APIKey: "k", TeamID: ""},         // missing team id
		{APIKey: "k", TeamID: "   "},      // blank team id
		{APIKey: "   ", TeamID: "team-1"}, // blank api key
	}
	for i, b := range bad {
		if _, _, err := b.validate(); !errors.Is(err, ErrLinearBadInput) {
			t.Errorf("bad input %d: err = %v, want ErrLinearBadInput", i, err)
		}
	}
	m := linearTestInput()
	m.Max = 0
	if _, max, err := m.validate(); err != nil || max != 100 {
		t.Errorf("max=0 → (%d,%v), want (100,nil)", max, err)
	}
	m.Max = 5000
	if _, max, err := m.validate(); err != nil || max != 500 {
		t.Errorf("max=5000 → (%d,%v), want (500,nil)", max, err)
	}
}

// ---------- SSRF guard ----------

// TestLinearSSRFGuard pins the fixed-host rule: the production client
// only ever builds https://api.linear.app/graphql URLs. A tampered
// base, plain HTTP, or any non-api.linear.app host is rejected. There
// is no user-controlled host anywhere: the team id travels as a
// GraphQL variable, never in a URL.
func TestLinearSSRFGuard(t *testing.T) {
	c := newLinearClient("k")
	raw, err := c.urlFor("/graphql")
	if err != nil {
		t.Fatalf("urlFor: %v", err)
	}
	if raw != "https://api.linear.app/graphql" {
		t.Errorf("urlFor = %q, want the pinned api.linear.app URL", raw)
	}
	hostile := &linearClient{http: newLinearHTTPClient(), baseURL: "https://evil.example", enforceHost: true}
	if _, err := hostile.urlFor("/graphql"); !errors.Is(err, errLinearSSRF) {
		t.Errorf("hostile urlFor err = %v, want errLinearSSRF", err)
	}
	lookalike := &linearClient{http: newLinearHTTPClient(), baseURL: "https://api.linear.app.evil.example", enforceHost: true}
	if _, err := lookalike.urlFor("/graphql"); !errors.Is(err, errLinearSSRF) {
		t.Errorf("lookalike urlFor err = %v, want errLinearSSRF", err)
	}
	plainHTTP := &linearClient{http: newLinearHTTPClient(), baseURL: "http://api.linear.app", enforceHost: true}
	if _, err := plainHTTP.urlFor("/graphql"); !errors.Is(err, errLinearSSRF) {
		t.Errorf("http urlFor err = %v, want errLinearSSRF", err)
	}
}

// ---------- mapping matrices ----------

// TestLinearStateGroupMatrix pins the state.type → glance group
// mapping, including canceled → cancelled (spelling differs) and the
// neutral unstarted fallback for unknown types.
func TestLinearStateGroupMatrix(t *testing.T) {
	cases := map[string]string{
		"backlog":   "backlog",
		"unstarted": "unstarted",
		"started":   "started",
		"completed": "completed",
		"canceled":  "cancelled",
		"CANCELED":  "cancelled",
		"triage":    "triage",
		"":          "unstarted",
		"whatever":  "unstarted",
	}
	for in, want := range cases {
		if got := linearStateGroup(in); got != want {
			t.Errorf("stateGroup(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestLinearPriorityMatrix pins the priority mapping: Linear's scale
// is inverted vs glance's (Linear 1=urgent … 4=low; glance 1=low …
// 4=urgent). Out-of-range values collapse to 0 (none).
func TestLinearPriorityMatrix(t *testing.T) {
	cases := map[int]int{0: 0, 1: 4, 2: 3, 3: 2, 4: 1, 5: 0, -1: 0, 99: 0}
	for in, want := range cases {
		if got := linearPriorityValue(in); got != want {
			t.Errorf("priority(%d) = %d, want %d", in, got, want)
		}
	}
}

// TestLinearDedupeNeedle pins the dedupe parser: the identifier is read
// out of the `*Imported from [IDENT](URL)` marker and the closing paren
// is required, so ENG-1 can never match ENG-12. A marker without a
// closing paren counts for nothing.
func TestLinearDedupeNeedle(t *testing.T) {
	descs := []string{
		"body\n\n---\n*Imported from [ENG-1](https://linear.app/acme/issue/ENG-1/ship-v2)*",
		"other\n\n---\n*Imported from [ENG-12](https://linear.app/acme/issue/ENG-12/docs)*",
		"broken\n\n---\n*Imported from [ENG-99](https://linear.app/acme/issue/ENG-99/no-close",
		"plain text with no marker at all",
		"*Imported from [ENG-1](https://linear.app/acme/issue/ENG-1/ship-v2)* and again *Imported from [ENG-1](https://linear.app/acme/issue/ENG-1/ship-v2)*",
	}
	got := parseImportedLinearIdentifiers(descs)
	if len(got) != 2 {
		t.Fatalf("identifiers = %v, want exactly {ENG-1, ENG-12}", got)
	}
	if !got["ENG-1"] || !got["ENG-12"] {
		t.Errorf("identifiers = %v, want ENG-1 and ENG-12", got)
	}
	if got["ENG-99"] {
		t.Errorf("unclosed marker counted ENG-99: %v", got)
	}
	// A description containing only the ENG-12 marker must not yield
	// ENG-1 (the prefix trap).
	only12 := parseImportedLinearIdentifiers([]string{
		"*Imported from [ENG-12](https://linear.app/acme/issue/ENG-12/docs)*",
	})
	if only12["ENG-1"] {
		t.Errorf("ENG-12 marker yielded ENG-1: %v", only12)
	}
}

// ---------- teams ----------

// TestListLinearTeams pins the teams endpoint: the stub's two teams
// come back, and the key travels as a Bearer header.
func TestListLinearTeams(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	actorID, memberEmail, wsSlug, ident, _ := linearImportFixture(t, pool)

	stub, srv := newLinearStub(t, memberEmail)
	in := linearTestInput()
	client := linearTestClient(t, stub, srv, in)

	res, err := ListLinearTeamsWithClient(ctx, pool, client, wsSlug, ident, actorID, in.APIKey)
	if err != nil {
		t.Fatalf("teams: %v", err)
	}
	if len(res.Teams) != 2 {
		t.Fatalf("teams = %+v, want 2", res.Teams)
	}
	if res.Teams[0] != (LinearTeam{ID: "team-1", Key: "ENG", Name: "Engineering"}) {
		t.Errorf("teams[0] = %+v, want ENG/Engineering", res.Teams[0])
	}
	if res.Teams[1] != (LinearTeam{ID: "team-2", Key: "DES", Name: "Design"}) {
		t.Errorf("teams[1] = %+v, want DES/Design", res.Teams[1])
	}
	stub.authOK(t, in.APIKey)

	// Empty key → 400 before any HTTP.
	if _, err := ListLinearTeamsWithClient(ctx, pool, client, wsSlug, ident, actorID, ""); !errors.Is(err, ErrLinearBadInput) {
		t.Errorf("empty key err = %v, want ErrLinearBadInput", err)
	}
}

// ---------- preview ----------

// TestPreviewLinearImport exercises the preview: two issue pages via
// the after cursor, state matching (case-insensitive via the seeded
// states), unmatched state flagged by name, priority inversion,
// labels, assignee match + miss counting, due dates, URLs — and no
// writes anywhere.
func TestPreviewLinearImport(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	actorID, memberEmail, wsSlug, ident, projectID := linearImportFixture(t, pool)

	stub, srv := newLinearStub(t, memberEmail)
	in := linearTestInput()
	client := linearTestClient(t, stub, srv, in)

	prev, err := PreviewLinearImportWithClient(ctx, pool, client, wsSlug, ident, actorID, in)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if prev.Total != 4 {
		t.Errorf("total = %d, want 4 (two pages)", prev.Total)
	}
	if len(prev.Issues) != 4 {
		t.Fatalf("issues = %d, want 4", len(prev.Issues))
	}
	byID := map[string]LinearImportPreviewRow{}
	for _, r := range prev.Issues {
		byID[r.Identifier] = r
	}
	// ENG-1: Todo matches the seeded state; Linear 2 (high) → 3.
	if r := byID["ENG-1"]; r.Title != "Ship v2" || r.State != "Todo" || r.Priority != 3 {
		t.Errorf("ENG-1 = %+v, want Ship v2/Todo/3", r)
	}
	if r := byID["ENG-1"]; len(r.Labels) != 1 || r.Labels[0] != "bug" {
		t.Errorf("ENG-1 labels = %v, want [bug]", r.Labels)
	}
	if r := byID["ENG-1"]; r.AssigneeEmail == nil || *r.AssigneeEmail != memberEmail || !r.AssigneeMatched {
		t.Errorf("ENG-1 assignee = %v matched=%v, want %s/true", r.AssigneeEmail, r.AssigneeMatched, memberEmail)
	}
	if r := byID["ENG-1"]; r.DueDate == nil || *r.DueDate != "2026-02-01" {
		t.Errorf("ENG-1 due = %v, want 2026-02-01", r.DueDate)
	}
	if r := byID["ENG-1"]; r.URL != "https://linear.app/acme/issue/ENG-1/ship-v2" {
		t.Errorf("ENG-1 url = %q", r.URL)
	}
	// ENG-2: In Progress matches; Linear 4 (low) → 1; ghost email is a miss.
	if r := byID["ENG-2"]; r.State != "In Progress" || r.Priority != 1 {
		t.Errorf("ENG-2 = %+v, want In Progress/1", r)
	}
	if r := byID["ENG-2"]; r.AssigneeEmail == nil || *r.AssigneeEmail != "ghost@example.com" || r.AssigneeMatched {
		t.Errorf("ENG-2 assignee = %v matched=%v, want ghost@example.com/false", r.AssigneeEmail, r.AssigneeMatched)
	}
	if r := byID["ENG-2"]; r.DueDate != nil {
		t.Errorf("ENG-2 due = %v, want nil", r.DueDate)
	}
	// ENG-12: Done matches; Linear 0 → 0; no assignee at all.
	if r := byID["ENG-12"]; r.State != "Done" || r.Priority != 0 {
		t.Errorf("ENG-12 = %+v, want Done/0", r)
	}
	if r := byID["ENG-12"]; r.AssigneeEmail != nil || r.AssigneeMatched {
		t.Errorf("ENG-12 assignee = %v matched=%v, want nil/false", r.AssigneeEmail, r.AssigneeMatched)
	}
	if r := byID["ENG-12"]; r.DueDate == nil || *r.DueDate != "2026-03-01" {
		t.Errorf("ENG-12 due = %v, want 2026-03-01", r.DueDate)
	}
	// ENG-13: Canceled is new (no seeded state by that name) — the
	// preview shows the Linear state name; the group mapping is pinned
	// by the end-to-end import test.
	if r := byID["ENG-13"]; r.State != "Canceled" || r.Priority != 2 {
		t.Errorf("ENG-13 = %+v, want Canceled/2", r)
	}
	// Assignee misses: only the ghost (ENG-2). Unassigned issues are
	// not misses.
	if prev.AssigneeMisses != 1 {
		t.Errorf("assignee_misses = %d, want 1 (ghost only)", prev.AssigneeMisses)
	}
	// Preview writes nothing: no issues, no states, no labels — scoped
	// to this test's project (the DB is never truncated).
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM issues WHERE project_id = $1::uuid`, projectID).Scan(&n); err != nil || n != 0 {
		t.Errorf("preview wrote %d issues", n)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM states WHERE project_id = $1::uuid AND name = 'Canceled'`, projectID).Scan(&n); err != nil || n != 0 {
		t.Errorf("preview created %d states", n)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM labels WHERE workspace_id = (SELECT workspace_id FROM projects WHERE id = $1::uuid) AND name IN ('bug','feature')`, projectID).Scan(&n); err != nil || n != 0 {
		t.Errorf("preview created %d labels", n)
	}
	stub.authOK(t, in.APIKey)
}

// TestPreviewLinearImportMaxPins the max truncation: max=1 fetches one
// issue even though the stub would serve more.
func TestPreviewLinearImportMax(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	actorID, memberEmail, wsSlug, ident, _ := linearImportFixture(t, pool)

	_, srv := newLinearStub(t, memberEmail)
	in := linearTestInput()
	in.Max = 1
	client := linearTestClient(t, nil, srv, in)

	prev, err := PreviewLinearImportWithClient(ctx, pool, client, wsSlug, ident, actorID, in)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if prev.Total != 1 || len(prev.Issues) != 1 || prev.Issues[0].Identifier != "ENG-1" {
		t.Errorf("max=1 preview = %+v, want exactly ENG-1", prev)
	}
}

// ---------- end-to-end import ----------

// TestImportLinearIssuesEndToEnd is the full pipeline: after-cursor
// pagination, Markdown description kept as-is, footer-marker dedupe,
// comment import (attributed, original timestamps), label creation
// with the default gray, due→target_date, assignee match + miss,
// canceled→cancelled state creation, and rerun dedupe.
func TestImportLinearIssuesEndToEnd(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	actorID, memberEmail, wsSlug, ident, projectID := linearImportFixture(t, pool)

	stub, srv := newLinearStub(t, memberEmail)
	in := linearTestInput()
	client := linearTestClient(t, stub, srv, in)

	res, err := ImportLinearIssuesWithClient(ctx, pool, client, wsSlug, ident, actorID, in)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Created != 4 {
		t.Errorf("created = %d, want 4", res.Created)
	}
	if res.Skipped != 0 {
		t.Errorf("skipped = %d, want 0", res.Skipped)
	}
	if res.Failed != 0 {
		t.Errorf("failed = %d, want 0 (errors: %+v)", res.Failed, res.Errors)
	}
	if len(res.Errors) != 0 {
		t.Errorf("errors = %+v, want none", res.Errors)
	}
	if res.AssigneeMisses != 1 { // ghost@example.com
		t.Errorf("assignee_misses = %d, want 1", res.AssigneeMisses)
	}

	// The new state lands in the cancelled group (Linear "canceled" →
	// glance "cancelled").
	var group string
	if err := pool.QueryRow(ctx,
		`SELECT "group" FROM states WHERE project_id = $1::uuid AND name = 'Canceled'`, projectID).Scan(&group); err != nil {
		t.Fatalf("Canceled state: %v", err)
	} else if group != "cancelled" {
		t.Errorf("Canceled group = %q, want cancelled", group)
	}

	// Labels are created with the default gray (Linear's label colors
	// are not carried over — plan-pinned).
	var color string
	if err := pool.QueryRow(ctx,
		`SELECT color FROM labels WHERE workspace_id = (SELECT workspace_id FROM projects WHERE id = $1::uuid) AND name = 'bug'`, projectID).Scan(&color); err != nil {
		t.Fatalf("bug label: %v", err)
	} else if strings.ToLower(color) != "#6b7280" {
		t.Errorf("bug label color = %q, want #6b7280", color)
	}

	// ENG-1: Markdown description kept as-is + footer marker;
	// target_date is the due date; assignee matched; priority inverted.
	var desc, stateName string
	var targetDate *string
	var assigneeID *string
	var priority int
	if err := pool.QueryRow(ctx, `
		SELECT i.description::text, s.name, i.target_date::text, a.user_id::text, i.priority
		FROM issues i JOIN states s ON s.id = i.state_id
		LEFT JOIN issue_assignees a ON a.issue_id = i.id
		WHERE i.project_id = $1::uuid AND i.name = 'Ship v2'`, projectID).Scan(&desc, &stateName, &targetDate, &assigneeID, &priority); err != nil {
		t.Fatalf("ENG-1 row: %v", err)
	}
	unq, err := strconv.Unquote(desc)
	if err != nil {
		t.Fatalf("unquote ENG-1 description: %v", err)
	}
	if !strings.Contains(unq, "**bold** markdown") {
		t.Errorf("ENG-1 description lost the Markdown: %q", unq)
	}
	if !strings.Contains(unq, "*Imported from [ENG-1](https://linear.app/acme/issue/ENG-1/ship-v2)*") {
		t.Errorf("ENG-1 missing footer marker: %q", unq)
	}
	if stateName != "Todo" {
		t.Errorf("ENG-1 state = %q, want Todo", stateName)
	}
	if targetDate == nil || *targetDate != "2026-02-01" {
		t.Errorf("ENG-1 target_date = %v, want 2026-02-01", targetDate)
	}
	if assigneeID == nil {
		t.Error("ENG-1: assignee not matched to the member")
	}
	if priority != 3 {
		t.Errorf("ENG-1 priority = %d, want 3 (Linear 2/high)", priority)
	}

	// ENG-13 lands in the created Canceled state.
	if err := pool.QueryRow(ctx, `
		SELECT s.name FROM issues i JOIN states s ON s.id = i.state_id
		WHERE i.project_id = $1::uuid AND i.name = 'Kill switch'`, projectID).Scan(&stateName); err != nil {
		t.Fatalf("ENG-13 row: %v", err)
	} else if stateName != "Canceled" {
		t.Errorf("ENG-13 state = %q, want Canceled", stateName)
	}

	// The imported comment is attributed with its original timestamp
	// (and keeps the original body).
	var commentContent string
	var commentAt string
	if err := pool.QueryRow(ctx, `
		SELECT c.content::text, c.created_at::text FROM comments c
		JOIN issues i ON i.id = c.issue_id
		WHERE i.project_id = $1::uuid AND i.name = 'Ship v2'`, projectID).Scan(&commentContent, &commentAt); err != nil {
		t.Fatalf("ENG-1 comment: %v", err)
	}
	unqComment, err := strconv.Unquote(commentContent)
	if err != nil {
		t.Fatalf("unquote comment: %v", err)
	}
	if !strings.Contains(unqComment, "first comment") || !strings.Contains(unqComment, "originally posted by Ada") {
		t.Errorf("comment attribution wrong: %q", unqComment)
	}
	if !strings.HasPrefix(commentAt, "2026-01-03 10:00:00") {
		t.Errorf("comment created_at = %q, want the original 2026-01-03T10:00Z", commentAt)
	}

	stub.authOK(t, in.APIKey)

	// Re-run: everything is skipped via the footer markers (ENG-1 does
	// not shadow ENG-12 — both are skipped on their own marker).
	res2, err := ImportLinearIssuesWithClient(ctx, pool, client, wsSlug, ident, actorID, in)
	if err != nil {
		t.Fatalf("re-run import: %v", err)
	}
	if res2.Created != 0 || res2.Skipped != 4 {
		t.Errorf("re-run: created=%d skipped=%d, want 0/4", res2.Created, res2.Skipped)
	}
}

// ---------- token handling ----------

// TestLinearTokenNeverPersisted pins the token handling: Bearer auth
// on the wire, never in the DB, never in error messages.
func TestLinearTokenNeverPersisted(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	actorID, memberEmail, wsSlug, ident, _ := linearImportFixture(t, pool)

	const token = "lin_api_" + "DONOTSTORE_z9x8c7v6b5"
	stub, srv := newLinearStub(t, memberEmail)
	client := newLinearTestClient(token, srv.URL)

	in := linearTestInput()
	in.APIKey = token
	if _, err := ImportLinearIssuesWithClient(ctx, pool, client, wsSlug, ident, actorID, in); err != nil {
		t.Fatalf("import: %v", err)
	}
	assertTokenAbsentFromDB(t, pool, token)
	stub.authOK(t, token)

	// Failed run (401): the error must not echo the token either — the
	// service returns the bare sentinel for 401.
	stub.override = func(w http.ResponseWriter, r *http.Request) bool {
		stub.record(r)
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"errors":[{"message":"Authentication required"}]}`)
		return true
	}
	_, err := ImportLinearIssuesWithClient(ctx, pool, client, wsSlug, ident, actorID, in)
	if err == nil {
		t.Fatal("expected 401 error")
	}
	if !errors.Is(err, ErrLinearUnauthorized) {
		t.Errorf("err = %v, want ErrLinearUnauthorized", err)
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("401 error leaks the token: %q", err.Error())
	}
	assertTokenAbsentFromDB(t, pool, token)
}

// ---------- error mapping / tenancy / gate ----------

// TestLinearImportErrorMapping pins the upstream-error contract:
// GraphQL errors[] → ErrLinearUpstream, HTTP 500 → ErrLinearUpstream,
// 429 → typed rate-limit error (never retried silently), null team →
// ErrLinearTeamNotFound (400).
func TestLinearImportErrorMapping(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	actorID, memberEmail, wsSlug, ident, _ := linearImportFixture(t, pool)
	in := linearTestInput()

	cases := []struct {
		name          string
		status        int
		body          string
		headers       map[string]string
		wantRateLimit bool
		wantErr       error
	}{
		{"graphql errors", 200, `{"errors":[{"message":"Something went wrong"}]}`, nil, false, ErrLinearUpstream},
		{"server error", 500, `{"errors":[{"message":"boom"}]}`, nil, false, ErrLinearUpstream},
		{"rate limited", 429, ``, map[string]string{"Retry-After": "30"}, true, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub, srv := newLinearStub(t, memberEmail)
			stub.override = func(w http.ResponseWriter, r *http.Request) bool {
				for k, v := range tc.headers {
					w.Header().Set(k, v)
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
				return true
			}
			client := newLinearTestClient(in.APIKey, srv.URL)
			_, err := PreviewLinearImportWithClient(ctx, pool, client, wsSlug, ident, actorID, in)
			if tc.wantRateLimit {
				var rl *LinearRateLimitError
				if !errors.As(err, &rl) {
					t.Errorf("err = %v, want *LinearRateLimitError", err)
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

	// Null team → team not found.
	t.Run("team not found", func(t *testing.T) {
		_, srv := newLinearStub(t, memberEmail)
		client := newLinearTestClient(in.APIKey, srv.URL)
		missing := linearTestInput()
		missing.TeamID = "team-missing"
		_, err := PreviewLinearImportWithClient(ctx, pool, client, wsSlug, ident, actorID, missing)
		if !errors.Is(err, ErrLinearTeamNotFound) {
			t.Errorf("err = %v, want ErrLinearTeamNotFound", err)
		}
	})
}

// TestLinearMemberGate pins the role gate: non-members get ErrNotFound
// (tenancy boundary), guests get ErrForbidden — same as the other
// importers. A cross-workspace actor cannot import into this project.
func TestLinearMemberGate(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	actorID, _, wsSlug, ident, _ := linearImportFixture(t, pool)

	_, srv := newLinearStub(t, "nobody@example.com")
	client := newLinearTestClient(linearTestInput().APIKey, srv.URL)

	outsider := createTestUser(t, pool, uniqueTestEmail("linear-outsider"))
	if _, err := PreviewLinearImportWithClient(ctx, pool, client, wsSlug, ident, outsider, linearTestInput()); !errors.Is(err, ErrNotFound) {
		t.Errorf("outsider preview err = %v, want ErrNotFound", err)
	}
	if _, err := ListLinearTeamsWithClient(ctx, pool, client, wsSlug, ident, outsider, linearTestInput().APIKey); !errors.Is(err, ErrNotFound) {
		t.Errorf("outsider teams err = %v, want ErrNotFound", err)
	}

	guest := createTestUser(t, pool, uniqueTestEmail("linear-guest"))
	if _, err := pool.Exec(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role)
		 SELECT w.id, $1::uuid, $2 FROM workspaces w WHERE w.slug = $3
		 ON CONFLICT DO NOTHING`, guest, RoleGuest, wsSlug); err != nil {
		t.Fatalf("add guest: %v", err)
	}
	_ = actorID
	if _, err := ImportLinearIssuesWithClient(ctx, pool, client, wsSlug, ident, guest, linearTestInput()); !errors.Is(err, ErrForbidden) {
		t.Errorf("guest import err = %v, want ErrForbidden", err)
	}
}
