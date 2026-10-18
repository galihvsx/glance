package service

// Webhook event filters (C69): per-webhook JSON condition lists over work
// item attributes, evaluated at ENQUEUE time in enqueueWebhookDeliveryTx.
// Plane evaluates filters at delivery time; glance evaluates at enqueue
// time instead — the observable behavior is identical (a non-matching
// event never delivers) and enqueue-time keeps the delivery history
// truthful: a filtered-out event never creates an outbox row, so it
// never pollutes webhook_deliveries.
//
// Filter shape: a JSON array of {field, op, value} conditions stored in
// webhooks.filter (NULL/empty = no filter → deliver everything, fully
// backwards compatible). All conditions are AND-ed. Fields: priority,
// state_group, state_id, assignee_id, label_id, project_id, target_date.
// Ops: eq (scalar value), in (non-empty array value). No PQL text mode
// this cycle — Basic-equivalent only.
//
// Filters apply to work-item events only (issue.*, comment.*):
// non-work-item events bypass the filter and deliver normally, matching
// Plane semantics.
//
// The issue.* payloads are THIN (issue.created carries only
// id/display_id/name/actor_id), so the evaluator loads the issue
// snapshot in-tx from data["id"] (issue events) / data["issue_id"]
// (comment events) — one place, no call-site changes. Soft-deleted
// issues still have their row in-tx, so issue.deleted evaluates
// normally; deleted_at is deliberately NOT excluded.
//
// Fail-closed (mirroring Plane): any evaluation failure — missing or
// malformed issue id, unloadable issue row, corrupt stored filter,
// unexpected condition shape — SKIPS the delivery rather than
// defaulting to "deliver everything". The enqueue hook logs the skip.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrInvalidWebhookFilter is returned when a filter condition uses an
// unknown field or op, or a value shape the field does not accept. It
// maps to HTTP 400.
var ErrInvalidWebhookFilter = errors.New("service: invalid webhook filter")

// Webhook filter ops.
const (
	WebhookFilterOpEq = "eq"
	WebhookFilterOpIn = "in"
)

// Webhook filter fields.
const (
	WebhookFilterFieldPriority   = "priority"
	WebhookFilterFieldStateGroup = "state_group"
	WebhookFilterFieldStateID    = "state_id"
	WebhookFilterFieldAssigneeID = "assignee_id"
	WebhookFilterFieldLabelID    = "label_id"
	WebhookFilterFieldProjectID  = "project_id"
	WebhookFilterFieldTargetDate = "target_date"
)

// WebhookCondition is one {field, op, value} filter row. Value is a
// scalar (string) for eq and a non-empty array of strings for in after
// JSON decoding.
type WebhookCondition struct {
	Field string `json:"field"`
	Op    string `json:"op"`
	Value any    `json:"value"`
}

// WebhookFilter is a list of conditions AND-ed together. nil/empty =
// no filtering (deliver everything).
type WebhookFilter []WebhookCondition

// webhookFilterPriorities maps the filter's priority vocabulary onto
// the issues.priority smallint (0-4). Mirrors priorityWords in
// issue_export.go.
var webhookFilterPriorities = map[string]int{
	"none": 0, "low": 1, "medium": 2, "high": 3, "urgent": 4,
}

// webhookFilterStateGroups is the closed state_group vocabulary for
// filters. It deliberately mirrors Plane's vocabulary, NOT the
// states."group" CHECK: glance's own "triage" group is not filterable
// (recorded scope decision, cycle-69 plan).
var webhookFilterStateGroups = map[string]bool{
	"backlog": true, "unstarted": true, "started": true,
	"completed": true, "cancelled": true,
}

// ValidateWebhookFilter rejects unknown fields/ops and value shapes the
// field does not accept. Field/op matching is case-insensitive for the
// closed vocabularies (priority names, state groups, ops); UUID and
// date values are validated strictly.
func ValidateWebhookFilter(f WebhookFilter) error {
	for i, c := range f {
		if err := validateWebhookCondition(c); err != nil {
			return fmt.Errorf("%w: condition %d: %v", ErrInvalidWebhookFilter, i, err)
		}
	}
	return nil
}

