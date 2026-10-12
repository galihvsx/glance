package service

// C28T1: recurring schedule execution — ExecuteSchedule creates one
// issue per occurrence inside a single logical execution, then advances
// the schedule. Runs against the test database.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// executeTestSetup builds a workspace/project plus a label, a state, and
// a second member for assignee coverage. Returns the workspace UUID too:
// ExecuteSchedule takes UUIDs, not slugs.
func executeTestSetup(t *testing.T) (context.Context, *pgxpool.Pool, string, string, string, string, string, string, string, string) {
	t.Helper()
	ctx, pool, slug, actor, projectID, ident := setupRecurringTest(t)
	var wsID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM workspaces WHERE slug = $1`, slug).Scan(&wsID); err != nil {
		t.Fatalf("workspace id: %v", err)
	}
	label, err := CreateLabel(ctx, pool, slug, ident, actor, LabelInput{Name: "exec-bug"})
	if err != nil {
		t.Fatalf("CreateLabel: %v", err)
	}
	state, err := CreateState(ctx, pool, slug, ident, actor, StateInput{Name: "Exec Progress", Group: "started", Color: "#000000"})
	if err != nil {
		t.Fatalf("CreateState: %v", err)
	}
	assignee := addWorkspaceMember(t, pool, slug, uniqueTestEmail("exec-assignee"), "Assignee", RoleMember)
	return ctx, pool, slug, wsID, actor, projectID, ident, label.ID, state.ID, assignee
}

// mustDueSchedule creates a schedule and forces next_run_at to runAt via
// a direct UPDATE (CreateSchedule always computes a future next_run_at).
func mustDueSchedule(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug, projectID, actor string, in CreateScheduleInput, runAt time.Time) *RecurringSchedule {
	t.Helper()
	s := mustCreateSchedule(t, ctx, pool, slug, projectID, actor, in)
	if _, err := pool.Exec(ctx,
		`UPDATE issue_schedules SET next_run_at = $1 WHERE id = $2::uuid`, runAt, s.ID); err != nil {
		t.Fatalf("force next_run_at: %v", err)
	}
	s.NextRunAt = runAt.UTC()
	return s
}

func issueLabels(t *testing.T, ctx context.Context, pool *pgxpool.Pool, issueID string) []string {
	t.Helper()
	rows, err := pool.Query(ctx,
		`SELECT label_id::text FROM issue_labels WHERE issue_id = $1::uuid`, issueID)
	if err != nil {
		t.Fatalf("issue labels: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan label: %v", err)
		}
		out = append(out, id)
	}
	return out
}

func issueAssignees(t *testing.T, ctx context.Context, pool *pgxpool.Pool, issueID string) []string {
	t.Helper()
	rows, err := pool.Query(ctx,
		`SELECT user_id::text FROM issue_assignees WHERE issue_id = $1::uuid`, issueID)
	if err != nil {
		t.Fatalf("issue assignees: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan assignee: %v", err)
		}
		out = append(out, id)
	}
	return out
}

func issueActivityFields(t *testing.T, ctx context.Context, pool *pgxpool.Pool, issueID string) map[string]string {
	t.Helper()
	rows, err := pool.Query(ctx,
		`SELECT field, new_value::text FROM issue_activities WHERE issue_id = $1::uuid`, issueID)
	if err != nil {
		t.Fatalf("issue activities: %v", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var f string
		var nv *string
		if err := rows.Scan(&f, &nv); err != nil {
			t.Fatalf("scan activity: %v", err)
		}
		if nv != nil {
			out[f] = *nv
		} else {
			out[f] = ""
		}
	}
	return out
}

func reloadSchedule(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug, projectID, scheduleID, actor string) *RecurringSchedule {
	t.Helper()
	s, err := GetSchedule(ctx, pool, slug, projectID, scheduleID, actor)
	if err != nil {
		t.Fatalf("reload schedule: %v", err)
	}
	return s
}

func TestExecuteScheduleCreatesIssue(t *testing.T) {
	ctx, pool, slug, wsID, actor, projectID, _, labelID, stateID, assignee := executeTestSetup(t)

	runAt := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	desc := "Notes for {{date}} at {{datetime}}"
	in := validScheduleInput()
	in.Frequency = "daily"
	in.DaysOfWeek = nil
	in.TitleTemplate = "Daily standup {{date}}"
	in.DescriptionTemplate = &desc
	in.StateID = &stateID
	in.Priority = 3
	in.LabelIDs = []string{labelID}
	in.AssigneeIDs = []string{assignee}
	s := mustDueSchedule(t, ctx, pool, slug, projectID, actor, in, runAt)

	iss, err := ExecuteSchedule(ctx, pool, wsID, projectID, s, runAt, actor)
	if err != nil {
		t.Fatalf("ExecuteSchedule: %v", err)
	}
	if iss == nil {
		t.Fatalf("ExecuteSchedule returned nil issue")
	}

	// Rendered title: {{date}} of the run time.
	if want := "Daily standup 2026-10-12"; iss.Name != want {
		t.Fatalf("issue name = %q, want %q", iss.Name, want)
	}
	// Priority + state honored.
	if iss.Priority != 3 {
		t.Fatalf("priority = %d, want 3", iss.Priority)
	}
	if iss.StateID != stateID {
		t.Fatalf("state = %q, want %q", iss.StateID, stateID)
	}
	// TipTap description: one paragraph per line, tokens rendered.
	var doc map[string]any
	if err := json.Unmarshal([]byte(iss.Description), &doc); err != nil {
		t.Fatalf("description is not JSON: %v", err)
	}
	if doc["type"] != "doc" {
		t.Fatalf("description type = %v, want doc", doc["type"])
	}
	content, _ := doc["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("description paragraphs = %d, want 1", len(content))
	}
	para, _ := content[0].(map[string]any)
	inner, _ := para["content"].([]any)
	txt, _ := inner[0].(map[string]any)
	if got := txt["text"]; got != "Notes for 2026-10-12 at 2026-10-12T09:00:00Z" {
		t.Fatalf("description text = %q", got)
	}

	// Labels replaced (fresh issue: exactly the schedule's set).
	if got := issueLabels(t, ctx, pool, iss.ID); len(got) != 1 || got[0] != labelID {
		t.Fatalf("labels = %v, want [%s]", got, labelID)
	}
	// Assignees attached.
	if got := issueAssignees(t, ctx, pool, iss.ID); len(got) != 1 || got[0] != assignee {
		t.Fatalf("assignees = %v, want [%s]", got, assignee)
	}
	// Activity entry attributing the schedule.
	acts := issueActivityFields(t, ctx, pool, iss.ID)
	nv, ok := acts["_schedule_created"]
	if !ok {
		t.Fatalf("missing _schedule_created activity, fields = %v", acts)
	}
	if !strings.Contains(nv, s.ID) || !strings.Contains(nv, "Weekly triage") {
		t.Fatalf("_schedule_created new_value = %q", nv)
	}

	// Schedule advanced: next occurrence after runAt, run_count bumped.
	after := reloadSchedule(t, ctx, pool, slug, projectID, s.ID, actor)
	wantNext := s.NextOccurrence(runAt.Add(time.Second))
	if !after.NextRunAt.Equal(wantNext) {
		t.Fatalf("next_run_at = %s, want %s", after.NextRunAt, wantNext)
	}
	if after.RunCount != 1 {
		t.Fatalf("run_count = %d, want 1", after.RunCount)
	}
	if after.LastRunAt == nil || !after.LastRunAt.Equal(runAt) {
		t.Fatalf("last_run_at = %v, want %s", after.LastRunAt, runAt)
	}
	if !after.Active {
		t.Fatalf("schedule deactivated unexpectedly")
	}
}

func TestExecuteScheduleExhausted(t *testing.T) {
	ctx, pool, slug, wsID, actor, projectID, _, _, _, _ := executeTestSetup(t)

	runAt := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	in := validScheduleInput()
	in.Frequency = "daily"
	in.DaysOfWeek = nil
	s := mustCreateSchedule(t, ctx, pool, slug, projectID, actor, in)
	// Force the row past exhaustion: ends_on before the due run (set via
	// SQL — CreateSchedule rightly rejects this up front).
	ends := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx,
		`UPDATE issue_schedules SET next_run_at = $1, ends_on = $2 WHERE id = $3::uuid`,
		runAt, ends, s.ID); err != nil {
		t.Fatalf("force exhaustion: %v", err)
	}
	s.NextRunAt = runAt.UTC()
	s.EndsOn = &ends

	iss, err := ExecuteSchedule(ctx, pool, wsID, projectID, s, runAt, actor)
	if err != nil {
		t.Fatalf("ExecuteSchedule: %v", err)
	}
	if iss != nil {
		t.Fatalf("exhausted schedule created issue %s", iss.ID)
	}
	after := reloadSchedule(t, ctx, pool, slug, projectID, s.ID, actor)
	if after.Active {
		t.Fatalf("exhausted schedule still active")
	}
	if after.RunCount != 0 {
		t.Fatalf("run_count = %d, want 0", after.RunCount)
	}
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM issues i JOIN projects p ON p.id = i.project_id WHERE p.id = $1::uuid`, projectID).Scan(&n); err != nil {
		t.Fatalf("count issues: %v", err)
	}
	if n != 0 {
		t.Fatalf("issues created = %d, want 0", n)
	}
}

