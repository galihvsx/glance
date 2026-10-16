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
//	  "project_updates": [{"id","project_id","author_id","author_email",
//	             "status","body","created_at","updated_at"}],
//	  "issues": [{"id","project_id","display_id","sequence_id","name","description",
//	              "priority","state_id","parent_id","start_date","target_date",
//	              "estimate_point_id","is_draft","archived_at","created_by",
//	              "created_at","updated_at",
//	              "assignees":[{"user_id","email"}],
//	              "labels":[{"label_id","name"}],
//	              "custom_values":[{"field_id","field_name","field_type","value"}],
//	              "comments":[{"id","parent_id","actor_id","actor_email","content",
//	                          "created_at","updated_at","resolved_at",
//	                          "resolved_by","resolved_by_email"}],
//	              "attachments":[{"id","filename","content_type","size_bytes",
//	                               "uploaded_by","created_at"}]}],
//	  "page_issue_links": [{"id","project_identifier","page_id","page_title",
//	             "issue_display_id","created_by","created_by_email","created_at"}],
//	  "objectives": [{"id","title","description","owner_id","owner_email",
//	             "start_date","target_date","status","created_at","updated_at",
//	             "key_results":[{"id","title","kind","unit","start_value",
//	                              "target_value","current_value",
//	                              "created_at","updated_at",
//	                              "check_ins":[{"id","previous_value",
//	                                           "new_value","note","author_id",
//	                                           "author_email","created_at"}],
//	                              "linked_issues":[{"project_identifier",
//	                                                "issue_display_id"}]}]}]
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
//     history, reactions, votes, issue links (issue↔issue), intake items, webhooks
//     configuration, notification prefs, API tokens, page revisions,
//     sessions/OTP material, and the workspace audit log
//     (workspace_audit_log, C37T2). History is NOT portable: audit rows
//     are witnessed history of the source workspace — importing them
//     into another workspace would fabricate events that never happened
//     there. The target workspace keeps its own audit log (empty until
//     its own events occur); the importer likewise ignores any
//     hand-crafted audit section in an archive. The archive covers the
//     current data model, not audit history or integration config.
//   - There IS an archive importer for this format:
//     ImportWorkspaceArchive in internal/service/workspace_import.go
//     (C11T0), served at POST /api/v1/workspaces/{slug}/import. It reads
//     against the format version pin and this schema note, and refuses
//     unknown versions rather than mis-importing.

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
	"labels, estimates, custom_fields, cycles, modules, pages, project_updates, issues, page_issue_links, objectives. " +
	"Empty sections are []. UUIDs are stable identity for re-linking on import; display_id is " +
	"informational only. Issues nest comments, custom_values, assignees, labels and attachments. " +
	"Project updates carry id, project_id, author_id, author_email, status, body, created_at, updated_at. " +
	"Objectives carry id, title, description, owner_id, owner_email, start_date, target_date, status, " +
	"created_at, updated_at, with nested key_results (id, title, kind, unit, start/target/current values, " +
	"created_at, updated_at, check_ins, linked_issues). Metric KRs carry ALL check-ins oldest-first " +
	"(no cap — the API list endpoint's cap-100 does not apply to the archive); each check-in carries " +
	"id, previous_value (null for the seed), new_value, note, author_id, author_email, created_at. " +
	"Issue KRs carry no check-ins (they have none). Per-KR linked_issues are keyed by project identifier + issue display_id " +
	"(the stable cross-archive key — issue UUIDs are remapped on import, display IDs are not). " +
	"Key result links to soft-deleted issues are excluded, matching the live-set semantics. " +
	"Page-issue links carry id, project_identifier, page_id, page_title, issue_display_id, " +
	"created_by, created_by_email, created_at. The page is keyed by id-then-title and the issue " +
	"by display_id — the stable cross-archive keys (page/issue UUIDs are remapped on import, " +
	"display IDs are not). Links to soft-deleted issues are excluded, matching the live-set semantics. " +
	"Draft issues are included; soft-deleted issues and comments are excluded. " +
	"Attachments carry metadata only (filename, content_type, size_bytes, uploaded_by, timestamps): " +
	"binaries are NOT included and the server-side stored_path is never exported. " +
	"Not exported: Slack webhook URL (secret), issue activity history, reactions, votes, issue links (issue↔issue), " +
	"intake items, webhooks config, notification prefs, API tokens, page revisions, sessions/OTP, " +
	"the workspace audit log (witnessed history is not portable — importing audit rows would fabricate " +
	"events that never happened in the target workspace; the target keeps its own audit log). " +
	"An archive importer is available at POST /api/v1/workspaces/{slug}/import (admin only); " +
	"it honors the format version pin and refuses unknown versions."

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

