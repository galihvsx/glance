package service

// Recurring issue schedules (cycle 28 "Recurring work"), C28T0 backend.
// Beyond Plane: Plane has no native scheduled issue creation; glance
// ships it free (Apache-2.0, no gated features).
//
// An issue_schedules row (migration 000051) is a cron-like recipe scoped
// to one project: every occurrence the ticker creates one issue whose
// title/description are the templates rendered with the run time. This
// file owns the schedule CRUD, validation, the pure occurrence math, and
// template rendering. The ticker (run loop) and HTTP handlers are later
// cycle-28 tasks built on this contract.
//
// Conventions (mirror modules/initiatives):
//   - Tenancy: workspaceID accepts a workspace slug OR a UUID string;
//     projectID accepts a project identifier OR a UUID string. The project
//     is always resolved WITHIN the workspace: a bad workspace (or a
//     non-member caller) surfaces ErrNotFound — the tenancy boundary —
//     while a project of another workspace surfaces ErrProjectNotFound,
//     never a leak. A malformed schedule id surfaces
//     ErrInvalidScheduleID (400, never a ::uuid 500).
//   - Roles: any member (guest 5+) may read; mutations need member (15)+.
//   - Times are UTC in v1 (documented limitation — per-user timezones
//     are a later cycle). hour is 0..23 UTC; starts_on/ends_on are dates.
//   - PATCH tri-state: nil = untouched; for nullable fields a non-nil
//     pointer to nil clears (NULL), a non-nil pointer to a value sets.
//     days_of_week/day_of_month are DERIVED from frequency on write
//     (weekly keeps days, monthly keeps day_of_month, daily keeps
//     neither), so cadence changes never leave stale values behind.
//   - References (state_id, label_ids, assignee_ids) are validated with
//     one query each (states of the project, labels of the workspace,
//     members of the workspace — the CSV importer's lookup-map pattern,
//     no N+1). Unknown or malformed ids are ErrInvalidSchedule — a clean
//     validation error, never a 500.
//   - state_id NULL means "the project's default state at run time"
//     (the backlog state, resolved when the ticker creates the issue —
//     NOT frozen at schedule creation).
//   - CreateSchedule rejects a schedule whose ends_on already exhausts
//     it (next occurrence would be the zero time): that is a client
//     error, not a row with a NULL next_run_at.
//   - Updating cadence fields (frequency/interval/days/hour/starts/ends)
//     recomputes next_run_at from max(starts_on, now); other updates
//     leave the ticker's next_run_at untouched.
//   - Template tokens: {{date}} → YYYY-MM-DD, {{datetime}} → RFC3339,
//     both of the run time in UTC. Unknown {{...}} tokens are left
//     as-is (documented).

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrScheduleNotFound is returned when the schedule id matches nothing
	// in the project. On this path the workspace and project are confirmed
	// and the caller is a member.
	ErrScheduleNotFound = errors.New("service: recurring schedule not found")
	// ErrInvalidSchedule is returned for malformed schedule input: bad
	// frequency vocabulary, out-of-range interval/hour/weekday/day,
	// ends_on before starts_on, blank name/title template, bad priority,
	// or references (state/labels/assignees) that resolve to nothing in
	// the project/workspace.
	ErrInvalidSchedule = errors.New("service: invalid recurring schedule")
	// ErrInvalidScheduleID is returned when a schedule id is not a
	// syntactically valid UUID. The handler maps it to 400 bad_request.
	ErrInvalidScheduleID = errors.New("service: invalid recurring schedule id")
)

// scheduleFrequencies is the frequency vocabulary enforced by the
// 000051 CHECK.
var scheduleFrequencies = map[string]bool{
	"daily":   true,
	"weekly":  true,
	"monthly": true,
}

// schedulePriorities mirrors the issue priority range 0-4
// (none/low/medium/high/urgent).
const (
	schedulePriorityMin = 0
	schedulePriorityMax = 4
)

