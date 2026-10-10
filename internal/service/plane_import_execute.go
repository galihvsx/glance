package service

// Plane-native export importer — T3: two-phase execute (C14T3, cycle 14
// "Plane-native import").
//
// ExecutePlaneImport is the write half of the two-phase importer. T1
// (ParsePlaneImport) parses the export; T2 (AnalyzePlaneImport) produces
// the unresolved inventory; this step takes the user's resolutions and
// imports everything in one transaction:
//
// Phase 1 (validation, no writes): every row is transformed — priority
// string→0-4 (the CSV importer's vocabulary map), dates must be
// YYYY-MM-DD, estimate values must match the project's estimate scales,
// parent identifiers must be well-formed (last-dash split). Failures are
// collected as []PlaneImportRowError keyed by Plane identifier; a bad row
// never aborts the others.
//
// Phase 2 (one tx — never partial state): missing states/labels/cycles/
// modules are created per the resolutions (unknown state_name → created
// in group "backlog" by default, "strict_states" opts into row errors);
// issues are inserted preserving Plane sequence_id and the project's
// sequence counter is advanced past the max imported id in the same tx
// (the archive importer's counter-repair pattern); parent_id is wired in
// a second pass via an identifier→UUID map (dangling parent → root issue
// + gap, never a failure); comments are inserted as raw rows with naive
// timestamps interpreted as UTC and HTML stripped defensively; links and
// subscribers follow the resolutions; an import-run record keyed by
// (project_id, plane_project_identifier) is written for idempotency —
// re-running the same export check-then-skips already-imported sequence
// ids (durable) and the run record (audit).
//
// Migration-free by design: everything rides on existing tables.
// glance has no external-link storage and no issue-to-issue relations, so
// Plane links and relations are never inserted — they are reported as
// per-issue gaps instead.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// planeImportMaxRows is the soft row cap for one execute run: exports
// bigger than this are rejected with a clear error rather than imported
// partially. The cap is checked after T1 parses (T1 already caps input at
// 256 MiB); callers needing more can raise it via opts.MaxRows.
const planeImportMaxRows = 50000

// planeImportStateGroups is the vocabulary for created states (mirrors the
// states table CHECK); planeDefaultStateGroup is design decision 6.
const planeDefaultStateGroup = "backlog"

var planeImportStateGroups = map[string]bool{
	"triage": true, "backlog": true, "unstarted": true,
	"started": true, "completed": true, "cancelled": true,
}

// planePersonResolution maps one Plane display name to a workspace member.
// Only mapping is supported: glance has no identity to fabricate a new
// user from a bare display name, so "create" is deliberately not offered
// for people — names absent from the resolutions stay unmapped (issue
// left unassigned, comment attributed to the importing actor) and are
// reported in the report's gaps. No silent guessing.
type PlanePersonResolution struct {
	MemberID string `json:"member_id"`
}

// PlaneStateResolution maps one Plane state name to an existing glance
// state, or requests creation (empty StateID) in Group (default
// "backlog" — design decision 6). Names with no entry follow the default
// policy: match case-insensitively to an existing state, else create in
// group "backlog" — unless StrictStates (opts) turns unknown states into
// row errors.
type PlaneStateResolution struct {
	StateID string `json:"state_id,omitempty"`
	Group   string `json:"group,omitempty"`
}

// PlaneLabelResolution maps one Plane label name to an existing glance
// label (workspace-scoped). Empty LabelID (or no entry) creates the label
// with the default color.
type PlaneLabelResolution struct {
	LabelID string `json:"label_id,omitempty"`
}

// PlaneCycleResolution maps one Plane cycle name to an existing glance
// cycle. Creating (empty CycleID, no entry) needs StartDate/EndDate
// (YYYY-MM-DD); without a resolution the importer creates the cycle with
// a documented default window (today → +30 days, status "upcoming").
type PlaneCycleResolution struct {
	CycleID   string `json:"cycle_id,omitempty"`
	StartDate string `json:"start_date,omitempty"`
	EndDate   string `json:"end_date,omitempty"`
}

// PlaneModuleResolution maps one Plane module name to an existing glance
// module. Empty ModuleID (or no entry) creates the module (status
// "active").
type PlaneModuleResolution struct {
	ModuleID string `json:"module_id,omitempty"`
}

// PlaneImportResolutions carries the user's explicit mappings for the
// names AnalyzePlaneImport reported as unresolved. Every map is keyed by
// Plane display name, matched case-insensitively (trimmed). The shape is
// JSON-serializable so T4 can take it as the execute HTTP body verbatim.
type PlaneImportResolutions struct {
	People  map[string]PlanePersonResolution `json:"people"`
	States  map[string]PlaneStateResolution  `json:"states"`
	Labels  map[string]PlaneLabelResolution  `json:"labels"`
	Cycles  map[string]PlaneCycleResolution  `json:"cycles"`
	Modules map[string]PlaneModuleResolution `json:"modules"`
}

// PlaneImportExecuteOpts tunes one execute run.
type PlaneImportExecuteOpts struct {
	// StrictStates turns an unknown state_name into a row error instead
	// of creating the state (CSV-importer-style error-on-unknown).
	StrictStates bool `json:"strict_states"`
	// MaxRows overrides the soft row cap (0 = planeImportMaxRows).
	MaxRows int `json:"max_rows,omitempty"`
}

// PlaneImportGap is one imported issue's loss documentation: everything
// about the Plane row that did not survive the import, in plain words.
type PlaneImportGap struct {
	Identifier string   `json:"identifier"`
	Losses     []string `json:"losses"`
}

// PlaneImportReport is the execute outcome.
type PlaneImportReport struct {
	ProjectIdentifier string                `json:"project_identifier"`
	Created           int                   `json:"created"`
	Skipped           int                   `json:"skipped"`
	Failed            int                   `json:"failed"`
	Errors            []PlaneImportRowError `json:"errors"`
	Gaps              []PlaneImportGap      `json:"gaps"`
	StatesCreated     []string              `json:"states_created"`
	LabelsCreated     []string              `json:"labels_created"`
	CyclesCreated     []string              `json:"cycles_created"`
	ModulesCreated    []string              `json:"modules_created"`
	Warnings          []string              `json:"warnings"`
}

