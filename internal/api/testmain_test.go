package api

// Test hygiene (C6T6): the rate_limits table is a fixed-window counter
// store that persists between `go test` runs. Stale rows from a previous
// run share the key space (otp:ip:…, token:…) and can push a fresh run's
// requests over budget → environmental 429 flakes (seen twice: the
// cycle-5 review and a C6T7 full-suite run, both "fixed" by a manual
// TRUNCATE before the run).
//
// The truncate happens ONCE here, at package startup — not per test.
// Per-test truncation would be safe for this package (its tests run
// sequentially), but `go test ./...` runs internal/auth in parallel and
// its 429-behavior tests assert exact counter sequences; a truncate
// landing mid-sequence would reset their buckets and flake them the
// other way. Within-run isolation stays on uniqueIP()/uniqueEmail().

import (
	"context"
	"os"
	"testing"

	"glance/internal/config"
	"glance/internal/store"
)

func TestMain(m *testing.M) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		panic("TEST_DATABASE_URL must be set; refusing to skip")
	}
	pool, err := store.NewPool(context.Background(), &config.Config{DatabaseURL: url})
	if err != nil {
		panic("testmain: NewPool: " + err.Error())
	}
	if _, err := pool.Exec(context.Background(), `TRUNCATE rate_limits`); err != nil {
		pool.Close()
		panic("testmain: TRUNCATE rate_limits: " + err.Error())
	}
	pool.Close()
	os.Exit(m.Run())
}
