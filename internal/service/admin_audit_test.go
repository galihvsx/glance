package service

// Admin audit-log tests (C15T1): the audit_log table is append-only —
// every admin write path (user deactivate/reactivate/role change,
// workspace delete) records exactly one row inside the same transaction
// as the mutation, and refused mutations record nothing. The read path
// (ListAuditLog) paginates and filters by action / actor_id /
// entity_type. Real test database, no skips.

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// auditRowsFor returns every audit entry for one entity, newest first.
func auditRowsFor(t *testing.T, pool *pgxpool.Pool, entityID string) []AuditEntry {
	t.Helper()
	entries, total, err := ListAuditLog(context.Background(), pool, 100, 0,
		AuditLogFilter{EntityType: "user", Action: "", ActorID: ""})
	if err != nil {
		t.Fatalf("ListAuditLog: %v", err)
	}
	_ = total
	var out []AuditEntry
	for _, e := range entries {
		if e.EntityID == entityID {
			out = append(out, e)
		}
	}
	return out
}

func TestAuditLogRecordsUserMutations(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)

	admin := adminTestUser(t, pool, uniqueTestEmail("audit-admin"), true)
	victim := adminTestUser(t, pool, uniqueTestEmail("audit-victim"), false)

	const ip = "203.0.113.7"

	// Role change: promote.
	if err := SetUserAdmin(ctx, pool, admin, victim, true, ip); err != nil {
		t.Fatalf("promote: %v", err)
	}
	// Deactivate, then reactivate.
	if err := DeactivateUser(ctx, pool, admin, victim, ip); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	if err := ReactivateUser(ctx, pool, admin, victim, ip); err != nil {
		t.Fatalf("reactivate: %v", err)
	}

	rows := auditRowsFor(t, pool, victim)
	if len(rows) != 3 {
		t.Fatalf("audit rows for victim = %d, want 3 (role change, deactivate, reactivate)", len(rows))
	}

	want := []string{
		AuditActionUserReactivated, // newest first
		AuditActionUserDeactivated,
		AuditActionUserRoleChanged,
	}
	for i, w := range want {
		got := rows[i]
		if got.Action != w {
			t.Errorf("row %d: action = %q, want %q", i, got.Action, w)
		}
		if got.EntityType != "user" {
			t.Errorf("row %d: entity_type = %q, want user", i, got.EntityType)
		}
		if got.ActorID == nil || *got.ActorID != admin {
			t.Errorf("row %d: actor_id = %v, want %s", i, got.ActorID, admin)
		}
		if got.ActorEmail == nil || *got.ActorEmail == "" {
			t.Errorf("row %d: actor_email not resolved", i)
		}
		if got.IP == nil || *got.IP != ip {
			t.Errorf("row %d: ip = %v, want %s", i, got.IP, ip)
		}
		if got.At.IsZero() {
			t.Errorf("row %d: at is zero", i)
		}
		// User-scoped actions carry no workspace.
		if got.WorkspaceID != nil {
			t.Errorf("row %d: workspace_id = %v, want nil", i, got.WorkspaceID)
		}
	}

	// The role-change row must carry the before/after flag in meta.
	var roleRow *AuditEntry
	for i := range rows {
		if rows[i].Action == AuditActionUserRoleChanged {
			roleRow = &rows[i]
		}
	}
	if roleRow == nil {
		t.Fatal("no user.role_changed row")
	}
	if isAdmin, ok := roleRow.Meta["is_admin"].(bool); !ok || !isAdmin {
		t.Errorf("role change meta = %v, want is_admin=true", roleRow.Meta)
	}
}

