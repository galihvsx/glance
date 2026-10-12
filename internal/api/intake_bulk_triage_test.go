package api

// Intake bulk triage endpoint tests (Cycle 25, C25T2): POST
// .../intake/bulk-triage triages many intake rows in one call, reusing the
// per-item accept/reject service logic — partial success matrix
// (accepted / skipped already-triaged / per-item errors), reject action,
// accept-with-assignee, 400 on empty ids / bad action / non-member
// assignee, 403 for a guest. Real test database, no skips.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

type bulkTriageItemError struct {
	IntakeID string `json:"intake_id"`
	Error    string `json:"error"`
}

type bulkTriageResult struct {
	Accepted []string              `json:"accepted"`
	Rejected []string              `json:"rejected"`
	Skipped  []string              `json:"skipped"`
	Errors   []bulkTriageItemError `json:"errors"`
}

type intakeBulkFixture struct {
	e          *echo.Echo
	pool       *pgxpool.Pool
	cookie     *http.Cookie
	slug       string
	ident      string
	issuesBase string
	intakeBase string
}

func setupIntakeBulk(t *testing.T) intakeBulkFixture {
	t.Helper()
	pool := newTestPool(t)
	e := testIntakeServer(t, pool)
	cookie := loginTestUser(t, e, pool, uniqueEmail("intake-bulk"), "test-agent", uniqueIP())
	slug := uniqueSlug("intake-bulk")
	createWorkspaceHTTP(t, e, cookie, "Bulk Co", slug)
	ident := uniqueProjectIdentifier("BK")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	issuesBase := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/issues"
	intakeBase := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/intake"
	return intakeBulkFixture{e, pool, cookie, slug, ident, issuesBase, intakeBase}
}

func (fx *intakeBulkFixture) createIntakeIssue(t *testing.T, name string) (issueID string) {
	t.Helper()
	rec := postAuthedJSON(t, fx.e, http.MethodPost, fx.issuesBase, fx.cookie,
		`{"name":`+strconv.Quote(name)+`,"intake":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create intake issue %q: status = %d (body: %s)", name, rec.Code, rec.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	return created.ID
}

// intakeRowIDs maps issue uuid → intake row id from GET /intake (only
// pending/surfaced rows are listed — call before triaging the rows you
// need ids for).
func (fx *intakeBulkFixture) intakeRowIDs(t *testing.T) map[string]string {
	t.Helper()
	rec := getAuthed(t, fx.e, http.MethodGet, fx.intakeBase, fx.cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("get intake: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var inbox struct {
		Items []struct {
			ID      string `json:"id"`
			IssueID string `json:"issue_id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &inbox); err != nil {
		t.Fatalf("decode inbox: %v", err)
	}
	m := make(map[string]string, len(inbox.Items))
	for _, it := range inbox.Items {
		m[it.IssueID] = it.ID
	}
	return m
}

// bulkTriage POSTs payload to the bulk-triage endpoint and decodes the
// result envelope when the status is 200.
func (fx *intakeBulkFixture) bulkTriage(t *testing.T, cookie *http.Cookie, payload string) (int, bulkTriageResult) {
	t.Helper()
	rec := postAuthedJSON(t, fx.e, http.MethodPost, fx.intakeBase+"/bulk-triage", cookie, payload)
	var res bulkTriageResult
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
			t.Fatalf("decode bulk triage: %v (body: %s)", err, rec.Body.String())
		}
	}
	return rec.Code, res
}