// RecurringSchedule is one scheduled-issue recipe of a project.
type RecurringSchedule struct {
	ID                  string     `json:"id"`
	WorkspaceID         string     `json:"workspace_id"`
	ProjectID           string     `json:"project_id"`
	Name                string     `json:"name"`
	Active              bool       `json:"active"`
	Frequency           string     `json:"frequency"`
	Interval            int        `json:"interval"`
	DaysOfWeek          []int      `json:"days_of_week"`
	DayOfMonth          *int       `json:"day_of_month,omitempty"`
	Hour                int        `json:"hour"`
	StartsOn            time.Time  `json:"starts_on"`
	EndsOn              *time.Time `json:"ends_on,omitempty"`
	NextRunAt           time.Time  `json:"next_run_at"`
	LastRunAt           *time.Time `json:"last_run_at,omitempty"`
	RunCount            int        `json:"run_count"`
	TitleTemplate       string     `json:"title_template"`
	DescriptionTemplate *string    `json:"description_template,omitempty"`
	StateID             *string    `json:"state_id,omitempty"`
	Priority            int        `json:"priority"`
	LabelIDs            []string   `json:"label_ids"`
	AssigneeIDs         []string   `json:"assignee_ids"`
	CreatedBy           *string    `json:"created_by,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// CreateScheduleInput carries schedule creation fields. Interval 0
// means "unset" → defaults to 1; Hour is 0..23 UTC; StartsOn/EndsOn are
// dates (time-of-day is ignored, Hour rules the run time).
type CreateScheduleInput struct {
	Name                string
	Active              *bool // nil = default true
	Frequency           string
	Interval            int // 0 = default 1
	DaysOfWeek          []int
	DayOfMonth          *int
	Hour                int
	StartsOn            time.Time
	EndsOn              *time.Time
	TitleTemplate       string
	DescriptionTemplate *string
	StateID             *string
	Priority            int
	LabelIDs            []string
	AssigneeIDs         []string
}

// UpdateScheduleInput is a partial schedule update: nil fields are
// untouched; for the nullable DayOfMonth/EndsOn/DescriptionTemplate/
// StateID a non-nil pointer to nil clears the column (NULL).
type UpdateScheduleInput struct {
	Name                *string
	Active              *bool
	Frequency           *string
	Interval            *int
	DaysOfWeek          *[]int
	DayOfMonth          **int
	Hour                *int
	StartsOn            *time.Time
	EndsOn              **time.Time
	TitleTemplate       *string
	DescriptionTemplate **string
	StateID             **string
	Priority            *int
	LabelIDs            *[]string
	AssigneeIDs         *[]string
}

// scheduleColumns is the shared SELECT list for issue_schedules rows.
// days_of_week scans into []int32 (converted to []int); uuid arrays are
// cast to text[] so uuids arrive as strings without a pgtype dependency.
const scheduleColumns = `id::text, workspace_id::text, project_id::text, name,
	active, frequency, interval, days_of_week, day_of_month, hour,
	starts_on, ends_on, next_run_at, last_run_at, run_count,
	title_template, description_template, state_id::text, priority,
	label_ids::text[], assignee_ids::text[], created_by::text,
	created_at, updated_at`

func scanSchedule(row pgx.Row) (*RecurringSchedule, error) {
	var s RecurringSchedule
	var days []int32
	var dayOfMonth *int
	var endsOn *time.Time
	var lastRunAt *time.Time
	var desc *string
	var stateID *string
	var createdBy *string
	err := row.Scan(
		&s.ID, &s.WorkspaceID, &s.ProjectID, &s.Name,
		&s.Active, &s.Frequency, &s.Interval, &days, &dayOfMonth, &s.Hour,
		&s.StartsOn, &endsOn, &s.NextRunAt, &lastRunAt, &s.RunCount,
		&s.TitleTemplate, &desc, &stateID, &s.Priority,
		&s.LabelIDs, &s.AssigneeIDs, &createdBy,
		&s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return nil, err
	}
	s.DaysOfWeek = int32sToInts(days)
	s.DayOfMonth = dayOfMonth
	s.EndsOn = endsOn
	s.LastRunAt = lastRunAt
	s.DescriptionTemplate = desc
	s.StateID = stateID
	s.CreatedBy = createdBy
	if s.LabelIDs == nil {
		s.LabelIDs = []string{}
	}
	if s.AssigneeIDs == nil {
		s.AssigneeIDs = []string{}
	}
	if s.DaysOfWeek == nil {
		s.DaysOfWeek = []int{}
	}
	return &s, nil
}

func int32sToInts(in []int32) []int {
	out := make([]int, len(in))
	for i, v := range in {
		out[i] = int(v)
	}
	return out
}

func intsToInt32s(in []int) []int32 {
	out := make([]int32, len(in))
	for i, v := range in {
		out[i] = int32(v)
	}
	return out
}

// resolveScheduleProject resolves (workspaceID, projectID) for the
// caller, enforcing the tenancy boundary: workspaceID may be a slug or
// a UUID string, projectID an identifier or a UUID string. A bad
// workspace or non-member caller surfaces ErrNotFound; a project of
// another workspace (or a malformed/unknown project identifier)
// surfaces ErrProjectNotFound. Guests may read; needMember enforces
// member (15)+.
func resolveScheduleProject(ctx context.Context, q queryRower, workspaceID, projectID, actorID string, needMember bool) (wsID, projID string, err error) {
	var wsRow pgx.Row
	if isUUIDFormat(workspaceID) {
		wsRow = q.QueryRow(ctx,
			`SELECT w.id::text, m.role
			 FROM workspaces w
			 JOIN workspace_members m ON m.workspace_id = w.id
			 WHERE w.id = $1::uuid AND m.user_id = $2::uuid`,
			workspaceID, actorID)
	} else {
		wsRow = q.QueryRow(ctx,
			`SELECT w.id::text, m.role
			 FROM workspaces w
			 JOIN workspace_members m ON m.workspace_id = w.id
			 WHERE w.slug = $1 AND m.user_id = $2::uuid`,
			workspaceID, actorID)
	}
	wsID, role, err := workspaceIDForActor(wsRow)
	if err != nil {
		return "", "", err
	}
	if isUUIDFormat(projectID) {
		err = q.QueryRow(ctx,
			`SELECT id::text FROM projects
			 WHERE id = $1::uuid AND workspace_id = $2::uuid`,
			projectID, wsID).Scan(&projID)
	} else {
		err = q.QueryRow(ctx,
			`SELECT id::text FROM projects
			 WHERE workspace_id = $1::uuid AND identifier = $2`,
			wsID, projectID).Scan(&projID)
	}
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", ErrProjectNotFound
		}
		return "", "", err
	}
	// C92T0: schedules are project-scoped, so the effective-access
	// order applies — a private project the caller cannot reach is
	// ErrNotFound, and needMember gates on the EFFECTIVE role (a
	// teamspace-derived member passes; a direct guest does not).
	effRole, err := effectiveAccessForProject(ctx, q, wsID, role, projID, actorID)
	if err != nil {
		return "", "", err
	}
	if effRole < 0 {
		return "", "", ErrNotFound
	}
	if needMember && effRole < RoleMember {
		return "", "", ErrForbidden
	}
	return wsID, projID, nil
}

// resolveSchedule loads a schedule of the project. Malformed ids match
// nothing — 400, not a 500 from the ::uuid cast.
func resolveSchedule(ctx context.Context, q queryRower, wsID, projID, scheduleID string) (*RecurringSchedule, error) {
	if !isUUIDFormat(scheduleID) {
		return nil, ErrInvalidScheduleID
	}
	s, err := scanSchedule(q.QueryRow(ctx,
		`SELECT `+scheduleColumns+`
		 FROM issue_schedules
		 WHERE id = $1::uuid AND workspace_id = $2::uuid AND project_id = $3::uuid`,
		scheduleID, wsID, projID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrScheduleNotFound
		}
		return nil, err
	}
	return s, nil
}

// scheduleRefs holds the validated reference sets for one schedule
// write: state ids of the project, label ids of the workspace, and
// member user ids of the workspace — each loaded in ONE query (the CSV
// importer's lookup-map pattern, no N+1).
type scheduleRefs struct {
	states  map[string]bool
	labels  map[string]bool
	members map[string]bool
}

func loadScheduleRefs(ctx context.Context, pool *pgxpool.Pool, wsID, projID string) (*scheduleRefs, error) {
	refs := &scheduleRefs{
		states:  map[string]bool{},
		labels:  map[string]bool{},
		members: map[string]bool{},
	}
	queries := []struct {
		sql string
		arg string
		set map[string]bool
	}{
		{`SELECT id::text FROM states WHERE project_id = $1::uuid`, projID, refs.states},
		{`SELECT id::text FROM labels WHERE workspace_id = $1::uuid`, wsID, refs.labels},
		{`SELECT user_id::text FROM workspace_members WHERE workspace_id = $1::uuid`, wsID, refs.members},
	}
	for _, qq := range queries {
		rows, err := pool.Query(ctx, qq.sql, qq.arg)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			qq.set[id] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return refs, nil
}

// normalizeDaysOfWeek dedups and sorts weekday numbers, rejecting
// out-of-range values.
func normalizeDaysOfWeek(days []int) ([]int, error) {
	seen := map[int]bool{}
	out := []int{}
	for _, d := range days {
		if d < 0 || d > 6 {
			return nil, fmt.Errorf("%w: day of week %d out of range 0..6", ErrInvalidSchedule, d)
		}
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	sort.Ints(out)
	return out, nil
}

// normalizeIDList trims, drops empties, dedups, and format-checks a list
// of uuid strings. Malformed ids are a client error — 400, never a 500
// from a ::uuid cast.
func normalizeIDList(ids []string, what string) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if !isUUIDFormat(id) {
			return nil, fmt.Errorf("%w: malformed %s id %q", ErrInvalidSchedule, what, id)
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

// scheduleParams is a fully-normalized schedule write.
type scheduleParams struct {
	name                string
	active              bool
	frequency           string
	interval            int
	daysOfWeek          []int
	dayOfMonth          *int
	hour                int
	startsOn            time.Time
	endsOn              *time.Time
	titleTemplate       string
	descriptionTemplate *string
	stateID             *string
	priority            int
	labelIDs            []string
	assigneeIDs         []string
}

// normalizeScheduleInput validates creation fields and derives
// days_of_week/day_of_month from the frequency.
func normalizeScheduleInput(in CreateScheduleInput) (scheduleParams, error) {
	var p scheduleParams
	p.name = strings.TrimSpace(in.Name)
	if p.name == "" {
		return p, fmt.Errorf("%w: name is required", ErrInvalidSchedule)
	}
	if !scheduleFrequencies[in.Frequency] {
		return p, fmt.Errorf("%w: unknown frequency %q", ErrInvalidSchedule, in.Frequency)
	}
	p.frequency = in.Frequency
	p.interval = in.Interval
	if p.interval == 0 {
		p.interval = 1 // 0 = unset → default
	}
	if p.interval < 1 {
		return p, fmt.Errorf("%w: interval must be >= 1", ErrInvalidSchedule)
	}
	if in.Hour < 0 || in.Hour > 23 {
		return p, fmt.Errorf("%w: hour must be 0..23", ErrInvalidSchedule)
	}
	p.hour = in.Hour
	if in.StartsOn.IsZero() {
		return p, fmt.Errorf("%w: starts_on is required", ErrInvalidSchedule)
	}
	p.startsOn = dateOnlyUTC(in.StartsOn)
	if in.EndsOn != nil {
		e := dateOnlyUTC(*in.EndsOn)
		if e.Before(p.startsOn) {
			return p, fmt.Errorf("%w: ends_on is before starts_on", ErrInvalidSchedule)
		}
		p.endsOn = &e
	}
	p.titleTemplate = strings.TrimSpace(in.TitleTemplate)
	if p.titleTemplate == "" {
		return p, fmt.Errorf("%w: title template is required", ErrInvalidSchedule)
	}
	if in.Priority < schedulePriorityMin || in.Priority > schedulePriorityMax {
		return p, fmt.Errorf("%w: priority must be 0..4", ErrInvalidSchedule)
	}
	p.priority = in.Priority

	switch p.frequency {
	case "weekly":
		days, err := normalizeDaysOfWeek(in.DaysOfWeek)
		if err != nil {
			return p, err
		}
		if len(days) == 0 {
			return p, fmt.Errorf("%w: weekly schedules need at least one day of week", ErrInvalidSchedule)
		}
		p.daysOfWeek = days
	case "monthly":
		if in.DayOfMonth == nil {
			return p, fmt.Errorf("%w: monthly schedules need a day of month", ErrInvalidSchedule)
		}
		if *in.DayOfMonth < 1 || *in.DayOfMonth > 31 {
			return p, fmt.Errorf("%w: day of month must be 1..31", ErrInvalidSchedule)
		}
		d := *in.DayOfMonth
		p.dayOfMonth = &d
	default: // daily: no weekday/monthday state
		p.daysOfWeek = []int{}
	}

	p.active = true
	if in.Active != nil {
		p.active = *in.Active
	}
	if in.DescriptionTemplate != nil {
		if d := strings.TrimSpace(*in.DescriptionTemplate); d != "" {
			p.descriptionTemplate = &d
		}
	}
	if in.StateID != nil {
		if s := strings.TrimSpace(*in.StateID); s != "" {
			if !isUUIDFormat(s) {
				return p, fmt.Errorf("%w: malformed state id %q", ErrInvalidSchedule, s)
			}
			p.stateID = &s
		}
	}
	var err error
	if p.labelIDs, err = normalizeIDList(in.LabelIDs, "label"); err != nil {
		return p, err
	}
	if p.assigneeIDs, err = normalizeIDList(in.AssigneeIDs, "assignee"); err != nil {
		return p, err
	}
	return p, nil
}

// checkScheduleRefs validates the schedule's references against the
// pre-loaded lookup maps. Unknown ids are ErrInvalidSchedule.
func checkScheduleRefs(p scheduleParams, refs *scheduleRefs) error {
	if p.stateID != nil && !refs.states[*p.stateID] {
		return fmt.Errorf("%w: unknown state %q", ErrInvalidSchedule, *p.stateID)
	}
	for _, id := range p.labelIDs {
		if !refs.labels[id] {
			return fmt.Errorf("%w: unknown label %q", ErrInvalidSchedule, id)
		}
	}
	for _, id := range p.assigneeIDs {
		if !refs.members[id] {
			return fmt.Errorf("%w: unknown assignee %q", ErrInvalidSchedule, id)
		}
	}
	return nil
}

// dateOnlyUTC truncates to the UTC calendar date.
func dateOnlyUTC(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// CreateSchedule creates a recurring schedule in the project.
// Member (15)+. next_run_at is the first occurrence on or after
// max(starts_on, now); a schedule already exhausted by ends_on is
// rejected as a client error.
func CreateSchedule(ctx context.Context, pool *pgxpool.Pool, workspaceID, projectID string, in CreateScheduleInput, actorID string) (*RecurringSchedule, error) {
	wsID, projID, err := resolveScheduleProject(ctx, pool, workspaceID, projectID, actorID, true)
	if err != nil {
		return nil, err
	}
	p, err := normalizeScheduleInput(in)
	if err != nil {
		return nil, err
	}
	refs, err := loadScheduleRefs(ctx, pool, wsID, projID)
	if err != nil {
		return nil, err
	}
	if err := checkScheduleRefs(p, refs); err != nil {
		return nil, err
	}

	probe := paramsToSchedule(p)
	from := time.Now().UTC()
	if from.Before(p.startsOn) {
		from = p.startsOn
	}
	next := probe.NextOccurrence(from)
	if next.IsZero() {
		return nil, fmt.Errorf("%w: ends_on is before the first run", ErrInvalidSchedule)
	}

	s, err := scanSchedule(pool.QueryRow(ctx,
		`INSERT INTO issue_schedules
			(workspace_id, project_id, name, active, frequency, interval,
			 days_of_week, day_of_month, hour, starts_on, ends_on,
			 next_run_at, title_template, description_template,
			 state_id, priority, label_ids, assignee_ids, created_by)
		 VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6,
			 $7, $8, $9, $10, $11,
			 $12, $13, $14,
			 $15::uuid, $16, $17, $18, $19::uuid)
		 RETURNING `+scheduleColumns,
		wsID, projID, p.name, p.active, p.frequency, p.interval,
		intsToInt32s(p.daysOfWeek), p.dayOfMonth, p.hour, p.startsOn, p.endsOn,
		next, p.titleTemplate, p.descriptionTemplate,
		p.stateID, p.priority, p.labelIDs, p.assigneeIDs, actorID))
	if err != nil {
		return nil, err
	}
	return s, nil
}

// paramsToSchedule builds a throwaway schedule for occurrence math.
func paramsToSchedule(p scheduleParams) *RecurringSchedule {
	return &RecurringSchedule{
		Frequency:  p.frequency,
		Interval:   p.interval,
		DaysOfWeek: p.daysOfWeek,
		DayOfMonth: p.dayOfMonth,
		Hour:       p.hour,
		StartsOn:   p.startsOn,
		EndsOn:     p.endsOn,
	}
}

// scheduleToInput converts a stored schedule back to creation input for
// update overlay.
func scheduleToInput(s *RecurringSchedule) CreateScheduleInput {
	return CreateScheduleInput{
		Name:                s.Name,
		Active:              &s.Active,
		Frequency:           s.Frequency,
		Interval:            s.Interval,
		DaysOfWeek:          s.DaysOfWeek,
		DayOfMonth:          s.DayOfMonth,
		Hour:                s.Hour,
		StartsOn:            s.StartsOn,
		EndsOn:              s.EndsOn,
		TitleTemplate:       s.TitleTemplate,
		DescriptionTemplate: s.DescriptionTemplate,
		StateID:             s.StateID,
		Priority:            s.Priority,
		LabelIDs:            s.LabelIDs,
		AssigneeIDs:         s.AssigneeIDs,
	}
}

// GetSchedule returns one schedule of the project. Any role may read.
func GetSchedule(ctx context.Context, pool *pgxpool.Pool, workspaceID, projectID, scheduleID, actorID string) (*RecurringSchedule, error) {
	wsID, projID, err := resolveScheduleProject(ctx, pool, workspaceID, projectID, actorID, false)
	if err != nil {
		return nil, err
	}
	return resolveSchedule(ctx, pool, wsID, projID, scheduleID)
}

// ListSchedules returns the project's schedules ordered by creation.
// Any role may read.
func ListSchedules(ctx context.Context, pool *pgxpool.Pool, workspaceID, projectID, actorID string) ([]*RecurringSchedule, error) {
	wsID, projID, err := resolveScheduleProject(ctx, pool, workspaceID, projectID, actorID, false)
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx,
		`SELECT `+scheduleColumns+`
		 FROM issue_schedules
		 WHERE workspace_id = $1::uuid AND project_id = $2::uuid
		 ORDER BY created_at`, wsID, projID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*RecurringSchedule{}
	for rows.Next() {
		s, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// UpdateSchedule applies a partial schedule update. Member (15)+. An
// empty patch is ErrNothingToUpdate. Cadence changes recompute
// next_run_at from max(starts_on, now).
func UpdateSchedule(ctx context.Context, pool *pgxpool.Pool, workspaceID, projectID, scheduleID, actorID string, in UpdateScheduleInput) (*RecurringSchedule, error) {
	wsID, projID, err := resolveScheduleProject(ctx, pool, workspaceID, projectID, actorID, true)
	if err != nil {
		return nil, err
	}
	current, err := resolveSchedule(ctx, pool, wsID, projID, scheduleID)
	if err != nil {
		return nil, err
	}

	base := scheduleToInput(current)
	cadenceChanged := false
	changed := false
	if in.Name != nil {
		base.Name = *in.Name
		changed = true
	}
	if in.Active != nil {
		base.Active = in.Active
		changed = true
	}
	if in.Frequency != nil {
		base.Frequency = *in.Frequency
		changed, cadenceChanged = true, true
	}
	if in.Interval != nil {
		base.Interval = *in.Interval
		changed, cadenceChanged = true, true
	}
	if in.DaysOfWeek != nil {
		base.DaysOfWeek = *in.DaysOfWeek
		changed, cadenceChanged = true, true
	}
	if in.DayOfMonth != nil {
		base.DayOfMonth = *in.DayOfMonth
		changed, cadenceChanged = true, true
	}
	if in.Hour != nil {
		base.Hour = *in.Hour
		changed, cadenceChanged = true, true
	}
	if in.StartsOn != nil {
		base.StartsOn = *in.StartsOn
		changed, cadenceChanged = true, true
	}
	if in.EndsOn != nil {
		base.EndsOn = *in.EndsOn
		changed, cadenceChanged = true, true
	}
	if in.TitleTemplate != nil {
		base.TitleTemplate = *in.TitleTemplate
		changed = true
	}
	if in.DescriptionTemplate != nil {
		base.DescriptionTemplate = *in.DescriptionTemplate
		changed = true
	}
	if in.StateID != nil {
		base.StateID = *in.StateID
		changed = true
	}
	if in.Priority != nil {
		base.Priority = *in.Priority
		changed = true
	}
	if in.LabelIDs != nil {
		base.LabelIDs = *in.LabelIDs
		changed = true
	}
	if in.AssigneeIDs != nil {
		base.AssigneeIDs = *in.AssigneeIDs
		changed = true
	}
	if !changed {
		return nil, ErrNothingToUpdate
	}

	p, err := normalizeScheduleInput(base)
	if err != nil {
		return nil, err
	}
	refs, err := loadScheduleRefs(ctx, pool, wsID, projID)
	if err != nil {
		return nil, err
	}
	if err := checkScheduleRefs(p, refs); err != nil {
		return nil, err
	}

	var sets []string
	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	sets = append(sets,
		"name = "+arg(p.name),
		"active = "+arg(p.active),
		"frequency = "+arg(p.frequency),
		"interval = "+arg(p.interval),
		"days_of_week = "+arg(intsToInt32s(p.daysOfWeek)),
		"day_of_month = "+arg(p.dayOfMonth),
		"hour = "+arg(p.hour),
		"starts_on = "+arg(p.startsOn),
		"ends_on = "+arg(p.endsOn),
		"title_template = "+arg(p.titleTemplate),
		"description_template = "+arg(p.descriptionTemplate),
		"state_id = "+arg(p.stateID)+`::uuid`,
		"priority = "+arg(p.priority),
		"label_ids = "+arg(p.labelIDs),
		"assignee_ids = "+arg(p.assigneeIDs),
	)
	if cadenceChanged {
		probe := paramsToSchedule(p)
		from := time.Now().UTC()
		if from.Before(p.startsOn) {
			from = p.startsOn
		}
		next := probe.NextOccurrence(from)
		if next.IsZero() {
			return nil, fmt.Errorf("%w: ends_on is before the first run", ErrInvalidSchedule)
		}
		sets = append(sets, "next_run_at = "+arg(next))
	}
	sets = append(sets, "updated_at = now()")

	updated, err := scanSchedule(pool.QueryRow(ctx,
		`UPDATE issue_schedules SET `+strings.Join(sets, ", ")+`
		 WHERE id = `+arg(scheduleID)+`::uuid
		   AND workspace_id = `+arg(wsID)+`::uuid
		   AND project_id = `+arg(projID)+`::uuid
		 RETURNING `+scheduleColumns,
		args...))
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// DeleteSchedule deletes the schedule. Member (15)+. Future ticker runs
// simply stop; already-created issues are untouched (the schedule owns
// no issue rows).
func DeleteSchedule(ctx context.Context, pool *pgxpool.Pool, workspaceID, projectID, scheduleID, actorID string) error {
	wsID, projID, err := resolveScheduleProject(ctx, pool, workspaceID, projectID, actorID, true)
	if err != nil {
		return err
	}
	if _, err := resolveSchedule(ctx, pool, wsID, projID, scheduleID); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx,
		`DELETE FROM issue_schedules
		 WHERE id = $1::uuid AND workspace_id = $2::uuid AND project_id = $3::uuid`,
		scheduleID, wsID, projID); err != nil {
		return err
	}
	return nil
}

// NextOccurrence returns the first scheduled run at or after `from`
// (UTC). Occurrences are anchored at starts_on: daily steps interval
// days, weekly steps interval weeks (only weekdays in days_of_week),
// monthly lands on day_of_month clamped to the month length (31 → Feb
// 28/29). The run time-of-day is always Hour:00 UTC. If ends_on is set
// and the occurrence falls after ends_on, the schedule is exhausted and
// the zero time is returned (the caller deactivates it). An unknown
// frequency also yields the zero time.
func (s *RecurringSchedule) NextOccurrence(from time.Time) time.Time {
	from = from.UTC()
	anchor := dateOnlyUTC(s.StartsOn)
	if from.Before(anchor) {
		from = anchor
	}
	var next time.Time
	switch s.Frequency {
	case "daily":
		next = nextDailyOccurrence(anchor, s.Hour, s.Interval, from)
	case "weekly":
		next = nextWeeklyOccurrence(anchor, s.Hour, s.Interval, s.DaysOfWeek, from)
	case "monthly":
		next = nextMonthlyOccurrence(anchor, s.Hour, s.Interval, s.DayOfMonth, from)
	default:
		return time.Time{}
	}
	if next.IsZero() {
		return next
	}
	if s.EndsOn != nil && dateOnlyUTC(next).After(dateOnlyUTC(*s.EndsOn)) {
		return time.Time{}
	}
	return next
}

// nextDailyOccurrence: every interval days from anchor at hour:00 UTC.
func nextDailyOccurrence(anchor time.Time, hour, interval int, from time.Time) time.Time {
	if interval < 1 {
		interval = 1
	}
	days := int(dateOnlyUTC(from).Sub(anchor).Hours() / 24)
	if days < 0 {
		days = 0
	}
	// Floor to the aligned day, then step once if its run already passed.
	aligned := anchor.AddDate(0, 0, days/interval*interval)
	cand := time.Date(aligned.Year(), aligned.Month(), aligned.Day(), hour, 0, 0, 0, time.UTC)
	if cand.Before(from) {
		cand = cand.AddDate(0, 0, interval)
	}
	return cand
}

// nextWeeklyOccurrence: interval-week steps from the anchor week, only on
// weekdays in days.
func nextWeeklyOccurrence(anchor time.Time, hour, interval int, days []int, from time.Time) time.Time {
	if interval < 1 {
		interval = 1
	}
	if len(days) == 0 {
		return time.Time{}
	}
	inSet := map[int]bool{}
	for _, d := range days {
		inSet[d] = true
	}
	start := dateOnlyUTC(from).AddDate(0, 0, -1)
	if start.Before(anchor) {
		start = anchor
	}
	for d := start; ; d = d.AddDate(0, 0, 1) {
		weekIdx := int(d.Sub(anchor).Hours()/24) / 7
		if weekIdx%interval == 0 && inSet[int(d.Weekday())] {
			cand := time.Date(d.Year(), d.Month(), d.Day(), hour, 0, 0, 0, time.UTC)
			if !cand.Before(from) {
				return cand
			}
		}
		if d.After(anchor.AddDate(10, 0, 0)) {
			return time.Time{} // unreachable safety bound
		}
	}
}

// nextMonthlyOccurrence: interval-month steps from the anchor month,
// landing on day clamped to the month length.
func nextMonthlyOccurrence(anchor time.Time, hour, interval int, day *int, from time.Time) time.Time {
	if interval < 1 {
		interval = 1
	}
	if day == nil || *day < 1 || *day > 31 {
		return time.Time{}
	}
	f := dateOnlyUTC(from)
	months := (f.Year()-anchor.Year())*12 + int(f.Month()) - int(anchor.Month())
	if months < 0 {
		months = 0
	}
	for k := months / interval; k < 1200; k++ {
		ym := time.Date(anchor.Year(), anchor.Month()+time.Month(k*interval), 1, 0, 0, 0, 0, time.UTC)
		dd := *day
		if last := daysInMonth(ym.Year(), ym.Month()); dd > last {
			dd = last
		}
		cand := time.Date(ym.Year(), ym.Month(), dd, hour, 0, 0, 0, time.UTC)
		if !cand.Before(from) {
			return cand
		}
	}
	return time.Time{} // unreachable safety bound
}

// daysInMonth returns the number of days in the month.
func daysInMonth(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// RenderTitle renders the title template with the run time.
func (s *RecurringSchedule) RenderTitle(runAt time.Time) string {
	return renderScheduleTemplate(s.TitleTemplate, runAt)
}

// RenderDescription renders the description template with the run time;
// a nil template renders empty.
func (s *RecurringSchedule) RenderDescription(runAt time.Time) string {
	if s.DescriptionTemplate == nil {
		return ""
	}
	return renderScheduleTemplate(*s.DescriptionTemplate, runAt)
}

// renderScheduleTemplate replaces {{date}} (YYYY-MM-DD) and {{datetime}}
// (RFC3339) with the run time in UTC. {{datetime}} is replaced first so
// it is never half-matched by {{date}}. Unknown {{...}} tokens are left
// as-is.
func renderScheduleTemplate(tpl string, runAt time.Time) string {
	rt := runAt.UTC()
	s := strings.ReplaceAll(tpl, "{{datetime}}", rt.Format(time.RFC3339))
	return strings.ReplaceAll(s, "{{date}}", rt.Format("2006-01-02"))
}
