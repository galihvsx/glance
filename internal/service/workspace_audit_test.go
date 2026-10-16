package service

// Workspace audit-log tests (C37T0): record + list with the admin
// gate (non-member 404, member non-admin 403), filters (action,
// actor-email substring, entity_type, since/until, invalid inputs),
// and one emitter test per catalogue group (member / project /
// workspace / integration) proving the row lands in the same tx as
// the mutation — plus the secret rule: no meta payload anywhere
// carries a token value, a webhook secret, a full webhook URL, or
// an OTP. Real test database, no skips.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// workspaceAuditRows lists every audit row of a workspace for the
// admin caller, newest first.
func workspaceAuditRows(t *testing.T, pool *pgxpool.Pool, slug, adminID string) []WorkspaceAuditEntry {
	t.Helper()
	entries, _, err := ListWorkspaceAuditLog(context.Background(), pool, slug, adminID, 100, 0, WorkspaceAuditFilter{})
	if err != nil {
		t.Fatalf("ListWorkspaceAuditLog: %v", err)
	}
	return entries
}

// workspaceAuditRowsByAction filters the workspace's audit rows to
// one action.
func workspaceAuditRowsByAction(t *testing.T, pool *pgxpool.Pool, slug, adminID, action string) []WorkspaceAuditEntry {
	t.Helper()
	var out []WorkspaceAuditEntry
	for _, e := range workspaceAuditRows(t, pool, slug, adminID) {
		if e.Action == action {
			out = append(out, e)
		}
	}
	return out
}

func TestWorkspaceAuditLogRecordAndList(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)

	admin := createTestUser(t, pool, uniqueTestEmail("ws-audit-admin"))
	ws := createTestWorkspace(t, pool, "Audit WS", uniqueTestSlug("ws-audit"), admin)

	// Pool variant with an explicit actor email + IP.
	if err := RecordWorkspaceAudit(ctx, pool, ws.ID, WorkspaceAuditEvent{
		ActorID: admin, ActorEmail: "admin@example.com", IP: "203.0.113.9",
		Action: WorkspaceAuditMemberAdded, EntityType: "member", EntityID: "someone",
		Meta: map[string]any{"role": 15, "via": "direct"},
	}); err != nil {
		t.Fatalf("RecordWorkspaceAudit: %v", err)
	}
	// Tx variant resolves the actor email from users; a garbage IP
	// degrades to NULL rather than failing the insert.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := RecordWorkspaceAuditTx(ctx, tx, ws.ID, WorkspaceAuditEvent{
		ActorID: admin, IP: "not-an-ip",
		Action: WorkspaceAuditProjectCreated, EntityType: "project", EntityID: "proj-1",
		Meta: map[string]any{"project_identifier": "ENG"},
	}); err != nil {
		t.Fatalf("RecordWorkspaceAuditTx: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	entries, total, err := ListWorkspaceAuditLog(ctx, pool, ws.Slug, admin, 100, 0, WorkspaceAuditFilter{})
	if err != nil {
		t.Fatalf("ListWorkspaceAuditLog: %v", err)
	}
	if total != 2 || len(entries) != 2 {
		t.Fatalf("entries = %d (total %d), want 2", len(entries), total)
	}
	// Newest first: the project row was recorded second.
	proj := entries[0]
	if proj.Action != WorkspaceAuditProjectCreated || proj.EntityID != "proj-1" {
		t.Errorf("newest = %s/%s, want project.created/proj-1", proj.Action, proj.EntityID)
	}
	if proj.ActorEmail == nil || *proj.ActorEmail == "" {
		t.Errorf("project row actor_email not resolved from users")
	}
	if proj.IP != nil {
		t.Errorf("project row ip = %v, want nil (garbage IP degrades to NULL)", *proj.IP)
	}
	if proj.Meta["project_identifier"] != "ENG" {
		t.Errorf("project row meta = %v, want project_identifier ENG", proj.Meta)
	}
	member := entries[1]
	if member.ActorEmail == nil || *member.ActorEmail != "admin@example.com" {
		t.Errorf("member row actor_email = %v, want explicit admin@example.com", member.ActorEmail)
	}
	if member.IP == nil || *member.IP != "203.0.113.9" {
		ip := "<nil>"
		if member.IP != nil {
			ip = *member.IP
		}
		t.Errorf("member row ip = %q, want 203.0.113.9", ip)
	}
	if member.Meta["via"] != "direct" {
		t.Errorf("member row meta = %v, want via=direct", member.Meta)
	}
	if member.At.IsZero() {
		t.Errorf("member row at is zero")
	}
}

func TestWorkspaceAuditLogGates(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)

	admin := createTestUser(t, pool, uniqueTestEmail("ws-audit-gate-admin"))
	member := createTestUser(t, pool, uniqueTestEmail("ws-audit-gate-member"))
	outsider := createTestUser(t, pool, uniqueTestEmail("ws-audit-gate-out"))
	ws := createTestWorkspace(t, pool, "Gate WS", uniqueTestSlug("ws-audit-gate"), admin)
	if err := UpsertMember(ctx, pool, ws.Slug, admin, member, RoleMember); err != nil {
		t.Fatalf("UpsertMember: %v", err)
	}

	// Member non-admin → 403.
	if _, _, err := ListWorkspaceAuditLog(ctx, pool, ws.Slug, member, 25, 0, WorkspaceAuditFilter{}); err != ErrForbidden {
		t.Errorf("member list: err = %v, want ErrForbidden", err)
	}
	// Non-member → 404-equivalent (ErrNotFound, never a hint the
	// workspace exists).
	if _, _, err := ListWorkspaceAuditLog(ctx, pool, ws.Slug, outsider, 25, 0, WorkspaceAuditFilter{}); err != ErrNotFound {
		t.Errorf("outsider list: err = %v, want ErrNotFound", err)
	}
	// Unknown slug → 404 too.
	if _, _, err := ListWorkspaceAuditLog(ctx, pool, "no-such-ws", admin, 25, 0, WorkspaceAuditFilter{}); err != ErrNotFound {
		t.Errorf("unknown slug list: err = %v, want ErrNotFound", err)
	}
}