func containsExactly(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestIntakeBulkTriageAcceptMatrix(t *testing.T) {
	fx := setupIntakeBulk(t)
	a := fx.createIntakeIssue(t, "Bulk A")
	b := fx.createIntakeIssue(t, "Bulk B")
	c := fx.createIntakeIssue(t, "Bulk C")
	d := fx.createIntakeIssue(t, "Bulk D")
	e := fx.createIntakeIssue(t, "Bulk E")
	f := fx.createIntakeIssue(t, "Bulk F")

	rows := fx.intakeRowIDs(t)
	rowA, rowB, rowC, rowD, rowE, rowF := rows[a], rows[b], rows[c], rows[d], rows[e], rows[f]
	for name, id := range map[string]string{"a": rowA, "b": rowB, "c": rowC, "d": rowD, "e": rowE, "f": rowF} {
		if id == "" {
			t.Fatalf("missing intake row id for issue %s", name)
		}
	}

	// Pre-accept C through the per-item endpoint (already-accepted → skipped).
	rec := postAuthedJSON(t, fx.e, http.MethodPost, fx.intakeBase+"/issues/"+c+"/accept", fx.cookie, `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("pre-accept C: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	zero := "00000000-0000-0000-0000-000000000000"
	code, res := fx.bulkTriage(t, fx.cookie,
		fmt.Sprintf(`{"intake_ids":[%q,%q,%q,%q,"nope"],"action":"accept"}`, rowA, rowB, rowC, zero))
	if code != http.StatusOK {
		t.Fatalf("bulk accept: status = %d, want 200", code)
	}
	if !containsExactly(res.Accepted, rowA, rowB) {
		t.Errorf("accepted = %v, want [%s %s] in input order", res.Accepted, rowA, rowB)
	}
	if len(res.Rejected) != 0 {
		t.Errorf("rejected = %v, want empty", res.Rejected)
	}
	if !containsExactly(res.Skipped, rowC) {
		t.Errorf("skipped = %v, want [%s] (already accepted)", res.Skipped, rowC)
	}
	if len(res.Errors) != 2 {
		t.Fatalf("errors = %+v, want 2 (unknown uuid + malformed id)", res.Errors)
	}
	errByID := map[string]string{}
	for _, ie := range res.Errors {
		errByID[ie.IntakeID] = ie.Error
	}
	for _, id := range []string{zero, "nope"} {
		msg, ok := errByID[id]
		if !ok {
			t.Errorf("errors missing entry for intake id %q", id)
			continue
		}
		if msg == "" {
			t.Errorf("error for %q has empty message", id)
		}
	}

	// Inbox no longer lists the accepted rows.
	after := fx.intakeRowIDs(t)
	for _, id := range []string{a, b} {
		if _, ok := after[id]; ok {
			t.Errorf("issue %s still listed in inbox after bulk accept", id)
		}
	}
	if _, ok := after[d]; !ok {
		t.Errorf("untouched issue %s missing from inbox", d)
	}

	// Reject D, then reject again → skipped.
	code, res = fx.bulkTriage(t, fx.cookie, fmt.Sprintf(`{"intake_ids":[%q],"action":"reject"}`, rowD))
	if code != http.StatusOK {
		t.Fatalf("bulk reject: status = %d, want 200", code)
	}
	if !containsExactly(res.Rejected, rowD) {
		t.Errorf("rejected = %v, want [%s]", res.Rejected, rowD)
	}
	code, res = fx.bulkTriage(t, fx.cookie, fmt.Sprintf(`{"intake_ids":[%q],"action":"reject"}`, rowD))
	if code != http.StatusOK {
		t.Fatalf("bulk re-reject: status = %d, want 200", code)
	}
	if !containsExactly(res.Skipped, rowD) {
		t.Errorf("skipped = %v, want [%s] (already rejected)", res.Skipped, rowD)
	}

	// Accept E with an assignee → accepted, and the issue gets the assignee.
	var adminID string
	if err := fx.pool.QueryRow(context.Background(),
		`SELECT m.user_id::text FROM workspace_members m
		 JOIN workspaces w ON w.id = m.workspace_id
		 WHERE w.slug = $1 AND m.role = 20`, fx.slug).Scan(&adminID); err != nil {
		t.Fatalf("admin id: %v", err)
	}
	code, res = fx.bulkTriage(t, fx.cookie,
		fmt.Sprintf(`{"intake_ids":[%q],"action":"accept","assignee_id":%q}`, rowE, adminID))
	if code != http.StatusOK {
		t.Fatalf("bulk accept with assignee: status = %d, want 200", code)
	}
	if !containsExactly(res.Accepted, rowE) {
		t.Errorf("accepted = %v, want [%s]", res.Accepted, rowE)
	}
	var assigned int
	if err := fx.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM issue_assignees WHERE issue_id = $1::uuid AND user_id = $2::uuid`,
		e, adminID).Scan(&assigned); err != nil {
		t.Fatalf("assignee check: %v", err)
	}
	if assigned != 1 {
		t.Errorf("issue_assignees for E = %d, want 1 row for the admin", assigned)
	}

	// Non-member assignee → 400, whole call rejected, F stays pending.
	code, _ = fx.bulkTriage(t, fx.cookie,
		fmt.Sprintf(`{"intake_ids":[%q],"action":"accept","assignee_id":%q}`, rowF, zero))
	if code != http.StatusBadRequest {
		t.Errorf("bad assignee: status = %d, want 400", code)
	}
	if _, ok := fx.intakeRowIDs(t)[f]; !ok {
		t.Errorf("issue F left the inbox after a rejected bulk call")
	}

	// Reject with assignee_id is accepted: the assignee is ignored (the
	// issue is deleted on reject), not an error.
	g := fx.createIntakeIssue(t, "Bulk G")
	rowG := fx.intakeRowIDs(t)[g]
	code, res = fx.bulkTriage(t, fx.cookie,
		fmt.Sprintf(`{"intake_ids":[%q],"action":"reject","assignee_id":%q}`, rowG, adminID))
	if code != http.StatusOK {
		t.Fatalf("bulk reject with assignee: status = %d, want 200", code)
	}
	if !containsExactly(res.Rejected, rowG) {
		t.Errorf("rejected = %v, want [%s]", res.Rejected, rowG)
	}

	// Validation: empty intake_ids → 400, bad action → 400.
	for name, payload := range map[string]string{
		"empty ids":  `{"intake_ids":[],"action":"accept"}`,
		"missing":    `{"action":"accept"}`,
		"bad action": fmt.Sprintf(`{"intake_ids":[%q],"action":"archive"}`, rowF),
	} {
		if code, _ := fx.bulkTriage(t, fx.cookie, payload); code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, code)
		}
	}
}

