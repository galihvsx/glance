package service

// Workflow automations v1 (C11T1): per-project rules that react to issue
// state changes. Plane paywalls this class of feature; glance ships it
// free.
//
// A rule fires when an issue's state changes and the transition matches
// the trigger filter. Actions run in order inside the state-change
// transaction; an action failure is LOGGED (slog) and never blocks the
// state change. Automation-driven changes never re-trigger automations:
// the depth-1 loop guard (automationActiveKey) suppresses evaluation
// while automation actions run, so a rule's own add_comment can never
// cascade. The guard is belt-and-braces — actions are direct tx-scoped
// SQL that never calls updateIssueTx — but it is enforced explicitly so
// a future action that routes through UpdateIssue stays safe.
//
// Auth convention (contract-first, documented choice): reads need member
// (15)+ like every other project read; writes need member (15)+,
// mirroring UpdateProject — the project-settings write convention.
// The plan's "maintainer/admin (role >= 10?)" hint maps to exactly this:
// this codebase has no maintainer role (roles are 5/15/20, spaced so a
// future intermediate slots in), and automation actions only touch issue
// fields a member can already change by hand, so the admin-only
// webhook/Slack convention (role == 20) would over-gate it. Guests (5)
// get ErrForbidden on both paths.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Automation scope guardrails (documented, enforced in service).
const (
	// MaxAutomationRulesPerProject caps rules per project; the 26th
	// create is rejected with ErrAutomationRuleLimit (409).
	MaxAutomationRulesPerProject = 25
	// MaxAutomationActionsPerRule caps actions on one rule.
	MaxAutomationActionsPerRule = 10
	// MaxAutomationNameLen caps the rule name (TEXT column, honest 400).
	MaxAutomationNameLen = 120
	// MaxAutomationCommentLen caps an add_comment body (TEXT-ish JSONB).
	MaxAutomationCommentLen = 10000
)

var (
	ErrAutomationRuleNotFound   = errors.New("service: automation rule not found")
	ErrAutomationRuleLimit      = errors.New("service: automation rule limit reached")
	ErrInvalidAutomationTrigger = errors.New("service: invalid automation trigger")
	ErrInvalidAutomationAction  = errors.New("service: invalid automation action")
)

// AutomationTrigger is the v1 trigger: issue.state_changed with optional
// from/to state filters. A null/empty filter matches any state.
type AutomationTrigger struct {
	Type       string   `json:"type"`
	FromStates []string `json:"from_states"`
	ToStates   []string `json:"to_states"`
}

// AutomationAction is one ordered step: exactly one of assign (user_id),
// add_label (label_id), add_comment (body).
type AutomationAction struct {
	Type    string  `json:"type"`
	UserID  *string `json:"user_id,omitempty"`
	LabelID *string `json:"label_id,omitempty"`
	Body    *string `json:"body,omitempty"`
}

