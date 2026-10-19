package service

// send_webhook automation action (C77T0): automation rules can call an
// HTTP endpoint when they fire (Plane v3.0.0 parity).
//
// Config (validated at rule write time by validateSendWebhookAction):
//   - url (required): absolute http(s) URL, parsed + scheme-checked.
//   - method: POST (default) | PUT. Anything else is a 400.
//   - headers: optional string→string map. Transport-owned names
//     (host, content-length — case-insensitive) are rejected. VALUES ARE
//     SECRETS: they are redacted in every API read (redactAutomationRuleActions)
//     and never logged; the redaction placeholder itself is rejected at
//     write time so a masked round-trip can never store the mask as a
//     secret. PATCH replaces the headers map wholesale (write-only
//     values — to keep existing headers, re-send their values).
//   - payload_template: optional JSON string with {{variable}}
//     placeholders over the firing context. Placeholders substitute the
//     JSON encoding of the value — do NOT quote them: {"title":
//     {{issue.title}}} renders {"title": "My title"}. The template is
//     trial-rendered at write time (unknown variables, unbalanced
//     braces, and non-JSON results are a 400); a fire-time render
//     failure records the action ok:false and aborts the tail.
//     Absent/blank → the documented default envelope
//     {event:"automation.webhook", rule_id, trigger, fired_at,
//     issue:{id, identifier, title, state, priority, labels, assignees}}.
//
// Template vocabulary: issue.id (issue UUID), issue.identifier (display
// id, e.g. ENG-123), issue.title, issue.state (state name),
// issue.priority (0-4), issue.labels (label names), issue.assignees
// (assignee display names), rule.id (rule UUID), trigger (trigger type),
// fired_at (RFC3339 UTC).
//
// Delivery reuses the existing webhook outbox pipeline (webhook.go): the
// action enqueues a webhook.deliver row with an empty webhook_id (ad-hoc
// delivery — no registered webhook row, so no per-webhook status or
// delivery-history attribution; those writers no-op on the empty id).
// Retry/backoff/dead-letter behavior applies uniformly, and the shared
// SSRF guard (guardTarget: delivery-time DNS resolution, non-public IPs
// refused, plus the http(s)-only scheme check) vets the rule-authored
// URL — there is no parallel delivery path. The dispatcher sends the
// rendered body verbatim (template or default envelope), the configured
// method, and the rule-authored headers.
//
// Failure semantics follow the existing action contract: a template
// render failure (or an enqueue failure) records the action ok:false on
// the run row and ABORTS the rule's remaining actions — it never rolls
// back the firing event. A successful action only means the delivery was
// ENQUEUED; the HTTP outcome itself is async (outbox retry → failed is
// visible in the outbox row's last_error, not the run row).
//
// The HTTP call changes no rows: no activity rows, no issue.updated
// event (see the carried limit on automation-triggered changes).
//
// Safety caps reused, not reinvented: send_webhook counts toward
// MaxAutomationActionsPerRule (10); ad-hoc rows share the dispatcher's
// global fairness partition (payload->>'webhook_id' = ''), capped at
// webhookPerWebhookCap rows per pass like every other webhook.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"glance/internal/mail"
)

// AutomationActionSendWebhook is the send_webhook action type.
const AutomationActionSendWebhook = "send_webhook"

// redactedAutomationHeaderValue is the placeholder shown in API reads
// for send_webhook header values. It is rejected at write time so a
// masked round-trip can never store the mask as a real secret.
const redactedAutomationHeaderValue = "********"

// sendWebhookBlockedHeaders are transport-owned: the HTTP client owns
// them (Host via req.Host, Content-Length via the body length). Rejected
// case-insensitively at write time, skipped defensively at send time.
var sendWebhookBlockedHeaders = map[string]bool{
	"host":           true,
	"content-length": true,
}

// ErrSendWebhookTemplateRender marks a payload_template that failed to
// render at fire time. The run row records it ok:false (wrapped with the
// detail); the tail of the rule is aborted per the action contract.
var ErrSendWebhookTemplateRender = errors.New("service: send_webhook template render failed")

// validateSendWebhookAction checks a send_webhook action's config at
// rule-write time. Every violation is ErrInvalidAutomationAction (an
// honest 400 via the automation handler).
func validateSendWebhookAction(a AutomationAction) error {
	if a.URL == nil || strings.TrimSpace(*a.URL) == "" {
		return ErrInvalidAutomationAction
	}
	// validateWebhookURL (webhook.go) does the parse + absolute +
	// http(s) + host check; map its sentinel to the automation one so
	// the handler keeps its single 400 mapping.
	if err := validateWebhookURL(*a.URL); err != nil {
		return ErrInvalidAutomationAction
	}
	if a.Method != nil {
		switch strings.ToUpper(strings.TrimSpace(*a.Method)) {
		case "", "POST", "PUT":
		default:
			return ErrInvalidAutomationAction
		}
	}
	for k, v := range a.Headers {
		name := strings.ToLower(strings.TrimSpace(k))
		if name == "" || sendWebhookBlockedHeaders[name] {
			return ErrInvalidAutomationAction
		}
		if v == redactedAutomationHeaderValue {
			return ErrInvalidAutomationAction
		}
	}
	if a.PayloadTemplate != nil && strings.TrimSpace(*a.PayloadTemplate) != "" {
		// Trial-render against dummy values: this catches unknown
		// variables, unbalanced braces, and templates whose
		// placeholders can't produce valid JSON (e.g. a quoted
		// "{{issue.title}}" double-encodes) at write time with an
		// honest 400, instead of failing the firing later. The
		// fire-time render path stays as the defensive backstop for
		// rows edited out-of-band.
		if _, err := renderSendWebhookTemplate(
			strings.TrimSpace(*a.PayloadTemplate), dummySendWebhookVars(),
		); err != nil {
			return ErrInvalidAutomationAction
		}
	}
	return nil
}

