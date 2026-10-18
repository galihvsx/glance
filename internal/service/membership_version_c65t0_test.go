package service

// C65T0: single-mutation membership versions. A real label/assignee
// attach/detach/assign/unassign with NO automation cascade must leave
// exactly one new version carrying the post-mutation issue row (the
// issue_activities 'labels'/'assignees' rows already record the
// old/new sets, so the snapshot stays the plain post-mutation row).
// Idempotent no-ops leave the version count untouched, and a mutation
// whose firing cascades a row move still leaves exactly one version
// (the cascade snapshot), never two.
//
// Restore semantics are row-fields-only and unchanged by this task: a
// restore-to-latest after a pure membership change is a row no-op, so
// the C64T0 409 no-op-restore guard fires there. That is asserted
// nowhere here on purpose — do not "fix" it by touching restore.

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func c65VersionCount(t *testing.T, pool *pgxpool.Pool, slug, ident, issueID, actor string) int {
	t.Helper()
	vs, err := ListVersions(context.Background(), pool, slug, ident, issueID, actor)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	return len(vs)
}

func c65LatestSnapshot(t *testing.T, pool *pgxpool.Pool, slug, ident, issueID, actor string) Issue {
	t.Helper()
	vs, err := ListVersions(context.Background(), pool, slug, ident, issueID, actor)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(vs) == 0 {
		t.Fatalf("versions = 0, want at least 1")
	}
	var snap Issue
	if err := json.Unmarshal(vs[len(vs)-1].Snapshot, &snap); err != nil {
		t.Fatalf("decode latest snapshot: %v", err)
	}
	return snap
}

func c65LiveRow(t *testing.T, pool *pgxpool.Pool, issueID string) *Issue {
	t.Helper()
	iss, err := scanIssue(pool.QueryRow(context.Background(),
		`SELECT `+issueColumns+` FROM issues WHERE id = $1::uuid`, issueID))
	if err != nil {
		t.Fatalf("scan live issue row: %v", err)
	}
	return iss
}

// c65AssertSingleMembershipVersion asserts the newest version carries
// the post-mutation row: same content as the live row
// (issueRowContentEqual semantics — UpdatedAt/DisplayID/SubtaskProgress
// excluded) and the derived DisplayID every call site stamps.
func c65AssertSingleMembershipVersion(t *testing.T, fx automationFixture, issueID string) {
	t.Helper()
	live := c65LiveRow(t, fx.pool, issueID)
	snap := c65LatestSnapshot(t, fx.pool, fx.slug, fx.ident, issueID, fx.admin)
	if !issueRowContentEqual(&snap, live) {
		t.Fatalf("latest snapshot row content differs from the live row")
	}
	// DisplayID is derived from the NORMALIZED identifier
	// (normalizeIdentifier uppercases), like every other snapshot.
	if want := strings.ToUpper(fx.ident) + "-" + strconv.Itoa(live.SequenceID); snap.DisplayID != want {
		t.Fatalf("snapshot DisplayID = %q, want derived %q", snap.DisplayID, want)
	}
}

func TestC65T0AssignLabelSnapshotsOneVersion(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()
	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "c65 label attach")
	if got := c65VersionCount(t, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin); got != 1 {
		t.Fatalf("versions before attach = %d, want 1 (create v1)", got)
	}

	if err := AssignLabel(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.labelID, fx.admin); err != nil {
		t.Fatalf("AssignLabel: %v", err)
	}

	if got := c65VersionCount(t, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin); got != 2 {
		t.Fatalf("versions = %d, want 2 (v1 create + v2 attach)", got)
	}
	c65AssertSingleMembershipVersion(t, fx, iss.ID)
}

func TestC65T0ReattachLabelSnapshotsNothing(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()
	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "c65 label reattach")
	if err := AssignLabel(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.labelID, fx.admin); err != nil {
		t.Fatalf("AssignLabel: %v", err)
	}
	if got := c65VersionCount(t, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin); got != 2 {
		t.Fatalf("versions after attach = %d, want 2", got)
	}

	// Idempotent re-attach (0 rows): no new version.
	if err := AssignLabel(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.labelID, fx.admin); err != nil {
		t.Fatalf("AssignLabel re-attach: %v", err)
	}
	if got := c65VersionCount(t, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin); got != 2 {
		t.Fatalf("versions after re-attach = %d, want 2 (no-op must not snapshot)", got)
	}
}

func TestC65T0DetachAbsentLabelSnapshotsNothing(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()
	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "c65 label detach absent")
	other, err := CreateLabel(ctx, fx.pool, fx.slug, fx.ident, fx.admin,
		LabelInput{Name: "c65-never-attached", Color: autoStrPtr("#a3a3a3")})
	if err != nil {
		t.Fatalf("CreateLabel: %v", err)
	}
	if got := c65VersionCount(t, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin); got != 1 {
		t.Fatalf("versions before detach = %d, want 1 (create v1)", got)
	}

	// Detaching a label that was never attached (0 rows): no version.
	if err := UnassignLabel(ctx, fx.pool, fx.slug, fx.ident, iss.ID, other.ID, fx.admin); err != nil {
		t.Fatalf("UnassignLabel absent: %v", err)
	}
	if got := c65VersionCount(t, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin); got != 1 {
		t.Fatalf("versions after absent detach = %d, want 1 (no-op must not snapshot)", got)
	}
}

