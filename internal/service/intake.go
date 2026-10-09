package service

// Intake inbox + triage (Task 20, spec §4).
//
// Every project owns exactly one default intake (seeded by CreateProject
// in the same tx; migration 000011 backfills pre-existing projects).
// Issues opt into the inbox with intake:true on create — one
// intake_issues row in status pending, written in the same tx as the
// issue itself.
//
// Triage state machine:
//   pending ──┬─ accept ──▶ accepted   (issue → project's backlog state)
//             ├─ reject ──▶ rejected   (issue soft-deleted)
//             ├─ snooze ──▶ snoozed    (snoozed_till set; issue untouched)
//             └─ duplicate ─▶ duplicate (issue soft-deleted, duplicate_to_id set)
//   snoozed ──▶ accept / reject / duplicate / re-snooze (new snoozed_till)
//   accepted / rejected / duplicate are TERMINAL: re-applying the SAME
//   action is a no-op success (idempotent-ish), a DIFFERENT action is
//   ErrIntakeAlreadyTriaged (409).
//
// Inbox surfacing rule (GET .../intake): status pending, plus snoozed
// rows whose snoozed_till has passed — those read back with an EFFECTIVE
// status of pending (computed, never written). A background ticker
// (internal/ticker.SnoozeTicker, spec §3) also flips expired rows to
// pending in the database, so the stored state reflects reality between
// reads; the read-time rule stays as the safety net.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Intake status codes — spec §4 vocabulary, SMALLINT in intake_issues.
const (
	IntakePending   int16 = 0
	IntakeRejected  int16 = 1
	IntakeSnoozed   int16 = 2
	IntakeAccepted  int16 = 3
	IntakeDuplicate int16 = 4
)

// intakeBackfillSQL is the idempotent backfill statement from migration
// 000011 (kept as a Go constant so the service test can re-run it).
const intakeBackfillSQL = `INSERT INTO intake (project_id, name, is_default)
SELECT p.id, 'Intake', true
FROM projects p
WHERE NOT EXISTS (
    SELECT 1 FROM intake i WHERE i.project_id = p.id AND i.is_default
)`

var (
	// ErrIntakeNotFound is returned when there is no intake row for the
	// issue in the project's default intake (never triaged, or a malformed
	// UUID that matches nothing).
	ErrIntakeNotFound = errors.New("service: intake issue not found")
	// ErrIntakeAlreadyTriaged is returned when a different triage action is
	// applied to a terminal intake row (accepted/rejected/duplicate).
	ErrIntakeAlreadyTriaged = errors.New("service: intake issue already triaged")
	// ErrSnoozeDatePast is returned when snoozed_till is not in the future.
	ErrSnoozeDatePast = errors.New("service: snoozed_till must be in the future")
	// ErrDuplicateTargetInvalid is returned when duplicate_to_id is not a
	// live issue of the same project (or is the issue itself).
	ErrDuplicateTargetInvalid = errors.New("service: invalid duplicate target")
)

// Intake is a project's intake inbox.
type Intake struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"project_id"`
	Name        string    `json:"name"`
	Description *string   `json:"description,omitempty"`
	IsDefault   bool      `json:"is_default"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// IntakeIssue is one row of the triage inbox.
