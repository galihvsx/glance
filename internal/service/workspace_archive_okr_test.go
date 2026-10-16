package service

// Workspace archive round-trip for OKRs (C35T2): export → import into
// a fresh workspace preserves objectives + key results + KR-issue
// links, re-import is idempotent, an unmapped owner degrades to a
// null owner (never fabricated), an unresolvable issue link skips
// with a note naming the KR (the KR still imports, link-less), and a
// zero-span KR is skipped with a note (never imported). Real test
// database, no skips. Service-level: exercises StreamWorkspaceExport +
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

func setupArchiveOKRSource(t *testing.T, prefix string) (context.Context, *pgxpool.Pool, string, string, string, string) {
	t.Helper()
	ctx := context.Background()
	pool := newTestPool(t)
	admin := createTestUser(t, pool, uniqueTestEmail(prefix+"-admin"))
	slug := uniqueTestSlug(prefix + "-ws")
	createTestWorkspace(t, pool, "OKR Co", slug, admin)
	ident := uniqueTestIdentifier()
	createTestProject(t, pool, slug, admin, "Eng", ident)
	owner := createTestUser(t, pool, uniqueTestEmail(prefix+"-owner"))
	addTestMember(t, pool, slug, admin, owner, RoleMember)
	return ctx, pool, slug, ident, admin, owner
}

func targetObjectiveOwner(t *testing.T, ctx context.Context, pool *pgxpool.Pool, targetWSID, title string) *string {
	t.Helper()
	var email *string
	if err := pool.QueryRow(ctx,
		`SELECT u.email::text
		 FROM objectives o LEFT JOIN users u ON u.id = o.owner_id
		 WHERE o.workspace_id = $1::uuid AND o.title = $2`, targetWSID, title).Scan(&email); err != nil {
		t.Fatalf("target objective owner: %v", err)
	}
	return email
}

