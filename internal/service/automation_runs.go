package service

// Automation run history (C12T1): observability for workflow
// automations. Every rule firing writes exactly one run row in the same
// tx as the state change (see runAutomationRulesTx), recording the
// per-action ok/error outcome — the answer to "why did this issue
// change?".
//
// Write ethos (same as actions): a failed action records ok:false with
// its error text and the state change still commits. The run row is
// still written. A run-row insert failure itself is logged, never
// fatal — it must never block the state change it records.
//
// Read ethos: member (15)+, same read auth as the automation rules.
// Newest first. Deleting a rule cascades its runs (FK).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AutomationRunDefaultLimit is the page size when the caller passes no
// limit; AutomationRunMaxLimit caps it. A limit above the max is
// clamped, not rejected (AC4); a limit below 1 is ErrInvalidAutomationRunLimit.
const (
	AutomationRunDefaultLimit = 20
	AutomationRunMaxLimit     = 100
)

// ErrInvalidAutomationRunLimit is returned when the runs list `limit`
// is below 1. The handler maps it to 400 bad_request.
var ErrInvalidAutomationRunLimit = errors.New("service: invalid automation run limit")

// AutomationActionResult is one fired action's recorded outcome: ok
// with no error on success, ok:false with the error text on failure.
type AutomationActionResult struct {
	Type  string  `json:"type"`
	OK    bool    `json:"ok"`
	Error *string `json:"error,omitempty"`
}

// AutomationRun is one recorded rule firing. IssueDisplayID is derived
// ({IDENTIFIER}-{sequence_id}, e.g. ENG-123) and null only if the issue
// row is gone entirely (issues are soft-deleted, so in practice this
// is always present).
type AutomationRun struct {
	ID             string          `json:"id"`
	RuleID         string          `json:"rule_id"`
	RuleName       string          `json:"rule_name"`
	IssueID        string          `json:"issue_id"`
	IssueDisplayID *string         `json:"issue_display_id"`
	TriggerType    string          `json:"trigger_type"`
	FiredAt        time.Time       `json:"fired_at"`
	Actions        json.RawMessage `json:"actions"`
}

// AutomationRunFilter narrows the runs list. RuleID/IssueID are
// validated UUIDs by the handler; Limit 0 means the default.
type AutomationRunFilter struct {
	RuleID  string
	IssueID string
	Limit   int
}

const automationRunColumns = `r.id::text, r.rule_id::text, r.issue_id::text,
	r.trigger_type, r.fired_at, r.actions, ar.name, i.sequence_id`

func scanAutomationRun(row pgx.Row, ident string) (*AutomationRun, error) {
	var r AutomationRun
	var seq *int32 // **int32: a NULL sequence (issue row gone) scans to nil
	if err := row.Scan(
		&r.ID, &r.RuleID, &r.IssueID, &r.TriggerType, &r.FiredAt,
		&r.Actions, &r.RuleName, &seq,
	); err != nil {
		return nil, err
	}
	if seq != nil {
		d := fmt.Sprintf("%s-%d", ident, *seq)
		r.IssueDisplayID = &d
	}
	return &r, nil
}

// ListAutomationRuns returns the project's automation run history,
// newest first. Member (15)+; guests get ErrForbidden, non-members
// ErrNotFound — the same read auth as ListAutomationRules.
func ListAutomationRuns(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string, f AutomationRunFilter) ([]*AutomationRun, error) {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return nil, err
	}
	_, projectID, role, err := resolveAutomationProject(ctx, pool, wsSlug, identifier, actorID)
	if err != nil {
		return nil, err
	}
	if role < RoleMember {
		return nil, ErrForbidden
	}
	limit := f.Limit
	if limit == 0 {
		limit = AutomationRunDefaultLimit
	}
	if limit < 1 {
		return nil, ErrInvalidAutomationRunLimit
	}
	if limit > AutomationRunMaxLimit {
		limit = AutomationRunMaxLimit // clamped, not rejected
	}

	var sb strings.Builder
	args := []any{projectID}
	sb.WriteString(`SELECT ` + automationRunColumns + `
		FROM automation_runs r
		JOIN automation_rules ar ON ar.id = r.rule_id
		LEFT JOIN issues i ON i.id = r.issue_id
		WHERE ar.project_id = $1::uuid`)
	if f.RuleID != "" {
		args = append(args, f.RuleID)
		fmt.Fprintf(&sb, ` AND r.rule_id = $%d::uuid`, len(args))
	}
	if f.IssueID != "" {
		args = append(args, f.IssueID)
		fmt.Fprintf(&sb, ` AND r.issue_id = $%d::uuid`, len(args))
	}
	args = append(args, limit)
	fmt.Fprintf(&sb, ` ORDER BY r.fired_at DESC, r.id DESC LIMIT $%d`, len(args))

	rows, err := pool.Query(ctx, sb.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*AutomationRun{}
	for rows.Next() {
		r, err := scanAutomationRun(rows, ident)
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

// recordAutomationRunTx inserts one run row in the caller's tx. It
// never returns an error: a run-row write must not block the state
// change it records. Marshal/insert failures are logged via slog and
// skipped. (No retention pruning in v1 — documented in migration
// 000038.)
func recordAutomationRunTx(ctx context.Context, tx pgx.Tx, ruleID, issueID, triggerType string, results []AutomationActionResult) {
	payload, err := json.Marshal(results)
	if err != nil {
		slog.Error("automation: run row marshal failed; skipping run row",
			"rule_id", ruleID, "issue_id", issueID, "error", err)
		return
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO automation_runs (rule_id, issue_id, trigger_type, actions)
		 VALUES ($1::uuid, $2::uuid, $3, $4::jsonb)`,
		ruleID, issueID, triggerType, string(payload)); err != nil {
		slog.Error("automation: run row insert failed; state change proceeds",
			"rule_id", ruleID, "issue_id", issueID, "error", err)
	}
}
