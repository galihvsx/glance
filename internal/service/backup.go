package service

// Scheduled workspace backups (C15T2).
//
// The backup job produces one gzipped glance-export/1 archive per
// workspace on local disk — the SAME archive format and the SAME
// streaming code path as the interactive export (cycle 10) and the
// archive importer (cycle 11). No new archive format: a backup file is
// exactly what POST /api/v1/workspaces/{slug}/export would stream,
// passed through gzip. Restore = gunzip + POST the JSON to the archive
// importer (or replay via ImportWorkspaceArchive directly).
//
// One backup run = one row in backup_runs (migration 000041) plus two
// files in the backup dir:
//
//	glance-<slug>-backup-<UTC ts>-<rand>.json.gz            the archive
//	glance-<slug>-backup-<UTC ts>-<rand>.json.gz.manifest.json
//	                                                      the manifest sidecar
//
// The manifest sidecar keeps the backup directory self-describing even
// if the database (and with it the backup_runs rows) is lost: an
// operator can list the directory and verify/restore any archive from
// the manifest alone.
//
// Integrity model: sha256 is computed over the STORED (gzipped) bytes,
// recorded in both the row and the manifest. VerifyBackup recomputes
// it, then streaming-decodes the archive header and checks
// format == ExportFormatVersion and workspace.slug == the row's slug.
// That is the brief's "at minimum verifies archive integrity +
// manifest checksum" — a full dry-run import against a scratch database
// is deliberately NOT done: it would need CREATE DATABASE privileges
// and a second migrated schema on every self-hosted instance, which is
// an ops burden the minimum check avoids while still catching
// corruption, truncation, and slug mix-ups.
//
// The scheduled pass (internal/ticker BackupTicker) backs up EVERY
// workspace on GLANCE_BACKUP_INTERVAL; an empty interval disables the
// schedule (default) and backups only happen via
// POST /api/v1/admin/backups/run. The job runs as the instance
// operator: streamWorkspaceBackup skips the per-workspace actor check
// the interactive export performs — there is no membership to resolve
// for a server-side job.
//
// Retention (GLANCE_BACKUP_RETENTION, default 7, 0 = keep forever):
// after each successful backup the oldest runs beyond the newest N per
// workspace are pruned — archive + manifest deleted from disk, the row
// kept with status 'pruned' and file_path NULL so history stays
// queryable.

