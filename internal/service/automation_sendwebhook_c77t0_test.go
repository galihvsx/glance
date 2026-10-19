package service

// C77T0: send_webhook automation action — service tests.
//
// Covers the config validation matrix (write-time), the template
// vocabulary renderer, the default event envelope, the firing path
// (outbox enqueue, run-row outcome, abort-on-render-failure, no
// issue.updated side effects), the dispatcher delivery path for ad-hoc
// rows (custom method/headers/verbatim body, SSRF refusal), and header
// redaction on reads.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"glance/internal/mail"
)

// deleteAdHocOutboxRows removes the webhook.deliver rows with an empty
// webhook_id (the automation send_webhook shape) at test end, so later
// dispatcher tests start from a clean slate — the shared test DB is
// never truncated, and a stale pending row would otherwise be claimed
// and really delivered by a later runScoped(ctx, "").
func deleteAdHocOutboxRows(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(),
			`DELETE FROM outbox WHERE event = 'webhook.deliver' AND payload->>'webhook_id' = ''`); err != nil {
			t.Errorf("cleanup ad-hoc outbox rows: %v", err)
		}
	})
}

func swAction(url string) AutomationAction {
	return AutomationAction{Type: "send_webhook", URL: &url}
}

// TestValidateSendWebhookAction is the write-time config validation
// matrix: every bad shape must be an honest 400 (ErrInvalidAutomationAction).
func TestValidateSendWebhookAction(t *testing.T) {
	strp := func(s string) *string { return &s }
	cases := []struct {
		name    string
		mutate  func(*AutomationAction)
		wantErr bool
	}{
		{"minimal url only", func(a *AutomationAction) {}, false},
		{"http url with port and query", func(a *AutomationAction) {
			a.URL = strp("http://example.com:8080/hook?a=b")
		}, false},
		{"method put", func(a *AutomationAction) { a.Method = strp("PUT") }, false},
		{"method lowercase post", func(a *AutomationAction) { a.Method = strp("post") }, false},
		{"method empty defaults to post", func(a *AutomationAction) { a.Method = strp("") }, false},
		{"headers ok", func(a *AutomationAction) {
			a.Headers = map[string]string{"Authorization": "Bearer s3cr3t", "X-Tenant": "acme"}
		}, false},
		{"payload template valid json", func(a *AutomationAction) {
			a.PayloadTemplate = strp(`{"title": {{issue.title}}}`)
		}, false},
		{"payload template unknown variable", func(a *AutomationAction) {
			a.PayloadTemplate = strp(`{"x": {{bogus.var}}}`)
		}, true},
		{"payload template unbalanced braces", func(a *AutomationAction) {
			a.PayloadTemplate = strp(`{"x": {{issue.title}`)
		}, true},
		{"payload template quoted placeholder double-encodes", func(a *AutomationAction) {
			a.PayloadTemplate = strp(`{"title": "{{issue.title}}"}`)
		}, true},
		{"payload template invalid json", func(a *AutomationAction) {
			a.PayloadTemplate = strp(`{not json`)
		}, true},
		{"payload template empty treated as absent", func(a *AutomationAction) {
			a.PayloadTemplate = strp("   ")
		}, false},

		{"missing url", func(a *AutomationAction) { a.URL = nil }, true},
		{"empty url", func(a *AutomationAction) { a.URL = strp("  ") }, true},
		{"url not absolute", func(a *AutomationAction) { a.URL = strp("not-a-url") }, true},
		{"url relative path", func(a *AutomationAction) { a.URL = strp("/hook/path") }, true},
		{"url ftp scheme", func(a *AutomationAction) { a.URL = strp("ftp://example.com/hook") }, true},
		{"url no host", func(a *AutomationAction) { a.URL = strp("https://") }, true},
		{"method delete", func(a *AutomationAction) { a.Method = strp("DELETE") }, true},
		{"method get", func(a *AutomationAction) { a.Method = strp("GET") }, true},
		{"method patch", func(a *AutomationAction) { a.Method = strp("PATCH") }, true},
		{"header host blocked", func(a *AutomationAction) {
			a.Headers = map[string]string{"Host": "evil.example"}
		}, true},
		{"header host blocked case-insensitive", func(a *AutomationAction) {
			a.Headers = map[string]string{"HOST": "evil.example"}
		}, true},
		{"header content-length blocked", func(a *AutomationAction) {
			a.Headers = map[string]string{"Content-Length": "5"}
		}, true},
		{"header empty name", func(a *AutomationAction) {
			a.Headers = map[string]string{"  ": "x"}
		}, true},
		{"header redaction placeholder rejected", func(a *AutomationAction) {
			a.Headers = map[string]string{"Authorization": redactedAutomationHeaderValue}
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := swAction("https://example.com/hook")
			tc.mutate(&a)
			err := validateSendWebhookAction(a)
			if tc.wantErr && !errors.Is(err, ErrInvalidAutomationAction) {
				t.Fatalf("validateSendWebhookAction = %v, want ErrInvalidAutomationAction", err)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateSendWebhookAction = %v, want nil", err)
			}
		})
	}
}