// AutomationRule is a stored rule row.
type AutomationRule struct {
	ID        string          `json:"id"`
	ProjectID string          `json:"project_id"`
	Name      string          `json:"name"`
	Trigger   json.RawMessage `json:"trigger"`
	Actions   json.RawMessage `json:"actions"`
	Enabled   bool            `json:"enabled"`
	CreatedBy string          `json:"created_by"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// AutomationRuleInput is the create payload (all fields required).
type AutomationRuleInput struct {
	Name    string
	Trigger AutomationTrigger
	Actions []AutomationAction
}

// AutomationRulePatch is the PATCH payload: nil fields are untouched.
type AutomationRulePatch struct {
	Name    *string
	Enabled *bool
	Trigger *AutomationTrigger
	Actions *[]AutomationAction
}

const automationRuleColumns = `id::text, project_id::text, name, trigger, actions,
	enabled, created_by::text, created_at, updated_at`

func scanAutomationRule(row pgx.Row) (*AutomationRule, error) {
	var r AutomationRule
	if err := row.Scan(
		&r.ID, &r.ProjectID, &r.Name, &r.Trigger, &r.Actions,
		&r.Enabled, &r.CreatedBy, &r.CreatedAt, &r.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &r, nil
}

// resolveAutomationProject resolves wsSlug + identifier to (wsID,
// projectID, role), mirroring ListStates' resolution. Non-members get
// ErrNotFound; a missing project is ErrProjectNotFound.
func resolveAutomationProject(ctx context.Context, q queryRower, wsSlug, identifier, actorID string) (wsID, projectID string, role int, err error) {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return "", "", 0, err
	}
	wsID, role, err = workspaceIDForActor(q.QueryRow(ctx,
		`SELECT w.id::text, m.role
		 FROM workspaces w
		 JOIN workspace_members m ON m.workspace_id = w.id
		 WHERE w.slug = $1 AND m.user_id = $2::uuid`,
		wsSlug, actorID))
	if err != nil {
		return "", "", 0, err
	}
	if err := q.QueryRow(ctx,
		`SELECT p.id::text FROM projects p
		 WHERE p.workspace_id = $1::uuid AND p.identifier = $2`,
		wsID, ident).Scan(&projectID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", 0, ErrProjectNotFound
		}
		return "", "", 0, err
	}
	return wsID, projectID, role, nil
}

// validateAutomationTrigger checks the v1 trigger shape: type must be
// issue.state_changed and every listed state must be a UUID of a state
// in this project (reuses checkStateInProject; its ErrInvalidState is
// mapped to ErrInvalidAutomationTrigger so callers see one sentinel).
func validateAutomationTrigger(ctx context.Context, q queryRower, projectID string, t AutomationTrigger) error {
	if t.Type != "issue.state_changed" {
		return ErrInvalidAutomationTrigger
	}
	for _, sid := range append(append([]string{}, t.FromStates...), t.ToStates...) {
		if _, err := checkStateInProject(ctx, q, projectID, sid); err != nil {
			return ErrInvalidAutomationTrigger
		}
	}
	return nil
}

// validateAutomationActions checks each action's shape and referential
// targets at write time: assign needs a workspace member, add_label a
// label in this workspace, add_comment a non-empty body. Targets can
// still vanish later — execution failures are logged, never fatal.
func validateAutomationActions(ctx context.Context, q queryRower, wsID string, actions []AutomationAction) error {
	if len(actions) == 0 || len(actions) > MaxAutomationActionsPerRule {
		return ErrInvalidAutomationAction
	}
	for _, a := range actions {
		switch a.Type {
		case "assign":
			if a.UserID == nil {
				return ErrInvalidAutomationAction
			}
			uid := strings.ToLower(strings.TrimSpace(*a.UserID))
			var one int
			err := q.QueryRow(ctx,
				`SELECT 1 FROM workspace_members WHERE workspace_id = $1::uuid AND user_id = $2::uuid`,
				wsID, uid).Scan(&one)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
					return ErrInvalidAutomationAction
				}
				return err
			}
		case "add_label":
			if a.LabelID == nil {
				return ErrInvalidAutomationAction
			}
			lid := strings.ToLower(strings.TrimSpace(*a.LabelID))
			var owner string
			err := q.QueryRow(ctx,
				`SELECT workspace_id::text FROM labels WHERE id = $1::uuid`, lid).Scan(&owner)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
					return ErrInvalidAutomationAction
				}
				return err
			}
			if owner != wsID {
				return ErrInvalidAutomationAction
			}
		case "add_comment":
			if a.Body == nil || strings.TrimSpace(*a.Body) == "" ||
				len(*a.Body) > MaxAutomationCommentLen {
				return ErrInvalidAutomationAction
			}
		default:
			return ErrInvalidAutomationAction
		}
	}
	return nil
}

func validateAutomationName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrNameRequired
	}
	if len(name) > MaxAutomationNameLen {
		return ErrNameRequired
	}
	return nil
}

// ListAutomationRules returns the project's rules, oldest first. Member
// (15)+; guests get ErrForbidden via the shared resolve pattern.
func ListAutomationRules(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string) ([]*AutomationRule, error) {
	_, projectID, role, err := resolveAutomationProject(ctx, pool, wsSlug, identifier, actorID)
	if err != nil {
		return nil, err
	}
	if role < RoleMember {
		return nil, ErrForbidden
	}
	rows, err := pool.Query(ctx,
		`SELECT `+automationRuleColumns+` FROM automation_rules
		 WHERE project_id = $1::uuid ORDER BY created_at, id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*AutomationRule{}
	for rows.Next() {
		r, err := scanAutomationRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// CreateAutomationRule inserts a rule. Write guard mirrors UpdateProject
// (member (15)+ — see the package doc for the documented choice). The
// 26th rule per project is rejected with ErrAutomationRuleLimit.
func CreateAutomationRule(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string, in AutomationRuleInput) (*AutomationRule, error) {
	if err := validateAutomationName(in.Name); err != nil {
		return nil, err
	}
	wsID, projectID, role, err := resolveAutomationProject(ctx, pool, wsSlug, identifier, actorID)
	if err != nil {
		return nil, err
	}
	if role < RoleMember {
		return nil, ErrForbidden
	}
	if err := validateAutomationTrigger(ctx, pool, projectID, in.Trigger); err != nil {
		return nil, err
	}
	if err := validateAutomationActions(ctx, pool, wsID, in.Actions); err != nil {
		return nil, err
	}

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM automation_rules WHERE project_id = $1::uuid`, projectID).Scan(&count); err != nil {
		return nil, err
	}
	if count >= MaxAutomationRulesPerProject {
		return nil, ErrAutomationRuleLimit
	}

	triggerJSON, err := json.Marshal(in.Trigger)
	if err != nil {
		return nil, err
	}
	actionsJSON, err := json.Marshal(in.Actions)
	if err != nil {
		return nil, err
	}
	r, err := scanAutomationRule(pool.QueryRow(ctx,
		`INSERT INTO automation_rules (project_id, name, trigger, actions, created_by)
		 VALUES ($1::uuid, $2, $3::jsonb, $4::jsonb, $5::uuid)
		 RETURNING `+automationRuleColumns,
		projectID, strings.TrimSpace(in.Name), string(triggerJSON), string(actionsJSON), actorID))
	if err != nil {
		return nil, err
	}
	return r, nil
}

// UpdateAutomationRule patches a rule (name/enabled/trigger/actions).
// Same member (15)+ write guard as create.
func UpdateAutomationRule(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, ruleID, actorID string, patch AutomationRulePatch) (*AutomationRule, error) {
	wsID, projectID, role, err := resolveAutomationProject(ctx, pool, wsSlug, identifier, actorID)
	if err != nil {
		return nil, err
	}
	if role < RoleMember {
		return nil, ErrForbidden
	}
	if patch.Trigger != nil {
		if err := validateAutomationTrigger(ctx, pool, projectID, *patch.Trigger); err != nil {
			return nil, err
		}
	}
	if patch.Actions != nil {
		if err := validateAutomationActions(ctx, pool, wsID, *patch.Actions); err != nil {
			return nil, err
		}
	}

	var sets []string
	var args []any
	add := func(column, cast string, arg any) {
		args = append(args, arg)
		sets = append(sets, fmt.Sprintf("%s = $%d%s", column, len(args)+2, cast))
	}
	if patch.Name != nil {
		if err := validateAutomationName(*patch.Name); err != nil {
			return nil, err
		}
		add("name", "", strings.TrimSpace(*patch.Name))
	}
	if patch.Enabled != nil {
		add("enabled", "", *patch.Enabled)
	}
	if patch.Trigger != nil {
		triggerJSON, err := json.Marshal(*patch.Trigger)
		if err != nil {
			return nil, err
		}
		add("trigger", "::jsonb", string(triggerJSON))
	}
	if patch.Actions != nil {
		actionsJSON, err := json.Marshal(*patch.Actions)
		if err != nil {
			return nil, err
		}
		add("actions", "::jsonb", string(actionsJSON))
	}
	if len(sets) == 0 {
		return nil, ErrNothingToUpdate
	}
	// args[0]/args[1] are ruleID/projectID in the WHERE clause, hence
	// the +2 placeholder offset (same trick as UpdateIssue).
	r, err := scanAutomationRule(pool.QueryRow(ctx,
		`UPDATE automation_rules SET `+strings.Join(sets, ", ")+`, updated_at = now()
		 WHERE id = $1::uuid AND project_id = $2::uuid
		 RETURNING `+automationRuleColumns,
		append([]any{ruleID, projectID}, args...)...))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return nil, ErrAutomationRuleNotFound
		}
		return nil, err
	}
	return r, nil
}

// DeleteAutomationRule removes a rule. Same member (15)+ write guard.
func DeleteAutomationRule(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, ruleID, actorID string) error {
	_, projectID, role, err := resolveAutomationProject(ctx, pool, wsSlug, identifier, actorID)
	if err != nil {
		return err
	}
	if role < RoleMember {
		return ErrForbidden
	}
	tag, err := pool.Exec(ctx,
		`DELETE FROM automation_rules WHERE id = $1::uuid AND project_id = $2::uuid`,
		ruleID, projectID)
	if err != nil {
		if isInvalidUUID(err) {
			return ErrAutomationRuleNotFound
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrAutomationRuleNotFound
	}
	return nil
}

// matches reports whether the transition fromStateID -> toStateID fires
// this trigger. A null/empty filter (nil from JSON null) matches any.
func (t AutomationTrigger) matches(fromStateID, toStateID string) bool {
	if len(t.FromStates) > 0 && !slices.Contains(t.FromStates, fromStateID) {
		return false
	}
	if len(t.ToStates) > 0 && !slices.Contains(t.ToStates, toStateID) {
		return false
	}
	return true
}

// automationActiveKey is the depth-1 loop guard: while automation
// actions execute, state-change evaluation is suppressed so an
// automation's own writes (notably add_comment) can never cascade into
// another automation pass.
type automationActiveKey struct{}

func automationActive(ctx context.Context) bool {
	v, _ := ctx.Value(automationActiveKey{}).(bool)
	return v
}

// runAutomationRulesTx evaluates enabled rules for the state transition
// and runs matching actions inside the caller's tx. Call it only from
// updateIssueTx's state-changed branch (guarded by !automationActive).
// It never returns an error: rule-selection failures and per-action
// failures are logged via slog and the state change proceeds — a broken
// rule must not roll back the user's move. Every firing writes exactly
// one automation_runs row in this same tx (C12T1 run history), recording
// the per-action ok/error outcomes. Returned notifications are
// for the caller to announce after commit.
func runAutomationRulesTx(ctx context.Context, tx pgx.Tx, wsID, projectID, ident, issueID, actorID, fromStateID, toStateID string) []*Notification {
	if automationActive(ctx) {
		return nil
	}
	rows, err := tx.Query(ctx,
		`SELECT `+automationRuleColumns+` FROM automation_rules
		 WHERE project_id = $1::uuid AND enabled
		 ORDER BY created_at, id`, projectID)
	if err != nil {
		slog.Error("automation: rule selection failed; skipping automation pass",
			"project_id", projectID, "issue_id", issueID, "error", err)
		return nil
	}
	defer rows.Close()
	var rules []*AutomationRule
	for rows.Next() {
		r, err := scanAutomationRule(rows)
		if err != nil {
			rows.Close()
			slog.Error("automation: rule scan failed; skipping automation pass",
				"project_id", projectID, "issue_id", issueID, "error", err)
			return nil
		}
		rules = append(rules, r)
	}
	if err := rows.Err(); err != nil {
		slog.Error("automation: rule scan failed; skipping automation pass",
			"project_id", projectID, "issue_id", issueID, "error", err)
		return nil
	}

	actx := context.WithValue(ctx, automationActiveKey{}, true)
	var notified []*Notification
	for _, r := range rules {
		var trig AutomationTrigger
		if err := json.Unmarshal(r.Trigger, &trig); err != nil {
			slog.Warn("automation: skipping rule with corrupt trigger",
				"rule_id", r.ID, "error", err)
			continue
		}
		if !trig.matches(fromStateID, toStateID) {
			continue
		}
		var actions []AutomationAction
		if err := json.Unmarshal(r.Actions, &actions); err != nil {
			slog.Warn("automation: skipping rule with corrupt actions",
				"rule_id", r.ID, "error", err)
			continue
		}
		// C12T1: record each action's outcome for the run row written
		// after the loop — one row per firing, never per action.
		results := make([]AutomationActionResult, 0, len(actions))
		for i, a := range actions {
			ns, err := execAutomationActionTx(actx, tx, wsID, projectID, ident, issueID, actorID, a)
			if err != nil {
				// Logged, never fatal: the state change already
				// happened; a broken action must not roll it back.
				slog.Error("automation: action failed; continuing with next action",
					"rule_id", r.ID, "rule_name", r.Name,
					"action_index", i, "action_type", a.Type, "error", err)
				errText := err.Error()
				results = append(results, AutomationActionResult{Type: a.Type, OK: false, Error: &errText})
				continue
			}
			notified = append(notified, ns...)
			results = append(results, AutomationActionResult{Type: a.Type, OK: true})
		}
		// C12T1: one run row per firing, in this same tx — the state
		// change and its log commit atomically. A run-row insert
		// failure is logged, never fatal (same ethos as actions).
		recordAutomationRunTx(actx, tx, r.ID, issueID, trig.Type, results)
	}
	return notified
}

// execAutomationActionTx runs one action inside the state-change tx.
// Failures are returned for the caller to log; the tx is untouched.
func execAutomationActionTx(ctx context.Context, tx pgx.Tx, wsID, projectID, ident, issueID, actorID string, a AutomationAction) ([]*Notification, error) {
	switch a.Type {
	case "assign":
		if a.UserID == nil {
			return nil, ErrInvalidAutomationAction
		}
		return automationAssignTx(ctx, tx, wsID, projectID, ident, issueID, actorID,
			strings.ToLower(strings.TrimSpace(*a.UserID)))
	case "add_label":
		if a.LabelID == nil {
			return nil, ErrInvalidAutomationAction
		}
		return nil, automationAddLabelTx(ctx, tx, wsID, issueID, actorID,
			strings.ToLower(strings.TrimSpace(*a.LabelID)))
	case "add_comment":
		if a.Body == nil {
			return nil, ErrInvalidAutomationAction
		}
		return automationAddCommentTx(ctx, tx, wsID, projectID, ident, issueID, actorID, *a.Body)
	default:
		return nil, ErrInvalidAutomationAction
	}
}

// automationAssignTx adds the user as an assignee (idempotent — mirrors
// AssignAssignee's INSERT ... ON CONFLICT DO NOTHING, not a replace).
// The user must still be a workspace member; a member who left since
// the rule was created fails the action (logged), not the state change.
func automationAssignTx(ctx context.Context, tx pgx.Tx, wsID, projectID, ident, issueID, actorID, userID string) ([]*Notification, error) {
	var one int
	if err := tx.QueryRow(ctx,
		`SELECT 1 FROM workspace_members WHERE workspace_id = $1::uuid AND user_id = $2::uuid`,
		wsID, userID).Scan(&one); err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return nil, ErrAssigneeNotMember
		}
		return nil, err
	}
	tag, err := tx.Exec(ctx,
		`INSERT INTO issue_assignees (issue_id, user_id) VALUES ($1::uuid, $2::uuid)
		 ON CONFLICT DO NOTHING`, issueID, userID)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, nil
	}
	// New assignment: one "assignees" activity row + assignee
	// notification, mirroring AssignAssignee.
	var old []string
	rows, err := tx.Query(ctx,
		`SELECT user_id::text FROM issue_assignees WHERE issue_id = $1::uuid AND user_id <> $2::uuid ORDER BY user_id::text`,
		issueID, userID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		old = append(old, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	want := append(append([]string{}, old...), userID)
	oldArr := old
	if oldArr == nil {
		oldArr = []string{}
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO issue_activities (issue_id, actor_id, field, old_value, new_value)
		 VALUES ($1::uuid, $2::uuid, 'assignees', $3::jsonb, $4::jsonb)`,
		issueID, actorID, toJSONBParam(oldArr), toJSONBParam(want)); err != nil {
		return nil, err
	}
	displayID, name, err := issueNotifyContextTx(ctx, tx, ident, issueID)
	if err != nil {
		return nil, err
	}
	actorName := actorDisplayName(ctx, tx, actorID)
	notified, err := notifyTx(ctx, tx, NotifyIssueAssigned,
		fmt.Sprintf("%s assigned you to %s", actorName, displayID),
		fmt.Sprintf("Issue: %s", name),
		map[string]any{
			"issue_id":     issueID,
			"display_id":   displayID,
			"issue_name":   name,
			"actor_id":     actorID,
			"workspace_id": wsID,
			"project_id":   projectID,
		},
		actorID, []string{userID})
	if err != nil {
		return nil, err
	}
	return notified, nil
}

// automationAddLabelTx attaches one label (idempotent). The label must
// still belong to this workspace; a label deleted since rule creation
// fails the action (logged), not the state change.
func automationAddLabelTx(ctx context.Context, tx pgx.Tx, wsID, issueID, actorID, labelID string) error {
	var owner string
	if err := tx.QueryRow(ctx,
		`SELECT workspace_id::text FROM labels WHERE id = $1::uuid`, labelID).Scan(&owner); err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return ErrInvalidAutomationAction
		}
		return err
	}
	if owner != wsID {
		return ErrInvalidAutomationAction
	}
	var old []string
	rows, err := tx.Query(ctx,
		`SELECT label_id::text FROM issue_labels WHERE issue_id = $1::uuid ORDER BY label_id::text`, issueID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		old = append(old, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if slices.Contains(old, labelID) {
		return nil
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO issue_labels (issue_id, label_id) VALUES ($1::uuid, $2::uuid)
		 ON CONFLICT DO NOTHING`, issueID, labelID); err != nil {
		return err
	}
	oldArr := old
	if oldArr == nil {
		oldArr = []string{}
	}
	newSet := append(append([]string{}, old...), labelID)
	slices.Sort(newSet)
	if _, err := tx.Exec(ctx,
		`INSERT INTO issue_activities (issue_id, actor_id, field, old_value, new_value)
		 VALUES ($1::uuid, $2::uuid, 'labels', $3::jsonb, $4::jsonb)`,
		issueID, actorID, toJSONBParam(oldArr), toJSONBParam(newSet)); err != nil {
		return err
	}
	return nil
}

// automationCommentDoc builds the minimal TipTap doc the frontend's
// tiptapText() renders: one paragraph per line of body text.
func automationCommentDoc(body string) json.RawMessage {
	var b strings.Builder
	b.WriteString(`{"type":"doc","content":[`)
	for i, p := range strings.Split(body, "\n") {
		if i > 0 {
			b.WriteByte(',')
		}
		txt, _ := json.Marshal(p)
		b.WriteString(`{"type":"paragraph","content":[{"type":"text","text":` + string(txt) + `}]}`)
	}
	b.WriteString(`]}`)
	return json.RawMessage(b.String())
}

// automationAddCommentTx posts the rule's comment as the triggering
// actor (attribution matches a manual comment on the same move) and
// notifies watchers minus the actor, mirroring CreateComment's fan-out
// minus mentions (automation bodies are plain text, no @-mentions).
// The loop guard (automationActiveKey) guarantees this comment can
// never trigger another automation pass.
func automationAddCommentTx(ctx context.Context, tx pgx.Tx, wsID, projectID, ident, issueID, actorID, body string) ([]*Notification, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, ErrInvalidAutomationAction
	}
	content := automationCommentDoc(body)
	var id string
	if err := tx.QueryRow(ctx,
		`INSERT INTO comments (issue_id, actor_id, content)
		 VALUES ($1::uuid, $2::uuid, $3::jsonb)
		 RETURNING id::text`,
		issueID, actorID, string(content)).Scan(&id); err != nil {
		return nil, err
	}
	displayID, name, err := issueNotifyContextTx(ctx, tx, ident, issueID)
	if err != nil {
		return nil, err
	}
	watchers, err := issueWatchersTx(ctx, tx, issueID)
	if err != nil {
		return nil, err
	}
	actorName := actorDisplayName(ctx, tx, actorID)
	notified, err := notifyTx(ctx, tx, NotifyCommentCreated,
		fmt.Sprintf("%s commented on %s", actorName, displayID),
		fmt.Sprintf("Issue: %s", name),
		map[string]any{
			"comment_id": id,
			"issue_id":   issueID,
			"display_id": displayID,
			"issue_name": name,
			"actor_id":   actorID,
			"project_id": projectID,
		},
		actorID, watchers)
	if err != nil {
		return nil, err
	}
	if err := enqueueWebhookDeliveryTx(ctx, tx, wsID, EventCommentCreated, map[string]any{
		"id":         id,
		"issue_id":   issueID,
		"display_id": displayID,
		"actor_id":   actorID,
	}); err != nil {
		return nil, err
	}
	if err := enqueueSlackDeliveryTx(ctx, tx, wsID,
		slackCommentText(displayID, actorName, TipTapPlainText(content))); err != nil {
		return nil, err
	}
	return notified, nil
}