import (
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Backup run statuses: the operator-facing contract stored in
// backup_runs.status.
const (
	BackupStatusOK     = "ok"
	BackupStatusFailed = "failed"
	BackupStatusPruned = "pruned"
)

// BackupManifestVersion pins the sidecar manifest schema.
const BackupManifestVersion = "glance-backup-manifest/1"

// BackupRun is one row of backup_runs: the queryable history behind
// GET /api/v1/admin/backups.
type BackupRun struct {
	ID               string     `json:"id"`
	WorkspaceID      *string    `json:"workspace_id"`
	WorkspaceSlug    string     `json:"workspace_slug"`
	At               time.Time  `json:"at"`
	FileName         string     `json:"file_name"`
	FilePath         *string    `json:"file_path"`
	ByteSize         int64      `json:"byte_size"`
	SHA256           string     `json:"sha256"`
	FormatVersion    string     `json:"format_version"`
	MigrationVersion string     `json:"migration_version"`
	Status           string     `json:"status"`
	VerifyOK         *bool      `json:"verify_ok"`
	VerifyError      *string    `json:"verify_error"`
	VerifiedAt       *time.Time `json:"verified_at"`
	TriggeredBy      string     `json:"triggered_by"`
}

// BackupManifest is the sidecar JSON written next to every archive:
// the same integrity metadata as the row, readable without the DB.
type BackupManifest struct {
	Format           string `json:"format"`
	WorkspaceSlug    string `json:"workspace_slug"`
	At               string `json:"at"`
	File             string `json:"file"`
	ByteSize         int64  `json:"byte_size"`
	SHA256           string `json:"sha256"`
	ArchiveFormat    string `json:"archive_format"`
	MigrationVersion string `json:"migration_version"`
}

// ErrBackupNotFound is returned when a backup run id names no row.
var ErrBackupNotFound = errors.New("service: backup run not found")

// scanBackupRun scans a full backup_runs row in column order.
func scanBackupRun(row interface {
	Scan(dest ...any) error
}) (BackupRun, error) {
	var b BackupRun
	err := row.Scan(
		&b.ID, &b.WorkspaceID, &b.WorkspaceSlug, &b.At, &b.FileName,
		&b.FilePath, &b.ByteSize, &b.SHA256, &b.FormatVersion,
		&b.MigrationVersion, &b.Status, &b.VerifyOK, &b.VerifyError,
		&b.VerifiedAt, &b.TriggeredBy,
	)
	return b, err
}

const backupRunColumns = `id::text, workspace_id::text, workspace_slug, at,
	file_name, file_path, byte_size, sha256, format_version,
	migration_version, status, verify_ok, verify_error, verified_at,
	triggered_by`

// currentMigrationVersion returns the newest applied migration version
// (e.g. "000041") for the manifest. Versions are zero-padded, so the
// TEXT max is the newest. A backup taken against an unknown schema
// state is worse than no version at all — empty is an error here.
func currentMigrationVersion(ctx context.Context, pool *pgxpool.Pool) (string, error) {
	var v *string
	if err := pool.QueryRow(ctx,
		`SELECT max(version) FROM schema_migrations`).Scan(&v); err != nil {
		return "", err
	}
	if v == nil || *v == "" {
		return "", errors.New("service: no applied migrations found")
	}
	return *v, nil
}

// backupFileName builds the archive file name. The slug is filename-safe
// by the slug contract; the random suffix keeps two runs in the same
// second from colliding.
func backupFileName(slug string, at time.Time) (string, error) {
	var rnd [3]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("glance-%s-backup-%s-%s.json.gz",
		slug, at.UTC().Format("20060102T150405"), hex.EncodeToString(rnd[:])), nil
}

// RunBackup backs up one workspace: streams the glance-export/1 archive
// (gzipped) to dir, writes the manifest sidecar, records the run, runs
// the integrity verification, and prunes retention. On export failure
// the run is still recorded with status 'failed' and the error is
// returned — a broken schedule must be visible in the admin UI, never
// silent. triggeredBy is "schedule" for the ticker or the admin's email
// for a manual run.
func RunBackup(ctx context.Context, pool *pgxpool.Pool, dir string, retention int, workspaceSlug, triggeredBy string) (BackupRun, error) {
	var wsID string
	var slug string
	if err := pool.QueryRow(ctx,
		`SELECT id::text, slug::text FROM workspaces WHERE slug = $1`,
		workspaceSlug).Scan(&wsID, &slug); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return BackupRun{}, ErrNotFound
		}
		return BackupRun{}, err
	}
	return runBackupForWorkspace(ctx, pool, dir, retention, wsID, slug, triggeredBy)
}

