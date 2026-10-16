package service

// Releases (C4T6): project-scoped milestones. Issues attach via the
// nullable issues.release_id FK (ON DELETE SET NULL), so deleting a
// release never deletes issues — it detaches them (or moves them to
// another release via ?reassign=).
//
// Conventions (mirror modules):
//   - Tenancy: every op resolves workspace membership + project via
//     resolveIssueProject. A bad slug or non-member caller surfaces
//     ErrNotFound ("workspace not found"); a bad project identifier
//     surfaces ErrProjectNotFound; a bad release id surfaces
//     ErrReleaseNotFound. Distinct 404s on purpose (Task 11 ruling).
//   - Roles: any member (guest 5+) may read; mutations need member (15)+.
//   - PATCH tri-state for nullable fields: nil = untouched, pointer to ""
//     = clear (NULL), pointer to a value = set. Applies to description
//     and release_date. Name is required non-empty when provided;
//     status is a fixed vocabulary.
//   - Delete guard: deleting a release that still has issues is 409
//     (ErrReleaseHasIssues) unless ?reassign=<releaseID> moves the issues
//     to another release of the same project, or ?force=true detaches
//     them (SET NULL).

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrReleaseNotFound is returned when the release id matches nothing
	// in the project. Deliberately distinct from ErrProjectNotFound: on
	// this path the workspace and project are confirmed and the caller
	// is a member.
	ErrReleaseNotFound = errors.New("service: release not found")
	// ErrReleaseConflict is returned when the release name is already
	// taken in the project (UNIQUE(project_id, name)).
	ErrReleaseConflict = errors.New("service: release name already exists")
	// ErrInvalidRelease is returned for malformed release input: bad
	// status vocabulary or an unparseable release date.
	ErrInvalidRelease = errors.New("service: invalid release")
	// ErrInvalidReleaseID is returned when a release id — or an issue id
	// passed to a release endpoint — is not a syntactically valid UUID.
	// The handler maps it to 400 bad_request.
	ErrInvalidReleaseID = errors.New("service: invalid release id")
	// ErrReleaseHasIssues is returned when deleting a release that still
	// has issues without ?reassign= or ?force=true. The handler maps it
	// to 409.
	ErrReleaseHasIssues = errors.New("service: release still has issues")
)

// releaseStatuses is the status vocabulary enforced by the 000023 CHECK.
var releaseStatuses = map[string]bool{
	"planned":  true,
	"released": true,
}

// Release is one project milestone.
type Release struct {
	ID          string     `json:"id"`
	ProjectID   string     `json:"project_id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Status      string     `json:"status"`
	ReleaseDate *time.Time `json:"release_date,omitempty"`
	IssueCount  int64      `json:"issue_count"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// ReleaseInput carries release creation fields. ReleaseDate is a
// YYYY-MM-DD string; nil means NULL.
type ReleaseInput struct {
	Name        string
	Description *string
	Status      string // "" = default "planned"
	ReleaseDate *string
}

// ReleasePatch is a partial release update: nil fields are untouched; a
// non-nil pointer to "" clears a nullable field (NULL); otherwise the
// value is set. Name, when provided, must be non-empty.
type ReleasePatch struct {
	Name        *string
	Description *string
	Status      *string
	ReleaseDate *string
}

// resolveReleaseProject resolves (projectID, role) for the caller.
// Guests may read; callers pass needMember to enforce member (15)+.
func resolveReleaseProject(ctx context.Context, q queryRower, wsSlug, identifier, actorID string, needMember bool) (projectID string, role int, err error) {
	_, projectID, role, err = resolveIssueProject(ctx, q, wsSlug, identifier, actorID)
	if err != nil {
		return "", 0, err
	}
	if needMember && role < RoleMember {
		return "", 0, ErrForbidden
	}
	return projectID, role, nil
}

const releaseColumns = `id::text, project_id::text, name, description, status,
	release_date, created_at, updated_at`

// releaseColumnsCounted adds the live issue count for list/get responses.
const releaseColumnsCounted = releaseColumns + `,
	(SELECT COUNT(*) FROM issues i WHERE i.release_id = releases.id AND i.deleted_at IS NULL)`

func scanRelease(row pgx.Row) (Release, error) {
	var r Release
	err := row.Scan(
		&r.ID, &r.ProjectID, &r.Name, &r.Description, &r.Status,
		&r.ReleaseDate, &r.CreatedAt, &r.UpdatedAt,
	)
	return r, err
}

func scanReleaseCounted(row pgx.Row) (Release, error) {
	var r Release
	err := row.Scan(
		&r.ID, &r.ProjectID, &r.Name, &r.Description, &r.Status,
		&r.ReleaseDate, &r.CreatedAt, &r.UpdatedAt, &r.IssueCount,
	)
	return r, err
}

// resolveRelease loads a release of the project or ErrReleaseNotFound.
func resolveRelease(ctx context.Context, q queryRower, projectID, releaseID string) (Release, error) {
	if !isUUIDFormat(releaseID) {
		return Release{}, ErrInvalidReleaseID
	}
	r, err := scanRelease(q.QueryRow(ctx,
		`SELECT `+releaseColumns+` FROM releases
		  WHERE id = $1::uuid AND project_id = $2::uuid`,
		releaseID, projectID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Release{}, ErrReleaseNotFound
		}
		return Release{}, err
	}
	return r, nil
}

// parseReleaseDate parses a YYYY-MM-DD string; "" means NULL.
func parseReleaseDate(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil, ErrInvalidRelease
	}
	return &t, nil
}