// planeEstimateScale is the project's estimate points for import matching
// (shared by T2 analysis and T3 execute — T2's lookups are derived from
// this). A value matches when it equals a point's numeric value; a
// non-empty string matches a point's key exactly.
type planeEstimateScale struct {
	idsByValue map[int]string
	idsByKey   map[string]string
	hasScale   bool
}

// planeScaleQueryer is satisfied by *pgxpool.Pool and pgx.Tx.
type planeScaleQueryer interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// loadPlaneEstimateScale loads every estimate point across ALL of the
// project's scales: glance has no single "active" scale, so a value is
// fine when any scale covers it. First point wins on duplicate values,
// in a stable order.
func loadPlaneEstimateScale(ctx context.Context, q planeScaleQueryer, projectID string) (*planeEstimateScale, error) {
	s := &planeEstimateScale{
		idsByValue: map[int]string{},
		idsByKey:   map[string]string{},
	}
	rows, err := q.Query(ctx,
		`SELECT ep.id::text, ep.key, ep.value FROM estimate_points ep
		  JOIN estimates e ON e.id = ep.estimate_id
		 WHERE e.project_id = $1::uuid
		 ORDER BY e.created_at, ep.key`, projectID)
	if err != nil {
		return nil, fmt.Errorf("service: load plane estimate scale: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, key string
		var value int
		if err := rows.Scan(&id, &key, &value); err != nil {
			return nil, fmt.Errorf("service: load plane estimate scale: %w", err)
		}
		if _, ok := s.idsByKey[key]; !ok {
			s.idsByKey[key] = id
		}
		if _, ok := s.idsByValue[value]; !ok {
			s.idsByValue[value] = id
		}
		s.hasScale = true
	}
	return s, rows.Err()
}

// lookup resolves a classified estimate payload to an estimate point id.
// ok=false means "unset": absent/null/"", or a set value that matches no
// point (the caller records a gap — never a row error). classifyPlaneEstimate
// is T2's classifier, reused verbatim.
func (s *planeEstimateScale) lookup(display string, kind planeEstimateKind) (pointID string, ok bool) {
	switch kind {
	case planeEstimateUnset:
		return "", false
	case planeEstimateNumber:
		if f, err := strconv.ParseFloat(display, 64); err == nil && f == math.Trunc(f) {
			if id, hit := s.idsByValue[int(f)]; hit {
				return id, true
			}
		}
		return "", false
	case planeEstimateString:
		if id, hit := s.idsByKey[display]; hit {
			return id, true
		}
		return "", false
	default: // planeEstimateOther
		return "", false
	}
}

// parsePlanePriority maps a Plane priority string to 0-4, reusing the CSV
// importer's vocabulary map (none/low/medium/high/urgent) plus bare
// numbers. Empty means 0 (none).
func parsePlanePriority(s string) (int, error) {
	p := strings.TrimSpace(s)
	if p == "" {
		return 0, nil
	}
	if n, err := strconv.Atoi(p); err == nil {
		if n < 0 || n > 4 {
			return 0, fmt.Errorf("invalid priority %q (want 0-4 or none/low/medium/high/urgent)", s)
		}
		return n, nil
	}
	if n, ok := importPriorityNames[strings.ToLower(p)]; ok {
		return n, nil
	}
	return 0, fmt.Errorf("unknown priority %q", s)
}

// parsePlaneDate parses a YYYY-MM-DD date strictly (time.Parse rejects
// "not-a-date" and short forms like 2026-1-2).
func parsePlaneDate(s string) (time.Time, error) {
	t, err := time.Parse("2006-01-02", strings.TrimSpace(s))
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid date %q (want YYYY-MM-DD)", s)
	}
	return t, nil
}

// planeTimestampLayouts covers the naive "2026-10-05 14:22:10" Plane
// emits for comments and the offset "...+00:00" form it emits for
// created_at. Naive values are interpreted as UTC (time.Parse returns
// UTC when the layout carries no zone).
var planeTimestampLayouts = []string{
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05Z07:00",
	time.RFC3339,
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05",
}

func parsePlaneTimestamp(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range planeTimestampLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid timestamp %q", s)
}

var (
	planeScriptRe = regexp.MustCompile(`(?is)<script.*?</script>`)
	planeStyleRe  = regexp.MustCompile(`(?is)<style.*?</style>`)
	planeTagRe    = regexp.MustCompile(`(?s)<[^>]*>`)
)

// stripPlaneHTML defensively strips markup from an imported comment:
// script/style blocks are removed entirely (a naive tag stripper would
// leave their text behind), then all tags, then HTML entities are
// unescaped and whitespace collapsed. The result is plain text — raw HTML
// is never stored.
func stripPlaneHTML(s string) string {
	s = planeScriptRe.ReplaceAllString(s, " ")
	s = planeStyleRe.ReplaceAllString(s, " ")
	s = planeTagRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(s), " ")
}

// planeResolutionKey normalizes a resolution map key: Plane display names
// are matched case-insensitively, trimmed.
func planeResolutionKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// normalizeResolutions returns a copy of the resolutions with every map
// key normalized, so lookups are case-insensitive even when the caller
// (e.g. T4's HTTP body) uses the display casing from T2's unresolved
// inventory.
func normalizeResolutions(r PlaneImportResolutions) PlaneImportResolutions {
	out := PlaneImportResolutions{
		People:  make(map[string]PlanePersonResolution, len(r.People)),
		States:  make(map[string]PlaneStateResolution, len(r.States)),
		Labels:  make(map[string]PlaneLabelResolution, len(r.Labels)),
		Cycles:  make(map[string]PlaneCycleResolution, len(r.Cycles)),
		Modules: make(map[string]PlaneModuleResolution, len(r.Modules)),
	}
	for k, v := range r.People {
		out.People[planeResolutionKey(k)] = v
	}
	for k, v := range r.States {
		out.States[planeResolutionKey(k)] = v
	}
	for k, v := range r.Labels {
		out.Labels[planeResolutionKey(k)] = v
	}
	for k, v := range r.Cycles {
		out.Cycles[planeResolutionKey(k)] = v
	}
	for k, v := range r.Modules {
		out.Modules[planeResolutionKey(k)] = v
	}
	return out
}

