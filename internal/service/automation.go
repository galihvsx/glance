package service

// Workflow automations (C11T1 v1, C12T2 expansion): per-project rules
// that react to issue events. Plane paywalls this class of feature;
// glance ships it free.
//
// Triggers: issue.state_changed (fires when an issue's state changes and
// the transition matches the trigger filter) and issue.created (C12T2 —
// fires on issue creation, no filters in v1), plus the C16T0 field
// events: issue.assigned / issue.unassigned (an assignee is added /
// removed), issue.labels_changed (a label is attached or detached;
// optional label_ids filter), issue.priority_changed (optional
// from_priorities / to_priorities filters), issue.due_date_changed (the
// target date changes), issue.estimate_changed (the estimate point
// changes) and issue.comment_added. Actions run in order
// inside the firing transaction: assign, add_label, add_comment,
// set_priority, set_state (C12T2), remove_label, unassign, set_estimate,
// set_due_date, move_to_cycle, move_to_module, add_watcher (C16T2). An
// action failure is LOGGED (slog), recorded on the run row as ok:false,
// and ABORTS the rule's remaining actions (C16T2 — a half-applied rule
// is worse than a stopped one); it never rolls back the firing event.
// Automation-driven changes never
// re-trigger automations: the depth-1 loop guard (automationActiveKey)
// suppresses evaluation while automation actions run, so a rule's own
// add_comment — or its set_state, even on the creation path — can never
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
	"strconv"
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

// AutomationTrigger fires a rule. C11T1 shipped issue.state_changed with
// optional from/to state filters (a null/empty filter matches any
// state); C12T2 adds issue.created, which fires on issue creation and
// takes no filters in v1. C16T0 adds the field-level events:
// issue.assigned / issue.unassigned (an assignee is added / removed),
// issue.labels_changed (a label is attached or detached; the optional
// label_ids filter fires only when a listed label is added or removed),
// issue.priority_changed (optional from_priorities / to_priorities
// filters, 0-4), issue.due_date_changed (the issue's target date — its
// due date — changes), issue.estimate_changed (the estimate point
// changes) and issue.comment_added. Apart from labels_changed and
// priority_changed, the new events take no filters in v1: a filter on
// them is rejected rather than silently ignored.
type AutomationTrigger struct {
	Type           string   `json:"type"`
	FromStates     []string `json:"from_states"`
	ToStates       []string `json:"to_states"`
	LabelIDs       []string `json:"label_ids,omitempty"`
	FromPriorities []int    `json:"from_priorities,omitempty"`
	ToPriorities   []int    `json:"to_priorities,omitempty"`
}

// AutomationAction is one ordered step: exactly one of assign (user_id),
// add_label (label_id), add_comment (body), set_priority (priority,
// 0-4), set_state (state_id, a state UUID in this project), remove_label
// (label_id), unassign (no parameters — clears every assignee),
// set_estimate (estimate, an estimate-point UUID on one of this project's
// scales), set_due_date (due_date: an absolute ISO date "YYYY-MM-DD" or a
// relative offset "+Nd" days from the firing time), move_to_cycle
// (cycle_id, a cycle UUID in this project — the issue leaves any other
// cycles), move_to_module (module_id, a module UUID in this project —
// the issue leaves any other modules), add_watcher (user_id, a workspace
// member, added as an issue subscriber).
type AutomationAction struct {
	Type     string  `json:"type"`
	UserID   *string `json:"user_id,omitempty"`
	LabelID  *string `json:"label_id,omitempty"`
	Body     *string `json:"body,omitempty"`
	Priority *int    `json:"priority,omitempty"`
	StateID  *string `json:"state_id,omitempty"`
	CycleID  *string `json:"cycle_id,omitempty"`
	ModuleID *string `json:"module_id,omitempty"`
	Estimate *string `json:"estimate,omitempty"`
	DueDate  *string `json:"due_date,omitempty"`
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

// validateAutomationTrigger checks the trigger shape. issue.state_changed
// takes optional from/to state filters (every listed state must be a
// UUID of a state in this project — checkStateInProject's ErrInvalidState
// is mapped to ErrInvalidAutomationTrigger so callers see one sentinel).
// issue.labels_changed takes an optional label_ids filter (every listed
// label must be a UUID of a label in this workspace). issue.priority_changed
// takes optional from/to priority filters pinned to the schema enum
// (0-4). The remaining types take no filters at all. Filters that don't
// belong to a trigger type are rejected rather than silently ignored.
// Unknown types are rejected as before.
func validateAutomationTrigger(ctx context.Context, q queryRower, wsID, projectID string, t AutomationTrigger) error {
	// filterless reports whether the trigger carries no filter field at
	// all; filterless event types reject any of them.
	filterless := len(t.FromStates) == 0 && len(t.ToStates) == 0 &&
		len(t.LabelIDs) == 0 && len(t.FromPriorities) == 0 && len(t.ToPriorities) == 0
	switch t.Type {
	case "issue.state_changed":
		if len(t.LabelIDs) > 0 || len(t.FromPriorities) > 0 || len(t.ToPriorities) > 0 {
			return ErrInvalidAutomationTrigger
		}
		for _, sid := range append(append([]string{}, t.FromStates...), t.ToStates...) {
			if _, err := checkStateInProject(ctx, q, projectID, sid); err != nil {
				return ErrInvalidAutomationTrigger
			}
		}
		return nil
	case "issue.labels_changed":
		if len(t.FromStates) > 0 || len(t.ToStates) > 0 ||
			len(t.FromPriorities) > 0 || len(t.ToPriorities) > 0 {
			return ErrInvalidAutomationTrigger
		}
		for _, lid := range t.LabelIDs {
			lid = strings.ToLower(strings.TrimSpace(lid))
			var owner string
			err := q.QueryRow(ctx,
				`SELECT workspace_id::text FROM labels WHERE id = $1::uuid`, lid).Scan(&owner)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
					return ErrInvalidAutomationTrigger
				}
				return err
			}
			if owner != wsID {
				return ErrInvalidAutomationTrigger
			}
		}
		return nil
	case "issue.priority_changed":
		if len(t.FromStates) > 0 || len(t.ToStates) > 0 || len(t.LabelIDs) > 0 {
			return ErrInvalidAutomationTrigger
		}
		for _, p := range append(append([]int{}, t.FromPriorities...), t.ToPriorities...) {
			if p < 0 || p > 4 {
				return ErrInvalidAutomationTrigger
			}
		}
		return nil
	case "issue.created", "issue.assigned", "issue.unassigned",
		"issue.due_date_changed", "issue.estimate_changed", "issue.comment_added":
		if !filterless {
			return ErrInvalidAutomationTrigger
		}
		return nil
	default:
		return ErrInvalidAutomationTrigger
	}
}

