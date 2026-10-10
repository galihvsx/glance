package api

// Backup admin endpoint tests (C15T2): GET /api/v1/admin/backups and
// POST /api/v1/admin/backups/run behind RequireAuth + RequireAdmin.
// Non-admins get 403 on both routes; unauthenticated requests get 401.
// Covers the list envelope + config, single-workspace trigger,
// all-workspace trigger, and unknown-workspace 404. Real test
// database, real temp backup dir — no skips.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"glance/internal/config"
	"glance/internal/service"
)

// testBackupServer builds the auth + admin routes with the backup dir
// pointed at a temp dir, so run-now writes real archives.
func testBackupServer(t *testing.T, pool *pgxpool.Pool, dir string) *echo.Echo {
	t.Helper()
	e := echo.New()
	h := &AuthHandler{
		Pool:   pool,
		Config: &config.Config{OTPPepper: "test-pepper-do-not-use-in-prod"},
	}
	RegisterAuthRoutes(e, h)
	RegisterAdminRoutes(e, &AdminHandler{
		Pool:      pool,
		BackupCfg: config.BackupConfig{Dir: dir, Retention: 7},
	})
	return e
}

func TestBackupRoutesUnauthenticated401(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testBackupServer(t, pool, t.TempDir())

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/admin/backups"},
		{http.MethodPost, "/api/v1/admin/backups/run"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: status = %d, want 401", tc.method, tc.path, rec.Code)
		}
	}
}

func TestBackupRoutesForbiddenForNonAdmin(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testBackupServer(t, pool, t.TempDir())
	cookie := loginTestUser(t, e, pool, uniqueEmail("backup-nonadmin"), "test-agent", uniqueIP())

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/admin/backups"},
		{http.MethodPost, "/api/v1/admin/backups/run"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s: status = %d, want 403", tc.method, tc.path, rec.Code)
		}
	}
}

func TestListBackupsEnvelopeAndConfig(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	dir := t.TempDir()
	e := testBackupServer(t, pool, dir)
	cookie := loginAdminUser(t, e, pool, uniqueEmail("backup-admin"))

	// The shared test DB accumulates backup_runs rows from other
	// tests, so scope every assertion to a workspace slug only this
	// test uses.
	ctx := context.Background()
	var userID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email) VALUES ($1) RETURNING id::text`,
		uniqueEmail("backup-list-owner")).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	slug := uniqueSlug("backuplistapi")
	if _, err := service.CreateWorkspace(ctx, pool, "Backup List WS", slug, userID); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	// Empty history for the fresh slug, but the config block is always
	// present.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/backups?workspace_slug="+slug, nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET backups: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items   []service.BackupRun `json:"items"`
		Total   int64               `json:"total"`
		Page    int                 `json:"page"`
		PerPage int                 `json:"per_page"`
		Config  struct {
			Enabled   bool   `json:"enabled"`
			Interval  string `json:"interval"`
			Dir       string `json:"dir"`
			Retention int    `json:"retention"`
		} `json:"config"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Page != 1 || body.PerPage != 25 || body.Total != 0 || len(body.Items) != 0 {
		t.Errorf("empty envelope = %+v, want page 1/25, total 0", body)
	}
	if body.Config.Enabled || body.Config.Interval != "" {
		t.Errorf("config.enabled = %v interval = %q, want disabled/empty", body.Config.Enabled, body.Config.Interval)
	}
	if body.Config.Dir != dir || body.Config.Retention != 7 {
		t.Errorf("config = %+v, want dir=%s retention=7", body.Config, dir)
	}

	// Trigger one backup for the slug, then the filtered list shows it.
	runReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/backups/run",
		strings.NewReader(`{"workspace_slug":"`+slug+`"}`))
	runReq.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	runReq.AddCookie(cookie)
	runRec := httptest.NewRecorder()
	e.ServeHTTP(runRec, runReq)
	if runRec.Code != http.StatusOK {
		t.Fatalf("POST run: status = %d", runRec.Code)
	}
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/admin/backups?workspace_slug="+slug, nil)
	req2.AddCookie(cookie)
	rec2 := httptest.NewRecorder()
	e.ServeHTTP(rec2, req2)
	var list struct {
		Items []service.BackupRun `json:"items"`
		Total int64               `json:"total"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if list.Total != 1 || len(list.Items) != 1 || list.Items[0].WorkspaceSlug != slug {
		t.Errorf("filtered list = total %d items %d, want the one run for %s",
			list.Total, len(list.Items), slug)
	}
}

func TestRunBackupNowSingleWorkspace(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	dir := t.TempDir()
	e := testBackupServer(t, pool, dir)
	cookie := loginAdminUser(t, e, pool, uniqueEmail("backup-admin-run"))

	ctx := context.Background()
	var userID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email) VALUES ($1) RETURNING id::text`,
		uniqueEmail("backup-ws-owner")).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	slug := uniqueSlug("backupapi")
	if _, err := service.CreateWorkspace(ctx, pool, "Backup API WS", slug, userID); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/backups/run",
		strings.NewReader(`{"workspace_slug":"`+slug+`"}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST run: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Backups []service.BackupRun `json:"backups"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Backups) != 1 {
		t.Fatalf("backups len = %d, want 1", len(body.Backups))
	}
	run := body.Backups[0]
	if run.WorkspaceSlug != slug || run.Status != service.BackupStatusOK {
		t.Errorf("run = %+v, want slug %s status ok", run, slug)
	}
	if run.VerifyOK == nil || !*run.VerifyOK {
		t.Errorf("verify_ok = %v, want true", run.VerifyOK)
	}

	// The history endpoint now lists the run (filtered to this test's
	// slug — the shared DB holds other tests' rows too).
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/admin/backups?workspace_slug="+slug, nil)
	req2.AddCookie(cookie)
	rec2 := httptest.NewRecorder()
	e.ServeHTTP(rec2, req2)
	var list struct {
		Items []service.BackupRun `json:"items"`
		Total int64               `json:"total"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if list.Total != 1 || len(list.Items) != 1 || list.Items[0].ID != run.ID {
		t.Errorf("list = total %d items %d, want the one run %s", list.Total, len(list.Items), run.ID)
	}
}

func TestRunBackupNowUnknownWorkspace404(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testBackupServer(t, pool, t.TempDir())
	cookie := loginAdminUser(t, e, pool, uniqueEmail("backup-admin-404"))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/backups/run",
		strings.NewReader(`{"workspace_slug":"no-such-workspace"}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("POST run unknown workspace: status = %d, want 404", rec.Code)
	}
}

func TestRunBackupNowAllWorkspaces(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	dir := t.TempDir()
	e := testBackupServer(t, pool, dir)
	cookie := loginAdminUser(t, e, pool, uniqueEmail("backup-admin-all"))

	ctx := context.Background()
	var userID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email) VALUES ($1) RETURNING id::text`,
		uniqueEmail("backup-all-owner")).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	slug := uniqueSlug("backupall")
	if _, err := service.CreateWorkspace(ctx, pool, "Backup All WS", slug, userID); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	// Empty body = all workspaces.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/backups/run", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST run (empty body): status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Backups []service.BackupRun `json:"backups"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	found := false
	for _, r := range body.Backups {
		if r.WorkspaceSlug == slug && r.Status == service.BackupStatusOK {
			found = true
		}
	}
	if !found {
		t.Errorf("workspace %s missing from all-workspace run: %+v", slug, body.Backups)
	}
}
