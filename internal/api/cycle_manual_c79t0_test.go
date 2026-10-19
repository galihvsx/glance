package api

// Manual start/end HTTP envelope tests (C79T0): the POST
// .../cycles/{id}/start and .../cycles/{id}/end endpoints — 200/400/404
// envelopes, the 409 overlap shape, and the guest 403 gate — plus a
// doc-side pin that both endpoints and the start 409 shape are in the
// OpenAPI spec. Real test database, no skips.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"glance/docs"
)

func manualHTTPSetup(t *testing.T, prefix string) (*echo.Echo, *pgxpool.Pool, *http.Cookie, string, string) {
	t.Helper()
	pool := newTestPool(t)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterCycleRoutes(e, &IssueHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail(prefix), "test-agent", uniqueIP())
	slug := uniqueSlug(prefix)
	createWorkspaceHTTP(t, e, cookie, "Manual Co", slug)
	ident := uniqueProjectIdentifier("MS")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	return e, pool, cookie, slug, ident
}

func createCycleHTTP(t *testing.T, e *echo.Echo, cookie *http.Cookie, base, name, start, end string) string {
	t.Helper()
	rec := postAuthedJSON(t, e, http.MethodPost, base, cookie,
		fmt.Sprintf(`{"name":%q,"start_date":%q,"end_date":%q}`, name, start, end))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create cycle %q: status = %d (body: %s)", name, rec.Code, rec.Body.String())
	}
	var created struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode cycle %q: %v", name, err)
	}
	return created.ID
}