func TestWorkspaceAuditLogFilters(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)

	adminEmail := uniqueTestEmail("ws-audit-filter-admin")
	admin := createTestUser(t, pool, adminEmail)
	ws := createTestWorkspace(t, pool, "Filter WS", uniqueTestSlug("ws-audit-filter"), admin)
	target := createTestUser(t, pool, uniqueTestEmail("ws-audit-filter-target"))
	if err := UpsertMember(ctx, pool, ws.Slug, admin, target, RoleMember); err != nil {
		t.Fatalf("UpsertMember: %v", err)
	}
	if _, err := CreateProject(ctx, pool, ws.Slug, admin, "Filter Project", "FLT"); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	rows := workspaceAuditRows(t, pool, ws.Slug, admin)
	if len(rows) != 2 {
		t.Fatalf("seeded rows = %d, want 2", len(rows))
	}
	at := rows[0].At

	// Action exact-match.
	got, total, err := ListWorkspaceAuditLog(ctx, pool, ws.Slug, admin, 25, 0,
		WorkspaceAuditFilter{Action: WorkspaceAuditMemberAdded})
	if err != nil || total != 1 || len(got) != 1 || got[0].Action != WorkspaceAuditMemberAdded {
		t.Errorf("action filter: total=%d entries=%v err=%v, want 1 member.added", total, got, err)
	}
	// Actor: case-insensitive actor_email substring (the admin's
	// email local-part, uppercased).
	got, total, err = ListWorkspaceAuditLog(ctx, pool, ws.Slug, admin, 25, 0,
		WorkspaceAuditFilter{Actor: strings.ToUpper(adminEmail[:12])})
	if err != nil || total != 2 {
		t.Errorf("actor filter: total=%d err=%v, want 2", total, err)
	}
	// Actor substring that matches nobody.
	_, total, err = ListWorkspaceAuditLog(ctx, pool, ws.Slug, admin, 25, 0,
		WorkspaceAuditFilter{Actor: "definitely-no-such-actor"})
	if err != nil || total != 0 {
		t.Errorf("actor no-match: total=%d err=%v, want 0", total, err)
	}
	// Entity type.
	_, total, err = ListWorkspaceAuditLog(ctx, pool, ws.Slug, admin, 25, 0,
		WorkspaceAuditFilter{EntityType: "project"})
	if err != nil || total != 1 {
		t.Errorf("entity_type filter: total=%d err=%v, want 1", total, err)
	}
	// Since/until window around the seeded rows.
	_, total, err = ListWorkspaceAuditLog(ctx, pool, ws.Slug, admin, 25, 0,
		WorkspaceAuditFilter{
			Since: at.Add(-time.Hour).Format(time.RFC3339),
			Until: at.Add(time.Hour).Format(time.RFC3339),
		})
	if err != nil || total != 2 {
		t.Errorf("since/until window: total=%d err=%v, want 2", total, err)
	}
	// Since after the rows → empty.
	_, total, err = ListWorkspaceAuditLog(ctx, pool, ws.Slug, admin, 25, 0,
		WorkspaceAuditFilter{Since: at.Add(time.Hour).Format(time.RFC3339)})
	if err != nil || total != 0 {
		t.Errorf("since-after: total=%d err=%v, want 0", total, err)
	}
	// Invalid since → filter sentinel (handlers map it to 400).
	if _, _, err := ListWorkspaceAuditLog(ctx, pool, ws.Slug, admin, 25, 0,
		WorkspaceAuditFilter{Since: "not-a-date"}); !errors.Is(err, ErrInvalidWorkspaceAuditFilter) {
		t.Errorf("invalid since: err = %v, want ErrInvalidWorkspaceAuditFilter", err)
	}
	// Invalid until → same.
	if _, _, err := ListWorkspaceAuditLog(ctx, pool, ws.Slug, admin, 25, 0,
		WorkspaceAuditFilter{Until: "yesterday"}); !errors.Is(err, ErrInvalidWorkspaceAuditFilter) {
		t.Errorf("invalid until: err = %v, want ErrInvalidWorkspaceAuditFilter", err)
	}
	// Invalid actor_id → same.
	if _, _, err := ListWorkspaceAuditLog(ctx, pool, ws.Slug, admin, 25, 0,
		WorkspaceAuditFilter{ActorID: "not-a-uuid"}); !errors.Is(err, ErrInvalidWorkspaceAuditFilter) {
		t.Errorf("invalid actor_id: err = %v, want ErrInvalidWorkspaceAuditFilter", err)
	}
	// Valid actor_id filters to the admin's rows.
	_, total, err = ListWorkspaceAuditLog(ctx, pool, ws.Slug, admin, 25, 0,
		WorkspaceAuditFilter{ActorID: admin})
	if err != nil || total != 2 {
		t.Errorf("actor_id filter: total=%d err=%v, want 2", total, err)
	}
}

