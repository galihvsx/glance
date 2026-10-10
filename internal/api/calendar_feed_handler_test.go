package api

// Calendar feed tests (C15T3):
// - Feed format: CRLF line endings, VCALENDAR envelope, TEXT escaping,
//   75-octet folding, per-issue VEVENT fields.
// - Auth: missing/bad/revoked/foreign tokens → 401; Bearer fallback;
//   session cookie alone is not enough.
// - Token management: POST mints (201, shown once), rotation revokes
//   the old secret, DELETE revokes, status reflects reality.
// - Scope: project feed, cycle feed, non-member rejection, archived and
//   undated issues excluded, over-cap → honest 400.
//
// Real test database, no skips.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"glance/internal/auth"
	"glance/internal/config"
	"glance/internal/service"
)

// testFeedServer registers the routes the feed tests need. The feed
// handler gets an explicit AppURL so absolute issue URLs in the feed
// are deterministic.
func testFeedServer(t *testing.T, pool *pgxpool.Pool) *echo.Echo {
	t.Helper()
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterCycleRoutes(e, &IssueHandler{Pool: pool})
	RegisterCalendarFeedRoutes(e, &CalendarFeedHandler{Pool: pool, Config: &config.Config{AppURL: "https://glance.example"}})
	return e
}

// setupFeedProject creates a user, workspace, and project, returning the
// pool, server, session cookie, slug, identifier, and feed base path.
func setupFeedProject(t *testing.T, prefix string) (*pgxpool.Pool, *echo.Echo, *http.Cookie, string, string, string) {
	t.Helper()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testFeedServer(t, pool)

	email := uniqueEmail(prefix + "-feed-user")
	cookie := loginTestUser(t, e, pool, email, "feed-test-agent", uniqueIP())
	slug := uniqueSlug(prefix + "-feed")
	createWorkspaceHTTP(t, e, cookie, "Feed Co", slug)
	ident := uniqueProjectIdentifier(strings.ToUpper(prefix[:2]))
	createProjectHTTP(t, e, cookie, slug, "Feed Project", ident)
	return pool, e, cookie, slug, ident,
		"/api/v1/workspaces/" + slug + "/projects/" + ident
}

// createFeedIssue creates an issue with optional target/start dates and
// returns its id. name and description are passed through verbatim.
func createFeedIssue(t *testing.T, e *echo.Echo, cookie *http.Cookie, issuesBase, name, targetDate, startDate string, description string) string {
	t.Helper()
	body := map[string]any{"name": name}
	if targetDate != "" {
		body["target_date"] = targetDate
	}
	if startDate != "" {
		body["start_date"] = startDate
	}
	if description != "" {
		body["description"] = json.RawMessage(description)
	}
	raw, _ := json.Marshal(body)
	rec := postAuthedJSON(t, e, http.MethodPost, issuesBase, cookie, string(raw))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create issue %q: status = %d (body: %s)", name, rec.Code, rec.Body.String())
	}
	var iss struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &iss); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	return iss.ID
}

