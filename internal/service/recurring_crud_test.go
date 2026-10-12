package service

// C28T0: recurring issue schedules — CRUD, tenancy, roles, validation.
// Runs against the test database.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func setupRecurringTest(t *testing.T) (context.Context, *pgxpool.Pool, string, string, string, string) {
	t.Helper()
	ctx := context.Background()
	pool := newTestPool(t)
	actor := createTestUser(t, pool, uniqueTestEmail("recur"))
	slug := uniqueTestSlug("recur-ws")
	createTestWorkspace(t, pool, "Recur Co", slug, actor)
	proj := createTestProject(t, pool, slug, actor, "Recur Project", "REC")
	return ctx, pool, slug, actor, proj.ID, proj.Identifier
}

func validScheduleInput() CreateScheduleInput {
	return CreateScheduleInput{
		Name:          "Weekly triage",
		Frequency:     "weekly",
		Interval:      1,
		DaysOfWeek:    []int{1},
		Hour:          9,
		StartsOn:      time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC),
		TitleTemplate: "Triage for {{date}}",
		Priority:      2,
	}
}

func mustCreateSchedule(t *testing.T, ctx context.Context, pool *pgxpool.Pool, wsID, projectID, actor string, in CreateScheduleInput) *RecurringSchedule {
	t.Helper()
	s, err := CreateSchedule(ctx, pool, wsID, projectID, in, actor)
	if err != nil {
		t.Fatalf("CreateSchedule: %v", err)
	}
	return s
}

func TestScheduleCRUD(t *testing.T) {
	ctx, pool, slug, actor, projectID, _ := setupRecurringTest(t)

	desc := "Auto-created"
	in := validScheduleInput()
	in.DescriptionTemplate = &desc
	s := mustCreateSchedule(t, ctx, pool, slug, projectID, actor, in)

	if s.ID == "" || s.WorkspaceID == "" {
		t.Fatalf("missing ids: %+v", s)
	}
	if s.ProjectID != projectID {
		t.Fatalf("ProjectID = %q, want %q", s.ProjectID, projectID)
	}
	if s.Name != "Weekly triage" || !s.Active || s.Frequency != "weekly" {
		t.Fatalf("fields = %+v", s)
	}
	if len(s.DaysOfWeek) != 1 || s.DaysOfWeek[0] != 1 {
		t.Fatalf("DaysOfWeek = %v", s.DaysOfWeek)
	}
	if s.RunCount != 0 || s.LastRunAt != nil {
		t.Fatalf("fresh schedule run state = %+v", s)
	}
	if s.CreatedBy == nil || *s.CreatedBy != actor {
		t.Fatalf("CreatedBy = %v", s.CreatedBy)
	}
	// next_run_at = first Monday on/after max(starts_on, now): 2026-10-05 is
	// in the past relative to "now", so it must be a future Monday 09:00.
	if s.NextRunAt.IsZero() || s.NextRunAt.Hour() != 9 || s.NextRunAt.Weekday() != time.Monday {
		t.Fatalf("NextRunAt = %s", s.NextRunAt)
	}
	if s.NextRunAt.Before(time.Now().UTC()) {
		t.Fatalf("NextRunAt in the past: %s", s.NextRunAt)
	}

	got, err := GetSchedule(ctx, pool, slug, projectID, s.ID, actor)
	if err != nil {
		t.Fatalf("GetSchedule: %v", err)
	}
	if got.ID != s.ID || got.TitleTemplate != "Triage for {{date}}" {
		t.Fatalf("got = %+v", got)
	}

	list, err := ListSchedules(ctx, pool, slug, projectID, actor)
	if err != nil {
		t.Fatalf("ListSchedules: %v", err)
	}
	if len(list) != 1 || list[0].ID != s.ID {
		t.Fatalf("list = %+v", list)
	}

	// Update: rename, deactivate, change weekday.
	updated, err := UpdateSchedule(ctx, pool, slug, projectID, s.ID, actor, UpdateScheduleInput{
		Name:       strptr("Weekly triage v2"),
		Active:     boolptr(false),
		DaysOfWeek: &[]int{3, 1}, // unsorted + will be normalized
	})
	if err != nil {
		t.Fatalf("UpdateSchedule: %v", err)
	}
	if updated.Name != "Weekly triage v2" || updated.Active {
		t.Fatalf("updated = %+v", updated)
	}
	if len(updated.DaysOfWeek) != 2 || updated.DaysOfWeek[0] != 1 || updated.DaysOfWeek[1] != 3 {
		t.Fatalf("DaysOfWeek not normalized = %v", updated.DaysOfWeek)
	}
	if updated.TitleTemplate != "Triage for {{date}}" {
		t.Fatalf("title template lost = %q", updated.TitleTemplate)
	}

	// Empty patch is ErrNothingToUpdate.
	if _, err := UpdateSchedule(ctx, pool, slug, projectID, s.ID, actor, UpdateScheduleInput{}); !errors.Is(err, ErrNothingToUpdate) {
		t.Fatalf("empty patch err = %v, want ErrNothingToUpdate", err)
	}

	if err := DeleteSchedule(ctx, pool, slug, projectID, s.ID, actor); err != nil {
		t.Fatalf("DeleteSchedule: %v", err)
	}
	if _, err := GetSchedule(ctx, pool, slug, projectID, s.ID, actor); !errors.Is(err, ErrScheduleNotFound) {
		t.Fatalf("get after delete err = %v, want ErrScheduleNotFound", err)
	}
}