func validateWebhookCondition(c WebhookCondition) error {
	switch c.Field {
	case WebhookFilterFieldPriority, WebhookFilterFieldStateGroup,
		WebhookFilterFieldStateID, WebhookFilterFieldAssigneeID,
		WebhookFilterFieldLabelID, WebhookFilterFieldProjectID,
		WebhookFilterFieldTargetDate:
	default:
		return fmt.Errorf("unknown field %q", c.Field)
	}
	switch c.Op {
	case WebhookFilterOpEq, WebhookFilterOpIn:
	default:
		return fmt.Errorf("unknown op %q", c.Op)
	}
	if c.Op == WebhookFilterOpIn {
		arr, ok := c.Value.([]any)
		if !ok {
			return fmt.Errorf("op %q requires an array value, got %T", WebhookFilterOpIn, c.Value)
		}
		if len(arr) == 0 {
			return fmt.Errorf("op %q requires a non-empty array value", WebhookFilterOpIn)
		}
		for j, v := range arr {
			if err := validateWebhookFilterValue(c.Field, v); err != nil {
				return fmt.Errorf("value[%d]: %v", j, err)
			}
		}
		return nil
	}
	return validateWebhookFilterValue(c.Field, c.Value)
}

func validateWebhookFilterValue(field string, v any) error {
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("%s value must be a string, got %T", field, v)
	}
	switch field {
	case WebhookFilterFieldPriority:
		if _, ok := webhookFilterPriorities[strings.ToLower(s)]; !ok {
			return fmt.Errorf("priority must be one of none|low|medium|high|urgent, got %q", s)
		}
	case WebhookFilterFieldStateGroup:
		if !webhookFilterStateGroups[strings.ToLower(s)] {
			return fmt.Errorf("state_group must be one of backlog|unstarted|started|completed|cancelled, got %q", s)
		}
	case WebhookFilterFieldStateID, WebhookFilterFieldAssigneeID,
		WebhookFilterFieldLabelID, WebhookFilterFieldProjectID:
		if _, err := uuid.Parse(s); err != nil {
			return fmt.Errorf("%s must be a uuid, got %q", field, s)
		}
	case WebhookFilterFieldTargetDate:
		if _, err := time.Parse(time.DateOnly, s); err != nil {
			return fmt.Errorf("target_date must be an ISO-8601 date (YYYY-MM-DD), got %q", s)
		}
	}
	return nil
}

// webhookIssueSnapshot is the filter-evaluable slice of an issue row:
// priority / state (+ its group) / project / target_date plus the
// assignee and label sets. assignee_id/label_id match when ANY of the
// issue's assignees/labels satisfies the condition (Plane semantics).
type webhookIssueSnapshot struct {
	priority   int
	stateID    string
	stateGroup string
	projectID  string
	targetDate *string
	assignees  []string
	labels     []string
}

// loadWebhookIssueSnapshot reads the snapshot in one query: the issue
// row joined to its state (for the group) with the assignee/label sets
// aggregated. deleted_at is deliberately NOT excluded — issue.deleted
// events evaluate against the soft-deleted row still present in-tx.
func loadWebhookIssueSnapshot(ctx context.Context, tx pgx.Tx, issueID string) (*webhookIssueSnapshot, error) {
	var s webhookIssueSnapshot
	err := tx.QueryRow(ctx, `
		SELECT i.priority, i.state_id::text, st."group", i.project_id::text,
		       i.target_date::text,
		       COALESCE(array_agg(DISTINCT a.user_id::text) FILTER (WHERE a.user_id IS NOT NULL), '{}'),
		       COALESCE(array_agg(DISTINCT l.label_id::text) FILTER (WHERE l.label_id IS NOT NULL), '{}')
		  FROM issues i
		  JOIN states st ON st.id = i.state_id
		  LEFT JOIN issue_assignees a ON a.issue_id = i.id
		  LEFT JOIN issue_labels l ON l.issue_id = i.id
		 WHERE i.id = $1::uuid
		 GROUP BY i.priority, i.state_id, st."group", i.project_id, i.target_date`,
		issueID).Scan(&s.priority, &s.stateID, &s.stateGroup, &s.projectID,
		&s.targetDate, &s.assignees, &s.labels)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return nil, fmt.Errorf("webhook filter: issue %s not found", issueID)
		}
		return nil, fmt.Errorf("webhook filter: load issue snapshot: %w", err)
	}
	return &s, nil
}

