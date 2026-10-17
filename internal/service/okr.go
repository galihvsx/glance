package service

// OKRs — Objectives + Key Results (C35T0 backend). Workspace-scoped
// objectives, each with measurable key results. Two KR kinds: metric
// (start → target → current, manual progress) and issue-linked
// (progress auto-derived from linked issues' done-group completion,
// same rollup semantics as the initiative rollup). Objective progress
// = mean of its KRs' progress. This is the strategic-planning tier
// above Initiatives (containers) and Releases (date-aligned
// milestones): it answers "are we hitting the goal?" with numbers.
//
// Schema: 000056 (objectives + key_results + key_result_issues) +
// 000060 (key_result_checkins: append-only metric-KR value history).
// Progress is COMPUTED, never stored.
//
// Conventions (mirror initiatives):
//   - Tenancy: every op resolves (workspaceID, role) via
//     resolveObjectiveWorkspace. A bad slug or non-member caller
//     surfaces ErrNotFound ("workspace not found") — the tenancy
//     boundary. A bad objective id surfaces ErrObjectiveNotFound; a
//     malformed one surfaces ErrInvalidObjectiveID (400, never a
//     ::uuid 500).
//   - Roles: any member (guest 5+) may read; mutations need member
//     (15)+. Any member may edit any objective (no author gate —
//     objectives are shared goals, like initiatives).
//   - PATCH tri-state for nullable fields: nil = untouched, "" = clear
//     (NULL), value = set. Status is a validated vocabulary.
//   - Duplicate objective title in a workspace → ErrObjectiveConflict
//     (409, mirror ErrInitiativeConflict).
//   - KR kind is immutable after create (PATCH with kind → 400);
//     target_value == start_value is rejected at write (a zero-span KR
//     can never move). Metric-only fields (unit, current_value) on an
//     issue KR → 400 on both create and PATCH.
//   - Issue links are idempotent (ON CONFLICT DO NOTHING); a linked
//     issue must belong to the same workspace (else ErrIssueNotFound);
//     linking to a metric KR → 400.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrObjectiveNotFound is returned when the objective id matches
	// nothing in the workspace. On this path the workspace is confirmed
	// and the caller is a member.
	ErrObjectiveNotFound = errors.New("service: objective not found")
	// ErrObjectiveConflict is returned when the objective title is
	// already taken in the workspace (UNIQUE(workspace_id, title)).
	ErrObjectiveConflict = errors.New("service: objective title already exists")
	// ErrInvalidObjective is returned for malformed objective input: a
	// bad status vocabulary, a bad date, or an unknown owner.
	ErrInvalidObjective = errors.New("service: invalid objective")
	// ErrInvalidObjectiveID is returned when an objective id, a key
	// result id, or an issue id passed to an OKR endpoint is not a
	// syntactically valid UUID. The handler maps it to 400 bad_request.
	ErrInvalidObjectiveID = errors.New("service: invalid objective id")
	// ErrKeyResultNotFound is returned when the key result id matches
	// nothing under the objective.
	ErrKeyResultNotFound = errors.New("service: key result not found")
	// ErrInvalidKeyResult is returned for malformed key result input: a
	// bad kind, a zero-span target (target == start), a kind change, a
	// metric-only field on an issue KR, or linking an issue to a
	// metric KR.
	ErrInvalidKeyResult = errors.New("service: invalid key result")
)

// objectiveStatuses is the status vocabulary enforced by the 000056
// CHECK. Every transition between values is allowed (mirror
// initiatives).
var objectiveStatuses = map[string]bool{
	"active":    true,
	"completed": true,
	"archived":  true,
}

// keyResultKinds is the KR kind vocabulary enforced by the 000056
// CHECK.
var keyResultKinds = map[string]bool{
	"metric": true,
	"issue":  true,
}

// Objective is one workspace-scoped goal. Progress is computed (mean
// of KR progress, 0 when no KRs) and KeyResultCount is live;
// KeyResults is populated only by GetObjective. ProgressHistory is
// the objective's progress-over-time series (one point per distinct
// event timestamp — a metric-KR check-in, an issue-KR link, or a
// linked issue's state transition — capped at the last 60 points),
// populated by ListObjectives and GetObjective; empty when no
// metric-KR check-in exists and no issue is linked.
type Objective struct {
	ID              string                   `json:"id"`
	WorkspaceID     string                   `json:"workspace_id"`
	Title           string                   `json:"title"`
	Description     *string                  `json:"description,omitempty"`
	Owner           *ActorRef                `json:"owner"`
	StartDate       *string                  `json:"start_date,omitempty"`
	TargetDate      *string                  `json:"target_date,omitempty"`
	Status          string                   `json:"status"`
	Progress        float64                  `json:"progress"`
	ProgressHistory []ObjectiveProgressPoint `json:"progress_history,omitempty"`
	KeyResultCount  int64                    `json:"key_result_count"`
	KeyResults      []KeyResult              `json:"key_results,omitempty"`
	CreatedAt       time.Time                `json:"created_at"`
	UpdatedAt       time.Time                `json:"updated_at"`
}

// ObjectiveProgressPoint is one point of an objective's
// progress-over-time series: the objective's computed progress
// (0..1, same mean-of-KRs formula as Progress) at timestamp T.
type ObjectiveProgressPoint struct {
	T        time.Time `json:"t"`
	Progress float64   `json:"progress"`
}

// KeyResult is one measurable result under an objective. Progress is
// computed: metric KRs clamp((current-start)/(target-start), 0, 1);
// issue KRs are done-group linked issues ÷ linked issues (0 when no
// links). LinkedIssues is populated only by GetObjective, for issue
// KRs.
type KeyResult struct {
	ID           string        `json:"id"`
	ObjectiveID  string        `json:"objective_id"`
	Title        string        `json:"title"`
	Kind         string        `json:"kind"`
	Unit         *string       `json:"unit,omitempty"`
	StartValue   float64       `json:"start_value"`
	TargetValue  float64       `json:"target_value"`
	CurrentValue float64       `json:"current_value"`
	Progress     float64       `json:"progress"`
	LinkedIssues []LinkedIssue `json:"linked_issues,omitempty"`
	CreatedAt    time.Time     `json:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at"`
}

// LinkedIssue is a live issue linked to an issue KR: enough for the
// detail UI to render it (id, display id, title, state group).
type LinkedIssue struct {
	ID         string `json:"id"`
	DisplayID  string `json:"display_id"`
	Title      string `json:"title"`
	StateGroup string `json:"state_group"`
}

// ObjectiveInput carries objective creation fields.
type ObjectiveInput struct {
	Title       string
	Description *string
	OwnerID     *string
	StartDate   *string // "YYYY-MM-DD", "" = NULL (same as nil)
	TargetDate  *string // "YYYY-MM-DD", "" = NULL (same as nil)
	Status      string  // "" = default "active"
}

// ObjectivePatch is a partial objective update: nil fields are
// untouched; a non-nil pointer to "" clears description / owner /
// dates (NULL).
type ObjectivePatch struct {
	Title       *string
	Description *string
	OwnerID     *string
	StartDate   *string
	TargetDate  *string
	Status      *string
}

// KeyResultInput carries key result creation fields.
type KeyResultInput struct {
	Title        string
	Kind         string
	Unit         *string
	StartValue   *float64 // nil = 0
	TargetValue  *float64 // required
	CurrentValue *float64 // nil = 0; metric KRs only
}