// exportProjectUpdate is one project status update (C34T2). The author
// is carried as id + email, the same field discipline as issue
// comments (actor_id + actor_email): the importer resolves the author
// by email and skips the update — never invents an author — when the
// author is unmapped. author_id/author_email are null when the
// author's user row was deleted (ON DELETE SET NULL).
type exportProjectUpdate struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"project_id"`
	AuthorID    *string   `json:"author_id"`
	AuthorEmail *string   `json:"author_email"`
	Status      string    `json:"status"`
	Body        string    `json:"body"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// exportPageIssueLink is one page↔issue link (C36T2). The page is
// carried as id + title (the importer resolves id-then-title) and the
// issue as project identifier + display_id (the stable cross-archive
// key — issue UUIDs are remapped on import, display IDs are not),
// the same key discipline as KR-issue links. The creator is carried
// as id + email, the same field discipline as project updates: the
// importer resolves the creator by UUID and leaves it null when
// unmapped — never a fabricated creator.
type exportPageIssueLink struct {
	ID                string    `json:"id"`
	ProjectIdentifier string    `json:"project_identifier"`
	PageID            string    `json:"page_id"`
	PageTitle         string    `json:"page_title"`
	IssueDisplayID    string    `json:"issue_display_id"`
	CreatedBy         *string   `json:"created_by"`
	CreatedByEmail    *string   `json:"created_by_email"`
	CreatedAt         time.Time `json:"created_at"`
}

// exportKRLink is one issue link of an issue key result, keyed by
// project identifier + issue display_id — the stable cross-archive
// key. Issue UUIDs are NOT used: the importer remaps UUIDs on
// clone-restore, while identifier + sequence (display_id) survive
// the round-trip unchanged. (Existing cycle/module memberships key
// links by raw issue UUID and resolve them through the importer's
// remap; OKR links deliberately use the display key instead, so a
// link still resolves when the issue was restored under a different
// UUID by any path.)
type exportKRLink struct {
	ProjectIdentifier string `json:"project_identifier"`
	IssueDisplayID    string `json:"issue_display_id"`
}

// exportKRCheckin is one check-in entry of a metric key result
// (C42T2): the append-only history of current_value changes. Values
// are json.Number so NUMERIC precision survives the round-trip
// exactly (the same discipline as KR values). The author is carried
// as id + email, the same field discipline as the objective owner:
// the importer resolves the author by email and leaves it null when
// unmapped — never a fabricated author. PreviousValue is nil for
// the seed entry (there is no previous value at creation).
type exportKRCheckin struct {
	ID            string       `json:"id"`
	PreviousValue *json.Number `json:"previous_value"`
	NewValue      json.Number  `json:"new_value"`
	Note          *string      `json:"note"`
	AuthorID      *string      `json:"author_id"`
	AuthorEmail   *string      `json:"author_email"`
	CreatedAt     time.Time    `json:"created_at"`
}