// validateAutomationActions checks each action's shape and referential
// targets at write time: assign needs a workspace member, add_label a
// label in this workspace, add_comment a non-empty body, set_priority a
// priority pinned to the schema enum (0-4), set_state a state in this
// project (like add_label's write-time check), remove_label a label in
// this workspace, unassign takes no parameters, set_estimate an
// estimate point on one of this project's scales (reusing
// checkEstimatePoint — a mismatch is an honest 400), set_due_date an
// absolute ISO date or a "+Nd" offset, move_to_cycle a cycle in this
// project, move_to_module a module in this project, add_watcher a
// workspace member. Targets can still vanish later — execution failures
// are logged, recorded on the run row, and abort the rule's remaining
// actions, never the firing event.
func validateAutomationActions(ctx context.Context, q queryRower, wsID, projectID string, actions []AutomationAction) error {
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
		case "set_priority":
			if a.Priority == nil || *a.Priority < 0 || *a.Priority > 4 {
				return ErrInvalidAutomationAction
			}
		case "set_state":
			if a.StateID == nil {
				return ErrInvalidAutomationAction
			}
			if _, err := checkStateInProject(ctx, q, projectID,
				strings.ToLower(strings.TrimSpace(*a.StateID))); err != nil {
				// Bad UUID or a state from another project: a write-time
				// 400, same as add_label's target check.
				return ErrInvalidAutomationAction
			}
		case "remove_label":
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
		case "unassign":
			// No parameters: clears every assignee. Nothing to validate.
		case "set_estimate":
			if a.Estimate == nil {
				return ErrInvalidAutomationAction
			}
			// Reuse the issue write path's scale validation
			// (checkEstimatePoint): the point must belong to a scale of
			// this project. A mismatch is an honest 400.
			if _, err := checkEstimatePoint(ctx, q, projectID, *a.Estimate); err != nil {
				if errors.Is(err, ErrInvalidEstimatePoint) {
					return ErrInvalidAutomationAction
				}
				return err
			}
		case "set_due_date":
			if a.DueDate == nil {
				return ErrInvalidAutomationAction
			}
			if _, err := parseAutomationDueDateSpec(*a.DueDate); err != nil {
				return ErrInvalidAutomationAction
			}
		case "move_to_cycle":
			if a.CycleID == nil {
				return ErrInvalidAutomationAction
			}
			if _, err := resolveCycle(ctx, q, projectID,
				strings.ToLower(strings.TrimSpace(*a.CycleID))); err != nil {
				// Bad UUID or a cycle from another project: a write-time
				// 400, same as set_state's target check.
				if errors.Is(err, ErrInvalidCycleID) || errors.Is(err, ErrCycleNotFound) {
					return ErrInvalidAutomationAction
				}
				return err
			}
		case "move_to_module":
			if a.ModuleID == nil {
				return ErrInvalidAutomationAction
			}
			if _, err := resolveModule(ctx, q, projectID,
				strings.ToLower(strings.TrimSpace(*a.ModuleID))); err != nil {
				if errors.Is(err, ErrInvalidModuleID) || errors.Is(err, ErrModuleNotFound) {
					return ErrInvalidAutomationAction
				}
				return err
			}
		case "add_watcher":
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
		default:
			return ErrInvalidAutomationAction
		}
	}
	return nil
}

