package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrIdentifierConflict is returned when the project identifier is
	// already taken in the workspace (compared case-insensitively: the
	// service uppercases before insert, so "eng" and "ENG" collide).
	ErrIdentifierConflict = errors.New("service: identifier already taken")
	// ErrInvalidIdentifier is returned when the identifier is empty,
	// longer than 12 chars, or contains anything but letters/digits.
	ErrInvalidIdentifier = errors.New("service: invalid identifier")
	// ErrProjectNotFound is returned when the project identifier matches
	// nothing in the workspace. It is deliberately distinct from
	// ErrNotFound (missing workspace / non-member caller): the workspace
	// is confirmed to exist and the caller confirmed a member on this
	// path, so "workspace not found" would be actively misleading.
	ErrProjectNotFound = errors.New("service: project not found")
	// ErrNothingToUpdate is returned by UpdateProject when the patch carries
	// no fields: PATCH with {} is a client error, not a no-op success.
	ErrNothingToUpdate = errors.New("service: nothing to update")
	// ErrInvalidIcon is returned when the project icon is not a single
	// emoji. The icon contract: exactly one emoji (one grapheme cluster).
	// ZWJ sequences (family/couple/profession combos), multi-emoji
	// strings, and non-emoji text are rejected; an empty string is
	// normalized to NULL (unset) rather than rejected.
	ErrInvalidIcon = errors.New("service: invalid project icon: must be a single emoji")
	// ErrConfirmMismatch is returned by DeleteProject when the
	// ?confirm= value does not match the project's identifier. The
	// typed confirmation is a safety interlock, not authz: it is checked
	// after the admin role gate.
	ErrConfirmMismatch = errors.New("service: confirm does not match the project identifier")
	// ErrProjectDeleteBlocked is returned by DeleteProject when the
	// project still has non-archived issues and force is false. The
	// concrete *DeleteBlockedError carries the count for the 409 details.
	ErrProjectDeleteBlocked = errors.New("service: project has non-archived issues")
)

// DeleteBlockedError is the concrete ErrProjectDeleteBlocked: the delete
// was refused because the project still has live issues. NonArchivedIssues
// is the blocker count surfaced in the 409 details.
type DeleteBlockedError struct {
	NonArchivedIssues int
}

func (e *DeleteBlockedError) Error() string {
	return fmt.Sprintf("service: project has %d non-archived issues", e.NonArchivedIssues)
}

// Is makes *DeleteBlockedError match the ErrProjectDeleteBlocked sentinel
// via errors.Is, so handlers can map it without knowing the concrete type.
func (e *DeleteBlockedError) Is(target error) bool {
	return target == ErrProjectDeleteBlocked
}

// Project is an issue container inside a workspace, addressed by its
// uppercase identifier (ENG-123 style display IDs build on it in Task 14).
type Project struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	Identifier  string `json:"identifier"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// Icon is the project's emoji icon (C18T4, migration 000046): NULL =
	// unset. Server-side validated to a single emoji on write; empty
	// string normalizes to NULL.
	Icon          *string         `json:"icon,omitempty"`
	ViewFlags     json.RawMessage `json:"view_flags,omitempty"`
	ArchiveInDays *int            `json:"archive_in_days,omitempty"`
	CloseInDays   *int            `json:"close_in_days,omitempty"`
	// AutomationRunRetentionDays is the automation run retention window
	// in days (C13T0, migration 000039): NULL = unset = the 90-day
	// default (service.DefaultAutomationRunRetentionDays), 0 = keep
	// forever. Negative is rejected at write.
	AutomationRunRetentionDays *int `json:"automation_run_retention_days,omitempty"`
	// ArchivedAt is the project archive timestamp (C18T3, migration
	// 000048): NULL = live. Archived projects are excluded from the
	// project list, sidebar, and global search; archiving a project
	// archives all its issues in the same transaction (reusing the
	// issue archived_at semantics).
	ArchivedAt *time.Time `json:"archived_at,omitempty"`
	// Labels are the workspace project-labels assigned to this project
	// (C76T0, migration 000070): [{id,name,color}] ordered by name.
	// Always present ([] never null) on every project payload.
	// ParallelCycles is the per-project parallel-cycles toggle (C78T0,
	// migration 000071): when true, multiple cycles of the project may
	// run simultaneously with overlapping date ranges. When false
	// (default), CreateCycle/UpdateCycle reject date ranges overlapping
	// another live cycle of the project.
	ParallelCycles bool           `json:"parallel_cycles"`
	Labels         []ProjectLabel `json:"labels"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	// TeamspaceID is the project's teamspace (C89T0, migration
	// 000076): NULL = ungrouped. Always present in JSON (null, never
	// omitted) so the sidebar groups on one fetch without guessing
	// whether the field was populated.
	TeamspaceID *string `json:"teamspace_id"`
}