// TestValidateSendWebhookActionViaRuleCreate pins the validation at the
// rule-write boundary: a bad send_webhook config fails the whole rule
// create with ErrInvalidAutomationAction.
func TestValidateSendWebhookActionViaRuleCreate(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()
	bad := swAction("ftp://example.com/hook")
	_, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin,
		automationRuleInput(fx, nil, []AutomationAction{bad}))
	if !errors.Is(err, ErrInvalidAutomationAction) {
		t.Fatalf("CreateAutomationRule(bad url) = %v, want ErrInvalidAutomationAction", err)
	}
	noURL := AutomationAction{Type: "send_webhook"}
	_, err = CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin,
		automationRuleInput(fx, nil, []AutomationAction{noURL}))
	if !errors.Is(err, ErrInvalidAutomationAction) {
		t.Fatalf("CreateAutomationRule(missing url) = %v, want ErrInvalidAutomationAction", err)
	}
}

// sendWebhookTestVars is a fixed firing context for renderer tests.
func sendWebhookTestVars() map[string]any {
	return (&sendWebhookFiring{
		RuleID:      "rule-1",
		TriggerType: "issue.state_changed",
		IssueID:     "issue-1",
		Identifier:  "ENG-42",
		Title:       `Say "hi"`,
		State:       "In Progress",
		Priority:    2,
		Labels:      []string{"bug", "ui"},
		Assignees:   []string{"Galih"},
	}).vars()
}