func TestWorkspaceAuditEmittersMember(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)

	admin := createTestUser(t, pool, uniqueTestEmail("ws-audit-m-admin"))
	target := createTestUser(t, pool, uniqueTestEmail("ws-audit-m-target"))
	invitee := createTestUser(t, pool, uniqueTestEmail("ws-audit-m-invitee"))
	ws := createTestWorkspace(t, pool, "Member WS", uniqueTestSlug("ws-audit-m"), admin)

	// Direct add → member.added (via direct).
	if err := UpsertMember(ctx, pool, ws.Slug, admin, target, RoleMember); err != nil {
		t.Fatalf("UpsertMember add: %v", err)
	}
	added := workspaceAuditRowsByAction(t, pool, ws.Slug, admin, WorkspaceAuditMemberAdded)
	if len(added) != 1 || added[0].EntityID != target || added[0].EntityType != "member" {
		t.Fatalf("member.added rows = %v, want 1 for target", added)
	}
	if added[0].Meta["via"] != "direct" {
		t.Errorf("added meta = %v, want via=direct", added[0].Meta)
	}
	// Same-role upsert → no new row (idempotent no-op).
	if err := UpsertMember(ctx, pool, ws.Slug, admin, target, RoleMember); err != nil {
		t.Fatalf("UpsertMember same-role: %v", err)
	}
	if n := len(workspaceAuditRows(t, pool, ws.Slug, admin)); n != 1 {
		t.Fatalf("rows after same-role upsert = %d, want 1", n)
	}
	// Real role change → member.role_changed ONLY (no second added).
	if err := UpsertMember(ctx, pool, ws.Slug, admin, target, RoleViewer); err != nil {
		t.Fatalf("UpsertMember role change: %v", err)
	}
	changed := workspaceAuditRowsByAction(t, pool, ws.Slug, admin, WorkspaceAuditMemberRoleChanged)
	if len(changed) != 1 {
		t.Fatalf("role_changed rows = %d, want 1", len(changed))
	}
	if changed[0].Meta["from_role"] != float64(RoleMember) || changed[0].Meta["to_role"] != float64(RoleViewer) {
		t.Errorf("role_changed meta = %v, want from_role=15 to_role=10", changed[0].Meta)
	}
	// Invite → member.invited (entity: email) + member.added (via invite).
	inviteeEmail := ""
	{
		var email string
		if qerr := pool.QueryRow(ctx, `SELECT email FROM users WHERE id = $1::uuid`, invitee).Scan(&email); qerr != nil {
			t.Fatalf("lookup invitee email: %v", qerr)
		}
		inviteeEmail = email
	}
	results, err := InviteMembers(ctx, pool, ws.Slug, admin, []string{inviteeEmail}, 0)
	if err != nil {
		t.Fatalf("InviteMembers: %v", err)
	}
	if len(results) != 1 || results[0].Status != InviteStatusInvited {
		t.Fatalf("invite results = %v, want 1 invited", results)
	}
	invited := workspaceAuditRowsByAction(t, pool, ws.Slug, admin, WorkspaceAuditMemberInvited)
	if len(invited) != 1 || invited[0].EntityType != "invite" || invited[0].EntityID != inviteeEmail {
		t.Fatalf("member.invited rows = %v, want 1 with entity invite/%s", invited, inviteeEmail)
	}
	added = workspaceAuditRowsByAction(t, pool, ws.Slug, admin, WorkspaceAuditMemberAdded)
	if len(added) != 2 || added[0].Meta["via"] != "invite" || added[0].EntityID != invitee {
		t.Fatalf("member.added rows after invite = %v, want newest via=invite for invitee", added)
	}
	// Remove → member.removed carrying the role the member held.
	if err := RemoveMember(ctx, pool, ws.Slug, admin, target); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	removed := workspaceAuditRowsByAction(t, pool, ws.Slug, admin, WorkspaceAuditMemberRemoved)
	if len(removed) != 1 || removed[0].EntityID != target {
		t.Fatalf("member.removed rows = %v, want 1 for target", removed)
	}
	if removed[0].Meta["role"] != float64(RoleViewer) {
		t.Errorf("removed meta = %v, want role=10 (viewer at removal)", removed[0].Meta)
	}
	// Refused mutations record nothing.
	before := len(workspaceAuditRows(t, pool, ws.Slug, admin))
	if err := UpsertMember(ctx, pool, ws.Slug, admin, target, 999); err == nil {
		t.Fatalf("UpsertMember invalid role: expected error")
	}
	if err := RemoveMember(ctx, pool, ws.Slug, admin, target); err == nil {
		t.Fatalf("RemoveMember non-member: expected error")
	}
	if n := len(workspaceAuditRows(t, pool, ws.Slug, admin)); n != before {
		t.Errorf("rows after refused mutations = %d, want %d", n, before)
	}
}

