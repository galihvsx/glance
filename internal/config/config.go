// Package config loads glance's runtime configuration from environment variables.
package config

import (
	"errors"
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
	}
	if cfg.DatabaseURL == "" {
		return nil, errors.New("config: DATABASE_URL is required")
	}
	return cfg, nil
}
