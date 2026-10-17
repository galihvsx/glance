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

// Response-shape pinning test (C54T2): for the swept issue-adjacent
// schemas (User / Label / State / Comment / Attachment / ReactionGroup /
// IssueVotes / SubtaskProgress / Page / PageRevision / ShareLink /
// IssueLink) plus the carried issue-list envelope fix, assert the LIVE
// response keys match the corrected docs/openapi.yaml. Pins the C54T2
// drift fixes: User has no role field (is_admin/is_active/avatar_url/
// last_login_at/updated_at instead, name nullable); Label/State carry
// their scope + timestamp fields (Label color never null, State group
// admits triage); Comment carries actor (ActorRef, not author/User),
// issue_id and list-only replies; Attachment carries issue_id +
// uploaded_by; GET reactions returns ReactionGroup[] (the old Reaction
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
	// author / replies keys; resolved_at + resolved_by present-but-
	// null while open. List nests the reply under replies[]. Resolve
	// fills resolved_at + resolved_by (ActorRef, not User).
	alpha := createIssue(`{"name":"Alpha"}`)
	commentBase := base + "/" + alpha + "/comments"
	rec = postAuthedJSON(t, e, http.MethodPost, commentBase, cookie,
		`{"content":{"type":"doc","content":[]}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create comment: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	comment := topKeys(rec)
	exactKeys(t, comment, "id", "issue_id", "content", "actor",
		"created_at", "updated_at", "resolved_at", "resolved_by")
	mustNotHave(t, comment, "author", "replies")
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
	// {links: [...]} envelope — the doc's {shares} envelope is
	// recorded out-of-scope drift for a future cycle, so the pin
	// asserts the live key, not the stale doc key.
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
