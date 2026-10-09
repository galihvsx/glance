package service

// Full workspace data export (C10T3).
//
// StreamWorkspaceExport streams the entire workspace as a single JSON
// archive document to w. The archive is written incrementally — section by
// section, row by row — so a large workspace never materializes the whole
// document in memory; the HTTP handler flushes after every encoded row,
// which yields chunked transfer encoding on the wire.
//
// Archive format (ExportFormatVersion):
//
//	{
//	  "format": "glance-export/1",
//	  "exported_at": "<RFC3339>",
///	  "schema_note": "<contract documentation, see exportSchemaNote>",
//	  "workspace": {"id","slug","name","created_at","updated_at"},
//	  "members": [{"user_id","email","name","role"}],
//	  "projects": [{"id","identifier","name","description","archive_in_days",
//	                "close_in_days","created_at","updated_at"}],
//	  "states": [{"id","project_id","name","group","color","sequence"}],
//	  "labels": [{"id","name","color","parent_id"}],
//	  "estimates": [{"id","project_id","name","points":[{"id","key","value","description"}]}],
//	  "custom_fields": [{"id","project_id","name","field_type","options","required","position"}],
//	  "cycles": [{"id","project_id","name","start_date","end_date","status","issue_ids":[]}],
//	  "modules": [{"id","project_id","name","description","status","lead_id","issue_ids":[]}],
//	  "pages": [{"id","project_id","parent_id","title","content","position",
//	             "author_id","created_at","updated_at"}],
//	  "issues": [{"id","project_id","display_id","sequence_id","name","description",
//	              "priority","state_id","parent_id","start_date","target_date",
//	              "estimate_point_id","is_draft","archived_at","created_by",
//	              "created_at","updated_at",
//	              "assignees":[{"user_id","email"}],
//	              "labels":[{"label_id","name"}],
//	              "custom_values":[{"field_id","field_name","field_type","value"}],
//	              "comments":[{"id","parent_id","actor_id","actor_email","content",
//	                          "created_at","updated_at"}],
//	              "attachments":[{"id","filename","content_type","size_bytes",
//	                               "uploaded_by","created_at"}]}]
//	}
//
// Empty sections encode as [] (never null) so a future importer needs no
// nil guards. UUIDs are exported verbatim: they are the stable identity a
// future archive importer can key re-linking on. display_id is
// informational (re-derivable from project identifier + sequence_id).
//
// Deliberate scope decisions (also recorded in exportSchemaNote):
//   - members and estimates are exported although the task brief lists
//     neither: issues reference users (assignees, comment actors) and
//     estimate points, and an archive without the referenced rows is not
//     a faithful backup. Both are admin-visible data; the endpoint is
//     admin-only, so no new exposure.
//   - attachments carry metadata ONLY (filename, content_type,
//     size_bytes, uploader, timestamps). Binaries are not included and
//     the server-side stored_path is never exported (internal layout).
//   - issue drafts ARE included (excluding them would silently lose
//     data); soft-deleted issues and comments are excluded, matching
//     every list endpoint's live-set semantics.
//   - NOT exported: the Slack webhook URL (secret), issue activity
//     history, reactions, votes, issue links, intake items, webhooks
//     configuration, notification prefs, API tokens, page revisions,
//     sessions/OTP material. The archive covers the current data model,
//     not audit history or integration config.
//   - There is NO archive importer in this task: import is planned future
//     work. The format version pin and this schema note are the contract
//     that importer will read against.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ExportFormatVersion pins the archive schema. A future archive-import
// cycle must refuse (with a clear error) any archive whose format it
// does not understand — never silently mis-import.
const ExportFormatVersion = "glance-export/1"

// exportSchemaNote is embedded in every archive as "schema_note": the
// human- and machine-readable contract a future importer builds against.
const exportSchemaNote = "glance workspace data archive, format glance-export/1. " +
	"Single JSON document; sections stream in the order: workspace, members, projects, states, " +
	"labels, estimates, custom_fields, cycles, modules, pages, issues. " +
	"Empty sections are []. UUIDs are stable identity for re-linking on import; display_id is " +
	"informational only. Issues nest comments, custom_values, assignees, labels and attachments. " +
	"Draft issues are included; soft-deleted issues and comments are excluded. " +
	"Attachments carry metadata only (filename, content_type, size_bytes, uploaded_by, timestamps): " +
	"binaries are NOT included and the server-side stored_path is never exported. " +
	"Not exported: Slack webhook URL (secret), issue activity history, reactions, votes, issue links, " +
	"intake items, webhooks config, notification prefs, API tokens, page revisions, sessions/OTP. " +
	"There is no archive importer yet; a future cycle implementing import must honor the format " +
	"version pin and refuse unknown versions."

