package service

// Workspace archive importer (C11T0).
//
// ImportWorkspaceArchive restores a `glance-export/1` archive produced by
// StreamWorkspaceExport (C10T3) into a workspace. Admin only: non-members
// get ErrNotFound (the actor-resolution contract — never a hint the slug
// exists), members/guests get ErrForbidden. Both are returned BEFORE the
// archive body is read.
//
// Design — v1 "restore-by-UUID" with deterministic clone remap:
//   - Sections are processed in dependency order (members, projects,
//     states, labels, estimates, custom_fields, cycles, modules, pages,
//     project_updates, issues, page_issue_links, objectives). The archive streams via json.Decoder: memory stays O(one
//     row) — the only in-memory growth is the small UUID bookkeeping
//     sets, the archived→final UUID remap, plus deferred cross-section
//     links (pairs of UUID strings, the same granularity the exporter
//     uses when it buffers issue IDs).
//   - UUIDs are the stable identity: a row whose UUID is free is
//     inserted with its archived UUID verbatim, so foreign keys re-link
//     directly — no uuid→id mapping is needed because the id IS the uuid
//     (the pin the exporter documents: "UUIDs are exported verbatim ...
//     stable identity a future archive importer can key re-linking on").
//   - A row whose UUID already exists in the target scope is SKIPPED,
//     which makes re-import idempotent — no duplicates, ever. Nested
//     rows (comments, custom values, ...) get their own UUID check, so
//     a retry after a partial failure still completes the missing rows.
//   - DEVIATION FROM THE BRIEF, forced by the schema: primary keys are
//     global, so "preserve UUIDs verbatim" is impossible when the source
//     workspace still exists in the same instance (e.g. cloning an
//     archive into another workspace of the same instance — the golden
//     round-trip test does exactly this). When the archived UUID belongs
//     to another scope, the row is inserted with a DETERMINISTIC clone
//     UUID — UUIDv5(namespace = target workspace id, name = archived
//     UUID) — and every reference to the archived UUID is re-linked to
//     the clone. Determinism is what keeps re-import idempotent: the
//     second import computes the same clone UUID, finds it in scope, and
//     skips. (Pure restores — different instance, or the source rows are
//     gone — keep every UUID verbatim.)
//   - Self-references (label parents, page parents, issue parents,
//     comment parents) and forward references (cycle/module issue
//     memberships — the archive lists cycles before issues) are buffered
//     as UUID pairs and applied after their section, so row order inside
//     a section never matters.
//   - Members are matched to instance users BY EMAIL (the archive
//     carries email per member). Users are never auto-created: an
//     archived member with no instance user is skipped with an honest
//     report entry, and every link to them degrades gracefully —
//     assignee links are dropped (unassigned), comment actors and
//     project-update authors cause their row to be skipped (actor is
//     NOT NULL for comments; an update imported under another author
//     would be a fabricated attribution), issue created_by falls
//     back to the importing admin (NOT NULL, and dropping whole issues
//     over an unmapped author would silently lose data), page authors
//     and module leads become NULL (both nullable).
//   - Objectives (C35T2) restore with nested key results. The owner
//     is resolved BY EMAIL: an unmapped owner degrades to a null
//     owner with an honest note — never a fabricated owner — and the
//     objective still imports. Metric KRs restore fully
//     (title/kind/unit/start/target/current/status-adjacent dates),
//     plus their check-in history (C42T2): each check-in restores
//     with its previous/new values, note, author (resolved by email,
//     null when unmapped — never fabricated) and created_at, via the
//     same insert-with-fallback + dedup discipline as every other
//     row. Restoring history never changes the KR's current_value
//     (history is a log, not state). Issue KRs carry no check-ins.
//     Issue-KR links resolve by project identifier + issue display_id
//     (the stable cross-archive key, immune to the UUID clone remap);
//     an unresolvable link skips with a note naming the KR and the KR
//     imports with no links — never dropped silently, never a
//     fabricated link. A KR whose target equals its start violates
//     the zero-span CHECK and is skipped with a note naming the KR.
//     A title conflict with a DIFFERENT objective skips the archived
//     objective and its KRs with honest notes — the importer never
//     merges KRs into an unrelated objective.
//     only (binaries were never exported). Each one is counted in the
//     report — never silently dropped.
//   - Page-issue links (C36T2) restore after the issues section:
//     the page resolves by id-then-title (the archived page UUID
//     through the remap first, else a title lookup within the link's
//     project) and the issue by project identifier + display_id (the
//     stable cross-archive key, immune to the UUID clone remap). An
//     unresolvable side skips the link with a note naming the link —
//     never dropped silently, never a fabricated page/issue. A link
//     whose page and issue land in different projects skips: a
//     hand-crafted archive must not smuggle in a cross-project link
//     the service could never have created. The creator resolves like
//     a page author (unmapped → null, with a note). Re-import is
//     idempotent via ON CONFLICT (page_id, issue_id) DO NOTHING —
//     the duplicate is a skip, not an error.
//   - NOT imported (never exported either): Slack webhook URL (secret),
//     activity history, reactions, votes, issue links (issue↔issue), intake items,
//     webhooks config, notification prefs, API tokens, page revisions,
//     sessions/OTP, and the workspace audit log (C37T2). History is NOT
//     portable: a hand-crafted audit section in an archive is ignored
//     (the unknown-key default), never imported — importing it would
//     fabricate witnessed events that never happened in the target
//     workspace, which keeps its own audit log.
//   - Unique-name conflicts (a DIFFERENT row already owns the archived
//     name in the target scope, e.g. a label "bug" with another UUID)
//     skip the archived row with an honest reason — the importer never
//     renames or merges silently.
//   - The whole import runs in ONE transaction: any failure rolls
//     everything back, so a broken archive can never leave a
//     half-restored workspace. The version pin is validated from the
//     stream before commit; a bad version fails 400 with zero writes.
//
// The archive's "workspace" section is informational: the import targets
// the workspace named by the route slug, which keeps its own id/slug.
// The archived workspace slug is echoed in the report.

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrArchiveVersion is returned when the archive's format field is
	// missing or names a version this importer does not understand. The
	// schema_note contract requires refusing unknown versions — never
	// silently mis-importing.
	ErrArchiveVersion = errors.New("service: unsupported archive format version")
	// ErrArchiveMalformed is returned when the upload is not a valid
	// archive JSON document.
	ErrArchiveMalformed = errors.New("service: malformed archive JSON")
)

// archiveUUIDRe validates UUID-shaped strings before they are cast to
// ::uuid in SQL: a hand-crafted archive with garbage IDs must produce
// skipped rows with reasons, not a 500 from a failed cast.
var archiveUUIDRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func validArchiveUUID(s string) bool { return archiveUUIDRe.MatchString(s) }

// isPKConflict reports whether err is a primary-key unique violation on
// table's PK. Only the PK is unchecked at insert time (unique-name
// conflicts are pre-checked with honest skip reasons), so a 23505 here
// means the archived UUID belongs to another scope.
func isPKConflict(err error, table string) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return pgErr.ConstraintName == table+"_pkey"
	}
	return false
}

// parseUUID parses a canonical UUID string into 16 bytes (stdlib only —
// no new dependencies for the clone-remap mechanism).
func parseUUID(s string) ([16]byte, bool) {
	var b [16]byte
	hex := strings.ReplaceAll(s, "-", "")
	if len(hex) != 32 {
		return b, false
	}
	for i := 0; i < 16; i++ {
		v, err := strconv.ParseUint(hex[2*i:2*i+2], 16, 8)
		if err != nil {
			return b, false
		}
		b[i] = byte(v)
	}
	return b, true
}

// uuidV5 returns the deterministic RFC 4122 version-5 UUID for
// (namespace, name): SHA-1(namespace || name) with the version and
// variant bits set. Used for the clone remap: the same archive imported
// into the same workspace always yields the same clone UUIDs, which is
// what makes re-import converge instead of duplicating.
func uuidV5(namespace, name string) string {
	ns, _ := parseUUID(namespace) // zero namespace on garbage: still deterministic
	h := sha1.New()
	h.Write(ns[:])
	h.Write([]byte(name))
	sum := h.Sum(nil)
	sum[6] = (sum[6] & 0x0f) | 0x50 // version 5
	sum[8] = (sum[8] & 0x3f) | 0x80 // variant RFC4122
	const hexd = "0123456789abcdef"
	var sb strings.Builder
	sb.Grow(36)
	for i, c := range sum[:16] {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			sb.WriteByte('-')
		}
		sb.WriteByte(hexd[c>>4])
		sb.WriteByte(hexd[c&0x0f])
	}
	return sb.String()
}

// ArchiveImportReport is the honest outcome of an archive import.
type ArchiveImportReport struct {
	Format          string               `json:"format"`
	SourceWorkspace string               `json:"source_workspace"`
	TargetWorkspace string               `json:"target_workspace"`
	Imported        map[string]int       `json:"imported"`
	Skipped         ArchiveImportSkipped `json:"skipped"`
}

// ArchiveImportSkipped counts skipped rows and carries a bounded,
// human-readable sample of the reasons (the count is exact; the list is
// capped so a pathological archive cannot bloat the response).
type ArchiveImportSkipped struct {
	Count   int      `json:"count"`
	Reasons []string `json:"reasons"`
}

// maxArchiveSkipReasons caps the reasons list in the report.
const maxArchiveSkipReasons = 64

// archiveImportSections enumerates the report's imported counters in a
// stable order; every key is always present, even at zero.
var archiveImportSections = []string{
	"members", "projects", "states", "labels", "estimates",
	"estimate_points", "custom_fields", "cycles", "cycle_issues",
	"modules", "module_issues", "pages", "project_updates", "issues", "issue_assignees",
	"issue_labels", "issue_custom_values", "comments", "attachments",
	"page_issue_links",
	"objectives", "key_results", "key_result_issue_links", "key_result_checkins",
}

// archiveLink is a deferred UUID→UUID reference applied after its
// section finishes streaming (self-references and forward references).
// Both ends are ARCHIVED UUIDs; they are resolved through the remap at
// apply time.
type archiveLink struct{ parent, child string }