// runBackupForWorkspace is RunBackup after workspace resolution.
func runBackupForWorkspace(ctx context.Context, pool *pgxpool.Pool, dir string, retention int, wsID, slug, triggeredBy string) (BackupRun, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return recordBackupFailure(ctx, pool, wsID, slug, triggeredBy, err)
	}
	migVersion, err := currentMigrationVersion(ctx, pool)
	if err != nil {
		return recordBackupFailure(ctx, pool, wsID, slug, triggeredBy, err)
	}
	at := time.Now().UTC()
	fileName, err := backupFileName(slug, at)
	if err != nil {
		return recordBackupFailure(ctx, pool, wsID, slug, triggeredBy, err)
	}
	finalPath := filepath.Join(dir, fileName)

	// Stream to a temp file first: a failed/cancelled export must never
	// leave a half-written archive under its final name.
	tmp, err := os.CreateTemp(dir, ".backup-*.tmp")
	if err != nil {
		return recordBackupFailure(ctx, pool, wsID, slug, triggeredBy, err)
	}
	tmpName := tmp.Name()
	// Any exit before the rename removes the temp file; after a
	// successful rename the flag is cleared.
	removeTmp := true
	defer func() {
		if removeTmp {
			_ = os.Remove(tmpName)
		}
	}()

	h := sha256.New()
	gz := gzip.NewWriter(io.MultiWriter(h, tmp))
	streamErr := streamWorkspaceBackup(ctx, pool, wsID, gz)
	// Close the gzip writer even on stream failure so buffered bytes
	// flush before we stat/remove the temp file.
	if cerr := gz.Close(); cerr != nil && streamErr == nil {
		streamErr = cerr
	}
	if cerr := tmp.Close(); cerr != nil && streamErr == nil {
		streamErr = cerr
	}
	if streamErr != nil {
		return recordBackupFailure(ctx, pool, wsID, slug, triggeredBy, streamErr)
	}

	sum := hex.EncodeToString(h.Sum(nil))
	var size int64
	if fi, err := os.Stat(tmpName); err != nil {
		return recordBackupFailure(ctx, pool, wsID, slug, triggeredBy, err)
	} else {
		size = fi.Size()
	}
	if err := os.Rename(tmpName, finalPath); err != nil {
		return recordBackupFailure(ctx, pool, wsID, slug, triggeredBy, err)
	}
	removeTmp = false

	manifest := BackupManifest{
		Format:           BackupManifestVersion,
		WorkspaceSlug:    slug,
		At:               at.Format(time.RFC3339),
		File:             fileName,
		ByteSize:         size,
		SHA256:           sum,
		ArchiveFormat:    ExportFormatVersion,
		MigrationVersion: migVersion,
	}
	if err := writeBackupManifest(dir, fileName, manifest); err != nil {
		// The archive itself is intact; record the run as ok but note
		// the manifest failure in the verify fields so it is visible.
		run, rerr := insertBackupRun(ctx, pool, wsID, slug, at, fileName, finalPath, size, sum, migVersion, BackupStatusOK, triggeredBy)
		if rerr != nil {
			return run, rerr
		}
		msg := "manifest write failed: " + err.Error()
		_ = updateBackupVerify(ctx, pool, run.ID, false, msg)
		run.VerifyOK = boolPtr(false)
		run.VerifyError = &msg
		_ = pruneBackups(ctx, pool, dir, slug, retention, run.ID)
		return run, nil
	}

	run, err := insertBackupRun(ctx, pool, wsID, slug, at, fileName, finalPath, size, sum, migVersion, BackupStatusOK, triggeredBy)
	if err != nil {
		return run, err
	}
	// Automatic integrity verification: sha256 recompute + archive
	// header check. A verification failure does NOT delete the archive
	// (the operator decides what to do with it), but it IS loud: the
	// row carries verify_ok=false + the reason, and RunBackup returns
	// the error. A backup that does not verify is not a backup.
	verr := VerifyBackup(ctx, pool, run.ID)
	run, rerr := getBackupRun(ctx, pool, run.ID)
	if rerr != nil {
		return run, rerr
	}
	if verr != nil {
		return run, fmt.Errorf("service: backup written but verification failed: %w", verr)
	}
	if err := pruneBackups(ctx, pool, dir, slug, retention, run.ID); err != nil {
		// Prune failures are reported but the backup itself succeeded.
		return run, fmt.Errorf("service: backup ok but retention prune failed: %w", err)
	}
	return run, nil
}

// recordBackupFailure inserts a 'failed' run row so a broken schedule
// is visible in the admin history, then returns the row and the error.
func recordBackupFailure(ctx context.Context, pool *pgxpool.Pool, wsID, slug, triggeredBy string, cause error) (BackupRun, error) {
	run, rerr := insertBackupRun(ctx, pool, wsID, slug, time.Now().UTC(), "",
		"", 0, "", "", BackupStatusFailed, triggeredBy)
	if rerr != nil {
		return BackupRun{}, fmt.Errorf("service: backup failed (%v) and failure could not be recorded: %w", cause, rerr)
	}
	msg := cause.Error()
	_ = updateBackupVerify(ctx, pool, run.ID, false, msg)
	run.VerifyOK = boolPtr(false)
	run.VerifyError = &msg
	return run, cause
}

// writeBackupManifest writes the sidecar manifest JSON next to the
// archive. The manifest name is deterministic from the archive name,
// so a directory listing pairs them without the database.
func writeBackupManifest(dir, fileName string, m BackupManifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	// 0644: manifests are non-sensitive integrity metadata; an operator
	// restoring from disk should be able to read them.
	return os.WriteFile(filepath.Join(dir, fileName+".manifest.json"), append(data, '\n'), 0o644)
}

// readBackupManifest reads a sidecar manifest back (manifest
// round-trip is covered by tests).
func readBackupManifest(dir, fileName string) (BackupManifest, error) {
	var m BackupManifest
	data, err := os.ReadFile(filepath.Join(dir, fileName+".manifest.json"))
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, err
	}
	return m, nil
}

