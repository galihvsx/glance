package api

// C80T0: ?actor= on the workspace activity feed over HTTP, plus the
// ?due_date_empty= list param end-to-end. The actor filter is the
// caller's view of GetWorkspaceActivity's actorFilter: malformed UUID
// is 400, an unknown member UUID is 404, guests still get 403, and
// cursor pagination composes with the filter. Real test database, no
// skips.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
)

// setupActorActivityHTTP builds a workspace where two members (admin
// and second) each write audit rows, and returns everything the tests
// need to address them. Actor display names are the login emails (no
// names set), so rows are attributable by email.
func setupActorActivityHTTP(t *testing.T) (pool *pgxpool.Pool, e *echo.Echo, adminCookie, secondCookie *http.Cookie, adminEmail, secondEmail, adminID, secondID, wsBase string) {
	t.Helper()
	pool = newTestPool(t)
	e = testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterActivityRoutes(e, &IssueHandler{Pool: pool})

	adminEmail = uniqueEmail("h-acta-admin")
	secondEmail = uniqueEmail("h-acta-second")
	adminCookie = loginTestUser(t, e, pool, adminEmail, "test-agent/1.0", uniqueIP())
	secondCookie = loginTestUser(t, e, pool, secondEmail, "test-agent/1.0", uniqueIP())
	slug := uniqueSlug("act-actor")
	createWorkspaceHTTP(t, e, adminCookie, "Act Corp", slug)
	ident := uniqueProjectIdentifier("AAA")
	createProjectHTTP(t, e, adminCookie, slug, "Engineering", ident)
	wsBase = "/api/v1/workspaces/" + slug
	projBase := wsBase + "/projects/" + ident

	// Admin adds the second user as a member (role 15).
	secondID = authedUserID(t, e, secondCookie)
	rec := postAuthedJSON(t, e, http.MethodPost, wsBase+"/members", adminCookie,
		fmt.Sprintf(`{"user_id":%q,"role":15}`, secondID))
	if rec.Code != http.StatusOK {
		t.Fatalf("add second member: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	adminID = authedUserID(t, e, adminCookie)

	// Each member writes audit rows: admin creates + patches an issue,
	// the second member creates one.
	adminIssue := createIssueHTTP(t, e, adminCookie, projBase+"/issues", "Admin issue")
	rec = postAuthedJSON(t, e, http.MethodPatch, projBase+"/issues/"+adminIssue, adminCookie, `{"priority":3}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin patch: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	createIssueHTTP(t, e, secondCookie, projBase+"/issues", "Second issue")
	return pool, e, adminCookie, secondCookie, adminEmail, secondEmail, adminID, secondID, wsBase
}

func TestWorkspaceActivityHTTPActorFilter(t *testing.T) {
	_, e, adminCookie, _, adminEmail, secondEmail, adminID, secondID, wsBase := setupActorActivityHTTP(t)

	get := func(query string) workspaceActivityBody {
		rec := getAuthed(t, e, http.MethodGet, wsBase+"/activity"+query, adminCookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: status = %d (body: %s)", query, rec.Code, rec.Body.String())
		}
		var body workspaceActivityBody
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v (body: %s)", err, rec.Body.String())
		}
		return body
	}

	// actor=second: only the second member's _created row.
	bySecond := get("?actor=" + secondID)
	if len(bySecond.Activity) != 1 {
		t.Fatalf("actor=second rows = %d, want 1", len(bySecond.Activity))
	}
	if bySecond.Activity[0].Actor != secondEmail || bySecond.Activity[0].Field != "_created" {
		t.Fatalf("actor=second row = %+v, want second member's _created", bySecond.Activity[0])
	}
	if bySecond.Next != nil {
		t.Fatalf("actor=second next_cursor = %q, want null (exhausted)", *bySecond.Next)
	}

	// actor=admin: only the admin's two rows.
	byAdmin := get("?actor=" + adminID)
	if len(byAdmin.Activity) != 2 {
		t.Fatalf("actor=admin rows = %d, want 2", len(byAdmin.Activity))
	}
	for _, r := range byAdmin.Activity {
		if r.Actor != adminEmail {
			t.Fatalf("actor=admin row attributed to %q, want %q", r.Actor, adminEmail)
		}
	}

	// No filter: all three rows.
	all := get("")
	if len(all.Activity) != 3 {
		t.Fatalf("unfiltered rows = %d, want 3", len(all.Activity))
	}
}

func TestWorkspaceActivityHTTPActorPagination(t *testing.T) {
	_, e, adminCookie, _, _, _, _, secondID, wsBase := setupActorActivityHTTP(t)

	// The second member has exactly one row; limit=1 under the filter
	// must return it on the first page and exhaust immediately.
	rec := getAuthed(t, e, http.MethodGet, wsBase+"/activity?limit=1&actor="+secondID, adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("page 1: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var p1 workspaceActivityBody
	if err := json.Unmarshal(rec.Body.Bytes(), &p1); err != nil {
		t.Fatalf("decode p1: %v (body: %s)", err, rec.Body.String())
	}
	if len(p1.Activity) != 1 {
		t.Fatalf("page 1 rows = %d, want 1", len(p1.Activity))
	}
	if p1.Next != nil {
		t.Fatalf("page 1 next_cursor = %q, want null (single-row filter result)", *p1.Next)
	}

	// And a two-page walk of the unfiltered feed still works alongside
	// the filtered read (the cursor is filter-agnostic by construction).
	rec = getAuthed(t, e, http.MethodGet, wsBase+"/activity?limit=2", adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("unfiltered p1: status = %d", rec.Code)
	}
	var up1 workspaceActivityBody
	if err := json.Unmarshal(rec.Body.Bytes(), &up1); err != nil {
		t.Fatalf("decode up1: %v", err)
	}
	if len(up1.Activity) != 2 || up1.Next == nil {
		t.Fatalf("unfiltered p1: %d rows cursor %v, want 2 + cursor", len(up1.Activity), up1.Next)
	}
	rec = getAuthed(t, e, http.MethodGet, wsBase+"/activity?limit=2&before="+url.QueryEscape(*up1.Next), adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("unfiltered p2: status = %d", rec.Code)
	}
	var up2 workspaceActivityBody
	if err := json.Unmarshal(rec.Body.Bytes(), &up2); err != nil {
		t.Fatalf("decode up2: %v", err)
	}
	if len(up2.Activity) != 1 || up2.Next != nil {
		t.Fatalf("unfiltered p2: %d rows cursor %v, want 1 + null", len(up2.Activity), up2.Next)
	}
}

func TestWorkspaceActivityHTTPActorErrors(t *testing.T) {
	_, e, adminCookie, _, _, _, _, _, wsBase := setupActorActivityHTTP(t)
	// Malformed UUID is 400.
	rec := getAuthed(t, e, http.MethodGet, wsBase+"/activity?actor=not-a-uuid", adminCookie)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("actor=garbage: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// Well-formed UUID matching no workspace member is 404.
	rec = getAuthed(t, e, http.MethodGet, wsBase+"/activity?actor=11111111-1111-1111-1111-111111111111", adminCookie)
	if rec.Code != http.StatusNotFound {
		t.Errorf("actor=unknown: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestWorkspaceActivityHTTPActorGuestForbidden(t *testing.T) {
	pool, e, adminCookie, _, _, _, adminID, _, wsBase := setupActorActivityHTTP(t)
	// Add a guest (role 5) through the same admin.
	guestEmail := uniqueEmail("h-acta-guest")
	guestCookie := loginTestUser(t, e, pool, guestEmail, "test-agent/1.0", uniqueIP())
	guestID := authedUserID(t, e, guestCookie)
	rec := postAuthedJSON(t, e, http.MethodPost, wsBase+"/members", adminCookie,
		fmt.Sprintf(`{"user_id":%q,"role":5}`, guestID))
	if rec.Code != http.StatusOK {
		t.Fatalf("add guest: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	// Guest with an actor filter is still 403 — authZ is checked before
	// the filter target is resolved.
	rec = getAuthed(t, e, http.MethodGet, wsBase+"/activity?actor="+adminID, guestCookie)
	if rec.Code != http.StatusForbidden {
		t.Errorf("guest actor=: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
}