// planeValidatedRow is one row after Phase 1: every transform applied,
// failures collected separately so a bad row never aborts the others.
type planeValidatedRow struct {
	row             PlaneIssueRow
	priority        int
	startDate       *string
	targetDate      *string
	createdAt       time.Time
	estimatePointID *string
	estimateGap     string // set when the estimate value matched no point
	parentIdent     string // well-formed parent identifier ("" = none)
	parentMalformed string // raw parent value when ill-formed (warning, not error)
	createdAtGap    string // set when created_at fell back to import time
	isDraft         bool
	archivedAt      *time.Time
}

// planeImportExecutor carries per-run state through both phases.
type planeImportExecutor struct {
	ctx         context.Context
	pool        *pgxpool.Pool
	projectID   string
	wsID        string
	actorID     string
	resolutions PlaneImportResolutions
	opts        PlaneImportExecuteOpts
	now         time.Time

	statesByName  map[string]string // lower(name) -> state id
	statesByID    map[string]string // state id -> name
	labelsByName  map[string]string
	labelsByID    map[string]string
	cyclesByName  map[string]string
	cyclesByID    map[string]string
	modulesByName map[string]string
	modulesByID   map[string]string
	memberIDs     map[string]bool // workspace member user ids
	scale         *planeEstimateScale

	report *PlaneImportReport
}

// ExecutePlaneImport imports a Plane export into the glance project
// projectID, on behalf of actorID. See the package doc above for the
// two-phase contract. actorID needs member (15)+ on the workspace —
// design decision 9, mirroring the CSV importer.
func ExecutePlaneImport(ctx context.Context, pool *pgxpool.Pool, projectID, actorID string,
	r io.Reader, resolutions PlaneImportResolutions, opts PlaneImportExecuteOpts) (*PlaneImportReport, error) {
	x := &planeImportExecutor{
		ctx: ctx, pool: pool, projectID: projectID, actorID: actorID,
		resolutions: normalizeResolutions(resolutions), opts: opts, now: time.Now().UTC(),
		report: &PlaneImportReport{Errors: []PlaneImportRowError{}, Gaps: []PlaneImportGap{}},
	}
	if r == nil {
		return nil, errors.New("service: plane import requires an input reader")
	}
	if err := x.authorize(); err != nil {
		return nil, err
	}
	parsed, err := ParsePlaneImport(r)
	if err != nil {
		return nil, err
	}
	x.report.ProjectIdentifier = parsed.ProjectIdentifier
	maxRows := opts.MaxRows
	if maxRows <= 0 {
		maxRows = planeImportMaxRows
	}
	if total := len(parsed.Rows) + len(parsed.Errors); total > maxRows {
		return nil, fmt.Errorf("service: plane export has %d rows, exceeding the soft cap of %d (raise max_rows to override)",
			total, maxRows)
	}
	// T1's per-row shape failures are already known-bad: surface them as
	// failed rows in this run's report rather than dropping them silently.
	for _, re := range parsed.Errors {
		x.failRow(re.Identifier, re.Message)
	}
	if err := x.loadLookups(); err != nil {
		return nil, err
	}
	if err := x.validateResolutions(); err != nil {
		return nil, err
	}
	valid := x.phase1(parsed.Rows)
	return x.phase2(valid)
}

// authorize resolves the project's workspace and gates membership:
// non-members get ErrNotFound (the tenancy boundary, like
// resolveIssueProject); members below RoleMember get ErrForbidden.
func (x *planeImportExecutor) authorize() error {
	if err := x.pool.QueryRow(x.ctx,
		`SELECT workspace_id::text FROM projects WHERE id = $1::uuid`,
		x.projectID).Scan(&x.wsID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return ErrProjectNotFound
		}
		return err
	}
	var role int
	if err := x.pool.QueryRow(x.ctx,
		`SELECT role FROM workspace_members WHERE workspace_id = $1::uuid AND user_id = $2::uuid`,
		x.wsID, x.actorID).Scan(&role); err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return ErrNotFound
		}
		return err
	}
	if role < RoleMember {
		return ErrForbidden
	}
	return nil
}

func (x *planeImportExecutor) failRow(identifier, message string) {
	if identifier == "" {
		identifier = "row-?"
	}
	x.report.Failed++
	x.report.Errors = append(x.report.Errors, PlaneImportRowError{
		Identifier: identifier,
		Message:    message,
	})
}

// loadLookups loads the reference data Phase 1/2 resolve against.
// Read-only.
func (x *planeImportExecutor) loadLookups() error {
	ctx := x.ctx
	nameMaps := func(label, q string, args ...any) (map[string]string, error) {
		m := map[string]string{}
		rows, err := x.pool.Query(ctx, q, args...)
		if err != nil {
			return nil, fmt.Errorf("service: plane import lookups (%s): %w", label, err)
		}
		defer rows.Close()
		for rows.Next() {
			var id, name string
			if err := rows.Scan(&id, &name); err != nil {
				return nil, fmt.Errorf("service: plane import lookups (%s): %w", label, err)
			}
			if n := planeResolutionKey(name); n != "" {
				if _, ok := m[n]; !ok {
					m[n] = id
				}
			}
		}
		return m, rows.Err()
	}
	var err error
	if x.statesByName, err = nameMaps("states",
		`SELECT id::text, name FROM states WHERE project_id = $1::uuid`, x.projectID); err != nil {
		return err
	}
	if x.labelsByName, err = nameMaps("labels",
		`SELECT id::text, name FROM labels WHERE workspace_id = $1::uuid`, x.wsID); err != nil {
		return err
	}
	if x.cyclesByName, err = nameMaps("cycles",
		`SELECT id::text, name FROM cycles WHERE project_id = $1::uuid`, x.projectID); err != nil {
		return err
	}
	if x.modulesByName, err = nameMaps("modules",
		`SELECT id::text, name FROM modules WHERE project_id = $1::uuid`, x.projectID); err != nil {
		return err
	}
	// Reverse maps for resolution validation (id -> name).
	rev := func(m map[string]string) map[string]string {
		r := make(map[string]string, len(m))
		for k, v := range m {
			r[v] = k
		}
		return r
	}
	x.statesByID = rev(x.statesByName)
	x.labelsByID = rev(x.labelsByName)
	x.cyclesByID = rev(x.cyclesByName)
	x.modulesByID = rev(x.modulesByName)

	x.memberIDs = map[string]bool{}
	mrows, err := x.pool.Query(ctx,
		`SELECT user_id::text FROM workspace_members WHERE workspace_id = $1::uuid`, x.wsID)
	if err != nil {
		return fmt.Errorf("service: plane import lookups (members): %w", err)
	}
	defer mrows.Close()
	for mrows.Next() {
		var id string
		if err := mrows.Scan(&id); err != nil {
			return fmt.Errorf("service: plane import lookups (members): %w", err)
		}
		x.memberIDs[strings.ToLower(id)] = true
	}
	if err := mrows.Err(); err != nil {
		return fmt.Errorf("service: plane import lookups (members): %w", err)
	}
	if x.scale, err = loadPlaneEstimateScale(ctx, x.pool, x.projectID); err != nil {
		return err
	}
	return nil
}

