package api

// Required custom-field enforcement HTTP tests (C18T1): POST /issues
// without values for required fields → 400 with the §5 envelope naming
// the missing fields (details.field_names); a satisfied create → 201;
// PUT /custom-values on a previously-empty required field with an empty
// value → 400 naming the field; filling it → 200. Real test database,
// no skips.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestRequiredCustomFieldsHTTP(t *testing.T) {
	pool := newTestPool(t)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterCustomFieldRoutes(e, &IssueHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail("cf-req-http"), "test-agent/1.0", uniqueIP())
	slug := uniqueSlug("cf-req-http")
	createWorkspaceHTTP(t, e, cookie, "Required Field Co", slug)
	ident := uniqueProjectIdentifier("CF")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	projBase := "/api/v1/workspaces/" + slug + "/projects/" + ident
	fieldBase := projBase + "/custom-fields"

	createField := func(body string) string {
		t.Helper()
		rec := postAuthedJSON(t, e, http.MethodPost, fieldBase, cookie, body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create field: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
		}
		var created struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
			t.Fatalf("decode field: %v", err)
		}
		return created.ID
	}

	// The probe issue predates the required fields.
	rec := postAuthedJSON(t, e, http.MethodPost, projBase+"/issues", cookie, `{"name":"probe"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create probe issue: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var probe struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &probe); err != nil {
		t.Fatalf("decode probe: %v", err)
	}

	textID := createField(`{"name":"Summary","field_type":"text","required":true}`)
	checkID := createField(`{"name":"Reviewed","field_type":"checkbox","required":true}`)

	// 1. Create without custom values → 400 naming both fields.
	rec = postAuthedJSON(t, e, http.MethodPost, projBase+"/issues", cookie, `{"name":"gap"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("create missing required: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	code, message, details, _ := decodeEnvelope(t, rec.Body.Bytes())
	if code != ErrCodeBadRequest {
		t.Fatalf("envelope code = %q, want %q", code, ErrCodeBadRequest)
	}
	if !strings.Contains(message, "Summary") || !strings.Contains(message, "Reviewed") {
		t.Fatalf("message %q does not name the missing fields", message)
	}
	names, ok := details["field_names"].([]any)
	if !ok || len(names) != 2 || names[0] != "Summary" || names[1] != "Reviewed" {
		t.Fatalf("details.field_names = %v, want [Summary Reviewed]", details["field_names"])
	}

	// 2. Create with an empty required value → 400 naming just that field.
	rec = postAuthedJSON(t, e, http.MethodPost, projBase+"/issues", cookie,
		`{"name":"gap","custom_values":{"`+textID+`":"","`+checkID+`":false}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("create empty required: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	_, message, details, _ = decodeEnvelope(t, rec.Body.Bytes())
	if !strings.Contains(message, "Summary") || strings.Contains(message, "Reviewed") {
		t.Fatalf("message %q names the wrong fields", message)
	}
	names, _ = details["field_names"].([]any)
	if len(names) != 1 || names[0] != "Summary" {
		t.Fatalf("details.field_names = %v, want [Summary]", details["field_names"])
	}

	// 3. Satisfied create (checkbox false counts as present) → 201.
	rec = postAuthedJSON(t, e, http.MethodPost, projBase+"/issues", cookie,
		`{"name":"whole","custom_values":{"`+textID+`":"all good","`+checkID+`":false}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create satisfied: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}

	// 4. Partial update: touch the previously-empty required field with an
	// empty value → 400 naming it.
	rec = postAuthedJSON(t, e, http.MethodPut, projBase+"/issues/"+probe.ID+"/custom-values", cookie,
		`{"values":{"`+textID+`":""}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("partial empty required: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	code, message, details, _ = decodeEnvelope(t, rec.Body.Bytes())
	if code != ErrCodeBadRequest || !strings.Contains(message, "Summary") {
		t.Fatalf("400 shape wrong: code=%q message=%q", code, message)
	}
	names, _ = details["field_names"].([]any)
	if len(names) != 1 || names[0] != "Summary" {
		t.Fatalf("details.field_names = %v, want [Summary]", details["field_names"])
	}

	// 5. Fill it → 200 with the stored values.
	rec = postAuthedJSON(t, e, http.MethodPut, projBase+"/issues/"+probe.ID+"/custom-values", cookie,
		`{"values":{"`+textID+`":"done"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("partial fill required: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
}