// archiveImporter carries the import's working state.
type archiveImporter struct {
	ctx     context.Context
	tx      pgx.Tx
	wsID    string
	slug    string
	actorID string
	rep     ArchiveImportReport

	userMap map[string]string // archived user UUID -> instance user UUID
	remap   map[string]string // archived row UUID -> final target-scope UUID (clone remap)
	// known UUID sets in the target scope (preloaded + imported).
	projects, labels, states, estimates, estimatePoints,
	customFields, cycles, modules, pages, projectUpdates, issues, comments,
	objectives, keyResults, checkins map[string]bool
	maxSeq map[string]int // resolved project UUID -> max restored issue sequence_id

	cycleLinks, moduleLinks      []archiveLink
	pageParents                  []archiveLink
	issueParents, commentParents []archiveLink
	labelParents                 []archiveLink
}

func newArchiveImporter(ctx context.Context, tx pgx.Tx, wsID, slug, actorID string) *archiveImporter {
	rep := ArchiveImportReport{
		Format:          ExportFormatVersion,
		TargetWorkspace: slug,
		Imported:        map[string]int{},
		Skipped:         ArchiveImportSkipped{Reasons: []string{}},
	}
	for _, s := range archiveImportSections {
		rep.Imported[s] = 0
	}
	sets := func() map[string]bool { return map[string]bool{} }
	return &archiveImporter{
		ctx: ctx, tx: tx, wsID: wsID, slug: slug, actorID: actorID, rep: rep,
		userMap:  map[string]string{},
		remap:    map[string]string{},
		projects: sets(), labels: sets(), states: sets(), estimates: sets(),
		estimatePoints: sets(), customFields: sets(), cycles: sets(),
		modules: sets(), pages: sets(), projectUpdates: sets(), issues: sets(), comments: sets(),
		objectives: sets(), keyResults: sets(), checkins: sets(),
		maxSeq: map[string]int{},
	}
}

// skip records one skipped row. The count is exact; the reasons list is
// a bounded sample.
func (imp *archiveImporter) skip(section, id, reason string) {
	imp.rep.Skipped.Count++
	if len(imp.rep.Skipped.Reasons) < maxArchiveSkipReasons {
		imp.rep.Skipped.Reasons = append(imp.rep.Skipped.Reasons,
			fmt.Sprintf("[%s] %s: %s", section, id, reason))
	}
}

// resolveID maps an archived row UUID to its final target-scope UUID:
// the deterministic clone when the row was remapped, else the archived
// UUID itself.
func (imp *archiveImporter) resolveID(archivedID string) string {
	if newID, ok := imp.remap[archivedID]; ok {
		return newID
	}
	return archivedID
}

// resolveUser maps an archived user UUID to an instance user UUID: first
// via the members section's email matching, then (same-instance restores
// with a sparse members section) by direct UUID lookup. ok=false means
// the user is unmapped — the caller degrades gracefully and reports it.
func (imp *archiveImporter) resolveUser(archivedID string) (id string, ok bool, err error) {
	if id, ok := imp.userMap[archivedID]; ok {
		return id, true, nil
	}
	if !validArchiveUUID(archivedID) {
		return "", false, nil
	}
	err = imp.tx.QueryRow(imp.ctx,
		`SELECT id::text FROM users WHERE id = $1::uuid`, archivedID).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	imp.userMap[archivedID] = id
	return id, true, nil
}

// alreadyImported reports whether the archived row is already present in
// the target scope — by its archived UUID, or by the deterministic clone
// UUID a previous import used — and records the remap so references
// re-link to the existing row.
func (imp *archiveImporter) alreadyImported(set map[string]bool, archivedID string) bool {
	if set[archivedID] {
		return true
	}
	if clone := uuidV5(imp.wsID, archivedID); set[clone] {
		imp.remap[archivedID] = clone
		return true
	}
	return false
}

// insertWithFallback inserts a new row via insert(id). The archived UUID
// is tried first (true restore: UUIDs preserved verbatim); a PK
// conflict means the UUID belongs to another scope, so it retries once
// with the deterministic clone UUID and records the remap. Returns the
// final UUID and whether a row was actually inserted (false when the
// clone was already present — a lost race with a concurrent import).
//
// The speculative first insert runs inside a SAVEPOINT: a failed
// statement would otherwise poison the whole transaction (25P02) and
// abort the import. Savepoints are released immediately so they cannot
// accumulate over a large archive.
func (imp *archiveImporter) insertWithFallback(set map[string]bool, table, archivedID string, insert func(id string) error) (finalID string, inserted bool, err error) {
	try := func(id string) (conflict bool, err error) {
		if _, err := imp.tx.Exec(imp.ctx, `SAVEPOINT glance_archive_row`); err != nil {
			return false, err
		}
		if err := insert(id); err != nil {
			if _, rbErr := imp.tx.Exec(imp.ctx, `ROLLBACK TO SAVEPOINT glance_archive_row`); rbErr != nil {
				return false, rbErr
			}
			if isPKConflict(err, table) {
				return true, nil // handled: the caller retries with the clone UUID
			}
			return false, err
		}
		if _, err := imp.tx.Exec(imp.ctx, `RELEASE SAVEPOINT glance_archive_row`); err != nil {
			return false, err
		}
		return false, nil
	}
	if conflict, err := try(archivedID); err != nil {
		return "", false, err
	} else if !conflict {
		set[archivedID] = true
		return archivedID, true, nil
	}
	clone := uuidV5(imp.wsID, archivedID)
	if set[clone] {
		imp.remap[archivedID] = clone
		return clone, false, nil
	}
	if conflict, err := try(clone); err != nil {
		return "", false, err
	} else if conflict {
		// Even the deterministic clone collided: something is deeply
		// wrong (or a concurrent import won the race). Fail loudly —
		// the outer transaction rolls the whole import back.
		return "", false, fmt.Errorf("service: archive import UUID collision on deterministic clone %s", clone)
	}
	set[clone] = true
	imp.remap[archivedID] = clone
	return clone, true, nil
}

// preload fills the known-UUID sets from the target workspace so the
// UUID-exists check works for both pre-existing and just-imported rows.
func (imp *archiveImporter) preload() error {
	queries := map[*map[string]bool]string{
		&imp.projects:       `SELECT id::text FROM projects WHERE workspace_id = $1::uuid`,
		&imp.labels:         `SELECT id::text FROM labels WHERE workspace_id = $1::uuid`,
		&imp.states:         `SELECT s.id::text FROM states s JOIN projects p ON p.id = s.project_id WHERE p.workspace_id = $1::uuid`,
		&imp.estimates:      `SELECT e.id::text FROM estimates e JOIN projects p ON p.id = e.project_id WHERE p.workspace_id = $1::uuid`,
		&imp.estimatePoints: `SELECT ep.id::text FROM estimate_points ep JOIN estimates e ON e.id = ep.estimate_id JOIN projects p ON p.id = e.project_id WHERE p.workspace_id = $1::uuid`,
		&imp.customFields:   `SELECT cf.id::text FROM custom_fields cf JOIN projects p ON p.id = cf.project_id WHERE p.workspace_id = $1::uuid`,
		&imp.cycles:         `SELECT c.id::text FROM cycles c JOIN projects p ON p.id = c.project_id WHERE p.workspace_id = $1::uuid`,
		&imp.modules:        `SELECT m.id::text FROM modules m JOIN projects p ON p.id = m.project_id WHERE p.workspace_id = $1::uuid`,
		&imp.pages:          `SELECT pg.id::text FROM pages pg JOIN projects p ON p.id = pg.project_id WHERE p.workspace_id = $1::uuid`,
		&imp.projectUpdates: `SELECT pu.id::text FROM project_updates pu JOIN projects p ON p.id = pu.project_id WHERE p.workspace_id = $1::uuid`,
		&imp.issues:         `SELECT i.id::text FROM issues i JOIN projects p ON p.id = i.project_id WHERE p.workspace_id = $1::uuid`,
		&imp.comments:       `SELECT c.id::text FROM comments c JOIN issues i ON i.id = c.issue_id JOIN projects p ON p.id = i.project_id WHERE p.workspace_id = $1::uuid`,
		&imp.objectives:     `SELECT id::text FROM objectives WHERE workspace_id = $1::uuid`,
		&imp.keyResults:     `SELECT kr.id::text FROM key_results kr JOIN objectives o ON o.id = kr.objective_id WHERE o.workspace_id = $1::uuid`,
		&imp.checkins:       `SELECT ci.id::text FROM key_result_checkins ci JOIN key_results kr ON kr.id = ci.key_result_id JOIN objectives o ON o.id = kr.objective_id WHERE o.workspace_id = $1::uuid`,
	}
	for set, q := range queries {
		rows, err := imp.tx.Query(imp.ctx, q, imp.wsID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			(*set)[id] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}
	return nil
}

// ImportWorkspaceArchive restores a glance-export archive into the
// workspace named by slug. See the package doc for the v1 semantics.
func ImportWorkspaceArchive(ctx context.Context, pool *pgxpool.Pool, slug, actorID string, r io.Reader) (ArchiveImportReport, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return ArchiveImportReport{}, err
	}
	defer tx.Rollback(ctx)

	// Authorize inside the transaction before reading the body: the
	// actor-resolution contract (non-member → ErrNotFound, member/guest
	// → ErrForbidden) mirrors StreamWorkspaceExport.
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
			return ArchiveImportReport{}, ErrNotFound
		}
		return ArchiveImportReport{}, err
	}
	if role != RoleAdmin {
		return ArchiveImportReport{}, ErrForbidden
	}

	imp := newArchiveImporter(ctx, tx, wsID, slug, actorID)
	if err := imp.preload(); err != nil {
		return ArchiveImportReport{}, err
	}
	if err := imp.decode(r); err != nil {
		return ArchiveImportReport{}, err
	}
	if err := imp.applyDeferred(); err != nil {
		return ArchiveImportReport{}, err
	}
	if err := imp.advanceSequences(); err != nil {
		return ArchiveImportReport{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ArchiveImportReport{}, err
	}
	return imp.rep, nil
}

// malformed wraps a JSON decoding failure as a 400-class error.
func malformed(err error) error {
	return fmt.Errorf("%w: %v", ErrArchiveMalformed, err)
}