// MatchWebhookFilter reports whether the event's issue satisfies every
// condition in filter. Empty filters and non-work-item events match
// unconditionally (true, nil). Any evaluation failure returns an error
// — the caller treats it as fail-closed (skip the delivery).
func MatchWebhookFilter(ctx context.Context, tx pgx.Tx, filter WebhookFilter, event string, data map[string]any) (bool, error) {
	if len(filter) == 0 {
		return true, nil
	}
	var issueKey string
	switch {
	case strings.HasPrefix(event, "issue."):
		issueKey = "id"
	case strings.HasPrefix(event, "comment."):
		issueKey = "issue_id"
	default:
		// Non-work-item events ignore filters (Plane semantics).
		return true, nil
	}
	rawID, _ := data[issueKey].(string)
	if rawID == "" {
		return false, fmt.Errorf("webhook filter: event %s carries no %q", event, issueKey)
	}
	if _, err := uuid.Parse(rawID); err != nil {
		return false, fmt.Errorf("webhook filter: event %s has malformed issue id: %w", event, err)
	}
	snap, err := loadWebhookIssueSnapshot(ctx, tx, rawID)
	if err != nil {
		return false, err
	}
	for i, c := range filter {
		ok, err := matchWebhookCondition(c, snap)
		if err != nil {
			return false, fmt.Errorf("webhook filter: condition %d: %w", i, err)
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

// matchWebhookCondition evaluates one condition against the snapshot.
// A non-string value (impossible after ValidateWebhookFilter, possible
// on a hand-rolled or corrupt path) is an evaluation failure —
// fail-closed — never a silent match or mismatch.
func matchWebhookCondition(c WebhookCondition, s *webhookIssueSnapshot) (bool, error) {
	var vals []string
	if c.Op == WebhookFilterOpIn {
		arr, ok := c.Value.([]any)
		if !ok {
			return false, fmt.Errorf("op %q has non-array value %T", WebhookFilterOpIn, c.Value)
		}
		for _, v := range arr {
			str, ok := v.(string)
			if !ok {
				return false, fmt.Errorf("op %q has non-string element %T", WebhookFilterOpIn, v)
			}
			vals = append(vals, str)
		}
	} else if c.Op == WebhookFilterOpEq {
		str, ok := c.Value.(string)
		if !ok {
			return false, fmt.Errorf("op %q has non-string value %T", WebhookFilterOpEq, c.Value)
		}
		vals = []string{str}
	} else {
		return false, fmt.Errorf("unknown op %q", c.Op)
	}

	switch c.Field {
	case WebhookFilterFieldPriority:
		for _, v := range vals {
			if n, ok := webhookFilterPriorities[strings.ToLower(v)]; ok && n == s.priority {
				return true, nil
			}
		}
		return false, nil
	case WebhookFilterFieldStateGroup:
		for _, v := range vals {
			if strings.EqualFold(v, s.stateGroup) {
				return true, nil
			}
		}
		return false, nil
	case WebhookFilterFieldStateID:
		return anyEqualFold(vals, s.stateID), nil
	case WebhookFilterFieldProjectID:
		return anyEqualFold(vals, s.projectID), nil
	case WebhookFilterFieldAssigneeID:
		return anyInSetFold(vals, s.assignees), nil
	case WebhookFilterFieldLabelID:
		return anyInSetFold(vals, s.labels), nil
	case WebhookFilterFieldTargetDate:
		// NULL target_date never matches (SQL NULL semantics).
		if s.targetDate == nil {
			return false, nil
		}
		for _, v := range vals {
			if v == *s.targetDate {
				return true, nil
			}
		}
		return false, nil
	default:
		return false, fmt.Errorf("unknown field %q", c.Field)
	}
}

func anyEqualFold(vals []string, target string) bool {
	for _, v := range vals {
		if strings.EqualFold(v, target) {
			return true
		}
	}
	return false
}

// anyInSetFold reports whether any of vals appears in set (the
// assignee_id/label_id ANY semantics).
func anyInSetFold(vals, set []string) bool {
	for _, v := range vals {
		for _, s := range set {
			if strings.EqualFold(v, s) {
				return true
			}
		}
	}
	return false
}

// parseStoredWebhookFilter decodes + validates the webhooks.filter
// JSONB column read at enqueue time. Corrupt data (bad JSON or a
// condition that no longer validates) is an error — the enqueue hook
// treats it as an evaluation failure and skips the delivery
// (fail-closed), never as "no filter".
func parseStoredWebhookFilter(raw []byte, webhookID string) (WebhookFilter, error) {
	var f WebhookFilter
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("webhook %s: corrupt filter: %w", webhookID, err)
	}
	if err := ValidateWebhookFilter(f); err != nil {
		return nil, fmt.Errorf("webhook %s: stored filter invalid: %w", webhookID, err)
	}
	return f, nil
}

// logWebhookFilterSkip records a fail-closed skip at enqueue time.
func logWebhookFilterSkip(webhookID, event, reason string, err error) {
	slog.Warn("webhook: skipping delivery (filter evaluation failed)",
		"webhook_id", webhookID, "event", event, "reason", reason, "error", err)
}
