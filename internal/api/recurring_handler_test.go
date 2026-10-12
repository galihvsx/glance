package api

// Recurring schedule HTTP endpoint tests (C28T2): CRUD happy paths,
// PATCH tri-state (absent vs null vs value), validation 400s, 404s
// (unknown id, cross-project tenancy, bad slug), guest 403s, and
// run-now firing an issue + advancing (or exhausting) the schedule.
// Real test database, no skips.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
)

// setupRecurringHTTP builds the echo server with the workspace, project,
// issue, and schedule routes, plus one workspace+project owned by the
// admin cookie.
func setupRecurringHTTP(t *testing.T) (*echo.Echo, *http.Cookie, string, string, string) {
	t.Helper()
	pool := newTestPool(t)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterTaxonomyRoutes(e, &IssueHandler{Pool: pool})
	RegisterRecurringRoutes(e, &ProjectHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail("h-rec-admin"), "test-agent/1.0", uniqueIP())
	slug := uniqueSlug("rec-http")
	createWorkspaceHTTP(t, e, cookie, "Recurring Co", slug)
	ident := uniqueProjectIdentifier("RC")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	base := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/schedules"
	return e, cookie, slug, ident, base
}

func todayYYYYMMDD() string {
	return time.Now().UTC().Format("2006-01-02")
}

func tomorrowYYYYMMDD() string {
	return time.Now().UTC().Add(24 * time.Hour).Format("2006-01-02")
}

type recurringScheduleJSON struct {
	ID                  string   `json:"id"`
	Name                string   `json:"name"`
	Active              bool     `json:"active"`
	Frequency           string   `json:"frequency"`
	Interval            int      `json:"interval"`
	DaysOfWeek          []int    `json:"days_of_week"`
	DayOfMonth          *int     `json:"day_of_month"`
	Hour                int      `json:"hour"`
	NextRunAt           string   `json:"next_run_at"`
	LastRunAt           *string  `json:"last_run_at"`
	RunCount            int      `json:"run_count"`
	TitleTemplate       string   `json:"title_template"`
	DescriptionTemplate *string  `json:"description_template"`
	StateID             *string  `json:"state_id"`
	Priority            int      `json:"priority"`
	LabelIDs            []string `json:"label_ids"`
	AssigneeIDs         []string `json:"assignee_ids"`
}

func decodeSchedule(t *testing.T, body []byte) recurringScheduleJSON {
	t.Helper()
	var s recurringScheduleJSON
	if err := json.Unmarshal(body, &s); err != nil {
		t.Fatalf("decode schedule: %v", err)
	}
	return s
}

