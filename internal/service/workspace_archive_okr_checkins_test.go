package service

// Workspace archive round-trip for KR check-ins (C42T2): export →
// import into a fresh workspace preserves a metric KR's full
// check-in history (seed + updates: previous/new/note/author/
// created_at, oldest-first), re-import is idempotent, an unmapped
// author degrades to a null author (never fabricated), restoring
// history never changes the KR's current_value, issue KRs
// export/import no check-ins, and old archives without the
// check_ins field still import (no check-ins, no error). Real test
// database, no skips.

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// archivedCheckinRow is one check-in row read back from the DB for
// exact round-trip comparison. Values are ::text so NUMERIC
// precision compares as strings.
type archivedCheckinRow struct {
	Prev        *string
	New         string
	Note        *string
	AuthorEmail *string
	CreatedAt   time.Time
}

func krCheckinsInWorkspace(t *testing.T, ctx context.Context, pool *pgxpool.Pool, wsID, krTitle string) []archivedCheckinRow {
	t.Helper()
	rows, err := pool.Query(ctx,
		`SELECT ci.previous_value::text, ci.new_value::text, ci.note, u.email::text, ci.created_at
		 FROM key_result_checkins ci
		 JOIN key_results kr ON kr.id = ci.key_result_id
		 JOIN objectives o ON o.id = kr.objective_id
		 LEFT JOIN users u ON u.id = ci.author_id
		 WHERE o.workspace_id = $1::uuid AND kr.title = $2
		 ORDER BY ci.created_at ASC, ci.id ASC`, wsID, krTitle)
	if err != nil {
		t.Fatalf("check-ins query: %v", err)
	}
	defer rows.Close()
	out := []archivedCheckinRow{}
	for rows.Next() {
		var r archivedCheckinRow
		if err := rows.Scan(&r.Prev, &r.New, &r.Note, &r.AuthorEmail, &r.CreatedAt); err != nil {
			t.Fatalf("scan check-in: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("check-ins rows: %v", err)
	}
	return out
}

func checkinStr(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

func assertCheckinsEqual(t *testing.T, got, want []archivedCheckinRow) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("check-ins = %d rows, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		g, w := got[i], want[i]
		if checkinStr(g.Prev) != checkinStr(w.Prev) || g.New != w.New ||
			checkinStr(g.Note) != checkinStr(w.Note) ||
			checkinStr(g.AuthorEmail) != checkinStr(w.AuthorEmail) ||
			!g.CreatedAt.Equal(w.CreatedAt) {
			t.Fatalf("check-in[%d] = prev %q new %q note %q author %q at %v, want prev %q new %q note %q author %q at %v",
				i, checkinStr(g.Prev), g.New, checkinStr(g.Note), checkinStr(g.AuthorEmail), g.CreatedAt,
				checkinStr(w.Prev), w.New, checkinStr(w.Note), checkinStr(w.AuthorEmail), w.CreatedAt)
		}
	}
}

func userEmail(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID string) string {
	t.Helper()
	var email string
	if err := pool.QueryRow(ctx, `SELECT email::text FROM users WHERE id = $1::uuid`, userID).Scan(&email); err != nil {
		t.Fatalf("user email: %v", err)
	}
	return email
}

// spliceObjectivesSectionRaw replaces the archive's objectives
// section (the last section) with a pre-marshalled array body — the
// raw counterpart of spliceObjectivesSection, for section edits
// that cannot round-trip through the exportObjective struct (e.g.
// deleting the check_ins key entirely, as a pre-C42 archive would).
func spliceObjectivesSectionRaw(t *testing.T, archive []byte, body []byte) []byte {
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
	var out bytes.Buffer
	out.Write(archive[:contentStart])
	out.Write(body)
	out.Write(archive[end:])
	return out.Bytes()
}

func TestWorkspaceArchiveOKRCheckinsRoundTrip(t *testing.T) {
	ctx, pool, slug, _, admin, owner := setupArchiveOKRSource(t, "arcokrci")

	member := createTestUser(t, pool, uniqueTestEmail("arcokrci-member"))
	if err := UpsertMember(ctx, pool, slug, admin, member, RoleMember); err != nil {
		t.Fatalf("UpsertMember member: %v", err)
	}
	adminEmail := userEmail(t, ctx, pool, admin)
	memberEmail := userEmail(t, ctx, pool, member)

	ob, err := CreateObjective(ctx, pool, slug, admin, ObjectiveInput{
		Title: "Grow revenue", OwnerID: &owner,
	})
	if err != nil {
		t.Fatalf("CreateObjective: %v", err)
	}
	metricKR, err := CreateKeyResult(ctx, pool, slug, admin, ob.ID, KeyResultInput{
		Title: "Grow revenue", Kind: "metric", Unit: strptr("%"),
		StartValue: fptr(0), TargetValue: fptr(100), CurrentValue: fptr(40),
	})
	if err != nil {
		t.Fatalf("CreateKeyResult metric: %v", err)
	}
	if _, err := UpdateKeyResult(ctx, pool, slug, admin, ob.ID, metricKR.ID, KeyResultPatch{
		CurrentValue: fptr(50), CheckinNote: strptr("Q1 push"),
	}); err != nil {
		t.Fatalf("UpdateKeyResult admin: %v", err)
	}
	if _, err := UpdateKeyResult(ctx, pool, slug, member, ob.ID, metricKR.ID, KeyResultPatch{
		CurrentValue: fptr(75),
	}); err != nil {
		t.Fatalf("UpdateKeyResult member: %v", err)
	}
	if _, err := CreateKeyResult(ctx, pool, slug, admin, ob.ID, KeyResultInput{
		Title: "Ship features", Kind: "issue",
		StartValue: fptr(0), TargetValue: fptr(2),
	}); err != nil {
		t.Fatalf("CreateKeyResult issue: %v", err)
	}

	sourceWSID := targetWorkspaceID(t, ctx, pool, slug)
	want := krCheckinsInWorkspace(t, ctx, pool, sourceWSID, "Grow revenue")
	if len(want) != 3 {
		t.Fatalf("source check-ins = %d, want 3 (seed + 2 updates)", len(want))
	}

	archive := exportWorkspaceBytes(t, ctx, pool, slug, admin)

	// The archive carries the full history oldest-first: seed
	// (previous null, new 40, no note, admin), admin update
	// (40 → 50, "Q1 push"), member update (50 → 75, no note).
	rows := decodeArchiveObjectives(t, archive)
	if len(rows) != 1 || len(rows[0].KeyResults) != 2 {
		t.Fatalf("decoded objectives = %+v, want 1 objective / 2 KRs", rows)
	}
	var archivedMetric, archivedIssue *exportKeyResult
	for i := range rows[0].KeyResults {
		if rows[0].KeyResults[i].Kind == "metric" {
			archivedMetric = &rows[0].KeyResults[i]
		} else {
			archivedIssue = &rows[0].KeyResults[i]
		}
	}
	if archivedMetric == nil || len(archivedMetric.CheckIns) != 3 {
		t.Fatalf("archived metric KR check-ins = %v, want 3", archivedMetric)
	}
	seed, upd1, upd2 := archivedMetric.CheckIns[0], archivedMetric.CheckIns[1], archivedMetric.CheckIns[2]
	if seed.PreviousValue != nil || seed.NewValue.String() != "40" || seed.Note != nil ||
		seed.AuthorEmail == nil || *seed.AuthorEmail != adminEmail {
		t.Fatalf("archived seed = %+v, want previous null new 40 note nil author %q", seed, adminEmail)
	}
	if upd1.PreviousValue == nil || upd1.PreviousValue.String() != "40" ||
		upd1.NewValue.String() != "50" || upd1.Note == nil || *upd1.Note != "Q1 push" ||
		upd1.AuthorEmail == nil || *upd1.AuthorEmail != adminEmail {
		t.Fatalf("archived update 1 = %+v, want 40 → 50 note %q author %q", upd1, "Q1 push", adminEmail)
	}
	if upd2.PreviousValue == nil || upd2.PreviousValue.String() != "50" ||
		upd2.NewValue.String() != "75" || upd2.Note != nil ||
		upd2.AuthorEmail == nil || *upd2.AuthorEmail != memberEmail {
		t.Fatalf("archived update 2 = %+v, want 50 → 75 note nil author %q", upd2, memberEmail)
	}
	if archivedIssue == nil || len(archivedIssue.CheckIns) != 0 {
		t.Fatalf("archived issue KR check-ins = %v, want none", archivedIssue)
	}

	// Import into a fresh workspace. The member author resolves by
	// email even though they are not a member of the target.
	targetSlug := uniqueTestSlug("arcokrci-tgt")
	createTestWorkspace(t, pool, "Target Co", targetSlug, admin)
	rep := importArchive(t, ctx, pool, targetSlug, admin, archive)
	if rep.Imported["objectives"] != 1 || rep.Imported["key_results"] != 2 || rep.Imported["key_result_checkins"] != 3 {
		t.Fatalf("report imported = objectives %d / KRs %d / check-ins %d, want 1/2/3 (skipped: %v)",
			rep.Imported["objectives"], rep.Imported["key_results"], rep.Imported["key_result_checkins"], rep.Skipped.Reasons)
	}
	targetWSID := targetWorkspaceID(t, ctx, pool, targetSlug)

	// History restores exactly: previous/new/note/author/created_at.
	assertCheckinsEqual(t, krCheckinsInWorkspace(t, ctx, pool, targetWSID, "Grow revenue"), want)

	// Restoring history MUST NOT change the KR's current_value.
	var currentV string
	if err := pool.QueryRow(ctx,
		`SELECT kr.current_value::text
		 FROM key_results kr JOIN objectives o ON o.id = kr.objective_id
		 WHERE o.workspace_id = $1::uuid AND kr.title = $2`, targetWSID, "Grow revenue").Scan(&currentV); err != nil {
		t.Fatalf("target metric KR current: %v", err)
	}
	if currentV != "75" {
		t.Fatalf("target KR current_value = %q, want 75 (history restore must not rewrite state)", currentV)
	}

	// Issue KR imports with zero check-ins.
	if got := krCheckinsInWorkspace(t, ctx, pool, targetWSID, "Ship features"); len(got) != 0 {
		t.Fatalf("target issue KR check-ins = %d, want 0", len(got))
	}

	// Re-import adds no duplicate check-ins.
	rep2 := importArchive(t, ctx, pool, targetSlug, admin, archive)
	if rep2.Imported["key_result_checkins"] != 0 {
		t.Fatalf("re-import check-ins = %d, want 0 (skipped: %v)",
			rep2.Imported["key_result_checkins"], rep2.Skipped.Reasons)
	}
	if got := krCheckinsInWorkspace(t, ctx, pool, targetWSID, "Grow revenue"); len(got) != 3 {
		t.Fatalf("check-ins after re-import = %d, want 3", len(got))
	}
}

func TestWorkspaceArchiveOKRCheckinsUnmappedAuthor(t *testing.T) {
	ctx, pool, slug, _, admin, owner := setupArchiveOKRSource(t, "arcokrciun")

	ob, err := CreateObjective(ctx, pool, slug, admin, ObjectiveInput{
		Title: "Grow revenue", OwnerID: &owner,
	})
	if err != nil {
		t.Fatalf("CreateObjective: %v", err)
	}
	kr, err := CreateKeyResult(ctx, pool, slug, admin, ob.ID, KeyResultInput{
		Title: "Grow revenue", Kind: "metric",
		StartValue: fptr(0), TargetValue: fptr(100), CurrentValue: fptr(10),
	})
	if err != nil {
		t.Fatalf("CreateKeyResult: %v", err)
	}
	if _, err := UpdateKeyResult(ctx, pool, slug, admin, ob.ID, kr.ID, KeyResultPatch{
		CurrentValue: fptr(20), CheckinNote: strptr("Push"),
	}); err != nil {
		t.Fatalf("UpdateKeyResult: %v", err)
	}

	// Tamper the archive so both check-ins point at a user that does
	// not exist in this instance — ghost UUID + ghost email.
	archive := exportWorkspaceBytes(t, ctx, pool, slug, admin)
	rows := decodeArchiveObjectives(t, archive)
	if len(rows) != 1 || len(rows[0].KeyResults) != 1 || len(rows[0].KeyResults[0].CheckIns) != 2 {
		t.Fatalf("decoded objectives = %+v, want 1 objective / 1 KR / 2 check-ins", rows)
	}
	ghostEmail := uniqueTestEmail("arcokrciun-ghost")
	ghostID := "ffffffff-ffff-ffff-ffff-ffffffffffff"
	for i := range rows[0].KeyResults[0].CheckIns {
		rows[0].KeyResults[0].CheckIns[i].AuthorID = &ghostID
		rows[0].KeyResults[0].CheckIns[i].AuthorEmail = &ghostEmail
	}
	tampered := spliceObjectivesSection(t, archive, rows)

	targetSlug := uniqueTestSlug("arcokrciun-tgt")
	createTestWorkspace(t, pool, "Target Co", targetSlug, admin)
	rep := importArchive(t, ctx, pool, targetSlug, admin, tampered)
	if rep.Imported["key_result_checkins"] != 2 {
		t.Fatalf("report check-ins = %d, want 2 (skipped: %v)",
			rep.Imported["key_result_checkins"], rep.Skipped.Reasons)
	}
	joined := strings.Join(rep.Skipped.Reasons, "\n")
	if !strings.Contains(joined, ghostEmail) {
		t.Errorf("skipped reasons do not name the unmapped check-in author %q: %q", ghostEmail, joined)
	}
	targetWSID := targetWorkspaceID(t, ctx, pool, targetSlug)
	got := krCheckinsInWorkspace(t, ctx, pool, targetWSID, "Grow revenue")
	if len(got) != 2 {
		t.Fatalf("target check-ins = %d, want 2", len(got))
	}
	// Values, notes and timestamps survive; authors are null —
	// never fabricated.
	if got[0].AuthorEmail != nil || got[1].AuthorEmail != nil {
		t.Fatalf("target check-in authors = %v / %v, want null (no fabricated author)",
			checkinStr(got[0].AuthorEmail), checkinStr(got[1].AuthorEmail))
	}
	if checkinStr(got[0].Prev) != "<nil>" || got[0].New != "10" || got[0].Note != nil {
		t.Fatalf("target seed = prev %q new %q note %q, want prev nil new 10 note nil",
			checkinStr(got[0].Prev), got[0].New, checkinStr(got[0].Note))
	}
	if checkinStr(got[1].Prev) != "10" || got[1].New != "20" || checkinStr(got[1].Note) != "Push" {
		t.Fatalf("target update = prev %q new %q note %q, want prev 10 new 20 note Push",
			checkinStr(got[1].Prev), got[1].New, checkinStr(got[1].Note))
	}
}

func TestWorkspaceArchiveOKRCheckinsOldArchive(t *testing.T) {
	ctx, pool, slug, _, admin, owner := setupArchiveOKRSource(t, "arcokrciold")

	ob, err := CreateObjective(ctx, pool, slug, admin, ObjectiveInput{
		Title: "Grow revenue", OwnerID: &owner,
	})
	if err != nil {
		t.Fatalf("CreateObjective: %v", err)
	}
	if _, err := CreateKeyResult(ctx, pool, slug, admin, ob.ID, KeyResultInput{
		Title: "Grow revenue", Kind: "metric",
		StartValue: fptr(0), TargetValue: fptr(100), CurrentValue: fptr(30),
	}); err != nil {
		t.Fatalf("CreateKeyResult: %v", err)
	}

	// Simulate a pre-C42 archive: delete the check_ins key from
	// every KR entirely (not null — absent).
	archive := exportWorkspaceBytes(t, ctx, pool, slug, admin)
	if !bytes.Contains(archive, []byte(`"check_ins"`)) {
		t.Fatal("archive has no check_ins field to strip")
	}
	prefix := []byte(`"objectives":[`)
	start := bytes.Index(archive, prefix)
	contentStart := start + len(prefix)
	end := bytes.LastIndex(archive, []byte(`]}`))
	var section []map[string]any
	if err := json.Unmarshal(append(append([]byte("["), archive[contentStart:end]...), ']'), &section); err != nil {
		t.Fatalf("decode objectives section: %v", err)
	}
	for _, o := range section {
		krs, _ := o["key_results"].([]any)
		for _, krAny := range krs {
			kr, _ := krAny.(map[string]any)
			delete(kr, "check_ins")
		}
	}
	body, err := json.Marshal(section)
	if err != nil {
		t.Fatalf("marshal stripped objectives: %v", err)
	}
	stripped := spliceObjectivesSectionRaw(t, archive, body[1:len(body)-1])
	if bytes.Contains(stripped, []byte(`"check_ins"`)) {
		t.Fatal("stripped archive still contains check_ins")
	}

	targetSlug := uniqueTestSlug("arcokrciold-tgt")
	createTestWorkspace(t, pool, "Target Co", targetSlug, admin)
	rep := importArchive(t, ctx, pool, targetSlug, admin, stripped)
	if rep.Imported["objectives"] != 1 || rep.Imported["key_results"] != 1 || rep.Imported["key_result_checkins"] != 0 {
		t.Fatalf("report imported = objectives %d / KRs %d / check-ins %d, want 1/1/0 (skipped: %v)",
			rep.Imported["objectives"], rep.Imported["key_results"], rep.Imported["key_result_checkins"], rep.Skipped.Reasons)
	}
	targetWSID := targetWorkspaceID(t, ctx, pool, targetSlug)
	if got := krCheckinsInWorkspace(t, ctx, pool, targetWSID, "Grow revenue"); len(got) != 0 {
		t.Fatalf("target check-ins = %d, want 0 (field absent → no check-ins, no error)", len(got))
	}
}

func TestWorkspaceArchiveOKRCheckinsIssueKRRejected(t *testing.T) {
	ctx, pool, slug, _, admin, _ := setupArchiveOKRSource(t, "arcokrciiss")

	ob, err := CreateObjective(ctx, pool, slug, admin, ObjectiveInput{Title: "Grow revenue"})
	if err != nil {
		t.Fatalf("CreateObjective: %v", err)
	}
	if _, err := CreateKeyResult(ctx, pool, slug, admin, ob.ID, KeyResultInput{
		Title: "Ship features", Kind: "issue",
		StartValue: fptr(0), TargetValue: fptr(2),
	}); err != nil {
		t.Fatalf("CreateKeyResult issue: %v", err)
	}

	// Hand-craft a check-in onto the issue KR — the service could
	// never have created one, so the importer must refuse it.
	archive := exportWorkspaceBytes(t, ctx, pool, slug, admin)
	rows := decodeArchiveObjectives(t, archive)
	if len(rows) != 1 || len(rows[0].KeyResults) != 1 {
		t.Fatalf("decoded objectives = %+v, want 1 objective / 1 KR", rows)
	}
	rows[0].KeyResults[0].CheckIns = []exportKRCheckin{{
		ID:       "44444444-4444-4444-4444-444444444444",
		NewValue: json.Number("1"),
		Note:     strptr("bogus"),
	}}
	tampered := spliceObjectivesSection(t, archive, rows)

	targetSlug := uniqueTestSlug("arcokrciiss-tgt")
	createTestWorkspace(t, pool, "Target Co", targetSlug, admin)
	rep := importArchive(t, ctx, pool, targetSlug, admin, tampered)
	if rep.Imported["key_results"] != 1 || rep.Imported["key_result_checkins"] != 0 {
		t.Fatalf("report imported = KRs %d / check-ins %d, want 1/0 (skipped: %v)",
			rep.Imported["key_results"], rep.Imported["key_result_checkins"], rep.Skipped.Reasons)
	}
	joined := strings.Join(rep.Skipped.Reasons, "\n")
	if !strings.Contains(joined, "never record check-ins") {
		t.Errorf("skipped reasons do not refuse the issue-KR check-in: %q", joined)
	}
	targetWSID := targetWorkspaceID(t, ctx, pool, targetSlug)
	if got := krCheckinsInWorkspace(t, ctx, pool, targetWSID, "Ship features"); len(got) != 0 {
		t.Fatalf("target issue KR check-ins = %d, want 0", len(got))
	}
}