// mintFeedToken mints a feed token via the management endpoint and
// returns the plaintext (shown once).
func mintFeedToken(t *testing.T, e *echo.Echo, cookie *http.Cookie) string {
	t.Helper()
	rec := postAuthedJSON(t, e, http.MethodPost, "/api/v1/me/calendar-token", cookie, `{}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("mint feed token: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created token: %v", err)
	}
	if !strings.HasPrefix(created.Token, auth.FeedTokenPrefix) {
		t.Fatalf("token = %q, want prefix %q", created.Token, auth.FeedTokenPrefix)
	}
	return created.Token
}

func getFeed(t *testing.T, e *echo.Echo, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path+"?token="+token, nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// unfoldICal joins folded continuation lines (CRLF + single space) so
// assertions can match long property values (URL, DESCRIPTION) that the
// 75-octet fold split across lines.
func unfoldICal(body string) string {
	return strings.ReplaceAll(body, "\r\n ", "")
}

// assertCRLF fails when the body contains a bare LF (not part of CRLF).
func assertCRLF(t *testing.T, body string) {
	t.Helper()
	if strings.Contains(strings.ReplaceAll(body, "\r\n", ""), "\n") {
		t.Fatal("feed contains bare LF line endings; want CRLF throughout")
	}
	if strings.Contains(body, "\r\r") {
		t.Fatal("feed contains doubled CR")
	}
}

// TestCalendarFeedProjectEndToEnd exercises the full project feed:
// format, escaping, field mapping, and issue selection.
func TestCalendarFeedProjectEndToEnd(t *testing.T) {
	pool, e, cookie, slug, ident, base := setupFeedProject(t, "cal")

	trickyName := "Fix login, redirect; edge \\ cases\nnewline"
	descDoc := `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"First paragraph."}]},{"type":"paragraph","content":[{"type":"text","text":"Second, with comma."}]}]}`
	trickyID := createFeedIssue(t, e, cookie, base+"/issues", trickyName, "2026-10-20", "", descDoc)
	datedID := createFeedIssue(t, e, cookie, base+"/issues", "Plain dated", "2026-10-22", "2026-10-18", "")
	undatedID := createFeedIssue(t, e, cookie, base+"/issues", "No due date", "", "", "")
	archivedID := createFeedIssue(t, e, cookie, base+"/issues", "Archived dated", "2026-10-21", "", "")
	if _, err := pool.Exec(context.Background(),
		`UPDATE issues SET archived_at = now() WHERE id = $1::uuid`, archivedID); err != nil {
		t.Fatalf("archive issue: %v", err)
	}

	token := mintFeedToken(t, e, cookie)
	rec := getFeed(t, e, base+"/calendar.ics", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("feed: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get(echo.HeaderContentType); ct != "text/calendar; charset=utf-8" {
		t.Fatalf("content-type = %q, want text/calendar", ct)
	}
	body := rec.Body.String()
	assertCRLF(t, body)

	// VCALENDAR envelope.
	for _, want := range []string{
		"BEGIN:VCALENDAR\r\n",
		"VERSION:2.0\r\n",
		"PRODID:-//glance//Calendar Feed//EN\r\n",
		"CALSCALE:GREGORIAN\r\n",
		"\r\nEND:VCALENDAR\r\n",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("feed missing %q", want)
		}
	}

	// SUMMARY escaping: comma → \,, semicolon → \;, backslash → \\,
	// newline → \n.
	escaped := `Fix login\, redirect\; edge \\ cases\nnewline`
	if !strings.Contains(body, "SUMMARY:") || !strings.Contains(body, escaped) {
		t.Errorf("SUMMARY escaping wrong; want %q in feed", escaped)
	}

	// Per-issue fields for the dated issue (matched on the unfolded body:
	// long URL/DESCRIPTION values fold across lines on the wire).
	unfolded := unfoldICal(body)
	issueURL := "https://glance.example/w/" + slug + "/p/" + ident + "/i/" + datedID
	for _, want := range []string{
		"UID:" + datedID + "@glance",
		"DTSTART;VALUE=DATE:20261018",
		"DUE;VALUE=DATE:20261022",
		"URL:" + issueURL,
		"DESCRIPTION:" + issueURL,
	} {
		if !strings.Contains(unfolded, want) {
			t.Errorf("feed missing %q", want)
		}
	}

	// DTSTAMP is a UTC timestamp.
	if !strings.Contains(body, "DTSTAMP:") {
		t.Error("feed missing DTSTAMP")
	}

	// The tricky issue's description text lands in DESCRIPTION (plain
	// text, comma escaped) alongside its URL — matched unfolded.
	trickyURL := "https://glance.example/w/" + slug + "/p/" + ident + "/i/" + trickyID
	if !strings.Contains(unfolded, "First paragraph.\\nSecond\\, with comma.\\n"+trickyURL) {
		t.Error("DESCRIPTION missing plain-text description + URL")
	}

	// Undated and archived issues are excluded.
	for _, excluded := range []string{"No due date", "Archived dated", "UID:" + undatedID + "@glance", "UID:" + archivedID + "@glance"} {
		if strings.Contains(body, excluded) {
			t.Errorf("feed contains excluded issue marker %q", excluded)
		}
	}

	// Exactly two VEVENTs.
	if n := strings.Count(body, "BEGIN:VEVENT"); n != 2 {
		t.Errorf("VEVENT count = %d, want 2", n)
	}
}

// TestCalendarFeedAuth covers the credential matrix.
func TestCalendarFeedAuth(t *testing.T) {
	_, e, cookie, _, _, base := setupFeedProject(t, "calauth")
	token := mintFeedToken(t, e, cookie)

	// No token → 401.
	req := httptest.NewRequest(http.MethodGet, base+"/calendar.ics", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing token: status = %d, want 401", rec.Code)
	}
	assertErrorCode(t, rec, "unauthorized")

	// Garbage token → 401.
	rec = getFeed(t, e, base+"/calendar.ics", auth.FeedTokenPrefix+"nope-nope-nope")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad token: status = %d, want 401", rec.Code)
	}
	assertErrorCode(t, rec, "unauthorized")

	// An API token (gl_) is not a feed token → 401, no confusion.
	rec = getFeed(t, e, base+"/calendar.ics", "gl_something")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("api token as feed token: status = %d, want 401", rec.Code)
	}

	// Session cookie alone is not enough — feeds are not session-authed.
	req = httptest.NewRequest(http.MethodGet, base+"/calendar.ics", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("cookie-only: status = %d, want 401", rec.Code)
	}

	// Bearer fallback works.
	req = httptest.NewRequest(http.MethodGet, base+"/calendar.ics", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("bearer fallback: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
}

// TestCalendarFeedRevokedToken: revoking via the management endpoint
// kills the feed immediately.
func TestCalendarFeedRevokedToken(t *testing.T) {
	_, e, cookie, _, _, base := setupFeedProject(t, "calrev")
	token := mintFeedToken(t, e, cookie)

	rec := getFeed(t, e, base+"/calendar.ics", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("pre-revoke feed: status = %d", rec.Code)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/me/calendar-token", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}

	rec = getFeed(t, e, base+"/calendar.ics", token)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token: status = %d, want 401", rec.Code)
	}
	assertErrorCode(t, rec, "unauthorized")

	// Revoking again → 404 (nothing live).
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/me/calendar-token", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("second revoke: status = %d, want 404", rec.Code)
	}

	// Status endpoint reflects the revoke.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/me/calendar-token", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	var status struct {
		Active bool `json:"active"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if status.Active {
		t.Fatal("status active after revoke")
	}
}