func insertBackupRun(ctx context.Context, pool *pgxpool.Pool, wsID, slug string, at time.Time, fileName, filePath string, size int64, sum, migVersion, status, triggeredBy string) (BackupRun, error) {
	var filePathArg *string
	if filePath != "" {
		filePathArg = &filePath
	}
	var b BackupRun
	err := pool.QueryRow(ctx,
		`INSERT INTO backup_runs
		 (workspace_id, workspace_slug, at, file_name, file_path, byte_size,
		  sha256, format_version, migration_version, status, triggered_by)
		 VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		 RETURNING `+backupRunColumns,
		wsID, slug, at, fileName, filePathArg, size, sum,
		ExportFormatVersion, migVersion, status, triggeredBy).Scan(
		&b.ID, &b.WorkspaceID, &b.WorkspaceSlug, &b.At, &b.FileName,
		&b.FilePath, &b.ByteSize, &b.SHA256, &b.FormatVersion,
		&b.MigrationVersion, &b.Status, &b.VerifyOK, &b.VerifyError,
		&b.VerifiedAt, &b.TriggeredBy,
	)
	return b, err
}

func getBackupRun(ctx context.Context, pool *pgxpool.Pool, id string) (BackupRun, error) {
	b, err := scanBackupRun(pool.QueryRow(ctx,
		`SELECT `+backupRunColumns+` FROM backup_runs WHERE id = $1::uuid`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return BackupRun{}, ErrBackupNotFound
		}
		return BackupRun{}, err
	}
	return b, nil
}

func updateBackupVerify(ctx context.Context, pool *pgxpool.Pool, id string, ok bool, verifyErr string) error {
	var errArg *string
	if verifyErr != "" {
		errArg = &verifyErr
	}
	_, err := pool.Exec(ctx,
		`UPDATE backup_runs SET verify_ok = $2, verify_error = $3, verified_at = now()
		 WHERE id = $1::uuid`, id, ok, errArg)
	return err
}

// VerifyBackup re-checks a recorded backup: recomputes sha256 over the
// stored bytes and compares it to the row (bit-level integrity), then
// streaming-decodes the archive header and checks the format version
// pin and the workspace slug (structural integrity — catches
// truncation, corruption, and slug mix-ups). The result is persisted
// on the row; a nil return means the backup verified clean.
func VerifyBackup(ctx context.Context, pool *pgxpool.Pool, id string) error {
	run, err := getBackupRun(ctx, pool, id)
	if err != nil {
		return err
	}
	fail := func(msg string) error {
		_ = updateBackupVerify(ctx, pool, id, false, msg)
		return errors.New("service: backup verification failed: " + msg)
	}
	if run.FilePath == nil || *run.FilePath == "" {
		return fail("archive file is gone (pruned or never written)")
	}
	f, err := os.Open(*run.FilePath)
	if err != nil {
		return fail("cannot open archive: " + err.Error())
	}
	defer f.Close()

	h := sha256.New()
	gr, err := gzip.NewReader(io.TeeReader(f, h))
	if err != nil {
		return fail("not a valid gzip archive: " + err.Error())
	}
	// Streaming header decode: the decoder discards the (potentially
	// huge) sections as it goes; memory stays flat. A full parse also
	// catches truncated JSON — a stronger check than peeking at tokens.
	var hdr struct {
		Format    string `json:"format"`
		Workspace struct {
			Slug string `json:"slug"`
		} `json:"workspace"`
	}
	if err := json.NewDecoder(gr).Decode(&hdr); err != nil {
		_ = gr.Close()
		return fail("archive JSON does not decode: " + err.Error())
	}
	if err := gr.Close(); err != nil {
		return fail("gzip stream corrupt: " + err.Error())
	}
	sum := hex.EncodeToString(h.Sum(nil))
	switch {
	case sum != run.SHA256:
		return fail(fmt.Sprintf("sha256 mismatch: file %s... != manifest %s...",
			shortHash(sum), shortHash(run.SHA256)))
	case hdr.Format != ExportFormatVersion:
		return fail(fmt.Sprintf("archive format %q, want %q", hdr.Format, ExportFormatVersion))
	case hdr.Format != run.FormatVersion:
		return fail(fmt.Sprintf("archive format %q does not match recorded %q", hdr.Format, run.FormatVersion))
	case hdr.Workspace.Slug != run.WorkspaceSlug:
		return fail(fmt.Sprintf("archive workspace %q does not match recorded %q",
			hdr.Workspace.Slug, run.WorkspaceSlug))
	}
	if err := updateBackupVerify(ctx, pool, id, true, ""); err != nil {
		return err
	}
	return nil
}

