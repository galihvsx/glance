package service

// Plane import execute tests (C14T3): two-phase import against the real
// test database, no skips. Mirrors the T1/T2 test conventions
// (planeAnalyzeSetup, seedPlaneAnalysisTargets, addWorkspaceMember).

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// planeExecuteSetup mirrors planeAnalyzeSetup: pool, ws slug, project
// identifier, admin actor id, project uuid.
func planeExecuteSetup(t *testing.T) (*pgxpool.Pool, string, string, string, string) {
	t.Helper()
	return planeAnalyzeSetup(t)
}

// seedPlaneMembers adds workspace members for the given display names
// and returns name -> user id.
func seedPlaneMembers(t *testing.T, pool *pgxpool.Pool, slug string, names ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, name := range names {
		out[name] = addWorkspaceMember(t, pool, slug, uniqueTestEmail("px-member"), name, RoleMember)
	}
	return out
}

func peopleResolutions(members map[string]string, names ...string) map[string]PlanePersonResolution {
	out := map[string]PlanePersonResolution{}
	for _, n := range names {
		out[n] = PlanePersonResolution{MemberID: members[n]}
	}
	return out
}

func executeFixture(t *testing.T, pool *pgxpool.Pool, projectID, actor string, resolutions PlaneImportResolutions, opts PlaneImportExecuteOpts) *PlaneImportReport {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "plane-export.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	rep, err := ExecutePlaneImport(context.Background(), pool, projectID, actor, bytes.NewReader(raw), resolutions, opts)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	return rep
}

func queryIssueID(t *testing.T, pool *pgxpool.Pool, projectID string, seq int) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`SELECT id::text FROM issues WHERE project_id = $1::uuid AND sequence_id = $2`,
		projectID, seq).Scan(&id); err != nil {
		t.Fatalf("lookup issue seq %d: %v", seq, err)
	}
	return id
}