// validateResolutions rejects malformed user resolutions before any row
// is touched: these are caller errors (whole-run failures), not row data
// problems.
func (x *planeImportExecutor) validateResolutions() error {
	isMember := func(id string) bool {
		return x.memberIDs[strings.ToLower(strings.TrimSpace(id))]
	}
	for name, res := range x.resolutions.People {
		if strings.TrimSpace(res.MemberID) == "" {
			return fmt.Errorf("service: plane import resolution for person %q: member_id is required", name)
		}
		if !isMember(res.MemberID) {
			return fmt.Errorf("service: plane import resolution for person %q: %q is not a workspace member",
				name, res.MemberID)
		}
	}
	for name, res := range x.resolutions.States {
		if strings.TrimSpace(res.StateID) != "" {
			if _, ok := x.statesByID[strings.ToLower(strings.TrimSpace(res.StateID))]; !ok {
				return fmt.Errorf("service: plane import resolution for state %q: %q is not a state of this project",
					name, res.StateID)
			}
			continue
		}
		group := strings.ToLower(strings.TrimSpace(res.Group))
		if group == "" {
			continue // default backlog at ensure time
		}
		if !planeImportStateGroups[group] {
			return fmt.Errorf("service: plane import resolution for state %q: unknown group %q", name, res.Group)
		}
	}
	for name, res := range x.resolutions.Labels {
		if strings.TrimSpace(res.LabelID) == "" {
			continue
		}
		if _, ok := x.labelsByID[strings.ToLower(strings.TrimSpace(res.LabelID))]; !ok {
			return fmt.Errorf("service: plane import resolution for label %q: %q is not a label of this workspace",
				name, res.LabelID)
		}
	}
	for name, res := range x.resolutions.Cycles {
		if strings.TrimSpace(res.CycleID) != "" {
			if _, ok := x.cyclesByID[strings.ToLower(strings.TrimSpace(res.CycleID))]; !ok {
				return fmt.Errorf("service: plane import resolution for cycle %q: %q is not a cycle of this project",
					name, res.CycleID)
			}
			continue
		}
		if res.StartDate == "" && res.EndDate == "" {
			continue // documented defaults at ensure time
		}
		start, err := parsePlaneDate(res.StartDate)
		if err != nil {
			return fmt.Errorf("service: plane import resolution for cycle %q: bad start_date: %v", name, err)
		}
		end, err := parsePlaneDate(res.EndDate)
		if err != nil {
			return fmt.Errorf("service: plane import resolution for cycle %q: bad end_date: %v", name, err)
		}
		if end.Before(start) {
			return fmt.Errorf("service: plane import resolution for cycle %q: end_date %q is before start_date %q",
				name, res.EndDate, res.StartDate)
		}
	}
	for name, res := range x.resolutions.Modules {
		if strings.TrimSpace(res.ModuleID) == "" {
			continue
		}
		if _, ok := x.modulesByID[strings.ToLower(strings.TrimSpace(res.ModuleID))]; !ok {
			return fmt.Errorf("service: plane import resolution for module %q: %q is not a module of this project",
				name, res.ModuleID)
		}
	}
	return nil
}

// phase1 validates and transforms every row. No writes. Failures are
// recorded per row; valid rows come back in export order.
func (x *planeImportExecutor) phase1(rows []PlaneIssueRow) []*planeValidatedRow {
	valid := make([]*planeValidatedRow, 0, len(rows))
	for _, row := range rows {
		v, err := x.validateRow(row)
		if err != nil {
			x.failRow(row.Identifier, err.Error())
			continue
		}
		valid = append(valid, v)
	}
	return valid
}

func (x *planeImportExecutor) validateRow(row PlaneIssueRow) (*planeValidatedRow, error) {
	v := &planeValidatedRow{row: row, isDraft: row.IsDraft}

	priority, err := parsePlanePriority(row.Priority)
	if err != nil {
		return nil, err
	}
	v.priority = priority

	dateField := func(raw *string, field string) (*string, error) {
		if raw == nil || strings.TrimSpace(*raw) == "" {
			return nil, nil
		}
		t, err := parsePlaneDate(*raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", field, err)
		}
		s := t.Format("2006-01-02")
		return &s, nil
	}
	if v.startDate, err = dateField(row.StartDate, "start_date"); err != nil {
		return nil, err
	}
	if v.targetDate, err = dateField(row.TargetDate, "target_date"); err != nil {
		return nil, err
	}

	// Estimate: uninterpretable payloads fail the row; values that match
	// no point import unset with a gap entry (mirrors T2's warnings).
	display, kind := classifyPlaneEstimate(row.Estimate)
	if kind == planeEstimateOther {
		return nil, fmt.Errorf("estimate value %s is not a number or string", display)
	}
	if pointID, ok := x.scale.lookup(display, kind); ok {
		id := pointID
		v.estimatePointID = &id
	} else if kind != planeEstimateUnset {
		if x.scale.hasScale {
			v.estimateGap = fmt.Sprintf("estimate value %s matches no point in this project's estimate scales — left unset", display)
		} else {
			v.estimateGap = fmt.Sprintf("estimate value %s dropped — this project has no estimate scales", display)
		}
	}

	// Parent: well-formedness is a warning, never a row error.
	if p := strings.TrimSpace(row.Parent); p != "" {
		if _, _, err := ParsePlaneIdentifier(p); err != nil {
			v.parentMalformed = p
		} else {
			v.parentIdent = p
		}
	}

	// created_at is preserved; an unparseable value falls back to import
	// time with a gap note rather than failing the row.
	if t, err := parsePlaneTimestamp(row.CreatedAt); err != nil {
		v.createdAt = x.now
		v.createdAtGap = fmt.Sprintf("created_at %q is not a recognized timestamp — used import time", row.CreatedAt)
	} else {
		v.createdAt = t.UTC()
	}
	if row.ArchivedAt != nil && strings.TrimSpace(*row.ArchivedAt) != "" {
		if t, err := parsePlaneTimestamp(*row.ArchivedAt); err == nil {
			utc := t.UTC()
			v.archivedAt = &utc
		}
		// An unparseable archived_at is dropped silently: it is
		// metadata about a state the issue may not even keep.
	}
	return v, nil
}