func TestC65T0AttachLabelCascadeSnapshotsExactlyOne(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()
	in := eventRuleInput("c65 label finisher",
		AutomationTrigger{Type: "issue.labels_changed"},
		[]AutomationAction{{Type: "set_state", StateID: &fx.completed}})
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "c65 label cascade")
	if got := c65LiveRow(t, fx.pool, iss.ID).StateID; got != fx.backlog {
		t.Fatalf("issue starts in state %s, want backlog %s", got, fx.backlog)
	}

	if err := AssignLabel(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.labelID, fx.admin); err != nil {
		t.Fatalf("AssignLabel: %v", err)
	}

	// The firing moved the row: the cascade snapshot fired, and no
	// second version may land on top of it.
	if got := c65LiveRow(t, fx.pool, iss.ID).StateID; got != fx.completed {
		t.Fatalf("issue state = %s, want cascade-moved %s", got, fx.completed)
	}
	if got := c65VersionCount(t, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin); got != 2 {
		t.Fatalf("versions = %d, want 2 (v1 create + v2 cascade, no double-snapshot)", got)
	}
	if snap := c65LatestSnapshot(t, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin); snap.StateID != fx.completed {
		t.Fatalf("snapshot StateID = %s, want post-cascade %s", snap.StateID, fx.completed)
	}
}

func TestC65T0AssignAssigneeSnapshotsOneVersion(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()
	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "c65 assign")
	if got := c65VersionCount(t, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin); got != 1 {
		t.Fatalf("versions before assign = %d, want 1 (create v1)", got)
	}

	if err := AssignAssignee(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.member, fx.admin); err != nil {
		t.Fatalf("AssignAssignee: %v", err)
	}

	if got := c65VersionCount(t, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin); got != 2 {
		t.Fatalf("versions = %d, want 2 (v1 create + v2 assign)", got)
	}
	c65AssertSingleMembershipVersion(t, fx, iss.ID)
}

func TestC65T0ReassignAssigneeSnapshotsNothing(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()
	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "c65 reassign")
	if err := AssignAssignee(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.member, fx.admin); err != nil {
		t.Fatalf("AssignAssignee: %v", err)
	}
	if got := c65VersionCount(t, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin); got != 2 {
		t.Fatalf("versions after assign = %d, want 2", got)
	}

	// Idempotent re-assign (0 rows): no new version.
	if err := AssignAssignee(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.member, fx.admin); err != nil {
		t.Fatalf("AssignAssignee re-assign: %v", err)
	}
	if got := c65VersionCount(t, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin); got != 2 {
		t.Fatalf("versions after re-assign = %d, want 2 (no-op must not snapshot)", got)
	}
}

func TestC65T0UnassignAbsentAssigneeSnapshotsNothing(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()
	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "c65 unassign absent")
	if got := c65VersionCount(t, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin); got != 1 {
		t.Fatalf("versions before unassign = %d, want 1 (create v1)", got)
	}

	// Unassigning someone never assigned (0 rows): no version.
	if err := UnassignAssignee(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.member, fx.admin); err != nil {
		t.Fatalf("UnassignAssignee absent: %v", err)
	}
	if got := c65VersionCount(t, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin); got != 1 {
		t.Fatalf("versions after absent unassign = %d, want 1 (no-op must not snapshot)", got)
	}
}

func TestC65T0AssignAssigneeCascadeSnapshotsExactlyOne(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()
	in := eventRuleInput("c65 assign finisher",
		AutomationTrigger{Type: "issue.assigned"},
		[]AutomationAction{{Type: "set_state", StateID: &fx.completed}})
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "c65 assign cascade")
	if err := AssignAssignee(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.member, fx.admin); err != nil {
		t.Fatalf("AssignAssignee: %v", err)
	}

	// The firing moved the row: the cascade snapshot fired, and no
	// second version may land on top of it.
	if got := c65LiveRow(t, fx.pool, iss.ID).StateID; got != fx.completed {
		t.Fatalf("issue state = %s, want cascade-moved %s", got, fx.completed)
	}
	if got := c65VersionCount(t, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin); got != 2 {
		t.Fatalf("versions = %d, want 2 (v1 create + v2 cascade, no double-snapshot)", got)
	}
	if snap := c65LatestSnapshot(t, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin); snap.StateID != fx.completed {
		t.Fatalf("snapshot StateID = %s, want post-cascade %s", snap.StateID, fx.completed)
	}
}
