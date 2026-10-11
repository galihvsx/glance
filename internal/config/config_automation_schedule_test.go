package config

// Scheduled automation-trigger config tests (C16T1):
// GLANCE_AUTOMATION_SCHEDULE_INTERVAL (empty = disabled, the default;
// invalid or sub-minute fails boot — same discipline as
// GLANCE_BACKUP_INTERVAL).

import "testing"

func TestLoadAutomationScheduleSettings(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/glance?sslmode=disable")
	t.Setenv("OTP_PEPPER", "test-pepper")
	t.Setenv("ALLOW_INSECURE_OTP_PEPPER", "")
	t.Setenv("GLANCE_AUTOMATION_SCHEDULE_INTERVAL", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.AutomationSchedule.ScheduleEnabled() {
		t.Error("ScheduleEnabled() = true with empty GLANCE_AUTOMATION_SCHEDULE_INTERVAL, want false (default off)")
	}
	if cfg.AutomationSchedule.Interval != 0 {
		t.Errorf("AutomationSchedule.Interval = %v, want 0", cfg.AutomationSchedule.Interval)
	}

	t.Setenv("GLANCE_AUTOMATION_SCHEDULE_INTERVAL", "15m")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if !cfg.AutomationSchedule.ScheduleEnabled() {
		t.Error("ScheduleEnabled() = false with GLANCE_AUTOMATION_SCHEDULE_INTERVAL=15m, want true")
	}
	if cfg.AutomationSchedule.Interval.Minutes() != 15 {
		t.Errorf("AutomationSchedule.Interval = %v, want 15m", cfg.AutomationSchedule.Interval)
	}

	// An unparseable value fails boot loudly.
	t.Setenv("GLANCE_AUTOMATION_SCHEDULE_INTERVAL", "soon")
	if _, err := Load(); err == nil {
		t.Error("Load() with GLANCE_AUTOMATION_SCHEDULE_INTERVAL=soon: expected error, got nil")
	}

	// Sub-minute intervals fail boot loudly (same discipline as
	// GLANCE_BACKUP_INTERVAL).
	t.Setenv("GLANCE_AUTOMATION_SCHEDULE_INTERVAL", "30s")
	if _, err := Load(); err == nil {
		t.Error("Load() with GLANCE_AUTOMATION_SCHEDULE_INTERVAL=30s: expected error, got nil")
	}
}