func TestWorkspaceAuditEmittersProject(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)

	admin := createTestUser(t, pool, uniqueTestEmail("ws-audit-p-admin"))
	ws := createTestWorkspace(t, pool, "Project WS", uniqueTestSlug("ws-audit-p"), admin)

	p, err := CreateProject(ctx, pool, ws.Slug, admin, "Audit Project", "AUD")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	created := workspaceAuditRowsByAction(t, pool, ws.Slug, admin, WorkspaceAuditProjectCreated)
	if len(created) != 1 || created[0].EntityID != p.ID || created[0].Meta["project_identifier"] != "AUD" {
		t.Fatalf("project.created rows = %v, want 1 for project", created)
	}

	if _, err := ArchiveProject(ctx, pool, ws.Slug, "AUD", admin, true); err != nil {
		t.Fatalf("ArchiveProject: %v", err)
	}
	archived := workspaceAuditRowsByAction(t, pool, ws.Slug, admin, WorkspaceAuditProjectArchived)
	if len(archived) != 1 || archived[0].Meta["project_identifier"] != "AUD" {
		t.Fatalf("project.archived rows = %v, want 1", archived)
	}
	// Idempotent re-archive → no second row.
	if _, err := ArchiveProject(ctx, pool, ws.Slug, "AUD", admin, true); err != nil {
		t.Fatalf("ArchiveProject idempotent: %v", err)
	}
	if n := len(workspaceAuditRowsByAction(t, pool, ws.Slug, admin, WorkspaceAuditProjectArchived)); n != 1 {
		t.Fatalf("archived rows after re-archive = %d, want 1", n)
	}
	// Restore → project.restored.
	if _, err := ArchiveProject(ctx, pool, ws.Slug, "AUD", admin, false); err != nil {
		t.Fatalf("ArchiveProject restore: %v", err)
	}
	if n := len(workspaceAuditRowsByAction(t, pool, ws.Slug, admin, WorkspaceAuditProjectRestored)); n != 1 {
		t.Fatalf("restored rows = %d, want 1", n)
	}
	// Delete → project.deleted.
	if err := DeleteProject(ctx, pool, ws.Slug, "AUD", admin, "AUD", false); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	deleted := workspaceAuditRowsByAction(t, pool, ws.Slug, admin, WorkspaceAuditProjectDeleted)
	if len(deleted) != 1 || deleted[0].EntityID != p.ID {
		t.Fatalf("project.deleted rows = %v, want 1 for project", deleted)
	}
}