// decode streams the archive document section by section. The format pin
// is validated the moment the "format" key is seen; a failure aborts
// before commit, so zero rows are ever written for a bad version.
func (imp *archiveImporter) decode(r io.Reader) error {
	dec := json.NewDecoder(r)
	dec.UseNumber() // custom number values must survive as json.Number

	tok, err := dec.Token()
	if err != nil {
		return malformed(err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return malformed(errors.New("archive must be a JSON object"))
	}
	formatSeen := false
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return malformed(err)
		}
		key, ok := kt.(string)
		if !ok {
			return malformed(errors.New("archive object keys must be strings"))
		}
		switch key {
		case "format":
			var f string
			if err := dec.Decode(&f); err != nil {
				return malformed(err)
			}
			if f != ExportFormatVersion {
				return fmt.Errorf("%w: archive format %q is not supported (this importer reads %q)",
					ErrArchiveVersion, f, ExportFormatVersion)
			}
			formatSeen = true
		case "workspace":
			var ws exportWorkspace
			if err := dec.Decode(&ws); err != nil {
				return malformed(err)
			}
			// Informational only: the target workspace keeps its own
			// identity. Recorded so the report says where the data
			// came from.
			imp.rep.SourceWorkspace = ws.Slug
			if imp.rep.SourceWorkspace == "" {
				imp.rep.SourceWorkspace = ws.Name
			}
		case "members":
			if err := decodeArchiveSection(dec, imp.importMember); err != nil {
				return err
			}
		case "projects":
			if err := decodeArchiveSection(dec, imp.importProject); err != nil {
				return err
			}
		case "states":
			if err := decodeArchiveSection(dec, imp.importState); err != nil {
				return err
			}
		case "labels":
			if err := decodeArchiveSection(dec, imp.importLabel); err != nil {
				return err
			}
		case "estimates":
			if err := decodeArchiveSection(dec, imp.importEstimate); err != nil {
				return err
			}
		case "custom_fields":
			if err := decodeArchiveSection(dec, imp.importCustomField); err != nil {
				return err
			}
		case "cycles":
			if err := decodeArchiveSection(dec, imp.importCycle); err != nil {
				return err
			}
		case "modules":
			if err := decodeArchiveSection(dec, imp.importModule); err != nil {
				return err
			}
		case "pages":
			if err := decodeArchiveSection(dec, imp.importPage); err != nil {
				return err
			}
		case "project_updates":
			if err := decodeArchiveSection(dec, imp.importProjectUpdate); err != nil {
				return err
			}
		case "issues":
			if err := decodeArchiveSection(dec, imp.importIssue); err != nil {
				return err
			}
		case "page_issue_links":
			if err := decodeArchiveSection(dec, imp.importPageIssueLink); err != nil {
				return err
			}
		case "objectives":
			if err := decodeArchiveSection(dec, imp.importObjective); err != nil {
				return err
			}
		default:
			// exported_at, schema_note and any future keys: ignored.
			var discard json.RawMessage
			if err := dec.Decode(&discard); err != nil {
				return malformed(err)
			}
		}
	}
	if _, err := dec.Token(); err != nil { // closing '}'
		return malformed(err)
	}
	if !formatSeen {
		return fmt.Errorf("%w: archive is missing the \"format\" field", ErrArchiveVersion)
	}
	return nil
}