// ---------- Phase 2: one transaction ----------

// phase2 inserts everything inside a single tx. valid rows are in export
// order; check-then-skip makes re-runs idempotent.
func (x *planeImportExecutor) phase2(valid []*planeValidatedRow) (*PlaneImportReport, error) {
	ctx := x.ctx
	tx, err := x.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("service: plane import begin: %w", err)
	}
	defer tx.Rollback(ctx) // no-op after Commit

	// Idempotency, durable and migration-free: an issue with this
	// (project_id, sequence_id) already present means this row was
	// imported by an earlier run — skip it. The in-tx set also dedupes
	// repeated identifiers inside one export (first wins).
	seenSeq := map[int]bool{}
	srows, err := tx.Query(ctx,
		`SELECT sequence_id FROM issues WHERE project_id = $1::uuid`, x.projectID)
	if err != nil {
		return nil, fmt.Errorf("service: plane import idempotency scan: %w", err)
	}
	for srows.Next() {
		var seq int
		if err := srows.Scan(&seq); err != nil {
			srows.Close()
			return nil, fmt.Errorf("service: plane import idempotency scan: %w", err)
		}
		seenSeq[seq] = true
	}
	srows.Close()
	if err := srows.Err(); err != nil {
		return nil, fmt.Errorf("service: plane import idempotency scan: %w", err)
	}

	// Ensure taxonomy first (states, then labels/cycles/modules), so issue
	// inserts can reference everything.
	stateIDs := map[string]string{} // lower(state name) -> state id
	dropped := map[string]bool{}    // identifiers rejected under strict_states
	for _, v := range valid {
		if seenSeq[v.row.SequenceID] {
			continue
		}
		name := strings.TrimSpace(v.row.StateName)
		if name == "" {
			continue
		}
		key := planeResolutionKey(name)
		if _, ok := stateIDs[key]; ok {
			continue
		}
		id, err := x.ensureState(ctx, tx, name)
		if err != nil {
			if errors.Is(err, errPlaneUnknownState) {
				x.failRow(v.row.Identifier, fmt.Sprintf(
					"unknown state %q: strict_states is on — add a state resolution or disable strict mode", name))
				dropped[v.row.Identifier] = true
				continue
			}
			return nil, fmt.Errorf("service: plane import ensure state: %w", err)
		}
		stateIDs[key] = id
	}
	// Rows rejected by strict_states never reach the insert loop.
	if len(dropped) > 0 {
		kept := valid[:0]
		for _, v := range valid {
			if !dropped[v.row.Identifier] {
				kept = append(kept, v)
			}
		}
		valid = kept
	}

	labelIDs := map[string]string{}
	cycleIDs := map[string]string{}
	moduleIDs := map[string]string{}
	for _, v := range valid {
		if seenSeq[v.row.SequenceID] {
			continue
		}
		for _, nm := range v.row.Labels {
			key := planeResolutionKey(nm)
			if key == "" || labelIDs[key] != "" {
				continue
			}
			id, err := x.ensureLabel(ctx, tx, nm)
			if err != nil {
				return nil, err
			}
			labelIDs[key] = id
		}
		for _, nm := range v.row.Cycles {
			key := planeResolutionKey(nm)
			if key == "" || cycleIDs[key] != "" {
				continue
			}
			id, err := x.ensureCycle(ctx, tx, nm)
			if err != nil {
				return nil, err
			}
			cycleIDs[key] = id
		}
		for _, nm := range v.row.Modules {
			key := planeResolutionKey(nm)
			if key == "" || moduleIDs[key] != "" {
				continue
			}
			id, err := x.ensureModule(ctx, tx, nm)
			if err != nil {
				return nil, err
			}
			moduleIDs[key] = id
		}
	}

	// Insert issues, preserving Plane sequence_id and created_at;
	// updated_at defaults to import time.
	identToID := map[string]string{} // Plane identifier -> new issue UUID
	maxSeq := 0
	for _, v := range valid {
		if seenSeq[v.row.SequenceID] {
			x.report.Skipped++
			continue
		}
		stateID := x.defaultStateID(stateIDs, v.row.StateName)
		var startDate, targetDate, estimatePointID, archivedAt any
		if v.startDate != nil {
			startDate = *v.startDate
		}
		if v.targetDate != nil {
			targetDate = *v.targetDate
		}
		if v.estimatePointID != nil {
			estimatePointID = *v.estimatePointID
		}
		if v.archivedAt != nil {
			archivedAt = *v.archivedAt
		}
		var issueID string
		err := tx.QueryRow(ctx,
			`INSERT INTO issues (project_id, sequence_id, name, description, priority,
				state_id, start_date, target_date, estimate_point_id, is_draft,
				archived_at, created_by, created_at)
			 VALUES ($1::uuid, $2, $3, NULL, $4, $5::uuid, $6, $7, $8::uuid, $9, $10, $11::uuid, $12)
			 RETURNING id::text`,
			x.projectID, v.row.SequenceID, v.row.Name, v.priority, stateID,
			startDate, targetDate, estimatePointID, v.isDraft, archivedAt,
			x.actorID, v.createdAt).Scan(&issueID)
		if err != nil {
			return nil, fmt.Errorf("service: plane import insert issue %s: %w", v.row.Identifier, err)
		}
		seenSeq[v.row.SequenceID] = true
		if _, dup := identToID[v.row.Identifier]; !dup {
			identToID[v.row.Identifier] = issueID
		}
		if v.row.SequenceID > maxSeq {
			maxSeq = v.row.SequenceID
		}
		x.insertSatellites(ctx, tx, v, issueID, labelIDs, cycleIDs, moduleIDs)
		x.report.Created++
		x.reportGaps(v)
	}

	// Advance the sequence counter past the max imported id inside the
	// same tx (the archive importer's counter-repair pattern): the next
	// natively created issue cannot collide with an imported display id.
	if maxSeq > 0 {
		if _, err := tx.Exec(ctx,
			`INSERT INTO issue_sequences (project_id, last_value)
			 VALUES ($1::uuid, $2)
			 ON CONFLICT (project_id) DO UPDATE
			 SET last_value = GREATEST(issue_sequences.last_value, EXCLUDED.last_value)`,
			x.projectID, maxSeq); err != nil {
			return nil, fmt.Errorf("service: plane import advance sequence: %w", err)
		}
	}

	// Second pass: wire parent_id via the identifier→UUID map. Child rows
	// may precede their parents in the export — the map is complete by
	// now. Dangling or self parents become root issues (gap, not failure).
	danglingParents := map[string]string{} // issue identifier -> parent identifier
	selfParents := map[string]bool{}
	for _, v := range valid {
		issueID, ok := identToID[v.row.Identifier]
		if !ok || v.parentIdent == "" {
			continue
		}
		parentID, ok := identToID[v.parentIdent]
		if !ok {
			danglingParents[v.row.Identifier] = v.parentIdent
			continue
		}
		if parentID == issueID {
			selfParents[v.row.Identifier] = true
			continue
		}
		if _, err := tx.Exec(ctx,
			`UPDATE issues SET parent_id = $2::uuid, updated_at = now() WHERE id = $1::uuid`,
			issueID, parentID); err != nil {
			return nil, fmt.Errorf("service: plane import wire parent %s: %w", v.row.Identifier, err)
		}
	}
	// The gap entries were written during the insert loop, before the
	// parent map was complete — append the parent losses now.
	for i := range x.report.Gaps {
		id := x.report.Gaps[i].Identifier
		if p, ok := danglingParents[id]; ok {
			x.report.Gaps[i].Losses = append(x.report.Gaps[i].Losses,
				fmt.Sprintf("parent %s not found in this import — imported as a root issue", p))
		}
		if selfParents[id] {
			x.report.Gaps[i].Losses = append(x.report.Gaps[i].Losses,
				"issue lists itself as its parent — imported as a root issue")
		}
	}

	// Import-run record keyed by (project_id, plane_project_identifier):
	// the audit trail for idempotency. The durable skip guard is the
	// sequence check above; this row documents the run. Re-running the
	// same export refreshes it.
	reportJSON, err := json.Marshal(x.report)
	if err != nil {
		return nil, fmt.Errorf("service: plane import marshal report: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO idempotency_keys (user_id, idem_key, endpoint, status, response_code, response_body)
		 VALUES ($1::uuid, $2, 'plane-import-execute', 'completed', 200, $3)
		 ON CONFLICT (user_id, idem_key) DO UPDATE
		 SET status = 'completed', response_code = 200, response_body = EXCLUDED.response_body,
		     created_at = now()`,
		x.actorID, "plane-import:"+x.projectID+":"+x.report.ProjectIdentifier, string(reportJSON)); err != nil {
		return nil, fmt.Errorf("service: plane import run record: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("service: plane import commit: %w", err)
	}
	x.sortReport()
	return x.report, nil
}

// defaultStateID resolves the issue's state: an ensured name → its id;
// a blank state_name → the project's "backlog" state, else any state
// (projects always seed states at creation, so a miss is impossible in
// practice).
func (x *planeImportExecutor) defaultStateID(stateIDs map[string]string, stateName string) string {
	if key := planeResolutionKey(stateName); key != "" {
		if id, ok := stateIDs[key]; ok {
			return id
		}
		if id, ok := x.statesByName[key]; ok {
			return id
		}
	}
	if id, ok := x.statesByName["backlog"]; ok {
		return id
	}
	for _, id := range x.statesByName {
		return id
	}
	return ""
}

// ensureState resolves a Plane state name to a glance state id, creating
// the state when nothing matches (design decision 6). Creation happens
// inside the import tx so a failed import never leaves orphan states.
// Under strict_states an unknown name returns errPlaneUnknownState — the
// caller turns it into a per-row error.
func (x *planeImportExecutor) ensureState(ctx context.Context, tx pgx.Tx, name string) (string, error) {
	key := planeResolutionKey(name)
	if id, ok := x.statesByName[key]; ok {
		return id, nil
	}
	if res, ok := x.resolutions.States[key]; ok && strings.TrimSpace(res.StateID) != "" {
		return strings.ToLower(strings.TrimSpace(res.StateID)), nil
	}
	if x.opts.StrictStates {
		// Unknown state under strict_states: not a creation, not a
		// whole-run failure — the caller records a per-row error.
		return "", errPlaneUnknownState
	}
	group := planeDefaultStateGroup
	if res, ok := x.resolutions.States[key]; ok && strings.TrimSpace(res.Group) != "" {
		group = strings.ToLower(strings.TrimSpace(res.Group))
	}
	display := strings.TrimSpace(name)
	var seq int
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(MAX(sequence),0)+10000 FROM states WHERE project_id = $1::uuid`,
		x.projectID).Scan(&seq); err != nil {
		return "", err
	}
	var id string
	if err := tx.QueryRow(ctx,
		`INSERT INTO states (project_id, name, "group", color, sequence)
		 VALUES ($1::uuid, $2, $3, '#6b7280', $4)
		 ON CONFLICT (project_id, name) DO NOTHING
		 RETURNING id::text`,
		x.projectID, display, group, seq).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Lost a race (or the name differs only by case from an
			// existing state): fall back to a lookup.
			if err := tx.QueryRow(ctx,
				`SELECT id::text FROM states WHERE project_id = $1::uuid AND lower(name) = $2`,
				x.projectID, key).Scan(&id); err != nil {
				return "", err
			}
		} else {
			return "", err
		}
	}
	x.statesByName[key] = id
	x.statesByID[strings.ToLower(id)] = display
	x.report.StatesCreated = append(x.report.StatesCreated,
		fmt.Sprintf("%s (group %s)", display, group))
	return id, nil
}