func TestExecutePlaneImportHappyPath(t *testing.T) {
	pool, slug, ident, admin, projectID := planeExecuteSetup(t)
	seedPlaneAnalysisTargets(t, pool, slug, ident, admin, projectID)
	members := seedPlaneMembers(t, pool, slug, "Ada Lovelace", "Alan Turing", "Grace Hopper")
	ctx := context.Background()

	rep := executeFixture(t, pool, projectID, admin,
		PlaneImportResolutions{People: peopleResolutions(members, "Ada Lovelace", "Alan Turing", "Grace Hopper")},
		PlaneImportExecuteOpts{})
	if rep.Created != 2 || rep.Skipped != 0 || rep.Failed != 0 {
		t.Fatalf("report = created=%d skipped=%d failed=%d, want 2/0/0 (errors=%v)",
			rep.Created, rep.Skipped, rep.Failed, rep.Errors)
	}
	if len(rep.Errors) != 0 {
		t.Fatalf("errors = %v, want none", rep.Errors)
	}
	if rep.ProjectIdentifier != "ACME" {
		t.Fatalf("project_identifier = %q, want ACME", rep.ProjectIdentifier)
	}

	// ACME-42: sequence preserved, priority high->3, existing state matched.
	id42 := queryIssueID(t, pool, projectID, 42)
	var name string
	var priority int
	var stateName string
	var startDate, targetDate string
	var estimatePointID *string
	var createdAt time.Time
	if err := pool.QueryRow(ctx,
		`SELECT i.name, i.priority, s.name, i.start_date::text, i.target_date::text,
		        i.estimate_point_id::text, i.created_at
		 FROM issues i JOIN states s ON s.id = i.state_id
		 WHERE i.id = $1::uuid`,
		id42).Scan(&name, &priority, &stateName, &startDate, &targetDate, &estimatePointID, &createdAt); err != nil {
		t.Fatalf("read issue 42: %v", err)
	}
	if name != "Login button misaligned on mobile Safari" {
		t.Fatalf("name = %q", name)
	}
	if priority != 3 {
		t.Fatalf("priority = %d, want 3 (high)", priority)
	}
	if stateName != "In Progress" {
		t.Fatalf("state = %q, want In Progress", stateName)
	}
	if startDate != "2026-10-01" || targetDate != "2026-10-15" {
		t.Fatalf("dates = %s/%s, want 2026-10-01/2026-10-15", startDate, targetDate)
	}
	if estimatePointID == nil {
		t.Fatalf("estimate_point_id is NULL, want the Fibonacci point for 3")
	}
	if createdAt.UTC().Format("2006-01-02 15:04:05") != "2026-09-28 09:12:33" {
		t.Fatalf("created_at = %v, want Plane's created_at preserved", createdAt.UTC())
	}

	// Assignee, subscriber, label, cycle, module all wired.
	var assigneeCount, subscriberCount, labelCount, cycleCount, moduleCount int
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM issue_assignees WHERE issue_id = $1::uuid AND user_id = $2::uuid`,
		id42, members["Ada Lovelace"]).Scan(&assigneeCount)
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM issue_subscribers WHERE issue_id = $1::uuid AND user_id = $2::uuid`,
		id42, members["Grace Hopper"]).Scan(&subscriberCount)
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM issue_labels l JOIN labels lb ON lb.id = l.label_id
		WHERE l.issue_id = $1::uuid AND lb.name = 'bug'`, id42).Scan(&labelCount)
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM cycle_issues ci JOIN cycles c ON c.id = ci.cycle_id
		WHERE ci.issue_id = $1::uuid AND c.name = 'Sprint 12'`, id42).Scan(&cycleCount)
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM module_issues mi JOIN modules m ON m.id = mi.module_id
		WHERE mi.issue_id = $1::uuid AND m.name = 'Mobile'`, id42).Scan(&moduleCount)
	for label, n := range map[string]int{"assignee": assigneeCount, "subscriber": subscriberCount,
		"label": labelCount, "cycle": cycleCount, "module": moduleCount} {
		if n != 1 {
			t.Fatalf("%s links = %d, want 1", label, n)
		}
	}

	// Comment: mapped author, stripped body, original UTC timestamp.
	var commentActor, commentBody string
	var commentAt time.Time
	if err := pool.QueryRow(ctx,
		`SELECT actor_id::text, content::text, created_at FROM comments WHERE issue_id = $1::uuid`,
		id42).Scan(&commentActor, &commentBody, &commentAt); err != nil {
		t.Fatalf("read comment: %v", err)
	}
	if !strings.EqualFold(commentActor, members["Ada Lovelace"]) {
		t.Fatalf("comment actor = %s, want Ada's member id", commentActor)
	}
	if !strings.Contains(commentBody, "Reproduced on iOS 18") {
		t.Fatalf("comment body = %q, want the Plane text", commentBody)
	}
	if commentAt.UTC().Format("2006-01-02 15:04:05") != "2026-10-05 14:22:10" {
		t.Fatalf("comment created_at = %v, want naive Plane timestamp as UTC", commentAt.UTC())
	}

	// ACME-43: child of ACME-42 (parent wired despite row order), medium->2.
	id43 := queryIssueID(t, pool, projectID, 43)
	var parentID *string
	var p43 int
	if err := pool.QueryRow(ctx, `SELECT parent_id::text, priority FROM issues WHERE id = $1::uuid`,
		id43).Scan(&parentID, &p43); err != nil {
		t.Fatalf("read issue 43: %v", err)
	}
	if parentID == nil || !strings.EqualFold(*parentID, id42) {
		t.Fatalf("parent_id = %v, want %s", parentID, id42)
	}
	if p43 != 2 {
		t.Fatalf("priority = %d, want 2 (medium)", p43)
	}
	var est43 *string
	pool.QueryRow(ctx, `SELECT estimate_point_id::text FROM issues WHERE id = $1::uuid`, id43).Scan(&est43)
	if est43 != nil {
		t.Fatalf("estimate_point_id set for empty-string estimate, want NULL")
	}

	// Sequence counter advanced past max imported id (43): next native
	// issue must not collide.
	var lastVal int
	if err := pool.QueryRow(ctx, `SELECT last_value FROM issue_sequences WHERE project_id = $1::uuid`,
		projectID).Scan(&lastVal); err != nil {
		t.Fatalf("read sequence: %v", err)
	}
	if lastVal < 43 {
		t.Fatalf("issue_sequences.last_value = %d, want >= 43", lastVal)
	}

	// Import-run record written, keyed by (project, plane identifier).
	var runs int
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM idempotency_keys
		WHERE endpoint = 'plane-import-execute' AND idem_key = $1`,
		"plane-import:"+projectID+":ACME").Scan(&runs)
	if runs != 1 {
		t.Fatalf("import-run records = %d, want 1", runs)
	}

	// _created activity rows exist.
	var activities int
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM issue_activities WHERE field = '_created'
		AND (issue_id = $1::uuid OR issue_id = $2::uuid)`, id42, id43).Scan(&activities)
	if activities != 2 {
		t.Fatalf("activity rows = %d, want 2", activities)
	}
}

func TestExecutePlaneImportZipInput(t *testing.T) {
	pool, slug, ident, admin, projectID := planeExecuteSetup(t)
	seedPlaneAnalysisTargets(t, pool, slug, ident, admin, projectID)
	raw, err := os.ReadFile(filepath.Join("testdata", "plane-export.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("plane-export.json")
	if err != nil {
		t.Fatalf("zip create: %v", err)
	}
	if _, err := w.Write(raw); err != nil {
		t.Fatalf("zip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	rep, err := ExecutePlaneImport(context.Background(), pool, projectID, admin,
		bytes.NewReader(buf.Bytes()), PlaneImportResolutions{}, PlaneImportExecuteOpts{})
	if err != nil {
		t.Fatalf("execute zip: %v", err)
	}
	if rep.Created != 2 {
		t.Fatalf("created = %d, want 2 via zip input", rep.Created)
	}
}

func TestExecutePlaneImportMalformedJSON(t *testing.T) {
	pool, _, _, admin, projectID := planeExecuteSetup(t)
	ctx := context.Background()
	var before int
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM issues`).Scan(&before)

	_, err := ExecutePlaneImport(ctx, pool, projectID, admin,
		strings.NewReader(`{"not": "an array"}`), PlaneImportResolutions{}, PlaneImportExecuteOpts{})
	if !errors.Is(err, ErrPlaneImportShape) {
		t.Fatalf("err = %v, want ErrPlaneImportShape", err)
	}
	var after int
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM issues`).Scan(&after)
	if after != before {
		t.Fatalf("issues changed %d -> %d on whole-file failure", before, after)
	}
}

func TestExecutePlaneImportRowErrorsDontAbort(t *testing.T) {
	pool, slug, ident, admin, projectID := planeExecuteSetup(t)
	seedPlaneAnalysisTargets(t, pool, slug, ident, admin, projectID)
	raw, err := os.ReadFile(filepath.Join("testdata", "plane-export-hostile.json"))
	if err != nil {
		t.Fatalf("read hostile fixture: %v", err)
	}
	rep, err := ExecutePlaneImport(context.Background(), pool, projectID, admin,
		bytes.NewReader(raw), PlaneImportResolutions{}, PlaneImportExecuteOpts{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	// Hostile rows: no identifier / empty name / wrong-typed name /
	// lowercase identifier fail at T1; ACME-103's bad date fails phase 1.
	// ACME-100 and ACME-104 still import.
	if rep.Created != 2 {
		t.Fatalf("created = %d, want 2 (ACME-100, ACME-104)", rep.Created)
	}
	if rep.Failed != 5 {
		t.Fatalf("failed = %d, want 5 (errors=%v)", rep.Failed, rep.Errors)
	}
	if rep.Skipped != 0 {
		t.Fatalf("skipped = %d, want 0", rep.Skipped)
	}
	for _, want := range []string{"row-2", "ACME-101", "ACME-102", "ACME-103", "acme-106"} {
		found := false
		for _, e := range rep.Errors {
			if e.Identifier == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("errors = %v, missing entry for %q", rep.Errors, want)
		}
	}
	// Control row really imported with its priority.
	id := queryIssueID(t, pool, projectID, 100)
	var priority int
	pool.QueryRow(context.Background(), `SELECT priority FROM issues WHERE id = $1::uuid`, id).Scan(&priority)
	if priority != 1 {
		t.Fatalf("priority = %d, want 1 (low)", priority)
	}
}

func TestParsePlanePriority(t *testing.T) {
	cases := []struct {
		in      string
		want    int
		wantErr bool
	}{
		{"urgent", 4, false},
		{"high", 3, false},
		{"medium", 2, false},
		{"low", 1, false},
		{"none", 0, false},
		{"", 0, false},
		{"HIGH", 3, false},
		{" Urgent ", 4, false},
		{"4", 4, false},
		{"0", 0, false},
		{"2", 2, false},
		{"5", 0, true},
		{"-1", 0, true},
		{"bogus", 0, true},
		{"critical", 0, true},
	}
	for _, c := range cases {
		got, err := parsePlanePriority(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parsePlanePriority(%q) = %d, want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parsePlanePriority(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parsePlanePriority(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func planeRowsJSON(t *testing.T, rows string) *bytes.Reader {
	t.Helper()
	return bytes.NewReader([]byte(rows))
}

func TestExecutePlaneImportParentWiring(t *testing.T) {
	pool, _, _, admin, projectID := planeExecuteSetup(t)
	ctx := context.Background()
	// Child before parent in row order; dangling parent; malformed parent.
	rows := `[
		{"identifier":"ACME-50","name":"child first","project_identifier":"ACME","sequence_id":50,
		 "parent":"ACME-51","priority":"low","state_name":"Todo","created_at":"2026-10-01 10:00:00"},
		{"identifier":"ACME-51","name":"the parent","project_identifier":"ACME","sequence_id":51,
		 "parent":"","priority":"low","state_name":"Todo","created_at":"2026-10-01 10:00:00"},
		{"identifier":"ACME-52","name":"orphan","project_identifier":"ACME","sequence_id":52,
		 "parent":"ACME-999","priority":"low","state_name":"Todo","created_at":"2026-10-01 10:00:00"},
		{"identifier":"ACME-53","name":"bad parent","project_identifier":"ACME","sequence_id":53,
		 "parent":"not an identifier!!","priority":"low","state_name":"Todo","created_at":"2026-10-01 10:00:00"},
		{"identifier":"ACME-54","name":"self parent","project_identifier":"ACME","sequence_id":54,
		 "parent":"ACME-54","priority":"low","state_name":"Todo","created_at":"2026-10-01 10:00:00"}
	]`
	rep, err := ExecutePlaneImport(ctx, pool, projectID, admin, planeRowsJSON(t, rows),
		PlaneImportResolutions{}, PlaneImportExecuteOpts{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if rep.Created != 5 || rep.Failed != 0 {
		t.Fatalf("created=%d failed=%d, want 5/0 (errors=%v)", rep.Created, rep.Failed, rep.Errors)
	}
	id50 := queryIssueID(t, pool, projectID, 50)
	id51 := queryIssueID(t, pool, projectID, 51)
	var parent50 *string
	pool.QueryRow(ctx, `SELECT parent_id::text FROM issues WHERE id = $1::uuid`, id50).Scan(&parent50)
	if parent50 == nil || !strings.EqualFold(*parent50, id51) {
		t.Fatalf("ACME-50 parent = %v, want %s (child-before-parent must link)", parent50, id51)
	}
	// Dangling, malformed, and self parents stay root, with gap entries.
	for seq, wantSub := range map[int]string{
		52: "parent ACME-999 not found",
		53: "not a well-formed Plane identifier",
		54: "lists itself as its parent",
	} {
		id := queryIssueID(t, pool, projectID, seq)
		var p *string
		pool.QueryRow(ctx, `SELECT parent_id::text FROM issues WHERE id = $1::uuid`, id).Scan(&p)
		if p != nil {
			t.Fatalf("seq %d parent = %s, want NULL (root)", seq, *p)
		}
		found := false
		for _, g := range rep.Gaps {
			if g.Identifier == map[int]string{52: "ACME-52", 53: "ACME-53", 54: "ACME-54"}[seq] {
				for _, l := range g.Losses {
					if strings.Contains(l, wantSub) {
						found = true
					}
				}
			}
		}
		if !found {
			t.Fatalf("gaps = %+v, missing %q for seq %d", rep.Gaps, wantSub, seq)
		}
	}
}

func TestExecutePlaneImportIdempotent(t *testing.T) {
	pool, slug, ident, admin, projectID := planeExecuteSetup(t)
	seedPlaneAnalysisTargets(t, pool, slug, ident, admin, projectID)
	ctx := context.Background()

	first := executeFixture(t, pool, projectID, admin, PlaneImportResolutions{}, PlaneImportExecuteOpts{})
	if first.Created != 2 {
		t.Fatalf("first run created = %d, want 2", first.Created)
	}
	second := executeFixture(t, pool, projectID, admin, PlaneImportResolutions{}, PlaneImportExecuteOpts{})
	if second.Created != 0 {
		t.Fatalf("second run created = %d, want 0", second.Created)
	}
	if second.Skipped != 2 {
		t.Fatalf("second run skipped = %d, want 2", second.Skipped)
	}
	if second.Failed != 0 || len(second.Errors) != 0 {
		t.Fatalf("second run failed=%d errors=%v, want clean skip", second.Failed, second.Errors)
	}
	var n int
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM issues WHERE project_id = $1::uuid`, projectID).Scan(&n)
	if n != 2 {
		t.Fatalf("issues = %d, want 2 (no duplicates from re-run)", n)
	}
	// The run record is refreshed, not duplicated per actor.
	var runs int
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM idempotency_keys
		WHERE endpoint = 'plane-import-execute' AND user_id = $1::uuid`, admin).Scan(&runs)
	if runs != 1 {
		t.Fatalf("run records = %d, want 1 (upserted)", runs)
	}
}

// TestExecutePlaneImportNativeSequenceCollision: a native glance issue
// holding a sequence a Plane row wants is a loud per-row error — never a
// silent skip — and it must not poison the other rows, the native issue,
// or the idempotency record.
func TestExecutePlaneImportNativeSequenceCollision(t *testing.T) {
	pool, slug, ident, admin, projectID := planeExecuteSetup(t)
	seedPlaneAnalysisTargets(t, pool, slug, ident, admin, projectID)
	ctx := context.Background()

	// Native issue squatting sequence 42 (the fixture's ACME-42).
	var stateID string
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM states WHERE project_id = $1::uuid LIMIT 1`, projectID).Scan(&stateID); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO issues (project_id, sequence_id, name, state_id, created_by)
		 VALUES ($1::uuid, 42, 'native squatter', $2::uuid, $3::uuid)`,
		projectID, stateID, admin); err != nil {
		t.Fatalf("seed native issue: %v", err)
	}

	rep := executeFixture(t, pool, projectID, admin, PlaneImportResolutions{}, PlaneImportExecuteOpts{})
	if rep.Created != 1 {
		t.Fatalf("created = %d, want 1 (only the non-colliding row)", rep.Created)
	}
	if rep.Failed != 1 {
		t.Fatalf("failed = %d, want 1 (the colliding row must fail loudly, not vanish)", rep.Failed)
	}
	found := false
	for _, e := range rep.Errors {
		if e.Identifier == "ACME-42" && strings.Contains(e.Message, "already taken") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no 'already taken' row error for ACME-42; errors=%v", rep.Errors)
	}
	// Re-run: stable and idempotent — the imported row skips by
	// identifier, the colliding row fails again.
	rep2 := executeFixture(t, pool, projectID, admin, PlaneImportResolutions{}, PlaneImportExecuteOpts{})
	if rep2.Created != 0 || rep2.Skipped != 1 || rep2.Failed != 1 {
		t.Fatalf("re-run created=%d skipped=%d failed=%d, want 0/1/1",
			rep2.Created, rep2.Skipped, rep2.Failed)
	}
	// The native issue is untouched.
	var name string
	if err := pool.QueryRow(ctx,
		`SELECT name FROM issues WHERE project_id = $1::uuid AND sequence_id = 42 AND name = 'native squatter'`,
		projectID).Scan(&name); err != nil {
		t.Fatalf("native issue disturbed: %v", err)
	}
}

func TestExecutePlaneImportPeopleMapping(t *testing.T) {
	pool, slug, _, admin, projectID := planeExecuteSetup(t)
	ctx := context.Background()
	members := seedPlaneMembers(t, pool, slug, "Ada Lovelace")
	// Same display name as a member, but deliberately unmapped: must stay
	// unassigned (T2's no-silent-guessing contract).
	rows := `[
		{"identifier":"ACME-60","name":"mapped work","project_identifier":"ACME","sequence_id":60,
		 "assignees":["Ada Lovelace"],"subscribers":["Grace Hopper"],"priority":"low","state_name":"Todo",
		 "created_at":"2026-10-01 10:00:00",
		 "comments":[{"comment":"looks good","created_at":"2026-10-02 09:00:00","created_by":"Ada Lovelace"}]},
		{"identifier":"ACME-61","name":"unmapped work","project_identifier":"ACME","sequence_id":61,
		 "assignees":["Nobody Known"],"priority":"low","state_name":"Todo","created_at":"2026-10-01 10:00:00",
		 "comments":[{"comment":"mystery note","created_at":"2026-10-02 09:00:00","created_by":"Ghost Writer"}]}
	]`
	rep, err := ExecutePlaneImport(ctx, pool, projectID, admin, planeRowsJSON(t, rows),
		PlaneImportResolutions{People: peopleResolutions(members, "Ada Lovelace")},
		PlaneImportExecuteOpts{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if rep.Created != 2 || rep.Failed != 0 {
		t.Fatalf("created=%d failed=%d, want 2/0", rep.Created, rep.Failed)
	}
	// Mapped: assigned to the right member; comment actor is the member.
	id60 := queryIssueID(t, pool, projectID, 60)
	var n int
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM issue_assignees WHERE issue_id = $1::uuid AND user_id = $2::uuid`,
		id60, members["Ada Lovelace"]).Scan(&n)
	if n != 1 {
		t.Fatalf("mapped assignee not assigned")
	}
	var actor60 string
	pool.QueryRow(ctx, `SELECT actor_id::text FROM comments WHERE issue_id = $1::uuid`, id60).Scan(&actor60)
	if !strings.EqualFold(actor60, members["Ada Lovelace"]) {
		t.Fatalf("comment actor = %s, want Ada's member id", actor60)
	}
	// Unmapped subscriber "Grace Hopper" (no resolution): not subscribed.
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM issue_subscribers WHERE issue_id = $1::uuid`, id60).Scan(&n)
	if n != 0 {
		t.Fatalf("unmapped subscriber was subscribed")
	}
	// Unmapped assignee: left unassigned, gap entry recorded.
	id61 := queryIssueID(t, pool, projectID, 61)
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM issue_assignees WHERE issue_id = $1::uuid`, id61).Scan(&n)
	if n != 0 {
		t.Fatalf("unmapped assignee was assigned")
	}
	// Unmapped comment author: attributed to the importing actor, with
	// provenance in the body.
	var actor61, body61 string
	pool.QueryRow(ctx, `SELECT actor_id::text, content::text FROM comments WHERE issue_id = $1::uuid`,
		id61).Scan(&actor61, &body61)
	if !strings.EqualFold(actor61, admin) {
		t.Fatalf("comment actor = %s, want the importing actor %s", actor61, admin)
	}
	if !strings.Contains(body61, "originally posted by Ghost Writer") {
		t.Fatalf("comment body = %q, want original-author attribution", body61)
	}
	// Gap entries for every unmapped person.
	gapLosses := func(ident string) []string {
		for _, g := range rep.Gaps {
			if g.Identifier == ident {
				return g.Losses
			}
		}
		return nil
	}
	if !containsSub(gapLosses("ACME-60"), `subscriber "Grace Hopper" has no member mapping`) {
		t.Fatalf("gaps for ACME-60 = %v, missing subscriber loss", gapLosses("ACME-60"))
	}
	if !containsSub(gapLosses("ACME-61"), `assignee "Nobody Known" has no member mapping`) {
		t.Fatalf("gaps for ACME-61 = %v, missing assignee loss", gapLosses("ACME-61"))
	}
	if !containsSub(gapLosses("ACME-61"), `comment by "Ghost Writer" has no member mapping`) {
		t.Fatalf("gaps for ACME-61 = %v, missing comment-author loss", gapLosses("ACME-61"))
	}
}