func cycleStatusHTTP(t *testing.T, e *echo.Echo, cookie *http.Cookie, base, id string) string {
	t.Helper()
	rec := getAuthed(t, e, http.MethodGet, base+"/"+id, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("get cycle: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var got struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	return got.Status
}

// TestCycleManualStartEndHTTP walks the happy path plus every error
// envelope on both endpoints: start 200 → repeat start 400 → end 200 →
// repeat end 400 → start on completed 400; unknown ids 404; malformed
// ids 400.
func TestCycleManualStartEndHTTP(t *testing.T) {
	e, _, cookie, slug, ident := manualHTTPSetup(t, "cycle-manual")
	cycleBase := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/cycles"
	now := time.Now()
	future := func(off int) string { return now.AddDate(0, 0, off).Format("2006-01-02") }

	id := createCycleHTTP(t, e, cookie, cycleBase, "Sprint 9", future(7), future(21))
	if got := cycleStatusHTTP(t, e, cookie, cycleBase, id); got != "upcoming" {
		t.Fatalf("seed status = %q, want upcoming", got)
	}

	// Start → 200, bare Cycle with status current.
	rec := postAuthedJSON(t, e, http.MethodPost, cycleBase+"/"+id+"/start", cookie, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("start: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var started struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &started); err != nil {
		t.Fatalf("decode start: %v", err)
	}
	if started.ID != id || started.Status != "current" {
		t.Fatalf("start body = %+v, want id %s status current", started, id)
	}

	// Repeat start on current → 400 bad_request envelope.
	rec = postAuthedJSON(t, e, http.MethodPost, cycleBase+"/"+id+"/start", cookie, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("repeat start: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"bad_request"`) {
		t.Fatalf("repeat start envelope: body=%s", rec.Body.String())
	}

	// End → 200, bare Cycle with status completed + frozen snapshot.
	rec = postAuthedJSON(t, e, http.MethodPost, cycleBase+"/"+id+"/end", cookie, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("end: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var ended struct {
		ID               string           `json:"id"`
		Status           string           `json:"status"`
		ProgressSnapshot map[string]int64 `json:"progress_snapshot"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ended); err != nil {
		t.Fatalf("decode end: %v", err)
	}
	if ended.ID != id || ended.Status != "completed" {
		t.Fatalf("end body = %+v, want id %s status completed", ended, id)
	}
	if len(ended.ProgressSnapshot) == 0 {
		t.Fatalf("end body has no progress_snapshot")
	}

	// Repeat end → 400 bad_request (not a silent no-op).
	rec = postAuthedJSON(t, e, http.MethodPost, cycleBase+"/"+id+"/end", cookie, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("repeat end: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"bad_request"`) {
		t.Fatalf("repeat end envelope: body=%s", rec.Body.String())
	}

	// Start on completed → 400.
	rec = postAuthedJSON(t, e, http.MethodPost, cycleBase+"/"+id+"/start", cookie, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("start completed: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// Unknown ids → 404 envelopes on both endpoints.
	unknown := "00000000-0000-0000-0000-000000000000"
	for _, action := range []string{"start", "end"} {
		rec = postAuthedJSON(t, e, http.MethodPost, cycleBase+"/"+unknown+"/"+action, cookie, "")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s unknown: status = %d, want 404 (body: %s)", action, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"code":"not_found"`) {
			t.Fatalf("%s unknown envelope: body=%s", action, rec.Body.String())
		}
	}

	// Malformed ids → 400 bad_request, never 500.
	for _, action := range []string{"start", "end"} {
		rec = postAuthedJSON(t, e, http.MethodPost, cycleBase+"/not-a-uuid/"+action, cookie, "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s malformed: status = %d, want 400 (body: %s)", action, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"code":"bad_request"`) {
			t.Fatalf("%s malformed envelope: body=%s", action, rec.Body.String())
		}
	}
}

// TestCycleManualStartOverlapHTTP pins the 409: toggle off, a live
// cycle "Sprint 12", and an upcoming cycle on an overlapping range —
// the manual start names the conflicting cycle in the message.
func TestCycleManualStartOverlapHTTP(t *testing.T) {
	e, pool, cookie, slug, ident := manualHTTPSetup(t, "cycle-manual-409")
	cycleBase := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/cycles"
	now := time.Now()
	f := func(off int) string { return now.AddDate(0, 0, off).Format("2006-01-02") }

	_ = createCycleHTTP(t, e, cookie, cycleBase, "Sprint 12", f(-7), f(7))

	// The overlapping upcoming row is inserted directly: creating it
	// through the API would 409, and this test isolates the start-time
	// check from the create-time one.
	var projectID string
	if err := pool.QueryRow(t.Context(),
		`SELECT p.id::text FROM projects p JOIN workspaces w ON w.id = p.workspace_id
		 WHERE w.slug = $1 AND p.identifier = $2`, slug, ident).Scan(&projectID); err != nil {
		t.Fatalf("project id: %v", err)
	}
	var upcomingID string
	if err := pool.QueryRow(t.Context(),
		`INSERT INTO cycles (project_id, name, start_date, end_date, status)
		 VALUES ($1::uuid, 'Sprint 13', $2::date, $3::date, 'upcoming')
		 RETURNING id::text`,
		projectID, f(3), f(17)).Scan(&upcomingID); err != nil {
		t.Fatalf("insert upcoming: %v", err)
	}

	rec := postAuthedJSON(t, e, http.MethodPost, cycleBase+"/"+upcomingID+"/start", cookie, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("start overlapping: status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"conflict"`) {
		t.Fatalf("409 envelope: body=%s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `cycle dates overlap with`) ||
		!strings.Contains(rec.Body.String(), "Sprint 12") {
		t.Fatalf("409 must name the conflicting cycle: body=%s", rec.Body.String())
	}

	// A disjoint upcoming cycle starts fine — the 409 is range-scoped,
	// not project-scoped.
	disjoint := createCycleHTTP(t, e, cookie, cycleBase, "Sprint 20", f(30), f(44))
	rec = postAuthedJSON(t, e, http.MethodPost, cycleBase+"/"+disjoint+"/start", cookie, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("start disjoint: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
}

// TestCycleManualGuestForbiddenHTTP: guests (5) are read-only — both
// manual endpoints are 403, and the member can still act afterwards.
func TestCycleManualGuestForbiddenHTTP(t *testing.T) {
	e, pool, cookie, slug, ident := manualHTTPSetup(t, "cycle-manual-guest")
	cycleBase := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/cycles"
	now := time.Now()
	f := func(off int) string { return now.AddDate(0, 0, off).Format("2006-01-02") }

	id := createCycleHTTP(t, e, cookie, cycleBase, "Sprint 9", f(7), f(21))

	guestCookie := loginTestUser(t, e, pool, uniqueEmail("cycle-manual-guest-2"), "test-agent", uniqueIP())
	guestID := authedUserID(t, e, guestCookie)
	rec := postAuthedJSON(t, e, http.MethodPost, "/api/v1/workspaces/"+slug+"/members", cookie,
		fmt.Sprintf(`{"user_id":%q,"role":5}`, guestID))
	if rec.Code != http.StatusOK {
		t.Fatalf("add guest: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	for _, action := range []string{"start", "end"} {
		rec = postAuthedJSON(t, e, http.MethodPost, cycleBase+"/"+id+"/"+action, guestCookie, "")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("guest %s: status = %d, want 403 (body: %s)", action, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"code":"forbidden"`) {
			t.Fatalf("guest %s envelope: body=%s", action, rec.Body.String())
		}
	}

	// The cycle is untouched; the member starts it fine afterwards.
	rec = postAuthedJSON(t, e, http.MethodPost, cycleBase+"/"+id+"/start", cookie, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("member start after guest 403: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if got := cycleStatusHTTP(t, e, cookie, cycleBase, id); got != "current" {
		t.Fatalf("status = %q, want current", got)
	}
}

// TestOpenAPIDocShapesCycleManualStartEnd (doc-side, no DB): both
// endpoints are documented with summaries and response shapes; the
// start op pins the 409 cycle-overlap error with the shared Error
// schema — the same pin create/update carry (C78T0).
func TestOpenAPIDocShapesCycleManualStartEnd(t *testing.T) {
	var spec struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(docs.OpenAPI, &spec); err != nil {
		t.Fatalf("embedded openapi spec is not valid JSON: %v", err)
	}
	for _, path := range []string{
		"/api/v1/workspaces/{slug}/projects/{identifier}/cycles/{cycleID}/start",
		"/api/v1/workspaces/{slug}/projects/{identifier}/cycles/{cycleID}/end",
	} {
		ops, ok := spec.Paths[path]
		if !ok {
			t.Fatalf("spec has no path %s", path)
		}
		raw, ok := ops["post"]
		if !ok {
			t.Fatalf("spec path %s has no post operation", path)
		}
		var op struct {
			Summary   string                     `json:"summary"`
			Responses map[string]json.RawMessage `json:"responses"`
		}
		if err := json.Unmarshal(raw, &op); err != nil {
			t.Fatalf("decode %s post: %v", path, err)
		}
		if op.Summary == "" {
			t.Fatalf("%s post has no summary", path)
		}
		for _, code := range []string{"200", "400", "403", "404"} {
			if _, ok := op.Responses[code]; !ok {
				t.Fatalf("%s post responses lack %s", path, code)
			}
		}
	}

	// The start 409 pins the overlap shape: description names the
	// conflicting cycle, body uses the shared Error schema.
	var startOp struct {
		Responses map[string]json.RawMessage `json:"responses"`
	}
	if err := json.Unmarshal(
		spec.Paths["/api/v1/workspaces/{slug}/projects/{identifier}/cycles/{cycleID}/start"]["post"],
		&startOp); err != nil {
		t.Fatalf("decode start op: %v", err)
	}
	raw409, ok := startOp.Responses["409"]
	if !ok {
		t.Fatalf("start operation responses lack 409 (cycle-overlap rejection)")
	}
	var conflict struct {
		Description string `json:"description"`
		Content     struct {
			JSON struct {
				Schema struct {
					Ref string `json:"$ref"`
				} `json:"schema"`
			} `json:"application/json"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw409, &conflict); err != nil {
		t.Fatalf("decode start 409: %v", err)
	}
	if !strings.Contains(conflict.Description, "names the conflicting cycle") {
		t.Fatalf("start 409 description = %q, want it to name the conflicting cycle", conflict.Description)
	}
	if conflict.Content.JSON.Schema.Ref != "#/components/schemas/Error" {
		t.Fatalf("start 409 schema $ref = %q, want #/components/schemas/Error",
			conflict.Content.JSON.Schema.Ref)
	}
}
