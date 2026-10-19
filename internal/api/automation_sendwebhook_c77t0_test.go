package api

// C77T0: send_webhook automation action — HTTP tests.
//
// Rule CRUD with the new action through the project-nested routes:
// 201 create echoes masked header values (never the secrets), list and
// patch redact the same way, the validation matrix is 400s, and the
// RBAC gates mirror the existing member(15)+ convention (guest → 403).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func sendWebhookTestRuleBody(url string) string {
	return fmt.Sprintf(`{
		"name": "webhook rule",
		"trigger": {"type": "issue.created"},
		"actions": [{
			"type": "send_webhook",
			"url": %q,
			"method": "PUT",
			"headers": {"Authorization": "Bearer s3cr3t-value", "X-Tenant": "acme"},
			"payload_template": "{\"title\": {{issue.title}}}"
		}]
	}`, url)
}

// decodeSendWebhookAction pulls actions[0] out of a rule response body.
func decodeSendWebhookAction(t *testing.T, body string) map[string]any {
	t.Helper()
	var rule struct {
		Actions []map[string]any `json:"actions"`
	}
	if err := json.Unmarshal([]byte(body), &rule); err != nil {
		t.Fatalf("decode rule: %v (body: %s)", err, body)
	}
	if len(rule.Actions) != 1 {
		t.Fatalf("actions = %d, want 1 (body: %s)", len(rule.Actions), body)
	}
	return rule.Actions[0]
}

func TestAutomationSendWebhookHTTP(t *testing.T) {
	pool := newTestPool(t)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterAutomationRoutes(e, &AutomationHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail("auto-swh"), "test-agent", uniqueIP())
	slug := uniqueSlug("auto-swh")
	createWorkspaceHTTP(t, e, cookie, "Webhook Co", slug)
	ident := uniqueProjectIdentifier("SW")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	autoBase := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/automations"

	// Guest (role 5): the write gate mirrors the existing automation
	// convention — member(15)+.
	guestCookie := loginTestUser(t, e, pool, uniqueEmail("auto-swh-guest"), "test-agent", uniqueIP())
	guestID := authedUserID(t, e, guestCookie)
	rec := postAuthedJSON(t, e, http.MethodPost, "/api/v1/workspaces/"+slug+"/members", cookie,
		fmt.Sprintf(`{"user_id":%q,"role":5}`, guestID))
	if rec.Code != http.StatusOK {
		t.Fatalf("add guest: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	// Create → 201. Header VALUES are masked; names survive; the secret
	// never appears in the response.
	rec = postAuthedJSON(t, e, http.MethodPost, autoBase, cookie,
		sendWebhookTestRuleBody("https://example.com/hook"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "s3cr3t-value") {
		t.Fatalf("create response leaks the header value: %s", rec.Body.String())
	}
	action := decodeSendWebhookAction(t, rec.Body.String())
	if action["type"] != "send_webhook" || action["url"] != "https://example.com/hook" ||
		action["method"] != "PUT" {
		t.Fatalf("action = %v, want send_webhook/https://example.com/hook/PUT", action)
	}
	hdrs, ok := action["headers"].(map[string]any)
	if !ok || hdrs["Authorization"] != "********" || hdrs["X-Tenant"] != "********" {
		t.Fatalf("headers = %v, want names kept and values masked", action["headers"])
	}
	if action["payload_template"] != `{"title": {{issue.title}}}` {
		t.Fatalf("payload_template = %v, want the template echoed back", action["payload_template"])
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.ID == "" {
		t.Fatalf("create returned no id (body: %s)", rec.Body.String())
	}

	// List → 200, redacted the same way.
	rec = getAuthed(t, e, http.MethodGet, autoBase, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "s3cr3t-value") {
		t.Fatalf("list response leaks the header value: %s", rec.Body.String())
	}

	// PATCH the URL → 200, still redacted. Header values are
	// write-only: the patch replaces the map wholesale.
	rec = postAuthedJSON(t, e, http.MethodPatch, autoBase+"/"+created.ID, cookie,
		`{"actions": [{"type": "send_webhook", "url": "https://example.com/hook2",
		  "headers": {"Authorization": "Bearer new-secret"}}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	action = decodeSendWebhookAction(t, rec.Body.String())
	if action["url"] != "https://example.com/hook2" {
		t.Fatalf("patched action url = %v", action["url"])
	}
	hdrs, ok = action["headers"].(map[string]any)
	if !ok || hdrs["Authorization"] != "********" || len(hdrs) != 1 {
		t.Fatalf("patched headers = %v, want only Authorization masked", action["headers"])
	}
	if strings.Contains(rec.Body.String(), "new-secret") {
		t.Fatalf("patch response leaks the header value: %s", rec.Body.String())
	}

	// Validation matrix → 400 with the spec §5 envelope.
	badCases := []struct {
		name string
		body string
	}{
		{"missing url", `{"name":"r","trigger":{"type":"issue.created"},
			"actions":[{"type":"send_webhook"}]}`},
		{"bad url scheme", `{"name":"r","trigger":{"type":"issue.created"},
			"actions":[{"type":"send_webhook","url":"ftp://example.com/hook"}]}`},
		{"bad method", `{"name":"r","trigger":{"type":"issue.created"},
			"actions":[{"type":"send_webhook","url":"https://example.com/hook","method":"DELETE"}]}`},
		{"blocked header", `{"name":"r","trigger":{"type":"issue.created"},
			"actions":[{"type":"send_webhook","url":"https://example.com/hook",
				"headers":{"Host":"evil.example"}}]}`},
		{"template unknown variable", `{"name":"r","trigger":{"type":"issue.created"},
			"actions":[{"type":"send_webhook","url":"https://example.com/hook",
				"payload_template":"{\"x\": {{bogus}}}"}]}`},
		{"template unbalanced", `{"name":"r","trigger":{"type":"issue.created"},
			"actions":[{"type":"send_webhook","url":"https://example.com/hook",
				"payload_template":"{\"x\": {{issue.title}"}]}`},
	}
	for _, tc := range badCases {
		t.Run(tc.name, func(t *testing.T) {
			rec := postAuthedJSON(t, e, http.MethodPost, autoBase, cookie, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
			}
			var errEnv struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &errEnv); err != nil {
				t.Fatalf("decode error envelope: %v", err)
			}
			if errEnv.Error.Code != "bad_request" {
				t.Fatalf("error code = %q, want bad_request", errEnv.Error.Code)
			}
		})
	}

	// Guest create → 403 (existing automation RBAC, mirrored).
	rec = postAuthedJSON(t, e, http.MethodPost, autoBase, guestCookie,
		sendWebhookTestRuleBody("https://example.com/hook"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest create: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}

	// Guest list → 403.
	rec = getAuthed(t, e, http.MethodGet, autoBase, guestCookie)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest list: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
}