func targetKRLinks(t *testing.T, ctx context.Context, pool *pgxpool.Pool, targetWSID, krTitle string) []string {
	t.Helper()
	rows, err := pool.Query(ctx,
		`SELECT p.identifier || '-' || i.sequence_id
		 FROM key_result_issues kri
		 JOIN key_results kr ON kr.id = kri.key_result_id
		 JOIN objectives o ON o.id = kr.objective_id
		 JOIN issues i ON i.id = kri.issue_id
		 JOIN projects p ON p.id = i.project_id
		 WHERE o.workspace_id = $1::uuid AND kr.title = $2
		 ORDER BY i.sequence_id`, targetWSID, krTitle)
	if err != nil {
		t.Fatalf("target KR links: %v", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			t.Fatalf("scan KR link: %v", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("target KR links rows: %v", err)
	}
	return out
}

func TestWorkspaceArchiveOKRRoundTrip(t *testing.T) {
	ctx, pool, slug, ident, admin, owner := setupArchiveOKRSource(t, "arcokr")

	iss1 := createTestIssue(t, pool, slug, ident, admin, "Ship login")
	iss2 := createTestIssue(t, pool, slug, ident, admin, "Ship billing")

	desc := "Grow the business"
	start := "2026-01-01"
	target := "2026-03-31"
	ob, err := CreateObjective(ctx, pool, slug, admin, ObjectiveInput{
		Title:       "Grow revenue",
		Description: &desc,
		OwnerID:     &owner,
		StartDate:   &start,
		TargetDate:  &target,
	})
	if err != nil {
		t.Fatalf("CreateObjective: %v", err)
	}
	if _, err := CreateKeyResult(ctx, pool, slug, admin, ob.ID, KeyResultInput{
		Title: "Grow revenue", Kind: "metric", Unit: strptr("%"),
		StartValue: fptr(0), TargetValue: fptr(100), CurrentValue: fptr(40),
	}); err != nil {
		t.Fatalf("CreateKeyResult metric: %v", err)
	}
	issueKR, err := CreateKeyResult(ctx, pool, slug, admin, ob.ID, KeyResultInput{
		Title: "Ship features", Kind: "issue",
		StartValue: fptr(0), TargetValue: fptr(2),
	})
	if err != nil {
		t.Fatalf("CreateKeyResult issue: %v", err)
	}
	for _, iss := range []*Issue{iss1, iss2} {
		if err := LinkIssueToKeyResult(ctx, pool, slug, admin, ob.ID, issueKR.ID, iss.ID); err != nil {
			t.Fatalf("LinkIssueToKeyResult %s: %v", iss.DisplayID, err)
		}
	}
	var ownerEmail string
	if err := pool.QueryRow(ctx, `SELECT email::text FROM users WHERE id = $1::uuid`, owner).Scan(&ownerEmail); err != nil {
		t.Fatalf("owner email: %v", err)
	}

	archive := exportWorkspaceBytes(t, ctx, pool, slug, admin)

	// The archive carries the new section: objective + 2 KRs, links
	// keyed by project identifier + issue display_id.
	var decoded struct {
		Objectives []exportObjective `json:"objectives"`
	}
	if err := json.Unmarshal(archive, &decoded); err != nil {
		t.Fatalf("decode archive: %v", err)
	}
	if len(decoded.Objectives) != 1 {
		t.Fatalf("archived objectives = %d, want 1", len(decoded.Objectives))
	}
	ao := decoded.Objectives[0]
	if ao.Title != "Grow revenue" || ao.OwnerEmail == nil || *ao.OwnerEmail != ownerEmail {
		t.Fatalf("archived objective = %+v, want title Grow revenue owner %q", ao, ownerEmail)
	}
	if ao.StartDate == nil || *ao.StartDate != start || ao.TargetDate == nil || *ao.TargetDate != target {
		t.Fatalf("archived dates = %v / %v, want %s / %s", ao.StartDate, ao.TargetDate, start, target)
	}
	if len(ao.KeyResults) != 2 {
		t.Fatalf("archived key results = %d, want 2", len(ao.KeyResults))
	}
	var metricKR, archivedIssueKR *exportKeyResult
	for i := range ao.KeyResults {
		if ao.KeyResults[i].Kind == "metric" {
			metricKR = &ao.KeyResults[i]
		} else {
			archivedIssueKR = &ao.KeyResults[i]
		}
	}
	if metricKR == nil || metricKR.Unit == nil || *metricKR.Unit != "%" ||
		metricKR.StartValue.String() != "0" || metricKR.TargetValue.String() != "100" ||
		metricKR.CurrentValue.String() != "40" {
		t.Fatalf("archived metric KR = %+v, want unit %% 0→100 current 40", metricKR)
	}
	if archivedIssueKR == nil || len(archivedIssueKR.LinkedIssues) != 2 {
		t.Fatalf("archived issue KR links = %v, want 2", archivedIssueKR)
	}
	for _, l := range archivedIssueKR.LinkedIssues {
		if l.ProjectIdentifier != ident || (l.IssueDisplayID != iss1.DisplayID && l.IssueDisplayID != iss2.DisplayID) {
			t.Fatalf("archived link = %+v, want project %q + display %q/%q", l, ident, iss1.DisplayID, iss2.DisplayID)
		}
	}

	// Import into a fresh workspace (same admin).
	targetSlug := uniqueTestSlug("arcokr-tgt")
	createTestWorkspace(t, pool, "Target Co", targetSlug, admin)
	rep := importArchive(t, ctx, pool, targetSlug, admin, archive)
	if rep.Imported["objectives"] != 1 || rep.Imported["key_results"] != 2 || rep.Imported["key_result_issue_links"] != 2 {
		t.Fatalf("report imported = objectives %d / KRs %d / links %d, want 1/2/2 (skipped: %v)",
			rep.Imported["objectives"], rep.Imported["key_results"], rep.Imported["key_result_issue_links"], rep.Skipped.Reasons)
	}
	targetWSID := targetWorkspaceID(t, ctx, pool, targetSlug)

	// Objective fields + owner email survive.
	var title, status, body string
	var sDate, tDate *string
	if err := pool.QueryRow(ctx,
		`SELECT o.title, o.description, o.status,
		        to_char(o.start_date, 'YYYY-MM-DD'), to_char(o.target_date, 'YYYY-MM-DD')
		 FROM objectives o WHERE o.workspace_id = $1::uuid`, targetWSID).Scan(&title, &body, &status, &sDate, &tDate); err != nil {
		t.Fatalf("target objective: %v", err)
	}
	if title != "Grow revenue" || body != desc || status != "active" ||
		sDate == nil || *sDate != start || tDate == nil || *tDate != target {
		t.Fatalf("target objective = %q/%q/%q/%v/%v, want Grow revenue/%q/active/%s/%s",
			title, body, status, sDate, tDate, desc, start, target)
	}
	if got := targetObjectiveOwner(t, ctx, pool, targetWSID, "Grow revenue"); got == nil || *got != ownerEmail {
		t.Fatalf("target owner = %v, want %q", got, ownerEmail)
	}

	// Metric KR values survive exactly.
	var unit *string
	var startV, targetV, currentV string
	if err := pool.QueryRow(ctx,
		`SELECT kr.unit, kr.start_value::text, kr.target_value::text, kr.current_value::text
		 FROM key_results kr JOIN objectives o ON o.id = kr.objective_id
		 WHERE o.workspace_id = $1::uuid AND kr.kind = 'metric'`, targetWSID).
		Scan(&unit, &startV, &targetV, &currentV); err != nil {
		t.Fatalf("target metric KR: %v", err)
	}
	if unit == nil || *unit != "%" || startV != "0" || targetV != "100" || currentV != "40" {
		t.Fatalf("target metric KR = %v/%s/%s/%s, want %%/0/100/40", unit, startV, targetV, currentV)
	}

	// Issue KR links resolve to the same display IDs in the target.
	gotLinks := targetKRLinks(t, ctx, pool, targetWSID, "Ship features")
	if len(gotLinks) != 2 || gotLinks[0] != iss1.DisplayID || gotLinks[1] != iss2.DisplayID {
		t.Fatalf("target KR links = %v, want [%s %s]", gotLinks, iss1.DisplayID, iss2.DisplayID)
	}

	// Re-import is idempotent: zero new rows anywhere.
	rep2 := importArchive(t, ctx, pool, targetSlug, admin, archive)
	if rep2.Imported["objectives"] != 0 || rep2.Imported["key_results"] != 0 || rep2.Imported["key_result_issue_links"] != 0 {
		t.Fatalf("re-import imported = objectives %d / KRs %d / links %d, want 0/0/0 (skipped: %v)",
			rep2.Imported["objectives"], rep2.Imported["key_results"], rep2.Imported["key_result_issue_links"], rep2.Skipped.Reasons)
	}
	var nObj, nKR, nLinks int
	if err := pool.QueryRow(ctx,
		`SELECT (SELECT count(*) FROM objectives WHERE workspace_id = $1::uuid),
		        (SELECT count(*) FROM key_results kr JOIN objectives o ON o.id = kr.objective_id WHERE o.workspace_id = $1::uuid),
		        (SELECT count(*) FROM key_result_issues kri JOIN key_results kr ON kr.id = kri.key_result_id JOIN objectives o ON o.id = kr.objective_id WHERE o.workspace_id = $1::uuid)`,
		targetWSID).Scan(&nObj, &nKR, &nLinks); err != nil {
		t.Fatalf("target counts: %v", err)
	}
	if nObj != 1 || nKR != 2 || nLinks != 2 {
		t.Fatalf("target counts after re-import = %d/%d/%d, want 1/2/2", nObj, nKR, nLinks)
	}
}

func TestWorkspaceArchiveOKRUnmappedOwner(t *testing.T) {
	ctx, pool, slug, ident, admin, owner := setupArchiveOKRSource(t, "arcokrun")

	if _, err := CreateObjective(ctx, pool, slug, admin, ObjectiveInput{
		Title: "Grow revenue", OwnerID: &owner,
	}); err != nil {
		t.Fatalf("CreateObjective: %v", err)
	}
	if _, err := CreateKeyResult(ctx, pool, slug, admin, mustObjectiveID(t, ctx, pool, slug, "Grow revenue"), KeyResultInput{
		Title: "Grow revenue", Kind: "metric",
		StartValue: fptr(0), TargetValue: fptr(100),
	}); err != nil {
		t.Fatalf("CreateKeyResult: %v", err)
	}
	var ownerEmail string
	if err := pool.QueryRow(ctx, `SELECT email::text FROM users WHERE id = $1::uuid`, owner).Scan(&ownerEmail); err != nil {
		t.Fatalf("owner email: %v", err)
	}

	// Tamper the archive so the owner points at a user that does not
	// exist in this instance — ghost UUID + ghost email, everywhere
	// they appear (members section + the objective's owner fields).
	archive := exportWorkspaceBytes(t, ctx, pool, slug, admin)
	ghostEmail := uniqueTestEmail("arcokrun-ghost")
	ghostID := "ffffffff-ffff-ffff-ffff-ffffffffffff"
	tampered := bytes.ReplaceAll(archive, []byte(`"`+owner+`"`), []byte(`"`+ghostID+`"`))
	tampered = bytes.ReplaceAll(tampered, []byte(ownerEmail), []byte(ghostEmail))
	if bytes.Equal(tampered, archive) {
		t.Fatal("tamper failed: owner id/email not found in archive")
	}
	_ = ident

	targetSlug := uniqueTestSlug("arcokrun-tgt")
	createTestWorkspace(t, pool, "Target Co", targetSlug, admin)
	rep := importArchive(t, ctx, pool, targetSlug, admin, tampered)

	// The objective still imports — with a null owner, never a
	// fabricated one — and the skip note names the ghost email.
	if rep.Imported["objectives"] != 1 || rep.Imported["key_results"] != 1 {
		t.Fatalf("report imported = objectives %d / KRs %d, want 1/1 (skipped: %v)",
			rep.Imported["objectives"], rep.Imported["key_results"], rep.Skipped.Reasons)
	}
	joined := strings.Join(rep.Skipped.Reasons, "\n")
	if !strings.Contains(joined, ghostEmail) {
		t.Errorf("skipped reasons do not name the unmapped owner %q: %q", ghostEmail, joined)
	}
	targetWSID := targetWorkspaceID(t, ctx, pool, targetSlug)
	if got := targetObjectiveOwner(t, ctx, pool, targetWSID, "Grow revenue"); got != nil {
		t.Fatalf("target owner = %q, want null (no fabricated owner)", *got)
	}
}

func mustObjectiveID(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug, title string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx,
		`SELECT o.id::text FROM objectives o JOIN workspaces w ON w.id = o.workspace_id WHERE w.slug = $1 AND o.title = $2`,
		slug, title).Scan(&id); err != nil {
		t.Fatalf("objective id: %v", err)
	}
	return id
}

// spliceObjectivesSection replaces the archive's objectives section
// (the last section) with rows, byte-spliced so every other section
// stays byte-identical and in the exporter's dependency order.
func spliceObjectivesSection(t *testing.T, archive []byte, rows []exportObjective) []byte {
	t.Helper()
	prefix := []byte(`"objectives":[`)
	start := bytes.Index(archive, prefix)
	if start < 0 {
		t.Fatal("objectives section not found in archive")
	}
	contentStart := start + len(prefix)
	end := bytes.LastIndex(archive, []byte(`]}`))
	if end < 0 || end < contentStart {
		t.Fatal("objectives section terminator not found in archive")
	}
	body, err := json.Marshal(rows)
	if err != nil {
		t.Fatalf("marshal objectives: %v", err)
	}
	var out bytes.Buffer
	out.Write(archive[:contentStart])
	out.Write(body[1 : len(body)-1]) // strip the outer [] — the section brackets stay
	out.Write(archive[end:])
	return out.Bytes()
}

func decodeArchiveObjectives(t *testing.T, archive []byte) []exportObjective {
	t.Helper()
	prefix := []byte(`"objectives":[`)
	start := bytes.Index(archive, prefix)
	if start < 0 {
		t.Fatal("objectives section not found in archive")
	}
	contentStart := start + len(prefix)
	end := bytes.LastIndex(archive, []byte(`]}`))
	if end < 0 || end < contentStart {
		t.Fatal("objectives section terminator not found in archive")
	}
	// content is the bare array body (no brackets); wrap it back.
	wrapped := append(append([]byte("["), archive[contentStart:end]...), ']')
	var rows []exportObjective
	if err := json.Unmarshal(wrapped, &rows); err != nil {
		t.Fatalf("decode objectives: %v", err)
	}
	return rows
}

func TestWorkspaceArchiveOKRUnresolvableLink(t *testing.T) {
	ctx, pool, slug, ident, admin, _ := setupArchiveOKRSource(t, "arcokrlink")

	iss := createTestIssue(t, pool, slug, ident, admin, "Ship login")
	ob, err := CreateObjective(ctx, pool, slug, admin, ObjectiveInput{Title: "Grow revenue"})
	if err != nil {
		t.Fatalf("CreateObjective: %v", err)
	}
	issueKR, err := CreateKeyResult(ctx, pool, slug, admin, ob.ID, KeyResultInput{
		Title: "Ship features", Kind: "issue",
		StartValue: fptr(0), TargetValue: fptr(2),
	})
	if err != nil {
		t.Fatalf("CreateKeyResult: %v", err)
	}
	if err := LinkIssueToKeyResult(ctx, pool, slug, admin, ob.ID, issueKR.ID, iss.ID); err != nil {
		t.Fatalf("LinkIssueToKeyResult: %v", err)
	}

	archive := exportWorkspaceBytes(t, ctx, pool, slug, admin)
	rows := decodeArchiveObjectives(t, archive)
	if len(rows) != 1 || len(rows[0].KeyResults) != 1 || len(rows[0].KeyResults[0].LinkedIssues) != 1 {
		t.Fatalf("decoded objectives = %+v, want 1 objective / 1 KR / 1 link", rows)
	}
	// Hand-craft a second link to an issue that does not exist.
	ghostDisplay := ident + "-9999"
	rows[0].KeyResults[0].LinkedIssues = append(rows[0].KeyResults[0].LinkedIssues,
		exportKRLink{ProjectIdentifier: ident, IssueDisplayID: ghostDisplay})
	tampered := spliceObjectivesSection(t, archive, rows)

	targetSlug := uniqueTestSlug("arcokrlink-tgt")
	createTestWorkspace(t, pool, "Target Co", targetSlug, admin)
	rep := importArchive(t, ctx, pool, targetSlug, admin, tampered)

	// The real link imports; the ghost link skips with a note naming
	// the KR and the ghost display id. The KR still imports.
	if rep.Imported["objectives"] != 1 || rep.Imported["key_results"] != 1 || rep.Imported["key_result_issue_links"] != 1 {
		t.Fatalf("report imported = objectives %d / KRs %d / links %d, want 1/1/1 (skipped: %v)",
			rep.Imported["objectives"], rep.Imported["key_results"], rep.Imported["key_result_issue_links"], rep.Skipped.Reasons)
	}
	joined := strings.Join(rep.Skipped.Reasons, "\n")
	if !strings.Contains(joined, "Ship features") || !strings.Contains(joined, ghostDisplay) {
		t.Errorf("skipped reasons do not name the KR and ghost link: %q", joined)
	}
	targetWSID := targetWorkspaceID(t, ctx, pool, targetSlug)
	gotLinks := targetKRLinks(t, ctx, pool, targetWSID, "Ship features")
	if len(gotLinks) != 1 || gotLinks[0] != iss.DisplayID {
		t.Fatalf("target KR links = %v, want [%s] — no fabricated link", gotLinks, iss.DisplayID)
	}
}

func TestWorkspaceArchiveOKRZeroSpanSkipped(t *testing.T) {
	ctx, pool, slug, ident, admin, _ := setupArchiveOKRSource(t, "arcokrspan")

	iss := createTestIssue(t, pool, slug, ident, admin, "Ship login")
	ob, err := CreateObjective(ctx, pool, slug, admin, ObjectiveInput{Title: "Grow revenue"})
	if err != nil {
		t.Fatalf("CreateObjective: %v", err)
	}
	if _, err := CreateKeyResult(ctx, pool, slug, admin, ob.ID, KeyResultInput{
		Title: "Good KR", Kind: "metric",
		StartValue: fptr(0), TargetValue: fptr(100), CurrentValue: fptr(10),
	}); err != nil {
		t.Fatalf("CreateKeyResult good: %v", err)
	}
	zeroIssueKR, err := CreateKeyResult(ctx, pool, slug, admin, ob.ID, KeyResultInput{
		Title: "Zero-span issue KR", Kind: "issue",
		StartValue: fptr(0), TargetValue: fptr(2),
	})
	if err != nil {
		t.Fatalf("CreateKeyResult zero-span issue: %v", err)
	}
	if err := LinkIssueToKeyResult(ctx, pool, slug, admin, ob.ID, zeroIssueKR.ID, iss.ID); err != nil {
		t.Fatalf("LinkIssueToKeyResult: %v", err)
	}
	// The zero-span metric KR cannot be created through the service
	// (target == start is rejected), so it is hand-crafted into the
	// archive below — the importer must refuse it like the CHECK does.

	archive := exportWorkspaceBytes(t, ctx, pool, slug, admin)
	rows := decodeArchiveObjectives(t, archive)
	if len(rows) != 1 || len(rows[0].KeyResults) != 2 {
		t.Fatalf("decoded objectives = %+v, want 1 objective / 2 KRs", rows)
	}
	for i := range rows[0].KeyResults {
		if rows[0].KeyResults[i].Title == "Zero-span issue KR" {
			// Collapse the span: target := start.
			rows[0].KeyResults[i].TargetValue = rows[0].KeyResults[i].StartValue
		}
	}
	// Hand-craft a zero-span metric KR (no links), cloned from the
	// good metric KR so every other field stays well-formed.
	var zeroMetric exportKeyResult
	found := false
	for _, kr := range rows[0].KeyResults {
		if kr.Title == "Good KR" {
			zeroMetric = kr
			found = true
		}
	}
	if !found {
		t.Fatal("good metric KR not found in decoded objectives")
	}
	zeroMetric.ID = "33333333-3333-3333-3333-333333333333"
	zeroMetric.Title = "Zero-span metric KR"
	zeroMetric.Kind = "metric"
	zeroMetric.Unit = strptr("users")
	zeroMetric.StartValue = json.Number("50")
	zeroMetric.TargetValue = json.Number("50")
	zeroMetric.CurrentValue = json.Number("50")
	zeroMetric.LinkedIssues = []exportKRLink{}
	rows[0].KeyResults = append(rows[0].KeyResults, zeroMetric)
	tampered := spliceObjectivesSection(t, archive, rows)

	targetSlug := uniqueTestSlug("arcokrspan-tgt")
	createTestWorkspace(t, pool, "Target Co", targetSlug, admin)
	rep := importArchive(t, ctx, pool, targetSlug, admin, tampered)

	// Only the good KR imports; both zero-span KRs skip with notes
	// naming them; the zero-span issue KR's link never imports.
	if rep.Imported["objectives"] != 1 || rep.Imported["key_results"] != 1 || rep.Imported["key_result_issue_links"] != 0 {
		t.Fatalf("report imported = objectives %d / KRs %d / links %d, want 1/1/0 (skipped: %v)",
			rep.Imported["objectives"], rep.Imported["key_results"], rep.Imported["key_result_issue_links"], rep.Skipped.Reasons)
	}
	joined := strings.Join(rep.Skipped.Reasons, "\n")
	if !strings.Contains(joined, "Zero-span metric KR") || !strings.Contains(joined, "Zero-span issue KR") {
		t.Errorf("skipped reasons do not name both zero-span KRs: %q", joined)
	}
	targetWSID := targetWorkspaceID(t, ctx, pool, targetSlug)
	var nKR int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM key_results kr JOIN objectives o ON o.id = kr.objective_id
		 WHERE o.workspace_id = $1::uuid`, targetWSID).Scan(&nKR); err != nil {
		t.Fatalf("target KR count: %v", err)
	}
	if nKR != 1 {
		t.Fatalf("target KRs = %d, want 1 (good KR only)", nKR)
	}
}

func TestWorkspaceArchiveOKRTitleConflict(t *testing.T) {
	ctx, pool, slug, _, admin, _ := setupArchiveOKRSource(t, "arcokrtitle")

	if _, err := CreateObjective(ctx, pool, slug, admin, ObjectiveInput{Title: "Grow revenue"}); err != nil {
		t.Fatalf("CreateObjective source: %v", err)
	}
	// The source objective id is needed for its KR; fetch it.
	obID := mustObjectiveID(t, ctx, pool, slug, "Grow revenue")
	if _, err := CreateKeyResult(ctx, pool, slug, admin, obID, KeyResultInput{
		Title: "Grow revenue", Kind: "metric",
		StartValue: fptr(0), TargetValue: fptr(100),
	}); err != nil {
		t.Fatalf("CreateKeyResult source: %v", err)
	}
	archive := exportWorkspaceBytes(t, ctx, pool, slug, admin)

	// The target already has a DIFFERENT objective with the same
	// title: the archived objective and its KRs must skip — never
	// merge into the unrelated objective.
	targetSlug := uniqueTestSlug("arcokrtitle-tgt")
	createTestWorkspace(t, pool, "Target Co", targetSlug, admin)
	if _, err := CreateObjective(ctx, pool, targetSlug, admin, ObjectiveInput{Title: "Grow revenue"}); err != nil {
		t.Fatalf("CreateObjective target: %v", err)
	}
	rep := importArchive(t, ctx, pool, targetSlug, admin, archive)

	if rep.Imported["objectives"] != 0 || rep.Imported["key_results"] != 0 {
		t.Fatalf("report imported = objectives %d / KRs %d, want 0/0 (skipped: %v)",
			rep.Imported["objectives"], rep.Imported["key_results"], rep.Skipped.Reasons)
	}
	joined := strings.Join(rep.Skipped.Reasons, "\n")
	if !strings.Contains(joined, "Grow revenue") {
		t.Errorf("skipped reasons do not name the conflicted objective: %q", joined)
	}
	targetWSID := targetWorkspaceID(t, ctx, pool, targetSlug)
	var nObj, nKR int
	if err := pool.QueryRow(ctx,
		`SELECT (SELECT count(*) FROM objectives WHERE workspace_id = $1::uuid),
		        (SELECT count(*) FROM key_results kr JOIN objectives o ON o.id = kr.objective_id WHERE o.workspace_id = $1::uuid)`,
		targetWSID).Scan(&nObj, &nKR); err != nil {
		t.Fatalf("target counts: %v", err)
	}
	if nObj != 1 || nKR != 0 {
		t.Fatalf("target counts = %d objectives / %d KRs, want 1/0 (pre-existing objective untouched, no merged KRs)", nObj, nKR)
	}
}
