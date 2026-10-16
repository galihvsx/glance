package service

// OKRs (C35T0): objective CRUD, KR progress math (metric clamp +
// zero-span rejection, issue-KR rollup incl. 0-links → 0, objective
// mean), title-conflict 409-equivalent, role gates, link idempotency,
// metric-KR link → 400, cross-workspace link → 404, tenancy.

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func setupOKRTest(t *testing.T) (context.Context, *pgxpool.Pool, string, string) {
	t.Helper()
	ctx := context.Background()
	pool := newTestPool(t)
	actor := createTestUser(t, pool, uniqueTestEmail("okr"))
	slug := uniqueTestSlug("okr-ws")
	createTestWorkspace(t, pool, "OKR Co", slug, actor)
	return ctx, pool, slug, actor
}

func setupOKRGuest(t *testing.T, pool *pgxpool.Pool, slug, adminID string) string {
	t.Helper()
	guest := createTestUser(t, pool, uniqueTestEmail("okr-guest"))
	if err := UpsertMember(context.Background(), pool, slug, adminID, guest, RoleGuest); err != nil {
		t.Fatalf("UpsertMember guest: %v", err)
	}
	return guest
}

func fptr(v float64) *float64 { return &v }

func TestObjectiveCRUD(t *testing.T) {
	ctx, pool, slug, actor := setupOKRTest(t)

	desc := "Grow the business"
	owner := actor
	start := "2026-01-01"
	target := "2026-03-31"
	ob, err := CreateObjective(ctx, pool, slug, actor, ObjectiveInput{
		Title:       "Grow revenue",
		Description: &desc,
		OwnerID:     &owner,
		StartDate:   &start,
		TargetDate:  &target,
	})
	if err != nil {
		t.Fatalf("CreateObjective: %v", err)
	}
	if ob.Title != "Grow revenue" || ob.Status != "active" {
		t.Fatalf("got %+v", ob)
	}
	if ob.Description == nil || *ob.Description != desc {
		t.Fatalf("description = %v", ob.Description)
	}
	if ob.Owner == nil || ob.Owner.ID != actor {
		t.Fatalf("owner = %+v", ob.Owner)
	}
	if ob.StartDate == nil || *ob.StartDate != start || ob.TargetDate == nil || *ob.TargetDate != target {
		t.Fatalf("dates = %v / %v", ob.StartDate, ob.TargetDate)
	}
	if ob.Progress != 0 || ob.KeyResultCount != 0 {
		t.Fatalf("fresh objective progress/count = %v / %d", ob.Progress, ob.KeyResultCount)
	}

	got, err := GetObjective(ctx, pool, slug, actor, ob.ID)
	if err != nil {
		t.Fatalf("GetObjective: %v", err)
	}
	if got.ID != ob.ID {
		t.Fatalf("got %+v", got)
	}

	list, err := ListObjectives(ctx, pool, slug, actor)
	if err != nil {
		t.Fatalf("ListObjectives: %v", err)
	}
	if len(list) != 1 || list[0].ID != ob.ID {
		t.Fatalf("list = %+v", list)
	}

	// PATCH: title + status + clear description + clear dates.
	updated, err := UpdateObjective(ctx, pool, slug, actor, ob.ID, ObjectivePatch{
		Title:       strptr("Grow revenue 2x"),
		Status:      strptr("completed"),
		Description: strptr(""),
		StartDate:   strptr(""),
		TargetDate:  strptr(""),
	})
	if err != nil {
		t.Fatalf("UpdateObjective: %v", err)
	}
	if updated.Title != "Grow revenue 2x" || updated.Status != "completed" {
		t.Fatalf("updated = %+v", updated)
	}
	if updated.Description != nil {
		t.Fatalf("description not cleared: %v", updated.Description)
	}
	if updated.StartDate != nil || updated.TargetDate != nil {
		t.Fatalf("dates not cleared: %v / %v", updated.StartDate, updated.TargetDate)
	}

	// PATCH: bad status → 400-equivalent; empty title → name required;
	// empty patch → nothing to update.
	if _, err := UpdateObjective(ctx, pool, slug, actor, ob.ID, ObjectivePatch{Status: strptr("bogus")}); !errors.Is(err, ErrInvalidObjective) {
		t.Fatalf("bad status err = %v, want ErrInvalidObjective", err)
	}
	if _, err := UpdateObjective(ctx, pool, slug, actor, ob.ID, ObjectivePatch{Title: strptr("  ")}); !errors.Is(err, ErrNameRequired) {
		t.Fatalf("empty title err = %v, want ErrNameRequired", err)
	}
	if _, err := UpdateObjective(ctx, pool, slug, actor, ob.ID, ObjectivePatch{}); !errors.Is(err, ErrNothingToUpdate) {
		t.Fatalf("empty patch err = %v, want ErrNothingToUpdate", err)
	}

	// Create validation: empty title, bad status, bad date, unknown owner.
	if _, err := CreateObjective(ctx, pool, slug, actor, ObjectiveInput{Title: "  "}); !errors.Is(err, ErrNameRequired) {
		t.Fatalf("empty title create err = %v, want ErrNameRequired", err)
	}
	if _, err := CreateObjective(ctx, pool, slug, actor, ObjectiveInput{Title: "X", Status: "bogus"}); !errors.Is(err, ErrInvalidObjective) {
		t.Fatalf("bad status create err = %v, want ErrInvalidObjective", err)
	}
	if _, err := CreateObjective(ctx, pool, slug, actor, ObjectiveInput{Title: "X", StartDate: strptr("2026-13-01")}); !errors.Is(err, ErrInvalidObjective) {
		t.Fatalf("bad date create err = %v, want ErrInvalidObjective", err)
	}
	if _, err := CreateObjective(ctx, pool, slug, actor, ObjectiveInput{Title: "X", OwnerID: strptr("00000000-0000-0000-0000-000000000000")}); !errors.Is(err, ErrInvalidObjective) {
		t.Fatalf("unknown owner create err = %v, want ErrInvalidObjective", err)
	}
	if _, err := CreateObjective(ctx, pool, slug, actor, ObjectiveInput{Title: "X", OwnerID: strptr("not-a-uuid")}); !errors.Is(err, ErrInvalidObjective) {
		t.Fatalf("malformed owner create err = %v, want ErrInvalidObjective", err)
	}

	// Duplicate title → 409-equivalent (title was patched, so the
	// duplicate uses the new title).
	if _, err := CreateObjective(ctx, pool, slug, actor, ObjectiveInput{Title: "Grow revenue 2x"}); !errors.Is(err, ErrObjectiveConflict) {
		t.Fatalf("duplicate title err = %v, want ErrObjectiveConflict", err)
	}
	if _, err := CreateObjective(ctx, pool, slug, actor, ObjectiveInput{Title: "Other"}); err != nil {
		t.Fatalf("second objective: %v", err)
	}
	other, err := CreateObjective(ctx, pool, slug, actor, ObjectiveInput{Title: "Third"})
	if err != nil {
		t.Fatalf("third objective: %v", err)
	}
	if _, err := UpdateObjective(ctx, pool, slug, actor, other.ID, ObjectivePatch{Title: strptr("Grow revenue 2x")}); !errors.Is(err, ErrObjectiveConflict) {
		t.Fatalf("patch duplicate title err = %v, want ErrObjectiveConflict", err)
	}

	// Delete → gone.
	if err := DeleteObjective(ctx, pool, slug, actor, ob.ID); err != nil {
		t.Fatalf("DeleteObjective: %v", err)
	}
	if _, err := GetObjective(ctx, pool, slug, actor, ob.ID); !errors.Is(err, ErrObjectiveNotFound) {
		t.Fatalf("deleted get err = %v, want ErrObjectiveNotFound", err)
	}
	if err := DeleteObjective(ctx, pool, slug, actor, ob.ID); !errors.Is(err, ErrObjectiveNotFound) {
		t.Fatalf("second delete err = %v, want ErrObjectiveNotFound", err)
	}

	// Malformed id → 400-equivalent, not a 500.
	if _, err := GetObjective(ctx, pool, slug, actor, "not-a-uuid"); !errors.Is(err, ErrInvalidObjectiveID) {
		t.Fatalf("malformed id err = %v, want ErrInvalidObjectiveID", err)
	}

	// Role gates: guest may read, not write.
	guest := setupOKRGuest(t, pool, slug, actor)
	if _, err := ListObjectives(ctx, pool, slug, guest); err != nil {
		t.Fatalf("guest list: %v", err)
	}
	if _, err := CreateObjective(ctx, pool, slug, guest, ObjectiveInput{Title: "Guest goal"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest create err = %v, want ErrForbidden", err)
	}
	if err := DeleteObjective(ctx, pool, slug, guest, other.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest delete err = %v, want ErrForbidden", err)
	}

	// Tenancy: non-member → ErrNotFound; other workspace → 404.
	if _, err := ListObjectives(ctx, pool, "no-such-ws", actor); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bad slug err = %v, want ErrNotFound", err)
	}
	otherActor := createTestUser(t, pool, uniqueTestEmail("okr-x"))
	otherSlug := uniqueTestSlug("okr-ws-x")
	createTestWorkspace(t, pool, "X Co", otherSlug, otherActor)
	if _, err := GetObjective(ctx, pool, otherSlug, otherActor, other.ID); !errors.Is(err, ErrObjectiveNotFound) {
		t.Fatalf("cross-workspace get err = %v, want ErrObjectiveNotFound", err)
	}
}

