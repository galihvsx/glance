package service

// Projects + default states tests (Task 12): CRUD, identifier uppercasing
// + per-workspace uniqueness (case-insensitive), default state seeding in
// the same tx as project creation, role gates (guest 5 cannot
// create/update), membership scoping. All tests run against the real test
// database — no skips. Reuses the harness from workspace_test.go
// (newTestPool, migrateTestDB, createTestUser, createTestWorkspace,
// uniqueTestEmail, uniqueTestSlug, UpsertMember).

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// uniqueTestIdentifier hands out run-unique project identifiers: uppercase
// alphanumeric, always within the 12-char limit.
func uniqueTestIdentifier() string {
	return fmt.Sprintf("P%05d%04d", os.Getpid()%100000, int(testSeq.Add(1))%10000)
}

func createTestProject(t *testing.T, pool *pgxpool.Pool, wsSlug, actorID, name, identifier string) *Project {
	t.Helper()
	p, err := CreateProject(context.Background(), pool, wsSlug, actorID, name, identifier)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	return p
}

func addTestMember(t *testing.T, pool *pgxpool.Pool, wsSlug, adminID, userID string, role int) {
	t.Helper()
	if err := UpsertMember(context.Background(), pool, wsSlug, adminID, userID, role); err != nil {
		t.Fatalf("UpsertMember: %v", err)
	}
}

var wantDefaultStates = []struct{ name, group string }{
	{"Backlog", "backlog"},
	{"Todo", "unstarted"},
	{"In Progress", "started"},
	{"Done", "completed"},
	{"Cancelled", "cancelled"},
}

