// Package config loads glance's runtime configuration from environment variables.
package config

import (
	"errors"
	"log"
	"os"
)

// Config holds the runtime configuration for the glance server.
type Config struct {
	// DatabaseURL is the Postgres connection string. Required.
	DatabaseURL string
	// Port is the HTTP listen port. Defaults to "8080".
	Port string
	// AppURL is the public base URL of the app (used for links in emails). Optional.
	AppURL string
	// SMTP settings for outbound mail. All optional; when unset, mail falls back to logging.
	SMTPHost     string
	SMTPPort     string
	SMTPUser     string
	SMTPPassword string
	SMTPFrom     string
	// OTPPepper is the server-side secret mixed into OTP code hashes
	// (SHA-256(pepper + code)). The spec does not name it; this dedicated
	// env var keeps it out of every other secret's blast radius.
	// Required in production — an empty pepper weakens code hashing.
	OTPPepper string
	// Google/GitHub OAuth client credentials. A provider with empty
	// credentials is disabled: its endpoints answer 404, never 500.
	GoogleClientID     string
	GoogleClientSecret string
	GitHubClientID     string
	GitHubClientSecret string
	// OAuthStateSecret signs the OAuth state cookie (HMAC-SHA256). The spec
	// does not name it; dedicated env var like OTP_PEPPER. Empty → the
	// OAuth endpoints fail fast instead of signing with a nil key.
	OAuthStateSecret string
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Load reads configuration from the environment.
func Load() (*Config, error) {
	cfg := &Config{
		DatabaseURL:  os.Getenv("DATABASE_URL"),
		Port:         getenv("PORT", "8080"),
		AppURL:       os.Getenv("APP_URL"),
		SMTPHost:     os.Getenv("SMTP_HOST"),
		SMTPPort:     os.Getenv("SMTP_PORT"),
		SMTPUser:     os.Getenv("SMTP_USER"),
		SMTPPassword: os.Getenv("SMTP_PASSWORD"),
		SMTPFrom:     os.Getenv("SMTP_FROM"),
		OTPPepper:    os.Getenv("OTP_PEPPER"),

		GoogleClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		GitHubClientID:     os.Getenv("GITHUB_CLIENT_ID"),
		GitHubClientSecret: os.Getenv("GITHUB_CLIENT_SECRET"),
		OAuthStateSecret:   os.Getenv("OAUTH_STATE_SECRET"),
	}
	if cfg.DatabaseURL == "" {
		return nil, errors.New("config: DATABASE_URL is required")
	}
	// Task 28 (R7): fail closed on an empty OTP pepper. Without a pepper
	// the code hashes are a plain unsalted SHA-256 of a 6-digit code —
	// trivially brute-forced offline if the table ever leaks — so booting
	// without one must be a deliberate, loud choice, never a silent
	// default. The escape hatch exists for local dev only.
	if cfg.OTPPepper == "" && os.Getenv("ALLOW_INSECURE_OTP_PEPPER") != "1" {
		return nil, errors.New("config: OTP_PEPPER is required (refusing to boot with unpeppered OTP hashes); set OTP_PEPPER, or explicitly allow insecure dev mode with ALLOW_INSECURE_OTP_PEPPER=1")
	}
	if cfg.OTPPepper == "" {
		log.Println("config: WARNING: ALLOW_INSECURE_OTP_PEPPER=1 — OTP code hashes are unpeppered and brute-forceable; NEVER use this in production")
	}
	return cfg, nil
}