// normalizeReleaseInput validates creation fields, returning the cleaned
// name, status, description, and release date.
func normalizeReleaseInput(in ReleaseInput) (name, status, desc string, relDate *time.Time, err error) {
	name = strings.TrimSpace(in.Name)
	if name == "" {
		return "", "", "", nil, ErrNameRequired
	}
	status = in.Status
	if status == "" {
		status = "planned"
	}
	if !releaseStatuses[status] {
		return "", "", "", nil, ErrInvalidRelease
	}
	if in.Description != nil {
		desc = strings.TrimSpace(*in.Description)
	}
	if relDate, err = parseReleaseDate(orEmpty(in.ReleaseDate)); err != nil {
		return "", "", "", nil, err
	}
	return name, status, desc, relDate, nil
}

// CreateRelease creates a release in the project. Member (15)+.
func CreateRelease(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string, in ReleaseInput) (*Release, error) {
	projectID, _, err := resolveReleaseProject(ctx, pool, wsSlug, identifier, actorID, true)
	if err != nil {
		return nil, err
	}
	name, status, desc, relDate, err := normalizeReleaseInput(in)
	if err != nil {
		return nil, err
	}
	r, err := scanRelease(pool.QueryRow(ctx,
		`INSERT INTO releases (project_id, name, description, status, release_date)
		 VALUES ($1::uuid, $2, $3, $4, $5::date)
		 RETURNING `+releaseColumns,
		projectID, name, desc, status, relDate))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrReleaseConflict
		}
		return nil, err
	}
	announceReleaseUpdated(ctx, pool, r.ID, projectID)
	return &r, nil
}

