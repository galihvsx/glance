package service

// CSV issue importer (C4T8).
//
// Design choice: TWO-PHASE. Phase 1 validates every row (per-row errors
// collected, never aborting the batch). Phase 2 inserts all valid rows
// in ONE transaction. Rationale: a single transaction with rollback on
// the first validation failure would make one bad row kill the whole
// import; per-row transactions would be N round-trips and leave partial
// imports on mid-batch DB failures. Two-phase gives per-row error
// reporting AND atomic inserts.
//
// Mapping: the client sends {"title": "<csv header>", ...} naming which
// CSV column feeds each issue field. Only "title" is required. Unknown
// CSV columns are ignored. Reference fields resolve by NAME against
// pre-loaded lookup maps (one query each, no N+1):
//   - state: case-insensitive state name → id; unknown → per-row error
//   - priority: "none"|"low"|"medium"|"high"|"urgent" (case-insensitive)
//     or 0-4; unknown → per-row error
//   - labels: comma-separated names; EVERY name must resolve (labels are
//     NOT created — unknown → per-row error, documented)
//   - assignee_email: workspace member email; unknown → per-row error
//     (never a 500)
// Dates accept YYYY-MM-DD (same contract as module dates).

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ImportMapping names the CSV column (header) feeding each issue field.
// Title is required; the rest are optional.
type ImportMapping struct {
	Title         string `json:"title"`
	Description   string `json:"description,omitempty"`
	State         string `json:"state,omitempty"`
	Priority      string `json:"priority,omitempty"`
	Labels        string `json:"labels,omitempty"`
	AssigneeEmail string `json:"assignee_email,omitempty"`
	StartDate     string `json:"start_date,omitempty"`
	TargetDate    string `json:"target_date,omitempty"`
}

// ImportRowError is one row's failure. Row is 1-based counting the header
// as row 1 (so the first data row is 2) — matches what a user sees in a
// spreadsheet.
type ImportRowError struct {
	Row     int    `json:"row"`
	Message string `json:"message"`
}

// ImportResult is the import outcome.
type ImportResult struct {
	Created int              `json:"created"`
	Failed  int              `json:"failed"`
	Errors  []ImportRowError `json:"errors"`
}

// ImportPreviewRow is one parsed data row for the preview endpoint.
type ImportPreviewRow struct {
	Row    int               `json:"row"`
	Values map[string]string `json:"values"`
}

// ImportPreview is the preview endpoint's response: parsed rows plus any
// mapping/row validation problems. No writes happen.
type ImportPreview struct {
	Rows   []ImportPreviewRow `json:"rows"`
	Errors []ImportRowError   `json:"errors"`
}

// importPriorityNames maps lowercase priority names to values
// (Plane vocabulary: 0=none, 1=low, 2=medium, 3=high, 4=urgent).
var importPriorityNames = map[string]int{
	"none": 0, "low": 1, "medium": 2, "high": 3, "urgent": 4,
}

var (
	// ErrImportMapping is returned when the column mapping itself is bad
	// (missing title column). It fails the whole request (400), not a row.
	ErrImportMapping = errors.New("service: invalid import column mapping")
	// ErrImportEmpty is returned when the CSV has no data rows.
	ErrImportEmpty = errors.New("service: csv has no data rows")
)

// parseImportCSV reads the CSV, stripping a UTF-8 BOM and letting
// encoding/csv handle quoted commas, CRLF, and blank lines. It returns
// the header and the data records.
func parseImportCSV(r io.Reader) (header []string, records [][]string, err error) {
	// Strip BOM: encoding/csv does not, and it would poison the first
	// header name.
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, nil, err
	}
	text := strings.TrimPrefix(string(data), "\ufeff")
	cr := csv.NewReader(strings.NewReader(text))
	cr.TrimLeadingSpace = true
	// FieldsPerRecord = -1: real-world CSVs have ragged rows (short
	// trailing rows). Missing cells read as ""; cell() guards the index.
	cr.FieldsPerRecord = -1
	// LazyQuotes is deliberately OFF: malformed quotes are a data error
	// the user should fix, not something to guess through.
	all, err := cr.ReadAll()
	if err != nil {
		return nil, nil, fmt.Errorf("service: parse csv: %w", err)
	}
	if len(all) == 0 {
		return nil, nil, ErrImportEmpty
	}
	header = all[0]
	for i := range header {
		header[i] = strings.TrimSpace(header[i])
	}
	records = all[1:]
	return header, records, nil
}