func TestWorkspaceAuditEmittersWorkspaceSettings(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)

	admin := createTestUser(t, pool, uniqueTestEmail("ws-audit-ws-admin"))
	ws := createTestWorkspace(t, pool, "Settings WS", uniqueTestSlug("ws-audit-ws"), admin)

	// No-change update → no row.
	if _, err := UpdateWorkspace(ctx, pool, ws.Slug, admin, "Settings WS", nil); err != nil {
		t.Fatalf("UpdateWorkspace no-change: %v", err)
	}
	if n := len(workspaceAuditRows(t, pool, ws.Slug, admin)); n != 0 {
		t.Fatalf("rows after no-change update = %d, want 0", n)
	}
	// Rename → settings_updated with changed_fields ["name"] — field
	// names only, never the new value.
	if _, err := UpdateWorkspace(ctx, pool, ws.Slug, admin, "Renamed WS", nil); err != nil {
		t.Fatalf("UpdateWorkspace rename: %v", err)
	}
	rows := workspaceAuditRowsByAction(t, pool, ws.Slug, admin, WorkspaceAuditWorkspaceSettingsUpdated)
	if len(rows) != 1 || rows[0].EntityType != "workspace" || rows[0].EntityID != ws.ID {
		t.Fatalf("settings_updated rows = %v, want 1 for workspace", rows)
	}
	metaJSON, _ := json.Marshal(rows[0].Meta)
	if strings.Contains(string(metaJSON), "Renamed WS") {
		t.Errorf("settings_updated meta carries the new value: %s", metaJSON)
	}
	fields, ok := rows[0].Meta["changed_fields"].([]any)
	if !ok || len(fields) != 1 || fields[0] != "name" {
		t.Errorf("changed_fields = %v, want [name]", rows[0].Meta["changed_fields"])
	}
	// Slug change → changed_fields ["slug"].
	newSlug := uniqueTestSlug("ws-audit-ws-renamed")
	if _, err := UpdateWorkspace(ctx, pool, ws.Slug, admin, "Renamed WS", &newSlug); err != nil {
		t.Fatalf("UpdateWorkspace slug change: %v", err)
	}
	rows = workspaceAuditRowsByAction(t, pool, newSlug, admin, WorkspaceAuditWorkspaceSettingsUpdated)
	if len(rows) != 2 {
		t.Fatalf("settings_updated rows after slug change = %d, want 2", len(rows))
	}
	fields, ok = rows[0].Meta["changed_fields"].([]any)
	if !ok || len(fields) != 1 || fields[0] != "slug" {
		t.Errorf("changed_fields after slug change = %v, want [slug]", rows[0].Meta["changed_fields"])
	}
}