// ListReleases returns the project's releases with live issue counts,
// ordered by creation. Any role may read.
func ListReleases(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string) ([]Release, error) {
	projectID, _, err := resolveReleaseProject(ctx, pool, wsSlug, identifier, actorID, false)
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx,
		`SELECT `+releaseColumnsCounted+`
		 FROM releases WHERE project_id = $1::uuid
		 ORDER BY created_at`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Release{}
	for rows.Next() {
		r, err := scanReleaseCounted(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetRelease returns one release with its live issue count. Any role may
// read.
func GetRelease(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, releaseID string) (*Release, error) {
	projectID, _, err := resolveReleaseProject(ctx, pool, wsSlug, identifier, actorID, false)
	if err != nil {
		return nil, err
	}
	if !isUUIDFormat(releaseID) {
		return nil, ErrInvalidReleaseID
	}
	r, err := scanReleaseCounted(pool.QueryRow(ctx,
		`SELECT `+releaseColumnsCounted+`
		 FROM releases WHERE id = $1::uuid AND project_id = $2::uuid`,
		releaseID, projectID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrReleaseNotFound
		}
		return nil, err
	}
	return &r, nil
}

// UpdateRelease applies a partial release update. Member (15)+. Only
// non-nil patch fields are written; "" clears a nullable field. An empty
// patch is ErrNothingToUpdate.
func UpdateRelease(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, releaseID string, patch ReleasePatch) (*Release, error) {
	projectID, _, err := resolveReleaseProject(ctx, pool, wsSlug, identifier, actorID, true)
	if err != nil {
		return nil, err
	}
	if _, err := resolveRelease(ctx, pool, projectID, releaseID); err != nil {
		return nil, err
	}
	set := []string{}
	setArgs := []any{}
	addSet := func(fragment string, v any) {
		setArgs = append(setArgs, v)
		set = append(set, fragment+"$"+strconv.Itoa(len(setArgs)+2))
	}
	if patch.Name != nil {
		name := strings.TrimSpace(*patch.Name)
		if name == "" {
			return nil, ErrNameRequired
		}
		addSet("name = ", name)
	}
	if patch.Description != nil {
		addSet("description = ", strings.TrimSpace(*patch.Description))
	}
	if patch.Status != nil {
		if !releaseStatuses[*patch.Status] {
			return nil, ErrInvalidRelease
		}
		addSet("status = ", *patch.Status)
	}
	if patch.ReleaseDate != nil {
		relDate, err := parseReleaseDate(*patch.ReleaseDate)
		if err != nil {
			return nil, err
		}
		addSet("release_date = ", relDate)
		set[len(set)-1] += "::date"
	}
	if len(set) == 0 {
		return nil, ErrNothingToUpdate
	}
	set = append(set, "updated_at = now()")
	r, err := scanRelease(pool.QueryRow(ctx,
		`UPDATE releases SET `+strings.Join(set, ", ")+`
		  WHERE id = $1::uuid AND project_id = $2::uuid
		  RETURNING `+releaseColumns,
		append([]any{releaseID, projectID}, setArgs...)...))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrReleaseConflict
		}
		return nil, err
	}
	announceReleaseUpdated(ctx, pool, r.ID, projectID)
	return &r, nil
}

// DeleteRelease removes a release. Member (15)+. A release that still has
// issues is 409 (ErrReleaseHasIssues) unless reassign moves the issues to
// another release of the same project, or force detaches them (SET NULL).
func DeleteRelease(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, releaseID string, reassign *string, force bool) error {
	projectID, _, err := resolveReleaseProject(ctx, pool, wsSlug, identifier, actorID, true)
	if err != nil {
		return err
	}
	if _, err := resolveRelease(ctx, pool, projectID, releaseID); err != nil {
		return err
	}
	var n int64
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM issues WHERE release_id = $1::uuid AND deleted_at IS NULL`,
		releaseID).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		switch {
		case reassign != nil:
			target := strings.TrimSpace(*reassign)
			if target == "" || target == releaseID {
				return ErrInvalidRelease
			}
			if _, err := resolveRelease(ctx, pool, projectID, target); err != nil {
				return err
			}
			if _, err := pool.Exec(ctx,
				`UPDATE issues SET release_id = $1::uuid
				  WHERE release_id = $2::uuid AND deleted_at IS NULL`,
				target, releaseID); err != nil {
				return err
			}
		case force:
			if _, err := pool.Exec(ctx,
				`UPDATE issues SET release_id = NULL
				  WHERE release_id = $1::uuid AND deleted_at IS NULL`,
				releaseID); err != nil {
				return err
			}
		default:
			return ErrReleaseHasIssues
		}
	}
	_, err = pool.Exec(ctx,
		`DELETE FROM releases WHERE id = $1::uuid AND project_id = $2::uuid`,
		releaseID, projectID)
	if err != nil {
		return err
	}
	announceReleaseUpdated(ctx, pool, releaseID, projectID)
	return nil
}

// AssignReleaseIssues bulk-assigns issues to a release. Every issue must be
// a live (non-deleted) issue of the same project, otherwise
// ErrIssueNotFound. The assign is idempotent (plain UPDATE — repeats are
// no-ops). Member (15)+; an empty id list is ErrBulkEmptyIDs.
func AssignReleaseIssues(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, releaseID string, issueIDs []string) error {
	if len(issueIDs) == 0 {
		return ErrBulkEmptyIDs
	}
	projectID, _, err := resolveReleaseProject(ctx, pool, wsSlug, identifier, actorID, true)
	if err != nil {
		return err
	}
	if _, err := resolveRelease(ctx, pool, projectID, releaseID); err != nil {
		return err
	}
	// Validate ownership + liveness for every id first: a partial assign
	// that silently drops foreign issues would be worse than a 404.
	for _, id := range issueIDs {
		if !isUUIDFormat(id) {
			// Malformed ids are a client error — 400, not a 500 from
			// the ::uuid cast.
			return ErrInvalidReleaseID
		}
		var owner string
		err := pool.QueryRow(ctx,
			`SELECT project_id::text FROM issues
			 WHERE id = $1::uuid AND deleted_at IS NULL`, id).Scan(&owner)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrIssueNotFound
			}
			return err
		}
		if owner != projectID {
			return ErrIssueNotFound
		}
	}
	_, err = pool.Exec(ctx,
		`UPDATE issues SET release_id = $1::uuid
		  WHERE id = ANY($2::uuid[]) AND deleted_at IS NULL`,
		releaseID, issueIDs)
	if err != nil {
		return err
	}
	announceReleaseUpdated(ctx, pool, releaseID, projectID)
	return nil
}

// UnassignReleaseIssues bulk-removes issues from a release (SET NULL).
// Idempotent: removing a non-member is a no-op. Member (15)+; an empty id
// list is ErrBulkEmptyIDs. Malformed ids are 400 (the ::uuid[] cast would
// 500).
func UnassignReleaseIssues(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, releaseID string, issueIDs []string) error {
	if len(issueIDs) == 0 {
		return ErrBulkEmptyIDs
	}
	projectID, _, err := resolveReleaseProject(ctx, pool, wsSlug, identifier, actorID, true)
	if err != nil {
		return err
	}
	if _, err := resolveRelease(ctx, pool, projectID, releaseID); err != nil {
		return err
	}
	for _, id := range issueIDs {
		if !isUUIDFormat(id) {
			return ErrInvalidReleaseID
		}
	}
	_, err = pool.Exec(ctx,
		`UPDATE issues SET release_id = NULL
		  WHERE release_id = $1::uuid AND id = ANY($2::uuid[]) AND deleted_at IS NULL`,
		releaseID, issueIDs)
	if err != nil {
		return err
	}
	announceReleaseUpdated(ctx, pool, releaseID, projectID)
	return nil
}

// ListReleaseIssues returns the release's live issues as full list items
// (assignees + labels aggregated, one query). Any role may read.
func ListReleaseIssues(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, releaseID string) ([]IssueListItem, error) {
	projectID, _, err := resolveReleaseProject(ctx, pool, wsSlug, identifier, actorID, false)
	if err != nil {
		return nil, err
	}
	if _, err := resolveRelease(ctx, pool, projectID, releaseID); err != nil {
		return nil, err
	}
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx,
		`SELECT `+prefixColumns("i.", issueColumns)+`, `+assigneesAgg+`, `+labelsAgg+`
		 FROM issues i
		 WHERE i.release_id = $1::uuid AND i.deleted_at IS NULL
		 ORDER BY i.created_at`, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IssueListItem{}
	for rows.Next() {
		item, err := scanModuleIssueRow(rows, ident)
		if err != nil {
			return nil, err
		}
		out = append(out, *item)
	}
	return out, rows.Err()
}

// ReleaseNotesRelease is the slim release summary in a notes response:
// the release's identity plus the fields the Markdown renders. ReleaseDate
// is a YYYY-MM-DD string (nil when the release has no date), matching the
// date form in the Markdown's release-date line.
type ReleaseNotesRelease struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Status      string  `json:"status"`
	ReleaseDate *string `json:"release_date"`
	Description string  `json:"description"`
}

// ReleaseNotesCounts partitions the release's issues the same way the
// Markdown sections do. Unstarted counts the Backlog section: every issue
// whose state group is neither completed nor started (unstarted, triage,
// backlog, cancelled, or an unknown group). The key keeps the pinned
// contract name; the value keeps completed+started+unstarted == total.
type ReleaseNotesCounts struct {
	Completed int `json:"completed"`
	Started   int `json:"started"`
	Unstarted int `json:"unstarted"`
	Total     int `json:"total"`
}

// ReleaseNotes is the GET .../releases/{releaseID}/notes response: a
// point-in-time snapshot, never a stored artifact. Regenerate after
// issues change.
type ReleaseNotes struct {
	Release  ReleaseNotesRelease `json:"release"`
	Markdown string              `json:"markdown"`
	Counts   ReleaseNotesCounts  `json:"counts"`
}

// releaseNoteItem is one Markdown bullet: "- <DISPLAY_ID> <title>".
type releaseNoteItem struct {
	DisplayID string
	Title     string
}

// releaseNoteRow is one scanned issue row for notes grouping: the
// sequence id (for the derived display id), the title, and the state
// group ("" when the state row is missing).
type releaseNoteRow struct {
	SequenceID int
	Title      string
	Group      string
}

// bucketReleaseNoteRows partitions scanned rows into the three notes
// sections, preserving row order within each section: completed →
// Completed, started → In progress, everything else (unstarted, triage,
// backlog, cancelled, unknown, or missing group) → Backlog. Pure, so
// the unknown-group branch is unit-testable: the states CHECK
// constraint + the issues.state_id FK make an unknown group
// unreachable from the live database, but the default branch keeps
// notes generation total if that ever changes.
func bucketReleaseNoteRows(ident string, rows []releaseNoteRow) (completed, started, backlog []releaseNoteItem) {
	completed = []releaseNoteItem{}
	started = []releaseNoteItem{}
	backlog = []releaseNoteItem{}
	for _, r := range rows {
		item := releaseNoteItem{DisplayID: ident + "-" + strconv.Itoa(r.SequenceID), Title: r.Title}
		switch r.Group {
		case "completed":
			completed = append(completed, item)
		case "started":
			started = append(started, item)
		default:
			backlog = append(backlog, item)
		}
	}
	return completed, started, backlog
}

// buildReleaseNotesMarkdown renders the pinned Markdown shape:
//
//	# Release notes — <name>
//
//	Release date: <YYYY-MM-DD|—>
//
//	[<description>
//
//	]## Completed (n)
//	- <DISPLAY_ID> <title>
//	...
//
//	## In progress (n)
//	...
//
//	## Backlog (n)
//	...
//
// Empty sections are omitted; with zero issues the body is
// "_No issues in this release._". No timestamps: the output is stable
// and diffable. Pure function — no DB access, so the exact shape is
// unit-testable without a database.
func buildReleaseNotesMarkdown(name string, releaseDate *time.Time, description string, completed, started, backlog []releaseNoteItem) string {
	var b strings.Builder
	b.WriteString("# Release notes — " + name + "\n\n")
	if releaseDate != nil {
		b.WriteString("Release date: " + releaseDate.Format("2006-01-02") + "\n")
	} else {
		b.WriteString("Release date: —\n")
	}
	if desc := strings.TrimSpace(description); desc != "" {
		b.WriteString("\n" + desc + "\n")
	}
	if len(completed)+len(started)+len(backlog) == 0 {
		b.WriteString("\n_No issues in this release._\n")
		return b.String()
	}
	writeSection := func(title string, items []releaseNoteItem) {
		if len(items) == 0 {
			return
		}
		b.WriteString("\n## " + title + " (" + strconv.Itoa(len(items)) + ")\n")
		for _, it := range items {
			b.WriteString("- " + it.DisplayID + " " + it.Title + "\n")
		}
	}
	writeSection("Completed", completed)
	writeSection("In progress", started)
	writeSection("Backlog", backlog)
	return b.String()
}

// GetReleaseNotes builds point-in-time Markdown release notes for the
// release's live issues, grouped by state group: completed → Completed,
// started → In progress, everything else (incl. unstarted, triage,
// backlog, cancelled, unknown/missing group) → Backlog. Read-only: it
// issues SELECTs only, never writes. Read gate = ListReleaseIssues: any
// workspace member (guest 5+) may read; non-members get ErrNotFound;
// unknown / cross-project release ids get ErrReleaseNotFound.
func GetReleaseNotes(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, releaseID string) (*ReleaseNotes, error) {
	projectID, _, err := resolveReleaseProject(ctx, pool, wsSlug, identifier, actorID, false)
	if err != nil {
		return nil, err
	}
	rel, err := resolveRelease(ctx, pool, projectID, releaseID)
	if err != nil {
		return nil, err
	}
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return nil, err
	}
	// LEFT JOIN states: an issue whose state row is gone (or whose group
	// is an unknown value) still renders — in Backlog — rather than
	// dropping from the notes. The sequence_id tiebreaker keeps the
	// order deterministic when created_at ties.
	rows, err := pool.Query(ctx,
		`SELECT i.sequence_id, i.name, COALESCE(s."group", '')
		 FROM issues i
		 LEFT JOIN states s ON s.id = i.state_id
		 WHERE i.release_id = $1::uuid AND i.deleted_at IS NULL
		 ORDER BY i.created_at, i.sequence_id`, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	scanned := []releaseNoteRow{}
	for rows.Next() {
		var r releaseNoteRow
		if err := rows.Scan(&r.SequenceID, &r.Title, &r.Group); err != nil {
			return nil, err
		}
		scanned = append(scanned, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	completed, started, backlog := bucketReleaseNoteRows(ident, scanned)
	var dateStr *string
	if rel.ReleaseDate != nil {
		s := rel.ReleaseDate.Format("2006-01-02")
		dateStr = &s
	}
	total := len(completed) + len(started) + len(backlog)
	return &ReleaseNotes{
		Release: ReleaseNotesRelease{
			ID:          rel.ID,
			Name:        rel.Name,
			Status:      rel.Status,
			ReleaseDate: dateStr,
			Description: rel.Description,
		},
		Markdown: buildReleaseNotesMarkdown(rel.Name, rel.ReleaseDate, rel.Description, completed, started, backlog),
		Counts: ReleaseNotesCounts{
			Completed: len(completed),
			Started:   len(started),
			Unstarted: len(backlog),
			Total:     total,
		},
	}, nil
}
