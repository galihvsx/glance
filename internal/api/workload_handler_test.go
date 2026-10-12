package api

// Workload HTTP endpoint tests (C29T0): GET report shape + tenancy +
// project scoping, PUT capacity role gating + validation + error
// mapping. Aggregation math itself is covered in the service tests;
// here we assert routing, auth, and the envelope. Real test database,
// no skips.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
)

type workloadHTTPFixture struct {
	e        *echo.Echo
	pool     *pgxpool.Pool
	admin    *http.Cookie
	member   *http.Cookie
	memberID string
	guest    *http.Cookie
	outsider *http.Cookie
	slug     string
	ident    string
	base     string // .../workspaces/<slug>/workload
	projID   string
}

func setupWorkloadHTTP(t *testing.T) *workloadHTTPFixture {
	t.Helper()
	fx := &workloadHTTPFixture{}
	fx.pool = newTestPool(t)
	fx.e = testServer(t, fx.pool)
	RegisterWorkspaceRoutes(fx.e, &WorkspaceHandler{Pool: fx.pool})
	RegisterProjectRoutes(fx.e, &ProjectHandler{Pool: fx.pool})
	RegisterIssueRoutes(fx.e, &IssueHandler{Pool: fx.pool})
	RegisterWorkloadRoutes(fx.e, &WorkloadHandler{Pool: fx.pool})

	fx.admin = loginTestUser(t, fx.e, fx.pool, uniqueEmail("wl-admin"), "ua", uniqueIP())
	fx.slug = uniqueSlug("wl-http")
	createWorkspaceHTTP(t, fx.e, fx.admin, "Workload Co", fx.slug)
	fx.ident = uniqueProjectIdentifier("WL")
	createProjectHTTP(t, fx.e, fx.admin, fx.slug, "Engineering", fx.ident)
	fx.base = "/api/v1/workspaces/" + fx.slug + "/workload"

	if err := fx.pool.QueryRow(context.Background(),
		`SELECT id::text FROM projects WHERE workspace_id =
			(SELECT id FROM workspaces WHERE slug = $1)`, fx.slug).Scan(&fx.projID); err != nil {
		t.Fatalf("project id: %v", err)
	}

	addRole := func(email string, role int) (*http.Cookie, string) {
		cookie := loginTestUser(t, fx.e, fx.pool, uniqueEmail(email), "ua", uniqueIP())
		id := authedUserID(t, fx.e, cookie)
		rec := postAuthedJSON(t, fx.e, http.MethodPost,
			"/api/v1/workspaces/"+fx.slug+"/members", fx.admin,
			fmt.Sprintf(`{"user_id":%q,"role":%d}`, id, role))
		if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
			t.Fatalf("upsertMember role %d: status = %d (body: %s)", role, rec.Code, rec.Body.String())
		}
		return cookie, id
	}
	fx.member, fx.memberID = addRole("wl-member", 15)
	fx.guest, _ = addRole("wl-guest", 5)
	fx.outsider = loginTestUser(t, fx.e, fx.pool, uniqueEmail("wl-out"), "ua", uniqueIP())
	return fx
}

type workloadReportJSON struct {
	Members []struct {
		UserID               string `json:"user_id"`
		DisplayName          string `json:"display_name"`
		Role                 int    `json:"role"`
		WeeklyCapacityPoints *int   `json:"weekly_capacity_points"`
		OpenCount            int64  `json:"open_count"`
		OpenPoints           int64  `json:"open_points"`
		OverdueCount         int64  `json:"overdue_count"`
		Priority             struct {
			Urgent int64 `json:"urgent"`
			High   int64 `json:"high"`
			Medium int64 `json:"medium"`
			Low    int64 `json:"low"`
			None   int64 `json:"none"`
		} `json:"priority"`
		Completed14d int64    `json:"completed_14d"`
		Overloaded   bool     `json:"overloaded"`
		ProjectIDs   []string `json:"project_ids"`
	} `json:"members"`
	Unassigned []struct {
		ProjectID         string `json:"project_id"`
		ProjectIdentifier string `json:"project_identifier"`
		OpenCount         int64  `json:"open_count"`
	} `json:"unassigned"`
	GeneratedAt string `json:"generated_at"`
}

func getWorkload(t *testing.T, fx *workloadHTTPFixture, cookie *http.Cookie, query string) (int, workloadReportJSON) {
	t.Helper()
	rec := getAuthed(t, fx.e, http.MethodGet, fx.base+query, cookie)
	var rep workloadReportJSON
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
			t.Fatalf("decode workload: %v", err)
		}
	}
	return rec.Code, rep
}