// errPlaneUnknownState is a sentinel: ensureState returns it (as a typed
// marker) when strict_states rejects an unknown state name.
var errPlaneUnknownState = errors.New("service: plane import: unknown state (strict_states)")

// ensureLabel resolves a Plane label name to a glance label id, creating
// the workspace label with the default color when missing.
func (x *planeImportExecutor) ensureLabel(ctx context.Context, tx pgx.Tx, name string) (string, error) {
	key := planeResolutionKey(name)
	if id, ok := x.labelsByName[key]; ok {
		return id, nil
	}
	if res, ok := x.resolutions.Labels[key]; ok && strings.TrimSpace(res.LabelID) != "" {
		return strings.ToLower(strings.TrimSpace(res.LabelID)), nil
	}
	display := strings.TrimSpace(name)
	var id string
	if err := tx.QueryRow(ctx,
		`INSERT INTO labels (workspace_id, name) VALUES ($1::uuid, $2)
		 ON CONFLICT (workspace_id, name) DO NOTHING
		 RETURNING id::text`,
		x.wsID, display).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			if err := tx.QueryRow(ctx,
				`SELECT id::text FROM labels WHERE workspace_id = $1::uuid AND lower(name) = $2`,
				x.wsID, key).Scan(&id); err != nil {
				return "", err
			}
		} else {
			return "", err
		}
	}
	x.labelsByName[key] = id
	x.labelsByID[strings.ToLower(id)] = display
	x.report.LabelsCreated = append(x.report.LabelsCreated, display)
	return id, nil
}

