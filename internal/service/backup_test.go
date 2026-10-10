package service

// Scheduled backup tests (C15T2): backup run round-trip (archive +
// manifest + row + auto-verify), unknown workspace, tamper detection,
// retention pruning, all-workspace pass, and failure recording. Real
// test database, real temp dirs — no skips.

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRunBackupRoundTrip(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	creator := createTestUser(t, pool, uniqueTestEmail("backup"))
	wsSlug := uniqueTestSlug("backupws")
	createTestWorkspace(t, pool, "Backup WS", wsSlug, creator)
	proj, err := CreateProject(ctx, pool, wsSlug, creator, "Backup Project", "BP")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := CreateIssue(ctx, pool, wsSlug, proj.Identifier, creator, CreateIssueInput{
		Name: "backed up issue",
	}); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	dir := t.TempDir()
	run, err := RunBackup(ctx, pool, dir, 7, wsSlug, "admin@example.com")
	if err != nil {
		t.Fatalf("RunBackup: %v", err)
	}
	if run.Status != BackupStatusOK {
		t.Fatalf("status = %q, want ok", run.Status)
	}
	if run.WorkspaceSlug != wsSlug {
		t.Errorf("workspace_slug = %q, want %q", run.WorkspaceSlug, wsSlug)
	}
	if run.ByteSize <= 0 {
		t.Errorf("byte_size = %d, want > 0", run.ByteSize)
	}
	if len(run.SHA256) != 64 {
		t.Errorf("sha256 = %q, want 64 hex chars", run.SHA256)
	}
	if run.FormatVersion != ExportFormatVersion {
		t.Errorf("format_version = %q, want %q", run.FormatVersion, ExportFormatVersion)
	}
	if run.MigrationVersion == "" {
		t.Error("migration_version empty, want the applied migration max")
	}
	if run.TriggeredBy != "admin@example.com" {
		t.Errorf("triggered_by = %q", run.TriggeredBy)
	}

	// Archive file on disk, named as documented.
	if run.FilePath == nil {
		t.Fatal("file_path nil")
	}
	data, err := os.ReadFile(*run.FilePath)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	if int64(len(data)) != run.ByteSize {
		t.Errorf("file size %d != recorded %d", len(data), run.ByteSize)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != run.SHA256 {
		t.Error("sha256(file) != recorded sha256")
	}

	// The archive really is the glance-export/1 format: gunzip + header.
	gr, err := gzip.NewReader(openFile(t, *run.FilePath))
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	var hdr struct {
		Format    string `json:"format"`
		Workspace struct {
			Slug string `json:"slug"`
		} `json:"workspace"`
		Issues []struct {
			Name string `json:"name"`
		} `json:"issues"`
	}
	if err := json.NewDecoder(gr).Decode(&hdr); err != nil {
		t.Fatalf("decode archive: %v", err)
	}
	_ = gr.Close()
	if hdr.Format != ExportFormatVersion {
		t.Errorf("archive format = %q, want %q", hdr.Format, ExportFormatVersion)
	}
	if hdr.Workspace.Slug != wsSlug {
		t.Errorf("archive workspace slug = %q, want %q", hdr.Workspace.Slug, wsSlug)
	}
	if len(hdr.Issues) != 1 || hdr.Issues[0].Name != "backed up issue" {
		t.Errorf("archive issues = %+v, want the one test issue", hdr.Issues)
	}

	// Manifest sidecar: JSON round-trip with matching fields.
	m, err := readBackupManifest(dir, run.FileName)
	if err != nil {
		t.Fatalf("readBackupManifest: %v", err)
	}
	if m.Format != BackupManifestVersion {
		t.Errorf("manifest format = %q", m.Format)
	}
	if m.WorkspaceSlug != wsSlug || m.SHA256 != run.SHA256 ||
		m.ByteSize != run.ByteSize || m.ArchiveFormat != ExportFormatVersion ||
		m.MigrationVersion != run.MigrationVersion || m.File != run.FileName {
		t.Errorf("manifest mismatch: %+v vs run %+v", m, run)
	}

	// Auto-verification ran and passed.
	if run.VerifyOK == nil || !*run.VerifyOK {
		t.Errorf("verify_ok = %v, want true (verify_error=%v)", run.VerifyOK, backupStrOrNil(run.VerifyError))
	}
	if err := VerifyBackup(ctx, pool, run.ID); err != nil {
		t.Errorf("re-verify: %v", err)
	}
}

func TestRunBackupUnknownWorkspace(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	_, err := RunBackup(ctx, pool, t.TempDir(), 7, "no-such-workspace-xyz", "schedule")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestVerifyBackupDetectsTamper(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	creator := createTestUser(t, pool, uniqueTestEmail("backuptamper"))
	wsSlug := uniqueTestSlug("backuptamper")
	createTestWorkspace(t, pool, "Tamper WS", wsSlug, creator)

	dir := t.TempDir()
	run, err := RunBackup(ctx, pool, dir, 7, wsSlug, "schedule")
	if err != nil {
		t.Fatalf("RunBackup: %v", err)
	}

	// Corrupt the stored bytes: sha256 recompute must fail.
	f, err := os.OpenFile(*run.FilePath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open for tamper: %v", err)
	}
	if _, err := f.WriteString("tampered"); err != nil {
		t.Fatalf("tamper write: %v", err)
	}
	_ = f.Close()

	if err := VerifyBackup(ctx, pool, run.ID); err == nil {
		t.Fatal("VerifyBackup on tampered archive: want error, got nil")
	}
	after, err := getBackupRun(ctx, pool, run.ID)
	if err != nil {
		t.Fatalf("getBackupRun: %v", err)
	}
	if after.VerifyOK == nil || *after.VerifyOK {
		t.Error("verify_ok still true after tamper")
	}
	if after.VerifyError == nil || *after.VerifyError == "" {
		t.Error("verify_error empty after tamper, want the reason")
	}
}

func TestPruneBackupsRetention(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	creator := createTestUser(t, pool, uniqueTestEmail("backupprune"))
	wsSlug := uniqueTestSlug("backupprune")
	createTestWorkspace(t, pool, "Prune WS", wsSlug, creator)

	dir := t.TempDir()
	var runs []BackupRun
	for i := 0; i < 3; i++ {
		// retention=1: every new backup prunes the previous one.
		run, err := RunBackup(ctx, pool, dir, 1, wsSlug, "schedule")
		if err != nil {
			t.Fatalf("RunBackup %d: %v", i, err)
		}
		runs = append(runs, run)
	}

	for i, run := range runs[:2] {
		after, err := getBackupRun(ctx, pool, run.ID)
		if err != nil {
			t.Fatalf("getBackupRun: %v", err)
		}
		if after.Status != BackupStatusPruned {
			t.Errorf("run %d status = %q, want pruned", i, after.Status)
		}
		if after.FilePath != nil {
			t.Errorf("run %d file_path = %q, want NULL after prune", i, *after.FilePath)
		}
		if _, err := os.Stat(filepath.Join(dir, run.FileName)); !os.IsNotExist(err) {
			t.Errorf("run %d archive still on disk after prune", i)
		}
		if _, err := os.Stat(filepath.Join(dir, run.FileName+".manifest.json")); !os.IsNotExist(err) {
			t.Errorf("run %d manifest still on disk after prune", i)
		}
	}
	latest, err := getBackupRun(ctx, pool, runs[2].ID)
	if err != nil {
		t.Fatalf("getBackupRun: %v", err)
	}
	if latest.Status != BackupStatusOK || latest.FilePath == nil {
		t.Errorf("latest run = %+v, want ok with file", latest)
	}

	// Retention is per workspace: another workspace's runs are untouched.
	other := createTestUser(t, pool, uniqueTestEmail("backupprune2"))
	otherSlug := uniqueTestSlug("backuppruneother")
	createTestWorkspace(t, pool, "Other WS", otherSlug, other)
	otherRun, err := RunBackup(ctx, pool, dir, 1, otherSlug, "schedule")
	if err != nil {
		t.Fatalf("RunBackup other: %v", err)
	}
	if otherRun.Status != BackupStatusOK {
		t.Errorf("other workspace run status = %q, want ok (retention is per workspace)", otherRun.Status)
	}
}

func TestRunAllBackups(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	var slugs []string
	for i, prefix := range []string{"backupall-a", "backupall-b"} {
		creator := createTestUser(t, pool, uniqueTestEmail(prefix))
		slug := uniqueTestSlug(prefix)
		createTestWorkspace(t, pool, "All WS", slug, creator)
		slugs = append(slugs, slug)
		_ = i
	}

	dir := t.TempDir()
	runs, err := RunAllBackups(ctx, pool, dir, 7, "schedule")
	if err != nil {
		t.Fatalf("RunAllBackups: %v", err)
	}
	seen := map[string]bool{}
	for _, r := range runs {
		seen[r.WorkspaceSlug] = true
		if r.Status != BackupStatusOK {
			t.Errorf("run for %s status = %q", r.WorkspaceSlug, r.Status)
		}
		if r.TriggeredBy != "schedule" {
			t.Errorf("run for %s triggered_by = %q, want schedule", r.WorkspaceSlug, r.TriggeredBy)
		}
	}
	for _, s := range slugs {
		if !seen[s] {
			t.Errorf("workspace %s missing from RunAllBackups result", s)
		}
	}
}

func TestRunBackupFailureRecorded(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	creator := createTestUser(t, pool, uniqueTestEmail("backupfail"))
	wsSlug := uniqueTestSlug("backupfail")
	createTestWorkspace(t, pool, "Fail WS", wsSlug, creator)

	// A regular file as the "dir": MkdirAll fails → failed row + error.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	run, err := RunBackup(ctx, pool, blocker, 7, wsSlug, "schedule")
	if err == nil {
		t.Fatal("RunBackup into a file-as-dir: want error, got nil")
	}
	if run.Status != BackupStatusFailed {
		t.Errorf("status = %q, want failed", run.Status)
	}
	if run.VerifyOK == nil || *run.VerifyOK || run.VerifyError == nil {
		t.Errorf("failed run should carry verify_ok=false + reason, got %+v", run)
	}
	// The failed run is listed in history.
	runs, total, err := ListBackups(ctx, pool, 25, 0, wsSlug)
	if err != nil {
		t.Fatalf("ListBackups: %v", err)
	}
	if total != 1 || len(runs) != 1 || runs[0].Status != BackupStatusFailed {
		t.Errorf("history = total %d runs %+v, want the one failed run", total, runs)
	}
}

func TestListBackupsPagination(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	creator := createTestUser(t, pool, uniqueTestEmail("backuplist"))
	wsSlug := uniqueTestSlug("backuplist")
	createTestWorkspace(t, pool, "List WS", wsSlug, creator)

	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		// retention=0: keep forever, so all 3 rows survive.
		if _, err := RunBackup(ctx, pool, dir, 0, wsSlug, "schedule"); err != nil {
			t.Fatalf("RunBackup %d: %v", i, err)
		}
	}
	page1, total, err := ListBackups(ctx, pool, 2, 0, wsSlug)
	if err != nil {
		t.Fatalf("ListBackups: %v", err)
	}
	if total != 3 || len(page1) != 2 {
		t.Fatalf("page1: total=%d len=%d, want 3/2", total, len(page1))
	}
	page2, _, err := ListBackups(ctx, pool, 2, 2, wsSlug)
	if err != nil {
		t.Fatalf("ListBackups page2: %v", err)
	}
	if len(page2) != 1 {
		t.Fatalf("page2 len=%d, want 1", len(page2))
	}
	if !page1[0].At.After(page1[1].At) && !page1[0].At.Equal(page1[1].At) {
		t.Error("runs not newest-first")
	}
	// Unfiltered list includes the rows too.
	_, totalAll, err := ListBackups(ctx, pool, 25, 0, "")
	if err != nil {
		t.Fatalf("ListBackups unfiltered: %v", err)
	}
	if totalAll < 3 {
		t.Errorf("unfiltered total=%d, want >= 3", totalAll)
	}
}

// openFile is a tiny helper: os.Open with a fatal on error.
func openFile(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	return f
}

func backupStrOrNil(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}