func TestAuditLogRecordsWorkspaceDelete(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)

	admin := adminTestUser(t, pool, uniqueTestEmail("audit-del-admin"), true)
	ws := createTestWorkspace(t, pool, "Audit Delete WS", uniqueTestSlug("audit-del-ws"), admin)

	if err := DeleteWorkspaceAsAdmin(ctx, pool, admin, ws.Slug, "Audit Delete WS", "203.0.113.8"); err != nil {
		t.Fatalf("delete workspace: %v", err)
	}

	entries, _, err := ListAuditLog(ctx, pool, 100, 0, AuditLogFilter{Action: AuditActionWorkspaceDeleted})
	if err != nil {
		t.Fatalf("ListAuditLog: %v", err)
	}
	var found *AuditEntry
	for i := range entries {
		if entries[i].EntityID == ws.ID {
			found = &entries[i]
		}
	}
	if found == nil {
		t.Fatal("no workspace.deleted audit row for the deleted workspace")
	}
	if found.EntityType != "workspace" {
		t.Errorf("entity_type = %q, want workspace", found.EntityType)
	}
	if found.ActorID == nil || *found.ActorID != admin {
		t.Errorf("actor_id = %v, want %s", found.ActorID, admin)
	}
	if found.IP == nil || *found.IP != "203.0.113.8" {
		t.Errorf("ip = %v, want 203.0.113.8", found.IP)
	}
	if name, ok := found.Meta["name"].(string); !ok || name != "Audit Delete WS" {
		t.Errorf("meta = %v, want name=Audit Delete WS", found.Meta)
	}
	// The workspace is gone, so the FK SET NULL fires: workspace_id must
	// be nil, not a dangling reference.
	if found.WorkspaceID != nil {
		t.Errorf("workspace_id = %v, want nil (workspace deleted)", found.WorkspaceID)
	}
}

func TestAuditLogRefusedMutationsRecordNothing(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)

	admin := adminTestUser(t, pool, uniqueTestEmail("audit-refuse-admin"), true)
	victim := adminTestUser(t, pool, uniqueTestEmail("audit-refuse-victim"), false)

	// All of these return errors before any mutation happens.
	_ = SetUserAdmin(ctx, pool, admin, admin, false, "127.0.0.1")                             // self-demote
	_ = DeactivateUser(ctx, pool, admin, admin, "127.0.0.1")                                  // self-deactivate
	_ = ReactivateUser(ctx, pool, admin, "00000000-0000-0000-0000-000000000000", "127.0.0.1") // unknown user
	_ = DeleteWorkspaceAsAdmin(ctx, pool, admin, uniqueTestSlug("nope"), "nope", "127.0.0.1") // unknown ws

	rows := auditRowsFor(t, pool, victim)
	if len(rows) != 0 {
		t.Errorf("refused/unknown-target mutations recorded %d audit rows, want 0", len(rows))
	}
	adminRows := auditRowsFor(t, pool, admin)
	if len(adminRows) != 0 {
		t.Errorf("self-mutations recorded %d audit rows, want 0", len(adminRows))
	}
}

func TestListAuditLogFilters(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)

	adminA := adminTestUser(t, pool, uniqueTestEmail("audit-f-a"), true)
	adminB := adminTestUser(t, pool, uniqueTestEmail("audit-f-b"), true)
	victimA := adminTestUser(t, pool, uniqueTestEmail("audit-f-va"), false)
	victimB := adminTestUser(t, pool, uniqueTestEmail("audit-f-vb"), false)

	if err := DeactivateUser(ctx, pool, adminA, victimA, "127.0.0.1"); err != nil {
		t.Fatalf("deactivate A: %v", err)
	}
	if err := DeactivateUser(ctx, pool, adminB, victimB, "127.0.0.1"); err != nil {
		t.Fatalf("deactivate B: %v", err)
	}
	ws := createTestWorkspace(t, pool, "Audit Filter WS", uniqueTestSlug("audit-f-ws"), adminA)
	if err := DeleteWorkspaceAsAdmin(ctx, pool, adminA, ws.Slug, "Audit Filter WS", "127.0.0.1"); err != nil {
		t.Fatalf("delete ws: %v", err)
	}

	// Filter by action.
	got, total, err := ListAuditLog(ctx, pool, 100, 0, AuditLogFilter{Action: AuditActionUserDeactivated})
	if err != nil {
		t.Fatalf("filter action: %v", err)
	}
	if total < 2 {
		t.Errorf("action filter total = %d, want >= 2", total)
	}
	for _, e := range got {
		if e.Action != AuditActionUserDeactivated {
			t.Errorf("action filter returned %q", e.Action)
		}
	}

	// Filter by actor_id: only adminB's deactivation.
	got, total, err = ListAuditLog(ctx, pool, 100, 0, AuditLogFilter{ActorID: adminB})
	if err != nil {
		t.Fatalf("filter actor: %v", err)
	}
	if total != 1 {
		t.Errorf("actor filter total = %d, want 1", total)
	}
	if len(got) == 1 {
		if got[0].ActorID == nil || *got[0].ActorID != adminB {
			t.Errorf("actor filter actor_id = %v, want %s", got[0].ActorID, adminB)
		}
		if got[0].EntityID != victimB {
			t.Errorf("actor filter entity_id = %s, want %s", got[0].EntityID, victimB)
		}
	}

	// Filter by entity_type.
	got, total, err = ListAuditLog(ctx, pool, 100, 0, AuditLogFilter{EntityType: "workspace"})
	if err != nil {
		t.Fatalf("filter entity_type: %v", err)
	}
	if total < 1 {
		t.Errorf("entity_type filter total = %d, want >= 1", total)
	}
	for _, e := range got {
		if e.EntityType != "workspace" {
			t.Errorf("entity_type filter returned %q", e.EntityType)
		}
	}

	// Combined filters: adminA's workspace deletion only.
	got, total, err = ListAuditLog(ctx, pool, 100, 0, AuditLogFilter{
		ActorID:    adminA,
		Action:     AuditActionWorkspaceDeleted,
		EntityType: "workspace",
	})
	if err != nil {
		t.Fatalf("combined filter: %v", err)
	}
	if total != 1 || len(got) != 1 || got[0].EntityID != ws.ID {
		t.Errorf("combined filter: total=%d len=%d, want exactly the workspace delete", total, len(got))
	}
}

