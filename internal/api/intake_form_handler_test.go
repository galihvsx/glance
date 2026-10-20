package api

// Intake form HTTP endpoint tests (C82T0): management CRUD + authZ under
// the project intake path, the public no-auth surface, write-time field
// validation, token regeneration, and public submission creating a pending
// intake issue through the same service path as in-app creation. Real test
// database, no skips.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"glance/docs"
)

func testIntakeFormServer(t *testing.T, pool *pgxpool.Pool) *echo.Echo {
	t.Helper()
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterIntakeRoutes(e, &IssueHandler{Pool: pool})
	RegisterCustomFieldRoutes(e, &IssueHandler{Pool: pool})
	RegisterIntakeFormPublicRoutes(e, &IssueHandler{Pool: pool})
	return e
}

// setupIntakeFormProject creates a workspace + project and returns the
// forms base, the custom-fields base, and the identifiers.
func setupIntakeFormProject(t *testing.T, e *echo.Echo, cookie *http.Cookie, prefix string) (formsBase, cfBase string) {
	t.Helper()
	slug := uniqueSlug(prefix)
	createWorkspaceHTTP(t, e, cookie, prefix+" Co", slug)
	ident := uniqueProjectIdentifier(strings.ToUpper(prefix[:2]))
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	projBase := "/api/v1/workspaces/" + slug + "/projects/" + ident
	return projBase + "/intake/forms", projBase + "/custom-fields"
}

func postPublicJSON(t *testing.T, e *echo.Echo, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

type intakeFormCreated struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	IsEnabled  bool   `json:"is_enabled"`
	ShareToken string `json:"share_token"`
	Fields     []struct {
		Key      string `json:"key"`
		Label    string `json:"label"`
		Required bool   `json:"required"`
	} `json:"fields"`
}

// createIntakeFormHTTP posts a form body and expects 201, returning the
// decoded form.
func createIntakeFormHTTP(t *testing.T, e *echo.Echo, cookie *http.Cookie, formsBase, body string) intakeFormCreated {
	t.Helper()
	rec := postAuthedJSON(t, e, http.MethodPost, formsBase, cookie, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create form: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var created intakeFormCreated
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode form: %v", err)
	}
	if created.ID == "" || created.ShareToken == "" {
		t.Fatalf("created form missing id/token: %s", rec.Body.String())
	}
	return created
}

const titleOnlyFields = `[{"key":"title","label":"Title","required":true}]`