func TestCreateProject(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	creator := createTestUser(t, pool, uniqueTestEmail("proj-creator"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-proj"), creator)

	ident := uniqueTestIdentifier()
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", strings.ToLower(ident))

	if p.Identifier != ident {
		t.Fatalf("identifier = %q, want uppercased %q", p.Identifier, ident)
	}
	if p.Name != "Engineering" || p.WorkspaceID != ws.ID || p.ID == "" {
		t.Fatalf("project = %+v, want name='Engineering' workspace=%s with id", p, ws.ID)
	}

	states, err := ListStates(ctx, pool, ws.Slug, p.Identifier, creator)
	if err != nil {
		t.Fatalf("ListStates: %v", err)
	}
	if len(states) != len(wantDefaultStates) {
		t.Fatalf("seeded %d states, want %d", len(states), len(wantDefaultStates))
	}
	for i, want := range wantDefaultStates {
		got := states[i]
		if got.Name != want.name || got.Group != want.group {
			t.Fatalf("state[%d] = (%q,%q), want (%q,%q)", i, got.Name, got.Group, want.name, want.group)
		}
		if got.ProjectID != p.ID {
			t.Fatalf("state[%d].ProjectID = %q, want %q", i, got.ProjectID, p.ID)
		}
		if i > 0 && got.Sequence <= states[i-1].Sequence {
			t.Fatalf("state sequences not increasing: %+v", states)
		}
	}
}

func TestCreateProjectDuplicateIdentifier(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	creator := createTestUser(t, pool, uniqueTestEmail("proj-dup"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-dup"), creator)

	ident := uniqueTestIdentifier()
	createTestProject(t, pool, ws.Slug, creator, "Engineering", ident)

	// Same identifier, different case: must 409, not create a second row.
	_, err := CreateProject(ctx, pool, ws.Slug, creator, "Eng Two", strings.ToLower(ident))
	if !errors.Is(err, ErrIdentifierConflict) {
		t.Fatalf("duplicate identifier: err = %v, want ErrIdentifierConflict", err)
	}

	// The failed create must not leave orphan states behind (same-tx seed).
	_, err = GetProject(ctx, pool, ws.Slug, ident+"X", creator)
	if !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("sentinel check: err = %v, want ErrProjectNotFound", err)
	}
	projects, err := ListProjects(ctx, pool, ws.Slug, creator)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("projects = %d, want 1 after failed duplicate create", len(projects))
	}
}

func TestCreateProjectIdentifierUniquePerWorkspace(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	creator := createTestUser(t, pool, uniqueTestEmail("proj-ws"))
	ws1 := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-ws1"), creator)
	ws2 := createTestWorkspace(t, pool, "Beta", uniqueTestSlug("beta-ws2"), creator)

	ident := uniqueTestIdentifier()
	createTestProject(t, pool, ws1.Slug, creator, "Engineering", ident)
	// Same identifier in a different workspace is fine.
	if _, err := CreateProject(ctx, pool, ws2.Slug, creator, "Engineering", ident); err != nil {
		t.Fatalf("same identifier in another workspace: err = %v, want nil", err)
	}
}

func TestCreateProjectGuestForbidden(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	admin := createTestUser(t, pool, uniqueTestEmail("proj-admin"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-guest"), admin)
	guest := createTestUser(t, pool, uniqueTestEmail("proj-guest"))
	addTestMember(t, pool, ws.Slug, admin, guest, RoleGuest)

	_, err := CreateProject(ctx, pool, ws.Slug, guest, "Engineering", uniqueTestIdentifier())
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest create: err = %v, want ErrForbidden", err)
	}
}

func TestCreateProjectMemberAllowed(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	admin := createTestUser(t, pool, uniqueTestEmail("proj-admin2"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-member"), admin)
	member := createTestUser(t, pool, uniqueTestEmail("proj-member"))
	addTestMember(t, pool, ws.Slug, admin, member, RoleMember)

	if _, err := CreateProject(ctx, pool, ws.Slug, member, "Engineering", uniqueTestIdentifier()); err != nil {
		t.Fatalf("member create: err = %v, want nil", err)
	}
}

func TestCreateProjectNonMemberNotFound(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	creator := createTestUser(t, pool, uniqueTestEmail("proj-out1"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-out"), creator)
	outsider := createTestUser(t, pool, uniqueTestEmail("proj-out2"))

	_, err := CreateProject(ctx, pool, ws.Slug, outsider, "Engineering", uniqueTestIdentifier())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-member create: err = %v, want ErrNotFound", err)
	}
}

func TestCreateProjectInvalidIdentifier(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	creator := createTestUser(t, pool, uniqueTestEmail("proj-badid"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-badid"), creator)

	for _, ident := range []string{"", "   ", "TOOLONGIDENTIFIER1", "has space", "under_score", "dash-ed"} {
		if _, err := CreateProject(ctx, pool, ws.Slug, creator, "Engineering", ident); !errors.Is(err, ErrInvalidIdentifier) {
			t.Fatalf("identifier %q: err = %v, want ErrInvalidIdentifier", ident, err)
		}
	}
}

func TestCreateProjectNameRequired(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	creator := createTestUser(t, pool, uniqueTestEmail("proj-noname"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-noname"), creator)

	if _, err := CreateProject(ctx, pool, ws.Slug, creator, "  ", uniqueTestIdentifier()); !errors.Is(err, ErrNameRequired) {
		t.Fatalf("empty name: err = %v, want ErrNameRequired", err)
	}
}

func TestGetProject(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	creator := createTestUser(t, pool, uniqueTestEmail("proj-get"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-get"), creator)
	created := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())

	// Lookup is case-insensitive (identifier stored uppercased).
	got, err := GetProject(ctx, pool, ws.Slug, strings.ToLower(created.Identifier), creator)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got.ID != created.ID || got.Identifier != created.Identifier {
		t.Fatalf("GetProject = %+v, want %+v", got, created)
	}

	if _, err := GetProject(ctx, pool, ws.Slug, "NOPE123", creator); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("missing project: err = %v, want ErrProjectNotFound", err)
	}

	outsider := createTestUser(t, pool, uniqueTestEmail("proj-get-out"))
	if _, err := GetProject(ctx, pool, ws.Slug, created.Identifier, outsider); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-member get: err = %v, want ErrNotFound", err)
	}
}

func TestListProjects(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	creator := createTestUser(t, pool, uniqueTestEmail("proj-list"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-list"), creator)
	createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())
	createTestProject(t, pool, ws.Slug, creator, "Design", uniqueTestIdentifier())

	projects, err := ListProjects(ctx, pool, ws.Slug, creator)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("projects = %d, want 2", len(projects))
	}

	outsider := createTestUser(t, pool, uniqueTestEmail("proj-list-out"))
	if _, err := ListProjects(ctx, pool, ws.Slug, outsider); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-member list: err = %v, want ErrNotFound", err)
	}
}

func TestUpdateProject(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	admin := createTestUser(t, pool, uniqueTestEmail("proj-upd-admin"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-upd"), admin)
	member := createTestUser(t, pool, uniqueTestEmail("proj-upd-member"))
	addTestMember(t, pool, ws.Slug, admin, member, RoleMember)
	guest := createTestUser(t, pool, uniqueTestEmail("proj-upd-guest"))
	addTestMember(t, pool, ws.Slug, admin, guest, RoleGuest)

	p := createTestProject(t, pool, ws.Slug, admin, "Engineering", uniqueTestIdentifier())

	updated, err := UpdateProject(ctx, pool, ws.Slug, p.Identifier, admin, "Platform", "All platform work")
	if err != nil {
		t.Fatalf("UpdateProject (admin): %v", err)
	}
	if updated.Name != "Platform" || updated.Description != "All platform work" {
		t.Fatalf("updated = %+v, want name/description changed", updated)
	}

	// Member (15) may update too.
	if _, err := UpdateProject(ctx, pool, ws.Slug, p.Identifier, member, "Platform Two", ""); err != nil {
		t.Fatalf("UpdateProject (member): %v, want nil", err)
	}

	// Guest (5) may not.
	if _, err := UpdateProject(ctx, pool, ws.Slug, p.Identifier, guest, "Hacked", ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest update: err = %v, want ErrForbidden", err)
	}

	// Empty name is rejected.
	if _, err := UpdateProject(ctx, pool, ws.Slug, p.Identifier, admin, "  ", ""); !errors.Is(err, ErrNameRequired) {
		t.Fatalf("empty name update: err = %v, want ErrNameRequired", err)
	}

	// Missing project.
	if _, err := UpdateProject(ctx, pool, ws.Slug, "NOPE123", admin, "X", ""); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("missing project update: err = %v, want ErrProjectNotFound", err)
	}
}

func TestListStatesNonMemberNotFound(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	creator := createTestUser(t, pool, uniqueTestEmail("proj-st"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-st"), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())

	outsider := createTestUser(t, pool, uniqueTestEmail("proj-st-out"))
	if _, err := ListStates(ctx, pool, ws.Slug, p.Identifier, outsider); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-member states: err = %v, want ErrNotFound", err)
	}
}
