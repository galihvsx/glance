package service

// Initiatives rich filters (C73T0): status multi-value, text search,
// created_by, created_at range, member-project, sort/order, the
// includeArchived backward-compat path, and the non-null empty result.
// Real test database, no skips.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// createFilterInitiative is a small helper: create with a status.
func createFilterInitiative(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug, actor, name, status string) *Initiative {
	t.Helper()
	in, err := CreateInitiative(ctx, pool, slug, actor, InitiativeInput{Name: name, Status: status})
	if err != nil {
		t.Fatalf("CreateInitiative %s: %v", name, err)
	}
	return in
}

func filterNames(list []Initiative) []string {
	names := make([]string, 0, len(list))
	for _, in := range list {
		names = append(names, in.Name)
	}
	return names
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// status values come back ordered by the list sort; this test pins the
// multi-value filter, not the sort.
func TestInitiativeListStatusFilter(t *testing.T) {
	ctx, pool, slug, actor := setupInitiativeTest(t)
	createFilterInitiative(t, ctx, pool, slug, actor, "Active One", "active")
	createFilterInitiative(t, ctx, pool, slug, actor, "Done One", "completed")
	createFilterInitiative(t, ctx, pool, slug, actor, "Old One", "archived")

	// Single status.
	list, err := ListInitiatives(ctx, pool, slug, actor, InitiativeListFilter{Statuses: []string{"active"}})
	if err != nil {
		t.Fatalf("ListInitiatives status=active: %v", err)
	}
	if len(list) != 1 || list[0].Status != "active" {
		t.Fatalf("status=active = %+v, want only Active One", filterNames(list))
	}

	// Multi-value.
	list, err = ListInitiatives(ctx, pool, slug, actor, InitiativeListFilter{Statuses: []string{"active", "completed"}})
	if err != nil {
		t.Fatalf("ListInitiatives status=active,completed: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("status=active,completed = %d, want 2", len(list))
	}

	// Explicit archived-only.
	list, err = ListInitiatives(ctx, pool, slug, actor, InitiativeListFilter{Statuses: []string{"archived"}})
	if err != nil {
		t.Fatalf("ListInitiatives status=archived: %v", err)
	}
	if len(list) != 1 || list[0].Status != "archived" {
		t.Fatalf("status=archived = %+v, want only Old One", filterNames(list))
	}

	// Unknown status value → sentinel, not a 500 or silent ignore.
	if _, err := ListInitiatives(ctx, pool, slug, actor, InitiativeListFilter{Statuses: []string{"bogus"}}); !errors.Is(err, ErrInvalidInitiativeFilter) {
		t.Fatalf("status=bogus err = %v, want ErrInvalidInitiativeFilter", err)
	}
}

func TestInitiativeListQFilter(t *testing.T) {
	ctx, pool, slug, actor := setupInitiativeTest(t)
	createFilterInitiative(t, ctx, pool, slug, actor, "Platform Revamp", "active")
	revamp, err := CreateInitiative(ctx, pool, slug, actor, InitiativeInput{Name: "Other", Description: strptr("Migrate to the new REVAMP stack"), Status: "active"})
	if err != nil {
		t.Fatalf("create desc: %v", err)
	}
	createFilterInitiative(t, ctx, pool, slug, actor, "Unrelated", "active")

	// Name match (case-insensitive).
	list, err := ListInitiatives(ctx, pool, slug, actor, InitiativeListFilter{Q: "revamp"})
	if err != nil {
		t.Fatalf("ListInitiatives q=revamp: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("q=revamp = %d, want 2 (name + description hits)", len(list))
	}
	found := false
	for _, in := range list {
		if in.ID == revamp.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("q=revamp missed the description hit: %+v", filterNames(list))
	}

	// Uppercase query matches lowercase name.
	list, err = ListInitiatives(ctx, pool, slug, actor, InitiativeListFilter{Q: "REVAMP"})
	if err != nil {
		t.Fatalf("ListInitiatives q=REVAMP: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("q=REVAMP = %d, want 2", len(list))
	}

	// No match → empty, never null.
	list, err = ListInitiatives(ctx, pool, slug, actor, InitiativeListFilter{Q: "zzzz-no-such-thing"})
	if err != nil {
		t.Fatalf("ListInitiatives q=nomatch: %v", err)
	}
	if len(list) != 0 || list == nil {
		t.Fatalf("q=nomatch = %+v (nil=%v), want non-nil []", list, list == nil)
	}
}

func TestInitiativeListCreatedByFilter(t *testing.T) {
	ctx, pool, slug, actor := setupInitiativeTest(t)
	createFilterInitiative(t, ctx, pool, slug, actor, "Mine", "active")
	other := createTestUser(t, pool, uniqueTestEmail("init-cb"))
	if err := UpsertMember(ctx, pool, slug, actor, other, RoleMember); err != nil {
		t.Fatalf("UpsertMember: %v", err)
	}
	otherInit, err := CreateInitiative(ctx, pool, slug, other, InitiativeInput{Name: "Theirs"})
	if err != nil {
		t.Fatalf("create theirs: %v", err)
	}

	list, err := ListInitiatives(ctx, pool, slug, actor, InitiativeListFilter{CreatedBy: other})
	if err != nil {
		t.Fatalf("ListInitiatives created_by: %v", err)
	}
	if len(list) != 1 || list[0].ID != otherInit.ID {
		t.Fatalf("created_by = %+v, want only Theirs", filterNames(list))
	}

	// Malformed uuid → sentinel.
	if _, err := ListInitiatives(ctx, pool, slug, actor, InitiativeListFilter{CreatedBy: "not-a-uuid"}); !errors.Is(err, ErrInvalidInitiativeFilter) {
		t.Fatalf("created_by=bogus err = %v, want ErrInvalidInitiativeFilter", err)
	}
}

func TestInitiativeListCreatedRangeFilter(t *testing.T) {
	ctx, pool, slug, actor := setupInitiativeTest(t)
	createFilterInitiative(t, ctx, pool, slug, actor, "Now", "active")

	// Range covering now → hit.
	from := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	list, err := ListInitiatives(ctx, pool, slug, actor, InitiativeListFilter{CreatedFrom: from, CreatedTo: to})
	if err != nil {
		t.Fatalf("ListInitiatives range: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("range covering now = %d, want 1", len(list))
	}

	// From in the future → empty.
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	list, err = ListInitiatives(ctx, pool, slug, actor, InitiativeListFilter{CreatedFrom: future})
	if err != nil {
		t.Fatalf("ListInitiatives from=future: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("from=future = %d, want 0", len(list))
	}

	// Date-only ISO-8601 is accepted too.
	list, err = ListInitiatives(ctx, pool, slug, actor, InitiativeListFilter{CreatedFrom: "2000-01-01"})
	if err != nil {
		t.Fatalf("ListInitiatives date-only: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("date-only from = %d, want 1", len(list))
	}

	// Unparseable → sentinel.
	if _, err := ListInitiatives(ctx, pool, slug, actor, InitiativeListFilter{CreatedFrom: "not-a-date"}); !errors.Is(err, ErrInvalidInitiativeFilter) {
		t.Fatalf("created_from=bogus err = %v, want ErrInvalidInitiativeFilter", err)
	}
	if _, err := ListInitiatives(ctx, pool, slug, actor, InitiativeListFilter{CreatedTo: "2026-13-99"}); !errors.Is(err, ErrInvalidInitiativeFilter) {
		t.Fatalf("created_to=bogus err = %v, want ErrInvalidInitiativeFilter", err)
	}
}

func TestInitiativeListProjectFilter(t *testing.T) {
	ctx, pool, slug, actor := setupInitiativeTest(t)
	identA := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Proj A", identA)
	identB := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Proj B", identB)
	projectIDA := projectIDByIdent(t, pool, slug, identA)
	projectIDB := projectIDByIdent(t, pool, slug, identB)

	inA := createFilterInitiative(t, ctx, pool, slug, actor, "With A", "active")
	inB := createFilterInitiative(t, ctx, pool, slug, actor, "With B", "active")
	createFilterInitiative(t, ctx, pool, slug, actor, "With None", "active")
	if err := AddInitiativeProject(ctx, pool, slug, actor, inA.ID, projectIDA); err != nil {
		t.Fatalf("add A: %v", err)
	}
	if err := AddInitiativeProject(ctx, pool, slug, actor, inB.ID, projectIDB); err != nil {
		t.Fatalf("add B: %v", err)
	}

	list, err := ListInitiatives(ctx, pool, slug, actor, InitiativeListFilter{ProjectID: projectIDA})
	if err != nil {
		t.Fatalf("ListInitiatives project_id: %v", err)
	}
	if len(list) != 1 || list[0].ID != inA.ID {
		t.Fatalf("project_id=A = %+v, want only With A", filterNames(list))
	}

	// Valid uuid, no memberships → empty.
	unknown := "00000000-0000-0000-0000-000000000000"
	list, err = ListInitiatives(ctx, pool, slug, actor, InitiativeListFilter{ProjectID: unknown})
	if err != nil {
		t.Fatalf("ListInitiatives project_id=unknown: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("project_id=unknown = %d, want 0", len(list))
	}

	// Malformed → sentinel.
	if _, err := ListInitiatives(ctx, pool, slug, actor, InitiativeListFilter{ProjectID: "nope"}); !errors.Is(err, ErrInvalidInitiativeFilter) {
		t.Fatalf("project_id=bogus err = %v, want ErrInvalidInitiativeFilter", err)
	}
}

func TestInitiativeListSortOrder(t *testing.T) {
	ctx, pool, slug, actor := setupInitiativeTest(t)
	// Inserted in this order; default sort is created_at desc → reverse.
	createFilterInitiative(t, ctx, pool, slug, actor, "Charlie", "active")
	createFilterInitiative(t, ctx, pool, slug, actor, "Alpha", "active")
	createFilterInitiative(t, ctx, pool, slug, actor, "Bravo", "active")

	list, err := ListInitiatives(ctx, pool, slug, actor, InitiativeListFilter{})
	if err != nil {
		t.Fatalf("ListInitiatives default: %v", err)
	}
	if got := filterNames(list); !sameStrings(got, []string{"Bravo", "Alpha", "Charlie"}) {
		t.Fatalf("default sort = %v, want [Bravo Alpha Charlie] (created_at desc)", got)
	}

	// sort=name → asc by default.
	list, err = ListInitiatives(ctx, pool, slug, actor, InitiativeListFilter{Sort: "name"})
	if err != nil {
		t.Fatalf("ListInitiatives sort=name: %v", err)
	}
	if got := filterNames(list); !sameStrings(got, []string{"Alpha", "Bravo", "Charlie"}) {
		t.Fatalf("sort=name = %v, want [Alpha Bravo Charlie]", got)
	}

	// sort=name&order=desc.
	list, err = ListInitiatives(ctx, pool, slug, actor, InitiativeListFilter{Sort: "name", Order: "desc"})
	if err != nil {
		t.Fatalf("ListInitiatives sort=name order=desc: %v", err)
	}
	if got := filterNames(list); !sameStrings(got, []string{"Charlie", "Bravo", "Alpha"}) {
		t.Fatalf("sort=name order=desc = %v, want [Charlie Bravo Alpha]", got)
	}

	// Invalid sort → sentinel; invalid order → sentinel.
	if _, err := ListInitiatives(ctx, pool, slug, actor, InitiativeListFilter{Sort: "bogus"}); !errors.Is(err, ErrInvalidInitiativeFilter) {
		t.Fatalf("sort=bogus err = %v, want ErrInvalidInitiativeFilter", err)
	}
	if _, err := ListInitiatives(ctx, pool, slug, actor, InitiativeListFilter{Order: "sideways"}); !errors.Is(err, ErrInvalidInitiativeFilter) {
		t.Fatalf("order=sideways err = %v, want ErrInvalidInitiativeFilter", err)
	}
}

func TestInitiativeListSortProjectCount(t *testing.T) {
	ctx, pool, slug, actor := setupInitiativeTest(t)
	identA := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Proj A", identA)
	identB := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Proj B", identB)
	projectIDA := projectIDByIdent(t, pool, slug, identA)
	projectIDB := projectIDByIdent(t, pool, slug, identB)

	heavy := createFilterInitiative(t, ctx, pool, slug, actor, "Heavy", "active")
	light := createFilterInitiative(t, ctx, pool, slug, actor, "Light", "active")
	if err := AddInitiativeProject(ctx, pool, slug, actor, heavy.ID, projectIDA); err != nil {
		t.Fatalf("add A: %v", err)
	}
	if err := AddInitiativeProject(ctx, pool, slug, actor, heavy.ID, projectIDB); err != nil {
		t.Fatalf("add B: %v", err)
	}
	if err := AddInitiativeProject(ctx, pool, slug, actor, light.ID, projectIDA); err != nil {
		t.Fatalf("add A to light: %v", err)
	}

	list, err := ListInitiatives(ctx, pool, slug, actor, InitiativeListFilter{Sort: "project_count"})
	if err != nil {
		t.Fatalf("ListInitiatives sort=project_count: %v", err)
	}
	if got := filterNames(list); !sameStrings(got, []string{"Heavy", "Light"}) {
		t.Fatalf("sort=project_count = %v, want [Heavy Light]", got)
	}
	if list[0].ProjectCount != 2 || list[1].ProjectCount != 1 {
		t.Fatalf("project counts = %d/%d, want 2/1", list[0].ProjectCount, list[1].ProjectCount)
	}
}

func TestInitiativeListFilterCombo(t *testing.T) {
	ctx, pool, slug, actor := setupInitiativeTest(t)
	identA := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Proj A", identA)
	projectIDA := projectIDByIdent(t, pool, slug, identA)

	hit := createFilterInitiative(t, ctx, pool, slug, actor, "Platform Revamp", "completed")
	createFilterInitiative(t, ctx, pool, slug, actor, "Platform Redo", "active")
	createFilterInitiative(t, ctx, pool, slug, actor, "Other Revamp", "completed")
	if err := AddInitiativeProject(ctx, pool, slug, actor, hit.ID, projectIDA); err != nil {
		t.Fatalf("add: %v", err)
	}

	list, err := ListInitiatives(ctx, pool, slug, actor, InitiativeListFilter{
		Statuses:  []string{"completed"},
		Q:         "revamp",
		ProjectID: projectIDA,
	})
	if err != nil {
		t.Fatalf("ListInitiatives combo: %v", err)
	}
	if len(list) != 1 || list[0].ID != hit.ID {
		t.Fatalf("combo = %+v, want only Platform Revamp", filterNames(list))
	}
}

func TestInitiativeListIncludeArchivedBackwardCompat(t *testing.T) {
	ctx, pool, slug, actor := setupInitiativeTest(t)
	createFilterInitiative(t, ctx, pool, slug, actor, "Active", "active")
	createFilterInitiative(t, ctx, pool, slug, actor, "Old", "archived")

	// Legacy path: includeArchived=true maps to no status exclusion.
	list, err := ListInitiatives(ctx, pool, slug, actor, InitiativeListFilter{IncludeArchived: true})
	if err != nil {
		t.Fatalf("ListInitiatives includeArchived: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("includeArchived = %d, want 2", len(list))
	}

	// Default still excludes archived.
	list, err = ListInitiatives(ctx, pool, slug, actor, InitiativeListFilter{})
	if err != nil {
		t.Fatalf("ListInitiatives default: %v", err)
	}
	if len(list) != 1 || list[0].Name != "Active" {
		t.Fatalf("default = %+v, want only Active", filterNames(list))
	}

	// Explicit status wins over includeArchived.
	list, err = ListInitiatives(ctx, pool, slug, actor, InitiativeListFilter{IncludeArchived: true, Statuses: []string{"archived"}})
	if err != nil {
		t.Fatalf("ListInitiatives status+includeArchived: %v", err)
	}
	if len(list) != 1 || list[0].Name != "Old" {
		t.Fatalf("status=archived+includeArchived = %+v, want only Old", filterNames(list))
	}
}

func TestInitiativeListEmptyResultIsNonNil(t *testing.T) {
	ctx, pool, slug, actor := setupInitiativeTest(t)
	createFilterInitiative(t, ctx, pool, slug, actor, "Something", "active")

	list, err := ListInitiatives(ctx, pool, slug, actor, InitiativeListFilter{Q: "nothing-matches-this"})
	if err != nil {
		t.Fatalf("ListInitiatives: %v", err)
	}
	if list == nil || len(list) != 0 {
		t.Fatalf("empty list = %+v (nil=%v), want non-nil []", list, list == nil)
	}
}
