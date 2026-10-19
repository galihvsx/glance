package service

// Project-label taxonomy tests (C76T0): workspace-level label CRUD with
// the issue-label discipline (unique (workspace_id, name) → 409,
// normalizeLabelColor → 400, guest read-only), per-project assignment
// with duplicate-assign 409, the ?label= filter on ListProjects, []-never-
// null label chips on project payloads, cascade deletes, and initiative
// member-project chips. Real test database, no skips.

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func createTestProjectLabel(t *testing.T, pool *pgxpool.Pool, wsSlug, actorID, name string) *ProjectLabel {
	t.Helper()
	l, err := CreateProjectLabel(context.Background(), pool, wsSlug, actorID,
		ProjectLabelInput{Name: name})
	if err != nil {
		t.Fatalf("CreateProjectLabel %q: %v", name, err)
	}
	return l
}

func projectLabelIDs(t *testing.T, pool *pgxpool.Pool, wsSlug, ident, actorID string) []string {
	t.Helper()
	p, err := GetProject(context.Background(), pool, wsSlug, ident, actorID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	var ids []string
	for _, l := range p.Labels {
		ids = append(ids, l.ID)
	}
	return ids
}

// TestProjectLabelCRUD: create (defaults, trimming), list ordering,
// get, partial update, and delete.
func TestProjectLabelCRUD(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	creator := createTestUser(t, pool, uniqueTestEmail("pl-crud"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-pl"), creator)

	// Create with defaults: color falls back to #6b7280, name trimmed.
	l, err := CreateProjectLabel(ctx, pool, ws.Slug, creator,
		ProjectLabelInput{Name: "  platform  "})
	if err != nil {
		t.Fatalf("CreateProjectLabel: %v", err)
	}
	if l.Name != "platform" {
		t.Fatalf("name = %q, want trimmed %q", l.Name, "platform")
	}
	if l.Color != "#6b7280" {
		t.Fatalf("color = %q, want default #6b7280", l.Color)
	}
	if l.WorkspaceID == "" || l.ID == "" {
		t.Fatal("label missing id/workspace_id")
	}

	// Create with an explicit color.
	red, err := CreateProjectLabel(ctx, pool, ws.Slug, creator,
		ProjectLabelInput{Name: "urgent", Color: strPtr2("#ef4444")})
	if err != nil {
		t.Fatalf("CreateProjectLabel urgent: %v", err)
	}
	if red.Color != "#ef4444" {
		t.Fatalf("color = %q, want #ef4444", red.Color)
	}

	// List: ordered by name, never nil.
	listed, err := ListProjectLabels(ctx, pool, ws.Slug, creator)
	if err != nil {
		t.Fatalf("ListProjectLabels: %v", err)
	}
	if listed == nil || len(listed) != 2 {
		t.Fatalf("listed = %d, want 2", len(listed))
	}
	if listed[0].Name != "platform" || listed[1].Name != "urgent" {
		t.Fatalf("order = %q,%q, want platform,urgent", listed[0].Name, listed[1].Name)
	}

	// Get.
	got, err := GetProjectLabel(ctx, pool, ws.Slug, l.ID, creator)
	if err != nil {
		t.Fatalf("GetProjectLabel: %v", err)
	}
	if got.Name != "platform" {
		t.Fatalf("get name = %q", got.Name)
	}

	// Partial update: rename only; color untouched.
	updated, err := UpdateProjectLabel(ctx, pool, ws.Slug, l.ID, creator,
		ProjectLabelPatch{Name: strPtr2("platform-x")})
	if err != nil {
		t.Fatalf("UpdateProjectLabel rename: %v", err)
	}
	if updated.Name != "platform-x" || updated.Color != "#6b7280" {
		t.Fatalf("after rename = %+v", updated)
	}

	// Partial update: recolor only.
	updated, err = UpdateProjectLabel(ctx, pool, ws.Slug, l.ID, creator,
		ProjectLabelPatch{Color: strPtr2("#10b981")})
	if err != nil {
		t.Fatalf("UpdateProjectLabel recolor: %v", err)
	}
	if updated.Color != "#10b981" || updated.Name != "platform-x" {
		t.Fatalf("after recolor = %+v", updated)
	}

	// Empty patch is a client error.
	if _, err := UpdateProjectLabel(ctx, pool, ws.Slug, l.ID, creator,
		ProjectLabelPatch{}); !errors.Is(err, ErrNothingToUpdate) {
		t.Fatalf("empty patch: err = %v, want ErrNothingToUpdate", err)
	}

	// Delete → gone.
	if err := DeleteProjectLabel(ctx, pool, ws.Slug, l.ID, creator); err != nil {
		t.Fatalf("DeleteProjectLabel: %v", err)
	}
	if _, err := GetProjectLabel(ctx, pool, ws.Slug, l.ID, creator); !errors.Is(err, ErrProjectLabelNotFound) {
		t.Fatalf("get after delete: err = %v, want ErrProjectLabelNotFound", err)
	}
	if err := DeleteProjectLabel(ctx, pool, ws.Slug, l.ID, creator); !errors.Is(err, ErrProjectLabelNotFound) {
		t.Fatalf("double delete: err = %v, want ErrProjectLabelNotFound", err)
	}
}

// TestProjectLabelValidation: name rules, color rules, conflicts, and
// tenancy boundaries.
func TestProjectLabelValidation(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	creator := createTestUser(t, pool, uniqueTestEmail("pl-val"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-plval"), creator)

	if _, err := CreateProjectLabel(ctx, pool, ws.Slug, creator,
		ProjectLabelInput{Name: "   "}); !errors.Is(err, ErrNameRequired) {
		t.Fatalf("blank name: err = %v, want ErrNameRequired", err)
	}
	long := make([]byte, 121)
	for i := range long {
		long[i] = 'x'
	}
	if _, err := CreateProjectLabel(ctx, pool, ws.Slug, creator,
		ProjectLabelInput{Name: string(long)}); !errors.Is(err, ErrNameRequired) {
		t.Fatalf("121-char name: err = %v, want ErrNameRequired", err)
	}
	if _, err := CreateProjectLabel(ctx, pool, ws.Slug, creator,
		ProjectLabelInput{Name: "bad", Color: strPtr2("red")}); !errors.Is(err, ErrInvalidLabel) {
		t.Fatalf("bad color: err = %v, want ErrInvalidLabel", err)
	}
	if _, err := CreateProjectLabel(ctx, pool, ws.Slug, creator,
		ProjectLabelInput{Name: "bad", Color: strPtr2("#12345")}); !errors.Is(err, ErrInvalidLabel) {
		t.Fatalf("short hex: err = %v, want ErrInvalidLabel", err)
	}

	createTestProjectLabel(t, pool, ws.Slug, creator, "backend")

	// Duplicate name in the same workspace → 409.
	if _, err := CreateProjectLabel(ctx, pool, ws.Slug, creator,
		ProjectLabelInput{Name: "backend"}); !errors.Is(err, ErrProjectLabelConflict) {
		t.Fatalf("duplicate name: err = %v, want ErrProjectLabelConflict", err)
	}

	// Same name in another workspace is fine.
	other := createTestUser(t, pool, uniqueTestEmail("pl-val-other"))
	ws2 := createTestWorkspace(t, pool, "Other", uniqueTestSlug("other-plval"), other)
	if _, err := CreateProjectLabel(ctx, pool, ws2.Slug, other,
		ProjectLabelInput{Name: "backend"}); err != nil {
		t.Fatalf("same name other workspace: %v", err)
	}

	// A label of another workspace reads as not-found here — never
	// leaks across the tenancy boundary.
	foreign := createTestProjectLabel(t, pool, ws2.Slug, other, "foreign")
	if _, err := GetProjectLabel(ctx, pool, ws.Slug, foreign.ID, creator); !errors.Is(err, ErrProjectLabelNotFound) {
		t.Fatalf("foreign get: err = %v, want ErrProjectLabelNotFound", err)
	}
	if err := DeleteProjectLabel(ctx, pool, ws.Slug, foreign.ID, creator); !errors.Is(err, ErrProjectLabelNotFound) {
		t.Fatalf("foreign delete: err = %v, want ErrProjectLabelNotFound", err)
	}

	// Malformed id is not-found, never a 500.
	if _, err := GetProjectLabel(ctx, pool, ws.Slug, "nope", creator); !errors.Is(err, ErrProjectLabelNotFound) {
		t.Fatalf("malformed get: err = %v, want ErrProjectLabelNotFound", err)
	}

	// Rename onto an existing name → 409.
	other2 := createTestProjectLabel(t, pool, ws.Slug, creator, "frontend")
	if _, err := UpdateProjectLabel(ctx, pool, ws.Slug, other2.ID, creator,
		ProjectLabelPatch{Name: strPtr2("backend")}); !errors.Is(err, ErrProjectLabelConflict) {
		t.Fatalf("rename conflict: err = %v, want ErrProjectLabelConflict", err)
	}

	// Bad workspace slug → the tenancy boundary.
	if _, err := ListProjectLabels(ctx, pool, "no-such-ws", creator); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bad slug list: err = %v, want ErrNotFound", err)
	}
	outsider := createTestUser(t, pool, uniqueTestEmail("pl-outsider"))
	if _, err := ListProjectLabels(ctx, pool, ws.Slug, outsider); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-member list: err = %v, want ErrNotFound", err)
	}
}

// TestProjectLabelGuestReadOnly: guests may read the taxonomy but every
// write — CRUD and assignment — is 403.
func TestProjectLabelGuestReadOnly(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	creator := createTestUser(t, pool, uniqueTestEmail("pl-guest"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-plguest"), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())
	l := createTestProjectLabel(t, pool, ws.Slug, creator, "platform")

	guest := createTestUser(t, pool, uniqueTestEmail("pl-guest-2"))
	addTestMember(t, pool, ws.Slug, creator, guest, RoleGuest)

	if _, err := ListProjectLabels(ctx, pool, ws.Slug, guest); err != nil {
		t.Fatalf("guest list: %v", err)
	}
	if _, err := GetProjectLabel(ctx, pool, ws.Slug, l.ID, guest); err != nil {
		t.Fatalf("guest get: %v", err)
	}
	if _, err := CreateProjectLabel(ctx, pool, ws.Slug, guest,
		ProjectLabelInput{Name: "nope"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest create: err = %v, want ErrForbidden", err)
	}
	if _, err := UpdateProjectLabel(ctx, pool, ws.Slug, l.ID, guest,
		ProjectLabelPatch{Name: strPtr2("nope")}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest update: err = %v, want ErrForbidden", err)
	}
	if err := DeleteProjectLabel(ctx, pool, ws.Slug, l.ID, guest); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest delete: err = %v, want ErrForbidden", err)
	}
	if err := AssignProjectLabel(ctx, pool, ws.Slug, p.Identifier, l.ID, guest); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest assign: err = %v, want ErrForbidden", err)
	}
	if err := UnassignProjectLabel(ctx, pool, ws.Slug, p.Identifier, l.ID, guest); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest unassign: err = %v, want ErrForbidden", err)
	}
}

// TestProjectLabelAssign: assign/unassign, duplicate-assign 409, and the
// 404s for unknown project/label.
func TestProjectLabelAssign(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	creator := createTestUser(t, pool, uniqueTestEmail("pl-assign"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-plassign"), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())
	l := createTestProjectLabel(t, pool, ws.Slug, creator, "platform")

	// Assign → the chip shows on the project payload.
	if err := AssignProjectLabel(ctx, pool, ws.Slug, p.Identifier, l.ID, creator); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if ids := projectLabelIDs(t, pool, ws.Slug, p.Identifier, creator); len(ids) != 1 || ids[0] != l.ID {
		t.Fatalf("project labels = %v, want [%s]", ids, l.ID)
	}

	// Duplicate assign → 409 (NOT an idempotent no-op — the brief's
	// ruling; issue labels differ).
	if err := AssignProjectLabel(ctx, pool, ws.Slug, p.Identifier, l.ID, creator); !errors.Is(err, ErrProjectLabelAssignConflict) {
		t.Fatalf("duplicate assign: err = %v, want ErrProjectLabelAssignConflict", err)
	}

	// Unassign → gone; unassign again → silent success.
	if err := UnassignProjectLabel(ctx, pool, ws.Slug, p.Identifier, l.ID, creator); err != nil {
		t.Fatalf("unassign: %v", err)
	}
	if ids := projectLabelIDs(t, pool, ws.Slug, p.Identifier, creator); len(ids) != 0 {
		t.Fatalf("project labels after unassign = %v, want []", ids)
	}
	if err := UnassignProjectLabel(ctx, pool, ws.Slug, p.Identifier, l.ID, creator); err != nil {
		t.Fatalf("unassign idempotent: %v", err)
	}
	// Malformed label id on unassign: matches nothing — the idempotent
	// no-op answer, never a 500.
	if err := UnassignProjectLabel(ctx, pool, ws.Slug, p.Identifier, "nope", creator); err != nil {
		t.Fatalf("unassign malformed: %v", err)
	}

	// Unknown project → 404; unknown label → 404; other workspace's
	// label → 404 (never leaks).
	if err := AssignProjectLabel(ctx, pool, ws.Slug, "NOPE", l.ID, creator); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("assign unknown project: err = %v, want ErrProjectNotFound", err)
	}
	if err := AssignProjectLabel(ctx, pool, ws.Slug, p.Identifier, "00000000-0000-0000-0000-000000000000", creator); !errors.Is(err, ErrProjectLabelNotFound) {
		t.Fatalf("assign unknown label: err = %v, want ErrProjectLabelNotFound", err)
	}
	if err := AssignProjectLabel(ctx, pool, ws.Slug, p.Identifier, "nope", creator); !errors.Is(err, ErrProjectLabelNotFound) {
		t.Fatalf("assign malformed label: err = %v, want ErrProjectLabelNotFound", err)
	}
	other := createTestUser(t, pool, uniqueTestEmail("pl-assign-other"))
	ws2 := createTestWorkspace(t, pool, "Other", uniqueTestSlug("other-plassign"), other)
	foreign := createTestProjectLabel(t, pool, ws2.Slug, other, "foreign")
	if err := AssignProjectLabel(ctx, pool, ws.Slug, p.Identifier, foreign.ID, creator); !errors.Is(err, ErrProjectLabelNotFound) {
		t.Fatalf("assign foreign label: err = %v, want ErrProjectLabelNotFound", err)
	}
}

// TestProjectListLabelFilter: the label filter on ListProjects — single,
// multi (OR), unknown/malformed → 400, no-match → [] (never nil), and
// chips on payloads ([] never null).
func TestProjectListLabelFilter(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	creator := createTestUser(t, pool, uniqueTestEmail("pl-filter"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-plfilter"), creator)
	pA := createTestProject(t, pool, ws.Slug, creator, "Alpha", uniqueTestIdentifier())
	pB := createTestProject(t, pool, ws.Slug, creator, "Beta", uniqueTestIdentifier())
	pC := createTestProject(t, pool, ws.Slug, creator, "Gamma", uniqueTestIdentifier())

	team := createTestProjectLabel(t, pool, ws.Slug, creator, "team-a")
	prio := createTestProjectLabel(t, pool, ws.Slug, creator, "p0")

	mustAssign := func(p *Project, l *ProjectLabel) {
		t.Helper()
		if err := AssignProjectLabel(ctx, pool, ws.Slug, p.Identifier, l.ID, creator); err != nil {
			t.Fatalf("assign %s→%s: %v", l.Name, p.Identifier, err)
		}
	}
	mustAssign(pA, team)
	mustAssign(pB, prio)
	mustAssign(pB, team)
	// pC carries no labels.

	idents := func(ps []Project) []string {
		var out []string
		for _, p := range ps {
			out = append(out, p.Identifier)
		}
		return out
	}

	// No filter → all three, each payload's Labels non-nil.
	all, err := ListProjects(ctx, pool, ws.Slug, creator, false, nil)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("unfiltered = %d, want 3", len(all))
	}
	for _, p := range all {
		if p.Labels == nil {
			t.Fatalf("project %s Labels = nil, want []", p.Identifier)
		}
	}

	// Single label → its projects (chips ordered by name).
	got, err := ListProjects(ctx, pool, ws.Slug, creator, false, []string{team.ID})
	if err != nil {
		t.Fatalf("filter team: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("filter team = %v, want 2 projects", idents(got))
	}
	for _, p := range got {
		if p.Labels == nil {
			t.Fatalf("filtered project %s Labels = nil", p.Identifier)
		}
	}

	// Multi label → OR union.
	got, err = ListProjects(ctx, pool, ws.Slug, creator, false, []string{team.ID, prio.ID})
	if err != nil {
		t.Fatalf("filter team+p0: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("filter team+p0 = %v, want 2 (OR union)", idents(got))
	}

	// Label nobody carries → [] never nil.
	lonely := createTestProjectLabel(t, pool, ws.Slug, creator, "lonely")
	got, err = ListProjects(ctx, pool, ws.Slug, creator, false, []string{lonely.ID})
	if err != nil {
		t.Fatalf("filter lonely: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("filter lonely = %v, want non-nil []", got)
	}

	// Unknown label id → 400-shaped filter error; malformed too.
	if _, err := ListProjects(ctx, pool, ws.Slug, creator, false,
		[]string{"00000000-0000-0000-0000-000000000000"}); !errors.Is(err, ErrInvalidProjectLabelFilter) {
		t.Fatalf("unknown label filter: err = %v, want ErrInvalidProjectLabelFilter", err)
	}
	if _, err := ListProjects(ctx, pool, ws.Slug, creator, false,
		[]string{"nope"}); !errors.Is(err, ErrInvalidProjectLabelFilter) {
		t.Fatalf("malformed label filter: err = %v, want ErrInvalidProjectLabelFilter", err)
	}

	// Detail payload: chips ordered by name, [] never null.
	detail, err := GetProject(ctx, pool, ws.Slug, pB.Identifier, creator)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if detail.Labels == nil {
		t.Fatal("detail Labels = nil, want []")
	}
	if len(detail.Labels) != 2 || detail.Labels[0].Name != "p0" || detail.Labels[1].Name != "team-a" {
		t.Fatalf("detail labels = %+v, want [p0 team-a]", detail.Labels)
	}
	bare, err := GetProject(ctx, pool, ws.Slug, pC.Identifier, creator)
	if err != nil {
		t.Fatalf("GetProject bare: %v", err)
	}
	if bare.Labels == nil || len(bare.Labels) != 0 {
		t.Fatalf("bare project Labels = %v, want non-nil []", bare.Labels)
	}
}

// TestProjectLabelCascade: deleting a project drops its junction rows
// (labels survive); deleting a label drops its junction rows (projects
// survive).
func TestProjectLabelCascade(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	creator := createTestUser(t, pool, uniqueTestEmail("pl-cascade"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-plcascade"), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())
	l := createTestProjectLabel(t, pool, ws.Slug, creator, "platform")

	if err := AssignProjectLabel(ctx, pool, ws.Slug, p.Identifier, l.ID, creator); err != nil {
		t.Fatalf("assign: %v", err)
	}

	// Deleting the label removes the junction row; the project is
	// untouched and its chips are back to [].
	if err := DeleteProjectLabel(ctx, pool, ws.Slug, l.ID, creator); err != nil {
		t.Fatalf("delete label: %v", err)
	}
	if ids := projectLabelIDs(t, pool, ws.Slug, p.Identifier, creator); len(ids) != 0 {
		t.Fatalf("project labels after label delete = %v, want []", ids)
	}

	// Re-create + re-assign, then delete the project: the label survives.
	l2 := createTestProjectLabel(t, pool, ws.Slug, creator, "platform")
	if err := AssignProjectLabel(ctx, pool, ws.Slug, p.Identifier, l2.ID, creator); err != nil {
		t.Fatalf("re-assign: %v", err)
	}
	if err := DeleteProject(ctx, pool, ws.Slug, p.Identifier, creator, p.Identifier, true); err != nil {
		t.Fatalf("delete project: %v", err)
	}
	if _, err := GetProjectLabel(ctx, pool, ws.Slug, l2.ID, creator); err != nil {
		t.Fatalf("label must survive project delete: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM project_project_labels WHERE label_id = $1::uuid`, l2.ID).Scan(&n); err != nil {
		t.Fatalf("count junction: %v", err)
	}
	if n != 0 {
		t.Fatalf("junction rows after project delete = %d, want 0", n)
	}
}

// TestInitiativeProjectLabels: member projects ship their label chips in
// the same list query — no per-project round trip.
func TestInitiativeProjectLabels(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	creator := createTestUser(t, pool, uniqueTestEmail("pl-init"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-plinit"), creator)
	pA := createTestProject(t, pool, ws.Slug, creator, "Alpha", uniqueTestIdentifier())
	pB := createTestProject(t, pool, ws.Slug, creator, "Beta", uniqueTestIdentifier())

	team := createTestProjectLabel(t, pool, ws.Slug, creator, "team-a")
	if err := AssignProjectLabel(ctx, pool, ws.Slug, pA.Identifier, team.ID, creator); err != nil {
		t.Fatalf("assign: %v", err)
	}

	init, err := CreateInitiative(ctx, pool, ws.Slug, creator, InitiativeInput{Name: "Prog"})
	if err != nil {
		t.Fatalf("CreateInitiative: %v", err)
	}
	var idA, idB string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM projects WHERE identifier = $1`, pA.Identifier).Scan(&idA); err != nil {
		t.Fatalf("project id A: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM projects WHERE identifier = $1`, pB.Identifier).Scan(&idB); err != nil {
		t.Fatalf("project id B: %v", err)
	}
	if err := AddInitiativeProject(ctx, pool, ws.Slug, creator, init.ID, idA); err != nil {
		t.Fatalf("add A: %v", err)
	}
	if err := AddInitiativeProject(ctx, pool, ws.Slug, creator, init.ID, idB); err != nil {
		t.Fatalf("add B: %v", err)
	}

	members, err := ListInitiativeProjects(ctx, pool, ws.Slug, creator, init.ID)
	if err != nil {
		t.Fatalf("ListInitiativeProjects: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("members = %d, want 2", len(members))
	}
	for _, m := range members {
		if m.Labels == nil {
			t.Fatalf("member %s Labels = nil, want []", m.ProjectName)
		}
	}
	var chips int
	for _, m := range members {
		if m.ProjectID == idA {
			chips = len(m.Labels)
			if chips != 1 || m.Labels[0].Name != "team-a" {
				t.Fatalf("member A labels = %+v, want [team-a]", m.Labels)
			}
		}
	}
	if chips != 1 {
		t.Fatal("member A not found in list")
	}
}