func TestWorkspaceAuditEmittersIntegration(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)

	admin := createTestUser(t, pool, uniqueTestEmail("ws-audit-i-admin"))
	ws := createTestWorkspace(t, pool, "Integration WS", uniqueTestSlug("ws-audit-i"), admin)

	// Webhook create: the URL embeds a token in its query string —
	// meta must carry the host only, never the URL or the token.
	hookURL := "https://example.com/hooks/abc?token=supersecret-token-value"
	wh, err := CreateWebhook(ctx, pool, ws.Slug, admin, WebhookInput{URL: hookURL, Events: []string{}})
	if err != nil {
		t.Fatalf("CreateWebhook: %v", err)
	}
	created := workspaceAuditRowsByAction(t, pool, ws.Slug, admin, WorkspaceAuditIntegrationWebhookCreated)
	if len(created) != 1 || created[0].EntityType != "webhook" || created[0].EntityID != wh.ID {
		t.Fatalf("webhook_created rows = %v, want 1 for webhook", created)
	}
	if created[0].Meta["host"] != "example.com" {
		t.Errorf("webhook_created meta = %v, want host=example.com", created[0].Meta)
	}
	metaJSON, _ := json.Marshal(created[0].Meta)
	if strings.Contains(string(metaJSON), "supersecret") || strings.Contains(string(metaJSON), hookURL) {
		t.Errorf("webhook_created meta leaks URL/token: %s", metaJSON)
	}
	// Webhook delete → webhook_deleted, host only.
	if err := DeleteWebhook(ctx, pool, ws.Slug, admin, wh.ID); err != nil {
		t.Fatalf("DeleteWebhook: %v", err)
	}
	deleted := workspaceAuditRowsByAction(t, pool, ws.Slug, admin, WorkspaceAuditIntegrationWebhookDeleted)
	if len(deleted) != 1 || deleted[0].Meta["host"] != "example.com" {
		t.Fatalf("webhook_deleted rows = %v, want 1 with host=example.com", deleted)
	}
	// Double delete → NotFound, no extra row.
	if err := DeleteWebhook(ctx, pool, ws.Slug, admin, wh.ID); err == nil {
		t.Fatalf("DeleteWebhook twice: expected error")
	}
	if n := len(workspaceAuditRowsByAction(t, pool, ws.Slug, admin, WorkspaceAuditIntegrationWebhookDeleted)); n != 1 {
		t.Fatalf("webhook_deleted rows after double delete = %d, want 1", n)
	}

	// GitHub secret set → github_configured with {configured: true}
	// only — never the secret.
	ghSecret := "gh-secret-abc-123"
	if _, err := SetWorkspaceGitHubSecret(ctx, pool, ws.Slug, admin, &ghSecret); err != nil {
		t.Fatalf("SetWorkspaceGitHubSecret: %v", err)
	}
	gh := workspaceAuditRowsByAction(t, pool, ws.Slug, admin, WorkspaceAuditIntegrationGitHubConfigured)
	if len(gh) != 1 || gh[0].EntityType != "integration" || gh[0].EntityID != "github" {
		t.Fatalf("github_configured rows = %v, want 1 for integration/github", gh)
	}
	if gh[0].Meta["configured"] != true {
		t.Errorf("github_configured meta = %v, want configured=true", gh[0].Meta)
	}
	metaJSON, _ = json.Marshal(gh[0].Meta)
	if strings.Contains(string(metaJSON), ghSecret) {
		t.Errorf("github_configured meta leaks the secret: %s", metaJSON)
	}
	// Clearing the GitHub secret emits nothing (no
	// github_disconnected event in the v1 catalogue).
	if _, err := SetWorkspaceGitHubSecret(ctx, pool, ws.Slug, admin, nil); err != nil {
		t.Fatalf("SetWorkspaceGitHubSecret clear: %v", err)
	}
	if n := len(workspaceAuditRowsByAction(t, pool, ws.Slug, admin, WorkspaceAuditIntegrationGitHubConfigured)); n != 1 {
		t.Fatalf("github_configured rows after clear = %d, want 1", n)
	}

	// Slack set → slack_configured; the URL is a secret and must not
	// appear in meta.
	slackURL := "https://hooks.slack.com/services/T000/B000/SECRETXYZ"
	if _, err := SetWorkspaceSlackURL(ctx, pool, ws.Slug, admin, &slackURL); err != nil {
		t.Fatalf("SetWorkspaceSlackURL: %v", err)
	}
	slack := workspaceAuditRowsByAction(t, pool, ws.Slug, admin, WorkspaceAuditIntegrationSlackConfigured)
	if len(slack) != 1 || slack[0].EntityID != "slack" {
		t.Fatalf("slack_configured rows = %v, want 1 for integration/slack", slack)
	}
	metaJSON, _ = json.Marshal(slack[0].Meta)
	if strings.Contains(string(metaJSON), "SECRETXYZ") || strings.Contains(string(metaJSON), slackURL) {
		t.Errorf("slack_configured meta leaks the URL: %s", metaJSON)
	}
	// Slack clear → slack_disconnected; a second clear (already
	// unconfigured) emits nothing.
	if _, err := SetWorkspaceSlackURL(ctx, pool, ws.Slug, admin, nil); err != nil {
		t.Fatalf("SetWorkspaceSlackURL clear: %v", err)
	}
	disconnected := workspaceAuditRowsByAction(t, pool, ws.Slug, admin, WorkspaceAuditIntegrationSlackDisconnected)
	if len(disconnected) != 1 {
		t.Fatalf("slack_disconnected rows = %d, want 1", len(disconnected))
	}
	if _, err := SetWorkspaceSlackURL(ctx, pool, ws.Slug, admin, nil); err != nil {
		t.Fatalf("SetWorkspaceSlackURL clear again: %v", err)
	}
	if n := len(workspaceAuditRowsByAction(t, pool, ws.Slug, admin, WorkspaceAuditIntegrationSlackDisconnected)); n != 1 {
		t.Fatalf("slack_disconnected rows after second clear = %d, want 1", n)
	}
}

