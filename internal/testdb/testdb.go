// Package testdb gives every DB-touching test package its own Postgres
// database, so parallel `go test` packages can never see each other's
// rows. Usage is one line in a *_test.go file of the package:
//
//	func TestMain(m *testing.M) {
//		testdb.Main(m, "mail")
//	}
//
// Main reads TEST_DATABASE_URL, derives a per-package database named
// <orig>_<name> (e.g. glance_test_mail), recreates it from scratch,
// applies every migration, rewrites TEST_DATABASE_URL to point at it so
// existing per-test pool helpers work unchanged, runs the tests, and
// drops the database again on the way out (best effort).
//
// RULE: any new test package that touches the database must add this
// one-line TestMain. Without it the package shares the global
// glance_test database with every other package, and row accumulation /
// cross-package interference flake bugs come back.
package testdb

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"glance/internal/config"
	"glance/internal/store"
	"glance/migrations"
)

// safeName matches the allowed charset for derived database names.
var safeName = regexp.MustCompile(`^[a-z0-9_]+$`)

// packageDBName derives the per-package database name from the base name in
// TEST_DATABASE_URL and the caller-supplied package tag. The tag is
// lowercased, then both parts must consist solely of [a-z0-9_] — anything
// else (empty tag, dashes, slashes, SQL metacharacters) is rejected with an
// error so a bad tag can never turn into a wildcard identifier or an
// injection vector in the DDL below.
func packageDBName(base, name string) (string, error) {
	name = strings.ToLower(name)
	if base == "" {
		return "", fmt.Errorf("testdb: empty base database name")
	}
	if name == "" {
		return "", fmt.Errorf("testdb: empty package name")
	}
	if !safeName.MatchString(base) {
		return "", fmt.Errorf("testdb: base database name %q is not [a-z0-9_]", base)
	}
	if !safeName.MatchString(name) {
		return "", fmt.Errorf("testdb: package name %q is not [a-z0-9_]", name)
	}
	return base + "_" + name, nil
}

// withDBName returns rawURL with its path (the database name) replaced by
// db, preserving scheme, host, port, userinfo and query parameters.
func withDBName(rawURL, db string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("testdb: parse database url: %w", err)
	}
	u.Path = "/" + db
	return u.String(), nil
}

// Main is the per-package TestMain described in the package doc comment.
// It panics on any setup failure: per the repo's no-silent-skip rule, a
// test package must never pass vacuously because its database was missing.
func Main(m *testing.M, name string) {
	start := time.Now()
	rawURL := os.Getenv("TEST_DATABASE_URL")
	if rawURL == "" {
		panic("testdb: TEST_DATABASE_URL must be set; refusing to skip")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		panic(fmt.Sprintf("testdb: parse TEST_DATABASE_URL: %v", err))
	}
	pkgDB, err := packageDBName(strings.TrimPrefix(u.Path, "/"), name)
	if err != nil {
		panic(fmt.Sprintf("testdb: %v", err))
	}
	pkgURL, err := withDBName(rawURL, pkgDB)
	if err != nil {
		panic(fmt.Sprintf("testdb: %v", err))
	}
	maintenanceURL, err := withDBName(rawURL, "postgres")
	if err != nil {
		panic(fmt.Sprintf("testdb: %v", err))
	}

	ctx := context.Background()
	quoted := pgx.Identifier{pkgDB}.Sanitize()
	execDDL := func(dbURL, sql string) error {
		conn, err := pgx.Connect(ctx, dbURL)
		if err != nil {
			return fmt.Errorf("connect: %w", err)
		}
		defer conn.Close(ctx)
		if _, err := conn.Exec(ctx, sql); err != nil {
			return fmt.Errorf("exec %q: %w", sql, err)
		}
		return nil
	}

	// Concurrent packages derive distinct database names, so this pair of
	// statements is safe to run in parallel across packages.
	if err := execDDL(maintenanceURL, `DROP DATABASE IF EXISTS `+quoted); err != nil {
		panic(fmt.Sprintf("testdb: drop %s: %v", pkgDB, err))
	}
	if err := execDDL(maintenanceURL, `CREATE DATABASE `+quoted); err != nil {
		panic(fmt.Sprintf("testdb: create %s: %v", pkgDB, err))
	}

	pool, err := store.NewPool(ctx, &config.Config{DatabaseURL: pkgURL})
	if err != nil {
		panic(fmt.Sprintf("testdb: pool for %s: %v", pkgDB, err))
	}
	if err := store.Migrate(ctx, pool, migrations.FS); err != nil {
		pool.Close()
		panic(fmt.Sprintf("testdb: migrate %s: %v", pkgDB, err))
	}
	pool.Close()

	if err := os.Setenv("TEST_DATABASE_URL", pkgURL); err != nil {
		panic(fmt.Sprintf("testdb: set TEST_DATABASE_URL: %v", err))
	}
	log.Printf("testdb: setup (drop+create+migrate) for %q took %v", pkgDB, time.Since(start))

	code := m.Run()

	// Best effort: never fail a green suite on teardown. A stale database
	// is harmless — the next run drops it again up front.
	if err := execDDL(maintenanceURL, `DROP DATABASE IF EXISTS `+quoted); err != nil {
		log.Printf("testdb: teardown drop of %q failed (ignored): %v", pkgDB, err)
	}

	os.Exit(code)
}