// ensureCycle resolves a Plane cycle name to a glance cycle id, creating
// it when missing. Creation needs dates: the resolution's, or the
// documented default window (today → +30 days, status "upcoming").
func (x *planeImportExecutor) ensureCycle(ctx context.Context, tx pgx.Tx, name string) (string, error) {
	key := planeResolutionKey(name)
	if id, ok := x.cyclesByName[key]; ok {
		return id, nil
	}
	if res, ok := x.resolutions.Cycles[key]; ok && strings.TrimSpace(res.CycleID) != "" {
		return strings.ToLower(strings.TrimSpace(res.CycleID)), nil
	}
	start := x.now.Format("2006-01-02")
	end := x.now.AddDate(0, 0, 30).Format("2006-01-02")
	if res, ok := x.resolutions.Cycles[key]; ok && res.StartDate != "" && res.EndDate != "" {
		start, end = res.StartDate, res.EndDate // validated in validateResolutions
	}
	display := strings.TrimSpace(name)
	var id string
	if err := tx.QueryRow(ctx,
		`INSERT INTO cycles (project_id, name, start_date, end_date)
		 VALUES ($1::uuid, $2, $3, $4)
		 ON CONFLICT (project_id, name) DO NOTHING
		 RETURNING id::text`,
		x.projectID, display, start, end).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			if err := tx.QueryRow(ctx,
				`SELECT id::text FROM cycles WHERE project_id = $1::uuid AND lower(name) = $2`,
				x.projectID, key).Scan(&id); err != nil {
				return "", err
			}
		} else {
			return "", err
		}
	}
	x.cyclesByName[key] = id
	x.cyclesByID[strings.ToLower(id)] = display
	x.report.CyclesCreated = append(x.report.CyclesCreated,
		fmt.Sprintf("%s (%s → %s)", display, start, end))
	return id, nil
}

// ensureModule resolves a Plane module name to a glance module id,
// creating it (status "active") when missing.
func (x *planeImportExecutor) ensureModule(ctx context.Context, tx pgx.Tx, name string) (string, error) {
	key := planeResolutionKey(name)
	if id, ok := x.modulesByName[key]; ok {
		return id, nil
	}
	if res, ok := x.resolutions.Modules[key]; ok && strings.TrimSpace(res.ModuleID) != "" {
		return strings.ToLower(strings.TrimSpace(res.ModuleID)), nil
	}
	display := strings.TrimSpace(name)
	var id string
	if err := tx.QueryRow(ctx,
		`INSERT INTO modules (project_id, name) VALUES ($1::uuid, $2)
		 ON CONFLICT (project_id, name) DO NOTHING
		 RETURNING id::text`,
		x.projectID, display).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			if err := tx.QueryRow(ctx,
				`SELECT id::text FROM modules WHERE project_id = $1::uuid AND lower(name) = $2`,
				x.projectID, key).Scan(&id); err != nil {
				return "", err
			}
		} else {
			return "", err
		}
	}
	x.modulesByName[key] = id
	x.modulesByID[strings.ToLower(id)] = display
	x.report.ModulesCreated = append(x.report.ModulesCreated, display)
	return id, nil
}

// personID maps a Plane display name to a member user id via the
// resolutions, or "" when unmapped. No silent guessing: unmapped names
// never auto-match workspace members (T2's analyze contract).
func (x *planeImportExecutor) personID(name string) string {
	if res, ok := x.resolutions.People[planeResolutionKey(name)]; ok {
		return strings.ToLower(strings.TrimSpace(res.MemberID))
	}
	return ""
}