// exportStreamWriter writes a JSON document incrementally: structural
// tokens via raw(), values via value() (which flushes when the writer
// supports it, so the HTTP layer emits chunked encoding at row
// granularity). json.Encoder's trailing newline after each value is legal
// JSON whitespace.
type exportStreamWriter struct {
	w   io.Writer
	enc *json.Encoder
}

func newExportStreamWriter(w io.Writer) *exportStreamWriter {
	return &exportStreamWriter{w: w, enc: json.NewEncoder(w)}
}

func (e *exportStreamWriter) raw(s string) error {
	_, err := io.WriteString(e.w, s)
	return err
}

func (e *exportStreamWriter) value(v any) error {
	if err := e.enc.Encode(v); err != nil {
		return err
	}
	if f, ok := e.w.(interface{ Flush() }); ok {
		f.Flush()
	}
	return nil
}

// streamArray encodes a top-level section as a JSON array, one row at a
// time, from a single query. Memory stays O(one row).
func streamArray[T any](ctx context.Context, ew *exportStreamWriter, tx pgx.Tx, query string, args []any, scan func(pgx.Row) (T, error)) error {
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	if err := ew.raw("["); err != nil {
		return err
	}
	first := true
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return err
		}
		if !first {
			if err := ew.raw(","); err != nil {
				return err
			}
		}
		first = false
		if err := ew.value(v); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return ew.raw("]")
}

// stringIDs collects a single text column into a []string (never nil).
func stringIDs(ctx context.Context, tx pgx.Tx, query string, args ...any) ([]string, error) {
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}

// --- archive row shapes -------------------------------------------------

type exportWorkspace struct {
	ID        string    `json:"id"`
	Slug      string    `json:"slug"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type exportMember struct {
	UserID string  `json:"user_id"`
	Email  string  `json:"email"`
	Name   *string `json:"name"`
	Role   int     `json:"role"`
}

type exportProject struct {
	ID            string    `json:"id"`
	Identifier    string    `json:"identifier"`
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	ArchiveInDays *int      `json:"archive_in_days"`
	CloseInDays   *int      `json:"close_in_days"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type exportState struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
	Group     string `json:"group"`
	Color     string `json:"color"`
	Sequence  int    `json:"sequence"`
}

type exportLabel struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Color    string  `json:"color"`
	ParentID *string `json:"parent_id"`
}

type exportEstimatePoint struct {
	ID          string  `json:"id"`
	Key         string  `json:"key"`
	Value       int     `json:"value"`
	Description *string `json:"description"`
}

type exportEstimate struct {
	ID        string                `json:"id"`
	ProjectID string                `json:"project_id"`
	Name      string                `json:"name"`
	Points    []exportEstimatePoint `json:"points"`
}

type exportCustomField struct {
	ID        string          `json:"id"`
	ProjectID string          `json:"project_id"`
	Name      string          `json:"name"`
	FieldType string          `json:"field_type"`
	Options   json.RawMessage `json:"options"`
	Required  bool            `json:"required"`
	Position  int             `json:"position"`
}

type exportCycle struct {
	ID        string   `json:"id"`
	ProjectID string   `json:"project_id"`
	Name      string   `json:"name"`
	StartDate string   `json:"start_date"`
	EndDate   string   `json:"end_date"`
	Status    string   `json:"status"`
	IssueIDs  []string `json:"issue_ids"`
}

type exportModule struct {
	ID          string   `json:"id"`
	ProjectID   string   `json:"project_id"`
	Name        string   `json:"name"`
	Description *string  `json:"description"`
	Status      string   `json:"status"`
	LeadID      *string  `json:"lead_id"`
	IssueIDs    []string `json:"issue_ids"`
}

type exportPage struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id"`
	ParentID  *string   `json:"parent_id"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	Position  int       `json:"position"`
	AuthorID  *string   `json:"author_id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type exportAssignee struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
}