func TestExecuteScheduleStaleLabel(t *testing.T) {
	ctx, pool, slug, wsID, actor, projectID, _, labelID, _, _ := executeTestSetup(t)

	runAt := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	in := validScheduleInput()
	in.Frequency = "daily"
	in.DaysOfWeek = nil
	in.LabelIDs = []string{labelID}
	s := mustDueSchedule(t, ctx, pool, slug, projectID, actor, in, runAt)

	// The label is deleted after the schedule was saved.
	if _, err := pool.Exec(ctx, `DELETE FROM labels WHERE id = $1::uuid`, labelID); err != nil {
		t.Fatalf("delete label: %v", err)
	}

	_, err := ExecuteSchedule(ctx, pool, wsID, projectID, s, runAt, actor)
	var stale *ScheduleStaleRef
	if !errors.As(err, &stale) {
		t.Fatalf("err = %v, want *ScheduleStaleRef", err)
	}
	if stale.Kind != "label" || stale.ID != labelID {
		t.Fatalf("stale = %+v, want kind=label id=%s", stale, labelID)
	}
	after := reloadSchedule(t, ctx, pool, slug, projectID, s.ID, actor)
	if after.Active {
		t.Fatalf("stale-ref schedule still active")
	}
}

func TestExecuteScheduleDeletedStateFallsBackToBacklog(t *testing.T) {
	// Migration 000051 declares state_id ON DELETE SET NULL with the
	// documented semantic "NULL = the project's default state at run
	// time": deleting a state degrades gracefully to the backlog state
	// instead of pausing the schedule (labels/assignees have no such
	// fallback — those DO pause).
	ctx, pool, slug, wsID, actor, projectID, _, _, stateID, _ := executeTestSetup(t)

	runAt := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	in := validScheduleInput()
	in.Frequency = "daily"
	in.DaysOfWeek = nil
	in.StateID = &stateID
	s := mustDueSchedule(t, ctx, pool, slug, projectID, actor, in, runAt)

	if _, err := pool.Exec(ctx, `DELETE FROM states WHERE id = $1::uuid`, stateID); err != nil {
		t.Fatalf("delete state: %v", err)
	}

	iss, err := ExecuteSchedule(ctx, pool, wsID, projectID, s, runAt, actor)
	if err != nil {
		t.Fatalf("ExecuteSchedule: %v", err)
	}
	if iss == nil {
		t.Fatalf("expected the issue to be created with the backlog fallback")
	}
	var backlogID string
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM states WHERE project_id = $1::uuid AND "group" = 'backlog' ORDER BY sequence LIMIT 1`,
		projectID).Scan(&backlogID); err != nil {
		t.Fatalf("backlog state: %v", err)
	}
	if iss.StateID != backlogID {
		t.Fatalf("issue state = %q, want backlog %q", iss.StateID, backlogID)
	}
	if after := reloadSchedule(t, ctx, pool, slug, projectID, s.ID, actor); !after.Active {
		t.Fatalf("schedule paused on deleted state; want graceful backlog fallback")
	}
}

func TestExecuteScheduleStaleAssignee(t *testing.T) {
	ctx, pool, slug, wsID, actor, projectID, _, _, _, assignee := executeTestSetup(t)

	runAt := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	in := validScheduleInput()
	in.Frequency = "daily"
	in.DaysOfWeek = nil
	in.AssigneeIDs = []string{assignee}
	s := mustDueSchedule(t, ctx, pool, slug, projectID, actor, in, runAt)

	// The assignee leaves the workspace after the schedule was saved.
	if _, err := pool.Exec(ctx,
		`DELETE FROM workspace_members WHERE workspace_id = $1::uuid AND user_id = $2::uuid`, wsID, assignee); err != nil {
		t.Fatalf("remove member: %v", err)
	}

	_, err := ExecuteSchedule(ctx, pool, wsID, projectID, s, runAt, actor)
	var stale *ScheduleStaleRef
	if !errors.As(err, &stale) {
		t.Fatalf("err = %v, want *ScheduleStaleRef", err)
	}
	if stale.Kind != "assignee" {
		t.Fatalf("stale kind = %q, want assignee", stale.Kind)
	}
	if after := reloadSchedule(t, ctx, pool, slug, projectID, s.ID, actor); after.Active {
		t.Fatalf("stale-ref schedule still active")
	}
}

func TestExecuteScheduleStaleActor(t *testing.T) {
	ctx, pool, slug, wsID, actor, projectID, _, _, _, reader := executeTestSetup(t)

	runAt := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	in := validScheduleInput()
	in.Frequency = "daily"
	in.DaysOfWeek = nil
	s := mustDueSchedule(t, ctx, pool, slug, projectID, actor, in, runAt)

	// The creator leaves the workspace: the schedule must pause rather
	// than misattribute the issue to someone else.
	if _, err := pool.Exec(ctx,
		`DELETE FROM workspace_members WHERE workspace_id = $1::uuid AND user_id = $2::uuid`, wsID, actor); err != nil {
		t.Fatalf("remove creator: %v", err)
	}

	_, err := ExecuteSchedule(ctx, pool, wsID, projectID, s, runAt, actor)
	var stale *ScheduleStaleRef
	if !errors.As(err, &stale) {
		t.Fatalf("err = %v, want *ScheduleStaleRef", err)
	}
	if stale.Kind != "actor" {
		t.Fatalf("stale kind = %q, want actor", stale.Kind)
	}
	if after := reloadSchedule(t, ctx, pool, slug, projectID, s.ID, reader); after.Active {
		t.Fatalf("stale-actor schedule still active")
	}
}

func TestExecuteScheduleNullActor(t *testing.T) {
	ctx, pool, slug, wsID, actor, projectID, _, _, _, reader := executeTestSetup(t)

	runAt := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	in := validScheduleInput()
	in.Frequency = "daily"
	in.DaysOfWeek = nil
	s := mustDueSchedule(t, ctx, pool, slug, projectID, actor, in, runAt)
	if _, err := pool.Exec(ctx,
		`UPDATE issue_schedules SET created_by = NULL WHERE id = $1::uuid`, s.ID); err != nil {
		t.Fatalf("null created_by: %v", err)
	}
	s.CreatedBy = nil

	_, err := ExecuteSchedule(ctx, pool, wsID, projectID, s, runAt, actor)
	var stale *ScheduleStaleRef
	if !errors.As(err, &stale) || stale.Kind != "actor" {
		t.Fatalf("err = %v, want *ScheduleStaleRef kind=actor", err)
	}
	if after := reloadSchedule(t, ctx, pool, slug, projectID, s.ID, reader); after.Active {
		t.Fatalf("null-actor schedule still active")
	}
}

func TestExecuteScheduleNoDoubleRun(t *testing.T) {
	ctx, pool, slug, wsID, actor, projectID, _, _, _, _ := executeTestSetup(t)

	runAt := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	in := validScheduleInput()
	in.Frequency = "daily"
	in.DaysOfWeek = nil
	s := mustDueSchedule(t, ctx, pool, slug, projectID, actor, in, runAt)

	first, err := ExecuteSchedule(ctx, pool, wsID, projectID, s, runAt, actor)
	if err != nil || first == nil {
		t.Fatalf("first run: issue=%v err=%v", first, err)
	}
	// A second execution of the same occurrence is a no-op: the claim
	// finds the row already advanced past runAt.
	second, err := ExecuteSchedule(ctx, pool, wsID, projectID, s, runAt, actor)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if second != nil {
		t.Fatalf("second run created a duplicate issue %s", second.ID)
	}
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM issues i WHERE i.project_id = $1::uuid`, projectID).Scan(&n); err != nil {
		t.Fatalf("count issues: %v", err)
	}
	if n != 1 {
		t.Fatalf("issues = %d, want 1", n)
	}
}