// insertSatellites inserts labels, assignees, cycle/module memberships,
// comments, subscribers, and the _created activity row for one issue.
// Deliberately raw rows (like the Trello importer): a bulk import of
// foreign history must not fire mention parsing, watcher notifications,
// or webhook events.
func (x *planeImportExecutor) insertSatellites(ctx context.Context, tx pgx.Tx, v *planeValidatedRow,
	issueID string, labelIDs, cycleIDs, moduleIDs map[string]string) {
	row := v.row
	exec := func(q string, args ...any) {
		if _, err := tx.Exec(ctx, q, args...); err != nil {
			// Satellite writes are best-effort within the import: a
			// failure here must not lose the issue row itself. Record
			// it as a gap on the issue.
			x.report.Warnings = append(x.report.Warnings,
				fmt.Sprintf("%s: satellite write failed: %v", row.Identifier, err))
		}
	}
	for _, nm := range row.Labels {
		if id := labelIDs[planeResolutionKey(nm)]; id != "" {
			exec(`INSERT INTO issue_labels (issue_id, label_id) VALUES ($1::uuid, $2::uuid)
				ON CONFLICT DO NOTHING`, issueID, id)
		}
	}
	for _, nm := range row.Cycles {
		if id := cycleIDs[planeResolutionKey(nm)]; id != "" {
			exec(`INSERT INTO cycle_issues (cycle_id, issue_id) VALUES ($1::uuid, $2::uuid)
				ON CONFLICT DO NOTHING`, id, issueID)
		}
	}
	for _, nm := range row.Modules {
		if id := moduleIDs[planeResolutionKey(nm)]; id != "" {
			exec(`INSERT INTO module_issues (module_id, issue_id) VALUES ($1::uuid, $2::uuid)
				ON CONFLICT DO NOTHING`, id, issueID)
		}
	}
	for _, a := range row.Assignees {
		if id := x.personID(a); id != "" {
			exec(`INSERT INTO issue_assignees (issue_id, user_id) VALUES ($1::uuid, $2::uuid)
				ON CONFLICT DO NOTHING`, issueID, id)
		}
	}
	for _, s := range row.Subscribers {
		if id := x.personID(s); id != "" {
			exec(`INSERT INTO issue_subscribers (issue_id, user_id) VALUES ($1::uuid, $2::uuid)
				ON CONFLICT DO NOTHING`, issueID, id)
		}
	}
	for _, c := range row.Comments {
		body := stripPlaneHTML(c.Comment)
		if body == "" {
			continue
		}
		author := x.personID(c.CreatedBy)
		attributed := ""
		if author == "" {
			author = x.actorID
			who := strings.TrimSpace(c.CreatedBy)
			if who == "" {
				who = "unknown Plane user"
			}
			when := x.now.UTC().Format("2006-01-02 15:04 MST")
			if t, err := parsePlaneTimestamp(c.CreatedAt); err == nil {
				when = t.UTC().Format("2006-01-02 15:04 MST")
			}
			attributed = fmt.Sprintf("\n\n— originally posted by %s on %s", who, when)
		}
		stamp := x.now
		if t, err := parsePlaneTimestamp(c.CreatedAt); err == nil {
			stamp = t.UTC()
		}
		exec(`INSERT INTO comments (issue_id, actor_id, content, created_at)
			VALUES ($1::uuid, $2::uuid, $3::jsonb, $4)`,
			issueID, author, strconv.Quote(body+attributed), stamp)
	}
	newVal := fmt.Sprintf(`{"sequence_id":%d,"name":%s,"plane_identifier":%s}`,
		row.SequenceID, strconv.Quote(row.Name), strconv.Quote(row.Identifier))
	exec(`INSERT INTO issue_activities (issue_id, actor_id, field, new_value)
		VALUES ($1::uuid, $2::uuid, '_created', $3::jsonb)`,
		issueID, x.actorID, newVal)
}

// reportGaps builds one imported issue's loss documentation.
func (x *planeImportExecutor) reportGaps(v *planeValidatedRow) {
	row := v.row
	losses := []string{
		"description is never present in Plane exports — imported with an empty body",
	}
	// Relations: dropped (glance has no issue-to-issue relations), deduped
	// per row — an outgoing+incoming pair describing one edge becomes a
	// single gap entry.
	seenRel := map[string]bool{}
	for _, rel := range row.Relations {
		other := strings.TrimSpace(rel.Issue)
		if other == "" {
			continue
		}
		key := strings.ToLower(other) + "\x00" + strings.ToLower(strings.TrimSpace(rel.Type))
		if seenRel[key] {
			continue
		}
		seenRel[key] = true
		relType := strings.TrimSpace(rel.Type)
		if relType == "" {
			relType = "related"
		}
		losses = append(losses, fmt.Sprintf(
			"relation with %s (%s) dropped — glance stores no issue-to-issue relations", other, relType))
	}
	if row.AttachmentCount > 0 {
		losses = append(losses, fmt.Sprintf(
			"%d attachment(s) referenced but not imported — Plane exports carry attachment counts, not files",
			row.AttachmentCount))
	}
	for _, l := range row.Links {
		title := strings.TrimSpace(l.Title)
		if title == "" {
			title = l.URL
		}
		losses = append(losses, fmt.Sprintf(
			"link %q (%s) not imported — glance has no external-link storage", title, l.URL))
	}
	if v.estimateGap != "" {
		losses = append(losses, v.estimateGap)
	}
	if v.createdAtGap != "" {
		losses = append(losses, v.createdAtGap)
	}
	if v.parentMalformed != "" {
		losses = append(losses, fmt.Sprintf(
			"parent %q is not a well-formed Plane identifier — imported as a root issue", v.parentMalformed))
	}
	// Well-formed parents are wired in phase 2's second pass; dangling and
	// self parents are appended to the gap entry there, once the
	// identifier→UUID map is complete.
	for _, a := range row.Assignees {
		if strings.TrimSpace(a) != "" && x.personID(a) == "" {
			losses = append(losses, fmt.Sprintf(
				"assignee %q has no member mapping — issue left unassigned", strings.TrimSpace(a)))
		}
	}
	for _, c := range row.Comments {
		if strings.TrimSpace(c.CreatedBy) != "" && x.personID(c.CreatedBy) == "" {
			losses = append(losses, fmt.Sprintf(
				"comment by %q has no member mapping — attributed to the importing user",
				strings.TrimSpace(c.CreatedBy)))
		}
	}
	for _, s := range row.Subscribers {
		if strings.TrimSpace(s) != "" && x.personID(s) == "" {
			losses = append(losses, fmt.Sprintf(
				"subscriber %q has no member mapping — not subscribed", strings.TrimSpace(s)))
		}
	}
	x.report.Gaps = append(x.report.Gaps, PlaneImportGap{
		Identifier: row.Identifier,
		Losses:     losses,
	})
}

// sortReport orders errors, gaps, and created-entity lists deterministically.
func (x *planeImportExecutor) sortReport() {
	r := x.report
	sort.Slice(r.Errors, func(i, j int) bool { return r.Errors[i].Identifier < r.Errors[j].Identifier })
	sort.Slice(r.Gaps, func(i, j int) bool { return r.Gaps[i].Identifier < r.Gaps[j].Identifier })
	sort.Strings(r.StatesCreated)
	sort.Strings(r.LabelsCreated)
	sort.Strings(r.CyclesCreated)
	sort.Strings(r.ModulesCreated)
	sort.Strings(r.Warnings)
}
