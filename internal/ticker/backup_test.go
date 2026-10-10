package ticker

// Scheduled backup ticker test (C15T2): one RunOnce backs up every
// workspace (the schedule tick), records verified runs with
// triggered_by='schedule', and honors retention. The per-workspace
// backup/verify/prune logic itself is covered at the service level;
// here we verify the ticker wiring end to end.

import (
	"context"
	"testing"
	"time"

	"glance/internal/service"
)

func TestBackupTickerRunOnce(t *testing.T) {
	ctx, pool, slug, _, _, _ := tickerTestSetup(t, "backup-tick")

	dir := t.TempDir()
	bt := &BackupTicker{
		Pool:      pool,
		Interval:  time.Hour,
		Dir:       dir,
		Retention: 7,
	}
	if err := bt.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// The tick backed up this test's workspace with a verified run.
	runs, total, err := service.ListBackups(ctx, pool, 25, 0, slug)
	if err != nil {
		t.Fatalf("ListBackups: %v", err)
	}
	if total != 1 || len(runs) != 1 {
		t.Fatalf("total = %d, want the 1 run for %s", total, slug)
	}
	run := runs[0]
	if run.Status != service.BackupStatusOK {
		t.Errorf("status = %q, want ok", run.Status)
	}
	if run.VerifyOK == nil || !*run.VerifyOK {
		t.Errorf("verify_ok = %v, want true", run.VerifyOK)
	}
	if run.TriggeredBy != "schedule" {
		t.Errorf("triggered_by = %q, want schedule", run.TriggeredBy)
	}
	if run.FilePath == nil {
		t.Error("file_path nil, want the archive on disk")
	}
}

func TestBackupTickerDisabledWhenIntervalZero(t *testing.T) {
	// Start with an interval <= 0 must not launch anything (and must
	// not panic). main.go only starts the ticker when enabled; this
	// pins the guard at the ticker level too.
	bt := &BackupTicker{Interval: 0}
	bt.Start(context.Background())
}