// TestCalendarFeedTokenRotation: a second POST kills the first secret.
func TestCalendarFeedTokenRotation(t *testing.T) {
	_, e, cookie, _, _, base := setupFeedProject(t, "calrot")
	first := mintFeedToken(t, e, cookie)

	// Status shows the live token.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/me/calendar-token", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	var status struct {
		Active    bool   `json:"active"`
		CreatedAt string `json:"created_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if !status.Active || status.CreatedAt == "" {
		t.Fatalf("status = %+v, want active with created_at", status)
	}

	second := mintFeedToken(t, e, cookie)
	if first == second {
		t.Fatal("rotation returned the same secret")
	}
	if rec := getFeed(t, e, base+"/calendar.ics", first); rec.Code != http.StatusUnauthorized {
		t.Fatalf("old token after rotation: status = %d, want 401", rec.Code)
	}
	if rec := getFeed(t, e, base+"/calendar.ics", second); rec.Code != http.StatusOK {
		t.Fatalf("new token after rotation: status = %d, want 200", rec.Code)
	}
}

// TestCalendarFeedCycle: the cycle feed carries the cycle's dated
// issues; unknown or foreign cycles 404.
func TestCalendarFeedCycle(t *testing.T) {
	_, e, cookie, _, _, base := setupFeedProject(t, "calcyc")

	inCycle := createFeedIssue(t, e, cookie, base+"/issues", "Cycle dated", "2026-11-05", "", "")
	outside := createFeedIssue(t, e, cookie, base+"/issues", "Outside dated", "2026-11-06", "", "")

	// Create a cycle and add one issue.
	rec := postAuthedJSON(t, e, http.MethodPost, base+"/cycles", cookie,
		`{"name":"Sprint 1","start_date":"2026-11-01","end_date":"2026-11-14"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create cycle: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var cyc struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &cyc); err != nil {
		t.Fatalf("decode cycle: %v", err)
	}
	rec = postAuthedJSON(t, e, http.MethodPost, base+"/cycles/"+cyc.ID+"/issues", cookie,
		fmt.Sprintf(`{"issue_ids":[%q]}`, inCycle))
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("add issue to cycle: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	token := mintFeedToken(t, e, cookie)
	cycleFeed := base + "/cycles/" + cyc.ID + "/calendar.ics"

	rec = getFeed(t, e, cycleFeed, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("cycle feed: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	assertCRLF(t, body)
	if !strings.Contains(body, "UID:"+inCycle+"@glance") {
		t.Error("cycle feed missing the cycle's issue")
	}
	if strings.Contains(body, "UID:"+outside+"@glance") {
		t.Error("cycle feed contains an issue outside the cycle")
	}
	if !strings.Contains(body, "X-WR-CALNAME:Sprint 1") {
		t.Error("cycle feed CALNAME should be the cycle name")
	}

	// Unknown cycle → 404.
	rec = getFeed(t, e, base+"/cycles/00000000-0000-0000-0000-000000000000/calendar.ics", token)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown cycle: status = %d, want 404", rec.Code)
	}

	// Malformed cycle id → 400 (invalid id), not 500.
	rec = getFeed(t, e, base+"/cycles/not-a-uuid/calendar.ics", token)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed cycle: status = %d, want 400", rec.Code)
	}
}

// TestCalendarFeedNonMember: a feed token's owner must be a workspace
// member of the feed's workspace — the feed reveals nothing beyond what
// the list endpoint would (404, not 403).
func TestCalendarFeedNonMember(t *testing.T) {
	pool, e, cookie, _, _, base := setupFeedProject(t, "calnm")

	outsiderEmail := uniqueEmail("calnm-outsider")
	outsiderCookie := loginTestUser(t, e, pool, outsiderEmail, "feed-test-agent", uniqueIP())
	outsiderToken := mintFeedToken(t, e, outsiderCookie)

	// The project owner can fetch.
	if rec := getFeed(t, e, base+"/calendar.ics", mintFeedToken(t, e, cookie)); rec.Code != http.StatusOK {
		t.Fatalf("owner feed: status = %d, want 200", rec.Code)
	}
	// The outsider cannot — 404 like the list endpoint.
	rec := getFeed(t, e, base+"/calendar.ics", outsiderToken)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("outsider feed: status = %d, want 404", rec.Code)
	}
}