// State is a kanban column of a project. Group is the Plane vocabulary
// (triage/backlog/unstarted/started/completed/cancelled); Sequence orders
// the columns left to right.
type State struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id"`
	Name      string    `json:"name"`
	Group     string    `json:"group"`
	Color     string    `json:"color"`
	Sequence  int       `json:"sequence"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// identifierRe enforces the identifier contract: 1-12 uppercase letters
// and digits. Input is uppercased before matching, so "eng" is accepted
// and stored as "ENG".
var identifierRe = regexp.MustCompile(`^[A-Z0-9]+$`)

func normalizeIdentifier(s string) (string, error) {
	ident := strings.ToUpper(strings.TrimSpace(s))
	if len(ident) < 1 || len(ident) > 12 || !identifierRe.MatchString(ident) {
		return "", ErrInvalidIdentifier
	}
	return ident, nil
}

// Unicode code points relevant to the single-emoji icon contract.
const (
	iconZWJ                   = 0x200D // zero-width joiner: compounds emoji
	iconTextPresentationSel   = 0xFE0E // forces text presentation
	iconVariationSelector16   = 0xFE0F // forces emoji presentation
	iconCombiningEnclosingKey = 0x20E3 // keycap combiner
	iconSkinToneModifierMin   = 0x1F3FB
	iconSkinToneModifierMax   = 0x1F3FF
	iconRegionalIndicatorMin  = 0x1F1E6
	iconRegionalIndicatorMax  = 0x1F1FF
)

// isEmojiBase reports whether r is a single-rune emoji base: one code
// point that renders as an emoji on its own. Regional indicators are
// excluded — a lone one is not an emoji, only a pair (a flag) is.
func isEmojiBase(r rune) bool {
	switch {
	case r >= 0x1F300 && r <= 0x1FAFF: // emoticons, symbols, transport, pictographs
		return true
	case r >= 0x2600 && r <= 0x27BF: // misc symbols, dingbats
		return true
	case r >= 0x2B00 && r <= 0x2BFF: // misc symbols and arrows
		return true
	case r >= 0x2300 && r <= 0x23FF: // misc technical (⌚ ⌛ ⏰ …)
		return true
	case r == 0x00A9 || r == 0x00AE: // © ®
		return true
	}
	return false
}

func isRegionalIndicator(r rune) bool {
	return r >= iconRegionalIndicatorMin && r <= iconRegionalIndicatorMax
}

func isSkinToneModifier(r rune) bool {
	return r >= iconSkinToneModifierMin && r <= iconSkinToneModifierMax
}

func isKeycapBase(r rune) bool {
	return (r >= '0' && r <= '9') || r == '#' || r == '*'
}

// isSingleEmoji reports whether s is exactly one emoji: one base emoji
// rune, optionally followed by VS16 (emoji presentation) or a skin-tone
// modifier; a flag (exactly two regional indicators); or a keycap
// sequence (base + VS16 + enclosing keycap). ZWJ sequences (👨‍👩‍👧),
// text-presentation selectors, and anything longer than one grapheme are
// rejected — the icon contract is one icon, one glyph.
func isSingleEmoji(s string) bool {
	runes := []rune(s)
	for _, r := range runes {
		if r == iconZWJ || r == iconTextPresentationSel {
			return false
		}
		if r <= 0x1F || r == 0x7F || r == ' ' {
			return false
		}
	}
	switch len(runes) {
	case 1:
		return isEmojiBase(runes[0])
	case 2:
		// Flag: exactly two regional indicators, e.g. 🇺🇸.
		if isRegionalIndicator(runes[0]) && isRegionalIndicator(runes[1]) {
			return true
		}
		if !isEmojiBase(runes[0]) {
			// Bare keycap (no VS16), e.g. 1⃣, is still one emoji.
			return isKeycapBase(runes[0]) && runes[1] == iconCombiningEnclosingKey
		}
		return runes[1] == iconVariationSelector16 || isSkinToneModifier(runes[1])
	case 3:
		// Keycap: base + VS16 + enclosing keycap, e.g. 1️⃣.
		return isKeycapBase(runes[0]) &&
			runes[1] == iconVariationSelector16 &&
			runes[2] == iconCombiningEnclosingKey
	default:
		return false
	}
}

// normalizeProjectIcon validates a project icon for storage. A trimmed
// empty string normalizes to nil (stored NULL = unset); otherwise the
// value must be exactly one emoji, else ErrInvalidIcon.
func normalizeProjectIcon(s string) (*string, error) {
	t := strings.TrimSpace(s)
	if t == "" {
		return nil, nil
	}
	if !isSingleEmoji(t) {
		return nil, ErrInvalidIcon
	}
	return &t, nil
}

// defaultStates are seeded for every new project in the same transaction
// as the project row — a project without states is unusable, so the two
// must commit atomically. Sequences are spaced 10000 apart so a future
// column-reorder (Task 23 style) has room without renumbering.
var defaultStates = []struct{ name, group, color string }{
	{"Backlog", "backlog", "#6b7280"},
	{"Todo", "unstarted", "#3b82f6"},
	{"In Progress", "started", "#f59e0b"},
	{"Done", "completed", "#22c55e"},
	{"Cancelled", "cancelled", "#ef4444"},
}

// scanProject scans the full project column list produced by the queries
// below. view_flags is nullable JSONB: a NULL scans into a nil []byte and
// stays omitted from the JSON response via omitempty.
func scanProjectBase(row pgx.Row) (*Project, error) {
	var p Project
	var viewFlags []byte
	err := row.Scan(
		&p.ID, &p.WorkspaceID, &p.Identifier, &p.Name, &p.Description,
		&p.Icon, &viewFlags, &p.ArchiveInDays, &p.CloseInDays, &p.AutomationRunRetentionDays, &p.ArchivedAt, &p.ParallelCycles, &p.CreatedAt, &p.UpdatedAt, &p.TeamspaceID,
	)
	if err != nil {
		return nil, err
	}
	if viewFlags != nil {
		p.ViewFlags = json.RawMessage(viewFlags)
	}
	// Labels default to [] — callers that select the chip aggregate
	// overwrite this via scanProject; write paths that cannot carry
	// the aggregate load them separately.
	p.Labels = []ProjectLabel{}
	return &p, nil
}

// scanProject scans a row carrying the trailing C76T0 label-chip
// aggregate (projectColumns / projectColumnsP): the base columns plus
// the decoded chips, [] never null.
func scanProject(row pgx.Row) (*Project, error) {
	var p Project
	var viewFlags []byte
	var labelsJSON []byte
	err := row.Scan(
		&p.ID, &p.WorkspaceID, &p.Identifier, &p.Name, &p.Description,
		&p.Icon, &viewFlags, &p.ArchiveInDays, &p.CloseInDays, &p.AutomationRunRetentionDays, &p.ArchivedAt, &p.ParallelCycles, &p.CreatedAt, &p.UpdatedAt, &p.TeamspaceID,
		&labelsJSON,
	)
	if err != nil {
		return nil, err
	}
	if viewFlags != nil {
		p.ViewFlags = json.RawMessage(viewFlags)
	}
	labels, err := decodeProjectLabels(labelsJSON)
	if err != nil {
		return nil, err
	}
	p.Labels = labels
	return &p, nil
}

// projectColumnsBase is the bare column list without the C76T0
// label-chip aggregate — for the one context where the aggregate
// cannot correlate: UPDATE ... RETURNING (PostgreSQL does not expose
// the target table by name to subqueries there). That path loads the
// chips separately via loadProjectLabels.
//
// projectColumns is the bare column list for INSERT ... RETURNING and
// SELECT ... FROM projects (no alias); projectColumnsP is the same
// list with the p. alias for SELECT ... FROM projects p. Both carry
// the label-chip aggregate as the trailing column (scanProject decodes
// it).
const projectColumnsBase = `id::text, workspace_id::text, identifier, name, description,
	icon, view_flags, archive_in_days, close_in_days, automation_run_retention_days, archived_at, parallel_cycles, created_at, updated_at, teamspace_id::text`

const projectColumnsBaseP = `p.id::text, p.workspace_id::text, p.identifier, p.name, p.description,
	p.icon, p.view_flags, p.archive_in_days, p.close_in_days, p.automation_run_retention_days, p.archived_at, p.parallel_cycles, p.created_at, p.updated_at, p.teamspace_id::text`

var projectColumns = projectColumnsBase + `,
	` + projectLabelsAgg("projects")

var projectColumnsP = projectColumnsBaseP + `,
	` + projectLabelsAgg("p")

// CreateProject inserts the project and seeds its default states in one
// transaction. The caller must be a workspace member (15) or admin (20);
// guests (5) get ErrForbidden, non-members ErrNotFound.
//
// Delegates to CreateProjectWithTemplate with no template and no icon;
// kept so the existing call sites and tests do not change shape.
func CreateProject(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID, name, identifier string) (*Project, error) {
	return CreateProjectWithTemplate(ctx, pool, wsSlug, actorID, name, identifier, nil, nil)
}

// ListProjects returns the workspace's live projects ordered by name.
// Archived projects are excluded unless includeArchived is true (C18T3:
// the projects page renders them in a separate "Archived" section).
// labelIDs (C76T0) optionally filters to projects carrying ANY of the
// given project-label ids (EXISTS on the junction, OR semantics,
// parameterized — never interpolated). Malformed values, or ids that
// name no label of the workspace, are ErrInvalidProjectLabelFilter
// (400). Non-members get ErrNotFound (see GetWorkspace).
func ListProjects(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID string, includeArchived bool, labelIDs []string) ([]Project, error) {
	wsID, _, err := workspaceIDForActor(pool.QueryRow(ctx,
		`SELECT w.id::text, m.role
		 FROM workspaces w
		 JOIN workspace_members m ON m.workspace_id = w.id
		 WHERE w.slug = $1 AND m.user_id = $2::uuid`,
		wsSlug, actorID))
	if err != nil {
		return nil, err
	}

	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	wsArg := arg(wsID)

	var conds []string
	conds = append(conds, `p.workspace_id = `+wsArg+`::uuid`)
	if !includeArchived {
		conds = append(conds, `p.archived_at IS NULL`)
	}
	if len(labelIDs) > 0 {
		ids, err := validateProjectLabelFilter(ctx, pool, wsID, labelIDs)
		if err != nil {
			return nil, err
		}
		if len(ids) > 0 {
			conds = append(conds, `EXISTS (SELECT 1 FROM project_project_labels ppl
				WHERE ppl.project_id = p.id AND ppl.label_id = ANY(`+arg(ids)+`::uuid[]))`)
		}
	}

	query := `SELECT ` + projectColumnsP + ` FROM projects p
		 WHERE ` + strings.Join(conds, " AND ") + `
		 ORDER BY p.name`
	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// GetProject returns one project by identifier (case-insensitive: the
// lookup is uppercased the same way inserts are). Non-members get
// ErrNotFound; members asking for a missing identifier get
// ErrProjectNotFound.
func GetProject(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string) (*Project, error) {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return nil, err
	}
	wsID, _, err := workspaceIDForActor(pool.QueryRow(ctx,
		`SELECT w.id::text, m.role
		 FROM workspaces w
		 JOIN workspace_members m ON m.workspace_id = w.id
		 WHERE w.slug = $1 AND m.user_id = $2::uuid`,
		wsSlug, actorID))
	if err != nil {
		return nil, err
	}

	p, err := scanProject(pool.QueryRow(ctx,
		`SELECT `+projectColumnsP+` FROM projects p
		 WHERE p.workspace_id = $1::uuid AND p.identifier = $2`,
		wsID, ident))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrProjectNotFound
		}
		return nil, err
	}
	return p, nil
}

// ProjectPatch is a partial project update: nil fields are left unchanged,
// so PATCH carries true partial-update semantics (a body that omits
// description does not wipe it). An explicit empty string is honored as a
// clear — JSON null and "" stay distinct.
type ProjectPatch struct {
	Name        *string
	Description *string
	// Icon is the project emoji icon (C18T4): nil = untouched; non-nil is
	// validated as a single emoji, and an explicit empty string clears
	// it (stored NULL = unset).
	Icon *string
	// CloseInDays is the ticker's rollover rule (Task 22): days after a
	// cycle's end_date before incomplete issues detach to the backlog
	// when no next cycle exists. Nil = untouched; explicit 0 (or a NULL
	// column) detaches at completion. Negative is rejected.
	CloseInDays *int
	// AutomationRunRetentionDays is the automation run retention window
	// in days (C13T0): nil = untouched; explicit 0 keeps runs forever
	// (or a NULL column = the 90-day default). Negative is rejected
	// with ErrInvalidAutomationRunRetentionDays.
	AutomationRunRetentionDays *int
	// ParallelCycles is the parallel-cycles toggle (C78T0, migration
	// 000071): nil = untouched. true lets cycles of this project overlap
	// in date; false (default) makes CreateCycle/UpdateCycle reject
	// date ranges overlapping another live (upcoming/current) cycle.
	// Grandfathering is deliberate: flipping the toggle OFF with
	// pre-existing overlapping cycles does NOT retroactively alter
	// them — only new creates/updates are blocked.
	ParallelCycles *bool
	// TeamspaceID is the teamspace assignment (C89T0): tri-state via
	// PatchField — Set=false = untouched, Set=true with Value=nil =
	// explicit null (ungroup), Set=true with a UUID = move. A set
	// value is validated to a teamspace of the same workspace;
	// unknown, malformed, or foreign-workspace ids are
	// ErrInvalidTeamspace (400), never a silent cross-workspace link.
	TeamspaceID PatchField[string]
}

// UpdateProject applies a partial project update. Member (15) or admin
// (20); guests get ErrForbidden. Only non-nil patch fields are written,
// so omitting a field leaves it untouched — PATCH carries true
// partial-update semantics. An explicit empty string is honored as a clear
// (null and "" stay distinct). A patch with no fields is
// ErrNothingToUpdate. The role check and the update run in one transaction
// so a concurrent demotion cannot slip between them.
func UpdateProject(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string, patch ProjectPatch) (*Project, error) {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return nil, err
	}

	// Build the SET clause from non-nil fields only. args[0]/args[1] are
	// reserved for wsID/ident in the WHERE clause, hence the +2 offset on
	// the placeholder indices.
	var sets []string
	args := make([]any, 0, 4)
	if patch.Name != nil {
		name := strings.TrimSpace(*patch.Name)
		if name == "" {
			return nil, ErrNameRequired
		}
		args = append(args, name)
		sets = append(sets, fmt.Sprintf("name = $%d", len(args)+2))
	}
	if patch.Description != nil {
		args = append(args, *patch.Description)
		sets = append(sets, fmt.Sprintf("description = $%d", len(args)+2))
	}
	if patch.Icon != nil {
		icon, err := normalizeProjectIcon(*patch.Icon)
		if err != nil {
			return nil, err
		}
		// nil *string encodes as NULL: an explicit empty string clears
		// the icon.
		args = append(args, icon)
		sets = append(sets, fmt.Sprintf("icon = $%d", len(args)+2))
	}
	if patch.CloseInDays != nil {
		if *patch.CloseInDays < 0 {
			return nil, ErrInvalidCloseInDays
		}
		args = append(args, *patch.CloseInDays)
		sets = append(sets, fmt.Sprintf("close_in_days = $%d", len(args)+2))
	}
	if patch.AutomationRunRetentionDays != nil {
		if *patch.AutomationRunRetentionDays < 0 {
			return nil, ErrInvalidAutomationRunRetentionDays
		}
		args = append(args, *patch.AutomationRunRetentionDays)
		sets = append(sets, fmt.Sprintf("automation_run_retention_days = $%d", len(args)+2))
	}
	if patch.ParallelCycles != nil {
		args = append(args, *patch.ParallelCycles)
		sets = append(sets, fmt.Sprintf("parallel_cycles = $%d", len(args)+2))
	}
	if patch.TeamspaceID.Set {
		// A nil *string encodes as NULL: explicit null ungroups the
		// project. Same-workspace validation runs in-tx below, after
		// the workspace resolves — a bad id must not reach the UPDATE.
		args = append(args, patch.TeamspaceID.Value)
		sets = append(sets, fmt.Sprintf("teamspace_id = $%d", len(args)+2))
	}
	if len(sets) == 0 {
		return nil, ErrNothingToUpdate
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	wsID, role, err := workspaceIDForActor(tx.QueryRow(ctx,
		`SELECT w.id::text, m.role
		 FROM workspaces w
		 JOIN workspace_members m ON m.workspace_id = w.id
		 WHERE w.slug = $1 AND m.user_id = $2::uuid`,
		wsSlug, actorID))
	if err != nil {
		return nil, err
	}
	if role < RoleMember {
		return nil, ErrForbidden
	}
	if patch.TeamspaceID.Set && patch.TeamspaceID.Value != nil {
		if err := resolveProjectTeamspace(ctx, tx, wsID, *patch.TeamspaceID.Value); err != nil {
			return nil, err
		}
	}

	p, err := scanProjectBase(tx.QueryRow(ctx,
		`UPDATE projects p SET `+strings.Join(sets, ", ")+`, updated_at = now()
		 WHERE p.workspace_id = $1::uuid AND p.identifier = $2
		 RETURNING `+projectColumnsBase,
		append([]any{wsID, ident}, args...)...))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrProjectNotFound
		}
		return nil, err
	}
	// UPDATE ... RETURNING cannot correlate the chip aggregate
	// (projectColumns is unusable here), so the chips load in-tx.
	p.Labels, err = loadProjectLabels(ctx, tx, p.ID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return p, nil
}

// ListStates returns the project's states ordered by sequence (kanban
// column order). Non-members get ErrNotFound; a missing project is
// ErrProjectNotFound.
func ListStates(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string) ([]State, error) {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return nil, err
	}
	wsID, _, err := workspaceIDForActor(pool.QueryRow(ctx,
		`SELECT w.id::text, m.role
		 FROM workspaces w
		 JOIN workspace_members m ON m.workspace_id = w.id
		 WHERE w.slug = $1 AND m.user_id = $2::uuid`,
		wsSlug, actorID))
	if err != nil {
		return nil, err
	}

	var projectID string
	if err := pool.QueryRow(ctx,
		`SELECT p.id::text FROM projects p
		 WHERE p.workspace_id = $1::uuid AND p.identifier = $2`,
		wsID, ident).Scan(&projectID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrProjectNotFound
		}
		return nil, err
	}

	rows, err := pool.Query(ctx,
		`SELECT s.id::text, s.project_id::text, s.name, s."group", s.color,
		        s.sequence, s.created_at, s.updated_at
		 FROM states s
		 WHERE s.project_id = $1::uuid
		 ORDER BY s.sequence`,
		projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []State{}
	for rows.Next() {
		var s State
		if err := rows.Scan(
			&s.ID, &s.ProjectID, &s.Name, &s.Group, &s.Color,
			&s.Sequence, &s.CreatedAt, &s.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ArchiveProject archives (archived=true) or unarchives (archived=false)
// a project. Admin (20) ONLY — members, viewers, and guests get
// ErrForbidden; non-members get ErrNotFound (never 403: the workspace
// existence must not leak to outsiders).
//
// Archiving sets projects.archived_at and, in the SAME transaction, sets
// archived_at = now() on every live issue of the project — reusing the
// issue archived_at semantics (archived issues drop out of default
// lists, analytics, and automation). Unarchiving clears the project's
// archived_at and clears archived_at on exactly the issues the archive
// set: issues archived individually BEFORE the project archive carry an
// earlier timestamp and are preserved. now() is the transaction start
// timestamp, so the archive's issues all share archived_at ==
// projects.archived_at at unarchive time.
//
// Both directions are idempotent: archiving an archived project (or
// unarchiving a live one) is a no-op returning the current row. The
// project row is locked FOR UPDATE so concurrent archive/unarchive/delete
// calls serialize.
func ArchiveProject(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string, archived bool) (*Project, error) {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return nil, err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	wsID, role, err := workspaceIDForActor(tx.QueryRow(ctx,
		`SELECT w.id::text, m.role
		 FROM workspaces w
		 JOIN workspace_members m ON m.workspace_id = w.id
		 WHERE w.slug = $1 AND m.user_id = $2::uuid`,
		wsSlug, actorID))
	if err != nil {
		return nil, err
	}
	if role != RoleAdmin {
		return nil, ErrForbidden
	}

	var projectID string
	var prevArchivedAt *time.Time
	if err := tx.QueryRow(ctx,
		`SELECT id::text, archived_at FROM projects
		 WHERE workspace_id = $1::uuid AND identifier = $2
		 FOR UPDATE`,
		wsID, ident).Scan(&projectID, &prevArchivedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrProjectNotFound
		}
		return nil, err
	}

	isArchived := prevArchivedAt != nil
	if archived == isArchived {
		// Idempotent no-op: already in the desired state.
		p, err := scanProject(tx.QueryRow(ctx,
			`SELECT `+projectColumns+` FROM projects WHERE id = $1::uuid`, projectID))
		if err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return p, nil
	}

	if archived {
		if _, err := tx.Exec(ctx,
			`UPDATE projects SET archived_at = now(), updated_at = now()
			 WHERE id = $1::uuid`, projectID); err != nil {
			return nil, err
		}
		// All live issues of the project become archived in the same
		// transaction. No per-issue activity rows or notifications: a
		// project archive is one administrative act, not N issue
		// updates, and N notifications would be a storm.
		if _, err := tx.Exec(ctx,
			`UPDATE issues SET archived_at = now(), updated_at = now()
			 WHERE project_id = $1::uuid AND archived_at IS NULL AND deleted_at IS NULL`,
			projectID); err != nil {
			return nil, err
		}
	} else {
		if _, err := tx.Exec(ctx,
			`UPDATE projects SET archived_at = NULL, updated_at = now()
			 WHERE id = $1::uuid`, projectID); err != nil {
			return nil, err
		}
		// Restore only the issues this archive archived: they share the
		// transaction-start timestamp with projects.archived_at.
		// Issues archived individually before the project archive keep
		// their earlier archived_at and stay archived.
		if _, err := tx.Exec(ctx,
			`UPDATE issues SET archived_at = NULL, updated_at = now()
			 WHERE project_id = $1::uuid AND archived_at IS NOT NULL
			   AND archived_at >= $2 AND deleted_at IS NULL`,
			projectID, prevArchivedAt); err != nil {
			return nil, err
		}
	}

	p, err := scanProject(tx.QueryRow(ctx,
		`SELECT `+projectColumns+` FROM projects WHERE id = $1::uuid`, projectID))
	if err != nil {
		return nil, err
	}
	// C37T0: archive/restore only — the idempotent no-op path above
	// returns before this point and records nothing.
	action := WorkspaceAuditProjectArchived
	if !archived {
		action = WorkspaceAuditProjectRestored
	}
	if err := recordWorkspaceAuditInTx(ctx, tx, wsID, actorID,
		action, "project", projectID,
		map[string]any{"project_identifier": ident}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return p, nil
}

// DeleteProject hard-deletes a project and, via FK cascades, everything
// under it (issues, cycles, modules, pages, views, links, ...). Admin
// (20) ONLY; non-members get ErrNotFound.
//
// Safety interlocks, in order:
//  1. Authz first: the role gate runs before any request validation, so
//     a non-admin learns nothing beyond 403.
//  2. confirm must equal the project identifier (normalized the same way
//     inserts are), else ErrConfirmMismatch.
//  3. A project with >0 non-archived issues refuses with
//     *DeleteBlockedError (naming the blocker count) unless force is
//     true — deleting a live project by accident must be a deliberate
//     two-step act.
//
// The deletes run in one transaction with the project row locked FOR
// UPDATE. Issues are deleted explicitly before the project row:
// issues.state_id -> states(id) is NO ACTION, so a bare DELETE FROM
// projects can hit the states cascade before the issues cascade and trip
// the RESTRICT check; deleting issues first (their children cascade)
// sidesteps the trigger ordering entirely.
func DeleteProject(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, confirm string, force bool) error {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	wsID, role, err := workspaceIDForActor(tx.QueryRow(ctx,
		`SELECT w.id::text, m.role
		 FROM workspaces w
		 JOIN workspace_members m ON m.workspace_id = w.id
		 WHERE w.slug = $1 AND m.user_id = $2::uuid`,
		wsSlug, actorID))
	if err != nil {
		return err
	}
	if role != RoleAdmin {
		return ErrForbidden
	}

	confirmIdent, err := normalizeIdentifier(confirm)
	if err != nil || confirmIdent != ident {
		return ErrConfirmMismatch
	}

	var projectID string
	if err := tx.QueryRow(ctx,
		`SELECT id::text FROM projects
		 WHERE workspace_id = $1::uuid AND identifier = $2
		 FOR UPDATE`,
		wsID, ident).Scan(&projectID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrProjectNotFound
		}
		return err
	}

	var live int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM issues
		 WHERE project_id = $1::uuid AND deleted_at IS NULL AND archived_at IS NULL`,
		projectID).Scan(&live); err != nil {
		return err
	}
	if live > 0 && !force {
		return &DeleteBlockedError{NonArchivedIssues: live}
	}

	// Explicit first: see the state_id NO ACTION note above.
	if _, err := tx.Exec(ctx,
		`DELETE FROM issues WHERE project_id = $1::uuid`, projectID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM projects WHERE id = $1::uuid`, projectID); err != nil {
		return err
	}
	// C37T0: project.deleted. The audit row references only the
	// workspace (not the deleted project row), so it lands in the
	// same tx after the deletes.
	if err := recordWorkspaceAuditInTx(ctx, tx, wsID, actorID,
		WorkspaceAuditProjectDeleted, "project", projectID,
		map[string]any{"project_identifier": ident}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
