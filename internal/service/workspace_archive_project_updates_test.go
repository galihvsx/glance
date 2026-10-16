package service

// Workspace archive round-trip for project status updates (C34T2):
// export → import into a fresh workspace preserves status/body/author,
// re-import is idempotent, and an unmapped author skips the update
// with an honest note — never a fabricated author. Real test database,
// no skips. Service-level: exercises StreamWorkspaceExport +
// ImportWorkspaceArchive directly (the HTTP handler tests cover the
// routes; the archive format is the service's contract).

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func setupArchiveUpdatesSource(t *testing.T, prefix string) (context.Context, *pgxpool.Pool, string, string, string, string) {
	t.Helper()
	ctx := context.Background()
	pool := newTestPool(t)
	admin := createTestUser(t, pool, uniqueTestEmail(prefix+"-admin"))
	slug := uniqueTestSlug(prefix + "-ws")
	createTestWorkspace(t, pool, "Updates Co", slug, admin)
	ident := uniqueTestIdentifier()
	createTestProject(t, pool, slug, admin, "Eng", ident)
	author := createTestUser(t, pool, uniqueTestEmail(prefix+"-author"))
	addTestMember(t, pool, slug, admin, author, RoleMember)
	return ctx, pool, slug, ident, admin, author
}

func exportWorkspaceBytes(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug, admin string) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := StreamWorkspaceExport(ctx, pool, slug, admin, &buf); err != nil {
		t.Fatalf("StreamWorkspaceExport: %v", err)
	}
	return buf.Bytes()
}

func importArchive(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug, admin string, archive []byte) ArchiveImportReport {
	t.Helper()
	rep, err := ImportWorkspaceArchive(ctx, pool, slug, admin, bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("ImportWorkspaceArchive: %v", err)
	}
	return rep
}

func countTargetUpdates(t *testing.T, ctx context.Context, pool *pgxpool.Pool, targetWSID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM project_updates pu
		 JOIN projects p ON p.id = pu.project_id
		 WHERE p.workspace_id = $1::uuid`, targetWSID).Scan(&n); err != nil {
		t.Fatalf("count target updates: %v", err)
	}
	return n
}

func targetWorkspaceID(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM workspaces WHERE slug = $1`, slug).Scan(&id); err != nil {
		t.Fatalf("target workspace id: %v", err)
	}
	return id
}

