package api

// Route-coverage test (C7T7): docs/openapi.yaml must document every route
// the server registers. registerAllAPIRoutes mirrors cmd/glance/main.go's
// registration sequence exactly (nil pools — registration never touches
// the DB), so any new route added to main.go without a spec entry fails
// this test. The served GET /api/v1/openapi.json returns the embedded
// docs.OpenAPI bytes verbatim, so what this test parses is what clients
// get.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"

	"glance/docs"
)

var openapiParamRe = regexp.MustCompile(`:([A-Za-z_]+)`)

// echoPathToOpenAPI converts Echo's :param style to OpenAPI's {param}.
func echoPathToOpenAPI(p string) string {
	return openapiParamRe.ReplaceAllString(p, "{$1}")
}

// registerAllAPIRoutes mirrors cmd/glance/main.go (Tasks 5+, C2T7, C4T2,
// C5T0, C5T2, C7T4, C7T7). Keep the two in lockstep: a route registered
// in main.go but not here weakens this test silently.
func registerAllAPIRoutes(e *echo.Echo) {
	authHandler := &AuthHandler{}
	RegisterAuthRoutes(e, authHandler)

	RegisterWSRoutes(e, &WSHandler{})

	workspaceHandler := &WorkspaceHandler{}
	RegisterWorkspaceRoutes(e, workspaceHandler)

	projectHandler := &ProjectHandler{}
	RegisterProjectRoutes(e, projectHandler)
	RegisterImportRoutes(e, projectHandler)
	// C17T1: keep in lockstep with cmd/glance/main.go.
	RegisterProjectTemplateRoutes(e, &ProjectTemplateHandler{})
	RegisterAutomationRoutes(e, &AutomationHandler{})

	issueHandler := &IssueHandler{}
	RegisterIssueRoutes(e, issueHandler)
	RegisterWorkItemRoutes(e, issueHandler)
	RegisterTokenRoutes(e, &TokenHandler{})
	RegisterCalendarFeedRoutes(e, &CalendarFeedHandler{})
	RegisterNotifyRoutes(e, &NotifyHandler{})
	RegisterAdminRoutes(e, &AdminHandler{})
	RegisterTaxonomyRoutes(e, issueHandler)
	RegisterStateRoutes(e, &StateHandler{})
	RegisterSatelliteRoutes(e, issueHandler)
	RegisterIntakeRoutes(e, issueHandler)
	RegisterIssueLinkRoutes(e, issueHandler)
	// C17T0: keep in lockstep with cmd/glance/main.go.
	RegisterGitHubLinkRoutes(e, issueHandler)
	RegisterGitHubWebhookRoutes(e, &GitHubWebhookHandler{})
	RegisterMyWorkRoutes(e, &MyWorkHandler{})
	RegisterCycleRoutes(e, issueHandler)
	RegisterModuleRoutes(e, issueHandler)
	// C27T0: keep in lockstep with cmd/glance/main.go.
	RegisterInitiativeRoutes(e, issueHandler)
	// C28T2: keep in lockstep with cmd/glance/main.go.
	RegisterRecurringRoutes(e, projectHandler)
	RegisterPageRoutes(e, issueHandler)
	// C34T0: keep in lockstep with cmd/glance/main.go.
	RegisterProjectUpdateRoutes(e, issueHandler)
	RegisterReleaseRoutes(e, issueHandler)
	RegisterTemplateRoutes(e, issueHandler)
	RegisterCustomFieldRoutes(e, issueHandler)
	RegisterPublicRoutes(e, issueHandler)
	RegisterAnalyticsRoutes(e, issueHandler)
	RegisterTimeSummaryRoutes(e, issueHandler)
	RegisterActivityRoutes(e, issueHandler)
	RegisterOverviewRoutes(e, issueHandler)
	// C29T0: keep in lockstep with cmd/glance/main.go.
	RegisterWorkloadRoutes(e, &WorkloadHandler{})
	// C30T1: keep in lockstep with cmd/glance/main.go.
	RegisterRiskRoutes(e, &RiskHandler{})

	RegisterFavoriteRoutes(e, &FavoriteHandler{})

	RegisterAIRoutes(e, &AIHandler{})

	RegisterOpenAPIRoutes(e)
}