func TestScheduleTenancy(t *testing.T) {
	ctx, pool, slug, actor, projectID, identifier := setupRecurringTest(t)
	s := mustCreateSchedule(t, ctx, pool, slug, projectID, actor, validScheduleInput())

	// Project may be addressed by identifier too.
	got, err := GetSchedule(ctx, pool, slug, identifier, s.ID, actor)
	if err != nil || got.ID != s.ID {
		t.Fatalf("identifier addressing: got %+v err %v", got, err)
	}

	// Bad workspace → ErrNotFound (tenancy boundary).
	if _, err := ListSchedules(ctx, pool, "no-such-ws", projectID, actor); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bad workspace err = %v, want ErrNotFound", err)
	}

	// Cross-workspace project → ErrProjectNotFound, never a leak.
	otherActor := createTestUser(t, pool, uniqueTestEmail("recur-other"))
	otherSlug := uniqueTestSlug("recur-ws2")
	createTestWorkspace(t, pool, "Other Co", otherSlug, otherActor)
	otherProj := createTestProject(t, pool, otherSlug, otherActor, "Other Project", "OTH")
	if _, err := GetSchedule(ctx, pool, slug, otherProj.ID, s.ID, actor); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("cross-workspace project err = %v, want ErrProjectNotFound", err)
	}
	if _, err := CreateSchedule(ctx, pool, slug, otherProj.ID, validScheduleInput(), actor); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("create in foreign project err = %v, want ErrProjectNotFound", err)
	}

	// Malformed ids are 400s, never 500s.
	if _, err := GetSchedule(ctx, pool, slug, projectID, "not-a-uuid", actor); !errors.Is(err, ErrInvalidScheduleID) {
		t.Fatalf("bad schedule id err = %v, want ErrInvalidScheduleID", err)
	}
	if _, err := CreateSchedule(ctx, pool, slug, "nope", validScheduleInput(), actor); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("bad project id err = %v, want ErrProjectNotFound", err)
	}
}