// KeyResultPatch is a partial key result update: nil fields are
// untouched; a non-nil Unit pointer to "" clears unit (NULL). Kind is
// immutable — a non-nil Kind is ErrInvalidKeyResult. CheckinNote is
// the optional note attached to the check-in row recorded when
// CurrentValue changes (trimmed, empty → NULL, max 1000 chars —
// longer is ErrInvalidKeyResult); it is ignored when no check-in row
// is recorded (no value change, title/unit/target-only change).
type KeyResultPatch struct {
	Title        *string
	Unit         *string
	TargetValue  *float64
	CurrentValue *float64
	Kind         *string
	CheckinNote  *string
}

// maxCheckinNoteLen is the check-in note cap in characters (runes).
const maxCheckinNoteLen = 1000

// CheckinAuthor is the author of a check-in entry: id + display name
// (users.name falling back to users.email — the same display-name
// rule as the workload report). This is NOT the ActorRef shape used
// by issue comments (id/name/email): the frozen C40 API contract
// specifies {id, display_name} for check-in authors.
type CheckinAuthor struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

// KeyResultCheckin is one append-only check-in entry: a metric KR's
// current_value moving from PreviousValue to NewValue, with the
// actor who made the change and an optional note. PreviousValue is
// NULL on the seed entry written at KR creation (there is no
// previous value yet). Author is nil when the author's user row was
// deleted (ON DELETE SET NULL). There is no update/delete path.
type KeyResultCheckin struct {
	ID            string         `json:"id"`
	KeyResultID   string         `json:"key_result_id"`
	Author        *CheckinAuthor `json:"author"`
	PreviousValue *float64       `json:"previous_value"`
	NewValue      float64        `json:"new_value"`
	Note          *string        `json:"note"`
	CreatedAt     time.Time      `json:"created_at"`
}

// normalizeCheckinNote trims a check-in note, mapping empty /
// whitespace-only to NULL, and rejects notes over the cap.
func normalizeCheckinNote(note *string) (*string, error) {
	if note == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*note)
	if trimmed == "" {
		return nil, nil
	}
	if utf8.RuneCountInString(trimmed) > maxCheckinNoteLen {
		return nil, ErrInvalidKeyResult
	}
	return &trimmed, nil
}

// resolveObjectiveWorkspace resolves (workspaceID, role) for the
// caller. Guests may read; callers pass needMember to enforce member
// (15)+. Non-members (and bad slugs) surface ErrNotFound — the
// tenancy boundary.
func resolveObjectiveWorkspace(ctx context.Context, q queryRower, wsSlug, actorID string, needMember bool) (wsID string, role int, err error) {
	wsID, role, err = workspaceIDForActor(q.QueryRow(ctx,
		`SELECT w.id::text, m.role
		 FROM workspaces w
		 JOIN workspace_members m ON m.workspace_id = w.id
		 WHERE w.slug = $1 AND m.user_id = $2::uuid`,
		wsSlug, actorID))
	if err != nil {
		return "", 0, err
	}
	if needMember && role < RoleMember {
		return "", 0, ErrForbidden
	}
	return wsID, role, nil
}

const objectiveColumns = `o.id::text, o.workspace_id::text, o.title, o.description,
	o.owner_id::text, ow.name, ow.email,
	o.start_date::text, o.target_date::text, o.status,
	o.created_at, o.updated_at`

const objectiveColumnsCounted = objectiveColumns + `,
	(SELECT COUNT(*) FROM key_results kr WHERE kr.objective_id = o.id)`

func scanObjectiveCounted(row pgx.Row) (*Objective, error) {
	var ob Objective
	var ownerID *string
	var name *string
	var email *string
	err := row.Scan(
		&ob.ID, &ob.WorkspaceID, &ob.Title, &ob.Description,
		&ownerID, &name, &email,
		&ob.StartDate, &ob.TargetDate, &ob.Status,
		&ob.CreatedAt, &ob.UpdatedAt, &ob.KeyResultCount)
	if err != nil {
		return nil, err
	}
	if ownerID != nil {
		ob.Owner = &ActorRef{ID: *ownerID, Name: name}
		if email != nil {
			ob.Owner.Email = *email
		}
	}
	return &ob, nil
}

// resolveObjective loads an objective of the workspace. Malformed ids
// match nothing — 400, not a 500 from the ::uuid cast.
func resolveObjective(ctx context.Context, q queryRower, wsID, objectiveID string) (*Objective, error) {
	if !isUUIDFormat(objectiveID) {
		return nil, ErrInvalidObjectiveID
	}
	ob, err := scanObjectiveCounted(q.QueryRow(ctx,
		`SELECT `+objectiveColumnsCounted+`
		 FROM objectives o LEFT JOIN users ow ON ow.id = o.owner_id
		 WHERE o.id = $1::uuid AND o.workspace_id = $2::uuid`,
		objectiveID, wsID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrObjectiveNotFound
		}
		return nil, err
	}
	return ob, nil
}

const keyResultColumns = `kr.id::text, kr.objective_id::text, kr.title, kr.kind, kr.unit,
	kr.start_value::float8, kr.target_value::float8, kr.current_value::float8,
	kr.created_at, kr.updated_at`