func TestExecuteScheduleActorOverride(t *testing.T) {
	ctx, pool, slug, wsID, actor, projectID, _, _, _, overrider := executeTestSetup(t)

	runAt := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	in := validScheduleInput()
	in.Frequency = "daily"
	in.DaysOfWeek = nil
	s := mustDueSchedule(t, ctx, pool, slug, projectID, actor, in, runAt)

	// run-now path: a different workspace member fires the occurrence;
	// the issue is attributed to the caller, the schedule stays active.
	iss, err := ExecuteSchedule(ctx, pool, wsID, projectID, s, runAt, overrider)
	if err != nil || iss == nil {
		t.Fatalf("override run: issue=%v err=%v", iss, err)
	}
	if iss.CreatedBy != overrider {
		t.Fatalf("issue created_by = %q, want override %q", iss.CreatedBy, overrider)
	}
	if after := reloadSchedule(t, ctx, pool, slug, projectID, s.ID, actor); !after.Active {
		t.Fatalf("override run deactivated the schedule")
	}
}

func TestExecuteScheduleActorOverrideNonMember(t *testing.T) {
	ctx, pool, slug, wsID, actor, projectID, _, _, _, _ := executeTestSetup(t)

	runAt := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	in := validScheduleInput()
	in.Frequency = "daily"
	in.DaysOfWeek = nil
	s := mustDueSchedule(t, ctx, pool, slug, projectID, actor, in, runAt)

	// A non-member override is rejected without touching the schedule.
	stranger := "00000000-0000-0000-0000-000000000000"
	_, err := ExecuteSchedule(ctx, pool, wsID, projectID, s, runAt, stranger)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
	if after := reloadSchedule(t, ctx, pool, slug, projectID, s.ID, actor); !after.Active {
		t.Fatalf("non-member override deactivated the schedule")
	}
}
