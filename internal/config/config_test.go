package config

import (
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	// PORT unset -> defaults to 8080 (DATABASE_URL must be set for Load to succeed)
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/glance?sslmode=disable")
	t.Setenv("PORT", "")
	t.Setenv("APP_URL", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.Port != "8080" {
		t.Errorf("Port = %q, want %q", cfg.Port, "8080")
	}
}

func TestLoadRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("PORT", "")
	t.Setenv("APP_URL", "")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() expected error when DATABASE_URL is empty, got nil")
	}
}

func TestLoadFromEnv(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/glance?sslmode=disable")
	t.Setenv("PORT", "9090")
	t.Setenv("APP_URL", "https://glance.example.com")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.Port != "9090" {
		t.Errorf("Port = %q, want %q", cfg.Port, "9090")
	}
	if cfg.DatabaseURL != "postgres://localhost:5432/glance?sslmode=disable" {
		t.Errorf("DatabaseURL = %q, want set value", cfg.DatabaseURL)
	}
	if cfg.AppURL != "https://glance.example.com" {
		t.Errorf("AppURL = %q, want %q", cfg.AppURL, "https://glance.example.com")
	}
}
