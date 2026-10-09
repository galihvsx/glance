// Package config loads glance's runtime configuration from environment variables.
package config

import (
	"errors"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
)

// Config holds the runtime configuration for the glance server.
type Config struct {
	// DatabaseURL is the Postgres connection string. Required.
	DatabaseURL string
	// Port is the HTTP listen port. Defaults to "8080".
	Port string
	// AppURL is the public base URL of the app (used for links in emails). Optional.
	AppURL string
	// Env is the deployment environment from APP_ENV ("production",
	// "staging", "development", ...). Empty defaults to development
	// behavior. Only "production" (case-insensitive) triggers the
	// production hardening rules (e.g. the OTP_PEPPER hatch is ignored).
	Env string
	// TrustedProxyCIDRs is the raw TRUSTED_PROXY_CIDRS value
	// (comma-separated CIDR list); TrustedProxyNets is the parsed form.
	// Proxy headers (X-Forwarded-For) are honored ONLY from these ranges;
	// empty means trust none (the direct TCP peer is the client IP).
	TrustedProxyCIDRs string
	TrustedProxyNets  []*net.IPNet
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
	// DataDir is the filesystem root for glance's local data
	// (attachments live under <DataDir>/attachments). From
	// GLANCE_DATA_DIR; defaults to "./data" (relative to the process
	// working directory). The server creates it on boot.
	DataDir string
	// MaxUploadMB caps a single attachment upload (C4T2). From
	// GLANCE_MAX_UPLOAD_MB; defaults to 25. Must be a positive integer
	// — anything else fails boot (fail closed: a misconfigured limit
	// must not silently become unlimited).
	MaxUploadMB int
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
		Env:          os.Getenv("APP_ENV"),
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
		DataDir:            getenv("GLANCE_DATA_DIR", "./data"),
	}
	if v := os.Getenv("GLANCE_MAX_UPLOAD_MB"); v != "" {
		mb, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || mb < 1 {
			return nil, errors.New("config: GLANCE_MAX_UPLOAD_MB must be a positive integer (megabytes)")
		}
		cfg.MaxUploadMB = mb
	} else {
		cfg.MaxUploadMB = 25
	}
	if cfg.DatabaseURL == "" {
		return nil, errors.New("config: DATABASE_URL is required")
	}
	// Task 28 (R7): fail closed on an empty OTP pepper. Without a pepper
	// the code hashes are a plain unsalted SHA-256 of a 6-digit code —
	// trivially brute-forced offline if the table ever leaks — so booting
	// without one must be a deliberate, loud choice, never a silent
	// default. The escape hatch exists for local dev only.
	//
	// C2T8 hardening: in production (APP_ENV=production) the hatch is
	// IGNORED — an empty pepper hard-fails boot even with
	// ALLOW_INSECURE_OTP_PEPPER=1. A "warn and continue" in prod is how
	// unpeppered hashes silently ship; the hatch must never be the thing
	// standing between a prod deploy and a brute-forceable OTP table.
	if cfg.OTPPepper == "" {
		if cfg.IsProduction() {
			return nil, errors.New("config: refusing to boot: OTP_PEPPER is empty and APP_ENV=production; set a real OTP_PEPPER (the ALLOW_INSECURE_OTP_PEPPER hatch is dev-only and is ignored in production)")
		}
		if os.Getenv("ALLOW_INSECURE_OTP_PEPPER") != "1" {
			return nil, errors.New("config: OTP_PEPPER is required (refusing to boot with unpeppered OTP hashes); set OTP_PEPPER, or explicitly allow insecure dev mode with ALLOW_INSECURE_OTP_PEPPER=1")
		}
		log.Println("config: WARNING: ALLOW_INSECURE_OTP_PEPPER=1 — OTP code hashes are unpeppered and brute-forceable; NEVER use this in production")
	}
	// TRUSTED_PROXY_CIDRS: comma-separated CIDRs whose X-Forwarded-For we
	// honor for client-IP-dependent logic (rate limits). Empty = trust
	// none (direct TCP peer is the client IP). A malformed entry fails
	// boot — silently ignoring it would either trust too much or too
	// little, both wrong.
	cfg.TrustedProxyCIDRs = os.Getenv("TRUSTED_PROXY_CIDRS")
	if cfg.TrustedProxyCIDRs != "" {
		for _, part := range strings.Split(cfg.TrustedProxyCIDRs, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			_, ipNet, err := net.ParseCIDR(part)
			if err != nil {
				return nil, errors.New("config: invalid TRUSTED_PROXY_CIDRS entry " + strconv.Quote(part) + ": want CIDR like 10.0.0.0/8")
			}
			cfg.TrustedProxyNets = append(cfg.TrustedProxyNets, ipNet)
		}
	}
	return cfg, nil
}

// IsProduction reports whether the deployment environment is production
// (APP_ENV=production, case-insensitive). Production-only hardening
// rules key off this — never off hostname heuristics.
func (c *Config) IsProduction() bool {
	return strings.EqualFold(c.Env, "production")
}