// TestWorkloadHTTPReportShape covers the GET happy path: every member
// listed once, the full response shape present, project scoping.
func TestWorkloadHTTPReportShape(t *testing.T) {
	fx := setupWorkloadHTTP(t)

	// Seed one open issue assigned to the member via the issue API.
	issueBase := "/api/v1/workspaces/" + fx.slug + "/projects/" + fx.ident + "/issues"
	issueID := createIssueHTTP(t, fx.e, fx.admin, issueBase, "seeded work")
	if _, err := fx.pool.Exec(context.Background(),
		`INSERT INTO issue_assignees (issue_id, user_id) VALUES ($1::uuid, $2::uuid)`,
		issueID, fx.memberID); err != nil {
		t.Fatalf("assign: %v", err)
	}

	code, rep := getWorkload(t, fx, fx.admin, "")
	if code != http.StatusOK {
		t.Fatalf("GET workload = %d", code)
	}
	if len(rep.Members) != 3 {
		t.Fatalf("members = %d, want 3", len(rep.Members))
	}
	if rep.GeneratedAt == "" {
		t.Fatalf("generated_at missing")
	}
	var memberRow *struct {
		UserID               string `json:"user_id"`
		DisplayName          string `json:"display_name"`
		Role                 int    `json:"role"`
		WeeklyCapacityPoints *int   `json:"weekly_capacity_points"`
		OpenCount            int64  `json:"open_count"`
		OpenPoints           int64  `json:"open_points"`
		OverdueCount         int64  `json:"overdue_count"`
		Priority             struct {
			Urgent int64 `json:"urgent"`
			High   int64 `json:"high"`
			Medium int64 `json:"medium"`
			Low    int64 `json:"low"`
			None   int64 `json:"none"`
		} `json:"priority"`
		Completed14d int64    `json:"completed_14d"`
		Overloaded   bool     `json:"overloaded"`
		ProjectIDs   []string `json:"project_ids"`
	}
	for i := range rep.Members {
		if rep.Members[i].UserID == fx.memberID {
			memberRow = &rep.Members[i]
		}
	}
	if memberRow == nil {
		t.Fatalf("member row missing")
	}
	if memberRow.OpenCount != 1 || memberRow.ProjectIDs == nil || len(memberRow.ProjectIDs) != 1 {
		t.Fatalf("member row = %+v", memberRow)
	}
	if len(rep.Unassigned) != 1 || rep.Unassigned[0].ProjectIdentifier != fx.ident {
		t.Fatalf("unassigned = %+v", rep.Unassigned)
	}
	if rep.Unassigned[0].OpenCount != 0 { // the only issue is assigned
		t.Fatalf("unassigned open = %d, want 0", rep.Unassigned[0].OpenCount)
	}

	// Project scoping: the known project id works; an unknown-but-valid
	// uuid is a 404; a malformed one is a 400.
	if code, _ := getWorkload(t, fx, fx.admin, "?project="+fx.projID); code != http.StatusOK {
		t.Fatalf("scoped GET = %d, want 200", code)
	}
	rec := getAuthed(t, fx.e, http.MethodGet, fx.base+"?project=123e4567-e89b-12d3-a456-426614174000", fx.admin)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("foreign project = %d, want 404", rec.Code)
	}
	rec = getAuthed(t, fx.e, http.MethodGet, fx.base+"?project=not-a-uuid", fx.admin)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed project = %d, want 400", rec.Code)
	}
	assertErrorCode(t, rec, "bad_request")
}

// TestWorkloadHTTPTenancy: guests may read; outsiders and bad slugs
// get 404 (never a leak).
func TestWorkloadHTTPTenancy(t *testing.T) {
	fx := setupWorkloadHTTP(t)

	if code, _ := getWorkload(t, fx, fx.guest, ""); code != http.StatusOK {
		t.Fatalf("guest GET = %d, want 200", code)
	}
	rec := getAuthed(t, fx.e, http.MethodGet, fx.base, fx.outsider)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("outsider GET = %d, want 404", rec.Code)
	}
	assertErrorCode(t, rec, "not_found")
	rec = getAuthed(t, fx.e, http.MethodGet, "/api/v1/workspaces/nope-nothing/workload", fx.admin)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("bad slug GET = %d, want 404", rec.Code)
	}
}