func createDailySchedule(t *testing.T, e *echo.Echo, base string, cookie *http.Cookie, extra string) recurringScheduleJSON {
	t.Helper()
	body := fmt.Sprintf(`{"name":"Daily standup","frequency":"daily","hour":9,"starts_on":%q,"title_template":"Standup {{date}}"%s}`,
		todayYYYYMMDD(), extra)
	rec := postAuthedJSON(t, e, http.MethodPost, base, cookie, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	return decodeSchedule(t, rec.Body.Bytes())
}

// TestRecurringScheduleHTTPCRUD covers the CRUD happy paths.
func TestRecurringScheduleHTTPCRUD(t *testing.T) {
	e, cookie, _, _, base := setupRecurringHTTP(t)

	// Create → 201; active defaults true; next_run_at present.
	s := createDailySchedule(t, e, base, cookie, "")
	if s.ID == "" || s.Name != "Daily standup" || !s.Active || s.Frequency != "daily" {
		t.Fatalf("created = %+v", s)
	}
	if s.NextRunAt == "" || s.Interval != 1 || s.RunCount != 0 {
		t.Fatalf("created next_run_at/interval/run_count = %+v", s)
	}
	schedURL := base + "/" + s.ID

	// List → 200, one row, never null.
	rec := getAuthed(t, e, http.MethodGet, base, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if strings.TrimSpace(rec.Body.String()) == "null" {
		t.Fatalf("list must not be null")
	}
	var list []recurringScheduleJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 || list[0].ID != s.ID {
		t.Fatalf("list = %+v", list)
	}

	// Get → 200.
	rec = getAuthed(t, e, http.MethodGet, schedURL, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	// PATCH name + pause → 200.
	rec = postAuthedJSON(t, e, http.MethodPatch, schedURL, cookie,
		`{"name":"Renamed","active":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	got := decodeSchedule(t, rec.Body.Bytes())
	if got.Name != "Renamed" || got.Active {
		t.Fatalf("patched = %+v", got)
	}

	// Empty PATCH → 400 nothing to update.
	rec = postAuthedJSON(t, e, http.MethodPatch, schedURL, cookie, `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty patch: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// DELETE → 204; get → 404 schedule_not_found; list → [].
	rec = postAuthedJSON(t, e, http.MethodDelete, schedURL, cookie, ``)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, schedURL, cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"schedule_not_found"`) {
		t.Fatalf("404 envelope code: body=%s", rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, base, cookie)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("list after delete: status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

// TestRecurringScheduleHTTPValidation covers the 400 paths: bad
// frequency, weekly without days, monthly without day, ends before
// starts, missing title template, malformed id.
func TestRecurringScheduleHTTPValidation(t *testing.T) {
	e, cookie, _, _, base := setupRecurringHTTP(t)
	today := todayYYYYMMDD()

	cases := []struct {
		name string
		body string
	}{
		{"bad frequency", fmt.Sprintf(`{"name":"x","frequency":"yearly","starts_on":%q,"title_template":"T"}`, today)},
		{"weekly without days", fmt.Sprintf(`{"name":"x","frequency":"weekly","starts_on":%q,"title_template":"T"}`, today)},
		{"weekly bad weekday", fmt.Sprintf(`{"name":"x","frequency":"weekly","days_of_week":[9],"starts_on":%q,"title_template":"T"}`, today)},
		{"monthly without day", fmt.Sprintf(`{"name":"x","frequency":"monthly","starts_on":%q,"title_template":"T"}`, today)},
		{"monthly bad day", fmt.Sprintf(`{"name":"x","frequency":"monthly","day_of_month":0,"starts_on":%q,"title_template":"T"}`, today)},
		{"ends before starts", fmt.Sprintf(`{"name":"x","frequency":"daily","starts_on":%q,"ends_on":"2000-01-01","title_template":"T"}`, today)},
		{"missing title template", fmt.Sprintf(`{"name":"x","frequency":"daily","starts_on":%q}`, today)},
		{"missing name", fmt.Sprintf(`{"frequency":"daily","starts_on":%q,"title_template":"T"}`, today)},
		{"bad starts_on", `{"name":"x","frequency":"daily","starts_on":"tomorrow","title_template":"T"}`},
		{"bad hour", fmt.Sprintf(`{"name":"x","frequency":"daily","hour":25,"starts_on":%q,"title_template":"T"}`, today)},
		{"bad priority", fmt.Sprintf(`{"name":"x","frequency":"daily","priority":9,"starts_on":%q,"title_template":"T"}`, today)},
		{"bad body", `not json`},
	}
	for _, tc := range cases {
		rec := postAuthedJSON(t, e, http.MethodPost, base, cookie, tc.body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400 (body: %s)", tc.name, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"code":"bad_request"`) {
			t.Fatalf("%s: 400 envelope code: body=%s", tc.name, rec.Body.String())
		}
	}

	// Malformed schedule id → 400 (never a ::uuid 500).
	rec := getAuthed(t, e, http.MethodGet, base+"/not-a-uuid", cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed id: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// Well-formed but unknown id → 404 schedule_not_found.
	rec = getAuthed(t, e, http.MethodGet, base+"/00000000-0000-0000-0000-000000000000", cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown id: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"schedule_not_found"`) {
		t.Fatalf("404 envelope code: body=%s", rec.Body.String())
	}

	// A well-formed PATCH that breaks cadence validation → 400.
	s := createDailySchedule(t, e, base, cookie, "")
	rec = postAuthedJSON(t, e, http.MethodPatch, base+"/"+s.ID, cookie,
		`{"frequency":"weekly"}`) // weekly needs days_of_week
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("cadence break: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

// TestRecurringScheduleHTTPTriState covers PATCH tri-state for the
// nullable fields: absent = untouched, null = clear, value = set.
func TestRecurringScheduleHTTPTriState(t *testing.T) {
	e, cookie, slug, _, base := setupRecurringHTTP(t)

	// Grab a real state id for the schedule.
	pool := newTestPool(t)
	var backlogStateID string
	if err := pool.QueryRow(context.Background(),
		`SELECT s.id::text FROM states s
		   JOIN projects p ON p.id = s.project_id
		   JOIN workspaces w ON w.id = p.workspace_id
		  WHERE w.slug = $1 AND s."group" = 'backlog' LIMIT 1`, slug).Scan(&backlogStateID); err != nil {
		t.Fatalf("backlog state: %v", err)
	}

	// Weekly schedule with description_template + state_id set.
	body := fmt.Sprintf(`{"name":"Weekly","frequency":"weekly","days_of_week":[1,3],"hour":10,"starts_on":%q,"title_template":"Sync {{date}}","description_template":"Agenda for {{date}}","state_id":%q}`,
		todayYYYYMMDD(), backlogStateID)
	rec := postAuthedJSON(t, e, http.MethodPost, base, cookie, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	s := decodeSchedule(t, rec.Body.Bytes())
	if s.DescriptionTemplate == nil || s.StateID == nil {
		t.Fatalf("created nullable fields = %+v", s)
	}
	schedURL := base + "/" + s.ID

	// PATCH absent → untouched.
	rec = postAuthedJSON(t, e, http.MethodPatch, schedURL, cookie, `{"name":"Still weekly"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch absent: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	got := decodeSchedule(t, rec.Body.Bytes())
	if got.DescriptionTemplate == nil || *got.DescriptionTemplate != "Agenda for {{date}}" {
		t.Fatalf("absent must leave description_template untouched: %+v", got)
	}
	if got.StateID == nil || *got.StateID != backlogStateID {
		t.Fatalf("absent must leave state_id untouched: %+v", got)
	}

	// PATCH null → cleared.
	rec = postAuthedJSON(t, e, http.MethodPatch, schedURL, cookie,
		`{"description_template":null,"state_id":null}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch null: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	got = decodeSchedule(t, rec.Body.Bytes())
	if got.DescriptionTemplate != nil {
		t.Fatalf("null must clear description_template: %+v", got)
	}
	if got.StateID != nil {
		t.Fatalf("null must clear state_id: %+v", got)
	}

	// PATCH value → set again.
	rec = postAuthedJSON(t, e, http.MethodPatch, schedURL, cookie,
		fmt.Sprintf(`{"description_template":"New {{date}}","state_id":%q}`, backlogStateID))
	if rec.Code != http.StatusOK {
		t.Fatalf("patch value: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	got = decodeSchedule(t, rec.Body.Bytes())
	if got.DescriptionTemplate == nil || *got.DescriptionTemplate != "New {{date}}" {
		t.Fatalf("value must set description_template: %+v", got)
	}
	if got.StateID == nil || *got.StateID != backlogStateID {
		t.Fatalf("value must set state_id: %+v", got)
	}

	// PATCH null on a required-when-monthly field is a 400, not a
	// silent clear: monthly needs day_of_month.
	mbody := fmt.Sprintf(`{"name":"Monthly","frequency":"monthly","day_of_month":15,"hour":9,"starts_on":%q,"title_template":"Month {{date}}"}`, todayYYYYMMDD())
	rec = postAuthedJSON(t, e, http.MethodPost, base, cookie, mbody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create monthly: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	ms := decodeSchedule(t, rec.Body.Bytes())
	rec = postAuthedJSON(t, e, http.MethodPatch, base+"/"+ms.ID, cookie, `{"day_of_month":null}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("clear required day_of_month: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	// Value sets; absent leaves the value alone.
	rec = postAuthedJSON(t, e, http.MethodPatch, base+"/"+ms.ID, cookie, `{"day_of_month":20}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("set day_of_month: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if got := decodeSchedule(t, rec.Body.Bytes()); got.DayOfMonth == nil || *got.DayOfMonth != 20 {
		t.Fatalf("day_of_month = %+v", got)
	}
	rec = postAuthedJSON(t, e, http.MethodPatch, base+"/"+ms.ID, cookie, `{"name":"x"}`)
	if got := decodeSchedule(t, rec.Body.Bytes()); got.DayOfMonth == nil || *got.DayOfMonth != 20 {
		t.Fatalf("absent must keep day_of_month=20: %+v", got)
	}

	// ends_on tri-state: absent untouched, value sets, null clears.
	weekOut := time.Now().UTC().Add(7 * 24 * time.Hour).Format("2006-01-02")
	rec = postAuthedJSON(t, e, http.MethodPatch, schedURL, cookie,
		fmt.Sprintf(`{"ends_on":%q}`, weekOut))
	if rec.Code != http.StatusOK {
		t.Fatalf("set ends_on: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPatch, schedURL, cookie, `{"ends_on":null}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear ends_on: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, schedURL, cookie)
	if strings.Contains(rec.Body.String(), `"ends_on"`) {
		t.Fatalf("cleared ends_on must be absent from JSON: %s", rec.Body.String())
	}
}

// TestRecurringScheduleHTTPTenancyAndRoles covers the 404 tenancy
// boundary and guest 403s.
func TestRecurringScheduleHTTPTenancyAndRoles(t *testing.T) {
	e, cookie, slug, _, base := setupRecurringHTTP(t)

	// Second project in the same workspace.
	ident2 := uniqueProjectIdentifier("RC2")
	createProjectHTTP(t, e, cookie, slug, "Other", ident2)

	s := createDailySchedule(t, e, base, cookie, "")

	// Cross-project id → 404 schedule_not_found (no leak).
	crossURL := "/api/v1/workspaces/" + slug + "/projects/" + ident2 + "/schedules/" + s.ID
	rec := getAuthed(t, e, http.MethodGet, crossURL, cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-project get: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"schedule_not_found"`) {
		t.Fatalf("cross-project envelope: body=%s", rec.Body.String())
	}

	// Bad slug → 404 workspace-not-found, no leak.
	rec = getAuthed(t, e, http.MethodGet,
		"/api/v1/workspaces/nope-not-here/projects/XX/schedules", cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("bad slug: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"not_found"`) {
		t.Fatalf("bad slug envelope: body=%s", rec.Body.String())
	}

	// Bad project identifier (confirmed workspace) → 404 project.
	rec = getAuthed(t, e, http.MethodGet,
		"/api/v1/workspaces/"+slug+"/projects/NOPE/schedules", cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("bad project: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}

	// Guest: reads fine, every mutation (incl. run-now) 403.
	pool := newTestPool(t)
	guestCookie := loginTestUser(t, e, pool, uniqueEmail("h-rec-guest"), "test-agent/1.0", uniqueIP())
	guestID := authedUserID(t, e, guestCookie)
	rec = postAuthedJSON(t, e, http.MethodPost, "/api/v1/workspaces/"+slug+"/members", cookie,
		fmt.Sprintf(`{"user_id":%q,"role":5}`, guestID))
	if rec.Code != http.StatusOK {
		t.Fatalf("add guest: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	rec = getAuthed(t, e, http.MethodGet, base, guestCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("guest list: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, base+"/"+s.ID, guestCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("guest get: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	guestBody := fmt.Sprintf(`{"name":"g","frequency":"daily","starts_on":%q,"title_template":"T"}`, todayYYYYMMDD())
	for _, tc := range []struct {
		method, url, body string
	}{
		{http.MethodPost, base, guestBody},
		{http.MethodPatch, base + "/" + s.ID, `{"name":"x"}`},
		{http.MethodDelete, base + "/" + s.ID, ``},
		{http.MethodPost, base + "/" + s.ID + "/run-now", ``},
	} {
		rec = postAuthedJSON(t, e, tc.method, tc.url, guestCookie, tc.body)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("guest %s %s: status = %d, want 403 (body: %s)",
				tc.method, tc.url, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"code":"forbidden"`) {
			t.Fatalf("guest %s envelope: body=%s", tc.method, rec.Body.String())
		}
	}
}

// TestRecurringScheduleHTTPRunNow covers the manual fire: an issue is
// created from the templates with the caller's attribution,
// next_run_at advances, and a one-shot schedule deactivates.
func TestRecurringScheduleHTTPRunNow(t *testing.T) {
	e, cookie, _, _, base := setupRecurringHTTP(t)
	pool := newTestPool(t)
	actorID := authedUserID(t, e, cookie)

	// A label to attach, proving refs flow through run-now.
	rec := postAuthedJSON(t, e, http.MethodPost,
		strings.TrimSuffix(base, "/schedules")+"/labels", cookie, `{"name":"recurring"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create label: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var label struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &label); err != nil {
		t.Fatalf("decode label: %v", err)
	}

	body := fmt.Sprintf(`{"name":"Firable","frequency":"daily","hour":9,"starts_on":%q,"title_template":"Standup {{date}}","description_template":"Notes {{datetime}}","label_ids":[%q],"assignee_ids":[%q],"priority":2}`,
		todayYYYYMMDD(), label.ID, actorID)
	rec = postAuthedJSON(t, e, http.MethodPost, base, cookie, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	s := decodeSchedule(t, rec.Body.Bytes())
	before := s.NextRunAt

	// Fire.
	rec = postAuthedJSON(t, e, http.MethodPost, base+"/"+s.ID+"/run-now", cookie, ``)
	if rec.Code != http.StatusOK {
		t.Fatalf("run-now: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var fired struct {
		Issue    json.RawMessage       `json:"issue"`
		Schedule recurringScheduleJSON `json:"schedule"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &fired); err != nil {
		t.Fatalf("decode run-now: %v", err)
	}
	var issue struct {
		ID          string          `json:"id"`
		Name        string          `json:"name"`
		DisplayID   string          `json:"display_id"`
		Priority    int             `json:"priority"`
		CreatedBy   string          `json:"created_by"`
		Description json.RawMessage `json:"description"`
	}
	if err := json.Unmarshal(fired.Issue, &issue); err != nil {
		t.Fatalf("decode issue: %v", err)
	}
	// run-now fires AT next_run_at (which may be tomorrow if today's
	// run already passed): the rendered date must match next_run_at.
	runAtDate, err := time.Parse(time.RFC3339, s.NextRunAt)
	if err != nil {
		t.Fatalf("next_run_at parse: %v", err)
	}
	wantName := "Standup " + runAtDate.Format("2006-01-02")
	if issue.Name != wantName {
		t.Fatalf("issue name = %q, want %q", issue.Name, wantName)
	}
	if issue.DisplayID == "" || issue.CreatedBy != actorID || issue.Priority != 2 {
		t.Fatalf("issue = %+v", issue)
	}
	// The description is a real TipTap doc (paragraph per line), not a
	// JSON string — the frontend renders {type:"doc",...}.
	var descDoc struct {
		Type    string `json:"type"`
		Content []struct {
			Type    string `json:"type"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"content"`
	}
	if err := json.Unmarshal(issue.Description, &descDoc); err != nil {
		t.Fatalf("description doc decode: %v", err)
	}
	if descDoc.Type != "doc" || len(descDoc.Content) == 0 || len(descDoc.Content[0].Content) == 0 {
		t.Fatalf("description not a TipTap doc: %s", string(issue.Description))
	}
	wantDescDate := runAtDate.Format(time.RFC3339)
	if !strings.Contains(descDoc.Content[0].Content[0].Text, wantDescDate) {
		t.Fatalf("description text = %q, want rendered datetime %q",
			descDoc.Content[0].Content[0].Text, wantDescDate)
	}
	// The label and assignee really landed on the issue.
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM issue_labels WHERE issue_id = $1::uuid AND label_id = $2::uuid`,
		issue.ID, label.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("label attached: n=%d err=%v", n, err)
	}
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM issue_assignees WHERE issue_id = $1::uuid AND user_id = $2::uuid`,
		issue.ID, actorID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("assignee attached: n=%d err=%v", n, err)
	}
	// The _schedule_created activity row carries the calling user's
	// attribution and names the schedule.
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM issue_activities WHERE issue_id = $1::uuid AND field = '_schedule_created' AND actor_id = $2::uuid`,
		issue.ID, actorID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("activity attribution: n=%d err=%v", n, err)
	}

	// The schedule advanced: run_count 1, last_run_at set, next_run_at
	// moved forward.
	upd := fired.Schedule
	if upd.RunCount != 1 || upd.LastRunAt == nil {
		t.Fatalf("schedule after fire = %+v", upd)
	}
	nextAt, err := time.Parse(time.RFC3339, upd.NextRunAt)
	if err != nil {
		t.Fatalf("next_run_at parse: %v", err)
	}
	beforeAt, err := time.Parse(time.RFC3339, before)
	if err != nil {
		t.Fatalf("before parse: %v", err)
	}
	if !nextAt.After(beforeAt) {
		t.Fatalf("next_run_at not advanced: before=%s after=%s", before, upd.NextRunAt)
	}

	// run-now on unknown id → 404 schedule_not_found.
	rec = postAuthedJSON(t, e, http.MethodPost,
		base+"/00000000-0000-0000-0000-000000000000/run-now", cookie, ``)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("run-now unknown: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}

	// run-now on an inactive schedule → 400.
	rec = postAuthedJSON(t, e, http.MethodPatch, base+"/"+s.ID, cookie, `{"active":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("pause: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPost, base+"/"+s.ID+"/run-now", cookie, ``)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("run-now inactive: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

// TestRecurringScheduleHTTPRunNowExhausts: a one-shot schedule (starts
// and ends tomorrow) fires once, then deactivates with no further runs.
func TestRecurringScheduleHTTPRunNowExhausts(t *testing.T) {
	e, cookie, _, _, base := setupRecurringHTTP(t)
	tmr := tomorrowYYYYMMDD()

	body := fmt.Sprintf(`{"name":"One shot","frequency":"daily","hour":0,"starts_on":%q,"ends_on":%q,"title_template":"Once {{date}}"}`,
		tmr, tmr)
	rec := postAuthedJSON(t, e, http.MethodPost, base, cookie, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	s := decodeSchedule(t, rec.Body.Bytes())

	rec = postAuthedJSON(t, e, http.MethodPost, base+"/"+s.ID+"/run-now", cookie, ``)
	if rec.Code != http.StatusOK {
		t.Fatalf("run-now: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var fired struct {
		Schedule recurringScheduleJSON `json:"schedule"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &fired); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if fired.Schedule.Active {
		t.Fatalf("exhausted schedule must deactivate: %+v", fired.Schedule)
	}
	if fired.Schedule.RunCount != 1 {
		t.Fatalf("run_count = %d, want 1", fired.Schedule.RunCount)
	}

	// A second fire is refused: inactive now.
	rec = postAuthedJSON(t, e, http.MethodPost, base+"/"+s.ID+"/run-now", cookie, ``)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("second fire: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

// TestRecurringScheduleHTTPRunNowStaleLabel: a label deleted after the
// schedule was saved pauses the schedule on run-now with a 400 naming
// the cause (not a 500), and no issue is created.
func TestRecurringScheduleHTTPRunNowStaleLabel(t *testing.T) {
	e, cookie, slug, _, base := setupRecurringHTTP(t)
	pool := newTestPool(t)

	rec := postAuthedJSON(t, e, http.MethodPost,
		strings.TrimSuffix(base, "/schedules")+"/labels", cookie, `{"name":"doomed"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create label: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var label struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &label); err != nil {
		t.Fatalf("decode label: %v", err)
	}

	s := createDailySchedule(t, e, base, cookie,
		fmt.Sprintf(`,"label_ids":[%q]`, label.ID))

	// Delete the label out from under the schedule.
	if _, err := pool.Exec(context.Background(),
		`DELETE FROM labels WHERE id = $1::uuid`, label.ID); err != nil {
		t.Fatalf("delete label: %v", err)
	}

	rec = postAuthedJSON(t, e, http.MethodPost, base+"/"+s.ID+"/run-now", cookie, ``)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("run-now stale label: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	var apiErr struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &apiErr); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if apiErr.Error.Code != "bad_request" || !strings.Contains(apiErr.Error.Message, "label") {
		t.Fatalf("error = %+v, want bad_request naming the label", apiErr)
	}

	// No issue was created and the schedule shows as paused.
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM issues i JOIN projects p ON p.id = i.project_id
		  JOIN workspaces w ON w.id = p.workspace_id WHERE w.slug = $1`, slug).Scan(&n); err != nil || n != 0 {
		t.Fatalf("issues created = %d, want 0", n)
	}
	rec = postAuthedJSON(t, e, http.MethodGet, base+"/"+s.ID, cookie, ``)
	upd := decodeSchedule(t, rec.Body.Bytes())
	if upd.Active {
		t.Fatalf("stale-label schedule still active")
	}
}