func shortHash(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// ListBackups returns backup runs newest-first, paginated, optionally
// filtered to one workspace slug.
func ListBackups(ctx context.Context, pool *pgxpool.Pool, limit, offset int, workspaceSlug string) ([]BackupRun, int64, error) {
	var where string
	var args []any
	if workspaceSlug != "" {
		where = `WHERE workspace_slug = $1`
		args = append(args, workspaceSlug)
	}
	var total int64
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM backup_runs `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := pool.Query(ctx,
		`SELECT `+backupRunColumns+` FROM backup_runs `+where+`
		 ORDER BY at DESC LIMIT $`+itoa(len(args)+1)+` OFFSET $`+itoa(len(args)+2),
		append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	runs := []BackupRun{}
	for rows.Next() {
		b, err := scanBackupRun(rows)
		if err != nil {
			return nil, 0, err
		}
		runs = append(runs, b)
	}
	return runs, total, rows.Err()
}

func itoa(i int) string {
	return fmt.Sprintf("%d", i)
}

// pruneBackups enforces retention: keeps the newest `keep` runs per
// workspace (status ok/failed — failed runs are evidence), deletes the
// archive + manifest files of older runs, and marks their rows
// 'pruned' with file_path NULL. keep <= 0 means keep forever. keepID
// is the run that just finished: it is definitionally the newest and
// is never a prune candidate, which keeps pruning deterministic even
// when several runs share the same `at` timestamp.
func pruneBackups(ctx context.Context, pool *pgxpool.Pool, dir, workspaceSlug string, keep int, keepID string) error {
	if keep <= 0 {
		return nil
	}
	// keepID already occupies one of the `keep` slots (it is the
	// newest), so among the remaining rows only keep-1 more survive.
	rows, err := pool.Query(ctx,
		`SELECT id::text, file_path FROM backup_runs
		 WHERE workspace_slug = $1 AND status IN ('ok', 'failed')
		   AND id != $3::uuid
		 ORDER BY at DESC OFFSET $2`, workspaceSlug, keep-1, keepID)
	if err != nil {
		return err
	}
	type victim struct {
		id, path string
	}
	var victims []victim
	for rows.Next() {
		var v victim
		var p *string
		if err := rows.Scan(&v.id, &p); err != nil {
			rows.Close()
			return err
		}
		if p != nil {
			v.path = *p
		}
		victims = append(victims, v)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, v := range victims {
		// Best effort on the files: a missing file is not a prune
		// failure (the row is still marked pruned below). Never
		// delete outside dir — the path came from our own insert,
		// but defense in depth costs one Clean check.
		if v.path != "" && filepath.Dir(filepath.Clean(v.path)) == filepath.Clean(dir) {
			_ = os.Remove(v.path)
			_ = os.Remove(v.path + ".manifest.json")
		}
		if _, err := pool.Exec(ctx,
			`UPDATE backup_runs
			 SET status = 'pruned', file_path = NULL
			 WHERE id = $1::uuid AND status != 'pruned'`, v.id); err != nil {
			return err
		}
	}
	return nil
}

// RunAllBackups backs up every workspace on the instance, continuing
// past per-workspace failures. The returned error joins the
// per-workspace failures (nil when all succeeded); the runs slice
// carries one row per workspace attempted, including failed ones, so
// the admin history always shows what happened.
func RunAllBackups(ctx context.Context, pool *pgxpool.Pool, dir string, retention int, triggeredBy string) ([]BackupRun, error) {
	rows, err := pool.Query(ctx,
		`SELECT id::text, slug::text FROM workspaces ORDER BY slug`)
	if err != nil {
		return nil, err
	}
	type ws struct{ id, slug string }
	var workspaces []ws
	for rows.Next() {
		var w ws
		if err := rows.Scan(&w.id, &w.slug); err != nil {
			rows.Close()
			return nil, err
		}
		workspaces = append(workspaces, w)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	runs := make([]BackupRun, 0, len(workspaces))
	var errs []error
	for _, w := range workspaces {
		run, err := runBackupForWorkspace(ctx, pool, dir, retention, w.id, w.slug, triggeredBy)
		runs = append(runs, run)
		if err != nil {
			errs = append(errs, fmt.Errorf("workspace %s: %w", w.slug, err))
		}
	}
	return runs, errors.Join(errs...)
}

func boolPtr(b bool) *bool { return &b }