// TestCapacityHTTPRoles: admin and member (15) may write; guest is
// 403; outsider and bad slug are 404.
func TestCapacityHTTPRoles(t *testing.T) {
	fx := setupWorkloadHTTP(t)
	url := "/api/v1/workspaces/" + fx.slug + "/members/" + fx.memberID + "/capacity"

	rec := postAuthedJSON(t, fx.e, http.MethodPut, url, fx.admin, `{"weekly_capacity_points": 20}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin PUT = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var got struct {
		UserID               string `json:"user_id"`
		WeeklyCapacityPoints *int   `json:"weekly_capacity_points"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode PUT: %v", err)
	}
	if got.UserID != fx.memberID || got.WeeklyCapacityPoints == nil || *got.WeeklyCapacityPoints != 20 {
		t.Fatalf("PUT body = %+v", got)
	}
	// The GET report reflects the write.
	_, rep := getWorkload(t, fx, fx.admin, "")
	for _, m := range rep.Members {
		if m.UserID == fx.memberID && (m.WeeklyCapacityPoints == nil || *m.WeeklyCapacityPoints != 20) {
			t.Fatalf("report capacity = %v, want 20", m.WeeklyCapacityPoints)
		}
	}

	// Member (15) may write too.
	rec = postAuthedJSON(t, fx.e, http.MethodPut, url, fx.member, `{"weekly_capacity_points": null}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("member PUT null = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"weekly_capacity_points":null`) {
		t.Fatalf("PUT null body = %s", rec.Body.String())
	}

	// Guest → 403.
	rec = postAuthedJSON(t, fx.e, http.MethodPut, url, fx.guest, `{"weekly_capacity_points": 5}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest PUT = %d, want 403", rec.Code)
	}
	assertErrorCode(t, rec, "forbidden")

	// Outsider → 404 (tenancy, never a leak).
	rec = postAuthedJSON(t, fx.e, http.MethodPut, url, fx.outsider, `{"weekly_capacity_points": 5}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("outsider PUT = %d, want 404", rec.Code)
	}
	assertErrorCode(t, rec, "not_found")

	// Bad slug → 404.
	rec = postAuthedJSON(t, fx.e, http.MethodPut,
		"/api/v1/workspaces/nope-nothing/members/"+fx.memberID+"/capacity", fx.admin,
		`{"weekly_capacity_points": 5}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("bad slug PUT = %d, want 404", rec.Code)
	}
}

// TestCapacityHTTPValidation: bounds, null-vs-absent, malformed ids
// and bodies, unknown member.
func TestCapacityHTTPValidation(t *testing.T) {
	fx := setupWorkloadHTTP(t)
	url := "/api/v1/workspaces/" + fx.slug + "/members/" + fx.memberID + "/capacity"

	for _, bad := range []string{`-1`, `1000`, `1.5`, `"lots"`} {
		rec := postAuthedJSON(t, fx.e, http.MethodPut, url, fx.admin,
			`{"weekly_capacity_points": `+bad+`}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("PUT %s = %d, want 400 (body: %s)", bad, rec.Code, rec.Body.String())
		}
		assertErrorCode(t, rec, "bad_request")
	}
	// Bounds are inclusive.
	for _, ok := range []string{`0`, `999`} {
		rec := postAuthedJSON(t, fx.e, http.MethodPut, url, fx.admin,
			`{"weekly_capacity_points": `+ok+`}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("PUT %s = %d, want 200", ok, rec.Code)
		}
	}
	// Absent field → 400; explicit null → 200 (clear).
	rec := postAuthedJSON(t, fx.e, http.MethodPut, url, fx.admin, `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT absent = %d, want 400", rec.Code)
	}
	rec = postAuthedJSON(t, fx.e, http.MethodPut, url, fx.admin, `not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT garbage = %d, want 400", rec.Code)
	}

	// Malformed user id → 400; well-formed but non-member → 404 member_not_found.
	rec = postAuthedJSON(t, fx.e, http.MethodPut,
		"/api/v1/workspaces/"+fx.slug+"/members/nope/capacity", fx.admin,
		`{"weekly_capacity_points": 5}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT bad user id = %d, want 400", rec.Code)
	}
	rec = postAuthedJSON(t, fx.e, http.MethodPut,
		"/api/v1/workspaces/"+fx.slug+"/members/123e4567-e89b-12d3-a456-426614174000/capacity", fx.admin,
		`{"weekly_capacity_points": 5}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("PUT unknown member = %d, want 404", rec.Code)
	}
	assertErrorCode(t, rec, "member_not_found")
}
