package service

// Epics rich filters (C74T0): status multi-value (active|archived),
// text search, created_by, created_at range, linked-initiative filter,
// sort/order, the includeArchived backward-compat path, and the non-null
// empty result. Real test database, no skips.

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// createFilterEpic is a small helper: create an epic by name.
func createFilterEpic(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug, ident, actor, name string) *Epic {
	t.Helper()
	e, err := CreateEpic(ctx, pool, slug, ident, actor, EpicInput{Name: name})
	if err != nil {
		t.Fatalf("CreateEpic %s: %v", name, err)
	}
	return e
}

// setEpicTimestamps pins created_at/updated_at so ordering tests are
// deterministic (wall-clock ties would make them flaky).
func setEpicTimestamps(t *testing.T, ctx context.Context, pool *pgxpool.Pool, epicID, ts string) {
	t.Helper()
	if _, err := pool.Exec(ctx,
		`UPDATE epics SET created_at = $2::timestamptz, updated_at = $2::timestamptz WHERE id = $1::uuid`,
		epicID, ts); err != nil {
		t.Fatalf("pin timestamps: %v", err)
	}
}

func epicFilterNames(list []Epic) []string {
	names := make([]string, 0, len(list))
	for _, e := range list {
		names = append(names, e.Name)
	}
	return names
}