func TestIntakeBulkTriageGuestForbidden(t *testing.T) {
	fx := setupIntakeBulk(t)
	a := fx.createIntakeIssue(t, "Guest A")
	rows := fx.intakeRowIDs(t)
	rowA := rows[a]
	if rowA == "" {
		t.Fatalf("missing intake row id for the guest fixture issue")
	}

	// Second user joins as guest (role 5): reads are fine, triage is not.
	guestEmail := uniqueEmail("intake-bulk-guest")
	guestCookie := loginTestUser(t, fx.e, fx.pool, guestEmail, "test-agent", uniqueIP())
	var adminID, guestID string
	ctx := context.Background()
	if err := fx.pool.QueryRow(ctx,
		`SELECT m.user_id::text FROM workspace_members m
		 JOIN workspaces w ON w.id = m.workspace_id
		 WHERE w.slug = $1 AND m.role = 20`, fx.slug).Scan(&adminID); err != nil {
		t.Fatalf("admin id: %v", err)
	}
	if err := fx.pool.QueryRow(ctx,
		`SELECT id::text FROM users WHERE email = $1`, guestEmail).Scan(&guestID); err != nil {
		t.Fatalf("guest id: %v", err)
	}
	if err := service.UpsertMember(ctx, fx.pool, fx.slug, adminID, guestID, service.RoleGuest); err != nil {
		t.Fatalf("UpsertMember guest: %v", err)
	}

	code, _ := fx.bulkTriage(t, guestCookie, fmt.Sprintf(`{"intake_ids":[%q],"action":"accept"}`, rowA))
	if code != http.StatusForbidden {
		t.Errorf("guest bulk triage: status = %d, want 403", code)
	}
	// The row is untouched.
	if _, ok := fx.intakeRowIDs(t)[a]; !ok {
		t.Errorf("guest bulk triage mutated the inbox")
	}
}