// decodeArchiveSection consumes one JSON array section, one row at a
// time, calling fn per row. Memory stays O(one row).
func decodeArchiveSection[T any](dec *json.Decoder, fn func(*T) error) error {
	tok, err := dec.Token()
	if err != nil {
		return malformed(err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '[' {
		return malformed(errors.New("archive section must be a JSON array"))
	}
	for dec.More() {
		var v T
		if err := dec.Decode(&v); err != nil {
			return malformed(err)
		}
		if err := fn(&v); err != nil {
			return err
		}
	}
	if _, err := dec.Token(); err != nil { // closing ']'
		return malformed(err)
	}
	return nil
}

// nameConflict checks a (scope, name) unique constraint for a row about
// to be inserted: a DIFFERENT row already owning the name is an honest
// skip, never a silent rename or merge.
func (imp *archiveImporter) nameConflict(query, scopeID, name, archivedID string) (bool, error) {
	var otherID string
	err := imp.tx.QueryRow(imp.ctx, query, scopeID, name).Scan(&otherID)
	if err == nil {
		return otherID != archivedID && otherID != imp.resolveID(archivedID), nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return false, err
}

// --- section importers ------------------------------------------------

// importMember matches an archived member to an instance user by email
// and adds them to the target workspace. Users are never auto-created.
func (imp *archiveImporter) importMember(m *exportMember) error {
	if !validArchiveUUID(m.UserID) {
		imp.skip("members", m.Email, "member skipped: malformed archived user id")
		return nil
	}
	var instanceID string
	err := imp.tx.QueryRow(imp.ctx,
		`SELECT id::text FROM users WHERE email = $1`, m.Email).Scan(&instanceID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			imp.skip("members", m.Email,
				"member skipped: no user with this email exists in this instance (members are matched by email, never auto-created)")
			return nil
		}
		return err
	}
	imp.userMap[m.UserID] = instanceID
	if m.Role != 5 && m.Role != 15 && m.Role != 20 {
		imp.skip("members", m.Email,
			fmt.Sprintf("member skipped: archived role %d is not valid (5, 15 or 20)", m.Role))
		return nil
	}
	tag, err := imp.tx.Exec(imp.ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role)
		 VALUES ($1::uuid, $2::uuid, $3)
		 ON CONFLICT (workspace_id, user_id) DO NOTHING`,
		imp.wsID, instanceID, m.Role)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		imp.rep.Imported["members"]++
	} else {
		imp.skip("members", m.Email,
			"member skipped: already a member of this workspace (existing role kept)")
	}
	return nil
}

func (imp *archiveImporter) importProject(p *exportProject) error {
	if !validArchiveUUID(p.ID) {
		imp.skip("projects", p.Identifier, "project skipped: malformed archived id")
		return nil
	}
	if imp.alreadyImported(imp.projects, p.ID) {
		imp.skip("projects", p.Identifier,
			fmt.Sprintf("project %q skipped: already exists in this workspace", p.Name))
		return nil
	}
	conflict, err := imp.nameConflict(
		`SELECT id::text FROM projects WHERE workspace_id = $1::uuid AND identifier = $2`,
		imp.wsID, p.Identifier, p.ID)
	if err != nil {
		return err
	}
	if conflict {
		imp.skip("projects", p.Identifier,
			fmt.Sprintf("project %q skipped: identifier %q is already used by another project in this workspace", p.Name, p.Identifier))
		return nil
	}
	finalID, inserted, err := imp.insertWithFallback(imp.projects, "projects", p.ID,
		func(id string) error {
			_, err := imp.tx.Exec(imp.ctx,
				`INSERT INTO projects (id, workspace_id, identifier, name, description,
				                       archive_in_days, close_in_days, created_at, updated_at)
				 VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9)`,
				id, imp.wsID, p.Identifier, p.Name, p.Description,
				p.ArchiveInDays, p.CloseInDays, p.CreatedAt, p.UpdatedAt)
			return err
		})
	if err != nil {
		return err
	}
	if !inserted {
		imp.skip("projects", p.Identifier,
			fmt.Sprintf("project %q skipped: already exists in this workspace", p.Name))
		return nil
	}
	// Every project needs its sequence counter row (normally seeded at
	// creation); ON CONFLICT keeps it idempotent.
	if _, err := imp.tx.Exec(imp.ctx,
		`INSERT INTO issue_sequences (project_id, last_value)
		 VALUES ($1::uuid, 0) ON CONFLICT (project_id) DO NOTHING`, finalID); err != nil {
		return err
	}
	imp.rep.Imported["projects"]++
	return nil
}

func (imp *archiveImporter) importState(s *exportState) error {
	if !validArchiveUUID(s.ID) || !validArchiveUUID(s.ProjectID) {
		imp.skip("states", s.Name, "state skipped: malformed archived id")
		return nil
	}
	if imp.alreadyImported(imp.states, s.ID) {
		imp.skip("states", s.Name, "state skipped: already exists in this workspace")
		return nil
	}
	projectID := imp.resolveID(s.ProjectID)
	if !imp.projects[projectID] {
		imp.skip("states", s.Name, "state skipped: its project was not imported into this workspace")
		return nil
	}
	conflict, err := imp.nameConflict(
		`SELECT id::text FROM states WHERE project_id = $1::uuid AND name = $2`,
		projectID, s.Name, s.ID)
	if err != nil {
		return err
	}
	if conflict {
		imp.skip("states", s.Name,
			fmt.Sprintf("state %q skipped: another state already has this name in the project", s.Name))
		return nil
	}
	_, inserted, err := imp.insertWithFallback(imp.states, "states", s.ID,
		func(id string) error {
			_, err := imp.tx.Exec(imp.ctx,
				`INSERT INTO states (id, project_id, name, "group", color, sequence)
				 VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6)`,
				id, projectID, s.Name, s.Group, s.Color, s.Sequence)
			return err
		})
	if err != nil {
		return err
	}
	if !inserted {
		imp.skip("states", s.Name, "state skipped: already exists in this workspace")
		return nil
	}
	imp.rep.Imported["states"]++
	return nil
}

func (imp *archiveImporter) importLabel(l *exportLabel) error {
	if !validArchiveUUID(l.ID) || (l.ParentID != nil && !validArchiveUUID(*l.ParentID)) {
		imp.skip("labels", l.Name, "label skipped: malformed archived id")
		return nil
	}
	if imp.alreadyImported(imp.labels, l.ID) {
		imp.skip("labels", l.Name, "label skipped: already exists in this workspace")
		return nil
	}
	conflict, err := imp.nameConflict(
		`SELECT id::text FROM labels WHERE workspace_id = $1::uuid AND name = $2`,
		imp.wsID, l.Name, l.ID)
	if err != nil {
		return err
	}
	if conflict {
		imp.skip("labels", l.Name,
			fmt.Sprintf("label %q skipped: another label already has this name in the workspace", l.Name))
		return nil
	}
	finalID, inserted, err := imp.insertWithFallback(imp.labels, "labels", l.ID,
		func(id string) error {
			// parent_id is applied after the section (parents may
			// stream later).
			_, err := imp.tx.Exec(imp.ctx,
				`INSERT INTO labels (id, workspace_id, name, color) VALUES ($1::uuid, $2::uuid, $3, $4)`,
				id, imp.wsID, l.Name, l.Color)
			return err
		})
	if err != nil {
		return err
	}
	if !inserted {
		imp.skip("labels", l.Name, "label skipped: already exists in this workspace")
		return nil
	}
	imp.rep.Imported["labels"]++
	if l.ParentID != nil {
		imp.labelParents = append(imp.labelParents, archiveLink{parent: *l.ParentID, child: finalID})
	}
	return nil
}

func (imp *archiveImporter) importEstimate(e *exportEstimate) error {
	if !validArchiveUUID(e.ID) || !validArchiveUUID(e.ProjectID) {
		imp.skip("estimates", e.Name, "estimate skipped: malformed archived id")
		return nil
	}
	estimateKnown := imp.alreadyImported(imp.estimates, e.ID)
	if !estimateKnown {
		projectID := imp.resolveID(e.ProjectID)
		if !imp.projects[projectID] {
			imp.skip("estimates", e.Name, "estimate skipped: its project was not imported into this workspace")
			return nil
		}
		conflict, err := imp.nameConflict(
			`SELECT id::text FROM estimates WHERE project_id = $1::uuid AND name = $2`,
			projectID, e.Name, e.ID)
		if err != nil {
			return err
		}
		if conflict {
			imp.skip("estimates", e.Name,
				fmt.Sprintf("estimate %q skipped: another estimate already has this name in the project", e.Name))
			return nil
		}
		_, inserted, err := imp.insertWithFallback(imp.estimates, "estimates", e.ID,
			func(id string) error {
				_, err := imp.tx.Exec(imp.ctx,
					`INSERT INTO estimates (id, project_id, name) VALUES ($1::uuid, $2::uuid, $3)`,
					id, projectID, e.Name)
				return err
			})
		if err != nil {
			return err
		}
		if !inserted {
			imp.skip("estimates", e.Name, "estimate skipped: already exists in this workspace")
		} else {
			imp.rep.Imported["estimates"]++
		}
	} else {
		imp.skip("estimates", e.Name, "estimate skipped: already exists in this workspace")
	}
	// Points carry their own UUIDs: processed even when the estimate row
	// already existed, so partial-failure retries complete them.
	estimateID := imp.resolveID(e.ID)
	for i := range e.Points {
		pt := &e.Points[i]
		if !validArchiveUUID(pt.ID) {
			imp.skip("estimate_points", pt.Key, "estimate point skipped: malformed archived id")
			continue
		}
		if imp.alreadyImported(imp.estimatePoints, pt.ID) {
			imp.skip("estimate_points", pt.Key, "estimate point skipped: already exists in this workspace")
			continue
		}
		conflict, err := imp.nameConflict(
			`SELECT id::text FROM estimate_points WHERE estimate_id = $1::uuid AND key = $2`,
			estimateID, pt.Key, pt.ID)
		if err != nil {
			return err
		}
		if conflict {
			imp.skip("estimate_points", pt.Key,
				fmt.Sprintf("estimate point %q skipped: another point already has this key", pt.Key))
			continue
		}
		_, inserted, err := imp.insertWithFallback(imp.estimatePoints, "estimate_points", pt.ID,
			func(id string) error {
				_, err := imp.tx.Exec(imp.ctx,
					`INSERT INTO estimate_points (id, estimate_id, key, value, description)
					 VALUES ($1::uuid, $2::uuid, $3, $4, $5)`,
					id, estimateID, pt.Key, pt.Value, pt.Description)
				return err
			})
		if err != nil {
			return err
		}
		if !inserted {
			imp.skip("estimate_points", pt.Key, "estimate point skipped: already exists in this workspace")
			continue
		}
		imp.rep.Imported["estimate_points"]++
	}
	return nil
}

var validCustomFieldTypes = map[string]bool{
	"text": true, "number": true, "date": true, "select": true, "checkbox": true,
}

func (imp *archiveImporter) importCustomField(f *exportCustomField) error {
	if !validArchiveUUID(f.ID) || !validArchiveUUID(f.ProjectID) {
		imp.skip("custom_fields", f.Name, "custom field skipped: malformed archived id")
		return nil
	}
	if imp.alreadyImported(imp.customFields, f.ID) {
		imp.skip("custom_fields", f.Name, "custom field skipped: already exists in this workspace")
		return nil
	}
	projectID := imp.resolveID(f.ProjectID)
	if !imp.projects[projectID] {
		imp.skip("custom_fields", f.Name, "custom field skipped: its project was not imported into this workspace")
		return nil
	}
	if !validCustomFieldTypes[f.FieldType] {
		imp.skip("custom_fields", f.Name,
			fmt.Sprintf("custom field skipped: unknown field_type %q", f.FieldType))
		return nil
	}
	conflict, err := imp.nameConflict(
		`SELECT id::text FROM custom_fields WHERE project_id = $1::uuid AND name = $2`,
		projectID, f.Name, f.ID)
	if err != nil {
		return err
	}
	if conflict {
		imp.skip("custom_fields", f.Name,
			fmt.Sprintf("custom field %q skipped: another field already has this name in the project", f.Name))
		return nil
	}
	options := "[]"
	if len(f.Options) > 0 && string(f.Options) != "null" {
		options = string(f.Options)
	}
	_, inserted, err := imp.insertWithFallback(imp.customFields, "custom_fields", f.ID,
		func(id string) error {
			_, err := imp.tx.Exec(imp.ctx,
				`INSERT INTO custom_fields (id, project_id, name, field_type, options, required, position)
				 VALUES ($1::uuid, $2::uuid, $3, $4, $5::jsonb, $6, $7)`,
				id, projectID, f.Name, f.FieldType, options, f.Required, f.Position)
			return err
		})
	if err != nil {
		return err
	}
	if !inserted {
		imp.skip("custom_fields", f.Name, "custom field skipped: already exists in this workspace")
		return nil
	}
	imp.rep.Imported["custom_fields"]++
	return nil
}

var validCycleStatuses = map[string]bool{"upcoming": true, "current": true, "completed": true}
var validModuleStatuses = map[string]bool{"active": true, "completed": true, "archived": true}

func (imp *archiveImporter) importCycle(c *exportCycle) error {
	if !validArchiveUUID(c.ID) || !validArchiveUUID(c.ProjectID) {
		imp.skip("cycles", c.Name, "cycle skipped: malformed archived id")
		return nil
	}
	cycleKnown := imp.alreadyImported(imp.cycles, c.ID)
	if !cycleKnown {
		projectID := imp.resolveID(c.ProjectID)
		if !imp.projects[projectID] {
			imp.skip("cycles", c.Name, "cycle skipped: its project was not imported into this workspace")
			return nil
		}
		if !validCycleStatuses[c.Status] {
			imp.skip("cycles", c.Name,
				fmt.Sprintf("cycle skipped: unknown status %q", c.Status))
			return nil
		}
		conflict, err := imp.nameConflict(
			`SELECT id::text FROM cycles WHERE project_id = $1::uuid AND name = $2`,
			projectID, c.Name, c.ID)
		if err != nil {
			return err
		}
		if conflict {
			imp.skip("cycles", c.Name,
				fmt.Sprintf("cycle %q skipped: another cycle already has this name in the project", c.Name))
			return nil
		}
		if _, err := time.Parse("2006-01-02", c.StartDate); err != nil {
			imp.skip("cycles", c.Name, fmt.Sprintf("cycle skipped: unparseable start_date %q", c.StartDate))
			return nil
		}
		if _, err := time.Parse("2006-01-02", c.EndDate); err != nil {
			imp.skip("cycles", c.Name, fmt.Sprintf("cycle skipped: unparseable end_date %q", c.EndDate))
			return nil
		}
		_, inserted, err := imp.insertWithFallback(imp.cycles, "cycles", c.ID,
			func(id string) error {
				_, err := imp.tx.Exec(imp.ctx,
					`INSERT INTO cycles (id, project_id, name, start_date, end_date, status)
					 VALUES ($1::uuid, $2::uuid, $3, $4::date, $5::date, $6)`,
					id, projectID, c.Name, c.StartDate, c.EndDate, c.Status)
				return err
			})
		if err != nil {
			return err
		}
		if !inserted {
			imp.skip("cycles", c.Name, "cycle skipped: already exists in this workspace")
		} else {
			imp.rep.Imported["cycles"]++
		}
	} else {
		imp.skip("cycles", c.Name, "cycle skipped: already exists in this workspace")
	}
	// Memberships reference issues, which stream later: buffer the UUID
	// pairs and link them after the issues section. Always buffered
	// (even for pre-existing cycles) so retries complete missing links.
	for _, issueID := range c.IssueIDs {
		imp.cycleLinks = append(imp.cycleLinks, archiveLink{parent: c.ID, child: issueID})
	}
	return nil
}

func (imp *archiveImporter) importModule(m *exportModule) error {
	if !validArchiveUUID(m.ID) || !validArchiveUUID(m.ProjectID) {
		imp.skip("modules", m.Name, "module skipped: malformed archived id")
		return nil
	}
	moduleKnown := imp.alreadyImported(imp.modules, m.ID)
	if !moduleKnown {
		projectID := imp.resolveID(m.ProjectID)
		if !imp.projects[projectID] {
			imp.skip("modules", m.Name, "module skipped: its project was not imported into this workspace")
			return nil
		}
		if !validModuleStatuses[m.Status] {
			imp.skip("modules", m.Name,
				fmt.Sprintf("module skipped: unknown status %q", m.Status))
			return nil
		}
		conflict, err := imp.nameConflict(
			`SELECT id::text FROM modules WHERE project_id = $1::uuid AND name = $2`,
			projectID, m.Name, m.ID)
		if err != nil {
			return err
		}
		if conflict {
			imp.skip("modules", m.Name,
				fmt.Sprintf("module %q skipped: another module already has this name in the project", m.Name))
			return nil
		}
		var leadID any
		if m.LeadID != nil {
			lead, ok, err := imp.resolveUser(*m.LeadID)
			if err != nil {
				return err
			}
			if ok {
				leadID = lead
			} else {
				imp.skip("modules", m.Name,
					"module lead skipped: no instance user matches the archived lead (lead left empty)")
			}
		}
		var description any
		if m.Description != nil {
			description = *m.Description
		}
		_, inserted, err := imp.insertWithFallback(imp.modules, "modules", m.ID,
			func(id string) error {
				_, err := imp.tx.Exec(imp.ctx,
					`INSERT INTO modules (id, project_id, name, description, status, lead_id)
					 VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6::uuid)`,
					id, projectID, m.Name, description, m.Status, leadID)
				return err
			})
		if err != nil {
			return err
		}
		if !inserted {
			imp.skip("modules", m.Name, "module skipped: already exists in this workspace")
		} else {
			imp.rep.Imported["modules"]++
		}
	} else {
		imp.skip("modules", m.Name, "module skipped: already exists in this workspace")
	}
	for _, issueID := range m.IssueIDs {
		imp.moduleLinks = append(imp.moduleLinks, archiveLink{parent: m.ID, child: issueID})
	}
	return nil
}

func (imp *archiveImporter) importPage(p *exportPage) error {
	if !validArchiveUUID(p.ID) || !validArchiveUUID(p.ProjectID) ||
		(p.ParentID != nil && !validArchiveUUID(*p.ParentID)) {
		imp.skip("pages", p.Title, "page skipped: malformed archived id")
		return nil
	}
	if imp.alreadyImported(imp.pages, p.ID) {
		imp.skip("pages", p.Title, "page skipped: already exists in this workspace")
		return nil
	}
	projectID := imp.resolveID(p.ProjectID)
	if !imp.projects[projectID] {
		imp.skip("pages", p.Title, "page skipped: its project was not imported into this workspace")
		return nil
	}
	var authorID any
	if p.AuthorID != nil {
		author, ok, err := imp.resolveUser(*p.AuthorID)
		if err != nil {
			return err
		}
		if ok {
			authorID = author
		} else {
			imp.skip("pages", p.Title,
				"page author skipped: no instance user matches the archived author (author left empty)")
		}
	}
	finalID, inserted, err := imp.insertWithFallback(imp.pages, "pages", p.ID,
		func(id string) error {
			// parent_id applied after the section (parents may
			// stream later).
			_, err := imp.tx.Exec(imp.ctx,
				`INSERT INTO pages (id, project_id, title, content, position, author_id, created_at, updated_at)
				 VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6::uuid, $7, $8)`,
				id, projectID, p.Title, p.Content, p.Position, authorID, p.CreatedAt, p.UpdatedAt)
			return err
		})
	if err != nil {
		return err
	}
	if !inserted {
		imp.skip("pages", p.Title, "page skipped: already exists in this workspace")
		return nil
	}
	imp.rep.Imported["pages"]++
	if p.ParentID != nil {
		imp.pageParents = append(imp.pageParents, archiveLink{parent: *p.ParentID, child: finalID})
	}
	return nil
}

// importProjectUpdate restores one project status update (C34T2).
// Degradation mirrors comments: an unmapped author skips the update
// with an honest note — never silently dropped, never imported under
// a fabricated author. A null author (the author's user row was
// deleted, ON DELETE SET NULL) imports as null. An unknown status or
// an empty body skips the row: a hand-crafted archive must not
// smuggle in a row the service layer could never have created.
func (imp *archiveImporter) importProjectUpdate(u *exportProjectUpdate) error {
	if !validArchiveUUID(u.ID) || !validArchiveUUID(u.ProjectID) ||
		(u.AuthorID != nil && !validArchiveUUID(*u.AuthorID)) {
		imp.skip("project_updates", u.ID, "project update skipped: malformed archived id")
		return nil
	}
	if imp.alreadyImported(imp.projectUpdates, u.ID) {
		imp.skip("project_updates", u.ID, "project update skipped: already exists in this workspace")
		return nil
	}
	projectID := imp.resolveID(u.ProjectID)
	if !imp.projects[projectID] {
		imp.skip("project_updates", u.ID, "project update skipped: its project was not imported into this workspace")
		return nil
	}
	switch u.Status {
	case ProjectUpdateOnTrack, ProjectUpdateAtRisk, ProjectUpdateOffTrack:
	default:
		imp.skip("project_updates", u.ID,
			fmt.Sprintf("project update skipped: unknown status %q", u.Status))
		return nil
	}
	body := strings.TrimSpace(u.Body)
	if body == "" {
		imp.skip("project_updates", u.ID, "project update skipped: body is empty")
		return nil
	}
	var authorID any
	if u.AuthorID != nil {
		author, ok, err := imp.resolveUser(*u.AuthorID)
		if err != nil {
			return err
		}
		if !ok {
			authorEmail := ""
			if u.AuthorEmail != nil {
				authorEmail = *u.AuthorEmail
			}
			imp.skip("project_updates", u.ID,
				fmt.Sprintf("project update skipped: author %q has no user in this instance", authorEmail))
			return nil
		}
		authorID = author
	}
	_, inserted, err := imp.insertWithFallback(imp.projectUpdates, "project_updates", u.ID,
		func(id string) error {
			_, err := imp.tx.Exec(imp.ctx,
				`INSERT INTO project_updates (id, project_id, author_id, status, body, created_at, updated_at)
				 VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7)`,
				id, projectID, authorID, u.Status, body, u.CreatedAt, u.UpdatedAt)
			return err
		})
	if err != nil {
		return err
	}
	if !inserted {
		imp.skip("project_updates", u.ID, "project update skipped: already exists in this workspace")
		return nil
	}
	imp.rep.Imported["project_updates"]++
	return nil
}

// rawJSONB converts an archived JSON document to a ::jsonb insert
// argument: empty or literal null becomes SQL NULL (or the fallback).
func rawJSONB(raw json.RawMessage, fallback string) any {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		if fallback == "" {
			return nil
		}
		return fallback
	}
	return s
}

// archiveDate validates an archived YYYY-MM-DD string for a ::date
// insert argument; invalid dates become NULL with a skip entry.
func (imp *archiveImporter) archiveDate(section, id, field string, s *string) any {
	if s == nil || *s == "" {
		return nil
	}
	if _, err := time.Parse("2006-01-02", *s); err != nil {
		imp.skip(section, id,
			fmt.Sprintf("%s skipped: unparseable %s %q (left empty)", field, field, *s))
		return nil
	}
	return *s
}

func (imp *archiveImporter) importIssue(is *exportIssue) error {
	if !validArchiveUUID(is.ID) || !validArchiveUUID(is.ProjectID) ||
		!validArchiveUUID(is.StateID) ||
		(is.ParentID != nil && !validArchiveUUID(*is.ParentID)) ||
		(is.EstimatePointID != nil && !validArchiveUUID(*is.EstimatePointID)) ||
		!validArchiveUUID(is.CreatedBy) {
		imp.skip("issues", is.DisplayID, "issue skipped: malformed archived id")
		return nil
	}
	projectID := imp.resolveID(is.ProjectID)
	if !imp.projects[projectID] {
		imp.skip("issues", is.DisplayID, "issue skipped: its project was not imported into this workspace")
		return nil
	}

	issueKnown := imp.alreadyImported(imp.issues, is.ID)
	if !issueKnown {
		// The state must exist in the target AND belong to this
		// project: a hand-crafted archive could otherwise attach an
		// issue to another project's column.
		var stateProject string
		err := imp.tx.QueryRow(imp.ctx,
			`SELECT project_id::text FROM states WHERE id = $1::uuid`, imp.resolveID(is.StateID)).Scan(&stateProject)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				imp.skip("issues", is.DisplayID,
					"issue skipped: its state was not imported into this workspace")
			} else {
				return err
			}
			return nil
		}
		if stateProject != projectID {
			imp.skip("issues", is.DisplayID,
				"issue skipped: its state belongs to a different project")
			return nil
		}
		var otherID string
		err = imp.tx.QueryRow(imp.ctx,
			`SELECT id::text FROM issues WHERE project_id = $1::uuid AND sequence_id = $2`,
			projectID, is.SequenceID).Scan(&otherID)
		if err == nil && otherID != is.ID && otherID != imp.resolveID(is.ID) {
			// A different issue owns this sequence number: the
			// archived row (and its nested rows, which key off the
			// archived UUID) cannot be restored without corrupting
			// display IDs.
			imp.skip("issues", is.DisplayID,
				fmt.Sprintf("issue %q skipped: sequence number %d is already used by another issue in the project",
					is.Name, is.SequenceID))
			return nil
		} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if is.Priority < 0 || is.Priority > 4 {
			imp.skip("issues", is.DisplayID,
				fmt.Sprintf("issue %q skipped: priority %d out of range 0-4", is.Name, is.Priority))
			return nil
		}
		createdBy, ok, err := imp.resolveUser(is.CreatedBy)
		if err != nil {
			return err
		}
		if !ok {
			// created_by is NOT NULL: fall back to the importing
			// admin rather than dropping the whole issue.
			createdBy = imp.actorID
			imp.skip("issues", is.DisplayID,
				"issue author has no instance user: created_by set to the importing admin")
		}
		var estimatePointID any
		if is.EstimatePointID != nil {
			if imp.estimatePoints[imp.resolveID(*is.EstimatePointID)] {
				estimatePointID = imp.resolveID(*is.EstimatePointID)
			} else {
				imp.skip("issues", is.DisplayID,
					"issue estimate point skipped: the point was not imported (left empty)")
			}
		}
		finalID, inserted, err := imp.insertWithFallback(imp.issues, "issues", is.ID,
			func(id string) error {
				_, err := imp.tx.Exec(imp.ctx,
					`INSERT INTO issues (id, project_id, sequence_id, name, description,
					                     priority, state_id, start_date, target_date,
					                     estimate_point_id, is_draft, archived_at,
					                     created_by, created_at, updated_at)
					 VALUES ($1::uuid, $2::uuid, $3, $4, $5::jsonb, $6, $7::uuid,
					         $8::date, $9::date, $10::uuid, $11, $12, $13::uuid, $14, $15)`,
					id, projectID, is.SequenceID, is.Name,
					rawJSONB(is.Description, ""), is.Priority, imp.resolveID(is.StateID),
					imp.archiveDate("issues", is.DisplayID, "start_date", is.StartDate),
					imp.archiveDate("issues", is.DisplayID, "target_date", is.TargetDate),
					estimatePointID, is.IsDraft, is.ArchivedAt,
					createdBy, is.CreatedAt, is.UpdatedAt)
				return err
			})
		if err != nil {
			return err
		}
		if !inserted {
			imp.skip("issues", is.DisplayID, "issue skipped: already exists in this workspace")
		} else {
			imp.rep.Imported["issues"]++
			if is.SequenceID > imp.maxSeq[projectID] {
				imp.maxSeq[projectID] = is.SequenceID
			}
			// parent_id applied after the section (parents may
			// stream later).
			if is.ParentID != nil {
				imp.issueParents = append(imp.issueParents, archiveLink{parent: *is.ParentID, child: finalID})
			}
		}
	} else {
		imp.skip("issues", is.DisplayID, "issue skipped: already exists in this workspace")
	}

	// Nested rows carry their own UUIDs: they are processed even when
	// the issue row already existed, so a retry after a partial failure
	// completes the missing rows instead of silently dropping them.
	for i := range is.Assignees {
		if err := imp.importIssueAssignee(is, &is.Assignees[i]); err != nil {
			return err
		}
	}
	for i := range is.Labels {
		if err := imp.importIssueLabel(is, &is.Labels[i]); err != nil {
			return err
		}
	}
	for i := range is.CustomValues {
		if err := imp.importCustomValue(is, &is.CustomValues[i]); err != nil {
			return err
		}
	}
	for i := range is.Comments {
		if err := imp.importComment(is, &is.Comments[i]); err != nil {
			return err
		}
	}
	for i := range is.Attachments {
		a := &is.Attachments[i]
		// Attachments carry metadata only in the archive — the binary
		// was never exported, so there is nothing to restore. Counted
		// here, never silently dropped.
		imp.skip("attachments", a.Filename,
			fmt.Sprintf("attachment %q skipped: archives carry attachment metadata only, binaries are never exported", a.Filename))
	}
	return nil
}

func (imp *archiveImporter) importIssueAssignee(is *exportIssue, a *exportAssignee) error {
	uid, ok, err := imp.resolveUser(a.UserID)
	if err != nil {
		return err
	}
	if !ok {
		imp.skip("issue_assignees", is.DisplayID,
			fmt.Sprintf("assignee %q skipped: no instance user with this email (issue left unassigned)", a.Email))
		return nil
	}
	tag, err := imp.tx.Exec(imp.ctx,
		`INSERT INTO issue_assignees (issue_id, user_id)
		 VALUES ($1::uuid, $2::uuid) ON CONFLICT (issue_id, user_id) DO NOTHING`,
		imp.resolveID(is.ID), uid)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		imp.rep.Imported["issue_assignees"]++
	} else {
		imp.skip("issue_assignees", is.DisplayID,
			fmt.Sprintf("assignee %q skipped: already assigned", a.Email))
	}
	return nil
}

func (imp *archiveImporter) importIssueLabel(is *exportIssue, l *exportIssueLabel) error {
	if !validArchiveUUID(l.LabelID) {
		imp.skip("issue_labels", is.DisplayID, "issue label skipped: malformed archived id")
		return nil
	}
	labelID := imp.resolveID(l.LabelID)
	if !imp.labels[labelID] {
		imp.skip("issue_labels", is.DisplayID,
			fmt.Sprintf("issue label %q skipped: the label was not imported into this workspace", l.Name))
		return nil
	}
	tag, err := imp.tx.Exec(imp.ctx,
		`INSERT INTO issue_labels (issue_id, label_id)
		 VALUES ($1::uuid, $2::uuid) ON CONFLICT (issue_id, label_id) DO NOTHING`,
		imp.resolveID(is.ID), labelID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		imp.rep.Imported["issue_labels"]++
	} else {
		imp.skip("issue_labels", is.DisplayID,
			fmt.Sprintf("issue label %q skipped: already attached", l.Name))
	}
	return nil
}

// importCustomValue restores one custom field value. The exporter emits
// exactly one typed value per row (text/number/date/bool); the importer
// writes it back into the matching value_* column.
func (imp *archiveImporter) importCustomValue(is *exportIssue, cv *exportCustomValue) error {
	if !validArchiveUUID(cv.FieldID) {
		imp.skip("issue_custom_values", is.DisplayID, "custom value skipped: malformed archived id")
		return nil
	}
	fieldID := imp.resolveID(cv.FieldID)
	if !imp.customFields[fieldID] {
		imp.skip("issue_custom_values", is.DisplayID,
			fmt.Sprintf("custom value for field %q skipped: the field was not imported", cv.FieldName))
		return nil
	}
	var valueText, valueNumber, valueDate any
	var valueBool any
	switch cv.FieldType {
	case "text", "select":
		s, ok := cv.Value.(string)
		if !ok {
			imp.skip("issue_custom_values", is.DisplayID,
				fmt.Sprintf("custom value for field %q skipped: expected a string value", cv.FieldName))
			return nil
		}
		valueText = s
	case "number":
		var s string
		switch n := cv.Value.(type) {
		case json.Number:
			s = n.String()
		case string:
			s = n
		default:
			imp.skip("issue_custom_values", is.DisplayID,
				fmt.Sprintf("custom value for field %q skipped: expected a numeric value", cv.FieldName))
			return nil
		}
		if _, err := strconv.ParseFloat(s, 64); err != nil {
			imp.skip("issue_custom_values", is.DisplayID,
				fmt.Sprintf("custom value for field %q skipped: %q is not numeric", cv.FieldName, s))
			return nil
		}
		valueNumber = s
	case "date":
		s, ok := cv.Value.(string)
		if !ok {
			imp.skip("issue_custom_values", is.DisplayID,
				fmt.Sprintf("custom value for field %q skipped: expected a YYYY-MM-DD value", cv.FieldName))
			return nil
		}
		if _, err := time.Parse("2006-01-02", s); err != nil {
			imp.skip("issue_custom_values", is.DisplayID,
				fmt.Sprintf("custom value for field %q skipped: %q is not a valid date", cv.FieldName, s))
			return nil
		}
		valueDate = s
	case "checkbox":
		b, ok := cv.Value.(bool)
		if !ok {
			imp.skip("issue_custom_values", is.DisplayID,
				fmt.Sprintf("custom value for field %q skipped: expected a boolean value", cv.FieldName))
			return nil
		}
		valueBool = b
	default:
		imp.skip("issue_custom_values", is.DisplayID,
			fmt.Sprintf("custom value for field %q skipped: unknown field_type %q", cv.FieldName, cv.FieldType))
		return nil
	}
	tag, err := imp.tx.Exec(imp.ctx,
		`INSERT INTO issue_custom_values (issue_id, field_id, value_text, value_number, value_date, value_bool)
		 VALUES ($1::uuid, $2::uuid, $3, $4::numeric, $5::date, $6)
		 ON CONFLICT (issue_id, field_id) DO NOTHING`,
		imp.resolveID(is.ID), fieldID, valueText, valueNumber, valueDate, valueBool)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		imp.rep.Imported["issue_custom_values"]++
	} else {
		imp.skip("issue_custom_values", is.DisplayID,
			fmt.Sprintf("custom value for field %q skipped: already set", cv.FieldName))
	}
	return nil
}

func (imp *archiveImporter) importComment(is *exportIssue, c *exportComment) error {
	if !validArchiveUUID(c.ID) || !validArchiveUUID(c.ActorID) ||
		(c.ParentID != nil && !validArchiveUUID(*c.ParentID)) {
		imp.skip("comments", is.DisplayID, "comment skipped: malformed archived id")
		return nil
	}
	if imp.alreadyImported(imp.comments, c.ID) {
		imp.skip("comments", c.ID, "comment skipped: already exists in this workspace")
		return nil
	}
	actor, ok, err := imp.resolveUser(c.ActorID)
	if err != nil {
		return err
	}
	if !ok {
		imp.skip("comments", c.ID,
			fmt.Sprintf("comment skipped: actor %q has no user in this instance", c.ActorEmail))
		return nil
	}
	// Resolution round-trips (C32T0): restore resolved_at/resolved_by
	// on thread roots only — a reply carrying resolution in a
	// hand-crafted archive is imported unresolved, keeping the
	// root-only invariant. The resolver maps like the actor (members
	// section first, then direct UUID); when the UUID is unmapped the
	// archived email is tried, matching the members-by-email rule.
	// An unmapped resolver degrades to unresolved, reported like a
	// dropped assignee — the comment itself still imports.
	var resolvedAt any
	var resolvedBy any
	if c.ResolvedAt != nil && c.ParentID == nil {
		resolvedAt = c.ResolvedAt
		if c.ResolvedBy != nil {
			if resolver, ok, err := imp.resolveUser(*c.ResolvedBy); err != nil {
				return err
			} else if ok {
				resolvedBy = resolver
			}
		}
		if resolvedBy == nil && c.ResolvedByEmail != nil {
			var byEmail string
			err := imp.tx.QueryRow(imp.ctx,
				`SELECT id::text FROM users WHERE email = $1`, *c.ResolvedByEmail).Scan(&byEmail)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if err == nil {
				resolvedBy = byEmail
			}
		}
		if resolvedBy == nil {
			resolvedAt = nil
			imp.skip("comments", c.ID,
				"comment resolution dropped: resolver has no user in this instance (comment imported unresolved)")
		}
	}
	finalID, inserted, err := imp.insertWithFallback(imp.comments, "comments", c.ID,
		func(id string) error {
			// parent_id applied after the section (replies may
			// stream before their parent).
			_, err := imp.tx.Exec(imp.ctx,
				`INSERT INTO comments (id, issue_id, actor_id, content, created_at, updated_at, resolved_at, resolved_by)
				 VALUES ($1::uuid, $2::uuid, $3::uuid, $4::jsonb, $5, $6, $7, $8::uuid)`,
				id, imp.resolveID(is.ID), actor, rawJSONB(c.Content, "{}"), c.CreatedAt, c.UpdatedAt, resolvedAt, resolvedBy)
			return err
		})
	if err != nil {
		return err
	}
	if !inserted {
		imp.skip("comments", c.ID, "comment skipped: already exists in this workspace")
		return nil
	}
	imp.rep.Imported["comments"]++
	if c.ParentID != nil {
		imp.commentParents = append(imp.commentParents, archiveLink{parent: *c.ParentID, child: finalID})
	}
	return nil
}

// importObjective restores one objective with its nested key
// results (C35T2). The owner is resolved BY EMAIL: an unmapped owner
// degrades to null with an honest note — never a fabricated owner —
// and the objective still imports. A title conflict with a DIFFERENT
// objective skips the objective; its KRs then hit the
// "objective was not imported" check below, so no KR is silently
// dropped or merged into the unrelated objective. KRs are processed
// even when the objective already exists, so a retry after a partial
// failure completes the missing KRs (the same discipline as issue
// nested rows).
func (imp *archiveImporter) importObjective(o *exportObjective) error {
	insertable := true
	if !validArchiveUUID(o.ID) {
		imp.skip("objectives", o.Title, "objective skipped: malformed archived id")
		insertable = false
	} else if strings.TrimSpace(o.Title) == "" {
		imp.skip("objectives", o.ID, "objective skipped: title is empty")
		insertable = false
	} else {
		switch o.Status {
		case "active", "completed", "archived":
		default:
			imp.skip("objectives", o.Title,
				fmt.Sprintf("objective skipped: unknown status %q", o.Status))
			insertable = false
		}
	}
	if insertable {
		var ownerID any
		if o.OwnerEmail != nil && *o.OwnerEmail != "" {
			var found string
			err := imp.tx.QueryRow(imp.ctx,
				`SELECT id::text FROM users WHERE email = $1`, *o.OwnerEmail).Scan(&found)
			if err == nil {
				ownerID = found
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return err
			} else {
				imp.skip("objectives", o.Title,
					fmt.Sprintf("objective owner %q has no user in this instance (owner left empty)", *o.OwnerEmail))
			}
		}
		startDate := imp.archiveDate("objectives", o.Title, "start_date", o.StartDate)
		targetDate := imp.archiveDate("objectives", o.Title, "target_date", o.TargetDate)
		if imp.alreadyImported(imp.objectives, o.ID) {
			imp.skip("objectives", o.Title, "objective skipped: already exists in this workspace")
		} else {
			conflict, err := imp.nameConflict(
				`SELECT id::text FROM objectives WHERE workspace_id = $1::uuid AND title = $2`,
				imp.wsID, o.Title, o.ID)
			if err != nil {
				return err
			}
			if conflict {
				imp.skip("objectives", o.Title,
					fmt.Sprintf("objective %q skipped: another objective already has this title in the workspace", o.Title))
			} else {
				_, inserted, err := imp.insertWithFallback(imp.objectives, "objectives", o.ID,
					func(id string) error {
						_, err := imp.tx.Exec(imp.ctx,
							`INSERT INTO objectives (id, workspace_id, title, description, owner_id,
							                         start_date, target_date, status, created_at, updated_at)
							 VALUES ($1::uuid, $2::uuid, $3, $4, $5::uuid, $6::date, $7::date, $8, $9, $10)`,
							id, imp.wsID, o.Title, o.Description, ownerID,
							startDate, targetDate, o.Status, o.CreatedAt, o.UpdatedAt)
						return err
					})
				if err != nil {
					return err
				}
				if !inserted {
					imp.skip("objectives", o.Title, "objective skipped: already exists in this workspace")
				} else {
					imp.rep.Imported["objectives"]++
				}
			}
		}
	}
	for i := range o.KeyResults {
		if err := imp.importKeyResult(o, &o.KeyResults[i]); err != nil {
			return err
		}
	}
	return nil
}

// importKeyResult restores one key result under its objective. A KR
// whose target equals its start violates the zero-span CHECK and is
// skipped with a note naming the KR — never imported. An issue KR
// carrying unit/current_value (meaningless for issue KRs — progress
// never reads them) is normalized to the service's canonical form
// (unit NULL, current 0) with an honest note, the same
// note-and-continue discipline as archiveDate. Links and check-ins
// are processed even when the KR already exists, so retries complete
// them. Check-ins restore after the KR insert (C42T2); a KR that
// was skipped imports no check-ins, and restoring history never
// changes the KR's current_value.
func (imp *archiveImporter) importKeyResult(o *exportObjective, kr *exportKeyResult) error {
	if !validArchiveUUID(kr.ID) {
		imp.skip("key_results", kr.Title, "key result skipped: malformed archived id")
		return nil
	}
	objectiveID := imp.resolveID(o.ID)
	if !imp.objectives[objectiveID] {
		imp.skip("key_results", kr.Title, "key result skipped: its objective was not imported into this workspace")
		return nil
	}
	if strings.TrimSpace(kr.Title) == "" {
		imp.skip("key_results", kr.ID, "key result skipped: title is empty")
		return nil
	}
	switch kr.Kind {
	case "metric", "issue":
	default:
		imp.skip("key_results", kr.Title,
			fmt.Sprintf("key result %q skipped: unknown kind %q", kr.Title, kr.Kind))
		return nil
	}
	start, err := kr.StartValue.Float64()
	if err != nil {
		imp.skip("key_results", kr.Title,
			fmt.Sprintf("key result %q skipped: start_value %q is not numeric", kr.Title, kr.StartValue.String()))
		return nil
	}
	target, err := kr.TargetValue.Float64()
	if err != nil {
		imp.skip("key_results", kr.Title,
			fmt.Sprintf("key result %q skipped: target_value %q is not numeric", kr.Title, kr.TargetValue.String()))
		return nil
	}
	current, err := kr.CurrentValue.Float64()
	if err != nil {
		imp.skip("key_results", kr.Title,
			fmt.Sprintf("key result %q skipped: current_value %q is not numeric", kr.Title, kr.CurrentValue.String()))
		return nil
	}
	// The zero-span CHECK (target <> start): a KR that can never move
	// is rejected at write by the service, so the importer must not
	// smuggle one in — skip with a note naming the KR.
	if target == start {
		imp.skip("key_results", kr.Title,
			fmt.Sprintf("key result %q skipped: target equals start (zero-span key results cannot move)", kr.Title))
		return nil
	}
	var unit any
	currentStr := kr.CurrentValue.String()
	if kr.Kind == "issue" {
		if kr.Unit != nil {
			imp.skip("key_results", kr.Title,
				fmt.Sprintf("key result %q: unit %q ignored (issue key results carry no unit)", kr.Title, *kr.Unit))
		}
		if current != 0 {
			imp.skip("key_results", kr.Title,
				fmt.Sprintf("key result %q: current value ignored (issue key result progress derives from linked issues)", kr.Title))
		}
		currentStr = "0"
	} else if kr.Unit != nil {
		if u := strings.TrimSpace(*kr.Unit); u != "" {
			unit = u
		}
	}
	if imp.alreadyImported(imp.keyResults, kr.ID) {
		imp.skip("key_results", kr.Title, "key result skipped: already exists in this workspace")
	} else {
		_, inserted, err := imp.insertWithFallback(imp.keyResults, "key_results", kr.ID,
			func(id string) error {
				_, err := imp.tx.Exec(imp.ctx,
					`INSERT INTO key_results (id, objective_id, title, kind, unit,
					                           start_value, target_value, current_value,
					                           created_at, updated_at)
					 VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6::numeric, $7::numeric, $8::numeric, $9, $10)`,
					id, objectiveID, kr.Title, kr.Kind, unit,
					kr.StartValue.String(), kr.TargetValue.String(), currentStr,
					kr.CreatedAt, kr.UpdatedAt)
				return err
			})
		if err != nil {
			return err
		}
		if !inserted {
			imp.skip("key_results", kr.Title, "key result skipped: already exists in this workspace")
		} else {
			imp.rep.Imported["key_results"]++
		}
	}
	for i := range kr.LinkedIssues {
		if err := imp.importKRLink(kr, &kr.LinkedIssues[i]); err != nil {
			return err
		}
	}
	for i := range kr.CheckIns {
		if err := imp.importKRCheckin(kr, &kr.CheckIns[i]); err != nil {
			return err
		}
	}
	return nil
}

// importKRCheckin restores one check-in entry of a metric KR (C42T2).
// The author resolves BY EMAIL (the same discipline as the objective
// owner): an unmapped author degrades to a null author with an honest
// note — never a fabricated author — and the check-in still imports.
// An absent check_ins field in an old archive decodes to nil, so
// this loop simply never runs (backfill covers those KRs later).
// A non-numeric previous/new value skips the entry: a hand-crafted
// archive must not smuggle in a row the service could never have
// created. Check-ins on an issue KR skip with a note (issue KRs
// never record check-ins). The insert touches only the check-ins
// table — the KR's current_value is never rewritten by history.
func (imp *archiveImporter) importKRCheckin(kr *exportKeyResult, c *exportKRCheckin) error {
	if kr.Kind == "issue" {
		imp.skip("key_result_checkins", kr.Title,
			fmt.Sprintf("key result %q: check-in %q skipped: issue key results never record check-ins", kr.Title, c.ID))
		return nil
	}
	if !validArchiveUUID(c.ID) {
		imp.skip("key_result_checkins", kr.Title, "check-in skipped: malformed archived id")
		return nil
	}
	if imp.alreadyImported(imp.checkins, c.ID) {
		imp.skip("key_result_checkins", kr.Title, "check-in skipped: already exists in this workspace")
		return nil
	}
	if _, err := c.NewValue.Float64(); err != nil {
		imp.skip("key_result_checkins", kr.Title,
			fmt.Sprintf("key result %q: check-in %q skipped: new_value %q is not numeric", kr.Title, c.ID, c.NewValue.String()))
		return nil
	}
	var previous any
	if c.PreviousValue != nil {
		if _, err := c.PreviousValue.Float64(); err != nil {
			imp.skip("key_result_checkins", kr.Title,
				fmt.Sprintf("key result %q: check-in %q skipped: previous_value %q is not numeric", kr.Title, c.ID, c.PreviousValue.String()))
			return nil
		}
		previous = c.PreviousValue.String()
	}
	var authorID any
	if c.AuthorEmail != nil && *c.AuthorEmail != "" {
		var found string
		err := imp.tx.QueryRow(imp.ctx,
			`SELECT id::text FROM users WHERE email = $1`, *c.AuthorEmail).Scan(&found)
		if err == nil {
			authorID = found
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		} else {
			imp.skip("key_result_checkins", kr.Title,
				fmt.Sprintf("key result %q: check-in %q author %q has no user in this instance (author left empty)", kr.Title, c.ID, *c.AuthorEmail))
		}
	}
	// A null email means a null author (the author's user row was
	// deleted, ON DELETE SET NULL): imported as null, no note — the
	// archive faithfully carries the deletion.
	_, inserted, err := imp.insertWithFallback(imp.checkins, "key_result_checkins", c.ID,
		func(id string) error {
			_, err := imp.tx.Exec(imp.ctx,
				`INSERT INTO key_result_checkins (id, key_result_id, author_id,
				                                  previous_value, new_value, note, created_at)
				 VALUES ($1::uuid, $2::uuid, $3::uuid, $4::numeric, $5::numeric, $6, $7)`,
				id, imp.resolveID(kr.ID), authorID, previous, c.NewValue.String(), c.Note, c.CreatedAt)
			return err
		})
	if err != nil {
		return err
	}
	if !inserted {
		imp.skip("key_result_checkins", kr.Title, "check-in skipped: already exists in this workspace")
		return nil
	}
	imp.rep.Imported["key_result_checkins"]++
	return nil
}

// importKRLink restores one issue-KR link, resolving the issue by
// project identifier + display_id — the stable cross-archive key.
// An unresolvable link skips with a note naming the KR; the KR
// itself still imports (with no links). Links on a metric KR are
// invalid (the service rejects them) and skip with a note.
func (imp *archiveImporter) importKRLink(kr *exportKeyResult, l *exportKRLink) error {
	if kr.Kind != "issue" {
		imp.skip("key_result_issue_links", kr.Title,
			fmt.Sprintf("key result %q: link to %q skipped: metric key results cannot link issues", kr.Title, l.IssueDisplayID))
		return nil
	}
	prefix := l.ProjectIdentifier + "-"
	if !strings.HasPrefix(l.IssueDisplayID, prefix) {
		imp.skip("key_result_issue_links", kr.Title,
			fmt.Sprintf("key result %q: link to %q skipped: display id does not match project %q", kr.Title, l.IssueDisplayID, l.ProjectIdentifier))
		return nil
	}
	seq, err := strconv.Atoi(strings.TrimPrefix(l.IssueDisplayID, prefix))
	if err != nil || seq < 0 {
		imp.skip("key_result_issue_links", kr.Title,
			fmt.Sprintf("key result %q: link to %q skipped: unparseable display id", kr.Title, l.IssueDisplayID))
		return nil
	}
	var issueID string
	err = imp.tx.QueryRow(imp.ctx,
		`SELECT i.id::text
		 FROM issues i JOIN projects p ON p.id = i.project_id
		 WHERE p.workspace_id = $1::uuid AND p.identifier = $2
		   AND i.sequence_id = $3 AND i.deleted_at IS NULL`,
		imp.wsID, l.ProjectIdentifier, seq).Scan(&issueID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			imp.skip("key_result_issue_links", kr.Title,
				fmt.Sprintf("key result %q: link to %q skipped: issue not found in this workspace (key result imported with no links)", kr.Title, l.IssueDisplayID))
			return nil
		}
		return err
	}
	tag, err := imp.tx.Exec(imp.ctx,
		`INSERT INTO key_result_issues (key_result_id, issue_id)
		 VALUES ($1::uuid, $2::uuid) ON CONFLICT (key_result_id, issue_id) DO NOTHING`,
		imp.resolveID(kr.ID), issueID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		imp.rep.Imported["key_result_issue_links"]++
	} else {
		imp.skip("key_result_issue_links", kr.Title,
			fmt.Sprintf("key result %q: link to %q skipped: already linked", kr.Title, l.IssueDisplayID))
	}
	return nil
}

// importPageIssueLink restores one page↔issue link (C36T2). The page
// resolves by id-then-title: the archived page UUID through the
// remap first, else a title lookup within the link's project (a
// page restored under a different UUID by another path still
// re-links). The issue resolves by project identifier + display_id,
// the stable cross-archive key (the same discipline as KR links).
// An unresolvable side skips the link with a note naming the link —
// never dropped silently, never a fabricated page/issue. A link
// whose page and issue land in different projects skips: the service
// never creates cross-project links. Re-import hits ON CONFLICT
// (page_id, issue_id) DO NOTHING and counts as an "already linked"
// skip — idempotent, never a duplicate, never an error.
func (imp *archiveImporter) importPageIssueLink(l *exportPageIssueLink) error {
	linkID := l.IssueDisplayID
	if linkID == "" {
		linkID = l.ID
	}
	if strings.TrimSpace(l.ProjectIdentifier) == "" || strings.TrimSpace(l.PageTitle) == "" ||
		strings.TrimSpace(l.IssueDisplayID) == "" {
		imp.skip("page_issue_links", linkID,
			"page-issue link skipped: missing project identifier, page title, or issue display id")
		return nil
	}
	// Page: archived UUID through the remap first, then title.
	var pageID, pageProjectID string
	if validArchiveUUID(l.PageID) {
		if resolved := imp.resolveID(l.PageID); imp.pages[resolved] {
			pageID = resolved
		}
	}
	if pageID == "" {
		err := imp.tx.QueryRow(imp.ctx,
			`SELECT pg.id::text, pg.project_id::text
			 FROM pages pg JOIN projects p ON p.id = pg.project_id
			 WHERE p.workspace_id = $1::uuid AND p.identifier = $2 AND pg.title = $3
			 ORDER BY pg.created_at, pg.id LIMIT 1`,
			imp.wsID, l.ProjectIdentifier, l.PageTitle).Scan(&pageID, &pageProjectID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				imp.skip("page_issue_links", linkID,
					fmt.Sprintf("page-issue link to %q skipped: page %q not found in project %q (page imported with no link)",
						l.IssueDisplayID, l.PageTitle, l.ProjectIdentifier))
				return nil
			}
			return err
		}
	} else {
		// The UUID-resolved page still needs its project for the
		// same-project check below.
		if err := imp.tx.QueryRow(imp.ctx,
			`SELECT project_id::text FROM pages WHERE id = $1::uuid`, pageID).Scan(&pageProjectID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				imp.skip("page_issue_links", linkID,
					fmt.Sprintf("page-issue link to %q skipped: page %q not found in this workspace (page imported with no link)",
						l.IssueDisplayID, l.PageTitle))
				return nil
			}
			return err
		}
	}
	// Issue: display_id must match the link's project (same prefix
	// check as KR links), then resolve by identifier + sequence.
	prefix := l.ProjectIdentifier + "-"
	if !strings.HasPrefix(l.IssueDisplayID, prefix) {
		imp.skip("page_issue_links", linkID,
			fmt.Sprintf("page-issue link for page %q skipped: display id %q does not match project %q",
				l.PageTitle, l.IssueDisplayID, l.ProjectIdentifier))
		return nil
	}
	seq, err := strconv.Atoi(strings.TrimPrefix(l.IssueDisplayID, prefix))
	if err != nil || seq < 0 {
		imp.skip("page_issue_links", linkID,
			fmt.Sprintf("page-issue link for page %q skipped: unparseable display id %q",
				l.PageTitle, l.IssueDisplayID))
		return nil
	}
	var issueID, issueProjectID string
	err = imp.tx.QueryRow(imp.ctx,
		`SELECT i.id::text, i.project_id::text
		 FROM issues i JOIN projects p ON p.id = i.project_id
		 WHERE p.workspace_id = $1::uuid AND p.identifier = $2
		   AND i.sequence_id = $3 AND i.deleted_at IS NULL`,
		imp.wsID, l.ProjectIdentifier, seq).Scan(&issueID, &issueProjectID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			imp.skip("page_issue_links", linkID,
				fmt.Sprintf("page-issue link for page %q skipped: issue %q not found in this workspace (page imported with no link)",
					l.PageTitle, l.IssueDisplayID))
			return nil
		}
		return err
	}
	if pageProjectID != issueProjectID {
		imp.skip("page_issue_links", linkID,
			fmt.Sprintf("page-issue link for page %q to %q skipped: page and issue are in different projects",
				l.PageTitle, l.IssueDisplayID))
		return nil
	}
	// Creator resolves like a page author: unmapped → null, noted.
	var createdBy any
	if l.CreatedBy != nil {
		creator, ok, err := imp.resolveUser(*l.CreatedBy)
		if err != nil {
			return err
		}
		if ok {
			createdBy = creator
		} else {
			imp.skip("page_issue_links", linkID,
				"page-issue link creator skipped: no instance user matches the archived creator (creator left empty)")
		}
	}
	tag, err := imp.tx.Exec(imp.ctx,
		`INSERT INTO page_issue_links (page_id, issue_id, created_by, created_at)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, $4)
		 ON CONFLICT (page_id, issue_id) DO NOTHING`,
		pageID, issueID, createdBy, l.CreatedAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		imp.rep.Imported["page_issue_links"]++
	} else {
		imp.skip("page_issue_links", linkID,
			fmt.Sprintf("page-issue link for page %q to %q skipped: already linked", l.PageTitle, l.IssueDisplayID))
	}
	return nil
}

// applyDeferred links the buffered cross-section references now that
// every section has streamed: label/page parents, issue parents,
// comment parents, and cycle/module issue memberships. Archived UUIDs
// are resolved through the remap first. Membership inserts are
// ON CONFLICT DO NOTHING so re-import stays idempotent.
func (imp *archiveImporter) applyDeferred() error {
	applyParent := func(section, table string, links []archiveLink, known map[string]bool) error {
		for _, l := range links {
			parent := imp.resolveID(l.parent)
			child := imp.resolveID(l.child)
			if !known[parent] {
				imp.skip(section, l.child,
					"parent link skipped: the parent row was not imported")
				continue
			}
			if _, err := imp.tx.Exec(imp.ctx,
				`UPDATE `+table+` SET parent_id = $1::uuid WHERE id = $2::uuid`,
				parent, child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := applyParent("labels", "labels", imp.labelParents, imp.labels); err != nil {
		return err
	}
	if err := applyParent("pages", "pages", imp.pageParents, imp.pages); err != nil {
		return err
	}
	if err := applyParent("issues", "issues", imp.issueParents, imp.issues); err != nil {
		return err
	}
	if err := applyParent("comments", "comments", imp.commentParents, imp.comments); err != nil {
		return err
	}
	applyMembership := func(section, table, parentCol string, links []archiveLink, parents, children map[string]bool) error {
		for _, l := range links {
			parent := imp.resolveID(l.parent)
			child := imp.resolveID(l.child)
			if !children[child] {
				imp.skip(section, l.child,
					"membership skipped: the issue was not imported into this workspace")
				continue
			}
			if !parents[parent] {
				imp.skip(section, l.parent,
					"membership skipped: the parent row was not imported")
				continue
			}
			tag, err := imp.tx.Exec(imp.ctx,
				`INSERT INTO `+table+` (`+parentCol+`, issue_id) VALUES ($1::uuid, $2::uuid)
				 ON CONFLICT (`+parentCol+`, issue_id) DO NOTHING`,
				parent, child)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 1 {
				imp.rep.Imported[section]++
			} else {
				imp.skip(section, l.child, "membership skipped: already linked")
			}
		}
		return nil
	}
	if err := applyMembership("cycle_issues", "cycle_issues", "cycle_id", imp.cycleLinks, imp.cycles, imp.issues); err != nil {
		return err
	}
	if err := applyMembership("module_issues", "module_issues", "module_id", imp.moduleLinks, imp.modules, imp.issues); err != nil {
		return err
	}
	return nil
}

// advanceSequences moves each touched project's issue_sequences counter
// past the highest restored sequence_id, so the next created issue
// cannot collide with a restored display ID.
func (imp *archiveImporter) advanceSequences() error {
	for projectID, maxSeq := range imp.maxSeq {
		if _, err := imp.tx.Exec(imp.ctx,
			`INSERT INTO issue_sequences (project_id, last_value)
			 VALUES ($1::uuid, $2)
			 ON CONFLICT (project_id) DO UPDATE
			 SET last_value = GREATEST(issue_sequences.last_value, EXCLUDED.last_value)`,
			projectID, maxSeq); err != nil {
			return err
		}
	}
	return nil
}