// importLookups holds the pre-loaded reference maps for one import.
type importLookups struct {
	states    map[string]string // lower(name) -> state id
	labels    map[string]string // lower(name) -> label id
	members   map[string]string // lower(email) -> user id (workspace members)
	wsID      string
	projectID string
}

// loadImportLookups resolves the workspace/project (member 15+ required)
// and pre-loads the reference maps in three queries.
func loadImportLookups(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string) (*importLookups, error) {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return nil, err
	}
	wsID, projectID, role, err := resolveIssueProject(ctx, pool, wsSlug, ident, actorID)
	if err != nil {
		return nil, err
	}
	if role < RoleMember {
		return nil, ErrForbidden
	}
	lu := &importLookups{
		states:    map[string]string{},
		labels:    map[string]string{},
		members:   map[string]string{},
		wsID:      wsID,
		projectID: projectID,
	}
	rows, err := pool.Query(ctx,
		`SELECT id::text, name FROM states WHERE project_id = $1::uuid`, projectID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			rows.Close()
			return nil, err
		}
		lu.states[strings.ToLower(strings.TrimSpace(name))] = id
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = pool.Query(ctx,
		`SELECT id::text, name FROM labels WHERE workspace_id = $1::uuid`, wsID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			rows.Close()
			return nil, err
		}
		lu.labels[strings.ToLower(strings.TrimSpace(name))] = id
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = pool.Query(ctx,
		`SELECT u.id::text, u.email FROM users u
		 JOIN workspace_members m ON m.user_id = u.id
		 WHERE m.workspace_id = $1::uuid`, wsID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, email string
		if err := rows.Scan(&id, &email); err != nil {
			rows.Close()
			return nil, err
		}
		lu.members[strings.ToLower(strings.TrimSpace(email))] = id
	}
	rows.Close()
	return lu, rows.Err()
}

// importRow is one validated row ready to insert.
type importRow struct {
	name        string
	description *string
	stateID     string
	priority    int
	labelIDs    []string
	assigneeID  *string
	startDate   *time.Time
	targetDate  *time.Time
}

// validateImportMapping checks the mapping against the CSV header.
// The title column must name an existing header.
func validateImportMapping(m ImportMapping, header []string) (map[string]int, error) {
	if strings.TrimSpace(m.Title) == "" {
		return nil, fmt.Errorf("%w: \"title\" column is required", ErrImportMapping)
	}
	cols := map[string]int{}
	for i, h := range header {
		if _, ok := cols[h]; !ok {
			cols[h] = i
		}
	}
	for _, col := range []string{m.Title, m.Description, m.State, m.Priority,
		m.Labels, m.AssigneeEmail, m.StartDate, m.TargetDate} {
		if col == "" {
			continue
		}
		if _, ok := cols[col]; !ok {
			return nil, fmt.Errorf("%w: column %q not in csv header", ErrImportMapping, col)
		}
	}
	return cols, nil
}

// cell returns the row's value for a mapped column ("" when unmapped).
func cell(cols map[string]int, record []string, col string) string {
	if col == "" {
		return ""
	}
	i, ok := cols[col]
	if !ok || i >= len(record) {
		return ""
	}
	return strings.TrimSpace(record[i])
}

// parseImportDate parses YYYY-MM-DD; empty = nil.
func parseImportDate(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil, fmt.Errorf("invalid date %q (want YYYY-MM-DD)", s)
	}
	return &t, nil
}

