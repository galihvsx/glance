package config

import (
	"net"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	// PORT unset -> defaults to 8080 (DATABASE_URL must be set for Load to succeed)
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/glance?sslmode=disable")
	t.Setenv("OTP_PEPPER", "test-pepper")
	t.Setenv("ALLOW_INSECURE_OTP_PEPPER", "")
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
	t.Setenv("OTP_PEPPER", "test-pepper")
	t.Setenv("ALLOW_INSECURE_OTP_PEPPER", "")
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

// TestLoadRefusesEmptyOTPPepper pins the Task 28 (R7) fail-closed rule:
// booting without OTP_PEPPER — and without the explicit dev hatch — is
// refused, because unpeppered OTP hashes are a plain SHA-256 of a
// 6-digit code (offline brute-forceable if the table ever leaks).
func TestLoadRefusesEmptyOTPPepper(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/glance?sslmode=<redacted>")
	t.Setenv("OTP_PEPPER", "")
	t.Setenv("ALLOW_INSECURE_OTP_PEPPER", "")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() with empty OTP_PEPPER and no hatch: expected refusal, got nil")
	}
}

// TestLoadAllowsInsecurePepperHatch pins the explicit dev escape hatch:
// ALLOW_INSECURE_OTP_PEPPER=1 lets Load succeed without a pepper (with a
// loud warning on stderr), for local dev only.
func TestLoadAllowsInsecurePepperHatch(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/glance?sslmode=<redacted>")
	t.Setenv("OTP_PEPPER", "")
	t.Setenv("ALLOW_INSECURE_OTP_PEPPER", "1")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() with ALLOW_INSECURE_OTP_PEPPER=1: unexpected error: %v", err)
	}
	if cfg.OTPPepper != "" {
		t.Errorf("OTPPepper = %q, want empty (hatch path)", cfg.OTPPepper)
	}
}

// TestLoadProductionIgnoresInsecurePepperHatch pins the C2T8 rule: in
// production (APP_ENV=production) an empty OTP_PEPPER hard-fails boot
// even with ALLOW_INSECURE_OTP_PEPPER=1 — the dev hatch must never be
// the thing standing between a prod deploy and brute-forceable OTPs.
func TestLoadProductionIgnoresInsecurePepperHatch(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/glance?sslmode=disable")
	t.Setenv("OTP_PEPPER", "")
	t.Setenv("ALLOW_INSECURE_OTP_PEPPER", "1")
	t.Setenv("APP_ENV", "production")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() in production with empty OTP_PEPPER: expected refusal even with the hatch, got nil")
	}
}

// TestLoadProductionEnvCaseInsensitive pins that "Production",
// "PRODUCTION" etc. all trigger the production rule.
func TestLoadProductionEnvCaseInsensitive(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/glance?sslmode=disable")
	t.Setenv("OTP_PEPPER", "")
	t.Setenv("ALLOW_INSECURE_OTP_PEPPER", "1")
	t.Setenv("APP_ENV", "Production")

	if _, err := Load(); err == nil {
		t.Fatal("Load() with APP_ENV=Production: expected refusal, got nil")
	}
}

// TestLoadNonProductionKeepsHatch pins that the dev hatch still works
// outside production (dev/test ergonomics preserved).
func TestLoadNonProductionKeepsHatch(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/glance?sslmode=disable")
	t.Setenv("OTP_PEPPER", "")
	t.Setenv("ALLOW_INSECURE_OTP_PEPPER", "1")
	t.Setenv("APP_ENV", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() in dev with the hatch: unexpected error: %v", err)
	}
	if cfg.IsProduction() {
		t.Error("IsProduction() = true for empty APP_ENV, want false")
	}
}

// TestLoadTrustedProxyCIDRs pins parsing: valid CIDRs parse, garbage
// fails boot (fail closed on misconfiguration).
func TestLoadTrustedProxyCIDRs(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/glance?sslmode=disable")
	t.Setenv("OTP_PEPPER", "test-pepper")
	t.Setenv("TRUSTED_PROXY_CIDRS", "10.0.0.0/8, 192.168.0.0/16")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() with valid CIDRs: unexpected error: %v", err)
	}
	if len(cfg.TrustedProxyNets) != 2 {
		t.Fatalf("len(TrustedProxyNets) = %d, want 2", len(cfg.TrustedProxyNets))
	}
	if !cfg.TrustedProxyNets[0].Contains(net.ParseIP("10.1.2.3")) {
		t.Error("first CIDR does not contain 10.1.2.3")
	}

	t.Setenv("TRUSTED_PROXY_CIDRS", "not-a-cidr")
	if _, err := Load(); err == nil {
		t.Fatal("Load() with invalid CIDR: expected error, got nil")
	}

	t.Setenv("TRUSTED_PROXY_CIDRS", "")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load() with empty CIDRs: unexpected error: %v", err)
	}
	if len(cfg.TrustedProxyNets) != 0 {
		t.Errorf("len(TrustedProxyNets) = %d, want 0 (trust none)", len(cfg.TrustedProxyNets))
	}
}