func TestWorkspaceAuditCSV(t *testing.T) {
	at := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	actorEmail := "admin@example.com"
	ip := "203.0.113.9"
	entries := []WorkspaceAuditEntry{
		{
			ID: "00000000-0000-0000-0000-000000000001", At: at,
			ActorEmail: &actorEmail, Action: WorkspaceAuditMemberAdded,
			EntityType: "member", EntityID: "user-1", IP: &ip,
			Meta: map[string]any{"role": 15, "via": "direct"},
		},
		{
			ID: "00000000-0000-0000-0000-000000000002", At: at,
			Action:     WorkspaceAuditWorkspaceSettingsUpdated,
			EntityType: "workspace", EntityID: "ws-1",
			Meta: map[string]any{"changed_fields": []any{"name"}},
		},
	}
	out, err := WorkspaceAuditCSV(entries)
	if err != nil {
		t.Fatalf("WorkspaceAuditCSV: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("CSV lines = %d, want 3 (header + 2 rows):\n%s", len(lines), out)
	}
	if lines[0] != "at,actor_email,action,entity_type,entity_id,ip,meta" {
		t.Errorf("CSV header = %q", lines[0])
	}
	if !strings.Contains(lines[1], "admin@example.com,member.added,member,user-1,203.0.113.9") {
		t.Errorf("CSV row 1 = %q", lines[1])
	}
	// Meta is compact JSON, CSV-quoted because it contains commas.
	if !strings.Contains(lines[1], `"{""role"":15,""via"":""direct""}"`) {
		t.Errorf("CSV row 1 meta not compact-quoted JSON: %q", lines[1])
	}
	if !strings.Contains(lines[2], "workspace.settings_updated,workspace,ws-1,,") {
		t.Errorf("CSV row 2 = %q (nil email/ip render empty)", lines[2])
	}
}