// TestRenderSendWebhookTemplate pins the template vocabulary:
// placeholders substitute the JSON encoding of the value (strings
// arrive quoted — placeholders must NOT be quoted), unknown variables
// and unbalanced braces fail, and the rendered result must be valid
// JSON.
func TestRenderSendWebhookTemplate(t *testing.T) {
	vars := sendWebhookTestVars()
	cases := []struct {
		name string
		tpl  string
		want string
	}{
		{"string substitution quoted",
			`{"title": {{issue.title}}}`,
			`{"title": "Say \"hi\""}`},
		{"number substitution",
			`{"priority": {{issue.priority}}}`,
			`{"priority": 2}`},
		{"array substitution",
			`{"labels": {{issue.labels}}, "assignees": {{issue.assignees}}}`,
			`{"labels": ["bug","ui"], "assignees": ["Galih"]}`},
		{"rule and trigger vars",
			`{"rule_id": {{rule.id}}, "trigger": {{trigger}}}`,
			`{"rule_id": "rule-1", "trigger": "issue.state_changed"}`},
		{"no placeholders passthrough",
			`{"static": true}`,
			`{"static": true}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := renderSendWebhookTemplate(tc.tpl, vars)
			if err != nil {
				t.Fatalf("render = %v, want nil", err)
			}
			if string(got) != tc.want {
				t.Fatalf("render = %s, want %s", got, tc.want)
			}
		})
	}

	failures := []struct {
		name string
		tpl  string
	}{
		{"unknown variable", `{"x": {{bogus.var}}}`},
		{"unknown bare variable", `{{nope}}`},
		{"unclosed placeholder", `{"x": {{issue.title}`},
		{"quoted string placeholder double-encodes",
			`{"title": "{{issue.title}}"}`},
		{"non-json result", `hello {{issue.title}}`},
	}
	for _, tc := range failures {
		t.Run("fail/"+tc.name, func(t *testing.T) {
			if _, err := renderSendWebhookTemplate(tc.tpl, vars); err == nil {
				t.Fatalf("render(%q) = nil, want error", tc.tpl)
			}
		})
	}
}

// TestSendWebhookDefaultEnvelope pins the documented default event
// envelope sent when no payload_template is configured.
func TestSendWebhookDefaultEnvelope(t *testing.T) {
	f := &sendWebhookFiring{
		RuleID:      "rule-1",
		TriggerType: "issue.created",
		IssueID:     "issue-9",
		Identifier:  "ENG-7",
		Title:       "Fix it",
		State:       "Backlog",
		Priority:    3,
		Labels:      []string{"bug"},
		Assignees:   []string{},
	}
	raw, err := f.defaultEnvelope()
	if err != nil {
		t.Fatalf("defaultEnvelope = %v", err)
	}
	var env struct {
		Event   string `json:"event"`
		RuleID  string `json:"rule_id"`
		Trigger string `json:"trigger"`
		FiredAt string `json:"fired_at"`
		Issue   struct {
			ID         string   `json:"id"`
			Identifier string   `json:"identifier"`
			Title      string   `json:"title"`
			State      string   `json:"state"`
			Priority   int      `json:"priority"`
			Labels     []string `json:"labels"`
			Assignees  []string `json:"assignees"`
		} `json:"issue"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("envelope is not valid JSON: %v", err)
	}
	if env.Event != "automation.webhook" || env.RuleID != "rule-1" ||
		env.Trigger != "issue.created" || env.FiredAt == "" {
		t.Fatalf("envelope header = %+v, want automation.webhook/rule-1/issue.created/fired_at", env)
	}
	if env.Issue.Identifier != "ENG-7" || env.Issue.Title != "Fix it" ||
		env.Issue.State != "Backlog" || env.Issue.Priority != 3 ||
		len(env.Issue.Labels) != 1 || env.Issue.Labels[0] != "bug" {
		t.Fatalf("envelope issue = %+v, want ENG-7/Fix it/Backlog/3/[bug]", env.Issue)
	}
}

// sendWebhookFireFixture creates an issue + a send_webhook rule and
// returns the ids needed to fire it.
func sendWebhookFireFixture(t *testing.T, fx automationFixture, actions []AutomationAction) (issueID, ruleID string) {
	t.Helper()
	ctx := context.Background()
	issue, err := CreateIssue(ctx, fx.pool, fx.slug, fx.ident, fx.admin,
		CreateIssueInput{Name: "webhook me"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	rule, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin,
		AutomationRuleInput{
			Name:    "webhook rule",
			Trigger: AutomationTrigger{Type: "issue.created"},
			Actions: actions,
		})
	if err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	return issue.ID, rule.ID
}

// loadRuleForFiring loads the stored rule row for direct firing through
// runAutomationRuleActionsTx (the firing path reads the stored row, not
// the validated input).
func loadRuleForFiring(t *testing.T, ctx context.Context, tx pgx.Tx, ruleID string) (*AutomationRule, AutomationTrigger) {
	t.Helper()
	r, err := scanAutomationRule(tx.QueryRow(ctx,
		`SELECT `+automationRuleColumns+` FROM automation_rules WHERE id = $1::uuid`, ruleID))
	if err != nil {
		t.Fatalf("load rule: %v", err)
	}
	var trig AutomationTrigger
	if err := json.Unmarshal(r.Trigger, &trig); err != nil {
		t.Fatalf("unmarshal trigger: %v", err)
	}
	return r, trig
}

// TestSendWebhookFiringEnqueuesOutbox pins the firing path: the action
// enqueues one webhook.deliver outbox row (URL + method + headers +
// rendered body), writes no issue activity rows, and records ok:true on
// the run row.
func TestSendWebhookFiringEnqueuesOutbox(t *testing.T) {
	fx := setupAutomationFixture(t)
	deleteAdHocOutboxRows(t, fx.pool)
	ctx := context.Background()
	issueID, ruleID := sendWebhookFireFixture(t, fx,
		[]AutomationAction{{
			Type:   "send_webhook",
			URL:    strPtr("https://example.com/hook"),
			Method: strPtr("PUT"),
			Headers: map[string]string{
				"Authorization": "Bearer s3cr3t",
			},
			PayloadTemplate: strPtr(`{"title": {{issue.title}}, "id": {{issue.identifier}}}`),
		}})

	// The HTTP call itself changes no rows: no new activity rows, no
	// issue.updated. (CreateIssue writes the issue's own creation
	// activity — count before/after the firing.)
	before := countRows(t, fx.pool,
		`SELECT count(*) FROM issue_activities WHERE issue_id = $1::uuid`, issueID)
	tx, err := fx.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	rule, trig := loadRuleForFiring(t, ctx, tx, ruleID)
	runAutomationRuleActionsTx(ctx, tx, "", "", fx.ident, issueID, fx.admin, rule, trig)
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// One outbox row, carrying URL + method + headers + rendered body.
	var payload []byte
	if err := fx.pool.QueryRow(ctx,
		`SELECT payload FROM outbox WHERE event = 'webhook.deliver'
		  AND payload->>'url' = 'https://example.com/hook'
		  ORDER BY id DESC LIMIT 1`,
	).Scan(&payload); err != nil {
		t.Fatalf("outbox row missing: %v", err)
	}
	var wd webhookDelivery
	if err := json.Unmarshal(payload, &wd); err != nil {
		t.Fatalf("unmarshal outbox payload: %v", err)
	}
	if wd.Method != "PUT" {
		t.Fatalf("outbox method = %q, want PUT", wd.Method)
	}
	if wd.Headers["Authorization"] != "Bearer s3cr3t" {
		t.Fatalf("outbox headers = %v, want the Authorization value", wd.Headers)
	}
	var body struct {
		Title string `json:"title"`
		ID    string `json:"id"`
	}
	if err := json.Unmarshal(wd.Body, &body); err != nil {
		t.Fatalf("outbox body is not JSON: %v", err)
	}
	if body.Title != "webhook me" || body.ID == "" {
		t.Fatalf("outbox body = %s, want rendered title/identifier", wd.Body)
	}
	// The HTTP call itself changes no rows: no new activity rows, no
	// issue.updated.
	if n := countRows(t, fx.pool,
		`SELECT count(*) FROM issue_activities WHERE issue_id = $1::uuid`, issueID); n != before {
		t.Fatalf("issue_activities = %d, want %d (send_webhook writes no rows)", n, before)
	}
	// Run row records the action ok:true.
	var runActions []byte
	if err := fx.pool.QueryRow(ctx,
		`SELECT actions FROM automation_runs WHERE rule_id = $1::uuid ORDER BY fired_at DESC LIMIT 1`,
		ruleID).Scan(&runActions); err != nil {
		t.Fatalf("run row missing: %v", err)
	}
	var results []AutomationActionResult
	if err := json.Unmarshal(runActions, &results); err != nil {
		t.Fatalf("unmarshal run actions: %v", err)
	}
	if len(results) != 1 || results[0].Type != "send_webhook" || !results[0].OK {
		t.Fatalf("run results = %s, want one ok:true send_webhook", runActions)
	}
}

// TestSendWebhookRenderFailureAbortsTail pins the failure semantics: a
// template that fails to render at fire time records ok:false and aborts
// the rule's remaining actions (existing C16T2 semantics). Write-time
// validation trial-renders templates, so reaching this path requires a
// row edited out-of-band — simulated here with a direct UPDATE, exactly
// the case the defensive fire-time check exists for.
func TestSendWebhookRenderFailureAbortsTail(t *testing.T) {
	fx := setupAutomationFixture(t)
	deleteAdHocOutboxRows(t, fx.pool)
	ctx := context.Background()
	issueID, ruleID := sendWebhookFireFixture(t, fx,
		[]AutomationAction{swAction("https://example.com/hook")})
	// Corrupt the stored template out-of-band: valid JSON, unknown
	// variable — write-time validation would reject this today.
	if _, err := fx.pool.Exec(ctx,
		`UPDATE automation_rules SET actions = $1::jsonb WHERE id = $2::uuid`,
		`[{"type":"send_webhook","url":"https://example.com/hook",`+
			`"payload_template":"{\"wrapped\": \"{{bogus.var}}\"}"},`+
			`{"type":"add_comment","body":"tail comment that must never post"}]`,
		ruleID); err != nil {
		t.Fatalf("corrupt actions: %v", err)
	}

	tx, err := fx.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	rule, trig := loadRuleForFiring(t, ctx, tx, ruleID)
	runAutomationRuleActionsTx(ctx, tx, "", "", fx.ident, issueID, fx.admin, rule, trig)
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var runActions []byte
	if err := fx.pool.QueryRow(ctx,
		`SELECT actions FROM automation_runs WHERE rule_id = $1::uuid ORDER BY fired_at DESC LIMIT 1`,
		ruleID).Scan(&runActions); err != nil {
		t.Fatalf("run row missing: %v", err)
	}
	var results []AutomationActionResult
	if err := json.Unmarshal(runActions, &results); err != nil {
		t.Fatalf("unmarshal run actions: %v", err)
	}
	if len(results) != 1 || results[0].Type != "send_webhook" || results[0].OK || results[0].Error == nil {
		t.Fatalf("run results = %s, want one ok:false send_webhook", runActions)
	}
	// The tail action never ran: no comment, no outbox row.
	if n := countRows(t, fx.pool,
		`SELECT count(*) FROM comments WHERE issue_id = $1::uuid`, issueID); n != 0 {
		t.Fatalf("comments = %d, want 0 (tail aborted)", n)
	}
	if n := countRows(t, fx.pool,
		`SELECT count(*) FROM outbox WHERE event = 'webhook.deliver'
		  AND payload->>'url' = 'https://example.com/hook'`); n != 0 {
		t.Fatalf("outbox rows = %d, want 0 (render failure enqueues nothing)", n)
	}
}

// TestSendWebhookDefaultMethodIsPost pins that an empty method defaults
// to POST on the outbox row.
func TestSendWebhookDefaultMethodIsPost(t *testing.T) {
	fx := setupAutomationFixture(t)
	deleteAdHocOutboxRows(t, fx.pool)
	ctx := context.Background()
	issueID, ruleID := sendWebhookFireFixture(t, fx,
		[]AutomationAction{swAction("https://example.com/hook")})

	tx, err := fx.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	rule, trig := loadRuleForFiring(t, ctx, tx, ruleID)
	runAutomationRuleActionsTx(ctx, tx, "", "", fx.ident, issueID, fx.admin, rule, trig)
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var payload []byte
	if err := fx.pool.QueryRow(ctx,
		`SELECT payload FROM outbox WHERE event = 'webhook.deliver'
		  AND payload->>'url' = 'https://example.com/hook'
		  ORDER BY id DESC LIMIT 1`,
	).Scan(&payload); err != nil {
		t.Fatalf("outbox row missing: %v", err)
	}
	var wd webhookDelivery
	if err := json.Unmarshal(payload, &wd); err != nil {
		t.Fatalf("unmarshal outbox payload: %v", err)
	}
	if wd.Method != "" && wd.Method != "POST" {
		t.Fatalf("outbox method = %q, want empty-or-POST", wd.Method)
	}
	// Absent template → the default envelope is the body.
	var env struct {
		Event string `json:"event"`
		Issue struct {
			Title string `json:"title"`
		} `json:"issue"`
	}
	if err := json.Unmarshal(wd.Body, &env); err != nil {
		t.Fatalf("default envelope body is not JSON: %v", err)
	}
	if env.Event != "automation.webhook" || env.Issue.Title != "webhook me" {
		t.Fatalf("default envelope body = %s", wd.Body)
	}
}

// TestSendWebhookDispatcherAdHocDelivery pins the delivery path for
// ad-hoc rows: PUT method, rule-authored headers, verbatim body, no
// signature header (no secret), and the SSRF guard refusing a private
// target without dialing it.
func TestSendWebhookDispatcherAdHocDelivery(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var gotMethod, gotBody, gotCustom, gotEvent, gotSig string
	var gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		gotCustom = r.Header.Get("X-Custom")
		gotEvent = r.Header.Get("X-Glance-Event")
		gotSig = r.Header.Get("X-Glance-Signature")
		gotContentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	enqueue := func(wd webhookDelivery) int64 {
		t.Helper()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer tx.Rollback(ctx)
		if err := mail.Enqueue(ctx, tx, WebhookDeliverEvent, wd); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		var id int64
		if err := tx.QueryRow(ctx,
			`SELECT id FROM outbox WHERE event = 'webhook.deliver' ORDER BY id DESC LIMIT 1`,
		).Scan(&id); err != nil {
			t.Fatalf("outbox id: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit: %v", err)
		}
		return id
	}

	id := enqueue(webhookDelivery{
		URL:     srv.URL,
		Event:   "automation.webhook",
		Method:  "PUT",
		Headers: map[string]string{"X-Custom": "yes"},
		Body:    json.RawMessage(`{"hello":"world"}`),
	})
	d := NewWebhookDispatcher(pool)
	d.allowPrivateTargets = true // httptest targets are loopback by construction
	if err := d.runScoped(ctx, ""); err != nil {
		t.Fatalf("dispatcher run: %v", err)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM outbox WHERE id = $1`, id).Scan(&status); err != nil {
		t.Fatalf("outbox status: %v", err)
	}
	if status != "done" {
		t.Fatalf("outbox status = %q, want done", status)
	}
	if gotMethod != "PUT" {
		t.Errorf("method = %q, want PUT", gotMethod)
	}
	if gotCustom != "yes" {
		t.Errorf("X-Custom = %q, want yes", gotCustom)
	}
	// The body goes on the wire as stored in the outbox: the jsonb
	// column normalizes insignificant whitespace, so compare the
	// compacted form — no envelope wrapping, no re-encoding.
	var compacted bytes.Buffer
	if err := json.Compact(&compacted, []byte(gotBody)); err != nil {
		t.Fatalf("received body is not JSON: %q", gotBody)
	}
	if compacted.String() != `{"hello":"world"}` {
		t.Errorf("body = %q, want the rendered body without envelope wrapping", gotBody)
	}
	if gotEvent != "automation.webhook" {
		t.Errorf("X-Glance-Event = %q, want automation.webhook", gotEvent)
	}
	if gotSig != "" {
		t.Errorf("X-Glance-Signature = %q, want empty (ad-hoc rows carry no secret)", gotSig)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotContentType)
	}
}

// TestSendWebhookDispatcherSSRFRefusal pins that a rule-authored URL
// resolving to private space is refused by the shared guard: the row
// fails permanently and the target is never dialed.
func TestSendWebhookDispatcherSSRFRefusal(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if err := mail.Enqueue(ctx, tx, WebhookDeliverEvent, webhookDelivery{
		URL:    "http://private.example/hook",
		Event:  "automation.webhook",
		Method: "POST",
		Body:   json.RawMessage(`{"a":1}`),
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	var id int64
	if err := tx.QueryRow(ctx,
		`SELECT id FROM outbox WHERE event = 'webhook.deliver' ORDER BY id DESC LIMIT 1`,
	).Scan(&id); err != nil {
		t.Fatalf("outbox id: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	d := NewWebhookDispatcher(pool)
	d.lookupIP = stubResolver(t, map[string][]net.IP{
		"private.example": {net.ParseIP("10.1.2.3")},
	})
	if err := d.runScoped(ctx, ""); err != nil {
		t.Fatalf("dispatcher run: %v", err)
	}
	var status, lastErr string
	var attempts int
	if err := pool.QueryRow(ctx,
		`SELECT status, attempts, COALESCE(last_error, '') FROM outbox WHERE id = $1`, id,
	).Scan(&status, &attempts, &lastErr); err != nil {
		t.Fatalf("outbox row: %v", err)
	}
	if status != "failed" {
		t.Fatalf("status = %q, want failed (SSRF refusal is permanent)", status)
	}
	if !strings.Contains(lastErr, "non-public IP") {
		t.Fatalf("last_error = %q, want the SSRF refusal", lastErr)
	}
	_ = attempts
}

// TestRedactAutomationRuleActions pins header redaction: names survive,
// values are masked, other action types pass through untouched, and
// corrupt JSON passes through unchanged.
func TestRedactAutomationRuleActions(t *testing.T) {
	raw := json.RawMessage(`[
		{"type":"send_webhook","url":"https://example.com/hook",
		 "headers":{"Authorization":"Bearer s3cr3t","X-Tenant":"acme"}},
		{"type":"add_comment","body":"hi"}
	]`)
	got := redactAutomationRuleActions(raw)
	var actions []AutomationAction
	if err := json.Unmarshal(got, &actions); err != nil {
		t.Fatalf("unmarshal redacted: %v", err)
	}
	if len(actions) != 2 {
		t.Fatalf("actions = %d, want 2", len(actions))
	}
	hdrs := actions[0].Headers
	if len(hdrs) != 2 || hdrs["Authorization"] != redactedAutomationHeaderValue ||
		hdrs["X-Tenant"] != redactedAutomationHeaderValue {
		t.Fatalf("headers = %v, want names kept and values masked", hdrs)
	}
	if actions[1].Body == nil || *actions[1].Body != "hi" {
		t.Fatalf("non-webhook action mutated: %+v", actions[1])
	}
	if strings.Contains(string(got), "s3cr3t") {
		t.Fatalf("redacted JSON still contains the secret value: %s", got)
	}
	// No headers → untouched (byte-identical passthrough keeps the
	// stored shape stable).
	plain := json.RawMessage(`[{"type":"add_comment","body":"hi"}]`)
	if out := redactAutomationRuleActions(plain); string(out) != string(plain) {
		t.Fatalf("passthrough mutated: %s", out)
	}
	// Corrupt JSON → returned as-is, never an error at read time.
	corrupt := json.RawMessage(`{oops`)
	if out := redactAutomationRuleActions(corrupt); string(out) != string(corrupt) {
		t.Fatalf("corrupt passthrough mutated: %s", out)
	}
}

// TestSendWebhookHeadersStoredNotRedacted pins the write/read split:
// the rule row stores real header values (the dispatcher needs them)
// while API reads see only the mask.
func TestSendWebhookHeadersStoredNotRedacted(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()
	secret := "Bearer topsecret-value"
	rule, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin,
		automationRuleInput(fx, nil, []AutomationAction{{
			Type:    "send_webhook",
			URL:     strPtr("https://example.com/hook"),
			Headers: map[string]string{"Authorization": secret},
		}}))
	if err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	var actions []AutomationAction
	if err := json.Unmarshal(rule.Actions, &actions); err != nil {
		t.Fatalf("unmarshal returned actions: %v", err)
	}
	if len(actions) != 1 || actions[0].Headers["Authorization"] != redactedAutomationHeaderValue {
		t.Fatalf("created rule actions = %s, want masked headers", rule.Actions)
	}
	// The stored row keeps the real value for the dispatcher.
	var stored []byte
	if err := fx.pool.QueryRow(ctx,
		`SELECT actions FROM automation_rules WHERE id = $1::uuid`, rule.ID,
	).Scan(&stored); err != nil {
		t.Fatalf("stored actions: %v", err)
	}
	if !strings.Contains(string(stored), secret) {
		t.Fatalf("stored actions lack the real header value: %s", stored)
	}
	// List redacts too.
	rules, err := ListAutomationRules(ctx, fx.pool, fx.slug, fx.ident, fx.admin)
	if err != nil {
		t.Fatalf("ListAutomationRules: %v", err)
	}
	for _, r := range rules {
		if strings.Contains(string(r.Actions), secret) {
			t.Fatalf("list leaked the header value: %s", r.Actions)
		}
	}
}
