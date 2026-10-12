package api

// Project risk radar HTTP endpoint tests (C30T1): GET report shape +
// the pinned C30T2/C30T3 contract, role gating (any member incl. guest
// reads), tenancy (bad slug → 404, non-member → 404). Aggregation math
// itself is covered in the service tests; here we assert routing,
// auth, and the envelope. Real test database, no skips.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
)

type riskHTTPFixture struct {
	e        *echo.Echo
	pool     *pgxpool.Pool
	admin    *http.Cookie
	member   *http.Cookie
	guest    *http.Cookie
	outsider *http.Cookie
	slug     string
	ident    string
	base     string // .../workspaces/<slug>/risk
}

func setupRiskHTTP(t *testing.T) *riskHTTPFixture {
	t.Helper()
	fx := &riskHTTPFixture{}
	fx.pool = newTestPool(t)
	fx.e = testServer(t, fx.pool)
	RegisterWorkspaceRoutes(fx.e, &WorkspaceHandler{Pool: fx.pool})
	RegisterProjectRoutes(fx.e, &ProjectHandler{Pool: fx.pool})
	RegisterRiskRoutes(fx.e, &RiskHandler{Pool: fx.pool})

	fx.admin = loginTestUser(t, fx.e, fx.pool, uniqueEmail("rk-admin"), "ua", uniqueIP())
	fx.slug = uniqueSlug("rk-http")
	createWorkspaceHTTP(t, fx.e, fx.admin, "Risk Co", fx.slug)
	fx.ident = uniqueProjectIdentifier("RK")
	createProjectHTTP(t, fx.e, fx.admin, fx.slug, "Atlas", fx.ident)
	fx.base = "/api/v1/workspaces/" + fx.slug + "/risk"

	addRole := func(email string, role int) *http.Cookie {
		cookie := loginTestUser(t, fx.e, fx.pool, uniqueEmail(email), "ua", uniqueIP())
		id := authedUserID(t, fx.e, cookie)
		rec := postAuthedJSON(t, fx.e, http.MethodPost,
			"/api/v1/workspaces/"+fx.slug+"/members", fx.admin,
			fmt.Sprintf(`{"user_id":%q,"role":%d}`, id, role))
		if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
			t.Fatalf("upsertMember role %d: status = %d (body: %s)", role, rec.Code, rec.Body.String())
		}
		return cookie
	}
	fx.member = addRole("rk-member", 15)
	fx.guest = addRole("rk-guest", 5)
	fx.outsider = loginTestUser(t, fx.e, fx.pool, uniqueEmail("rk-out"), "ua", uniqueIP())
	return fx
}

type riskReportJSON struct {
	Projects []struct {
		ProjectID  string `json:"project_id"`
		Identifier string `json:"identifier"`
		Name       string `json:"name"`
		OpenCount  int64  `json:"open_count"`
		RiskScore  int    `json:"risk_score"`
		RiskLevel  string `json:"risk_level"`
		Factors    []struct {
			Key    string  `json:"key"`
			Label  string  `json:"label"`
			Value  float64 `json:"value"`
			Detail string  `json:"detail"`
		} `json:"factors"`
		GeneratedAt string `json:"generated_at"`
	} `json:"projects"`
	GeneratedAt string `json:"generated_at"`
}

func getRisk(t *testing.T, fx *riskHTTPFixture, path string, cookie *http.Cookie) (int, riskReportJSON) {
	t.Helper()
	rec := getAuthed(t, fx.e, http.MethodGet, path, cookie)
	var rep riskReportJSON
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
			t.Fatalf("decode risk: %v", err)
		}
	}
	return rec.Code, rep
}

// TestGetRiskContract: 200 with the pinned contract shape — every role
// at member 5+ may read.
func TestGetRiskContract(t *testing.T) {
	fx := setupRiskHTTP(t)
	for name, cookie := range map[string]*http.Cookie{
		"admin": fx.admin, "member": fx.member, "guest": fx.guest,
	} {
		code, rep := getRisk(t, fx, fx.base, cookie)
		if code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200", name, code)
		}
		if rep.GeneratedAt == "" {
			t.Fatalf("%s: generated_at missing", name)
		}
		if len(rep.Projects) != 1 {
			t.Fatalf("%s: projects = %d, want 1", name, len(rep.Projects))
		}
		p := rep.Projects[0]
		if p.ProjectID == "" || p.Identifier != fx.ident || p.Name != "Atlas" {
			t.Fatalf("%s: project identity = %+v", name, p)
		}
		if p.OpenCount != 0 || p.RiskScore != 0 || p.RiskLevel != "healthy" {
			t.Fatalf("%s: empty project must be 0/healthy, got %+v", name, p)
		}
		if p.Factors == nil {
			t.Fatalf("%s: factors must be a non-nil empty array", name)
		}
		if p.GeneratedAt == "" {
			t.Fatalf("%s: per-project generated_at missing", name)
		}
	}
}

// TestGetRiskTenancy: a bad slug and a non-member caller both get 404
// (never a leak).
func TestGetRiskTenancy(t *testing.T) {
	fx := setupRiskHTTP(t)

	code, _ := getRisk(t, fx, "/api/v1/workspaces/no-such-ws/risk", fx.admin)
	if code != http.StatusNotFound {
		t.Fatalf("bad slug: status = %d, want 404", code)
	}

	code, _ = getRisk(t, fx, fx.base, fx.outsider)
	if code != http.StatusNotFound {
		t.Fatalf("outsider: status = %d, want 404", code)
	}
}