// dummySendWebhookVars is the write-time trial-render context: one
// representative value per template variable. Real values are always
// JSON-encodable, so a template that renders to valid JSON here renders
// to valid JSON at fire time too.
func dummySendWebhookVars() map[string]any {
	return (&sendWebhookFiring{
		RuleID:      "00000000-0000-0000-0000-000000000000",
		TriggerType: "issue.created",
		IssueID:     "00000000-0000-0000-0000-000000000000",
		Identifier:  "ENG-1",
		Title:       "dummy",
		State:       "Backlog",
		Priority:    0,
		Labels:      []string{},
		Assignees:   []string{},
	}).vars()
}

// redactAutomationRuleActions masks send_webhook header values in rule
// action JSON for API reads: names survive, values become the redaction
// placeholder. Actions without headers pass through byte-identical (and
// corrupt JSON passes through unchanged — a read path never errors on
// stored data). The firing path reads the stored row directly and is
// unaffected.
func redactAutomationRuleActions(raw json.RawMessage) json.RawMessage {
	var actions []AutomationAction
	if err := json.Unmarshal(raw, &actions); err != nil {
		return raw
	}
	changed := false
	for i := range actions {
		if actions[i].Type != AutomationActionSendWebhook || len(actions[i].Headers) == 0 {
			continue
		}
		masked := make(map[string]string, len(actions[i].Headers))
		for k := range actions[i].Headers {
			masked[k] = redactedAutomationHeaderValue
		}
		actions[i].Headers = masked
		changed = true
	}
	if !changed {
		return raw
	}
	out, err := json.Marshal(actions)
	if err != nil {
		return raw
	}
	return out
}

// sendWebhookFiring is one send_webhook firing's template context.
type sendWebhookFiring struct {
	RuleID      string
	TriggerType string
	FiredAt     time.Time
	IssueID     string
	Identifier  string
	Title       string
	State       string
	Priority    int
	Labels      []string
	Assignees   []string
}

// vars flattens the firing context into the template vocabulary.
func (f *sendWebhookFiring) vars() map[string]any {
	return map[string]any{
		"issue.id":         f.IssueID,
		"issue.identifier": f.Identifier,
		"issue.title":      f.Title,
		"issue.state":      f.State,
		"issue.priority":   f.Priority,
		"issue.labels":     f.Labels,
		"issue.assignees":  f.Assignees,
		"rule.id":          f.RuleID,
		"trigger":          f.TriggerType,
		"fired_at":         f.FiredAt.UTC().Format(time.RFC3339),
	}
}

// defaultEnvelope renders the documented default event envelope.
func (f *sendWebhookFiring) defaultEnvelope() (json.RawMessage, error) {
	env := map[string]any{
		"event":    "automation.webhook",
		"rule_id":  f.RuleID,
		"trigger":  f.TriggerType,
		"fired_at": f.FiredAt.UTC().Format(time.RFC3339),
		"issue": map[string]any{
			"id":         f.IssueID,
			"identifier": f.Identifier,
			"title":      f.Title,
			"state":      f.State,
			"priority":   f.Priority,
			"labels":     f.Labels,
			"assignees":  f.Assignees,
		},
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

// renderSendWebhookTemplate substitutes {{variable}} placeholders with
// the JSON encoding of the context value (single pass over the template
// — values are never re-scanned). Unknown variables, unbalanced braces,
// and non-JSON render results are errors.
func renderSendWebhookTemplate(tpl string, vars map[string]any) (json.RawMessage, error) {
	var b strings.Builder
	rest := tpl
	for {
		open := strings.Index(rest, "{{")
		if open < 0 {
			b.WriteString(rest)
			break
		}
		b.WriteString(rest[:open])
		rest = rest[open+2:]
		close := strings.Index(rest, "}}")
		if close < 0 {
			return nil, fmt.Errorf("%w: unclosed placeholder", ErrSendWebhookTemplateRender)
		}
		name := strings.TrimSpace(rest[:close])
		v, ok := vars[name]
		if !ok {
			return nil, fmt.Errorf("%w: unknown variable %q", ErrSendWebhookTemplateRender, name)
		}
		enc, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrSendWebhookTemplateRender, err)
		}
		b.Write(enc)
		rest = rest[close+2:]
	}
	out := b.String()
	var js json.RawMessage
	if err := json.Unmarshal([]byte(out), &js); err != nil {
		return nil, fmt.Errorf("%w: rendered template is not valid JSON: %v", ErrSendWebhookTemplateRender, err)
	}
	return json.RawMessage([]byte(out)), nil
}

