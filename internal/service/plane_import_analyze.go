package service

// Plane-native export importer — T2: analysis / unresolved inventory
// (C14T2, cycle 14 "Plane-native import").
//
// AnalyzePlaneImport is the read-only first half of the two-phase
// importer (T4 will expose it over HTTP; T3 adds the execute phase on top
// of its output). It takes already-parsed rows plus the target glance
// project and resolves everything that can be resolved without user
// input:
//
//   - states, labels, cycles, modules: matched by name, case-insensitive;
//     anything unmatched is collected as unresolved (T3 creates missing
//     states/labels/cycles/modules per the user's resolutions — except
//     under strict_states, which is T3's concern, not analysis');
//   - estimate values: checked against the project's estimate scales (a
//     value matches when it equals a point's numeric value, or a
//     non-empty string key — Plane exports numbers or ""; mismatches and
//     missing scales surface as warnings);
//   - people (assignees, creators, comment authors, subscribers): ALWAYS
//     collected as unresolved. Plane exports display names only, so there
//     is nothing safe to match against — no silent guessing. A name that
//     collides with two or more workspace members' display names is
//     additionally flagged as ambiguous in the warnings.
//
// No writes happen here — every query is a SELECT. Duplicate Plane
// identifiers are tolerated at analysis (reported as a warning);
// de-duplication/idempotency is the execute step's job. Relations are
// counted and reported as drops, never inserted.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PlaneImportUnresolved is the inventory of names the import cannot map
// to existing glance entities without the user's explicit resolutions.
type PlaneImportUnresolved struct {
	People  []string `json:"people"`
	States  []string `json:"states"`
	Labels  []string `json:"labels"`
	Cycles  []string `json:"cycles"`
	Modules []string `json:"modules"`
}

// PlaneImportAnalysis is the read-only analysis of a parsed Plane export
// against a target glance project.
type PlaneImportAnalysis struct {
	ProjectIdentifier string                `json:"project_identifier"`
	IssueCount        int                   `json:"issue_count"`
	Unresolved        PlaneImportUnresolved `json:"unresolved"`
	Warnings          []string              `json:"warnings"`
}

// planeAnalysisLookups holds the reference data a target project offers
// for name matching. All maps are keyed by lowercased trimmed name.
type planeAnalysisLookups struct {
	states           map[string]bool
	labels           map[string]bool
	cycles           map[string]bool
	modules          map[string]bool
	memberNameCounts map[string]int // lower(display name) -> member count
	estimateKeys     map[string]bool
	estimateValues   map[int]bool
	hasEstimateScale bool
}

// loadPlaneAnalysisLookups loads the reference data for name matching.
// Read-only: five SELECTs, no writes.
func loadPlaneAnalysisLookups(ctx context.Context, pool *pgxpool.Pool, wsID, projectID string) (*planeAnalysisLookups, error) {
	lu := &planeAnalysisLookups{
		states:           map[string]bool{},
		labels:           map[string]bool{},
		cycles:           map[string]bool{},
		modules:          map[string]bool{},
		memberNameCounts: map[string]int{},
		estimateKeys:     map[string]bool{},
		estimateValues:   map[int]bool{},
	}
	nameQuery := func(label, q string, args ...any) (map[string]bool, error) {
		m := map[string]bool{}
		rows, err := pool.Query(ctx, q, args...)
		if err != nil {
			return nil, fmt.Errorf("service: analyze plane import (%s): %w", label, err)
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				return nil, fmt.Errorf("service: analyze plane import (%s): %w", label, err)
			}
			if n := strings.ToLower(strings.TrimSpace(name)); n != "" {
				m[n] = true
			}
		}
		return m, rows.Err()
	}
	var err error
	if lu.states, err = nameQuery("states",
		`SELECT name FROM states WHERE project_id = $1::uuid`, projectID); err != nil {
		return nil, err
	}
	if lu.labels, err = nameQuery("labels",
		`SELECT name FROM labels WHERE workspace_id = $1::uuid`, wsID); err != nil {
		return nil, err
	}
	if lu.cycles, err = nameQuery("cycles",
		`SELECT name FROM cycles WHERE project_id = $1::uuid`, projectID); err != nil {
		return nil, err
	}
	if lu.modules, err = nameQuery("modules",
		`SELECT name FROM modules WHERE project_id = $1::uuid`, projectID); err != nil {
		return nil, err
	}
	// Members by display name (u.name is nullable — email-only users are
	// not name-addressable and never enter the ambiguity check).
	mrows, err := pool.Query(ctx,
		`SELECT u.name FROM users u
		 JOIN workspace_members m ON m.user_id = u.id
		 WHERE m.workspace_id = $1::uuid`, wsID)
	if err != nil {
		return nil, fmt.Errorf("service: analyze plane import (members): %w", err)
	}
	for mrows.Next() {
		var name *string
		if err := mrows.Scan(&name); err != nil {
			mrows.Close()
			return nil, fmt.Errorf("service: analyze plane import (members): %w", err)
		}
		if name == nil {
			continue
		}
		if n := strings.ToLower(strings.TrimSpace(*name)); n != "" {
			lu.memberNameCounts[n]++
		}
	}
	mrows.Close()
	if err := mrows.Err(); err != nil {
		return nil, fmt.Errorf("service: analyze plane import (members): %w", err)
	}
	// Estimate points across ALL of the project's scales: glance has no
	// single "active" scale, so a value is fine when any scale covers it.
	erows, err := pool.Query(ctx,
		`SELECT ep.key, ep.value FROM estimate_points ep
		  JOIN estimates e ON e.id = ep.estimate_id
		 WHERE e.project_id = $1::uuid`, projectID)
	if err != nil {
		return nil, fmt.Errorf("service: analyze plane import (estimates): %w", err)
	}
	for erows.Next() {
		var key string
		var value int
		if err := erows.Scan(&key, &value); err != nil {
			erows.Close()
			return nil, fmt.Errorf("service: analyze plane import (estimates): %w", err)
		}
		lu.estimateKeys[key] = true
		lu.estimateValues[value] = true
		lu.hasEstimateScale = true
	}
	erows.Close()
	if err := erows.Err(); err != nil {
		return nil, fmt.Errorf("service: analyze plane import (estimates): %w", err)
	}
	return lu, nil
}

