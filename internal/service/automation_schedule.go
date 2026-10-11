package service

// Scheduled automation triggers (C16T1): time-based rules evaluated by
// a ticker instead of by issue events. Four new trigger types:
//
//   - issue.due_soon: an open issue whose target_date falls within
//     window_hours from now (default DefaultDueSoonWindowHours = 48).
//   - issue.overdue: an open issue whose target_date has passed.
//   - issue.stale: an open issue with no update in stale_days
//     (default DefaultStaleDays = 7).
//   - cycle.ending_soon: an open issue attached to a current cycle
//     whose end_date is within CycleEndingSoonWindowHours = 72h.
//
// "Open" is deliberate: issues in completed/cancelled states are never
// reminded about, and archived or soft-deleted issues are excluded.
//
// Run semantics (from the C16T1 brief): evaluate enabled
// scheduled-trigger rules per project; dedupe via automation_runs — one
// scheduled fire per (rule, issue) per calendar day, so a issue.stale
// rule can never comment every 5 minutes. Rules with no match do
// nothing and record nothing. Each (rule, issue) firing runs in its
// own tx: actions first, then the run row, committed atomically; a
// per-firing failure is logged via slog and never aborts the rest of
// the pass. Scheduled firings have no triggering actor — the rule's
// author (r.CreatedBy) is used for honest attribution on activities
// and comments.
//
// The ticker (internal/ticker/automation_schedule.go) calls
// RunScheduledAutomationPass; it is disabled by default (see
// GLANCE_AUTOMATION_SCHEDULE_INTERVAL). No schema change: the pass
// reuses automation_rules and automation_runs.

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// DefaultDueSoonWindowHours is the issue.due_soon window when the
	// rule sets no window_hours (C16T1 brief: default 48h).
	DefaultDueSoonWindowHours = 48
	// DefaultStaleDays is the issue.stale threshold when the rule sets
	// no stale_days (C16T1 brief: default 7).
	DefaultStaleDays = 7
	// CycleEndingSoonWindowHours is the cycle.ending_soon window:
	// cycles whose end_date is within 72h (C16T1 brief). Fixed — not
	// configurable on the trigger.
	CycleEndingSoonWindowHours = 72
)

// openScheduledIssueSQL is the "live, unfinished work" predicate shared
// by every scheduled trigger: soft-deleted rows are excluded (they are
// never listed anywhere), archived issues are out, and completed /
// cancelled states never get time-based nudges.
const openScheduledIssueSQL = `i.deleted_at IS NULL AND i.archived_at IS NULL
	AND s."group" NOT IN ('completed', 'cancelled')`

