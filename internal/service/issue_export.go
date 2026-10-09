package service

// Issues export (C8T3): stream the project's filtered working set as
// export rows. Filter/scope semantics are shared with ListIssues via
// issueListConds, so what the user sees in the list is what the export
// contains. Rows stream from a single pgx cursor — the full set is never
// materialized in memory.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// MaxExportRows caps the rows a single export request may stream. It is a
// var (not a const) so tests can exercise the over-cap path without
// seeding 50k rows; production code must not mutate it.
var MaxExportRows = 50000

// ErrExportTooLarge is returned before a single row is streamed when the
// export filter matches more than MaxExportRows issues. The error text
// names the matched count so the caller can answer honestly (400 with the
// count — never a silently truncated file).
var ErrExportTooLarge = errors.New("service: export row limit exceeded")

// ExportIssue is one streamed export row: the list item (the exact shape
// the list endpoint returns, description always included) plus the
// human-readable extras the CSV columns need. The extras are json:"-":
// JSON export marshals only the embedded IssueListItem.
type ExportIssue struct {
	IssueListItem
	StateName string `json:"-"`
	// EstimateKey is the estimate point's key (e.g. "3", "5").
	EstimateKey string `json:"-"`
	// AssigneeEmails are the assignees' emails in agg order, for the CSV
	// "assignee" column (emails round-trip through the CSV importer).
	AssigneeEmails []string `json:"-"`
}

// priorityWords maps the 0-4 priority int to the word form the CSV
// importer accepts (none/low/medium/high/urgent), so exports round-trip
// through imports.
var priorityWords = []string{"none", "low", "medium", "high", "urgent"}

// PriorityWord renders a 0-4 priority as its export word.
func PriorityWord(p int) string {
	if p < 0 || p > 4 {
		return strconv.Itoa(p)
	}
	return priorityWords[p]
}

// ExportScope validates the export request's scope without streaming any
// rows: identifier syntax, the caller's workspace membership, and filter
// value formats. Any workspace member (guests included) may export — the
// same read gate as the issue list. It returns the normalized identifier
// (safe for filenames) for the Content-Disposition header.
func ExportScope(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string, in ListIssuesInput) (string, error) {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return "", err
	}
	if err := validateListFilterValues(in); err != nil {
		return "", err
	}
	if _, _, _, err := resolveIssueProject(ctx, pool, wsSlug, ident, actorID); err != nil {
		return "", err
	}
	return ident, nil
}

// exportWhere builds the WHERE clause shared by the export count and the
// export select. arg numbering restarts per query (each query gets its own
// args slice).
func exportWhere(in ListIssuesInput, projectID, actorID string) (string, []any) {
	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	return strings.Join(issueListConds(in, projectID, actorID, arg), " AND "), args
}

// checkExportCap counts the filtered working set and rejects over-cap
// exports before any row is streamed.
func checkExportCap(ctx context.Context, pool *pgxpool.Pool, where string, args []any) error {
	var n int64
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM issues i WHERE `+where, args...).Scan(&n); err != nil {
		return err
	}
	if n > int64(MaxExportRows) {
		return fmt.Errorf("%w: %d issues match, cap is %d — narrow the filters and retry",
			ErrExportTooLarge, n, MaxExportRows)
	}
	return nil
}

// StreamExportIssues streams the filtered working set in display-ID
// (sequence_id) order, one ExportIssue per yield call. The row cap is
// enforced up front: ErrExportTooLarge is returned before the first row
// is yielded, so handlers can still answer 400. Returning a non-nil error
// from yield aborts the stream.
func StreamExportIssues(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string, in ListIssuesInput, yield func(*ExportIssue) error) error {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return err
	}
	if err := validateListFilterValues(in); err != nil {
		return err
	}
	_, projectID, _, err := resolveIssueProject(ctx, pool, wsSlug, ident, actorID)
	if err != nil {
		return err
	}
	where, args := exportWhere(in, projectID, actorID)
	if err := checkExportCap(ctx, pool, where, args); err != nil {
		return err
	}

	// Column order mirrors ListIssues, then the export extras: state
	// name, estimate key, and the relation aggregates. Description is
	// always selected (exports are not a working-set view; sparse
	// fieldsets don't apply).
	cols := `i.id::text, i.project_id::text, i.sequence_id, i.name, i.description,
		i.priority, i.state_id::text, i.parent_id::text, i.sort_order, i.start_date, i.target_date,
		i.estimate_point_id::text, i.is_draft, i.archived_at, i.created_by::text, i.created_at, i.updated_at,
		s.name, ep.key, ` + assigneesAgg + ` AS assignees, ` + labelsAgg + ` AS labels`
	query := `SELECT ` + cols + ` FROM issues i
		LEFT JOIN states s ON s.id = i.state_id
		LEFT JOIN estimate_points ep ON ep.id = i.estimate_point_id
		WHERE ` + where + `
		ORDER BY i.sequence_id ASC, i.id ASC`

	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var item ExportIssue
		var desc []byte
		var stateName, estimateKey *string
		var assigneesJSON, labelsJSON []byte
		if err := rows.Scan(
			&item.ID, &item.ProjectID, &item.SequenceID, &item.Name,
			&desc,
			&item.Priority, &item.StateID, &item.ParentID, &item.SortOrder,
			&item.StartDate, &item.TargetDate, &item.EstimatePointID,
			&item.IsDraft, &item.ArchivedAt,
			&item.CreatedBy, &item.CreatedAt, &item.UpdatedAt,
			&stateName, &estimateKey,
			&assigneesJSON, &labelsJSON,
		); err != nil {
			return err
		}
		if desc != nil {
			item.Description = json.RawMessage(desc)
		}
		if stateName != nil {
			item.StateName = *stateName
		}
		if estimateKey != nil {
			item.EstimateKey = *estimateKey
		}
		if err := unmarshalRelations(assigneesJSON, labelsJSON, &item.IssueListItem); err != nil {
			return err
		}
		// The aggregate carries emails (ignored by unmarshalRelations);
		// decode them separately for the CSV assignee column.
		var withEmails []struct {
			Email string `json:"email"`
		}
		if err := json.Unmarshal(assigneesJSON, &withEmails); err != nil {
			return fmt.Errorf("service: decode assignee emails: %w", err)
		}
		for _, a := range withEmails {
			if a.Email != "" {
				item.AssigneeEmails = append(item.AssigneeEmails, a.Email)
			}
		}
		item.DisplayID = ident + "-" + strconv.Itoa(item.SequenceID)
		if err := yield(&item); err != nil {
			return err
		}
	}
	return rows.Err()
}