// TestCalendarFeedTooLarge: an over-cap feed answers 400 honestly.
func TestCalendarFeedTooLarge(t *testing.T) {
	old := service.MaxFeedRows
	service.MaxFeedRows = 1
	defer func() { service.MaxFeedRows = old }()

	_, e, cookie, _, _, base := setupFeedProject(t, "calcap")
	createFeedIssue(t, e, cookie, base+"/issues", "Dated one", "2026-12-01", "", "")
	createFeedIssue(t, e, cookie, base+"/issues", "Dated two", "2026-12-02", "", "")
	token := mintFeedToken(t, e, cookie)

	rec := getFeed(t, e, base+"/calendar.ics", token)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("over-cap feed: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "bad_request")
}

// TestCalendarFeedUnknownProject: bad slug → 404, bad identifier → 400.
func TestCalendarFeedUnknownProject(t *testing.T) {
	_, e, cookie, slug, _, _ := setupFeedProject(t, "calunk")
	token := mintFeedToken(t, e, cookie)

	rec := getFeed(t, e, "/api/v1/workspaces/no-such-ws-xyz/projects/AA/calendar.ics", token)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown workspace: status = %d, want 404", rec.Code)
	}
	rec = getFeed(t, e, "/api/v1/workspaces/"+slug+"/projects/bad!!/calendar.ics", token)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad identifier: status = %d, want 400", rec.Code)
	}
}

