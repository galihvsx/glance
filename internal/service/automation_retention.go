package service

// Automation run retention (C13T0): closes the documented C12T1 v1 note
// ("no retention pruning, documented honestly" — see migration 000038).
// Every rule firing appends a row to automation_runs forever, so a
// workspace with active automations grows unboundedly; the sweep below
// bounds it.
//
// Policy is per project, stored on the projects row (migration 000039),
// following the archive_in_days / close_in_days convention: NULL =
// unset = DefaultAutomationRunRetentionDays; 0 = keep forever; negative
// is rejected at write (ErrInvalidAutomationRunRetentionDays). No
// per-rule configuration in v1 (documented honestly).
//
// Execution ethos (same as the digest scheduling): the sweep runs in a
// scheduled background pass (ticker.RetentionTicker, daily) — never on
// an HTTP hot path, never inside the firing transaction, so a prune
// failure can never block a state change. A per-project failure is
// logged via slog and the sweep CONTINUES with the next project; the
// pass returns the joined error for the ticker to log. Prune failures
// are never fatal.

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DefaultAutomationRunRetentionDays is the prune window for projects
// whose automation_run_retention_days column is NULL (unset). Pinned by
// test.
const DefaultAutomationRunRetentionDays = 90

// ErrInvalidAutomationRunRetentionDays is returned when the retention
// window is negative. The handler maps it to 400 bad_request.
var ErrInvalidAutomationRunRetentionDays = errors.New("service: automation_run_retention_days must be >= 0")

// retentionTarget is one project's prune job. retentionDays is the
// effective window: the column, or DefaultAutomationRunRetentionDays
// when NULL. Retention 0 (keep forever) never produces a target.
type retentionTarget struct {
	projectID     string
	retentionDays int
}

// pruneTargets scans every project with a non-zero effective retention
// window. One batched query, no row-by-row work.
func pruneTargets(ctx context.Context, pool *pgxpool.Pool) ([]retentionTarget, error) {
	rows, err := pool.Query(ctx,
		`SELECT id::text, COALESCE(automation_run_retention_days, $1)::int
		   FROM projects
		  WHERE COALESCE(automation_run_retention_days, $1)::int > 0`,
		DefaultAutomationRunRetentionDays)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []retentionTarget
	for rows.Next() {
		var t retentionTarget
		if err := rows.Scan(&t.projectID, &t.retentionDays); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// PruneAutomationRuns deletes automation runs older than each project's
// retention window — one DELETE per project, bounded work. The explicit
// now makes the cutoff deterministic under a test clock; production
// passes time.Now(). Returns the total rows deleted.
//
// A per-project failure is logged and the sweep CONTINUES with the next
// project; the returned error joins them (errors.Join) for the ticker to
// log. It is never fatal: the sweep never shares a transaction with the
// firing path (recordAutomationRunTx), so a prune failure cannot block
// a state change — and the ticker loop survives a failed pass.
func PruneAutomationRuns(ctx context.Context, pool *pgxpool.Pool, now time.Time) (int64, error) {
	targets, err := pruneTargets(ctx, pool)
	if err != nil {
		return 0, err
	}
	var total int64
	var errs []error
	for _, t := range targets {
		cutoff := now.AddDate(0, 0, -t.retentionDays)
		n, err := pruneProjectRuns(ctx, pool, t.projectID, cutoff)
		if err != nil {
			// Logged, never fatal: the next project still prunes, and
			// the joined error goes to the ticker log, not the pager.
			slog.Error("automation: retention prune failed; continuing with next project",
				"project_id", t.projectID, "retention_days", t.retentionDays, "error", err)
			errs = append(errs, err)
			continue
		}
		total += n
	}
	return total, errors.Join(errs...)
}

// pruneProjectRuns deletes one project's runs fired before cutoff. The
// (rule_id, fired_at) index serves the predicate through the
// automation_rules project lookup; a single DELETE, no per-run work.
func pruneProjectRuns(ctx context.Context, pool *pgxpool.Pool, projectID string, cutoff time.Time) (int64, error) {
	ct, err := pool.Exec(ctx,
		`DELETE FROM automation_runs r
		  USING automation_rules ar
		  WHERE r.rule_id = ar.id
		    AND ar.project_id = $1::uuid
		    AND r.fired_at < $2`,
		projectID, cutoff)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}

// GetAutomationRunRetentionDays returns the project's effective run
// retention window in days: the column, or DefaultAutomationRunRetentionDays
// when unset (NULL); 0 means keep forever. Member (15)+ — the same read
// auth as the runs list: guests get ErrForbidden, non-members
// ErrNotFound.
func GetAutomationRunRetentionDays(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string) (int, error) {
	_, projectID, role, err := resolveAutomationProject(ctx, pool, wsSlug, identifier, actorID)
	if err != nil {
		return 0, err
	}
	if role < RoleMember {
		return 0, ErrForbidden
	}
	var retention *int
	if err := pool.QueryRow(ctx,
		`SELECT automation_run_retention_days FROM projects WHERE id = $1::uuid`,
		projectID).Scan(&retention); err != nil {
		return 0, err
	}
	if retention == nil {
		return DefaultAutomationRunRetentionDays, nil
	}
	return *retention, nil
}