// validateImportRow maps one CSV record to an importRow, resolving
// references against the lookup maps. Any problem is a per-row error,
// never a panic and never a 500.
func validateImportRow(m ImportMapping, cols map[string]int, record []string, lu *importLookups) (*importRow, error) {
	name := cell(cols, record, m.Title)
	if name == "" {
		return nil, errors.New("title is required")
	}
	row := &importRow{name: name}

	if d := cell(cols, record, m.Description); d != "" {
		row.description = &d
	}

	// State: default to the project's backlog state when unmapped/empty.
	if s := cell(cols, record, m.State); s != "" {
		id, ok := lu.states[strings.ToLower(s)]
		if !ok {
			return nil, fmt.Errorf("unknown state %q", s)
		}
		row.stateID = id
	}

	// Priority: name or number.
	if p := cell(cols, record, m.Priority); p != "" {
		if n, err := strconv.Atoi(p); err == nil {
			if n < 0 || n > 4 {
				return nil, fmt.Errorf("invalid priority %q (want 0-4 or none/low/medium/high/urgent)", p)
			}
			row.priority = n
		} else if n, ok := importPriorityNames[strings.ToLower(p)]; ok {
			row.priority = n
		} else {
			return nil, fmt.Errorf("unknown priority %q", p)
		}
	}

	// Labels: every name must resolve; labels are never created by import.
	if l := cell(cols, record, m.Labels); l != "" {
		for _, name := range strings.Split(l, ",") {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			id, ok := lu.labels[strings.ToLower(name)]
			if !ok {
				return nil, fmt.Errorf("unknown label %q (labels are not created by import)", name)
			}
			row.labelIDs = append(row.labelIDs, id)
		}
	}

	// Assignee: must be a workspace member.
	if e := cell(cols, record, m.AssigneeEmail); e != "" {
		id, ok := lu.members[strings.ToLower(e)]
		if !ok {
			return nil, fmt.Errorf("unknown assignee %q (not a workspace member)", e)
		}
		row.assigneeID = &id
	}

	var err error
	if row.startDate, err = parseImportDate(cell(cols, record, m.StartDate)); err != nil {
		return nil, err
	}
	if row.targetDate, err = parseImportDate(cell(cols, record, m.TargetDate)); err != nil {
		return nil, err
	}
	if row.startDate != nil && row.targetDate != nil && row.startDate.After(*row.targetDate) {
		return nil, errors.New("start_date is after target_date")
	}
	return row, nil
}

// PreviewImport parses the CSV and validates the mapping plus the first
// 10 data rows. No writes.
func PreviewImport(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string, r io.Reader, m ImportMapping) (*ImportPreview, error) {
	header, records, err := parseImportCSV(r)
	if err != nil {
		return nil, err
	}
	cols, err := validateImportMapping(m, header)
	if err != nil {
		return nil, err
	}
	lu, err := loadImportLookups(ctx, pool, wsSlug, identifier, actorID)
	if err != nil {
		return nil, err
	}
	out := &ImportPreview{Rows: []ImportPreviewRow{}, Errors: []ImportRowError{}}
	n := len(records)
	if n > 10 {
		n = 10
	}
	for i := 0; i < n; i++ {
		rec := records[i]
		rowNum := i + 2 // header is row 1
		vals := map[string]string{}
		for _, h := range header {
			vals[h] = cell(cols, rec, h)
		}
		out.Rows = append(out.Rows, ImportPreviewRow{Row: rowNum, Values: vals})
		if _, err := validateImportRow(m, cols, rec, lu); err != nil {
			out.Errors = append(out.Errors, ImportRowError{Row: rowNum, Message: err.Error()})
		}
	}
	return out, nil
}