// TestFeedTokenManagementAuth: the management endpoints require the
// session cookie — Bearer tokens (even feed tokens) cannot mint.
func TestFeedTokenManagementAuth(t *testing.T) {
	_, e, cookie, _, _, _ := setupFeedProject(t, "calmgmt")
	token := mintFeedToken(t, e, cookie)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/me/calendar-token", strings.NewReader(`{}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("feed-token minting feed token: status = %d, want 401", rec.Code)
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/v1/me/calendar-token", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("feed-token revoking feed token: status = %d, want 401", rec.Code)
	}
}

// TestWriteICalLineFolding: long lines fold at 75 octets, continuation
// lines start with a space, and unfolding restores the original.
func TestWriteICalLineFolding(t *testing.T) {
	var b strings.Builder
	long := "SUMMARY:" + strings.Repeat("ä", 40) // 2-byte runes: fold must not split them
	writeICalLine(&b, long)
	out := b.String()
	assertCRLF(t, out)
	lines := strings.Split(strings.TrimSuffix(out, "\r\n"), "\r\n")
	if len(lines) < 2 {
		t.Fatalf("long line did not fold: %d lines", len(lines))
	}
	for _, l := range lines {
		if len(l) > 75 {
			t.Errorf("folded line is %d octets, want ≤ 75", len(l))
		}
	}
	for _, l := range lines[1:] {
		if !strings.HasPrefix(l, " ") {
			t.Errorf("continuation line %q does not start with a space", l)
		}
	}
	// Unfolding restores the original bytes exactly.
	var unfolded strings.Builder
	for i, l := range lines {
		if i == 0 {
			unfolded.WriteString(l)
		} else {
			unfolded.WriteString(l[1:])
		}
	}
	if unfolded.String() != long {
		t.Error("unfolded line differs from the original")
	}
	if !utf8.ValidString(out) {
		t.Error("folded output is not valid UTF-8")
	}
}

// TestEscapeICalText covers the RFC 5545 §3.3.11 escapes.
func TestEscapeICalText(t *testing.T) {
	in := "a\\b;c,d\ne\rf\ng"
	want := `a\\b\;c\,d\ne\nf\ng`
	if got := escapeICalText(in); got != want {
		t.Errorf("escapeICalText(%q) = %q, want %q", in, got, want)
	}
}

// TestTiptapPlainText covers the description extraction used in
// DESCRIPTION.
func TestTiptapPlainText(t *testing.T) {
	doc := `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Hello"}]},{"type":"paragraph","content":[{"type":"text","text":"World","marks":[]}]}]}`
	if got := service.TiptapPlainText([]byte(doc)); got != "Hello\nWorld" {
		t.Errorf("TiptapPlainText = %q, want %q", got, "Hello\nWorld")
	}
	if got := service.TiptapPlainText(nil); got != "" {
		t.Errorf("TiptapPlainText(nil) = %q, want empty", got)
	}
	if got := service.TiptapPlainText([]byte("not json")); got != "" {
		t.Errorf("TiptapPlainText(garbage) = %q, want empty", got)
	}
}