func loadOpenAPISpec(t *testing.T) map[string]map[string]json.RawMessage {
	t.Helper()
	var spec struct {
		OpenAPI string                                `json:"openapi"`
		Paths   map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(docs.OpenAPI, &spec); err != nil {
		t.Fatalf("embedded openapi spec is not valid JSON: %v", err)
	}
	if !strings.HasPrefix(spec.OpenAPI, "3.0.") {
		t.Fatalf("spec openapi = %q, want 3.0.x", spec.OpenAPI)
	}
	if len(spec.Paths) == 0 {
		t.Fatal("spec has no paths")
	}
	return spec.Paths
}

func TestOpenAPISpecCoversAllRoutes(t *testing.T) {
	e := echo.New()
	registerAllAPIRoutes(e)
	paths := loadOpenAPISpec(t)

	var missing []string
	for _, r := range e.Router().Routes() {
		switch r.Method {
		case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		default:
			continue // echo internal routes (echo_route_not_found)
		}
		if !strings.HasPrefix(r.Path, "/api/v1/") {
			continue // /ws, /health, SPA catch-all: out of spec scope
		}
		p := echoPathToOpenAPI(r.Path)
		ops, ok := paths[p]
		if !ok {
			missing = append(missing, r.Method+" "+p+" (path missing from spec)")
			continue
		}
		if _, ok := ops[strings.ToLower(r.Method)]; !ok {
			missing = append(missing, r.Method+" "+p+" (method missing from spec)")
		}
	}
	if len(missing) > 0 {
		t.Fatalf("openapi spec is missing %d registered routes:\n%s",
			len(missing), strings.Join(missing, "\n"))
	}
}

func TestOpenAPIOperationsHaveSummaryAndResponses(t *testing.T) {
	paths := loadOpenAPISpec(t)
	var bad []string
	for p, ops := range paths {
		for method, raw := range ops {
			var op struct {
				Summary   string         `json:"summary"`
				Responses map[string]any `json:"responses"`
			}
			if err := json.Unmarshal(raw, &op); err != nil {
				t.Fatalf("spec path %s %s: invalid operation: %v", p, method, err)
			}
			if op.Summary == "" {
				bad = append(bad, method+" "+p+" (missing summary)")
			}
			if len(op.Responses) == 0 {
				bad = append(bad, method+" "+p+" (missing responses)")
			}
		}
	}
	if len(bad) > 0 {
		t.Fatalf("spec has %d operations missing summary/responses:\n%s",
			len(bad), strings.Join(bad, "\n"))
	}
}

func TestOpenAPIEndpointServesSpec(t *testing.T) {
	e := echo.New()
	RegisterOpenAPIRoutes(e)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/openapi.json", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/openapi.json = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	var spec struct {
		OpenAPI string `json:"openapi"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &spec); err != nil {
		t.Fatalf("endpoint body is not valid JSON: %v", err)
	}
	if !strings.HasPrefix(spec.OpenAPI, "3.0.") {
		t.Fatalf("endpoint openapi = %q, want 3.0.x", spec.OpenAPI)
	}
}

// Response-shape pinning test (C53T2): for bulk-delete + the three
// versions endpoints, assert the LIVE response envelope keys match the
// documented schema in docs/openapi.yaml. The route-coverage test above
// checks paths exist, not shapes — this is the regression net that
// catches the next {deleted: integer} vs {results: [...]} drift.
// Kept to these four endpoints by design; the helper shape (decode +
// top-level key assertions) is reusable if a future cycle wants a
// wider shape audit.

func TestOpenAPIResponseShapesFourEndpoints(t *testing.T) {
	pool := newTestPool(t)
	e := testSatelliteServer(t, pool)

	cookie := loginTestUser(t, e, pool, uniqueEmail("openapi-shapes"), "test-agent", uniqueIP())
	slug := uniqueSlug("openapi-shapes")
	createWorkspaceHTTP(t, e, cookie, "Shapes Co", slug)
	ident := uniqueProjectIdentifier("OS")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	base := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/issues"

	// Guest (role 5): reads versions fine, mutating calls 403.
	guestCookie := loginTestUser(t, e, pool, uniqueEmail("openapi-shapes-guest"), "test-agent", uniqueIP())
	guestID := authedUserID(t, e, guestCookie)
	rec := postAuthedJSON(t, e, http.MethodPost, "/api/v1/workspaces/"+slug+"/members", cookie,
		fmt.Sprintf(`{"user_id":%q,"role":5}`, guestID))
	if rec.Code != http.StatusOK {
		t.Fatalf("add guest: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	decode := func(rec *httptest.ResponseRecorder, v any) {
		t.Helper()
		if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
			t.Fatalf("decode body %s: %v", rec.Body.String(), err)
		}
	}
	topKeys := func(rec *httptest.ResponseRecorder) map[string]json.RawMessage {
		t.Helper()
		var m map[string]json.RawMessage
		decode(rec, &m)
		return m
	}
	createIssue := func(name string) string {
		t.Helper()
		rec := postAuthedJSON(t, e, http.MethodPost, base, cookie,
			fmt.Sprintf(`{"name":%q}`, name))
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %q: status = %d (body: %s)", name, rec.Code, rec.Body.String())
		}
		var created struct {
			ID string `json:"id"`
		}
		decode(rec, &created)
		if created.ID == "" {
			t.Fatalf("create %q returned no id", name)
		}
		return created.ID
	}

	alpha := createIssue("Alpha")
	// PATCH writes v2, so the versions list has [v1, v2] to pin against.
	rec = postAuthedJSON(t, e, http.MethodPatch, base+"/"+alpha, cookie, `{"name":"Alpha renamed"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch Alpha: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	beta := createIssue("Beta")

	type versionShape struct {
		VersionNo int `json:"version_no"`
		Snapshot  struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"snapshot"`
		CreatedBy struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Email string `json:"email"`
		} `json:"created_by"`
		CreatedAt string `json:"created_at"`
	}

	// 1. GET versions → 200 {versions: [IssueVersion...]}, oldest first.
	//    Pins the documented envelope AND the IssueVersion schema keys:
	//    version_no / snapshot / created_by{id,name,email} / created_at —
	//    and NOT the stale {n, description, created_by: uuid} shape.
	rec = getAuthed(t, e, http.MethodGet, base+"/"+alpha+"/versions", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list versions: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var list struct {
		Versions []versionShape `json:"versions"`
	}
	decode(rec, &list)
	if len(list.Versions) != 2 ||
		list.Versions[0].VersionNo != 1 || list.Versions[1].VersionNo != 2 {
		t.Fatalf("versions = %+v, want [v1, v2]", list.Versions)
	}
	if list.Versions[0].Snapshot.Name != "Alpha" ||
		list.Versions[1].Snapshot.Name != "Alpha renamed" {
		t.Fatalf("version snapshots = %q / %q, want Alpha / Alpha renamed",
			list.Versions[0].Snapshot.Name, list.Versions[1].Snapshot.Name)
	}
	for i, v := range list.Versions {
		if v.Snapshot.ID != alpha {
			t.Fatalf("versions[%d].snapshot.id = %q, want %q", i, v.Snapshot.ID, alpha)
		}
		if v.CreatedBy.ID == "" || v.CreatedBy.Email == "" || v.CreatedAt == "" {
			t.Fatalf("versions[%d].created_by/created_at = %+v, want id+email+created_at", i, v)
		}
	}
	raw := topKeys(rec)
	if _, ok := raw["n"]; ok {
		t.Fatalf("list versions body has stale top-level key %q (body: %s)", "n", rec.Body.String())
	}

	// Guest may read the versions list.
	rec = getAuthed(t, e, http.MethodGet, base+"/"+alpha+"/versions", guestCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("guest list versions: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	// 2. GET versions/{n} → 200 bare IssueVersion (no {versions} wrap).
	rec = getAuthed(t, e, http.MethodGet, base+"/"+alpha+"/versions/1", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("get version 1: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var one versionShape
	decode(rec, &one)
	if one.VersionNo != 1 || one.Snapshot.Name != "Alpha" {
		t.Fatalf("version 1 = %+v, want v1 snapshot Alpha", one)
	}
	if _, ok := topKeys(rec)["versions"]; ok {
		t.Fatalf("get version body is wrapped in {versions} (body: %s)", rec.Body.String())
	}

	// 3. GET versions/0 → 400; GET versions/99 → 404 version not found.
	rec = getAuthed(t, e, http.MethodGet, base+"/"+alpha+"/versions/0", cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("get version 0: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, base+"/"+alpha+"/versions/99", cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get version 99: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
	var errEnv struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decode(rec, &errEnv)
	if errEnv.Error.Code != "not_found" {
		t.Fatalf("get version 99 error code = %q, want not_found", errEnv.Error.Code)
	}

	// 4. POST versions/{n}/restore: guest → 403, member → 200 Issue,
	//    and the restore lands as a new version (list grows to 3).
	rec = postAuthedJSON(t, e, http.MethodPost, base+"/"+alpha+"/versions/1/restore", guestCookie, `{}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest restore: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPost, base+"/"+alpha+"/versions/1/restore", cookie, `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("restore v1: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var restored struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	decode(rec, &restored)
	if restored.ID != alpha || restored.Name != "Alpha" {
		t.Fatalf("restored = %+v, want id %s name Alpha", restored, alpha)
	}
	rec = getAuthed(t, e, http.MethodGet, base+"/"+alpha+"/versions", cookie)
	decode(rec, &list)
	if len(list.Versions) != 3 || list.Versions[2].VersionNo != 3 {
		t.Fatalf("versions after restore = %+v, want [v1, v2, v3]", list.Versions)
	}

	// 5. POST bulk-delete → 200 {results: [{id, ok, error?}]}, NOT
	//    {deleted: integer}. Per-item: live id ok, unknown id fails.
	rec = postAuthedJSON(t, e, http.MethodPost, base+"/bulk-delete", cookie,
		fmt.Sprintf(`{"ids":[%q,"00000000-0000-0000-0000-000000000000"]}`, beta))
	if rec.Code != http.StatusOK {
		t.Fatalf("bulk-delete: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	keys := topKeys(rec)
	if _, ok := keys["deleted"]; ok {
		t.Fatalf("bulk-delete body has stale key %q (body: %s)", "deleted", rec.Body.String())
	}
	var bulk struct {
		Results []struct {
			ID    string  `json:"id"`
			OK    bool    `json:"ok"`
			Error *string `json:"error"`
		} `json:"results"`
	}
	decode(rec, &bulk)
	if len(bulk.Results) != 2 {
		t.Fatalf("bulk-delete results = %+v, want 2 items", bulk.Results)
	}
	if bulk.Results[0].ID != beta || !bulk.Results[0].OK || bulk.Results[0].Error != nil {
		t.Fatalf("bulk-delete results[0] = %+v, want {id %s, ok true, no error}", bulk.Results[0], beta)
	}
	if bulk.Results[1].OK || bulk.Results[1].Error == nil || *bulk.Results[1].Error == "" {
		t.Fatalf("bulk-delete results[1] = %+v, want ok false with an error", bulk.Results[1])
	}
	rec = getAuthed(t, e, http.MethodGet, base+"/"+beta, cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get bulk-deleted Beta: status = %d, want 404", rec.Code)
	}

	// 6. Bulk-delete validation + role pins: empty ids → 400, guest → 403.
	rec = postAuthedJSON(t, e, http.MethodPost, base+"/bulk-delete", cookie, `{"ids":[]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bulk-delete empty ids: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPost, base+"/bulk-delete", guestCookie,
		fmt.Sprintf(`{"ids":[%q]}`, alpha))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest bulk-delete: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
}