// status values come back ordered by the list sort; this test pins the
// multi-value filter, not the sort.
func TestEpicListStatusFilter(t *testing.T) {
	ctx, pool, slug, ident, actor := setupEpicTest(t)
	createFilterEpic(t, ctx, pool, slug, ident, actor, "Active One")
	createFilterEpic(t, ctx, pool, slug, ident, actor, "Active Two")
	old := createFilterEpic(t, ctx, pool, slug, ident, actor, "Old One")
	if _, err := ArchiveEpic(ctx, pool, slug, ident, actor, old.ID); err != nil {
		t.Fatalf("ArchiveEpic: %v", err)
	}

	// Single status.
	list, err := ListEpics(ctx, pool, slug, ident, actor, EpicListFilter{Statuses: []string{"active"}})
	if err != nil {
		t.Fatalf("ListEpics status=active: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("status=active = %v, want 2", epicFilterNames(list))
	}

	// Archived-only.
	list, err = ListEpics(ctx, pool, slug, ident, actor, EpicListFilter{Statuses: []string{"archived"}})
	if err != nil {
		t.Fatalf("ListEpics status=archived: %v", err)
	}
	if len(list) != 1 || list[0].Name != "Old One" {
		t.Fatalf("status=archived = %v, want [Old One]", epicFilterNames(list))
	}

	// Multi-value: everything.
	list, err = ListEpics(ctx, pool, slug, ident, actor, EpicListFilter{Statuses: []string{"active", "archived"}})
	if err != nil {
		t.Fatalf("ListEpics status=active,archived: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("status=active,archived = %v, want 3", epicFilterNames(list))
	}

	// Unknown status is a client error, not a silently-empty list.
	if _, err := ListEpics(ctx, pool, slug, ident, actor, EpicListFilter{Statuses: []string{"completed"}}); !errors.Is(err, ErrInvalidEpicFilter) {
		t.Fatalf("status=completed: err = %v, want ErrInvalidEpicFilter", err)
	}

	// Empty explicit value is garbage too (C73 ruling: explicit garbage
	// is a client error).
	if _, err := ListEpics(ctx, pool, slug, ident, actor, EpicListFilter{Statuses: []string{""}}); !errors.Is(err, ErrInvalidEpicFilter) {
		t.Fatalf("status='': err = %v, want ErrInvalidEpicFilter", err)
	}
}

func TestEpicListSearchQ(t *testing.T) {
	ctx, pool, slug, ident, actor := setupEpicTest(t)
	desc := "The long-running overhaul"
	if _, err := CreateEpic(ctx, pool, slug, ident, actor, EpicInput{Name: "Platform Revamp", Description: &desc}); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	createFilterEpic(t, ctx, pool, slug, ident, actor, "Boring Thing")

	// Name match, case-insensitive.
	list, err := ListEpics(ctx, pool, slug, ident, actor, EpicListFilter{Q: "revamp"})
	if err != nil {
		t.Fatalf("ListEpics q: %v", err)
	}
	if len(list) != 1 || list[0].Name != "Platform Revamp" {
		t.Fatalf("q=revamp = %v, want [Platform Revamp]", epicFilterNames(list))
	}

	// Description match.
	list, err = ListEpics(ctx, pool, slug, ident, actor, EpicListFilter{Q: "overhaul"})
	if err != nil {
		t.Fatalf("ListEpics q description: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("q=overhaul = %v, want 1", epicFilterNames(list))
	}

	// Uppercase query still matches (ILIKE).
	list, err = ListEpics(ctx, pool, slug, ident, actor, EpicListFilter{Q: "REVAMP"})
	if err != nil {
		t.Fatalf("ListEpics q upper: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("q=REVAMP = %v, want 1", epicFilterNames(list))
	}
}

func TestEpicListCreatedBy(t *testing.T) {
	ctx, pool, slug, ident, actor := setupEpicTest(t)
	other := createTestUser(t, pool, uniqueTestEmail("epic-other"))
	if err := UpsertMember(ctx, pool, slug, actor, other, RoleMember); err != nil {
		t.Fatalf("UpsertMember: %v", err)
	}
	createFilterEpic(t, ctx, pool, slug, ident, actor, "Mine")
	createFilterEpic(t, ctx, pool, slug, ident, other, "Theirs")

	list, err := ListEpics(ctx, pool, slug, ident, actor, EpicListFilter{CreatedBy: actor})
	if err != nil {
		t.Fatalf("ListEpics created_by: %v", err)
	}
	if len(list) != 1 || list[0].Name != "Mine" {
		t.Fatalf("created_by=actor = %v, want [Mine]", epicFilterNames(list))
	}

	// Malformed uuid is a client error, not a ::uuid 500.
	if _, err := ListEpics(ctx, pool, slug, ident, actor, EpicListFilter{CreatedBy: "xyz"}); !errors.Is(err, ErrInvalidEpicFilter) {
		t.Fatalf("created_by=xyz: err = %v, want ErrInvalidEpicFilter", err)
	}
}

func TestEpicListDateRange(t *testing.T) {
	ctx, pool, slug, ident, actor := setupEpicTest(t)
	jan := createFilterEpic(t, ctx, pool, slug, ident, actor, "Jan Epic")
	setEpicTimestamps(t, ctx, pool, jan.ID, "2024-01-01T00:00:00Z")
	jun := createFilterEpic(t, ctx, pool, slug, ident, actor, "Jun Epic")
	setEpicTimestamps(t, ctx, pool, jun.ID, "2024-06-01T00:00:00Z")

	// Lower bound.
	list, err := ListEpics(ctx, pool, slug, ident, actor, EpicListFilter{CreatedFrom: "2024-03-01"})
	if err != nil {
		t.Fatalf("ListEpics created_from: %v", err)
	}
	if len(list) != 1 || list[0].Name != "Jun Epic" {
		t.Fatalf("created_from=2024-03-01 = %v, want [Jun Epic]", epicFilterNames(list))
	}

	// Upper bound, date-only: whole-day-inclusive — the Jan 1 epic
	// (created exactly at 2024-01-01T00:00:00Z) matches created_to on
	// 2024-01-01.
	list, err = ListEpics(ctx, pool, slug, ident, actor, EpicListFilter{CreatedTo: "2024-01-01"})
	if err != nil {
		t.Fatalf("ListEpics created_to: %v", err)
	}
	if len(list) != 1 || list[0].Name != "Jan Epic" {
		t.Fatalf("created_to=2024-01-01 = %v, want [Jan Epic]", epicFilterNames(list))
	}

	// Full RFC3339 still works.
	list, err = ListEpics(ctx, pool, slug, ident, actor, EpicListFilter{
		CreatedFrom: "2024-06-01T00:00:00Z",
		CreatedTo:   "2024-06-01T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("ListEpics rfc3339 range: %v", err)
	}
	if len(list) != 1 || list[0].Name != "Jun Epic" {
		t.Fatalf("rfc3339 range = %v, want [Jun Epic]", epicFilterNames(list))
	}

	// Unparseable date is a client error.
	if _, err := ListEpics(ctx, pool, slug, ident, actor, EpicListFilter{CreatedFrom: "not-a-date"}); !errors.Is(err, ErrInvalidEpicFilter) {
		t.Fatalf("created_from=not-a-date: err = %v, want ErrInvalidEpicFilter", err)
	}
}

func TestEpicListInitiativeFilter(t *testing.T) {
	ctx, pool, slug, ident, actor := setupEpicTest(t)
	linked := createFilterEpic(t, ctx, pool, slug, ident, actor, "Linked")
	createFilterEpic(t, ctx, pool, slug, ident, actor, "Unlinked")

	init, err := CreateInitiative(ctx, pool, slug, actor, InitiativeInput{Name: "Big Bet"})
	if err != nil {
		t.Fatalf("CreateInitiative: %v", err)
	}
	if err := AddInitiativeEpic(ctx, pool, slug, actor, init.ID, linked.ID); err != nil {
		t.Fatalf("AddInitiativeEpic: %v", err)
	}

	list, err := ListEpics(ctx, pool, slug, ident, actor, EpicListFilter{InitiativeID: init.ID})
	if err != nil {
		t.Fatalf("ListEpics initiative_id: %v", err)
	}
	if len(list) != 1 || list[0].Name != "Linked" {
		t.Fatalf("initiative_id = %v, want [Linked]", epicFilterNames(list))
	}

	// Valid-but-unknown initiative id matches nothing (never an error).
	list, err = ListEpics(ctx, pool, slug, ident, actor, EpicListFilter{InitiativeID: "00000000-0000-4000-8000-000000000000"})
	if err != nil {
		t.Fatalf("ListEpics unknown initiative_id: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("unknown initiative_id = %v, want []", epicFilterNames(list))
	}

	// Malformed initiative id is a client error, not a ::uuid 500.
	if _, err := ListEpics(ctx, pool, slug, ident, actor, EpicListFilter{InitiativeID: "xyz"}); !errors.Is(err, ErrInvalidEpicFilter) {
		t.Fatalf("initiative_id=xyz: err = %v, want ErrInvalidEpicFilter", err)
	}
}

func TestEpicListSortOrder(t *testing.T) {
	ctx, pool, slug, ident, actor := setupEpicTest(t)
	a := createFilterEpic(t, ctx, pool, slug, ident, actor, "Alpha")
	setEpicTimestamps(t, ctx, pool, a.ID, "2024-01-01T00:00:00Z")
	b := createFilterEpic(t, ctx, pool, slug, ident, actor, "Beta")
	setEpicTimestamps(t, ctx, pool, b.ID, "2024-06-01T00:00:00Z")
	c := createFilterEpic(t, ctx, pool, slug, ident, actor, "Gamma")
	setEpicTimestamps(t, ctx, pool, c.ID, "2024-03-01T00:00:00Z")

	sameNames := func(got []Epic, want ...string) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i].Name != want[i] {
				return false
			}
		}
		return true
	}

	// sort=name defaults to asc.
	list, err := ListEpics(ctx, pool, slug, ident, actor, EpicListFilter{Sort: "name"})
	if err != nil {
		t.Fatalf("sort=name: %v", err)
	}
	if !sameNames(list, "Alpha", "Beta", "Gamma") {
		t.Fatalf("sort=name = %v", epicFilterNames(list))
	}

	// Default sort is created_at desc.
	list, err = ListEpics(ctx, pool, slug, ident, actor, EpicListFilter{})
	if err != nil {
		t.Fatalf("default sort: %v", err)
	}
	if !sameNames(list, "Beta", "Gamma", "Alpha") {
		t.Fatalf("default = %v, want [Beta Gamma Alpha]", epicFilterNames(list))
	}

	// Explicit order flips it.
	list, err = ListEpics(ctx, pool, slug, ident, actor, EpicListFilter{Sort: "created_at", Order: "asc"})
	if err != nil {
		t.Fatalf("sort=created_at&order=asc: %v", err)
	}
	if !sameNames(list, "Alpha", "Gamma", "Beta") {
		t.Fatalf("created_at asc = %v", epicFilterNames(list))
	}

	// sort=updated_at: touch one row to the future.
	setEpicTimestamps(t, ctx, pool, a.ID, "2025-01-01T00:00:00Z")
	list, err = ListEpics(ctx, pool, slug, ident, actor, EpicListFilter{Sort: "updated_at"})
	if err != nil {
		t.Fatalf("sort=updated_at: %v", err)
	}
	if !sameNames(list, "Alpha", "Beta", "Gamma") {
		t.Fatalf("updated_at desc = %v, want [Alpha Beta Gamma]", epicFilterNames(list))
	}

	// sort=issue_count: the epic with member issues ranks first desc.
	iss := createTestEpicIssue(t, pool, slug, ident, actor, "member", "started")
	if err := AddEpicIssues(ctx, pool, slug, ident, actor, c.ID, []string{iss.ID}); err != nil {
		t.Fatalf("AddEpicIssues: %v", err)
	}
	list, err = ListEpics(ctx, pool, slug, ident, actor, EpicListFilter{Sort: "issue_count", Order: "desc"})
	if err != nil {
		t.Fatalf("sort=issue_count: %v", err)
	}
	if len(list) != 3 || list[0].Name != "Gamma" {
		t.Fatalf("issue_count desc = %v, want Gamma first", epicFilterNames(list))
	}

	// Unknown sort / order are client errors.
	if _, err := ListEpics(ctx, pool, slug, ident, actor, EpicListFilter{Sort: "bogus"}); !errors.Is(err, ErrInvalidEpicFilter) {
		t.Fatalf("sort=bogus: err = %v, want ErrInvalidEpicFilter", err)
	}
	if _, err := ListEpics(ctx, pool, slug, ident, actor, EpicListFilter{Order: "sideways"}); !errors.Is(err, ErrInvalidEpicFilter) {
		t.Fatalf("order=sideways: err = %v, want ErrInvalidEpicFilter", err)
	}
}

func TestEpicListIncludeArchivedCompat(t *testing.T) {
	ctx, pool, slug, ident, actor := setupEpicTest(t)
	createFilterEpic(t, ctx, pool, slug, ident, actor, "Live")
	old := createFilterEpic(t, ctx, pool, slug, ident, actor, "Old")
	if _, err := ArchiveEpic(ctx, pool, slug, ident, actor, old.ID); err != nil {
		t.Fatalf("ArchiveEpic: %v", err)
	}

	// Default excludes archived.
	list, err := ListEpics(ctx, pool, slug, ident, actor, EpicListFilter{})
	if err != nil {
		t.Fatalf("default: %v", err)
	}
	if len(list) != 1 || list[0].Name != "Live" {
		t.Fatalf("default = %v, want [Live]", epicFilterNames(list))
	}

	// Legacy includeArchived maps to "no status exclusion".
	list, err = ListEpics(ctx, pool, slug, ident, actor, EpicListFilter{IncludeArchived: true})
	if err != nil {
		t.Fatalf("includeArchived: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("includeArchived = %v, want 2", epicFilterNames(list))
	}

	// Explicit status wins over the legacy flag.
	list, err = ListEpics(ctx, pool, slug, ident, actor, EpicListFilter{IncludeArchived: true, Statuses: []string{"active"}})
	if err != nil {
		t.Fatalf("status wins: %v", err)
	}
	if len(list) != 1 || list[0].Name != "Live" {
		t.Fatalf("status=active + includeArchived = %v, want [Live]", epicFilterNames(list))
	}
}

func TestEpicListCombosAndEmptyNeverNull(t *testing.T) {
	ctx, pool, slug, ident, actor := setupEpicTest(t)
	desc := "revamp the platform"
	if _, err := CreateEpic(ctx, pool, slug, ident, actor, EpicInput{Name: "Platform Revamp", Description: &desc}); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	createFilterEpic(t, ctx, pool, slug, ident, actor, "Mobile Push")
	old := createFilterEpic(t, ctx, pool, slug, ident, actor, "Old Revamp")
	if _, err := ArchiveEpic(ctx, pool, slug, ident, actor, old.ID); err != nil {
		t.Fatalf("ArchiveEpic: %v", err)
	}
	init, err := CreateInitiative(ctx, pool, slug, actor, InitiativeInput{Name: "Big Bet"})
	if err != nil {
		t.Fatalf("CreateInitiative: %v", err)
	}
	linked, err := CreateEpic(ctx, pool, slug, ident, actor, EpicInput{Name: "Linked Revamp"})
	if err != nil {
		t.Fatalf("CreateEpic linked: %v", err)
	}
	if err := AddInitiativeEpic(ctx, pool, slug, actor, init.ID, linked.ID); err != nil {
		t.Fatalf("AddInitiativeEpic: %v", err)
	}

	// q + status combo: only the active revamp.
	list, err := ListEpics(ctx, pool, slug, ident, actor, EpicListFilter{
		Q:        "revamp",
		Statuses: []string{"active"},
	})
	if err != nil {
		t.Fatalf("combo q+status: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("combo = %v, want 2 (Platform Revamp, Linked Revamp)", epicFilterNames(list))
	}

	// q + initiative combo: only the linked revamp.
	list, err = ListEpics(ctx, pool, slug, ident, actor, EpicListFilter{
		Q:            "revamp",
		InitiativeID: init.ID,
	})
	if err != nil {
		t.Fatalf("combo q+initiative: %v", err)
	}
	if len(list) != 1 || list[0].Name != "Linked Revamp" {
		t.Fatalf("q+initiative = %v, want [Linked Revamp]", epicFilterNames(list))
	}

	// Empty match is [] — never nil (handler renders "epics":[]).
	list, err = ListEpics(ctx, pool, slug, ident, actor, EpicListFilter{Q: "no-such-epic-xyz"})
	if err != nil {
		t.Fatalf("empty: %v", err)
	}
	if list == nil || len(list) != 0 {
		t.Fatalf("empty = %#v, want non-nil []", list)
	}
}