type IntakeIssue struct {
	ID            string     `json:"id"`
	IssueID       string     `json:"issue_id"`
	Issue         *Issue     `json:"issue,omitempty"`
	Status        int16      `json:"status"`
	StatusName    string     `json:"status_name"`
	SnoozedTill   *time.Time `json:"snoozed_till,omitempty"`
	DuplicateToID *string    `json:"duplicate_to_id,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// EffectiveStatus applies the resurfacing rule: a snoozed item whose
// snoozed_till has passed reads as pending. This is the safety net for
// the window between snooze-ticker passes — the ticker itself rewrites
// the stored status, so this path only triggers for rows it hasn't
// reached yet.
func (ii *IntakeIssue) EffectiveStatus() int16 {
	if ii.Status == IntakeSnoozed && ii.SnoozedTill != nil && !ii.SnoozedTill.After(time.Now()) {
		return IntakePending
	}
	return ii.Status
}

// IntakeStatusName renders the wire-friendly status vocabulary.
func IntakeStatusName(s int16) string {
	switch s {
	case IntakePending:
		return "pending"
	case IntakeRejected:
		return "rejected"
	case IntakeSnoozed:
		return "snoozed"
	case IntakeAccepted:
		return "accepted"
	case IntakeDuplicate:
		return "duplicate"
	default:
		return "unknown"
	}
}

// intakeStatusTerminal reports whether the status is a triage endpoint.
func intakeStatusTerminal(s int16) bool {
	return s == IntakeAccepted || s == IntakeRejected || s == IntakeDuplicate
}

func scanIntake(row pgx.Row) (*Intake, error) {
	var in Intake
	if err := row.Scan(&in.ID, &in.ProjectID, &in.Name, &in.Description,
		&in.IsDefault, &in.CreatedAt, &in.UpdatedAt); err != nil {
		return nil, err
	}
	return &in, nil
}

const intakeColumns = `id::text, project_id::text, name, description, is_default, created_at, updated_at`

// issueColumnsI is issueColumns with the i. table alias (the inbox query
// joins intake_issues/intake/issues, so bare column names would be
// ambiguous). Keep the column ORDER identical to issueColumns —
// scanIntakeRow scans positionally.
const issueColumnsI = `i.id::text, i.project_id::text, i.sequence_id, i.name, i.description,
	i.priority, i.state_id::text, i.parent_id::text, i.sort_order, i.start_date, i.target_date,
	i.estimate_point_id::text, i.is_draft, i.archived_at, i.created_by::text, i.created_at, i.updated_at`

// scanIntakeIssueRow scans one intake_issues row; the issue payload is
// scanned by the caller from the same row (issue columns follow the
// intake columns in the SELECT list).
func scanIntakeIssueRow(row pgx.Row) (*IntakeIssue, error) {
	var ii IntakeIssue
	if err := row.Scan(&ii.ID, &ii.IssueID, &ii.Status, &ii.SnoozedTill,
		&ii.DuplicateToID, &ii.CreatedAt); err != nil {
		return nil, err
	}
	ii.StatusName = IntakeStatusName(ii.Status)
	return &ii, nil
}

// seedIntakeTx inserts the default intake for a new project. Tx-scoped —
// CreateProject calls it in the same tx as the states, the issue
// sequence counter, and the project row itself.
func seedIntakeTx(ctx context.Context, tx pgx.Tx, projectID string) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO intake (project_id, name, is_default) VALUES ($1::uuid, 'Intake', true)`,
		projectID)
	return err
}

// defaultIntakeIDTx resolves the project's default intake inside a tx.
// A miss is data corruption (CreateProject seeds, migration 000011
// backfills) — surfaced as an internal error, never a silent skip.
func defaultIntakeIDTx(ctx context.Context, q queryRower, projectID string) (string, error) {
	var id string
	err := q.QueryRow(ctx,
		`SELECT id::text FROM intake WHERE project_id = $1::uuid AND is_default`,
		projectID).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("service: project %s has no default intake", projectID)
		}
		return "", err
	}
	return id, nil
}

// writeIntakeActivityTx appends one _intake_<action> activity row for a
// triage action. The issue row may be soft-deleted by the time reject /
// duplicate runs — the FK still holds, so the audit trail survives.
func writeIntakeActivityTx(ctx context.Context, tx pgx.Tx, issueID, actorID, field string, newVal any) error {
	nv, _ := json.Marshal(newVal)
	_, err := tx.Exec(ctx,
		`INSERT INTO issue_activities (issue_id, actor_id, field, new_value)
		 VALUES ($1::uuid, $2::uuid, $3, $4::jsonb)`,
		issueID, actorID, field, string(nv))
	return err
}

// GetIntake returns the project's default intake. Any workspace member
// (guest 5+) may read; non-members get ErrNotFound.
func GetIntake(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string) (*Intake, error) {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return nil, err
	}
	_, projectID, _, err := resolveIssueProject(ctx, pool, wsSlug, ident, actorID)
	if err != nil {
		return nil, err
	}
	in, err := scanIntake(pool.QueryRow(ctx,
		`SELECT `+intakeColumns+` FROM intake WHERE project_id = $1::uuid AND is_default`,
		projectID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("service: project %s has no default intake", projectID)
		}
		return nil, err
	}
	return in, nil
}