func TestScheduleRoles(t *testing.T) {
	ctx, pool, slug, actor, projectID, _ := setupRecurringTest(t)
	guest := createTestUser(t, pool, uniqueTestEmail("recur-guest"))
	if err := UpsertMember(ctx, pool, slug, actor, guest, RoleGuest); err != nil {
		t.Fatalf("UpsertMember guest: %v", err)
	}
	s := mustCreateSchedule(t, ctx, pool, slug, projectID, actor, validScheduleInput())

	// Guest may read...
	if _, err := GetSchedule(ctx, pool, slug, projectID, s.ID, guest); err != nil {
		t.Fatalf("guest read: %v", err)
	}
	if _, err := ListSchedules(ctx, pool, slug, projectID, guest); err != nil {
		t.Fatalf("guest list: %v", err)
	}
	// ...but not mutate.
	if _, err := CreateSchedule(ctx, pool, slug, projectID, validScheduleInput(), guest); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest create err = %v, want ErrForbidden", err)
	}
	if _, err := UpdateSchedule(ctx, pool, slug, projectID, s.ID, guest, UpdateScheduleInput{Name: strptr("x")}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest update err = %v, want ErrForbidden", err)
	}
	if err := DeleteSchedule(ctx, pool, slug, projectID, s.ID, guest); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest delete err = %v, want ErrForbidden", err)
	}

	// Non-member sees the tenancy boundary.
	outsider := createTestUser(t, pool, uniqueTestEmail("recur-out"))
	if _, err := ListSchedules(ctx, pool, slug, projectID, outsider); !errors.Is(err, ErrNotFound) {
		t.Fatalf("outsider list err = %v, want ErrNotFound", err)
	}
}

func TestScheduleValidation(t *testing.T) {
	ctx, pool, slug, actor, projectID, _ := setupRecurringTest(t)

	cases := []struct {
		name   string
		mutate func(*CreateScheduleInput)
	}{
		{"blank name", func(in *CreateScheduleInput) { in.Name = "  " }},
		{"bad frequency", func(in *CreateScheduleInput) { in.Frequency = "yearly" }},
		{"negative interval", func(in *CreateScheduleInput) { in.Interval = -2 }},
		{"hour too high", func(in *CreateScheduleInput) { in.Hour = 24 }},
		{"hour negative", func(in *CreateScheduleInput) { in.Hour = -1 }},
		{"weekly empty days", func(in *CreateScheduleInput) { in.DaysOfWeek = nil }},
		{"weekly bad weekday", func(in *CreateScheduleInput) { in.DaysOfWeek = []int{7} }},
		{"monthly missing day", func(in *CreateScheduleInput) {
			in.Frequency = "monthly"
			in.DayOfMonth = nil
		}},
		{"monthly day 0", func(in *CreateScheduleInput) {
			in.Frequency = "monthly"
			d := 0
			in.DayOfMonth = &d
		}},
		{"monthly day 32", func(in *CreateScheduleInput) {
			in.Frequency = "monthly"
			d := 32
			in.DayOfMonth = &d
		}},
		{"ends before starts", func(in *CreateScheduleInput) {
			e := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
			in.EndsOn = &e
		}},
		{"ends_on already exhausted", func(in *CreateScheduleInput) {
			now := time.Now().UTC()
			in.StartsOn = now.AddDate(0, 0, -30)
			e := dateOnlyUTC(now.AddDate(0, 0, -1))
			in.EndsOn = &e
		}},
		{"blank title template", func(in *CreateScheduleInput) { in.TitleTemplate = "" }},
		{"priority too high", func(in *CreateScheduleInput) { in.Priority = 5 }},
		{"priority negative", func(in *CreateScheduleInput) { in.Priority = -1 }},
	}
	for _, c := range cases {
		in := validScheduleInput()
		c.mutate(&in)
		if _, err := CreateSchedule(ctx, pool, slug, projectID, in, actor); !errors.Is(err, ErrInvalidSchedule) {
			t.Errorf("%s: err = %v, want ErrInvalidSchedule", c.name, err)
		}
	}

	// Interval 0 defaults to 1 (documented), not an error.
	in := validScheduleInput()
	in.Interval = 0
	s, err := CreateSchedule(ctx, pool, slug, projectID, in, actor)
	if err != nil {
		t.Fatalf("interval 0: %v", err)
	}
	if s.Interval != 1 {
		t.Fatalf("interval 0 defaulted to %d, want 1", s.Interval)
	}

	// Weekly dedups days.
	in2 := validScheduleInput()
	in2.DaysOfWeek = []int{5, 1, 5}
	s2, err := CreateSchedule(ctx, pool, slug, projectID, in2, actor)
	if err != nil {
		t.Fatalf("dedup days: %v", err)
	}
	if len(s2.DaysOfWeek) != 2 || s2.DaysOfWeek[0] != 1 || s2.DaysOfWeek[1] != 5 {
		t.Fatalf("dedup days = %v", s2.DaysOfWeek)
	}
}