// automationDueDateSpec is a parsed set_due_date value: either an
// absolute calendar date or a relative offset of N days from the moment
// the rule fires.
type automationDueDateSpec struct {
	absolute   time.Time // midnight UTC of the absolute date
	offsetDays int       // +Nd: days after the firing day (UTC)
	relative   bool
}

// parseAutomationDueDateSpec parses a set_due_date value: an absolute
// ISO date ("YYYY-MM-DD") or a relative offset "+Nd" (N days from now).
// Anything else — "tomorrow", datetimes, negative offsets — is rejected
// at rule-write time with an honest 400.
func parseAutomationDueDateSpec(s string) (automationDueDateSpec, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "+") && strings.HasSuffix(s, "d") && len(s) > 2 {
		n, err := strconv.Atoi(s[1 : len(s)-1])
		if err != nil || n < 0 {
			return automationDueDateSpec{}, ErrInvalidAutomationAction
		}
		return automationDueDateSpec{relative: true, offsetDays: n}, nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return automationDueDateSpec{}, ErrInvalidAutomationAction
	}
	return automationDueDateSpec{absolute: t.UTC()}, nil
}

// at resolves the spec to a calendar date: the absolute date, or the
// firing day (UTC) plus the offset. Relative offsets are resolved at
// execution time — "+Nd" means N days from when the rule fires, not
// from when it was written.
func (d automationDueDateSpec) at(now time.Time) time.Time {
	if d.relative {
		return now.UTC().Truncate(24*time.Hour).AddDate(0, 0, d.offsetDays)
	}
	return d.absolute
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
	if err := validateAutomationTrigger(ctx, pool, wsID, projectID, in.Trigger); err != nil {
		return nil, err
	}
	if err := validateAutomationActions(ctx, pool, wsID, projectID, in.Actions); err != nil {
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
		if err := validateAutomationTrigger(ctx, pool, wsID, projectID, *patch.Trigger); err != nil {
			return nil, err
		}
	}
	if patch.Actions != nil {
		if err := validateAutomationActions(ctx, pool, wsID, projectID, *patch.Actions); err != nil {
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

// matchesEvent reports whether the event fires this trigger. The type
// was already matched by the caller. Filterless event types always
// match; a null/empty filter (nil from JSON null) matches any value of
// its kind. label_ids fires when any listed label is added OR removed.
func (t AutomationTrigger) matchesEvent(ev automationEvent) bool {
	switch ev.typ {
	case "issue.state_changed":
		return t.matches(ev.fromStateID, ev.toStateID)
	case "issue.priority_changed":
		if len(t.FromPriorities) > 0 && !slices.Contains(t.FromPriorities, ev.fromPriority) {
			return false
		}
		if len(t.ToPriorities) > 0 && !slices.Contains(t.ToPriorities, ev.toPriority) {
			return false
		}
		return true
	case "issue.labels_changed":
		if len(t.LabelIDs) == 0 {
			return true
		}
		for _, id := range slices.Concat(ev.addedLabelIDs, ev.removedLabelIDs) {
			if slices.Contains(t.LabelIDs, id) {
				return true
			}
		}
		return false
	default:
		return true
	}
}

// automationEvent is one firing event: the trigger type plus the
// filter-matching payload. Payload fields apply only to their event
// type; filterless events carry just the type.
type automationEvent struct {
	typ string
	// issue.state_changed
	fromStateID, toStateID string
	// issue.priority_changed
	fromPriority, toPriority int
	// issue.labels_changed
	addedLabelIDs, removedLabelIDs []string
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
// the per-action ok/error outcomes — except a firing whose every action
// was a no-op (C12T2: set_priority/set_state targeting the current
// value), which writes no run row and no activity. Returned
// notifications are for the caller to announce after commit.
func runAutomationRulesTx(ctx context.Context, tx pgx.Tx, wsID, projectID, ident, issueID, actorID, fromStateID, toStateID string) []*Notification {
	return runAutomationRulesForEventTx(ctx, tx, wsID, projectID, ident, issueID, actorID,
		automationEvent{typ: "issue.state_changed", fromStateID: fromStateID, toStateID: toStateID})
}

// runAutomationCreatedRulesTx evaluates enabled issue.created rules for a
// newly created issue and runs matching actions inside the caller's tx.
// Call it only from CreateIssue after the insert (guarded by
// !automationActive), in the same tx so the creation, its automation
// writes, and the run rows commit atomically. Never returns an error;
// same logging ethos as the state-change path.
func runAutomationCreatedRulesTx(ctx context.Context, tx pgx.Tx, wsID, projectID, ident, issueID, actorID string) []*Notification {
	return runAutomationRulesForEventTx(ctx, tx, wsID, projectID, ident, issueID, actorID,
		automationEvent{typ: "issue.created"})
}

// runAutomationAssignedTx evaluates enabled issue.assigned rules: an
// assignee was newly added to the issue. Call it only from the
// assignee-write paths (guarded by !automationActive), in the same tx
// as the assignment so a rolled-back write can never fire a rule.
// Never returns an error; same logging ethos as the state-change path.
func runAutomationAssignedTx(ctx context.Context, tx pgx.Tx, wsID, projectID, ident, issueID, actorID string) []*Notification {
	return runAutomationRulesForEventTx(ctx, tx, wsID, projectID, ident, issueID, actorID,
		automationEvent{typ: "issue.assigned"})
}

// runAutomationUnassignedTx evaluates enabled issue.unassigned rules:
// an assignee was removed from the issue. Same-tx, same ethos as
// runAutomationAssignedTx.
func runAutomationUnassignedTx(ctx context.Context, tx pgx.Tx, wsID, projectID, ident, issueID, actorID string) []*Notification {
	return runAutomationRulesForEventTx(ctx, tx, wsID, projectID, ident, issueID, actorID,
		automationEvent{typ: "issue.unassigned"})
}

// runAutomationLabelsChangedTx evaluates enabled issue.labels_changed
// rules: labels were attached (added) and/or detached (removed). A rule
// with a label_ids filter fires when any listed label appears on either
// side. Same-tx, same ethos as the other field events.
func runAutomationLabelsChangedTx(ctx context.Context, tx pgx.Tx, wsID, projectID, ident, issueID, actorID string, added, removed []string) []*Notification {
	return runAutomationRulesForEventTx(ctx, tx, wsID, projectID, ident, issueID, actorID,
		automationEvent{typ: "issue.labels_changed", addedLabelIDs: added, removedLabelIDs: removed})
}

// runAutomationPriorityChangedTx evaluates enabled
// issue.priority_changed rules for the priority transition
// fromPriority -> toPriority. Same-tx, same ethos as the state-change
// path.
func runAutomationPriorityChangedTx(ctx context.Context, tx pgx.Tx, wsID, projectID, ident, issueID, actorID string, fromPriority, toPriority int) []*Notification {
	return runAutomationRulesForEventTx(ctx, tx, wsID, projectID, ident, issueID, actorID,
		automationEvent{typ: "issue.priority_changed", fromPriority: fromPriority, toPriority: toPriority})
}

// runAutomationDueDateChangedTx evaluates enabled issue.due_date_changed
// rules: the issue's target date (its due date) changed. Same-tx, same
// ethos as the other field events.
func runAutomationDueDateChangedTx(ctx context.Context, tx pgx.Tx, wsID, projectID, ident, issueID, actorID string) []*Notification {
	return runAutomationRulesForEventTx(ctx, tx, wsID, projectID, ident, issueID, actorID,
		automationEvent{typ: "issue.due_date_changed"})
}

// runAutomationEstimateChangedTx evaluates enabled issue.estimate_changed
// rules: the issue's estimate point changed. Same-tx, same ethos as
// the other field events.
func runAutomationEstimateChangedTx(ctx context.Context, tx pgx.Tx, wsID, projectID, ident, issueID, actorID string) []*Notification {
	return runAutomationRulesForEventTx(ctx, tx, wsID, projectID, ident, issueID, actorID,
		automationEvent{typ: "issue.estimate_changed"})
}

// runAutomationCommentAddedTx evaluates enabled issue.comment_added
// rules: a comment was posted on the issue. Call it only from
// CreateComment (guarded by !automationActive), in the same tx.
// Automation-driven comments bypass CreateComment (direct tx SQL under
// the loop guard), so they can never re-trigger.
func runAutomationCommentAddedTx(ctx context.Context, tx pgx.Tx, wsID, projectID, ident, issueID, actorID string) []*Notification {
	return runAutomationRulesForEventTx(ctx, tx, wsID, projectID, ident, issueID, actorID,
		automationEvent{typ: "issue.comment_added"})
}

// runAutomationRulesForEventTx is the shared firing core. ev is the
// event being evaluated; rules carrying any other trigger type are
// skipped, so a created rule can never fire on a state change and vice
// versa. The event's filter payload is matched by
// AutomationTrigger.matchesEvent: filterless event types always match
// once the type matched. It never returns an error: rule-selection
// failures are logged via slog and the event proceeds. A failed action
// is recorded on the run row as ok:false and ABORTS the rule's
// remaining actions (C16T2 — a half-applied rule is worse than a stopped
// one); the firing transaction still commits, so a broken rule can
// never roll back the user's change.
func runAutomationRulesForEventTx(ctx context.Context, tx pgx.Tx, wsID, projectID, ident, issueID, actorID string, ev automationEvent) []*Notification {
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
		// Trigger types are event-scoped: a rule only ever fires for
		// the event its trigger names.
		if trig.Type != ev.typ {
			continue
		}
		if !trig.matchesEvent(ev) {
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
		anyEffect := false
		for i, a := range actions {
			ns, noop, err := execAutomationActionTx(actx, tx, wsID, projectID, ident, issueID, actorID, a)
			if err != nil {
				// C16T2: a failed action aborts the rule's remaining
				// actions — the failure is recorded ok:false on the run
				// row below, and the firing tx still commits. A broken
				// rule must not roll back the user's change, and a
				// half-applied rule is worse than a stopped one.
				slog.Error("automation: action failed; aborting remaining actions of the rule",
					"rule_id", r.ID, "rule_name", r.Name,
					"action_index", i, "action_type", a.Type, "error", err)
				errText := err.Error()
				results = append(results, AutomationActionResult{Type: a.Type, OK: false, Error: &errText})
				anyEffect = true
				break
			}
			notified = append(notified, ns...)
			results = append(results, AutomationActionResult{Type: a.Type, OK: true})
			if !noop {
				anyEffect = true
			}
		}
		if !anyEffect {
			// C12T2: the firing changed nothing (every action was a
			// no-op, e.g. set_state to the issue's current state) —
			// no run row, no activity, nothing to announce.
			continue
		}
		// C12T1: one run row per firing, in this same tx — the state
		// change and its log commit atomically. A run-row insert
		// failure is logged, never fatal (same ethos as actions).
		recordAutomationRunTx(actx, tx, r.ID, issueID, trig.Type, results)
	}
	return notified
}

// execAutomationActionTx runs one action inside the firing tx and
// reports (notifications, noop, error). noop is true when the action
// evaluated cleanly but changed nothing (C12T2: set_priority/set_state
// targeting the current value) — the caller records it ok:true but a
// firing whose every action was a no-op writes no run row. Failures are
// returned for the caller to log; the tx is untouched.
func execAutomationActionTx(ctx context.Context, tx pgx.Tx, wsID, projectID, ident, issueID, actorID string, a AutomationAction) ([]*Notification, bool, error) {
	switch a.Type {
	case "assign":
		if a.UserID == nil {
			return nil, false, ErrInvalidAutomationAction
		}
		ns, err := automationAssignTx(ctx, tx, wsID, projectID, ident, issueID, actorID,
			strings.ToLower(strings.TrimSpace(*a.UserID)))
		return ns, false, err
	case "add_label":
		if a.LabelID == nil {
			return nil, false, ErrInvalidAutomationAction
		}
		return nil, false, automationAddLabelTx(ctx, tx, wsID, issueID, actorID,
			strings.ToLower(strings.TrimSpace(*a.LabelID)))
	case "add_comment":
		if a.Body == nil {
			return nil, false, ErrInvalidAutomationAction
		}
		ns, err := automationAddCommentTx(ctx, tx, wsID, projectID, ident, issueID, actorID, *a.Body)
		return ns, false, err
	case "set_priority":
		if a.Priority == nil {
			return nil, false, ErrInvalidAutomationAction
		}
		return automationSetPriorityTx(ctx, tx, issueID, actorID, *a.Priority)
	case "set_state":
		if a.StateID == nil {
			return nil, false, ErrInvalidAutomationAction
		}
		return automationSetStateTx(ctx, tx, wsID, projectID, ident, issueID, actorID,
			strings.ToLower(strings.TrimSpace(*a.StateID)))
	case "remove_label":
		if a.LabelID == nil {
			return nil, false, ErrInvalidAutomationAction
		}
		return automationRemoveLabelTx(ctx, tx, wsID, issueID, actorID,
			strings.ToLower(strings.TrimSpace(*a.LabelID)))
	case "unassign":
		return automationUnassignTx(ctx, tx, wsID, projectID, ident, issueID, actorID)
	case "set_estimate":
		if a.Estimate == nil {
			return nil, false, ErrInvalidAutomationAction
		}
		return automationSetEstimateTx(ctx, tx, projectID, issueID, actorID,
			strings.TrimSpace(*a.Estimate))
	case "set_due_date":
		if a.DueDate == nil {
			return nil, false, ErrInvalidAutomationAction
		}
		return automationSetDueDateTx(ctx, tx, issueID, actorID, *a.DueDate)
	case "move_to_cycle":
		if a.CycleID == nil {
			return nil, false, ErrInvalidAutomationAction
		}
		return automationMoveToCycleTx(ctx, tx, projectID, issueID, actorID,
			strings.ToLower(strings.TrimSpace(*a.CycleID)))
	case "move_to_module":
		if a.ModuleID == nil {
			return nil, false, ErrInvalidAutomationAction
		}
		return automationMoveToModuleTx(ctx, tx, projectID, issueID, actorID,
			strings.ToLower(strings.TrimSpace(*a.ModuleID)))
	case "add_watcher":
		if a.UserID == nil {
			return nil, false, ErrInvalidAutomationAction
		}
		return automationAddWatcherTx(ctx, tx, wsID, issueID, actorID,
			strings.ToLower(strings.TrimSpace(*a.UserID)))
	default:
		return nil, false, ErrInvalidAutomationAction
	}
}

// automationSetPriorityTx sets the issue's priority directly (never
// through UpdateIssue, so the loop guard is belt-and-braces). A target
// equal to the current priority is a no-op: no activity row, and the
// caller skips the run row when the whole firing was no-ops. Manual
// priority changes notify nobody in-app, so neither does this.
func automationSetPriorityTx(ctx context.Context, tx pgx.Tx, issueID, actorID string, priority int) ([]*Notification, bool, error) {
	var old int
	if err := tx.QueryRow(ctx,
		`SELECT priority FROM issues WHERE id = $1::uuid`, issueID).Scan(&old); err != nil {
		return nil, false, err
	}
	if old == priority {
		return nil, true, nil
	}
	if _, err := tx.Exec(ctx,
		`UPDATE issues SET priority = $1, updated_at = now() WHERE id = $2::uuid`,
		priority, issueID); err != nil {
		return nil, false, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO issue_activities (issue_id, actor_id, field, old_value, new_value)
		 VALUES ($1::uuid, $2::uuid, 'priority', $3::jsonb, $4::jsonb)`,
		issueID, actorID, toJSONBParam(old), toJSONBParam(priority)); err != nil {
		return nil, false, err
	}
	return nil, false, nil
}

// automationSetStateTx moves the issue to the target state directly
// (never through UpdateIssue — the depth-1 loop guard makes a cascade
// structurally impossible even if this were refactored later). The
// target was validated at rule-write time but may have been deleted
// since: a vanished target fails the action (logged, ok:false), never
// the firing. A target equal to the current state is a no-op: no
// activity row, and the caller skips the run row when the whole firing
// was no-ops. A real move mirrors the manual move's fan-out — one
// state_id activity row plus NotifyStateChanged to the watchers — but
// enqueues no webhooks/Slack in v1 (documented scope cut).
func automationSetStateTx(ctx context.Context, tx pgx.Tx, wsID, projectID, ident, issueID, actorID, stateID string) ([]*Notification, bool, error) {
	var old string
	if err := tx.QueryRow(ctx,
		`SELECT state_id::text FROM issues WHERE id = $1::uuid`, issueID).Scan(&old); err != nil {
		return nil, false, err
	}
	if old == stateID {
		return nil, true, nil
	}
	sid, err := checkStateInProject(ctx, tx, projectID, stateID)
	if err != nil {
		return nil, false, err
	}
	// C16T3: blocker guard — an automation move into a completed state is
	// rejected when open blockers point at the issue. The failure surfaces
	// on the run row as ok:false (a loud failure, never a silent skip);
	// the issue keeps its current state and the firing tx is untouched.
	if err := assertNoOpenBlockersTx(ctx, tx, projectID, ident, issueID, sid); err != nil {
		return nil, false, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE issues SET state_id = $1::uuid, updated_at = now() WHERE id = $2::uuid`,
		sid, issueID); err != nil {
		return nil, false, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO issue_activities (issue_id, actor_id, field, old_value, new_value)
		 VALUES ($1::uuid, $2::uuid, 'state_id', $3::jsonb, $4::jsonb)`,
		issueID, actorID, toJSONBParam(old), toJSONBParam(sid)); err != nil {
		return nil, false, err
	}
	displayID, name, err := issueNotifyContextTx(ctx, tx, ident, issueID)
	if err != nil {
		return nil, false, err
	}
	var stateName string
	if err := tx.QueryRow(ctx,
		`SELECT name FROM states WHERE id = $1::uuid`, sid).Scan(&stateName); err != nil {
		return nil, false, err
	}
	watchers, err := issueWatchersTx(ctx, tx, issueID)
	if err != nil {
		return nil, false, err
	}
	actorName := actorDisplayName(ctx, tx, actorID)
	notified, err := notifyTx(ctx, tx, NotifyStateChanged,
		fmt.Sprintf("%s moved %s to %s", actorName, displayID, stateName),
		fmt.Sprintf("Issue: %s", name),
		map[string]any{
			"issue_id":   issueID,
			"display_id": displayID,
			"issue_name": name,
			"state_id":   sid,
			"state_name": stateName,
			"actor_id":   actorID,
			"project_id": projectID,
		},
		actorID, watchers)
	if err != nil {
		return nil, false, err
	}
	return notified, false, nil
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

// automationRemoveLabelTx detaches one label. Idempotent: a label that
// is not attached is a no-op. The label must still belong to this
// workspace; a label deleted since rule creation fails the action
// (logged + run row, aborting the tail), never the firing.
func automationRemoveLabelTx(ctx context.Context, tx pgx.Tx, wsID, issueID, actorID, labelID string) ([]*Notification, bool, error) {
	var owner string
	if err := tx.QueryRow(ctx,
		`SELECT workspace_id::text FROM labels WHERE id = $1::uuid`, labelID).Scan(&owner); err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return nil, false, ErrInvalidAutomationAction
		}
		return nil, false, err
	}
	if owner != wsID {
		return nil, false, ErrInvalidAutomationAction
	}
	var old []string
	rows, err := tx.Query(ctx,
		`SELECT label_id::text FROM issue_labels WHERE issue_id = $1::uuid ORDER BY label_id::text`, issueID)
	if err != nil {
		return nil, false, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, false, err
		}
		old = append(old, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if !slices.Contains(old, labelID) {
		return nil, true, nil
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM issue_labels WHERE issue_id = $1::uuid AND label_id = $2::uuid`,
		issueID, labelID); err != nil {
		return nil, false, err
	}
	oldArr := old
	if oldArr == nil {
		oldArr = []string{}
	}
	idx := slices.Index(old, labelID) // >= 0: the Contains check above
	newSet := slices.Delete(slices.Clone(old), idx, idx+1)
	if _, err := tx.Exec(ctx,
		`INSERT INTO issue_activities (issue_id, actor_id, field, old_value, new_value)
		 VALUES ($1::uuid, $2::uuid, 'labels', $3::jsonb, $4::jsonb)`,
		issueID, actorID, toJSONBParam(oldArr), toJSONBParam(newSet)); err != nil {
		return nil, false, err
	}
	return nil, false, nil
}

// automationUnassignTx clears every assignee (the unassign action takes
// no parameters). A real removal mirrors the manual path: one
// "assignees" activity row plus an unassigned notification per removed
// assignee. No assignees is a no-op.
func automationUnassignTx(ctx context.Context, tx pgx.Tx, wsID, projectID, ident, issueID, actorID string) ([]*Notification, bool, error) {
	rows, err := tx.Query(ctx,
		`SELECT user_id::text FROM issue_assignees WHERE issue_id = $1::uuid ORDER BY user_id::text`,
		issueID)
	if err != nil {
		return nil, false, err
	}
	var old []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, false, err
		}
		old = append(old, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if len(old) == 0 {
		return nil, true, nil
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM issue_assignees WHERE issue_id = $1::uuid`, issueID); err != nil {
		return nil, false, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO issue_activities (issue_id, actor_id, field, old_value, new_value)
		 VALUES ($1::uuid, $2::uuid, 'assignees', $3::jsonb, $4::jsonb)`,
		issueID, actorID, toJSONBParam(old), toJSONBParam([]string{})); err != nil {
		return nil, false, err
	}
	displayID, name, err := issueNotifyContextTx(ctx, tx, ident, issueID)
	if err != nil {
		return nil, false, err
	}
	actorName := actorDisplayName(ctx, tx, actorID)
	var notified []*Notification
	for _, uid := range old {
		ns, err := notifyTx(ctx, tx, NotifyIssueUnassigned,
			fmt.Sprintf("%s unassigned you from %s", actorName, displayID),
			fmt.Sprintf("Issue: %s", name),
			map[string]any{
				"issue_id":     issueID,
				"display_id":   displayID,
				"issue_name":   name,
				"actor_id":     actorID,
				"workspace_id": wsID,
				"project_id":   projectID,
			},
			actorID, []string{uid})
		if err != nil {
			return nil, false, err
		}
		notified = append(notified, ns...)
	}
	return notified, false, nil
}

// automationSetEstimateTx points the issue at an estimate point on one
// of this project's scales, reusing the issue write path's scale
// validation (checkEstimatePoint): a point deleted since rule creation
// fails the action (logged + run row), never the firing. A target equal
// to the current point is a no-op. Manual estimate changes notify
// nobody in-app, so neither does this.
func automationSetEstimateTx(ctx context.Context, tx pgx.Tx, projectID, issueID, actorID, pointID string) ([]*Notification, bool, error) {
	var old *string
	if err := tx.QueryRow(ctx,
		`SELECT estimate_point_id::text FROM issues WHERE id = $1::uuid`, issueID).Scan(&old); err != nil {
		return nil, false, err
	}
	if old != nil && *old == pointID {
		return nil, true, nil
	}
	epid, err := checkEstimatePoint(ctx, tx, projectID, pointID)
	if err != nil {
		return nil, false, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE issues SET estimate_point_id = $1::uuid, updated_at = now() WHERE id = $2::uuid`,
		epid, issueID); err != nil {
		return nil, false, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO issue_activities (issue_id, actor_id, field, old_value, new_value)
		 VALUES ($1::uuid, $2::uuid, 'estimate_point_id', $3::jsonb, $4::jsonb)`,
		issueID, actorID, toJSONBParam(derefPtr(old)), toJSONBParam(epid)); err != nil {
		return nil, false, err
	}
	return nil, false, nil
}

// automationSetDueDateTx sets the issue's target date from a due_date
// spec: an absolute ISO date or a "+Nd" offset resolved against the
// firing moment (UTC day). A target equal to the current date is a
// no-op. Manual due-date changes notify nobody in-app, so neither does
// this.
func automationSetDueDateTx(ctx context.Context, tx pgx.Tx, issueID, actorID, spec string) ([]*Notification, bool, error) {
	d, err := parseAutomationDueDateSpec(spec)
	if err != nil {
		// Unreachable after write-time validation; kept honest anyway.
		return nil, false, ErrInvalidAutomationAction
	}
	target := d.at(time.Now())
	var old *time.Time
	if err := tx.QueryRow(ctx,
		`SELECT target_date FROM issues WHERE id = $1::uuid`, issueID).Scan(&old); err != nil {
		return nil, false, err
	}
	if old != nil && old.UTC().Truncate(24*time.Hour).Equal(target) {
		return nil, true, nil
	}
	if _, err := tx.Exec(ctx,
		`UPDATE issues SET target_date = $1, updated_at = now() WHERE id = $2::uuid`,
		target, issueID); err != nil {
		return nil, false, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO issue_activities (issue_id, actor_id, field, old_value, new_value)
		 VALUES ($1::uuid, $2::uuid, 'target_date', $3::jsonb, $4::jsonb)`,
		issueID, actorID, toJSONBParam(derefPtr(old)), toJSONBParam(&target)); err != nil {
		return nil, false, err
	}
	return nil, false, nil
}

// automationMoveToCycleTx moves the issue into the target cycle: it
// leaves every other cycle first ("move" semantics — an issue rides one
// sprint at a time), then joins the target. Already only in the target
// is a no-op. A cycle deleted since rule creation fails the action
// (logged + run row), never the firing. The loop guard makes a cascade
// structurally impossible.
func automationMoveToCycleTx(ctx context.Context, tx pgx.Tx, projectID, issueID, actorID, cycleID string) ([]*Notification, bool, error) {
	if _, err := resolveCycle(ctx, tx, projectID, cycleID); err != nil {
		return nil, false, err
	}
	var current []string
	rows, err := tx.Query(ctx,
		`SELECT cycle_id::text FROM cycle_issues WHERE issue_id = $1::uuid ORDER BY cycle_id::text`,
		issueID)
	if err != nil {
		return nil, false, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, false, err
		}
		current = append(current, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if slices.Equal(current, []string{cycleID}) {
		return nil, true, nil
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM cycle_issues WHERE issue_id = $1::uuid`, issueID); err != nil {
		return nil, false, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO cycle_issues (cycle_id, issue_id) VALUES ($1::uuid, $2::uuid)
		 ON CONFLICT DO NOTHING`, cycleID, issueID); err != nil {
		return nil, false, err
	}
	oldArr := current
	if oldArr == nil {
		oldArr = []string{}
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO issue_activities (issue_id, actor_id, field, old_value, new_value)
		 VALUES ($1::uuid, $2::uuid, 'cycle_id', $3::jsonb, $4::jsonb)`,
		issueID, actorID, toJSONBParam(oldArr), toJSONBParam([]string{cycleID})); err != nil {
		return nil, false, err
	}
	return nil, false, nil
}

// automationMoveToModuleTx moves the issue into the target module:
// same "move" semantics as move_to_cycle over the module_issues
// junction. A module deleted since rule creation fails the action
// (logged + run row), never the firing.
func automationMoveToModuleTx(ctx context.Context, tx pgx.Tx, projectID, issueID, actorID, moduleID string) ([]*Notification, bool, error) {
	if _, err := resolveModule(ctx, tx, projectID, moduleID); err != nil {
		return nil, false, err
	}
	var current []string
	rows, err := tx.Query(ctx,
		`SELECT module_id::text FROM module_issues WHERE issue_id = $1::uuid ORDER BY module_id::text`,
		issueID)
	if err != nil {
		return nil, false, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, false, err
		}
		current = append(current, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if slices.Equal(current, []string{moduleID}) {
		return nil, true, nil
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM module_issues WHERE issue_id = $1::uuid`, issueID); err != nil {
		return nil, false, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO module_issues (module_id, issue_id) VALUES ($1::uuid, $2::uuid)
		 ON CONFLICT DO NOTHING`, moduleID, issueID); err != nil {
		return nil, false, err
	}
	oldArr := current
	if oldArr == nil {
		oldArr = []string{}
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO issue_activities (issue_id, actor_id, field, old_value, new_value)
		 VALUES ($1::uuid, $2::uuid, 'module_id', $3::jsonb, $4::jsonb)`,
		issueID, actorID, toJSONBParam(oldArr), toJSONBParam([]string{moduleID})); err != nil {
		return nil, false, err
	}
	return nil, false, nil
}

// automationAddWatcherTx adds the user as an issue subscriber (a
// "watcher" is the subscriber+assignee union — see issueWatchersTx).
// Idempotent: an existing subscriber — or an assignee, who already
// watches — is a no-op. The user must still be a workspace member; a
// member who left since the rule was created fails the action (logged +
// run row), never the firing.
func automationAddWatcherTx(ctx context.Context, tx pgx.Tx, wsID, issueID, actorID, userID string) ([]*Notification, bool, error) {
	var one int
	if err := tx.QueryRow(ctx,
		`SELECT 1 FROM workspace_members WHERE workspace_id = $1::uuid AND user_id = $2::uuid`,
		wsID, userID).Scan(&one); err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return nil, false, ErrInvalidAutomationAction
		}
		return nil, false, err
	}
	var watching bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM issue_subscribers WHERE issue_id = $1::uuid AND user_id = $2::uuid)
		    OR EXISTS(SELECT 1 FROM issue_assignees WHERE issue_id = $1::uuid AND user_id = $2::uuid)`,
		issueID, userID).Scan(&watching); err != nil {
		return nil, false, err
	}
	if watching {
		return nil, true, nil
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO issue_subscribers (issue_id, user_id) VALUES ($1::uuid, $2::uuid)
		 ON CONFLICT DO NOTHING`, issueID, userID); err != nil {
		return nil, false, err
	}
	return nil, false, nil
}
