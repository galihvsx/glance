package config

// Scheduled backup config tests (C15T2): GLANCE_BACKUP_INTERVAL
// (empty = disabled, the default; invalid or sub-minute fails boot),
// GLANCE_BACKUP_DIR (default ./backups), GLANCE_BACKUP_RETENTION
// (default 7, 0 = keep forever, negative/non-numeric fails boot).

import "testing"

func TestLoadBackupSettings(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/glance?sslmode=disable")
	t.Setenv("OTP_PEPPER", "test-pepper")
	t.Setenv("ALLOW_INSECURE_OTP_PEPPER", "")
	t.Setenv("GLANCE_BACKUP_INTERVAL", "")
	t.Setenv("GLANCE_BACKUP_DIR", "")
	t.Setenv("GLANCE_BACKUP_RETENTION", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.Backup.BackupEnabled() {
		t.Error("BackupEnabled() = true with empty GLANCE_BACKUP_INTERVAL, want false (default off)")
	}
	if cfg.Backup.Dir != "./backups" {
		t.Errorf("Backup.Dir = %q, want ./backups", cfg.Backup.Dir)
	}
	if cfg.Backup.Retention != 7 {
		t.Errorf("Backup.Retention = %d, want 7", cfg.Backup.Retention)
	}

	t.Setenv("GLANCE_BACKUP_INTERVAL", "24h")
	t.Setenv("GLANCE_BACKUP_DIR", "/var/lib/glance/backups")
	t.Setenv("GLANCE_BACKUP_RETENTION", "30")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if !cfg.Backup.BackupEnabled() {
		t.Error("BackupEnabled() = false with GLANCE_BACKUP_INTERVAL=24h, want true")
	}
	if cfg.Backup.Interval.Hours() != 24 {
		t.Errorf("Backup.Interval = %v, want 24h", cfg.Backup.Interval)
	}
	if cfg.Backup.Dir != "/var/lib/glance/backups" {
		t.Errorf("Backup.Dir = %q", cfg.Backup.Dir)
	}
	if cfg.Backup.Retention != 30 {
		t.Errorf("Backup.Retention = %d, want 30", cfg.Backup.Retention)
	}

	// 0 = keep forever is valid.
	t.Setenv("GLANCE_BACKUP_RETENTION", "0")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load() with retention 0: unexpected error: %v", err)
	}
	if cfg.Backup.Retention != 0 {
		t.Errorf("Backup.Retention = %d, want 0 (keep forever)", cfg.Backup.Retention)
	}

	for _, bad := range []string{"tomorrow", "10x", "-5h"} {
		t.Setenv("GLANCE_BACKUP_INTERVAL", bad)
		if _, err := Load(); err == nil {
			t.Errorf("Load() with GLANCE_BACKUP_INTERVAL=%q: expected error, got nil", bad)
		}
	}
	// Sub-minute intervals fail boot: a full export per tick is
	// expensive, and anything shorter is a misconfiguration.
	t.Setenv("GLANCE_BACKUP_INTERVAL", "30s")
	if _, err := Load(); err == nil {
		t.Error("Load() with GLANCE_BACKUP_INTERVAL=30s: expected error, got nil")
	}
	t.Setenv("GLANCE_BACKUP_INTERVAL", "24h")

	for _, bad := range []string{"-1", "ten", "2.5"} {
		t.Setenv("GLANCE_BACKUP_RETENTION", bad)
		if _, err := Load(); err == nil {
			t.Errorf("Load() with GLANCE_BACKUP_RETENTION=%q: expected error, got nil", bad)
		}
	}
}