func TestScheduleReferenceValidation(t *testing.T) {
	ctx, pool, slug, actor, projectID, identifier := setupRecurringTest(t)

	// A real state of this project.
	var stateID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM states WHERE project_id = $1::uuid LIMIT 1`, projectID).Scan(&stateID); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	// A real label of this workspace (labels are workspace-scoped).
	label, err := CreateLabel(ctx, pool, slug, identifier, actor, LabelInput{Name: "recurring"})
	if err != nil {
		t.Fatalf("CreateLabel: %v", err)
	}
	// A real workspace member as assignee.
	mate := createTestUser(t, pool, uniqueTestEmail("recur-mate"))
	if err := UpsertMember(ctx, pool, slug, actor, mate, RoleMember); err != nil {
		t.Fatalf("UpsertMember mate: %v", err)
	}

	in := validScheduleInput()
	in.StateID = &stateID
	in.LabelIDs = []string{label.ID}
	in.AssigneeIDs = []string{mate}
	s := mustCreateSchedule(t, ctx, pool, slug, projectID, actor, in)
	if s.StateID == nil || *s.StateID != stateID {
		t.Fatalf("StateID = %v", s.StateID)
	}
	if len(s.LabelIDs) != 1 || s.LabelIDs[0] != label.ID {
		t.Fatalf("LabelIDs = %v", s.LabelIDs)
	}
	if len(s.AssigneeIDs) != 1 || s.AssigneeIDs[0] != mate {
		t.Fatalf("AssigneeIDs = %v", s.AssigneeIDs)
	}

	// Round-trips through Get.
	got, err := GetSchedule(ctx, pool, slug, projectID, s.ID, actor)
	if err != nil {
		t.Fatalf("GetSchedule: %v", err)
	}
	if len(got.LabelIDs) != 1 || len(got.AssigneeIDs) != 1 || got.StateID == nil {
		t.Fatalf("refs lost: %+v", got)
	}

	// Unknown references are clean validation errors, never 500s.
	unknown := "11111111-2222-3333-4444-555555555555"
	for name, mutate := range map[string]func(*CreateScheduleInput){
		"unknown state":    func(in *CreateScheduleInput) { in.StateID = &unknown },
		"unknown label":    func(in *CreateScheduleInput) { in.LabelIDs = []string{unknown} },
		"unknown assignee": func(in *CreateScheduleInput) { in.AssigneeIDs = []string{unknown} },
		"malformed label":  func(in *CreateScheduleInput) { in.LabelIDs = []string{"nope"} },
	} {
		bad := validScheduleInput()
		mutate(&bad)
		if _, err := CreateSchedule(ctx, pool, slug, projectID, bad, actor); !errors.Is(err, ErrInvalidSchedule) {
			t.Errorf("%s: err = %v, want ErrInvalidSchedule", name, err)
		}
	}

	// A state from ANOTHER project is rejected.
	otherProj := createTestProject(t, pool, slug, actor, "Other", "OTH2")
	var foreignState string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM states WHERE project_id = $1::uuid LIMIT 1`, otherProj.ID).Scan(&foreignState); err != nil {
		t.Fatalf("foreign state: %v", err)
	}
	bad := validScheduleInput()
	bad.StateID = &foreignState
	if _, err := CreateSchedule(ctx, pool, slug, projectID, bad, actor); !errors.Is(err, ErrInvalidSchedule) {
		t.Errorf("foreign state: err = %v, want ErrInvalidSchedule", err)
	}

	// Update with a bad label is rejected too.
	upd := UpdateScheduleInput{LabelIDs: &[]string{unknown}}
	if _, err := UpdateSchedule(ctx, pool, slug, projectID, s.ID, actor, upd); !errors.Is(err, ErrInvalidSchedule) {
		t.Errorf("update bad label: err = %v, want ErrInvalidSchedule", err)
	}
}

func boolptr(b bool) *bool { return &b }