type exportIssueLabel struct {
	LabelID string `json:"label_id"`
	Name    string `json:"name"`
}

type exportCustomValue struct {
	FieldID   string `json:"field_id"`
	FieldName string `json:"field_name"`
	FieldType string `json:"field_type"`
	Value     any    `json:"value"`
}

type exportComment struct {
	ID         string          `json:"id"`
	ParentID   *string         `json:"parent_id"`
	ActorID    string          `json:"actor_id"`
	ActorEmail string          `json:"actor_email"`
	Content    json.RawMessage `json:"content"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

// exportAttachment is attachment METADATA only. Binaries are never
// exported and the server-side stored_path is deliberately absent.
type exportAttachment struct {
	ID          string    `json:"id"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"content_type"`
	SizeBytes   int64     `json:"size_bytes"`
	UploadedBy  string    `json:"uploaded_by"`
	CreatedAt   time.Time `json:"created_at"`
}

type exportIssue struct {
	ID              string              `json:"id"`
	ProjectID       string              `json:"project_id"`
	DisplayID       string              `json:"display_id"`
	SequenceID      int                 `json:"sequence_id"`
	Name            string              `json:"name"`
	Description     json.RawMessage     `json:"description"`
	Priority        int                 `json:"priority"`
	StateID         string              `json:"state_id"`
	ParentID        *string             `json:"parent_id"`
	StartDate       *string             `json:"start_date"`
	TargetDate      *string             `json:"target_date"`
	EstimatePointID *string             `json:"estimate_point_id"`
	IsDraft         bool                `json:"is_draft"`
	ArchivedAt      *time.Time          `json:"archived_at"`
	CreatedBy       string              `json:"created_by"`
	CreatedAt       time.Time           `json:"created_at"`
	UpdatedAt       time.Time           `json:"updated_at"`
	Assignees       []exportAssignee    `json:"assignees"`
	Labels          []exportIssueLabel  `json:"labels"`
	CustomValues    []exportCustomValue `json:"custom_values"`
	Comments        []exportComment     `json:"comments"`
	Attachments     []exportAttachment  `json:"attachments"`
}

// StreamWorkspaceExport streams the full workspace archive for slug as a
// single JSON document to w (C10T3). Admin only: non-members get
// ErrNotFound (the actor-resolution contract — never a hint the slug
// exists), members/guests get ErrForbidden. Both are returned BEFORE the
// first archive byte is written, so the handler can still answer with a
// status. The read runs in a REPEATABLE READ, read-only transaction so
// concurrent writes mid-export cannot produce dangling references.
func StreamWorkspaceExport(ctx context.Context, pool *pgxpool.Pool, slug, actorID string, w io.Writer) error {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var wsID string
	var role int
	err = tx.QueryRow(ctx,
		`SELECT w.id::text, m.role
		 FROM workspaces w
		 JOIN workspace_members m ON m.workspace_id = w.id
		 WHERE w.slug = $1 AND m.user_id = $2::uuid`,
		slug, actorID).Scan(&wsID, &role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if role != RoleAdmin {
		return ErrForbidden
	}

	ew := newExportStreamWriter(w)

	if err := ew.raw(`{"format":`); err != nil {
		return err
	}
	if err := ew.value(ExportFormatVersion); err != nil {
		return err
	}
	if err := ew.raw(`,"exported_at":`); err != nil {
		return err
	}
	if err := ew.value(time.Now().UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	if err := ew.raw(`,"schema_note":`); err != nil {
		return err
	}
	if err := ew.value(exportSchemaNote); err != nil {
		return err
	}

	// workspace (the Slack webhook URL is a secret: never exported).
	if err := ew.raw(`,"workspace":`); err != nil {
		return err
	}
	var ws exportWorkspace
	if err := tx.QueryRow(ctx,
		`SELECT id::text, slug::text, name, created_at, updated_at
		 FROM workspaces WHERE id = $1::uuid`,
		wsID).Scan(&ws.ID, &ws.Slug, &ws.Name, &ws.CreatedAt, &ws.UpdatedAt); err != nil {
		return err
	}
	if err := ew.value(ws); err != nil {
		return err
	}

	if err := ew.raw(`,"members":`); err != nil {
		return err
	}
	if err := streamArray(ctx, ew, tx,
		`SELECT m.user_id::text, u.email::text, u.name, m.role
		 FROM workspace_members m
		 JOIN users u ON u.id = m.user_id
		 WHERE m.workspace_id = $1::uuid
		 ORDER BY u.email`,
		[]any{wsID},
		func(r pgx.Row) (exportMember, error) {
			var m exportMember
			err := r.Scan(&m.UserID, &m.Email, &m.Name, &m.Role)
			return m, err
		}); err != nil {
		return err
	}

	if err := ew.raw(`,"projects":`); err != nil {
		return err
	}
	if err := streamArray(ctx, ew, tx,
		`SELECT id::text, identifier, name, description, archive_in_days,
		        close_in_days, created_at, updated_at
		 FROM projects WHERE workspace_id = $1::uuid ORDER BY created_at`,
		[]any{wsID},
		func(r pgx.Row) (exportProject, error) {
			var p exportProject
			err := r.Scan(&p.ID, &p.Identifier, &p.Name, &p.Description,
				&p.ArchiveInDays, &p.CloseInDays, &p.CreatedAt, &p.UpdatedAt)
			return p, err
		}); err != nil {
		return err
	}

	if err := ew.raw(`,"states":`); err != nil {
		return err
	}
	if err := streamArray(ctx, ew, tx,
		`SELECT s.id::text, s.project_id::text, s.name, s."group", s.color, s.sequence
		 FROM states s
		 JOIN projects p ON p.id = s.project_id
		 WHERE p.workspace_id = $1::uuid
		 ORDER BY s.project_id, s.sequence`,
		[]any{wsID},
		func(r pgx.Row) (exportState, error) {
			var s exportState
			err := r.Scan(&s.ID, &s.ProjectID, &s.Name, &s.Group, &s.Color, &s.Sequence)
			return s, err
		}); err != nil {
		return err
	}

	if err := ew.raw(`,"labels":`); err != nil {
		return err
	}
	if err := streamArray(ctx, ew, tx,
		`SELECT id::text, name, color, parent_id::text
		 FROM labels WHERE workspace_id = $1::uuid ORDER BY name`,
		[]any{wsID},
		func(r pgx.Row) (exportLabel, error) {
			var l exportLabel
			err := r.Scan(&l.ID, &l.Name, &l.Color, &l.ParentID)
			return l, err
		}); err != nil {
		return err
	}

	if err := ew.raw(`,"estimates":`); err != nil {
		return err
	}
	if err := streamEstimates(ctx, ew, tx, wsID); err != nil {
		return err
	}

	if err := ew.raw(`,"custom_fields":`); err != nil {
		return err
	}
	if err := streamArray(ctx, ew, tx,
		`SELECT cf.id::text, cf.project_id::text, cf.name, cf.field_type,
		        cf.options, cf.required, cf.position
		 FROM custom_fields cf
		 JOIN projects p ON p.id = cf.project_id
		 WHERE p.workspace_id = $1::uuid
		 ORDER BY cf.project_id, cf.position`,
		[]any{wsID},
		func(r pgx.Row) (exportCustomField, error) {
			var f exportCustomField
			var options []byte
			err := r.Scan(&f.ID, &f.ProjectID, &f.Name, &f.FieldType,
				&options, &f.Required, &f.Position)
			f.Options = json.RawMessage(options)
			return f, err
		}); err != nil {
		return err
	}

	if err := ew.raw(`,"cycles":`); err != nil {
		return err
	}
	if err := streamCycles(ctx, ew, tx, wsID); err != nil {
		return err
	}

	if err := ew.raw(`,"modules":`); err != nil {
		return err
	}
	if err := streamModules(ctx, ew, tx, wsID); err != nil {
		return err
	}

	if err := ew.raw(`,"pages":`); err != nil {
		return err
	}
	if err := streamArray(ctx, ew, tx,
		`SELECT pg.id::text, pg.project_id::text, pg.parent_id::text, pg.title,
		        pg.content, pg.position, pg.author_id::text, pg.created_at, pg.updated_at
		 FROM pages pg
		 JOIN projects p ON p.id = pg.project_id
		 WHERE p.workspace_id = $1::uuid
		 ORDER BY pg.project_id, pg.position, pg.created_at`,
		[]any{wsID},
		func(r pgx.Row) (exportPage, error) {
			var p exportPage
			err := r.Scan(&p.ID, &p.ProjectID, &p.ParentID, &p.Title,
				&p.Content, &p.Position, &p.AuthorID, &p.CreatedAt, &p.UpdatedAt)
			return p, err
		}); err != nil {
		return err
	}

	if err := ew.raw(`,"issues":`); err != nil {
		return err
	}
	if err := streamIssues(ctx, ew, tx, wsID); err != nil {
		return err
	}

	if err := ew.raw("}"); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// streamEstimates streams estimates with their nested points. The base
// rows are collected (and closed) before the per-estimate point queries
// run: pgx serves a transaction over a single connection, so a second
// query while rows are open fails with "conn busy".
func streamEstimates(ctx context.Context, ew *exportStreamWriter, tx pgx.Tx, wsID string) error {
	type estimateBase struct {
		id, projectID, name string
	}
	var bases []estimateBase
	collectErr := func() error {
		rows, err := tx.Query(ctx,
			`SELECT e.id::text, e.project_id::text, e.name
			 FROM estimates e
			 JOIN projects p ON p.id = e.project_id
			 WHERE p.workspace_id = $1::uuid
			 ORDER BY e.project_id, e.name`,
			wsID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var b estimateBase
			if err := rows.Scan(&b.id, &b.projectID, &b.name); err != nil {
				return err
			}
			bases = append(bases, b)
		}
		return rows.Err()
	}()
	if collectErr != nil {
		return collectErr
	}
	if err := ew.raw("["); err != nil {
		return err
	}
	for i, b := range bases {
		var e exportEstimate
		e.ID, e.ProjectID, e.Name = b.id, b.projectID, b.name
		e.Points = []exportEstimatePoint{}
		pRows, err := tx.Query(ctx,
			`SELECT id::text, key, value, description
			 FROM estimate_points WHERE estimate_id = $1::uuid ORDER BY value, key`, e.ID)
		if err != nil {
			return err
		}
		for pRows.Next() {
			var pt exportEstimatePoint
			if err := pRows.Scan(&pt.ID, &pt.Key, &pt.Value, &pt.Description); err != nil {
				pRows.Close()
				return err
			}
			e.Points = append(e.Points, pt)
		}
		pRows.Close()
		if err := pRows.Err(); err != nil {
			return err
		}
		if i > 0 {
			if err := ew.raw(","); err != nil {
				return err
			}
		}
		if err := ew.value(e); err != nil {
			return err
		}
	}
	return ew.raw("]")
}

// streamCycles streams cycles with their member issue IDs. Base rows are
// collected first (see streamEstimates: no nested queries on an open rows).
func streamCycles(ctx context.Context, ew *exportStreamWriter, tx pgx.Tx, wsID string) error {
	var cycles []exportCycle
	collectErr := func() error {
		rows, err := tx.Query(ctx,
			`SELECT c.id::text, c.project_id::text, c.name,
			        to_char(c.start_date, 'YYYY-MM-DD'), to_char(c.end_date, 'YYYY-MM-DD'), c.status
			 FROM cycles c
			 JOIN projects p ON p.id = c.project_id
			 WHERE p.workspace_id = $1::uuid
			 ORDER BY c.project_id, c.start_date`,
			wsID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c exportCycle
			if err := rows.Scan(&c.ID, &c.ProjectID, &c.Name, &c.StartDate, &c.EndDate, &c.Status); err != nil {
				return err
			}
			cycles = append(cycles, c)
		}
		return rows.Err()
	}()
	if collectErr != nil {
		return collectErr
	}
	if err := ew.raw("["); err != nil {
		return err
	}
	for i := range cycles {
		ids, err := stringIDs(ctx, tx,
			`SELECT issue_id::text FROM cycle_issues WHERE cycle_id = $1::uuid ORDER BY created_at`, cycles[i].ID)
		if err != nil {
			return err
		}
		cycles[i].IssueIDs = ids
		if i > 0 {
			if err := ew.raw(","); err != nil {
				return err
			}
		}
		if err := ew.value(cycles[i]); err != nil {
			return err
		}
	}
	return ew.raw("]")
}

// streamModules streams modules with their member issue IDs. Base rows are
// collected first (see streamEstimates: no nested queries on an open rows).
func streamModules(ctx context.Context, ew *exportStreamWriter, tx pgx.Tx, wsID string) error {
	var modules []exportModule
	collectErr := func() error {
		rows, err := tx.Query(ctx,
			`SELECT m.id::text, m.project_id::text, m.name, m.description, m.status, m.lead_id::text
			 FROM modules m
			 JOIN projects p ON p.id = m.project_id
			 WHERE p.workspace_id = $1::uuid
			 ORDER BY m.project_id, m.name`,
			wsID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var m exportModule
			if err := rows.Scan(&m.ID, &m.ProjectID, &m.Name, &m.Description, &m.Status, &m.LeadID); err != nil {
				return err
			}
			modules = append(modules, m)
		}
		return rows.Err()
	}()
	if collectErr != nil {
		return collectErr
	}
	if err := ew.raw("["); err != nil {
		return err
	}
	for i := range modules {
		ids, err := stringIDs(ctx, tx,
			`SELECT issue_id::text FROM module_issues WHERE module_id = $1::uuid ORDER BY created_at`, modules[i].ID)
		if err != nil {
			return err
		}
		modules[i].IssueIDs = ids
		if i > 0 {
			if err := ew.raw(","); err != nil {
				return err
			}
		}
		if err := ew.value(modules[i]); err != nil {
			return err
		}
	}
	return ew.raw("]")
}

// streamIssues streams every live issue (soft-deleted excluded, drafts
// included) with its nested relations. The issue IDs are collected first
// (plain strings — cheap even for huge workspaces); each issue is then
// loaded and encoded one at a time with its relations, so memory stays
// flat regardless of workspace size. Sequential per-issue queries keep
// pgx's single-connection transaction happy (no nested queries on open
// rows), all inside the one REPEATABLE READ snapshot.
func streamIssues(ctx context.Context, ew *exportStreamWriter, tx pgx.Tx, wsID string) error {
	ids, err := stringIDs(ctx, tx,
		`SELECT i.id::text
		 FROM issues i
		 JOIN projects p ON p.id = i.project_id
		 WHERE p.workspace_id = $1::uuid AND i.deleted_at IS NULL
		 ORDER BY i.project_id, i.sequence_id`,
		wsID)
	if err != nil {
		return err
	}
	if err := ew.raw("["); err != nil {
		return err
	}
	for i, id := range ids {
		var is exportIssue
		var description []byte
		if err := tx.QueryRow(ctx,
			`SELECT i.id::text, i.project_id::text, p.identifier || '-' || i.sequence_id,
			        i.sequence_id, i.name, i.description, i.priority, i.state_id::text,
			        i.parent_id::text, to_char(i.start_date, 'YYYY-MM-DD'),
			        to_char(i.target_date, 'YYYY-MM-DD'), i.estimate_point_id::text,
			        i.is_draft, i.archived_at, i.created_by::text, i.created_at, i.updated_at
			 FROM issues i
			 JOIN projects p ON p.id = i.project_id
			 WHERE i.id = $1::uuid`,
			id).Scan(
			&is.ID, &is.ProjectID, &is.DisplayID, &is.SequenceID, &is.Name,
			&description, &is.Priority, &is.StateID, &is.ParentID, &is.StartDate,
			&is.TargetDate, &is.EstimatePointID, &is.IsDraft, &is.ArchivedAt,
			&is.CreatedBy, &is.CreatedAt, &is.UpdatedAt,
		); err != nil {
			return err
		}
		is.Description = json.RawMessage(description)
		if err := fillIssueRelations(ctx, tx, &is); err != nil {
			return err
		}
		if i > 0 {
			if err := ew.raw(","); err != nil {
				return err
			}
		}
		if err := ew.value(is); err != nil {
			return err
		}
	}
	return ew.raw("]")
}

// fillIssueRelations loads one issue's nested export relations.
func fillIssueRelations(ctx context.Context, tx pgx.Tx, is *exportIssue) error {
	var err error

	is.Assignees = []exportAssignee{}
	aRows, err := tx.Query(ctx,
		`SELECT a.user_id::text, u.email::text
		 FROM issue_assignees a
		 JOIN users u ON u.id = a.user_id
		 WHERE a.issue_id = $1::uuid
		 ORDER BY u.email`,
		is.ID)
	if err != nil {
		return err
	}
	for aRows.Next() {
		var a exportAssignee
		if err := aRows.Scan(&a.UserID, &a.Email); err != nil {
			aRows.Close()
			return err
		}
		is.Assignees = append(is.Assignees, a)
	}
	aRows.Close()
	if err := aRows.Err(); err != nil {
		return err
	}

	is.Labels = []exportIssueLabel{}
	lRows, err := tx.Query(ctx,
		`SELECT l.id::text, l.name
		 FROM issue_labels il
		 JOIN labels l ON l.id = il.label_id
		 WHERE il.issue_id = $1::uuid
		 ORDER BY l.name`,
		is.ID)
	if err != nil {
		return err
	}
	for lRows.Next() {
		var l exportIssueLabel
		if err := lRows.Scan(&l.LabelID, &l.Name); err != nil {
			lRows.Close()
			return err
		}
		is.Labels = append(is.Labels, l)
	}
	lRows.Close()
	if err := lRows.Err(); err != nil {
		return err
	}

	is.CustomValues = []exportCustomValue{}
	cvRows, err := tx.Query(ctx,
		`SELECT icv.field_id::text, cf.name, cf.field_type,
		        icv.value_text, icv.value_number::text,
		        to_char(icv.value_date, 'YYYY-MM-DD'), icv.value_bool
		 FROM issue_custom_values icv
		 JOIN custom_fields cf ON cf.id = icv.field_id
		 WHERE icv.issue_id = $1::uuid
		 ORDER BY cf.position`,
		is.ID)
	if err != nil {
		return err
	}
	for cvRows.Next() {
		var cv exportCustomValue
		var text, number, date *string
		var boolean *bool
		if err := cvRows.Scan(&cv.FieldID, &cv.FieldName, &cv.FieldType,
			&text, &number, &date, &boolean); err != nil {
			cvRows.Close()
			return err
		}
		// Exactly one value column is non-null per row; surface it typed.
		switch {
		case text != nil:
			cv.Value = *text
		case number != nil:
			cv.Value = json.Number(*number)
		case date != nil:
			cv.Value = *date
		case boolean != nil:
			cv.Value = *boolean
		}
		is.CustomValues = append(is.CustomValues, cv)
	}
	cvRows.Close()
	if err := cvRows.Err(); err != nil {
		return err
	}

	is.Comments = []exportComment{}
	cRows, err := tx.Query(ctx,
		`SELECT c.id::text, c.parent_id::text, c.actor_id::text, u.email::text,
		        c.content, c.created_at, c.updated_at
		 FROM comments c
		 JOIN users u ON u.id = c.actor_id
		 WHERE c.issue_id = $1::uuid AND c.deleted_at IS NULL
		 ORDER BY c.created_at`,
		is.ID)
	if err != nil {
		return err
	}
	for cRows.Next() {
		var c exportComment
		var content []byte
		if err := cRows.Scan(&c.ID, &c.ParentID, &c.ActorID, &c.ActorEmail,
			&content, &c.CreatedAt, &c.UpdatedAt); err != nil {
			cRows.Close()
			return err
		}
		c.Content = json.RawMessage(content)
		is.Comments = append(is.Comments, c)
	}
	cRows.Close()
	if err := cRows.Err(); err != nil {
		return err
	}

	// Attachments: metadata only — stored_path is server-internal and the
	// binary itself never leaves the blob store.
	is.Attachments = []exportAttachment{}
	atRows, err := tx.Query(ctx,
		`SELECT id::text, filename, content_type, size_bytes,
		        uploaded_by::text, created_at
		 FROM attachments
		 WHERE issue_id = $1::uuid
		 ORDER BY created_at`,
		is.ID)
	if err != nil {
		return err
	}
	for atRows.Next() {
		var a exportAttachment
		if err := atRows.Scan(&a.ID, &a.Filename, &a.ContentType,
			&a.SizeBytes, &a.UploadedBy, &a.CreatedAt); err != nil {
			atRows.Close()
			return err
		}
		is.Attachments = append(is.Attachments, a)
	}
	atRows.Close()
	if err := atRows.Err(); err != nil {
		return err
	}

	return nil
}