// planeEstimateKind classifies a Plane estimate payload.
type planeEstimateKind int

const (
	planeEstimateUnset  planeEstimateKind = iota // absent, null, or "" — nothing to import
	planeEstimateNumber                          // JSON number
	planeEstimateString                          // non-empty JSON string — a scale key
	planeEstimateOther                           // bool/array/object — uninterpretable
)

// classifyPlaneEstimate interprets a row's estimate RawMessage (Plane
// emits a number or an empty string in the same file). The display string
// is what analysis reports on.
func classifyPlaneEstimate(raw json.RawMessage) (display string, kind planeEstimateKind) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return "", planeEstimateUnset
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(bytes.TrimSpace(raw)), planeEstimateOther
	}
	switch t := v.(type) {
	case nil:
		return "", planeEstimateUnset
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), planeEstimateNumber
	case string:
		if strings.TrimSpace(t) == "" {
			return "", planeEstimateUnset
		}
		return strings.TrimSpace(t), planeEstimateString
	default:
		return string(bytes.TrimSpace(raw)), planeEstimateOther
	}
}

// AnalyzePlaneImport builds the unresolved inventory for a parsed Plane
// export against the target glance project. Member (15)+ to analyze
// (design decision 9 — CSV-importer conventions); guests and outsiders
// are rejected. No writes.
func AnalyzePlaneImport(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string, rows []PlaneIssueRow) (*PlaneImportAnalysis, error) {
	if len(rows) == 0 {
		return nil, ErrPlaneImportEmpty
	}
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
	lu, err := loadPlaneAnalysisLookups(ctx, pool, wsID, projectID)
	if err != nil {
		return nil, err
	}

	out := &PlaneImportAnalysis{
		ProjectIdentifier: rows[0].ProjectIdentifier,
		IssueCount:        len(rows),
		Unresolved: PlaneImportUnresolved{
			People: []string{}, States: []string{}, Labels: []string{},
			Cycles: []string{}, Modules: []string{},
		},
		Warnings: []string{},
	}

	// First-seen casing is kept for display; matching is case-insensitive.
	people := map[string]string{}
	states := map[string]string{}
	labels := map[string]string{}
	cycles := map[string]string{}
	modules := map[string]string{}
	collect := func(set map[string]string, name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if _, ok := set[strings.ToLower(name)]; !ok {
			set[strings.ToLower(name)] = name
		}
	}

	identifierCounts := map[string]int{}
	relationTotal := 0
	attachmentTotal := 0
	attachmentIssues := 0
	// estimate display -> occurrence count, per kind.
	numEstimates := map[string]int{}
	strEstimates := map[string]int{}
	otherEstimates := map[string]int{}
	setEstimates := 0

	for _, row := range rows {
		identifierCounts[row.Identifier]++
		relationTotal += len(row.Relations)
		if row.AttachmentCount > 0 {
			attachmentTotal += row.AttachmentCount
			attachmentIssues++
		}
		collect(states, row.StateName)
		for _, l := range row.Labels {
			collect(labels, l)
		}
		for _, c := range row.Cycles {
			collect(cycles, c)
		}
		for _, m := range row.Modules {
			collect(modules, m)
		}
		for _, a := range row.Assignees {
			collect(people, a)
		}
		collect(people, row.CreatedByName)
		for _, c := range row.Comments {
			collect(people, c.CreatedBy)
		}
		for _, s := range row.Subscribers {
			collect(people, s)
		}
		if display, kind := classifyPlaneEstimate(row.Estimate); kind != planeEstimateUnset {
			setEstimates++
			switch kind {
			case planeEstimateNumber:
				numEstimates[display]++
			case planeEstimateString:
				strEstimates[display]++
			case planeEstimateOther:
				otherEstimates[display]++
			}
		}
	}

	sortedVals := func(set map[string]string) []string {
		vals := make([]string, 0, len(set))
		for _, v := range set {
			vals = append(vals, v)
		}
		sort.SliceStable(vals, func(i, j int) bool {
			return strings.ToLower(vals[i]) < strings.ToLower(vals[j])
		})
		return vals
	}
	resolved := func(set map[string]string, known map[string]bool) []string {
		out := []string{}
		for lower, original := range set {
			if !known[lower] {
				out = append(out, original)
			}
		}
		sort.SliceStable(out, func(i, j int) bool {
			return strings.ToLower(out[i]) < strings.ToLower(out[j])
		})
		return out
	}

	out.Unresolved.People = sortedVals(people) // people are never resolved
	out.Unresolved.States = resolved(states, lu.states)
	out.Unresolved.Labels = resolved(labels, lu.labels)
	out.Unresolved.Cycles = resolved(cycles, lu.cycles)
	out.Unresolved.Modules = resolved(modules, lu.modules)

	// Warnings, in a fixed order: losses first, then mismatches.
	out.Warnings = append(out.Warnings,
		"descriptions are never present in Plane exports — imported issues will have no body")
	if relationTotal > 0 {
		out.Warnings = append(out.Warnings, fmt.Sprintf(
			"%d issue relation(s) will be dropped (glance has no issue-to-issue relations)", relationTotal))
	}
	if attachmentTotal > 0 {
		out.Warnings = append(out.Warnings, fmt.Sprintf(
			"%d attachment(s) referenced across %d issue(s) will be dropped (Plane exports carry attachment counts, not files)",
			attachmentTotal, attachmentIssues))
	}
	dups := []string{}
	for id, n := range identifierCounts {
		if n > 1 {
			dups = append(dups, fmt.Sprintf("%s appears %d times", id, n))
		}
	}
	if len(dups) > 0 {
		sort.Strings(dups)
		out.Warnings = append(out.Warnings, "duplicate Plane identifiers in the export (tolerated at analysis; the execute step de-duplicates): "+strings.Join(dups, ", "))
	}
	// Estimates: mismatch warnings only for set values that match nothing.
	sortedCounts := func(m map[string]int) []string {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return keys
	}
	if setEstimates > 0 && !lu.hasEstimateScale {
		out.Warnings = append(out.Warnings, fmt.Sprintf(
			"this project has no estimate scales — %d set estimate value(s) will be unset", setEstimates))
	} else {
		for _, display := range sortedCounts(numEstimates) {
			v, err := strconv.ParseFloat(display, 64)
			if err != nil || v != float64(int(v)) || !lu.estimateValues[int(v)] {
				out.Warnings = append(out.Warnings, fmt.Sprintf(
					"estimate value %s on %d issue(s) matches no point in this project's estimate scales",
					display, numEstimates[display]))
			}
		}
		for _, display := range sortedCounts(strEstimates) {
			if !lu.estimateKeys[display] {
				out.Warnings = append(out.Warnings, fmt.Sprintf(
					"estimate key %q on %d issue(s) matches no point in this project's estimate scales",
					display, strEstimates[display]))
			}
		}
		for _, display := range sortedCounts(otherEstimates) {
			out.Warnings = append(out.Warnings, fmt.Sprintf(
				"estimate value %s on %d issue(s) is not a number or string and will be unset",
				display, otherEstimates[display]))
		}
	}
	// Ambiguous people: names colliding with 2+ workspace members. Never
	// auto-resolved — flagged here so execute demands explicit mapping.
	for _, name := range out.Unresolved.People {
		if n := lu.memberNameCounts[strings.ToLower(name)]; n > 1 {
			out.Warnings = append(out.Warnings, fmt.Sprintf(
				"person name %q matches %d workspace members (ambiguous) — resolve it explicitly at execute; it will not be auto-matched",
				name, n))
		}
	}
	return out, nil
}