func TestListAuditLogPagination(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)

	admin := adminTestUser(t, pool, uniqueTestEmail("audit-pg-admin"), true)
	victims := make([]string, 0, 4)
	for i := 0; i < 4; i++ {
		victims = append(victims, adminTestUser(t, pool, uniqueTestEmail("audit-pg-v"), false))
	}
	for _, v := range victims {
		if err := DeactivateUser(ctx, pool, admin, v, "127.0.0.1"); err != nil {
			t.Fatalf("deactivate: %v", err)
		}
	}

	// Four deactivations by this actor; page through 2 at a time.
	seen := map[string]bool{}
	for page := 0; page < 2; page++ {
		batch, total, err := ListAuditLog(ctx, pool, 2, page*2,
			AuditLogFilter{ActorID: admin, Action: AuditActionUserDeactivated})
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		if total != 4 {
			t.Fatalf("total = %d, want 4", total)
		}
		if len(batch) != 2 {
			t.Fatalf("page %d: len = %d, want 2", page, len(batch))
		}
		for _, e := range batch {
			if seen[e.ID] {
				t.Errorf("duplicate entry %s across pages", e.ID)
			}
			seen[e.ID] = true
		}
	}
	if len(seen) != 4 {
		t.Errorf("distinct entries seen = %d, want 4", len(seen))
	}

	// Offset past the end: empty items, correct total.
	batch, total, err := ListAuditLog(ctx, pool, 2, 100,
		AuditLogFilter{ActorID: admin, Action: AuditActionUserDeactivated})
	if err != nil {
		t.Fatalf("offset past end: %v", err)
	}
	if len(batch) != 0 || total != 4 {
		t.Errorf("offset past end: len=%d total=%d, want 0/4", len(batch), total)
	}
}

func TestListAuditLogNewestFirst(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)

	admin := adminTestUser(t, pool, uniqueTestEmail("audit-ord-admin"), true)
	victim := adminTestUser(t, pool, uniqueTestEmail("audit-ord-victim"), false)

	if err := DeactivateUser(ctx, pool, admin, victim, "127.0.0.1"); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	if err := ReactivateUser(ctx, pool, admin, victim, "127.0.0.1"); err != nil {
		t.Fatalf("reactivate: %v", err)
	}

	entries, _, err := ListAuditLog(ctx, pool, 10, 0, AuditLogFilter{ActorID: admin, EntityType: "user"})
	if err != nil {
		t.Fatalf("ListAuditLog: %v", err)
	}
	if len(entries) < 2 {
		t.Fatalf("entries = %d, want >= 2", len(entries))
	}
	if entries[0].Action != AuditActionUserReactivated ||
		entries[1].Action != AuditActionUserDeactivated {
		t.Errorf("order = [%s %s], want [reactivated deactivated] (newest first)",
			entries[0].Action, entries[1].Action)
	}
	for i := 1; i < len(entries); i++ {
		if entries[i].At.After(entries[i-1].At) {
			t.Errorf("entries not newest-first at index %d", i)
		}
	}
}
