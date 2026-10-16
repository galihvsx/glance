package service

// Workspace audit-log tamper-evidence (C37T2): the DB-level BEFORE
// UPDATE OR DELETE trigger on workspace_audit_log rejects direct
// UPDATE/DELETE (append-only at the DB level, not just by convention)
// while still allowing the table's own FK lifecycle — workspace
// DELETE cascades its audit rows, user DELETE NULLs actor_id — and
// the workspace archive export/import excludes the audit log
// (history is not portable: importing audit rows would fabricate
// witnessed history). Real test database, no skips.

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func countWorkspaceAuditRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool, wsID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM workspace_audit_log WHERE workspace_id = $1::uuid`, wsID).Scan(&n); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	return n
}

func seedWorkspaceAuditRow(t *testing.T, ctx context.Context, pool *pgxpool.Pool, wsID, actorID string) {
	t.Helper()
	if err := RecordWorkspaceAudit(ctx, pool, wsID, WorkspaceAuditEvent{
		ActorID: actorID, ActorEmail: "tamper-admin@example.com",
		Action: WorkspaceAuditMemberAdded, EntityType: "member", EntityID: "someone",
		Meta: map[string]any{"role": 15, "via": "direct"},
	}); err != nil {
		t.Fatalf("RecordWorkspaceAudit: %v", err)
	}
}

func TestWorkspaceAuditLogImmutableUpdate(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)

	admin := createTestUser(t, pool, uniqueTestEmail("ws-audit-tamper-admin"))
	ws := createTestWorkspace(t, pool, "Tamper WS", uniqueTestSlug("ws-audit-tamper"), admin)
	seedWorkspaceAuditRow(t, ctx, pool, ws.ID, admin)

	// Direct UPDATE of the meta payload fails: append-only.
	_, err := pool.Exec(ctx,
		`UPDATE workspace_audit_log SET meta = '{"role": 99}' WHERE workspace_id = $1::uuid`, ws.ID)
	if err == nil {
		t.Fatalf("UPDATE audit row: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "append-only") {
		t.Errorf("UPDATE error = %q, want it to mention append-only", err)
	}
	// The row is untouched.
	var meta string
	if err := pool.QueryRow(ctx,
		`SELECT meta::text FROM workspace_audit_log WHERE workspace_id = $1::uuid`, ws.ID).Scan(&meta); err != nil {
		t.Fatalf("read audit row: %v", err)
	}
	if !strings.Contains(meta, `"role": 15`) {
		t.Errorf("audit row meta after blocked UPDATE = %s, want role 15 intact", meta)
	}
	// Even a seemingly innocent UPDATE (actor_id only) fails at depth 1.
	other := createTestUser(t, pool, uniqueTestEmail("ws-audit-tamper-other"))
	if _, err := pool.Exec(ctx,
		`UPDATE workspace_audit_log SET actor_id = $2::uuid WHERE workspace_id = $1::uuid`, ws.ID, other); err == nil {
		t.Errorf("UPDATE actor_id: expected error, got nil")
	}
	if n := countWorkspaceAuditRows(t, ctx, pool, ws.ID); n != 1 {
		t.Errorf("audit rows = %d, want 1", n)
	}
	// INSERTs are unaffected: the log stays appendable.
	seedWorkspaceAuditRow(t, ctx, pool, ws.ID, admin)
	if n := countWorkspaceAuditRows(t, ctx, pool, ws.ID); n != 2 {
		t.Errorf("audit rows after second insert = %d, want 2", n)
	}
}

func TestWorkspaceAuditLogImmutableDelete(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)

	admin := createTestUser(t, pool, uniqueTestEmail("ws-audit-tamper-del-admin"))
	ws := createTestWorkspace(t, pool, "Tamper Del WS", uniqueTestSlug("ws-audit-tamper-del"), admin)
	seedWorkspaceAuditRow(t, ctx, pool, ws.ID, admin)

	// Direct DELETE fails: append-only.
	_, err := pool.Exec(ctx,
		`DELETE FROM workspace_audit_log WHERE workspace_id = $1::uuid`, ws.ID)
	if err == nil {
		t.Fatalf("DELETE audit row: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "append-only") {
		t.Errorf("DELETE error = %q, want it to mention append-only", err)
	}
	if n := countWorkspaceAuditRows(t, ctx, pool, ws.ID); n != 1 {
		t.Errorf("audit rows after blocked DELETE = %d, want 1", n)
	}
}

func TestWorkspaceAuditLogImmutableAllowsFKLifecycle(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)

	// Workspace DELETE cascades its audit rows: DeleteWorkspace must
	// keep working with the trigger in place.
	admin := createTestUser(t, pool, uniqueTestEmail("ws-audit-tamper-fk-admin"))
	ws := createTestWorkspace(t, pool, "FK WS", uniqueTestSlug("ws-audit-tamper-fk"), admin)
	seedWorkspaceAuditRow(t, ctx, pool, ws.ID, admin)
	seedWorkspaceAuditRow(t, ctx, pool, ws.ID, admin)
	if n := countWorkspaceAuditRows(t, ctx, pool, ws.ID); n != 2 {
		t.Fatalf("seeded audit rows = %d, want 2", n)
	}
	if err := DeleteWorkspace(ctx, pool, ws.Slug, admin); err != nil {
		t.Fatalf("DeleteWorkspace with audit rows: %v (trigger must allow the FK cascade)", err)
	}
	if n := countWorkspaceAuditRows(t, ctx, pool, ws.ID); n != 0 {
		t.Errorf("audit rows after workspace delete = %d, want 0 (cascade)", n)
	}

	// User DELETE NULLs actor_id (ON DELETE SET NULL) and preserves
	// the actor_email snapshot: the row survives its actor.
	admin2 := createTestUser(t, pool, uniqueTestEmail("ws-audit-tamper-fk-admin2"))
	ws2 := createTestWorkspace(t, pool, "FK WS 2", uniqueTestSlug("ws-audit-tamper-fk2"), admin2)
	actor := createTestUser(t, pool, uniqueTestEmail("ws-audit-tamper-fk-actor"))
	var actorEmail string
	if err := pool.QueryRow(ctx, `SELECT email::text FROM users WHERE id = $1::uuid`, actor).Scan(&actorEmail); err != nil {
		t.Fatalf("actor email: %v", err)
	}
	if err := RecordWorkspaceAudit(ctx, pool, ws2.ID, WorkspaceAuditEvent{
		ActorID: actor, Action: WorkspaceAuditMemberAdded,
		EntityType: "member", EntityID: "someone",
		Meta: map[string]any{"role": 15},
	}); err != nil {
		t.Fatalf("RecordWorkspaceAudit: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1::uuid`, actor); err != nil {
		t.Fatalf("DELETE user (actor): %v (trigger must allow the FK SET NULL)", err)
	}
	var actorID *string
	var gotEmail *string
	if err := pool.QueryRow(ctx,
		`SELECT actor_id::text, actor_email FROM workspace_audit_log WHERE workspace_id = $1::uuid`,
		ws2.ID).Scan(&actorID, &gotEmail); err != nil {
		t.Fatalf("read audit row after actor delete: %v", err)
	}
	if actorID != nil {
		t.Errorf("actor_id after actor delete = %q, want NULL", *actorID)
	}
	if gotEmail == nil || *gotEmail != actorEmail {
		t.Errorf("actor_email after actor delete = %v, want snapshot %q", gotEmail, actorEmail)
	}
}

