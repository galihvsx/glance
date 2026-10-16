package service

// Workspace archive round-trip for page↔issue links (C36T2):
// export → import into a fresh workspace preserves links, re-import
// is idempotent, an unresolvable side skips the link with an honest
// note — never dropped silently, never a fabricated page/issue — and
// a link whose archived page id is stale still restores via the
// title fallback (id-then-title). Real test database, no skips.
// Service-level: exercises StreamWorkspaceExport +
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

func setupArchivePageIssueLinksSource(t *testing.T, prefix string) (context.Context, *pgxpool.Pool, string, string, string) {
	t.Helper()
	ctx := context.Background()
	pool := newTestPool(t)
	admin := createTestUser(t, pool, uniqueTestEmail(prefix+"-admin"))
	slug := uniqueTestSlug(prefix + "-ws")
	createTestWorkspace(t, pool, "Links Co", slug, admin)
	ident := uniqueTestIdentifier()
	createTestProject(t, pool, slug, admin, "Eng", ident)
	return ctx, pool, slug, ident, admin
}

func countTargetPageIssueLinks(t *testing.T, ctx context.Context, pool *pgxpool.Pool, targetWSID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM page_issue_links pil
		 JOIN pages pg ON pg.id = pil.page_id
		 JOIN projects p ON p.id = pg.project_id
		 WHERE p.workspace_id = $1::uuid`, targetWSID).Scan(&n); err != nil {
		t.Fatalf("count target page-issue links: %v", err)
	}
	return n
}

// targetPageIssueLinks returns the target workspace's links as
// (page title, issue display id) pairs, ordered for comparison.
func targetPageIssueLinks(t *testing.T, ctx context.Context, pool *pgxpool.Pool, targetWSID string) [][2]string {
	t.Helper()
	rows, err := pool.Query(ctx,
		`SELECT pg.title, (p.identifier || '-' || i.sequence_id)
		 FROM page_issue_links pil
		 JOIN pages pg ON pg.id = pil.page_id
		 JOIN issues i ON i.id = pil.issue_id
		 JOIN projects p ON p.id = pg.project_id
		 WHERE p.workspace_id = $1::uuid
		 ORDER BY pg.title, i.sequence_id`, targetWSID)
	if err != nil {
		t.Fatalf("target page-issue links: %v", err)
	}
	defer rows.Close()
	var out [][2]string
	for rows.Next() {
		var pair [2]string
		if err := rows.Scan(&pair[0], &pair[1]); err != nil {
			t.Fatalf("scan target link: %v", err)
		}
		out = append(out, pair)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("target links rows: %v", err)
	}
	return out
}

func decodeArchivePageIssueLinks(t *testing.T, archive []byte) []exportPageIssueLink {
	t.Helper()
	prefix := []byte(`"page_issue_links":[`)
	start := bytes.Index(archive, prefix)
	if start < 0 {
		t.Fatal("page_issue_links section not found in archive")
	}
	contentStart := start + len(prefix)
	end := bytes.Index(archive[contentStart:], []byte(`],"objectives":`))
	if end < 0 {
		t.Fatal("page_issue_links section terminator not found in archive")
	}
	contentEnd := contentStart + end
	var rows []exportPageIssueLink
	wrapped := make([]byte, 0, contentEnd-contentStart+2)
	wrapped = append(wrapped, '[')
	wrapped = append(wrapped, archive[contentStart:contentEnd]...)
	wrapped = append(wrapped, ']')
	if err := json.Unmarshal(wrapped, &rows); err != nil {
		t.Fatalf("decode page_issue_links: %v", err)
	}
	if rows == nil {
		rows = []exportPageIssueLink{}
	}
	return rows
}

// splicePageIssueLinksSection replaces the archive's
// page_issue_links section with rows, byte-level (the same splice
// discipline as the project-updates tamper tests: a generic
// map-decode/re-encode would sort the top-level keys and reorder
// the sections, and the importer processes sections in the
// exporter's dependency order).
func splicePageIssueLinksSection(t *testing.T, archive []byte, rows []exportPageIssueLink) []byte {
	t.Helper()
	prefix := []byte(`"page_issue_links":[`)
	start := bytes.Index(archive, prefix)
	if start < 0 {
		t.Fatal("page_issue_links section not found in archive")
	}
	contentStart := start + len(prefix)
	end := bytes.Index(archive[contentStart:], []byte(`],"objectives":`))
	if end < 0 {
		t.Fatal("page_issue_links section terminator not found in archive")
	}
	contentEnd := contentStart + end
	encoded, err := json.Marshal(rows)
	if err != nil {
		t.Fatalf("marshal page_issue_links: %v", err)
	}
	// encoded includes the surrounding brackets; strip them so the
	// replacement is the bare element list.
	encoded = encoded[1 : len(encoded)-1]
	var out bytes.Buffer
	out.Write(archive[:contentStart])
	out.Write(encoded)
	out.Write(archive[contentEnd:])
	return out.Bytes()
}

func TestWorkspaceArchivePageIssueLinksRoundTrip(t *testing.T) {
	ctx, pool, slug, ident, admin := setupArchivePageIssueLinksSource(t, "arcpil")

	spec := mustCreatePage(t, ctx, pool, slug, ident, admin, "Spec", nil)
	home := mustCreatePage(t, ctx, pool, slug, ident, admin, "Home", nil)
	iss1 := createTestIssue(t, pool, slug, ident, admin, "Ship login")
	iss2 := createTestIssue(t, pool, slug, ident, admin, "Ship billing")

	if _, err := LinkPageIssue(ctx, pool, slug, ident, spec.ID, admin, iss1.ID); err != nil {
		t.Fatalf("LinkPageIssue spec↔login: %v", err)
	}
	if _, err := LinkPageIssue(ctx, pool, slug, ident, spec.ID, admin, iss2.ID); err != nil {
		t.Fatalf("LinkPageIssue spec↔billing: %v", err)
	}
	if _, err := LinkPageIssue(ctx, pool, slug, ident, home.ID, admin, iss1.ID); err != nil {
		t.Fatalf("LinkPageIssue home↔login: %v", err)
	}

	archive := exportWorkspaceBytes(t, ctx, pool, slug, admin)

	// The archive carries the new section: project + page title +
	// issue display id + creator per link.
	rows := decodeArchivePageIssueLinks(t, archive)
	if len(rows) != 3 {
		t.Fatalf("archived page_issue_links = %d, want 3", len(rows))
	}
	for _, l := range rows {
		if l.ProjectIdentifier != ident || l.PageTitle == "" || l.IssueDisplayID == "" {
			t.Fatalf("archived link missing project/page/issue keys: %+v", l)
		}
		if l.CreatedBy == nil || *l.CreatedBy != admin {
			t.Fatalf("archived link created_by = %v, want admin %q", l.CreatedBy, admin)
		}
		if l.IssueDisplayID != iss1.DisplayID && l.IssueDisplayID != iss2.DisplayID {
			t.Fatalf("archived link issue_display_id = %q, want %q or %q",
				l.IssueDisplayID, iss1.DisplayID, iss2.DisplayID)
		}
	}

	// Import into a fresh workspace (same admin).
	targetSlug := uniqueTestSlug("arcpil-tgt")
	createTestWorkspace(t, pool, "Target Co", targetSlug, admin)
	rep := importArchive(t, ctx, pool, targetSlug, admin, archive)
	if rep.Imported["page_issue_links"] != 3 {
		t.Fatalf("report imported.page_issue_links = %d, want 3 (skipped: %v)",
			rep.Imported["page_issue_links"], rep.Skipped.Reasons)
	}
	targetWSID := targetWorkspaceID(t, ctx, pool, targetSlug)
	got := targetPageIssueLinks(t, ctx, pool, targetWSID)
	want := [][2]string{
		{"Home", iss1.DisplayID},
		{"Spec", iss1.DisplayID},
		{"Spec", iss2.DisplayID},
	}
	if len(got) != len(want) {
		t.Fatalf("target links = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("target link %d = %v, want %v", i, got[i], want[i])
		}
	}

	// Re-import is idempotent: zero new links, zero new rows.
	rep2 := importArchive(t, ctx, pool, targetSlug, admin, archive)
	if rep2.Imported["page_issue_links"] != 0 {
		t.Fatalf("re-import imported.page_issue_links = %d, want 0", rep2.Imported["page_issue_links"])
	}
	if n := countTargetPageIssueLinks(t, ctx, pool, targetWSID); n != 3 {
		t.Fatalf("target links after re-import = %d, want 3", n)
	}
}

func TestWorkspaceArchivePageIssueLinksUnresolvableSide(t *testing.T) {
	ctx, pool, slug, ident, admin := setupArchivePageIssueLinksSource(t, "arcpilun")

	page := mustCreatePage(t, ctx, pool, slug, ident, admin, "Spec", nil)
	iss := createTestIssue(t, pool, slug, ident, admin, "Ship login")
	if _, err := LinkPageIssue(ctx, pool, slug, ident, page.ID, admin, iss.ID); err != nil {
		t.Fatalf("LinkPageIssue: %v", err)
	}

	archive := exportWorkspaceBytes(t, ctx, pool, slug, admin)
	rows := decodeArchivePageIssueLinks(t, archive)
	if len(rows) != 1 {
		t.Fatalf("archived page_issue_links = %d, want 1", len(rows))
	}
	// Hand-craft two ghost links: one whose issue does not exist,
	// one whose page does not exist (ghost id + ghost title).
	ghostDisplay := ident + "-9999"
	ghostTitle := "No such page"
	rows = append(rows,
		exportPageIssueLink{
			ID: "33333333-3333-3333-3333-333333333333", ProjectIdentifier: ident,
			PageID: page.ID, PageTitle: "Spec", IssueDisplayID: ghostDisplay,
		},
		exportPageIssueLink{
			ID: "44444444-4444-4444-4444-444444444444", ProjectIdentifier: ident,
			PageID: "ffffffff-ffff-ffff-ffff-ffffffffffff", PageTitle: ghostTitle,
			IssueDisplayID: iss.DisplayID,
		},
	)
	tampered := splicePageIssueLinksSection(t, archive, rows)

	targetSlug := uniqueTestSlug("arcpilun-tgt")
	createTestWorkspace(t, pool, "Target Co", targetSlug, admin)
	rep := importArchive(t, ctx, pool, targetSlug, admin, tampered)

	// The real link imports; both ghost links skip with notes naming
	// the unresolvable side. Nothing is fabricated.
	if rep.Imported["page_issue_links"] != 1 {
		t.Fatalf("report imported.page_issue_links = %d, want 1 (real link only; skipped: %v)",
			rep.Imported["page_issue_links"], rep.Skipped.Reasons)
	}
	joined := strings.Join(rep.Skipped.Reasons, "\n")
	if !strings.Contains(joined, ghostDisplay) {
		t.Errorf("skipped reasons do not name the ghost issue %q: %q", ghostDisplay, joined)
	}
	if !strings.Contains(joined, ghostTitle) {
		t.Errorf("skipped reasons do not name the ghost page %q: %q", ghostTitle, joined)
	}
	targetWSID := targetWorkspaceID(t, ctx, pool, targetSlug)
	got := targetPageIssueLinks(t, ctx, pool, targetWSID)
	if len(got) != 1 || got[0] != [2]string{"Spec", iss.DisplayID} {
		t.Fatalf("target links = %v, want [[Spec %s]] — no fabricated link", got, iss.DisplayID)
	}
}

func TestWorkspaceArchivePageIssueLinksTitleFallback(t *testing.T) {
	ctx, pool, slug, ident, admin := setupArchivePageIssueLinksSource(t, "arcpiltitle")

	page := mustCreatePage(t, ctx, pool, slug, ident, admin, "Spec", nil)
	iss := createTestIssue(t, pool, slug, ident, admin, "Ship login")
	if _, err := LinkPageIssue(ctx, pool, slug, ident, page.ID, admin, iss.ID); err != nil {
		t.Fatalf("LinkPageIssue: %v", err)
	}

	// Tamper the archive so the link's page id is stale (a page
	// restored under a different UUID by another path) while the
	// title still matches: id-then-title must restore the link via
	// the title fallback, not drop it.
	archive := exportWorkspaceBytes(t, ctx, pool, slug, admin)
	rows := decodeArchivePageIssueLinks(t, archive)
	if len(rows) != 1 {
		t.Fatalf("archived page_issue_links = %d, want 1", len(rows))
	}
	rows[0].PageID = "ffffffff-ffff-ffff-ffff-ffffffffffff"
	tampered := splicePageIssueLinksSection(t, archive, rows)

	targetSlug := uniqueTestSlug("arcpiltitle-tgt")
	createTestWorkspace(t, pool, "Target Co", targetSlug, admin)
	rep := importArchive(t, ctx, pool, targetSlug, admin, tampered)
	if rep.Imported["page_issue_links"] != 1 {
		t.Fatalf("report imported.page_issue_links = %d, want 1 via title fallback (skipped: %v)",
			rep.Imported["page_issue_links"], rep.Skipped.Reasons)
	}
	targetWSID := targetWorkspaceID(t, ctx, pool, targetSlug)
	got := targetPageIssueLinks(t, ctx, pool, targetWSID)
	if len(got) != 1 || got[0] != [2]string{"Spec", iss.DisplayID} {
		t.Fatalf("target links = %v, want [[Spec %s]]", got, iss.DisplayID)
	}
}

func TestWorkspaceArchivePageIssueLinksCrossProjectSkipped(t *testing.T) {
	ctx, pool, slug, ident, admin := setupArchivePageIssueLinksSource(t, "arcpilx")

	// A second project in the same workspace: its page + issue give
	// the tampered link a real page UUID from one project and a real
	// issue display id from the other. The importer must refuse to
	// smuggle a cross-project link in.
	ident2 := uniqueTestIdentifier()
	createTestProject(t, pool, slug, admin, "Other", ident2)
	foreignPage := mustCreatePage(t, ctx, pool, slug, ident2, admin, "Foreign", nil)
	iss := createTestIssue(t, pool, slug, ident, admin, "Ship login")

	archive := exportWorkspaceBytes(t, ctx, pool, slug, admin)
	rows := decodeArchivePageIssueLinks(t, archive)
	if len(rows) != 0 {
		t.Fatalf("archived page_issue_links = %d, want 0 (no links yet)", len(rows))
	}
	rows = append(rows, exportPageIssueLink{
		ID: "55555555-5555-5555-5555-555555555555", ProjectIdentifier: ident,
		PageID: foreignPage.ID, PageTitle: "Foreign", IssueDisplayID: iss.DisplayID,
	})
	tampered := splicePageIssueLinksSection(t, archive, rows)

	targetSlug := uniqueTestSlug("arcpilx-tgt")
	createTestWorkspace(t, pool, "Target Co", targetSlug, admin)
	rep := importArchive(t, ctx, pool, targetSlug, admin, tampered)
	if rep.Imported["page_issue_links"] != 0 {
		t.Fatalf("report imported.page_issue_links = %d, want 0 (cross-project link refused)", rep.Imported["page_issue_links"])
	}
	joined := strings.Join(rep.Skipped.Reasons, "\n")
	if !strings.Contains(joined, "different projects") {
		t.Errorf("skipped reasons do not explain the cross-project refusal: %q", joined)
	}
	targetWSID := targetWorkspaceID(t, ctx, pool, targetSlug)
	if n := countTargetPageIssueLinks(t, ctx, pool, targetWSID); n != 0 {
		t.Fatalf("target links = %d, want 0 — no smuggled cross-project link", n)
	}
}