// RunScheduledAutomationPass evaluates every enabled scheduled-trigger
// rule on the instance once, firing each (rule, issue) match at most
// once per calendar day. now is the clock (tests pass a fake; the
// ticker passes time.Now()). Never returns a per-firing error — the
// only error is a hard failure selecting the rules; everything else is
// logged and the pass continues.
func RunScheduledAutomationPass(ctx context.Context, pool *pgxpool.Pool, now time.Time) error {
	rows, err := pool.Query(ctx,
		`SELECT `+automationRuleColumns+` FROM automation_rules
		 WHERE enabled
		   AND trigger->>'type' IN ('issue.due_soon','issue.overdue','issue.stale','cycle.ending_soon')
		 ORDER BY created_at, id`)
	if err != nil {
		return err
	}
	var rules []*AutomationRule
	for rows.Next() {
		r, err := scanAutomationRule(rows)
		if err != nil {
			rows.Close()
			return err
		}
		rules = append(rules, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, r := range rules {
		var trig AutomationTrigger
		if err := json.Unmarshal(r.Trigger, &trig); err != nil {
			slog.Warn("automation: scheduled pass skipping rule with corrupt trigger",
				"rule_id", r.ID, "error", err)
			continue
		}
		// Project context for action execution (wsID + identifier +
		// slug — the rule carries only the project id).
		wsID, ident, err := scheduledRuleProject(ctx, pool, r.ProjectID)
		if err != nil {
			slog.Error("automation: scheduled pass cannot resolve rule project; skipping rule",
				"rule_id", r.ID, "error", err)
			continue
		}
		issues, err := matchScheduledIssues(ctx, pool, r.ProjectID, trig, now)
		if err != nil {
			slog.Error("automation: scheduled pass match query failed; skipping rule",
				"rule_id", r.ID, "trigger", trig.Type, "error", err)
			continue
		}
		// Rules with no match do nothing and record nothing.
		for _, issueID := range issues {
			fired, err := scheduledFireRecordedToday(ctx, pool, r.ID, issueID, now)
			if err != nil {
				slog.Error("automation: scheduled pass dedupe check failed; skipping issue",
					"rule_id", r.ID, "issue_id", issueID, "error", err)
				continue
			}
			if fired {
				continue
			}
			fireScheduledRule(ctx, pool, r, trig, wsID, ident, issueID)
		}
	}
	return nil
}

// scheduledRuleProject resolves a rule's project to the (wsID,
// identifier) action execution needs. The slug is unused by actions
// but returned for parity with the service resolution shape.
func scheduledRuleProject(ctx context.Context, pool *pgxpool.Pool, projectID string) (wsID, ident string, err error) {
	err = pool.QueryRow(ctx,
		`SELECT p.workspace_id::text, p.identifier
		 FROM projects p WHERE p.id = $1::uuid`, projectID).Scan(&wsID, &ident)
	return wsID, ident, err
}

// matchScheduledIssues returns the ids of the project's open issues
// matching the scheduled trigger. Unknown types match nothing (they
// could only exist from hand-edited JSONB — the trigger was validated
// at rule-write time).
func matchScheduledIssues(ctx context.Context, pool *pgxpool.Pool, projectID string, trig AutomationTrigger, now time.Time) ([]string, error) {
	var q string
	var args []any
	switch trig.Type {
	case "issue.due_soon":
		window := DefaultDueSoonWindowHours
		if trig.WindowHours != nil {
			window = *trig.WindowHours
		}
		// Due within the window, starting today: already-overdue
		// issues belong to issue.overdue, not here. Both bounds are
		// calendar dates: the upper bound is strict (<), so an issue
		// due on the date the window ends is NOT due_soon (a 24h
		// window at 00:57 does not cover an issue due tomorrow) —
		// date-granularity keeps the window honest against
		// target_date being a DATE column.
		q = `SELECT i.id::text FROM issues i
			 JOIN states s ON s.id = i.state_id
			 WHERE i.project_id = $1::uuid AND ` + openScheduledIssueSQL + `
			   AND i.target_date IS NOT NULL
			   AND i.target_date >= $2::date
			   AND i.target_date < ($2::timestamptz + make_interval(hours => $3))::date
			 ORDER BY i.id`
		args = []any{projectID, now, window}
	case "issue.overdue":
		q = `SELECT i.id::text FROM issues i
			 JOIN states s ON s.id = i.state_id
			 WHERE i.project_id = $1::uuid AND ` + openScheduledIssueSQL + `
			   AND i.target_date IS NOT NULL
			   AND i.target_date < $2::date
			 ORDER BY i.id`
		args = []any{projectID, now}
	case "issue.stale":
		days := DefaultStaleDays
		if trig.StaleDays != nil {
			days = *trig.StaleDays
		}
		q = `SELECT i.id::text FROM issues i
			 JOIN states s ON s.id = i.state_id
			 WHERE i.project_id = $1::uuid AND ` + openScheduledIssueSQL + `
			   AND i.updated_at < $2::timestamptz - make_interval(days => $3)
			 ORDER BY i.id`
		args = []any{projectID, now, days}
	case "cycle.ending_soon":
		// Open issues attached to a current cycle whose end_date is
		// within 72h. DISTINCT: an issue attached to two
		// ending-soon cycles of the same project fires once per day
		// per rule — the dedupe is (rule, issue), not (rule, cycle).
		q = `SELECT DISTINCT i.id::text FROM issues i
			 JOIN cycle_issues ci ON ci.issue_id = i.id
			 JOIN cycles c ON c.id = ci.cycle_id
			 JOIN states s ON s.id = i.state_id
			 WHERE c.project_id = $1::uuid
			   AND c.status = 'current'
			   AND c.end_date >= $2::date
			   AND c.end_date <= $2::date + make_interval(hours => ` +
			strconv.Itoa(CycleEndingSoonWindowHours) + `)
			   AND ` + openScheduledIssueSQL + `
			 ORDER BY 1`
		args = []any{projectID, now}
	default:
		return nil, nil
	}
	rows, err := pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// scheduledFireRecordedToday is the dedupe window: a scheduled rule
// fires for a (rule, issue) pair at most once per calendar day. The
// boundary is UTC (date_trunc on the server clock) — documented choice,
// consistent across the pass.
func scheduledFireRecordedToday(ctx context.Context, pool *pgxpool.Pool, ruleID, issueID string, now time.Time) (bool, error) {
	var fired bool
	err := pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM automation_runs
		 WHERE rule_id = $1::uuid AND issue_id = $2::uuid
		   AND fired_at >= date_trunc('day', $3::timestamptz))`,
		ruleID, issueID, now).Scan(&fired)
	return fired, err
}

// fireScheduledRule runs one (rule, issue) scheduled firing in its own
// tx: the rule's actions, then exactly one automation_runs row,
// committed atomically. Failures are logged, never returned — a broken
// firing must not abort the rest of the pass. The rule's author is the
// actor (no triggering user exists for a scheduled pass).
func fireScheduledRule(ctx context.Context, pool *pgxpool.Pool, r *AutomationRule, trig AutomationTrigger, wsID, ident, issueID string) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		slog.Error("automation: scheduled pass begin tx failed",
			"rule_id", r.ID, "issue_id", issueID, "error", err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// runAutomationRuleActionsTx writes the run row inside this tx and
	// logs action failures itself (never fatal). Notifications have no
	// one to announce to post-commit here: the notification rows were
	// already written in the tx, and there is no HTTP request to fan out
	// to — discarded deliberately.
	_ = runAutomationRuleActionsTx(ctx, tx, wsID, r.ProjectID, ident, issueID, r.CreatedBy, r, trig)
	if err := tx.Commit(ctx); err != nil {
		slog.Error("automation: scheduled pass commit failed",
			"rule_id", r.ID, "issue_id", issueID, "error", err)
	}
}