func scanKeyResult(row pgx.Row) (*KeyResult, error) {
	var kr KeyResult
	err := row.Scan(
		&kr.ID, &kr.ObjectiveID, &kr.Title, &kr.Kind, &kr.Unit,
		&kr.StartValue, &kr.TargetValue, &kr.CurrentValue,
		&kr.CreatedAt, &kr.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &kr, nil
}

// resolveKeyResult loads a key result under the objective. Malformed
// ids are 400; unknown ids (or a KR of another objective) are
// ErrKeyResultNotFound.
func resolveKeyResult(ctx context.Context, q queryRower, objectiveID, krID string) (*KeyResult, error) {
	if !isUUIDFormat(krID) {
		return nil, ErrInvalidObjectiveID
	}
	kr, err := scanKeyResult(q.QueryRow(ctx,
		`SELECT `+keyResultColumns+`
		 FROM key_results kr
		 WHERE kr.id = $1::uuid AND kr.objective_id = $2::uuid`,
		krID, objectiveID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrKeyResultNotFound
		}
		return nil, err
	}
	return kr, nil
}

// validateObjectiveDate checks a "YYYY-MM-DD" date; "" means NULL and
// passes through as nil.
func validateObjectiveDate(s *string) (*string, error) {
	if s == nil {
		return nil, nil
	}
	if d := strings.TrimSpace(*s); d != "" {
		if _, err := time.Parse("2006-01-02", d); err != nil {
			return nil, ErrInvalidObjective
		}
		return &d, nil
	}
	return nil, nil
}

// validateOwnerID resolves an owner id to a user id: "" means NULL,
// a malformed id or an unknown user is ErrInvalidObjective (400 —
// never a foreign-key 500). Owners need not be workspace members:
// the plan constrains membership only on the caller, not the owner.
func validateOwnerID(ctx context.Context, q queryRower, ownerID *string) (*string, error) {
	if ownerID == nil {
		return nil, nil
	}
	id := strings.TrimSpace(*ownerID)
	if id == "" {
		return nil, nil
	}
	if !isUUIDFormat(id) {
		return nil, ErrInvalidObjective
	}
	var exists bool
	err := q.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM users WHERE id = $1::uuid)`, id).Scan(&exists)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrInvalidObjective
	}
	return &id, nil
}

// metricKRProgress clamps (current-start)/(target-start) to [0, 1].
// A zero span (target == start) is rejected at write; the 0 fallback
// defends against legacy rows that ever violate it.
func metricKRProgress(start, target, current float64) float64 {
	if target == start {
		return 0
	}
	p := (current - start) / (target - start)
	if p < 0 {
		return 0
	}
	if p > 1 {
		return 1
	}
	return p
}

// krProgressEntry is one KR's computed progress plus its objective,
// for assembling objective means.
type krProgressEntry struct {
	ObjectiveID string
	Progress    float64
}

// queryKRProgress computes progress for key results in scope: either
// one objective (objectiveID != "") or a whole workspace (wsID !=
// ""). Issue-KR counts count only live (non-deleted) linked issues;
// done = linked issues in a done state group (completed/cancelled —
// doneStateGroups, the same predicate as the initiative rollup).
func queryKRProgress(ctx context.Context, q *pgxpool.Pool, objectiveID, wsID string) (map[string]krProgressEntry, error) {
	done := doneStateGroups
	var rows pgx.Rows
	var err error
	if objectiveID != "" {
		rows, err = q.Query(ctx,
			`SELECT kr.id::text, kr.objective_id::text, kr.kind,
				kr.start_value::float8, kr.target_value::float8, kr.current_value::float8,
				COUNT(i.id) FILTER (WHERE i.id IS NOT NULL) AS total,
				COUNT(i.id) FILTER (WHERE s."group" = ANY($2::text[])) AS done
			 FROM key_results kr
			 LEFT JOIN key_result_issues kri ON kri.key_result_id = kr.id
			 LEFT JOIN issues i ON i.id = kri.issue_id AND i.deleted_at IS NULL
			 LEFT JOIN states s ON s.id = i.state_id
			 WHERE kr.objective_id = $1::uuid
			 GROUP BY kr.id, kr.objective_id, kr.kind,
				kr.start_value, kr.target_value, kr.current_value`,
			objectiveID, done)
	} else {
		rows, err = q.Query(ctx,
			`SELECT kr.id::text, kr.objective_id::text, kr.kind,
				kr.start_value::float8, kr.target_value::float8, kr.current_value::float8,
				COUNT(i.id) FILTER (WHERE i.id IS NOT NULL) AS total,
				COUNT(i.id) FILTER (WHERE s."group" = ANY($2::text[])) AS done
			 FROM key_results kr
			 JOIN objectives o ON o.id = kr.objective_id
			 LEFT JOIN key_result_issues kri ON kri.key_result_id = kr.id
			 LEFT JOIN issues i ON i.id = kri.issue_id AND i.deleted_at IS NULL
			 LEFT JOIN states s ON s.id = i.state_id
			 WHERE o.workspace_id = $1::uuid
			 GROUP BY kr.id, kr.objective_id, kr.kind,
				kr.start_value, kr.target_value, kr.current_value`,
			wsID, done)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]krProgressEntry{}
	for rows.Next() {
		var krID, obID, kind string
		var start, target, current float64
		var total, doneCount int64
		if err := rows.Scan(&krID, &obID, &kind, &start, &target, &current, &total, &doneCount); err != nil {
			return nil, err
		}
		var p float64
		switch kind {
		case "metric":
			p = metricKRProgress(start, target, current)
		case "issue":
			if total > 0 {
				p = float64(doneCount) / float64(total)
			}
		}
		out[krID] = krProgressEntry{ObjectiveID: obID, Progress: p}
	}
	return out, rows.Err()
}

// fillObjectiveProgress sets Progress on objectives from a KR
// progress map: mean of KR progress, 0 when no KRs.
func fillObjectiveProgress(objs []*Objective, entries map[string]krProgressEntry) {
	sums := map[string]float64{}
	counts := map[string]int{}
	for _, e := range entries {
		sums[e.ObjectiveID] += e.Progress
		counts[e.ObjectiveID]++
	}
	for _, ob := range objs {
		if n := counts[ob.ID]; n > 0 {
			ob.Progress = sums[ob.ID] / float64(n)
		} else {
			ob.Progress = 0
		}
	}
}

// historyKR is one key result's replay input for
// buildObjectiveProgressHistory: its kind, metric span, current
// value, current issue-KR progress, and creation time.
type historyKR struct {
	ID            string
	Kind          string
	StartValue    float64
	TargetValue   float64
	CurrentValue  float64
	IssueProgress float64 // current progress for issue KRs; unused for metric
	CreatedAt     time.Time
}

// historyCheckin is one metric-KR check-in row's replay input.
// PreviousValue is nil on the seed row (value at creation).
type historyCheckin struct {
	KeyResultID   string
	PreviousValue *float64
	NewValue      float64
	CreatedAt     time.Time
}

// historyIssueTransition is one state_id activity row's replay
// input for one linked issue: the state the issue moved from and
// to, at the row's timestamp. Rows arrive ordered by
// (created_at, id) from the loader; the builder re-sorts
// defensively (stable) so caller order never matters.
type historyIssueTransition struct {
	At         time.Time
	OldStateID string // "" when old_value is NULL (never for state_id rows)
	NewStateID string // "" when new_value is NULL (never for state_id rows)
}

// historyIssueLink is one currently-linked issue's replay input
// for an issue KR: when the issue joined the KR, when the issue
// was created, its current state (the fallback when it has no
// state_id rows — it never changed state since creation), and its
// state_id activity rows.
type historyIssueLink struct {
	KeyResultID    string
	IssueID        string
	LinkCreatedAt  time.Time
	IssueCreatedAt time.Time
	CurrentStateID string
	Transitions    []historyIssueTransition
}

// effectiveStart is the first timestamp at which a link contributes
// to its KR: the link cannot count before the issue existed, before
// the KR existed, or before the link itself (mirrors the metric
// rule "a KR contributes only from its creation onward").
func (l historyIssueLink) effectiveStart(krCreatedAt time.Time) time.Time {
	start := l.LinkCreatedAt
	if krCreatedAt.After(start) {
		start = krCreatedAt
	}
	if l.IssueCreatedAt.After(start) {
		start = l.IssueCreatedAt
	}
	return start
}

// stateAt reconstructs the issue's state at timestamp T: the
// initial state is the earliest row's old_value (the state before
// the first recorded transition); with no rows the issue never
// changed state and its current state_id is the state at every T.
// Transitions with timestamp <= T are applied in order, so a
// pre-link transition shapes the state at link time without
// emitting its own point.
func (l historyIssueLink) stateAt(t time.Time) string {
	if len(l.Transitions) == 0 {
		return l.CurrentStateID
	}
	state := l.Transitions[0].OldStateID
	for _, tr := range l.Transitions {
		if tr.At.After(t) {
			break
		}
		state = tr.NewStateID
	}
	return state
}

// maxObjectiveHistoryPoints caps the progress_history payload: only
// the last 60 points are kept.
const maxObjectiveHistoryPoints = 60

// buildObjectiveProgressHistory replays one objective's metric-KR
// check-in rows chronologically into its progress-over-time series.
//
//   - A metric KR's value starts at its seed row's new_value (the
//     first previous-NULL row in chronological order) and each later
//     check-in sets that KR's value at its timestamp. A metric KR
//     with no seed row falls back to its current value.
//   - Issue KRs have no check-in rows; their contribution stays at
//     their current progress for the whole replay.
//   - A KR contributes only from its creation onward: the point at
//     timestamp T is the mean progress of KRs created at or before
//     T, using the same formula as fillObjectiveProgress (metric via
//     metricKRProgress, issue via current done/total, 0 when no KRs
//     exist at T).
//   - Check-ins sharing a timestamp collapse to a single point at
//     the post-event state (all of that timestamp's events applied
//     before the point is emitted).
//   - Check-in rows for KRs not in krs are skipped (orphan defense:
//     key_result_checkins.key_result_id is ON DELETE CASCADE, so
//     surviving rows normally all resolve — a deleted KR's rows are
//     gone and the replay over surviving rows equals current state).
//
// The input slices are not mutated. This is the legacy entry point:
// it delegates to buildObjectiveProgressHistoryWithIssues with no
// issue data, so issue KRs keep their constant-current-progress
// behavior and metric-only histories stay byte-identical.
func buildObjectiveProgressHistory(krs []historyKR, checkins []historyCheckin) []ObjectiveProgressPoint {
	return buildObjectiveProgressHistoryWithIssues(krs, checkins, nil, nil)
}

// buildObjectiveProgressHistoryWithIssues extends
// buildObjectiveProgressHistory with issue-KR state-transition
// replay. Metric-KR handling is unchanged; when issueLinks is
// non-nil, issue KRs are replayed from their links instead of
// holding their current progress constant:
//
//   - A link contributes only from its effectiveStart onward:
//     max(link created_at, KR created_at, issue created_at). An
//     issue KR's progress at T is done/total over links with
//     effectiveStart <= T, where an issue is done iff its
//     reconstructed state at T (historyIssueLink.stateAt) maps to a
//     done state in doneStates. A KR with no contributing links at
//     T scores 0 (same as the 0-links rule in fillObjectiveProgress),
//     so a link is never backdated to before it happened.
//   - State→done mapping is by state id against the states table
//     as loaded by the caller: a state_id with no surviving states
//     row (state deleted pre-C47T0) maps to NOT done — conservative,
//     progress is never over-claimed.
//   - Event timestamps are the union of metric check-in timestamps,
//     link effective-starts, and state transitions at/after their
//     link's effective start. Pre-link transitions emit no point.
//     Events sharing a timestamp collapse to a single point at the
//     post-event state (all of that timestamp's events applied
//     before the point is emitted).
//   - The last-60 cap still applies.
//
// The input slices (and the links' transition slices) are not
// mutated.
func buildObjectiveProgressHistoryWithIssues(krs []historyKR, checkins []historyCheckin, issueLinks map[string][]historyIssueLink, doneStates map[string]bool) []ObjectiveProgressPoint {
	type krState struct {
		kr    historyKR
		value float64 // metric KR's replayed current value
	}
	// Chronological replay order; stable so same-timestamp rows
	// keep caller order (the DB loader orders by created_at, id).
	ordered := make([]historyCheckin, len(checkins))
	copy(ordered, checkins)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].CreatedAt.Before(ordered[j].CreatedAt)
	})

	states := make([]*krState, 0, len(krs))
	byID := make(map[string]*krState, len(krs))
	for _, kr := range krs {
		st := &krState{kr: kr, value: kr.CurrentValue}
		// Seed value (value at creation) wins over the current
		// value as the replay's starting point.
		for _, ci := range ordered {
			if ci.KeyResultID == kr.ID && ci.PreviousValue == nil {
				st.value = ci.NewValue
				break
			}
		}
		states = append(states, st)
		byID[kr.ID] = st
	}

	// Normalized issue links: transition slices copied + sorted,
	// so stateAt's in-order walk is valid and caller slices are
	// never mutated.
	links := map[string][]historyIssueLink{}
	if issueLinks != nil {
		for krID, ls := range issueLinks {
			norm := make([]historyIssueLink, len(ls))
			copy(norm, ls)
			for i := range norm {
				trs := make([]historyIssueTransition, len(norm[i].Transitions))
				copy(trs, norm[i].Transitions)
				sort.SliceStable(trs, func(a, b int) bool {
					return trs[a].At.Before(trs[b].At)
				})
				norm[i].Transitions = trs
			}
			links[krID] = norm
		}
	}

	// Event timestamps: metric check-ins + issue link starts +
	// issue transitions at/after their link's start, deduplicated
	// by time equality (not map keys — equal instants in different
	// locations must still collapse).
	var events []time.Time
	for _, ci := range ordered {
		events = append(events, ci.CreatedAt)
	}
	if issueLinks != nil {
		for _, st := range states {
			if st.kr.Kind != "issue" {
				continue
			}
			for _, l := range links[st.kr.ID] {
				start := l.effectiveStart(st.kr.CreatedAt)
				events = append(events, start)
				for _, tr := range l.Transitions {
					if !tr.At.Before(start) {
						events = append(events, tr.At)
					}
				}
			}
		}
	}
	sort.Slice(events, func(i, j int) bool {
		return events[i].Before(events[j])
	})
	uniqEvents := events[:0]
	for _, t := range events {
		if len(uniqEvents) > 0 && uniqEvents[len(uniqEvents)-1].Equal(t) {
			continue
		}
		uniqEvents = append(uniqEvents, t)
	}

	out := []ObjectiveProgressPoint{}
	ciIdx := 0
	for _, t := range uniqEvents {
		// Apply every check-in at or before this timestamp
		// before emitting (a check-in between two issue events
		// is already in effect at the later event). Each
		// check-in is applied exactly once: ciIdx only moves
		// forward and every check-in timestamp is itself an
		// event, so metric-only inputs behave exactly as the
		// legacy builder (apply the timestamp's check-ins,
		// then emit).
		for ; ciIdx < len(ordered) && !ordered[ciIdx].CreatedAt.After(t); ciIdx++ {
			if st, ok := byID[ordered[ciIdx].KeyResultID]; ok {
				st.value = ordered[ciIdx].NewValue
			}
		}
		var sum float64
		var n int
		for _, st := range states {
			if st.kr.CreatedAt.After(t) {
				continue
			}
			var p float64
			switch st.kr.Kind {
			case "metric":
				p = metricKRProgress(st.kr.StartValue, st.kr.TargetValue, st.value)
			case "issue":
				if issueLinks == nil {
					p = st.kr.IssueProgress
				} else {
					var done, total int64
					for _, l := range links[st.kr.ID] {
						if l.effectiveStart(st.kr.CreatedAt).After(t) {
							continue
						}
						total++
						if doneStates[l.stateAt(t)] {
							done++
						}
					}
					if total > 0 {
						p = float64(done) / float64(total)
					}
				}
			}
			sum += p
			n++
		}
		progress := 0.0
		if n > 0 {
			progress = sum / float64(n)
		}
		out = append(out, ObjectiveProgressPoint{T: t, Progress: progress})
	}
	if len(out) > maxObjectiveHistoryPoints {
		out = out[len(out)-maxObjectiveHistoryPoints:]
	}
	return out
}

// loadIssueTransitions returns every state_id activity row for the
// issues linked to the given objectives, grouped by issue id. Old/
// new are JSONB scalars; #>> '{}' extracts the bare state id (the
// analytics burndown precedent). NULL old/new (never for state_id
// rows) decode as "" — the replay treats an unknown state as NOT
// done, never over-claiming. Rows arrive ordered by (issue_id,
// created_at, id).
//
// DISTINCT (C48T0): the join to key_result_issues fans each
// activity row out once per KR linking its issue. DISTINCT
// collapses those copies back to one row. ia.id is selected only
// so DISTINCT keeps genuinely distinct rows apart (two transitions
// can share issue/old/new/timestamp) and so ORDER BY can tie-break
// on it; its value is unused. ORDER BY uses select-list ordinals
// because DISTINCT requires ORDER BY expressions to appear in the
// select list.
func loadIssueTransitions(ctx context.Context, pool *pgxpool.Pool, objectiveIDs []string) (map[string][]historyIssueTransition, error) {
	transByIssue := map[string][]historyIssueTransition{}
	trRows, err := pool.Query(ctx,
		`SELECT DISTINCT ia.issue_id::text,
			ia.old_value #>> '{}', ia.new_value #>> '{}', ia.created_at, ia.id::text
		 FROM issue_activities ia
		 JOIN key_result_issues kri ON kri.issue_id = ia.issue_id
		 JOIN key_results kr ON kr.id = kri.key_result_id
		 JOIN issues i ON i.id = ia.issue_id AND i.deleted_at IS NULL
		 WHERE kr.objective_id::text = ANY($1::text[]) AND ia.field = 'state_id'
		 ORDER BY 1, 4, 5`,
		objectiveIDs)
	if err != nil {
		return nil, err
	}
	for trRows.Next() {
		var issueID string
		var tr historyIssueTransition
		var oldVal, newVal *string
		var activityID string // selected for DISTINCT/ORDER BY only, unused
		if err := trRows.Scan(&issueID, &oldVal, &newVal, &tr.At, &activityID); err != nil {
			trRows.Close()
			return nil, err
		}
		_ = activityID
		if oldVal != nil {
			tr.OldStateID = *oldVal
		}
		if newVal != nil {
			tr.NewStateID = *newVal
		}
		transByIssue[issueID] = append(transByIssue[issueID], tr)
	}
	if err := trRows.Err(); err != nil {
		trRows.Close()
		return nil, err
	}
	trRows.Close()
	return transByIssue, nil
}

// fillObjectiveProgressHistory populates ProgressHistory on each
// objective from its metric-KR check-in rows plus its issue-KR
// link/state-transition replay, in a fixed handful of queries
// (no per-objective N+1): one for the objectives' KRs with current
// issue-KR progress, one for their check-in rows, one for their
// currently-linked issues (link + issue created_at, current
// state), one for those issues' state_id activity rows, and one
// for the done-group membership of every state id the replay can
// observe.
func fillObjectiveProgressHistory(ctx context.Context, pool *pgxpool.Pool, objs []*Objective) error {
	if len(objs) == 0 {
		return nil
	}
	ids := make([]string, 0, len(objs))
	byID := make(map[string]*Objective, len(objs))
	for _, ob := range objs {
		ids = append(ids, ob.ID)
		byID[ob.ID] = ob
	}
	done := doneStateGroups
	rows, err := pool.Query(ctx,
		`SELECT kr.id::text, kr.objective_id::text, kr.kind,
			kr.start_value::float8, kr.target_value::float8, kr.current_value::float8,
			kr.created_at,
			COUNT(i.id) FILTER (WHERE i.id IS NOT NULL) AS total,
			COUNT(i.id) FILTER (WHERE s."group" = ANY($2::text[])) AS done
		 FROM key_results kr
		 LEFT JOIN key_result_issues kri ON kri.key_result_id = kr.id
		 LEFT JOIN issues i ON i.id = kri.issue_id AND i.deleted_at IS NULL
		 LEFT JOIN states s ON s.id = i.state_id
		 WHERE kr.objective_id::text = ANY($1::text[])
		 GROUP BY kr.id, kr.objective_id, kr.kind,
			kr.start_value, kr.target_value, kr.current_value, kr.created_at`,
		ids, done)
	if err != nil {
		return err
	}
	krsByObjective := map[string][]historyKR{}
	for rows.Next() {
		var kr historyKR
		var obID string
		var total, doneCount int64
		if err := rows.Scan(&kr.ID, &obID, &kr.Kind, &kr.StartValue,
			&kr.TargetValue, &kr.CurrentValue, &kr.CreatedAt,
			&total, &doneCount); err != nil {
			rows.Close()
			return err
		}
		if kr.Kind == "issue" && total > 0 {
			kr.IssueProgress = float64(doneCount) / float64(total)
		}
		krsByObjective[obID] = append(krsByObjective[obID], kr)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	ciRows, err := pool.Query(ctx,
		`SELECT ci.key_result_id::text, ci.previous_value::float8,
			ci.new_value::float8, ci.created_at, kr.objective_id::text
		 FROM key_result_checkins ci
		 JOIN key_results kr ON kr.id = ci.key_result_id
		 WHERE kr.objective_id::text = ANY($1::text[])
		 ORDER BY ci.created_at, ci.id`,
		ids)
	if err != nil {
		return err
	}
	defer ciRows.Close()
	checkinsByObjective := map[string][]historyCheckin{}
	for ciRows.Next() {
		var ci historyCheckin
		var obID string
		if err := ciRows.Scan(&ci.KeyResultID, &ci.PreviousValue,
			&ci.NewValue, &ci.CreatedAt, &obID); err != nil {
			return err
		}
		checkinsByObjective[obID] = append(checkinsByObjective[obID], ci)
	}
	if err := ciRows.Err(); err != nil {
		return err
	}
	ciRows.Close()

	// Issue-KR replay inputs, batched: currently-linked issues
	// (link + issue created_at, current state) grouped by
	// objective → KR.
	linksByObjective := map[string]map[string][]historyIssueLink{}
	linkRows, err := pool.Query(ctx,
		`SELECT kr.objective_id::text, kri.key_result_id::text,
			kri.issue_id::text, kri.created_at, i.created_at, i.state_id::text
		 FROM key_result_issues kri
		 JOIN key_results kr ON kr.id = kri.key_result_id
		 JOIN issues i ON i.id = kri.issue_id AND i.deleted_at IS NULL
		 WHERE kr.objective_id::text = ANY($1::text[])
		 ORDER BY kri.key_result_id, kri.created_at, kri.issue_id`,
		ids)
	if err != nil {
		return err
	}
	for linkRows.Next() {
		var obID string
		var link historyIssueLink
		if err := linkRows.Scan(&obID, &link.KeyResultID, &link.IssueID,
			&link.LinkCreatedAt, &link.IssueCreatedAt, &link.CurrentStateID); err != nil {
			linkRows.Close()
			return err
		}
		byKR := linksByObjective[obID]
		if byKR == nil {
			byKR = map[string][]historyIssueLink{}
			linksByObjective[obID] = byKR
		}
		byKR[link.KeyResultID] = append(byKR[link.KeyResultID], link)
	}
	if err := linkRows.Err(); err != nil {
		linkRows.Close()
		return err
	}
	linkRows.Close()

	// The linked issues' state_id activity rows, batched (see
	// loadIssueTransitions).
	transByIssue, err := loadIssueTransitions(ctx, pool, ids)
	if err != nil {
		return err
	}
	for _, byKR := range linksByObjective {
		for krID, ls := range byKR {
			for i := range ls {
				ls[i].Transitions = transByIssue[ls[i].IssueID]
			}
			byKR[krID] = ls
		}
	}

	// Done-group membership for every state id the replay can
	// observe (current states + transition endpoints). A state id
	// absent here has no surviving states row and maps to NOT done.
	doneStates := map[string]bool{}
	stateIDSet := map[string]bool{}
	for _, byKR := range linksByObjective {
		for _, ls := range byKR {
			for _, l := range ls {
				stateIDSet[l.CurrentStateID] = true
				for _, tr := range l.Transitions {
					if tr.OldStateID != "" {
						stateIDSet[tr.OldStateID] = true
					}
					if tr.NewStateID != "" {
						stateIDSet[tr.NewStateID] = true
					}
				}
			}
		}
	}
	if len(stateIDSet) > 0 {
		stateIDs := make([]string, 0, len(stateIDSet))
		for id := range stateIDSet {
			stateIDs = append(stateIDs, id)
		}
		doneRows, err := pool.Query(ctx,
			`SELECT id::text FROM states
			 WHERE id::text = ANY($1::text[]) AND "group" = ANY($2::text[])`,
			stateIDs, done)
		if err != nil {
			return err
		}
		for doneRows.Next() {
			var id string
			if err := doneRows.Scan(&id); err != nil {
				doneRows.Close()
				return err
			}
			doneStates[id] = true
		}
		if err := doneRows.Err(); err != nil {
			doneRows.Close()
			return err
		}
		doneRows.Close()
	}

	for _, ob := range objs {
		links := linksByObjective[ob.ID]
		if links == nil {
			// Non-nil empty map: the replay path (a zero-link
			// issue KR scores 0), not the legacy constant path.
			links = map[string][]historyIssueLink{}
		}
		ob.ProgressHistory = buildObjectiveProgressHistoryWithIssues(
			krsByObjective[ob.ID], checkinsByObjective[ob.ID], links, doneStates)
	}
	return nil
}

// loadKeyResults loads an objective's KRs ordered by creation with
// computed progress attached.
func loadKeyResults(ctx context.Context, q *pgxpool.Pool, objectiveID string) ([]KeyResult, error) {
	rows, err := q.Query(ctx,
		`SELECT `+keyResultColumns+`
		 FROM key_results kr WHERE kr.objective_id = $1::uuid
		 ORDER BY kr.created_at, kr.id`, objectiveID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []KeyResult{}
	for rows.Next() {
		kr, err := scanKeyResult(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *kr)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	entries, err := queryKRProgress(ctx, q, objectiveID, "")
	if err != nil {
		return nil, err
	}
	for i := range out {
		if e, ok := entries[out[i].ID]; ok {
			out[i].Progress = e.Progress
		}
	}
	return out, nil
}

// loadLinkedIssues loads live linked issues for an objective's issue
// KRs, grouped by KR id. Soft-deleted issues are excluded (they no
// longer count toward progress either).
func loadLinkedIssues(ctx context.Context, q *pgxpool.Pool, objectiveID string) (map[string][]LinkedIssue, error) {
	rows, err := q.Query(ctx,
		`SELECT kri.key_result_id::text, i.id::text,
			(p.identifier || '-' || i.sequence_id), i.name,
			COALESCE(s."group", '')
		 FROM key_result_issues kri
		 JOIN key_results kr ON kr.id = kri.key_result_id
		 JOIN issues i ON i.id = kri.issue_id AND i.deleted_at IS NULL
		 JOIN projects p ON p.id = i.project_id
		 LEFT JOIN states s ON s.id = i.state_id
		 WHERE kr.objective_id = $1::uuid
		 ORDER BY kri.key_result_id, (p.identifier || '-' || i.sequence_id)`,
		objectiveID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]LinkedIssue{}
	for rows.Next() {
		var krID string
		var li LinkedIssue
		if err := rows.Scan(&krID, &li.ID, &li.DisplayID, &li.Title, &li.StateGroup); err != nil {
			return nil, err
		}
		out[krID] = append(out[krID], li)
	}
	return out, rows.Err()
}

// normalizeObjectiveInput validates creation fields, returning the
// cleaned title, status, description ("" → NULL), owner ("" → NULL),
// and dates ("" → NULL).
func normalizeObjectiveInput(ctx context.Context, q queryRower, in ObjectiveInput) (title, status string, desc, owner, start, target *string, err error) {
	title = strings.TrimSpace(in.Title)
	if title == "" {
		return "", "", nil, nil, nil, nil, ErrNameRequired
	}
	status = in.Status
	if status == "" {
		status = "active"
	}
	if !objectiveStatuses[status] {
		return "", "", nil, nil, nil, nil, ErrInvalidObjective
	}
	if in.Description != nil {
		if d := strings.TrimSpace(*in.Description); d != "" {
			desc = &d
		}
	}
	if owner, err = validateOwnerID(ctx, q, in.OwnerID); err != nil {
		return "", "", nil, nil, nil, nil, err
	}
	if start, err = validateObjectiveDate(in.StartDate); err != nil {
		return "", "", nil, nil, nil, nil, err
	}
	if target, err = validateObjectiveDate(in.TargetDate); err != nil {
		return "", "", nil, nil, nil, nil, err
	}
	return title, status, desc, owner, start, target, nil
}

// CreateObjective creates an objective in the workspace. Member (15)+.
func CreateObjective(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID string, in ObjectiveInput) (*Objective, error) {
	wsID, _, err := resolveObjectiveWorkspace(ctx, pool, wsSlug, actorID, true)
	if err != nil {
		return nil, err
	}
	title, status, desc, owner, start, target, err := normalizeObjectiveInput(ctx, pool, in)
	if err != nil {
		return nil, err
	}
	var id string
	err = pool.QueryRow(ctx,
		`INSERT INTO objectives (workspace_id, title, description, owner_id, start_date, target_date, status)
		 VALUES ($1::uuid, $2, $3, $4::uuid, $5::date, $6::date, $7)
		 RETURNING id::text`,
		wsID, title, desc, owner, start, target, status).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrObjectiveConflict
		}
		return nil, err
	}
	ob, err := GetObjective(ctx, pool, wsSlug, actorID, id)
	if err != nil {
		return nil, err
	}
	announceObjectiveUpdated(ctx, wsSlug, ob.ID)
	return ob, nil
}

// ListObjectives returns the workspace's objectives ordered by
// creation, with computed progress + live KR counts. Any role may
// read.
func ListObjectives(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID string) ([]*Objective, error) {
	wsID, _, err := resolveObjectiveWorkspace(ctx, pool, wsSlug, actorID, false)
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx,
		`SELECT `+objectiveColumnsCounted+`
		 FROM objectives o LEFT JOIN users ow ON ow.id = o.owner_id
		 WHERE o.workspace_id = $1::uuid
		 ORDER BY o.created_at, o.id`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Objective{}
	for rows.Next() {
		ob, err := scanObjectiveCounted(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ob)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	entries, err := queryKRProgress(ctx, pool, "", wsID)
	if err != nil {
		return nil, err
	}
	fillObjectiveProgress(out, entries)
	if err := fillObjectiveProgressHistory(ctx, pool, out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetObjective returns one objective with its KRs (each with computed
// progress; issue KRs carry their linked issues). Any role may read.
func GetObjective(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID, objectiveID string) (*Objective, error) {
	wsID, _, err := resolveObjectiveWorkspace(ctx, pool, wsSlug, actorID, false)
	if err != nil {
		return nil, err
	}
	ob, err := resolveObjective(ctx, pool, wsID, objectiveID)
	if err != nil {
		return nil, err
	}
	krs, err := loadKeyResults(ctx, pool, ob.ID)
	if err != nil {
		return nil, err
	}
	linked, err := loadLinkedIssues(ctx, pool, ob.ID)
	if err != nil {
		return nil, err
	}
	for i := range krs {
		if krs[i].Kind == "issue" {
			krs[i].LinkedIssues = linked[krs[i].ID]
			if krs[i].LinkedIssues == nil {
				krs[i].LinkedIssues = []LinkedIssue{}
			}
		}
	}
	ob.KeyResults = krs
	entries, err := queryKRProgress(ctx, pool, ob.ID, "")
	if err != nil {
		return nil, err
	}
	fillObjectiveProgress([]*Objective{ob}, entries)
	if err := fillObjectiveProgressHistory(ctx, pool, []*Objective{ob}); err != nil {
		return nil, err
	}
	return ob, nil
}

// UpdateObjective applies a partial objective update. Member (15)+.
// Only non-nil patch fields are written; "" clears description /
// owner / dates. An empty patch is ErrNothingToUpdate.
func UpdateObjective(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID, objectiveID string, patch ObjectivePatch) (*Objective, error) {
	wsID, _, err := resolveObjectiveWorkspace(ctx, pool, wsSlug, actorID, true)
	if err != nil {
		return nil, err
	}
	if _, err := resolveObjective(ctx, pool, wsID, objectiveID); err != nil {
		return nil, err
	}

	var sets []string
	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if patch.Title != nil {
		title := strings.TrimSpace(*patch.Title)
		if title == "" {
			return nil, ErrNameRequired
		}
		sets = append(sets, "title = "+arg(title))
	}
	if patch.Description != nil {
		var desc *string
		if d := strings.TrimSpace(*patch.Description); d != "" {
			desc = &d
		}
		sets = append(sets, "description = "+arg(desc))
	}
	if patch.OwnerID != nil {
		owner, err := validateOwnerID(ctx, pool, patch.OwnerID)
		if err != nil {
			return nil, err
		}
		sets = append(sets, "owner_id = "+arg(owner)+"::uuid")
	}
	if patch.StartDate != nil {
		start, err := validateObjectiveDate(patch.StartDate)
		if err != nil {
			return nil, err
		}
		sets = append(sets, "start_date = "+arg(start)+"::date")
	}
	if patch.TargetDate != nil {
		target, err := validateObjectiveDate(patch.TargetDate)
		if err != nil {
			return nil, err
		}
		sets = append(sets, "target_date = "+arg(target)+"::date")
	}
	if patch.Status != nil {
		if !objectiveStatuses[*patch.Status] {
			return nil, ErrInvalidObjective
		}
		sets = append(sets, "status = "+arg(*patch.Status))
	}
	if len(sets) == 0 {
		return nil, ErrNothingToUpdate
	}
	sets = append(sets, "updated_at = now()")

	var id string
	err = pool.QueryRow(ctx,
		`UPDATE objectives SET `+strings.Join(sets, ", ")+`
		 WHERE id = `+arg(objectiveID)+`::uuid AND workspace_id = `+arg(wsID)+`::uuid
		 RETURNING id::text`,
		args...).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrObjectiveConflict
		}
		return nil, err
	}
	ob, err := GetObjective(ctx, pool, wsSlug, actorID, id)
	if err != nil {
		return nil, err
	}
	announceObjectiveUpdated(ctx, wsSlug, ob.ID)
	return ob, nil
}

// DeleteObjective deletes the objective. Its KRs + links cascade;
// linked issues are untouched. Member (15)+.
func DeleteObjective(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID, objectiveID string) error {
	wsID, _, err := resolveObjectiveWorkspace(ctx, pool, wsSlug, actorID, true)
	if err != nil {
		return err
	}
	if _, err := resolveObjective(ctx, pool, wsID, objectiveID); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx,
		`DELETE FROM objectives WHERE id = $1::uuid AND workspace_id = $2::uuid`,
		objectiveID, wsID); err != nil {
		return err
	}
	announceObjectiveUpdated(ctx, wsSlug, objectiveID)
	return nil
}

// CreateKeyResult creates a key result under the objective. Member
// (15)+. Target is required and must differ from start (zero-span
// rejected); metric-only fields on an issue KR are rejected.
func CreateKeyResult(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID, objectiveID string, in KeyResultInput) (*KeyResult, error) {
	wsID, _, err := resolveObjectiveWorkspace(ctx, pool, wsSlug, actorID, true)
	if err != nil {
		return nil, err
	}
	ob, err := resolveObjective(ctx, pool, wsID, objectiveID)
	if err != nil {
		return nil, err
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return nil, ErrNameRequired
	}
	if !keyResultKinds[in.Kind] {
		return nil, ErrInvalidKeyResult
	}
	var unit *string
	if in.Unit != nil {
		if u := strings.TrimSpace(*in.Unit); u != "" {
			unit = &u
		}
	}
	start := 0.0
	if in.StartValue != nil {
		start = *in.StartValue
	}
	if in.TargetValue == nil {
		return nil, ErrInvalidKeyResult
	}
	target := *in.TargetValue
	if target == start {
		return nil, ErrInvalidKeyResult
	}
	current := 0.0
	if in.CurrentValue != nil {
		current = *in.CurrentValue
	}
	if in.Kind == "issue" {
		// Metric-only fields are invalid on an issue KR — same rule
		// as PATCH (a present key is rejected, even "").
		if in.Unit != nil || in.CurrentValue != nil {
			return nil, ErrInvalidKeyResult
		}
		unit = nil
		current = 0
	}
	// The KR insert + its seed check-in row are one transaction:
	// a metric KR is never observable without its creation entry.
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var id string
	err = tx.QueryRow(ctx,
		`INSERT INTO key_results (objective_id, title, kind, unit, start_value, target_value, current_value)
		 VALUES ($1::uuid, $2, $3, $4, $5, $6, $7)
		 RETURNING id::text`,
		ob.ID, title, in.Kind, unit, start, target, current).Scan(&id)
	if err != nil {
		return nil, err
	}
	if in.Kind == "metric" {
		// Seed entry: previous_value NULL (no previous value at
		// creation), new_value = the current_value actually
		// inserted (0 when CurrentValue was omitted — the service
		// default, not start_value), note NULL, author = actor.
		// Issue KRs seed nothing.
		if _, err := tx.Exec(ctx,
			`INSERT INTO key_result_checkins (key_result_id, author_id, previous_value, new_value, note)
			 VALUES ($1::uuid, $2::uuid, NULL, $3, NULL)`,
			id, actorID, current); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	kr, err := loadKeyResultWithProgress(ctx, pool, ob.ID, id)
	if err != nil {
		return nil, err
	}
	announceObjectiveUpdated(ctx, wsSlug, ob.ID)
	return kr, nil
}

// loadKeyResultWithProgress loads one KR with its computed progress
// attached (issue KRs carry no linked issues here — GetObjective is
// the detail view that populates them).
func loadKeyResultWithProgress(ctx context.Context, q *pgxpool.Pool, objectiveID, krID string) (*KeyResult, error) {
	kr, err := resolveKeyResult(ctx, q, objectiveID, krID)
	if err != nil {
		return nil, err
	}
	entries, err := queryKRProgress(ctx, q, objectiveID, "")
	if err != nil {
		return nil, err
	}
	if e, ok := entries[kr.ID]; ok {
		kr.Progress = e.Progress
	}
	return kr, nil
}

// UpdateKeyResult applies a partial key result update. Member (15)+.
// Kind is immutable (a non-nil Kind is ErrInvalidKeyResult);
// metric-only fields on an issue KR are ErrInvalidKeyResult;
// target_value == start_value is ErrInvalidKeyResult. An empty patch
// is ErrNothingToUpdate. When CurrentValue changes on a metric KR,
// a check-in row (old → new, note = checkin_note, author = actor) is
// inserted in the same transaction as the UPDATE; no-op value
// updates, title/unit/target-only changes, and issue KRs record
// nothing. A checkin_note over 1000 chars is ErrInvalidKeyResult.
func UpdateKeyResult(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID, objectiveID, krID string, patch KeyResultPatch) (*KeyResult, error) {
	wsID, _, err := resolveObjectiveWorkspace(ctx, pool, wsSlug, actorID, true)
	if err != nil {
		return nil, err
	}
	ob, err := resolveObjective(ctx, pool, wsID, objectiveID)
	if err != nil {
		return nil, err
	}
	kr, err := resolveKeyResult(ctx, pool, ob.ID, krID)
	if err != nil {
		return nil, err
	}
	if patch.Kind != nil {
		return nil, ErrInvalidKeyResult
	}
	if kr.Kind == "issue" && (patch.Unit != nil || patch.CurrentValue != nil) {
		return nil, ErrInvalidKeyResult
	}
	if patch.TargetValue != nil && *patch.TargetValue == kr.StartValue {
		return nil, ErrInvalidKeyResult
	}
	note, err := normalizeCheckinNote(patch.CheckinNote)
	if err != nil {
		return nil, err
	}

	var sets []string
	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if patch.Title != nil {
		title := strings.TrimSpace(*patch.Title)
		if title == "" {
			return nil, ErrNameRequired
		}
		sets = append(sets, "title = "+arg(title))
	}
	if patch.Unit != nil {
		var unit *string
		if u := strings.TrimSpace(*patch.Unit); u != "" {
			unit = &u
		}
		sets = append(sets, "unit = "+arg(unit))
	}
	if patch.TargetValue != nil {
		sets = append(sets, "target_value = "+arg(*patch.TargetValue))
	}
	if patch.CurrentValue != nil {
		sets = append(sets, "current_value = "+arg(*patch.CurrentValue))
	}
	if len(sets) == 0 {
		return nil, ErrNothingToUpdate
	}
	sets = append(sets, "updated_at = now()")

	// The UPDATE + its check-in row (when one is recorded) are one
	// transaction: a value change is never observable without its
	// history entry.
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var id string
	err = tx.QueryRow(ctx,
		`UPDATE key_results SET `+strings.Join(sets, ", ")+`
		 WHERE id = `+arg(krID)+`::uuid AND objective_id = `+arg(ob.ID)+`::uuid
		 RETURNING id::text`,
		args...).Scan(&id)
	if err != nil {
		return nil, err
	}
	if patch.CurrentValue != nil && *patch.CurrentValue != kr.CurrentValue && kr.Kind == "metric" {
		if _, err := tx.Exec(ctx,
			`INSERT INTO key_result_checkins (key_result_id, author_id, previous_value, new_value, note)
			 VALUES ($1::uuid, $2::uuid, $3, $4, $5)`,
			krID, actorID, kr.CurrentValue, *patch.CurrentValue, note); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	updated, err := loadKeyResultWithProgress(ctx, pool, ob.ID, id)
	if err != nil {
		return nil, err
	}
	// The check-in write (when one was recorded) rides this same
	// call — there is no separate check-in event type.
	announceObjectiveUpdated(ctx, wsSlug, ob.ID)
	return updated, nil
}

// DeleteKeyResult deletes the key result. Its issue links cascade;
// linked issues are untouched. Member (15)+.
func DeleteKeyResult(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID, objectiveID, krID string) error {
	wsID, _, err := resolveObjectiveWorkspace(ctx, pool, wsSlug, actorID, true)
	if err != nil {
		return err
	}
	ob, err := resolveObjective(ctx, pool, wsID, objectiveID)
	if err != nil {
		return err
	}
	if _, err := resolveKeyResult(ctx, pool, ob.ID, krID); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx,
		`DELETE FROM key_results WHERE id = $1::uuid AND objective_id = $2::uuid`,
		krID, ob.ID); err != nil {
		return err
	}
	announceObjectiveUpdated(ctx, wsSlug, ob.ID)
	return nil
}

// ListKeyResultCheckins returns a key result's check-in history,
// newest first, capped at 100. Member (15)+. For issue KRs it
// returns an empty list, not an error (issue KRs never record
// check-ins). The author display name is resolved via a JOIN to
// users at read time (name falling back to email); author is nil
// when the author's user row was deleted.
func ListKeyResultCheckins(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID, objectiveID, krID string) ([]*KeyResultCheckin, error) {
	wsID, _, err := resolveObjectiveWorkspace(ctx, pool, wsSlug, actorID, true)
	if err != nil {
		return nil, err
	}
	ob, err := resolveObjective(ctx, pool, wsID, objectiveID)
	if err != nil {
		return nil, err
	}
	kr, err := resolveKeyResult(ctx, pool, ob.ID, krID)
	if err != nil {
		return nil, err
	}
	out := []*KeyResultCheckin{}
	if kr.Kind == "issue" {
		return out, nil
	}
	rows, err := pool.Query(ctx,
		`SELECT ci.id::text, ci.key_result_id::text,
			ci.author_id::text, COALESCE(u.name, u.email::text),
			ci.previous_value::float8, ci.new_value::float8, ci.note, ci.created_at
		 FROM key_result_checkins ci
		 LEFT JOIN users u ON u.id = ci.author_id
		 WHERE ci.key_result_id = $1::uuid
		 ORDER BY ci.created_at DESC, ci.id DESC
		 LIMIT 100`,
		krID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var ci KeyResultCheckin
		var authorID *string
		var displayName *string
		if err := rows.Scan(&ci.ID, &ci.KeyResultID, &authorID, &displayName,
			&ci.PreviousValue, &ci.NewValue, &ci.Note, &ci.CreatedAt); err != nil {
			return nil, err
		}
		if authorID != nil {
			name := ""
			if displayName != nil {
				name = *displayName
			}
			ci.Author = &CheckinAuthor{ID: *authorID, DisplayName: name}
		}
		out = append(out, &ci)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// resolveLinkIssue validates that issueID names a live issue of the
// workspace: malformed → ErrInvalidObjectiveID (400), unknown,
// soft-deleted, or foreign-workspace → ErrIssueNotFound (404 — no
// cross-workspace leak).
func resolveLinkIssue(ctx context.Context, q queryRower, wsID, issueID string) error {
	if !isUUIDFormat(issueID) {
		return ErrInvalidObjectiveID
	}
	var found bool
	err := q.QueryRow(ctx,
		`SELECT EXISTS(
			SELECT 1 FROM issues i
			JOIN projects p ON p.id = i.project_id
			WHERE i.id = $1::uuid AND i.deleted_at IS NULL
			  AND p.workspace_id = $2::uuid)`,
		issueID, wsID).Scan(&found)
	if err != nil {
		return err
	}
	if !found {
		return ErrIssueNotFound
	}
	return nil
}

// LinkIssueToKeyResult links a live issue to an issue KR. Idempotent:
// repeats are no-ops via ON CONFLICT DO NOTHING — never a 409.
// Linking to a metric KR is ErrInvalidKeyResult (400). Member (15)+.
func LinkIssueToKeyResult(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID, objectiveID, krID, issueID string) error {
	wsID, _, err := resolveObjectiveWorkspace(ctx, pool, wsSlug, actorID, true)
	if err != nil {
		return err
	}
	ob, err := resolveObjective(ctx, pool, wsID, objectiveID)
	if err != nil {
		return err
	}
	kr, err := resolveKeyResult(ctx, pool, ob.ID, krID)
	if err != nil {
		return err
	}
	if kr.Kind != "issue" {
		return ErrInvalidKeyResult
	}
	if err := resolveLinkIssue(ctx, pool, wsID, issueID); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO key_result_issues (key_result_id, issue_id)
		 VALUES ($1::uuid, $2::uuid) ON CONFLICT DO NOTHING`,
		krID, issueID); err != nil {
		return err
	}
	announceObjectiveUpdated(ctx, wsSlug, ob.ID)
	return nil
}

// UnlinkIssueFromKeyResult removes an issue link from a KR.
// Idempotent: removing a non-link is a no-op. Member (15)+.
func UnlinkIssueFromKeyResult(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID, objectiveID, krID, issueID string) error {
	wsID, _, err := resolveObjectiveWorkspace(ctx, pool, wsSlug, actorID, true)
	if err != nil {
		return err
	}
	ob, err := resolveObjective(ctx, pool, wsID, objectiveID)
	if err != nil {
		return err
	}
	if _, err := resolveKeyResult(ctx, pool, ob.ID, krID); err != nil {
		return err
	}
	if !isUUIDFormat(issueID) {
		// Malformed ids are a client error — 400, not a 500 from
		// the ::uuid cast.
		return ErrInvalidObjectiveID
	}
	if _, err := pool.Exec(ctx,
		`DELETE FROM key_result_issues
		 WHERE key_result_id = $1::uuid AND issue_id = $2::uuid`,
		krID, issueID); err != nil {
		return err
	}
	announceObjectiveUpdated(ctx, wsSlug, ob.ID)
	return nil
}
