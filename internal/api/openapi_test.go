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