func TestExecutePlaneImportRelationsDropped(t *testing.T) {
	pool, slug, ident, admin, projectID := planeExecuteSetup(t)
	seedPlaneAnalysisTargets(t, pool, slug, ident, admin, projectID)
	ctx := context.Background()

	rep := executeFixture(t, pool, projectID, admin, PlaneImportResolutions{}, PlaneImportExecuteOpts{})
	if rep.Created != 2 {
		t.Fatalf("created = %d, want 2", rep.Created)
	}
	// Nothing inserted into issue_relations.
	var n int
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM issue_relations`).Scan(&n)
	if n != 0 {
		t.Fatalf("issue_relations = %d, want 0 (relations are gaps, never rows)", n)
	}
	// Per-issue gap entries; the outgoing+incoming pair on ACME-42/43 is
	// one entry per issue (deduped within the row).
	for _, g := range rep.Gaps {
		if g.Identifier == "ACME-42" && !containsSub(g.Losses, "relation with ACME-43 (blocking) dropped") {
			t.Fatalf("ACME-42 losses = %v, missing relation drop", g.Losses)
		}
		if g.Identifier == "ACME-43" && !containsSub(g.Losses, "relation with ACME-42 (blocking) dropped") {
			t.Fatalf("ACME-43 losses = %v, missing relation drop", g.Losses)
		}
	}
}

func TestExecutePlaneImportStatePolicy(t *testing.T) {
	pool, _, _, admin, projectID := planeExecuteSetup(t)
	ctx := context.Background()
	rows := `[
		{"identifier":"ACME-70","name":"needs review","project_identifier":"ACME","sequence_id":70,
		 "priority":"low","state_name":"In Review","created_at":"2026-10-01 10:00:00"}
	]`

	// Default: unknown state_name is created in group "backlog".
	rep, err := ExecutePlaneImport(ctx, pool, projectID, admin, planeRowsJSON(t, rows),
		PlaneImportResolutions{}, PlaneImportExecuteOpts{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if rep.Created != 1 || rep.Failed != 0 {
		t.Fatalf("created=%d failed=%d, want 1/0", rep.Created, rep.Failed)
	}
	var group, color string
	if err := pool.QueryRow(ctx,
		`SELECT "group", color FROM states WHERE project_id = $1::uuid AND name = 'In Review'`,
		projectID).Scan(&group, &color); err != nil {
		t.Fatalf("created state not found: %v", err)
	}
	if group != "backlog" {
		t.Fatalf("group = %q, want backlog (design decision 6)", group)
	}
	if !containsSub(rep.StatesCreated, "In Review (group backlog)") {
		t.Fatalf("states_created = %v, want the In Review entry", rep.StatesCreated)
	}

	// strict_states: the same unknown name is a row error instead.
	rows2 := `[
		{"identifier":"ACME-71","name":"strict row","project_identifier":"ACME","sequence_id":71,
		 "priority":"low","state_name":"Needs Triage","created_at":"2026-10-01 10:00:00"}
	]`
	rep2, err := ExecutePlaneImport(ctx, pool, projectID, admin, planeRowsJSON(t, rows2),
		PlaneImportResolutions{}, PlaneImportExecuteOpts{StrictStates: true})
	if err != nil {
		t.Fatalf("execute strict: %v", err)
	}
	if rep2.Created != 0 || rep2.Failed != 1 {
		t.Fatalf("strict: created=%d failed=%d, want 0/1", rep2.Created, rep2.Failed)
	}
	if !containsSub([]string{rep2.Errors[0].Message}, "strict_states") {
		t.Fatalf("strict error = %q, want strict_states mention", rep2.Errors[0].Message)
	}
	var n int
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM states WHERE project_id = $1::uuid AND name = 'Needs Triage'`,
		projectID).Scan(&n)
	if n != 0 {
		t.Fatalf("strict mode created the state anyway")
	}

	// Resolution can request creation in a specific group.
	rows3 := `[
		{"identifier":"ACME-72","name":"qa row","project_identifier":"ACME","sequence_id":72,
		 "priority":"low","state_name":"QA","created_at":"2026-10-01 10:00:00"}
	]`
	rep3, err := ExecutePlaneImport(ctx, pool, projectID, admin, planeRowsJSON(t, rows3),
		PlaneImportResolutions{States: map[string]PlaneStateResolution{
			"QA": {Group: "started"},
		}}, PlaneImportExecuteOpts{})
	if err != nil {
		t.Fatalf("execute group resolution: %v", err)
	}
	if rep3.Created != 1 {
		t.Fatalf("created = %d, want 1", rep3.Created)
	}
	pool.QueryRow(ctx, `SELECT "group" FROM states WHERE project_id = $1::uuid AND name = 'QA'`,
		projectID).Scan(&group)
	if group != "started" {
		t.Fatalf("group = %q, want started (resolution)", group)
	}
}