func TestWorkspaceArchiveProjectUpdatesRoundTrip(t *testing.T) {
	ctx, pool, slug, ident, admin, author := setupArchiveUpdatesSource(t, "arcupd")

	// Three updates, two authors, all three health indicators.
	if _, err := CreateProjectUpdate(ctx, pool, slug, ident, admin, ProjectUpdateOnTrack, "Kicked off"); err != nil {
		t.Fatalf("CreateProjectUpdate admin 1: %v", err)
	}
	if _, err := CreateProjectUpdate(ctx, pool, slug, ident, author, ProjectUpdateAtRisk, "Blocked on vendor"); err != nil {
		t.Fatalf("CreateProjectUpdate author: %v", err)
	}
	if _, err := CreateProjectUpdate(ctx, pool, slug, ident, admin, ProjectUpdateOffTrack, "At a standstill"); err != nil {
		t.Fatalf("CreateProjectUpdate admin 2: %v", err)
	}
	var adminEmail, memberEmail string
	if err := pool.QueryRow(ctx, `SELECT email::text FROM users WHERE id = $1::uuid`, admin).Scan(&adminEmail); err != nil {
		t.Fatalf("admin email: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT email::text FROM users WHERE id = $1::uuid`, author).Scan(&memberEmail); err != nil {
		t.Fatalf("author email: %v", err)
	}

	archive := exportWorkspaceBytes(t, ctx, pool, slug, admin)

	// The archive carries the new section with id/project/author/
	// status/body/timestamps.
	var decoded struct {
		ProjectUpdates []exportProjectUpdate `json:"project_updates"`
	}
	if err := json.Unmarshal(archive, &decoded); err != nil {
		t.Fatalf("decode archive: %v", err)
	}
	if len(decoded.ProjectUpdates) != 3 {
		t.Fatalf("archived project_updates = %d, want 3", len(decoded.ProjectUpdates))
	}
	for _, u := range decoded.ProjectUpdates {
		if u.AuthorEmail == nil || (*u.AuthorEmail != adminEmail && *u.AuthorEmail != memberEmail) {
			t.Fatalf("archived update author_email = %v, want %q or %q", u.AuthorEmail, adminEmail, memberEmail)
		}
		if u.Status == "" || u.Body == "" {
			t.Fatalf("archived update missing status/body: %+v", u)
		}
	}

	// Import into a fresh workspace (same admin).
	targetSlug := uniqueTestSlug("arcupd-tgt")
	createTestWorkspace(t, pool, "Target Co", targetSlug, admin)
	rep := importArchive(t, ctx, pool, targetSlug, admin, archive)
	if rep.Imported["project_updates"] != 3 {
		t.Fatalf("report imported.project_updates = %d, want 3 (skipped: %v)", rep.Imported["project_updates"], rep.Skipped.Reasons)
	}
	targetWSID := targetWorkspaceID(t, ctx, pool, targetSlug)

	// Status, body and author email all survive, keyed by body.
	rows, err := pool.Query(ctx,
		`SELECT pu.status, pu.body, u.email::text
		 FROM project_updates pu
		 JOIN projects p ON p.id = pu.project_id
		 JOIN users u ON u.id = pu.author_id
		 WHERE p.workspace_id = $1::uuid`, targetWSID)
	if err != nil {
		t.Fatalf("target updates: %v", err)
	}
	got := map[string][2]string{}
	for rows.Next() {
		var status, body, email string
		if err := rows.Scan(&status, &body, &email); err != nil {
			rows.Close()
			t.Fatalf("scan target update: %v", err)
		}
		got[body] = [2]string{status, email}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("target updates rows: %v", err)
	}
	want := map[string][2]string{
		"Kicked off":        {ProjectUpdateOnTrack, adminEmail},
		"Blocked on vendor": {ProjectUpdateAtRisk, memberEmail},
		"At a standstill":   {ProjectUpdateOffTrack, adminEmail},
	}
	if len(got) != len(want) {
		t.Fatalf("target updates = %v, want %v", got, want)
	}
	for body, wantRow := range want {
		if got[body] != wantRow {
			t.Fatalf("target update %q = %v, want %v", body, got[body], wantRow)
		}
	}

	// Re-import is idempotent: zero new updates, zero new rows.
	rep2 := importArchive(t, ctx, pool, targetSlug, admin, archive)
	if rep2.Imported["project_updates"] != 0 {
		t.Fatalf("re-import imported.project_updates = %d, want 0", rep2.Imported["project_updates"])
	}
	if n := countTargetUpdates(t, ctx, pool, targetWSID); n != 3 {
		t.Fatalf("target updates after re-import = %d, want 3", n)
	}
}

func TestWorkspaceArchiveProjectUpdatesUnmappedAuthor(t *testing.T) {
	ctx, pool, slug, ident, admin, author := setupArchiveUpdatesSource(t, "arcupdun")

	if _, err := CreateProjectUpdate(ctx, pool, slug, ident, author, ProjectUpdateAtRisk, "Author's note"); err != nil {
		t.Fatalf("CreateProjectUpdate author: %v", err)
	}
	if _, err := CreateProjectUpdate(ctx, pool, slug, ident, admin, ProjectUpdateOnTrack, "Admin note"); err != nil {
		t.Fatalf("CreateProjectUpdate admin: %v", err)
	}
	var adminEmail, memberEmail string
	if err := pool.QueryRow(ctx, `SELECT email::text FROM users WHERE id = $1::uuid`, admin).Scan(&adminEmail); err != nil {
		t.Fatalf("admin email: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT email::text FROM users WHERE id = $1::uuid`, author).Scan(&memberEmail); err != nil {
		t.Fatalf("author email: %v", err)
	}

	// Tamper the archive so the author points at a user that does not
	// exist in this instance — ghost UUID + ghost email, everywhere
	// they appear (members section + the update's author fields), the
	// same tamper the HTTP-level unmapped-member test applies. Byte
	// replacement keeps every other field intact.
	archive := exportWorkspaceBytes(t, ctx, pool, slug, admin)
	ghostEmail := uniqueTestEmail("arcupdun-ghost")
	ghostID := "ffffffff-ffff-ffff-ffff-ffffffffffff"
	tampered := bytes.ReplaceAll(archive, []byte(`"`+author+`"`), []byte(`"`+ghostID+`"`))
	tampered = bytes.ReplaceAll(tampered, []byte(memberEmail), []byte(ghostEmail))
	if bytes.Equal(tampered, archive) {
		t.Fatal("tamper failed: author id/email not found in archive")
	}

	targetSlug := uniqueTestSlug("arcupdun-tgt")
	createTestWorkspace(t, pool, "Target Co", targetSlug, admin)
	rep := importArchive(t, ctx, pool, targetSlug, admin, tampered)

	// The admin's update imports; the ghost author's update is
	// skipped with an honest note naming the ghost email.
	if rep.Imported["project_updates"] != 1 {
		t.Fatalf("report imported.project_updates = %d, want 1 (admin's update; skipped: %v)",
			rep.Imported["project_updates"], rep.Skipped.Reasons)
	}
	joined := strings.Join(rep.Skipped.Reasons, "\n")
	if !strings.Contains(joined, ghostEmail) {
		t.Errorf("skipped reasons do not name the unmapped author %q: %q", ghostEmail, joined)
	}

	targetWSID := targetWorkspaceID(t, ctx, pool, targetSlug)
	if n := countTargetUpdates(t, ctx, pool, targetWSID); n != 1 {
		t.Fatalf("target updates = %d, want 1 (ghost author's update skipped)", n)
	}
	var body, status, email string
	if err := pool.QueryRow(ctx,
		`SELECT pu.body, pu.status, u.email::text
		 FROM project_updates pu
		 JOIN projects p ON p.id = pu.project_id
		 JOIN users u ON u.id = pu.author_id
		 WHERE p.workspace_id = $1::uuid`, targetWSID).Scan(&body, &status, &email); err != nil {
		t.Fatalf("target update: %v", err)
	}
	if body != "Admin note" || status != ProjectUpdateOnTrack || email != adminEmail {
		t.Fatalf("target update = %q/%q/%q, want Admin note/on_track/%q — no fabricated author",
			body, status, email, adminEmail)
	}
}

func TestWorkspaceArchiveProjectUpdatesInvalidRowsSkipped(t *testing.T) {
	ctx, pool, slug, ident, admin, _ := setupArchiveUpdatesSource(t, "arcupdinv")

	if _, err := CreateProjectUpdate(ctx, pool, slug, ident, admin, ProjectUpdateOnTrack, "Good update"); err != nil {
		t.Fatalf("CreateProjectUpdate: %v", err)
	}
	archive := exportWorkspaceBytes(t, ctx, pool, slug, admin)

	// Hand-craft an archive whose project_updates section carries an
	// unknown status and an empty body: both rows must skip, the good
	// row must still import. The splice is byte-level on the exported
	// section (like the HTTP-level tamper tests): a generic
	// map-decode/re-encode would sort the top-level keys and reorder
	// the sections, and the importer processes sections in the
	// exporter's dependency order.
	prefix := []byte(`"project_updates":[`)
	start := bytes.Index(archive, prefix)
	if start < 0 {
		t.Fatal("project_updates section not found in archive")
	}
	contentStart := start + len(prefix)
	end := bytes.Index(archive[contentStart:], []byte(`],"issues":`))
	if end < 0 {
		t.Fatal("project_updates section terminator not found in archive")
	}
	contentEnd := contentStart + end
	var good map[string]any
	if err := json.Unmarshal(archive[contentStart:contentEnd], &good); err != nil {
		t.Fatalf("decode good update: %v", err)
	}
	cloneWith := func(id, status, body string) []byte {
		t.Helper()
		row := map[string]any{}
		for k, v := range good {
			row[k] = v
		}
		row["id"] = id
		row["status"] = status
		row["body"] = body
		b, err := json.Marshal(row)
		if err != nil {
			t.Fatalf("marshal bad row: %v", err)
		}
		return b
	}
	badStatus := cloneWith("11111111-1111-1111-1111-111111111111", "green", "Bad status row")
	emptyBody := cloneWith("22222222-2222-2222-2222-222222222222", ProjectUpdateOnTrack, "   ")
	var tampered bytes.Buffer
	tampered.Write(archive[:contentEnd])
	tampered.WriteByte(',')
	tampered.Write(badStatus)
	tampered.WriteByte(',')
	tampered.Write(emptyBody)
	tampered.Write(archive[contentEnd:])

	targetSlug := uniqueTestSlug("arcupdinv-tgt")
	createTestWorkspace(t, pool, "Target Co", targetSlug, admin)
	rep := importArchive(t, ctx, pool, targetSlug, admin, tampered.Bytes())
	if rep.Imported["project_updates"] != 1 {
		t.Fatalf("report imported.project_updates = %d, want 1 (good row only; skipped: %v)",
			rep.Imported["project_updates"], rep.Skipped.Reasons)
	}
	joined := strings.Join(rep.Skipped.Reasons, "\n")
	if !strings.Contains(joined, "unknown status") || !strings.Contains(joined, "body is empty") {
		t.Errorf("skipped reasons do not explain both bad rows: %q", joined)
	}
	targetWSID := targetWorkspaceID(t, ctx, pool, targetSlug)
	if n := countTargetUpdates(t, ctx, pool, targetWSID); n != 1 {
		t.Fatalf("target updates = %d, want 1", n)
	}
}