// sendWebhookFiringContextTx gathers the firing context for one issue:
// display id, title, state name, priority, label names, assignee display
// names. Labels/assignees default to empty (never null) so templates
// always see JSON arrays.
func sendWebhookFiringContextTx(ctx context.Context, tx pgx.Tx, ident, issueID, ruleID, triggerType string) (*sendWebhookFiring, error) {
	f := &sendWebhookFiring{
		RuleID:      ruleID,
		TriggerType: triggerType,
		FiredAt:     time.Now().UTC(),
		IssueID:     issueID,
		Labels:      []string{},
		Assignees:   []string{},
	}
	var seq int
	if err := tx.QueryRow(ctx,
		`SELECT i.sequence_id, i.name, i.priority, s.name
		   FROM issues i JOIN states s ON s.id = i.state_id
		  WHERE i.id = $1::uuid`,
		issueID).Scan(&seq, &f.Title, &f.Priority, &f.State); err != nil {
		return nil, err
	}
	f.Identifier = ident + "-" + strconv.Itoa(seq)

	rows, err := tx.Query(ctx,
		`SELECT l.name FROM labels l
		   JOIN issue_labels il ON il.label_id = l.id
		  WHERE il.issue_id = $1::uuid ORDER BY l.name`,
		issueID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return nil, err
		}
		f.Labels = append(f.Labels, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	arows, err := tx.Query(ctx,
		`SELECT user_id::text FROM issue_assignees WHERE issue_id = $1::uuid ORDER BY user_id::text`,
		issueID)
	if err != nil {
		return nil, err
	}
	for arows.Next() {
		var uid string
		if err := arows.Scan(&uid); err != nil {
			arows.Close()
			return nil, err
		}
		f.Assignees = append(f.Assignees, actorDisplayName(ctx, tx, uid))
	}
	arows.Close()
	if err := arows.Err(); err != nil {
		return nil, err
	}
	return f, nil
}

// automationSendWebhookTx executes one send_webhook action: render the
// body (template or default envelope) and enqueue it on the shared
// webhook outbox. It returns (nil, false, nil) on success — the action
// is never a no-op (an enqueue happened) and notifies nobody. A render
// or enqueue failure is returned for the caller to record ok:false and
// abort the tail; the firing tx is untouched.
func automationSendWebhookTx(ctx context.Context, tx pgx.Tx, ident, issueID string, a AutomationAction, ruleID, triggerType string) ([]*Notification, bool, error) {
	if a.URL == nil {
		// Unreachable after write-time validation; kept honest anyway.
		return nil, false, ErrInvalidAutomationAction
	}
	rawURL := strings.TrimSpace(*a.URL)
	if err := validateWebhookURL(rawURL); err != nil {
		return nil, false, ErrInvalidAutomationAction
	}
	method := "POST"
	if a.Method != nil && strings.TrimSpace(*a.Method) != "" {
		method = strings.ToUpper(strings.TrimSpace(*a.Method))
	}
	if method != "POST" && method != "PUT" {
		return nil, false, ErrInvalidAutomationAction
	}
	firing, err := sendWebhookFiringContextTx(ctx, tx, ident, issueID, ruleID, triggerType)
	if err != nil {
		return nil, false, err
	}
	var body json.RawMessage
	if a.PayloadTemplate != nil && strings.TrimSpace(*a.PayloadTemplate) != "" {
		body, err = renderSendWebhookTemplate(strings.TrimSpace(*a.PayloadTemplate), firing.vars())
		if err != nil {
			return nil, false, err
		}
	} else {
		body, err = firing.defaultEnvelope()
		if err != nil {
			return nil, false, err
		}
	}
	// Defensive re-check at fire time: write-time validation already
	// rejected transport-owned names, but a stored row predating the
	// check (or edited out-of-band) must never hand the transport a
	// Host/Content-Length override.
	headers := make(map[string]string, len(a.Headers))
	for k, v := range a.Headers {
		name := strings.TrimSpace(k)
		if name == "" || sendWebhookBlockedHeaders[strings.ToLower(name)] {
			continue
		}
		headers[name] = v
	}
	// Ad-hoc delivery: empty webhook_id (no registered webhook row —
	// the dispatcher skips the active lookup and the status/history
	// attribution no-ops on it), the rule-authored URL + method +
	// headers, and the fully rendered body sent verbatim. No secret: no
	// signature header.
	if err := mail.Enqueue(ctx, tx, WebhookDeliverEvent, webhookDelivery{
		WebhookID: "",
		URL:       rawURL,
		Secret:    "",
		Event:     "automation.webhook",
		Data:      nil,
		Method:    method,
		Headers:   headers,
		Body:      body,
	}); err != nil {
		return nil, false, err
	}
	return nil, false, nil
}