func TestExecutePlaneImportGaps(t *testing.T) {
	pool, slug, ident, admin, projectID := planeExecuteSetup(t)
	seedPlaneAnalysisTargets(t, pool, slug, ident, admin, projectID)

	rep := executeFixture(t, pool, projectID, admin, PlaneImportResolutions{}, PlaneImportExecuteOpts{})
	if len(rep.Gaps) != 2 {
		t.Fatalf("gaps = %d entries, want one per imported issue", len(rep.Gaps))
	}
	losses42 := map[string]bool{}
	for _, g := range rep.Gaps {
		if g.Identifier == "ACME-42" {
			for _, l := range g.Losses {
				losses42[l] = true
			}
		}
	}
	// The honest-loss list for the canonical row.
	for _, wantSub := range []string{
		"description is never present in Plane exports",
		"relation with ACME-43 (blocking) dropped",
		"2 attachment(s) referenced but not imported",
		`link "Login screen spec" (https://example.com/designs/login-spec) not imported`,
	} {
		found := false
		for l := range losses42 {
			if strings.Contains(l, wantSub) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("ACME-42 losses missing %q (have %v)", wantSub, losses42)
		}
	}
}

func TestExecutePlaneImportEstimateHandling(t *testing.T) {
	pool, slug, ident, admin, projectID := planeExecuteSetup(t)
	seedPlaneAnalysisTargets(t, pool, slug, ident, admin, projectID)
	ctx := context.Background()
	rows := `[
		{"identifier":"ACME-80","name":"good estimate","project_identifier":"ACME","sequence_id":80,
		 "estimate":5,"priority":"low","state_name":"Todo","created_at":"2026-10-01 10:00:00"},
		{"identifier":"ACME-81","name":"bad estimate","project_identifier":"ACME","sequence_id":81,
		 "estimate":99,"priority":"low","state_name":"Todo","created_at":"2026-10-01 10:00:00"},
		{"identifier":"ACME-82","name":"unset estimate","project_identifier":"ACME","sequence_id":82,
		 "estimate":"","priority":"low","state_name":"Todo","created_at":"2026-10-01 10:00:00"},
		{"identifier":"ACME-83","name":"garbage estimate","project_identifier":"ACME","sequence_id":83,
		 "estimate":[1,2],"priority":"low","state_name":"Todo","created_at":"2026-10-01 10:00:00"}
	]`
	rep, err := ExecutePlaneImport(ctx, pool, projectID, admin, planeRowsJSON(t, rows),
		PlaneImportResolutions{}, PlaneImportExecuteOpts{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if rep.Created != 3 || rep.Failed != 1 {
		t.Fatalf("created=%d failed=%d, want 3/1 (errors=%v)", rep.Created, rep.Failed, rep.Errors)
	}
	// 5 matches the Fibonacci point; 99 matches nothing -> NULL + gap;
	// "" -> NULL, no gap; [1,2] -> row error.
	var p80, p81, p82 *string
	pool.QueryRow(ctx, `SELECT estimate_point_id::text FROM issues WHERE project_id = $1::uuid AND sequence_id = 80`,
		projectID).Scan(&p80)
	pool.QueryRow(ctx, `SELECT estimate_point_id::text FROM issues WHERE project_id = $1::uuid AND sequence_id = 81`,
		projectID).Scan(&p81)
	pool.QueryRow(ctx, `SELECT estimate_point_id::text FROM issues WHERE project_id = $1::uuid AND sequence_id = 82`,
		projectID).Scan(&p82)
	if p80 == nil {
		t.Fatalf("estimate 5: point not linked")
	}
	if p81 != nil {
		t.Fatalf("estimate 99: point linked, want NULL")
	}
	if p82 != nil {
		t.Fatalf("empty estimate: point linked, want NULL")
	}
	gap := func(ident string) []string {
		for _, g := range rep.Gaps {
			if g.Identifier == ident {
				return g.Losses
			}
		}
		return nil
	}
	if !containsSub(gap("ACME-81"), "estimate value 99 matches no point") {
		t.Fatalf("ACME-81 gaps = %v, missing estimate loss", gap("ACME-81"))
	}
	if containsSub(gap("ACME-82"), "estimate") {
		t.Fatalf("ACME-82 gaps = %v, unset estimate must not produce a loss", gap("ACME-82"))
	}
	if rep.Errors[0].Identifier != "ACME-83" {
		t.Fatalf("errors = %v, want ACME-83 (array estimate)", rep.Errors)
	}
}

func TestExecutePlaneImportRowCap(t *testing.T) {
	pool, slug, ident, admin, projectID := planeExecuteSetup(t)
	seedPlaneAnalysisTargets(t, pool, slug, ident, admin, projectID)
	raw, _ := os.ReadFile(filepath.Join("testdata", "plane-export.json"))

	_, err := ExecutePlaneImport(context.Background(), pool, projectID, admin,
		bytes.NewReader(raw), PlaneImportResolutions{}, PlaneImportExecuteOpts{MaxRows: 1})
	if err == nil || !strings.Contains(err.Error(), "soft cap") {
		t.Fatalf("err = %v, want the soft-cap error", err)
	}
	var n int
	pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM issues WHERE project_id = $1::uuid`, projectID).Scan(&n)
	if n != 0 {
		t.Fatalf("issues = %d after cap rejection, want 0 (nothing written)", n)
	}
}

func TestExecutePlaneImportAuth(t *testing.T) {
	pool, slug, _, admin, projectID := planeExecuteSetup(t)
	ctx := context.Background()
	raw, _ := os.ReadFile(filepath.Join("testdata", "plane-export.json"))

	guest := addWorkspaceMember(t, pool, slug, uniqueTestEmail("px-guest"), "Guest", RoleGuest)
	if _, err := ExecutePlaneImport(ctx, pool, projectID, guest,
		bytes.NewReader(raw), PlaneImportResolutions{}, PlaneImportExecuteOpts{}); err != ErrForbidden {
		t.Fatalf("guest: err = %v, want ErrForbidden", err)
	}
	outsider := createTestUser(t, pool, uniqueTestEmail("px-out"))
	if _, err := ExecutePlaneImport(ctx, pool, projectID, outsider,
		bytes.NewReader(raw), PlaneImportResolutions{}, PlaneImportExecuteOpts{}); err != ErrNotFound {
		t.Fatalf("outsider: err = %v, want ErrNotFound", err)
	}
	if _, err := ExecutePlaneImport(ctx, pool, "00000000-0000-0000-0000-000000000000", admin,
		bytes.NewReader(raw), PlaneImportResolutions{}, PlaneImportExecuteOpts{}); err != ErrProjectNotFound {
		t.Fatalf("bad project: err = %v, want ErrProjectNotFound", err)
	}
}

func TestExecutePlaneImportResolutionValidation(t *testing.T) {
	pool, _, _, admin, projectID := planeExecuteSetup(t)
	ctx := context.Background()
	rows := `[{"identifier":"ACME-90","name":"x","project_identifier":"ACME","sequence_id":90,
		"priority":"low","state_name":"Todo","created_at":"2026-10-01 10:00:00",
		"assignees":["Ada Lovelace"]}]`

	// Unknown member id in a people resolution: whole-run error.
	_, err := ExecutePlaneImport(ctx, pool, projectID, admin, planeRowsJSON(t, rows),
		PlaneImportResolutions{People: map[string]PlanePersonResolution{
			"Ada Lovelace": {MemberID: "00000000-0000-0000-0000-000000000000"},
		}}, PlaneImportExecuteOpts{})
	if err == nil || !strings.Contains(err.Error(), "not a workspace member") {
		t.Fatalf("err = %v, want non-member resolution error", err)
	}
	// Unknown state group: whole-run error.
	_, err = ExecutePlaneImport(ctx, pool, projectID, admin, planeRowsJSON(t, rows),
		PlaneImportResolutions{States: map[string]PlaneStateResolution{
			"Todo": {Group: "bogus"},
		}}, PlaneImportExecuteOpts{})
	if err == nil || !strings.Contains(err.Error(), "unknown group") {
		t.Fatalf("err = %v, want unknown-group error", err)
	}
	// Cycle resolution with end before start: whole-run error.
	_, err = ExecutePlaneImport(ctx, pool, projectID, admin, planeRowsJSON(t, rows),
		PlaneImportResolutions{Cycles: map[string]PlaneCycleResolution{
			"Sprint X": {StartDate: "2026-02-01", EndDate: "2026-01-01"},
		}}, PlaneImportExecuteOpts{})
	if err == nil || !strings.Contains(err.Error(), "end_date") {
		t.Fatalf("err = %v, want end_date error", err)
	}
	var n int
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM issues WHERE project_id = $1::uuid`, projectID).Scan(&n)
	if n != 0 {
		t.Fatalf("issues = %d after rejected resolutions, want 0", n)
	}
}

func TestExecutePlaneImportCycleModuleCreation(t *testing.T) {
	pool, _, _, admin, projectID := planeExecuteSetup(t)
	ctx := context.Background()
	// No cycles/modules seeded: the importer creates them with documented
	// defaults and links the issues.
	rows := `[
		{"identifier":"ACME-95","name":"cycled","project_identifier":"ACME","sequence_id":95,
		 "cycles":["Sprint 99"],"modules":["Platform"],"priority":"low","state_name":"Todo",
		 "created_at":"2026-10-01 10:00:00"}
	]`
	rep, err := ExecutePlaneImport(ctx, pool, projectID, admin, planeRowsJSON(t, rows),
		PlaneImportResolutions{}, PlaneImportExecuteOpts{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if rep.Created != 1 {
		t.Fatalf("created = %d, want 1", rep.Created)
	}
	if !containsSub(rep.CyclesCreated, "Sprint 99") {
		t.Fatalf("cycles_created = %v, want Sprint 99", rep.CyclesCreated)
	}
	if !containsSub(rep.ModulesCreated, "Platform") {
		t.Fatalf("modules_created = %v, want Platform", rep.ModulesCreated)
	}
	id := queryIssueID(t, pool, projectID, 95)
	var n int
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM cycle_issues ci JOIN cycles c ON c.id = ci.cycle_id
		WHERE ci.issue_id = $1::uuid AND c.name = 'Sprint 99'`, id).Scan(&n)
	if n != 1 {
		t.Fatalf("cycle membership missing")
	}
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM module_issues mi JOIN modules m ON m.id = mi.module_id
		WHERE mi.issue_id = $1::uuid AND m.name = 'Platform'`, id).Scan(&n)
	if n != 1 {
		t.Fatalf("module membership missing")
	}
	var start, end, status string
	pool.QueryRow(ctx, `SELECT start_date::text, end_date::text, status FROM cycles
		WHERE project_id = $1::uuid AND name = 'Sprint 99'`, projectID).Scan(&start, &end, &status)
	if start == "" || end == "" || start > end {
		t.Fatalf("cycle dates = %s/%s, want a valid default window", start, end)
	}
	if status != "upcoming" {
		t.Fatalf("cycle status = %q, want upcoming", status)
	}
	// Resolution-provided cycle dates are honored.
	rows2 := `[
		{"identifier":"ACME-96","name":"cycled 2","project_identifier":"ACME","sequence_id":96,
		 "cycles":["Sprint 100"],"priority":"low","state_name":"Todo","created_at":"2026-10-01 10:00:00"}
	]`
	rep2, err := ExecutePlaneImport(ctx, pool, projectID, admin, planeRowsJSON(t, rows2),
		PlaneImportResolutions{Cycles: map[string]PlaneCycleResolution{
			"Sprint 100": {StartDate: "2026-03-01", EndDate: "2026-03-14"},
		}}, PlaneImportExecuteOpts{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if rep2.Created != 1 {
		t.Fatalf("created = %d, want 1", rep2.Created)
	}
	pool.QueryRow(ctx, `SELECT start_date::text, end_date::text FROM cycles
		WHERE project_id = $1::uuid AND name = 'Sprint 100'`, projectID).Scan(&start, &end)
	if start != "2026-03-01" || end != "2026-03-14" {
		t.Fatalf("cycle dates = %s/%s, want the resolution's dates", start, end)
	}
}

func TestStripPlaneHTML(t *testing.T) {
	cases := []struct{ in, want string }{
		{`<p>Hello <b>world</b></p>`, "Hello world"},
		{`<script>alert('xss')</script>visible`, "visible"},
		{`<style>.a{color:red}</style>text`, "text"},
		{`a &amp; b &lt;c&gt;`, "a & b <c>"},
		{"line1<br/>line2", "line1 line2"},
		{`plain text`, "plain text"},
		{``, ""},
	}
	for _, c := range cases {
		if got := stripPlaneHTML(c.in); got != c.want {
			t.Errorf("stripPlaneHTML(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestExecutePlaneImportCommentHTMLStripped(t *testing.T) {
	pool, _, _, admin, projectID := planeExecuteSetup(t)
	ctx := context.Background()
	rows := `[
		{"identifier":"ACME-97","name":"html comment","project_identifier":"ACME","sequence_id":97,
		 "priority":"low","state_name":"Todo","created_at":"2026-10-01 10:00:00",
		 "comments":[{"comment":"<p>See <a href=\"https://x.example\">this</a></p><script>evil()</script>",
		 "created_at":"2026-10-02 09:00:00","created_by":""}]}
	]`
	rep, err := ExecutePlaneImport(ctx, pool, projectID, admin, planeRowsJSON(t, rows),
		PlaneImportResolutions{}, PlaneImportExecuteOpts{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if rep.Created != 1 {
		t.Fatalf("created = %d, want 1", rep.Created)
	}
	id := queryIssueID(t, pool, projectID, 97)
	var body string
	pool.QueryRow(ctx, `SELECT content::text FROM comments WHERE issue_id = $1::uuid`, id).Scan(&body)
	if strings.Contains(body, "<p>") || strings.Contains(body, "evil()") {
		t.Fatalf("comment body = %q, want markup stripped", body)
	}
	if !strings.Contains(body, "See this") {
		t.Fatalf("comment body = %q, want the text content", body)
	}
}

func TestExecutePlaneImportDuplicateIdentifiersInExport(t *testing.T) {
	pool, _, _, admin, projectID := planeExecuteSetup(t)
	ctx := context.Background()
	// Same sequence twice in one export: first wins, second is skipped
	// (check-then-skip sees the in-tx insert).
	rows := `[
		{"identifier":"ACME-98","name":"first wins","project_identifier":"ACME","sequence_id":98,
		 "priority":"high","state_name":"Todo","created_at":"2026-10-01 10:00:00"},
		{"identifier":"ACME-98","name":"second loses","project_identifier":"ACME","sequence_id":98,
		 "priority":"low","state_name":"Todo","created_at":"2026-10-01 10:00:00"}
	]`
	rep, err := ExecutePlaneImport(ctx, pool, projectID, admin, planeRowsJSON(t, rows),
		PlaneImportResolutions{}, PlaneImportExecuteOpts{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if rep.Created != 1 || rep.Skipped != 1 {
		t.Fatalf("created=%d skipped=%d, want 1/1", rep.Created, rep.Skipped)
	}
	id := queryIssueID(t, pool, projectID, 98)
	var name string
	var priority int
	pool.QueryRow(ctx, `SELECT name, priority FROM issues WHERE id = $1::uuid`, id).Scan(&name, &priority)
	if name != "first wins" || priority != 3 {
		t.Fatalf("kept row = %q/%d, want the first occurrence", name, priority)
	}
}

// TestExecutePlaneImportReportJSON ensures the report (and the
// resolutions input) round-trip through JSON — T4 will serve/accept them
// as HTTP bodies.
func TestExecutePlaneImportReportJSON(t *testing.T) {
	rep := &PlaneImportReport{
		ProjectIdentifier: "ACME",
		Created:           2,
		Gaps:              []PlaneImportGap{{Identifier: "ACME-42", Losses: []string{"x"}}},
		Errors:            []PlaneImportRowError{{Identifier: "ACME-43", Message: "y"}},
	}
	data, err := json.Marshal(rep)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	var back PlaneImportReport
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if back.Created != 2 || back.Gaps[0].Identifier != "ACME-42" {
		t.Fatalf("round-trip mismatch: %+v", back)
	}
	res := PlaneImportResolutions{
		People: map[string]PlanePersonResolution{"Ada Lovelace": {MemberID: "u1"}},
		States: map[string]PlaneStateResolution{"In Review": {Group: "backlog"}},
		Cycles: map[string]PlaneCycleResolution{"Sprint 9": {StartDate: "2026-01-01", EndDate: "2026-01-14"}},
	}
	rdata, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal resolutions: %v", err)
	}
	var rback PlaneImportResolutions
	if err := json.Unmarshal(rdata, &rback); err != nil {
		t.Fatalf("unmarshal resolutions: %v", err)
	}
	if rback.People["Ada Lovelace"].MemberID != "u1" ||
		rback.Cycles["Sprint 9"].EndDate != "2026-01-14" {
		t.Fatalf("resolutions round-trip mismatch: %+v", rback)
	}
}