// ListIntakeIssues returns the triage inbox: pending items plus snoozed
// items whose snoozed_till has passed (resurfaced — EffectiveStatus()
// reads pending). Terminal rows (accepted/rejected/duplicate) never
// appear, and neither do archived issues (archived work is out of
// triage). Ordered by arrival (created_at ASC) — triage in arrival order.
// limit is clamped to 1..100. Any workspace member may read.
func ListIntakeIssues(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string, limit int) ([]IntakeIssue, error) {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return nil, err
	}
	if limit < 1 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	_, projectID, _, err := resolveIssueProject(ctx, pool, wsSlug, ident, actorID)
	if err != nil {
		return nil, err
	}

	rows, err := pool.Query(ctx,
		`SELECT ii.id::text, ii.issue_id::text, ii.status, ii.snoozed_till,
		        ii.duplicate_to_id::text, ii.created_at, `+issueColumnsI+`
		 FROM intake_issues ii
		 JOIN intake t ON t.id = ii.intake_id AND t.is_default AND t.project_id = $1::uuid
		 JOIN issues i ON i.id = ii.issue_id AND i.deleted_at IS NULL AND i.archived_at IS NULL
		 WHERE ii.status = $2 OR (ii.status = $3 AND ii.snoozed_till <= now())
		 ORDER BY ii.created_at ASC, ii.id ASC
		 LIMIT $4`,
		projectID, IntakePending, IntakeSnoozed, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out, err := scanIntakeIssues(rows, ident)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// scanIntakeIssues scans the shared intake_issues + issues column list
// (SELECT must list the intake columns first, then issueColumnsI).
func scanIntakeIssues(rows pgx.Rows, ident string) ([]IntakeIssue, error) {
	out := []IntakeIssue{}
	for rows.Next() {
		var ii IntakeIssue
		var iss Issue
		var description []byte
		if err := rows.Scan(
			&ii.ID, &ii.IssueID, &ii.Status, &ii.SnoozedTill,
			&ii.DuplicateToID, &ii.CreatedAt,
			&iss.ID, &iss.ProjectID, &iss.SequenceID, &iss.Name,
			&description,
			&iss.Priority, &iss.StateID, &iss.ParentID, &iss.SortOrder,
			&iss.StartDate, &iss.TargetDate, &iss.EstimatePointID,
			&iss.IsDraft, &iss.ArchivedAt,
			&iss.CreatedBy, &iss.CreatedAt, &iss.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if description != nil {
			iss.Description = json.RawMessage(description)
		}
		iss.DisplayID = ident + "-" + strconv.Itoa(iss.SequenceID)
		ii.StatusName = IntakeStatusName(ii.Status)
		ii.Issue = &iss
		out = append(out, ii)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ListSnoozedIntakeIssues returns the still-snoozed inbox items: status
// snoozed with snoozed_till in the future. Ordered by snoozed_till ASC —
// the one waking up soonest first. Any workspace member may read.
// (Task 21: powers the web inbox's "Snoozed" section; the main inbox
// query deliberately excludes these.)
func ListSnoozedIntakeIssues(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string, limit int) ([]IntakeIssue, error) {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return nil, err
	}
	if limit < 1 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	_, projectID, _, err := resolveIssueProject(ctx, pool, wsSlug, ident, actorID)
	if err != nil {
		return nil, err
	}

	rows, err := pool.Query(ctx,
		`SELECT ii.id::text, ii.issue_id::text, ii.status, ii.snoozed_till,
		        ii.duplicate_to_id::text, ii.created_at, `+issueColumnsI+`
		 FROM intake_issues ii
		 JOIN intake t ON t.id = ii.intake_id AND t.is_default AND t.project_id = $1::uuid
		 JOIN issues i ON i.id = ii.issue_id AND i.deleted_at IS NULL AND i.archived_at IS NULL
		 WHERE ii.status = $2 AND ii.snoozed_till > now()
		 ORDER BY ii.snoozed_till ASC, ii.id ASC
		 LIMIT $3`,
		projectID, IntakeSnoozed, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanIntakeIssues(rows, ident)
}

// intakeRow is the locked triage row plus the issue's liveness.
type intakeRow struct {
	id            string
	issueID       string
	status        int16
	snoozedTill   *time.Time
	duplicateToID *string
	issueDeleted  bool
	createdAt     time.Time
}

// lockIntakeIssueTx locks the intake_issues row for (project, issue) for
// update. The issues join is a LEFT JOIN and does NOT filter deleted_at:
// terminal rows reference soft-deleted issues, and re-applying the same
// action must find the row to no-op. A malformed UUID or a missing row
// (never triaged, or another project's issue) is ErrIntakeNotFound — the
// join on the default intake keeps this tenancy-tight.
func lockIntakeIssueTx(ctx context.Context, tx pgx.Tx, projectID, issueID string) (*intakeRow, error) {
	var r intakeRow
	err := tx.QueryRow(ctx,
		`SELECT ii.id::text, ii.issue_id::text, ii.status, ii.snoozed_till,
		        ii.duplicate_to_id::text, (i.id IS NOT NULL AND i.deleted_at IS NOT NULL), ii.created_at
		 FROM intake_issues ii
		 JOIN intake t ON t.id = ii.intake_id AND t.is_default AND t.project_id = $1::uuid
		 LEFT JOIN issues i ON i.id = ii.issue_id AND i.project_id = $1::uuid
		 WHERE ii.issue_id = $2::uuid
		 FOR UPDATE OF ii`,
		projectID, issueID).Scan(
		&r.id, &r.issueID, &r.status, &r.snoozedTill,
		&r.duplicateToID, &r.issueDeleted, &r.createdAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return nil, ErrIntakeNotFound
		}
		return nil, err
	}
	return &r, nil
}

// markIntakeTx rewrites the triage row's status and side columns.
func markIntakeTx(ctx context.Context, tx pgx.Tx, id string, status int16, snoozedTill *time.Time, duplicateToID *string) error {
	_, err := tx.Exec(ctx,
		`UPDATE intake_issues
		 SET status = $2, snoozed_till = $3, duplicate_to_id = $4::uuid, updated_at = now()
		 WHERE id = $1::uuid`,
		id, status, snoozedTill, duplicateToID)
	return err
}

// readIntakeIssueTx re-reads the triage row after mutation for the response.
func readIntakeIssueTx(ctx context.Context, tx pgx.Tx, id string) (*IntakeIssue, error) {
	ii, err := scanIntakeIssueRow(tx.QueryRow(ctx,
		`SELECT id::text, issue_id::text, status, snoozed_till, duplicate_to_id::text, created_at
		 FROM intake_issues WHERE id = $1::uuid`, id))
	if err != nil {
		return nil, err
	}
	return ii, nil
}

// triageTx opens the transaction shared by all four actions: resolve the
// project + member+ role gate, then run fn inside the tx.
func triageTx(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string, fn func(tx pgx.Tx, projectID, ident string) (*IntakeIssue, []*Notification, error)) (*IntakeIssue, error) {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return nil, err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	_, projectID, role, err := resolveIssueProject(ctx, tx, wsSlug, ident, actorID)
	if err != nil {
		return nil, err
	}
	if role < RoleMember {
		return nil, ErrForbidden
	}

	ii, fnNotified, err := fn(tx, projectID, ident)
	if err != nil {
		return nil, err
	}
	// Non-accept triage actions (reject / snooze / duplicate) notify the
	// issue's watchers as intake.triaged. Accept is covered by the
	// issue.state_changed notification updateIssueTx already wrote — the
	// state visibly changed to backlog. (If the state didn't change, there
	// is nothing new for watchers.)
	var triageNotified []*Notification
	if ii.Status != IntakeAccepted {
		watchers, err := issueWatchersTx(ctx, tx, ii.IssueID)
		if err != nil {
			return nil, err
		}
		displayID, name, err := issueNotifyContextTx(ctx, tx, ident, ii.IssueID)
		if err != nil {
			return nil, err
		}
		actorName := actorDisplayName(ctx, tx, actorID)
		triageNotified, err = notifyTx(ctx, tx, NotifyIntakeTriaged,
			fmt.Sprintf("%s marked %s as %s in intake", actorName, displayID, ii.StatusName),
			fmt.Sprintf("Issue: %s", name),
			map[string]any{
				"intake_issue_id": ii.ID,
				"issue_id":        ii.IssueID,
				"display_id":      displayID,
				"issue_name":      name,
				"status":          ii.Status,
				"status_name":     ii.StatusName,
				"actor_id":        actorID,
				"project_id":      projectID,
			},
			actorID, watchers)
		if err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	// One interception point for all triage actions (accept / reject /
	// snooze / duplicate): the intake issue changed.
	announce(
		[]string{projectChannel(wsSlug, ident), workspaceChannel(wsSlug)},
		EventIntakeUpdated,
		map[string]any{
			"id":          ii.ID,
			"issue_id":    ii.IssueID,
			"status":      ii.Status,
			"status_name": ii.StatusName,
		},
	)
	announceNotifications(append(fnNotified, triageNotified...))
	return ii, nil
}

// AcceptIntakeIssue triages the intake issue into the project: the
// issue's state becomes the project's backlog-group state and the intake
// row is marked accepted. Re-accepting is a no-op success; a different
// action on the terminal row is ErrIntakeAlreadyTriaged.
func AcceptIntakeIssue(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, actorID string) (*IntakeIssue, error) {
	return triageTx(ctx, pool, wsSlug, identifier, actorID, func(tx pgx.Tx, projectID, ident string) (*IntakeIssue, []*Notification, error) {
		r, err := lockIntakeIssueTx(ctx, tx, projectID, issueID)
		if err != nil {
			return nil, nil, err
		}
		if r.status == IntakeAccepted {
			ii, err := readIntakeIssueTx(ctx, tx, r.id)
			if err != nil {
				return nil, nil, err
			}
			return ii, nil, nil
		}
		if intakeStatusTerminal(r.status) {
			return nil, nil, ErrIntakeAlreadyTriaged
		}
		if r.issueDeleted {
			return nil, nil, ErrIntakeNotFound
		}

		backlog, err := backlogState(ctx, tx, projectID)
		if err != nil {
			return nil, nil, err
		}
		// updateIssueTx writes the state_id activity row only when the
		// state actually changes, plus a version snapshot of the
		// post-mutation row. Its state-change notifications ride along to
		// triageTx, which broadcasts them after commit.
		_, notified, err := updateIssueTx(ctx, tx, projectID, ident, r.issueID, actorID,
			IssuePatch{StateID: &backlog})
		if err != nil {
			return nil, nil, err
		}
		if err := writeIntakeActivityTx(ctx, tx, r.issueID, actorID, "_intake_accepted",
			map[string]any{"status": "accepted"}); err != nil {
			return nil, nil, err
		}
		if err := markIntakeTx(ctx, tx, r.id, IntakeAccepted, nil, nil); err != nil {
			return nil, nil, err
		}
		ii, err := readIntakeIssueTx(ctx, tx, r.id)
		if err != nil {
			return nil, nil, err
		}
		return ii, notified, nil
	})
}

// RejectIntakeIssue triages the intake issue out: the intake row is
// marked rejected and the issue itself is soft-deleted (reads report
// ErrIssueNotFound, including a second reject — which is a no-op).
func RejectIntakeIssue(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, actorID string) (*IntakeIssue, error) {
	return triageTx(ctx, pool, wsSlug, identifier, actorID, func(tx pgx.Tx, projectID, ident string) (*IntakeIssue, []*Notification, error) {
		r, err := lockIntakeIssueTx(ctx, tx, projectID, issueID)
		if err != nil {
			return nil, nil, err
		}
		if r.status == IntakeRejected {
			ii, err := readIntakeIssueTx(ctx, tx, r.id)
			if err != nil {
				return nil, nil, err
			}
			return ii, nil, nil
		}
		if intakeStatusTerminal(r.status) {
			return nil, nil, ErrIntakeAlreadyTriaged
		}

		// The issue may already be gone (deleted directly while pending):
		// deleteIssueTx reports ErrIssueNotFound then; the triage intent
		// ("out of the inbox") still holds, so tolerate it.
		if err := deleteIssueTx(ctx, tx, projectID, ident, r.issueID, actorID); err != nil &&
			!errors.Is(err, ErrIssueNotFound) {
			return nil, nil, err
		}
		if err := writeIntakeActivityTx(ctx, tx, r.issueID, actorID, "_intake_rejected",
			map[string]any{"status": "rejected"}); err != nil {
			return nil, nil, err
		}
		if err := markIntakeTx(ctx, tx, r.id, IntakeRejected, nil, nil); err != nil {
			return nil, nil, err
		}
		ii, err := readIntakeIssueTx(ctx, tx, r.id)
		if err != nil {
			return nil, nil, err
		}
		return ii, nil, nil
	})
}

// SnoozeIntakeIssue parks the intake issue until till (which must be in
// the future — validated before any DB work). Re-snoozing updates the
// date; re-snoozing with the same date (to the second) is a no-op.
func SnoozeIntakeIssue(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, actorID string, till time.Time) (*IntakeIssue, error) {
	if !till.After(time.Now()) {
		return nil, ErrSnoozeDatePast
	}
	return triageTx(ctx, pool, wsSlug, identifier, actorID, func(tx pgx.Tx, projectID, ident string) (*IntakeIssue, []*Notification, error) {
		r, err := lockIntakeIssueTx(ctx, tx, projectID, issueID)
		if err != nil {
			return nil, nil, err
		}
		if r.status == IntakeSnoozed && r.snoozedTill != nil &&
			r.snoozedTill.Truncate(time.Second).Equal(till.Truncate(time.Second)) {
			ii, err := readIntakeIssueTx(ctx, tx, r.id)
			if err != nil {
				return nil, nil, err
			}
			return ii, nil, nil
		}
		if intakeStatusTerminal(r.status) {
			return nil, nil, ErrIntakeAlreadyTriaged
		}
		if r.issueDeleted {
			return nil, nil, ErrIntakeNotFound
		}

		tillUTC := till.UTC()
		if err := writeIntakeActivityTx(ctx, tx, r.issueID, actorID, "_intake_snoozed",
			map[string]any{"status": "snoozed", "snoozed_till": tillUTC.Format(time.RFC3339)}); err != nil {
			return nil, nil, err
		}
		if err := markIntakeTx(ctx, tx, r.id, IntakeSnoozed, &tillUTC, nil); err != nil {
			return nil, nil, err
		}
		ii, err := readIntakeIssueTx(ctx, tx, r.id)
		if err != nil {
			return nil, nil, err
		}
		return ii, nil, nil
	})
}

// DuplicateIntakeIssue marks the intake issue a duplicate of targetID — a
// live issue of the same project — and soft-deletes the dupe itself.
// Marking duplicate of the same target twice is a no-op; a different
// target (or any other action) on the terminal row is
// ErrIntakeAlreadyTriaged.
func DuplicateIntakeIssue(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, actorID, targetID string) (*IntakeIssue, error) {
	target := strings.ToLower(strings.TrimSpace(targetID))
	return triageTx(ctx, pool, wsSlug, identifier, actorID, func(tx pgx.Tx, projectID, ident string) (*IntakeIssue, []*Notification, error) {
		r, err := lockIntakeIssueTx(ctx, tx, projectID, issueID)
		if err != nil {
			return nil, nil, err
		}
		if r.status == IntakeDuplicate && r.duplicateToID != nil && *r.duplicateToID == target {
			ii, err := readIntakeIssueTx(ctx, tx, r.id)
			if err != nil {
				return nil, nil, err
			}
			return ii, nil, nil
		}
		if intakeStatusTerminal(r.status) {
			return nil, nil, ErrIntakeAlreadyTriaged
		}
		if r.issueDeleted {
			return nil, nil, ErrIntakeNotFound
		}

		// The target must be a live issue of this project and not the
		// issue itself.
		if target == "" || strings.EqualFold(target, r.issueID) {
			return nil, nil, ErrDuplicateTargetInvalid
		}
		var one int
		err = tx.QueryRow(ctx,
			`SELECT 1 FROM issues
			 WHERE id = $1::uuid AND project_id = $2::uuid AND deleted_at IS NULL`,
			target, projectID).Scan(&one)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
				return nil, nil, ErrDuplicateTargetInvalid
			}
			return nil, nil, err
		}

		if err := deleteIssueTx(ctx, tx, projectID, ident, r.issueID, actorID); err != nil &&
			!errors.Is(err, ErrIssueNotFound) {
			return nil, nil, err
		}
		if err := writeIntakeActivityTx(ctx, tx, r.issueID, actorID, "_intake_duplicate",
			map[string]any{"status": "duplicate", "duplicate_to_id": target}); err != nil {
			return nil, nil, err
		}
		if err := markIntakeTx(ctx, tx, r.id, IntakeDuplicate, nil, &target); err != nil {
			return nil, nil, err
		}
		ii, err := readIntakeIssueTx(ctx, tx, r.id)
		if err != nil {
			return nil, nil, err
		}
		return ii, nil, nil
	})
}

// addIntakeIssueTx writes the pending intake_issues row for a newly
// created issue. Called from CreateIssue inside its tx when
// CreateIssueInput.Intake is set.
func addIntakeIssueTx(ctx context.Context, tx pgx.Tx, projectID, issueID string) error {
	intakeID, err := defaultIntakeIDTx(ctx, tx, projectID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO intake_issues (intake_id, issue_id, status)
		 VALUES ($1::uuid, $2::uuid, $3)`,
		intakeID, issueID, IntakePending)
	return err
}