// ImportIssues validates every row (per-row errors collected) then inserts
// all valid rows in one transaction. Member (15)+.
func ImportIssues(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string, r io.Reader, m ImportMapping) (*ImportResult, error) {
	header, records, err := parseImportCSV(r)
	if err != nil {
		return nil, err
	}
	cols, err := validateImportMapping(m, header)
	if err != nil {
		return nil, err
	}
	lu, err := loadImportLookups(ctx, pool, wsSlug, identifier, actorID)
	if err != nil {
		return nil, err
	}

	res := &ImportResult{Errors: []ImportRowError{}}
	var valid []*importRow
	for i, rec := range records {
		rowNum := i + 2
		// Skip fully-blank rows (trailing newlines).
		blank := true
		for _, c := range rec {
			if strings.TrimSpace(c) != "" {
				blank = false
				break
			}
		}
		if blank {
			continue
		}
		row, err := validateImportRow(m, cols, rec, lu)
		if err != nil {
			res.Failed++
			res.Errors = append(res.Errors, ImportRowError{Row: rowNum, Message: err.Error()})
			continue
		}
		valid = append(valid, row)
	}
	if len(valid) == 0 && res.Failed == 0 {
		return nil, ErrImportEmpty
	}

	// Phase 2: insert all valid rows atomically.
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Default state for rows without one: project's backlog state.
	var backlogID string
	needBacklog := false
	for _, row := range valid {
		if row.stateID == "" {
			needBacklog = true
			break
		}
	}
	if needBacklog {
		backlogID, err = backlogState(ctx, tx, lu.projectID)
		if err != nil {
			return nil, err
		}
	}

	for _, row := range valid {
		stateID := row.stateID
		if stateID == "" {
			stateID = backlogID
		}
		var seq int
		if err := tx.QueryRow(ctx,
			`INSERT INTO issue_sequences (project_id, last_value) VALUES ($1::uuid, 1)
			 ON CONFLICT (project_id) DO UPDATE SET last_value = issue_sequences.last_value + 1
			 RETURNING last_value`,
			lu.projectID).Scan(&seq); err != nil {
			return nil, fmt.Errorf("service: increment issue sequence: %w", err)
		}
		var descParam any
		if row.description != nil {
			// Description is jsonb; plain text becomes a JSON string.
			descParam = strconv.Quote(*row.description)
		}
		var issueID string
		err = tx.QueryRow(ctx,
			`INSERT INTO issues (project_id, sequence_id, name, description, priority,
				state_id, start_date, target_date, created_by)
			 VALUES ($1::uuid, $2, $3, $4::jsonb, $5, $6::uuid, $7, $8, $9::uuid)
			 RETURNING id::text`,
			lu.projectID, seq, row.name, descParam, row.priority, stateID,
			row.startDate, row.targetDate, actorID).Scan(&issueID)
		if err != nil {
			return nil, err
		}
		for _, lid := range row.labelIDs {
			if _, err := tx.Exec(ctx,
				`INSERT INTO issue_labels (issue_id, label_id) VALUES ($1::uuid, $2::uuid)
				 ON CONFLICT DO NOTHING`, issueID, lid); err != nil {
				return nil, err
			}
		}
		if row.assigneeID != nil {
			if _, err := tx.Exec(ctx,
				`INSERT INTO issue_assignees (issue_id, user_id) VALUES ($1::uuid, $2::uuid)
				 ON CONFLICT DO NOTHING`, issueID, *row.assigneeID); err != nil {
				return nil, err
			}
		}
		newVal := fmt.Sprintf(`{"sequence_id":%d,"name":%s}`, seq, strconv.Quote(row.name))
		if _, err := tx.Exec(ctx,
			`INSERT INTO issue_activities (issue_id, actor_id, field, new_value)
			 VALUES ($1::uuid, $2::uuid, '_created', $3::jsonb)`,
			issueID, actorID, newVal); err != nil {
			return nil, err
		}
		res.Created++
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return res, nil
}

// ImportTemplateCSV returns the template CSV with the expected headers.
func ImportTemplateCSV() string {
	return "title,description,state,priority,labels,assignee_email,start_date,target_date\n" +
		"\"Example issue\",\"Optional description\",Backlog,medium,\"bug, frontend\",teammate@example.com,2026-01-05,2026-01-12\n"
}