// exportKeyResult is one key result (C35T2). Values are json.Number
// so NUMERIC precision survives the round-trip exactly (the same
// discipline as issue custom values). Metric KRs carry ALL their
// check-ins in CheckIns, oldest-first; issue KRs carry none.
type exportKeyResult struct {
	ID           string            `json:"id"`
	Title        string            `json:"title"`
	Kind         string            `json:"kind"`
	Unit         *string           `json:"unit"`
	StartValue   json.Number       `json:"start_value"`
	TargetValue  json.Number       `json:"target_value"`
	CurrentValue json.Number       `json:"current_value"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
	CheckIns     []exportKRCheckin `json:"check_ins"`
	LinkedIssues []exportKRLink    `json:"linked_issues"`
}

// exportObjective is one workspace objective (C35T2) with nested key
// results. The owner is carried as id + email, the same field
// discipline as project updates: the importer resolves the owner by
// email and leaves owner null when unmapped — never a fabricated
// owner.
type exportObjective struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Description *string           `json:"description"`
	OwnerID     *string           `json:"owner_id"`
	OwnerEmail  *string           `json:"owner_email"`
	StartDate   *string           `json:"start_date"`
	TargetDate  *string           `json:"target_date"`
	Status      string            `json:"status"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
	KeyResults  []exportKeyResult `json:"key_results"`
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
	ID              string          `json:"id"`
	ParentID        *string         `json:"parent_id"`
	ActorID         string          `json:"actor_id"`
	ActorEmail      string          `json:"actor_email"`
	Content         json.RawMessage `json:"content"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
	ResolvedAt      *time.Time      `json:"resolved_at,omitempty"`
	ResolvedBy      *string         `json:"resolved_by,omitempty"`
	ResolvedByEmail *string         `json:"resolved_by_email,omitempty"`
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

	if err := exportWorkspaceArchive(ctx, tx, wsID, w); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// streamWorkspaceBackup streams the same glance-export/1 archive for
// the workspace id WITHOUT a per-workspace actor check (C15T2). The
// backup job (scheduled or admin-triggered) runs as the instance
// operator, so there is no membership to resolve; the snapshot
// isolation, the streaming behavior and the archive format are
// identical to the interactive export. External callers go through
// service.RunBackup, which records the run and verifies the archive.
func streamWorkspaceBackup(ctx context.Context, pool *pgxpool.Pool, wsID string, w io.Writer) error {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := exportWorkspaceArchive(ctx, tx, wsID, w); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// exportWorkspaceArchive writes the full glance-export/1 archive for
// wsID into the already-open REPEATABLE READ snapshot transaction tx.
// Shared by the interactive export (after its actor check) and the
// actor-less backup job — one streaming implementation, no format
// drift between manual exports and scheduled backups.
func exportWorkspaceArchive(ctx context.Context, tx pgx.Tx, wsID string, w io.Writer) error {
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

	if err := ew.raw(`,"project_updates":`); err != nil {
		return err
	}
	if err := streamArray(ctx, ew, tx,
		`SELECT pu.id::text, pu.project_id::text, pu.author_id::text, u.email::text,
		        pu.status, pu.body, pu.created_at, pu.updated_at
		 FROM project_updates pu
		 JOIN projects p ON p.id = pu.project_id
		 LEFT JOIN users u ON u.id = pu.author_id
		 WHERE p.workspace_id = $1::uuid
		 ORDER BY pu.project_id, pu.created_at`,
		[]any{wsID},
		func(r pgx.Row) (exportProjectUpdate, error) {
			var u exportProjectUpdate
			err := r.Scan(&u.ID, &u.ProjectID, &u.AuthorID, &u.AuthorEmail,
				&u.Status, &u.Body, &u.CreatedAt, &u.UpdatedAt)
			return u, err
		}); err != nil {
		return err
	}

	if err := ew.raw(`,"issues":`); err != nil {
		return err
	}
	if err := streamIssues(ctx, ew, tx, wsID); err != nil {
		return err
	}

	// Page-issue links stream after issues: their issue side resolves
	// against the issues section, and the importer processes sections
	// in archive order, so links must come after issues. Links to
	// soft-deleted issues are excluded — they are not live data, and
	// the importer cannot restore a link to an issue the archive
	// never carries (the same discipline as KR-issue links).
	if err := ew.raw(`,"page_issue_links":`); err != nil {
		return err
	}
	if err := streamArray(ctx, ew, tx,
		`SELECT pil.id::text, p.identifier, pil.page_id::text, pg.title,
		        (p.identifier || '-' || i.sequence_id),
		        pil.created_by::text, u.email::text, pil.created_at
		 FROM page_issue_links pil
		 JOIN pages pg ON pg.id = pil.page_id
		 JOIN projects p ON p.id = pg.project_id
		 JOIN issues i ON i.id = pil.issue_id AND i.deleted_at IS NULL
		 LEFT JOIN users u ON u.id = pil.created_by
		 WHERE p.workspace_id = $1::uuid
		 ORDER BY p.identifier, pg.created_at, i.sequence_id, pil.created_at`,
		[]any{wsID},
		func(r pgx.Row) (exportPageIssueLink, error) {
			var l exportPageIssueLink
			err := r.Scan(&l.ID, &l.ProjectIdentifier, &l.PageID, &l.PageTitle,
				&l.IssueDisplayID, &l.CreatedBy, &l.CreatedByEmail, &l.CreatedAt)
			return l, err
		}); err != nil {
		return err
	}

	// Objectives stream last: their KR-issue links resolve against
	// the issues section, and the importer processes sections in
	// archive order, so links must come after issues.
	if err := ew.raw(`,"objectives":`); err != nil {
		return err
	}
	if err := streamObjectives(ctx, ew, tx, wsID); err != nil {
		return err
	}

	if err := ew.raw("}"); err != nil {
		return err
	}
	return nil
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

// streamObjectives streams workspace objectives with nested key
// results + KR-issue links (C35T2). Base rows are collected first
// (see streamEstimates: no nested queries on an open rows); each
// objective's KRs and each KR's links are then loaded one at a time.
func streamObjectives(ctx context.Context, ew *exportStreamWriter, tx pgx.Tx, wsID string) error {
	var objectives []exportObjective
	collectErr := func() error {
		rows, err := tx.Query(ctx,
			`SELECT o.id::text, o.title, o.description, o.owner_id::text, ow.email::text,
			        to_char(o.start_date, 'YYYY-MM-DD'), to_char(o.target_date, 'YYYY-MM-DD'),
			        o.status, o.created_at, o.updated_at
			 FROM objectives o
			 LEFT JOIN users ow ON ow.id = o.owner_id
			 WHERE o.workspace_id = $1::uuid
			 ORDER BY o.created_at, o.id`,
			wsID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var o exportObjective
			if err := rows.Scan(&o.ID, &o.Title, &o.Description, &o.OwnerID, &o.OwnerEmail,
				&o.StartDate, &o.TargetDate, &o.Status, &o.CreatedAt, &o.UpdatedAt); err != nil {
				return err
			}
			objectives = append(objectives, o)
		}
		return rows.Err()
	}()
	if collectErr != nil {
		return collectErr
	}
	if err := ew.raw("["); err != nil {
		return err
	}
	for i := range objectives {
		if err := fillObjectiveKeyResults(ctx, tx, &objectives[i]); err != nil {
			return err
		}
		if i > 0 {
			if err := ew.raw(","); err != nil {
				return err
			}
		}
		if err := ew.value(objectives[i]); err != nil {
			return err
		}
	}
	return ew.raw("]")
}

// fillObjectiveKeyResults loads one objective's key results with
// their check-ins (metric KRs only, ALL rows oldest-first — the API
// list endpoint's cap-100 does NOT apply to the archive) and their
// issue links. Links to soft-deleted issues are excluded —
// they are not live data, and the importer cannot restore a link to
// an issue the archive never carries. Issue KRs carry no check-ins.
func fillObjectiveKeyResults(ctx context.Context, tx pgx.Tx, o *exportObjective) error {
	o.KeyResults = []exportKeyResult{}
	krRows, err := tx.Query(ctx,
		`SELECT kr.id::text, kr.title, kr.kind, kr.unit,
		        kr.start_value::text, kr.target_value::text, kr.current_value::text,
		        kr.created_at, kr.updated_at
		 FROM key_results kr
		 WHERE kr.objective_id = $1::uuid
		 ORDER BY kr.created_at, kr.id`,
		o.ID)
	if err != nil {
		return err
	}
	for krRows.Next() {
		var kr exportKeyResult
		var start, target, current string
		if err := krRows.Scan(&kr.ID, &kr.Title, &kr.Kind, &kr.Unit,
			&start, &target, &current, &kr.CreatedAt, &kr.UpdatedAt); err != nil {
			krRows.Close()
			return err
		}
		kr.StartValue = json.Number(start)
		kr.TargetValue = json.Number(target)
		kr.CurrentValue = json.Number(current)
		o.KeyResults = append(o.KeyResults, kr)
	}
	krRows.Close()
	if err := krRows.Err(); err != nil {
		return err
	}
	for i := range o.KeyResults {
		if o.KeyResults[i].Kind == "issue" {
			o.KeyResults[i].CheckIns = []exportKRCheckin{}
			links, err := loadKRLinks(ctx, tx, o.KeyResults[i].ID)
			if err != nil {
				return err
			}
			o.KeyResults[i].LinkedIssues = links
			continue
		}
		o.KeyResults[i].LinkedIssues = []exportKRLink{}
		checkIns, err := loadKRCheckins(ctx, tx, o.KeyResults[i].ID)
		if err != nil {
			return err
		}
		o.KeyResults[i].CheckIns = checkIns
	}
	return nil
}

// loadKRCheckins loads ALL of one metric KR's check-ins, oldest
// first, for a stable round-trip. No cap: the archive must carry the
// full history, unlike the API list endpoint (cap-100). The id
// tiebreaker keeps the order deterministic when rows share a
// timestamp (backfilled / imported rows can).
func loadKRCheckins(ctx context.Context, tx pgx.Tx, krID string) ([]exportKRCheckin, error) {
	out := []exportKRCheckin{}
	rows, err := tx.Query(ctx,
		`SELECT ci.id::text, ci.previous_value::text, ci.new_value::text,
		        ci.note, ci.author_id::text, u.email::text, ci.created_at
		 FROM key_result_checkins ci
		 LEFT JOIN users u ON u.id = ci.author_id
		 WHERE ci.key_result_id = $1::uuid
		 ORDER BY ci.created_at ASC, ci.id ASC`,
		krID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c exportKRCheckin
		var previous *string
		var newValue string
		if err := rows.Scan(&c.ID, &previous, &newValue, &c.Note,
			&c.AuthorID, &c.AuthorEmail, &c.CreatedAt); err != nil {
			return nil, err
		}
		if previous != nil {
			n := json.Number(*previous)
			c.PreviousValue = &n
		}
		c.NewValue = json.Number(newValue)
		out = append(out, c)
	}
	return out, rows.Err()
}

// loadKRLinks loads one issue KR's links as (project identifier,
// issue display_id) pairs.
func loadKRLinks(ctx context.Context, tx pgx.Tx, krID string) ([]exportKRLink, error) {
	links := []exportKRLink{}
	rows, err := tx.Query(ctx,
		`SELECT p.identifier, (p.identifier || '-' || i.sequence_id)
		 FROM key_result_issues kri
		 JOIN issues i ON i.id = kri.issue_id AND i.deleted_at IS NULL
		 JOIN projects p ON p.id = i.project_id
		 WHERE kri.key_result_id = $1::uuid
		 ORDER BY i.sequence_id`,
		krID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var l exportKRLink
		if err := rows.Scan(&l.ProjectIdentifier, &l.IssueDisplayID); err != nil {
			return nil, err
		}
		links = append(links, l)
	}
	return links, rows.Err()
}
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
		        c.content, c.created_at, c.updated_at,
		        c.resolved_at, c.resolved_by::text, r.email::text
		 FROM comments c
		 JOIN users u ON u.id = c.actor_id
		 LEFT JOIN users r ON r.id = c.resolved_by
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
			&content, &c.CreatedAt, &c.UpdatedAt,
			&c.ResolvedAt, &c.ResolvedBy, &c.ResolvedByEmail); err != nil {
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
