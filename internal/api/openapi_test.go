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
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"

	"glance/docs"
	"glance/internal/service"
	"glance/internal/store"
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
	// C68T0: keep in lockstep with cmd/glance/main.go.
	RegisterWorkflowRoutes(e, &WorkflowHandler{})
	RegisterSatelliteRoutes(e, issueHandler)
	RegisterIntakeRoutes(e, issueHandler)
	RegisterIssueLinkRoutes(e, issueHandler)
	// C17T0: keep in lockstep with cmd/glance/main.go.
	RegisterGitHubLinkRoutes(e, issueHandler)
	RegisterGitHubWebhookRoutes(e, &GitHubWebhookHandler{})
	RegisterMyWorkRoutes(e, &MyWorkHandler{})
	RegisterCycleRoutes(e, issueHandler)
	RegisterModuleRoutes(e, issueHandler)
	// C71T0: keep in lockstep with cmd/glance/main.go.
	RegisterEpicRoutes(e, issueHandler)
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
	// C81T0: keep in lockstep with cmd/glance/main.go.
	RegisterDashboardRoutes(e, &DashboardHandler{})
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

	// 4b. No-op restore: the live row now equals v3's snapshot, so
	// restoring v3 is meaningless → 409 conflict, no version written.
	rec = postAuthedJSON(t, e, http.MethodPost, base+"/"+alpha+"/versions/3/restore", cookie, `{}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("no-op restore v3: status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	decode(rec, &errEnv)
	if errEnv.Error.Code != "conflict" {
		t.Fatalf("no-op restore v3 error code = %q, want conflict", errEnv.Error.Code)
	}
	rec = getAuthed(t, e, http.MethodGet, base+"/"+alpha+"/versions", cookie)
	decode(rec, &list)
	if len(list.Versions) != 3 {
		t.Fatalf("versions after no-op restore = %+v, want still [v1, v2, v3]", list.Versions)
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

// Doc-side pinning test (C64T0): POST .../issues/{uuid}/versions/{n}/restore
// documents the 409 no-op rejection (service.ErrVersionRestoreNoOp): a
// restore whose snapshot is identical to the live row writes no version
// and no _restored marker. This is the doc-side complement to the live
// 409 pin in TestOpenAPIResponseShapesFourEndpoints §4b. No DB: parses
// the embedded spec.
func TestOpenAPIDocShapesVersionRestoreNoOp(t *testing.T) {
	var spec struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(docs.OpenAPI, &spec); err != nil {
		t.Fatalf("embedded openapi spec is not valid JSON: %v", err)
	}
	const path = "/api/v1/workspaces/{slug}/projects/{identifier}/issues/{uuid}/versions/{n}/restore"
	ops, ok := spec.Paths[path]
	if !ok {
		t.Fatalf("spec has no path %s", path)
	}
	var op struct {
		Responses map[string]json.RawMessage `json:"responses"`
	}
	if err := json.Unmarshal(ops["post"], &op); err != nil {
		t.Fatalf("decode restore operation: %v", err)
	}
	if _, ok := op.Responses["409"]; !ok {
		t.Fatalf("restore operation responses lack 409 (no-op restore rejection): %v", op.Responses)
	}
	var conflict struct {
		Ref string `json:"$ref"`
	}
	if err := json.Unmarshal(op.Responses["409"], &conflict); err != nil {
		t.Fatalf("decode restore 409 response: %v", err)
	}
	if conflict.Ref != "#/components/responses/Conflict" {
		t.Fatalf("restore 409 $ref = %q, want #/components/responses/Conflict", conflict.Ref)
	}
}

// Response-shape pinning test (C54T0): for the issue-family schemas
// (Issue / IssueDetail / IssueCreate / IssuePatch / SimilarIssue),
// assert the LIVE response keys match the corrected docs/openapi.yaml.
// Pins the C54T0 drift fixes: project_id + created_by present,
// state_id a non-null string, NO archived key on any response,
// assignees = [{id, name}] (not User), labels = [{id, name, color}]
// (not Label), children = IssueChild summaries (not Issue),
// custom_values = {field_id: CustomValue}, project_structure gated
// by its flag, deleted_at trash-only, and release_id accepted by
// PATCH (tri-state) with the effect visible via project_structure.

func TestOpenAPIResponseShapesIssueFamily(t *testing.T) {
	pool := newTestPool(t)
	e := testSatelliteServer(t, pool)
	// testSatelliteServer registers the issue/satellite routes only;
	// the pins below also hit the refs, release and custom-field
	// routes, so mount them the same way other tests do.
	h := &IssueHandler{Pool: pool}
	RegisterGitHubLinkRoutes(e, h)
	RegisterReleaseRoutes(e, h)
	RegisterCustomFieldRoutes(e, h)

	cookie := loginTestUser(t, e, pool, uniqueEmail("openapi-issue"), "test-agent", uniqueIP())
	myID := authedUserID(t, e, cookie)
	slug := uniqueSlug("openapi-issue")
	createWorkspaceHTTP(t, e, cookie, "Issue Co", slug)
	ident := uniqueProjectIdentifier("OI")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	projBase := "/api/v1/workspaces/" + slug + "/projects/" + ident
	base := projBase + "/issues"

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
	// mustHave/mustNotHave pin the exact key contract of one response
	// object: a key the doc promises must be present, a key the doc
	// says is absent (omitempty / flag-gated / request-only) must not
	// appear at all — not even as null.
	mustHave := func(t *testing.T, m map[string]json.RawMessage, keys ...string) {
		t.Helper()
		for _, k := range keys {
			if _, ok := m[k]; !ok {
				t.Fatalf("response missing key %q (keys: %v)", k, rawKeysOf(m))
			}
		}
	}
	mustNotHave := func(t *testing.T, m map[string]json.RawMessage, keys ...string) {
		t.Helper()
		for _, k := range keys {
			if _, ok := m[k]; ok {
				t.Fatalf("response has forbidden key %q (body keys: %v)", k, rawKeysOf(m))
			}
		}
	}
	exactKeys := func(t *testing.T, m map[string]json.RawMessage, keys ...string) {
		t.Helper()
		mustHave(t, m, keys...)
		if len(m) != len(keys) {
			t.Fatalf("response keys = %v, want exactly %v", rawKeysOf(m), keys)
		}
	}
	nonEmptyString := func(t *testing.T, m map[string]json.RawMessage, key string) string {
		t.Helper()
		var s string
		if err := json.Unmarshal(m[key], &s); err != nil || s == "" {
			t.Fatalf("key %q = %s, want a non-empty string", key, string(m[key]))
		}
		return s
	}
	createIssue := func(body string) string {
		t.Helper()
		rec := postAuthedJSON(t, e, http.MethodPost, base, cookie, body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s: status = %d (body: %s)", body, rec.Code, rec.Body.String())
		}
		var created struct {
			ID string `json:"id"`
		}
		decode(rec, &created)
		if created.ID == "" {
			t.Fatalf("create %s returned no id", body)
		}
		return created.ID
	}
	getIssueKeys := func(id, query string) map[string]json.RawMessage {
		t.Helper()
		rec := getAuthed(t, e, http.MethodGet, base+"/"+id+query, cookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("get %s%s: status = %d (body: %s)", id, query, rec.Code, rec.Body.String())
		}
		return topKeys(rec)
	}

	// 1. POST create → 201 bare Issue: project_id + created_by
	// present, state_id a non-null string, NO archived / assignees /
	// labels / subtask_progress / archived_at / deleted_at keys.
	rec := postAuthedJSON(t, e, http.MethodPost, base, cookie,
		`{"name":"Alpha","description":{"type":"doc","content":[]}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create Alpha: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	created := topKeys(rec)
	mustHave(t, created, "id", "project_id", "sequence_id", "display_id",
		"name", "description", "priority", "state_id", "sort_order",
		"is_draft", "created_by", "created_at", "updated_at")
	mustNotHave(t, created, "archived", "assignees", "labels",
		"subtask_progress", "archived_at", "deleted_at",
		"parent_id", "start_date", "target_date", "estimate_point_id")
	alpha := nonEmptyString(t, created, "id")
	nonEmptyString(t, created, "project_id")
	nonEmptyString(t, created, "created_by")
	nonEmptyString(t, created, "state_id")
	if !strings.HasPrefix(nonEmptyString(t, created, "display_id"), ident+"-") {
		t.Fatalf("display_id = %s, want prefix %q", string(created["display_id"]), ident+"-")
	}
	var desc struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(created["description"], &desc); err != nil || desc.Type != "doc" {
		t.Fatalf("description = %s, want the sent rich-text doc", string(created["description"]))
	}

	// 2. GET detail (no flags) → IssueListItem: assignees/labels/
	// subtask_progress present (empty), NO archived / archived_at /
	// deleted_at, NO flag-gated attachments, and NO external_refs
	// (omitempty drops the empty array — it appears only once a
	// ref is linked; see 2b).
	detail := getIssueKeys(alpha, "")
	mustHave(t, detail, "id", "project_id", "created_by", "state_id",
		"assignees", "labels", "subtask_progress")
	mustNotHave(t, detail, "archived", "archived_at", "deleted_at",
		"children", "custom_values", "project_structure", "external_refs")
	decodeDetail := func(raw json.RawMessage, v any) {
		t.Helper()
		if err := json.Unmarshal(raw, v); err != nil {
			t.Fatalf("decode %s: %v", string(raw), err)
		}
	}
	var assignees []map[string]json.RawMessage
	decodeDetail(detail["assignees"], &assignees)
	if len(assignees) != 0 {
		t.Fatalf("assignees = %v, want []", assignees)
	}
	var labels []map[string]json.RawMessage
	decodeDetail(detail["labels"], &labels)
	if len(labels) != 0 {
		t.Fatalf("labels = %v, want []", labels)
	}
	// external_refs absence is already pinned by mustNotHave above.
	var progress struct {
		Total int `json:"total"`
		Done  int `json:"done"`
	}
	decodeDetail(detail["subtask_progress"], &progress)
	if progress.Total != 0 || progress.Done != 0 {
		t.Fatalf("subtask_progress = %+v, want {total 0, done 0}", progress)
	}

	// 3. Assignee/label item shapes: assign self + a label, then the
	// detail items must be exactly {id, name} / {id, name, color} —
	// NOT the User / Label shapes the old spec claimed (no email,
	// role, created_at, parent_id).
	rec = postAuthedJSON(t, e, http.MethodPost, projBase+"/labels", cookie, `{"name":"Bug","color":"#ef4444"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create label: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var label struct {
		ID string `json:"id"`
	}
	decode(rec, &label)
	rec = postAuthedJSON(t, e, http.MethodPost, base+"/"+alpha+"/assignees/"+myID, cookie, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("assign self: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPost, base+"/"+alpha+"/labels/"+label.ID, cookie, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("assign label: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	detail = getIssueKeys(alpha, "")
	decodeDetail(detail["assignees"], &assignees)
	if len(assignees) != 1 {
		t.Fatalf("assignees = %v, want 1 item", assignees)
	}
	exactKeys(t, assignees[0], "id", "name")
	if nonEmptyString(t, assignees[0], "id") != myID {
		t.Fatalf("assignee id = %s, want %s", string(assignees[0]["id"]), myID)
	}
	decodeDetail(detail["labels"], &labels)
	if len(labels) != 1 {
		t.Fatalf("labels = %v, want 1 item", labels)
	}
	exactKeys(t, labels[0], "id", "name", "color")
	if nonEmptyString(t, labels[0], "color") != "#ef4444" {
		t.Fatalf("label color = %s, want #ef4444", string(labels[0]["color"]))
	}

	// 2b. external_refs present-case: link one PR, then the detail
	// carries exactly one ExternalRef with the full 9-key shape.
	rec = postAuthedJSON(t, e, http.MethodPost, base+"/"+alpha+"/refs", cookie,
		`{"repo":"acme/repo","ref_type":"pr","ref_id":"42","url":"https://github.com/acme/repo/pull/42"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("link ref: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	detail = getIssueKeys(alpha, "")
	mustHave(t, detail, "external_refs")
	var linked []map[string]json.RawMessage
	decodeDetail(detail["external_refs"], &linked)
	if len(linked) != 1 {
		t.Fatalf("external_refs = %d items, want 1", len(linked))
	}
	exactKeys(t, linked[0], "id", "issue_id", "provider", "repo",
		"ref_type", "ref_id", "url", "state", "created_at")
	if nonEmptyString(t, linked[0], "issue_id") != alpha ||
		nonEmptyString(t, linked[0], "provider") != "github" ||
		nonEmptyString(t, linked[0], "repo") != "acme/repo" ||
		nonEmptyString(t, linked[0], "ref_type") != "pr" ||
		nonEmptyString(t, linked[0], "ref_id") != "42" ||
		nonEmptyString(t, linked[0], "state") != "open" {
		t.Fatalf("external_ref = %v, want the linked PR", rawKeysOf(linked[0]))
	}

	// 4. List: sparse fieldsets — description ABSENT by default,
	// present with ?fields=description. No archived key either way.
	rec = getAuthed(t, e, http.MethodGet, base, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var list struct {
		Results []map[string]json.RawMessage `json:"results"`
	}
	decode(rec, &list)
	if len(list.Results) != 1 {
		t.Fatalf("list results = %d items, want 1", len(list.Results))
	}
	mustHave(t, list.Results[0], "id", "project_id", "created_by",
		"state_id", "assignees", "labels", "subtask_progress")
	mustNotHave(t, list.Results[0], "archived", "description",
		"archived_at", "deleted_at", "external_refs")
	rec = getAuthed(t, e, http.MethodGet, base+"?fields=description", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list ?fields=description: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	decode(rec, &list)
	if len(list.Results) != 1 {
		t.Fatalf("list ?fields=description results = %d, want 1", len(list.Results))
	}
	mustHave(t, list.Results[0], "description")

	// 5. PATCH archived=true → archived_at set, NO archived key;
	// PATCH archived=false → archived_at omitted again.
	rec = postAuthedJSON(t, e, http.MethodPatch, base+"/"+alpha, cookie, `{"archived":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch archived=true: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	patched := topKeys(rec)
	mustHave(t, patched, "archived_at")
	nonEmptyString(t, patched, "archived_at")
	mustNotHave(t, patched, "archived")
	rec = postAuthedJSON(t, e, http.MethodPatch, base+"/"+alpha, cookie, `{"archived":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch archived=false: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	mustNotHave(t, topKeys(rec), "archived", "archived_at")

	// 6. PATCH release_id (tri-state): assign → visible via
	// project_structure.release; null → cleared. The Issue body
	// itself never carries a release_id key.
	rec = postAuthedJSON(t, e, http.MethodPost, projBase+"/releases", cookie, `{"name":"v0.4.0","status":"planned"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create release: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var release struct {
		ID string `json:"id"`
	}
	decode(rec, &release)
	rec = postAuthedJSON(t, e, http.MethodPatch, base+"/"+alpha, cookie,
		`{"release_id":"`+release.ID+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch release_id: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	mustNotHave(t, topKeys(rec), "release_id")
	structured := getIssueKeys(alpha, "?include_project_structure=1")
	mustHave(t, structured, "project_structure")
	var structure struct {
		Modules []map[string]json.RawMessage `json:"modules"`
		Release *struct {
			ID string `json:"id"`
		} `json:"release"`
	}
	decodeDetail(structured["project_structure"], &structure)
	if structure.Release == nil || structure.Release.ID != release.ID {
		t.Fatalf("project_structure.release = %+v, want id %s", structure.Release, release.ID)
	}
	if len(structure.Modules) != 0 {
		t.Fatalf("project_structure.modules = %v, want []", structure.Modules)
	}
	var rawStructure map[string]json.RawMessage
	decodeDetail(structured["project_structure"], &rawStructure)
	mustNotHave(t, rawStructure, "cycle")
	rec = postAuthedJSON(t, e, http.MethodPatch, base+"/"+alpha, cookie, `{"release_id":null}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch release_id=null: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	structured = getIssueKeys(alpha, "?include_project_structure=1")
	// Fresh map: decoding into the previous one would keep the
	// stale "release" key (Unmarshal never deletes map keys).
	rawStructure = map[string]json.RawMessage{}
	decodeDetail(structured["project_structure"], &rawStructure)
	mustNotHave(t, rawStructure, "release")
	// Without the flag the attachment is absent entirely.
	mustNotHave(t, getIssueKeys(alpha, ""), "project_structure")

	// 7. Children are IssueChild summaries {uuid, identifier, title,
	// state} — NOT full Issues. Custom values are absent (empty map
	// omitted) until a field has a set value.
	child := createIssue(`{"name":"Alpha child","parent_id":"` + alpha + `"}`)
	withKids := getIssueKeys(alpha, "?include_children=1")
	mustHave(t, withKids, "children")
	var children []map[string]json.RawMessage
	decodeDetail(withKids["children"], &children)
	if len(children) != 1 {
		t.Fatalf("children = %v, want 1 item", children)
	}
	exactKeys(t, children[0], "uuid", "identifier", "title", "state")
	if nonEmptyString(t, children[0], "uuid") != child {
		t.Fatalf("child uuid = %s, want %s", string(children[0]["uuid"]), child)
	}
	if nonEmptyString(t, children[0], "title") != "Alpha child" {
		t.Fatalf("child title = %s, want Alpha child", string(children[0]["title"]))
	}
	if !strings.HasPrefix(nonEmptyString(t, children[0], "identifier"), ident+"-") {
		t.Fatalf("child identifier = %s, want prefix %q", string(children[0]["identifier"]), ident+"-")
	}
	childState := nonEmptyString(t, children[0], "state")
	if childState == nonEmptyString(t, withKids, "state_id") {
		t.Fatalf("child state = %q, want the human state name, not a state_id", childState)
	}
	// No custom fields yet: the flag is a no-op for custom_values.
	mustNotHave(t, getIssueKeys(alpha, "?include_custom=1"), "custom_values")

	// 8. Custom values: field with a set value → custom_values is
	// {field_id: {field_id, name, field_type, value}}.
	rec = postAuthedJSON(t, e, http.MethodPost, projBase+"/custom-fields", cookie, `{"name":"Summary","field_type":"text"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create custom field: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var field struct {
		ID string `json:"id"`
	}
	decode(rec, &field)
	rec = postAuthedJSON(t, e, http.MethodPut, base+"/"+alpha+"/custom-values", cookie,
		`{"values":{"`+field.ID+`":"hello"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("put custom-values: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	withCustom := getIssueKeys(alpha, "?include_custom=1")
	mustHave(t, withCustom, "custom_values")
	var customValues map[string]map[string]json.RawMessage
	decodeDetail(withCustom["custom_values"], &customValues)
	cv, ok := customValues[field.ID]
	if !ok {
		t.Fatalf("custom_values has %d entries, want entry for field %s", len(customValues), field.ID)
	}
	exactKeys(t, cv, "field_id", "name", "field_type", "value")
	if nonEmptyString(t, cv, "name") != "Summary" ||
		nonEmptyString(t, cv, "field_type") != "text" ||
		nonEmptyString(t, cv, "value") != "hello" {
		t.Fatalf("custom value = %v, want {Summary, text, hello}", rawKeysOf(cv))
	}

	// 9. Similar: exact {id, display_id, name, state, similarity}
	// keys; state is the human name; similarity > 0.3.
	rec = getAuthed(t, e, http.MethodGet, base+"/similar?q=Alpha", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("similar: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var similar []map[string]json.RawMessage
	decode(rec, &similar)
	var hit map[string]json.RawMessage
	for _, s := range similar {
		if nonEmptyString(t, s, "name") == "Alpha" {
			hit = s
		}
	}
	if hit == nil {
		t.Fatalf("similar has no hit named Alpha (body: %s)", rec.Body.String())
	}
	exactKeys(t, hit, "id", "display_id", "name", "state", "similarity")
	var sim float64
	if err := json.Unmarshal(hit["similarity"], &sim); err != nil || sim <= 0.3 {
		t.Fatalf("similarity = %s, want > 0.3", string(hit["similarity"]))
	}
	if nonEmptyString(t, hit, "state") == "" {
		t.Fatal("similar hit has empty state")
	}

	// 10. Trash: deleted_at present ONLY on the trash list; restore
	// returns the bare issue with NO deleted_at / archived keys.
	beta := createIssue(`{"name":"Beta"}`)
	rec = postAuthedJSON(t, e, http.MethodDelete, base+"/"+beta, cookie, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete Beta: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, base+"/"+beta, cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get deleted Beta: status = %d, want 404", rec.Code)
	}
	rec = getAuthed(t, e, http.MethodGet, base+"?deleted=1", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("trash list: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var trash struct {
		Results []map[string]json.RawMessage `json:"results"`
	}
	decode(rec, &trash)
	if len(trash.Results) != 1 {
		t.Fatalf("trash results = %d items, want 1", len(trash.Results))
	}
	mustHave(t, trash.Results[0], "deleted_at")
	nonEmptyString(t, trash.Results[0], "deleted_at")
	mustNotHave(t, trash.Results[0], "archived")
	rec = postAuthedJSON(t, e, http.MethodPost, base+"/"+beta+"/restore", cookie, `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("restore Beta: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	mustNotHave(t, topKeys(rec), "deleted_at", "archived")
	if rec.Code = getAuthed(t, e, http.MethodGet, base+"/"+beta, cookie).Code; rec.Code != http.StatusOK {
		t.Fatalf("get restored Beta: status = %d, want 200", rec.Code)
	}
}

func rawKeysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Response-shape pinning test (C54T2): for the swept issue-adjacent
// schemas (User / Label / State / Comment / Attachment / ReactionGroup /
// IssueVotes / SubtaskProgress / Page / PageRevision / ShareLink /
// IssueLink) plus the carried issue-list envelope fix, assert the LIVE
// response keys match the corrected docs/openapi.yaml. Pins the C54T2
// drift fixes: User has no role field (is_admin/is_active/avatar_url/
// last_login_at/updated_at instead, name nullable); Label/State carry
// their scope + timestamp fields (Label color never null, State group
// admits triage); Comment carries actor (ActorRef, not author/User),
// issue_id, list-only replies, and reactions (C67T0: always an array,
// [] when empty, never null — populated on list, empty on single
// responses); Attachment carries issue_id + uploaded_by; GET reactions returns ReactionGroup[] (the old Reaction
// {emoji,user_id,created_at} shape never existed); GET votes returns
// {count, voted} (the old Vote[] shape never existed); SubtaskProgress
// is present with real values on detail; Page/PageRevision content is
// never null and carry project_id/page_id/author_id; ShareLink carries
// id/scope/revoked_at; the list envelope is {results, next_cursor?,
// links?} with no issues/total keys.

func TestOpenAPIResponseShapesSweep(t *testing.T) {
	pool := newTestPool(t)
	st, err := store.NewFileAttachmentStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileAttachmentStore: %v", err)
	}
	e := testIssueServer(t, pool)
	h := &IssueHandler{Pool: pool, Attachments: st, MaxUploadBytes: 25 << 20}
	RegisterSatelliteRoutes(e, h)
	RegisterPageRoutes(e, h)
	RegisterStateRoutes(e, &StateHandler{Pool: pool})
	RegisterIssueLinkRoutes(e, h)

	email := uniqueEmail("openapi-sweep")
	cookie := loginTestUser(t, e, pool, email, "test-agent", uniqueIP())
	myID := authedUserID(t, e, cookie)
	slug := uniqueSlug("openapi-sweep")
	createWorkspaceHTTP(t, e, cookie, "Sweep Co", slug)
	ident := uniqueProjectIdentifier("SW")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	projBase := "/api/v1/workspaces/" + slug + "/projects/" + ident
	base := projBase + "/issues"

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
	mustHave := func(t *testing.T, m map[string]json.RawMessage, keys ...string) {
		t.Helper()
		for _, k := range keys {
			if _, ok := m[k]; !ok {
				t.Fatalf("response missing key %q (keys: %v)", k, rawKeysOf(m))
			}
		}
	}
	mustNotHave := func(t *testing.T, m map[string]json.RawMessage, keys ...string) {
		t.Helper()
		for _, k := range keys {
			if _, ok := m[k]; ok {
				t.Fatalf("response has forbidden key %q (body keys: %v)", k, rawKeysOf(m))
			}
		}
	}
	exactKeys := func(t *testing.T, m map[string]json.RawMessage, keys ...string) {
		t.Helper()
		mustHave(t, m, keys...)
		if len(m) != len(keys) {
			t.Fatalf("response keys = %v, want exactly %v", rawKeysOf(m), keys)
		}
	}
	nonEmptyString := func(t *testing.T, m map[string]json.RawMessage, key string) string {
		t.Helper()
		var s string
		if err := json.Unmarshal(m[key], &s); err != nil || s == "" {
			t.Fatalf("key %q = %s, want a non-empty string", key, string(m[key]))
		}
		return s
	}
	isNull := func(t *testing.T, m map[string]json.RawMessage, key string) {
		t.Helper()
		if string(m[key]) != "null" {
			t.Fatalf("key %q = %s, want null", key, string(m[key]))
		}
	}
	// actorKeys pins the ActorRef shape {id, name?, email}: the id+email
	// core, an optional name (omitempty drops a null name), and nothing
	// else — proving the value is NOT a full User (no role/created_at).
	actorKeys := func(t *testing.T, m map[string]json.RawMessage) {
		t.Helper()
		mustHave(t, m, "id", "email")
		mustNotHave(t, m, "role", "created_at", "is_admin", "is_active")
		if len(m) > 3 {
			t.Fatalf("actor keys = %v, want at most {id, name, email}", rawKeysOf(m))
		}
	}
	createIssue := func(body string) string {
		t.Helper()
		rec := postAuthedJSON(t, e, http.MethodPost, base, cookie, body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s: status = %d (body: %s)", body, rec.Code, rec.Body.String())
		}
		var created struct {
			ID string `json:"id"`
		}
		decode(rec, &created)
		if created.ID == "" {
			t.Fatalf("create %s returned no id", body)
		}
		return created.ID
	}

	// 1. User: GET /me → exactly the auth.User keys; NO role field.
	// The OTP login sets no display name, so name is null (nullable).
	rec := getAuthed(t, e, http.MethodGet, "/api/v1/me", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("get /me: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	me := topKeys(rec)
	exactKeys(t, me, "id", "email", "name", "avatar_url", "is_active",
		"is_admin", "last_login_at", "created_at", "updated_at")
	mustNotHave(t, me, "role")
	if nonEmptyString(t, me, "id") != myID {
		t.Fatalf("/me id = %s, want %s", string(me["id"]), myID)
	}
	if nonEmptyString(t, me, "email") != email {
		t.Fatalf("/me email = %s, want %s", string(me["email"]), email)
	}
	isNull(t, me, "name")
	var isActive, isAdmin bool
	if err := json.Unmarshal(me["is_active"], &isActive); err != nil || !isActive {
		t.Fatalf("/me is_active = %s, want true", string(me["is_active"]))
	}
	if err := json.Unmarshal(me["is_admin"], &isAdmin); err != nil || isAdmin {
		t.Fatalf("/me is_admin = %s, want false", string(me["is_admin"]))
	}

	// 2. Label: create → exactly {id, workspace_id, name, color,
	// created_at, updated_at} (parent_id omitted for roots, color the
	// sent value, never null); child create carries parent_id.
	rec = postAuthedJSON(t, e, http.MethodPost, projBase+"/labels", cookie, `{"name":"Bug","color":"#ef4444"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create label: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	label := topKeys(rec)
	exactKeys(t, label, "id", "workspace_id", "name", "color", "created_at", "updated_at")
	if nonEmptyString(t, label, "color") != "#ef4444" {
		t.Fatalf("label color = %s, want #ef4444", string(label["color"]))
	}
	labelID := nonEmptyString(t, label, "id")
	nonEmptyString(t, label, "workspace_id")
	rec = postAuthedJSON(t, e, http.MethodPost, projBase+"/labels", cookie,
		`{"name":"Bug child","parent_id":"`+labelID+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create child label: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	childLabel := topKeys(rec)
	exactKeys(t, childLabel, "id", "workspace_id", "name", "color",
		"parent_id", "created_at", "updated_at")
	if nonEmptyString(t, childLabel, "parent_id") != labelID {
		t.Fatalf("child label parent_id = %s, want %s", string(childLabel["parent_id"]), labelID)
	}
	// Default color: omitted on create → #6b7280, not null.
	rec = postAuthedJSON(t, e, http.MethodPost, projBase+"/labels", cookie, `{"name":"Plain"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create plain label: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if nonEmptyString(t, topKeys(rec), "color") != "#6b7280" {
		t.Fatalf("default label color = %s, want #6b7280", string(topKeys(rec)["color"]))
	}
	rec = getAuthed(t, e, http.MethodGet, projBase+"/labels/"+labelID, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("get label: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	exactKeys(t, topKeys(rec), "id", "workspace_id", "name", "color", "created_at", "updated_at")

	// 3. State: list items are exactly the service.State keys;
	// groups are within the six-group vocabulary; creating a triage
	// state works (the old doc omitted triage).
	rec = getAuthed(t, e, http.MethodGet, projBase+"/states", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list states: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var stateList struct {
		States []map[string]json.RawMessage `json:"states"`
	}
	decode(rec, &stateList)
	if len(stateList.States) == 0 {
		t.Fatal("states list is empty, want the seeded defaults")
	}
	validGroups := map[string]bool{"triage": true, "backlog": true, "unstarted": true,
		"started": true, "completed": true, "cancelled": true}
	seenGroups := map[string]bool{}
	var completedStateID string
	for _, s := range stateList.States {
		exactKeys(t, s, "id", "project_id", "name", "group", "color", "sequence", "created_at", "updated_at")
		g := nonEmptyString(t, s, "group")
		if !validGroups[g] {
			t.Fatalf("state group = %q, want one of the six groups", g)
		}
		seenGroups[g] = true
		if g == "completed" && completedStateID == "" {
			completedStateID = nonEmptyString(t, s, "id")
		}
	}
	if !seenGroups["backlog"] || !seenGroups["completed"] {
		t.Fatalf("state groups = %v, want backlog + completed among them", seenGroups)
	}
	if completedStateID == "" {
		t.Fatal("no completed-group state in the seeded defaults")
	}
	rec = postAuthedJSON(t, e, http.MethodPost, projBase+"/states", cookie,
		`{"name":"Triage","group":"triage","color":"#f59e0b"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create triage state: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	triage := topKeys(rec)
	exactKeys(t, triage, "id", "project_id", "name", "group", "color", "sequence", "created_at", "updated_at")
	if nonEmptyString(t, triage, "group") != "triage" {
		t.Fatalf("triage state group = %s, want triage", string(triage["group"]))
	}

	// 4. Comment: create → bare Comment with actor (ActorRef), no
	// author / replies keys, reactions present-but-empty; resolved_at
	// + resolved_by present-but-null while open. List nests the reply
	// under replies[] and attaches reactions[] to roots and replies.
	// Resolve fills resolved_at + resolved_by (ActorRef, not User).
	alpha := createIssue(`{"name":"Alpha"}`)
	commentBase := base + "/" + alpha + "/comments"
	rec = postAuthedJSON(t, e, http.MethodPost, commentBase, cookie,
		`{"content":{"type":"doc","content":[]}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create comment: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	comment := topKeys(rec)
	exactKeys(t, comment, "id", "issue_id", "content", "actor",
		"created_at", "updated_at", "resolved_at", "resolved_by", "reactions")
	mustNotHave(t, comment, "author", "replies")
	isEmptyArray := func(t *testing.T, m map[string]json.RawMessage, key string) {
		t.Helper()
		if string(m[key]) != "[]" {
			t.Fatalf("%s = %s, want []", key, string(m[key]))
		}
	}
	isEmptyArray(t, comment, "reactions")
	if nonEmptyString(t, comment, "issue_id") != alpha {
		t.Fatalf("comment issue_id = %s, want %s", string(comment["issue_id"]), alpha)
	}
	var actor map[string]json.RawMessage
	decodeDetail := func(raw json.RawMessage, v any) {
		t.Helper()
		if err := json.Unmarshal(raw, v); err != nil {
			t.Fatalf("decode %s: %v", string(raw), err)
		}
	}
	decodeDetail(comment["actor"], &actor)
	actorKeys(t, actor)
	if nonEmptyString(t, actor, "id") != myID {
		t.Fatalf("comment actor id = %s, want %s", string(actor["id"]), myID)
	}
	isNull(t, comment, "resolved_at")
	isNull(t, comment, "resolved_by")
	commentID := nonEmptyString(t, comment, "id")
	rec = postAuthedJSON(t, e, http.MethodPost, commentBase, cookie,
		`{"content":{"type":"doc"},"parent_id":"`+commentID+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create reply: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	mustNotHave(t, topKeys(rec), "author", "replies")
	rec = getAuthed(t, e, http.MethodGet, commentBase, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list comments: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var commentList struct {
		Comments []map[string]json.RawMessage `json:"comments"`
	}
	decode(rec, &commentList)
	if len(commentList.Comments) != 1 {
		t.Fatalf("comments = %d roots, want 1", len(commentList.Comments))
	}
	root := commentList.Comments[0]
	mustHave(t, root, "replies")
	mustNotHave(t, root, "author")
	var replies []map[string]json.RawMessage
	decodeDetail(root["replies"], &replies)
	if len(replies) != 1 {
		t.Fatalf("replies = %d, want 1", len(replies))
	}
	mustNotHave(t, replies[0], "author")
	var replyActor map[string]json.RawMessage
	decodeDetail(replies[0]["actor"], &replyActor)
	actorKeys(t, replyActor)
	// C67T0: the list path attaches reactions to roots and replies —
	// always an array, [] when empty, never null / never absent.
	mustHave(t, root, "reactions")
	mustHave(t, replies[0], "reactions")
	isEmptyArray(t, root, "reactions")
	isEmptyArray(t, replies[0], "reactions")
	rec = postAuthedJSON(t, e, http.MethodPost, commentBase+"/"+commentID+"/reactions", cookie,
		`{"emoji":"👍"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("add comment reaction: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, commentBase, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("relist comments: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var relist struct {
		Comments []map[string]json.RawMessage `json:"comments"`
	}
	decode(rec, &relist)
	relisted := decodeCommentReactionList(t, append([]byte(`{"reactions":`), append(relist.Comments[0]["reactions"], '}')...))
	if len(relisted) != 1 || relisted[0].Emoji != "👍" || relisted[0].Count != 1 || !relisted[0].Reacted {
		t.Fatalf("root reactions after add = %+v, want one 👍 count 1 reacted true", relisted)
	}
	rec = postAuthedJSON(t, e, http.MethodPost, commentBase+"/"+commentID+"/resolve", cookie, `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve comment: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	resolved := topKeys(rec)
	nonEmptyString(t, resolved, "resolved_at")
	var resolvedBy map[string]json.RawMessage
	decodeDetail(resolved["resolved_by"], &resolvedBy)
	actorKeys(t, resolvedBy)
	if nonEmptyString(t, resolvedBy, "id") != myID {
		t.Fatalf("resolved_by id = %s, want %s", string(resolvedBy["id"]), myID)
	}

	// 5. Attachment: multipart upload → exactly the service.Attachment
	// keys (issue_id + uploaded_by present, stored_path never leaks);
	// list carries the same shape.
	attBase := commentBase[:len(commentBase)-len("/comments")] + "/attachments"
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0xde, 0xad}
	rec = postMultipartFile(t, e, attBase, cookie, "shot.png", "image/png", png)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	att := topKeys(rec)
	exactKeys(t, att, "id", "issue_id", "filename", "content_type",
		"size_bytes", "uploaded_by", "created_at")
	mustNotHave(t, att, "stored_path")
	if nonEmptyString(t, att, "issue_id") != alpha {
		t.Fatalf("attachment issue_id = %s, want %s", string(att["issue_id"]), alpha)
	}
	if nonEmptyString(t, att, "filename") != "shot.png" {
		t.Fatalf("attachment filename = %s, want shot.png", string(att["filename"]))
	}
	var uploader map[string]json.RawMessage
	decodeDetail(att["uploaded_by"], &uploader)
	actorKeys(t, uploader)
	rec = getAuthed(t, e, http.MethodGet, attBase, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list attachments: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var attList struct {
		Attachments []map[string]json.RawMessage `json:"attachments"`
	}
	decode(rec, &attList)
	if len(attList.Attachments) != 1 {
		t.Fatalf("attachments = %d, want 1", len(attList.Attachments))
	}
	exactKeys(t, attList.Attachments[0], "id", "issue_id", "filename",
		"content_type", "size_bytes", "uploaded_by", "created_at")

	// 6. Reactions: GET returns {reactions: [ReactionGroup]} —
	// exactly {emoji, count, reacted}; the old Reaction shape
	// {emoji, user_id, created_at} never appears.
	reactBase := base + "/" + alpha + "/reactions"
	rec = getAuthed(t, e, http.MethodGet, reactBase, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list reactions: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var reactList struct {
		Reactions []map[string]json.RawMessage `json:"reactions"`
	}
	decode(rec, &reactList)
	if len(reactList.Reactions) != 0 {
		t.Fatalf("reactions = %d, want 0", len(reactList.Reactions))
	}
	rec = postAuthedJSON(t, e, http.MethodPost, reactBase, cookie, `{"emoji":"👍"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("add reaction: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPost, reactBase, cookie, `{"emoji":"👍"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("re-add reaction: status = %d, want 204 idempotent (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, reactBase, cookie)
	decode(rec, &reactList)
	if len(reactList.Reactions) != 1 {
		t.Fatalf("reactions = %d, want 1", len(reactList.Reactions))
	}
	exactKeys(t, reactList.Reactions[0], "emoji", "count", "reacted")
	if nonEmptyString(t, reactList.Reactions[0], "emoji") != "👍" {
		t.Fatalf("reaction emoji = %s, want 👍", string(reactList.Reactions[0]["emoji"]))
	}
	var reactCount int
	if err := json.Unmarshal(reactList.Reactions[0]["count"], &reactCount); err != nil || reactCount != 1 {
		t.Fatalf("reaction count = %s, want 1", string(reactList.Reactions[0]["count"]))
	}
	var reacted bool
	if err := json.Unmarshal(reactList.Reactions[0]["reacted"], &reacted); err != nil || !reacted {
		t.Fatalf("reaction reacted = %s, want true", string(reactList.Reactions[0]["reacted"]))
	}

	// 7. Votes: GET returns exactly {count, voted} — never a Vote[]
	// {user_id, created_at} shape. Toggle twice: count stays 1.
	voteBase := base + "/" + alpha + "/votes"
	getVotes := func() map[string]json.RawMessage {
		t.Helper()
		rec := getAuthed(t, e, http.MethodGet, voteBase, cookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("get votes: status = %d (body: %s)", rec.Code, rec.Body.String())
		}
		return topKeys(rec)
	}
	votes := getVotes()
	exactKeys(t, votes, "count", "voted")
	mustNotHave(t, votes, "votes")
	var voteCount int
	if err := json.Unmarshal(votes["count"], &voteCount); err != nil || voteCount != 0 {
		t.Fatalf("votes count = %s, want 0", string(votes["count"]))
	}
	rec = postAuthedJSON(t, e, http.MethodPost, voteBase, cookie, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("vote: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPost, voteBase, cookie, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("re-vote: status = %d, want 204 idempotent (body: %s)", rec.Code, rec.Body.String())
	}
	votes = getVotes()
	exactKeys(t, votes, "count", "voted")
	if err := json.Unmarshal(votes["count"], &voteCount); err != nil || voteCount != 1 {
		t.Fatalf("votes count = %s, want 1", string(votes["count"]))
	}
	var voted bool
	if err := json.Unmarshal(votes["voted"], &voted); err != nil || !voted {
		t.Fatalf("votes voted = %s, want true", string(votes["voted"]))
	}
	rec = postAuthedJSON(t, e, http.MethodDelete, voteBase, cookie, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("unvote: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	votes = getVotes()
	if err := json.Unmarshal(votes["count"], &voteCount); err != nil || voteCount != 0 {
		t.Fatalf("votes count after unvote = %s, want 0", string(votes["count"]))
	}

	// 8. SubtaskProgress: parent detail carries {total, done} with
	// real values; moving the child to a completed state flips done.
	parent := createIssue(`{"name":"Parent"}`)
	child := createIssue(`{"name":"Child","parent_id":"` + parent + `"}`)
	rec = getAuthed(t, e, http.MethodGet, base+"/"+parent, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("get parent: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	parentDetail := topKeys(rec)
	mustHave(t, parentDetail, "subtask_progress")
	var progress struct {
		Total int `json:"total"`
		Done  int `json:"done"`
	}
	decodeDetail(parentDetail["subtask_progress"], &progress)
	if progress.Total != 1 || progress.Done != 0 {
		t.Fatalf("subtask_progress = %+v, want {total 1, done 0}", progress)
	}
	var progressKeys map[string]json.RawMessage
	decodeDetail(parentDetail["subtask_progress"], &progressKeys)
	exactKeys(t, progressKeys, "total", "done")
	rec = postAuthedJSON(t, e, http.MethodPatch, base+"/"+child, cookie,
		`{"state_id":"`+completedStateID+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("move child to completed: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, base+"/"+parent, cookie)
	decodeDetail(topKeys(rec)["subtask_progress"], &progress)
	if progress.Total != 1 || progress.Done != 1 {
		t.Fatalf("subtask_progress after completion = %+v, want {total 1, done 1}", progress)
	}

	// 9. Page + PageRevision: create → exactly the service.Page keys
	// (project_id + author_id present, parent_id/reactions omitted);
	// content "" when unset, never null. Detail embeds reactions[]
	// only after a reaction; a title update leaves a revision with
	// the pre-update title + author_id.
	pageBase := projBase + "/pages"
	rec = postAuthedJSON(t, e, http.MethodPost, pageBase, cookie, `{"title":"Guide","content":"hello"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create page: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	page := topKeys(rec)
	exactKeys(t, page, "id", "project_id", "title", "content", "position",
		"author_id", "created_at", "updated_at")
	mustNotHave(t, page, "parent_id", "reactions", "created_by")
	if nonEmptyString(t, page, "content") != "hello" {
		t.Fatalf("page content = %s, want hello", string(page["content"]))
	}
	if nonEmptyString(t, page, "author_id") != myID {
		t.Fatalf("page author_id = %s, want %s", string(page["author_id"]), myID)
	}
	pageID := nonEmptyString(t, page, "id")
	rec = postAuthedJSON(t, e, http.MethodPost, pageBase, cookie, `{"title":"Empty"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create empty page: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	emptyPage := topKeys(rec)
	if string(emptyPage["content"]) != `""` {
		t.Fatalf("empty page content = %s, want empty string, never null", string(emptyPage["content"]))
	}
	rec = getAuthed(t, e, http.MethodGet, pageBase+"/"+pageID, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("get page: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	mustNotHave(t, topKeys(rec), "reactions")
	rec = postAuthedJSON(t, e, http.MethodPost, pageBase+"/"+pageID+"/reactions?emoji=%F0%9F%91%8D", cookie, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("add page reaction: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, pageBase+"/"+pageID, cookie)
	pageDetail := topKeys(rec)
	mustHave(t, pageDetail, "reactions")
	var pageReactions []map[string]json.RawMessage
	decodeDetail(pageDetail["reactions"], &pageReactions)
	if len(pageReactions) != 1 {
		t.Fatalf("page reactions = %d, want 1", len(pageReactions))
	}
	exactKeys(t, pageReactions[0], "emoji", "count", "reacted")
	rec = postAuthedJSON(t, e, http.MethodPatch, pageBase+"/"+pageID, cookie, `{"title":"Guide v2"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("update page: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, pageBase+"/"+pageID+"/revisions", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list revisions: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var revList struct {
		Revisions []map[string]json.RawMessage `json:"revisions"`
	}
	decode(rec, &revList)
	if len(revList.Revisions) != 1 {
		t.Fatalf("revisions = %d, want 1", len(revList.Revisions))
	}
	exactKeys(t, revList.Revisions[0], "id", "page_id", "title", "content", "author_id", "created_at")
	mustNotHave(t, revList.Revisions[0], "created_by")
	if nonEmptyString(t, revList.Revisions[0], "page_id") != pageID {
		t.Fatalf("revision page_id = %s, want %s", string(revList.Revisions[0]["page_id"]), pageID)
	}
	if nonEmptyString(t, revList.Revisions[0], "title") != "Guide" {
		t.Fatalf("revision title = %s, want the pre-update Guide", string(revList.Revisions[0]["title"]))
	}
	if nonEmptyString(t, revList.Revisions[0], "content") != "hello" {
		t.Fatalf("revision content = %s, want hello", string(revList.Revisions[0]["content"]))
	}
	if nonEmptyString(t, revList.Revisions[0], "author_id") != myID {
		t.Fatalf("revision author_id = %s, want %s", string(revList.Revisions[0]["author_id"]), myID)
	}

	// 10. Share: create → {token, url, share}; the share object is
	// exactly {id, token, scope, created_at} (expires_at/revoked_at
	// omitted while unset); scope issue. List returns the live
	// {links: [...]} envelope, and the doc pins the same key
	// (C55T0 fixed the doc's stale {shares} envelope).
	shareBase := base + "/" + alpha + "/share"
	rec = postAuthedJSON(t, e, http.MethodPost, shareBase, cookie, `{}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create share: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	shareResp := topKeys(rec)
	exactKeys(t, shareResp, "token", "url", "share")
	shareToken := nonEmptyString(t, shareResp, "token")
	if !strings.Contains(nonEmptyString(t, shareResp, "url"), "/s/"+shareToken) {
		t.Fatalf("share url = %s, want it to contain /s/%s", string(shareResp["url"]), shareToken)
	}
	var share map[string]json.RawMessage
	decodeDetail(shareResp["share"], &share)
	exactKeys(t, share, "id", "token", "scope", "created_at")
	mustNotHave(t, share, "expires_at", "revoked_at")
	if nonEmptyString(t, share, "scope") != "issue" {
		t.Fatalf("share scope = %s, want issue", string(share["scope"]))
	}
	if nonEmptyString(t, share, "token") != shareToken {
		t.Fatalf("share.token = %s, want %s", string(share["token"]), shareToken)
	}
	rec = postAuthedJSON(t, e, http.MethodPost, shareBase, cookie, `{"expires_at":"2030-01-01T00:00:00Z"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create expiring share: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var expShare map[string]json.RawMessage
	decodeDetail(topKeys(rec)["share"], &expShare)
	mustHave(t, expShare, "expires_at")
	nonEmptyString(t, expShare, "expires_at")
	rec = getAuthed(t, e, http.MethodGet, shareBase, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list shares: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	shareList := topKeys(rec)
	mustHave(t, shareList, "links")
	mustNotHave(t, shareList, "shares")
	var links []map[string]json.RawMessage
	decodeDetail(shareList["links"], &links)
	if len(links) != 2 {
		t.Fatalf("share links = %d, want 2", len(links))
	}

	// 11. List envelope (carried C54T0 item): fresh project, two
	// issues. Default list → exactly {results} (no issues/total/
	// next_cursor/links). per_page=1 → next_cursor present; following
	// it yields the other issue with no next_cursor. ?include_links
	// attaches links[] (IssueLink WITHOUT direction); without the
	// flag the key is absent. The single-issue links endpoint DOES
	// carry direction=outgoing.
	ident2 := uniqueProjectIdentifier("LE")
	createProjectHTTP(t, e, cookie, slug, "Envelope", ident2)
	projBase2 := "/api/v1/workspaces/" + slug + "/projects/" + ident2
	base2 := projBase2 + "/issues"
	mkIssue := func(name string) string {
		t.Helper()
		rec := postAuthedJSON(t, e, http.MethodPost, base2, cookie, fmt.Sprintf(`{"name":%q}`, name))
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %q: status = %d (body: %s)", name, rec.Code, rec.Body.String())
		}
		var created struct {
			ID string `json:"id"`
		}
		decode(rec, &created)
		return created.ID
	}
	envA := mkIssue("Env A")
	envB := mkIssue("Env B")
	rec = getAuthed(t, e, http.MethodGet, base2, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list envelope: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	exactKeys(t, topKeys(rec), "results")
	var envList struct {
		Results []struct {
			ID string `json:"id"`
		} `json:"results"`
	}
	decode(rec, &envList)
	if len(envList.Results) != 2 {
		t.Fatalf("list results = %d, want 2", len(envList.Results))
	}
	rec = getAuthed(t, e, http.MethodGet, base2+"?per_page=1", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list per_page=1: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	page1 := topKeys(rec)
	exactKeys(t, page1, "results", "next_cursor")
	cursor := nonEmptyString(t, page1, "next_cursor")
	var page1List struct {
		Results []struct {
			ID string `json:"id"`
		} `json:"results"`
	}
	decode(rec, &page1List)
	if len(page1List.Results) != 1 {
		t.Fatalf("page 1 results = %d, want 1", len(page1List.Results))
	}
	rec = getAuthed(t, e, http.MethodGet, base2+"?per_page=1&cursor="+cursor, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list page 2: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	exactKeys(t, topKeys(rec), "results")
	var page2List struct {
		Results []struct {
			ID string `json:"id"`
		} `json:"results"`
	}
	decode(rec, &page2List)
	if len(page2List.Results) != 1 {
		t.Fatalf("page 2 results = %d, want 1", len(page2List.Results))
	}
	if page1List.Results[0].ID == page2List.Results[0].ID {
		t.Fatalf("pages repeat id %s, want disjoint pages", page1List.Results[0].ID)
	}
	seen := map[string]bool{page1List.Results[0].ID: true, page2List.Results[0].ID: true}
	if !seen[envA] || !seen[envB] {
		t.Fatalf("pages cover %v, want both %s and %s", seen, envA, envB)
	}
	// Link A → B, then the list envelope's links[] vs the
	// single-issue endpoint's direction-carrying links[].
	rec = postAuthedJSON(t, e, http.MethodPost, base2+"/"+envA+"/links", cookie,
		`{"target_issue_id":"`+envB+`","kind":"blocks"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create link: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, base2+"?include_links=true", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list include_links: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	linked := topKeys(rec)
	mustHave(t, linked, "results", "links")
	var envLinks []map[string]json.RawMessage
	decodeDetail(linked["links"], &envLinks)
	if len(envLinks) != 1 {
		t.Fatalf("list links = %d, want 1", len(envLinks))
	}
	exactKeys(t, envLinks[0], "id", "issue_id", "target_issue_id", "kind", "created_at")
	mustNotHave(t, envLinks[0], "direction")
	if nonEmptyString(t, envLinks[0], "issue_id") != envA ||
		nonEmptyString(t, envLinks[0], "target_issue_id") != envB ||
		nonEmptyString(t, envLinks[0], "kind") != "blocks" {
		t.Fatalf("list link = %v, want A→B blocks", rawKeysOf(envLinks[0]))
	}
	rec = getAuthed(t, e, http.MethodGet, base2, cookie)
	mustNotHave(t, topKeys(rec), "links")
	rec = getAuthed(t, e, http.MethodGet, base2+"/"+envA+"/links", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("single-issue links: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var singleLinks struct {
		Links []map[string]json.RawMessage `json:"links"`
	}
	decode(rec, &singleLinks)
	if len(singleLinks.Links) != 1 {
		t.Fatalf("single-issue links = %d, want 1", len(singleLinks.Links))
	}
	exactKeys(t, singleLinks.Links[0], "id", "issue_id", "target_issue_id",
		"kind", "created_at", "direction")
	if nonEmptyString(t, singleLinks.Links[0], "direction") != "outgoing" {
		t.Fatalf("link direction = %s, want outgoing", string(singleLinks.Links[0]["direction"]))
	}
}

// Doc-side pinning test (C55T0): for the two share-list endpoints,
// the subscribers endpoint, and the new Subscriber schema, assert
// the DOCUMENTED shapes in docs/openapi.yaml match the code. This
// is the doc-side complement to the live pins: share_handler_test
// already pins live {links}, and TestOpenAPIResponseShapesSweep §10
// pins the live share-list envelope — this test pins the doc so
// the {shares} drift (doc said {shares}, handlers returned
// {links}) cannot silently return, and pins Subscriber field-for-
// field against the Go struct via reflection so a struct change
// without a doc update fails. No DB: parses the embedded spec.

func TestOpenAPIDocShapesShareSubscriber(t *testing.T) {
	var spec struct {
		Paths      map[string]map[string]json.RawMessage `json:"paths"`
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(docs.OpenAPI, &spec); err != nil {
		t.Fatalf("embedded openapi spec is not valid JSON: %v", err)
	}
	decodeObj := func(raw json.RawMessage) map[string]json.RawMessage {
		t.Helper()
		var m map[string]json.RawMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("decode %s: %v", string(raw), err)
		}
		return m
	}
	// responseSchema returns the 200 response schema of one operation.
	responseSchema := func(path, method string) map[string]json.RawMessage {
		t.Helper()
		ops, ok := spec.Paths[path]
		if !ok {
			t.Fatalf("spec has no path %s", path)
		}
		raw, ok := ops[method]
		if !ok {
			t.Fatalf("spec path %s has no %s operation", path, method)
		}
		op := decodeObj(raw)
		responses := decodeObj(op["responses"])
		resp := decodeObj(responses["200"])
		content := decodeObj(resp["content"])
		media := decodeObj(content["application/json"])
		return decodeObj(media["schema"])
	}
	// envelopeProp returns properties[name] of an object schema, as
	// (properties, propSchema) maps, failing on a non-object schema.
	envelopeProp := func(schema map[string]json.RawMessage, name string) map[string]json.RawMessage {
		t.Helper()
		props, ok := schema["properties"]
		if !ok {
			t.Fatalf("schema %s has no properties", string(mustJSON(t, schema)))
		}
		prop := decodeObj(props)[name]
		if prop == nil {
			t.Fatalf("schema properties lack %q", name)
		}
		return decodeObj(prop)
	}
	// itemsRef returns the $ref of an array property's items.
	itemsRef := func(prop map[string]json.RawMessage) string {
		t.Helper()
		var arr struct {
			Type  string `json:"type"`
			Items struct {
				Ref string `json:"$ref"`
			} `json:"items"`
		}
		if err := json.Unmarshal(mustJSON(t, prop), &arr); err != nil {
			t.Fatalf("decode array prop: %v", err)
		}
		if arr.Type != "array" {
			t.Fatalf("property type = %q, want array", arr.Type)
		}
		return arr.Items.Ref
	}
	// envelopeKeys returns the property names of an object schema.
	envelopeKeys := func(schema map[string]json.RawMessage) map[string]bool {
		t.Helper()
		out := map[string]bool{}
		for k := range decodeObj(schema["properties"]) {
			out[k] = true
		}
		return out
	}

	// 1. Both share-list endpoints document {links: ShareLink[]} —
	// NOT the stale {shares: ...} envelope the doc used to claim.
	for _, path := range []string{
		"/api/v1/workspaces/{slug}/projects/{identifier}/issues/{uuid}/share",
		"/api/v1/workspaces/{slug}/projects/{identifier}/pages/{pageID}/share",
	} {
		schema := responseSchema(path, "get")
		keys := envelopeKeys(schema)
		if !keys["links"] {
			t.Fatalf("GET %s doc envelope keys = %v, want links", path, keys)
		}
		if keys["shares"] {
			t.Fatalf("GET %s doc envelope still has stale key %q", path, "shares")
		}
		if got := itemsRef(envelopeProp(schema, "links")); got != "#/components/schemas/ShareLink" {
			t.Fatalf("GET %s doc links items $ref = %q, want ShareLink", path, got)
		}
	}

	// 2. Issue-link list endpoint: checked in code (handler returns
	// {links}) and its doc already said {links} — pin it so the
	// same drift class cannot appear here unnoticed.
	linkSchema := responseSchema(
		"/api/v1/workspaces/{slug}/projects/{identifier}/issues/{uuid}/links", "get")
	linkKeys := envelopeKeys(linkSchema)
	if !linkKeys["links"] || linkKeys["shares"] {
		t.Fatalf("GET .../links doc envelope keys = %v, want exactly links", linkKeys)
	}
	if got := itemsRef(envelopeProp(linkSchema, "links")); got != "#/components/schemas/IssueLink" {
		t.Fatalf("GET .../links doc links items $ref = %q, want IssueLink", got)
	}

	// 3. Subscribers endpoint refs Subscriber, not User.
	subSchema := responseSchema(
		"/api/v1/workspaces/{slug}/projects/{identifier}/issues/{uuid}/subscribers", "get")
	subKeys := envelopeKeys(subSchema)
	if !subKeys["subscribers"] {
		t.Fatalf("GET .../subscribers doc envelope keys = %v, want subscribers", subKeys)
	}
	if got := itemsRef(envelopeProp(subSchema, "subscribers")); got != "#/components/schemas/Subscriber" {
		t.Fatalf("GET .../subscribers doc items $ref = %q, want Subscriber", got)
	}

	// 4. Subscriber schema matches service.Subscriber field-for-
	// field, derived from the Go struct via reflection (not
	// hard-coded): every json tag name must be a doc property and
	// vice versa; user_id + email are required; name is NOT
	// required and NOT nullable — the Go field is *string +
	// omitempty, so the key is OMITTED when nil, never null.
	raw, ok := spec.Components.Schemas["Subscriber"]
	if !ok {
		t.Fatal("spec has no Subscriber schema")
	}
	var docSchema struct {
		Type     string                     `json:"type"`
		Required []string                   `json:"required"`
		Props    map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &docSchema); err != nil {
		t.Fatalf("decode Subscriber schema: %v", err)
	}
	if docSchema.Type != "object" {
		t.Fatalf("Subscriber schema type = %q, want object", docSchema.Type)
	}
	structType := reflect.TypeOf(service.Subscriber{})
	if structType.Kind() != reflect.Struct {
		t.Fatalf("service.Subscriber kind = %v, want struct", structType.Kind())
	}
	wantProps := map[string]bool{}
	for i := 0; i < structType.NumField(); i++ {
		f := structType.Field(i)
		tag := f.Tag.Get("json")
		name := strings.SplitN(tag, ",", 2)[0]
		if name == "" || name == "-" {
			t.Fatalf("service.Subscriber field %s has no json name", f.Name)
		}
		wantProps[name] = true
		// Presence semantics per field, from the struct tag.
		omitempty := strings.Contains(tag, ",omitempty")
		switch name {
		case "name":
			if f.Type.Kind() != reflect.Pointer {
				t.Fatalf("service.Subscriber.Name type = %v, want *string", f.Type)
			}
			if !omitempty {
				t.Fatal("service.Subscriber.Name tag lacks omitempty — doc presence claim would be wrong")
			}
		default:
			if omitempty {
				t.Fatalf("service.Subscriber.%s tag has unexpected omitempty", f.Name)
			}
		}
	}
	if len(docSchema.Props) != len(wantProps) {
		t.Fatalf("Subscriber doc properties = %v, want exactly %v (service.Subscriber)",
			docKeysOf(docSchema.Props), docKeysOf(wantProps))
	}
	for name := range wantProps {
		if _, ok := docSchema.Props[name]; !ok {
			t.Fatalf("Subscriber doc missing property %q (struct field)", name)
		}
	}
	var prop struct {
		Type     string `json:"type"`
		Format   string `json:"format"`
		Nullable bool   `json:"nullable"`
	}
	decodeProp := func(name string) {
		t.Helper()
		prop = struct {
			Type     string `json:"type"`
			Format   string `json:"format"`
			Nullable bool   `json:"nullable"`
		}{}
		if err := json.Unmarshal(docSchema.Props[name], &prop); err != nil {
			t.Fatalf("decode Subscriber.%s: %v", name, err)
		}
	}
	decodeProp("user_id")
	if prop.Type != "string" || prop.Format != "uuid" {
		t.Fatalf("Subscriber.user_id = {%s, %s}, want {string, uuid}", prop.Type, prop.Format)
	}
	decodeProp("email")
	if prop.Type != "string" || prop.Format != "email" {
		t.Fatalf("Subscriber.email = {%s, %s}, want {string, email}", prop.Type, prop.Format)
	}
	decodeProp("name")
	if prop.Type != "string" {
		t.Fatalf("Subscriber.name type = %q, want string", prop.Type)
	}
	if prop.Nullable {
		t.Fatal("Subscriber.name is nullable in the doc, but *string + omitempty means the key is omitted when nil, never null")
	}
	required := map[string]bool{}
	for _, r := range docSchema.Required {
		required[r] = true
	}
	if !required["user_id"] || !required["email"] {
		t.Fatalf("Subscriber required = %v, want user_id + email", docSchema.Required)
	}
	if required["name"] {
		t.Fatalf("Subscriber required = %v, want name absent (omitempty: omitted when unset)", docSchema.Required)
	}
}

func TestOpenAPIDocShapesAdminAuthor(t *testing.T) {
	var spec struct {
		Paths      map[string]map[string]json.RawMessage `json:"paths"`
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(docs.OpenAPI, &spec); err != nil {
		t.Fatalf("embedded openapi spec is not valid JSON: %v", err)
	}
	decodeObj := func(raw json.RawMessage) map[string]json.RawMessage {
		t.Helper()
		var m map[string]json.RawMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("decode %s: %v", string(raw), err)
		}
		return m
	}
	// opOf returns one operation object, failing when path/method is missing.
	opOf := func(path, method string) map[string]json.RawMessage {
		t.Helper()
		ops, ok := spec.Paths[path]
		if !ok {
			t.Fatalf("spec has no path %s", path)
		}
		raw, ok := ops[method]
		if !ok {
			t.Fatalf("spec path %s has no %s operation", path, method)
		}
		return decodeObj(raw)
	}
	// responsesOf returns the responses map of one operation.
	responsesOf := func(op map[string]json.RawMessage) map[string]json.RawMessage {
		t.Helper()
		raw, ok := op["responses"]
		if !ok {
			t.Fatalf("operation has no responses: %s", string(mustJSON(t, op)))
		}
		return decodeObj(raw)
	}
	// respSchemaRef returns the $ref of a response's JSON schema
	// ("" when the response has no JSON schema).
	respSchemaRef := func(op map[string]json.RawMessage, status string) string {
		t.Helper()
		respRaw, ok := responsesOf(op)[status]
		if !ok {
			t.Fatalf("operation responses lack %q: %v", status, docKeysOf(responsesOf(op)))
		}
		resp := decodeObj(respRaw)
		contentRaw, ok := resp["content"]
		if !ok {
			return ""
		}
		media := decodeObj(decodeObj(contentRaw)["application/json"])
		var schema struct {
			Ref string `json:"$ref"`
		}
		if err := json.Unmarshal(media["schema"], &schema); err != nil {
			t.Fatalf("decode response %s schema: %v", status, err)
		}
		return schema.Ref
	}
	// respSchema returns the full schema object of a response.
	respSchema := func(op map[string]json.RawMessage, status string) map[string]json.RawMessage {
		t.Helper()
		resp := decodeObj(responsesOf(op)[status])
		content := decodeObj(resp["content"])
		media := decodeObj(content["application/json"])
		return decodeObj(media["schema"])
	}
	// wantResponse asserts a response status is documented.
	wantResponse := func(op map[string]json.RawMessage, status, why string) {
		t.Helper()
		if _, ok := responsesOf(op)[status]; !ok {
			t.Fatalf("operation responses lack %q (%s): %v", status, why, docKeysOf(responsesOf(op)))
		}
	}
	// wantNoResponse asserts a response status is NOT documented.
	wantNoResponse := func(op map[string]json.RawMessage, status, why string) {
		t.Helper()
		if _, ok := responsesOf(op)[status]; ok {
			t.Fatalf("operation documents unexpected %q (%s)", status, why)
		}
	}
	// propOf returns properties[name] of an object schema.
	propOf := func(schema map[string]json.RawMessage, name string) map[string]json.RawMessage {
		t.Helper()
		props, ok := schema["properties"]
		if !ok {
			t.Fatalf("schema has no properties: %s", string(mustJSON(t, schema)))
		}
		prop := decodeObj(props)[name]
		if prop == nil {
			t.Fatalf("schema properties lack %q", name)
		}
		return decodeObj(prop)
	}
	// schemaOf returns one top-level component schema.
	schemaOf := func(name string) map[string]json.RawMessage {
		t.Helper()
		raw, ok := spec.Components.Schemas[name]
		if !ok {
			t.Fatalf("spec has no %s schema", name)
		}
		return decodeObj(raw)
	}

	// 1. GET /api/v1/admin/users documents the page envelope
	// {items: AdminUser[], total, page, per_page} — NOT the stale
	// {users: User[]} the doc used to claim.
	listOp := opOf("/api/v1/admin/users", "get")
	listSchema := respSchema(listOp, "200")
	if keys := docKeysOf(decodeObj(listSchema["properties"])); len(keys) != 4 {
		t.Fatalf("GET /api/v1/admin/users doc envelope keys = %v, want exactly items/total/page/per_page", keys)
	} else {
		want := map[string]bool{"items": true, "total": true, "page": true, "per_page": true}
		for _, k := range keys {
			if !want[k] {
				t.Fatalf("GET /api/v1/admin/users doc envelope has unexpected key %q", k)
			}
		}
	}
	itemsProp := propOf(listSchema, "items")
	var items struct {
		Type  string `json:"type"`
		Items struct {
			Ref string `json:"$ref"`
		} `json:"items"`
	}
	if err := json.Unmarshal(mustJSON(t, itemsProp), &items); err != nil {
		t.Fatalf("decode items prop: %v", err)
	}
	if items.Type != "array" || items.Items.Ref != "#/components/schemas/AdminUser" {
		t.Fatalf("GET /api/v1/admin/users doc items = {%q, %q}, want {array, AdminUser}", items.Type, items.Items.Ref)
	}
	for _, name := range []string{"total", "page", "per_page"} {
		var prop struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(mustJSON(t, propOf(listSchema, name)), &prop); err != nil {
			t.Fatalf("decode %s prop: %v", name, err)
		}
		if prop.Type != "integer" {
			t.Fatalf("GET /api/v1/admin/users doc %s type = %q, want integer", name, prop.Type)
		}
	}
	// Pagination params match pageParams (admin_handler.go):
	// page defaults to 1, per_page to 25; page < 1 or per_page
	// outside 1-100 is 400.
	var params []struct {
		Name   string `json:"name"`
		In     string `json:"in"`
		Schema struct {
			Type    string   `json:"type"`
			Default float64  `json:"default"`
			Minimum *float64 `json:"minimum"`
			Maximum *float64 `json:"maximum"`
		} `json:"schema"`
	}
	if err := json.Unmarshal(listOp["parameters"], &params); err != nil {
		t.Fatalf("decode GET /api/v1/admin/users parameters: %v", err)
	}
	byName := map[string]int{}
	for i, p := range params {
		byName[p.Name] = i
	}
	pageIdx, ok := byName["page"]
	if !ok {
		t.Fatalf("GET /api/v1/admin/users doc lacks page param: %v", params)
	}
	if params[pageIdx].Schema.Type != "integer" || params[pageIdx].Schema.Default != 1 ||
		params[pageIdx].Schema.Minimum == nil || *params[pageIdx].Schema.Minimum != 1 {
		t.Fatalf("GET /api/v1/admin/users doc page param = %+v, want integer default 1 minimum 1", params[pageIdx].Schema)
	}
	perPageIdx, ok := byName["per_page"]
	if !ok {
		t.Fatalf("GET /api/v1/admin/users doc lacks per_page param")
	}
	if params[perPageIdx].Schema.Type != "integer" || params[perPageIdx].Schema.Default != 25 ||
		params[perPageIdx].Schema.Minimum == nil || *params[perPageIdx].Schema.Minimum != 1 ||
		params[perPageIdx].Schema.Maximum == nil || *params[perPageIdx].Schema.Maximum != 100 {
		t.Fatalf("GET /api/v1/admin/users doc per_page param = %+v, want integer default 25 minimum 1 maximum 100", params[perPageIdx].Schema)
	}
	wantResponse(listOp, "400", "pageParams rejects page < 1 / per_page outside 1-100")

	// 2. AdminUser schema matches service.AdminUser field-for-field,
	// derived from the Go struct via reflection (not hard-coded).
	// Every Go field has NO omitempty, so every key is always present
	// and every field is required; name is *string, so it is
	// nullable:true (key present, null when unset).
	adminUser := schemaOf("AdminUser")
	var adminDoc struct {
		Type     string                     `json:"type"`
		Required []string                   `json:"required"`
		Props    map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(mustJSON(t, adminUser), &adminDoc); err != nil {
		t.Fatalf("decode AdminUser schema: %v", err)
	}
	if adminDoc.Type != "object" {
		t.Fatalf("AdminUser schema type = %q, want object", adminDoc.Type)
	}
	structType := reflect.TypeOf(service.AdminUser{})
	wantProps := map[string]bool{}
	for i := 0; i < structType.NumField(); i++ {
		f := structType.Field(i)
		tag := f.Tag.Get("json")
		name := strings.SplitN(tag, ",", 2)[0]
		if name == "" || name == "-" {
			t.Fatalf("service.AdminUser field %s has no json name", f.Name)
		}
		if strings.Contains(tag, ",omitempty") {
			t.Fatalf("service.AdminUser.%s tag has omitempty — doc always-present claim would be wrong", f.Name)
		}
		wantProps[name] = true
		switch name {
		case "id", "email":
			if f.Type.Kind() != reflect.String {
				t.Fatalf("service.AdminUser.%s type = %v, want string", f.Name, f.Type)
			}
		case "name":
			if f.Type.Kind() != reflect.Pointer || f.Type.Elem().Kind() != reflect.String {
				t.Fatalf("service.AdminUser.Name type = %v, want *string", f.Type)
			}
		case "is_admin", "is_active":
			if f.Type.Kind() != reflect.Bool {
				t.Fatalf("service.AdminUser.%s type = %v, want bool", f.Name, f.Type)
			}
		case "workspace_count":
			if f.Type.Kind() != reflect.Int64 {
				t.Fatalf("service.AdminUser.WorkspaceCount type = %v, want int64", f.Type)
			}
		case "created_at":
			if f.Type.String() != "time.Time" {
				t.Fatalf("service.AdminUser.CreatedAt type = %v, want time.Time", f.Type)
			}
		default:
			t.Fatalf("service.AdminUser has unexpected field %s (doc must be re-checked)", f.Name)
		}
	}
	if len(adminDoc.Props) != len(wantProps) {
		t.Fatalf("AdminUser doc properties = %v, want exactly %v (service.AdminUser)",
			docKeysOf(adminDoc.Props), docKeysOf(wantProps))
	}
	for name := range wantProps {
		if _, ok := adminDoc.Props[name]; !ok {
			t.Fatalf("AdminUser doc missing property %q (struct field)", name)
		}
	}
	required := map[string]bool{}
	for _, r := range adminDoc.Required {
		required[r] = true
	}
	if len(required) != len(wantProps) {
		t.Fatalf("AdminUser required = %v, want every field (no omitempty anywhere)", adminDoc.Required)
	}
	for name := range wantProps {
		if !required[name] {
			t.Fatalf("AdminUser required lacks %q (field has no omitempty: always present)", name)
		}
	}
	var prop struct {
		Type     string `json:"type"`
		Format   string `json:"format"`
		Nullable bool   `json:"nullable"`
	}
	decodeAdminProp := func(name string) {
		t.Helper()
		prop = struct {
			Type     string `json:"type"`
			Format   string `json:"format"`
			Nullable bool   `json:"nullable"`
		}{}
		if err := json.Unmarshal(adminDoc.Props[name], &prop); err != nil {
			t.Fatalf("decode AdminUser.%s: %v", name, err)
		}
	}
	decodeAdminProp("id")
	if prop.Type != "string" || prop.Format != "uuid" || prop.Nullable {
		t.Fatalf("AdminUser.id = {%s, %s, nullable=%v}, want {string, uuid, false}", prop.Type, prop.Format, prop.Nullable)
	}
	decodeAdminProp("email")
	if prop.Type != "string" || prop.Format != "email" || prop.Nullable {
		t.Fatalf("AdminUser.email = {%s, %s, nullable=%v}, want {string, email, false}", prop.Type, prop.Format, prop.Nullable)
	}
	decodeAdminProp("name")
	if prop.Type != "string" || !prop.Nullable {
		t.Fatalf("AdminUser.name = {%s, nullable=%v}, want {string, nullable=true} (*string, no omitempty: always present, null when unset)", prop.Type, prop.Nullable)
	}
	for _, name := range []string{"is_admin", "is_active"} {
		decodeAdminProp(name)
		if prop.Type != "boolean" || prop.Nullable {
			t.Fatalf("AdminUser.%s = {%s, nullable=%v}, want {boolean, false}", name, prop.Type, prop.Nullable)
		}
	}
	decodeAdminProp("workspace_count")
	if prop.Type != "integer" || prop.Nullable {
		t.Fatalf("AdminUser.workspace_count = {%s, nullable=%v}, want {integer, false}", prop.Type, prop.Nullable)
	}
	decodeAdminProp("created_at")
	if prop.Type != "string" || prop.Format != "date-time" || prop.Nullable {
		t.Fatalf("AdminUser.created_at = {%s, %s, nullable=%v}, want {string, date-time, false}", prop.Type, prop.Format, prop.Nullable)
	}

	// 3. PATCH /api/v1/admin/users/{id}: request is exactly
	// {is_admin: boolean, required}; response is Ok; the old
	// {name, role} → User shape must be gone.
	patchOp := opOf("/api/v1/admin/users/{id}", "patch")
	var summary struct {
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal(mustJSON(t, patchOp), &summary); err != nil {
		t.Fatalf("decode PATCH op: %v", err)
	}
	if summary.Summary != "Set a user's instance-admin flag" {
		t.Fatalf("PATCH /api/v1/admin/users/{id} summary = %q, want the instance-admin-flag wording (role is NOT settable here)", summary.Summary)
	}
	var body struct {
		Required []string                   `json:"required"`
		Props    map[string]json.RawMessage `json:"properties"`
	}
	{
		var reqBody struct {
			Content struct {
				JSON struct {
					Schema struct {
						Required []string                   `json:"required"`
						Props    map[string]json.RawMessage `json:"properties"`
					} `json:"schema"`
				} `json:"application/json"`
			} `json:"content"`
		}
		if err := json.Unmarshal(patchOp["requestBody"], &reqBody); err != nil {
			t.Fatalf("decode PATCH requestBody: %v", err)
		}
		body.Required = reqBody.Content.JSON.Schema.Required
		body.Props = reqBody.Content.JSON.Schema.Props
	}
	if len(body.Props) != 1 {
		t.Fatalf("PATCH /api/v1/admin/users/{id} request properties = %v, want exactly is_admin", docKeysOf(body.Props))
	}
	if _, ok := body.Props["is_admin"]; !ok {
		t.Fatalf("PATCH /api/v1/admin/users/{id} request lacks is_admin (old name/role shape must be gone)")
	}
	if len(body.Required) != 1 || body.Required[0] != "is_admin" {
		t.Fatalf("PATCH /api/v1/admin/users/{id} request required = %v, want [is_admin] (missing = 400)", body.Required)
	}
	var isAdminProp struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(body.Props["is_admin"], &isAdminProp); err != nil {
		t.Fatalf("decode is_admin prop: %v", err)
	}
	if isAdminProp.Type != "boolean" {
		t.Fatalf("PATCH is_admin type = %q, want boolean", isAdminProp.Type)
	}
	if got := respSchemaRef(patchOp, "200"); got != "#/components/schemas/Ok" {
		t.Fatalf("PATCH /api/v1/admin/users/{id} 200 schema $ref = %q, want Ok", got)
	}
	wantResponse(patchOp, "400", "missing is_admin / malformed id")
	wantResponse(patchOp, "404", "unknown user")
	wantResponse(patchOp, "409", "self-demotion + last-admin demotion (SetUserAdmin)")

	// 4. Deactivate/reactivate: both return Ok (no drift there —
	// the doc already said Ok). Deactivate documents the 409
	// self-deactivation case; reactivate documents NO 409 —
	// ReactivateUser has no conflict path (no self guard, no
	// already-active rejection), so a 409 there would be a lie.
	deactOp := opOf("/api/v1/admin/users/{id}/deactivate", "post")
	if got := respSchemaRef(deactOp, "200"); got != "#/components/schemas/Ok" {
		t.Fatalf("POST .../deactivate 200 schema $ref = %q, want Ok", got)
	}
	wantResponse(deactOp, "400", "malformed id")
	wantResponse(deactOp, "404", "unknown user")
	wantResponse(deactOp, "409", "self-deactivation (DeactivateUser)")
	reactOp := opOf("/api/v1/admin/users/{id}/reactivate", "post")
	if got := respSchemaRef(reactOp, "200"); got != "#/components/schemas/Ok" {
		t.Fatalf("POST .../reactivate 200 schema $ref = %q, want Ok", got)
	}
	wantResponse(reactOp, "400", "malformed id")
	wantResponse(reactOp, "404", "unknown user")
	wantNoResponse(reactOp, "409", "ReactivateUser has no conflict path")

	// 5. ProjectUpdate.author + PageComment.author point at
	// ActorRef (the Go field is *ActorRef), nullable:true —
	// kept because the scan funcs leave Author nil when the
	// author row is gone (FK ON DELETE SET NULL → author_id
	// NULL → Author stays nil → marshals as "author": null,
	// since the field has no omitempty).
	for _, name := range []string{"ProjectUpdate", "PageComment"} {
		schema := schemaOf(name)
		author := propOf(schema, "author")
		var authorProp struct {
			Ref      string `json:"$ref"`
			Nullable bool   `json:"nullable"`
		}
		if err := json.Unmarshal(mustJSON(t, author), &authorProp); err != nil {
			t.Fatalf("decode %s.author: %v", name, err)
		}
		if authorProp.Ref != "#/components/schemas/ActorRef" {
			t.Fatalf("%s.author $ref = %q, want ActorRef (Go field is *ActorRef, not *User)", name, authorProp.Ref)
		}
		if !authorProp.Nullable {
			t.Fatalf("%s.author nullable = false, want true (deleted author → null)", name)
		}
	}
}

func TestOpenAPIDocShapesAdminStats(t *testing.T) {
	var spec struct {
		Paths      map[string]map[string]json.RawMessage `json:"paths"`
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(docs.OpenAPI, &spec); err != nil {
		t.Fatalf("embedded openapi spec is not valid JSON: %v", err)
	}

	// 1. GET /api/v1/admin/stats documents a 200 response whose
	// schema is exactly the AdminStats component — the endpoint
	// must not drift to an inline shape that bypasses the pin below.
	ops, ok := spec.Paths["/api/v1/admin/stats"]
	if !ok {
		t.Fatal("spec has no path /api/v1/admin/stats")
	}
	var op struct {
		Responses map[string]struct {
			Content map[string]struct {
				Schema struct {
					Ref string `json:"$ref"`
				} `json:"schema"`
			} `json:"content"`
		} `json:"responses"`
	}
	if err := json.Unmarshal(ops["get"], &op); err != nil {
		t.Fatalf("decode GET /api/v1/admin/stats op: %v", err)
	}
	if got := op.Responses["200"].Content["application/json"].Schema.Ref; got != "#/components/schemas/AdminStats" {
		t.Fatalf("GET /api/v1/admin/stats 200 schema $ref = %q, want AdminStats", got)
	}

	// 2. AdminStats schema matches service.AdminStats field-for-
	// field, derived from the Go struct via reflection (not
	// hard-coded) the way C55T0/T1 pinned Subscriber/AdminUser:
	// every json tag name must be a doc property and vice versa.
	// This is the regression pin for the attachment_bytes drift
	// (the Go struct had attachment_bytes; the doc omitted it).
	raw, ok := spec.Components.Schemas["AdminStats"]
	if !ok {
		t.Fatal("spec has no AdminStats schema")
	}
	var docSchema struct {
		Type  string                     `json:"type"`
		Props map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &docSchema); err != nil {
		t.Fatalf("decode AdminStats schema: %v", err)
	}
	if docSchema.Type != "object" {
		t.Fatalf("AdminStats schema type = %q, want object", docSchema.Type)
	}
	structType := reflect.TypeOf(service.AdminStats{})
	wantProps := map[string]bool{}
	for i := 0; i < structType.NumField(); i++ {
		f := structType.Field(i)
		tag := f.Tag.Get("json")
		name := strings.SplitN(tag, ",", 2)[0]
		if name == "" || name == "-" {
			t.Fatalf("service.AdminStats field %s has no json name", f.Name)
		}
		if strings.Contains(tag, ",omitempty") {
			t.Fatalf("service.AdminStats.%s tag has omitempty — doc always-present claim would be wrong", f.Name)
		}
		if f.Type.Kind() != reflect.Int64 {
			t.Fatalf("service.AdminStats.%s type = %v, want int64", f.Name, f.Type)
		}
		wantProps[name] = true
	}
	if len(docSchema.Props) != len(wantProps) {
		t.Fatalf("AdminStats doc properties = %v, want exactly %v (service.AdminStats)",
			docKeysOf(docSchema.Props), docKeysOf(wantProps))
	}
	for name := range wantProps {
		if _, ok := docSchema.Props[name]; !ok {
			t.Fatalf("AdminStats doc missing property %q (struct field)", name)
		}
	}
	var prop struct {
		Type        string `json:"type"`
		Description string `json:"description"`
		Nullable    bool   `json:"nullable"`
	}
	for name := range wantProps {
		prop = struct {
			Type        string `json:"type"`
			Description string `json:"description"`
			Nullable    bool   `json:"nullable"`
		}{}
		if err := json.Unmarshal(docSchema.Props[name], &prop); err != nil {
			t.Fatalf("decode AdminStats.%s: %v", name, err)
		}
		if prop.Type != "integer" || prop.Nullable {
			t.Fatalf("AdminStats.%s = {%s, nullable=%v}, want {integer, false} (int64, no omitempty)", name, prop.Type, prop.Nullable)
		}
	}
	// attachment_bytes carries the one-line description matching
	// the Go field (SUM(size_bytes) over attachments); pin that it
	// is documented, not just typed.
	prop = struct {
		Type        string `json:"type"`
		Description string `json:"description"`
		Nullable    bool   `json:"nullable"`
	}{}
	if err := json.Unmarshal(docSchema.Props["attachment_bytes"], &prop); err != nil {
		t.Fatalf("decode AdminStats.attachment_bytes: %v", err)
	}
	if prop.Description == "" {
		t.Fatal("AdminStats.attachment_bytes has no description (doc must say what the bytes sum over)")
	}
	if !strings.Contains(prop.Description, "size_bytes") {
		t.Fatalf("AdminStats.attachment_bytes description = %q, want it to name the summed column (size_bytes)", prop.Description)
	}
}

func TestOpenAPIDocShapesIssueListParams(t *testing.T) {
	var spec struct {
		Paths      map[string]map[string]json.RawMessage `json:"paths"`
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(docs.OpenAPI, &spec); err != nil {
		t.Fatalf("embedded openapi spec is not valid JSON: %v", err)
	}
	decodeObj := func(raw json.RawMessage) map[string]json.RawMessage {
		t.Helper()
		var m map[string]json.RawMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("decode %s: %v", string(raw), err)
		}
		return m
	}
	ops, ok := spec.Paths["/api/v1/workspaces/{slug}/projects/{identifier}/issues"]
	if !ok {
		t.Fatal("spec has no issue-list path")
	}
	op := decodeObj(ops["get"])

	// 1. Endpoint description: names the real ordering + pagination
	// params, keeps the trash-mode sentence, and no longer mentions
	// the stale page/cursor, sort, or search phrasing.
	var desc struct {
		Description string `json:"description"`
	}
	if err := json.Unmarshal(mustJSON(t, op), &desc); err != nil {
		t.Fatalf("decode issue-list op: %v", err)
	}
	for _, want := range []string{
		"order_by",
		"cursor",
		"there is no page param",
		"Other filters/order are ignored in trash mode",
	} {
		if !strings.Contains(desc.Description, want) {
			t.Fatalf("issue-list description lacks %q: %q", want, desc.Description)
		}
	}
	for _, stale := range []string{"page/cursor", "sort", "search"} {
		if strings.Contains(desc.Description, stale) {
			t.Fatalf("issue-list description still mentions stale %q: %q", stale, desc.Description)
		}
	}

	// 2. Parameter set: exactly the 30 query params the handler
	// reads (parseIssueListInput + the listIssues include_links
	// branch) plus the Slug/Identifier path refs — no page,
	// no search, no sort.
	var params []struct {
		Ref    string `json:"$ref"`
		Name   string `json:"name"`
		In     string `json:"in"`
		Schema struct {
			Type        string   `json:"type"`
			Format      string   `json:"format"`
			Minimum     *float64 `json:"minimum"`
			Maximum     *float64 `json:"maximum"`
			Default     any      `json:"default"`
			Enum        []string `json:"enum"`
			Description string   `json:"description"`
		} `json:"schema"`
	}
	if err := json.Unmarshal(op["parameters"], &params); err != nil {
		t.Fatalf("decode issue-list parameters: %v", err)
	}
	pathRefs := map[string]bool{}
	byName := map[string]int{}
	for i, p := range params {
		if p.Ref != "" {
			pathRefs[p.Ref] = true
			continue
		}
		if _, dup := byName[p.Name]; dup {
			t.Fatalf("issue-list doc duplicates parameter %q", p.Name)
		}
		byName[p.Name] = i
	}
	if !pathRefs["#/components/parameters/Slug"] || !pathRefs["#/components/parameters/Identifier"] || len(pathRefs) != 2 {
		t.Fatalf("issue-list path params = %v, want exactly Slug + Identifier", docKeysOf(pathRefs))
	}
	wantParams := []string{
		"per_page", "cursor", "order_by",
		"state", "priority", "label", "assignee", "estimate", "cycle",
		"parent", "epic",
		"q", "fields", "draft", "sequence_id",
		"undated", "due_date_empty", "subscribed", "archived", "deleted",
		"include_links", "include_project_structure",
		"created_after", "created_before",
		"updated_after", "updated_before",
		"due_after", "due_before",
		"start_after", "start_before",
	}
	if len(byName) != len(wantParams) {
		t.Fatalf("issue-list query params = %v, want exactly %v", docKeysOf(byName), wantParams)
	}
	for _, name := range wantParams {
		idx, ok := byName[name]
		if !ok {
			t.Fatalf("issue-list doc lacks parameter %q", name)
		}
		if params[idx].In != "query" {
			t.Fatalf("issue-list param %q in = %q, want query", name, params[idx].In)
		}
	}
	for _, stale := range []string{"page", "search", "sort"} {
		if _, ok := byName[stale]; ok {
			t.Fatalf("issue-list doc still has stale parameter %q (handler never reads it)", stale)
		}
	}
	param := func(name string) struct {
		Ref    string `json:"$ref"`
		Name   string `json:"name"`
		In     string `json:"in"`
		Schema struct {
			Type        string   `json:"type"`
			Format      string   `json:"format"`
			Minimum     *float64 `json:"minimum"`
			Maximum     *float64 `json:"maximum"`
			Default     any      `json:"default"`
			Enum        []string `json:"enum"`
			Description string   `json:"description"`
		} `json:"schema"`
	} {
		t.Helper()
		return params[byName[name]]
	}
	num := func(name, field string, got *float64, want float64) {
		t.Helper()
		if got == nil || *got != want {
			t.Fatalf("issue-list param %q %s = %v, want %v", name, field, got, want)
		}
	}

	// 3. per_page: 1-100, default 25 — the handler REJECTS
	// out-of-range values with 400 (never clamps), so max 200
	// in the doc used to be a lie.
	pp := param("per_page")
	if pp.Schema.Type != "integer" {
		t.Fatalf("per_page type = %q, want integer", pp.Schema.Type)
	}
	num("per_page", "minimum", pp.Schema.Minimum, 1)
	num("per_page", "maximum", pp.Schema.Maximum, 100)
	if d, ok := pp.Schema.Default.(float64); !ok || d != 25 {
		t.Fatalf("per_page default = %v (%T), want 25", pp.Schema.Default, pp.Schema.Default)
	}

	// 4. order_by: exactly the listOrders whitelist from
	// internal/service/issue.go (default -updated_at); an
	// invalid value is 400 ErrInvalidOrderBy. No "sort" param.
	ob := param("order_by")
	if ob.Schema.Type != "string" {
		t.Fatalf("order_by type = %q, want string", ob.Schema.Type)
	}
	wantOrderBy := []string{
		"updated_at", "-updated_at",
		"created_at", "-created_at",
		"sequence_id", "-sequence_id",
		"sort_order", "-sort_order",
		"priority", "-priority",
	}
	if !reflect.DeepEqual(ob.Schema.Enum, wantOrderBy) {
		t.Fatalf("order_by enum = %v, want exactly the service whitelist %v", ob.Schema.Enum, wantOrderBy)
	}
	if d, ok := ob.Schema.Default.(string); !ok || d != "-updated_at" {
		t.Fatalf("order_by default = %v, want -updated_at (the service default when empty)", ob.Schema.Default)
	}

	// 5. q, not search: the handler reads qp.Get("q") for the
	// full-text filter; "search" was never read.
	q := param("q")
	if q.Schema.Type != "string" || !strings.Contains(q.Schema.Description, "Full-text") {
		t.Fatalf("q param = %+v, want string full-text description", q.Schema)
	}

	// 6. Validation semantics spot-checks, each verified in code
	// (parseIssueListInput / validateListFilterValues /
	// ListIssues / ListDeletedIssues).
	draft := param("draft")
	if !reflect.DeepEqual(draft.Schema.Enum, []string{"true", "false", "1", "0"}) {
		t.Fatalf("draft enum = %v, want the tri-state values [true false 1 0]", draft.Schema.Enum)
	}
	seq := param("sequence_id")
	if seq.Schema.Type != "integer" {
		t.Fatalf("sequence_id type = %q, want integer", seq.Schema.Type)
	}
	num("sequence_id", "minimum", seq.Schema.Minimum, 1)
	if !strings.Contains(seq.Schema.Description, "Bypasses the draft filter") {
		t.Fatalf("sequence_id description lacks the draft-bypass note: %q", seq.Schema.Description)
	}
	deleted := param("deleted")
	if !reflect.DeepEqual(deleted.Schema.Enum, []string{"1", "true"}) {
		t.Fatalf("deleted enum = %v, want [1 true]", deleted.Schema.Enum)
	}
	if !strings.Contains(deleted.Schema.Description, "Other filters/order are ignored in trash mode") {
		t.Fatalf("deleted description lacks the trash-mode sentence: %q", deleted.Schema.Description)
	}
	for _, name := range []string{
		"created_after", "created_before",
		"updated_after", "updated_before",
		"due_after", "due_before",
		"start_after", "start_before",
	} {
		p := param(name)
		if p.Schema.Format != "date-time" || !strings.Contains(p.Schema.Description, "RFC3339") {
			t.Fatalf("%s param = %+v, want date-time + RFC3339/400 description", name, p.Schema)
		}
	}
	for _, name := range []string{"state", "cycle"} {
		if param(name).Schema.Format != "uuid" {
			t.Fatalf("%s param format = %q, want uuid (malformed UUID is 400)", name, param(name).Schema.Format)
		}
	}
	for _, name := range []string{"label", "assignee", "estimate"} {
		if !strings.Contains(param(name).Schema.Description, "UUID") {
			t.Fatalf("%s description lacks UUID: %q", name, param(name).Schema.Description)
		}
	}
	for _, name := range []string{"assignee", "estimate"} {
		if !strings.Contains(param(name).Schema.Description, `"none"`) {
			t.Fatalf("%s description lacks the \"none\" sentinel: %q", name, param(name).Schema.Description)
		}
	}
	// C75T0: parent=/epic= are comma-separated (repeatable), UUID-valued,
	// "none"-sentineled filters with 400-on-malformed semantics.
	for _, name := range []string{"parent", "epic"} {
		desc := param(name).Schema.Description
		for _, want := range []string{"UUID", `"none"`, "malformed", "repeatable"} {
			if !strings.Contains(desc, want) {
				t.Fatalf("%s description lacks %q: %q", name, want, desc)
			}
		}
	}
	for _, name := range []string{"include_links", "include_project_structure"} {
		p := param(name)
		if !reflect.DeepEqual(p.Schema.Enum, []string{"1", "true"}) {
			t.Fatalf("%s enum = %v, want [1 true]", name, p.Schema.Enum)
		}
		if !strings.Contains(strings.ToLower(p.Schema.Description), "trash mode") {
			t.Fatalf("%s description lacks the trash-mode note: %q", name, p.Schema.Description)
		}
	}
	if !strings.Contains(param("fields").Schema.Description, "description") {
		t.Fatalf("fields description lacks the description-only note: %q", param("fields").Schema.Description)
	}
	if param("cursor").Schema.Type != "string" {
		t.Fatalf("cursor type = %q, want string", param("cursor").Schema.Type)
	}

	// 7. Responses: 400 (every invalid param above) + 404
	// (bad slug / non-member caller) are documented.
	responses := decodeObj(op["responses"])
	if _, ok := responses["400"]; !ok {
		t.Fatal("issue-list doc lacks the 400 response (invalid params are 400)")
	}
	if _, ok := responses["404"]; !ok {
		t.Fatal("issue-list doc lacks the 404 response (bad slug / non-member)")
	}

	// 8. ArchiveImportReport is a real top-level schema (same
	// fields as the old nested copy) and NOT nested under
	// ImportResult.properties anymore.
	raw, ok := spec.Components.Schemas["ArchiveImportReport"]
	if !ok {
		t.Fatal("spec has no top-level ArchiveImportReport schema")
	}
	var report struct {
		Type     string                     `json:"type"`
		Required []string                   `json:"required"`
		Props    map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("decode ArchiveImportReport: %v", err)
	}
	if report.Type != "object" {
		t.Fatalf("ArchiveImportReport type = %q, want object", report.Type)
	}
	wantReportProps := map[string]bool{
		"format": true, "source_workspace": true, "target_workspace": true,
		"imported": true, "skipped": true,
	}
	if len(report.Props) != len(wantReportProps) {
		t.Fatalf("ArchiveImportReport properties = %v, want exactly %v", docKeysOf(report.Props), docKeysOf(wantReportProps))
	}
	for name := range wantReportProps {
		if _, ok := report.Props[name]; !ok {
			t.Fatalf("ArchiveImportReport lacks property %q", name)
		}
	}
	wantRequired := map[string]bool{"format": true, "target_workspace": true, "imported": true, "skipped": true}
	if len(report.Required) != len(wantRequired) {
		t.Fatalf("ArchiveImportReport required = %v, want %v", report.Required, docKeysOf(wantRequired))
	}
	for _, r := range report.Required {
		if !wantRequired[r] {
			t.Fatalf("ArchiveImportReport required has unexpected %q", r)
		}
	}
	importRaw, ok := spec.Components.Schemas["ImportResult"]
	if !ok {
		t.Fatal("spec has no ImportResult schema")
	}
	var importResult struct {
		Props map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(importRaw, &importResult); err != nil {
		t.Fatalf("decode ImportResult: %v", err)
	}
	if _, nested := importResult.Props["ArchiveImportReport"]; nested {
		t.Fatal("ArchiveImportReport is still nested under ImportResult.properties — the hoist did not happen")
	}
	wantImportProps := map[string]bool{"created": true, "failed": true, "errors": true}
	if len(importResult.Props) != len(wantImportProps) {
		t.Fatalf("ImportResult properties = %v, want exactly %v", docKeysOf(importResult.Props), docKeysOf(wantImportProps))
	}
}

// resolveOpenAPIRef resolves a local JSON-pointer $ref ("#/...")
// against doc, reporting whether every pointer segment exists.
// JSON Pointer unescaping: ~1 -> / first, then ~0 -> ~.
func resolveOpenAPIRef(doc any, ref string) bool {
	if !strings.HasPrefix(ref, "#/") {
		return false
	}
	cur := doc
	for _, seg := range strings.Split(ref[2:], "/") {
		seg = strings.ReplaceAll(strings.ReplaceAll(seg, "~1", "/"), "~0", "~")
		m, ok := cur.(map[string]any)
		if !ok {
			return false
		}
		cur, ok = m[seg]
		if !ok {
			return false
		}
	}
	return true
}

// findDanglingLocalRefs walks the whole spec and returns every local
// $ref that does not resolve, as "json-path -> $ref" strings. A $ref
// that is not a string, or not a local "#/..." pointer, is reported
// too: this spec is a single self-contained file, so every $ref in
// it must be a resolvable local pointer.
func findDanglingLocalRefs(doc any) []string {
	var dangling []string
	var walk func(v any, path string)
	walk = func(v any, path string) {
		switch n := v.(type) {
		case map[string]any:
			if raw, ok := n["$ref"]; ok {
				if ref, ok := raw.(string); ok {
					if !resolveOpenAPIRef(doc, ref) {
						dangling = append(dangling, path+" -> "+ref)
					}
				} else {
					dangling = append(dangling, path+" -> $ref is not a string")
				}
			}
			for k, child := range n {
				walk(child, path+"/"+k)
			}
		case []any:
			for i, child := range n {
				walk(child, fmt.Sprintf("%s[%d]", path, i))
			}
		}
	}
	walk(doc, "")
	return dangling
}

// TestOpenAPIAllLocalRefsResolve is the C55T2 regression pin for the
// ArchiveImportReport class of bug: the import endpoint $ref'd
// #/components/schemas/ArchiveImportReport while the only
// definition was nested inside ImportResult.properties, so any
// strict OpenAPI validator resolving the $ref failed. Resolving
// EVERY local $ref in the document makes that class of bug
// impossible to reintroduce anywhere in the spec.
func TestOpenAPIAllLocalRefsResolve(t *testing.T) {
	var doc any
	if err := json.Unmarshal(docs.OpenAPI, &doc); err != nil {
		t.Fatalf("embedded openapi spec is not valid JSON: %v", err)
	}
	if dangling := findDanglingLocalRefs(doc); len(dangling) != 0 {
		t.Fatalf("spec has %d dangling $ref(s):\n%s", len(dangling), strings.Join(dangling, "\n"))
	}
}

// TestOpenAPIRefResolverCatchesDanglingRef proves the pin above can
// fail: a resolver that always returned nil would pass
// TestOpenAPIAllLocalRefsResolve while catching nothing. It checks
// a synthetic doc (one valid ref, one dangling) plus the exact
// pre-fix shape of this bug — the real spec with the top-level
// ArchiveImportReport schema deleted.
func TestOpenAPIRefResolverCatchesDanglingRef(t *testing.T) {
	var synthetic any
	if err := json.Unmarshal([]byte(`{
		"components": { "schemas": { "Exists": { "type": "object" } } },
		"paths": {
			"/ok": { "get": { "responses": { "200": { "content": { "application/json": { "schema": { "$ref": "#/components/schemas/Exists" } } } } } } },
			"/bad": { "get": { "responses": { "200": { "content": { "application/json": { "schema": { "$ref": "#/components/schemas/Missing" } } } } } } }
		}
	}`), &synthetic); err != nil {
		t.Fatalf("decode synthetic doc: %v", err)
	}
	dangling := findDanglingLocalRefs(synthetic)
	if len(dangling) != 1 || !strings.HasSuffix(dangling[0], "#/components/schemas/Missing") {
		t.Fatalf("synthetic doc dangling = %v, want exactly the Missing $ref", dangling)
	}

	var doc map[string]any
	if err := json.Unmarshal(docs.OpenAPI, &doc); err != nil {
		t.Fatalf("embedded openapi spec is not valid JSON: %v", err)
	}
	components, ok := doc["components"].(map[string]any)
	if !ok {
		t.Fatal("spec has no components")
	}
	schemas, ok := components["schemas"].(map[string]any)
	if !ok {
		t.Fatal("spec has no component schemas")
	}
	delete(schemas, "ArchiveImportReport")
	dangling = findDanglingLocalRefs(doc)
	found := false
	for _, d := range dangling {
		if strings.HasSuffix(d, "#/components/schemas/ArchiveImportReport") {
			found = true
		}
	}
	if !found {
		t.Fatalf("pre-fix-shaped doc (no top-level ArchiveImportReport) dangling = %v, want the import endpoint's $ref reported", dangling)
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func docKeysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Doc-side pinning test (C57T2): the Webhook schema in docs/openapi.yaml
// carries the three last-delivery fields with the right types, the
// status enum, and nullable presence (present-but-null when never
// attempted — the Go fields are pointers WITHOUT omitempty, so the
// keys are never omitted). WebhookCreateResponse inherits them via
// allOf → Webhook. This is the doc-side complement to the live pins
// (TestWebhookDeliveryStatusHTTP asserts the keys in real responses).
func TestOpenAPIDocShapesWebhookDeliveryStatus(t *testing.T) {
	var spec struct {
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(docs.OpenAPI, &spec); err != nil {
		t.Fatalf("embedded openapi spec is not valid JSON: %v", err)
	}
	raw, ok := spec.Components.Schemas["Webhook"]
	if !ok {
		t.Fatal("spec has no Webhook schema")
	}
	var docSchema struct {
		Type  string                     `json:"type"`
		Props map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &docSchema); err != nil {
		t.Fatalf("decode Webhook schema: %v", err)
	}
	if docSchema.Type != "object" {
		t.Fatalf("Webhook schema type = %q, want object", docSchema.Type)
	}
	var prop struct {
		Type     string   `json:"type"`
		Format   string   `json:"format"`
		Enum     []string `json:"enum"`
		Nullable bool     `json:"nullable"`
	}
	decodeProp := func(name string) {
		t.Helper()
		prop = struct {
			Type     string   `json:"type"`
			Format   string   `json:"format"`
			Enum     []string `json:"enum"`
			Nullable bool     `json:"nullable"`
		}{}
		rawProp, ok := docSchema.Props[name]
		if !ok {
			t.Fatalf("Webhook doc missing property %q", name)
		}
		if err := json.Unmarshal(rawProp, &prop); err != nil {
			t.Fatalf("decode Webhook.%s: %v", name, err)
		}
	}
	decodeProp("last_delivery_at")
	if prop.Type != "string" || prop.Format != "date-time" || !prop.Nullable {
		t.Fatalf("Webhook.last_delivery_at = {%s, %s, nullable=%v}, want {string, date-time, true}",
			prop.Type, prop.Format, prop.Nullable)
	}
	decodeProp("last_delivery_status")
	if prop.Type != "string" || !prop.Nullable {
		t.Fatalf("Webhook.last_delivery_status = {%s, nullable=%v}, want {string, true}",
			prop.Type, prop.Nullable)
	}
	wantEnum := map[string]bool{"delivered": true, "retrying": true, "failed": true}
	if len(prop.Enum) != len(wantEnum) {
		t.Fatalf("Webhook.last_delivery_status enum = %v, want exactly %v", prop.Enum, wantEnum)
	}
	for _, e := range prop.Enum {
		if !wantEnum[e] {
			t.Fatalf("Webhook.last_delivery_status enum = %v, want exactly %v", prop.Enum, wantEnum)
		}
	}
	decodeProp("last_error")
	if prop.Type != "string" || !prop.Nullable {
		t.Fatalf("Webhook.last_error = {%s, nullable=%v}, want {string, true}",
			prop.Type, prop.Nullable)
	}

	// WebhookCreateResponse is allOf → Webhook: the create-201 shape
	// inherits the delivery fields (null on a fresh webhook) without
	// re-declaring them.
	rawCreate, ok := spec.Components.Schemas["WebhookCreateResponse"]
	if !ok {
		t.Fatal("spec has no WebhookCreateResponse schema")
	}
	var createSchema struct {
		AllOf []struct {
			Ref string `json:"$ref"`
		} `json:"allOf"`
	}
	if err := json.Unmarshal(rawCreate, &createSchema); err != nil {
		t.Fatalf("decode WebhookCreateResponse schema: %v", err)
	}
	inherits := false
	for _, ref := range createSchema.AllOf {
		if ref.Ref == "#/components/schemas/Webhook" {
			inherits = true
		}
	}
	if !inherits {
		t.Fatalf("WebhookCreateResponse allOf = %v, want a $ref to Webhook (delivery fields inherited)",
			createSchema.AllOf)
	}

	// Reflection pin: every json tag on service.Webhook (except the
	// json:"-" secret) must be a doc property and vice versa, so a
	// struct change without a doc update fails.
	structType := reflect.TypeOf(service.Webhook{})
	if structType.Kind() != reflect.Struct {
		t.Fatalf("service.Webhook kind = %v, want struct", structType.Kind())
	}
	wantProps := map[string]bool{}
	for i := 0; i < structType.NumField(); i++ {
		f := structType.Field(i)
		tag := f.Tag.Get("json")
		name := strings.SplitN(tag, ",", 2)[0]
		if name == "-" {
			continue // secret: never serialized in list/get
		}
		if name == "" {
			t.Fatalf("service.Webhook field %s has no json name", f.Name)
		}
		wantProps[name] = true
	}
	if len(docSchema.Props) != len(wantProps) {
		t.Fatalf("Webhook doc properties = %v, want exactly %v (service.Webhook minus secret)",
			docKeysOf(docSchema.Props), docKeysOf(wantProps))
	}
	for name := range wantProps {
		if _, ok := docSchema.Props[name]; !ok {
			t.Fatalf("Webhook doc missing property %q (struct field)", name)
		}
	}
}

// Doc-side pinning test (C69T0): the Webhook.filter property and the
// WebhookCondition schema in docs/openapi.yaml carry the filter
// contract — the closed field/op vocabularies, nullable presence
// (present-but-null when unset — the Go field is a pointer WITHOUT
// omitempty), and the create/update request bodies accept filter.
// This is the doc-side complement to the live pins
// (TestWebhookFilterHTTP asserts the key in real responses).
func TestOpenAPIDocShapesWebhookFilter(t *testing.T) {
	var spec struct {
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
		Paths map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(docs.OpenAPI, &spec); err != nil {
		t.Fatalf("embedded openapi spec is not valid JSON: %v", err)
	}
	var docSchema struct {
		Type  string                     `json:"type"`
		Props map[string]json.RawMessage `json:"properties"`
	}
	decodeSchema := func(name string) {
		t.Helper()
		raw, ok := spec.Components.Schemas[name]
		if !ok {
			t.Fatalf("spec has no %s schema", name)
		}
		docSchema = struct {
			Type  string                     `json:"type"`
			Props map[string]json.RawMessage `json:"properties"`
		}{}
		if err := json.Unmarshal(raw, &docSchema); err != nil {
			t.Fatalf("decode %s schema: %v", name, err)
		}
	}

	// Webhook.filter: nullable array of WebhookCondition.
	decodeSchema("Webhook")
	rawFilter, ok := docSchema.Props["filter"]
	if !ok {
		t.Fatal("Webhook doc missing property \"filter\"")
	}
	var filterProp struct {
		Type     string `json:"type"`
		Nullable bool   `json:"nullable"`
		Items    struct {
			Ref string `json:"$ref"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rawFilter, &filterProp); err != nil {
		t.Fatalf("decode Webhook.filter: %v", err)
	}
	if filterProp.Type != "array" || !filterProp.Nullable ||
		filterProp.Items.Ref != "#/components/schemas/WebhookCondition" {
		t.Fatalf("Webhook.filter = {%s, nullable=%v, items=%s}, want {array, true, WebhookCondition}",
			filterProp.Type, filterProp.Nullable, filterProp.Items.Ref)
	}

	// WebhookCondition: field/op enums pin the closed vocabularies.
	decodeSchema("WebhookCondition")
	var fieldProp, opProp struct {
		Type string   `json:"type"`
		Enum []string `json:"enum"`
	}
	if raw, ok := docSchema.Props["field"]; !ok {
		t.Fatal("WebhookCondition doc missing property \"field\"")
	} else if err := json.Unmarshal(raw, &fieldProp); err != nil {
		t.Fatalf("decode WebhookCondition.field: %v", err)
	}
	wantFields := map[string]bool{
		"priority": true, "state_group": true, "state_id": true,
		"assignee_id": true, "label_id": true, "project_id": true,
		"target_date": true,
	}
	if len(fieldProp.Enum) != len(wantFields) {
		t.Fatalf("WebhookCondition.field enum = %v, want exactly %v", fieldProp.Enum, wantFields)
	}
	for _, f := range fieldProp.Enum {
		if !wantFields[f] {
			t.Fatalf("WebhookCondition.field enum = %v, want exactly %v", fieldProp.Enum, wantFields)
		}
	}
	if raw, ok := docSchema.Props["op"]; !ok {
		t.Fatal("WebhookCondition doc missing property \"op\"")
	} else if err := json.Unmarshal(raw, &opProp); err != nil {
		t.Fatalf("decode WebhookCondition.op: %v", err)
	}
	if len(opProp.Enum) != 2 || opProp.Enum[0] != "eq" || opProp.Enum[1] != "in" {
		t.Fatalf("WebhookCondition.op enum = %v, want [eq in]", opProp.Enum)
	}
	if _, ok := docSchema.Props["value"]; !ok {
		t.Fatal("WebhookCondition doc missing property \"value\"")
	}

	// The create (POST) and update (PATCH) request bodies accept filter.
	checkRequestBody := func(path, method string, wantNullable bool) {
		t.Helper()
		rawPath, ok := spec.Paths[path]
		if !ok {
			t.Fatalf("spec has no path %s", path)
		}
		var pathItem map[string]json.RawMessage
		if err := json.Unmarshal(rawPath, &pathItem); err != nil {
			t.Fatalf("decode path %s: %v", path, err)
		}
		var op struct {
			RequestBody struct {
				Content map[string]struct {
					Schema struct {
						Props map[string]json.RawMessage `json:"properties"`
					} `json:"schema"`
				} `json:"content"`
			} `json:"requestBody"`
		}
		if err := json.Unmarshal(pathItem[method], &op); err != nil {
			t.Fatalf("decode %s %s: %v", method, path, err)
		}
		rawProp, ok := op.RequestBody.Content["application/json"].Schema.Props["filter"]
		if !ok {
			t.Fatalf("%s %s request body lacks filter property", method, path)
		}
		var prop struct {
			Nullable bool `json:"nullable"`
		}
		if err := json.Unmarshal(rawProp, &prop); err != nil {
			t.Fatalf("decode %s %s filter: %v", method, path, err)
		}
		if prop.Nullable != wantNullable {
			t.Fatalf("%s %s filter nullable = %v, want %v", method, path, prop.Nullable, wantNullable)
		}
	}
	checkRequestBody("/api/v1/workspaces/{slug}/webhooks", "post", true)
	checkRequestBody("/api/v1/workspaces/{slug}/webhooks/{id}", "patch", true)
}

// Doc-side pinning test (C59T1): the WebhookDelivery schema in
// docs/openapi.yaml matches service.WebhookDelivery field-for-field
// (reflection pin — a struct change without a doc update fails),
// carries NO data / outbox_row_id (never part of the log view), and
// the list + redeliver endpoints are documented with their params
// and response shapes. This is the doc-side complement to the live
// pins (TestWebhookDeliveriesHTTP asserts the shapes in real
// responses).
func TestOpenAPIDocShapesWebhookDelivery(t *testing.T) {
	var spec struct {
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(docs.OpenAPI, &spec); err != nil {
		t.Fatalf("embedded openapi spec is not valid JSON: %v", err)
	}
	raw, ok := spec.Components.Schemas["WebhookDelivery"]
	if !ok {
		t.Fatal("spec has no WebhookDelivery schema")
	}
	var docSchema struct {
		Type  string                     `json:"type"`
		Props map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &docSchema); err != nil {
		t.Fatalf("decode WebhookDelivery schema: %v", err)
	}
	if docSchema.Type != "object" {
		t.Fatalf("WebhookDelivery schema type = %q, want object", docSchema.Type)
	}
	var prop struct {
		Type     string   `json:"type"`
		Format   string   `json:"format"`
		Enum     []string `json:"enum"`
		Nullable bool     `json:"nullable"`
	}
	decodeProp := func(name string) {
		t.Helper()
		prop = struct {
			Type     string   `json:"type"`
			Format   string   `json:"format"`
			Enum     []string `json:"enum"`
			Nullable bool     `json:"nullable"`
		}{}
		rawProp, ok := docSchema.Props[name]
		if !ok {
			t.Fatalf("WebhookDelivery doc missing property %q", name)
		}
		if err := json.Unmarshal(rawProp, &prop); err != nil {
			t.Fatalf("decode WebhookDelivery.%s: %v", name, err)
		}
	}
	decodeProp("id")
	if prop.Type != "integer" || prop.Format != "int64" {
		t.Fatalf("WebhookDelivery.id = {%s, %s}, want {integer, int64}", prop.Type, prop.Format)
	}
	decodeProp("webhook_id")
	if prop.Type != "string" || prop.Format != "uuid" {
		t.Fatalf("WebhookDelivery.webhook_id = {%s, %s}, want {string, uuid}", prop.Type, prop.Format)
	}
	decodeProp("event")
	if prop.Type != "string" {
		t.Fatalf("WebhookDelivery.event type = %q, want string", prop.Type)
	}
	decodeProp("status")
	if prop.Type != "string" {
		t.Fatalf("WebhookDelivery.status type = %q, want string", prop.Type)
	}
	wantEnum := map[string]bool{"delivered": true, "retrying": true, "failed": true}
	if len(prop.Enum) != len(wantEnum) {
		t.Fatalf("WebhookDelivery.status enum = %v, want exactly %v", prop.Enum, wantEnum)
	}
	for _, e := range prop.Enum {
		if !wantEnum[e] {
			t.Fatalf("WebhookDelivery.status enum = %v, want exactly %v", prop.Enum, wantEnum)
		}
	}
	decodeProp("http_status")
	if prop.Type != "integer" || !prop.Nullable {
		t.Fatalf("WebhookDelivery.http_status = {%s, nullable=%v}, want {integer, true}", prop.Type, prop.Nullable)
	}
	decodeProp("error")
	if prop.Type != "string" || !prop.Nullable {
		t.Fatalf("WebhookDelivery.error = {%s, nullable=%v}, want {string, true}", prop.Type, prop.Nullable)
	}
	decodeProp("duration_ms")
	if prop.Type != "integer" {
		t.Fatalf("WebhookDelivery.duration_ms type = %q, want integer", prop.Type)
	}
	decodeProp("attempted_at")
	if prop.Type != "string" || prop.Format != "date-time" {
		t.Fatalf("WebhookDelivery.attempted_at = {%s, %s}, want {string, date-time}", prop.Type, prop.Format)
	}
	// The log view never carries the payload or the internal
	// correlation id.
	for _, forbidden := range []string{"data", "outbox_row_id"} {
		if _, ok := docSchema.Props[forbidden]; ok {
			t.Fatalf("WebhookDelivery doc must not carry %q", forbidden)
		}
	}

	// Reflection pin: every json tag on service.WebhookDelivery must
	// be a doc property and vice versa.
	structType := reflect.TypeOf(service.WebhookDelivery{})
	if structType.Kind() != reflect.Struct {
		t.Fatalf("service.WebhookDelivery kind = %v, want struct", structType.Kind())
	}
	wantProps := map[string]bool{}
	for i := 0; i < structType.NumField(); i++ {
		f := structType.Field(i)
		tag := f.Tag.Get("json")
		name := strings.SplitN(tag, ",", 2)[0]
		if name == "-" || name == "" {
			t.Fatalf("service.WebhookDelivery field %s has no json name", f.Name)
		}
		wantProps[name] = true
	}
	if len(docSchema.Props) != len(wantProps) {
		t.Fatalf("WebhookDelivery doc properties = %v, want exactly %v (service.WebhookDelivery)",
			docKeysOf(docSchema.Props), docKeysOf(wantProps))
	}
	for name := range wantProps {
		if _, ok := docSchema.Props[name]; !ok {
			t.Fatalf("WebhookDelivery doc missing property %q (struct field)", name)
		}
	}

	// Endpoints: list + redeliver documented with params + responses.
	paths := loadOpenAPISpec(t)
	listPath := "/api/v1/workspaces/{slug}/webhooks/{id}/deliveries"
	ops, ok := paths[listPath]
	if !ok {
		t.Fatalf("spec has no path %q", listPath)
	}
	var listOp struct {
		Parameters []struct {
			Name     string `json:"name"`
			In       string `json:"in"`
			Required bool   `json:"required"`
			Ref      string `json:"$ref"`
		} `json:"parameters"`
		Responses map[string]json.RawMessage `json:"responses"`
	}
	if err := json.Unmarshal(ops["get"], &listOp); err != nil {
		t.Fatalf("decode list-deliveries operation: %v", err)
	}
	wantParams := map[string]bool{"slug": true, "id": true, "limit": true, "before": true}
	for _, p := range listOp.Parameters {
		if p.Ref == "#/components/parameters/Slug" {
			delete(wantParams, "slug")
			continue
		}
		delete(wantParams, p.Name)
	}
	if len(wantParams) != 0 {
		t.Fatalf("list-deliveries params missing %v", docKeysOf(wantParams))
	}
	for _, want := range []string{"200", "400", "403", "404"} {
		if _, ok := listOp.Responses[want]; !ok {
			t.Fatalf("list-deliveries responses lack %q", want)
		}
	}
	var listOK struct {
		Content map[string]struct {
			Schema struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"schema"`
		} `json:"content"`
	}
	if err := json.Unmarshal(listOp.Responses["200"], &listOK); err != nil {
		t.Fatalf("decode list-deliveries 200: %v", err)
	}
	listSchema := listOK.Content["application/json"].Schema
	if _, ok := listSchema.Properties["deliveries"]; !ok {
		t.Fatal("list-deliveries 200 schema lacks deliveries")
	}
	if _, ok := listSchema.Properties["next_before"]; !ok {
		t.Fatal("list-deliveries 200 schema lacks next_before")
	}

	redeliverPath := "/api/v1/workspaces/{slug}/webhooks/{id}/deliveries/{deliveryId}/redeliver"
	ops, ok = paths[redeliverPath]
	if !ok {
		t.Fatalf("spec has no path %q", redeliverPath)
	}
	var redeliverOp struct {
		Parameters []struct {
			Name string `json:"name"`
			Ref  string `json:"$ref"`
		} `json:"parameters"`
		Responses map[string]json.RawMessage `json:"responses"`
	}
	if err := json.Unmarshal(ops["post"], &redeliverOp); err != nil {
		t.Fatalf("decode redeliver operation: %v", err)
	}
	wantParams = map[string]bool{"slug": true, "id": true, "deliveryId": true}
	for _, p := range redeliverOp.Parameters {
		if p.Ref == "#/components/parameters/Slug" {
			delete(wantParams, "slug")
			continue
		}
		delete(wantParams, p.Name)
	}
	if len(wantParams) != 0 {
		t.Fatalf("redeliver params missing %v", docKeysOf(wantParams))
	}
	for _, want := range []string{"202", "400", "403", "404", "409"} {
		if _, ok := redeliverOp.Responses[want]; !ok {
			t.Fatalf("redeliver responses lack %q", want)
		}
	}
}

// Doc-side pinning test (C60T1): the WebhookDeliveryDetail schema in
// docs/openapi.yaml matches service.WebhookDeliveryDetail field-for-field
// (reflection pin — the struct embeds service.WebhookDelivery plus the
// `data` payload; a struct change without a doc update fails), carries NO
// outbox_row_id (internal correlation, never exposed), and the delivery-
// detail GET endpoint is documented with its params and response shapes.
// This is the doc-side complement to the live pins
// (TestGetWebhookDeliveryHTTP asserts the shapes in real responses).
func TestOpenAPIDocShapesWebhookDeliveryDetail(t *testing.T) {
	var spec struct {
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(docs.OpenAPI, &spec); err != nil {
		t.Fatalf("embedded openapi spec is not valid JSON: %v", err)
	}
	raw, ok := spec.Components.Schemas["WebhookDeliveryDetail"]
	if !ok {
		t.Fatal("spec has no WebhookDeliveryDetail schema")
	}
	var docSchema struct {
		Type  string                     `json:"type"`
		Props map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &docSchema); err != nil {
		t.Fatalf("decode WebhookDeliveryDetail schema: %v", err)
	}
	if docSchema.Type != "object" {
		t.Fatalf("WebhookDeliveryDetail schema type = %q, want object", docSchema.Type)
	}

	// The detail shape is the list shape PLUS data.
	var dataProp struct {
		Type string `json:"type"`
	}
	rawData, ok := docSchema.Props["data"]
	if !ok {
		t.Fatal("WebhookDeliveryDetail doc missing property \"data\"")
	}
	if err := json.Unmarshal(rawData, &dataProp); err != nil {
		t.Fatalf("decode WebhookDeliveryDetail.data: %v", err)
	}
	if dataProp.Type != "object" {
		t.Fatalf("WebhookDeliveryDetail.data type = %q, want object", dataProp.Type)
	}
	// Internal correlation id is never part of the exposed shape.
	if _, ok := docSchema.Props["outbox_row_id"]; ok {
		t.Fatal("WebhookDeliveryDetail doc must not carry \"outbox_row_id\"")
	}

	// Reflection pin: every json tag on service.WebhookDeliveryDetail
	// (flattening the embedded service.WebhookDelivery) must be a doc
	// property and vice versa.
	structType := reflect.TypeOf(service.WebhookDeliveryDetail{})
	if structType.Kind() != reflect.Struct {
		t.Fatalf("service.WebhookDeliveryDetail kind = %v, want struct", structType.Kind())
	}
	jsonName := func(f reflect.StructField) string {
		return strings.SplitN(f.Tag.Get("json"), ",", 2)[0]
	}
	wantProps := map[string]bool{}
	for i := 0; i < structType.NumField(); i++ {
		f := structType.Field(i)
		fields := []reflect.StructField{f}
		if f.Anonymous {
			fields = fields[:0]
			for j := 0; j < f.Type.NumField(); j++ {
				fields = append(fields, f.Type.Field(j))
			}
		}
		for _, ef := range fields {
			name := jsonName(ef)
			if name == "-" || name == "" {
				t.Fatalf("service.WebhookDeliveryDetail field %s has no json name", ef.Name)
			}
			wantProps[name] = true
		}
	}
	if len(docSchema.Props) != len(wantProps) {
		t.Fatalf("WebhookDeliveryDetail doc properties = %v, want exactly %v (service.WebhookDeliveryDetail)",
			docKeysOf(docSchema.Props), docKeysOf(wantProps))
	}
	for name := range wantProps {
		if _, ok := docSchema.Props[name]; !ok {
			t.Fatalf("WebhookDeliveryDetail doc missing property %q (struct field)", name)
		}
	}

	// Endpoint: the detail GET documented with params + responses.
	paths := loadOpenAPISpec(t)
	detailPath := "/api/v1/workspaces/{slug}/webhooks/{id}/deliveries/{deliveryId}"
	ops, ok := paths[detailPath]
	if !ok {
		t.Fatalf("spec has no path %q", detailPath)
	}
	var detailOp struct {
		Parameters []struct {
			Name string `json:"name"`
			Ref  string `json:"$ref"`
		} `json:"parameters"`
		Responses map[string]json.RawMessage `json:"responses"`
	}
	if err := json.Unmarshal(ops["get"], &detailOp); err != nil {
		t.Fatalf("decode detail-delivery operation: %v", err)
	}
	wantParams := map[string]bool{"slug": true, "id": true, "deliveryId": true}
	for _, p := range detailOp.Parameters {
		if p.Ref == "#/components/parameters/Slug" {
			delete(wantParams, "slug")
			continue
		}
		delete(wantParams, p.Name)
	}
	if len(wantParams) != 0 {
		t.Fatalf("detail-delivery params missing %v", docKeysOf(wantParams))
	}
	for _, want := range []string{"200", "400", "403", "404"} {
		if _, ok := detailOp.Responses[want]; !ok {
			t.Fatalf("detail-delivery responses lack %q", want)
		}
	}
	// 200 wraps the detail under a `delivery` property pointing at
	// the WebhookDeliveryDetail schema.
	var detailOK struct {
		Content map[string]struct {
			Schema struct {
				Properties map[string]struct {
					Ref string `json:"$ref"`
				} `json:"properties"`
			} `json:"schema"`
		} `json:"content"`
	}
	if err := json.Unmarshal(detailOp.Responses["200"], &detailOK); err != nil {
		t.Fatalf("decode detail-delivery 200: %v", err)
	}
	gotRef := detailOK.Content["application/json"].Schema.Properties["delivery"].Ref
	if gotRef != "#/components/schemas/WebhookDeliveryDetail" {
		t.Fatalf("detail-delivery 200 delivery $ref = %q, want WebhookDeliveryDetail", gotRef)
	}
}

// TestOpenAPIDocShapesSearchPages (C66T0): pins the GET /api/v1/search
// doc against the Go envelope. The issue contract (results/next_cursor)
// is unchanged; page_results/page_next_cursor are added with their own
// cursor. Every doc property name must match the Go JSON tags exactly.
func TestOpenAPIDocShapesSearchPages(t *testing.T) {
	var spec struct {
		Paths map[string]struct {
			Get struct {
				Responses map[string]struct {
					Content map[string]struct {
						Schema struct {
							Properties map[string]struct {
								Type   string `json:"type"`
								Format string `json:"format"`
								Items  struct {
									Ref string `json:"$ref"`
								} `json:"items"`
							} `json:"properties"`
						} `json:"schema"`
					} `json:"content"`
				} `json:"responses"`
			} `json:"get"`
		} `json:"paths"`
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(docs.OpenAPI, &spec); err != nil {
		t.Fatalf("embedded openapi spec is not valid JSON: %v", err)
	}
	path, ok := spec.Paths["/api/v1/search"]
	if !ok {
		t.Fatal("spec has no /api/v1/search path")
	}
	props := path.Get.Responses["200"].Content["application/json"].Schema.Properties

	// Doc properties must match the Go envelope's JSON tags exactly —
	// results/next_cursor byte-for-byte, plus the page fields.
	want := map[string]bool{}
	for i := 0; i < reflect.TypeOf(searchResponse{}).NumField(); i++ {
		tag := reflect.TypeOf(searchResponse{}).Field(i).Tag.Get("json")
		want[strings.Split(tag, ",")[0]] = true
	}
	if len(props) != len(want) {
		t.Fatalf("search 200 properties = %v, Go envelope has %v", propNames(props), want)
	}
	for name := range want {
		if _, ok := props[name]; !ok {
			t.Fatalf("search 200 missing property %q (doc: %v, Go: %v)", name, propNames(props), want)
		}
	}
	if props["page_results"].Type != "array" {
		t.Fatalf("page_results type = %q, want array", props["page_results"].Type)
	}
	if got := props["page_results"].Items.Ref; got != "#/components/schemas/PageSearchResult" {
		t.Fatalf("page_results items $ref = %q, want PageSearchResult", got)
	}
	if props["page_next_cursor"].Type != "string" {
		t.Fatalf("page_next_cursor type = %q, want string", props["page_next_cursor"].Type)
	}

	// The PageSearchResult schema matches service.SearchPageItem's
	// exported JSON tags (rank is unexported and must stay undocumented).
	raw, ok := spec.Components.Schemas["PageSearchResult"]
	if !ok {
		t.Fatal("spec has no PageSearchResult schema")
	}
	var itemDoc struct {
		Type  string                     `json:"type"`
		Props map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &itemDoc); err != nil {
		t.Fatalf("decode PageSearchResult schema: %v", err)
	}
	wantItem := map[string]bool{}
	for i := 0; i < reflect.TypeOf(service.SearchPageItem{}).NumField(); i++ {
		f := reflect.TypeOf(service.SearchPageItem{}).Field(i)
		if f.PkgPath != "" { // unexported (rank) — must not appear
			continue
		}
		wantItem[strings.Split(f.Tag.Get("json"), ",")[0]] = true
	}
	if len(itemDoc.Props) != len(wantItem) {
		t.Fatalf("PageSearchResult properties = %v, Go item has %v", propNamesRaw(itemDoc.Props), wantItem)
	}
	for name := range wantItem {
		if _, ok := itemDoc.Props[name]; !ok {
			t.Fatalf("PageSearchResult missing property %q", name)
		}
	}
	if _, ok := itemDoc.Props["rank"]; ok {
		t.Fatal("PageSearchResult documents rank, which the Go struct keeps unexported")
	}
	var idProp struct {
		Type   string `json:"type"`
		Format string `json:"format"`
	}
	if err := json.Unmarshal(itemDoc.Props["id"], &idProp); err != nil {
		t.Fatalf("decode PageSearchResult.id: %v", err)
	}
	if idProp.Type != "string" || idProp.Format != "uuid" {
		t.Fatalf("PageSearchResult.id = {%s, %s}, want {string, uuid}", idProp.Type, idProp.Format)
	}
}

func propNames(props map[string]struct {
	Type   string `json:"type"`
	Format string `json:"format"`
	Items  struct {
		Ref string `json:"$ref"`
	} `json:"items"`
}) []string {
	names := make([]string, 0, len(props))
	for n := range props {
		names = append(names, n)
	}
	return names
}

func propNamesRaw(props map[string]json.RawMessage) []string {
	names := make([]string, 0, len(props))
	for n := range props {
		names = append(names, n)
	}
	return names
}

// Response-shape pinning test (C70T0): time-in-state exposure.
// Pins the live contract: state_entered_at (RFC3339) +
// time_in_state_seconds (int64, >= 0) on issue detail + issue list,
// per-transition durations on the history feed's state_id entries
// (absent on every other entry), the created_at UPPER BOUND fallback
// when no state_id activity row exists, and the doc-side schema pins
// on Issue + ActivityEntry.
func TestOpenAPIResponseShapesTimeInState(t *testing.T) {
	pool := newTestPool(t)
	e := testSatelliteServer(t, pool)
	h := &IssueHandler{Pool: pool}
	RegisterGitHubLinkRoutes(e, h)
	RegisterReleaseRoutes(e, h)
	RegisterCustomFieldRoutes(e, h)

	cookie := loginTestUser(t, e, pool, uniqueEmail("openapi-tis"), "test-agent", uniqueIP())
	slug := uniqueSlug("openapi-tis")
	createWorkspaceHTTP(t, e, cookie, "TIS Co", slug)
	ident := uniqueProjectIdentifier("TIS")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	projBase := "/api/v1/workspaces/" + slug + "/projects/" + ident
	base := projBase + "/issues"

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
	mustHave := func(t *testing.T, m map[string]json.RawMessage, keys ...string) {
		t.Helper()
		for _, k := range keys {
			if _, ok := m[k]; !ok {
				t.Fatalf("response missing key %q (keys: %v)", k, rawKeysOf(m))
			}
		}
	}
	mustNotHave := func(t *testing.T, m map[string]json.RawMessage, keys ...string) {
		t.Helper()
		for _, k := range keys {
			if _, ok := m[k]; ok {
				t.Fatalf("response has forbidden key %q (body keys: %v)", k, rawKeysOf(m))
			}
		}
	}
	rfc3339 := func(t *testing.T, m map[string]json.RawMessage, key string) time.Time {
		t.Helper()
		var s string
		if err := json.Unmarshal(m[key], &s); err != nil || s == "" {
			t.Fatalf("key %q = %s, want an RFC3339 string", key, string(m[key]))
		}
		ts, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatalf("key %q = %q, not RFC3339: %v", key, s, err)
		}
		return ts
	}
	nonNegInt := func(t *testing.T, m map[string]json.RawMessage, key string) int64 {
		t.Helper()
		var n int64
		if err := json.Unmarshal(m[key], &n); err != nil {
			t.Fatalf("key %q = %s, want an int64", key, string(m[key]))
		}
		if n < 0 {
			t.Fatalf("key %q = %d, want >= 0", key, n)
		}
		return n
	}

	// Create: no state_id activity row yet (created directly in the
	// backlog state) → fallback case on both detail and list.
	rec := postAuthedJSON(t, e, http.MethodPost, base, cookie, `{"name":"TIS issue"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		ID        string `json:"id"`
		CreatedAt string `json:"created_at"`
	}
	decode(rec, &created)

	// 1. Detail: fallback — state_entered_at == created_at (UPPER
	// BOUND), time_in_state_seconds >= 0. The bare create response
	// itself must NOT carry the derived fields (detail/list only).
	mustNotHave(t, topKeys(rec), "state_entered_at", "time_in_state_seconds")
	rec = getAuthed(t, e, http.MethodGet, base+"/"+created.ID, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("get detail: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	detail := topKeys(rec)
	mustHave(t, detail, "state_entered_at", "time_in_state_seconds")
	entered := rfc3339(t, detail, "state_entered_at")
	createdAt := rfc3339(t, detail, "created_at")
	if !entered.Equal(createdAt) {
		t.Fatalf("state_entered_at = %v, want created_at %v (no transitions yet)", entered, createdAt)
	}
	secs := nonNegInt(t, detail, "time_in_state_seconds")
	if secs > int64(time.Since(createdAt).Seconds())+5 {
		t.Fatalf("time_in_state_seconds = %d, exceeds now-created_at", secs)
	}

	// 2. List rows carry the same fields (fallback case too).
	rec = getAuthed(t, e, http.MethodGet, base, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var list struct {
		Results []map[string]json.RawMessage `json:"results"`
	}
	decode(rec, &list)
	if len(list.Results) != 1 {
		t.Fatalf("list results = %d, want 1", len(list.Results))
	}
	mustHave(t, list.Results[0], "state_entered_at", "time_in_state_seconds")
	nonNegInt(t, list.Results[0], "time_in_state_seconds")
	rfc3339(t, list.Results[0], "state_entered_at")

	// 3. Transition the issue to another state → a state_id activity
	// row exists; state_entered_at must move OFF created_at (to the
	// transition time, which is >= created_at).
	rec = getAuthed(t, e, http.MethodGet, projBase+"/states", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list states: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var stateList struct {
		States []struct {
			ID    string `json:"id"`
			Group string `json:"group"`
		} `json:"states"`
	}
	decode(rec, &stateList)
	var startedID string
	for _, s := range stateList.States {
		if s.Group == "started" {
			startedID = s.ID
			break
		}
	}
	if startedID == "" {
		t.Fatal("no started-group state in the seeded defaults")
	}
	rec = postAuthedJSON(t, e, http.MethodPatch, base+"/"+created.ID, cookie,
		`{"state_id":"`+startedID+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch state: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	// The bare update response must NOT carry the derived fields.
	mustNotHave(t, topKeys(rec), "state_entered_at", "time_in_state_seconds")
	rec = getAuthed(t, e, http.MethodGet, base+"/"+created.ID, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("get detail after transition: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	detail = topKeys(rec)
	mustHave(t, detail, "state_entered_at", "time_in_state_seconds")
	entered = rfc3339(t, detail, "state_entered_at")
	createdAt = rfc3339(t, detail, "created_at")
	if entered.Before(createdAt) {
		t.Fatalf("state_entered_at = %v, before created_at %v", entered, createdAt)
	}
	afterSecs := nonNegInt(t, detail, "time_in_state_seconds")
	if afterSecs > secs {
		t.Fatalf("time_in_state_seconds grew %d → %d across a transition", secs, afterSecs)
	}

	// 4. History feed: the state_id entry carries
	// time_in_state_seconds; every other entry must not.
	rec = getAuthed(t, e, http.MethodGet, base+"/"+created.ID+"/history", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("get history: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var hist struct {
		History []map[string]json.RawMessage `json:"history"`
	}
	decode(rec, &hist)
	if len(hist.History) == 0 {
		t.Fatal("history is empty, want the state transition row")
	}
	seenState := false
	for _, en := range hist.History {
		var field string
		if err := json.Unmarshal(en["field"], &field); err != nil {
			t.Fatalf("decode field: %v", err)
		}
		if field == "state_id" {
			seenState = true
			d := nonNegInt(t, en, "time_in_state_seconds")
			// Latest transition's duration IS the current
			// time_in_state_seconds (same formula, same read).
			if d > afterSecs+5 {
				t.Fatalf("transition time_in_state_seconds = %d, exceeds detail value %d", d, afterSecs)
			}
		} else {
			mustNotHave(t, en, "time_in_state_seconds")
		}
	}
	if !seenState {
		t.Fatal("history has no state_id entry, want the transition row")
	}

	// 5. Doc-side pins: Issue schema carries both new fields with the
	// right types; ActivityEntry carries the per-transition duration.
	var spec struct {
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(docs.OpenAPI, &spec); err != nil {
		t.Fatalf("embedded openapi spec is not valid JSON: %v", err)
	}
	docProps := func(name string) map[string]json.RawMessage {
		t.Helper()
		raw, ok := spec.Components.Schemas[name]
		if !ok {
			t.Fatalf("spec has no %s schema", name)
		}
		var s struct {
			Props map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(raw, &s); err != nil {
			t.Fatalf("decode %s schema: %v", name, err)
		}
		return s.Props
	}
	propType := func(props map[string]json.RawMessage, name, wantType, wantFormat string) {
		t.Helper()
		raw, ok := props[name]
		if !ok {
			t.Fatalf("schema properties lack %q", name)
		}
		var p struct {
			Type   string `json:"type"`
			Format string `json:"format"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Fatalf("decode prop %s: %v", name, err)
		}
		if p.Type != wantType || p.Format != wantFormat {
			t.Fatalf("prop %s = {%s %s}, want {%s %s}", name, p.Type, p.Format, wantType, wantFormat)
		}
	}
	issueProps := docProps("Issue")
	propType(issueProps, "state_entered_at", "string", "date-time")
	propType(issueProps, "time_in_state_seconds", "integer", "int64")
	activityProps := docProps("ActivityEntry")
	propType(activityProps, "time_in_state_seconds", "integer", "int64")
}

// Response-shape pinning test (C71T0): for the epic endpoints, assert the
// LIVE response envelope keys match the documented Epic / EpicProgress /
// EpicRef / InitiativeEpic schemas in docs/openapi.yaml, and that the
// documented schemas carry the promised keys. Same helper style as the
// four-endpoints pin test: decode + top-level key assertions.

func TestOpenAPIEpicShapes(t *testing.T) {
	pool := newTestPool(t)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterEpicRoutes(e, &IssueHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail("openapi-epics"), "test-agent", uniqueIP())
	slug := uniqueSlug("openapi-epics")
	createWorkspaceHTTP(t, e, cookie, "Epic Shapes Co", slug)
	ident := uniqueProjectIdentifier("OE")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	epicBase := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/epics"
	issueBase := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/issues"

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

	// 1. POST create → 201 bare Epic: pins the documented Epic schema
	// keys and NOT the module shape (no status/lead_id/start_date).
	rec := postAuthedJSON(t, e, http.MethodPost, epicBase, cookie,
		`{"name":"Shapes","description":"d"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create epic: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	keys := topKeys(rec)
	for _, k := range []string{"id", "project_id", "name", "created_at", "updated_at"} {
		if _, ok := keys[k]; !ok {
			t.Fatalf("create epic body lacks key %q (body: %s)", k, rec.Body.String())
		}
	}
	for _, k := range []string{"status", "lead_id", "start_date", "target_date"} {
		if _, ok := keys[k]; ok {
			t.Fatalf("create epic body has module-only key %q (body: %s)", k, rec.Body.String())
		}
	}
	var created struct {
		ID string `json:"id"`
	}
	decode(rec, &created)
	epicURL := epicBase + "/" + created.ID

	// 2. GET detail → 200 bare Epic with progress {total, by_group}.
	iss := createIssueHTTP(t, e, cookie, issueBase, "shaped")
	rec = postAuthedJSON(t, e, http.MethodPost, epicURL+"/issues", cookie,
		`{"issue_ids":["`+iss+`"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("add issue: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, epicURL, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("get epic: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var detail struct {
		Progress struct {
			Total   int64            `json:"total"`
			ByGroup map[string]int64 `json:"by_group"`
		} `json:"progress"`
	}
	decode(rec, &detail)
	if detail.Progress.Total != 1 {
		t.Fatalf("progress.total = %d, want 1", detail.Progress.Total)
	}
	if _, ok := detail.Progress.ByGroup["backlog"]; !ok {
		t.Fatalf("progress.by_group = %v, want a backlog key", detail.Progress.ByGroup)
	}

	// 3. POST archive → 200 bare Epic with archived_at.
	rec = postAuthedJSON(t, e, http.MethodPost, epicURL+"/archive", cookie, `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("archive: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if _, ok := topKeys(rec)["archived_at"]; !ok {
		t.Fatalf("archive body lacks archived_at (body: %s)", rec.Body.String())
	}

	// 4. Issue detail → epics: [{id, name}] (archived stays visible).
	rec = getAuthed(t, e, http.MethodGet, issueBase+"/"+iss, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("get issue: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var issueDetail struct {
		Epics []struct {
			ID         string  `json:"id"`
			Name       string  `json:"name"`
			ArchivedAt *string `json:"archived_at"`
		} `json:"epics"`
	}
	decode(rec, &issueDetail)
	if len(issueDetail.Epics) != 1 || issueDetail.Epics[0].ID != created.ID ||
		issueDetail.Epics[0].Name != "Shapes" {
		t.Fatalf("issue epics = %+v", issueDetail)
	}
	if issueDetail.Epics[0].ArchivedAt == nil {
		t.Fatalf("archived epic ref must carry archived_at: %+v", issueDetail.Epics[0])
	}

	// 5. The documented schemas carry the promised keys.
	var spec struct {
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(docs.OpenAPI, &spec); err != nil {
		t.Fatalf("embedded openapi spec is not valid JSON: %v", err)
	}
	docProps := func(name string) map[string]json.RawMessage {
		t.Helper()
		raw, ok := spec.Components.Schemas[name]
		if !ok {
			t.Fatalf("spec has no %s schema", name)
		}
		var s struct {
			Props map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(raw, &s); err != nil {
			t.Fatalf("decode %s schema: %v", name, err)
		}
		return s.Props
	}
	for _, k := range []string{"id", "project_id", "name", "archived_at", "issue_count", "progress", "created_at"} {
		if _, ok := docProps("Epic")[k]; !ok {
			t.Fatalf("Epic schema lacks %q", k)
		}
	}
	for _, k := range []string{"total", "by_group"} {
		if _, ok := docProps("EpicProgress")[k]; !ok {
			t.Fatalf("EpicProgress schema lacks %q", k)
		}
	}
	for _, k := range []string{"id", "name", "archived_at"} {
		if _, ok := docProps("EpicRef")[k]; !ok {
			t.Fatalf("EpicRef schema lacks %q", k)
		}
	}
	for _, k := range []string{"epic_id", "epic_name", "project_id", "project_name", "added_at"} {
		if _, ok := docProps("InitiativeEpic")[k]; !ok {
			t.Fatalf("InitiativeEpic schema lacks %q", k)
		}
	}
	if _, ok := docProps("Issue")["epics"]; !ok {
		t.Fatalf("Issue schema lacks epics")
	}
}

// Doc-side pinning test (C77T0): the AutomationAction schema documents
// the send_webhook action — the type enum entry plus the url / method /
// headers / payload_template properties with their validation shapes.
// This is the doc-side complement to the live contract pins in
// TestAutomationSendWebhookHTTP (201 redaction, 400 validation matrix).
func TestOpenAPIDocAutomationSendWebhookAction(t *testing.T) {
	var spec struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]struct {
					Enum        []string `json:"enum"`
					Type        string   `json:"type"`
					Format      string   `json:"format"`
					Default     any      `json:"default"`
					Description string   `json:"description"`
				} `json:"properties"`
				Description string `json:"description"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(docs.OpenAPI, &spec); err != nil {
		t.Fatalf("embedded openapi spec is not valid JSON: %v", err)
	}
	action, ok := spec.Components.Schemas["AutomationAction"]
	if !ok {
		t.Fatal("spec has no AutomationAction schema")
	}
	found := false
	for _, e := range action.Properties["type"].Enum {
		if e == "send_webhook" {
			found = true
		}
	}
	if !found {
		t.Fatalf("AutomationAction type enum lacks send_webhook: %v",
			action.Properties["type"].Enum)
	}
	urlProp, ok := action.Properties["url"]
	if !ok || urlProp.Format != "uri" {
		t.Fatalf("AutomationAction url property = %+v, want format uri", urlProp)
	}
	methodProp, ok := action.Properties["method"]
	if !ok || len(methodProp.Enum) != 2 ||
		methodProp.Enum[0] != "POST" || methodProp.Enum[1] != "PUT" {
		t.Fatalf("AutomationAction method property = %+v, want enum [POST PUT]", methodProp)
	}
	headersProp, ok := action.Properties["headers"]
	if !ok || headersProp.Type != "object" {
		t.Fatalf("AutomationAction headers property = %+v, want type object", headersProp)
	}
	if !strings.Contains(headersProp.Description, "********") {
		t.Fatalf("AutomationAction headers description does not document redaction: %q",
			headersProp.Description)
	}
	tmplProp, ok := action.Properties["payload_template"]
	if !ok || tmplProp.Type != "string" {
		t.Fatalf("AutomationAction payload_template property = %+v, want type string", tmplProp)
	}
	if !strings.Contains(tmplProp.Description, "{{variable}}") ||
		!strings.Contains(tmplProp.Description, "issue.identifier") {
		t.Fatalf("AutomationAction payload_template description does not document the vocabulary: %q",
			tmplProp.Description)
	}
	if !strings.Contains(action.Description, "send_webhook") {
		t.Fatal("AutomationAction description does not mention send_webhook")
	}
}

// TestOpenAPIDocShapesDashboard pins the C81T0 dashboards API contract
// in docs/openapi.yaml: every new path + method, the path params, the
// 400/403/404 response semantics, and the schema enums (visibility,
// widget_type, group_by). If the implementation drifts from the pinned
// contract, this test fails first.
func TestOpenAPIDocShapesDashboard(t *testing.T) {
	var spec struct {
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(docs.OpenAPI, &spec); err != nil {
		t.Fatalf("embedded openapi spec is not valid JSON: %v", err)
	}

	// Every new route is documented with the expected methods.
	wantOps := map[string][]string{
		"/api/v1/workspaces/{slug}/dashboards":              {"get", "post"},
		"/api/v1/workspaces/{slug}/dashboards/{id}":         {"get", "patch", "delete"},
		"/api/v1/workspaces/{slug}/dashboards/{id}/widgets": {"get", "post"},
		"/api/v1/widgets/{widget_id}":                       {"patch", "delete"},
		"/api/v1/widgets/{widget_id}/layout":                {"patch"},
		"/api/v1/widgets/{widget_id}/data":                  {"get"},
	}
	for path, methods := range wantOps {
		ops, ok := spec.Paths[path]
		if !ok {
			t.Fatalf("spec missing dashboard path %s", path)
		}
		for _, m := range methods {
			raw, ok := ops[m]
			if !ok {
				t.Fatalf("spec path %s missing %s operation", path, m)
			}
			var op struct {
				Summary    string `json:"summary"`
				Parameters []struct {
					Name string `json:"name"`
					In   string `json:"in"`
				} `json:"parameters"`
				Responses map[string]json.RawMessage `json:"responses"`
			}
			if err := json.Unmarshal(raw, &op); err != nil {
				t.Fatalf("decode %s %s: %v", m, path, err)
			}
			if op.Summary == "" {
				t.Fatalf("%s %s: missing summary", m, path)
			}
			// 400/403/404 are pinned on every dashboard operation:
			// 400 = malformed UUID / bad config, 403 = guest or
			// edit-rule denial, 404 = bad slug / private-dashboard
			// invisibility. DELETE answers 204 instead of 200.
			// (GET list has no 400 path: nothing on it validates.)
			need400 := !(m == "get" && path == "/api/v1/workspaces/{slug}/dashboards")
			for _, code := range []string{"403", "404"} {
				if _, ok := op.Responses[code]; !ok {
					t.Fatalf("%s %s: missing %s response", m, path, code)
				}
			}
			if need400 {
				if _, ok := op.Responses["400"]; !ok {
					t.Fatalf("%s %s: missing 400 response", m, path)
				}
			}
			if m == "delete" {
				if _, ok := op.Responses["204"]; !ok {
					t.Fatalf("%s %s: missing 204 response", m, path)
				}
			} else if _, ok := op.Responses["200"]; !ok {
				if _, ok := op.Responses["201"]; !ok {
					t.Fatalf("%s %s: missing 200/201 response", m, path)
				}
			}
		}
	}

	// The widget item routes carry widget_id (not id) as the path param.
	var widgetParamNames []string
	raw := spec.Paths["/api/v1/widgets/{widget_id}"]["patch"]
	var widgetOp struct {
		Parameters []struct {
			Name string `json:"name"`
			In   string `json:"in"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(raw, &widgetOp); err != nil {
		t.Fatalf("decode widget patch: %v", err)
	}
	for _, p := range widgetOp.Parameters {
		if p.In == "path" {
			widgetParamNames = append(widgetParamNames, p.Name)
		}
	}
	if len(widgetParamNames) != 1 || widgetParamNames[0] != "widget_id" {
		t.Fatalf("widget path params = %v, want [widget_id]", widgetParamNames)
	}

	// Schema enums pin the write-time validation contract.
	var dashboard struct {
		Props map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	decode := func(name string, v any) {
		t.Helper()
		raw, ok := spec.Components.Schemas[name]
		if !ok {
			t.Fatalf("spec has no %s schema", name)
		}
		if err := json.Unmarshal(raw, v); err != nil {
			t.Fatalf("decode %s schema: %v", name, err)
		}
	}
	decode("Dashboard", &dashboard)
	if vis := dashboard.Props["visibility"].Enum; len(vis) != 2 || vis[0] != "public" || vis[1] != "private" {
		t.Fatalf("Dashboard.visibility enum = %v, want [public private]", vis)
	}

	var widget struct {
		Props map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	decode("DashboardWidget", &widget)
	wantTypes := []string{"bar", "line", "area", "donut", "pie", "number"}
	if got := widget.Props["widget_type"].Enum; !reflect.DeepEqual(got, wantTypes) {
		t.Fatalf("DashboardWidget.widget_type enum = %v, want %v", got, wantTypes)
	}

	var config struct {
		Props map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	decode("WidgetConfig", &config)
	wantGroupBy := []string{"state", "priority", "assignee", "label"}
	if got := config.Props["group_by"].Enum; !reflect.DeepEqual(got, wantGroupBy) {
		t.Fatalf("WidgetConfig.group_by enum = %v, want %v", got, wantGroupBy)
	}
	if got := config.Props["metric"].Enum; !reflect.DeepEqual(got, []string{"count"}) {
		t.Fatalf("WidgetConfig.metric enum = %v, want [count]", got)
	}

	// WidgetData carries groups (chart) and value (number).
	var data struct {
		Props map[string]json.RawMessage `json:"properties"`
	}
	decode("WidgetData", &data)
	if _, ok := data.Props["groups"]; !ok {
		t.Fatal("WidgetData missing groups property")
	}
	if _, ok := data.Props["value"]; !ok {
		t.Fatal("WidgetData missing value property")
	}
}