func TestMetricKRProgress(t *testing.T) {
	ctx, pool, slug, actor := setupOKRTest(t)

	ob, err := CreateObjective(ctx, pool, slug, actor, ObjectiveInput{Title: "Metrics"})
	if err != nil {
		t.Fatalf("CreateObjective: %v", err)
	}

	// 0 → 100, current 25 → 0.25.
	unit := "%"
	kr, err := CreateKeyResult(ctx, pool, slug, actor, ob.ID, KeyResultInput{
		Title: "NPS", Kind: "metric", Unit: &unit,
		StartValue: fptr(0), TargetValue: fptr(100), CurrentValue: fptr(25),
	})
	if err != nil {
		t.Fatalf("CreateKeyResult: %v", err)
	}
	if kr.Progress != 0.25 {
		t.Fatalf("progress = %v, want 0.25", kr.Progress)
	}
	if kr.Unit == nil || *kr.Unit != "%" {
		t.Fatalf("unit = %v", kr.Unit)
	}

	// Clamp: below start → 0, above target → 1.
	low, err := CreateKeyResult(ctx, pool, slug, actor, ob.ID, KeyResultInput{
		Title: "Low", Kind: "metric",
		StartValue: fptr(10), TargetValue: fptr(20), CurrentValue: fptr(5),
	})
	if err != nil {
		t.Fatalf("CreateKeyResult low: %v", err)
	}
	if low.Progress != 0 {
		t.Fatalf("below-start progress = %v, want 0", low.Progress)
	}
	high, err := CreateKeyResult(ctx, pool, slug, actor, ob.ID, KeyResultInput{
		Title: "High", Kind: "metric",
		StartValue: fptr(10), TargetValue: fptr(20), CurrentValue: fptr(99),
	})
	if err != nil {
		t.Fatalf("CreateKeyResult high: %v", err)
	}
	if high.Progress != 1 {
		t.Fatalf("above-target progress = %v, want 1", high.Progress)
	}

	// Descending metric: start 100 → target 0 (reduce churn), current
	// 50 → 0.5.
	desc, err := CreateKeyResult(ctx, pool, slug, actor, ob.ID, KeyResultInput{
		Title: "Churn", Kind: "metric",
		StartValue: fptr(100), TargetValue: fptr(0), CurrentValue: fptr(50),
	})
	if err != nil {
		t.Fatalf("CreateKeyResult descending: %v", err)
	}
	if desc.Progress != 0.5 {
		t.Fatalf("descending progress = %v, want 0.5", desc.Progress)
	}

	// Zero-span rejected at write (create + patch).
	if _, err := CreateKeyResult(ctx, pool, slug, actor, ob.ID, KeyResultInput{
		Title: "Flat", Kind: "metric",
		StartValue: fptr(50), TargetValue: fptr(50),
	}); !errors.Is(err, ErrInvalidKeyResult) {
		t.Fatalf("zero-span create err = %v, want ErrInvalidKeyResult", err)
	}
	if _, err := UpdateKeyResult(ctx, pool, slug, actor, ob.ID, kr.ID, KeyResultPatch{
		TargetValue: fptr(0), // start is 0 → zero span
	}); !errors.Is(err, ErrInvalidKeyResult) {
		t.Fatalf("zero-span patch err = %v, want ErrInvalidKeyResult", err)
	}

	// Missing target → 400-equivalent.
	if _, err := CreateKeyResult(ctx, pool, slug, actor, ob.ID, KeyResultInput{
		Title: "NoTarget", Kind: "metric",
	}); !errors.Is(err, ErrInvalidKeyResult) {
		t.Fatalf("missing target err = %v, want ErrInvalidKeyResult", err)
	}

	// Bad kind → 400-equivalent; empty title → name required.
	if _, err := CreateKeyResult(ctx, pool, slug, actor, ob.ID, KeyResultInput{
		Title: "Bad", Kind: "vibes", TargetValue: fptr(1),
	}); !errors.Is(err, ErrInvalidKeyResult) {
		t.Fatalf("bad kind err = %v, want ErrInvalidKeyResult", err)
	}
	if _, err := CreateKeyResult(ctx, pool, slug, actor, ob.ID, KeyResultInput{
		Title: "  ", Kind: "metric", TargetValue: fptr(1),
	}); !errors.Is(err, ErrNameRequired) {
		t.Fatalf("empty KR title err = %v, want ErrNameRequired", err)
	}

	// PATCH: current_value moves progress; unit clears with "".
	moved, err := UpdateKeyResult(ctx, pool, slug, actor, ob.ID, kr.ID, KeyResultPatch{
		CurrentValue: fptr(50), Unit: strptr(""),
	})
	if err != nil {
		t.Fatalf("UpdateKeyResult: %v", err)
	}
	if moved.Progress != 0.5 {
		t.Fatalf("patched progress = %v, want 0.5", moved.Progress)
	}
	if moved.Unit != nil {
		t.Fatalf("unit not cleared: %v", moved.Unit)
	}

	// PATCH: kind change → 400; empty patch → nothing to update.
	if _, err := UpdateKeyResult(ctx, pool, slug, actor, ob.ID, kr.ID, KeyResultPatch{
		Kind: strptr("issue"),
	}); !errors.Is(err, ErrInvalidKeyResult) {
		t.Fatalf("kind change err = %v, want ErrInvalidKeyResult", err)
	}
	if _, err := UpdateKeyResult(ctx, pool, slug, actor, ob.ID, kr.ID, KeyResultPatch{}); !errors.Is(err, ErrNothingToUpdate) {
		t.Fatalf("empty KR patch err = %v, want ErrNothingToUpdate", err)
	}

	// Objective progress = mean of KR progress: (0.5 + 0 + 1 + 0.5) / 4.
	got, err := GetObjective(ctx, pool, slug, actor, ob.ID)
	if err != nil {
		t.Fatalf("GetObjective: %v", err)
	}
	if got.Progress != 0.5 {
		t.Fatalf("objective progress = %v, want 0.5", got.Progress)
	}
	if got.KeyResultCount != 4 {
		t.Fatalf("kr count = %d, want 4", got.KeyResultCount)
	}
	if len(got.KeyResults) != 4 {
		t.Fatalf("key_results = %d, want 4", len(got.KeyResults))
	}

	// Delete KR → progress recomputed over the rest.
	if err := DeleteKeyResult(ctx, pool, slug, actor, ob.ID, low.ID); err != nil {
		t.Fatalf("DeleteKeyResult: %v", err)
	}
	got, err = GetObjective(ctx, pool, slug, actor, ob.ID)
	if err != nil {
		t.Fatalf("GetObjective after delete: %v", err)
	}
	// (0.5 + 1 + 0.5) / 3 = 2/3.
	if got.Progress < 0.6666 || got.Progress > 0.6667 {
		t.Fatalf("objective progress after delete = %v, want 2/3", got.Progress)
	}
	if err := DeleteKeyResult(ctx, pool, slug, actor, ob.ID, low.ID); !errors.Is(err, ErrKeyResultNotFound) {
		t.Fatalf("second KR delete err = %v, want ErrKeyResultNotFound", err)
	}
	if _, err := UpdateKeyResult(ctx, pool, slug, actor, ob.ID, "not-a-uuid", KeyResultPatch{Title: strptr("x")}); !errors.Is(err, ErrInvalidObjectiveID) {
		t.Fatalf("malformed KR id err = %v, want ErrInvalidObjectiveID", err)
	}

	// Guest may read KRs via GetObjective, not mutate them.
	guest := setupOKRGuest(t, pool, slug, actor)
	if _, err := GetObjective(ctx, pool, slug, guest, ob.ID); err != nil {
		t.Fatalf("guest get: %v", err)
	}
	if _, err := CreateKeyResult(ctx, pool, slug, guest, ob.ID, KeyResultInput{
		Title: "Guest KR", Kind: "metric", TargetValue: fptr(1),
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest KR create err = %v, want ErrForbidden", err)
	}
}

func TestIssueKRProgressAndLinks(t *testing.T) {
	ctx, pool, slug, actor := setupOKRTest(t)

	ident := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Alpha", ident)
	doneState, err := CreateState(ctx, pool, slug, ident, actor, StateInput{Name: "Finished", Group: "completed", Color: "green"})
	if err != nil {
		t.Fatalf("CreateState done: %v", err)
	}
	backlogID := backlogStateID(t, pool, slug, ident, actor)

	iss1, err := CreateIssue(ctx, pool, slug, ident, actor, CreateIssueInput{Name: "i1", StateID: &backlogID})
	if err != nil {
		t.Fatalf("create i1: %v", err)
	}
	iss2, err := CreateIssue(ctx, pool, slug, ident, actor, CreateIssueInput{Name: "i2", StateID: &doneState.ID})
	if err != nil {
		t.Fatalf("create i2: %v", err)
	}

	ob, err := CreateObjective(ctx, pool, slug, actor, ObjectiveInput{Title: "Ship"})
	if err != nil {
		t.Fatalf("CreateObjective: %v", err)
	}

	// Issue KR with no links → progress 0.
	kr, err := CreateKeyResult(ctx, pool, slug, actor, ob.ID, KeyResultInput{
		Title: "Ship issues", Kind: "issue", TargetValue: fptr(100),
	})
	if err != nil {
		t.Fatalf("CreateKeyResult issue: %v", err)
	}
	if kr.Progress != 0 {
		t.Fatalf("unlinked progress = %v, want 0", kr.Progress)
	}

	// Metric-only fields on an issue KR → 400 (create + patch).
	if _, err := CreateKeyResult(ctx, pool, slug, actor, ob.ID, KeyResultInput{
		Title: "Bad unit", Kind: "issue", Unit: strptr("%"), TargetValue: fptr(100),
	}); !errors.Is(err, ErrInvalidKeyResult) {
		t.Fatalf("issue KR with unit err = %v, want ErrInvalidKeyResult", err)
	}
	if _, err := CreateKeyResult(ctx, pool, slug, actor, ob.ID, KeyResultInput{
		Title: "Bad current", Kind: "issue", CurrentValue: fptr(5), TargetValue: fptr(100),
	}); !errors.Is(err, ErrInvalidKeyResult) {
		t.Fatalf("issue KR with current err = %v, want ErrInvalidKeyResult", err)
	}
	if _, err := UpdateKeyResult(ctx, pool, slug, actor, ob.ID, kr.ID, KeyResultPatch{
		CurrentValue: fptr(5),
	}); !errors.Is(err, ErrInvalidKeyResult) {
		t.Fatalf("issue KR patch current err = %v, want ErrInvalidKeyResult", err)
	}

	// Link both issues: 1 of 2 done → 0.5. Link is idempotent.
	if err := LinkIssueToKeyResult(ctx, pool, slug, actor, ob.ID, kr.ID, iss1.ID); err != nil {
		t.Fatalf("link i1: %v", err)
	}
	if err := LinkIssueToKeyResult(ctx, pool, slug, actor, ob.ID, kr.ID, iss2.ID); err != nil {
		t.Fatalf("link i2: %v", err)
	}
	if err := LinkIssueToKeyResult(ctx, pool, slug, actor, ob.ID, kr.ID, iss1.ID); err != nil {
		t.Fatalf("re-link i1 (idempotent): %v", err)
	}
	got, err := GetObjective(ctx, pool, slug, actor, ob.ID)
	if err != nil {
		t.Fatalf("GetObjective: %v", err)
	}
	if len(got.KeyResults) != 1 || got.KeyResults[0].Progress != 0.5 {
		t.Fatalf("issue KR progress = %+v, want 0.5", got.KeyResults)
	}
	if got.Progress != 0.5 {
		t.Fatalf("objective progress = %v, want 0.5", got.Progress)
	}
	linked := got.KeyResults[0].LinkedIssues
	if len(linked) != 2 {
		t.Fatalf("linked_issues = %d, want 2", len(linked))
	}
	byID := map[string]LinkedIssue{}
	for _, li := range linked {
		byID[li.ID] = li
	}
	if byID[iss1.ID].StateGroup != "backlog" || byID[iss1.ID].DisplayID == "" {
		t.Fatalf("i1 linked = %+v", byID[iss1.ID])
	}
	if byID[iss2.ID].StateGroup != "completed" {
		t.Fatalf("i2 linked = %+v", byID[iss2.ID])
	}

	// Unlink the done issue → 0 of 1 → 0. Unlink again is a no-op.
	if err := UnlinkIssueFromKeyResult(ctx, pool, slug, actor, ob.ID, kr.ID, iss2.ID); err != nil {
		t.Fatalf("unlink i2: %v", err)
	}
	if err := UnlinkIssueFromKeyResult(ctx, pool, slug, actor, ob.ID, kr.ID, iss2.ID); err != nil {
		t.Fatalf("re-unlink i2 (idempotent): %v", err)
	}
	got, err = GetObjective(ctx, pool, slug, actor, ob.ID)
	if err != nil {
		t.Fatalf("GetObjective after unlink: %v", err)
	}
	if got.KeyResults[0].Progress != 0 {
		t.Fatalf("progress after unlink = %v, want 0", got.KeyResults[0].Progress)
	}

	// Linking to a metric KR → 400.
	mkr, err := CreateKeyResult(ctx, pool, slug, actor, ob.ID, KeyResultInput{
		Title: "Metric", Kind: "metric", TargetValue: fptr(10),
	})
	if err != nil {
		t.Fatalf("CreateKeyResult metric: %v", err)
	}
	if err := LinkIssueToKeyResult(ctx, pool, slug, actor, ob.ID, mkr.ID, iss1.ID); !errors.Is(err, ErrInvalidKeyResult) {
		t.Fatalf("metric link err = %v, want ErrInvalidKeyResult", err)
	}

	// Cross-workspace issue → 404. Unknown issue → 404. Malformed → 400.
	otherActor := createTestUser(t, pool, uniqueTestEmail("okr-xws"))
	otherSlug := uniqueTestSlug("okr-ws-xws")
	createTestWorkspace(t, pool, "X Co", otherSlug, otherActor)
	otherIdent := uniqueTestIdentifier()
	createTestProject(t, pool, otherSlug, otherActor, "Foreign", otherIdent)
	foreign, err := CreateIssue(ctx, pool, otherSlug, otherIdent, otherActor, CreateIssueInput{Name: "fx"})
	if err != nil {
		t.Fatalf("create foreign issue: %v", err)
	}
	if err := LinkIssueToKeyResult(ctx, pool, slug, actor, ob.ID, kr.ID, foreign.ID); !errors.Is(err, ErrIssueNotFound) {
		t.Fatalf("cross-workspace link err = %v, want ErrIssueNotFound", err)
	}
	if err := LinkIssueToKeyResult(ctx, pool, slug, actor, ob.ID, kr.ID, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrIssueNotFound) {
		t.Fatalf("unknown issue link err = %v, want ErrIssueNotFound", err)
	}
	if err := LinkIssueToKeyResult(ctx, pool, slug, actor, ob.ID, kr.ID, "not-a-uuid"); !errors.Is(err, ErrInvalidObjectiveID) {
		t.Fatalf("malformed issue link err = %v, want ErrInvalidObjectiveID", err)
	}
	if err := UnlinkIssueFromKeyResult(ctx, pool, slug, actor, ob.ID, kr.ID, "not-a-uuid"); !errors.Is(err, ErrInvalidObjectiveID) {
		t.Fatalf("malformed unlink err = %v, want ErrInvalidObjectiveID", err)
	}

	// Guest may not link/unlink.
	guest := setupOKRGuest(t, pool, slug, actor)
	if err := LinkIssueToKeyResult(ctx, pool, slug, guest, ob.ID, kr.ID, iss1.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest link err = %v, want ErrForbidden", err)
	}

	// Deleting the objective cascades KRs (links die with them).
	if err := DeleteObjective(ctx, pool, slug, actor, ob.ID); err != nil {
		t.Fatalf("DeleteObjective: %v", err)
	}
	var krCount int
	err = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM key_results WHERE objective_id = $1::uuid`, ob.ID).Scan(&krCount)
	if err != nil {
		t.Fatalf("kr count after objective delete: %v", err)
	}
	if krCount != 0 {
		t.Fatalf("kr count after objective delete = %d, want 0", krCount)
	}
}