func TestIntakeFormCreateRequiresTitle(t *testing.T) {
	pool := newTestPool(t)
	e := testIntakeFormServer(t, pool)
	cookie := loginTestUser(t, e, pool, uniqueEmail("if-title"), "test-agent", uniqueIP())
	formsBase, _ := setupIntakeFormProject(t, e, cookie, "if-title")

	// No title field at all → 400.
	rec := postAuthedJSON(t, e, http.MethodPost, formsBase, cookie,
		`{"name":"Bug report","fields":[{"key":"description","label":"Details","required":false}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing title: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// Title present but not required → 400 (title is always required).
	rec = postAuthedJSON(t, e, http.MethodPost, formsBase, cookie,
		`{"name":"Bug report","fields":[{"key":"title","label":"Title","required":false}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("optional title: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "title") {
		t.Fatalf("optional title: body %s does not name the title field", rec.Body.String())
	}
}

func TestIntakeFormCreateRejectsUnknownKey(t *testing.T) {
	pool := newTestPool(t)
	e := testIntakeFormServer(t, pool)
	cookie := loginTestUser(t, e, pool, uniqueEmail("if-unknown"), "test-agent", uniqueIP())
	formsBase, _ := setupIntakeFormProject(t, e, cookie, "if-unknown")

	rec := postAuthedJSON(t, e, http.MethodPost, formsBase, cookie,
		`{"name":"Bug report","fields":[{"key":"title","label":"Title","required":true},{"key":"bogus","label":"Bogus","required":false}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown key: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "bogus") {
		t.Fatalf("unknown key: body %s does not name the key", rec.Body.String())
	}
}

func TestIntakeFormCreateRejectsDuplicateKeys(t *testing.T) {
	pool := newTestPool(t)
	e := testIntakeFormServer(t, pool)
	cookie := loginTestUser(t, e, pool, uniqueEmail("if-dup"), "test-agent", uniqueIP())
	formsBase, _ := setupIntakeFormProject(t, e, cookie, "if-dup")

	rec := postAuthedJSON(t, e, http.MethodPost, formsBase, cookie,
		`{"name":"Bug report","fields":[{"key":"title","label":"Title","required":true},{"key":"title","label":"Title again","required":true}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("duplicate keys: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestIntakeFormCreateRejectsForeignCustomField(t *testing.T) {
	pool := newTestPool(t)
	e := testIntakeFormServer(t, pool)
	cookie := loginTestUser(t, e, pool, uniqueEmail("if-foreign"), "test-agent", uniqueIP())
	formsBaseA, cfBaseA := setupIntakeFormProject(t, e, cookie, "if-foreign-a")
	_, cfBaseB := setupIntakeFormProject(t, e, cookie, "if-foreign-b")

	// A select custom field on project B…
	rec := postAuthedJSON(t, e, http.MethodPost, cfBaseB, cookie,
		`{"name":"Severity","field_type":"select","options":[{"value":"low"},{"value":"high"}]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create field on B: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var field struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &field); err != nil {
		t.Fatalf("decode field: %v", err)
	}
	_ = cfBaseA

	// …referenced from a form on project A → 400.
	rec = postAuthedJSON(t, e, http.MethodPost, formsBaseA, cookie,
		fmt.Sprintf(`{"name":"Bug report","fields":[{"key":"title","label":"Title","required":true},{"key":"custom:%s","label":"Severity","required":false}]}`, field.ID))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("foreign custom field: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestIntakeFormCreateDefaultsLabels(t *testing.T) {
	pool := newTestPool(t)
	e := testIntakeFormServer(t, pool)
	cookie := loginTestUser(t, e, pool, uniqueEmail("if-labels"), "test-agent", uniqueIP())
	formsBase, cfBase := setupIntakeFormProject(t, e, cookie, "if-labels")

	rec := postAuthedJSON(t, e, http.MethodPost, cfBase, cookie,
		`{"name":"Repro steps","field_type":"text"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create field: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var field struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &field); err != nil {
		t.Fatalf("decode field: %v", err)
	}

	// Blank labels default to the canonical names.
	created := createIntakeFormHTTP(t, e, cookie, formsBase,
		fmt.Sprintf(`{"name":"Bug report","fields":[{"key":"title","label":"","required":true},{"key":"custom:%s","label":"","required":false}]}`, field.ID))
	if created.Fields[0].Label != "Title" {
		t.Errorf("title label = %q, want %q", created.Fields[0].Label, "Title")
	}
	if created.Fields[1].Label != "Repro steps" {
		t.Errorf("custom label = %q, want %q", created.Fields[1].Label, "Repro steps")
	}
	if !created.IsEnabled {
		t.Errorf("is_enabled = false, want default true")
	}
	if len(created.ShareToken) < 43 {
		t.Errorf("share_token len = %d, want >= 43 (32 random bytes base64url)", len(created.ShareToken))
	}
}

// addProjectMember adds a user to the workspace with the given role and
// returns their session cookie.
func addProjectMember(t *testing.T, e *echo.Echo, pool *pgxpool.Pool, slug string, adminCookie *http.Cookie, email string, role int) *http.Cookie {
	t.Helper()
	memberCookie := loginTestUser(t, e, pool, email, "test-agent", uniqueIP())
	memberID := authedUserID(t, e, memberCookie)
	rec := postAuthedJSON(t, e, http.MethodPost, "/api/v1/workspaces/"+slug+"/members", adminCookie,
		fmt.Sprintf(`{"user_id":%q,"role":%d}`, memberID, role))
	if rec.Code != http.StatusOK {
		t.Fatalf("add member: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	return memberCookie
}

func TestIntakeFormManageAuthZ(t *testing.T) {
	pool := newTestPool(t)
	e := testIntakeFormServer(t, pool)
	cookie := loginTestUser(t, e, pool, uniqueEmail("if-authz"), "test-agent", uniqueIP())
	slug := uniqueSlug("if-authz")
	createWorkspaceHTTP(t, e, cookie, "if-authz Co", slug)
	ident := uniqueProjectIdentifier("IF")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	formsBase := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/intake/forms"

	// A plain member (15) — not an admin — gets 403 on every manage route.
	memberCookie := addProjectMember(t, e, pool, slug, cookie, uniqueEmail("if-authz-member"), 15)
	for _, tc := range []struct {
		method, path, body string
	}{
		{http.MethodGet, formsBase, ""},
		{http.MethodPost, formsBase, `{"name":"x","fields":` + titleOnlyFields + `}`},
		{http.MethodGet, formsBase + "/00000000-0000-0000-0000-000000000000", ""},
		{http.MethodPatch, formsBase + "/00000000-0000-0000-0000-000000000000", `{"name":"y"}`},
		{http.MethodDelete, formsBase + "/00000000-0000-0000-0000-000000000000", ""},
		{http.MethodPost, formsBase + "/00000000-0000-0000-0000-000000000000/regenerate", ""},
	} {
		rec := postAuthedJSON(t, e, tc.method, tc.path, memberCookie, tc.body)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s: status = %d, want 403 (body: %s)", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}

	// A non-member gets 404 (the tenancy boundary), not 403.
	strangerCookie := loginTestUser(t, e, pool, uniqueEmail("if-authz-stranger"), "test-agent", uniqueIP())
	rec := postAuthedJSON(t, e, http.MethodGet, formsBase, strangerCookie, "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("non-member GET: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestIntakeFormManageMalformedID(t *testing.T) {
	pool := newTestPool(t)
	e := testIntakeFormServer(t, pool)
	cookie := loginTestUser(t, e, pool, uniqueEmail("if-malid"), "test-agent", uniqueIP())
	formsBase, _ := setupIntakeFormProject(t, e, cookie, "if-malid")

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, formsBase + "/not-a-uuid"},
		{http.MethodPost, formsBase + "/not-a-uuid/regenerate"},
	} {
		rec := postAuthedJSON(t, e, tc.method, tc.path, cookie, "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s %s: status = %d, want 400 (body: %s)", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
}

// createFullForm builds a form with every field kind: title (required),
// description, priority, a required select custom field and an optional
// number custom field. It returns the created form plus the custom field ids.
func createFullForm(t *testing.T, e *echo.Echo, cookie *http.Cookie, formsBase, cfBase string) (intakeFormCreated, string, string) {
	t.Helper()
	rec := postAuthedJSON(t, e, http.MethodPost, cfBase, cookie,
		`{"name":"Severity","field_type":"select","required":true,"options":[{"value":"low"},{"value":"high"}]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create select field: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var sel struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &sel); err != nil {
		t.Fatalf("decode select field: %v", err)
	}
	rec = postAuthedJSON(t, e, http.MethodPost, cfBase, cookie,
		`{"name":"Repro count","field_type":"number"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create number field: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var num struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &num); err != nil {
		t.Fatalf("decode number field: %v", err)
	}
	created := createIntakeFormHTTP(t, e, cookie, formsBase, fmt.Sprintf(
		`{"name":"Bug report","description":"Tell us what broke.","fields":[`+
			`{"key":"title","label":"Summary","required":true},`+
			`{"key":"description","label":"Details","required":false},`+
			`{"key":"priority","label":"Urgency","required":false},`+
			`{"key":"custom:%s","label":"How bad?","required":true},`+
			`{"key":"custom:%s","label":"Times seen","required":false}]}`,
		sel.ID, num.ID))
	return created, sel.ID, num.ID
}

func TestIntakeFormPublicGetShape(t *testing.T) {
	pool := newTestPool(t)
	e := testIntakeFormServer(t, pool)
	cookie := loginTestUser(t, e, pool, uniqueEmail("if-pubget"), "test-agent", uniqueIP())
	formsBase, cfBase := setupIntakeFormProject(t, e, cookie, "if-pubget")
	created, _, _ := createFullForm(t, e, cookie, formsBase, cfBase)

	rec := getPublic(t, e, "/api/v1/forms/"+created.ShareToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("public GET: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode public form: %v", err)
	}
	// Top-level keys are exactly the documented public envelope.
	for _, want := range []string{"name", "description", "project_name", "fields"} {
		if _, ok := got[want]; !ok {
			t.Errorf("public form missing key %q (body: %s)", want, rec.Body.String())
		}
	}
	for _, leak := range []string{"id", "project_id", "intake_id", "share_token", "created_by", "is_enabled"} {
		if _, ok := got[leak]; ok {
			t.Errorf("public form leaks internal key %q", leak)
		}
	}
	var envelope struct {
		Name        string  `json:"name"`
		Description *string `json:"description"`
		ProjectName string  `json:"project_name"`
		Fields      []struct {
			Key      string `json:"key"`
			Label    string `json:"label"`
			Required bool   `json:"required"`
			Type     string `json:"type"`
			Options  []struct {
				Value string `json:"value"`
			} `json:"options"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope.Name != "Bug report" || envelope.ProjectName != "Engineering" {
		t.Errorf("envelope = %+v, want name Bug report + project Engineering", envelope)
	}
	if envelope.Description == nil || *envelope.Description != "Tell us what broke." {
		t.Errorf("description = %v, want the form description", envelope.Description)
	}
	if len(envelope.Fields) != 5 {
		t.Fatalf("fields = %d, want 5", len(envelope.Fields))
	}
	wantTypes := map[string]string{
		"title": "title", "description": "description", "priority": "priority",
	}
	for _, f := range envelope.Fields {
		if strings.HasPrefix(f.Key, "custom:") {
			continue
		}
		if want, ok := wantTypes[f.Key]; !ok || f.Type != want {
			t.Errorf("field %q type = %q, want %q", f.Key, f.Type, want)
		}
		if len(f.Options) != 0 {
			t.Errorf("field %q options = %v, want empty for non-select", f.Key, f.Options)
		}
	}
	// The select custom field resolves type + options; the number field
	// resolves its type.
	var selField, numField *struct {
		Key      string `json:"key"`
		Label    string `json:"label"`
		Required bool   `json:"required"`
		Type     string `json:"type"`
		Options  []struct {
			Value string `json:"value"`
		} `json:"options"`
	}
	for i := range envelope.Fields {
		f := &envelope.Fields[i]
		if f.Label == "How bad?" {
			selField = f
		}
		if f.Label == "Times seen" {
			numField = f
		}
	}
	if selField == nil || selField.Type != "select" || !selField.Required {
		t.Errorf("select field = %+v, want type select + required", selField)
	} else if len(selField.Options) != 2 || selField.Options[0].Value != "low" || selField.Options[1].Value != "high" {
		t.Errorf("select options = %+v, want [low high]", selField.Options)
	}
	if numField == nil || numField.Type != "number" || numField.Required {
		t.Errorf("number field = %+v, want type number + optional", numField)
	}
	if envelope.Fields[0].Key != "title" || !envelope.Fields[0].Required {
		t.Errorf("first field = %+v, want the required title", envelope.Fields[0])
	}

	// Shape-guard: the public paths must stay documented in docs/openapi.yaml
	// (the route-coverage test pins every registered route to a spec entry).
	var rawSpec struct {
		Paths map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(docs.OpenAPI, &rawSpec); err != nil {
		t.Fatalf("spec not valid JSON: %v", err)
	}
	if _, ok := rawSpec.Paths["/api/v1/forms/{token}"]; !ok {
		t.Error("spec missing /api/v1/forms/{token}")
	}
	if _, ok := rawSpec.Paths["/api/v1/forms/{token}/submit"]; !ok {
		t.Error("spec missing /api/v1/forms/{token}/submit")
	}
}

func TestIntakeFormPublicGetUnknownToken(t *testing.T) {
	pool := newTestPool(t)
	e := testIntakeFormServer(t, pool)

	rec := getPublic(t, e, "/api/v1/forms/does-not-exist")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown token: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postPublicJSON(t, e, "/api/v1/forms/does-not-exist/submit", `{"values":{"title":"x"}}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown token submit: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestIntakeFormDisabledForm(t *testing.T) {
	pool := newTestPool(t)
	e := testIntakeFormServer(t, pool)
	cookie := loginTestUser(t, e, pool, uniqueEmail("if-disabled"), "test-agent", uniqueIP())
	formsBase, _ := setupIntakeFormProject(t, e, cookie, "if-disabled")
	created := createIntakeFormHTTP(t, e, cookie, formsBase,
		`{"name":"Bug report","fields":`+titleOnlyFields+`}`)

	// Disable the form.
	rec := postAuthedJSON(t, e, http.MethodPatch, formsBase+"/"+created.ID, cookie, `{"is_enabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("disable: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	// Manage GET still 200 (admins can see and re-enable it).
	rec = postAuthedJSON(t, e, http.MethodGet, formsBase+"/"+created.ID, cookie, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("manage GET disabled: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	// Public GET and submit both 404 — never leak that a disabled form exists.
	rec = getPublic(t, e, "/api/v1/forms/"+created.ShareToken)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("public GET disabled: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postPublicJSON(t, e, "/api/v1/forms/"+created.ShareToken+"/submit", `{"values":{"title":"x"}}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("submit disabled: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestIntakeFormRegenerateRotates(t *testing.T) {
	pool := newTestPool(t)
	e := testIntakeFormServer(t, pool)
	cookie := loginTestUser(t, e, pool, uniqueEmail("if-regen"), "test-agent", uniqueIP())
	formsBase, _ := setupIntakeFormProject(t, e, cookie, "if-regen")
	created := createIntakeFormHTTP(t, e, cookie, formsBase,
		`{"name":"Bug report","fields":`+titleOnlyFields+`}`)
	oldToken := created.ShareToken

	rec := postAuthedJSON(t, e, http.MethodPost, formsBase+"/"+created.ID+"/regenerate", cookie, `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("regenerate: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var rotated intakeFormCreated
	if err := json.Unmarshal(rec.Body.Bytes(), &rotated); err != nil {
		t.Fatalf("decode rotated: %v", err)
	}
	if rotated.ShareToken == "" || rotated.ShareToken == oldToken {
		t.Fatalf("rotated token = %q, want a fresh token", rotated.ShareToken)
	}

	// The old token is immediately invalid: public GET and submit 404.
	if rec := getPublic(t, e, "/api/v1/forms/"+oldToken); rec.Code != http.StatusNotFound {
		t.Errorf("old token GET: status = %d, want 404", rec.Code)
	}
	if rec := postPublicJSON(t, e, "/api/v1/forms/"+oldToken+"/submit", `{"values":{"title":"x"}}`); rec.Code != http.StatusNotFound {
		t.Errorf("old token submit: status = %d, want 404", rec.Code)
	}
	// The new token works.
	if rec := getPublic(t, e, "/api/v1/forms/"+rotated.ShareToken); rec.Code != http.StatusOK {
		t.Errorf("new token GET: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestIntakeFormSubmitMissingTitle(t *testing.T) {
	pool := newTestPool(t)
	e := testIntakeFormServer(t, pool)
	cookie := loginTestUser(t, e, pool, uniqueEmail("if-subnotitle"), "test-agent", uniqueIP())
	formsBase, _ := setupIntakeFormProject(t, e, cookie, "if-subnotitle")
	created := createIntakeFormHTTP(t, e, cookie, formsBase,
		`{"name":"Bug report","fields":`+titleOnlyFields+`}`)

	for _, body := range []string{`{"values":{}}`, `{"values":{"title":"  "}}`, `{"values":{"title":null}}`, `{}`} {
		rec := postPublicJSON(t, e, "/api/v1/forms/"+created.ShareToken+"/submit", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("submit %s: status = %d, want 400 (body: %s)", body, rec.Code, rec.Body.String())
		}
	}
}

func TestIntakeFormSubmitRequiredCustomMissing(t *testing.T) {
	pool := newTestPool(t)
	e := testIntakeFormServer(t, pool)
	cookie := loginTestUser(t, e, pool, uniqueEmail("if-subreq"), "test-agent", uniqueIP())
	formsBase, cfBase := setupIntakeFormProject(t, e, cookie, "if-subreq")
	created, _, _ := createFullForm(t, e, cookie, formsBase, cfBase)

	// The required select field ("How bad?") is missing → 400 naming the label.
	rec := postPublicJSON(t, e, "/api/v1/forms/"+created.ShareToken+"/submit",
		`{"values":{"title":"It broke"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing required custom: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "How bad?") {
		t.Fatalf("body %s does not name the missing label", rec.Body.String())
	}
}

func TestIntakeFormSubmitInvalidSelect(t *testing.T) {
	pool := newTestPool(t)
	e := testIntakeFormServer(t, pool)
	cookie := loginTestUser(t, e, pool, uniqueEmail("if-subsel"), "test-agent", uniqueIP())
	formsBase, cfBase := setupIntakeFormProject(t, e, cookie, "if-subsel")
	created, selID, _ := createFullForm(t, e, cookie, formsBase, cfBase)

	rec := postPublicJSON(t, e, "/api/v1/forms/"+created.ShareToken+"/submit",
		fmt.Sprintf(`{"values":{"title":"It broke","custom:%s":"catastrophic"}}`, selID))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid select option: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "How bad?") {
		t.Fatalf("body %s does not name the field label", rec.Body.String())
	}
}

func TestIntakeFormSubmitPriorityValidation(t *testing.T) {
	pool := newTestPool(t)
	e := testIntakeFormServer(t, pool)
	cookie := loginTestUser(t, e, pool, uniqueEmail("if-subpri"), "test-agent", uniqueIP())
	formsBase, cfBase := setupIntakeFormProject(t, e, cookie, "if-subpri")
	created, selID, _ := createFullForm(t, e, cookie, formsBase, cfBase)
	validValues := fmt.Sprintf(`"title":"Prio test","custom:%s":"low"`, selID)

	for _, prio := range []string{`9`, `-1`, `"high"`, `2.5`} {
		rec := postPublicJSON(t, e, "/api/v1/forms/"+created.ShareToken+"/submit",
			fmt.Sprintf(`{"values":{%s,"priority":%s}}`, validValues, prio))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("priority %s: status = %d, want 400 (body: %s)", prio, rec.Code, rec.Body.String())
		}
	}
	// A valid priority submits.
	rec := postPublicJSON(t, e, "/api/v1/forms/"+created.ShareToken+"/submit",
		fmt.Sprintf(`{"values":{%s,"priority":3}}`, validValues))
	if rec.Code != http.StatusCreated {
		t.Fatalf("priority 3: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestIntakeFormSubmitValid(t *testing.T) {
	pool := newTestPool(t)
	e := testIntakeFormServer(t, pool)
	cookie := loginTestUser(t, e, pool, uniqueEmail("if-subvalid"), "test-agent", uniqueIP())
	formsBase, cfBase := setupIntakeFormProject(t, e, cookie, "if-subvalid")
	created, selID, numID := createFullForm(t, e, cookie, formsBase, cfBase)

	rec := postPublicJSON(t, e, "/api/v1/forms/"+created.ShareToken+"/submit",
		fmt.Sprintf(`{"values":{"title":"Login explodes","description":"Steps: open app","priority":2,"custom:%s":"high","custom:%s":7}}`,
			selID, numID))
	if rec.Code != http.StatusCreated {
		t.Fatalf("valid submit: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var submitted struct {
		IssueID string `json:"issue_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &submitted); err != nil {
		t.Fatalf("decode submit: %v", err)
	}
	if submitted.IssueID == "" {
		t.Fatal("submit returned no issue_id")
	}

	ctx := context.Background()
	// The issue exists, attributed to the dedicated system user — never a
	// random member — with the submitted title/priority/description doc.
	var createdBy, name string
	var priority int
	var descJSON string
	if err := pool.QueryRow(ctx,
		`SELECT created_by::text, name, priority, description::text FROM issues WHERE id = $1::uuid`,
		submitted.IssueID).Scan(&createdBy, &name, &priority, &descJSON); err != nil {
		t.Fatalf("read issue: %v", err)
	}
	if createdBy != "4451ef68-06f5-4bc7-80db-dc0aec4c9d67" {
		t.Errorf("created_by = %s, want the dedicated system user", createdBy)
	}
	if name != "Login explodes" || priority != 2 {
		t.Errorf("issue = (%q, prio %d), want (Login explodes, 2)", name, priority)
	}
	var doc struct {
		Type    string `json:"type"`
		Content []struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"content"`
	}
	if err := json.Unmarshal([]byte(descJSON), &doc); err != nil || doc.Type != "doc" {
		t.Fatalf("description = %s, want a rich-text doc", descJSON)
	}
	if len(doc.Content) == 0 || len(doc.Content[0].Content) == 0 || doc.Content[0].Content[0].Text != "Steps: open app" {
		t.Errorf("description text = %s, want the submitted text", descJSON)
	}

	// The intake_issues row is pending (same as in-app intake creation).
	var status int16
	if err := pool.QueryRow(ctx,
		`SELECT status FROM intake_issues WHERE issue_id = $1::uuid`, submitted.IssueID).Scan(&status); err != nil {
		t.Fatalf("read intake row: %v", err)
	}
	if status != 0 {
		t.Errorf("intake status = %d, want pending (0)", status)
	}

	// Custom values persisted: select → value_text, number → value_number.
	vals := map[string]struct {
		text *string
		num  *string
	}{}
	rows, err := pool.Query(ctx,
		`SELECT field_id::text, value_text, value_number::text FROM issue_custom_values WHERE issue_id = $1::uuid`,
		submitted.IssueID)
	if err != nil {
		t.Fatalf("read custom values: %v", err)
	}
	for rows.Next() {
		var fid string
		var vt, vn *string
		if err := rows.Scan(&fid, &vt, &vn); err != nil {
			t.Fatalf("scan custom value: %v", err)
		}
		vals[fid] = struct {
			text *string
			num  *string
		}{vt, vn}
	}
	rows.Close()
	sv, ok := vals[selID]
	if !ok || sv.text == nil || *sv.text != "high" {
		t.Errorf("select value = %+v, want value_text high", sv)
	}
	nv, ok := vals[numID]
	if !ok || nv.num == nil || *nv.num != "7" {
		t.Errorf("number value = %+v, want value_number 7", nv)
	}

	// The _created activity row exists with the system user as actor —
	// the same row in-app creation writes.
	var actorID string
	if err := pool.QueryRow(ctx,
		`SELECT actor_id::text FROM issue_activities WHERE issue_id = $1::uuid AND field = '_created'`,
		submitted.IssueID).Scan(&actorID); err != nil {
		t.Fatalf("read _created activity: %v", err)
	}
	if actorID != "4451ef68-06f5-4bc7-80db-dc0aec4c9d67" {
		t.Errorf("_created actor = %s, want the system user", actorID)
	}
}

func TestIntakeFormSubmitValidMinimal(t *testing.T) {
	pool := newTestPool(t)
	e := testIntakeFormServer(t, pool)
	cookie := loginTestUser(t, e, pool, uniqueEmail("if-submin"), "test-agent", uniqueIP())
	formsBase, _ := setupIntakeFormProject(t, e, cookie, "if-submin")
	created := createIntakeFormHTTP(t, e, cookie, formsBase,
		`{"name":"Quick","fields":`+titleOnlyFields+`}`)

	// Title only — optional fields absent entirely.
	rec := postPublicJSON(t, e, "/api/v1/forms/"+created.ShareToken+"/submit",
		`{"values":{"title":"Just a title"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("minimal submit: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestIntakeFormListAndDelete(t *testing.T) {
	pool := newTestPool(t)
	e := testIntakeFormServer(t, pool)
	cookie := loginTestUser(t, e, pool, uniqueEmail("if-listdel"), "test-agent", uniqueIP())
	formsBase, _ := setupIntakeFormProject(t, e, cookie, "if-listdel")

	a := createIntakeFormHTTP(t, e, cookie, formsBase,
		`{"name":"Bugs","fields":`+titleOnlyFields+`}`)
	b := createIntakeFormHTTP(t, e, cookie, formsBase,
		`{"name":"Features","fields":`+titleOnlyFields+`}`)

	rec := postAuthedJSON(t, e, http.MethodGet, formsBase, cookie, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var listed struct {
		Forms []intakeFormCreated `json:"forms"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed.Forms) != 2 {
		t.Fatalf("forms = %d, want 2", len(listed.Forms))
	}
	// Tokens differ per form.
	if listed.Forms[0].ShareToken == listed.Forms[1].ShareToken {
		t.Error("forms share the same token, want unique tokens")
	}

	// GET one.
	rec = postAuthedJSON(t, e, http.MethodGet, formsBase+"/"+a.ID, cookie, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	// PATCH rename.
	rec = postAuthedJSON(t, e, http.MethodPatch, formsBase+"/"+b.ID, cookie, `{"name":"Ideas"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var patched intakeFormCreated
	if err := json.Unmarshal(rec.Body.Bytes(), &patched); err != nil {
		t.Fatalf("decode patched: %v", err)
	}
	if patched.Name != "Ideas" {
		t.Errorf("patched name = %q, want Ideas", patched.Name)
	}

	// DELETE one; the list shrinks and the token dies.
	rec = postAuthedJSON(t, e, http.MethodDelete, formsBase+"/"+a.ID, cookie, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodGet, formsBase, cookie, "")
	var relisted struct {
		Forms []intakeFormCreated `json:"forms"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &relisted); err != nil {
		t.Fatalf("decode relist: %v", err)
	}
	if len(relisted.Forms) != 1 || relisted.Forms[0].ID != b.ID {
		t.Fatalf("relisted = %+v, want only the surviving form", relisted.Forms)
	}
	if rec := getPublic(t, e, "/api/v1/forms/"+a.ShareToken); rec.Code != http.StatusNotFound {
		t.Errorf("deleted form token: status = %d, want 404", rec.Code)
	}
	// Deleting again → 404.
	rec = postAuthedJSON(t, e, http.MethodDelete, formsBase+"/"+a.ID, cookie, "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("re-delete: status = %d, want 404", rec.Code)
	}
}

func TestIntakeFormPatchRejectsInvalidFields(t *testing.T) {
	pool := newTestPool(t)
	e := testIntakeFormServer(t, pool)
	cookie := loginTestUser(t, e, pool, uniqueEmail("if-patchbad"), "test-agent", uniqueIP())
	formsBase, _ := setupIntakeFormProject(t, e, cookie, "if-patchbad")
	created := createIntakeFormHTTP(t, e, cookie, formsBase,
		`{"name":"Bug report","fields":`+titleOnlyFields+`}`)

	// PATCH that drops the title → 400; the stored form is unchanged.
	rec := postAuthedJSON(t, e, http.MethodPatch, formsBase+"/"+created.ID, cookie,
		`{"fields":[{"key":"description","label":"Details","required":false}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("patch dropping title: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodGet, formsBase+"/"+created.ID, cookie, "")
	var refetched intakeFormCreated
	if err := json.Unmarshal(rec.Body.Bytes(), &refetched); err != nil {
		t.Fatalf("decode refetch: %v", err)
	}
	if len(refetched.Fields) != 1 || refetched.Fields[0].Key != "title" {
		t.Errorf("fields after failed patch = %+v, want the original title field", refetched.Fields)
	}
}

func TestIntakeFormPatchDescriptionClear(t *testing.T) {
	pool := newTestPool(t)
	e := testIntakeFormServer(t, pool)
	cookie := loginTestUser(t, e, pool, uniqueEmail("if-patchdesc"), "test-agent", uniqueIP())
	formsBase, _ := setupIntakeFormProject(t, e, cookie, "if-patchdesc")
	created := createIntakeFormHTTP(t, e, cookie, formsBase,
		`{"name":"Bug report","description":"Has a description","fields":`+titleOnlyFields+`}`)

	// Explicit null clears the description (omitted would leave it).
	rec := postAuthedJSON(t, e, http.MethodPatch, formsBase+"/"+created.ID, cookie, `{"description":null}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch clear: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var patched struct {
		Description *string `json:"description"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &patched); err != nil {
		t.Fatalf("decode patched: %v", err)
	}
	if patched.Description != nil {
		t.Errorf("description = %q, want cleared (null)", *patched.Description)
	}

	// Setting a new description works.
	rec = postAuthedJSON(t, e, http.MethodPatch, formsBase+"/"+created.ID, cookie, `{"description":"Back again"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch set: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &patched); err != nil {
		t.Fatalf("decode repatched: %v", err)
	}
	if patched.Description == nil || *patched.Description != "Back again" {
		t.Errorf("description = %v, want Back again", patched.Description)
	}
}