func TestWorkspaceAuditLogArchiveExcludesAuditLog(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)

	admin := createTestUser(t, pool, uniqueTestEmail("ws-audit-tamper-arc-admin"))
	slug := uniqueTestSlug("ws-audit-tamper-arc")
	createTestWorkspace(t, pool, "Archive Co", slug, admin)
	ident := uniqueTestIdentifier()
	createTestProject(t, pool, slug, admin, "Eng", ident) // emits project.created
	target := createTestUser(t, pool, uniqueTestEmail("ws-audit-tamper-arc-target"))
	if err := UpsertMember(ctx, pool, slug, admin, target, RoleMember); err != nil { // emits member.added
		t.Fatalf("UpsertMember: %v", err)
	}
	sourceWSID := targetWorkspaceID(t, ctx, pool, slug)
	if n := countWorkspaceAuditRows(t, ctx, pool, sourceWSID); n < 2 {
		t.Fatalf("source audit rows = %d, want >= 2", n)
	}

	archive := exportWorkspaceBytes(t, ctx, pool, slug, admin)

	// No audit section in the archive, under any plausible key.
	var top map[string]json.RawMessage
	if err := json.Unmarshal(archive, &top); err != nil {
		t.Fatalf("decode archive top level: %v", err)
	}
	for _, key := range []string{"audit_log", "workspace_audit_log", "audit"} {
		if _, ok := top[key]; ok {
			t.Errorf("archive carries a %q section — the audit log must be excluded", key)
		}
	}
	if bytes.Contains(archive, []byte("audit_log")) {
		t.Errorf("archive bytes contain \"audit_log\" — the audit log must be excluded")
	}

	// Import into a fresh workspace: no audit rows are fabricated.
	targetSlug := uniqueTestSlug("ws-audit-tamper-arc-tgt")
	createTestWorkspace(t, pool, "Target Co", targetSlug, admin)
	rep := importArchive(t, ctx, pool, targetSlug, admin, archive)
	if rep.Imported["audit_log"] != 0 || rep.Imported["workspace_audit_log"] != 0 {
		t.Errorf("import report claims audit rows imported: %v", rep.Imported)
	}
	targetWSID := targetWorkspaceID(t, ctx, pool, targetSlug)
	if n := countWorkspaceAuditRows(t, ctx, pool, targetWSID); n != 0 {
		t.Errorf("target audit rows after import = %d, want 0 — no fabricated history", n)
	}
	// The source keeps its own rows: export never consumes them.
	if n := countWorkspaceAuditRows(t, ctx, pool, sourceWSID); n < 2 {
		t.Errorf("source audit rows after export = %d, want >= 2", n)
	}
}

func TestWorkspaceAuditLogArchiveIgnoresFabricatedAuditSection(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)

	admin := createTestUser(t, pool, uniqueTestEmail("ws-audit-tamper-fab-admin"))
	slug := uniqueTestSlug("ws-audit-tamper-fab")
	createTestWorkspace(t, pool, "Fab Co", slug, admin)
	ident := uniqueTestIdentifier()
	createTestProject(t, pool, slug, admin, "Eng", ident)

	archive := exportWorkspaceBytes(t, ctx, pool, slug, admin)

	// Hand-craft an audit_log section claiming an admin granted
	// themselves the admin role — the kind of history an archive
	// must never be able to smuggle in.
	fabricated := `{"id":"99999999-9999-9999-9999-999999999999","at":"2026-10-16T00:00:00Z",` +
		`"actor_email":"attacker@example.com","action":"member.role_changed",` +
		`"entity_type":"member","entity_id":"attacker-user","ip":null,` +
		`"meta":{"from_role":15,"to_role":20}}`
	anchor := []byte(`,"objectives":`)
	if !bytes.Contains(archive, anchor) {
		t.Fatalf("archive has no objectives section anchor for the splice")
	}
	splice := []byte(`,"audit_log":[` + fabricated + `]`)
	splice = append(splice, anchor...)
	tampered := bytes.Replace(archive, anchor, splice, 1)

	targetSlug := uniqueTestSlug("ws-audit-tamper-fab-tgt")
	createTestWorkspace(t, pool, "Target Co", targetSlug, admin)
	rep := importArchive(t, ctx, pool, targetSlug, admin, tampered)
	if rep.Imported["audit_log"] != 0 {
		t.Errorf("import report claims audit rows imported: %v", rep.Imported)
	}
	targetWSID := targetWorkspaceID(t, ctx, pool, targetSlug)
	if n := countWorkspaceAuditRows(t, ctx, pool, targetWSID); n != 0 {
		t.Errorf("target audit rows after importing a fabricated audit section = %d, want 0", n)
	}
}
